package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// Start data service worker monitoring
	if jm.dataWorker != nil {
		jm.dataWorker.Start()
	}

	// Start status update worker
	jm.startStatusUpdateWorker()

	// Start job queue processor
	jm.startJobQueueProcessor()

	// Start input readiness checker for IDLE jobs with INPUTS_READY constraint
	jm.startInputReadinessChecker()

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

	localPeerID := jm.peerManager.GetPeerID()

	for _, job := range jobs {
		// Check if job should be executed on a remote peer
		if job.ExecutorPeerID != localPeerID {
			jm.logger.Info(fmt.Sprintf("Job %d should be executed on peer %s (not local peer %s), sending to remote peer",
				job.ID, job.ExecutorPeerID[:8], localPeerID[:8]), "job_manager")

			// Send job to remote peer for execution
			go jm.sendJobToRemotePeer(job)
			continue
		}

		// Check if job is already running locally
		jm.mu.RLock()
		isRunning := jm.runningJobs[job.ID]
		jm.mu.RUnlock()

		if isRunning {
			jm.logger.Info(fmt.Sprintf("Job %d is already running, skipping", job.ID), "job_manager")
			continue
		}

		// Validate execution constraints (check file system for inputs)
		if job.ExecutionConstraint == types.ExecutionConstraintInputsReady {
			ready, err := jm.checkInputsReady(job)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to check inputs for job %d: %v", job.ID, err), "job_manager")
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

	// Don't send status updates to ourselves
	if job.OrderingPeerID == jm.peerManager.GetPeerID() {
		jm.logger.Debug(fmt.Sprintf("Job %d ordered by local peer, skipping status update send", update.JobExecutionID), "job_manager")
		return
	}

	// Send status update to ordering peer via P2P
	jm.logger.Info(fmt.Sprintf("Sending status update to ordering peer %s for job %d: %s",
		job.OrderingPeerID[:8], update.JobExecutionID, update.Status), "job_manager")

	jobHandler := jm.peerManager.GetJobHandler()
	if jobHandler == nil {
		jm.logger.Error("Job handler not available for sending status update", "job_manager")
		return
	}

	err = jobHandler.SendJobStatusUpdate(job.OrderingPeerID, update)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to send status update to peer %s: %v",
			job.OrderingPeerID[:8], err), "job_manager")
	} else {
		jm.logger.Info(fmt.Sprintf("Successfully sent status update to peer %s", job.OrderingPeerID[:8]), "job_manager")
	}
}

