package core

import (
	"context"
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
	ctx            context.Context
	cancel         context.CancelFunc
	db             *database.SQLiteManager
	cm             *utils.ConfigManager
	logger         *utils.LogsManager
	jobManager     *JobManager
	peerManager    *PeerManager
	activeWorkflows map[int64]*WorkflowExecution // workflow_job_id -> execution
	mu             sync.RWMutex
	wg             sync.WaitGroup
}

// WorkflowExecution represents an active workflow execution
type WorkflowExecution struct {
	WorkflowJobID   int64
	WorkflowID      int64
	Definition      *types.WorkflowDefinition
	JobExecutions   map[string]*database.JobExecution // job_name -> execution
	CompletedJobs   int
	Status          string // pending, running, completed, failed, cancelled
	StartedAt       time.Time
	CompletedAt     time.Time
	ErrorMessage    string
	mu              sync.RWMutex
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

// Start starts the workflow manager
func (wm *WorkflowManager) Start() error {
	wm.logger.Info("Starting Workflow Manager", "workflow_manager")
	wm.logger.Info("Workflow Manager started successfully", "workflow_manager")
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

	// Create workflow job (execution instance)
	workflowJob, err := wm.db.CreateWorkflowJob(workflowID)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to create workflow job: %v", err), "workflow_manager")
		return nil, fmt.Errorf("failed to create workflow job: %v", err)
	}

	// Create workflow execution tracker
	execution := &WorkflowExecution{
		WorkflowJobID: workflowJob.ID,
		WorkflowID:    workflowID,
		Definition:    workflowDef,
		JobExecutions: make(map[string]*database.JobExecution),
		Status:        "pending",
		StartedAt:     time.Now(),
	}

	// Register execution
	wm.mu.Lock()
	wm.activeWorkflows[workflowJob.ID] = execution
	wm.mu.Unlock()

	// Update workflow job status to running
	err = wm.db.UpdateWorkflowJobStatus(workflowJob.ID, "running", nil, "")
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to update workflow job status: %v", err), "workflow_manager")
	}

	// Start workflow execution in background
	wm.wg.Add(1)
	go func() {
		defer wm.wg.Done()
		wm.executeWorkflowJobs(execution)
	}()

	wm.logger.Info(fmt.Sprintf("Workflow %d execution started (workflow_job_id: %d)", workflowID, workflowJob.ID), "workflow_manager")
	return workflowJob, nil
}

