package workers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// IncomingTransfer tracks an active incoming file transfer
type IncomingTransfer struct {
	TransferID      string
	JobExecutionID  int64
	SourcePeerID    string
	TargetPath      string
	ExpectedHash    string
	ExpectedSize    int64
	TotalChunks     int
	ReceivedChunks  map[int]bool
	File            *os.File
	BytesReceived   int64
	Passphrase      string // For encrypted transfers (memory-only, cleared after use)
	Encrypted       bool   // Whether this transfer is encrypted
	StartedAt       time.Time
	LastChunkAt     time.Time
	mu              sync.Mutex
}

// DataServiceWorker handles DATA service job execution
type DataServiceWorker struct {
	db               *database.SQLiteManager
	cm               *utils.ConfigManager
	logger           *utils.LogsManager
	jobHandler       *p2p.JobMessageHandler
	peerID           string // Local peer ID
	chunkSize        int    // Size of data chunks for streaming
	incomingTransfers map[string]*IncomingTransfer // transferID -> transfer state
	transfersMu      sync.RWMutex
}

// NewDataServiceWorker creates a new DATA service worker
func NewDataServiceWorker(db *database.SQLiteManager, cm *utils.ConfigManager, jobHandler *p2p.JobMessageHandler) *DataServiceWorker {
	return &DataServiceWorker{
		db:                db,
		cm:                cm,
		logger:            utils.NewLogsManager(cm),
		jobHandler:        jobHandler,
		chunkSize:         cm.GetConfigInt("job_data_chunk_size", 65536, 1024, 1048576), // Default 64KB
		incomingTransfers: make(map[string]*IncomingTransfer),
	}
}

// SetJobHandler sets the job handler for data transfer
func (dsw *DataServiceWorker) SetJobHandler(jobHandler *p2p.JobMessageHandler) {
	dsw.jobHandler = jobHandler
	dsw.logger.Info("Job handler set for data service worker", "data_worker")
}

// SetPeerID sets the local peer ID
func (dsw *DataServiceWorker) SetPeerID(peerID string) {
	dsw.peerID = peerID
	dsw.logger.Info(fmt.Sprintf("Peer ID set for data service worker: %s", peerID[:8]), "data_worker")
}

// ExecuteDataService executes a DATA service job
func (dsw *DataServiceWorker) ExecuteDataService(job *database.JobExecution, service *database.OfferedService) error {
	dsw.logger.Info(fmt.Sprintf("Executing DATA service for job %d", job.ID), "data_worker")

	// Get data service details
	dataDetails, err := dsw.db.GetDataServiceDetails(service.ID)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to get data service details for job %d: %v", job.ID, err), "data_worker")
		return fmt.Errorf("failed to get data service details: %v", err)
	}

	if dataDetails == nil {
		dsw.logger.Error(fmt.Sprintf("No data service details found for service %d", service.ID), "data_worker")
		return fmt.Errorf("no data service details found for service %d", service.ID)
	}

	// Determine which file to use (encrypted or original)
	filePath := dataDetails.EncryptedPath
	if filePath == "" {
		filePath = dataDetails.FilePath
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		dsw.logger.Error(fmt.Sprintf("Data file not found: %s", filePath), "data_worker")
		return fmt.Errorf("data file not found: %s", filePath)
	}

	dsw.logger.Info(fmt.Sprintf("Data file located: %s (size: %d bytes)", filePath, dataDetails.SizeBytes), "data_worker")

	// Get job interfaces
	interfaces, err := dsw.db.GetJobInterfaces(job.ID)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to get interfaces for job %d: %v", job.ID, err), "data_worker")
		return fmt.Errorf("failed to get interfaces: %v", err)
	}

	// Find STDOUT interface (DATA services deliver data via STDOUT)
	var stdoutInterface *database.JobInterface
	for _, iface := range interfaces {
		if iface.InterfaceType == types.InterfaceTypeStdout {
			stdoutInterface = iface
			break
		}
	}

	if stdoutInterface == nil {
		dsw.logger.Error(fmt.Sprintf("No STDOUT interface found for DATA service job %d", job.ID), "data_worker")
		return fmt.Errorf("no STDOUT interface found for DATA service")
	}

	// Get interface peers (destinations)
	peers, err := dsw.db.GetJobInterfacePeers(stdoutInterface.ID)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to get interface peers for job %d: %v", job.ID, err), "data_worker")
		return fmt.Errorf("failed to get interface peers: %v", err)
	}

	if len(peers) == 0 {
		dsw.logger.Error(fmt.Sprintf("No interface peers found for DATA service job %d", job.ID), "data_worker")
		return fmt.Errorf("no interface peers found for DATA service")
	}

	dsw.logger.Info(fmt.Sprintf("DATA service job %d will transfer data to %d peer(s)", job.ID, len(peers)), "data_worker")

	// Transfer data to each peer
	for _, peer := range peers {
		if peer.PeerMountFunction == types.MountFunctionReceiver {
			dsw.logger.Info(fmt.Sprintf("Transferring data to peer %s (path: %s)", peer.PeerNodeID[:8], peer.PeerPath), "data_worker")

			err := dsw.transferDataToPeer(job.ID, filePath, peer, dataDetails)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to transfer data to peer %s: %v", peer.PeerNodeID[:8], err), "data_worker")
				return fmt.Errorf("failed to transfer data to peer %s: %v", peer.PeerNodeID[:8], err)
			}

			dsw.logger.Info(fmt.Sprintf("Successfully transferred data to peer %s", peer.PeerNodeID[:8]), "data_worker")
		}
	}

	dsw.logger.Info(fmt.Sprintf("DATA service job %d completed successfully", job.ID), "data_worker")
	return nil
}

