package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// WorkflowManager manages workflow lifecycle and orchestration
type WorkflowManager struct {
	ctx             context.Context
	cancel          context.CancelFunc
	db              *database.SQLiteManager
	cm              *utils.ConfigManager
	logger          *utils.LogsManager
	jobManager      *JobManager
	peerManager     *PeerManager
	eventEmitter    EventEmitter // Interface for broadcasting events via WebSocket
	activeWorkflows map[int64]*WorkflowExecution // workflow_job_id -> execution
	mu              sync.RWMutex
	wg              sync.WaitGroup
}

// WorkflowExecution represents an active workflow execution
type WorkflowExecution struct {
	WorkflowExecutionID int64
	WorkflowID          int64
	Definition          *types.WorkflowDefinition
	WorkflowJobs        map[int64]*database.WorkflowJob   // workflow_job_id -> WorkflowJob
	JobExecutions       map[string]*database.JobExecution // job_name -> execution (for local jobs)
	CompletedJobs       int
	Status              string // pending, running, completed, failed, cancelled
	StartedAt           time.Time
	CompletedAt         time.Time
	ErrorMessage        string
	mu                  sync.RWMutex
}

// NewWorkflowManager creates a new workflow manager
func NewWorkflowManager(ctx context.Context, db *database.SQLiteManager, cm *utils.ConfigManager, jobManager *JobManager, peerManager *PeerManager) *WorkflowManager {
	wmCtx, cancel := context.WithCancel(ctx)

	return &WorkflowManager{
		ctx:             wmCtx,
		cancel:          cancel,
		db:              db,
		cm:              cm,
		logger:          utils.NewLogsManager(cm),
		jobManager:      jobManager,
		peerManager:     peerManager,
		activeWorkflows: make(map[int64]*WorkflowExecution),
	}
}

// SetEventEmitter sets the event emitter for broadcasting updates
func (wm *WorkflowManager) SetEventEmitter(emitter EventEmitter) {
	wm.eventEmitter = emitter
}

// Start starts the workflow manager
func (wm *WorkflowManager) Start() error {
	wm.logger.Info("Starting Workflow Manager", "workflow_manager")

	// Recover running workflows from database (in case of restart)
	if err := wm.recoverRunningWorkflows(); err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to recover running workflows: %v", err), "workflow_manager")
		// Continue anyway - this is not fatal
	}

	// Start periodic workflow status updater
	wm.startWorkflowStatusUpdater()

	wm.logger.Info("Workflow Manager started successfully", "workflow_manager")
	return nil
}

// recoverRunningWorkflows recovers running workflows from database after restart
func (wm *WorkflowManager) recoverRunningWorkflows() error {
	wm.logger.Info("Recovering running workflows from database", "workflow_manager")

	// Query database for workflow_jobs with status='running'
	rows, err := wm.db.GetDB().Query(`
		SELECT id, workflow_id, status, created_at, updated_at
		FROM workflow_jobs
		WHERE status = 'running'
	`)
	if err != nil {
		return fmt.Errorf("failed to query running workflows: %v", err)
	}
	defer rows.Close()

	recoveredCount := 0
	for rows.Next() {
		var workflowJobID, workflowID int64
		var status, createdAt, updatedAt string

		if err := rows.Scan(&workflowJobID, &workflowID, &status, &createdAt, &updatedAt); err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to scan workflow job row: %v", err), "workflow_manager")
			continue
		}

		// Get workflow definition
		workflow, err := wm.db.GetWorkflowByID(workflowID)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to get workflow %d: %v", workflowID, err), "workflow_manager")
			continue
		}

		workflowDef, err := wm.parseWorkflowDefinition(workflow)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to parse workflow %d definition: %v", workflowID, err), "workflow_manager")
			continue
		}

		// Create workflow execution tracker
		execution := &WorkflowExecution{
			WorkflowExecutionID: workflowJobID, // workflowJobID is actually execution ID here
			WorkflowID:          workflowID,
			Definition:          workflowDef,
			WorkflowJobs:        make(map[int64]*database.WorkflowJob),
			JobExecutions:       make(map[string]*database.JobExecution),
			Status:              status,
			StartedAt:           time.Now(), // We don't have exact start time, use current time
		}

		// Load all job executions for this workflow from database
		jobRows, err := wm.db.GetDB().Query(`
			SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
				   status, entrypoint, commands, execution_constraint, constraint_detail,
				   started_at, ended_at, error_message, created_at, updated_at
			FROM job_executions
			WHERE workflow_job_id = ?
		`, workflowJobID)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to query job executions for workflow job %d: %v", workflowJobID, err), "workflow_manager")
			continue
		}

		jobCount := 0
		for jobRows.Next() {
			var job database.JobExecution
			var startedAt, endedAt, createdAt, updatedAt sql.NullString

			err := jobRows.Scan(
				&job.ID, &job.WorkflowJobID, &job.ServiceID, &job.ExecutorPeerID, &job.OrderingPeerID,
				&job.Status, &job.Entrypoint, &job.Commands, &job.ExecutionConstraint, &job.ConstraintDetail,
				&startedAt, &endedAt, &job.ErrorMessage, &createdAt, &updatedAt,
			)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to scan job execution: %v", err), "workflow_manager")
				continue
			}

			// Find the job name from workflow definition
			// For now, use a generic name based on service_id (we can improve this later)
			jobName := fmt.Sprintf("job_%d", job.ID)
			execution.JobExecutions[jobName] = &job
			jobCount++
		}
		jobRows.Close()

		// Add to active workflows
		wm.mu.Lock()
		wm.activeWorkflows[workflowJobID] = execution
		wm.mu.Unlock()

		wm.logger.Info(fmt.Sprintf("Recovered workflow job %d (workflow %d) with %d job executions",
			workflowJobID, workflowID, jobCount), "workflow_manager")
		recoveredCount++
	}

	if recoveredCount > 0 {
		wm.logger.Info(fmt.Sprintf("Recovered %d running workflows from database", recoveredCount), "workflow_manager")
	} else {
		wm.logger.Info("No running workflows to recover", "workflow_manager")
	}

	return nil
}

// Stop stops the workflow manager
func (wm *WorkflowManager) Stop() {
	wm.logger.Info("Stopping Workflow Manager", "workflow_manager")

	// Cancel context
	wm.cancel()

	// Wait for all goroutines
	wm.wg.Wait()

	wm.logger.Info("Workflow Manager stopped", "workflow_manager")
	wm.logger.Close()
}

