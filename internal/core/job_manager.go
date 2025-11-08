package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/workers"
)

// JobManager manages job lifecycle and execution
type JobManager struct {
	ctx              context.Context
	cancel           context.CancelFunc
	db               *database.SQLiteManager
	cm               *utils.ConfigManager
	logger           *utils.LogsManager
	workerPool       *workers.WorkerPool
	dataWorker       *workers.DataServiceWorker
	statusUpdateChan chan *types.JobStatusUpdate
	peerManager      *PeerManager // Reference to peer manager for P2P communication
	tickerInterval   time.Duration
	ticker           *time.Ticker
	tickerStopChan   chan bool
	wg               sync.WaitGroup
	mu               sync.RWMutex
	runningJobs      map[int64]bool // Track running job IDs
}

// NewJobManager creates a new job manager
func NewJobManager(ctx context.Context, db *database.SQLiteManager, cm *utils.ConfigManager, peerManager *PeerManager) *JobManager {
	jobCtx, cancel := context.WithCancel(ctx)

	// Get configuration
	numWorkers := cm.GetConfigInt("job_worker_pool_size", 5, 1, 100)
	tickerInterval := time.Duration(cm.GetConfigInt("job_queue_check_interval_seconds", 10, 1, 300)) * time.Second

	// Create worker pool
	workerPool := workers.NewWorkerPool(jobCtx, numWorkers, cm)

	// Create data service worker (will be set later when job handler is available)
	// For now, pass nil for job handler
	dataWorker := workers.NewDataServiceWorker(db, cm, nil)

	jm := &JobManager{
		ctx:              jobCtx,
		cancel:           cancel,
		db:               db,
		cm:               cm,
		logger:           utils.NewLogsManager(cm),
		workerPool:       workerPool,
		dataWorker:       dataWorker,
		statusUpdateChan: make(chan *types.JobStatusUpdate, 100),
		peerManager:      peerManager,
		tickerInterval:   tickerInterval,
		tickerStopChan:   make(chan bool),
		runningJobs:      make(map[int64]bool),
	}

	return jm
}

// Start starts the job manager
func (jm *JobManager) Start() error {
	jm.logger.Info("Starting Job Manager", "job_manager")

	// Start worker pool
	jm.workerPool.Start()

	// Start status update worker
	jm.startStatusUpdateWorker()

	// Start job queue processor
	jm.startJobQueueProcessor()

	jm.logger.Info("Job Manager started successfully", "job_manager")
	return nil
}

// Stop stops the job manager
func (jm *JobManager) Stop() {
	jm.logger.Info("Stopping Job Manager", "job_manager")

	// Stop ticker
	if jm.ticker != nil {
		jm.ticker.Stop()
	}
	close(jm.tickerStopChan)

	// Cancel context
	jm.cancel()

	// Stop worker pool
	jm.workerPool.Stop()

	// Close data worker
	if jm.dataWorker != nil {
		jm.dataWorker.Close()
	}

	// Close status update channel
	close(jm.statusUpdateChan)

	// Wait for all goroutines
	jm.wg.Wait()

	jm.logger.Info("Job Manager stopped", "job_manager")
	jm.logger.Close()
}

// startJobQueueProcessor starts the periodic job queue processor
func (jm *JobManager) startJobQueueProcessor() {
	jm.ticker = time.NewTicker(jm.tickerInterval)
	jm.wg.Add(1)

	go func() {
		defer jm.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				jm.logger.Error(fmt.Sprintf("Job queue processor panic recovered: %v", r), "job_manager")
			}
		}()

		jm.logger.Info("Job queue processor started", "job_manager")

		for {
			select {
			case <-jm.ticker.C:
				jm.processJobQueue()

			case <-jm.tickerStopChan:
				jm.logger.Info("Job queue processor stopping", "job_manager")
				return

			case <-jm.ctx.Done():
				jm.logger.Info("Job queue processor stopping (context done)", "job_manager")
				return
			}
		}
	}()
}

