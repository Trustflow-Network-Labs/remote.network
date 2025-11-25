package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/dependencies"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/services"
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
	dockerService    *services.DockerService
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

	// Initialize logger
	logger := utils.NewLogsManager(cm)

	// Initialize Docker service for job execution
	appPaths := utils.GetAppPaths("")
	gitService := services.NewGitService(logger, cm, appPaths)
	depManager := dependencies.NewDependencyManager(cm, logger)
	dockerService := services.NewDockerService(db, logger, cm, appPaths, depManager, gitService)

	jm := &JobManager{
		ctx:              jobCtx,
		cancel:           cancel,
		db:               db,
		cm:               cm,
		logger:           logger,
		workerPool:       workerPool,
		dataWorker:       dataWorker,
		dockerService:    dockerService,
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
		jm.executeDockerService(job, service)

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

	// Transfer outputs to connected jobs/peers
	appPaths := utils.GetAppPaths("")
	inputDir := filepath.Join(
		appPaths.DataDir,
		"workflows",
		job.OrderingPeerID,
		fmt.Sprintf("%d", job.WorkflowJobID),
		"jobs",
		fmt.Sprintf("%d", job.ID),
		"input",
	)

	err = jm.transferDataServiceOutputs(job, job.WorkflowJobID, inputDir)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to transfer DATA service outputs: %v", err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to transfer outputs: %v", err))
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
	jm.logger.Info(fmt.Sprintf("DATA service job %d completed successfully", job.ID), "job_manager")
}

// executeDockerService executes a DOCKER service job
func (jm *JobManager) executeDockerService(job *database.JobExecution, service *database.OfferedService) {
	jm.logger.Info(fmt.Sprintf("Executing DOCKER service for job %d", job.ID), "job_manager")

	// Get service interfaces
	interfaces, err := jm.db.GetServiceInterfaces(service.ID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get service interfaces for job %d: %v", job.ID, err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to get service interfaces: %v", err))
		return
	}

	// Create workflow directory structure
	// Use workflow_job_id (execution instance ID) for directory isolation
	appPaths := utils.GetAppPaths("")
	workflowJobDir := filepath.Join(
		appPaths.DataDir,
		"workflows",
		job.OrderingPeerID,
		fmt.Sprintf("%d", job.WorkflowJobID), // Use workflow_job_id (execution instance)
		"jobs",
		fmt.Sprintf("%d", job.ID),
	)
	outputDir := filepath.Join(workflowJobDir, "output")
	inputDir := filepath.Join(workflowJobDir, "input")

	// Create directories
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to create output directory: %v", err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to create output directory: %v", err))
		return
	}
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to create input directory: %v", err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to create input directory: %v", err))
		return
	}

	// Get entrypoint and commands - workflow job overrides take precedence over service defaults
	// 1. Start with LOCAL service defaults from docker_service_details
	// 2. Override with workflow job's values if provided (stored in job execution record)
	//
	// This allows users to customize entrypoint/commands per workflow job while keeping
	// service defaults as fallback. The job.Entrypoint/Commands come from the workflow
	// definition (set by the workflow creator), not from the orchestrator's service database.
	dockerDetails, err := jm.db.GetDockerServiceDetails(service.ID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get Docker service details for service %d: %v", service.ID, err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to get Docker service details: %v", err))
		return
	}

	// Start with service defaults
	var entrypoint, commands []string
	if dockerDetails != nil {
		entrypoint, _ = database.UnmarshalStringSlice(dockerDetails.Entrypoint)
		commands, _ = database.UnmarshalStringSlice(dockerDetails.Cmd)
		jm.logger.Info(fmt.Sprintf("Service defaults - entrypoint: %v, commands: %v", entrypoint, commands), "job_manager")
	}

	// Override with workflow job's values if provided
	jobEntrypoint, _ := database.UnmarshalStringSlice(job.Entrypoint)
	jobCommands, _ := database.UnmarshalStringSlice(job.Commands)

	if len(jobEntrypoint) > 0 {
		entrypoint = jobEntrypoint
		jm.logger.Info(fmt.Sprintf("Using workflow job entrypoint override: %v", entrypoint), "job_manager")
	}
	if len(jobCommands) > 0 {
		commands = jobCommands
		jm.logger.Info(fmt.Sprintf("Using workflow job commands override: %v", commands), "job_manager")
	}

	jm.logger.Info(fmt.Sprintf("Final execution config - entrypoint: %v, commands: %v", entrypoint, commands), "job_manager")

	// Build relative output paths
	inputs := []string{}
	outputs := []string{}
	mounts := make(map[string]string)
	envVars := make(map[string]string)

	// Relative path prefix for outputs
	relPathPrefix := filepath.Join("workflows", job.OrderingPeerID, fmt.Sprintf("%d", job.WorkflowJobID), "jobs", fmt.Sprintf("%d", job.ID), "output")

	// Get job interfaces to check for input peers
	jobInterfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to get job interfaces: %v", err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to get job interfaces: %v", err))
		return
	}

	// Process STDIN interfaces - stream file contents to docker STDIN
	for _, jobIface := range jobInterfaces {
		if jobIface.InterfaceType == "STDIN" {
			peers, err := jm.db.GetJobInterfacePeers(jobIface.ID)
			if err == nil && len(peers) > 0 {
				for _, peer := range peers {
					if peer.PeerMountFunction == types.MountFunctionInput || peer.PeerMountFunction == types.MountFunctionBoth {
						// Read file contents from input directory and stream to STDIN
						entries, err := os.ReadDir(inputDir)
						if err != nil {
							jm.logger.Warn(fmt.Sprintf("Failed to read input directory for STDIN: %v", err), "job_manager")
							continue
						}

						for _, entry := range entries {
							if !entry.IsDir() {
								filePath := filepath.Join(inputDir, entry.Name())
								content, err := os.ReadFile(filePath)
								if err != nil {
									jm.logger.Warn(fmt.Sprintf("Failed to read file for STDIN: %v", err), "job_manager")
									continue
								}

								// Add file content to inputs for streaming to STDIN
								inputs = append(inputs, string(content))
								jm.logger.Info(fmt.Sprintf("Adding file content to STDIN: %s (%d bytes)", entry.Name(), len(content)), "job_manager")
							}
						}
						break
					}
				}
			}
		}
	}

	for _, iface := range interfaces {
		switch iface.InterfaceType {
		case "STDIN":
			// STDIN is handled above by reading file contents and streaming to docker STDIN
		case "STDOUT":
			outputs = append(outputs, filepath.Join(relPathPrefix, "stdout.txt"))
		case "STDERR":
			outputs = append(outputs, filepath.Join(relPathPrefix, "stderr.txt"))
		case "LOGS":
			outputs = append(outputs, filepath.Join(relPathPrefix, "logs.txt"))
		case "MOUNT":
			// Get peers to determine mount function
			peers, err := jm.db.GetJobInterfacePeers(iface.ID)
			if err != nil {
				jm.logger.Warn(fmt.Sprintf("Failed to get interface peers for MOUNT: %v", err), "job_manager")
				continue
			}

			// Check if this mount is used for INPUT or BOTH
			hasInputFunction := false
			for _, peer := range peers {
				if peer.PeerMountFunction == types.MountFunctionInput || peer.PeerMountFunction == types.MountFunctionBoth {
					hasInputFunction = true
					break
				}
			}

			if hasInputFunction && iface.Path != "" {
				// Mount for INPUT: mount the mounts directory containing input files
				hostPath := filepath.Join(workflowJobDir, "mounts", filepath.Base(iface.Path))

				// Ensure the directory exists with world-writable permissions
				// Docker containers may run as different users, so we need 0777
				if err := os.MkdirAll(hostPath, 0777); err != nil {
					jm.logger.Error(fmt.Sprintf("Failed to create INPUT mount directory %s: %v", hostPath, err), "job_manager")
					jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to create INPUT mount directory: %v", err))
					return
				}
				// Ensure permissions are correct even if directory already existed
				os.Chmod(hostPath, 0777)

				mounts[hostPath] = iface.Path
				jm.logger.Info(fmt.Sprintf("Adding INPUT mount: %s -> %s", hostPath, iface.Path), "job_manager")
			} else if iface.Path != "" {
				// Mount for OUTPUT: mount the mounts directory for output files
				hostPath := filepath.Join(workflowJobDir, "mounts", filepath.Base(iface.Path))

				// Ensure the directory exists with world-writable permissions
				// Docker containers may run as different users, so we need 0777
				if err := os.MkdirAll(hostPath, 0777); err != nil {
					jm.logger.Error(fmt.Sprintf("Failed to create OUTPUT mount directory %s: %v", hostPath, err), "job_manager")
					jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to create OUTPUT mount directory: %v", err))
					return
				}
				// Ensure permissions are correct even if directory already existed
				os.Chmod(hostPath, 0777)

				mounts[hostPath] = iface.Path
				jm.logger.Info(fmt.Sprintf("Adding OUTPUT mount: %s -> %s", hostPath, iface.Path), "job_manager")
			}
		}
	}

	// Create execution config
	config := &services.ExecutionConfig{
		ServiceID:      service.ID,
		JobExecutionID: job.ID,
		Entrypoint:     entrypoint,
		Commands:       commands,
		Inputs:         inputs,
		Outputs:        outputs,
		Mounts:         mounts,
		EnvVars:        envVars,
		Timeout:        10 * time.Minute,
	}

	// Execute container
	jm.logger.Info(fmt.Sprintf("Executing Docker container for job %d", job.ID), "job_manager")
	result, err := jm.dockerService.ExecuteService(config)
	if err != nil {
		jm.logger.Error(fmt.Sprintf("DOCKER service execution failed: %v", err), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("DOCKER execution failed: %v", err))
		return
	}

	// Check exit code
	if result.ExitCode != 0 {
		jm.logger.Error(fmt.Sprintf("Container exited with code %d", result.ExitCode), "job_manager")
		jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Container exited with code %d", result.ExitCode))
		return
	}

	// Transfer outputs to connected jobs/peers
	if len(outputs) > 0 {
		err = jm.transferDockerOutputs(job, job.WorkflowJobID, outputDir)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to transfer outputs: %v", err), "job_manager")
			jm.handleJobError(job.ID, job.WorkflowJobID, fmt.Sprintf("Failed to transfer outputs: %v", err))
			return
		}
	}

	// Update status to COMPLETED
	err = jm.db.UpdateJobStatus(job.ID, types.JobStatusCompleted, "")
	if err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to update job status: %v", err), "job_manager")
		return
	}

	jm.sendStatusUpdate(job.ID, job.WorkflowJobID, types.JobStatusCompleted, "")
	jm.logger.Info(fmt.Sprintf("DOCKER service job %d completed successfully", job.ID), "job_manager")
}

