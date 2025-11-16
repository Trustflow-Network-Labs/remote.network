package workers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	db                *database.SQLiteManager
	cm                *utils.ConfigManager
	logger            *utils.LogsManager
	jobHandler        *p2p.JobMessageHandler
	peerID            string // Local peer ID
	chunkSize         int    // Size of data chunks for streaming
	incomingTransfers map[string]*IncomingTransfer // transferID -> transfer state
	transfersMu       sync.RWMutex
	ctx               context.Context
	cancel            context.CancelFunc
	monitoringStarted bool
	monitoringMu      sync.Mutex
}

// NewDataServiceWorker creates a new DATA service worker
func NewDataServiceWorker(db *database.SQLiteManager, cm *utils.ConfigManager, jobHandler *p2p.JobMessageHandler) *DataServiceWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &DataServiceWorker{
		db:                db,
		cm:                cm,
		logger:            utils.NewLogsManager(cm),
		jobHandler:        jobHandler,
		chunkSize:         cm.GetConfigInt("job_data_chunk_size", 1048576, 1024, 10485760), // Default 1MB (matches WebSocket upload performance)
		incomingTransfers: make(map[string]*IncomingTransfer),
		ctx:               ctx,
		cancel:            cancel,
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

// Start starts background monitoring for the data service worker
func (dsw *DataServiceWorker) Start() {
	dsw.monitoringMu.Lock()
	if dsw.monitoringStarted {
		dsw.monitoringMu.Unlock()
		return
	}
	dsw.monitoringStarted = true
	dsw.monitoringMu.Unlock()

	dsw.logger.Info("Starting data service worker monitoring", "data_worker")

	// Start transfer monitoring goroutine
	go dsw.MonitorTransfers()
}


// MonitorTransfers monitors active transfers and requests resumption for stalled receiver-side transfers
func (dsw *DataServiceWorker) MonitorTransfers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	dsw.logger.Info("Transfer monitoring started (checking every 30 seconds)", "data_worker")

	for {
		select {
		case <-ticker.C:
			// Get stalled transfers (no activity for 30+ seconds)
			stalledTransfers, err := dsw.db.GetStalledTransfers(30 * time.Second)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to get stalled transfers: %v", err), "data_worker")
				continue
			}

			if len(stalledTransfers) == 0 {
				continue
			}

			dsw.logger.Info(fmt.Sprintf("Found %d stalled transfers, checking for resumption", len(stalledTransfers)), "data_worker")

			// Process each stalled transfer
			for _, transfer := range stalledTransfers {
				// Only resume receiver-side transfers (sender-side is handled by peer manager)
				if transfer.Direction != "receiver" {
					continue
				}

				// Skip if transfer is already paused or failed
				if transfer.Status == "paused" || transfer.Status == "failed" {
					continue
				}

				dsw.logger.Info(fmt.Sprintf("Requesting resume for stalled receiver transfer %s (last activity: %s ago)",
					transfer.TransferID, time.Since(transfer.LastActivity)), "data_worker")

				// Send resume request to sender
				if err := dsw.sendResumeRequest(transfer); err != nil {
					dsw.logger.Error(fmt.Sprintf("Failed to send resume request for transfer %s: %v", transfer.TransferID, err), "data_worker")
					continue
				}

				dsw.logger.Info(fmt.Sprintf("Resume request sent for transfer %s", transfer.TransferID), "data_worker")
			}

		case <-dsw.ctx.Done():
			dsw.logger.Info("Transfer monitoring stopped", "data_worker")
			return
		}
	}
}

// sendResumeRequest sends a resume request to the sender for a stalled receiver-side transfer
func (dsw *DataServiceWorker) sendResumeRequest(transfer *database.DataTransfer) error {
	if dsw.jobHandler == nil {
		return fmt.Errorf("job handler not set")
	}

	// Create resume request with list of received chunks
	resume := &types.JobDataTransferResume{
		TransferID:     transfer.TransferID,
		ReceivedChunks: transfer.ChunksReceived,
		LastKnownChunk: len(transfer.ChunksReceived) - 1,
	}

	// Send to the source peer (sender)
	return dsw.jobHandler.SendJobDataTransferResume(transfer.SourcePeerID, resume)
}