// ExecuteWorkflow starts execution of a workflow
func (wm *WorkflowManager) ExecuteWorkflow(workflowID int64) (*database.WorkflowJob, error) {
	wm.logger.Info(fmt.Sprintf("Executing workflow %d", workflowID), "workflow_manager")

	// Get workflow definition
	workflow, err := wm.db.GetWorkflowByID(workflowID)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to get workflow %d: %v", workflowID, err), "workflow_manager")
		return nil, fmt.Errorf("failed to get workflow: %v", err)
	}

	// Parse workflow definition
	workflowDef, err := wm.parseWorkflowDefinition(workflow)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to parse workflow definition: %v", err), "workflow_manager")
		return nil, fmt.Errorf("failed to parse workflow definition: %v", err)
	}

	// Validate workflow
	if err := wm.validateWorkflow(workflowDef); err != nil {
		wm.logger.Error(fmt.Sprintf("Workflow validation failed: %v", err), "workflow_manager")
		return nil, fmt.Errorf("workflow validation failed: %v", err)
	}

	// Create workflow execution instance
	workflowExecution, err := wm.db.CreateWorkflowExecution(workflowID)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to create workflow execution: %v", err), "workflow_manager")
		return nil, fmt.Errorf("failed to create workflow execution: %v", err)
	}

	// Create workflow execution tracker
	execution := &WorkflowExecution{
		WorkflowExecutionID: workflowExecution.ID,
		WorkflowID:          workflowID,
		Definition:          workflowDef,
		WorkflowJobs:        make(map[int64]*database.WorkflowJob), // workflow_job_id -> WorkflowJob
		JobExecutions:       make(map[string]*database.JobExecution),
		Status:              "PENDING",
		StartedAt:           time.Now(),
	}

	// Register execution
	wm.mu.Lock()
	wm.activeWorkflows[workflowExecution.ID] = execution
	wm.mu.Unlock()

	// Update workflow execution status to running
	err = wm.db.UpdateWorkflowExecutionStatus(workflowExecution.ID, "RUNNING", "")
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to update workflow execution status: %v", err), "workflow_manager")
	}

	// Broadcast execution status update via WebSocket
	if wm.eventEmitter != nil {
		wm.eventEmitter.BroadcastExecutionUpdate(workflowExecution.ID)
	}

	// Broadcast job execution requests (non-blocking)
	wm.executeWorkflowJobs(execution)

	wm.logger.Info(fmt.Sprintf("Workflow %d execution requests sent (execution_id: %d). Status will be updated via periodic polling.", workflowID, workflowExecution.ID), "workflow_manager")

	// Return a compatible structure (for backward compatibility with API)
	compatJob := &database.WorkflowJob{
		ID:                  workflowExecution.ID, // Use execution ID as job ID for now
		WorkflowExecutionID: workflowExecution.ID,
		WorkflowID:          workflowID,
		Status:              "RUNNING",
		CreatedAt:           workflowExecution.CreatedAt,
		UpdatedAt:           workflowExecution.UpdatedAt,
	}
	return compatJob, nil
}

