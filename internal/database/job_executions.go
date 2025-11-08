package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// JobExecution represents a job execution instance
type JobExecution struct {
	ID                  int64     `json:"id"`
	WorkflowJobID       int64     `json:"workflow_job_id"`
	ServiceID           int64     `json:"service_id"`
	ExecutorPeerID      string    `json:"executor_peer_id"`      // Ed25519 peer ID who executes
	OrderingPeerID      string    `json:"ordering_peer_id"`      // Ed25519 peer ID who requested
	Status              string    `json:"status"`                // IDLE, READY, RUNNING, COMPLETED, ERRORED, CANCELLED
	Entrypoint          string    `json:"entrypoint,omitempty"`  // JSON array of strings
	Commands            string    `json:"commands,omitempty"`    // JSON array of strings
	ExecutionConstraint string    `json:"execution_constraint"`  // NONE, INPUTS_READY
	ConstraintDetail    string    `json:"constraint_detail,omitempty"`
	StartedAt           time.Time `json:"started_at,omitempty"`
	EndedAt             time.Time `json:"ended_at,omitempty"`
	ErrorMessage        string    `json:"error_message,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// JobInterface represents an interface (STDIN, STDOUT, MOUNT) for a job
type JobInterface struct {
	ID             int64     `json:"id"`
	JobExecutionID int64     `json:"job_execution_id"`
	InterfaceType  string    `json:"interface_type"` // STDIN, STDOUT, MOUNT
	Path           string    `json:"path"`
	CreatedAt      time.Time `json:"created_at"`
}

// JobInterfacePeer represents a peer connection for a job interface
type JobInterfacePeer struct {
	ID                 int64     `json:"id"`
	JobInterfaceID     int64     `json:"job_interface_id"`
	PeerNodeID         string    `json:"peer_node_id"`    // Ed25519 peer ID
	PeerJobID          *int64    `json:"peer_job_id"`     // Connected job ID (nullable)
	PeerPath           string    `json:"peer_path"`
	PeerMountFunction  string    `json:"peer_mount_function"` // PROVIDER, RECEIVER
	DutyAcknowledged   bool      `json:"duty_acknowledged"`
	CreatedAt          time.Time `json:"created_at"`
}

// InitJobExecutionsTable creates the job execution tables
func (sm *SQLiteManager) InitJobExecutionsTable() error {
	createTableSQL := `
	-- Job executions table
	CREATE TABLE IF NOT EXISTS job_executions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_job_id INTEGER NOT NULL,
		service_id INTEGER NOT NULL,
		executor_peer_id TEXT NOT NULL,
		ordering_peer_id TEXT NOT NULL,
		status TEXT CHECK(status IN ('IDLE', 'READY', 'RUNNING', 'COMPLETED', 'ERRORED', 'CANCELLED')) NOT NULL DEFAULT 'IDLE',
		entrypoint TEXT,
		commands TEXT,
		execution_constraint TEXT CHECK(execution_constraint IN ('NONE', 'INPUTS_READY')) DEFAULT 'NONE',
		constraint_detail TEXT,
		started_at DATETIME,
		ended_at DATETIME,
		error_message TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_job_id) REFERENCES workflow_jobs(id) ON DELETE CASCADE,
		FOREIGN KEY (service_id) REFERENCES services(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_executions_workflow_job_id ON job_executions(workflow_job_id);
	CREATE INDEX IF NOT EXISTS idx_job_executions_status ON job_executions(status);
	CREATE INDEX IF NOT EXISTS idx_job_executions_executor_peer_id ON job_executions(executor_peer_id);
	CREATE INDEX IF NOT EXISTS idx_job_executions_ordering_peer_id ON job_executions(ordering_peer_id);

	-- Job interfaces table
	CREATE TABLE IF NOT EXISTS job_interfaces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_execution_id INTEGER NOT NULL,
		interface_type TEXT CHECK(interface_type IN ('STDIN', 'STDOUT', 'MOUNT')) NOT NULL,
		path TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (job_execution_id) REFERENCES job_executions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_interfaces_job_execution_id ON job_interfaces(job_execution_id);
	CREATE INDEX IF NOT EXISTS idx_job_interfaces_interface_type ON job_interfaces(interface_type);

	-- Job interface peers table
	CREATE TABLE IF NOT EXISTS job_interface_peers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_interface_id INTEGER NOT NULL,
		peer_node_id TEXT NOT NULL,
		peer_job_id INTEGER,
		peer_path TEXT NOT NULL,
		peer_mount_function TEXT CHECK(peer_mount_function IN ('PROVIDER', 'RECEIVER')) NOT NULL,
		duty_acknowledged BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (job_interface_id) REFERENCES job_interfaces(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_job_interface_id ON job_interface_peers(job_interface_id);
	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_peer_node_id ON job_interface_peers(peer_node_id);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create job executions tables: %v", err), "database")
		return err
	}

	sm.logger.Info("Job executions tables initialized successfully", "database")
	return nil
}