// executeWorkflowJobs executes all jobs in a workflow with parallel execution for independent jobs
func (wm *WorkflowManager) executeWorkflowJobs(execution *WorkflowExecution) {
	wm.logger.Info(fmt.Sprintf("Starting workflow execution for workflow_job %d", execution.WorkflowJobID), "workflow_manager")

	execution.mu.Lock()
	execution.Status = "running"
	execution.mu.Unlock()

	// Build dependency graph
	dependencies := wm.buildJobDependencies(execution.Definition.Jobs)

	// Track job statuses
	jobStatuses := make(map[string]string) // job_name -> status (pending, running, completed, failed)
	jobErrors := make(map[string]error)
	var statusMu sync.Mutex

	// Initialize all jobs as pending
	for _, job := range execution.Definition.Jobs {
		statusMu.Lock()
		jobStatuses[job.JobName] = "pending"
		statusMu.Unlock()
	}

	// Channel to signal job completion
	jobCompleteChan := make(chan string, len(execution.Definition.Jobs))

	// WaitGroup to track all jobs
	var jobWg sync.WaitGroup

	// Error channel for early termination
	errorChan := make(chan error, 1)

	// Worker function to execute a job
	executeJob := func(workflowJob *types.WorkflowJob) {
		defer jobWg.Done()

		jobName := workflowJob.JobName
		wm.logger.Info(fmt.Sprintf("Executing job '%s' for workflow_job %d", jobName, execution.WorkflowJobID), "workflow_manager")

		// Check if workflow was cancelled
		select {
		case <-wm.ctx.Done():
			statusMu.Lock()
			jobStatuses[jobName] = "failed"
			jobErrors[jobName] = fmt.Errorf("workflow cancelled")
			statusMu.Unlock()
			jobCompleteChan <- jobName
			return
		default:
		}

		// Mark as running
		statusMu.Lock()
		jobStatuses[jobName] = "running"
		statusMu.Unlock()

		// Create job execution request
		request := &types.JobExecutionRequest{
			WorkflowID:          execution.WorkflowID,
			WorkflowJobID:       execution.WorkflowJobID,
			JobName:             jobName,
			ServiceID:           workflowJob.ServiceID,
			ServiceType:         workflowJob.ServiceType,
			Entrypoint:          workflowJob.Entrypoint,
			Commands:            workflowJob.Commands,
			ExecutionConstraint: workflowJob.ExecutionConstraint,
			Interfaces:          workflowJob.Interfaces,
			OrderingPeerID:      wm.peerManager.GetPeerID(),
			RequestedAt:         time.Now(),
		}

		// Submit job to job manager
		jobExecution, err := wm.jobManager.SubmitJob(request)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Failed to submit job '%s': %v", jobName, err), "workflow_manager")
			statusMu.Lock()
			jobStatuses[jobName] = "failed"
			jobErrors[jobName] = fmt.Errorf("failed to submit: %v", err)
			statusMu.Unlock()

			// Send error to error channel (non-blocking)
			select {
			case errorChan <- fmt.Errorf("failed to submit job '%s': %v", jobName, err):
			default:
			}

			jobCompleteChan <- jobName
			return
		}

		// Track job execution
		execution.mu.Lock()
		execution.JobExecutions[jobName] = jobExecution
		execution.mu.Unlock()

		wm.logger.Info(fmt.Sprintf("Job '%s' submitted (job_execution_id: %d)", jobName, jobExecution.ID), "workflow_manager")

		// Wait for job completion
		err = wm.waitForJobCompletion(jobExecution.ID)
		if err != nil {
			wm.logger.Error(fmt.Sprintf("Job '%s' failed: %v", jobName, err), "workflow_manager")
			statusMu.Lock()
			jobStatuses[jobName] = "failed"
			jobErrors[jobName] = err
			statusMu.Unlock()

			// Send error to error channel (non-blocking)
			select {
			case errorChan <- fmt.Errorf("job '%s' failed: %v", jobName, err):
			default:
			}

			jobCompleteChan <- jobName
			return
		}

		// Mark as completed
		statusMu.Lock()
		jobStatuses[jobName] = "completed"
		statusMu.Unlock()

		// Update completed jobs count
		execution.mu.Lock()
		execution.CompletedJobs++
		execution.mu.Unlock()

		wm.logger.Info(fmt.Sprintf("Job '%s' completed successfully (%d/%d jobs completed)",
			jobName, execution.CompletedJobs, len(execution.Definition.Jobs)), "workflow_manager")

		jobCompleteChan <- jobName
	}

	// Start a goroutine to monitor job completions and start new jobs
	go func() {
		jobsMap := make(map[string]*types.WorkflowJob)
		for _, job := range execution.Definition.Jobs {
			jobsMap[job.JobName] = job
		}

		for {
			// Check if we can start any pending jobs
			statusMu.Lock()
			for jobName, status := range jobStatuses {
				if status != "pending" {
					continue
				}

				// Check if all dependencies are completed
				deps := dependencies[jobName]
				canStart := true
				for _, dep := range deps {
					depStatus := jobStatuses[dep]
					if depStatus != "completed" {
						canStart = false
						break
					}
				}

				if canStart {
					// Start this job
					job := jobsMap[jobName]
					jobStatuses[jobName] = "starting"
					statusMu.Unlock()

					wm.logger.Info(fmt.Sprintf("Starting job '%s' (dependencies satisfied)", jobName), "workflow_manager")
					jobWg.Add(1)
					go executeJob(job)

					statusMu.Lock()
				}
			}
			statusMu.Unlock()

			// Check if all jobs are done
			statusMu.Lock()
			allDone := true
			for _, status := range jobStatuses {
				if status == "pending" || status == "running" || status == "starting" {
					allDone = false
					break
				}
			}
			statusMu.Unlock()

			if allDone {
				break
			}

			// Wait for a job to complete before checking again
			select {
			case <-jobCompleteChan:
				// A job completed, loop again to check for new jobs to start
			case <-wm.ctx.Done():
				// Workflow cancelled
				return
			case <-time.After(100 * time.Millisecond):
				// Periodic check
			}
		}
	}()

	// Wait for all jobs to complete
	jobWg.Wait()
	close(jobCompleteChan)

	// Check for errors
	select {
	case err := <-errorChan:
		wm.handleWorkflowCompletion(execution, "failed", err.Error())
		return
	default:
	}

	// Check if any job failed
	statusMu.Lock()
	var failedJob string
	var failedError error
	for jobName, status := range jobStatuses {
		if status == "failed" {
			failedJob = jobName
			failedError = jobErrors[jobName]
			break
		}
	}
	statusMu.Unlock()

	if failedJob != "" {
		wm.handleWorkflowCompletion(execution, "failed", fmt.Sprintf("Job '%s' failed: %v", failedJob, failedError))
		return
	}

	// All jobs completed successfully
	wm.handleWorkflowCompletion(execution, "completed", "")
}

