package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