// executeWorkflowJobs creates workflow jobs and initiates execution
// NEW ARCHITECTURE:
// - Phase 1: Create workflow_jobs entries (one per job definition)
// - Phase 2: For local jobs, create job_executions; for remote jobs, send requests
// - Phase 3: Create interfaces with proper workflow_job_id to job_execution_id mapping
func (wm *WorkflowManager) executeWorkflowJobs(execution *WorkflowExecution) {
	wm.logger.Info(fmt.Sprintf("Broadcasting job execution requests for workflow execution %d", execution.WorkflowExecutionID), "workflow_manager")

	execution.mu.Lock()
	execution.Status = "RUNNING"
	execution.mu.Unlock()

	localPeerID := wm.peerManager.GetPeerID()

	// ============== PHASE 1: Create workflow_jobs (one per job definition) ==============
	wm.logger.Info("Phase 1: Creating workflow_jobs entries", "workflow_manager")

	for _, jobDef := range execution.Definition.Jobs {
		wm.logger.Info(fmt.Sprintf("Creating workflow_job for '%s' (node_id: %d, executor: %s)",
			jobDef.JobName, jobDef.NodeID, jobDef.ExecutorPeerID[:8]), "workflow_manager")

		// Create workflow_job entry
		workflowJob, err := wm.db.CreateWorkflowJob(
			execution.WorkflowExecutionID,
			execution.WorkflowID,
			jobDef.NodeID,
			jobDef.JobName,
			jobDef.ServiceID,
			jobDef.ExecutorPeerID,
		)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to create workflow_job for '%s': %v", jobDef.JobName, err), "workflow_manager")
			continue
		}

		// Track workflow_job
		execution.mu.Lock()
		execution.WorkflowJobs[workflowJob.ID] = workflowJob
		execution.mu.Unlock()

		wm.logger.Info(fmt.Sprintf("Created workflow_job ID=%d for '%s' (node_id=%d)",
			workflowJob.ID, jobDef.JobName, jobDef.NodeID), "workflow_manager")
	}

	// ============== PHASE 2: Create job_executions (local) or send requests (remote) ==============
	wm.logger.Info("Phase 2: Initiating job executions (local) or sending job requests (remote)", "workflow_manager")

	// Build node_id to workflow_job_id mapping for interface creation
	nodeIDToWorkflowJobIDMap := make(map[int64]int64)
	for _, wfJob := range execution.WorkflowJobs {
		nodeIDToWorkflowJobIDMap[wfJob.NodeID] = wfJob.ID
	}

	for _, jobDef := range execution.Definition.Jobs {
		workflowJobID := nodeIDToWorkflowJobIDMap[jobDef.NodeID]
		workflowJob := execution.WorkflowJobs[workflowJobID]

		if workflowJob == nil {
			wm.logger.Error(fmt.Sprintf("Workflow job not found for node_id=%d, job='%s' - skipping",
				jobDef.NodeID, jobDef.JobName), "workflow_manager")
			continue
		}

		if jobDef.ExecutorPeerID == localPeerID {
			// LOCAL JOB: Create job_execution on this peer
			wm.logger.Info(fmt.Sprintf("Creating LOCAL job_execution for workflow_job %d ('%s')",
				workflowJob.ID, jobDef.JobName), "workflow_manager")

			jobExec, err := wm.createLocalJobExecution(workflowJob, jobDef)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to create local job_execution: %v", err), "workflow_manager")
				continue
			}

			// Store job_execution_id in workflow_job (database)
			err = wm.db.UpdateWorkflowJobRemoteExecutionID(workflowJob.ID, jobExec.ID)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to update workflow_job with job_execution_id: %v", err), "workflow_manager")
			}

			// Update in-memory workflow_job with job_execution_id
			execution.mu.Lock()
			workflowJob.RemoteJobExecutionID = &jobExec.ID
			execution.WorkflowJobs[workflowJob.ID] = workflowJob
			execution.JobExecutions[jobDef.JobName] = jobExec
			execution.mu.Unlock()

		} else {
			// REMOTE JOB: Send job request to remote peer
			wm.logger.Info(fmt.Sprintf("Sending REMOTE job request for workflow_job %d ('%s') to peer %s",
				workflowJob.ID, jobDef.JobName, jobDef.ExecutorPeerID[:8]), "workflow_manager")

			remoteJobExecID, err := wm.sendRemoteJobRequest(workflowJob, jobDef, nodeIDToWorkflowJobIDMap)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to send remote job request: %v", err), "workflow_manager")
				continue
			}

			// Update in-memory workflow_job with remote job_execution_id
			execution.mu.Lock()
			workflowJob.RemoteJobExecutionID = &remoteJobExecID
			execution.WorkflowJobs[workflowJob.ID] = workflowJob
			execution.mu.Unlock()
		}
	}

	wm.logger.Info(fmt.Sprintf("All job execution requests sent for workflow execution %d",
		execution.WorkflowExecutionID), "workflow_manager")

	// ============== PHASE 3: Create interfaces with proper ID mapping ==============
	wm.logger.Info("Phase 3: Creating job interfaces with proper workflow_job_id -> job_execution_id mapping", "workflow_manager")

	// Build nodeID to job_execution_id mapping
	// This maps workflow definition node_id to the actual job_execution_id
	nodeIDToJobExecutionIDMap := make(map[int64]int64)

	// Collect job_execution_ids from workflow_jobs
	// For local jobs, we already have the job_execution_id
	// For remote jobs, we need to fetch them from workflow_job.remote_job_execution_id
	for nodeID, wfJobID := range nodeIDToWorkflowJobIDMap {
		wfJob := execution.WorkflowJobs[wfJobID]

		// Get the job_execution_id from workflow_job
		// For local jobs, we stored it in remote_job_execution_id in Phase 2
		// For remote jobs, we also stored it in remote_job_execution_id in Phase 2
		if wfJob.RemoteJobExecutionID != nil {
			nodeIDToJobExecutionIDMap[nodeID] = *wfJob.RemoteJobExecutionID
			wm.logger.Debug(fmt.Sprintf("Mapped node_id=%d -> job_execution_id=%d (from workflow_job %d)",
				nodeID, *wfJob.RemoteJobExecutionID, wfJob.ID), "workflow_manager")
		} else {
			wm.logger.Warn(fmt.Sprintf("Workflow job %d (node_id=%d) has no job_execution_id - skipping interface creation",
				wfJob.ID, nodeID), "workflow_manager")
		}
	}

	// Now create interfaces for all jobs with proper ID mapping
	// NOTE: Only create interfaces for LOCAL jobs. Remote jobs will create their own interfaces
	// on the executor peer when they process the job request.
	for _, jobDef := range execution.Definition.Jobs {
		workflowJob := execution.WorkflowJobs[nodeIDToWorkflowJobIDMap[jobDef.NodeID]]

		// Skip if no job_execution_id
		if workflowJob.RemoteJobExecutionID == nil {
			wm.logger.Warn(fmt.Sprintf("Skipping interface creation for workflow_job %d - no job_execution_id",
				workflowJob.ID), "workflow_manager")
			continue
		}

		jobExecutionID := *workflowJob.RemoteJobExecutionID

		// Only create interfaces for LOCAL jobs
		// Remote jobs will create interfaces on their executor peer
		if jobDef.ExecutorPeerID != localPeerID {
			wm.logger.Info(fmt.Sprintf("Skipping local interface creation for remote job_execution %d ('%s') - interfaces will be created on executor peer %s",
				jobExecutionID, jobDef.JobName, jobDef.ExecutorPeerID[:8]), "workflow_manager")
			continue
		}

		// Get the job_execution record for LOCAL jobs
		execution.mu.RLock()
		jobExec := execution.JobExecutions[jobDef.JobName]
		execution.mu.RUnlock()

		if jobExec == nil {
			wm.logger.Error(fmt.Sprintf("Local job execution '%s' not found for interface creation", jobDef.JobName), "workflow_manager")
			continue
		}

		// Create interfaces with proper ID mapping
		wm.logger.Info(fmt.Sprintf("Creating interfaces for LOCAL job_execution %d ('%s') with %d interface definitions",
			jobExec.ID, jobDef.JobName, len(jobDef.Interfaces)), "workflow_manager")

		err := wm.jobManager.CreateJobInterfaces(jobExec, jobDef.Interfaces, nodeIDToJobExecutionIDMap, nodeIDToWorkflowJobIDMap)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to create interfaces for job %d: %v", jobExec.ID, err), "workflow_manager")
			continue
		}

		wm.logger.Info(fmt.Sprintf("Interfaces created successfully for LOCAL job_execution %d", jobExec.ID), "workflow_manager")
	}

	wm.logger.Info(fmt.Sprintf("Phase 3 completed: All interfaces created for workflow execution %d. Jobs will execute based on their constraints.",
		execution.WorkflowExecutionID), "workflow_manager")

	// Workflow continues asynchronously
	// Status updates will be received via periodic polling or push notifications
}

// createLocalJobExecution creates a job_execution for a local job
func (wm *WorkflowManager) createLocalJobExecution(workflowJob *database.WorkflowJob, jobDef *types.WorkflowJob) (*database.JobExecution, error) {
	request := &types.JobExecutionRequest{
		WorkflowID:          workflowJob.WorkflowID,
		WorkflowJobID:       workflowJob.ID,
		JobName:             workflowJob.JobName,
		ServiceID:           workflowJob.ServiceID,
		ServiceType:         jobDef.ServiceType,
		ExecutorPeerID:      workflowJob.ExecutorPeerID,
		Entrypoint:          jobDef.Entrypoint,
		Commands:            jobDef.Commands,
		ExecutionConstraint: jobDef.ExecutionConstraint,
		Interfaces:          jobDef.Interfaces, // Store for Phase 3
		OrderingPeerID:      wm.peerManager.GetPeerID(),
		RequestedAt:         time.Now(),
	}

	// Create job_execution WITHOUT interfaces (Phase 3 will create them)
	// Skip interface creation in Phase 2 - interfaces will be created in Phase 3 with proper ID mapping
	jobExec, err := wm.jobManager.SubmitJobWithOptions(request, nil, true) // skipInterfaces = true
	if err != nil {
		return nil, fmt.Errorf("failed to create local job execution: %v", err)
	}

	wm.logger.Info(fmt.Sprintf("Created local job_execution ID=%d for workflow_job ID=%d (interfaces will be created in Phase 3)",
		jobExec.ID, workflowJob.ID), "workflow_manager")

	return jobExec, nil
}

