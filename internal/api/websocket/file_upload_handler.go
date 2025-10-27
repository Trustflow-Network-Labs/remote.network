package websocket

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/google/uuid"
)

// FileUploadHandler manages chunked file uploads via WebSocket
type FileUploadHandler struct {
	dbManager         *database.SQLiteManager
	logger            *utils.LogsManager
	configMgr         *utils.ConfigManager
	uploadDir         string
	mu                sync.RWMutex
	activeSessions    map[string]*uploadSessionHandler
	processingGroups  map[string]bool        // Track groups currently being processed
	processingMu      sync.Mutex             // Lock for processingGroups map
	onUploadComplete  func(uploadGroupID string, serviceID int64) // Callback when all files in upload group complete
}

// uploadSessionHandler manages a single upload session
type uploadSessionHandler struct {
	sessionID    string
	serviceID    int64
	filename     string
	totalSize    int64
	totalChunks  int
	chunkSize    int
	tempFilePath string
	file         *os.File
	chunksReceived int
	bytesUploaded  int64
	mu           sync.Mutex
}

// NewFileUploadHandler creates a new file upload handler
func NewFileUploadHandler(dbManager *database.SQLiteManager, logger *utils.LogsManager, configMgr *utils.ConfigManager, appPaths *utils.AppPaths) *FileUploadHandler {
	// Use proper OS-specific temp directory for uploads
	uploadDir := filepath.Join(appPaths.TempDir, "remote-network-uploads")

	// Allow config override if specified
	if customDir := configMgr.GetConfigWithDefault("upload_temp_dir", ""); customDir != "" {
		uploadDir = customDir
	}

	// Ensure upload directory exists
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		logger.Error(fmt.Sprintf("Failed to create upload directory: %v", err), "file_upload")
	}

	logger.Info(fmt.Sprintf("File upload temp directory: %s", uploadDir), "file_upload")

	return &FileUploadHandler{
		dbManager:        dbManager,
		logger:           logger,
		configMgr:        configMgr,
		uploadDir:        uploadDir,
		activeSessions:   make(map[string]*uploadSessionHandler),
		processingGroups: make(map[string]bool),
	}
}