// transferDockerOutputs transfers Docker job outputs to connected jobs/peers
// Handles STDOUT, STDERR, LOGS, and MOUNT outputs (where PeerMountFunction is OUTPUT or BOTH)
// For remote peers, creates a transfer package with all outputs bundled together
func (jm *JobManager) transferDockerOutputs(job *database.JobExecution, workflowID int64, outputDir string) error {
	jm.logger.Info(fmt.Sprintf("Transferring outputs for job %d", job.ID), "job_manager")

	// Get job interfaces to find output connections
	jobInterfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		return fmt.Errorf("failed to get job interfaces: %w", err)
	}

	// Get workflow job directory for mount points
	appPaths := utils.GetAppPaths("")
	workflowJobDir := filepath.Join(
		appPaths.DataDir,
		"workflows",
		job.OrderingPeerID,
		fmt.Sprintf("%d", workflowID),
		"jobs",
		fmt.Sprintf("%d", job.ID),
	)

	localPeerID := jm.peerManager.GetPeerID()

	// Collect outputs by destination peer for remote transfers
	// Structure: peerID -> list of outputs to send
	type outputInfo struct {
		interfaceType string
		sourcePath    string
		destPath      string
		mountPath     string // For MOUNT interfaces
		peer          *database.JobInterfacePeer
	}
	remoteOutputs := make(map[string][]outputInfo)

	// Process each output interface
	for _, iface := range jobInterfaces {
		var outputFilePath string
		var outputFileName string
		isOutputInterface := false

		// Determine if this is an output interface and get the file path
		switch iface.InterfaceType {
		case "STDOUT":
			outputFileName = "stdout.txt"
			outputFilePath = filepath.Join(outputDir, outputFileName)
			isOutputInterface = true

		case "STDERR":
			outputFileName = "stderr.txt"
			outputFilePath = filepath.Join(outputDir, outputFileName)
			isOutputInterface = true

		case "LOGS":
			outputFileName = "logs.txt"
			outputFilePath = filepath.Join(outputDir, outputFileName)
			isOutputInterface = true

		case "MOUNT":
			// Get peers to check if any are OUTPUT or BOTH
			mountPeers, err := jm.db.GetJobInterfacePeers(iface.ID)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to get interface peers for MOUNT: %v", err), "job_manager")
				continue
			}

			// Check if any peer is OUTPUT or BOTH
			hasOutputPeer := false
			for _, peer := range mountPeers {
				if peer.PeerMountFunction == types.MountFunctionOutput || peer.PeerMountFunction == types.MountFunctionBoth {
					hasOutputPeer = true
					break
				}
			}

			if hasOutputPeer && iface.Path != "" {
				// This is a MOUNT used for output - iterate through all files in the mount directory
				mountDir := filepath.Join(workflowJobDir, "mounts", filepath.Base(iface.Path))

				// Check if mount directory exists
				if _, err := os.Stat(mountDir); os.IsNotExist(err) {
					jm.logger.Warn(fmt.Sprintf("MOUNT output directory not found: %s", mountDir), "job_manager")
					continue
				}

				// Read all files in the mount directory
				entries, err := os.ReadDir(mountDir)
				if err != nil {
					jm.logger.Warn(fmt.Sprintf("Failed to read MOUNT directory: %v", err), "job_manager")
					continue
				}

				// Collect mount files for each receiver peer
				for _, entry := range entries {
					if entry.IsDir() {
						continue // Skip subdirectories for now
					}

					mountFilePath := filepath.Join(mountDir, entry.Name())
					mountFileName := entry.Name()

					for _, peer := range mountPeers {
						// Only transfer if peer is OUTPUT or BOTH
						if peer.PeerMountFunction != types.MountFunctionOutput && peer.PeerMountFunction != types.MountFunctionBoth {
							continue
						}

						// Check if this is a local transfer (same peer)
						jm.logger.Info(fmt.Sprintf("Checking MOUNT output destination: peer.PeerNodeID=%s, localPeerID=%s, OrderingPeerID=%s",
							peer.PeerNodeID[:8], localPeerID[:8], job.OrderingPeerID[:8]), "job_manager")
						if peer.PeerNodeID == localPeerID {
							jm.logger.Info(fmt.Sprintf("Transferring MOUNT file %s locally", mountFileName), "job_manager")
							err = jm.transferOutputLocally(job, workflowID, peer, mountFilePath, mountFileName)
							if err != nil {
								jm.logger.Error(fmt.Sprintf("Failed to transfer MOUNT file locally: %v", err), "job_manager")
							}
						} else {
							// Collect for remote package transfer
							destPath := filepath.Join("mounts", filepath.Base(peer.PeerPath), mountFileName)
							remoteOutputs[peer.PeerNodeID] = append(remoteOutputs[peer.PeerNodeID], outputInfo{
								interfaceType: iface.InterfaceType,
								sourcePath:    mountFilePath,
								destPath:      destPath,
								mountPath:     iface.Path,
								peer:          peer,
							})
						}
					}
				}
			}
			continue // MOUNT handled, skip the common transfer logic below
		}

		if !isOutputInterface {
			continue
		}

		// Get peers that should receive this output (for STDOUT/STDERR/LOGS)
		peers, err := jm.db.GetJobInterfacePeers(iface.ID)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to get interface peers: %v", err), "job_manager")
			continue
		}

		// Check if output file exists
		if _, err := os.Stat(outputFilePath); os.IsNotExist(err) {
			jm.logger.Warn(fmt.Sprintf("Output path not found: %s", outputFilePath), "job_manager")
			continue
		}

		// Collect for each receiver peer (for STDOUT/STDERR/LOGS)
		for _, peer := range peers {
			// For STD interfaces, only transfer to OUTPUT peers
			if peer.PeerMountFunction != types.MountFunctionOutput {
				continue
			}

			// Check if this is a local transfer (same peer)
			jm.logger.Info(fmt.Sprintf("Checking %s output destination: peer.PeerNodeID=%s, localPeerID=%s, OrderingPeerID=%s",
				iface.InterfaceType, peer.PeerNodeID[:8], localPeerID[:8], job.OrderingPeerID[:8]), "job_manager")
			if peer.PeerNodeID == localPeerID {
				jm.logger.Info(fmt.Sprintf("Transferring %s locally", iface.InterfaceType), "job_manager")
				err = jm.transferOutputLocally(job, workflowID, peer, outputFilePath, outputFileName)
				if err != nil {
					jm.logger.Error(fmt.Sprintf("Failed to transfer %s locally: %v", iface.InterfaceType, err), "job_manager")
				}
			} else {
				// Collect for remote package transfer
				destPath := filepath.Join("input", outputFileName)
				remoteOutputs[peer.PeerNodeID] = append(remoteOutputs[peer.PeerNodeID], outputInfo{
					interfaceType: iface.InterfaceType,
					sourcePath:    outputFilePath,
					destPath:      destPath,
					peer:          peer,
				})
			}
		}
	}

	// Now create and send transfer packages for remote peers
	for peerID, outputs := range remoteOutputs {
		if len(outputs) == 0 {
			continue
		}

		jm.logger.Info(fmt.Sprintf("Building transfer package for peer %s with %d outputs", peerID[:8], len(outputs)), "job_manager")

		// Create transfer package builder
		builder, err := utils.NewTransferPackageBuilder(
			localPeerID,
			peerID,
			workflowID,
			job.ID,
			jm.logger,
		)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to create transfer package builder for peer %s: %v", peerID[:8], err), "job_manager")
			continue
		}

		// Add all outputs to the package
		for _, output := range outputs {
			switch output.interfaceType {
			case types.InterfaceTypeStdout, types.InterfaceTypeStderr, types.InterfaceTypeLogs:
				err = builder.AddStdOutput(output.interfaceType, output.sourcePath, output.destPath)
			case types.InterfaceTypeMount:
				err = builder.AddMountOutput(output.mountPath, output.sourcePath, output.destPath)
			}

			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to add output to package: %v", err), "job_manager")
			}
		}

		// Build the package
		packagePath, err := builder.Build()
		if err != nil {
			builder.Cleanup()
			jm.logger.Error(fmt.Sprintf("Failed to build transfer package for peer %s: %v", peerID[:8], err), "job_manager")
			continue
		}

		// Use the first peer info for the transfer (they all go to the same peer)
		peer := outputs[0].peer

		// Send the package
		jm.logger.Info(fmt.Sprintf("Sending transfer package to peer %s", peerID[:8]), "job_manager")
		err = jm.transferPackageRemotely(job, peer, packagePath)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to transfer package to peer %s: %v", peerID[:8], err), "job_manager")
		}

		// Cleanup temp files
		builder.Cleanup()
	}

	return nil
}