// sendRemoteJobRequest sends a job execution request to a remote peer and returns the remote job_execution_id
// NOTE: This function must be called with the workflow execution's nodeIDToWorkflowJobIDMap
func (wm *WorkflowManager) sendRemoteJobRequest(workflowJob *database.WorkflowJob, jobDef *types.WorkflowJob, nodeIDToWorkflowJobIDMap map[int64]int64) (int64, error) {
	// Note: Connection is handled by jobHandler.SendJobRequest
	// No explicit connection check needed here

	// Get job handler
	jobHandler := wm.peerManager.GetJobHandler()
	if jobHandler == nil {
		return 0, fmt.Errorf("job handler not available")
	}

	// Translate node_ids to workflow_job_ids in interface peer definitions
	// Remote executors don't have workflow_job records, so they can't do this translation themselves
	// The orchestrator must send already-translated workflow_job_ids
	translatedInterfaces := make([]*types.JobInterface, len(jobDef.Interfaces))
	for i, iface := range jobDef.Interfaces {
		translatedIface := &types.JobInterface{
			Type:           iface.Type,
			Path:           iface.Path,
			InterfacePeers: make([]*types.InterfacePeer, len(iface.InterfacePeers)),
		}

		for j, peer := range iface.InterfacePeers {
			translatedPeer := &types.InterfacePeer{
				PeerID:            peer.PeerID,
				PeerWorkflowJobID: peer.PeerWorkflowJobID, // Will translate if needed
				PeerPath:          peer.PeerPath,
				PeerMountFunction: peer.PeerMountFunction,
				DutyAcknowledged:  peer.DutyAcknowledged,
			}

			// Translate node_id to workflow_job_id
			if peer.PeerWorkflowJobID != nil && nodeIDToWorkflowJobIDMap != nil {
				if workflowJobID, ok := nodeIDToWorkflowJobIDMap[*peer.PeerWorkflowJobID]; ok {
					translatedPeer.PeerWorkflowJobID = &workflowJobID
					wm.logger.Debug(fmt.Sprintf("Translated peer node_id %d -> workflow_job_id %d for remote job request",
						*peer.PeerWorkflowJobID, workflowJobID), "workflow_manager")
				}
			}

			translatedIface.InterfacePeers[j] = translatedPeer
		}

		translatedInterfaces[i] = translatedIface
	}

	// Create job execution request
	request := &types.JobExecutionRequest{
		WorkflowID:          workflowJob.WorkflowID,
		WorkflowJobID:       workflowJob.ID,  // Send workflow_job_id
		JobName:             workflowJob.JobName,
		ServiceID:           workflowJob.ServiceID,
		ServiceType:         jobDef.ServiceType,
		ExecutorPeerID:      workflowJob.ExecutorPeerID,
		Entrypoint:          jobDef.Entrypoint,
		Commands:            jobDef.Commands,
		ExecutionConstraint: jobDef.ExecutionConstraint,
		Interfaces:          translatedInterfaces, // Use translated interfaces
		OrderingPeerID:      wm.peerManager.GetPeerID(),
		RequestedAt:         time.Now(),
	}

	// Send request to remote peer
	wm.logger.Info(fmt.Sprintf("Sending job request to peer %s for workflow_job %d",
		workflowJob.ExecutorPeerID[:8], workflowJob.ID), "workflow_manager")

	response, err := jobHandler.SendJobRequest(workflowJob.ExecutorPeerID, request)
	if err != nil {
		return 0, fmt.Errorf("failed to send job request: %v", err)
	}

	if !response.Accepted {
		return 0, fmt.Errorf("job rejected by remote peer: %s", response.Message)
	}

	// Store the remote peer's job_execution_id in workflow_job
	wm.logger.Info(fmt.Sprintf("Job accepted by peer %s, remote job_execution_id=%d",
		workflowJob.ExecutorPeerID[:8], response.JobExecutionID), "workflow_manager")

	err = wm.db.UpdateWorkflowJobRemoteExecutionID(workflowJob.ID, response.JobExecutionID)
	if err != nil {
		return 0, fmt.Errorf("failed to store remote job_execution_id: %v", err)
	}

	return response.JobExecutionID, nil
}

// Note: Dependency resolution and job orchestration moved to peer-side
// Each peer evaluates its own execution constraints (NONE vs INPUTS_READY)
// Workflow completion is determined by periodic status polling

