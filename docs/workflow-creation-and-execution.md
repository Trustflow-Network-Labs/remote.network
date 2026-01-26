# Workflow Creation and Execution

This document provides a detailed technical guide to workflow creation and execution in the Remote Network system. It covers workflow definition, node types, job scheduling, data flow, and execution orchestration with specific file/function references for debugging.

## Table of Contents
- [Overview](#overview)
- [Workflow Creation](#workflow-creation)
  - [Workflow Structure](#workflow-structure)
  - [Node Types](#node-types)
  - [Connections and Data Flow](#connections-and-data-flow)
  - [Validation Rules](#validation-rules)
  - [UI State Management](#ui-state-management)
- [Workflow Execution](#workflow-execution)
  - [Execution Triggers](#execution-triggers)
  - [Two-Phase Execution Model](#two-phase-execution-model)
  - [Job Scheduling](#job-scheduling)
  - [Dependency Resolution](#dependency-resolution)
  - [State Management](#state-management)
- [Data Transfer Between Nodes](#data-transfer-between-nodes)
  - [Interface System](#interface-system)
  - [Path Hierarchy](#path-hierarchy)
  - [Transfer Protocol](#transfer-protocol)
- [Node Execution Details](#node-execution-details)
  - [DATA Service Execution](#data-service-execution)
  - [DOCKER Service Execution](#docker-service-execution)
  - [STANDALONE Service Execution](#standalone-service-execution)
- [Complete Data Flows](#complete-data-flows)
- [Key Data Structures](#key-data-structures)
- [Debugging Guide](#debugging-guide)

---

## Overview

The Remote Network workflow system enables orchestration of distributed compute jobs across multiple peers. Key capabilities:

### Workflow Features
- **Visual workflow designer** - Drag-and-drop node-based UI
- **Multi-peer execution** - Jobs run on different peers simultaneously
- **Data flow orchestration** - Automatic data transfer between nodes
- **Service integration** - DATA, DOCKER, and STANDALONE services
- **Execution tracking** - Real-time status monitoring

### Architecture Components
- **WorkflowManager** - Workflow orchestration and execution coordination
- **JobManager** - Individual job execution and lifecycle
- **DataWorker** - Inter-node data transfers
- **JobHandler** - P2P job request/response messaging

### Execution Model
```
Workflow
  └─> WorkflowExecution (instance)
      └─> WorkflowJobs (orchestrator tracking)
          └─> JobExecutions (actual execution on peer)
              └─> Data Transfers (outputs to next jobs)
```

---

## Workflow Creation

### Workflow Structure

#### Core Data Model
**Files:** `internal/database/workflows.go`, `internal/types/workflow_types.go`

```go
// Workflow - Container for visual workflow definition
type Workflow struct {
    ID          int64
    Name        string                 // User-defined workflow name
    Description string
    Definition  map[string]interface{} // JSON: {"jobs": [...]}
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// WorkflowNode - Visual node in workflow designer
type WorkflowNode struct {
    ID              int64
    WorkflowID      int64
    ServiceID       int64  // References offered_services.id
    PeerID          string // Executor peer ID (local or remote)
    ServiceName     string
    ServiceType     string // DATA, DOCKER, STANDALONE
    Order           int    // Execution order hint

    // UI Positioning
    GUIX, GUIY      int    // Canvas coordinates

    // Service Configuration
    Interfaces      []interface{} // From remote service search
    Entrypoint      []string      // Docker: ["python3"]
    Cmd             []string      // Docker: ["script.py", "--arg"]

    // Pricing (copied from service)
    PricingAmount   float64
    PricingType     string
}

// WorkflowConnection - Data flow edge between nodes
type WorkflowConnection struct {
    ID                  int64
    WorkflowID          int64

    // Source node (null = self-peer/orchestrator)
    FromNodeID          *int64
    FromInterfaceType   string  // STDIN, STDOUT, MOUNT

    // Destination node (null = self-peer/orchestrator)
    ToNodeID            *int64
    ToInterfaceType     string  // STDIN, MOUNT

    // Optional file renaming
    DestinationFileName *string
}
```

**Database Tables:**
```sql
CREATE TABLE workflows (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    definition TEXT NOT NULL,  -- JSON with jobs array
    created_at DATETIME,
    updated_at DATETIME
);

CREATE TABLE workflow_nodes (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER,
    service_id INTEGER,
    peer_id TEXT,
    service_name TEXT,
    service_type TEXT,
    "order" INTEGER,
    gui_x INTEGER,
    gui_y INTEGER,
    interfaces TEXT,      -- JSON: service interfaces
    entrypoint TEXT,      -- JSON: ["python3"]
    cmd TEXT,             -- JSON: ["script.py"]
    pricing_amount REAL,
    pricing_type TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

CREATE TABLE workflow_connections (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER,
    from_node_id INTEGER,
    from_interface_type TEXT,
    to_node_id INTEGER,
    to_interface_type TEXT,
    destination_file_name TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id),
    FOREIGN KEY (from_node_id) REFERENCES workflow_nodes(id),
    FOREIGN KEY (to_node_id) REFERENCES workflow_nodes(id)
);
```

---

### Node Types

#### 1. DATA Nodes
**Purpose:** Static data storage and transfer

**File:** `internal/types/service_types.go:15`

```
Characteristics:
├─ Service Type: "DATA"
├─ No execution (files already exist)
├─ MUST have at least one STDOUT interface
├─ Cannot transfer peer-to-peer (security constraint)
└─ Valid destinations:
    ├─ Orchestrator (download data)
    └─ Remote peer's DOCKER/STANDALONE job (computation input)
```

**Example Use Cases:**
- Training datasets
- Configuration files
- Pre-trained models
- Static assets

**Interface Requirements:**
```
Required: STDOUT (at least one)
Optional: None (no input, data pre-exists)
```

**Restrictions:**
- Cannot connect DATA → DATA (peer-to-peer download blocked)
- Can connect DATA → DOCKER/STANDALONE (computation allowed)
- Can connect DATA → Self-peer (orchestrator download allowed)

**File:** `internal/core/workflow_manager.go:774-806`

---

#### 2. DOCKER Nodes
**Purpose:** Containerized computation

**File:** `internal/types/service_types.go:16`

```
Characteristics:
├─ Service Type: "DOCKER"
├─ Runs Docker containers
├─ Configurable entrypoint and commands
├─ Supports all interface types
└─ Can consume inputs from DATA or other DOCKER/STANDALONE nodes
```

**Configuration:**
```go
// Entrypoint: Override container ENTRYPOINT
Entrypoint: ["python3"]

// Cmd: Override container CMD
Cmd: ["train.py", "--epochs", "10"]

// Environment variables (future)
Env: {"GPU_ENABLED": "true"}
```

**Interface Support:**
```
Input:
  ├─ STDIN: Stream input data
  └─ MOUNT: Filesystem mount volumes

Output:
  ├─ STDOUT: Stream output data
  ├─ STDERR: Error output
  ├─ LOGS: Container logs
  └─ MOUNT: Output volumes
```

**Execution Flow:**
```
1. Wait for input data (if INPUTS_READY constraint)
2. Pull Docker image (if not cached)
3. Create container with mounts
4. Execute with configured entrypoint/cmd
5. Collect outputs (stdout, mounts)
6. Transfer outputs to connected nodes
```

---

#### 3. STANDALONE Nodes
**Purpose:** Native binary execution

**File:** `internal/types/service_types.go:17`

```
Characteristics:
├─ Service Type: "STANDALONE"
├─ Runs compiled executables directly
├─ No container overhead
├─ Same interface support as DOCKER
└─ Platform-dependent (check capabilities)
```

**Configuration:**
```go
// Executable path (from service definition)
Entrypoint: ["/path/to/binary"]

// Arguments
Cmd: ["--input", "data.txt", "--output", "result.txt"]
```

**Platform Considerations:**
- Binary must match executor peer's platform/architecture
- Check SystemCapabilities before assigning
- No cross-platform compatibility (unlike Docker with emulation)

---

### Connections and Data Flow

#### Connection Types

**File:** `internal/database/workflows.go:269-576`

**1. Node-to-Node Connections**
```
Source Node (STDOUT/MOUNT) → Destination Node (STDIN/MOUNT)

Example:
  DATA Service (STDOUT) → DOCKER Service (MOUNT: /input/data.csv)
```

**2. Self-Peer Connections**
```
Source Node (STDOUT) → Orchestrator (download)

Example:
  DOCKER Service (MOUNT: /output/results.txt) → Self-Peer
  (Downloads results.txt to local machine)
```

**3. Multi-Connection Support**
```
One output can connect to multiple inputs:
  DATA Service (STDOUT)
    ├─> DOCKER Service A (MOUNT: /input/)
    └─> DOCKER Service B (MOUNT: /data/)
```

#### Interface Type Mapping

**File:** `internal/database/workflows.go:420-470`

```
Connection Rules:
├─ STDOUT → STDIN: Stream connection
├─ STDOUT → MOUNT: File-based connection
├─ MOUNT → MOUNT: Directory mount
└─ MOUNT → STDIN: File content streaming

Forbidden:
├─ STDIN → STDOUT (reversed direction)
├─ MOUNT → STDOUT (type mismatch)
└─ DATA → DATA (peer-to-peer download blocked)
```

#### Mount Function Semantics

**File:** `internal/types/job_types.go:49-51`

```go
type InterfacePeer struct {
    PeerMountFunction string // INPUT, OUTPUT, BOTH
}
```

**Critical Understanding (Fixed Semantics):**
- **OUTPUT:** Peer sends data TO this job (we RECEIVE/WAIT for it)
- **INPUT:** Peer receives data FROM this job (we SEND to it)
- **BOTH:** Bidirectional (rare, used for shared mounts)

**Example:**
```
Job A (DATA service) → Job B (DOCKER service)

Job A's perspective:
  ├─ Interface: STDOUT
  └─ InterfacePeer:
      ├─ PeerID: Job B's executor
      ├─ PeerMountFunction: INPUT (Job B receives FROM Job A)

Job B's perspective:
  ├─ Interface: MOUNT (/input/)
  └─ InterfacePeer:
      ├─ PeerID: Job A's executor
      ├─ PeerMountFunction: OUTPUT (Job B receives FROM Job A)
```

**Why This Matters:**
- Determines which peer initiates data transfer
- Affects path construction
- Controls readiness checks (INPUTS_READY constraint)

---

### Validation Rules

**File:** `internal/core/workflow_manager.go:738-806`

#### Phase 1: Basic Validation

```
ValidateWorkflow(workflow)
├─ CHECK 1: At least one job exists
│   └─ Failure: "Workflow must have at least one job"
│
├─ CHECK 2: Service references valid (if executing locally)
│   ├─ Query: offered_services WHERE id = job.ServiceID
│   └─ Failure: "Service X not found"
│
└─ CHECK 3: Service type matches
    ├─ job.ServiceType == service.ServiceType
    └─ Failure: "Service type mismatch"
```

#### Phase 2: Interface Validation

```
ValidateInterfaces(job)
├─ CHECK 1: Interface types valid
│   ├─ Allowed: STDIN, STDOUT, STDERR, LOGS, MOUNT
│   └─ Failure: "Invalid interface type: X"
│
├─ CHECK 2: Mount function valid
│   ├─ Allowed: INPUT, OUTPUT, BOTH
│   └─ Failure: "Invalid mount function: X"
│
└─ CHECK 3: DATA service constraints
    ├─ If ServiceType == DATA:
    │   ├─ Must have at least one STDOUT interface
    │   └─ Failure: "DATA service must have STDOUT interface"
    │
    └─ If has STDOUT interfaces:
        ├─ For each InterfacePeer:
        │   ├─ If destination is remote peer:
        │   │   ├─ Destination job must be DOCKER or STANDALONE
        │   │   └─ Failure: "Cannot download from DATA service on remote peer"
        │   └─ If destination is self-peer: OK (download allowed)
```

**Security Rationale:**
- Prevents peer-to-peer downloads (bandwidth abuse)
- Forces computation on data (intended use case)
- Orchestrator can still download results

#### Phase 3: Connection Validation

```
ValidateConnections(workflow)
├─ CHECK 1: From/To nodes exist
│   └─ Failure: "Connection references non-existent node"
│
├─ CHECK 2: Interface types compatible
│   ├─ STDOUT → STDIN: OK
│   ├─ STDOUT → MOUNT: OK
│   ├─ MOUNT → MOUNT: OK
│   └─ Other combinations: Failure
│
└─ CHECK 3: No cycles (future enhancement)
    └─ DAG validation to prevent deadlocks
```

---

### UI State Management

**File:** `internal/database/workflow_nodes.go:33-46`

```go
type WorkflowUIState struct {
    ID         int64
    WorkflowID int64

    // Canvas settings
    SnapToGrid bool
    ZoomLevel  float64 // 0.5 to 2.0 typically
    PanX, PanY float64 // Canvas offset

    // Special node position
    SelfPeerX, SelfPeerY float64 // Local peer card position
}
```

**Database Table:**
```sql
CREATE TABLE workflow_ui_state (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER UNIQUE,
    snap_to_grid BOOLEAN DEFAULT 1,
    zoom_level REAL DEFAULT 1.0,
    pan_x REAL DEFAULT 0,
    pan_y REAL DEFAULT 0,
    self_peer_x REAL DEFAULT 0,
    self_peer_y REAL DEFAULT 0,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);
```

**Usage:**
- Persists canvas state between sessions
- Allows consistent visual layout
- Independent of workflow execution logic

---

## Workflow Execution

### Execution Triggers

#### Manual Execution via API
**File:** `internal/api/handlers_workflows.go:196-274`

```
POST /api/workflows/:id/execute

Request Body:
{
  "passphrase": "optional-encryption-key"
}

Response:
HTTP 202 Accepted
{
  "execution_id": 123,
  "status": "PENDING"
}
```

**Handler Flow:**
```go
HandleExecuteWorkflow(c *gin.Context)
├─ File: handlers_workflows.go:196
│
├─ Step 1: Validate workflow exists
│   └─> db.GetWorkflow(workflowID)
│
├─ Step 2: Build workflow definition from nodes/connections
│   └─> db.BuildWorkflowDefinition(workflowID, localPeerID)
│       ├─ File: workflows.go:269
│       ├─ Constructs jobs array with interfaces
│       └─ Updates workflow.definition JSON
│
├─ Step 3: Create workflow execution record
│   └─> db.CreateWorkflowExecution(workflowID)
│       └─ Status: PENDING
│
├─ Step 4: Start asynchronous execution
│   └─> workflowManager.ExecuteWorkflow(execution, passphrase)
│       └─ File: workflow_manager.go:202
│
└─ Return HTTP 202 (execution continues in background)
```

**Logs to check:**
- "Executing workflow: workflow_id=X"
- "Building workflow definition: workflow_id=X"
- "Created workflow execution: execution_id=X"
- "Starting workflow execution: execution_id=X"

---

### Two-Phase Execution Model

**File:** `internal/core/workflow_manager.go:202-419`

#### Why Two Phases?

**Problem:** Remote peers need to know about ALL jobs and their interconnections BEFORE starting execution.

**Solution:** Separate job creation from job execution.

---

#### Phase 1: Job Creation and Registration

```
WorkflowManager.ExecuteWorkflow(execution, passphrase)
├─ File: workflow_manager.go:202
│
├─ Update execution status: PENDING → RUNNING
│
└─> executeWorkflowJobs(execution)
    ├─ File: workflow_manager.go:247
    │
    ├─ For each job in workflow.definition.jobs:
    │   │
    │   ├─ Create workflow_job record (orchestrator tracking)
    │   │   └─> db.CreateWorkflowJob(executionID, workflowID, nodeID, jobName, serviceID, executorPeerID)
    │   │       └─ File: workflows.go:578
    │   │
    │   ├─ If job.ExecutorPeerID == localPeerID:
    │   │   └─ LOCAL EXECUTION:
    │   │       └─> createLocalJobExecution(workflowJob, jobDef, passphrase)
    │   │           ├─ File: workflow_manager.go:421
    │   │           ├─ Create job_execution record
    │   │           ├─ Create job_interfaces records
    │   │           ├─ Create job_interface_peers records
    │   │           └─ Status: IDLE (waiting for start command)
    │   │
    │   └─ Else (remote execution):
    │       └─ REMOTE EXECUTION:
    │           └─> sendRemoteJobRequest(workflowJob, jobDef, passphrase)
    │               ├─ File: workflow_manager.go:535
    │               ├─ Message: job_request
    │               │   ├─ ServiceID (local to orchestrator)
    │               │   ├─ ServiceName (peer uses this to find service)
    │               │   ├─ ServiceType
    │               │   ├─ Interfaces (with peer routing)
    │               │   └─ Entrypoint/Cmd
    │               │
    │               ├─ Peer creates job_execution on their side
    │               ├─ Peer responds with remote job_execution_id
    │               └─ Store in workflow_job.remote_job_execution_id
    │
    └─ All jobs created (IDLE status, awaiting start)
```

**Critical:** At this point, ALL job_executions exist (local and remote) with complete interface definitions, but NONE have started executing yet.

---

#### Phase 2: Start Command Distribution

```
executeWorkflowJobs(execution) [continued]
├─ Build complete routing mappings
│   └─> buildNodeIDToJobExecutionIDMap(execution)
│       ├─ File: workflow_manager.go:312
│       ├─ Maps: workflow_node.id → job_execution.id
│       └─ Needed for interface peer resolution
│
├─ Update all interface peers with final job execution IDs
│   └─> updateInterfacePeersWithJobExecutionIDs(execution, mappings)
│       ├─ File: workflow_manager.go:XXX (implicit in Phase 1)
│       └─ Sets peer_job_execution_id in job_interface_peers
│
└─> sendStartCommands(execution, mappings)
    ├─ File: workflow_manager.go:638
    │
    ├─ For each job in execution:
    │   │
    │   ├─ Prepare start command data:
    │   │   ├─ JobExecutionID
    │   │   ├─ Updated interfaces (with all peer job execution IDs)
    │   │   └─ Complete routing information
    │   │
    │   ├─ If job is local:
    │   │   └─> jobManager.HandleJobStart(startData)
    │   │       ├─ File: job_manager.go:140
    │   │       ├─ Mark: start_received = true
    │   │       └─ Add to job queue for execution
    │   │
    │   └─ If job is remote:
    │       └─> jobHandler.SendJobStartCommand(executorPeerID, startData)
    │           ├─ File: job_handler.go:XXX
    │           ├─ Message: job_start
    │           └─ Peer receives and handles same as local
    │
    └─ All jobs now have start_received = true
```

**Why This Matters:**
- Jobs can reference each other's execution IDs
- Data transfers know exact source/destination
- Dependency resolution works correctly
- All peers have complete workflow graph

**Logs to check:**
- "Creating workflow job: node_id=X, executor=Y"
- "Created local job execution: job_id=X"
- "Sending remote job request: peer_id=X, service_name=Y"
- "Received remote job execution ID: X"
- "Building node to job execution ID mappings"
- "Sending start command: job_execution_id=X, executor=Y"
- "Job start received: job_id=X"

---

### Job Scheduling

**File:** `internal/core/job_manager.go:96-172`

#### Job Queue System

```
JobManager.Start()
├─ File: job_manager.go:96
│
├─ Start background goroutine:
│   └─ processJobQueue()
│       ├─ Ticker: 10 seconds (configurable)
│       │
│       └─ Loop:
│           ├─> Query database for READY jobs
│           │   └─ SELECT * FROM job_executions
│           │       WHERE status = 'READY'
│           │       AND start_received = 1
│           │
│           ├─ For each ready job:
│           │   └─ Dispatch to worker pool:
│           │       └─> executeJob(job)
│           │           └─ File: job_manager.go:285
│           │
│           └─ Continue loop
```

#### Job State Transitions

```
Job Lifecycle:
┌─────────────────────────────────────────────────────────────┐
│                                                               │
│  IDLE → READY → RUNNING → COMPLETED/ERRORED/CANCELLED       │
│   ↑                                                           │
│   │                                                           │
│   └─── Created in Phase 1, waiting for start command         │
│                                                               │
└─────────────────────────────────────────────────────────────┘

State Details:
├─ IDLE:
│   ├─ Job created, interfaces defined
│   ├─ Waiting for start command (start_received = 0)
│   └─ Not eligible for execution
│
├─ READY:
│   ├─ Start command received (start_received = 1)
│   ├─ Execution constraint satisfied
│   └─ Eligible for worker dispatch
│
├─ RUNNING:
│   ├─ Worker executing job
│   ├─ Container running or data transferring
│   └─ Status updates sent to orchestrator
│
├─ COMPLETED:
│   ├─ Job finished successfully
│   ├─ Outputs transferred (if any)
│   └─ Terminal state
│
├─ ERRORED:
│   ├─ Job failed with error
│   ├─ Error message recorded
│   └─ Terminal state
│
└─ CANCELLED:
    ├─ Job manually cancelled
    └─ Terminal state
```

**File:** `internal/database/job_executions.go:XXX`

---

### Dependency Resolution

**File:** `internal/core/job_manager.go:174-283`

#### Execution Constraints

```go
// Set during workflow definition building
type JobExecution struct {
    ExecutionConstraint string // NONE or INPUTS_READY
    ConstraintDetail    string // JSON with details
}
```

#### Constraint Types

**1. NONE - Execute Immediately**
```
Conditions:
├─ Job has no incoming connections
├─ Start command received
└─ Immediately transitions IDLE → READY

Examples:
├─ DATA services (data already exists)
└─ Input jobs with no dependencies
```

**2. INPUTS_READY - Wait for Data**
```
Conditions:
├─ Job has incoming connections
├─ Start command received
├─ All input files/mounts must exist
└─ Transitions IDLE → READY when inputs present

Examples:
├─ DOCKER job consuming DATA service output
└─ Processing job with multiple input sources
```

#### Input Readiness Check

**File:** `internal/core/job_manager.go:174-244`

```
checkInputsReady(job)
├─ File: job_manager.go:174
│
├─ Get all job interfaces
│   └─> db.GetJobInterfaces(job.ID)
│
├─ For each interface:
│   ├─ Skip if not input type (STDOUT, STDERR, LOGS)
│   ├─> For each InterfacePeer with MountFunction == OUTPUT:
│   │   ├─ Build expected input path:
│   │   │   └─ BuildJobPath(pathInfo, PathTypeInput)
│   │   │       ├─ Format: /workflows/<orch>/<wf_job>/jobs/<sender>/<sender_job>/<receiver>/<receiver_job>/input/
│   │   │       └─ File: utils/path_utils.go:XXX
│   │   │
│   │   ├─ Check if path exists:
│   │   │   └─ os.Stat(expectedPath)
│   │   │
│   │   ├─ If NOT exists:
│   │   │   └─ Return false (not ready)
│   │   │
│   │   └─ If duty_acknowledged == false:
│   │       └─ Return false (transfer not complete)
│   │
│   └─ All inputs exist: Continue
│
└─ Return true (ready to execute)
```

**Logs to check:**
- "Checking inputs ready: job_id=X"
- "Expected input path: X"
- "Input path exists: X"
- "Input path missing: X (waiting)"
- "Duty acknowledged: false (waiting for transfer)"
- "All inputs ready: job_id=X"

**Potential Issues:**
- **Job stuck in IDLE** - Check if input paths constructed correctly
- **Path mismatch** - Sender and receiver path calculations differ
- **Missing peer_job_execution_id** - Phase 2 mappings not updated

---

### State Management

**File:** `internal/core/workflow_manager.go:31-44`

#### WorkflowExecution In-Memory State

```go
type WorkflowExecution struct {
    WorkflowExecutionID int64
    WorkflowID          int64
    Definition          *types.WorkflowDefinition

    // Job tracking
    WorkflowJobs  map[int64]*database.WorkflowJob     // workflow_job.id → job
    JobExecutions map[string]*database.JobExecution   // executor:job_id → job

    // Progress tracking
    CompletedJobs int

    // Status
    Status         string    // PENDING, RUNNING, COMPLETED, FAILED, CANCELLED
    StartedAt      time.Time
    CompletedAt    time.Time
    ErrorMessage   string

    // Thread safety
    mu sync.RWMutex
}
```

#### Active Workflows Registry

```go
type WorkflowManager struct {
    activeWorkflows map[int64]*WorkflowExecution // execution_id → execution
    mu              sync.RWMutex
}
```

**Usage:**
- Tracks in-progress workflow executions
- Enables status updates and monitoring
- Removed when workflow reaches terminal state

---

#### Periodic Status Updates

**File:** `internal/core/workflow_manager.go:1012-1062`

```
WorkflowManager.Start()
└─> startStatusUpdateLoop()
    ├─ Ticker: 10 seconds
    │
    └─ Loop:
        └─> updateActiveWorkflowStatuses()
            ├─ File: workflow_manager.go:1012
            │
            ├─ For each active workflow execution:
            │   │
            │   ├─ For each job in execution:
            │   │   │
            │   │   ├─ If job is local:
            │   │   │   └─> db.GetJobExecution(job.ID)
            │   │   │       └─ Get latest status from database
            │   │   │
            │   │   └─ If job is remote:
            │   │       └─> jobHandler.SendJobStatusRequest(executorPeerID, remoteJobID)
            │   │           ├─ File: job_handler.go:XXX
            │   │           ├─ Message: job_status_request
            │   │           ├─ Response: job_status_response
            │   │           │   ├─ Status
            │   │           │   ├─ ErrorMessage
            │   │           │   └─ StartedAt, EndedAt
            │   │           │
            │   │           └─> db.UpdateJobStatus(job.ID, response.Status, response.Error)
            │   │
            │   └─ Update in-memory execution state
            │
            └─> checkWorkflowCompletion(execution)
                ├─ Count completed/errored/cancelled jobs
                ├─ If all jobs terminal:
                │   ├─ Update execution status: COMPLETED or FAILED
                │   ├─ Set completed_at timestamp
                │   └─ Remove from activeWorkflows
                └─ Else: Continue monitoring
```

**Logs to check:**
- "Updating workflow statuses: active_count=X"
- "Requesting job status: peer_id=X, job_id=Y"
- "Received job status: job_id=X, status=Y"
- "Workflow completed: execution_id=X, status=Y"
- "Workflow failed: execution_id=X, error=Y"

---

## Data Transfer Between Nodes

### Interface System

**File:** `internal/types/job_types.go:38-53`

#### Interface Structure

```go
type JobInterface struct {
    Type          string  // STDIN, STDOUT, STDERR, LOGS, MOUNT
    Path          string  // Filesystem path or stream identifier
    InterfacePeers []*InterfacePeer
}

type InterfacePeer struct {
    PeerID             string  // Executor peer ID
    PeerWorkflowJobID  *int64  // Orchestrator's workflow_job.id
    PeerJobExecutionID *int64  // Executor's job_execution.id (set in Phase 2)
    PeerPath           string  // Path on peer's filesystem
    PeerFileName       *string // Optional filename override
    PeerMountFunction  string  // INPUT, OUTPUT, BOTH
    DutyAcknowledged   bool    // Transfer complete flag
}
```

#### Interface Types

**1. STDOUT - Standard Output**
```
Purpose: Job output data
Direction: Source job → Destination job
Type: Stream or file-based
```

**2. STDIN - Standard Input**
```
Purpose: Job input data
Direction: Source job → This job
Type: Stream
```

**3. MOUNT - Filesystem Mount**
```
Purpose: File/directory sharing
Direction: Bidirectional (based on MountFunction)
Type: Filesystem path
```

**4. STDERR - Standard Error**
```
Purpose: Error output
Direction: Job → Orchestrator/logs
Type: Stream
```

**5. LOGS - Execution Logs**
```
Purpose: Job execution logs
Direction: Job → Orchestrator
Type: File-based
```

---

### Path Hierarchy

**File:** `internal/utils/path_utils.go`

#### Hierarchical Directory Structure

```
Application Data Directory
└── workflows/
    └── <orchestrator_peer_id>/
        └── <workflow_job_id>/
            └── jobs/
                └── <executor_peer_id>/
                    └── <job_execution_id>/
                        ├── input/
                        │   └── <sender_peer_id>/
                        │       └── <sender_job_id>/
                        │           └── [files transferred from sender]
                        │
                        ├── output/
                        │   └── [job output files]
                        │
                        └── mounts/
                            └── <mount_path>/
                                └── [mounted files]
```

**Example:**
```
Workflow: peer_orchestrator/workflow_job_123/
├── Local DATA job (job_exec_1):
│   └── jobs/peer_orchestrator/1/
│       └── output/
│           └── dataset.csv
│
└── Remote DOCKER job (job_exec_2):
    └── jobs/peer_worker/2/
        ├── input/
        │   └── peer_orchestrator/1/
        │       └── dataset.csv (transferred from job 1)
        └── output/
            └── results.txt
```

#### Path Construction

**File:** `internal/utils/path_utils.go:XXX`

```go
func BuildJobPath(appPaths AppPaths, info JobPathInfo, pathType PathType) string {
    // Base: /app_data/workflows/<orchestrator>/<workflow_job>/jobs/<executor>/<job_exec>/
    basePath := filepath.Join(
        appPaths.WorkflowsDir,
        info.OrchestratorPeerID,
        fmt.Sprintf("%d", info.WorkflowJobID),
        "jobs",
        info.ExecutorPeerID,
        fmt.Sprintf("%d", info.JobExecutionID),
    )

    switch pathType {
    case PathTypeInput:
        // Add sender info for input paths
        return filepath.Join(basePath, "input", info.SenderPeerID,
            fmt.Sprintf("%d", info.SenderJobExecutionID))

    case PathTypeOutput:
        return filepath.Join(basePath, "output")

    case PathTypeMount:
        return filepath.Join(basePath, "mounts", info.MountPath)
    }
}
```

---

### Transfer Protocol

**File:** `internal/workers/data_worker.go`

#### Transfer Initiation

```
Job Completion → Trigger Output Transfers
├─ File: job_manager.go:XXX (after job completes)
│
└─> transferOutputs(job)
    ├─ Get all STDOUT and MOUNT interfaces
    │
    ├─ For each interface:
    │   ├─ Scan output directory for files
    │   │
    │   └─ For each InterfacePeer with MountFunction == INPUT:
    │       │
    │       ├─> dataWorker.InitiateTransfer(transferSpec)
    │       │   ├─ File: data_worker.go:XXX
    │       │   │
    │       │   ├─ Build transfer specification:
    │       │   │   ├─ Source path (local output)
    │       │   │   ├─ Destination peer
    │       │   │   ├─ Destination path (peer's input)
    │       │   │   └─ Encryption (if passphrase set)
    │       │   │
    │       │   └─> Send transfer_request to destination peer
    │       │       └─ Message: data_transfer_request
    │       │           ├─ TransferID (UUID)
    │       │           ├─ SourceJobExecutionID
    │       │           ├─ DestJobExecutionID
    │       │           ├─ SourcePath
    │       │           ├─ DestPath
    │       │           ├─ FileSize
    │       │           └─ Encrypted (bool)
    │       │
    │       └─ Logs: "Initiating transfer: job_id=X, peer=Y, path=Z"
```

#### Transfer Acceptance

```
Destination Peer Receives transfer_request
├─ File: data_handler.go:XXX
│
├─> HandleTransferRequest(request)
│   ├─ Validate request
│   ├─ Create destination directory
│   ├─ Allocate buffer for receiving
│   │
│   └─> Send transfer_accept response
│       └─ Message: data_transfer_accept
│           └─ TransferID
│
└─ Wait for data chunks
```

#### Data Streaming

```
Source Peer Receives transfer_accept
├─> startDataStreaming(transferID)
│   ├─ File: data_worker.go:XXX
│   │
│   ├─ Open source file
│   ├─ Read in chunks (default: 1MB)
│   │
│   └─ For each chunk:
│       ├─ If encrypted:
│       │   └─> Encrypt chunk with passphrase
│       │
│       └─> Send data_chunk message
│           ├─ TransferID
│           ├─ ChunkIndex
│           ├─ ChunkData (base64 encoded)
│           └─ IsLastChunk (bool)
│
└─ All chunks sent
```

#### Transfer Completion

```
Destination Peer Receives Chunks
├─ For each chunk:
│   ├─ Verify ChunkIndex sequence
│   ├─ If encrypted:
│   │   └─> Decrypt chunk
│   ├─ Write to destination file
│   └─ Send chunk_ack
│
├─ After last chunk:
│   ├─ Close file
│   ├─ Verify file size
│   │
│   └─> Send transfer_complete
│       └─ Message: data_transfer_complete
│           ├─ TransferID
│           ├─ Success (bool)
│           └─ BytesReceived
│
└─ Update database: duty_acknowledged = true
```

#### Source Receives Completion

```
Source Peer Receives transfer_complete
├─> markTransferComplete(transferID)
│   ├─ Update job_interface_peers:
│   │   └─ SET duty_acknowledged = true
│   │
│   ├─ Log: "Transfer completed: transfer_id=X, bytes=Y"
│   │
│   └─> Notify destination job manager
│       └─ Trigger checkInputsReady() on destination job
│           └─ May transition IDLE → READY
```

**Logs to check:**
- "Initiating transfer: transfer_id=X, source=Y, dest=Z"
- "Transfer request sent: size=X bytes"
- "Transfer accepted: transfer_id=X"
- "Sending chunk: transfer_id=X, chunk=Y/Z"
- "Chunk received: transfer_id=X, chunk=Y"
- "Transfer completed: transfer_id=X, bytes=Y"
- "Duty acknowledged: interface_peer_id=X"
- "Inputs ready: job_id=X (transitioning to READY)"

---

## Node Execution Details

### DATA Service Execution

**File:** `internal/core/job_manager.go:350-378`

```
executeDataService(job, service)
├─ File: job_manager.go:350
│
├─ Step 1: Get job interfaces
│   └─> db.GetJobInterfaces(job.ID)
│       └─ Includes InterfacePeers with routing info
│
├─ Step 2: Locate data files
│   ├─ Source path: service.ServicePath (encrypted file location)
│   └─ Or: Decrypted cache location
│
├─ Step 3: Delegate to data worker
│   └─> dataWorker.ExecuteDataService(job, service)
│       ├─ File: workers/data_worker.go:XXX
│       │
│       ├─ For each STDOUT interface:
│       │   └─ For each InterfacePeer:
│       │       ├─ Build transfer spec
│       │       └─> initiateTransfer(spec)
│       │
│       └─ Wait for all transfers to complete
│
└─ Step 4: Update status
    └─> db.UpdateJobStatus(job.ID, COMPLETED, "")
```

**DATA Service Characteristics:**
- No computation (files already exist)
- Execution = Data transfer initiation
- Completes immediately after transfers start
- Transfers happen asynchronously

**Logs to check:**
- "Executing DATA service: job_id=X, service_id=Y"
- "DATA service path: X"
- "Initiating transfers for DATA service: count=X"
- "DATA service execution completed: job_id=X"

---

### DOCKER Service Execution

**File:** `internal/core/job_manager.go:380-631`

```
executeDockerService(job, service)
├─ File: job_manager.go:380
│
├─ Step 1: Get job configuration
│   ├─ Entrypoint: job.Entrypoint (JSON array)
│   ├─ Commands: job.Commands (JSON array)
│   └─ Interfaces: db.GetJobInterfaces(job.ID)
│
├─ Step 2: Build hierarchical paths
│   ├─> BuildJobPath(appPaths, pathInfo, PathTypeOutput)
│   │   └─ outputDir = .../output/
│   │
│   ├─> BuildJobPath(appPaths, pathInfo, PathTypeInput)
│   │   └─ inputDir = .../input/
│   │
│   └─ Create directories if not exist
│
├─ Step 3: Process interfaces → Docker mounts
│   ├─ buildMountsFromInterfaces(interfaces, basePaths)
│   │   ├─ File: job_manager.go:450
│   │   │
│   │   ├─ For each MOUNT interface:
│   │   │   ├─ Build host path (hierarchical)
│   │   │   ├─ Container path: interface.Path
│   │   │   └─ Add mount: hostPath:containerPath:rw
│   │   │
│   │   ├─ For each STDIN interface:
│   │   │   └─ Mount as: inputDir:/input:ro
│   │   │
│   │   └─ For each STDOUT interface:
│   │       └─ Mount as: outputDir:/output:rw
│   │
│   └─ mounts = ["/host/path:/container/path", ...]
│
├─ Step 4: Build environment variables (future)
│   └─ envVars = {"VAR": "value"}
│
├─ Step 5: Execute Docker container
│   └─> dockerService.Execute(service, entrypoint, commands, mounts, envVars)
│       ├─ File: services/docker_service_execution.go:XXX
│       │
│       ├─ Pull image (if not cached):
│       │   └─> docker pull <image>
│       │
│       ├─ Create container:
│       │   └─> docker create
│       │       --name job-<job_id>
│       │       --entrypoint <entrypoint>
│       │       <mounts...>
│       │       <env_vars...>
│       │       <image> <commands...>
│       │
│       ├─ Start container:
│       │   └─> docker start <container_id>
│       │
│       ├─ Stream logs:
│       │   └─> docker logs -f <container_id>
│       │       └─ Write to logs directory
│       │
│       ├─ Wait for completion:
│       │   └─> docker wait <container_id>
│       │
│       ├─ Get exit code:
│       │   └─> docker inspect --format='{{.State.ExitCode}}' <container_id>
│       │
│       └─ Cleanup:
│           └─> docker rm <container_id>
│
├─ Step 6: Check execution result
│   ├─ If exitCode == 0:
│   │   └─> db.UpdateJobStatus(job.ID, COMPLETED, "")
│   └─ Else:
│       └─> db.UpdateJobStatus(job.ID, ERRORED, "Container exited with code X")
│
└─ Step 7: Transfer outputs (if successful)
    └─> transferOutputs(job, interfaces)
        └─ Scan outputDir for files
        └─ Initiate transfers to connected peers
```

**Docker Execution Details:**

**Image Pull:**
- Pulls from Docker Hub or custom registry
- Respects Docker authentication (docker login)
- Caches locally for future executions

**Container Naming:**
```
job-<job_execution_id>
Example: job-42
```

**Mount Examples:**
```
Input mount:
  /app_data/workflows/peer1/123/jobs/peer2/456/input:/input:ro

Output mount:
  /app_data/workflows/peer1/123/jobs/peer2/456/output:/output:rw

Custom mount:
  /app_data/workflows/peer1/123/jobs/peer2/456/mounts/data:/data:rw
```

**Logs to check:**
- "Executing DOCKER service: job_id=X, image=Y"
- "Building mounts: count=X"
- "Pulling Docker image: X"
- "Creating container: name=job-X"
- "Starting container: container_id=X"
- "Streaming logs: job_id=X"
- "Container exited: code=X"
- "DOCKER service completed: job_id=X, status=Y"
- "Transferring outputs: count=X files"

---

### STANDALONE Service Execution

**File:** `internal/core/job_manager.go:633-XXX`

```
executeStandaloneService(job, service)
├─ Similar to Docker execution, but:
│   ├─ No container management
│   ├─ Direct process spawn
│   └─ Filesystem mounts via symlinks or file operations
│
├─ Step 1: Get executable path
│   └─ service.ServicePath (binary location)
│
├─ Step 2: Build arguments
│   └─ Merge entrypoint + commands
│
├─ Step 3: Set working directory
│   └─ workingDir = outputDir
│
├─ Step 4: Execute process
│   └─> exec.Command(executable, args...)
│       ├─ Set working directory
│       ├─ Set environment variables
│       ├─ Redirect stdout/stderr to files
│       └─ cmd.Run()
│
├─ Step 5: Check exit code
│   └─ Similar to Docker
│
└─ Step 6: Transfer outputs
    └─ Same as Docker
```

**STANDALONE Characteristics:**
- No Docker dependency
- Faster startup (no container overhead)
- Platform-dependent (binary must match peer OS/arch)
- Less isolation (runs directly on host)

**Logs to check:**
- "Executing STANDALONE service: job_id=X, binary=Y"
- "Setting working directory: X"
- "Executing process: command=X, args=Y"
- "Process exited: code=X"
- "STANDALONE service completed: job_id=X"

---

## Complete Data Flows

### Flow 1: Workflow Creation

```
User creates workflow in UI
│
├─ Step 1: Create workflow record
│   └─> POST /api/workflows
│       ├─ Body: {name: "My Workflow", description: "..."}
│       └─ Response: {id: 123}
│
├─ Step 2: Add nodes (drag services onto canvas)
│   └─> POST /api/workflows/123/nodes
│       ├─ Body: {
│       │   service_id: 456,
│       │   peer_id: "peer_abc",
│       │   service_name: "Train Model",
│       │   service_type: "DOCKER",
│       │   gui_x: 100, gui_y: 200,
│       │   entrypoint: ["python3"],
│       │   cmd: ["train.py"]
│       │ }
│       └─ Response: {node_id: 789}
│
├─ Step 3: Create connections (drag from output to input)
│   └─> POST /api/workflows/123/connections
│       ├─ Body: {
│       │   from_node_id: 789,
│       │   from_interface_type: "STDOUT",
│       │   to_node_id: 790,
│       │   to_interface_type: "MOUNT"
│       │ }
│       └─ Response: {connection_id: 111}
│
└─ Step 4: Save UI state
    └─> PUT /api/workflows/123/ui_state
        └─ Body: {zoom: 1.0, pan_x: 0, pan_y: 0}
```

**Total time:** <1 second (all database operations)

---

### Flow 2: Workflow Execution Start

```
User clicks "Execute" button
│
├─> POST /api/workflows/123/execute
│   └─ Body: {passphrase: "optional"}
│
├─ API Handler:
│   ├─ File: handlers_workflows.go:196
│   │
│   ├─ Get workflow
│   ├─> BuildWorkflowDefinition(workflowID, localPeerID)
│   │   ├─ File: workflows.go:269
│   │   ├─ Query nodes and connections
│   │   ├─ Build jobs array with interfaces
│   │   └─ Update workflow.definition
│   │
│   ├─> CreateWorkflowExecution(workflowID)
│   │   └─ Status: PENDING
│   │
│   └─> workflowManager.ExecuteWorkflow(execution, passphrase)
│       └─ Async goroutine starts
│
├─ Return HTTP 202 immediately
│
└─ Background: Two-Phase Execution
    │
    ├─ PHASE 1: Job Creation
    │   ├─ For each job in workflow.definition:
    │   │   ├─ Create workflow_job
    │   │   ├─ If local:
    │   │   │   └─ Create job_execution (IDLE)
    │   │   └─ If remote:
    │   │       ├─ Send job_request to peer
    │   │       └─ Receive remote_job_execution_id
    │   │
    │   └─ All jobs created (5 jobs in 2 seconds)
    │
    └─ PHASE 2: Start Commands
        ├─ Build node → job execution ID mappings
        ├─ Update all interface peers with job IDs
        ├─ For each job:
        │   ├─ If local:
        │   │   └─ jobManager.HandleJobStart()
        │   └─ If remote:
        │       └─ Send job_start message
        │
        └─ All jobs now eligible for execution (1 second)
```

**Total time:** ~3-5 seconds (depending on number of remote jobs)

---

### Flow 3: Job Execution (DOCKER with Data Input)

```
Job Queue Ticker (every 10s)
├─> processJobQueue()
│   ├─ Query: SELECT * FROM job_executions WHERE status = 'READY'
│   └─ Found: Job 456 (DOCKER service)
│
├─> executeJob(job456)
│   ├─ File: job_manager.go:285
│   │
│   ├─ Update status: READY → RUNNING
│   │
│   └─> executeDockerService(job456, service)
│       ├─ File: job_manager.go:380
│       │
│       ├─ Get interfaces:
│       │   ├─ MOUNT: /input (receives from DATA job 123)
│       │   └─ MOUNT: /output (sends to DOCKER job 789)
│       │
│       ├─ Build paths:
│       │   ├─ outputDir: /workflows/peer_orch/10/jobs/peer_worker/456/output/
│       │   ├─ inputDir: /workflows/peer_orch/10/jobs/peer_worker/456/input/peer_data/123/
│       │   └─ Input already exists (transferred from job 123)
│       │
│       ├─ Build mounts:
│       │   ├─ inputDir:/input:ro
│       │   └─ outputDir:/output:rw
│       │
│       └─> dockerService.Execute()
│           ├─ Pull image: pytorch/pytorch:latest (if needed)
│           ├─ Create container: job-456
│           ├─ Start container
│           ├─ Stream logs
│           ├─ Wait for completion
│           ├─ Container exits: code=0
│           └─ Return success
│
├─ Update status: RUNNING → COMPLETED
│
└─> transferOutputs(job456)
    ├─ Scan: /workflows/peer_orch/10/jobs/peer_worker/456/output/
    ├─ Found: model.pth (500MB)
    │
    └─ For MOUNT interface → Job 789:
        ├─> dataWorker.InitiateTransfer()
        │   ├─ Send: transfer_request to peer_final
        │   ├─ Receive: transfer_accept
        │   ├─ Stream: 500 chunks (1MB each)
        │   └─ Receive: transfer_complete
        │
        └─ Mark: duty_acknowledged = true
```

**Total time:** Variable (depends on job execution duration + transfer size)
- Container startup: 2-5 seconds
- Job execution: Varies (seconds to hours)
- Transfer: ~1 minute per 100MB

---

### Flow 4: Data Transfer Between Jobs

```
Job A completes (DATA service)
├─ outputDir: /workflows/peer_orch/10/jobs/peer_data/123/output/dataset.csv
│
├─> transferOutputs(jobA)
│   ├─ Interface: STDOUT
│   ├─ InterfacePeer:
│   │   ├─ PeerID: peer_worker
│   │   ├─ PeerJobExecutionID: 456
│   │   └─ MountFunction: INPUT (peer_worker receives)
│   │
│   └─> dataWorker.InitiateTransfer()
│       └─ transferSpec:
│           ├─ SourcePath: /workflows/.../output/dataset.csv
│           ├─ DestPeer: peer_worker
│           ├─ DestJobExecID: 456
│           └─ DestPath: /workflows/.../input/peer_data/123/dataset.csv
│
├─ Send: transfer_request
│   └─ To: peer_worker
│
├─ peer_worker receives request:
│   ├─ Create destination directory
│   ├─ Validate path
│   └─ Send: transfer_accept
│
├─ Receive: transfer_accept
│   ├─> startDataStreaming()
│   │   ├─ Open source file: dataset.csv (10MB)
│   │   ├─ Read chunks: 10 chunks (1MB each)
│   │   │
│   │   └─ For each chunk:
│   │       ├─ Send: data_chunk (chunk_index, data)
│   │       └─ Receive: chunk_ack
│   │
│   └─ Send: transfer_complete
│
├─ peer_worker receives chunks:
│   ├─ Write to: .../input/peer_data/123/dataset.csv
│   ├─ Verify size: 10MB ✓
│   └─ Send: transfer_complete confirmation
│
├─ Receive: transfer_complete
│   └─> markTransferComplete()
│       ├─ Update: duty_acknowledged = true
│       └─ Notify: peer_worker job manager
│
└─ peer_worker: Job 456 inputs ready check
    ├─> checkInputsReady(job456)
    │   ├─ Check: .../input/peer_data/123/dataset.csv exists ✓
    │   ├─ Check: duty_acknowledged = true ✓
    │   └─ Return: true
    │
    └─ Transition: IDLE → READY
```

**Total time:** ~1-2 seconds for 10MB file

---

## Key Data Structures

### Workflow Definition (JSON)

```json
{
  "jobs": [
    {
      "node_id": 123,
      "name": "Load Dataset",
      "service_id": 456,
      "service_type": "DATA",
      "executor_peer_id": "peer_data",
      "execution_constraint": "NONE",
      "interfaces": [
        {
          "type": "STDOUT",
          "path": "/output/dataset.csv",
          "interface_peers": [
            {
              "peer_id": "peer_worker",
              "peer_workflow_job_id": 124,
              "peer_job_execution_id": null,
              "peer_path": "/input/dataset.csv",
              "peer_mount_function": "INPUT"
            }
          ]
        }
      ]
    },
    {
      "node_id": 124,
      "name": "Train Model",
      "service_id": 789,
      "service_type": "DOCKER",
      "executor_peer_id": "peer_worker",
      "execution_constraint": "INPUTS_READY",
      "entrypoint": ["python3"],
      "cmd": ["train.py", "--epochs", "10"],
      "interfaces": [
        {
          "type": "MOUNT",
          "path": "/input",
          "interface_peers": [
            {
              "peer_id": "peer_data",
              "peer_workflow_job_id": 123,
              "peer_job_execution_id": null,
              "peer_path": "/output/dataset.csv",
              "peer_mount_function": "OUTPUT"
            }
          ]
        },
        {
          "type": "MOUNT",
          "path": "/output",
          "interface_peers": [
            {
              "peer_id": "peer_orchestrator",
              "peer_workflow_job_id": null,
              "peer_job_execution_id": null,
              "peer_path": "/results/model.pth",
              "peer_mount_function": "INPUT"
            }
          ]
        }
      ]
    }
  ]
}
```

### Database Schema Summary

```sql
-- Workflow container
CREATE TABLE workflows (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    definition TEXT NOT NULL,  -- JSON with jobs array
    created_at DATETIME,
    updated_at DATETIME
);

-- Visual nodes
CREATE TABLE workflow_nodes (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER,
    service_id INTEGER,
    peer_id TEXT,
    service_name TEXT,
    service_type TEXT,
    "order" INTEGER,
    gui_x INTEGER,
    gui_y INTEGER,
    interfaces TEXT,      -- JSON
    entrypoint TEXT,      -- JSON
    cmd TEXT,             -- JSON
    pricing_amount REAL,
    pricing_type TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

-- Data flow edges
CREATE TABLE workflow_connections (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER,
    from_node_id INTEGER,
    from_interface_type TEXT,
    to_node_id INTEGER,
    to_interface_type TEXT,
    destination_file_name TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

-- Execution instances
CREATE TABLE workflow_executions (
    id INTEGER PRIMARY KEY,
    workflow_id INTEGER,
    status TEXT CHECK(status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')),
    error TEXT,
    started_at DATETIME,
    completed_at DATETIME,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

-- Job tracking (orchestrator)
CREATE TABLE workflow_jobs (
    id INTEGER PRIMARY KEY,
    workflow_execution_id INTEGER,
    workflow_id INTEGER,
    node_id INTEGER,
    job_name TEXT,
    service_id INTEGER,
    executor_peer_id TEXT,
    remote_job_execution_id INTEGER,
    status TEXT,
    result TEXT,
    error TEXT,
    FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions(id)
);

-- Job execution (both local and remote)
CREATE TABLE job_executions (
    id INTEGER PRIMARY KEY,
    workflow_job_id INTEGER,
    service_id INTEGER,
    executor_peer_id TEXT,
    ordering_peer_id TEXT,
    remote_job_execution_id INTEGER,
    status TEXT CHECK(status IN ('IDLE', 'READY', 'RUNNING', 'COMPLETED', 'ERRORED', 'CANCELLED')),
    entrypoint TEXT,
    commands TEXT,
    execution_constraint TEXT CHECK(execution_constraint IN ('NONE', 'INPUTS_READY')),
    constraint_detail TEXT,
    start_received INTEGER DEFAULT 0,
    started_at DATETIME,
    ended_at DATETIME,
    error_message TEXT
);

-- Job interfaces
CREATE TABLE job_interfaces (
    id INTEGER PRIMARY KEY,
    job_execution_id INTEGER,
    interface_type TEXT CHECK(interface_type IN ('STDIN', 'STDOUT', 'STDERR', 'LOGS', 'MOUNT')),
    path TEXT,
    FOREIGN KEY (job_execution_id) REFERENCES job_executions(id)
);

-- Interface peer routing
CREATE TABLE job_interface_peers (
    id INTEGER PRIMARY KEY,
    job_interface_id INTEGER,
    peer_id TEXT,
    peer_workflow_job_id INTEGER,
    peer_job_execution_id INTEGER,
    peer_path TEXT,
    peer_file_name TEXT,
    peer_mount_function TEXT CHECK(peer_mount_function IN ('INPUT', 'OUTPUT', 'BOTH')),
    duty_acknowledged BOOLEAN DEFAULT 0,
    FOREIGN KEY (job_interface_id) REFERENCES job_interfaces(id)
);
```

---

## Debugging Guide

### Common Issues and Solutions

#### Issue 1: Job Stuck in IDLE State

**Symptoms:**
- Job created but never transitions to READY
- Workflow execution hangs indefinitely
- No execution logs

**Investigation:**
```bash
# Check job status
sqlite3 app.db "SELECT id, status, execution_constraint, start_received FROM job_executions WHERE id = X;"

# Check if start command received
# start_received should be 1
```

**Common Causes:**
1. **Phase 2 never executed** - Start command not sent
2. **start_received = 0** - Job waiting for start command
3. **Execution constraint blocking** - Waiting for inputs

**Fix Locations:**
- `internal/core/workflow_manager.go:638` - Check sendStartCommands() called
- `internal/core/job_manager.go:140` - Verify HandleJobStart() executed
- Check logs for "Sending start command" and "Job start received"

---

#### Issue 2: Job Stuck Waiting for Inputs (INPUTS_READY)

**Symptoms:**
- Job status: IDLE with start_received = 1
- Execution constraint: INPUTS_READY
- checkInputsReady() returns false

**Investigation:**
```bash
# Check job interfaces
sqlite3 app.db "
  SELECT ji.interface_type, ji.path,
         jip.peer_id, jip.peer_mount_function, jip.duty_acknowledged
  FROM job_interfaces ji
  JOIN job_interface_peers jip ON ji.id = jip.job_interface_id
  WHERE ji.job_execution_id = X;
"

# Check if input paths exist
ls -la /workflows/*/*/jobs/*/*/input/*/*/

# Check duty_acknowledged flags
# All should be TRUE (1) for inputs ready
```

**Common Causes:**
1. **Input path doesn't exist** - Data transfer never completed
2. **duty_acknowledged = false** - Transfer in progress or failed
3. **Wrong path construction** - Sender/receiver path mismatch
4. **peer_job_execution_id = null** - Phase 2 mappings not updated

**Fix Locations:**
- `internal/core/job_manager.go:174` - Check checkInputsReady() logic
- `internal/utils/path_utils.go` - Verify BuildJobPath() consistency
- `internal/workers/data_worker.go` - Check transfer completion
- `internal/core/workflow_manager.go:312` - Verify mapping updates

**Debug Logging:**
```go
// Add to checkInputsReady()
logger.Info("Checking input path",
    "path", expectedPath,
    "exists", pathExists,
    "duty_acknowledged", interfacePeer.DutyAcknowledged)
```

---

#### Issue 3: Data Transfer Fails or Hangs

**Symptoms:**
- Transfer initiated but never completes
- duty_acknowledged remains false
- Destination job never receives data

**Investigation:**
```bash
# Check data worker logs
grep "Initiating transfer" logs/peer.log
grep "Transfer request sent" logs/peer.log
grep "Transfer accepted" logs/peer.log
grep "Transfer completed" logs/peer.log

# Check if destination peer received request
# On destination peer:
grep "Received transfer request" logs/peer.log
```

**Common Causes:**
1. **Peer connectivity lost** - QUIC connection dropped
2. **Path permission errors** - Can't create destination directory
3. **Encryption/decryption mismatch** - Wrong passphrase
4. **Large file timeout** - Transfer exceeds timeout

**Fix Locations:**
- `internal/workers/data_worker.go` - Check transfer initiation
- `internal/p2p/data_handler.go` - Check transfer request handling
- Check both source and destination peer logs
- Verify QUIC connection: `grep "QUIC connection" logs/peer.log`

**Troubleshooting Steps:**
1. Verify source file exists: `ls -la /workflows/.../output/`
2. Check destination peer online: `ping <peer_ip>`
3. Test QUIC connectivity: Look for recent ping/pong messages
4. Check passphrase consistency across peers

---

#### Issue 4: Remote Job Never Starts

**Symptoms:**
- workflow_job created with remote executor
- remote_job_execution_id = null
- No job execution on remote peer

**Investigation:**
```bash
# Check workflow_jobs table
sqlite3 app.db "
  SELECT id, executor_peer_id, remote_job_execution_id, status
  FROM workflow_jobs
  WHERE workflow_execution_id = X;
"

# Check job request sent
grep "Sending remote job request" logs/orchestrator.log

# On remote peer, check if request received
grep "Received job request" logs/remote_peer.log
```

**Common Causes:**
1. **Peer unreachable** - Not connected to network
2. **Service not found on remote peer** - service_id is local, service_name lookup failed
3. **Job request timeout** - No response from peer
4. **Permission denied** - Remote peer rejected job request

**Fix Locations:**
- `internal/core/workflow_manager.go:535` - Check sendRemoteJobRequest()
- `internal/p2p/job_handler.go` - Check HandleJobRequest()
- Verify peer connectivity with relay or direct connection
- Check service exists on remote: `SELECT * FROM offered_services WHERE name = 'X';`

**Service Name Lookup:**
```
Orchestrator uses:
  ├─ service_id: 123 (local database)
  └─ service_name: "Train Model" (sent to remote)

Remote peer looks up:
  └─ SELECT * FROM offered_services WHERE name = "Train Model"
```

---

#### Issue 5: Workflow Never Completes

**Symptoms:**
- Some jobs completed
- Workflow status stuck in RUNNING
- Status update loop continues indefinitely

**Investigation:**
```bash
# Check job statuses
sqlite3 app.db "
  SELECT je.id, je.status, je.error_message
  FROM job_executions je
  JOIN workflow_jobs wj ON je.workflow_job_id = wj.id
  WHERE wj.workflow_execution_id = X;
"

# Count terminal states
# All jobs should be COMPLETED, ERRORED, or CANCELLED
```

**Common Causes:**
1. **Orphaned job in non-terminal state** - Job stuck in RUNNING
2. **Status update loop failure** - Remote status requests failing
3. **Workflow completion check bug** - Terminal state count incorrect

**Fix Locations:**
- `internal/core/workflow_manager.go:1012` - Check updateActiveWorkflowStatuses()
- `internal/core/workflow_manager.go:XXX` - Check checkWorkflowCompletion()
- Manually update orphaned job: `UPDATE job_executions SET status = 'ERRORED' WHERE id = X;`

**Manual Completion:**
```sql
-- Find non-terminal jobs
SELECT id, status FROM job_executions
WHERE workflow_job_id IN (
  SELECT id FROM workflow_jobs WHERE workflow_execution_id = X
) AND status NOT IN ('COMPLETED', 'ERRORED', 'CANCELLED');

-- Force completion
UPDATE workflow_executions SET status = 'COMPLETED', completed_at = datetime('now')
WHERE id = X;
```

---

#### Issue 6: Docker Container Fails to Start

**Symptoms:**
- Job status: ERRORED
- Error message: "Failed to create container" or "Image pull failed"

**Investigation:**
```bash
# Check Docker service logs
grep "Executing DOCKER service" logs/peer.log
grep "Creating container" logs/peer.log
grep "Container exited" logs/peer.log

# Check Docker daemon
docker ps -a | grep job-

# Check image availability
docker images | grep <image_name>
```

**Common Causes:**
1. **Image doesn't exist** - Typo in image name or not pulled
2. **Docker daemon not running** - Service stopped
3. **Mount path errors** - Invalid host paths
4. **Resource limits** - Out of memory or disk space

**Fix Locations:**
- `internal/services/docker_service_execution.go` - Check Execute()
- Verify image name in service definition
- Check Docker daemon: `systemctl status docker`
- Check disk space: `df -h`

**Test Docker Manually:**
```bash
# Test image pull
docker pull pytorch/pytorch:latest

# Test container creation with same mounts
docker create --name test-job \
  -v /workflows/.../output:/output:rw \
  -v /workflows/.../input:/input:ro \
  pytorch/pytorch:latest python3 train.py
```

---

### Log Correlation Examples

#### Successful Workflow Execution

**Orchestrator Logs:**
```
[INFO] Executing workflow: workflow_id=10, execution_id=50
[INFO] Building workflow definition: workflow_id=10
[INFO] Phase 1: Creating jobs
[INFO] Creating workflow job: node_id=1, executor=peer_data
[INFO] Created local job execution: job_id=100
[INFO] Creating workflow job: node_id=2, executor=peer_worker
[INFO] Sending remote job request: peer_id=peer_worker, service_name=Train Model
[INFO] Received remote job execution ID: 200
[INFO] All jobs created: count=2
[INFO] Phase 2: Building mappings
[INFO] Node to job execution ID: {1: 100, 2: 200}
[INFO] Sending start commands
[INFO] Sent start command: job_id=100 (local)
[INFO] Sent start command: job_id=200 (remote peer_worker)
[INFO] Workflow execution started: execution_id=50
```

**Job Execution Logs (Orchestrator):**
```
[INFO] Job queue processing: found 1 READY jobs
[INFO] Executing job: job_id=100, type=DATA
[INFO] Executing DATA service: job_id=100, service_id=10
[INFO] Initiating transfers for DATA service: count=1
[INFO] Transfer request sent: transfer_id=uuid-123, dest=peer_worker
[INFO] Transfer accepted: transfer_id=uuid-123
[INFO] Sending chunks: transfer_id=uuid-123, total=10
[INFO] Transfer completed: transfer_id=uuid-123, bytes=10485760
[INFO] Duty acknowledged: interface_peer_id=50
[INFO] DATA service execution completed: job_id=100
```

**Remote Peer Logs:**
```
[INFO] Received job request: orchestrator=peer_orch, service_name=Train Model
[INFO] Found service: id=25, name=Train Model, type=DOCKER
[INFO] Created job execution: id=200
[INFO] Sending job request response: remote_job_id=200
[INFO] Received job start command: job_id=200
[INFO] Job start received: job_id=200
[INFO] Received transfer request: transfer_id=uuid-123, size=10MB
[INFO] Transfer accepted: transfer_id=uuid-123
[INFO] Receiving chunks: 1/10, 2/10, ... 10/10
[INFO] Transfer completed: transfer_id=uuid-123
[INFO] Duty acknowledged: interface_peer_id=75
[INFO] Checking inputs ready: job_id=200
[INFO] All inputs ready: job_id=200
[INFO] Job transitioning: IDLE → READY
[INFO] Executing job: job_id=200, type=DOCKER
[INFO] Pulling Docker image: pytorch/pytorch:latest
[INFO] Creating container: name=job-200
[INFO] Starting container: container_id=abc123
[INFO] Container exited: code=0
[INFO] DOCKER service completed: job_id=200
[INFO] Transferring outputs: count=1 files
```

**Timeline:** ~2 minutes (5s setup + 1s transfer + 90s execution + 10s output transfer)

---

### Performance Tuning

**Job Queue Interval:**
- Default: 10 seconds
- Decrease for faster job dispatch (higher CPU usage)
- Increase for lower overhead (slower response)

**Status Update Interval:**
- Default: 10 seconds
- Affects workflow completion detection time
- Balance between responsiveness and network overhead

**Data Transfer Chunk Size:**
- Default: 1MB
- Larger chunks: Faster for large files, more memory
- Smaller chunks: Better for unreliable networks

**Workflow Definition Caching:**
- Built once per execution
- Cached in workflow.definition JSON
- Rebuild if nodes/connections change

---

## Conclusion

This document provides comprehensive coverage of workflow creation and execution. Use it to:

1. **Understand workflow architecture** - Two-phase execution, job lifecycle, data flow
2. **Debug execution issues** - Job stuck in IDLE/READY, transfer failures, remote job issues
3. **Trace data flows** - Interface system, path hierarchy, transfer protocol
4. **Optimize performance** - Tune intervals, chunk sizes, parallelism
5. **Extend functionality** - Add new node types, interface types, constraints

For additional debugging, enable verbose logging in:
- `internal/core/workflow_manager.go` - Workflow orchestration
- `internal/core/job_manager.go` - Job execution
- `internal/workers/data_worker.go` - Data transfers
- `internal/p2p/job_handler.go` - P2P job messaging

---

## Related Documentation

### Workflow & Services
- [STANDALONE_SERVICES_API.md](./STANDALONE_SERVICES_API.md) - Standalone service creation and management
- [DOCKER_SERVICE_QUICKSTART.md](./DOCKER_SERVICE_QUICKSTART.md) - Docker service setup
- [VERIFIABLE_COMPUTE_DESIGN.md](./VERIFIABLE_COMPUTE_DESIGN.md) - Verifiable computation architecture

### Security & Encryption
- [ENCRYPTION_ARCHITECTURE.md](./ENCRYPTION_ARCHITECTURE.md) - DATA service encryption details
- [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md) - Node identity key management

### API & Architecture
- [API_REFERENCE.md](./API_REFERENCE.md) - HTTP/WebSocket API reference
- [WEBSOCKET_API.md](./WEBSOCKET_API.md) - Real-time WebSocket events
- [ARCHITECTURE.md](./ARCHITECTURE.md) - System architecture overview