// GetJobExecutionIDForTransfer returns the job execution ID associated with a transfer
func (dsw *DataServiceWorker) GetJobExecutionIDForTransfer(transferID string) (int64, bool) {
	dsw.transfersMu.RLock()
	defer dsw.transfersMu.RUnlock()

	transfer, exists := dsw.incomingTransfers[transferID]
	if !exists {
		return 0, false
	}

	return transfer.JobExecutionID, true
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

			err := dsw.transferDataToPeer(job, filePath, peer, dataDetails)
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
func (dsw *DataServiceWorker) transferDataToPeer(job *database.JobExecution, filePath string, peer *database.JobInterfacePeer, dataDetails *database.DataServiceDetails) error {
	// Check if destination is the local peer (self-transfer)
	if peer.PeerNodeID == dsw.peerID {
		dsw.logger.Info(fmt.Sprintf("Destination is local peer - handling data locally for job %d", job.ID), "data_worker")
		return dsw.handleLocalDataTransfer(job.ID, filePath, peer, dataDetails)
	}

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
	transferID := dsw.GenerateTransferID(job.ID, peer.PeerNodeID)

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

	// Check if job handler is available for actual transfer
	if dsw.jobHandler != nil {
		// Send transfer request using WorkflowJobID (shared identifier both peers understand)
		transferRequest := &types.JobDataTransferRequest{
			TransferID:        transferID, // Include transfer ID so both peers use the same one
			WorkflowJobID:     job.WorkflowJobID,
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

	// Create transfer record in database for state persistence
	transfer := &database.DataTransfer{
		TransferID:        transferID,
		JobExecutionID:    job.ID,
		SourcePeerID:      dsw.peerID,
		DestinationPeerID: peer.PeerNodeID,
		FilePath:          filePath,
		TotalChunks:       totalChunks,
		ChunkSize:         dsw.chunkSize,
		TotalBytes:        fileSize,
		FileHash:          dataDetails.Hash,
		Encrypted:         encrypted,
		ChunksSent:        make([]int, 0),
		ChunksReceived:    make([]int, 0),
		ChunksAcked:       make([]int, 0),
		BytesTransferred:  0,
		Status:            "active",
		Direction:         "sender",
		LastActivity:      time.Now(),
		LastCheckpoint:    time.Now(),
	}

	err = dsw.db.CreateDataTransfer(transfer)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to create transfer record: %v", err), "data_worker")
		return fmt.Errorf("failed to create transfer record: %v", err)
	}

	dsw.logger.Info(fmt.Sprintf("Created transfer record %s in database", transferID), "data_worker")

	// Transfer data in chunks with retry and ACK tracking
	buffer := make([]byte, dsw.chunkSize)
	chunkIndex := 0
	bytesTransferred := int64(0)
	chunksSent := make(map[int]bool)
	chunksAcked := make(map[int]bool)

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			dsw.db.FailTransfer(transferID, fmt.Sprintf("failed to read file chunk: %v", err))
			return fmt.Errorf("failed to read file chunk: %v", err)
		}

		if n == 0 {
			break
		}

		isLast := chunkIndex == totalChunks-1

		// Send data chunk (with retry on failure)
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

			// Retry logic: try up to 3 times with exponential backoff
			sent := false
			for attempt := 0; attempt < 3; attempt++ {
				err := dsw.jobHandler.SendJobDataChunk(peer.PeerNodeID, chunk)
				if err == nil {
					sent = true
					break
				}

				dsw.logger.Warn(fmt.Sprintf("Failed to send chunk %d (attempt %d/3): %v", chunkIndex, attempt+1, err), "data_worker")

				if attempt < 2 {
					// Exponential backoff: 100ms, 200ms, 400ms
					backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
					time.Sleep(backoff)
				}
			}

			if !sent {
				// Mark transfer as failed after retries exhausted
				dsw.db.FailTransfer(transferID, fmt.Sprintf("failed to send chunk %d after 3 attempts", chunkIndex))
				return fmt.Errorf("failed to send chunk %d after 3 attempts", chunkIndex)
			}

			// Track sent chunk
			chunksSent[chunkIndex] = true
		} else {
			dsw.logger.Info(fmt.Sprintf("Would send chunk %d/%d (%d bytes) for transfer %s (job handler not available)",
				chunkIndex+1, totalChunks, n, transferID), "data_worker")
		}

		bytesTransferred += int64(n)
		chunkIndex++

		// Update progress every 20 chunks (checkpoint)
		if chunkIndex%20 == 0 || isLast {
			sentList := make([]int, 0, len(chunksSent))
			for idx := range chunksSent {
				sentList = append(sentList, idx)
			}
			ackedList := make([]int, 0, len(chunksAcked))
			for idx := range chunksAcked {
				ackedList = append(ackedList, idx)
			}

			err = dsw.db.UpdateTransferProgress(transferID, sentList, nil, ackedList, bytesTransferred)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to update transfer progress: %v", err), "data_worker")
			}

			err = dsw.db.UpdateTransferCheckpoint(transferID)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to update transfer checkpoint: %v", err), "data_worker")
			}

			dsw.logger.Debug(fmt.Sprintf("Checkpoint saved for transfer %s: %d chunks sent, %d bytes",
				transferID, chunkIndex, bytesTransferred), "data_worker")
		}

		// Small delay to avoid overwhelming the receiver
		time.Sleep(10 * time.Millisecond)
	}

	// Send transfer complete notification
	if dsw.jobHandler != nil {
		transferComplete := &types.JobDataTransferComplete{
			TransferID:       transferID,
			JobExecutionID:   job.ID,
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

	// Mark transfer as completed in database
	err = dsw.db.CompleteTransfer(transferID)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to mark transfer as completed: %v", err), "data_worker")
	} else {
		dsw.logger.Info(fmt.Sprintf("Transfer %s marked as completed in database", transferID), "data_worker")
	}

	dsw.logger.Info(fmt.Sprintf("Transfer %s completed: %d bytes in %d chunks",
		transferID, bytesTransferred, chunkIndex), "data_worker")

	return nil
}