// transferDataToPeer transfers data file to a peer
func (dsw *DataServiceWorker) transferDataToPeer(jobExecutionID int64, filePath string, peer *database.JobInterfacePeer, dataDetails *database.DataServiceDetails) error {
	// Open file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	fileSize := fileInfo.Size()
	totalChunks := int((fileSize + int64(dsw.chunkSize) - 1) / int64(dsw.chunkSize))

	dsw.logger.Info(fmt.Sprintf("Transferring file %s (%d bytes) in %d chunks to peer %s",
		filePath, fileSize, totalChunks, peer.PeerNodeID[:8]), "data_worker")

	// Generate transfer ID
	transferID := dsw.GenerateTransferID(jobExecutionID, peer.PeerNodeID)

	// Check if job handler is available for actual transfer
	if dsw.jobHandler != nil {
		// Get encryption key if data is encrypted
		var keyData string
		var encrypted bool
		if dataDetails.EncryptionKeyID != nil {
			encryptionKey, err := dsw.db.GetEncryptionKey(dataDetails.ServiceID)
			if err != nil {
				dsw.logger.Warn(fmt.Sprintf("Failed to get encryption key for service %d: %v - proceeding without encryption", dataDetails.ServiceID, err), "data_worker")
			} else if encryptionKey != nil {
				keyData = encryptionKey.KeyData
				encrypted = true
				dsw.logger.Info(fmt.Sprintf("Including encryption key for transfer %s", transferID), "data_worker")
			}
		}

		// Send transfer request
		transferRequest := &types.JobDataTransferRequest{
			JobExecutionID:    jobExecutionID,
			InterfaceType:     types.InterfaceTypeStdout,
			SourcePeerID:      dsw.peerID,
			DestinationPeerID: peer.PeerNodeID,
			SourcePath:        filePath,
			DestinationPath:   peer.PeerPath,
			DataHash:          dataDetails.Hash,
			SizeBytes:         fileSize,
			Passphrase:        keyData,
			Encrypted:         encrypted,
		}

		dsw.logger.Info(fmt.Sprintf("Sending transfer request for transfer ID %s to peer %s (encrypted: %v)", transferID, peer.PeerNodeID[:8], encrypted), "data_worker")

		// Send transfer request via job handler
		response, err := dsw.jobHandler.SendJobDataTransferRequest(peer.PeerNodeID, transferRequest)
		if err != nil {
			return fmt.Errorf("failed to send transfer request: %v", err)
		}

		if !response.Accepted {
			return fmt.Errorf("transfer request rejected by peer %s: %s", peer.PeerNodeID[:8], response.Message)
		}

		dsw.logger.Info(fmt.Sprintf("Transfer request accepted by peer %s for transfer ID %s", peer.PeerNodeID[:8], transferID), "data_worker")
	}

	// Transfer data in chunks
	buffer := make([]byte, dsw.chunkSize)
	chunkIndex := 0
	bytesTransferred := int64(0)

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file chunk: %v", err)
		}

		if n == 0 {
			break
		}

		isLast := chunkIndex == totalChunks-1

		// Send data chunk
		if dsw.jobHandler != nil {
			chunk := &types.JobDataChunk{
				TransferID:  transferID,
				ChunkIndex:  chunkIndex,
				TotalChunks: totalChunks,
				Data:        buffer[:n],
				IsLast:      isLast,
			}

			dsw.logger.Info(fmt.Sprintf("Sending chunk %d/%d (%d bytes) for transfer %s",
				chunkIndex+1, totalChunks, n, transferID), "data_worker")

			// Send chunk via job handler
			err := dsw.jobHandler.SendJobDataChunk(peer.PeerNodeID, chunk)
			if err != nil {
				return fmt.Errorf("failed to send chunk %d: %v", chunkIndex, err)
			}
		} else {
			dsw.logger.Info(fmt.Sprintf("Would send chunk %d/%d (%d bytes) for transfer %s (job handler not available)",
				chunkIndex+1, totalChunks, n, transferID), "data_worker")
		}

		bytesTransferred += int64(n)
		chunkIndex++

		// Small delay to avoid overwhelming the receiver
		time.Sleep(10 * time.Millisecond)
	}

	// Send transfer complete notification
	if dsw.jobHandler != nil {
		transferComplete := &types.JobDataTransferComplete{
			TransferID:       transferID,
			JobExecutionID:   jobExecutionID,
			Success:          true,
			BytesTransferred: bytesTransferred,
			CompletedAt:      time.Now(),
		}

		dsw.logger.Info(fmt.Sprintf("Sending transfer complete for transfer ID %s (%d bytes)",
			transferID, transferComplete.BytesTransferred), "data_worker")

		// Send transfer complete via job handler
		err := dsw.jobHandler.SendJobDataTransferComplete(peer.PeerNodeID, transferComplete)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Failed to send transfer complete notification: %v", err), "data_worker")
			// Don't return error here - transfer was successful even if notification failed
		}
	} else {
		dsw.logger.Info(fmt.Sprintf("Transfer %s simulated: %d bytes (job handler not available)", transferID, bytesTransferred), "data_worker")
	}

	dsw.logger.Info(fmt.Sprintf("Transfer %s completed: %d bytes in %d chunks",
		transferID, bytesTransferred, chunkIndex), "data_worker")

	return nil
}