// processJobQueue finds and dispatches READY jobs
func (jm *JobManager) processJobQueue() {
	jm.logger.Info("Processing job queue", "job_manager")

	// Get all READY jobs
	jobs, err := jm.db.GetReadyJobs()
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get ready jobs: %v", err), "job_manager")
		return
	}

	if len(jobs) == 0 {
		jm.logger.Info("No ready jobs to process", "job_manager")
		return
	}

	jm.logger.Info(fmt.Sprintf("Found %d ready jobs", len(jobs)), "job_manager")

	for _, job := range jobs {
		// Check if job is already running
		jm.mu.RLock()
		isRunning := jm.runningJobs[job.ID]
		jm.mu.RUnlock()

		if isRunning {
			jm.logger.Info(fmt.Sprintf("Job %d is already running, skipping", job.ID), "job_manager")
			continue
		}

		// Validate execution constraints
		if job.ExecutionConstraint == types.ExecutionConstraintInputsReady {
			ready, err := jm.db.ValidateJobInputsReady(job.ID)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to validate inputs for job %d: %v", job.ID, err), "job_manager")
				continue
			}

			if !ready {
				jm.logger.Info(fmt.Sprintf("Job %d inputs not ready, skipping", job.ID), "job_manager")
				continue
			}
		}

		// Submit job to worker pool
		jobCopy := job // Create copy for closure
		err := jm.workerPool.Submit(func() {
			jm.executeJob(jobCopy)
		})

		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to submit job %d to worker pool: %v", job.ID, err), "job_manager")
		} else {
			// Mark job as running
			jm.mu.Lock()
			jm.runningJobs[job.ID] = true
			jm.mu.Unlock()

			jm.logger.Info(fmt.Sprintf("Job %d submitted to worker pool", job.ID), "job_manager")
		}
	}
}

// executeJob executes a single job
func (jm *JobManager) executeJob(job *database.JobExecution) {
	defer func() {
		// Remove from running jobs
		jm.mu.Lock()
		delete(jm.runningJobs, job.ID)
		jm.mu.Unlock()

		if r := recover(); r != nil {
			jm.logger.Error(fmt.Sprintf("Job %d execution panic recovered: %v", job.ID, r), "job_manager")

			// Update job status to ERRORED
			err := jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, fmt.Sprintf("Panic during execution: %v", r))
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to update job %d status after panic: %v", job.ID, err), "job_manager")
			}

			// Send status update
			jm.sendStatusUpdate(job.ID, job.WorkflowJobID, types.JobStatusErrored, fmt.Sprintf("Panic during execution: %v", r))
		}
	}()

	jm.logger.Info(fmt.Sprintf("Executing job %d (workflow job %d)", job.ID, job.WorkflowJobID), "job_manager")

	// Update status to RUNNING
	err := jm.db.UpdateJobStatus(job.ID, types.JobStatusRunning, "")
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job %d status to RUNNING: %v", job.ID, err), "job_manager")
		return
	}

	// Send status update
	jm.sendStatusUpdate(job.ID, job.WorkflowJobID, types.JobStatusRunning, "")

	// Get service details
	service, err := jm.db.GetService(job.ServiceID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get service %d for job %d: %v", job.ServiceID, job.ID, err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Service not found: %v", err))
		return
	}

	// Execute based on service type
	switch service.ServiceType {
	case types.ServiceTypeData:
		jm.executeDataService(job, service)

	case types.ServiceTypeDocker:
		jm.logger.Error(fmt.Sprintf("DOCKER service type not yet implemented for job %d", job.ID), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, "DOCKER service type not yet implemented")

	case types.ServiceTypeStandalone:
		jm.logger.Error(fmt.Sprintf("STANDALONE service type not yet implemented for job %d", job.ID), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, "STANDALONE service type not yet implemented")

	default:
		jm.logger.Error(fmt.Sprintf("Unknown service type %s for job %d", service.ServiceType, job.ID), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Unknown service type: %s", service.ServiceType))
	}
}

// executeDataService executes a DATA service job
func (jm *JobManager) executeDataService(job *database.JobExecution, service *database.OfferedService) {
	jm.logger.Info(fmt.Sprintf("Executing DATA service for job %d", job.ID), "job_manager")

	// Use data worker to execute the service
	err := jm.dataWorker.ExecuteDataService(job, service)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("DATA service execution failed for job %d: %v", job.ID, err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("DATA service execution failed: %v", err))
		return
	}

	// Update status to COMPLETED
	err = jm.db.UpdateJobStatus(job.ID, types.JobStatusCompleted, "")
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job %d status to COMPLETED: %v", job.ID, err), "job_manager")
		return
	}

	// Send status update
	jm.sendStatusUpdate(job.ID, job.WorkflowJobID, types.JobStatusCompleted, "")
}

