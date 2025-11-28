package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Workflow represents a workflow definition
type Workflow struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Definition  map[string]interface{} `json:"definition"` // JSON structure with nodes and edges
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// WorkflowExecution represents a single execution instance of a workflow
type WorkflowExecution struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	Status     string    `json:"status"` // "pending", "running", "completed", "failed", "cancelled"
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WorkflowJob represents a single job within a workflow execution
type WorkflowJob struct {
	ID                    int64                  `json:"id"`
	WorkflowExecutionID   int64                  `json:"workflow_execution_id"` // Links to workflow_executions table
	WorkflowID            int64                  `json:"workflow_id"`           // For quick reference
	NodeID                int64                  `json:"node_id"`               // Workflow node ID from design
	JobName               string                 `json:"job_name"`
	ServiceID             int64                  `json:"service_id"`
	ExecutorPeerID        string                 `json:"executor_peer_id"`
	RemoteJobExecutionID  *int64                 `json:"remote_job_execution_id,omitempty"` // ID from executor peer's job_executions table
	Status                string                 `json:"status"` // "pending", "running", "completed", "failed"
	Result                map[string]interface{} `json:"result,omitempty"`
	Error                 string                 `json:"error,omitempty"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

// InitWorkflowsTable creates the workflows table if it doesn't exist
func (sm *SQLiteManager) InitWorkflowsTable() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS workflows (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT,
		definition TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS workflow_executions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_id INTEGER NOT NULL,
		status TEXT CHECK(status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')) NOT NULL DEFAULT 'PENDING',
		error TEXT,
		started_at DATETIME,
		completed_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS workflow_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_execution_id INTEGER NOT NULL,
		workflow_id INTEGER NOT NULL,
		node_id INTEGER NOT NULL,
		job_name TEXT NOT NULL,
		service_id INTEGER NOT NULL,
		executor_peer_id TEXT NOT NULL,
		remote_job_execution_id INTEGER,
		status TEXT CHECK(status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED', 'ERRORED')) NOT NULL DEFAULT 'PENDING',
		result TEXT,
		error TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_execution_id) REFERENCES workflow_executions(id) ON DELETE CASCADE,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_workflows_name ON workflows(name);
	CREATE INDEX IF NOT EXISTS idx_workflow_executions_workflow_id ON workflow_executions(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_executions_status ON workflow_executions(status);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_workflow_execution_id ON workflow_jobs(workflow_execution_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_workflow_id ON workflow_jobs(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_status ON workflow_jobs(status);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_executor_peer_id ON workflow_jobs(executor_peer_id);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create workflows tables: %v", err), "database")
		return err
	}

	sm.logger.Info("Workflows tables initialized successfully", "database")
	return nil
}

// AddWorkflow creates a new workflow
func (sm *SQLiteManager) AddWorkflow(workflow *Workflow) error {
	definitionJSON, err := json.Marshal(workflow.Definition)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow definition: %v", err)
	}

	result, err := sm.db.Exec(
		"INSERT INTO workflows (name, description, definition) VALUES (?, ?, ?)",
		workflow.Name,
		workflow.Description,
		string(definitionJSON),
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to add workflow: %v", err), "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	workflow.ID = id
	workflow.CreatedAt = time.Now()
	workflow.UpdatedAt = time.Now()

	sm.logger.Info(fmt.Sprintf("Workflow added successfully: %s (ID: %d)", workflow.Name, id), "database")
	return nil
}

// GetAllWorkflows retrieves all workflows
func (sm *SQLiteManager) GetAllWorkflows() ([]*Workflow, error) {
	rows, err := sm.db.Query("SELECT id, name, description, definition, created_at, updated_at FROM workflows ORDER BY created_at DESC")
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflows: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var workflows []*Workflow
	for rows.Next() {
		var workflow Workflow
		var definitionJSON string

		err := rows.Scan(
			&workflow.ID,
			&workflow.Name,
			&workflow.Description,
			&definitionJSON,
			&workflow.CreatedAt,
			&workflow.UpdatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan workflow: %v", err), "database")
			continue
		}

		// Parse definition JSON
		if err := json.Unmarshal([]byte(definitionJSON), &workflow.Definition); err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to unmarshal workflow definition: %v", err), "database")
			workflow.Definition = make(map[string]interface{})
		}

		workflows = append(workflows, &workflow)
	}

	return workflows, nil
}

// GetWorkflowByID retrieves a specific workflow by ID
func (sm *SQLiteManager) GetWorkflowByID(id int64) (*Workflow, error) {
	var workflow Workflow
	var definitionJSON string

	err := sm.db.QueryRow(
		"SELECT id, name, description, definition, created_at, updated_at FROM workflows WHERE id = ?",
		id,
	).Scan(
		&workflow.ID,
		&workflow.Name,
		&workflow.Description,
		&definitionJSON,
		&workflow.CreatedAt,
		&workflow.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("workflow not found")
	}
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow: %v", err), "database")
		return nil, err
	}

	// Parse definition JSON
	if err := json.Unmarshal([]byte(definitionJSON), &workflow.Definition); err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to unmarshal workflow definition: %v", err), "database")
		workflow.Definition = make(map[string]interface{})
	}

	return &workflow, nil
}

// UpdateWorkflow updates an existing workflow
func (sm *SQLiteManager) UpdateWorkflow(id int64, workflow *Workflow) error {
	definitionJSON, err := json.Marshal(workflow.Definition)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow definition: %v", err)
	}

	result, err := sm.db.Exec(
		"UPDATE workflows SET name = ?, description = ?, definition = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		workflow.Name,
		workflow.Description,
		string(definitionJSON),
		id,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update workflow: %v", err), "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("workflow not found")
	}

	sm.logger.Info(fmt.Sprintf("Workflow updated successfully: ID %d", id), "database")
	return nil
}

// DeleteWorkflow deletes a workflow
func (sm *SQLiteManager) DeleteWorkflow(id int64) error {
	result, err := sm.db.Exec("DELETE FROM workflows WHERE id = ?", id)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to delete workflow: %v", err), "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("workflow not found")
	}

	sm.logger.Info(fmt.Sprintf("Workflow deleted successfully: ID %d", id), "database")
	return nil
}

// BuildWorkflowDefinition converts workflow nodes and connections into execution format
// This bridges the frontend workflow designer with the backend WorkflowManager
func (sm *SQLiteManager) BuildWorkflowDefinition(workflowID int64, localPeerID string) error {
	sm.logger.Info(fmt.Sprintf("Building workflow definition for workflow %d", workflowID), "database")

	// Get all workflow nodes
	nodes, err := sm.GetWorkflowNodes(workflowID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow nodes: %v", err), "database")
		return fmt.Errorf("failed to get workflow nodes: %v", err)
	}

	if len(nodes) == 0 {
		return fmt.Errorf("workflow has no nodes")
	}

	// Get all connections
	connections, err := sm.GetWorkflowConnections(workflowID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow connections: %v", err), "database")
		return fmt.Errorf("failed to get workflow connections: %v", err)
	}

	sm.logger.Info(fmt.Sprintf("Found %d nodes and %d connections for workflow %d", len(nodes), len(connections), workflowID), "database")

	// Build jobs array
	type InterfacePeerDef struct {
		PeerID        string `json:"peer_id"`
		PeerWorkflowNodeID *int64 `json:"peer_workflow_node_id,omitempty"` // workflow_nodes.id (planning time reference)
		PeerPath          string `json:"peer_path"`
		PeerMountFunction string `json:"peer_mount_function"` // INPUT, OUTPUT, or BOTH
		DutyAcknowledged  bool   `json:"duty_acknowledged,omitempty"`
	}

	type InterfaceDef struct {
		Type           string             `json:"type"` // STDIN, STDOUT, MOUNT
		Path           string             `json:"path"`
		InterfacePeers []InterfacePeerDef `json:"interface_peers"`
	}

	type JobDef struct {
		NodeID              int64          `json:"node_id"` // Workflow node ID for mapping after job execution creation
		Name                string         `json:"name"`
		ServiceID           int64          `json:"service_id"`
		ServiceType         string         `json:"service_type"`
		ExecutorPeerID      string         `json:"executor_peer_id"`
		Entrypoint          []string       `json:"entrypoint,omitempty"`
		Commands            []string       `json:"commands,omitempty"`
		ExecutionConstraint string         `json:"execution_constraint"`
		Interfaces          []InterfaceDef `json:"interfaces"`
	}

	jobs := make([]JobDef, 0, len(nodes))

	// Create a map of node ID to node for quick lookup
	nodeMap := make(map[int64]*WorkflowNode)
	for _, node := range nodes {
		nodeMap[node.ID] = node
	}

	// Helper function to find interface path from node's interfaces
	findInterfacePath := func(node *WorkflowNode, interfaceType string) string {
		if node.Interfaces == nil {
			return ""
		}
		for _, iface := range node.Interfaces {
			if ifaceMap, ok := iface.(map[string]interface{}); ok {
				if ifType, ok := ifaceMap["interface_type"].(string); ok && ifType == interfaceType {
					if path, ok := ifaceMap["path"].(string); ok {
						return path
					}
				}
			}
		}
		return ""
	}

	// Helper function to find mount function from node's interfaces
	findMountFunction := func(node *WorkflowNode, interfaceType string) string {
		if node.Interfaces == nil {
			return ""
		}
		for _, iface := range node.Interfaces {
			if ifaceMap, ok := iface.(map[string]interface{}); ok {
				if ifType, ok := ifaceMap["interface_type"].(string); ok && ifType == interfaceType {
					if mountFunc, ok := ifaceMap["mount_function"].(string); ok {
						return mountFunc
					}
				}
			}
		}
		return ""
	}

	// determineMountFunction determines the mount function for an interface
	// based on its type and node definition
	determineMountFunction := func(node *WorkflowNode, interfaceType string) string {
		if interfaceType == "MOUNT" {
			// MOUNT: Read from node's interface definition
			mountFunc := findMountFunction(node, interfaceType)
			if mountFunc == "" {
				return "BOTH" // Default for MOUNT
			}
			return mountFunc
		}

		// For all other interfaces, direction is obvious from type
		switch interfaceType {
		case "STDIN":
			return "INPUT"  // Always receives data
		case "STDOUT", "STDERR", "LOGS":
			return "OUTPUT" // Always sends data
		default:
			return "INPUT"  // Safe default
		}
	}

	// Process each node
	for _, node := range nodes {
		sm.logger.Debug(fmt.Sprintf("Processing node %d (%s)", node.ID, node.ServiceName), "database")

		job := JobDef{
			NodeID:         node.ID, // Store node ID for mapping after job execution creation
			Name:           node.ServiceName,
			ServiceID:      node.ServiceID,
			ServiceType:    node.ServiceType,
			ExecutorPeerID: node.PeerID,
			// ExecutionConstraint will be set after checking for incoming connections
			Interfaces: []InterfaceDef{},
		}

		// Get entrypoint and commands from WorkflowNode (populated from remote service search)
		// NOTE: Do NOT read from local docker_service_details - service IDs are local to each peer.
		// The entrypoint/cmd should come from the remote peer's service via search results.
		if node.ServiceType == "DOCKER" {
			if len(node.Entrypoint) > 0 {
				job.Entrypoint = node.Entrypoint
				sm.logger.Debug(fmt.Sprintf("  Setting entrypoint from node: %v", node.Entrypoint), "database")
			}
			if len(node.Cmd) > 0 {
				job.Commands = node.Cmd
				sm.logger.Debug(fmt.Sprintf("  Setting commands from node: %v", node.Cmd), "database")
			}
		}

		// Map to track interfaces by type+path to avoid duplicates
		interfaceMap := make(map[string]*InterfaceDef)

		// Track if this job has any incoming connections
		hasIncomingConnections := false

		// Process outgoing connections (this node is the source)
		for _, conn := range connections {
			if conn.FromNodeID != nil && *conn.FromNodeID == node.ID {
				sm.logger.Debug(fmt.Sprintf("  Outgoing connection: %s -> %s", conn.FromInterfaceType, conn.ToInterfaceType), "database")

				// This node outputs data via FromInterfaceType
				interfaceType := conn.FromInterfaceType

				// Get the actual interface path from this node's interfaces
				fromPath := findInterfacePath(node, interfaceType)
				if fromPath == "" {
					fromPath = "output" + string(os.PathSeparator) // Default fallback
				}

				interfaceKey := interfaceType + ":" + fromPath
				if _, exists := interfaceMap[interfaceKey]; !exists {
					interfaceMap[interfaceKey] = &InterfaceDef{
						Type:           interfaceType,
						Path:           fromPath,
						InterfacePeers: []InterfacePeerDef{},
					}
				}

				// Determine destination peer, path, and mount function
				var destPeerID string
				var destJobID *int64
				var destPath string
				var destMountFunc string

				if conn.ToNodeID == nil {
					// Destination is Local Peer
					destPeerID = localPeerID
					destJobID = nil
					destPath = "input" + string(os.PathSeparator)
					destMountFunc = "INPUT" // Local Peer STDIN is always INPUT
				} else {
					// Destination is another workflow node
					destNode := nodeMap[*conn.ToNodeID]
					if destNode != nil {
						destPeerID = destNode.PeerID
						destJobID = conn.ToNodeID
						destPath = findInterfacePath(destNode, conn.ToInterfaceType)
						if destPath == "" {
							destPath = "input" + string(os.PathSeparator)
						}

						// Use helper to determine mount function
						destMountFunc = determineMountFunction(destNode, conn.ToInterfaceType)
					} else {
						sm.logger.Warn(fmt.Sprintf("  Destination node %d not found, skipping connection", *conn.ToNodeID), "database")
						continue
					}
				}

				// Determine source for mount function
				mountFuncSource := "interface type"
				if conn.ToInterfaceType == "MOUNT" {
					mountFuncSource = "interface definition"
				}
				sm.logger.Debug(fmt.Sprintf("    Dest interface: %s, mount func: %s (source: %s)", conn.ToInterfaceType, destMountFunc, mountFuncSource), "database")

				// Add destination as RECEIVER peer - PeerPath is where data should be SENT TO
				// Use the mount function from the destination's interface definition
				interfaceMap[interfaceKey].InterfacePeers = append(interfaceMap[interfaceKey].InterfacePeers, InterfacePeerDef{
					PeerID:         destPeerID,
					PeerWorkflowNodeID: destJobID,
					PeerPath:           destPath, // Actual destination path (e.g., /app for MOUNT)
					PeerMountFunction:  destMountFunc,  // From destination's interface definition
					DutyAcknowledged:   false,
				})
			}
		}

		// Process incoming connections (this node is the destination)
		for _, conn := range connections {
			if conn.ToNodeID != nil && *conn.ToNodeID == node.ID {
				sm.logger.Debug(fmt.Sprintf("  Incoming connection: %s -> %s", conn.FromInterfaceType, conn.ToInterfaceType), "database")

				// Mark that this job has incoming connections
				hasIncomingConnections = true

				// This node receives data via ToInterfaceType
				interfaceType := conn.ToInterfaceType

				// Get the actual interface path from this node's interfaces
				toPath := findInterfacePath(node, interfaceType)
				if toPath == "" {
					toPath = "input" + string(os.PathSeparator)
				}

				// Determine our mount function based on OUR interface type and definition
				ourMountFunc := determineMountFunction(node, interfaceType)

				interfaceKey := interfaceType + ":" + toPath
				if _, exists := interfaceMap[interfaceKey]; !exists {
					interfaceMap[interfaceKey] = &InterfaceDef{
						Type:           interfaceType,
						Path:           toPath, // Actual interface path (e.g., /app for MOUNT)
						InterfacePeers: []InterfacePeerDef{},
					}
				}

				// Determine source peer and path
				var srcPeerID string
				var srcJobID *int64
				var srcPath string

				if conn.FromNodeID == nil {
					// Source is local peer (self-peer)
					srcPeerID = localPeerID
					srcJobID = nil
					srcPath = "output" + string(os.PathSeparator) // Default for local peer
				} else {
					// Source is another workflow node
					srcNode := nodeMap[*conn.FromNodeID]
					if srcNode != nil {
						srcPeerID = srcNode.PeerID
						srcJobID = conn.FromNodeID
						// Get the actual source interface path
						srcPath = findInterfacePath(srcNode, conn.FromInterfaceType)
						if srcPath == "" {
							srcPath = "output" + string(os.PathSeparator) // Default fallback
						}
					} else {
						sm.logger.Warn(fmt.Sprintf("  Source node %d not found, skipping connection", *conn.FromNodeID), "database")
						continue
					}
				}

				// Determine source for mount function
				mountFuncSource := "interface type"
				if interfaceType == "MOUNT" {
					mountFuncSource = "interface definition"
				}
				sm.logger.Debug(fmt.Sprintf("    Our interface: %s, mount func: %s (source: %s)", interfaceType, ourMountFunc, mountFuncSource), "database")

				// Add source as PROVIDER peer
				// PeerMountFunction describes how WE (destination) receive data via OUR interface
				interfaceMap[interfaceKey].InterfacePeers = append(interfaceMap[interfaceKey].InterfacePeers, InterfacePeerDef{
					PeerID:         srcPeerID,
					PeerWorkflowNodeID: srcJobID,
					PeerPath:           srcPath, // Source's output path
					PeerMountFunction:  ourMountFunc, // Use our interface's mount function
					DutyAcknowledged:   false,
				})
			}
		}

		// DATA services MUST have a STDOUT interface (for output)
		// Create it automatically if it doesn't exist (even without connections)
		if node.ServiceType == "DATA" {
			stdoutKey := "STDOUT:output" + string(os.PathSeparator)
			if _, exists := interfaceMap[stdoutKey]; !exists {
				sm.logger.Debug(fmt.Sprintf("  Auto-creating STDOUT interface for DATA service '%s' (no connections yet)", node.ServiceName), "database")
				interfaceMap[stdoutKey] = &InterfaceDef{
					Type:           "STDOUT",
					Path:           "output" + string(os.PathSeparator),
					InterfacePeers: []InterfacePeerDef{}, // Empty initially - user must connect it
				}
			}
		}

		// Convert interface map to array
		for _, iface := range interfaceMap {
			job.Interfaces = append(job.Interfaces, *iface)
		}

		// Set execution constraint based on whether job has incoming connections
		if hasIncomingConnections {
			job.ExecutionConstraint = "INPUTS_READY"
		} else {
			job.ExecutionConstraint = "NONE"
		}

		sm.logger.Debug(fmt.Sprintf("  Job %s has %d interfaces, constraint: %s", job.Name, len(job.Interfaces), job.ExecutionConstraint), "database")

		jobs = append(jobs, job)
	}

	// Create workflow definition
	definition := map[string]interface{}{
		"jobs": jobs,
	}

	// Update workflow with new definition
	workflow, err := sm.GetWorkflowByID(workflowID)
	if err != nil {
		return fmt.Errorf("failed to get workflow: %v", err)
	}

	workflow.Definition = definition

	err = sm.UpdateWorkflow(workflowID, workflow)
	if err != nil {
		return fmt.Errorf("failed to update workflow definition: %v", err)
	}

	sm.logger.Info(fmt.Sprintf("Workflow definition built successfully: %d jobs", len(jobs)), "database")
	return nil
}

// CreateWorkflowExecution creates a new workflow execution instance
func (sm *SQLiteManager) CreateWorkflowExecution(workflowID int64) (*WorkflowExecution, error) {
	now := time.Now()
	result, err := sm.db.Exec(
		"INSERT INTO workflow_executions (workflow_id, status, started_at) VALUES (?, ?, ?)",
		workflowID,
		"PENDING",
		now,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create workflow execution: %v", err), "database")
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	execution := &WorkflowExecution{
		ID:         id,
		WorkflowID: workflowID,
		Status:     "PENDING",
		StartedAt:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	sm.logger.Info(fmt.Sprintf("Workflow execution created: ID %d for workflow %d", id, workflowID), "database")
	return execution, nil
}

// CreateWorkflowJob creates a new workflow job within an execution
func (sm *SQLiteManager) CreateWorkflowJob(executionID, workflowID, nodeID int64, jobName string, serviceID int64, executorPeerID string) (*WorkflowJob, error) {
	result, err := sm.db.Exec(
		"INSERT INTO workflow_jobs (workflow_execution_id, workflow_id, node_id, job_name, service_id, executor_peer_id, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		executionID,
		workflowID,
		nodeID,
		jobName,
		serviceID,
		executorPeerID,
		"PENDING",
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create workflow job: %v", err), "database")
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	job := &WorkflowJob{
		ID:                  id,
		WorkflowExecutionID: executionID,
		WorkflowID:          workflowID,
		NodeID:              nodeID,
		JobName:             jobName,
		ServiceID:           serviceID,
		ExecutorPeerID:      executorPeerID,
		Status:              "PENDING",
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	sm.logger.Info(fmt.Sprintf("Workflow job created: ID %d (execution:%d, node:%d, name:%s)", id, executionID, nodeID, jobName), "database")
	return job, nil
}

// UpdateWorkflowJobStatus updates the status of a workflow job
func (sm *SQLiteManager) UpdateWorkflowJobStatus(jobID int64, status string, result map[string]interface{}, errorMsg string) error {
	var resultJSON []byte
	var err error

	if result != nil {
		resultJSON, err = json.Marshal(result)
		if err != nil {
			return fmt.Errorf("failed to marshal job result: %v", err)
		}
	}

	_, err = sm.db.Exec(
		"UPDATE workflow_jobs SET status = ?, result = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		status,
		string(resultJSON),
		errorMsg,
		jobID,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update workflow job status: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Workflow job status updated: ID %d, status: %s", jobID, status), "database")
	return nil
}

// GetWorkflowJobsByExecution retrieves all jobs for a specific workflow execution
func (sm *SQLiteManager) GetWorkflowJobsByExecution(executionID int64) ([]*WorkflowJob, error) {
	rows, err := sm.db.Query(
		"SELECT id, workflow_execution_id, workflow_id, node_id, job_name, service_id, executor_peer_id, remote_job_execution_id, status, result, error, created_at, updated_at FROM workflow_jobs WHERE workflow_execution_id = ? ORDER BY created_at ASC",
		executionID,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow jobs: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var jobs []*WorkflowJob
	for rows.Next() {
		var job WorkflowJob
		var remoteJobExecID sql.NullInt64
		var resultJSON sql.NullString
		var errorMsg sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.WorkflowExecutionID,
			&job.WorkflowID,
			&job.NodeID,
			&job.JobName,
			&job.ServiceID,
			&job.ExecutorPeerID,
			&remoteJobExecID,
			&job.Status,
			&resultJSON,
			&errorMsg,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan workflow job: %v", err), "database")
			continue
		}

		// Set remote job execution ID if valid
		if remoteJobExecID.Valid {
			job.RemoteJobExecutionID = &remoteJobExecID.Int64
		}

		// Parse result JSON
		if resultJSON.Valid && resultJSON.String != "" {
			if err := json.Unmarshal([]byte(resultJSON.String), &job.Result); err != nil {
				sm.logger.Error(fmt.Sprintf("Failed to unmarshal job result: %v", err), "database")
			}
		}

		if errorMsg.Valid {
			job.Error = errorMsg.String
		}

		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// GetWorkflowJobByID retrieves a workflow job by its ID
func (sm *SQLiteManager) GetWorkflowJobByID(id int64) (*WorkflowJob, error) {
	var job WorkflowJob
	var remoteJobExecID sql.NullInt64
	var resultStr, errorStr sql.NullString

	err := sm.db.QueryRow(`
		SELECT id, workflow_execution_id, workflow_id, node_id, job_name, service_id, executor_peer_id,
		       remote_job_execution_id, status, result, error, created_at, updated_at
		FROM workflow_jobs
		WHERE id = ?
	`, id).Scan(
		&job.ID, &job.WorkflowExecutionID, &job.WorkflowID, &job.NodeID, &job.JobName,
		&job.ServiceID, &job.ExecutorPeerID, &remoteJobExecID, &job.Status,
		&resultStr, &errorStr, &job.CreatedAt, &job.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	// Set remote job execution ID if valid
	if remoteJobExecID.Valid {
		job.RemoteJobExecutionID = &remoteJobExecID.Int64
	}

	// Parse JSON result if present
	if resultStr.Valid && resultStr.String != "" {
		if err := json.Unmarshal([]byte(resultStr.String), &job.Result); err != nil {
			return nil, fmt.Errorf("failed to parse result JSON: %v", err)
		}
	}

	if errorStr.Valid {
		job.Error = errorStr.String
	}

	return &job, nil
}

// GetWorkflowJobs is a legacy compatibility method
// TODO: This should be replaced with GetWorkflowExecutions + GetWorkflowJobsByExecution
// For now, returns empty to prevent compilation errors
func (sm *SQLiteManager) GetWorkflowJobs(workflowID int64) ([]*WorkflowJob, error) {
	sm.logger.Warn("GetWorkflowJobs called - this is a legacy method that needs refactoring", "database")
	return []*WorkflowJob{}, nil
}

// UpdateWorkflowExecutionStatus updates the status of a workflow execution
func (sm *SQLiteManager) UpdateWorkflowExecutionStatus(executionID int64, status string, errorMsg string) error {
	// If status is completed or failed, set completed_at timestamp
	var err error
	if status == "COMPLETED" || status == "FAILED" || status == "CANCELLED" {
		_, err = sm.db.Exec(
			"UPDATE workflow_executions SET status = ?, error = ?, completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
			status,
			errorMsg,
			executionID,
		)
	} else {
		_, err = sm.db.Exec(
			"UPDATE workflow_executions SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
			status,
			errorMsg,
			executionID,
		)
	}

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update workflow execution status: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Workflow execution status updated: ID %d, status: %s", executionID, status), "database")
	return nil
}

// UpdateWorkflowJobRemoteExecutionID updates the remote job execution ID for a workflow job
// This is called when the orchestrator receives the job_execution_id from the remote executor peer
func (sm *SQLiteManager) UpdateWorkflowJobRemoteExecutionID(workflowJobID int64, remoteJobExecutionID int64) error {
	_, err := sm.db.Exec(
		"UPDATE workflow_jobs SET remote_job_execution_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		remoteJobExecutionID,
		workflowJobID,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update workflow job remote execution ID: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Workflow job remote execution ID updated: workflow_job_id=%d, remote_job_execution_id=%d", workflowJobID, remoteJobExecutionID), "database")
	return nil
}

// GetWorkflowExecutionsByWorkflowID retrieves all execution instances for a specific workflow
func (sm *SQLiteManager) GetWorkflowExecutionsByWorkflowID(workflowID int64) ([]*WorkflowExecution, error) {
	rows, err := sm.db.Query(`
		SELECT id, workflow_id, status, error, started_at, completed_at, created_at, updated_at
		FROM workflow_executions
		WHERE workflow_id = ?
		ORDER BY created_at DESC
	`, workflowID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow executions for workflow %d: %v", workflowID, err), "database")
		return nil, err
	}
	defer rows.Close()

	var executions []*WorkflowExecution
	for rows.Next() {
		var execution WorkflowExecution
		var errorMsg sql.NullString
		var completedAt sql.NullTime

		err := rows.Scan(
			&execution.ID,
			&execution.WorkflowID,
			&execution.Status,
			&errorMsg,
			&execution.StartedAt,
			&completedAt,
			&execution.CreatedAt,
			&execution.UpdatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan workflow execution: %v", err), "database")
			continue
		}

		if errorMsg.Valid {
			execution.Error = errorMsg.String
		}
		if completedAt.Valid {
			execution.CompletedAt = completedAt.Time
		}

		executions = append(executions, &execution)
	}

	return executions, nil
}

// GetWorkflowExecutionByID retrieves a specific workflow execution by ID
func (sm *SQLiteManager) GetWorkflowExecutionByID(executionID int64) (*WorkflowExecution, error) {
	var execution WorkflowExecution
	var errorMsg sql.NullString
	var completedAt sql.NullTime

	err := sm.db.QueryRow(`
		SELECT id, workflow_id, status, error, started_at, completed_at, created_at, updated_at
		FROM workflow_executions
		WHERE id = ?
	`, executionID).Scan(
		&execution.ID,
		&execution.WorkflowID,
		&execution.Status,
		&errorMsg,
		&execution.StartedAt,
		&completedAt,
		&execution.CreatedAt,
		&execution.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow execution not found")
		}
		sm.logger.Error(fmt.Sprintf("Failed to get workflow execution %d: %v", executionID, err), "database")
		return nil, err
	}

	if errorMsg.Valid {
		execution.Error = errorMsg.String
	}
	if completedAt.Valid {
		execution.CompletedAt = completedAt.Time
	}

	return &execution, nil
}
