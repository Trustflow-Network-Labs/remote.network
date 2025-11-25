# Workflow Execution Architecture Migration Plan

## Overview
This document outlines the complete migration plan for fixing the workflow execution architecture to properly synchronize IDs between orchestrator and executor peers.

## Database Schema Changes ✅ COMPLETED

### New Tables
- **workflow_executions**: Tracks individual execution instances of workflows
  - `id` - Unique execution instance ID (auto-increment)
  - `workflow_id` - References the workflow definition
  - `status` - pending, running, completed, failed, cancelled
  - `started_at`, `completed_at`, timestamps

### Modified Tables
- **workflow_jobs**: Now stores individual jobs within execution (one per job)
  - `id` - Unique workflow_job_id (auto-increment)
  - `workflow_execution_id` - Links to execution instance
  - `workflow_id` - For quick reference
  - `node_id` - Workflow node ID from design
  - `job_name`, `service_id`, `executor_peer_id` - Job details
  - `remote_job_execution_id` - Stores executor's job_execution_id
  - `status`, `result`, `error` - Job status tracking

## Correct Execution Flow

### Orchestrator Side (Workflow Owner)

#### 1. StartWorkflow() - FILE: workflow_manager.go
**Current (WRONG)**:
```go
workflowJob, _ := wm.db.CreateWorkflowJob(workflowID)  // Creates ONE entry
// Then creates job_executions for ALL jobs
```

**Correct (NEW)**:
```go
// Step 1: Create workflow execution instance
execution, _ := wm.db.CreateWorkflowExecution(workflowID)

// Step 2: Create workflow_jobs entries (one per job in workflow)
for _, jobDef := range workflowDef.Jobs {
    workflowJob, _ := wm.db.CreateWorkflowJob(
        execution.ID,           // workflow_execution_id
        workflowID,            // workflow_id
        jobDef.NodeID,         // node_id
        jobDef.JobName,        // job_name
        jobDef.ServiceID,      // service_id
        jobDef.ExecutorPeerID, // executor_peer_id
    )
    // workflowJob.ID is now the workflow_job_id
}

// Step 3: For each workflow_job, determine if local or remote
for _, workflowJob := range workflowJobs {
    if workflowJob.ExecutorPeerID == wm.peerManager.GetPeerID() {
        // LOCAL JOB: Create job_execution locally
        jobExec := createLocalJobExecution(workflowJob)
        // Store jobExec.ID in workflow_jobs.remote_job_execution_id
        wm.db.UpdateWorkflowJobRemoteExecutionID(workflowJob.ID, jobExec.ID)
    } else {
        // REMOTE JOB: Send to remote peer
        sendRemoteJobRequest(workflowJob)
        // Remote peer will create job_execution and return ID
        // Store returned ID in workflow_jobs.remote_job_execution_id
    }
}
```

#### 2. sendJobToRemotePeer() - FILE: job_manager.go
**Changes Needed**:
```go
func (jm *JobManager) sendJobToRemotePeer(workflowJob *database.WorkflowJob) {
    // Get workflow_job details (already in workflowJob struct)

    // Create job execution request
    request := &types.JobExecutionRequest{
        WorkflowJobID:       workflowJob.ID,  // Send workflow_job_id
        WorkflowID:          workflowJob.WorkflowID,
        JobName:             workflowJob.JobName,
        ServiceID:           workflowJob.ServiceID,
        ExecutorPeerID:      workflowJob.ExecutorPeerID,
        // ... interfaces, entrypoint, commands, etc.
    }

    // Send to remote peer
    response, _ := jobHandler.SendJobRequest(workflowJob.ExecutorPeerID, request)

    // Store remote executor's job_execution_id in workflow_jobs table
    jm.db.UpdateWorkflowJobRemoteExecutionID(
        workflowJob.ID,              // workflow_job_id
        response.JobExecutionID,      // executor's job_execution_id
    )
}
```

### Executor Side (Remote Peer)