// HandleFileUploadStart handles FILE_UPLOAD_START message
func (fuh *FileUploadHandler) HandleFileUploadStart(client *Client, payload json.RawMessage) error {
	var startPayload FileUploadStartPayload
	if err := json.Unmarshal(payload, &startPayload); err != nil {
		return fmt.Errorf("invalid file upload start payload: %w", err)
	}

	// Validate payload
	if startPayload.ServiceID == 0 {
		return fmt.Errorf("service_id is required")
	}
	if startPayload.UploadGroupID == "" {
		return fmt.Errorf("upload_group_id is required")
	}
	if startPayload.Filename == "" {
		return fmt.Errorf("filename is required")
	}
	if startPayload.FilePath == "" {
		return fmt.Errorf("file_path is required")
	}
	if startPayload.TotalSize <= 0 {
		return fmt.Errorf("total_size must be positive")
	}
	if startPayload.TotalChunks <= 0 {
		return fmt.Errorf("total_chunks must be positive")
	}
	if startPayload.ChunkSize <= 0 {
		return fmt.Errorf("chunk_size must be positive")
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Create temp file directory (preserving path structure within upload group)
	uploadGroupDir := filepath.Join(fuh.uploadDir, startPayload.UploadGroupID)
	if err := os.MkdirAll(uploadGroupDir, 0755); err != nil {
		return fmt.Errorf("failed to create upload group directory: %w", err)
	}

	// Create temp file for this specific file in the group
	tempFilePath := filepath.Join(uploadGroupDir, fmt.Sprintf("%s.tmp", sessionID))
	file, err := os.Create(tempFilePath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// Create upload session in database
	session := &database.UploadSession{
		ServiceID:     startPayload.ServiceID,
		SessionID:     sessionID,
		UploadGroupID: startPayload.UploadGroupID,
		Filename:      startPayload.Filename,
		FilePath:      startPayload.FilePath,
		FileIndex:     startPayload.FileIndex,
		TotalFiles:    startPayload.TotalFiles,
		TotalChunks:   startPayload.TotalChunks,
		TotalBytes:    startPayload.TotalSize,
		ChunkSize:     startPayload.ChunkSize,
		TempFilePath:  tempFilePath,
		Status:        "IN_PROGRESS",
	}

	if err := fuh.dbManager.CreateUploadSession(session); err != nil {
		file.Close()
		os.Remove(tempFilePath)
		return fmt.Errorf("failed to create upload session in database: %w", err)
	}

	// Create session handler
	sessionHandler := &uploadSessionHandler{
		sessionID:    sessionID,
		serviceID:    startPayload.ServiceID,
		filename:     startPayload.Filename,
		totalSize:    startPayload.TotalSize,
		totalChunks:  startPayload.TotalChunks,
		chunkSize:    startPayload.ChunkSize,
		tempFilePath: tempFilePath,
		file:         file,
	}

	// Store session
	fuh.mu.Lock()
	fuh.activeSessions[sessionID] = sessionHandler
	fuh.mu.Unlock()

	// Send initial progress with session ID
	progressMsg, _ := NewMessage(MessageTypeFileUploadProgress, FileUploadProgressPayload{
		SessionID:      sessionID,
		ChunksReceived: 0,
		BytesUploaded:  0,
		Percentage:     0,
	})
	client.Send(progressMsg)

	fuh.logger.Info(fmt.Sprintf("File upload session started: %s (file: %s, size: %d bytes)", sessionID, startPayload.Filename, startPayload.TotalSize), "file_upload")

	return nil
}

// HandleFileUploadChunk handles FILE_UPLOAD_CHUNK message
func (fuh *FileUploadHandler) HandleFileUploadChunk(client *Client, payload json.RawMessage) error {
	var chunkPayload FileUploadChunkPayload
	if err := json.Unmarshal(payload, &chunkPayload); err != nil {
		return fmt.Errorf("invalid file upload chunk payload: %w", err)
	}

	// Get session
	fuh.mu.RLock()
	sessionHandler, exists := fuh.activeSessions[chunkPayload.SessionID]
	fuh.mu.RUnlock()

	if !exists {
		// Try to restore from database
		dbSession, err := fuh.dbManager.GetUploadSession(chunkPayload.SessionID)
		if err != nil || dbSession == nil {
			errMsg, _ := NewMessage(MessageTypeFileUploadError, FileUploadErrorPayload{
				SessionID: chunkPayload.SessionID,
				Error:     "Upload session not found",
				Code:      "SESSION_NOT_FOUND",
			})
			client.Send(errMsg)
			return fmt.Errorf("upload session not found: %s", chunkPayload.SessionID)
		}

		// Restore session handler
		sessionHandler, err = fuh.restoreSession(dbSession)
		if err != nil {
			errMsg, _ := NewMessage(MessageTypeFileUploadError, FileUploadErrorPayload{
				SessionID: chunkPayload.SessionID,
				Error:     "Failed to restore upload session",
				Code:      "SESSION_RESTORE_FAILED",
			})
			client.Send(errMsg)
			return fmt.Errorf("failed to restore session: %w", err)
		}

		fuh.mu.Lock()
		fuh.activeSessions[chunkPayload.SessionID] = sessionHandler
		fuh.mu.Unlock()
	}

	// Decode chunk data
	chunkData, err := base64.StdEncoding.DecodeString(chunkPayload.Data)
	if err != nil {
		errMsg, _ := NewMessage(MessageTypeFileUploadError, FileUploadErrorPayload{
			SessionID: chunkPayload.SessionID,
			Error:     "Invalid chunk data encoding",
			Code:      "INVALID_ENCODING",
		})
		client.Send(errMsg)
		return fmt.Errorf("failed to decode chunk data: %w", err)
	}

	// Write chunk to file
	sessionHandler.mu.Lock()
	if _, err := sessionHandler.file.Write(chunkData); err != nil {
		sessionHandler.mu.Unlock()
		errMsg, _ := NewMessage(MessageTypeFileUploadError, FileUploadErrorPayload{
			SessionID: chunkPayload.SessionID,
			Error:     "Failed to write chunk to file",
			Code:      "WRITE_FAILED",
		})
		client.Send(errMsg)
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	sessionHandler.chunksReceived++
	sessionHandler.bytesUploaded += int64(len(chunkData))
	chunksReceived := sessionHandler.chunksReceived
	bytesUploaded := sessionHandler.bytesUploaded
	totalChunks := sessionHandler.totalChunks
	sessionHandler.mu.Unlock()

	// Update session in database
	dbSession, err := fuh.dbManager.GetUploadSession(chunkPayload.SessionID)
	if err == nil && dbSession != nil {
		dbSession.ChunkIndex = chunksReceived
		dbSession.BytesUploaded = bytesUploaded
		fuh.dbManager.UpdateUploadSession(dbSession)
	}

	// Calculate progress
	percentage := float64(chunksReceived) / float64(totalChunks) * 100.0

	// Send progress update
	progressMsg, _ := NewMessage(MessageTypeFileUploadProgress, FileUploadProgressPayload{
		SessionID:      chunkPayload.SessionID,
		ChunksReceived: chunksReceived,
		BytesUploaded:  bytesUploaded,
		Percentage:     percentage,
	})
	client.Send(progressMsg)

	// Check if upload is complete
	if chunksReceived >= totalChunks {
		return fuh.completeUpload(client, chunkPayload.SessionID, sessionHandler)
	}

	return nil
}

// HandleFileUploadPause handles FILE_UPLOAD_PAUSE message
func (fuh *FileUploadHandler) HandleFileUploadPause(client *Client, payload json.RawMessage) error {
	var pausePayload FileUploadPausePayload
	if err := json.Unmarshal(payload, &pausePayload); err != nil {
		return fmt.Errorf("invalid file upload pause payload: %w", err)
	}

	// Update session status in database
	session, err := fuh.dbManager.GetUploadSession(pausePayload.SessionID)
	if err != nil || session == nil {
		return fmt.Errorf("upload session not found: %s", pausePayload.SessionID)
	}

	session.Status = "PAUSED"
	if err := fuh.dbManager.UpdateUploadSession(session); err != nil {
		return fmt.Errorf("failed to pause upload session: %w", err)
	}

	fuh.logger.Info(fmt.Sprintf("File upload session paused: %s", pausePayload.SessionID), "file_upload")

	return nil
}

// HandleFileUploadResume handles FILE_UPLOAD_RESUME message
func (fuh *FileUploadHandler) HandleFileUploadResume(client *Client, payload json.RawMessage) error {
	var resumePayload FileUploadResumePayload
	if err := json.Unmarshal(payload, &resumePayload); err != nil {
		return fmt.Errorf("invalid file upload resume payload: %w", err)
	}

	// Update session status in database
	session, err := fuh.dbManager.GetUploadSession(resumePayload.SessionID)
	if err != nil || session == nil {
		return fmt.Errorf("upload session not found: %s", resumePayload.SessionID)
	}

	session.Status = "IN_PROGRESS"
	if err := fuh.dbManager.UpdateUploadSession(session); err != nil {
		return fmt.Errorf("failed to resume upload session: %w", err)
	}

	// Send current progress
	progressMsg, _ := NewMessage(MessageTypeFileUploadProgress, FileUploadProgressPayload{
		SessionID:      resumePayload.SessionID,
		ChunksReceived: session.ChunkIndex,
		BytesUploaded:  session.BytesUploaded,
		Percentage:     float64(session.ChunkIndex) / float64(session.TotalChunks) * 100.0,
	})
	client.Send(progressMsg)

	fuh.logger.Info(fmt.Sprintf("File upload session resumed: %s", resumePayload.SessionID), "file_upload")

	return nil
}

// completeUpload finalizes an upload session
func (fuh *FileUploadHandler) completeUpload(client *Client, sessionID string, sessionHandler *uploadSessionHandler) error {
	sessionHandler.mu.Lock()
	serviceID := sessionHandler.serviceID
	sessionHandler.mu.Unlock()

	// Close file
	sessionHandler.mu.Lock()
	if err := sessionHandler.file.Close(); err != nil {
		fuh.logger.Warn(fmt.Sprintf("Failed to close upload file: %v", err), "file_upload")
	}
	sessionHandler.mu.Unlock()

	// Calculate file hash (placeholder - will be done by file processor)
	fileHash := "processing"

	// Update session status in database
	dbSession, err := fuh.dbManager.GetUploadSession(sessionID)
	if err == nil && dbSession != nil {
		dbSession.Status = "COMPLETED"
		fuh.dbManager.UpdateUploadSession(dbSession)
	}

	// Remove from active sessions
	fuh.mu.Lock()
	delete(fuh.activeSessions, sessionID)
	fuh.mu.Unlock()

	// Send completion message for this file
	completeMsg, _ := NewMessage(MessageTypeFileUploadComplete, FileUploadCompletePayload{
		SessionID: sessionID,
		FileHash:  fileHash,
	})
	client.Send(completeMsg)

	fuh.logger.Info(fmt.Sprintf("File upload completed: %s (file: %s, %d/%d)",
		sessionID, sessionHandler.filename,
		dbSession.FileIndex+1, dbSession.TotalFiles), "file_upload")

	// Check if all files in the upload group are completed
	uploadGroupID := dbSession.UploadGroupID
	allComplete, err := fuh.dbManager.IsUploadGroupComplete(uploadGroupID)
	if err != nil {
		fuh.logger.Error(fmt.Sprintf("Failed to check upload group completion: %v", err), "file_upload")
		return nil
	}

	// Trigger file processing callback only when ALL files in group are completed
	if allComplete && fuh.onUploadComplete != nil {
		// Use mutex to prevent duplicate processing
		fuh.processingMu.Lock()
		alreadyProcessing := fuh.processingGroups[uploadGroupID]
		if !alreadyProcessing {
			fuh.processingGroups[uploadGroupID] = true
			fuh.processingMu.Unlock()

			fuh.logger.Info(fmt.Sprintf("All files in upload group %s completed. Triggering processing...", uploadGroupID), "file_upload")

			// Process in goroutine and clean up processing flag when done
			go func() {
				defer func() {
					fuh.processingMu.Lock()
					delete(fuh.processingGroups, uploadGroupID)
					fuh.processingMu.Unlock()
				}()
				fuh.onUploadComplete(uploadGroupID, serviceID)
			}()
		} else {
			fuh.processingMu.Unlock()
			fuh.logger.Info(fmt.Sprintf("Upload group %s already being processed, skipping duplicate call", uploadGroupID), "file_upload")
		}
	}

	return nil
}

// SetOnUploadCompleteCallback sets the callback to be called when all files in an upload group complete
func (fuh *FileUploadHandler) SetOnUploadCompleteCallback(callback func(uploadGroupID string, serviceID int64)) {
	fuh.onUploadComplete = callback
}

// restoreSession restores an upload session from the database
func (fuh *FileUploadHandler) restoreSession(dbSession *database.UploadSession) (*uploadSessionHandler, error) {
	// Re-open the temp file
	file, err := os.OpenFile(dbSession.TempFilePath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open temp file: %w", err)
	}

	sessionHandler := &uploadSessionHandler{
		sessionID:      dbSession.SessionID,
		serviceID:      dbSession.ServiceID,
		filename:       dbSession.Filename,
		totalSize:      dbSession.TotalBytes,
		totalChunks:    dbSession.TotalChunks,
		chunkSize:      dbSession.ChunkSize,
		tempFilePath:   dbSession.TempFilePath,
		file:           file,
		chunksReceived: dbSession.ChunkIndex,
		bytesUploaded:  dbSession.BytesUploaded,
	}

	return sessionHandler, nil
}

// Cleanup removes session data and temp files
func (fuh *FileUploadHandler) Cleanup(sessionID string) error {
	fuh.mu.Lock()
	sessionHandler, exists := fuh.activeSessions[sessionID]
	if exists {
		delete(fuh.activeSessions, sessionID)
	}
	fuh.mu.Unlock()

	if sessionHandler != nil {
		sessionHandler.mu.Lock()
		if sessionHandler.file != nil {
			sessionHandler.file.Close()
		}
		os.Remove(sessionHandler.tempFilePath)
		sessionHandler.mu.Unlock()
	}

	// Delete from database
	return fuh.dbManager.DeleteUploadSession(sessionID)
}

// GetTempFilePath returns the temporary file path for a session
func (fuh *FileUploadHandler) GetTempFilePath(sessionID string) (string, error) {
	fuh.mu.RLock()
	defer fuh.mu.RUnlock()

	sessionHandler, exists := fuh.activeSessions[sessionID]
	if !exists {
		// Try database
		dbSession, err := fuh.dbManager.GetUploadSession(sessionID)
		if err != nil || dbSession == nil {
			return "", fmt.Errorf("session not found")
		}
		return dbSession.TempFilePath, nil
	}

	return sessionHandler.tempFilePath, nil
}