// transferDataServiceOutputs transfers DATA service outputs to connected jobs/peers
// For DATA services, files in the input directory are treated as STDOUT outputs
func (jm *JobManager) transferDataServiceOutputs(job *database.JobExecution, workflowID int64, inputDir string) error {
	jm.logger.Info(fmt.Sprintf("Transferring DATA service outputs for job %d", job.ID), "job_manager")

	// Get job interfaces to find STDOUT connections
	jobInterfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		return fmt.Errorf("failed to get job interfaces: %w", err)
	}

	// Find STDOUT interface
	for _, iface := range jobInterfaces {
		if iface.InterfaceType != "STDOUT" {
			continue
		}

		// Get peers that should receive this output
		peers, err := jm.db.GetJobInterfacePeers(iface.ID)
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to get interface peers: %v", err), "job_manager")
			continue
		}

		// Get all files in the input directory (these are the data files to transfer)
		entries, err := os.ReadDir(inputDir)
		if err != nil {
			jm.logger.Warn(fmt.Sprintf("Failed to read input directory: %v", err), "job_manager")
			continue
		}

		// Transfer each file to each receiver peer
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			outputFilePath := filepath.Join(inputDir, entry.Name())
			outputFileName := entry.Name()

			for _, peer := range peers {
				jm.logger.Info(fmt.Sprintf("Transferring DATA file %s to peer %s", outputFileName, peer.PeerNodeID[:8]), "job_manager")

				// Check if this is a local transfer (same peer)
				if peer.PeerNodeID == jm.peerManager.GetPeerID() {
					// Local copy: input/ -> receiving job's input/
					err = jm.transferOutputLocally(job, workflowID, peer, outputFilePath, outputFileName)
				} else {
					// Remote P2P transfer
					err = jm.transferOutputRemotely(job, peer, outputFilePath, iface.InterfaceType)
				}

				if err != nil {
					jm.logger.Error(fmt.Sprintf("Failed to transfer output to peer %s: %v", peer.PeerNodeID[:8], err), "job_manager")
					// Continue with other transfers
				}
			}
		}
	}

	return nil
}