// SubmitJob creates and submits a job for execution
func (jm *JobManager) SubmitJob(request *types.JobExecutionRequest) (*database.JobExecution, error) {
	jm.logger.Info(fmt.Sprintf("Submitting job for workflow job %d", request.WorkflowJobID), "job_manager")

	// Application-level validation for workflow_job reference
	// Note: workflow_job_id is a soft reference (no FK) to support distributed P2P execution.
	// For local workflows, we validate the reference exists. For remote workflows (received
	// from other peers), we accept that workflow_job may not exist in our local database.
	isLocalWorkflow := request.OrderingPeerID == jm.peerManager.GetPeerID()
	isLocalExecution := request.ExecutorPeerID == jm.peerManager.GetPeerID()

	if isLocalWorkflow {
		// Validate workflow_job exists for local workflows
		_, err := jm.db.GetWorkflowJobs(request.WorkflowID)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Workflow job %d not found for local workflow: %v",
				request.WorkflowJobID, err), "job_manager")
			return nil, fmt.Errorf("workflow job not found: %v", err)
		}
		jm.logger.Debug(fmt.Sprintf("Validated workflow job %d exists locally", request.WorkflowJobID), "job_manager")
	} else {
		// Remote workflow - workflow_job may not exist locally (by design)
		jm.logger.Debug(fmt.Sprintf("Accepting remote workflow job %d from peer %s (soft reference)",
			request.WorkflowJobID, request.OrderingPeerID[:8]), "job_manager")
	}

	// Validate that the requested service exists - but only for local execution
	// For remote execution, the service exists on the remote peer, not locally
	if isLocalExecution {
		service, svcErr := jm.db.GetService(request.ServiceID)
		if svcErr != nil || service == nil {
			jm.logger.Error(fmt.Sprintf("Service ID %d not found on local executor peer", request.ServiceID), "job_manager")
			return nil, fmt.Errorf("service ID %d not found on executor peer", request.ServiceID)
		}
		jm.logger.Info(fmt.Sprintf("Validated service '%s' (ID: %d) exists locally for execution",
			service.Name, service.ID), "job_manager")
	} else {
		jm.logger.Info(fmt.Sprintf("Job will execute on remote peer %s (service ID %d validation skipped)",
			request.ExecutorPeerID[:8], request.ServiceID), "job_manager")
	}

	// Create job execution
	entrypointJSON, _ := database.MarshalStringSlice(request.Entrypoint)
	commandsJSON, _ := database.MarshalStringSlice(request.Commands)

	job := &database.JobExecution{
		WorkflowJobID:       request.WorkflowJobID,
		ServiceID:           request.ServiceID,
		ExecutorPeerID:      request.ExecutorPeerID, // Peer that should execute this job
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
		// Check if inputs are ready on file system
		ready, err := jm.checkInputsReady(job)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to check inputs for job %d: %v", job.ID, err), "job_manager")
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

// HandleJobStatusRequest handles incoming job status requests
func (jm *JobManager) HandleJobStatusRequest(request *types.JobStatusRequest, peerID string) (*types.JobStatusResponse, error) {
	jm.logger.Debug(fmt.Sprintf("Handling status request from peer %s for job %d", peerID[:8], request.JobExecutionID), "job_manager")

	// Get job from database
	job, err := jm.db.GetJobExecution(request.JobExecutionID)
	if err != nil || job == nil {
		jm.logger.Warn(fmt.Sprintf("Job %d not found for status request from peer %s", request.JobExecutionID, peerID[:8]), "job_manager")
		return &types.JobStatusResponse{
			JobExecutionID: request.JobExecutionID,
			WorkflowJobID:  request.WorkflowJobID,
			Found:          false,
		}, nil
	}

	// Return current job status
	response := &types.JobStatusResponse{
		JobExecutionID: job.ID,
		WorkflowJobID:  job.WorkflowJobID,
		Status:         job.Status,
		ErrorMessage:   job.ErrorMessage,
		UpdatedAt:      job.UpdatedAt,
		Found:          true,
	}

	jm.logger.Debug(fmt.Sprintf("Returning status for job %d to peer %s: %s", request.JobExecutionID, peerID[:8], job.Status), "job_manager")
	return response, nil
}

// HandleJobDataTransferRequest handles incoming data transfer requests
func (jm *JobManager) HandleJobDataTransferRequest(request *types.JobDataTransferRequest, peerID string) (*types.JobDataTransferResponse, error) {
	jm.logger.Info(fmt.Sprintf("Handling data transfer request from peer %s for workflow job %d", peerID[:8], request.WorkflowJobID), "job_manager")

	// Look up job by workflow_job_id and executor_peer_id (the source is the DATA service executor)
	jobs, err := jm.db.GetJobExecutionsByWorkflowJob(request.WorkflowJobID)
	if err != nil || len(jobs) == 0 {
		jm.logger.Error(fmt.Sprintf("Workflow job %d not found for data transfer: %v", request.WorkflowJobID, err), "job_manager")
		return &types.JobDataTransferResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("Workflow job not found: %v", err),
		}, err
	}

	// Find the job execution where the source peer is the executor (DATA service sender)
	var job *database.JobExecution
	for _, j := range jobs {
		if j.ExecutorPeerID == request.SourcePeerID {
			job = j
			break
		}
	}

	if job == nil {
		jm.logger.Error(fmt.Sprintf("No job execution found for workflow job %d from executor peer %s", request.WorkflowJobID, request.SourcePeerID[:8]), "job_manager")
		return &types.JobDataTransferResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       "Job execution not found for this executor peer",
		}, fmt.Errorf("job execution not found")
	}

	// Construct hierarchical destination path: workflows/{ordering_peer_id}/{workflow_job_id}/jobs/{job_id}/{template}
	// This follows the libp2p distributed P2P pattern for clear file organization
	appPaths := utils.GetAppPaths("remote-network")
	hierarchicalPath := filepath.Join(
		appPaths.DataDir,
		"workflows",
		job.OrderingPeerID, // Full peer ID to prevent collisions
		fmt.Sprintf("%d", job.WorkflowJobID),
		"jobs",
		fmt.Sprintf("%d", job.ID),
		request.DestinationPath, // e.g., "input/" or "output/"
	)

	// Preserve directory indicator - filepath.Join() removes trailing separators
	// Re-add it if the template indicated a directory
	if strings.HasSuffix(request.DestinationPath, string(os.PathSeparator)) && !strings.HasSuffix(hierarchicalPath, string(os.PathSeparator)) {
		hierarchicalPath += string(os.PathSeparator)
	}

	jm.logger.Info(fmt.Sprintf("Accepted data transfer request for workflow job %d (local job %d) from peer %s (file: %s, size: %d bytes)",
		request.WorkflowJobID, job.ID, peerID[:8], request.SourcePath, request.SizeBytes), "job_manager")
	jm.logger.Info(fmt.Sprintf("Resolved destination path template '%s' to: %s",
		request.DestinationPath, hierarchicalPath), "job_manager")

	// Initialize transfer in data worker with proper metadata
	if jm.dataWorker != nil {
		// Calculate expected chunks
		chunkSize := jm.cm.GetConfigInt("job_data_chunk_size", 1048576, 1024, 10485760)
		totalChunks := int((request.SizeBytes + int64(chunkSize) - 1) / int64(chunkSize))

		// Use transfer ID from request (sender generates it)
		transferID := request.TransferID

		// Initialize incoming transfer with hierarchical path
		err := jm.dataWorker.InitializeIncomingTransferWithPassphrase(
			transferID,
			job.ID, // Use local job execution ID
			peerID,
			hierarchicalPath, // Use hierarchical path instead of template
			request.DataHash,
			request.SizeBytes,
			totalChunks,
			request.Passphrase,
			request.Encrypted,
		)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to initialize transfer: %v", err), "job_manager")
			return &types.JobDataTransferResponse{
				WorkflowJobID: request.WorkflowJobID,
				Accepted:      false,
				Message:       fmt.Sprintf("Failed to initialize transfer: %v", err),
			}, err
		}
	}

	return &types.JobDataTransferResponse{
		WorkflowJobID: request.WorkflowJobID,
		Accepted:      true,
		Message:       "Transfer request accepted",
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

// ensurePeerConnection ensures a connection to the peer exists
func (jm *JobManager) ensurePeerConnection(peerID string) error {
	quicPeer := jm.peerManager.GetQUICPeer()
	if quicPeer == nil {
		return fmt.Errorf("QUIC peer not available")
	}

	// Check if connection already exists
	_, err := quicPeer.GetConnectionByPeerID(peerID)
	if err == nil {
		jm.logger.Info(fmt.Sprintf("Using existing connection to peer %s", peerID[:8]), "job_manager")
		return nil // Connection exists
	}

	jm.logger.Info(fmt.Sprintf("No existing connection to peer %s, establishing new connection", peerID[:8]), "job_manager")

	// Get peer from known peers
	peer, err := jm.db.KnownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil {
		return fmt.Errorf("peer %s not found in known peers", peerID[:8])
	}

	// Query metadata to get connection info
	metadataQuery := jm.peerManager.GetMetadataQueryService()
	if metadataQuery == nil {
		return fmt.Errorf("metadata query service not available")
	}

	metadata, err := metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to query peer metadata: %v", err)
	}

	// For public peers, connect directly
	if !metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.PublicIP != "" {
		peerAddr := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
		jm.logger.Info(fmt.Sprintf("Connecting to public peer %s at %s", peerID[:8], peerAddr), "job_manager")

		_, err = quicPeer.ConnectToPeer(peerAddr)
		if err != nil {
			return fmt.Errorf("failed to connect to %s: %v", peerAddr, err)
		}

		jm.logger.Info(fmt.Sprintf("Successfully connected to peer %s", peerID[:8]), "job_manager")
		return nil
	}

	// For NAT/relay peers, the connection will be handled via relay by SendMessageWithResponse
	jm.logger.Info(fmt.Sprintf("Peer %s is behind NAT/using relay - connection will be established via relay", peerID[:8]), "job_manager")
	return nil
}