#### 3. HandleJobRequest() - FILE: p2p/service_query_handler.go or job_manager.go
**Changes Needed**:
```go
func (jm *JobManager) HandleJobRequest(request *types.JobExecutionRequest, peerID string) (*types.JobExecutionResponse, error) {
    // Create job_execution record
    jobExec := &database.JobExecution{
        WorkflowJobID:       request.WorkflowJobID,  // Store orchestrator's workflow_job_id
        ServiceID:           request.ServiceID,
        ExecutorPeerID:      jm.peerManager.GetPeerID(),  // Self
        OrderingPeerID:      peerID,  // Orchestrator peer ID
        ExecutionConstraint: request.ExecutionConstraint,
        Status:              "PENDING",
        // ... other fields
    }

    _ = jm.db.CreateJobExecution(jobExec)
    // jobExec.ID is now the executor's job_execution_id

    // Return response with both IDs
    return &types.JobExecutionResponse{
        WorkflowJobID:   request.WorkflowJobID,  // Echo back
        JobExecutionID:  jobExec.ID,             // Executor's job_execution_id
        Accepted:        true,
    }, nil
}
```

## Path Construction

### Standard Paths
All paths follow this pattern:
```
/workflows/<orchestrator_peer_id>/<workflow_execution_id>/jobs/<executor_peer_id>/<job_execution_id>/<type>/
```

Where:
- `orchestrator_peer_id` - Peer that created the workflow
- `workflow_execution_id` - The workflow_executions.id (NOT workflow_job_id!)
- `executor_peer_id` - Peer executing the job
- `job_execution_id` - The executor's job_executions.id
- `type` - input, output, mounts, package, etc.

### Special Cases
**"Local Peer" (final destination)**:
```
/workflows/<orchestrator_peer_id>/<workflow_execution_id>/jobs/<orchestrator_peer_id>/0/input/
```
(Use job_execution_id = 0 for "Local Peer")

### Path Construction Utility
**NEW FILE**: `internal/utils/path_builder.go`
```go
type JobPathInfo struct {
    OrchestratorPeerID   string
    WorkflowExecutionID  int64
    ExecutorPeerID       string
    JobExecutionID       int64
}

func BuildJobPath(info JobPathInfo, pathType string) string {
    baseDir := getWorkflowsBaseDir()
    return filepath.Join(
        baseDir,
        "workflows",
        info.OrchestratorPeerID,
        strconv.FormatInt(info.WorkflowExecutionID, 10),
        "jobs",
        info.ExecutorPeerID,
        strconv.FormatInt(info.JobExecutionID, 10),
        pathType,
    )
}
```

## Interface Connections

### Problem
Current `job_interface_peers` table only stores `peer_job_id` which is ambiguous.

### Solution
Need to track BOTH IDs for each peer connection:
- `workflow_job_id` - For orchestrator coordination
- `job_execution_id` - For actual filesystem paths

### Schema Update Needed
**FILE**: `internal/database/job_executions.go`
```sql
ALTER TABLE job_interface_peers ADD COLUMN peer_workflow_job_id INTEGER;
ALTER TABLE job_interface_peers ADD COLUMN peer_job_execution_id INTEGER;
-- Keep peer_job_id for backward compatibility during migration
```

**Updated Struct**:
```go
type JobInterfacePeer struct {
    ID                   int64
    JobInterfaceID       int64
    PeerNodeID           string    // Ed25519 peer ID
    PeerWorkflowJobID    *int64    // Orchestrator's workflow_job_id
    PeerJobExecutionID   *int64    // Executor's job_execution_id (for paths)
    PeerPath             string
    PeerMountFunction    string
    DutyAcknowledged     bool
}
```

### Populating Interface IDs

During interface creation (Phase 2 in executeWorkflowJobs):
```go
// For each interface peer connection
for _, peerDef := range interfaceDef.InterfacePeers {
    // Look up the workflow_job by node_id
    workflowJob := findWorkflowJobByNodeID(peerDef.PeerJobID)

    peer := &database.JobInterfacePeer{
        PeerNodeID:          peerDef.PeerNodeID,
        PeerWorkflowJobID:   &workflowJob.ID,  // workflow_job_id
        PeerJobExecutionID:  workflowJob.RemoteJobExecutionID,  // job_execution_id (null until set)
        // ...
    }
}
```

## Data Transfer Flow

### Current Issue
Transfer code uses wrong IDs to construct paths and route data.

### Fix Needed - FILE: workers/data_worker.go

