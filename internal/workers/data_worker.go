package workers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// formatPeerID safely formats a peer ID for logging, handling empty strings
func formatPeerID(peerID string) string {
	if peerID == "" {
		return "<empty>"
	}
	if len(peerID) > 8 {
		return peerID[:8]
	}
	return peerID
}

// PeerPublicKeyProvider provides access to peer public keys
type PeerPublicKeyProvider interface {
	GetPeerPublicKey(peerID string) (ed25519.PublicKey, error)
}

// IncomingTransfer tracks an active incoming file transfer
type IncomingTransfer struct {
	TransferID          string
	JobExecutionID      int64
	SourcePeerID        string
	TargetPath          string
	ExpectedHash        string
	ExpectedSize        int64
	TotalChunks         int
	ReceivedChunks      map[int]bool
	File                *os.File
	BytesReceived       int64

	// ECDH encryption fields (replaces Passphrase)
	Encrypted            bool      // Whether this transfer is encrypted
	EphemeralPubKey      []byte    // 32 bytes X25519 ephemeral public key from sender
	KeyExchangeSignature []byte    // Ed25519 signature for authentication
	KeyExchangeTimestamp int64     // Unix timestamp for replay protection
	DerivedKey           *[32]byte // Computed decryption key, cleared after use

	InterfaceType       string    // Interface type (STDOUT, MOUNT, PACKAGE, etc.)
	DestinationFileName string    // Optional: rename file/folder at destination
	StartedAt           time.Time
	LastChunkAt         time.Time
	mu                  sync.Mutex
}