// GenerateTransferID generates a unique transfer ID (public method for JobManager)
func (dsw *DataServiceWorker) GenerateTransferID(jobExecutionID int64, peerID string) string {
	data := fmt.Sprintf("%d-%s-%d", jobExecutionID, peerID, time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes as transfer ID
}


// InitializeIncomingTransfer creates a new incoming transfer state
func (dsw *DataServiceWorker) InitializeIncomingTransfer(transferID string, jobExecutionID int64, sourcePeerID string, targetPath string, expectedHash string, expectedSize int64, totalChunks int) error {
	return dsw.InitializeIncomingTransferWithPassphrase(transferID, jobExecutionID, sourcePeerID, targetPath, expectedHash, expectedSize, totalChunks, "", false)
}

// InitializeIncomingTransferWithPassphrase creates a new incoming transfer state with optional encryption
func (dsw *DataServiceWorker) InitializeIncomingTransferWithPassphrase(transferID string, jobExecutionID int64, sourcePeerID string, targetPath string, expectedHash string, expectedSize int64, totalChunks int, passphrase string, encrypted bool) error {
	dsw.transfersMu.Lock()
	defer dsw.transfersMu.Unlock()

	// Check if transfer already exists
	if _, exists := dsw.incomingTransfers[transferID]; exists {
		return nil // Already initialized
	}

	dsw.logger.Info(fmt.Sprintf("Initializing incoming transfer %s: %s (%d bytes, %d chunks, encrypted: %v)",
		transferID, targetPath, expectedSize, totalChunks, encrypted), "data_worker")

	// Create target directory if it doesn't exist
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %v", err)
	}

	// Open or create target file
	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open target file: %v", err)
	}

	// Create transfer state
	transfer := &IncomingTransfer{
		TransferID:     transferID,
		JobExecutionID: jobExecutionID,
		SourcePeerID:   sourcePeerID,
		TargetPath:     targetPath,
		ExpectedHash:   expectedHash,
		ExpectedSize:   expectedSize,
		TotalChunks:    totalChunks,
		ReceivedChunks: make(map[int]bool),
		File:           file,
		BytesReceived:  0,
		Passphrase:     passphrase, // Store passphrase temporarily in memory
		Encrypted:      encrypted,
		StartedAt:      time.Now(),
		LastChunkAt:    time.Now(),
	}

	dsw.incomingTransfers[transferID] = transfer
	dsw.logger.Info(fmt.Sprintf("Incoming transfer %s initialized", transferID), "data_worker")

	return nil
}