// CreateJobExecution creates a new job execution
func (sm *SQLiteManager) CreateJobExecution(job *JobExecution) error {
	result, err := sm.db.Exec(`
		INSERT INTO job_executions (
			workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
			status, entrypoint, commands, execution_constraint, constraint_detail
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.WorkflowJobID,
		job.ServiceID,
		job.ExecutorPeerID,
		job.OrderingPeerID,
		job.Status,
		job.Entrypoint,
		job.Commands,
		job.ExecutionConstraint,
		job.ConstraintDetail,
	)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create job execution: %v", err), "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	job.ID = id
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()

	sm.logger.Info(fmt.Sprintf("Job execution created: ID %d for workflow job %d", id, job.WorkflowJobID), "database")
	return nil
}

// GetJobExecution retrieves a job execution by ID
func (sm *SQLiteManager) GetJobExecution(id int64) (*JobExecution, error) {
	var job JobExecution
	var startedAt, endedAt sql.NullTime
	var errorMsg sql.NullString

	err := sm.db.QueryRow(`
		SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       status, entrypoint, commands, execution_constraint, constraint_detail,
		       started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE id = ?
	`, id).Scan(
		&job.ID,
		&job.WorkflowJobID,
		&job.ServiceID,
		&job.ExecutorPeerID,
		&job.OrderingPeerID,
		&job.Status,
		&job.Entrypoint,
		&job.Commands,
		&job.ExecutionConstraint,
		&job.ConstraintDetail,
		&startedAt,
		&endedAt,
		&errorMsg,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job execution not found")
	}
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job execution: %v", err), "database")
		return nil, err
	}

	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if endedAt.Valid {
		job.EndedAt = endedAt.Time
	}
	if errorMsg.Valid {
		job.ErrorMessage = errorMsg.String
	}

	return &job, nil
}

// UpdateJobStatus updates the status of a job execution
func (sm *SQLiteManager) UpdateJobStatus(id int64, status string, errorMsg string) error {
	var err error

	if status == "RUNNING" {
		_, err = sm.db.Exec(`
			UPDATE job_executions
			SET status = ?, started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, status, id)
	} else if status == "COMPLETED" || status == "ERRORED" || status == "CANCELLED" {
		_, err = sm.db.Exec(`
			UPDATE job_executions
			SET status = ?, ended_at = CURRENT_TIMESTAMP, error_message = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, status, errorMsg, id)
	} else {
		_, err = sm.db.Exec(`
			UPDATE job_executions
			SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, status, errorMsg, id)
	}

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update job status: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Job execution status updated: ID %d, status: %s", id, status), "database")
	return nil
}