// parseWorkflowDefinition parses a workflow definition from JSON
func (wm *WorkflowManager) parseWorkflowDefinition(workflow *database.Workflow) (*types.WorkflowDefinition, error) {
	wm.logger.Info(fmt.Sprintf("Parsing workflow definition for workflow %d", workflow.ID), "workflow_manager")

	// Parse JSON definition
	var jsonDef struct {
		Jobs []struct {
			NodeID              int64    `json:"node_id"` // Workflow node ID for mapping
			Name                string   `json:"name"`
			ServiceID           int64    `json:"service_id"`
			ServiceType         string   `json:"service_type"`
			ExecutorPeerID      string   `json:"executor_peer_id"`
			Entrypoint          []string `json:"entrypoint,omitempty"`
			Commands            []string `json:"commands,omitempty"`
			ExecutionConstraint string   `json:"execution_constraint"`
			Interfaces          []struct {
				Type           string `json:"type"`
				Path           string `json:"path"`
				InterfacePeers []struct {
					PeerID             string  `json:"peer_id"`
					PeerWorkflowNodeID *int64  `json:"peer_workflow_node_id,omitempty"`
					PeerPath           string  `json:"peer_path"`
					PeerMountFunction  string  `json:"peer_mount_function"`
					DutyAcknowledged   bool    `json:"duty_acknowledged,omitempty"`
				} `json:"interface_peers"`
			} `json:"interfaces"`
		} `json:"jobs"`
	}

	// Marshal the definition map back to JSON
	defBytes, err := json.Marshal(workflow.Definition)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to marshal workflow definition: %v", err), "workflow_manager")
		return nil, fmt.Errorf("failed to marshal workflow definition: %v", err)
	}

	// Unmarshal into our structured type
	if err := json.Unmarshal(defBytes, &jsonDef); err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to parse workflow definition JSON: %v", err), "workflow_manager")
		return nil, fmt.Errorf("failed to parse workflow definition: %v", err)
	}

	// Convert to WorkflowDefinition
	workflowDef := &types.WorkflowDefinition{
		ID:          workflow.ID,
		Name:        workflow.Name,
		Description: workflow.Description,
		Jobs:        make([]*types.WorkflowJob, 0, len(jsonDef.Jobs)),
		RawDef:      workflow.Definition,
		CreatedAt:   workflow.CreatedAt,
		UpdatedAt:   workflow.UpdatedAt,
	}

	// Get local peer ID (orchestrator) for normalizing empty PeerID values
	localPeerID := wm.peerManager.GetPeerID()

	// Parse each job
	for _, jobDef := range jsonDef.Jobs {
		job := &types.WorkflowJob{
			NodeID:              jobDef.NodeID, // Store node ID for mapping
			JobName:             jobDef.Name,
			ServiceID:           jobDef.ServiceID,
			ServiceType:         jobDef.ServiceType,
			ExecutorPeerID:      jobDef.ExecutorPeerID,
			Entrypoint:          jobDef.Entrypoint,
			Commands:            jobDef.Commands,
			ExecutionConstraint: jobDef.ExecutionConstraint,
			Interfaces:          make([]*types.JobInterface, 0, len(jobDef.Interfaces)),
			Status:              "PENDING",
		}

		// Parse interfaces
		for _, ifaceDef := range jobDef.Interfaces {
			iface := &types.JobInterface{
				Type:           ifaceDef.Type,
				Path:           ifaceDef.Path,
				InterfacePeers: make([]*types.InterfacePeer, 0, len(ifaceDef.InterfacePeers)),
			}

			// Parse interface peers
			for _, peerDef := range ifaceDef.InterfacePeers {
				// Normalize empty PeerID to actual orchestrator peer ID
				// Empty PeerID from frontend means "Local Peer" (orchestrator/requester)
				// We normalize it here so all code downstream can use actual peer IDs
				peerID := peerDef.PeerID
				if peerID == "" {
					peerID = localPeerID
					wm.logger.Debug(fmt.Sprintf("Normalized empty PeerID to orchestrator %s for job '%s' interface %s",
						localPeerID[:8], jobDef.Name, ifaceDef.Type), "workflow_manager")
				}

				peer := &types.InterfacePeer{
					PeerID:            peerID, // Use normalized peer ID
					PeerWorkflowJobID: peerDef.PeerWorkflowNodeID, // NOTE: At workflow definition time, this is workflow_node_id
					PeerPath:          peerDef.PeerPath,
					PeerMountFunction: peerDef.PeerMountFunction,
					DutyAcknowledged:  peerDef.DutyAcknowledged,
				}
				iface.InterfacePeers = append(iface.InterfacePeers, peer)
			}

			job.Interfaces = append(job.Interfaces, iface)
		}

		workflowDef.Jobs = append(workflowDef.Jobs, job)
	}

	wm.logger.Info(fmt.Sprintf("Workflow definition parsed: %d jobs", len(workflowDef.Jobs)), "workflow_manager")
	return workflowDef, nil
}

// validateWorkflow validates a workflow definition
func (wm *WorkflowManager) validateWorkflow(workflow *types.WorkflowDefinition) error {
	wm.logger.Info(fmt.Sprintf("Validating workflow '%s'", workflow.Name), "workflow_manager")

	// Check if workflow has jobs
	if len(workflow.Jobs) == 0 {
		return fmt.Errorf("workflow has no jobs")
	}

	// Validate each job
	localPeerID := wm.peerManager.GetPeerID()
	for _, job := range workflow.Jobs {
		// Only validate service exists locally if this job executes on the local peer
		// For remote jobs, the service exists on the executor peer's database
		if job.ExecutorPeerID == localPeerID {
			// Check if service exists locally
			service, err := wm.db.GetService(job.ServiceID)
			if err != nil {
				return fmt.Errorf("service %d not found locally for job '%s'", job.ServiceID, job.JobName)
			}

			if service == nil {
				return fmt.Errorf("service %d not found locally for job '%s'", job.ServiceID, job.JobName)
			}

			// Validate service type matches
			if service.ServiceType != job.ServiceType {
				return fmt.Errorf("service type mismatch for job '%s': expected %s, got %s",
					job.JobName, job.ServiceType, service.ServiceType)
			}
		}
		// For remote jobs, skip local service validation - will be validated against remote peer later

		// Validate interfaces
		for _, iface := range job.Interfaces {
			if iface.Type != types.InterfaceTypeStdin &&
			   iface.Type != types.InterfaceTypeStdout &&
			   iface.Type != types.InterfaceTypeStderr &&
			   iface.Type != types.InterfaceTypeLogs &&
			   iface.Type != types.InterfaceTypeMount {
				return fmt.Errorf("invalid interface type '%s' for job '%s'", iface.Type, job.JobName)
			}

			// Validate interface peers
			for _, peer := range iface.InterfacePeers {
				if peer.PeerMountFunction != types.MountFunctionInput &&
				   peer.PeerMountFunction != types.MountFunctionOutput &&
				   peer.PeerMountFunction != types.MountFunctionBoth {
					return fmt.Errorf("invalid mount function '%s' for job '%s'", peer.PeerMountFunction, job.JobName)
				}
			}
		}
	}

	// TODO: Validate job dependencies (no circular dependencies)
	// TODO: Validate interface compatibility between jobs

	// Note: Service availability validation is done on the executor peer when the job is submitted
	// This avoids the issue of service IDs being local to each peer's database
	// The executor peer will validate the service exists and return an error if not found

	// Validate data transfer constraints
	if err := wm.validateDataTransferConstraints(workflow.Jobs); err != nil {
		return fmt.Errorf("data transfer constraints validation failed: %v", err)
	}

	wm.logger.Info(fmt.Sprintf("Workflow '%s' validation successful", workflow.Name), "workflow_manager")
	return nil
}

// validatePeerConnectivityAndServices actively connects to peers and verifies services are available
func (wm *WorkflowManager) validatePeerConnectivityAndServices(jobs []*types.WorkflowJob) error {
	wm.logger.Info("Validating peer connectivity and service availability", "workflow_manager")

	// Track unique peers and their services to validate
	type peerServiceCheck struct {
		peerID    string
		serviceID int64
		jobName   string
	}
	checks := make([]peerServiceCheck, 0)

	// Collect all peer-service pairs that need validation
	for _, job := range jobs {
		checks = append(checks, peerServiceCheck{
			peerID:    job.ExecutorPeerID,
			serviceID: job.ServiceID,
			jobName:   job.JobName,
		})
	}

	// Validate each peer-service pair
	for _, check := range checks {
		wm.logger.Info(fmt.Sprintf("Validating peer %s has service '%s' for job '%s'",
			check.peerID[:8], check.jobName, check.jobName), "workflow_manager")

		// Use the service name from the workflow definition (check.jobName)
		// Note: check.serviceID is the executor peer's local service ID, not ours
		// We should NOT look it up in our local database as service IDs are not global

		// Try to connect to peer and query for this specific service
		available, err := wm.verifyPeerServiceAvailability(check.peerID, check.serviceID, check.jobName)
		if err != nil {
			return fmt.Errorf("failed to verify peer %s for job '%s': %v", check.peerID[:8], check.jobName, err)
		}

		if !available {
			return fmt.Errorf("service '%s' is not available on peer %s for job '%s'",
				check.jobName, check.peerID[:8], check.jobName)
		}

		wm.logger.Info(fmt.Sprintf("Peer %s has service %d available for job '%s'",
			check.peerID[:8], check.serviceID, check.jobName), "workflow_manager")
	}

	wm.logger.Info("All peers reachable and services available", "workflow_manager")
	return nil
}