// ResumeTransfer resumes a stalled file transfer using fresh peer metadata
func (dsw *DataServiceWorker) ResumeTransfer(transfer *database.DataTransfer, peerMetadata *database.PeerMetadata) error {
	dsw.logger.Info(fmt.Sprintf("Resuming transfer %s to peer %s (metadata refreshed from DHT)",
		transfer.TransferID, transfer.DestinationPeerID[:8]), "data_worker")

	// Verify file still exists
	if _, err := os.Stat(transfer.FilePath); os.IsNotExist(err) {
		return fmt.Errorf("source file no longer exists: %s", transfer.FilePath)
	}

	// Open file for reading
	file, err := os.Open(transfer.FilePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Calculate missing chunks (chunks not acknowledged yet)
	ackedMap := make(map[int]bool)
	for _, idx := range transfer.ChunksAcked {
		ackedMap[idx] = true
	}

	missingChunks := make([]int, 0)
	for i := 0; i < transfer.TotalChunks; i++ {
		if !ackedMap[i] {
			missingChunks = append(missingChunks, i)
		}
	}

	if len(missingChunks) == 0 {
		dsw.logger.Info(fmt.Sprintf("Transfer %s has all chunks acked, marking as completed", transfer.TransferID), "data_worker")
		return dsw.db.CompleteTransfer(transfer.TransferID)
	}

	dsw.logger.Info(fmt.Sprintf("Transfer %s resuming: %d/%d chunks still need to be sent",
		transfer.TransferID, len(missingChunks), transfer.TotalChunks), "data_worker")

	// Update transfer to active status
	if err := dsw.db.ResumeTransfer(transfer.TransferID); err != nil {
		dsw.logger.Warn(fmt.Sprintf("Failed to update transfer status to active: %v", err), "data_worker")
	}

	// Resend missing chunks
	buffer := make([]byte, dsw.chunkSize)
	chunksSent := make(map[int]bool)

	for _, idx := range transfer.ChunksSent {
		chunksSent[idx] = true
	}

	sentCount := 0
	for _, chunkIndex := range missingChunks {
		// Calculate offset for this chunk
		offset := int64(chunkIndex) * int64(dsw.chunkSize)

		// Seek to chunk position
		if _, err := file.Seek(offset, 0); err != nil {
			return fmt.Errorf("failed to seek to chunk %d: %v", chunkIndex, err)
		}

		// Read chunk data
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read chunk %d: %v", chunkIndex, err)
		}

		if n == 0 {
			break
		}

		isLast := chunkIndex == transfer.TotalChunks-1

		// Send chunk via job handler (with fresh peer metadata for routing)
		if dsw.jobHandler != nil {
			chunk := &types.JobDataChunk{
				TransferID:  transfer.TransferID,
				ChunkIndex:  chunkIndex,
				TotalChunks: transfer.TotalChunks,
				Data:        buffer[:n],
				IsLast:      isLast,
			}

			dsw.logger.Debug(fmt.Sprintf("Resending chunk %d/%d (%d bytes) for transfer %s",
				chunkIndex+1, transfer.TotalChunks, n, transfer.TransferID), "data_worker")

			// Retry logic: try up to 3 times with exponential backoff
			sent := false
			for attempt := 0; attempt < 3; attempt++ {
				err := dsw.jobHandler.SendJobDataChunk(transfer.DestinationPeerID, chunk)
				if err == nil {
					sent = true
					break
				}

				dsw.logger.Warn(fmt.Sprintf("Failed to send chunk %d (attempt %d/3): %v", chunkIndex, attempt+1, err), "data_worker")

				if attempt < 2 {
					backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
					time.Sleep(backoff)
				}
			}

			if !sent {
				dsw.logger.Error(fmt.Sprintf("Failed to send chunk %d after 3 attempts during resume", chunkIndex), "data_worker")
				return fmt.Errorf("failed to send chunk %d after 3 attempts", chunkIndex)
			}

			// Track sent chunk
			chunksSent[chunkIndex] = true
			sentCount++
		}

		// Update progress every 20 chunks
		if sentCount%20 == 0 {
			sentList := make([]int, 0, len(chunksSent))
			for idx := range chunksSent {
				sentList = append(sentList, idx)
			}

			err = dsw.db.UpdateTransferProgress(transfer.TransferID, sentList, nil, transfer.ChunksAcked, transfer.BytesTransferred)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to update transfer progress: %v", err), "data_worker")
			}

			err = dsw.db.UpdateTransferCheckpoint(transfer.TransferID)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to update transfer checkpoint: %v", err), "data_worker")
			}

			dsw.logger.Debug(fmt.Sprintf("Resume progress saved for transfer %s: %d chunks sent",
				transfer.TransferID, sentCount), "data_worker")
		}

		// Small delay to avoid overwhelming the receiver
		time.Sleep(10 * time.Millisecond)
	}

	// Final progress update
	sentList := make([]int, 0, len(chunksSent))
	for idx := range chunksSent {
		sentList = append(sentList, idx)
	}

	err = dsw.db.UpdateTransferProgress(transfer.TransferID, sentList, nil, transfer.ChunksAcked, transfer.BytesTransferred)
	if err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to update final transfer progress: %v", err), "data_worker")
	}

	dsw.logger.Info(fmt.Sprintf("Transfer %s resumed: sent %d missing chunks, waiting for ACKs",
		transfer.TransferID, sentCount), "data_worker")

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

	// Determine if targetPath is a directory (ends with separator) or a file
	// Following libp2p pattern: directories end with "/" or "\"
	isDir := strings.HasSuffix(targetPath, string(os.PathSeparator))

	var actualFilePath string
	if isDir {
		// Create directory
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			return fmt.Errorf("failed to create target directory: %v", err)
		}
		// Save file inside directory with transfer ID as filename
		actualFilePath = filepath.Join(targetPath, transferID+".dat")
		dsw.logger.Info(fmt.Sprintf("Target is directory, saving file as: %s", actualFilePath), "data_worker")
	} else {
		// Create parent directory
		targetDir := filepath.Dir(targetPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %v", err)
		}
		actualFilePath = targetPath
	}

	// Open or create target file
	file, err := os.OpenFile(actualFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open target file: %v", err)
	}

	// Create transfer state
	transfer := &IncomingTransfer{
		TransferID:     transferID,
		JobExecutionID: jobExecutionID,
		SourcePeerID:   sourcePeerID,
		TargetPath:     actualFilePath, // Use actual file path, not directory path
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

	// Update TotalChunks from chunk message (sender knows the actual count)
	// This handles cases where receiver's initial calculation was based on different chunk size
	if transfer.TotalChunks != chunk.TotalChunks {
		dsw.logger.Info(fmt.Sprintf("Updating transfer %s total chunks from %d to %d (using sender's actual chunk count)",
			chunk.TransferID, transfer.TotalChunks, chunk.TotalChunks), "data_worker")
		transfer.TotalChunks = chunk.TotalChunks
	}

	// Check if we already received this chunk
	if transfer.ReceivedChunks[chunk.ChunkIndex] {
		transfer.mu.Unlock()
		dsw.logger.Warn(fmt.Sprintf("Duplicate chunk %d for transfer %s, ignoring", chunk.ChunkIndex, chunk.TransferID), "data_worker")
		return nil
	}

	// Calculate offset for this chunk
	offset := int64(chunk.ChunkIndex) * int64(dsw.chunkSize)

	// Write chunk data to file at correct offset
	n, err := transfer.File.WriteAt(chunk.Data, offset)
	if err != nil {
		transfer.mu.Unlock()
		return fmt.Errorf("failed to write chunk %d to file: %v", chunk.ChunkIndex, err)
	}

	if n != len(chunk.Data) {
		transfer.mu.Unlock()
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
	isComplete := len(transfer.ReceivedChunks) == transfer.TotalChunks

	// Create or update transfer record in database
	receivedList := make([]int, 0, len(transfer.ReceivedChunks))
	for idx := range transfer.ReceivedChunks {
		receivedList = append(receivedList, idx)
	}

	// Check if transfer exists in database
	_, err = dsw.db.GetTransferByID(chunk.TransferID)
	if err != nil {
		// Transfer doesn't exist in database yet, create it
		newTransfer := &database.DataTransfer{
			TransferID:        chunk.TransferID,
			JobExecutionID:    transfer.JobExecutionID,
			SourcePeerID:      transfer.SourcePeerID,
			DestinationPeerID: dsw.peerID,
			FilePath:          transfer.TargetPath,
			TotalChunks:       transfer.TotalChunks,
			ChunkSize:         dsw.chunkSize,
			TotalBytes:        transfer.ExpectedSize,
			FileHash:          transfer.ExpectedHash,
			Encrypted:         transfer.Encrypted,
			ChunksSent:        make([]int, 0),
			ChunksReceived:    receivedList,
			ChunksAcked:       make([]int, 0),
			BytesTransferred:  transfer.BytesReceived,
			Status:            "active",
			Direction:         "receiver",
			LastActivity:      time.Now(),
			LastCheckpoint:    time.Now(),
		}

		err = dsw.db.CreateDataTransfer(newTransfer)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Failed to create transfer record: %v", err), "data_worker")
		} else {
			dsw.logger.Debug(fmt.Sprintf("Created transfer record in database for %s", chunk.TransferID), "data_worker")
		}
	} else {
		// Update existing transfer
		err = dsw.db.UpdateTransferProgress(chunk.TransferID, nil, receivedList, nil, transfer.BytesReceived)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Failed to update transfer progress: %v", err), "data_worker")
		}
	}

	// Send batch ACK every 10-20 chunks (random between 10-20 to avoid synchronization)
	chunkCount := len(transfer.ReceivedChunks)
	shouldSendAck := (chunkCount%15 == 0) // Send ACK every 15 chunks on average

	// Always send ACK on last chunk
	if isComplete {
		shouldSendAck = true
	}

	if shouldSendAck && dsw.jobHandler != nil {
		// Unlock before sending ACK to avoid blocking
		transfer.mu.Unlock()

		ack := &types.JobDataChunkAck{
			TransferID:   chunk.TransferID,
			ChunkIndexes: receivedList,
			Timestamp:    time.Now(),
		}

		err := dsw.jobHandler.SendJobDataChunkAck(peerID, ack)
		if err != nil {
			dsw.logger.Warn(fmt.Sprintf("Failed to send chunk ACK: %v", err), "data_worker")
		} else {
			dsw.logger.Debug(fmt.Sprintf("Sent ACK for %d chunks to peer %s", len(receivedList), peerID[:8]), "data_worker")
		}

		// Re-lock after sending ACK (will be unlocked below)
		transfer.mu.Lock()
	}

	// Always unlock before finalizing or returning
	// finalizeTransfer will acquire its own locks
	transfer.mu.Unlock()

	if isComplete {
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
	if transfer.Encrypted {
		if transfer.Passphrase == "" {
			dsw.logger.Error(fmt.Sprintf("Transfer %s marked as encrypted but passphrase is EMPTY!", transferID), "data_worker")
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			return fmt.Errorf("encrypted transfer missing passphrase")
		}

		dsw.logger.Info(fmt.Sprintf("Decrypting transfer %s", transferID), "data_worker")

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

	// Extract to the parent directory (e.g., if file is input/abc.dat, extract to input/)
	// This puts the decompressed files directly in the input/ directory as expected
	extractDir := filepath.Dir(transfer.TargetPath)
	dsw.logger.Info(fmt.Sprintf("Extracting transfer %s to directory: %s", transferID, extractDir), "data_worker")

	// Ensure extraction directory exists
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

	// Update job status to COMPLETED
	if transfer.JobExecutionID > 0 {
		job, err := dsw.db.GetJobExecution(transfer.JobExecutionID)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Failed to get job execution %d for transfer %s: %v", transfer.JobExecutionID, transferID, err), "data_worker")
		} else if job != nil && job.Status == "RUNNING" {
			// Only update if job is still in RUNNING state
			err = dsw.db.UpdateJobStatus(transfer.JobExecutionID, "COMPLETED", "")
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to update job status for transfer %s: %v", transferID, err), "data_worker")
			} else {
				dsw.logger.Info(fmt.Sprintf("Updated job %d status to COMPLETED after transfer %s", transfer.JobExecutionID, transferID), "data_worker")
			}
		}
	}

	// Mark transfer as completed in database
	if err := dsw.db.CompleteTransfer(transferID); err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to mark transfer as completed in database: %v", err), "data_worker")
	} else {
		dsw.logger.Info(fmt.Sprintf("Transfer %s marked as completed in database", transferID), "data_worker")
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
		return fmt.Errorf("failed to decode hex key data: %v", err)
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

	// Use streaming decryption to match file_processor's streaming encryption
	tempOutputPath := filePath + ".decrypted"
	err = dsw.decryptFileStreaming(filePath, tempOutputPath, key)
	if err != nil {
		return fmt.Errorf("streaming decryption failed: %v", err)
	}

	// Replace encrypted file with decrypted file
	if err := os.Remove(filePath); err != nil {
		os.Remove(tempOutputPath)
		return fmt.Errorf("failed to remove encrypted file: %v", err)
	}
	if err := os.Rename(tempOutputPath, filePath); err != nil {
		return fmt.Errorf("failed to rename decrypted file: %v", err)
	}

	// Get final file size for logging
	fileInfo, _ := os.Stat(filePath)
	decryptedSize := int64(0)
	if fileInfo != nil {
		decryptedSize = fileInfo.Size()
	}

	dsw.logger.Info(fmt.Sprintf("Successfully decrypted file: %s (%d bytes encrypted -> %d bytes decrypted)",
		filePath, len(ciphertext), decryptedSize), "data_worker")

	return nil
}