// buildJobDependencies builds a dependency graph for jobs based on their interfaces
func (wm *WorkflowManager) buildJobDependencies(jobs []*types.WorkflowJob) map[string][]string {
	// Map of job_name -> list of job names it depends on
	dependencies := make(map[string][]string)

	// Map of job_name -> job for quick lookup
	jobsMap := make(map[string]*types.WorkflowJob)
	for _, job := range jobs {
		jobsMap[job.JobName] = job
		dependencies[job.JobName] = []string{}
	}

	// For each job, check if it has STDIN interfaces that receive from other jobs
	for _, job := range jobs {
		for _, iface := range job.Interfaces {
			if iface.Type == types.InterfaceTypeStdin {
				// Check if any peer is a provider from another job
				for _, peer := range iface.InterfacePeers {
					if peer.PeerMountFunction == types.MountFunctionProvider {
						// Find which job outputs to this peer
						for _, otherJob := range jobs {
							if otherJob.JobName == job.JobName {
								continue
							}

							// Check if otherJob has STDOUT to this job
							for _, otherIface := range otherJob.Interfaces {
								if otherIface.Type == types.InterfaceTypeStdout {
									for _, otherPeer := range otherIface.InterfacePeers {
										// Check if this output goes to our job
										if otherPeer.PeerMountFunction == types.MountFunctionReceiver &&
										   otherPeer.PeerNodeID == peer.PeerNodeID {
											// This job depends on otherJob
											dependencies[job.JobName] = append(dependencies[job.JobName], otherJob.JobName)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return dependencies
}

// waitForJobCompletion waits for a job to complete
func (wm *WorkflowManager) waitForJobCompletion(jobExecutionID int64) error {
	wm.logger.Info(fmt.Sprintf("Waiting for job execution %d to complete", jobExecutionID), "workflow_manager")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(time.Duration(wm.cm.GetConfigInt("workflow_job_timeout_seconds", 3600, 60, 86400)) * time.Second)

	for {
		select {
		case <-ticker.C:
			// Check job status
			status, err := wm.jobManager.GetJobStatus(jobExecutionID)
			if err != nil {
				wm.logger.Error(fmt.Sprintf("Failed to get job status: %v", err), "workflow_manager")
				continue
			}

			wm.logger.Info(fmt.Sprintf("Job execution %d status: %s", jobExecutionID, status), "workflow_manager")

			switch status {
			case types.JobStatusCompleted:
				wm.logger.Info(fmt.Sprintf("Job execution %d completed successfully", jobExecutionID), "workflow_manager")
				return nil

			case types.JobStatusErrored:
				job, err := wm.db.GetJobExecution(jobExecutionID)
				if err != nil {
					return fmt.Errorf("job execution failed (error details unavailable)")
				}
				return fmt.Errorf("job execution failed: %s", job.ErrorMessage)

			case types.JobStatusCancelled:
				return fmt.Errorf("job execution was cancelled")
			}

		case <-timeout:
			wm.logger.Error(fmt.Sprintf("Job execution %d timed out", jobExecutionID), "workflow_manager")
			return fmt.Errorf("job execution timed out")

		case <-wm.ctx.Done():
			wm.logger.Info(fmt.Sprintf("Workflow manager stopping, cancelling wait for job %d", jobExecutionID), "workflow_manager")
			return fmt.Errorf("workflow manager stopped")
		}
	}
}

// handleWorkflowCompletion handles workflow completion
func (wm *WorkflowManager) handleWorkflowCompletion(execution *WorkflowExecution, status string, errorMsg string) {
	wm.logger.Info(fmt.Sprintf("Workflow %d completed with status: %s", execution.WorkflowJobID, status), "workflow_manager")

	execution.mu.Lock()
	execution.Status = status
	execution.CompletedAt = time.Now()
	execution.ErrorMessage = errorMsg
	execution.mu.Unlock()

	// Update workflow job status in database
	var result map[string]interface{}
	if status == "completed" {
		result = map[string]interface{}{
			"completed_jobs": execution.CompletedJobs,
			"total_jobs":     len(execution.Definition.Jobs),
		}
	}

	err := wm.db.UpdateWorkflowJobStatus(execution.WorkflowJobID, status, result, errorMsg)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to update workflow job status: %v", err), "workflow_manager")
	}

	// Remove from active workflows
	wm.mu.Lock()
	delete(wm.activeWorkflows, execution.WorkflowJobID)
	wm.mu.Unlock()

	wm.logger.Info(fmt.Sprintf("Workflow execution %d finalized", execution.WorkflowJobID), "workflow_manager")
}

// parseWorkflowDefinition parses a workflow definition from JSON
func (wm *WorkflowManager) parseWorkflowDefinition(workflow *database.Workflow) (*types.WorkflowDefinition, error) {
	wm.logger.Info(fmt.Sprintf("Parsing workflow definition for workflow %d", workflow.ID), "workflow_manager")

	// Parse JSON definition
	var jsonDef struct {
		Jobs []struct {
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
					PeerNodeID        string  `json:"peer_node_id"`
					PeerJobID         *int64  `json:"peer_job_id,omitempty"`
					PeerPath          string  `json:"peer_path"`
					PeerMountFunction string  `json:"peer_mount_function"`
					DutyAcknowledged  bool    `json:"duty_acknowledged,omitempty"`
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

	// Parse each job
	for _, jobDef := range jsonDef.Jobs {
		job := &types.WorkflowJob{
			JobName:             jobDef.Name,
			ServiceID:           jobDef.ServiceID,
			ServiceType:         jobDef.ServiceType,
			ExecutorPeerID:      jobDef.ExecutorPeerID,
			Entrypoint:          jobDef.Entrypoint,
			Commands:            jobDef.Commands,
			ExecutionConstraint: jobDef.ExecutionConstraint,
			Interfaces:          make([]*types.JobInterface, 0, len(jobDef.Interfaces)),
			Status:              "pending",
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
				peer := &types.InterfacePeer{
					PeerNodeID:        peerDef.PeerNodeID,
					PeerJobID:         peerDef.PeerJobID,
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
	for _, job := range workflow.Jobs {
		// Check if service exists
		service, err := wm.db.GetService(job.ServiceID)
		if err != nil {
			return fmt.Errorf("service %d not found for job '%s'", job.ServiceID, job.JobName)
		}

		if service == nil {
			return fmt.Errorf("service %d not found for job '%s'", job.ServiceID, job.JobName)
		}

		// Validate service type matches
		if service.ServiceType != job.ServiceType {
			return fmt.Errorf("service type mismatch for job '%s': expected %s, got %s",
				job.JobName, job.ServiceType, service.ServiceType)
		}

		// Validate interfaces
		for _, iface := range job.Interfaces {
			if iface.Type != types.InterfaceTypeStdin &&
			   iface.Type != types.InterfaceTypeStdout &&
			   iface.Type != types.InterfaceTypeMount {
				return fmt.Errorf("invalid interface type '%s' for job '%s'", iface.Type, job.JobName)
			}

			// Validate interface peers
			for _, peer := range iface.InterfacePeers {
				if peer.PeerMountFunction != types.MountFunctionProvider &&
				   peer.PeerMountFunction != types.MountFunctionReceiver {
					return fmt.Errorf("invalid mount function '%s' for job '%s'", peer.PeerMountFunction, job.JobName)
				}
			}
		}
	}

	// TODO: Validate job dependencies (no circular dependencies)
	// TODO: Validate interface compatibility between jobs

	// Validate peer connectivity and service availability
	if err := wm.validatePeerConnectivityAndServices(workflow.Jobs); err != nil {
		return fmt.Errorf("peer connectivity validation failed: %v", err)
	}

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
		wm.logger.Info(fmt.Sprintf("Validating peer %s has service %d for job '%s'",
			check.peerID[:8], check.serviceID, check.jobName), "workflow_manager")

		// Get service details from database
		service, err := wm.db.GetService(check.serviceID)
		if err != nil || service == nil {
			return fmt.Errorf("service %d not found in database for job '%s'", check.serviceID, check.jobName)
		}

		// Try to connect to peer and query for this specific service
		available, err := wm.verifyPeerServiceAvailability(check.peerID, check.serviceID, service.Name)
		if err != nil {
			return fmt.Errorf("failed to verify peer %s for job '%s': %v", check.peerID[:8], check.jobName, err)
		}

		if !available {
			return fmt.Errorf("service '%s' (ID: %d) is not available on peer %s for job '%s'",
				service.Name, check.serviceID, check.peerID[:8], check.jobName)
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
		service, err := wm.db.GetService(job.ServiceID)
		if err != nil || service == nil {
			continue // Already validated in validateWorkflow
		}

		// Only check DATA services
		if service.ServiceType != types.ServiceTypeData {
			continue
		}

		wm.logger.Debug(fmt.Sprintf("Validating data transfer constraints for DATA job '%s'", job.JobName), "workflow_manager")

		// Check all STDOUT interfaces (where data goes)
		for _, iface := range job.Interfaces {
			if iface.Type != types.InterfaceTypeStdout {
				continue
			}

			for _, peer := range iface.InterfacePeers {
				if peer.PeerMountFunction != types.MountFunctionReceiver {
					continue // We only care about receivers (where data goes)
				}

				destPeerID := peer.PeerNodeID

				// Rule 1: Allow transfer to requester (download)
				if destPeerID == requesterPeerID {
					wm.logger.Debug(fmt.Sprintf("DATA job '%s' transfers to requester: OK", job.JobName), "workflow_manager")
					continue
				}

				// Rule 2: Transfer to another peer ONLY if it's input for DOCKER/STANDALONE computation
				// Find the destination job that will receive this data
				destJob := wm.findJobByPeerAndInputPath(jobs, destPeerID, peer.PeerPath)
				if destJob == nil {
					return fmt.Errorf("DATA job '%s' cannot transfer to peer %s: destination is not requester and no computation job found using this data as input",
						job.JobName, destPeerID[:8])
				}

				// Verify destination job is a computation job (DOCKER or STANDALONE)
				destService, err := wm.db.GetService(destJob.ServiceID)
				if err != nil || destService == nil {
					return fmt.Errorf("DATA job '%s': destination service not found for job '%s'", job.JobName, destJob.JobName)
				}

				if destService.ServiceType != types.ServiceTypeDocker && destService.ServiceType != types.ServiceTypeStandalone {
					return fmt.Errorf("DATA job '%s' cannot transfer to peer %s: destination job '%s' is %s, not DOCKER or STANDALONE (data can only be input for computation, not peer-to-peer download)",
						job.JobName, destPeerID[:8], destJob.JobName, destService.ServiceType)
				}

				wm.logger.Debug(fmt.Sprintf("DATA job '%s' transfers to job '%s' (%s) as input: OK",
					job.JobName, destJob.JobName, destService.ServiceType), "workflow_manager")
			}
		}
	}

	wm.logger.Info("Data transfer constraints validated successfully", "workflow_manager")
	return nil
}

// findJobByPeerAndInputPath finds a job that runs on given peer and has given path as STDIN input
func (wm *WorkflowManager) findJobByPeerAndInputPath(jobs []*types.WorkflowJob, peerID string, inputPath string) *types.WorkflowJob {
	for _, job := range jobs {
		if job.ExecutorPeerID != peerID {
			continue
		}

		// Check if this job has the path as STDIN input
		for _, iface := range job.Interfaces {
			if iface.Type != types.InterfaceTypeStdin {
				continue
			}

			// Check if this interface has the matching path
			for _, peer := range iface.InterfacePeers {
				if peer.PeerPath == inputPath && peer.PeerMountFunction == types.MountFunctionProvider {
					return job
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
		// Try to get from database
		workflowJob, err := wm.db.GetWorkflowJobs(workflowJobID)
		if err != nil || len(workflowJob) == 0 {
			return nil, fmt.Errorf("workflow job %d not found", workflowJobID)
		}

		return &types.WorkflowStatus{
			WorkflowID:    workflowJob[0].WorkflowID,
			Status:        workflowJob[0].Status,
			CompletedJobs: 0,
			TotalJobs:     0,
			ErrorMessage:  workflowJob[0].Error,
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