// verifyPeerServiceAvailability connects to peer and verifies service is available
func (wm *WorkflowManager) verifyPeerServiceAvailability(peerID string, serviceID int64, serviceName string) (bool, error) {
	wm.logger.Debug(fmt.Sprintf("Verifying service %d on peer %s", serviceID, peerID[:8]), "workflow_manager")

	// Get QUIC peer from peer manager
	quicPeer := wm.peerManager.GetQUICPeer()
	if quicPeer == nil {
		return false, fmt.Errorf("QUIC peer not available")
	}

	// Try to get existing connection first
	conn, err := quicPeer.GetConnectionByPeerID(peerID)
	if err != nil || conn == nil {
		wm.logger.Debug(fmt.Sprintf("No existing connection to peer %s, attempting to connect", peerID[:8]), "workflow_manager")

		// Try to connect using DHT metadata
		peer, err := wm.db.KnownPeers.GetKnownPeer(peerID, "remote-network-mesh")
		if err != nil || peer == nil {
			return false, fmt.Errorf("peer %s not found in known peers", peerID[:8])
		}

		// Query DHT for peer metadata to get connection info
		metadataQuery := wm.peerManager.GetMetadataQueryService()
		if metadataQuery == nil {
			return false, fmt.Errorf("metadata query service not available")
		}

		metadata, err := metadataQuery.QueryMetadata(peerID, peer.PublicKey)
		if err != nil {
			wm.logger.Warn(fmt.Sprintf("Failed to query metadata for peer %s: %v", peerID[:8], err), "workflow_manager")
			// If we can't get metadata, we can't verify connectivity
			return false, fmt.Errorf("failed to query peer metadata: %v", err)
		}

		// Check if peer is public or using relay
		if !metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.PublicIP != "" {
			// Try direct connection to public peer
			peerAddr := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
			conn, err = quicPeer.ConnectToPeer(peerAddr)
			if err != nil {
				wm.logger.Warn(fmt.Sprintf("Failed to connect to public peer %s at %s: %v", peerID[:8], peerAddr, err), "workflow_manager")
				return false, fmt.Errorf("peer %s unreachable at %s: %v", peerID[:8], peerAddr, err)
			}
		} else {
			// Peer is behind NAT or using relay
			wm.logger.Warn(fmt.Sprintf("Peer %s is behind NAT/using relay, will be accessible via relay during execution", peerID[:8]), "workflow_manager")
			// For NAT/relay peers, we trust the service exists if it's in the database
			// The actual connection will be established via relay during execution
			return true, nil
		}
	}

	// Query service from peer
	ctx := context.WithValue(context.Background(), "timeout", 10*time.Second)
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		// Stream open failure often indicates a stale connection (connection exists locally
		// but remote side has closed it). Clean it up to force fresh connection on next attempt.
		peerAddr := conn.RemoteAddr().String()
		wm.logger.Debug(fmt.Sprintf("Cleaning up potentially stale connection %s after stream open failure", peerAddr), "workflow_manager")

		// Clean up stale connection to force fresh reconnection on retry
		if cleanupErr := quicPeer.DisconnectFromPeer(peerAddr); cleanupErr != nil {
			wm.logger.Debug(fmt.Sprintf("Connection cleanup completed for %s (connection may have already been closed)", peerAddr), "workflow_manager")
		}

		return false, fmt.Errorf("failed to open stream to peer: %v", err)
	}
	defer stream.Close()

	// Send service search request for this specific service
	searchMsg := p2p.CreateServiceSearchRequest(serviceName, "", false)
	msgBytes, err := searchMsg.Marshal()
	if err != nil {
		return false, fmt.Errorf("failed to marshal service search: %v", err)
	}

	if _, err := stream.Write(msgBytes); err != nil {
		return false, fmt.Errorf("failed to send service search: %v", err)
	}

	// Read response
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	buffer := make([]byte, 1024*1024)
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("failed to read service search response: %v", err)
	}

	responseMsg, err := p2p.UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	var searchResponse p2p.ServiceSearchResponse
	if err := responseMsg.GetDataAs(&searchResponse); err != nil {
		return false, fmt.Errorf("failed to parse search response: %v", err)
	}

	// Check if our service is in the response
	for _, svc := range searchResponse.Services {
		if svc.ID == serviceID {
			return true, nil
		}
	}

	return false, nil
}