// GetReadyJobs retrieves all jobs with READY status
func (sm *SQLiteManager) GetReadyJobs() ([]*JobExecution, error) {
	rows, err := sm.db.Query(`
		SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       status, entrypoint, commands, execution_constraint, constraint_detail,
		       started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE status = 'READY'
		ORDER BY created_at ASC
	`)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get ready jobs: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var jobs []*JobExecution
	for rows.Next() {
		var job JobExecution
		var startedAt, endedAt sql.NullTime
		var errorMsg sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.WorkflowJobID,
			&job.ServiceID,
			&job.ExecutorPeerID,
			&job.OrderingPeerID,
			&job.Status,
			&job.Entrypoint,
			&job.Commands,
			&job.ExecutionConstraint,
			&job.ConstraintDetail,
			&startedAt,
			&endedAt,
			&errorMsg,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan job execution: %v", err), "database")
			continue
		}

		if startedAt.Valid {
			job.StartedAt = startedAt.Time
		}
		if endedAt.Valid {
			job.EndedAt = endedAt.Time
		}
		if errorMsg.Valid {
			job.ErrorMessage = errorMsg.String
		}

		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// CreateJobInterface creates a new job interface
func (sm *SQLiteManager) CreateJobInterface(iface *JobInterface) error {
	result, err := sm.db.Exec(`
		INSERT INTO job_interfaces (job_execution_id, interface_type, path)
		VALUES (?, ?, ?)
	`, iface.JobExecutionID, iface.InterfaceType, iface.Path)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create job interface: %v", err), "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	iface.ID = id
	iface.CreatedAt = time.Now()

	return nil
}

// CreateJobInterfacePeer creates a new job interface peer connection
func (sm *SQLiteManager) CreateJobInterfacePeer(peer *JobInterfacePeer) error {
	result, err := sm.db.Exec(`
		INSERT INTO job_interface_peers (
			job_interface_id, peer_node_id, peer_job_id, peer_path,
			peer_mount_function, duty_acknowledged
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		peer.JobInterfaceID,
		peer.PeerNodeID,
		peer.PeerJobID,
		peer.PeerPath,
		peer.PeerMountFunction,
		peer.DutyAcknowledged,
	)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create job interface peer: %v", err), "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	peer.ID = id
	peer.CreatedAt = time.Now()

	return nil
}

// GetJobInterfaces retrieves all interfaces for a job execution
func (sm *SQLiteManager) GetJobInterfaces(jobExecutionID int64) ([]*JobInterface, error) {
	rows, err := sm.db.Query(`
		SELECT id, job_execution_id, interface_type, path, created_at
		FROM job_interfaces
		WHERE job_execution_id = ?
	`, jobExecutionID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job interfaces: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var interfaces []*JobInterface
	for rows.Next() {
		var iface JobInterface
		err := rows.Scan(
			&iface.ID,
			&iface.JobExecutionID,
			&iface.InterfaceType,
			&iface.Path,
			&iface.CreatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan job interface: %v", err), "database")
			continue
		}
		interfaces = append(interfaces, &iface)
	}

	return interfaces, nil
}

// GetJobInterfacePeers retrieves all peer connections for a job interface
func (sm *SQLiteManager) GetJobInterfacePeers(jobInterfaceID int64) ([]*JobInterfacePeer, error) {
	rows, err := sm.db.Query(`
		SELECT id, job_interface_id, peer_node_id, peer_job_id, peer_path,
		       peer_mount_function, duty_acknowledged, created_at
		FROM job_interface_peers
		WHERE job_interface_id = ?
	`, jobInterfaceID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job interface peers: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var peers []*JobInterfacePeer
	for rows.Next() {
		var peer JobInterfacePeer
		var peerJobID sql.NullInt64

		err := rows.Scan(
			&peer.ID,
			&peer.JobInterfaceID,
			&peer.PeerNodeID,
			&peerJobID,
			&peer.PeerPath,
			&peer.PeerMountFunction,
			&peer.DutyAcknowledged,
			&peer.CreatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan job interface peer: %v", err), "database")
			continue
		}

		if peerJobID.Valid {
			peer.PeerJobID = &peerJobID.Int64
		}

		peers = append(peers, &peer)
	}

	return peers, nil
}

// ValidateJobInputsReady checks if all STDIN interfaces have their data ready
func (sm *SQLiteManager) ValidateJobInputsReady(jobExecutionID int64) (bool, error) {
	// Get all STDIN interfaces for this job
	interfaces, err := sm.GetJobInterfaces(jobExecutionID)
	if err != nil {
		return false, err
	}

	// For each STDIN interface, check if all peers have acknowledged their duty
	for _, iface := range interfaces {
		if iface.InterfaceType == "STDIN" {
			peers, err := sm.GetJobInterfacePeers(iface.ID)
			if err != nil {
				return false, err
			}

			// Check if all provider peers have acknowledged
			for _, peer := range peers {
				if peer.PeerMountFunction == "PROVIDER" && !peer.DutyAcknowledged {
					return false, nil
				}
			}
		}
	}

	return true, nil
}

// MarkInterfacePeerAcknowledged marks an interface peer connection as acknowledged
func (sm *SQLiteManager) MarkInterfacePeerAcknowledged(interfacePeerID int64) error {
	_, err := sm.db.Exec(`
		UPDATE job_interface_peers
		SET duty_acknowledged = 1
		WHERE id = ?
	`, interfacePeerID)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to mark interface peer acknowledged: %v", err), "database")
		return err
	}

	return nil
}

// GetJobExecutionsByWorkflowJob retrieves all job executions for a workflow job
func (sm *SQLiteManager) GetJobExecutionsByWorkflowJob(workflowJobID int64) ([]*JobExecution, error) {
	rows, err := sm.db.Query(`
		SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       status, entrypoint, commands, execution_constraint, constraint_detail,
		       started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE workflow_job_id = ?
		ORDER BY created_at ASC
	`, workflowJobID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job executions: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var jobs []*JobExecution
	for rows.Next() {
		var job JobExecution
		var startedAt, endedAt sql.NullTime
		var errorMsg sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.WorkflowJobID,
			&job.ServiceID,
			&job.ExecutorPeerID,
			&job.OrderingPeerID,
			&job.Status,
			&job.Entrypoint,
			&job.Commands,
			&job.ExecutionConstraint,
			&job.ConstraintDetail,
			&startedAt,
			&endedAt,
			&errorMsg,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan job execution: %v", err), "database")
			continue
		}

		if startedAt.Valid {
			job.StartedAt = startedAt.Time
		}
		if endedAt.Valid {
			job.EndedAt = endedAt.Time
		}
		if errorMsg.Valid {
			job.ErrorMessage = errorMsg.String
		}

		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// Helper function to marshal string slices to JSON
func MarshalStringSlice(slice []string) (string, error) {
	if slice == nil {
		return "", nil
	}
	data, err := json.Marshal(slice)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper function to unmarshal JSON to string slices
func UnmarshalStringSlice(data string) ([]string, error) {
	if data == "" {
		return nil, nil
	}
	var slice []string
	err := json.Unmarshal([]byte(data), &slice)
	if err != nil {
		return nil, err
	}
	return slice, nil
}