// HandleDataChunk handles incoming data chunks from remote peers
func (dsw *DataServiceWorker) HandleDataChunk(chunk *types.JobDataChunk, peerID string) error {
	dsw.logger.Debug(fmt.Sprintf("Handling data chunk %d/%d for transfer %s from peer %s",
		chunk.ChunkIndex+1, chunk.TotalChunks, chunk.TransferID, peerID[:8]), "data_worker")

	// Get or initialize transfer
	dsw.transfersMu.RLock()
	transfer, exists := dsw.incomingTransfers[chunk.TransferID]
	dsw.transfersMu.RUnlock()

	if !exists {
		// Auto-initialize transfer on first chunk
		// In a production system, we'd get this info from a transfer request message first
		// For now, create a temporary path based on transfer ID
		tempPath := filepath.Join("/tmp", "remote-network-transfers", chunk.TransferID)

		err := dsw.InitializeIncomingTransfer(
			chunk.TransferID,
			0, // Unknown job ID
			peerID,
			tempPath,
			"", // Unknown hash
			-1, // Unknown size
			chunk.TotalChunks,
		)
		if err != nil {
			return fmt.Errorf("failed to initialize transfer: %v", err)
		}

		dsw.transfersMu.RLock()
		transfer = dsw.incomingTransfers[chunk.TransferID]
		dsw.transfersMu.RUnlock()
	}

	// Lock the transfer for chunk processing
	transfer.mu.Lock()
	defer transfer.mu.Unlock()

	// Check if we already received this chunk
	if transfer.ReceivedChunks[chunk.ChunkIndex] {
		dsw.logger.Warn(fmt.Sprintf("Duplicate chunk %d for transfer %s, ignoring", chunk.ChunkIndex, chunk.TransferID), "data_worker")
		return nil
	}

	// Calculate offset for this chunk
	offset := int64(chunk.ChunkIndex) * int64(dsw.chunkSize)

	// Write chunk data to file at correct offset
	n, err := transfer.File.WriteAt(chunk.Data, offset)
	if err != nil {
		return fmt.Errorf("failed to write chunk %d to file: %v", chunk.ChunkIndex, err)
	}

	if n != len(chunk.Data) {
		return fmt.Errorf("incomplete write: wrote %d bytes, expected %d", n, len(chunk.Data))
	}

	// Mark chunk as received
	transfer.ReceivedChunks[chunk.ChunkIndex] = true
	transfer.BytesReceived += int64(n)
	transfer.LastChunkAt = time.Now()

	dsw.logger.Info(fmt.Sprintf("Received chunk %d/%d (%d bytes) for transfer %s - total: %d/%d bytes",
		chunk.ChunkIndex+1, chunk.TotalChunks, n, chunk.TransferID,
		transfer.BytesReceived, transfer.ExpectedSize), "data_worker")

	// Check if transfer is complete
	if len(transfer.ReceivedChunks) == transfer.TotalChunks {
		dsw.logger.Info(fmt.Sprintf("All chunks received for transfer %s, finalizing...", chunk.TransferID), "data_worker")
		return dsw.finalizeTransfer(chunk.TransferID)
	}

	return nil
}