// validateDataTransferConstraints validates data services can only be transferred appropriately
func (wm *WorkflowManager) validateDataTransferConstraints(jobs []*types.WorkflowJob) error {
	wm.logger.Info("Validating data transfer constraints", "workflow_manager")

	requesterPeerID := wm.peerManager.GetPeerID()

	// Create map of jobs by executor peer for quick lookup
	jobsByPeer := make(map[string]*types.WorkflowJob)
	for _, job := range jobs {
		jobsByPeer[job.ExecutorPeerID+":"+job.JobName] = job
	}

	for _, job := range jobs {
		// Only check DATA services
		// Note: Use job.ServiceType directly - ServiceID refers to remote peer's local service ID
		// and cannot be looked up in orchestrator's local database
		if job.ServiceType != types.ServiceTypeData {
			continue
		}

		wm.logger.Debug(fmt.Sprintf("Validating data transfer constraints for DATA job '%s'", job.JobName), "workflow_manager")

		// DATA services MUST have at least one STDOUT interface (they must transfer data somewhere)
		hasStdout := false
		for _, iface := range job.Interfaces {
			if iface.Type == types.InterfaceTypeStdout {
				hasStdout = true
				break
			}
		}
		if !hasStdout {
			return fmt.Errorf("DATA job '%s' has no STDOUT interface - DATA services must transfer data to at least one destination", job.JobName)
		}

		// Check all STDOUT interfaces (where data goes)
		for _, iface := range job.Interfaces {
			if iface.Type != types.InterfaceTypeStdout {
				continue
			}

			for _, peer := range iface.InterfacePeers {
				if peer.PeerMountFunction != types.MountFunctionOutput && peer.PeerMountFunction != types.MountFunctionBoth {
					continue // We only care about outputs (where data goes)
				}

				// Note: PeerID is always populated (normalized at workflow parsing time)
				destPeerID := peer.PeerID

				// Rule 1: Allow transfer to requester (download)
				if destPeerID == requesterPeerID {
					wm.logger.Debug(fmt.Sprintf("DATA job '%s' transfers to requester: OK", job.JobName), "workflow_manager")
					continue
				}

				// Rule 2: Transfer to another peer ONLY if it's input for DOCKER/STANDALONE computation
				// Find the destination job that will receive this data
				destJob := wm.findJobByPeerAndInputPath(jobs, destPeerID, peer.PeerPath)
				if destJob == nil {
					// Safely format peer ID (handle short peer IDs)
					peerIDDisplay := destPeerID
					if len(destPeerID) > 8 {
						peerIDDisplay = destPeerID[:8]
					}
					return fmt.Errorf("DATA job '%s' cannot transfer to peer %s: destination is not requester and no computation job found using this data as input",
						job.JobName, peerIDDisplay)
				}

				// Verify destination job is a computation job (DOCKER or STANDALONE)
				// Note: Use destJob.ServiceType directly - ServiceID refers to remote peer's local service ID
				// and cannot be looked up in orchestrator's local database
				if destJob.ServiceType != types.ServiceTypeDocker && destJob.ServiceType != types.ServiceTypeStandalone {
					return fmt.Errorf("DATA job '%s' cannot transfer to peer %s: destination job '%s' is %s, not DOCKER or STANDALONE (data can only be input for computation, not peer-to-peer download)",
						job.JobName, destPeerID[:8], destJob.JobName, destJob.ServiceType)
				}

				wm.logger.Debug(fmt.Sprintf("DATA job '%s' transfers to job '%s' (%s) as input: OK",
					job.JobName, destJob.JobName, destJob.ServiceType), "workflow_manager")
			}
		}
	}

	wm.logger.Info("Data transfer constraints validated successfully", "workflow_manager")
	return nil
}

// findJobByPeerAndInputPath finds a job that runs on given peer and has given path as input (STDIN or MOUNT)
func (wm *WorkflowManager) findJobByPeerAndInputPath(jobs []*types.WorkflowJob, peerID string, inputPath string) *types.WorkflowJob {
	for _, job := range jobs {
		if job.ExecutorPeerID != peerID {
			continue
		}

		// Check if this job has the path as input (STDIN or MOUNT interface)
		for _, iface := range job.Interfaces {
			// Check both STDIN and MOUNT interfaces for input
			if iface.Type != types.InterfaceTypeStdin && iface.Type != types.InterfaceTypeMount {
				continue
			}

			// Check if this interface has the matching path as input
			for _, peer := range iface.InterfacePeers {
				if peer.PeerPath == inputPath && (peer.PeerMountFunction == types.MountFunctionInput || peer.PeerMountFunction == types.MountFunctionBoth) {
					return job
				}
			}

			// For MOUNT interfaces, also check if the interface path matches (the receiving side)
			if iface.Type == types.InterfaceTypeMount && iface.Path == inputPath {
				// Check if any peer is providing input to this mount
				for _, peer := range iface.InterfacePeers {
					if peer.PeerMountFunction == types.MountFunctionInput || peer.PeerMountFunction == types.MountFunctionBoth {
						return job
					}
				}
			}
		}
	}
	return nil
}

// CancelWorkflow cancels a running workflow
func (wm *WorkflowManager) CancelWorkflow(workflowJobID int64) error {
	wm.logger.Info(fmt.Sprintf("Cancelling workflow %d", workflowJobID), "workflow_manager")

	wm.mu.RLock()
	execution, exists := wm.activeWorkflows[workflowJobID]
	wm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("workflow %d not found or already completed", workflowJobID)
	}

	// Cancel all running jobs
	execution.mu.RLock()
	jobExecutions := make([]*database.JobExecution, 0, len(execution.JobExecutions))
	for _, jobExec := range execution.JobExecutions {
		jobExecutions = append(jobExecutions, jobExec)
	}
	execution.mu.RUnlock()

	for _, jobExec := range jobExecutions {
		if jobExec.Status == types.JobStatusRunning || jobExec.Status == types.JobStatusReady {
			wm.logger.Info(fmt.Sprintf("Cancelling job execution %d", jobExec.ID), "workflow_manager")
			err := wm.jobManager.CancelJob(jobExec.ID)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to cancel job execution %d: %v", jobExec.ID, err), "workflow_manager")
			}
		}
	}

	wm.logger.Info(fmt.Sprintf("Workflow %d cancelled", workflowJobID), "workflow_manager")
	return nil
}

// GetWorkflowStatus returns the status of a workflow execution
func (wm *WorkflowManager) GetWorkflowStatus(workflowJobID int64) (*types.WorkflowStatus, error) {
	wm.mu.RLock()
	execution, exists := wm.activeWorkflows[workflowJobID]
	wm.mu.RUnlock()

	if !exists {
		// Try to get from database (workflowJobID is actually execution ID here)
		workflowJobs, err := wm.db.GetWorkflowJobsByExecution(workflowJobID)
		if err != nil || len(workflowJobs) == 0 {
			return nil, fmt.Errorf("workflow execution %d not found", workflowJobID)
		}

		// Count completed jobs
		completed := 0
		var lastError string
		for _, job := range workflowJobs {
			if job.Status == "COMPLETED" {
				completed++
			}
			if job.Error != "" {
				lastError = job.Error
			}
		}

		return &types.WorkflowStatus{
			WorkflowID:    workflowJobs[0].WorkflowID,
			Status:        workflowJobs[0].Status, // Use first job's status as overall (needs improvement)
			CompletedJobs: completed,
			TotalJobs:     len(workflowJobs),
			ErrorMessage:  lastError,
		}, nil
	}

	execution.mu.RLock()
	defer execution.mu.RUnlock()

	return &types.WorkflowStatus{
		WorkflowID:    execution.WorkflowID,
		Status:        execution.Status,
		CompletedJobs: execution.CompletedJobs,
		TotalJobs:     len(execution.Definition.Jobs),
		ErrorMessage:  execution.ErrorMessage,
		StartedAt:     execution.StartedAt,
		CompletedAt:   execution.CompletedAt,
	}, nil
}