// transferOutputLocally moves output file to receiving job's input directory
// If PeerJobID is nil (Local Peer/requester), no transfer is needed - files stay in place
// Otherwise, it goes to the receiving job's input/mounts directory
// IMPORTANT: This should only be called when the destination peer is the same as the local peer.
// For remote destinations, use transferOutputRemotely or transferOutputPackageRemotely instead.
func (jm *JobManager) transferOutputLocally(job *database.JobExecution, workflowID int64, peer *database.JobInterfacePeer, outputFilePath, fileName string) error {
	appPaths := utils.GetAppPaths("")
	var destPath string

	localPeerID := jm.peerManager.GetPeerID()

	// Check if this is a transfer to the requester (no receiving job) or to another job
	if peer.PeerJobID == nil || *peer.PeerJobID == 0 {
		// Destination is "Local Peer" (requester/orchestrator)
		// Files stay in place ONLY if the requester is the local peer (same machine)
		// If the job ran remotely and outputs need to go back to the orchestrator,
		// this is an error - should have been handled by the remote transfer path
		if job.OrderingPeerID != localPeerID {
			// The orchestrator is a remote peer - this shouldn't happen
			// Log a warning - the caller should have routed this to remote transfer
			jm.logger.Warn(fmt.Sprintf("Output %s destined for remote orchestrator %s but called transferOutputLocally - this may indicate a routing bug",
				outputFilePath, job.OrderingPeerID[:8]), "job_manager")
		}
		jm.logger.Info(fmt.Sprintf("Output %s stays in place for Local Peer (requester)", outputFilePath), "job_manager")
		return nil
	} else {
		// Transfer to another job's input
		receivingJobID := *peer.PeerJobID

		// Determine destination based on peer interface type
		// If peer_path is empty, it's a stream interface (STDIN) -> use input directory
		// If peer_path is set, it's a MOUNT interface -> use mounts directory
		if peer.PeerPath != "" {
			// MOUNT interface: transfer to mounts/<basename(peer_path)>/{fileName}
			destPath = filepath.Join(
				appPaths.DataDir,
				"workflows",
				job.OrderingPeerID,
				fmt.Sprintf("%d", workflowID),
				"jobs",
				fmt.Sprintf("%d", receivingJobID),
				"mounts",
				filepath.Base(peer.PeerPath),
				fileName,
			)
			jm.logger.Info(fmt.Sprintf("Transferring to MOUNT interface at %s", destPath), "job_manager")
		} else {
			// Stream interface (STDIN): transfer to input directory
			destPath = filepath.Join(
				appPaths.DataDir,
				"workflows",
				job.OrderingPeerID,
				fmt.Sprintf("%d", workflowID),
				"jobs",
				fmt.Sprintf("%d", receivingJobID),
				"input",
				fileName,
			)
			jm.logger.Info(fmt.Sprintf("Transferring to stream interface (input directory) at %s", destPath), "job_manager")
		}
	}

	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Check if source file exists
	if _, err := os.Stat(outputFilePath); os.IsNotExist(err) {
		return fmt.Errorf("source file not found: %s", outputFilePath)
	}

	// Move file (more efficient than copy for local transfers)
	jm.logger.Info(fmt.Sprintf("Moving %s to %s", outputFilePath, destPath), "job_manager")

	err := os.Rename(outputFilePath, destPath)
	if err != nil {
		// If rename fails (e.g., cross-device), fall back to copy+delete
		jm.logger.Warn(fmt.Sprintf("Move failed, falling back to copy: %v", err), "job_manager")

		sourceFile, err := os.Open(outputFilePath)
		if err != nil {
			return fmt.Errorf("failed to open source file: %w", err)
		}
		defer sourceFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create destination file: %w", err)
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, sourceFile)
		if err != nil {
			return fmt.Errorf("failed to copy file: %w", err)
		}

		// Delete source after successful copy
		if err := os.Remove(outputFilePath); err != nil {
			jm.logger.Warn(fmt.Sprintf("Failed to remove source file after copy: %v", err), "job_manager")
		}
	}

	jm.logger.Info(fmt.Sprintf("Successfully transferred output locally to %s", destPath), "job_manager")
	return nil
}

// generateTransferPassphrase generates a one-time passphrase and encryption key for secure transfers
func (jm *JobManager) generateTransferPassphrase() (passphrase string, keyData []byte, err error) {
	// Generate a random 32-byte passphrase
	passphraseBytes := make([]byte, 32)
	if _, err := rand.Read(passphraseBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate passphrase: %w", err)
	}
	passphrase = hex.EncodeToString(passphraseBytes)

	// Derive AES-256 key using PBKDF2
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive key using PBKDF2 with SHA-256
	keyData = pbkdf2.Key([]byte(passphrase), salt, 100000, 32, sha256.New)

	// Prepend salt to keyData (first 16 bytes = salt, rest = derived key)
	fullKeyData := append(salt, keyData...)

	return passphrase, fullKeyData, nil
}

// encryptFileStreaming encrypts a file using AES-256-GCM in chunks (memory-efficient)
func (jm *JobManager) encryptFileStreaming(inputPath, outputPath string, keyData []byte) error {
	// Extract key from keyData (skip first 16 bytes which is salt)
	if len(keyData) < 48 {
		return fmt.Errorf("invalid key data length")
	}
	key := keyData[16:48]

	// Open input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Write nonce to output file first
	if _, err := outputFile.Write(nonce); err != nil {
		return fmt.Errorf("failed to write nonce: %w", err)
	}

	// Encrypt file in chunks (64KB chunks for streaming)
	const chunkSize = 64 * 1024 // 64KB chunks
	buffer := make([]byte, chunkSize)
	counter := uint64(0) // Counter for GCM nonce variation

	for {
		n, err := inputFile.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read chunk: %w", err)
		}
		if n == 0 {
			break
		}

		// Create unique nonce for this chunk by XORing with counter
		chunkNonce := make([]byte, len(nonce))
		copy(chunkNonce, nonce)
		for i := 0; i < 8 && i < len(chunkNonce); i++ {
			chunkNonce[i] ^= byte(counter >> (i * 8))
		}

		// Encrypt chunk
		cipherChunk := gcm.Seal(nil, chunkNonce, buffer[:n], nil)

		// Write encrypted chunk to output
		if _, err := outputFile.Write(cipherChunk); err != nil {
			return fmt.Errorf("failed to write encrypted chunk: %w", err)
		}

		counter++
	}

	return nil
}