```go
func (dw *DataWorker) transferDataToPeer(/* params */) error {
    // Get interface peer connection
    peers, _ := dw.db.GetJobInterfacePeers(interfaceID)

    for _, peer := range peers {
        if peer.PeerMountFunction == "INPUT" || peer.PeerMountFunction == "BOTH" {
            // Look up workflow_job to get execution context
            workflowJob, _ := dw.db.GetWorkflowJobByID(*peer.PeerWorkflowJobID)

            // Look up workflow_execution to get execution_id
            execution, _ := dw.db.GetWorkflowExecutionByID(workflowJob.WorkflowExecutionID)

            // Build destination path using CORRECT IDs
            destPath := BuildJobPath(JobPathInfo{
                OrchestratorPeerID:  workflowJob.OrderingPeerID,
                WorkflowExecutionID: execution.ID,  // Use workflow_execution_id!
                ExecutorPeerID:      peer.PeerNodeID,
                JobExecutionID:      *peer.PeerJobExecutionID,  // Executor's job_execution_id
            }, "input")

            // Send transfer with correct routing
            transferRequest := &types.DataTransferRequest{
                DestinationJobExecutionID:  *peer.PeerJobExecutionID,  // For executor to find job
                DestinationPath:            destPath,
                // ...
            }
        }
    }
}
```

## Input Readiness Checking

### Current Issue
Jobs start executing before inputs arrive because:
1. Remote job arrives at executor before transfer is initiated
2. Input check doesn't wait for expected transfers

### Fix Needed - FILE: job_manager.go

```go
func (jm *JobManager) checkInputsReady(job *database.JobExecution) (bool, error) {
    // Get job interfaces
    interfaces, _ := jm.db.GetJobInterfaces(job.ID)

    for _, iface := range interfaces {
        // Skip output interfaces
        if iface.InterfaceType == "STDOUT" || iface.InterfaceType == "STDERR" {
            continue
        }

        // Get interface peers (sources)
        peers, _ := jm.db.GetJobInterfacePeers(iface.ID)

        for _, peer := range peers {
            if peer.PeerMountFunction == "INPUT" || peer.PeerMountFunction == "BOTH" {
                // Check if transfer exists and is complete
                hasPendingTransfer, _ := jm.db.HasPendingTransferFrom(
                    job.ID,
                    *peer.PeerJobExecutionID,  // Source job_execution_id
                )

                if hasPendingTransfer {
                    return false, nil  // Wait for transfer
                }

                // Check if files exist on filesystem
                expectedPath := BuildJobPath(JobPathInfo{
                    OrchestratorPeerID:  job.OrderingPeerID,
                    WorkflowExecutionID: getWorkflowExecutionID(job.WorkflowJobID),
                    ExecutorPeerID:      jm.peerManager.GetPeerID(),
                    JobExecutionID:      job.ID,
                }, getPathTypeForInterface(iface.InterfaceType))

                if !pathExists(expectedPath) {
                    return false, nil  // Files not ready
                }
            }
        }
    }

    return true, nil  // All inputs ready
}
```

## Migration Steps

### Step 1: Complete Database Schema ✅
- [x] Create workflow_executions table
- [x] Update workflow_jobs table
- [x] Update database methods

### Step 2: Update Core Workflow Logic
- [ ] **File: workflow_manager.go**
  - [ ] Refactor StartWorkflow() to create workflow_execution first
  - [ ] Refactor executeWorkflowJobs() to:
    - [ ] Create workflow_jobs for each job definition
    - [ ] Separate local vs remote job handling
    - [ ] Build proper node_id to workflow_job_id mapping
  - [ ] Update WorkflowExecution struct in-memory tracker

### Step 3: Fix Job Submission
- [ ] **File: job_manager.go**
  - [ ] Update SubmitJobWithOptions() to handle local jobs only
  - [ ] Refactor sendJobToRemotePeer() to use workflow_jobs
  - [ ] Update remote job response handling

### Step 4: Update Remote Job Handling
- [ ] **File: p2p/service_query_handler.go** (or job_manager.go)
  - [ ] Update HandleJobRequest() to properly store workflow_job_id
  - [ ] Ensure job_execution_id is returned correctly

### Step 5: Implement Path Construction
- [ ] **New File: utils/path_builder.go**
  - [ ] Create BuildJobPath() utility
  - [ ] Add path type constants
  - [ ] Add helper functions for special cases

### Step 6: Update Interface Handling
- [ ] **File: database/job_executions.go**
  - [ ] Add peer_workflow_job_id and peer_job_execution_id columns
  - [ ] Update JobInterfacePeer struct
  - [ ] Update interface creation/query methods