// finalizeTransfer completes a transfer and cleans up resources
func (dsw *DataServiceWorker) finalizeTransfer(transferID string) error {
	dsw.transfersMu.Lock()
	defer dsw.transfersMu.Unlock()

	transfer, exists := dsw.incomingTransfers[transferID]
	if !exists {
		return fmt.Errorf("transfer %s not found", transferID)
	}

	transfer.mu.Lock()
	defer transfer.mu.Unlock()

	// Close file
	if err := transfer.File.Sync(); err != nil {
		dsw.logger.Warn(fmt.Sprintf("Failed to sync file for transfer %s: %v", transferID, err), "data_worker")
	}

	if err := transfer.File.Close(); err != nil {
		dsw.logger.Warn(fmt.Sprintf("Failed to close file for transfer %s: %v", transferID, err), "data_worker")
	}

	duration := time.Since(transfer.StartedAt)
	throughput := float64(transfer.BytesReceived) / duration.Seconds() / 1024 / 1024 // MB/s

	dsw.logger.Info(fmt.Sprintf("Transfer %s completed: %d bytes in %.2fs (%.2f MB/s) - file: %s",
		transferID, transfer.BytesReceived, duration.Seconds(), throughput, transfer.TargetPath), "data_worker")

	// IMPORTANT: Validate hash BEFORE decryption/decompression
	// The hash in the database is calculated on the encrypted+compressed file
	if transfer.ExpectedHash != "" {
		if err := dsw.validateFileHash(transfer.TargetPath, transfer.ExpectedHash); err != nil {
			dsw.logger.Error(fmt.Sprintf("Hash validation failed for transfer %s: %v", transferID, err), "data_worker")
			// Delete corrupted file
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			// Clear passphrase from memory
			if transfer.Passphrase != "" {
				transfer.Passphrase = ""
			}
			return fmt.Errorf("hash validation failed: %v", err)
		}
		dsw.logger.Info(fmt.Sprintf("Transfer %s hash validated successfully", transferID), "data_worker")
	}

	// Decrypt file if encrypted (AFTER hash validation)
	if transfer.Encrypted && transfer.Passphrase != "" {
		dsw.logger.Info(fmt.Sprintf("Decrypting transfer %s with passphrase", transferID), "data_worker")

		err := dsw.decryptFile(transfer.TargetPath, transfer.Passphrase)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Decryption failed for transfer %s: %v", transferID, err), "data_worker")
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			// Clear passphrase from memory
			transfer.Passphrase = ""
			return fmt.Errorf("decryption failed: %v", err)
		}

		dsw.logger.Info(fmt.Sprintf("Transfer %s decrypted successfully", transferID), "data_worker")
	}

	// Decompress file (all data services are compressed as tar.gz after encryption)
	// After decryption, the file is a tar.gz archive that needs to be extracted
	dsw.logger.Info(fmt.Sprintf("Decompressing transfer %s", transferID), "data_worker")

	// Create extraction directory next to the downloaded file
	extractDir := transfer.TargetPath + "_extracted"
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to create extraction directory for transfer %s: %v", transferID, err), "data_worker")
		os.Remove(transfer.TargetPath)
		delete(dsw.incomingTransfers, transferID)
		// Clear passphrase from memory
		if transfer.Passphrase != "" {
			transfer.Passphrase = ""
		}
		return fmt.Errorf("failed to create extraction directory: %v", err)
	}

	// Decompress the tar.gz file
	if err := utils.Decompress(transfer.TargetPath, extractDir); err != nil {
		dsw.logger.Error(fmt.Sprintf("Decompression failed for transfer %s: %v", transferID, err), "data_worker")
		os.Remove(transfer.TargetPath)
		os.RemoveAll(extractDir)
		delete(dsw.incomingTransfers, transferID)
		// Clear passphrase from memory
		if transfer.Passphrase != "" {
			transfer.Passphrase = ""
		}
		return fmt.Errorf("decompression failed: %v", err)
	}

	// Remove the compressed archive after successful extraction
	if err := os.Remove(transfer.TargetPath); err != nil {
		dsw.logger.Warn(fmt.Sprintf("Failed to remove compressed archive for transfer %s: %v", transferID, err), "data_worker")
	}

	dsw.logger.Info(fmt.Sprintf("Transfer %s decompressed successfully to %s", transferID, extractDir), "data_worker")

	// Clear passphrase from memory immediately after use
	if transfer.Passphrase != "" {
		transfer.Passphrase = ""
		dsw.logger.Debug(fmt.Sprintf("Passphrase cleared from memory for transfer %s", transferID), "data_worker")
	}

	// Remove from active transfers
	delete(dsw.incomingTransfers, transferID)

	dsw.logger.Info(fmt.Sprintf("Transfer %s finalized successfully", transferID), "data_worker")
	return nil
}