// decryptFileStreaming decrypts a file that was encrypted with streaming encryption (64KB chunks)
// This matches the file_processor.encryptFileStreaming format
func (dsw *DataServiceWorker) decryptFileStreaming(inputPath, outputPath string, key []byte) error {
	// Open encrypted input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %v", err)
	}
	defer inputFile.Close()

	// Create decrypted output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outputFile.Close()

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

	// Read base nonce (first 12 bytes)
	baseNonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(inputFile, baseNonce); err != nil {
		return fmt.Errorf("failed to read nonce: %v", err)
	}

	// Decrypt file in chunks matching encryption chunk size (64KB chunks)
	// Each encrypted chunk has 16 bytes of GCM authentication tag overhead
	const plaintextChunkSize = 64 * 1024 // 64KB plaintext
	const gcmOverhead = 16                // GCM authentication tag size
	const encryptedChunkSize = plaintextChunkSize + gcmOverhead

	buffer := make([]byte, encryptedChunkSize)
	counter := uint64(0)
	totalDecrypted := int64(0)

	for {
		// Read encrypted chunk
		n, err := inputFile.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read chunk: %v", err)
		}
		if n == 0 {
			break
		}

		// Create chunk nonce by XORing base nonce with counter (matches encryption)
		chunkNonce := make([]byte, len(baseNonce))
		copy(chunkNonce, baseNonce)
		for i := 0; i < 8 && i < len(chunkNonce); i++ {
			chunkNonce[i] ^= byte(counter >> (i * 8))
		}

		// Decrypt chunk
		plainChunk, err := gcm.Open(nil, chunkNonce, buffer[:n], nil)
		if err != nil {
			return fmt.Errorf("failed to decrypt chunk %d: %v", counter, err)
		}

		// Write decrypted chunk to output
		if _, err := outputFile.Write(plainChunk); err != nil {
			return fmt.Errorf("failed to write decrypted chunk: %v", err)
		}

		totalDecrypted += int64(len(plainChunk))
		counter++
	}

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