// DataServiceWorker handles DATA service job execution
type DataServiceWorker struct {
	db                *database.SQLiteManager
	cm                *utils.ConfigManager
	logger            *utils.LogsManager
	jobHandler        *p2p.JobMessageHandler
	peerID            string    // Local peer ID
	chunkSize         int       // Size of data chunks for streaming
	incomingTransfers map[string]*IncomingTransfer // transferID -> transfer state
	transfersMu       sync.RWMutex
	ctx               context.Context
	cancel            context.CancelFunc
	monitoringStarted bool
	monitoringMu      sync.Mutex
	keyPair           *crypto.KeyPair                      // Node's Ed25519 identity keys
	dataCrypto        *crypto.DataTransferCryptoManager    // ECDH key exchange for data transfers
	peerManager       PeerPublicKeyProvider                 // Interface to get peer public keys
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

// SetCryptoComponents sets the crypto components for ECDH encryption
func (dsw *DataServiceWorker) SetCryptoComponents(keyPair *crypto.KeyPair, peerManager PeerPublicKeyProvider) {
	dsw.keyPair = keyPair
	dsw.dataCrypto = crypto.NewDataTransferCryptoManager(keyPair)
	dsw.peerManager = peerManager
	dsw.logger.Info("Crypto components set for data service worker", "data_worker")
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
			// Clean up abandoned in-memory transfers (no activity for 5+ minutes)
			dsw.cleanupAbandonedTransfers(5 * time.Minute)

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

// cleanupAbandonedTransfers removes abandoned in-memory transfers that have had no activity for the specified duration
func (dsw *DataServiceWorker) cleanupAbandonedTransfers(timeout time.Duration) {
	dsw.transfersMu.Lock()
	defer dsw.transfersMu.Unlock()

	now := time.Now()
	var abandoned []string

	// Find abandoned transfers
	for transferID, transfer := range dsw.incomingTransfers {
		transfer.mu.Lock()
		lastActivity := transfer.LastChunkAt
		if lastActivity.IsZero() {
			lastActivity = transfer.StartedAt
		}
		transfer.mu.Unlock()

		if now.Sub(lastActivity) > timeout {
			abandoned = append(abandoned, transferID)
		}
	}

	if len(abandoned) == 0 {
		return
	}

	dsw.logger.Info(fmt.Sprintf("Found %d abandoned transfers to clean up", len(abandoned)), "data_worker")

	// Clean up abandoned transfers
	for _, transferID := range abandoned {
		transfer := dsw.incomingTransfers[transferID]
		if transfer == nil {
			continue
		}

		dsw.logger.Warn(fmt.Sprintf("Cleaning up abandoned transfer %s (no activity for %s)",
			transferID, timeout), "data_worker")

		// Close file handle
		if transfer.File != nil {
			transfer.File.Close()
		}

		// Remove the incomplete .dat file
		if transfer.TargetPath != "" {
			if err := os.Remove(transfer.TargetPath); err != nil {
				dsw.logger.Warn(fmt.Sprintf("Failed to remove abandoned transfer file %s: %v",
					transfer.TargetPath, err), "data_worker")
			} else {
				dsw.logger.Info(fmt.Sprintf("Removed abandoned transfer file: %s", transfer.TargetPath), "data_worker")
			}
		}

		// Mark transfer as failed in database
		if err := dsw.db.FailTransfer(transferID, "Transfer abandoned - no activity for "+timeout.String()); err != nil {
			dsw.logger.Error(fmt.Sprintf("Failed to mark transfer %s as failed in database: %v",
				transferID, err), "data_worker")
		}

		// Remove from in-memory map
		delete(dsw.incomingTransfers, transferID)

		dsw.logger.Info(fmt.Sprintf("Abandoned transfer %s cleaned up successfully", transferID), "data_worker")
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
	transferCount := 0
	for _, peer := range peers {
		dsw.logger.Info(fmt.Sprintf("Checking peer %s: PeerMountFunction='%s'",
			formatPeerID(peer.PeerID), peer.PeerMountFunction), "data_worker")

		// With fixed workflow definition, only INPUT peers are destinations that receive data
		if peer.PeerMountFunction == types.MountFunctionInput {
			dsw.logger.Info(fmt.Sprintf("Transferring data to peer %s (path: %s)", formatPeerID(peer.PeerID), peer.PeerPath), "data_worker")

			err := dsw.transferDataToPeer(job, filePath, peer, dataDetails)
			if err != nil {
				dsw.logger.Error(fmt.Sprintf("Failed to transfer data to peer %s: %v", formatPeerID(peer.PeerID), err), "data_worker")
				return fmt.Errorf("failed to transfer data to peer %s: %v", formatPeerID(peer.PeerID), err)
			}

			dsw.logger.Info(fmt.Sprintf("Successfully transferred data to peer %s", formatPeerID(peer.PeerID)), "data_worker")
			transferCount++
		} else {
			dsw.logger.Debug(fmt.Sprintf("Skipping peer %s: mount function '%s' (OUTPUT peers are sources, not destinations)",
				formatPeerID(peer.PeerID), peer.PeerMountFunction), "data_worker")
		}
	}

	if transferCount == 0 {
		dsw.logger.Warn(fmt.Sprintf("DATA service job %d: No transfers performed (found %d peers but none are INPUT destinations)",
			job.ID, len(peers)), "data_worker")
	}

	dsw.logger.Info(fmt.Sprintf("DATA service job %d completed successfully", job.ID), "data_worker")
	return nil
}

// transferDataToPeer transfers data file to a peer
func (dsw *DataServiceWorker) transferDataToPeer(job *database.JobExecution, filePath string, peer *database.JobInterfacePeer, dataDetails *database.DataServiceDetails) error {
	// Check if destination is the local peer (self-transfer)
	// Note: PeerID is always populated (normalized at workflow parsing time)
	// Empty PeerID should never occur, but we handle it defensively
	if peer.PeerID == "" {
		return fmt.Errorf("invalid interface peer: PeerID is empty (should be normalized at workflow parsing)")
	}

	if peer.PeerID == dsw.peerID {
		dsw.logger.Info(fmt.Sprintf("Destination is local peer - handling data locally for job %d", job.ID), "data_worker")
		// Determine interface type for path construction
		// For STDIN: peer.PeerPath = "input" or "input/"
		// For MOUNT: peer.PeerPath = mount path (e.g., "/data", "/output")
		interfaceType := "STDIN"
		peerPath := strings.TrimSuffix(peer.PeerPath, "/")
		if peerPath != "" && peerPath != "input" {
			interfaceType = "MOUNT"
		}
		return dsw.handleLocalDataTransfer(job.ID, filePath, peer, dataDetails, interfaceType)
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
		filePath, fileSize, totalChunks, formatPeerID(peer.PeerID)), "data_worker")

	// Generate transfer ID
	transferID := dsw.GenerateTransferID(job.ID, peer.PeerID)

	// Try to get recipient's public key for ECDH encryption
	// Note: Peer should be in known_peers from job orchestration phase
	recipientPubKey, err := dsw.peerManager.GetPeerPublicKey(peer.PeerID)

	// Variables for transfer - will be set based on whether we can encrypt
	var encryptedHash string
	var encryptedSize int64
	var ephemeralPubKey [32]byte
	var signature []byte
	var timestamp int64
	var encrypted bool
	var tempDir string

	if err != nil {
		// Peer not in known_peers - log warning and fall back to unencrypted transfer
		// This can happen if the orchestrator/peer hasn't been properly discovered yet
		dsw.logger.Warn(fmt.Sprintf("Cannot encrypt data for peer %s: %v. Sending unencrypted (compressed only). This may indicate the peer needs to be discovered first.", formatPeerID(peer.PeerID), err), "data_worker")

		// Use original file (compressed but not encrypted)
		encryptedHash = dataDetails.Hash
		encryptedSize = fileSize
		encrypted = false
	} else {
		// Successfully got public key - proceed with ECDH encryption
		var encryptionKey [32]byte
		ephemeralPubKey, signature, timestamp, encryptionKey, err = dsw.dataCrypto.GenerateKeyExchange(recipientPubKey)
		if err != nil {
			return fmt.Errorf("failed to generate ECDH key exchange: %w", err)
		}
		dsw.logger.Info(fmt.Sprintf("Generated ECDH key exchange for transfer to peer %s", formatPeerID(peer.PeerID)), "data_worker")

		// Create temp directory for encrypted file
		tempDir = filepath.Join(os.TempDir(), fmt.Sprintf("data-transfer-%d-%d", job.ID, time.Now().Unix()))
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer os.RemoveAll(tempDir)

		// Encrypt the file using ECDH-derived key
		encryptedPath := filepath.Join(tempDir, "data.encrypted")
		if err := dsw.encryptFileStreaming(filePath, encryptedPath, encryptionKey); err != nil {
			return fmt.Errorf("failed to encrypt file: %w", err)
		}

		// Get encrypted file info
		encryptedFileInfo, err := os.Stat(encryptedPath)
		if err != nil {
			return fmt.Errorf("failed to get encrypted file info: %w", err)
		}
		encryptedSize = encryptedFileInfo.Size()

		// Calculate hash of encrypted file
		encryptedHash, err = utils.HashFileToCID(encryptedPath)
		if err != nil {
			return fmt.Errorf("failed to calculate encrypted file hash: %w", err)
		}

		// Close original file and open encrypted file for transfer
		file.Close()
		file, err = os.Open(encryptedPath)
		if err != nil {
			return fmt.Errorf("failed to open encrypted file: %w", err)
		}

		// Update size and chunk count for encrypted file
		fileSize = encryptedSize
		totalChunks = int((fileSize + int64(dsw.chunkSize) - 1) / int64(dsw.chunkSize))
		encrypted = true
	}

	// Check if job handler is available for actual transfer
	if dsw.jobHandler != nil {
		// Get destination job execution ID from peer record
		// If PeerWorkflowJobID is nil, this is "Local Peer" (final destination) - use 0
		var destJobExecID int64
		if peer.PeerWorkflowJobID == nil {
			// No workflow job ID - this is "Local Peer" final destination
			destJobExecID = 0
		} else if peer.PeerJobExecutionID != nil {
			// Specific job destination with known execution ID
			destJobExecID = *peer.PeerJobExecutionID
		} else {
			// Fallback: Look up the job_execution by workflow_job_id and ordering_peer_id
			// On executor side, we need to look up job_execution.id directly (not workflow_job.remote_job_execution_id)
			// because the executor IS the remote side, so remote_job_execution_id would be empty
			jobExec, err := dsw.db.GetJobExecutionByWorkflowJobAndOrderingPeer(*peer.PeerWorkflowJobID, job.OrderingPeerID)
			if err == nil && jobExec != nil {
				destJobExecID = jobExec.ID
				dsw.logger.Debug(fmt.Sprintf("Using job_execution_id %d for workflow_job %d (ordering_peer: %s)",
					destJobExecID, *peer.PeerWorkflowJobID, job.OrderingPeerID), "data_worker")
			}
		}

		// Determine destination workflow_job_id
		// peer.PeerWorkflowJobID contains the actual workflow_job.id (already translated from node_id by orchestrator)
		// For "Local Peer" (peer.PeerWorkflowJobID == nil), use source job's workflow_job_id
		destWorkflowJobID := job.WorkflowJobID // Default to source
		if peer.PeerWorkflowJobID != nil {
			destWorkflowJobID = *peer.PeerWorkflowJobID
			dsw.logger.Debug(fmt.Sprintf("Using destination workflow_job_id=%d from peer interface",
				destWorkflowJobID), "data_worker")
		}

		// Determine interface type for path construction
		// For STDIN: peer.PeerPath = "input" or "input/"
		// For MOUNT: peer.PeerPath = mount path (e.g., "/data", "/output")
		interfaceType := "STDIN"
		peerPath := strings.TrimSuffix(peer.PeerPath, "/")
		if peerPath != "" && peerPath != "input" {
			interfaceType = "MOUNT"
		}

		// Determine destination file name for renaming
		var destFileName string
		if peer.PeerFileName != nil {
			destFileName = *peer.PeerFileName
		}

		// Send transfer request using destination's WorkflowJobID
		transferRequest := &types.JobDataTransferRequest{
			TransferID:                transferID,        // Include transfer ID so both peers use the same one
			WorkflowJobID:             destWorkflowJobID, // Use destination's workflow_job_id for correct path
			SourceJobExecutionID:      job.ID,            // Source job execution ID (this job)
			DestinationJobExecutionID: destJobExecID,     // Destination job execution ID (receiver's job)
			InterfaceType:             interfaceType,     // Use determined interface type, not hardcoded STDOUT
			SourcePeerID:              dsw.peerID,
			DestinationPeerID:         peer.PeerID,
			SourcePath:                filePath,
			DestinationPath:           peer.PeerPath,
			DestinationFileName:       destFileName,      // Optional: rename file/folder at destination
			DataHash:                  encryptedHash,     // File hash (encrypted if ECDH available, otherwise compressed)
			SizeBytes:                 encryptedSize,     // File size (encrypted if ECDH available, otherwise compressed)
			Encrypted:                 encrypted,
			EphemeralPubKey:           ephemeralPubKey[:],
			KeyExchangeSignature:      signature,
			KeyExchangeTimestamp:      timestamp,
		}

		dsw.logger.Info(fmt.Sprintf("Sending transfer request for transfer ID %s to peer %s (encrypted: %v)", transferID, formatPeerID(peer.PeerID), encrypted), "data_worker")

		// Send transfer request via job handler
		response, err := dsw.jobHandler.SendJobDataTransferRequest(peer.PeerID, transferRequest)
		if err != nil {
			return fmt.Errorf("failed to send transfer request: %v", err)
		}

		if !response.Accepted {
			return fmt.Errorf("transfer request rejected by peer %s: %s", formatPeerID(peer.PeerID), response.Message)
		}

		dsw.logger.Info(fmt.Sprintf("Transfer request accepted by peer %s for transfer ID %s", formatPeerID(peer.PeerID), transferID), "data_worker")
	}

	// Create transfer record in database for state persistence
	transfer := &database.DataTransfer{
		TransferID:        transferID,
		JobExecutionID:    job.ID,
		SourcePeerID:      dsw.peerID,
		DestinationPeerID: peer.PeerID,
		FilePath:          filePath, // Original file path (may be encrypted temp file or original compressed file)
		TotalChunks:       totalChunks,
		ChunkSize:         dsw.chunkSize,
		TotalBytes:        fileSize,
		FileHash:          encryptedHash,
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
				err := dsw.jobHandler.SendJobDataChunk(peer.PeerID, chunk)
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
		err := dsw.jobHandler.SendJobDataTransferComplete(peer.PeerID, transferComplete)
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
		transfer.TransferID, formatPeerID(transfer.DestinationPeerID)), "data_worker")

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
	hash := utils.HashString(data)
	return hash[:16] // Use first 16 hex chars as transfer ID
}


// InitializeIncomingTransfer creates a new incoming transfer state
func (dsw *DataServiceWorker) InitializeIncomingTransfer(transferID string, jobExecutionID int64, sourcePeerID string, targetPath string, expectedHash string, expectedSize int64, totalChunks int) error {
	return dsw.InitializeIncomingTransferWithPassphrase(transferID, jobExecutionID, sourcePeerID, targetPath, expectedHash, expectedSize, totalChunks, "", false)
}

// InitializeIncomingTransferWithPassphrase creates a new incoming transfer state with optional encryption
// Deprecated: Use InitializeIncomingTransferFull with ECDH parameters instead
func (dsw *DataServiceWorker) InitializeIncomingTransferWithPassphrase(transferID string, jobExecutionID int64, sourcePeerID string, targetPath string, expectedHash string, expectedSize int64, totalChunks int, passphrase string, encrypted bool) error {
	return dsw.InitializeIncomingTransferFull(transferID, jobExecutionID, sourcePeerID, targetPath, expectedHash, expectedSize, totalChunks, encrypted, nil, nil, 0, "", "")
}

// InitializeIncomingTransferFull creates a new incoming transfer state with all options including ECDH encryption
func (dsw *DataServiceWorker) InitializeIncomingTransferFull(transferID string, jobExecutionID int64, sourcePeerID string, targetPath string, expectedHash string, expectedSize int64, totalChunks int, encrypted bool, ephemeralPubKey []byte, keyExchangeSignature []byte, keyExchangeTimestamp int64, interfaceType string, destinationFileName string) error {
	dsw.transfersMu.Lock()
	defer dsw.transfersMu.Unlock()

	// Check if transfer already exists
	if _, exists := dsw.incomingTransfers[transferID]; exists {
		return nil // Already initialized
	}

	dsw.logger.Info(fmt.Sprintf("Initializing incoming transfer %s: %s (%d bytes, %d chunks, encrypted: %v, type: %s)",
		transferID, targetPath, expectedSize, totalChunks, encrypted, interfaceType), "data_worker")

	// Determine if targetPath is a directory (ends with separator) or a file
	// Following libp2p pattern: directories end with "/" or "\"
	isDir := strings.HasSuffix(targetPath, string(os.PathSeparator))

	var actualFilePath string
	if isDir {
		// Create directory with world-writable permissions for Docker container access
		if err := os.MkdirAll(targetPath, 0777); err != nil {
			return fmt.Errorf("failed to create target directory: %v", err)
		}
		os.Chmod(targetPath, 0777)
		// Save file inside directory with transfer ID as filename
		actualFilePath = filepath.Join(targetPath, transferID+".dat")
		dsw.logger.Info(fmt.Sprintf("Target is directory, saving file as: %s", actualFilePath), "data_worker")
	} else {
		// Create parent directory with world-writable permissions for Docker container access
		targetDir := filepath.Dir(targetPath)
		if err := os.MkdirAll(targetDir, 0777); err != nil {
			return fmt.Errorf("failed to create parent directory: %v", err)
		}
		os.Chmod(targetDir, 0777)
		actualFilePath = targetPath
	}

	// Open or create target file
	file, err := os.OpenFile(actualFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open target file: %v", err)
	}

	// Create transfer state
	transfer := &IncomingTransfer{
		TransferID:           transferID,
		JobExecutionID:       jobExecutionID,
		SourcePeerID:         sourcePeerID,
		TargetPath:           actualFilePath, // Use actual file path, not directory path
		ExpectedHash:         expectedHash,
		ExpectedSize:         expectedSize,
		TotalChunks:          totalChunks,
		ReceivedChunks:       make(map[int]bool),
		File:                 file,
		BytesReceived:        0,
		Encrypted:            encrypted,
		EphemeralPubKey:      ephemeralPubKey,
		KeyExchangeSignature: keyExchangeSignature,
		KeyExchangeTimestamp: keyExchangeTimestamp,
		InterfaceType:        interfaceType,
		DestinationFileName:  destinationFileName, // Optional: rename file/folder at destination
		StartedAt:            time.Now(),
		LastChunkAt:          time.Now(),
	}

	// If encrypted, derive the decryption key from ECDH parameters
	if encrypted && len(ephemeralPubKey) == 32 && dsw.dataCrypto != nil && dsw.peerManager != nil {
		// Get sender's public key
		senderPubKey, err := dsw.peerManager.GetPeerPublicKey(sourcePeerID)
		if err != nil {
			file.Close()
			return fmt.Errorf("failed to get sender public key: %w", err)
		}

		// Convert ephemeral public key to [32]byte
		var ephKey [32]byte
		copy(ephKey[:], ephemeralPubKey)

		// Derive decryption key using ECDH
		derivedKey, err := dsw.dataCrypto.DeriveDecryptionKey(
			ephKey,
			keyExchangeSignature,
			keyExchangeTimestamp,
			senderPubKey,
		)
		if err != nil {
			file.Close()
			return fmt.Errorf("ECDH key derivation failed: %w", err)
		}

		// Store derived key (will be cleared after decryption)
		transfer.DerivedKey = &derivedKey
		dsw.logger.Info(fmt.Sprintf("Derived decryption key for transfer %s", transferID), "data_worker")
	}

	dsw.incomingTransfers[transferID] = transfer
	dsw.logger.Info(fmt.Sprintf("Incoming transfer %s initialized (encrypted: %v)", transferID, encrypted), "data_worker")

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
			// Clear derived key from memory
			if transfer.DerivedKey != nil {
				crypto.ZeroKey(transfer.DerivedKey)
				transfer.DerivedKey = nil
			}
			return fmt.Errorf("hash validation failed: %v", err)
		}
		dsw.logger.Info(fmt.Sprintf("Transfer %s hash validated successfully", transferID), "data_worker")
	}

	// Decrypt file if encrypted (AFTER hash validation)
	if transfer.Encrypted {
		if transfer.DerivedKey == nil {
			dsw.logger.Error(fmt.Sprintf("Transfer %s marked as encrypted but decryption key is missing!", transferID), "data_worker")
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			return fmt.Errorf("encrypted transfer missing decryption key")
		}

		dsw.logger.Info(fmt.Sprintf("Decrypting transfer %s", transferID), "data_worker")

		err := dsw.decryptFile(transfer.TargetPath, transfer)
		if err != nil {
			dsw.logger.Error(fmt.Sprintf("Decryption failed for transfer %s: %v", transferID, err), "data_worker")
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			// Clear derived key from memory
			crypto.ZeroKey(transfer.DerivedKey)
			transfer.DerivedKey = nil
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

	// Ensure extraction directory exists with world-writable permissions for Docker container access
	if err := os.MkdirAll(extractDir, 0777); err != nil {
		dsw.logger.Error(fmt.Sprintf("Failed to create extraction directory for transfer %s: %v", transferID, err), "data_worker")
		os.Remove(transfer.TargetPath)
		delete(dsw.incomingTransfers, transferID)
		// Clear derived key from memory
		if transfer.DerivedKey != nil {
			crypto.ZeroKey(transfer.DerivedKey)
			transfer.DerivedKey = nil
		}
		return fmt.Errorf("failed to create extraction directory: %v", err)
	}

	// Check if this is a transfer package (PACKAGE interface type)
	isTransferPackage := transfer.InterfaceType == types.InterfaceTypePackage

	if isTransferPackage {
		// Handle transfer package with manifest-based extraction
		dsw.logger.Info(fmt.Sprintf("Transfer %s is a transfer package, using manifest-based extraction", transferID), "data_worker")

		// Determine the base directory for extraction
		// The hierarchical path is already constructed in HandleJobDataTransferRequest
		// transfer.TargetPath follows this pattern:
		// .../jobs/<sender_peer>/<sender_job>/<receiver_peer>/<receiver_job>/<destDir>/transfer_id.dat
		// where <destDir> can be:
		//   - "input" for STD interfaces
		//   - "mounts/<mount_path>" for MOUNT interfaces (can be multiple levels deep!)
		//
		// The manifest entries have relative paths like "input/logs.txt" or "mounts/app/file.md"
		// So baseDir must be the receiver job directory: .../jobs/<sender>/<sender_job>/<receiver>/<receiver_job>/
		//
		// To extract baseDir, we parse the hierarchical path structure:
		// Find "/jobs/" and then parse 4 path components forward: sender_peer, sender_job, receiver_peer, receiver_job

		targetPath := transfer.TargetPath
		jobsIndex := strings.Index(targetPath, "/jobs/")
		if jobsIndex == -1 {
			return fmt.Errorf("invalid hierarchical path format: missing /jobs/ in path: %s", targetPath)
		}

		// Get the path after "/jobs/"
		pathAfterJobs := targetPath[jobsIndex+6:] // Skip "/jobs/"

		// Split into components and take first 4: <sender_peer>/<sender_job>/<receiver_peer>/<receiver_job>
		components := strings.Split(pathAfterJobs, string(os.PathSeparator))
		if len(components) < 4 {
			return fmt.Errorf("invalid hierarchical path format: not enough components after /jobs/: %s", targetPath)
		}

		// Reconstruct baseDir up to and including receiver_job
		baseDir := filepath.Join(targetPath[:jobsIndex], "jobs", components[0], components[1], components[2], components[3])

		dsw.logger.Info(fmt.Sprintf("Transfer %s extraction: targetPath=%s, baseDir=%s",
			transferID, targetPath, baseDir), "data_worker")

		// Use transfer package extractor
		extractor := utils.NewTransferPackageExtractor(dsw.logger)
		if err := extractor.ExtractAndPlace(transfer.TargetPath, baseDir); err != nil {
			dsw.logger.Error(fmt.Sprintf("Transfer package extraction failed for %s: %v", transferID, err), "data_worker")
			os.Remove(transfer.TargetPath)
			delete(dsw.incomingTransfers, transferID)
			if transfer.DerivedKey != nil {
				crypto.ZeroKey(transfer.DerivedKey)
				transfer.DerivedKey = nil
			}
			return fmt.Errorf("transfer package extraction failed: %v", err)
		}

		// Remove the package archive after successful extraction
		if err := os.Remove(transfer.TargetPath); err != nil {
			dsw.logger.Warn(fmt.Sprintf("Failed to remove transfer package archive for %s: %v", transferID, err), "data_worker")
		}

		dsw.logger.Info(fmt.Sprintf("Transfer package %s extracted successfully to %s", transferID, baseDir), "data_worker")
	} else {
		// Standard decompression for DATA service transfers
		// Use DecompressWithRename if DestinationFileName is set for file/folder renaming
		if err := utils.DecompressWithRename(transfer.TargetPath, extractDir, transfer.DestinationFileName); err != nil {
			dsw.logger.Error(fmt.Sprintf("Decompression failed for transfer %s: %v", transferID, err), "data_worker")
			os.Remove(transfer.TargetPath)
			os.RemoveAll(extractDir)
			delete(dsw.incomingTransfers, transferID)
			// Clear derived key from memory
			if transfer.DerivedKey != nil {
				crypto.ZeroKey(transfer.DerivedKey)
				transfer.DerivedKey = nil
			}
			return fmt.Errorf("decompression failed: %v", err)
		}
		if transfer.DestinationFileName != "" {
			dsw.logger.Info(fmt.Sprintf("Transfer %s extracted with rename to '%s'", transferID, transfer.DestinationFileName), "data_worker")
		}

		// Check if extracted content contains a manifest (backward compatibility check)
		// In case the sender didn't set interface type correctly
		manifestPath := filepath.Join(extractDir, types.TransferManifestFileName)
		if _, err := os.Stat(manifestPath); err == nil {
			dsw.logger.Info("Found manifest.json in extracted content, handling as transfer package", "data_worker")

			// Get the job's base directory
			job, err := dsw.db.GetJobExecution(transfer.JobExecutionID)
			if err == nil && job != nil {
				appPaths := utils.GetAppPaths("remote-network")
				baseDir := filepath.Join(
					appPaths.DataDir,
					"workflows",
					job.OrderingPeerID,
					fmt.Sprintf("%d", job.WorkflowJobID),
					"jobs",
					fmt.Sprintf("%d", job.ID),
				)

				// Re-extract using package extractor for proper file placement
				extractor := utils.NewTransferPackageExtractor(dsw.logger)
				if err := extractor.ExtractAndPlace(transfer.TargetPath, baseDir); err != nil {
					dsw.logger.Warn(fmt.Sprintf("Failed to re-extract as transfer package: %v", err), "data_worker")
					// Continue with standard extraction results
				} else {
					// Clean up the original extraction directory contents
					// since we've re-extracted properly
					dsw.logger.Info(fmt.Sprintf("Transfer package re-extraction successful to %s", baseDir), "data_worker")
				}
			}
		}

		// Remove the compressed archive after successful extraction
		if err := os.Remove(transfer.TargetPath); err != nil {
			dsw.logger.Warn(fmt.Sprintf("Failed to remove compressed archive for transfer %s: %v", transferID, err), "data_worker")
		}

		dsw.logger.Info(fmt.Sprintf("Transfer %s decompressed successfully to %s", transferID, extractDir), "data_worker")
	}

	// Clear derived key from memory immediately after use
	if transfer.DerivedKey != nil {
		crypto.ZeroKey(transfer.DerivedKey)
		transfer.DerivedKey = nil
		dsw.logger.Debug(fmt.Sprintf("Decryption key cleared from memory for transfer %s", transferID), "data_worker")
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
// decryptFile decrypts a file using ECDH-derived key from transfer
func (dsw *DataServiceWorker) decryptFile(filePath string, transfer *IncomingTransfer) error {
	// Verify derived key is available
	if transfer.DerivedKey == nil {
		return fmt.Errorf("no decryption key available")
	}

	// Use streaming decryption with ECDH-derived key
	tempOutputPath := filePath + ".decrypted"
	err := dsw.decryptFileStreaming(filePath, tempOutputPath, transfer.DerivedKey[:])
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

	dsw.logger.Info(fmt.Sprintf("Successfully decrypted file: %s (%d bytes decrypted)",
		filePath, decryptedSize), "data_worker")

	return nil
}

// decryptFileWithPassphrase decrypts a file using legacy passphrase-based encryption
// Deprecated: Used for backward compatibility with local transfers
func (dsw *DataServiceWorker) decryptFileWithPassphrase(filePath string, keyData string) error {
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

	// Use streaming decryption
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

	dsw.logger.Info(fmt.Sprintf("Successfully decrypted file: %s (%d bytes decrypted)",
		filePath, decryptedSize), "data_worker")

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

// encryptFileStreaming encrypts a file using AES-256-GCM in chunks
func (dsw *DataServiceWorker) encryptFileStreaming(inputPath, outputPath string, key [32]byte) error {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	if _, err := outputFile.Write(nonce); err != nil {
		return fmt.Errorf("failed to write nonce: %w", err)
	}

	const chunkSize = 64 * 1024
	buffer := make([]byte, chunkSize)
	counter := uint64(0)

	for {
		n, err := inputFile.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read chunk: %w", err)
		}
		if n == 0 {
			break
		}

		chunkNonce := make([]byte, len(nonce))
		copy(chunkNonce, nonce)
		for i := 0; i < 8 && i < len(chunkNonce); i++ {
			chunkNonce[i] ^= byte(counter >> (i * 8))
		}

		cipherChunk := gcm.Seal(nil, chunkNonce, buffer[:n], nil)
		if _, err := outputFile.Write(cipherChunk); err != nil {
			return fmt.Errorf("failed to write encrypted chunk: %w", err)
		}

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
func (dsw *DataServiceWorker) handleLocalDataTransfer(jobExecutionID int64, filePath string, peer *database.JobInterfacePeer, dataDetails *database.DataServiceDetails, interfaceType string) error {
	dsw.logger.Info(fmt.Sprintf("Processing local data transfer for job %d", jobExecutionID), "data_worker")

	// Get job execution to build hierarchical path
	job, err := dsw.db.GetJobExecution(jobExecutionID)
	if err != nil {
		return fmt.Errorf("failed to get job execution: %v", err)
	}

	appPaths := utils.GetAppPaths("remote-network")
	var hierarchicalPath string

	// Check if this is a transfer to the requester (no receiving job) or to another job
	if peer.PeerJobExecutionID == nil || *peer.PeerJobExecutionID == 0 {
		// Transfer to workflow output (for the requester/user - "Local Peer")
		// Pattern: /workflows/<orchestrator>/<execution>/jobs/<sender_peer>/<sender_job>/<receiver_peer>/0/input/
		// Note: receiver_job_id = 0 as a special marker for "Local Peer" final destination

		// Get workflow context to get the workflow_execution_id
		// Note: GetWorkflowJobByID returns (nil, nil) if row not found
		workflowJob, err := dsw.db.GetWorkflowJobByID(job.WorkflowJobID)
		if err != nil {
			return fmt.Errorf("failed to get workflow job %d: %v", job.WorkflowJobID, err)
		}
		if workflowJob == nil {
			return fmt.Errorf("workflow job %d not found", job.WorkflowJobID)
		}

		// Build path with sender (this job) and receiver (orchestrator/"Local Peer")
		pathInfo := utils.JobPathInfo{
			OrchestratorPeerID: job.OrderingPeerID,
			WorkflowJobID:      workflowJob.ID,      // Use workflow_job.id for consistent paths
			ExecutorPeerID:     job.ExecutorPeerID,  // Sender peer
			JobExecutionID:     job.ID,              // Sender job execution ID
			ReceiverPeerID:     job.OrderingPeerID,  // Receiver is orchestrator
			ReceiverJobExecID:  0,                   // 0 for "Local Peer"
		}

		hierarchicalPath = utils.BuildTransferDestinationPath(
			appPaths.DataDir,
			pathInfo,
			interfaceType,
			peer.PeerPath,
		)
		dsw.logger.Info(fmt.Sprintf("Transferring to workflow output (requester/'Local Peer') at %s", hierarchicalPath), "data_worker")
	} else {
		// Transfer to another job's input
		destJobExecID := *peer.PeerJobExecutionID
		dsw.logger.Info(fmt.Sprintf("Transferring to receiver job_execution %d (sender job %d)", destJobExecID, job.ID), "data_worker")

		// Get destination job execution to determine executor peer and workflow_job_id
		destJob, err := dsw.db.GetJobExecution(destJobExecID)
		if err != nil {
			return fmt.Errorf("failed to get destination job execution %d: %v", destJobExecID, err)
		}

		// Build path info with sender and receiver information
		// IMPORTANT: Use receiver's WorkflowJobID for consistent path construction
		// The receiver (job_manager.checkInputsReady) uses its own WorkflowJobID when checking for input,
		// so the sender must use the receiver's WorkflowJobID when constructing the destination path
		pathInfo := utils.JobPathInfo{
			OrchestratorPeerID: job.OrderingPeerID,
			WorkflowJobID:      destJob.WorkflowJobID,    // Use RECEIVER's workflow_job.id for consistent paths
			ExecutorPeerID:     job.ExecutorPeerID,       // Sender peer
			JobExecutionID:     job.ID,                   // Sender job execution ID
			ReceiverPeerID:     destJob.ExecutorPeerID,   // Receiver peer
			ReceiverJobExecID:  destJobExecID,            // Receiver job execution ID
		}

		// Use BuildTransferDestinationPath which includes sender and receiver info
		hierarchicalPath = utils.BuildTransferDestinationPath(
			appPaths.DataDir,
			pathInfo,
			interfaceType,
			peer.PeerPath,
		)
		dsw.logger.Info(fmt.Sprintf("Transferring to receiver job at %s", hierarchicalPath), "data_worker")
	}

	// Add trailing separator to indicate directory
	hierarchicalPath += string(os.PathSeparator)

	dsw.logger.Info(fmt.Sprintf("Resolved path '%s' to hierarchical path: %s", peer.PeerPath, hierarchicalPath), "data_worker")

	// Determine if hierarchicalPath is a directory (ends with separator) or a file
	isDir := strings.HasSuffix(hierarchicalPath, string(os.PathSeparator))

	// Create the destination directory
	var extractDir string
	if isDir {
		// hierarchicalPath is the extraction directory (e.g., input/)
		extractDir = hierarchicalPath
		if err := os.MkdirAll(extractDir, 0777); err != nil {
			return fmt.Errorf("failed to create destination directory: %v", err)
		}
		os.Chmod(extractDir, 0777)
		dsw.logger.Info(fmt.Sprintf("Extracting to directory: %s", extractDir), "data_worker")
	} else {
		// hierarchicalPath is a file path - extract to parent directory
		extractDir = filepath.Dir(hierarchicalPath)
		if err := os.MkdirAll(extractDir, 0777); err != nil {
			return fmt.Errorf("failed to create parent directory: %v", err)
		}
		os.Chmod(extractDir, 0777)
		dsw.logger.Info(fmt.Sprintf("Extracting to parent directory: %s", extractDir), "data_worker")
	}

	// Use a temp file in the extraction directory for processing
	// This matches the remote transfer behavior where the archive is in the same dir
	tempFile := filepath.Join(extractDir, filepath.Base(filePath)) + ".tmp"
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

	// Decrypt file if encrypted at rest (legacy DATA services only)
	// New DATA services are not encrypted at rest, only compressed
	var decryptedFile string
	if dataDetails.EncryptionKeyID != nil {
		// Legacy: decrypt using database-stored key (files created before ECDH-only implementation)
		encryptionKey, err := dsw.db.GetEncryptionKey(dataDetails.ServiceID)
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %v", err)
		}
		if encryptionKey == nil {
			return fmt.Errorf("encryption key not found for service %d", dataDetails.ServiceID)
		}

		dsw.logger.Info("Decrypting legacy encrypted-at-rest file", "data_worker")
		if err := dsw.decryptFileWithPassphrase(tempFile, encryptionKey.KeyData); err != nil {
			return fmt.Errorf("decryption failed: %v", err)
		}

		// decryptFile decrypts in place - file is still at tempFile
		decryptedFile = tempFile
		dsw.logger.Info(fmt.Sprintf("File decrypted in place at %s", decryptedFile), "data_worker")
	} else {
		// New: file is not encrypted at rest (only compressed)
		// Encryption only happens during transfer via ECDH
		decryptedFile = tempFile
		dsw.logger.Info("File is not encrypted at rest (compressed only)", "data_worker")
	}
	defer os.Remove(decryptedFile) // Clean up decrypted temp file

	// Decompress file (it's a tar.gz archive)
	// Extract directly to the extraction directory (e.g., input/)
	// This matches remote transfer behavior where files are extracted directly without a containing folder
	// If PeerFileName is set, rename the root file/folder during extraction
	var newName string
	if peer.PeerFileName != nil && *peer.PeerFileName != "" {
		newName = *peer.PeerFileName
		dsw.logger.Info(fmt.Sprintf("Decompressing file to %s with rename to '%s'", extractDir, newName), "data_worker")
	} else {
		dsw.logger.Info(fmt.Sprintf("Decompressing file to %s", extractDir), "data_worker")
	}

	if err := utils.DecompressWithRename(decryptedFile, extractDir, newName); err != nil {
		return fmt.Errorf("decompression failed: %v", err)
	}

	dsw.logger.Info(fmt.Sprintf("Local data transfer completed successfully for job %d to %s", jobExecutionID, extractDir), "data_worker")
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