// transferOutputRemotely transfers output file to a remote peer via P2P with compression and encryption
func (jm *JobManager) transferOutputRemotely(job *database.JobExecution, peer *database.JobInterfacePeer, outputFilePath, interfaceType string) error {
	jm.logger.Info(fmt.Sprintf("Preparing secure transfer of %s to peer %s", outputFilePath, peer.PeerNodeID[:8]), "job_manager")

	// Step 1: Create temporary directory for processing
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("docker-output-%d-%d", job.ID, time.Now().Unix()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	// Step 2: Compress output file to tar.gz
	compressedPath := filepath.Join(tempDir, "output.tar.gz")
	jm.logger.Info(fmt.Sprintf("Compressing %s to tar.gz", outputFilePath), "job_manager")

	if err := utils.Compress(outputFilePath, compressedPath); err != nil {
		return fmt.Errorf("failed to compress output: %w", err)
	}

	// Step 3: Generate one-time passphrase for encryption
	passphrase, keyData, err := jm.generateTransferPassphrase()
	if err != nil {
		return fmt.Errorf("failed to generate passphrase: %w", err)
	}
	jm.logger.Info("Generated one-time encryption passphrase for transfer", "job_manager")

	// Step 4: Encrypt the compressed archive
	encryptedPath := filepath.Join(tempDir, "output.tar.gz.encrypted")
	jm.logger.Info("Encrypting compressed archive", "job_manager")

	if err := jm.encryptFileStreaming(compressedPath, encryptedPath, keyData); err != nil {
		return fmt.Errorf("failed to encrypt output: %w", err)
	}

	// Step 5: Get encrypted file info
	fileInfo, err := os.Stat(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to get encrypted file info: %w", err)
	}
	encryptedSize := fileInfo.Size()

	// Step 6: Calculate hash of encrypted file
	hash, err := utils.HashFileToCID(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	jm.logger.Info(fmt.Sprintf("Encrypted output ready: %d bytes", encryptedSize), "job_manager")

	// Step 7: Open encrypted file for streaming
	file, err := os.Open(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to open encrypted file: %w", err)
	}
	defer file.Close()

	chunkSize := 1024 * 1024 // 1MB chunks
	totalChunks := int((encryptedSize + int64(chunkSize) - 1) / int64(chunkSize))

	// Step 8: Generate transfer ID
	transferID := jm.dataWorker.GenerateTransferID(job.ID, peer.PeerNodeID)

	// Step 9: Get job handler
	jobHandler := jm.peerManager.GetJobHandler()
	if jobHandler == nil {
		return fmt.Errorf("job handler not available")
	}

	// Step 10: Send transfer request with passphrase in expected format
	// Format: "passphrase|hexkeydata" (same format as DATA services)
	keyDataFormatted := fmt.Sprintf("%s|%s", passphrase, hex.EncodeToString(keyData))

	// Get destination job execution ID from peer record
	var destJobExecID int64
	if peer.PeerJobID != nil {
		destJobExecID = *peer.PeerJobID
	}

	transferRequest := &types.JobDataTransferRequest{
		TransferID:                transferID,
		WorkflowJobID:             job.WorkflowJobID,
		DestinationJobExecutionID: destJobExecID,
		InterfaceType:             interfaceType,
		SourcePeerID:              jm.peerManager.GetPeerID(),
		DestinationPeerID:         peer.PeerNodeID,
		SourcePath:                outputFilePath,
		DestinationPath:           peer.PeerPath,
		DataHash:                  hash,
		SizeBytes:                 encryptedSize,
		Passphrase:                keyDataFormatted, // Format: passphrase|hexkeydata
		Encrypted:                 true,             // Data is encrypted
	}

	jm.logger.Info(fmt.Sprintf("Sending encrypted transfer request for transfer ID %s to peer %s",
		transferID, peer.PeerNodeID[:8]), "job_manager")

	response, err := jobHandler.SendJobDataTransferRequest(peer.PeerNodeID, transferRequest)
	if err != nil {
		return fmt.Errorf("failed to send transfer request: %w", err)
	}

	if !response.Accepted {
		return fmt.Errorf("transfer rejected: %s", response.Message)
	}

	jm.logger.Info(fmt.Sprintf("Transfer request accepted by peer %s for transfer ID %s", peer.PeerNodeID[:8], transferID), "job_manager")

	// Step 11: Send encrypted file in chunks
	buffer := make([]byte, chunkSize)
	chunkIndex := 0

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file chunk: %w", err)
		}

		if n == 0 {
			break
		}

		isLast := chunkIndex == totalChunks-1

		// Send data chunk
		chunk := &types.JobDataChunk{
			TransferID:  transferID,
			ChunkIndex:  chunkIndex,
			TotalChunks: totalChunks,
			Data:        buffer[:n],
			IsLast:      isLast,
		}

		jm.logger.Info(fmt.Sprintf("Sending chunk %d/%d (%d bytes) for transfer %s",
			chunkIndex+1, totalChunks, n, transferID), "job_manager")

		// Retry logic: try up to 3 times with exponential backoff
		sent := false
		for attempt := 0; attempt < 3; attempt++ {
			err := jobHandler.SendJobDataChunk(peer.PeerNodeID, chunk)
			if err == nil {
				sent = true
				break
			}

			jm.logger.Warn(fmt.Sprintf("Failed to send chunk %d (attempt %d/3): %v", chunkIndex, attempt+1, err), "job_manager")

			// Exponential backoff: 100ms, 200ms, 400ms
			if attempt < 2 {
				time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
			}
		}

		if !sent {
			return fmt.Errorf("failed to send chunk %d after 3 attempts", chunkIndex)
		}

		chunkIndex++
	}

	jm.logger.Info(fmt.Sprintf("Successfully sent all %d chunks for transfer %s (encrypted)", totalChunks, transferID), "job_manager")

	return nil
}