// handleLocalDataTransfer handles data transfer to the local peer (self)
func (dsw *DataServiceWorker) handleLocalDataTransfer(jobExecutionID int64, filePath string, peer *database.JobInterfacePeer, dataDetails *database.DataServiceDetails) error {
	dsw.logger.Info(fmt.Sprintf("Processing local data transfer for job %d", jobExecutionID), "data_worker")

	// Get job execution to build hierarchical path
	job, err := dsw.db.GetJobExecution(jobExecutionID)
	if err != nil {
		return fmt.Errorf("failed to get job execution: %v", err)
	}

	// Construct hierarchical path: workflows/{ordering_peer_id}/{workflow_job_id}/jobs/{job_id}/{template}
	// This follows the libp2p distributed P2P pattern for clear file organization
	appPaths := utils.GetAppPaths("remote-network")
	hierarchicalPath := filepath.Join(
		appPaths.DataDir,
		"workflows",
		job.OrderingPeerID, // Full peer ID to prevent collisions
		fmt.Sprintf("%d", job.WorkflowJobID),
		"jobs",
		fmt.Sprintf("%d", job.ID),
		peer.PeerPath, // e.g., "input/" or "output/"
	)

	// Preserve directory indicator - filepath.Join() removes trailing separators
	// Re-add it if the template indicated a directory
	if strings.HasSuffix(peer.PeerPath, string(os.PathSeparator)) && !strings.HasSuffix(hierarchicalPath, string(os.PathSeparator)) {
		hierarchicalPath += string(os.PathSeparator)
	}

	dsw.logger.Info(fmt.Sprintf("Resolved path template '%s' to hierarchical path: %s", peer.PeerPath, hierarchicalPath), "data_worker")

	// Determine if hierarchicalPath is a directory (ends with separator) or a file
	isDir := strings.HasSuffix(hierarchicalPath, string(os.PathSeparator))

	var resolvedPath string
	if isDir {
		// Create directory
		if err := os.MkdirAll(hierarchicalPath, 0755); err != nil {
			return fmt.Errorf("failed to create destination directory: %v", err)
		}
		// Save file inside directory with source filename
		resolvedPath = filepath.Join(hierarchicalPath, filepath.Base(filePath))
		dsw.logger.Info(fmt.Sprintf("Target is directory, saving file as: %s", resolvedPath), "data_worker")
	} else {
		// Create parent directory
		destDir := filepath.Dir(hierarchicalPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %v", err)
		}
		resolvedPath = hierarchicalPath
	}

	// Copy the encrypted file to a temporary location first
	tempFile := resolvedPath + ".encrypted.tmp"
	if err := dsw.copyFile(filePath, tempFile); err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}
	defer os.Remove(tempFile) // Clean up temp file

	dsw.logger.Info(fmt.Sprintf("Copied encrypted file to %s", tempFile), "data_worker")

	// Validate hash on encrypted file
	if dataDetails.Hash != "" {
		if err := dsw.validateFileHash(tempFile, dataDetails.Hash); err != nil {
			return fmt.Errorf("hash validation failed: %v", err)
		}
		dsw.logger.Info("Hash validation successful", "data_worker")
	}

	// Decrypt file if encrypted
	var decryptedFile string
	if dataDetails.EncryptionKeyID != nil {
		encryptionKey, err := dsw.db.GetEncryptionKey(dataDetails.ServiceID)
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %v", err)
		}
		if encryptionKey == nil {
			return fmt.Errorf("encryption key not found for service %d", dataDetails.ServiceID)
		}

		dsw.logger.Info("Decrypting file", "data_worker")
		if err := dsw.decryptFile(tempFile, encryptionKey.KeyData); err != nil {
			return fmt.Errorf("decryption failed: %v", err)
		}

		// After decryption, the file extension changes from .encrypted.tmp to .tmp
		decryptedFile = resolvedPath + ".tmp"
		dsw.logger.Info(fmt.Sprintf("File decrypted to %s", decryptedFile), "data_worker")
	} else {
		// No encryption, just rename
		decryptedFile = tempFile
	}
	defer os.Remove(decryptedFile) // Clean up decrypted temp file

	// Decompress file (it's a tar.gz archive)
	dsw.logger.Info(fmt.Sprintf("Decompressing file to %s", resolvedPath), "data_worker")
	if err := utils.Decompress(decryptedFile, resolvedPath); err != nil {
		return fmt.Errorf("decompression failed: %v", err)
	}

	dsw.logger.Info(fmt.Sprintf("Local data transfer completed successfully for job %d", jobExecutionID), "data_worker")
	return nil
}

// copyFile copies a file from src to dst
func (dsw *DataServiceWorker) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	return destFile.Sync()
}

// Close closes the data service worker
func (dsw *DataServiceWorker) Close() {
	dsw.logger.Info("Closing data service worker", "data_worker")

	// Stop monitoring goroutines
	dsw.cancel()

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
