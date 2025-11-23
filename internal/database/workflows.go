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

// WorkflowJob represents an execution instance of a workflow
type WorkflowJob struct {
	ID         int64                  `json:"id"`
	WorkflowID int64                  `json:"workflow_id"`
	Status     string                 `json:"status"` // "pending", "running", "completed", "failed"
	Result     map[string]interface{} `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
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

	CREATE TABLE IF NOT EXISTS workflow_jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_id INTEGER NOT NULL,
		status TEXT NOT NULL,
		result TEXT,
		error TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_workflows_name ON workflows(name);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_workflow_id ON workflow_jobs(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_jobs_status ON workflow_jobs(status);
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
		PeerNodeID        string `json:"peer_node_id"`
		PeerJobID         *int64 `json:"peer_job_id,omitempty"`
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

		// Get entrypoint and commands from docker_service_details if this is a DOCKER service
		if node.ServiceType == "DOCKER" {
			dockerDetails, err := sm.GetDockerServiceDetails(node.ServiceID)
			if err == nil && dockerDetails != nil {
				// Parse entrypoint from JSON
				if dockerDetails.Entrypoint != "" {
					var entrypoint []string
					if err := json.Unmarshal([]byte(dockerDetails.Entrypoint), &entrypoint); err == nil {
						job.Entrypoint = entrypoint
						sm.logger.Debug(fmt.Sprintf("  Setting entrypoint: %v", entrypoint), "database")
					}
				}
				// Parse cmd from JSON
				if dockerDetails.Cmd != "" {
					var commands []string
					if err := json.Unmarshal([]byte(dockerDetails.Cmd), &commands); err == nil {
						job.Commands = commands
						sm.logger.Debug(fmt.Sprintf("  Setting commands: %v", commands), "database")
					}
				}
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

				// Determine destination peer and path
				var destPeerID string
				var destJobID *int64
				var destPath string

				if conn.ToNodeID == nil {
					// Destination is local peer (self-peer)
					destPeerID = localPeerID
					destJobID = nil
					destPath = "input" + string(os.PathSeparator) // Default for local peer
				} else {
					// Destination is another workflow node
					destNode := nodeMap[*conn.ToNodeID]
					if destNode != nil {
						destPeerID = destNode.PeerID
						destJobID = conn.ToNodeID
						// Get the actual destination interface path
						destPath = findInterfacePath(destNode, conn.ToInterfaceType)
						if destPath == "" {
							destPath = "input" + string(os.PathSeparator) // Default fallback
						}
					} else {
						sm.logger.Warn(fmt.Sprintf("  Destination node %d not found, skipping connection", *conn.ToNodeID), "database")
						continue
					}
				}

				sm.logger.Debug(fmt.Sprintf("    Source path: %s, Dest path: %s", fromPath, destPath), "database")

				// Add destination as RECEIVER peer - PeerPath is where data should be SENT TO
				interfaceMap[interfaceKey].InterfacePeers = append(interfaceMap[interfaceKey].InterfacePeers, InterfacePeerDef{
					PeerNodeID:        destPeerID,
					PeerJobID:         destJobID,
					PeerPath:          destPath, // Actual destination path (e.g., /app for MOUNT)
					PeerMountFunction: "OUTPUT",
					DutyAcknowledged:  false,
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
					toPath = "input" + string(os.PathSeparator) // Default fallback
				}

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

				sm.logger.Debug(fmt.Sprintf("    Source path: %s, Dest path: %s", srcPath, toPath), "database")

				// Add source as PROVIDER peer - PeerPath is where source outputs FROM
				interfaceMap[interfaceKey].InterfacePeers = append(interfaceMap[interfaceKey].InterfacePeers, InterfacePeerDef{
					PeerNodeID:        srcPeerID,
					PeerJobID:         srcJobID,
					PeerPath:          srcPath, // Source's output path
					PeerMountFunction: "INPUT",
					DutyAcknowledged:  false,
				})
			}
		}

		// DATA services MUST have a STDOUT interface (for output)
		// Create it automatically if it doesn't exist (even without connections)
		if node.ServiceType == "DATA" {
			if _, exists := interfaceMap["STDOUT"]; !exists {
				sm.logger.Debug(fmt.Sprintf("  Auto-creating STDOUT interface for DATA service '%s' (no connections yet)", node.ServiceName), "database")
				interfaceMap["STDOUT"] = &InterfaceDef{
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

// CreateWorkflowJob creates a new workflow job (execution instance)
func (sm *SQLiteManager) CreateWorkflowJob(workflowID int64) (*WorkflowJob, error) {
	result, err := sm.db.Exec(
		"INSERT INTO workflow_jobs (workflow_id, status) VALUES (?, ?)",
		workflowID,
		"pending",
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
		ID:         id,
		WorkflowID: workflowID,
		Status:     "pending",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	sm.logger.Info(fmt.Sprintf("Workflow job created: ID %d for workflow %d", id, workflowID), "database")
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

// GetWorkflowJobs retrieves all jobs for a specific workflow
func (sm *SQLiteManager) GetWorkflowJobs(workflowID int64) ([]*WorkflowJob, error) {
	rows, err := sm.db.Query(
		"SELECT id, workflow_id, status, result, error, created_at, updated_at FROM workflow_jobs WHERE workflow_id = ? ORDER BY created_at DESC",
		workflowID,
	)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get workflow jobs: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var jobs []*WorkflowJob
	for rows.Next() {
		var job WorkflowJob
		var resultJSON sql.NullString
		var errorMsg sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.WorkflowID,
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
	var resultStr, errorStr sql.NullString

	err := sm.db.QueryRow(`
		SELECT id, workflow_id, status, result, error, created_at, updated_at
		FROM workflow_jobs
		WHERE id = ?
	`, id).Scan(
		&job.ID, &job.WorkflowID, &job.Status,
		&resultStr, &errorStr, &job.CreatedAt, &job.UpdatedAt,
	)

	if err != nil {
		return nil, err
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