// handleJobError handles job execution errors
func (jm *JobManager) handleJobError(jobExecutionID int64, workflowJobID int64, errorMsg string) {
	// Update status to ERRORED
	err := jm.db.UpdateJobStatus(jobExecutionID, types.JobStatusErrored, errorMsg)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job %d status to ERRORED: %v", jobExecutionID, err), "job_manager")
	}

	// Send status update
	jm.sendStatusUpdate(jobExecutionID, workflowJobID, types.JobStatusErrored, errorMsg)
}

// sendStatusUpdate sends a status update to the ordering peer
func (jm *JobManager) sendStatusUpdate(jobExecutionID int64, workflowJobID int64, status string, errorMsg string) {
	update := &types.JobStatusUpdate{
		JobExecutionID: jobExecutionID,
		WorkflowJobID:  workflowJobID,
		Status:         status,
		ErrorMessage:   errorMsg,
		UpdatedAt:      time.Now(),
	}

	select {
	case jm.statusUpdateChan <- update:
		jm.logger.Info(fmt.Sprintf("Status update queued for job %d: %s", jobExecutionID, status), "job_manager")
	default:
		jm.logger.Error(fmt.Sprintf("Status update channel full, dropping update for job %d", jobExecutionID), "job_manager")
	}
}

// startStatusUpdateWorker starts the worker that processes status updates
func (jm *JobManager) startStatusUpdateWorker() {
	jm.wg.Add(1)

	go func() {
		defer jm.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				jm.logger.Error(fmt.Sprintf("Status update worker panic recovered: %v", r), "job_manager")
			}
		}()

		jm.logger.Info("Status update worker started", "job_manager")

		for {
			select {
			case update := <-jm.statusUpdateChan:
				jm.processStatusUpdate(update)

			case <-jm.ctx.Done():
				jm.logger.Info("Status update worker stopping (context done)", "job_manager")
				return
			}
		}
	}()
}

// processStatusUpdate processes a single status update
func (jm *JobManager) processStatusUpdate(update *types.JobStatusUpdate) {
	jm.logger.Info(fmt.Sprintf("Processing status update for job %d: %s", update.JobExecutionID, update.Status), "job_manager")

	// Get job execution to find ordering peer
	job, err := jm.db.GetJobExecution(update.JobExecutionID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get job execution %d for status update: %v", update.JobExecutionID, err), "job_manager")
		return
	}

	// Send status update to ordering peer via P2P
	// TODO: Implement in Phase 4 when QUIC integration is done
	jm.logger.Info(fmt.Sprintf("Would send status update to ordering peer %s (implementation pending)", job.OrderingPeerID), "job_manager")
}

// SubmitJob creates and submits a job for execution
func (jm *JobManager) SubmitJob(request *types.JobExecutionRequest) (*database.JobExecution, error) {
	jm.logger.Info(fmt.Sprintf("Submitting job for workflow job %d", request.WorkflowJobID), "job_manager")

	// Create job execution
	entrypointJSON, _ := database.MarshalStringSlice(request.Entrypoint)
	commandsJSON, _ := database.MarshalStringSlice(request.Commands)

	job := &database.JobExecution{
		WorkflowJobID:       request.WorkflowJobID,
		ServiceID:           request.ServiceID,
		ExecutorPeerID:      jm.peerManager.GetPeerID(), // Current peer
		OrderingPeerID:      request.OrderingPeerID,
		Status:              types.JobStatusIdle,
		Entrypoint:          entrypointJSON,
		Commands:            commandsJSON,
		ExecutionConstraint: request.ExecutionConstraint,
	}

	err := jm.db.CreateJobExecution(job)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to create job execution: %v", err), "job_manager")
		return nil, err
	}

	// Create job interfaces
	for _, iface := range request.Interfaces {
		jobInterface := &database.JobInterface{
			JobExecutionID: job.ID,
			InterfaceType:  iface.Type,
			Path:           iface.Path,
		}

		err := jm.db.CreateJobInterface(jobInterface)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to create job interface: %v", err), "job_manager")
			continue
		}

		// Create interface peers
		for _, peer := range iface.InterfacePeers {
			jobInterfacePeer := &database.JobInterfacePeer{
				JobInterfaceID:    jobInterface.ID,
				PeerNodeID:        peer.PeerNodeID,
				PeerJobID:         peer.PeerJobID,
				PeerPath:          peer.PeerPath,
				PeerMountFunction: peer.PeerMountFunction,
				DutyAcknowledged:  peer.DutyAcknowledged,
			}

			err := jm.db.CreateJobInterfacePeer(jobInterfacePeer)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to create job interface peer: %v", err), "job_manager")
			}
		}
	}

	// Determine initial status based on execution constraint
	initialStatus := types.JobStatusReady
	if request.ExecutionConstraint == types.ExecutionConstraintInputsReady {
		// Check if inputs are ready
		ready, err := jm.db.ValidateJobInputsReady(job.ID)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to validate inputs for job %d: %v", job.ID, err), "job_manager")
			initialStatus = types.JobStatusIdle
		} else if !ready {
			initialStatus = types.JobStatusIdle
		}
	}

	// Update status
	err = jm.db.UpdateJobStatus(job.ID, initialStatus, "")
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job %d status to %s: %v", job.ID, initialStatus, err), "job_manager")
	}

	job.Status = initialStatus

	jm.logger.Info(fmt.Sprintf("Job %d created with status %s", job.ID, initialStatus), "job_manager")
	return job, nil
}