// startWorkflowStatusUpdater starts a periodic updater that requests status from executor peers
func (wm *WorkflowManager) startWorkflowStatusUpdater() {
	wm.wg.Add(1)
	go func() {
		defer wm.wg.Done()

		// Check every 10 seconds
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		wm.logger.Info("Started workflow status updater", "workflow_manager")

		for {
			select {
			case <-ticker.C:
				wm.updateActiveWorkflowStatuses()
			case <-wm.ctx.Done():
				wm.logger.Info("Workflow status updater stopped", "workflow_manager")
				return
			}
		}
	}()
}

// updateActiveWorkflowStatuses requests status updates from all executor peers for active workflows
func (wm *WorkflowManager) updateActiveWorkflowStatuses() {
	wm.mu.RLock()
	executions := make([]*WorkflowExecution, 0, len(wm.activeWorkflows))
	for _, execution := range wm.activeWorkflows {
		executions = append(executions, execution)
	}
	wm.mu.RUnlock()

	// For each active workflow, request status from all executor peers
	for _, execution := range executions {
		execution.mu.RLock()
		jobs := make(map[string]*database.JobExecution)
		for jobName, jobExec := range execution.JobExecutions {
			jobs[jobName] = jobExec
		}
		execution.mu.RUnlock()

		// Request status for each job
		for jobName, jobExec := range jobs {
			wm.requestJobStatus(execution, jobName, jobExec)
		}

		// Check if workflow is complete
		wm.checkWorkflowCompletion(execution)
	}
}

// requestJobStatus requests status for a specific job from its executor peer
func (wm *WorkflowManager) requestJobStatus(execution *WorkflowExecution, jobName string, jobExec *database.JobExecution) {
	// Skip completed or errored jobs
	if jobExec.Status == types.JobStatusCompleted || jobExec.Status == types.JobStatusErrored || jobExec.Status == types.JobStatusCancelled {
		return
	}

	// Get job handler
	jobHandler := wm.peerManager.GetQUICPeer().GetJobHandler()
	if jobHandler == nil {
		return
	}

	// Refresh job from database to get latest RemoteJobExecutionID
	// (it may have been updated after the in-memory cache was created)
	freshJob, err := wm.db.GetJobExecution(jobExec.ID)
	if err != nil || freshJob == nil {
		wm.logger.Warn(fmt.Sprintf("Failed to refresh job %d from database: %v", jobExec.ID, err), "workflow_manager")
		// Fall back to cached data
		freshJob = jobExec
	}

	// Request status from executor peer
	// Use remote_job_execution_id if this is a remote job
	jobIDToQuery := freshJob.ID
	if freshJob.RemoteJobExecutionID != nil {
		jobIDToQuery = *freshJob.RemoteJobExecutionID
	}

	request := &types.JobStatusRequest{
		JobExecutionID: jobIDToQuery,
		WorkflowJobID:  jobExec.WorkflowJobID,
	}

	response, err := jobHandler.SendJobStatusRequest(jobExec.ExecutorPeerID, request)
	if err != nil {
		wm.logger.Debug(fmt.Sprintf("Failed to request status for job '%s' from peer %s: %v", jobName, jobExec.ExecutorPeerID[:8], err), "workflow_manager")
		return
	}

	if !response.Found {
		wm.logger.Warn(fmt.Sprintf("Job '%s' (ID: %d) not found on executor peer %s", jobName, jobExec.ID, jobExec.ExecutorPeerID[:8]), "workflow_manager")
		return
	}

	// Update job status in database if it changed
	if response.Status != jobExec.Status {
		wm.logger.Info(fmt.Sprintf("Job '%s' status updated: %s -> %s", jobName, jobExec.Status, response.Status), "workflow_manager")
		err := wm.db.UpdateJobStatus(jobExec.ID, response.Status, response.ErrorMessage)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to update job %d status: %v", jobExec.ID, err), "workflow_manager")
		} else {
			// Update in-memory status
			jobExec.Status = response.Status
			jobExec.ErrorMessage = response.ErrorMessage
		}
	}
}

// checkWorkflowCompletion checks if a workflow has completed and updates its status
func (wm *WorkflowManager) checkWorkflowCompletion(execution *WorkflowExecution) {
	execution.mu.RLock()
	jobExecutions := make([]*database.JobExecution, 0, len(execution.JobExecutions))
	for _, jobExec := range execution.JobExecutions {
		jobExecutions = append(jobExecutions, jobExec)
	}
	execution.mu.RUnlock()

	// Count job statuses - refresh from database to get latest status
	var completed, errored, total int
	total = len(jobExecutions)

	for _, jobExec := range jobExecutions {
		// Refresh job status from database (important for local jobs)
		freshJob, err := wm.db.GetJobExecution(jobExec.ID)
		if err != nil {
			wm.logger.Warn(fmt.Sprintf("Failed to refresh job %d status from database: %v", jobExec.ID, err), "workflow_manager")
			// Fall back to cached status
			freshJob = jobExec
		}

		// Update in-memory status if it changed
		if freshJob.Status != jobExec.Status {
			execution.mu.Lock()
			jobExec.Status = freshJob.Status
			jobExec.ErrorMessage = freshJob.ErrorMessage
			execution.mu.Unlock()
		}

		switch freshJob.Status {
		case types.JobStatusCompleted:
			completed++
		case types.JobStatusErrored, types.JobStatusCancelled:
			errored++
		}
	}

	// Check if all jobs are done
	if completed+errored == total {
		var finalStatus string
		var errorMsg string

		if errored > 0 {
			finalStatus = "FAILED"
			errorMsg = fmt.Sprintf("%d of %d jobs failed", errored, total)
		} else {
			finalStatus = "COMPLETED"
		}

		wm.logger.Info(fmt.Sprintf("Workflow execution %d completed: %s (completed: %d, errored: %d, total: %d)",
			execution.WorkflowExecutionID, finalStatus, completed, errored, total), "workflow_manager")

		// Update workflow execution status in database
		err := wm.db.UpdateWorkflowExecutionStatus(execution.WorkflowExecutionID, finalStatus, errorMsg)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to update workflow execution status: %v", err), "workflow_manager")
		}

		// Broadcast execution status update via WebSocket
		if wm.eventEmitter != nil {
			wm.eventEmitter.BroadcastExecutionUpdate(execution.WorkflowExecutionID)
		}

		// Remove from active workflows
		wm.mu.Lock()
		delete(wm.activeWorkflows, execution.WorkflowExecutionID)
		wm.mu.Unlock()

		wm.logger.Info(fmt.Sprintf("Workflow execution %d finalized and removed from active workflows", execution.WorkflowExecutionID), "workflow_manager")
	}
}