// transferPackageRemotely transfers a pre-built transfer package to a remote peer via P2P with encryption
// The package is already compressed (tar.gz), so we only encrypt and send it
func (jm *JobManager) transferPackageRemotely(job *database.JobExecution, peer *database.JobInterfacePeer, packagePath string) error {
	jm.logger.Info(fmt.Sprintf("Preparing secure transfer of package to peer %s", peer.PeerNodeID[:8]), "job_manager")

	// Create temporary directory for encrypted file
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("transfer-pkg-%d-%d", job.ID, time.Now().Unix()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Generate one-time passphrase for encryption
	passphrase, keyData, err := jm.generateTransferPassphrase()
	if err != nil {
		return fmt.Errorf("failed to generate passphrase: %w", err)
	}
	jm.logger.Info("Generated one-time encryption passphrase for package transfer", "job_manager")

	// Encrypt the package (already compressed)
	encryptedPath := filepath.Join(tempDir, "package.tar.gz.encrypted")
	jm.logger.Info("Encrypting transfer package", "job_manager")

	if err := jm.encryptFileStreaming(packagePath, encryptedPath, keyData); err != nil {
		return fmt.Errorf("failed to encrypt package: %w", err)
	}

	// Get encrypted file info
	fileInfo, err := os.Stat(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to get encrypted file info: %w", err)
	}
	encryptedSize := fileInfo.Size()

	// Calculate hash of encrypted file
	hash, err := utils.HashFileToCID(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	jm.logger.Info(fmt.Sprintf("Encrypted package ready: %d bytes", encryptedSize), "job_manager")

	// Open encrypted file for streaming
	file, err := os.Open(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to open encrypted file: %w", err)
	}
	defer file.Close()

	chunkSize := 1024 * 1024 // 1MB chunks
	totalChunks := int((encryptedSize + int64(chunkSize) - 1) / int64(chunkSize))

	// Generate transfer ID
	transferID := jm.dataWorker.GenerateTransferID(job.ID, peer.PeerNodeID)

	// Get job handler
	jobHandler := jm.peerManager.GetJobHandler()
	if jobHandler == nil {
		return fmt.Errorf("job handler not available")
	}

	// Format passphrase as expected: "passphrase|hexkeydata"
	keyDataFormatted := fmt.Sprintf("%s|%s", passphrase, hex.EncodeToString(keyData))

	// Determine destination path - use "input/" for transfer packages
	destPath := peer.PeerPath
	if destPath == "" {
		destPath = "input/"
	}

	// Get destination job execution ID from peer record
	var destJobExecID int64
	if peer.PeerJobID != nil {
		destJobExecID = *peer.PeerJobID
	}

	// Send transfer request - use PACKAGE interface type to indicate this is a transfer package
	transferRequest := &types.JobDataTransferRequest{
		TransferID:                transferID,
		WorkflowJobID:             job.WorkflowJobID,
		DestinationJobExecutionID: destJobExecID,
		InterfaceType:             "PACKAGE", // Special type indicating transfer package
		SourcePeerID:              jm.peerManager.GetPeerID(),
		DestinationPeerID:         peer.PeerNodeID,
		SourcePath:                packagePath,
		DestinationPath:           destPath,
		DataHash:                  hash,
		SizeBytes:                 encryptedSize,
		Passphrase:                keyDataFormatted,
		Encrypted:                 true,
	}

	jm.logger.Info(fmt.Sprintf("Sending transfer package request for transfer ID %s to peer %s",
		transferID, peer.PeerNodeID[:8]), "job_manager")

	response, err := jobHandler.SendJobDataTransferRequest(peer.PeerNodeID, transferRequest)
	if err != nil {
		return fmt.Errorf("failed to send transfer request: %w", err)
	}

	if !response.Accepted {
		return fmt.Errorf("transfer rejected: %s", response.Message)
	}

	jm.logger.Info(fmt.Sprintf("Transfer package request accepted by peer %s for transfer ID %s",
		peer.PeerNodeID[:8], transferID), "job_manager")

	// Send encrypted file in chunks
	buffer := make([]byte, chunkSize)
	chunkIndex := 0

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file chunk: %w", err)
		}

		if n == 0 {
			break
		}

		isLast := chunkIndex == totalChunks-1

		// Send data chunk
		chunk := &types.JobDataChunk{
			TransferID:  transferID,
			ChunkIndex:  chunkIndex,
			TotalChunks: totalChunks,
			Data:        buffer[:n],
			IsLast:      isLast,
		}

		jm.logger.Info(fmt.Sprintf("Sending package chunk %d/%d (%d bytes) for transfer %s",
			chunkIndex+1, totalChunks, n, transferID), "job_manager")

		// Retry logic: try up to 3 times with exponential backoff
		sent := false
		for attempt := 0; attempt < 3; attempt++ {
			err := jobHandler.SendJobDataChunk(peer.PeerNodeID, chunk)
			if err == nil {
				sent = true
				break
			}

			jm.logger.Warn(fmt.Sprintf("Failed to send chunk %d (attempt %d/3): %v", chunkIndex, attempt+1, err), "job_manager")

			if attempt < 2 {
				time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
			}
		}

		if !sent {
			return fmt.Errorf("failed to send chunk %d after 3 attempts", chunkIndex)
		}

		chunkIndex++
	}

	jm.logger.Info(fmt.Sprintf("Successfully sent all %d chunks for package transfer %s", totalChunks, transferID), "job_manager")

	return nil
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
	return jm.SubmitJobWithOptions(request, nil, false)
}