// CancelJob cancels a running job
func (jm *JobManager) CancelJob(jobExecutionID int64) error {
	jm.logger.Info(fmt.Sprintf("Cancelling job %d", jobExecutionID), "job_manager")

	// Update status to CANCELLED
	err := jm.db.UpdateJobStatus(jobExecutionID, types.JobStatusCancelled, "Cancelled by user")
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to cancel job %d: %v", jobExecutionID, err), "job_manager")
		return err
	}

	// Remove from running jobs
	jm.mu.Lock()
	delete(jm.runningJobs, jobExecutionID)
	jm.mu.Unlock()

	jm.logger.Info(fmt.Sprintf("Job %d cancelled successfully", jobExecutionID), "job_manager")
	return nil
}

// GetJobStatus returns the current status of a job
func (jm *JobManager) GetJobStatus(jobExecutionID int64) (string, error) {
	job, err := jm.db.GetJobExecution(jobExecutionID)
	if err != nil {
		return "", err
	}

	return job.Status, nil
}

// P2P Message Handlers

// HandleJobRequest handles incoming job execution requests from remote peers
func (jm *JobManager) HandleJobRequest(request *types.JobExecutionRequest, peerID string) (*types.JobExecutionResponse, error) {
	jm.logger.Info(fmt.Sprintf("Handling job request from peer %s for workflow job %d", peerID[:8], request.WorkflowJobID), "job_manager")

	// Set ordering peer ID to the requesting peer
	request.OrderingPeerID = peerID

	// Submit the job for execution
	job, err := jm.SubmitJob(request)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to submit job from peer %s: %v", peerID[:8], err), "job_manager")
		return &types.JobExecutionResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("Failed to submit job: %v", err),
		}, err
	}

	jm.logger.Info(fmt.Sprintf("Accepted job request from peer %s, job execution ID: %d", peerID[:8], job.ID), "job_manager")

	return &types.JobExecutionResponse{
		WorkflowJobID:  request.WorkflowJobID,
		JobExecutionID: job.ID,
		Accepted:       true,
		Message:        "Job accepted for execution",
	}, nil
}

// HandleJobStatusUpdate handles incoming job status updates from executor peers
func (jm *JobManager) HandleJobStatusUpdate(update *types.JobStatusUpdate, peerID string) error {
	jm.logger.Info(fmt.Sprintf("Handling status update from peer %s for job %d: %s", peerID[:8], update.JobExecutionID, update.Status), "job_manager")

	// Update job status in database
	err := jm.db.UpdateJobStatus(update.JobExecutionID, update.Status, update.ErrorMessage)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job %d status: %v", update.JobExecutionID, err), "job_manager")
		return err
	}

	// If job completed or errored, notify workflow manager
	if update.Status == types.JobStatusCompleted || update.Status == types.JobStatusErrored {
		jm.mu.Lock()
		delete(jm.runningJobs, update.JobExecutionID)
		jm.mu.Unlock()
	}

	jm.logger.Info(fmt.Sprintf("Updated job %d status to %s", update.JobExecutionID, update.Status), "job_manager")
	return nil
}