// sendJobToRemotePeer sends a job execution request to a remote peer
func (jm *JobManager) sendJobToRemotePeer(job *database.JobExecution) {
	jm.logger.Info(fmt.Sprintf("Sending job %d to remote peer %s for execution", job.ID, job.ExecutorPeerID[:8]), "job_manager")

	// Ensure connection to peer exists
	if err := jm.ensurePeerConnection(job.ExecutorPeerID); err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to establish connection to peer %s: %v", job.ExecutorPeerID[:8], err), "job_manager")
		jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, fmt.Sprintf("Failed to connect to executor peer: %v", err))
		return
	}

	// Get job handler
	jobHandler := jm.peerManager.GetJobHandler()
	if jobHandler == nil {
		jm.logger.Error(fmt.Sprintf("Job handler not available, cannot send job %d to remote peer", job.ID), "job_manager")
		// Update job status to ERRORED
		jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, "Job handler not available")
		return
	}

	// Get job interfaces
	interfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get interfaces for job %d: %v", job.ID, err), "job_manager")
		jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, fmt.Sprintf("Failed to get interfaces: %v", err))
		return
	}

	// Convert database interfaces to types.JobInterface
	typeInterfaces := make([]*types.JobInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		// Get interface peers
		peers, err := jm.db.GetJobInterfacePeers(iface.ID)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to get interface peers for interface %d: %v", iface.ID, err), "job_manager")
			continue
		}

		// Convert peers
		typePeers := make([]*types.InterfacePeer, 0, len(peers))
		for _, peer := range peers {
			typePeers = append(typePeers, &types.InterfacePeer{
				PeerNodeID:        peer.PeerNodeID,
				PeerJobID:         peer.PeerJobID,
				PeerPath:          peer.PeerPath,
				PeerMountFunction: peer.PeerMountFunction,
				DutyAcknowledged:  peer.DutyAcknowledged,
			})
		}

		typeInterfaces = append(typeInterfaces, &types.JobInterface{
			Type:           iface.InterfaceType,
			Path:           iface.Path,
			InterfacePeers: typePeers,
		})
	}

	// Parse entrypoint and commands
	entrypoint, _ := database.UnmarshalStringSlice(job.Entrypoint)
	commands, _ := database.UnmarshalStringSlice(job.Commands)

	// Create job execution request
	request := &types.JobExecutionRequest{
		WorkflowID:          job.WorkflowJobID, // Note: This is actually workflow_job_id
		WorkflowJobID:       job.WorkflowJobID,
		JobName:             fmt.Sprintf("job-%d", job.ID),
		ServiceID:           job.ServiceID,
		ServiceType:         "", // TODO: Get from service
		ExecutorPeerID:      job.ExecutorPeerID,
		Entrypoint:          entrypoint,
		Commands:            commands,
		ExecutionConstraint: job.ExecutionConstraint,
		Interfaces:          typeInterfaces,
		OrderingPeerID:      job.OrderingPeerID,
		RequestedAt:         time.Now(),
	}

	// Send request to remote peer
	jm.logger.Info(fmt.Sprintf("Sending job execution request for job %d to peer %s", job.ID, job.ExecutorPeerID[:8]), "job_manager")
	response, err := jobHandler.SendJobRequest(job.ExecutorPeerID, request)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to send job %d to peer %s: %v", job.ID, job.ExecutorPeerID[:8], err), "job_manager")
		jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, fmt.Sprintf("Failed to send to remote peer: %v", err))
		return
	}

	if !response.Accepted {
		jm.logger.Error(fmt.Sprintf("Job %d rejected by peer %s: %s", job.ID, job.ExecutorPeerID[:8], response.Message), "job_manager")
		jm.db.UpdateJobStatus(job.ID, types.JobStatusErrored, fmt.Sprintf("Rejected by remote peer: %s", response.Message))
		return
	}

	jm.logger.Info(fmt.Sprintf("Job %d accepted by remote peer %s (remote job_execution_id: %d)",
		job.ID, job.ExecutorPeerID[:8], response.JobExecutionID), "job_manager")

	// Update job status to RUNNING (remote peer will send status updates)
	jm.db.UpdateJobStatus(job.ID, types.JobStatusRunning, "")
}