- [ ] **File: job_manager.go**
  - [ ] Update CreateJobInterfaces() to populate both IDs
  - [ ] Update interface lookup methods

### Step 7: Fix Data Transfer
- [ ] **File: workers/data_worker.go**
  - [ ] Update transferDataToPeer() to use correct IDs
  - [ ] Update path construction for transfers
  - [ ] Update transfer request routing

### Step 8: Fix Input Readiness
- [ ] **File: job_manager.go**
  - [ ] Update checkInputsReady() logic
  - [ ] Add proper transfer tracking
  - [ ] Implement waiting for expected inputs

### Step 9: Update API Handlers
- [ ] **File: api/handlers_workflows.go**
  - [ ] Update workflow start endpoint
  - [ ] Update workflow status endpoint
  - [ ] Update job status queries

### Step 10: Testing & Validation
- [ ] Test local job execution
- [ ] Test remote job execution
- [ ] Test Job #26 → Job #27 scenario
- [ ] Verify ID synchronization
- [ ] Verify path construction
- [ ] Verify input dependency waiting

## Key Mappings Reference

### ID Relationships
```
Workflow Definition (workflow.id = 1)
  └─> Workflow Execution (workflow_execution.id = 100)
      ├─> Workflow Job #1 (workflow_job.id = 27, node_id = 5)
      │   └─> Job Execution (job_execution.id = 12 on executor peer)
      │       - Executor stores: workflow_job_id = 27
      │       - Orchestrator stores: remote_job_execution_id = 12 in workflow_job #27
      │
      ├─> Workflow Job #2 (workflow_job.id = 28, node_id = 6)
      │   └─> Job Execution (job_execution.id = 13 on executor peer)
      │       - Executor stores: workflow_job_id = 28
      │       - Orchestrator stores: remote_job_execution_id = 13 in workflow_job #28
      │
      └─> Workflow Job #3 (workflow_job.id = 29, node_id = 7)
          └─> Job Execution (job_execution.id = 45 on orchestrator - local job)
              - Orchestrator stores: remote_job_execution_id = 45 in workflow_job #29
```

### Path Example
```
Job #27 (DATA service) on Peer A → Job #28 (DOCKER service) on Peer B

Orchestrator: Peer C (creates workflow)
- workflow_execution.id = 100
- workflow_job #27: executor_peer_id = Peer A, remote_job_execution_id = 12
- workflow_job #28: executor_peer_id = Peer B, remote_job_execution_id = 13

Peer A (executor for job #27):
- job_execution.id = 12
- workflow_job_id = 27
- Output path: /workflows/PeerC/100/jobs/PeerA/12/output/

Peer B (executor for job #28):
- job_execution.id = 13
- workflow_job_id = 28
- Input path: /workflows/PeerC/100/jobs/PeerB/13/input/
- Mount path: /workflows/PeerC/100/jobs/PeerB/13/mounts/app/
```

## Backward Compatibility Considerations

During migration, old workflow executions may exist. Consider:
1. Add migration script to update existing data
2. Handle NULL workflow_execution_id in old workflow_jobs records
3. Gracefully handle missing peer_workflow_job_id/peer_job_execution_id
4. Log warnings for deprecated code paths

## Testing Checklist

### Unit Tests
- [ ] Database schema creation
- [ ] workflow_execution CRUD
- [ ] workflow_job CRUD with all fields
- [ ] Path construction utilities
- [ ] Interface peer ID mapping

### Integration Tests
- [ ] Workflow start with local jobs only
- [ ] Workflow start with remote jobs only
- [ ] Workflow start with mixed local/remote jobs
- [ ] Job #26 → Job #27 remote transfer scenario
- [ ] Multi-hop workflows (A → B → C)
- [ ] "Local Peer" final destination

### Validation Tests
- [ ] Verify workflow_job_id same on orchestrator and executor
- [ ] Verify job_execution_id correctly stored
- [ ] Verify paths constructed correctly
- [ ] Verify Job #27 waits for Job #26 input
- [ ] Verify file appears in correct mount location

## Notes
- All changes are in `fix/workflow-execution-architecture` branch
- Commit frequently with clear messages
- Run tests after each major component update
- Update API documentation when handlers change