// HandleJobDataTransferRequest handles incoming data transfer requests
func (jm *JobManager) HandleJobDataTransferRequest(request *types.JobDataTransferRequest, peerID string) (*types.JobDataTransferResponse, error) {
	jm.logger.Info(fmt.Sprintf("Handling data transfer request from peer %s for job %d", peerID[:8], request.JobExecutionID), "job_manager")

	// Verify job exists
	_, err := jm.db.GetJobExecution(request.JobExecutionID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Job %d not found for data transfer: %v", request.JobExecutionID, err), "job_manager")
		return &types.JobDataTransferResponse{
			JobExecutionID: request.JobExecutionID,
			Accepted:       false,
			Message:        fmt.Sprintf("Job not found: %v", err),
		}, err
	}

	jm.logger.Info(fmt.Sprintf("Accepted data transfer request for job %d from peer %s (file: %s, size: %d bytes)",
		request.JobExecutionID, peerID[:8], request.SourcePath, request.SizeBytes), "job_manager")

	// Initialize transfer in data worker with proper metadata
	if jm.dataWorker != nil {
		// Calculate expected chunks
		chunkSize := jm.cm.GetConfigInt("job_data_chunk_size", 65536, 1024, 1048576)
		totalChunks := int((request.SizeBytes + int64(chunkSize) - 1) / int64(chunkSize))

		// Generate transfer ID (same algorithm as sender)
		transferID := jm.dataWorker.GenerateTransferID(request.JobExecutionID, peerID)

		// Initialize incoming transfer with metadata from request (including encryption info)
		err := jm.dataWorker.InitializeIncomingTransferWithPassphrase(
			transferID,
			request.JobExecutionID,
			peerID,
			request.DestinationPath,
			request.DataHash,
			request.SizeBytes,
			totalChunks,
			request.Passphrase,
			request.Encrypted,
		)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to initialize transfer: %v", err), "job_manager")
			return &types.JobDataTransferResponse{
				JobExecutionID: request.JobExecutionID,
				Accepted:       false,
				Message:        fmt.Sprintf("Failed to initialize transfer: %v", err),
			}, err
		}
	}

	return &types.JobDataTransferResponse{
		JobExecutionID: request.JobExecutionID,
		Accepted:       true,
		Message:        "Transfer request accepted",
	}, nil
}

// HandleJobDataChunk handles incoming data chunks
func (jm *JobManager) HandleJobDataChunk(chunk *types.JobDataChunk, peerID string) error {
	jm.logger.Debug(fmt.Sprintf("Handling data chunk from peer %s: chunk %d/%d for transfer %s",
		peerID[:8], chunk.ChunkIndex+1, chunk.TotalChunks, chunk.TransferID), "job_manager")

	// Delegate to data worker for actual file handling
	if jm.dataWorker != nil {
		return jm.dataWorker.HandleDataChunk(chunk, peerID)
	}

	jm.logger.Warn("Data worker not available to handle chunk", "job_manager")
	return fmt.Errorf("data worker not available")
}

// HandleJobDataTransferComplete handles transfer completion notifications
func (jm *JobManager) HandleJobDataTransferComplete(complete *types.JobDataTransferComplete, peerID string) error {
	jm.logger.Info(fmt.Sprintf("Handling data transfer complete from peer %s for transfer %s: success=%v",
		peerID[:8], complete.TransferID, complete.Success), "job_manager")

	if complete.Success {
		jm.logger.Info(fmt.Sprintf("Data transfer %s completed successfully (%d bytes)", complete.TransferID, complete.BytesTransferred), "job_manager")

		// Update data transfer record status
		// Note: We'd need to look up the record by transfer ID to update it properly
		// For now, just log the completion
	} else {
		jm.logger.Error(fmt.Sprintf("Data transfer %s failed: %s", complete.TransferID, complete.ErrorMessage), "job_manager")
	}

	return nil
}

// HandleJobCancel handles job cancellation requests
func (jm *JobManager) HandleJobCancel(request *types.JobCancelRequest, peerID string) (*types.JobCancelResponse, error) {
	jm.logger.Info(fmt.Sprintf("Handling job cancel request from peer %s for job %d", peerID[:8], request.JobExecutionID), "job_manager")

	// Cancel the job
	err := jm.CancelJob(request.JobExecutionID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to cancel job %d: %v", request.JobExecutionID, err), "job_manager")
		return &types.JobCancelResponse{
			JobExecutionID: request.JobExecutionID,
			Cancelled:      false,
			Message:        fmt.Sprintf("Failed to cancel job: %v", err),
		}, err
	}

	jm.logger.Info(fmt.Sprintf("Job %d cancelled by peer %s", request.JobExecutionID, peerID[:8]), "job_manager")

	return &types.JobCancelResponse{
		JobExecutionID: request.JobExecutionID,
		Cancelled:      true,
		Message:        "Job cancelled successfully",
	}, nil
}