// checkInputsReady checks if all expected input files exist on the file system
// Following the libp2p data path pattern: /workflows/<ordering_peer>/<workflow_id>/job/<job_id>/input/<sender_peer>/
func (jm *JobManager) checkInputsReady(job *database.JobExecution) (bool, error) {
	// Get job interfaces
	interfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		return false, fmt.Errorf("failed to get job interfaces: %v", err)
	}

	// For each STDIN interface, check if data exists from all provider peers
	for _, iface := range interfaces {
		if iface.InterfaceType != "STDIN" {
			continue
		}

		// Get interface peers
		peers, err := jm.db.GetJobInterfacePeers(iface.ID)
		if err != nil {
			return false, fmt.Errorf("failed to get interface peers: %v", err)
		}

		// Check if data exists from all provider peers
		for _, peer := range peers {
			if peer.PeerMountFunction != "PROVIDER" {
				continue
			}

			// Build expected input data path
			// /workflows/<ordering_peer>/<workflow_id>/job/<job_id>/input/<sender_peer>/
			localStoragePath := jm.cm.GetConfigWithDefault("local_storage", "./local_storage/")
			inputPath := fmt.Sprintf("%s/workflows/%s/%d/job/%d/input/%s",
				localStoragePath,
				job.OrderingPeerID,
				job.WorkflowJobID, // workflow ID
				job.ID,            // job execution ID
				peer.PeerNodeID)

			// Check if directory exists and has content
			exists, err := utils.PathExistsWithContent(inputPath)
			if err != nil {
				return false, fmt.Errorf("failed to check input path %s: %v", inputPath, err)
			}

			if !exists {
				jm.logger.Debug(fmt.Sprintf("Job %d input not ready: waiting for data from peer %s at %s",
					job.ID, peer.PeerNodeID[:8], inputPath), "job_manager")
				return false, nil
			}

			jm.logger.Debug(fmt.Sprintf("Job %d found input from peer %s at %s",
				job.ID, peer.PeerNodeID[:8], inputPath), "job_manager")
		}
	}

	jm.logger.Info(fmt.Sprintf("Job %d: all inputs ready", job.ID), "job_manager")
	return true, nil
}