// decryptFile decrypts a file using AES-256-GCM
// The keyData parameter should be in format "passphrase|hexkeydata" where hexkeydata is [16 bytes salt][32 bytes derived key]
// This matches the format used by file_processor and stored in the encryption_keys table
func (dsw *DataServiceWorker) decryptFile(filePath string, keyData string) error {
	// Parse keyData format: "passphrase|hexkeydata"
	parts := dsw.splitKeyData(keyData)
	if len(parts) != 2 {
		return fmt.Errorf("invalid key data format - expected 'passphrase|hexkeydata'")
	}

	// Decode hex key data
	hexKeyData := parts[1]
	decodedKeyData, err := hex.DecodeString(hexKeyData)
	if err != nil {
		return fmt.Errorf("failed to decode key data: %v", err)
	}

	// Extract AES key from keyData format: [16 bytes salt][32 bytes derived key]
	if len(decodedKeyData) < 48 {
		return fmt.Errorf("invalid key data length: expected at least 48 bytes, got %d", len(decodedKeyData))
	}
	key := decodedKeyData[16:48] // Extract 32-byte AES-256 key

	// Read encrypted file
	ciphertext, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read encrypted file: %v", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %v", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %v", err)
	}

	// Extract nonce from ciphertext
	// File format from file_processor.encryptFile: [nonce][encrypted data]
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return fmt.Errorf("ciphertext too short: expected at least %d bytes, got %d", nonceSize, len(ciphertext))
	}
	nonce, encryptedData := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt data
	plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return fmt.Errorf("decryption failed (wrong passphrase or corrupted data): %v", err)
	}

	// Write decrypted data back to the same file
	if err := os.WriteFile(filePath, plaintext, 0644); err != nil {
		return fmt.Errorf("failed to write decrypted file: %v", err)
	}

	dsw.logger.Info(fmt.Sprintf("Successfully decrypted file: %s (%d bytes encrypted -> %d bytes decrypted)",
		filePath, len(ciphertext), len(plaintext)), "data_worker")

	return nil
}

// splitKeyData splits the stored key data into passphrase and key
// Format: "passphrase|hexkeydata"
func (dsw *DataServiceWorker) splitKeyData(keyData string) []string {
	// Find the first occurrence of | separator
	for i := 0; i < len(keyData); i++ {
		if keyData[i] == '|' {
			return []string{keyData[:i], keyData[i+1:]}
		}
	}
	return []string{keyData}
}

// validateFileHash validates the BLAKE3 hash of a file
func (dsw *DataServiceWorker) validateFileHash(filePath string, expectedHash string) error {
	// Use the same hash function as file_processor (BLAKE3)
	actualHash, err := utils.HashFileToCID(filePath)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %v", err)
	}

	if actualHash != expectedHash {
		return fmt.Errorf("hash mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// CleanupStalledTransfers removes transfers that have timed out
func (dsw *DataServiceWorker) CleanupStalledTransfers(timeout time.Duration) {
	dsw.transfersMu.Lock()
	defer dsw.transfersMu.Unlock()

	now := time.Now()
	for transferID, transfer := range dsw.incomingTransfers {
		transfer.mu.Lock()
		lastActivity := transfer.LastChunkAt
		transfer.mu.Unlock()

		if now.Sub(lastActivity) > timeout {
			dsw.logger.Warn(fmt.Sprintf("Transfer %s stalled (no activity for %v), cleaning up", transferID, timeout), "data_worker")

			transfer.mu.Lock()
			if transfer.File != nil {
				transfer.File.Close()
			}
			// Optionally delete incomplete file
			os.Remove(transfer.TargetPath)
			transfer.mu.Unlock()

			delete(dsw.incomingTransfers, transferID)
		}
	}
}

// Close closes the data service worker
func (dsw *DataServiceWorker) Close() {
	dsw.logger.Info("Closing data service worker", "data_worker")

	// Close all active transfers
	dsw.transfersMu.Lock()
	for transferID, transfer := range dsw.incomingTransfers {
		transfer.mu.Lock()
		if transfer.File != nil {
			transfer.File.Close()
			dsw.logger.Info(fmt.Sprintf("Closed incomplete transfer %s", transferID), "data_worker")
		}
		transfer.mu.Unlock()
	}
	dsw.incomingTransfers = nil
	dsw.transfersMu.Unlock()

	dsw.logger.Close()
}