// SubmitJobWithOptions creates and submits a job with optional node_id to job_execution_id mapping
// The mapping is used to fix PeerJobID values (which are initially set to workflow_node IDs)
// to the actual job_execution IDs.
// If skipInterfaces is true, only the job execution is created (interfaces are skipped for first pass).
func (jm *JobManager) SubmitJobWithOptions(request *types.JobExecutionRequest, nodeIDToJobIDMap map[int64]int64, skipInterfaces bool) (*database.JobExecution, error) {
	jm.logger.Info(fmt.Sprintf("Submitting job for workflow job %d (skipInterfaces: %v)", request.WorkflowJobID, skipInterfaces), "job_manager")

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

	// Skip interface creation if requested (used in first pass of two-phase job submission)
	if skipInterfaces {
		jm.logger.Debug(fmt.Sprintf("Skipping interface creation for job %d (first pass)", job.ID), "job_manager")
		return job, nil
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
			// Fix PeerJobID if mapping is provided
			// PeerJobID is initially set to workflow_node ID, we need to map it to job_execution ID
			peerJobID := peer.PeerJobID
			if nodeIDToJobIDMap != nil && peer.PeerJobID != nil {
				if mappedID, ok := nodeIDToJobIDMap[*peer.PeerJobID]; ok {
					peerJobID = &mappedID
					jm.logger.Debug(fmt.Sprintf("Mapped PeerJobID: node_id %d -> job_execution_id %d",
						*peer.PeerJobID, mappedID), "job_manager")
				}
			}

			jobInterfacePeer := &database.JobInterfacePeer{
				JobInterfaceID:    jobInterface.ID,
				PeerNodeID:        peer.PeerNodeID,
				PeerJobID:         peerJobID,
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
		// Only check local filesystem for local execution jobs
		// For remote jobs, set to READY immediately so they can be dispatched to the executor
		// The executor peer will do its own input checking when it receives the job
		if isLocalExecution {
			// Check if inputs are ready on file system
			ready, err := jm.checkInputsReady(job)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to check inputs for job %d: %v", job.ID, err), "job_manager")
				initialStatus = types.JobStatusIdle
			} else if !ready {
				initialStatus = types.JobStatusIdle
			}
		} else {
			jm.logger.Info(fmt.Sprintf("Job %d is for remote execution on peer %s, setting to READY for dispatch",
				job.ID, request.ExecutorPeerID[:8]), "job_manager")
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

// CreateJobInterfaces creates interfaces for an existing job execution (used in second pass of two-phase submission)
func (jm *JobManager) CreateJobInterfaces(job *database.JobExecution, interfaces []*types.JobInterface, nodeIDToJobIDMap map[int64]int64) error {
	jm.logger.Info(fmt.Sprintf("Creating interfaces for job %d with mapping", job.ID), "job_manager")

	// Create job interfaces
	for _, iface := range interfaces {
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

		// Create interface peers with corrected PeerJobID
		for _, peer := range iface.InterfacePeers {
			// Fix PeerJobID using the mapping
			peerJobID := peer.PeerJobID
			if nodeIDToJobIDMap != nil && peer.PeerJobID != nil {
				if mappedID, ok := nodeIDToJobIDMap[*peer.PeerJobID]; ok {
					peerJobID = &mappedID
					jm.logger.Debug(fmt.Sprintf("Mapped PeerJobID: node_id %d -> job_execution_id %d",
						*peer.PeerJobID, mappedID), "job_manager")
				}
			}

			jobInterfacePeer := &database.JobInterfacePeer{
				JobInterfaceID:    jobInterface.ID,
				PeerNodeID:        peer.PeerNodeID,
				PeerJobID:         peerJobID,
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

	// Check and update job status based on execution constraint
	isLocalExecution := job.ExecutorPeerID == jm.peerManager.GetPeerID()

	if job.ExecutionConstraint == types.ExecutionConstraintInputsReady {
		// Only check local filesystem for local execution jobs
		// For remote jobs, set to READY immediately so they can be dispatched to the executor
		if isLocalExecution {
			ready, err := jm.checkInputsReady(job)
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to check inputs for job %d: %v", job.ID, err), "job_manager")
			} else if ready {
				err = jm.db.UpdateJobStatus(job.ID, types.JobStatusReady, "")
				if err != nil {
					jm.logger.Error(fmt.Sprintf("Failed to update job %d status: %v", job.ID, err), "job_manager")
				} else {
					job.Status = types.JobStatusReady
					jm.logger.Info(fmt.Sprintf("Job %d status updated to READY (inputs ready)", job.ID), "job_manager")
				}
			}
		} else {
			// Remote execution job - set to READY for dispatch
			err := jm.db.UpdateJobStatus(job.ID, types.JobStatusReady, "")
			if err != nil {
				jm.logger.Error(fmt.Sprintf("Failed to update job %d status: %v", job.ID, err), "job_manager")
			} else {
				job.Status = types.JobStatusReady
				jm.logger.Info(fmt.Sprintf("Job %d is for remote execution on peer %s, set to READY for dispatch",
					job.ID, job.ExecutorPeerID[:8]), "job_manager")
			}
		}
	} else {
		// No input constraints, set to READY
		err := jm.db.UpdateJobStatus(job.ID, types.JobStatusReady, "")
		if err != nil {
			jm.logger.Error(fmt.Sprintf("Failed to update job %d status: %v", job.ID, err), "job_manager")
		} else {
			job.Status = types.JobStatusReady
		}
	}

	jm.logger.Info(fmt.Sprintf("Interfaces created for job %d", job.ID), "job_manager")
	return nil
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
	jm.logger.Info(fmt.Sprintf("Handling data transfer request from peer %s for workflow job %d (dest job exec ID: %d)",
		peerID[:8], request.WorkflowJobID, request.DestinationJobExecutionID), "job_manager")

	// Determine which job should receive this data
	var destJob *database.JobExecution
	var err error

	// If sender provided destination job execution ID, use that directly
	if request.DestinationJobExecutionID > 0 {
		destJob, err = jm.db.GetJobExecution(request.DestinationJobExecutionID)
		if err != nil || destJob == nil {
			jm.logger.Warn(fmt.Sprintf("Destination job %d not found, falling back to source peer lookup: %v",
				request.DestinationJobExecutionID, err), "job_manager")
		}
	}

	// Fallback: Look up by workflow_job_id and find sender's job (old behavior for backward compatibility)
	if destJob == nil {
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
		// NOTE: This is the OLD behavior - uses sender's job ID which may be incorrect
		for _, j := range jobs {
			if j.ExecutorPeerID == request.SourcePeerID {
				destJob = j
				break
			}
		}

		if destJob == nil {
			jm.logger.Error(fmt.Sprintf("No job execution found for workflow job %d from executor peer %s", request.WorkflowJobID, request.SourcePeerID[:8]), "job_manager")
			return &types.JobDataTransferResponse{
				WorkflowJobID: request.WorkflowJobID,
				Accepted:      false,
				Message:       "Job execution not found for this executor peer",
			}, fmt.Errorf("job execution not found")
		}
	}

	// Construct hierarchical destination path: workflows/{ordering_peer_id}/{workflow_job_id}/jobs/{dest_job_id}/{destDir}
	// This follows the libp2p distributed P2P pattern for clear file organization
	//
	// Path conventions for destination directories:
	// - STDIN interface: peer.PeerPath = "input/" -> jobs/<id>/input/
	// - MOUNT interface: peer.PeerPath = "/app" or "/" -> jobs/<id>/mounts/<basename>/
	//
	// For MOUNT interfaces, we need to add the "mounts/" prefix

	// Determine the destination directory based on the path type
	var destDir string
	destPath := request.DestinationPath

	if destPath == "" || strings.HasPrefix(destPath, "input") {
		// STDIN interface or empty path: use input/ directory
		destDir = "input"
	} else if strings.HasPrefix(destPath, "output") {
		// Output directory
		destDir = destPath
	} else {
		// MOUNT interface: path is a mount path like "/" or "/app" or "/asdasdf/fsddf"
		// Convert to mounts/<full_path>/ structure (strip leading "/" only)
		// Examples:
		//   "/" -> "mounts/"
		//   "/app" -> "mounts/app/"
		//   "/asdasdf/fsddf" -> "mounts/asdasdf/fsddf/"
		mountPath := strings.TrimPrefix(destPath, "/")
		if mountPath == "" {
			// For root mount "/", use "mounts" directly
			destDir = "mounts"
		} else {
			// For named mounts like "/app" or "/asdasdf/fsddf", preserve full path
			destDir = filepath.Join("mounts", mountPath)
		}
		jm.logger.Info(fmt.Sprintf("MOUNT interface detected: path '%s' -> destDir '%s'", destPath, destDir), "job_manager")
	}

	appPaths := utils.GetAppPaths("remote-network")
	hierarchicalPath := filepath.Join(
		appPaths.DataDir,
		"workflows",
		destJob.OrderingPeerID, // Full peer ID to prevent collisions
		fmt.Sprintf("%d", destJob.WorkflowJobID),
		"jobs",
		fmt.Sprintf("%d", destJob.ID), // Use DESTINATION job ID, not sender's
		destDir,
	)

	// Add trailing separator to indicate directory
	hierarchicalPath += string(os.PathSeparator)

	jm.logger.Info(fmt.Sprintf("Accepted data transfer request for workflow job %d (dest job %d) from peer %s (file: %s, size: %d bytes)",
		request.WorkflowJobID, destJob.ID, peerID[:8], request.SourcePath, request.SizeBytes), "job_manager")
	jm.logger.Info(fmt.Sprintf("Resolved destination path template '%s' to: %s",
		request.DestinationPath, hierarchicalPath), "job_manager")

	// Initialize transfer in data worker with proper metadata
	if jm.dataWorker != nil {
		// Calculate expected chunks
		chunkSize := jm.cm.GetConfigInt("job_data_chunk_size", 1048576, 1024, 10485760)
		totalChunks := int((request.SizeBytes + int64(chunkSize) - 1) / int64(chunkSize))

		// Use transfer ID from request (sender generates it)
		transferID := request.TransferID

		// Initialize incoming transfer with hierarchical path and interface type
		err := jm.dataWorker.InitializeIncomingTransferFull(
			transferID,
			destJob.ID, // Use destination job execution ID
			peerID,
			hierarchicalPath, // Use hierarchical path instead of template
			request.DataHash,
			request.SizeBytes,
			totalChunks,
			request.Passphrase,
			request.Encrypted,
			request.InterfaceType, // Pass interface type for package detection
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
		WorkflowID:          job.WorkflowJobID, // Workflow job ID (execution instance) for directory paths
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

	// Store the remote job execution ID for later status requests
	if err := jm.db.UpdateRemoteJobExecutionID(job.ID, response.JobExecutionID); err != nil {
		jm.logger.Error(fmt.Sprintf("Failed to store remote_job_execution_id for job %d: %v", job.ID, err), "job_manager")
	}

	// Update job status to RUNNING (remote peer will send status updates)
	jm.db.UpdateJobStatus(job.ID, types.JobStatusRunning, "")
}

// checkInputsReady checks if all expected input files exist on the file system
// Following the libp2p data path pattern: /workflows/<ordering_peer>/<workflow_id>/job/<job_id>/input/<sender_peer>/
func (jm *JobManager) checkInputsReady(job *database.JobExecution) (bool, error) {
	// First check if there are any pending/active incoming transfers for this job
	// This prevents starting execution while data is still being transferred/decrypted
	hasPending, err := jm.db.HasPendingTransfersForJob(job.ID)
	if err != nil {
		jm.logger.Warn(fmt.Sprintf("Failed to check pending transfers for job %d: %v", job.ID, err), "job_manager")
		// Continue with file-based check as fallback
	} else if hasPending {
		jm.logger.Debug(fmt.Sprintf("Job %d has pending transfers, inputs not ready", job.ID), "job_manager")
		return false, nil
	}

	// Get job interfaces
	interfaces, err := jm.db.GetJobInterfaces(job.ID)
	if err != nil {
		return false, fmt.Errorf("failed to get job interfaces: %v", err)
	}

	// For each STDIN or MOUNT interface, check if data exists from all provider peers
	for _, iface := range interfaces {
		// Skip if not an input-capable interface
		if iface.InterfaceType != "STDIN" && iface.InterfaceType != "MOUNT" {
			continue
		}

		// Get interface peers
		peers, err := jm.db.GetJobInterfacePeers(iface.ID)
		if err != nil {
			return false, fmt.Errorf("failed to get interface peers: %v", err)
		}

		// Check if data exists from all input provider peers
		for _, peer := range peers {
			if peer.PeerMountFunction != types.MountFunctionInput && peer.PeerMountFunction != types.MountFunctionBoth {
				continue
			}

			// Build expected input data path based on interface type
			appPaths := utils.GetAppPaths("")
			var inputPath string

			if iface.InterfaceType == "STDIN" {
				// <DataDir>/workflows/<ordering_peer>/<workflow_id>/jobs/<job_id>/input/
				inputPath = filepath.Join(
					appPaths.DataDir,
					"workflows",
					job.OrderingPeerID,
					fmt.Sprintf("%d", job.WorkflowJobID),
					"jobs",
					fmt.Sprintf("%d", job.ID),
					"input",
				)
			} else if iface.InterfaceType == "MOUNT" {
				// For MOUNT interfaces, check the mounts directory
				// <DataDir>/workflows/<ordering_peer>/<workflow_id>/jobs/<job_id>/mounts/<mount_path_basename>/
				//
				// Important: peer.PeerPath on the receiver side is where the SENDER outputs FROM,
				// not where they sent TO. We need to query the sender's peer record to find
				// where they actually sent the data (their peer_path pointing to us).
				var mountPath string

				// Try to find where the sender sent data by querying their peer records
				if peer.PeerJobID != nil {
					senderDestPath, err := jm.db.GetSenderDestinationPath(*peer.PeerJobID, job.ID)
					if err == nil && senderDestPath != "" {
						mountPath = senderDestPath
					}
				}

				// Fallback to iface.Path if sender lookup failed
				if mountPath == "" {
					mountPath = iface.Path
				}

				if mountPath != "" {
					inputPath = filepath.Join(
						appPaths.DataDir,
						"workflows",
						job.OrderingPeerID,
						fmt.Sprintf("%d", job.WorkflowJobID),
						"jobs",
						fmt.Sprintf("%d", job.ID),
						"mounts",
						filepath.Base(mountPath),
					)
				} else {
					// If no mount path specified, use default input directory
					inputPath = filepath.Join(
						appPaths.DataDir,
						"workflows",
						job.OrderingPeerID,
						fmt.Sprintf("%d", job.WorkflowJobID),
						"jobs",
						fmt.Sprintf("%d", job.ID),
						"input",
					)
				}
			}

			// Check if directory exists and has content
			exists, err := utils.PathExistsWithContent(inputPath)
			if err != nil {
				return false, fmt.Errorf("failed to check input path %s: %v", inputPath, err)
			}

			if !exists {
				jm.logger.Debug(fmt.Sprintf("Job %d input not ready: waiting for data from peer %s at %s (%s interface)",
					job.ID, peer.PeerNodeID[:8], inputPath, iface.InterfaceType), "job_manager")
				return false, nil
			}

			jm.logger.Debug(fmt.Sprintf("Job %d found input from peer %s at %s (%s interface)",
				job.ID, peer.PeerNodeID[:8], inputPath, iface.InterfaceType), "job_manager")
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

	localPeerID := jm.peerManager.GetPeerID()

	for _, job := range jobs {
		// Only check jobs with INPUTS_READY constraint
		if job.ExecutionConstraint != types.ExecutionConstraintInputsReady {
			continue
		}

		// Skip jobs that should execute on remote peers - the orchestrator cannot check
		// their local filesystem for inputs. Remote jobs will be sent to the executor
		// once they reach READY status, and the executor will do its own input checking.
		if job.ExecutorPeerID != localPeerID {
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