// startInputReadinessChecker starts a periodic checker for IDLE jobs with INPUTS_READY constraint
func (jm *JobManager) startInputReadinessChecker() {
	jm.wg.Add(1)
	go func() {
		defer jm.wg.Done()

		ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
		defer ticker.Stop()

		jm.logger.Info("Started input readiness checker", "job_manager")

		for {
			select {
			case <-ticker.C:
				jm.checkIdleJobsForReadiness()
			case <-jm.ctx.Done():
				jm.logger.Info("Input readiness checker stopped", "job_manager")
				return
			}
		}
	}()
}

// checkIdleJobsForReadiness checks IDLE jobs with INPUTS_READY constraint to see if they can transition to READY
func (jm *JobManager) checkIdleJobsForReadiness() {
	// Get all IDLE jobs with INPUTS_READY constraint
	jobs, err := jm.db.GetJobsByStatus(types.JobStatusIdle)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get IDLE jobs: %v", err), "job_manager")
		return
	}

	for _, job := range jobs {
		// Only check jobs with INPUTS_READY constraint
		if job.ExecutionConstraint != types.ExecutionConstraintInputsReady {
			continue
		}

		// Check if inputs are ready
		ready, err := jm.checkInputsReady(job)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to check inputs for job %d: %v", job.ID, err), "job_manager")
			continue
		}

		if ready {
			// Transition to READY status
			err := jm.db.UpdateJobStatus(job.ID, types.JobStatusReady, "")
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to update job %d status to READY: %v", job.ID, err), "job_manager")
			} else {
				jm.logger.Info(fmt.Sprintf("Job %d transitioned from IDLE to READY (inputs ready)", job.ID), "job_manager")
			}
		}
	}
}
