package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JobExecution represents a job execution instance
type JobExecution struct {
	ID                   int64     `json:"id"`
	WorkflowJobID        int64     `json:"workflow_job_id"` // Workflow execution instance ID
	ServiceID            int64     `json:"service_id"`
	ExecutorPeerID       string    `json:"executor_peer_id"`        // Ed25519 peer ID who executes
	OrderingPeerID       string    `json:"ordering_peer_id"`        // Ed25519 peer ID who requested
	RemoteJobExecutionID *int64    `json:"remote_job_execution_id"` // Job ID on remote executor peer (nullable for local jobs)
	Status               string    `json:"status"`                  // IDLE, READY, RUNNING, COMPLETED, ERRORED, CANCELLED
	Entrypoint           string    `json:"entrypoint,omitempty"`    // JSON array of strings
	Commands             string    `json:"commands,omitempty"`      // JSON array of strings
	ExecutionConstraint  string    `json:"execution_constraint"`    // NONE, INPUTS_READY
	ConstraintDetail     string    `json:"constraint_detail,omitempty"`
	StartReceived        bool      `json:"start_received"`        // True when HandleJobStart has been called (Phase 2 complete)
	StartedAt            time.Time `json:"started_at,omitempty"`
	EndedAt              time.Time `json:"ended_at,omitempty"`
	ErrorMessage         string    `json:"error_message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// JobInterface represents an interface (STDIN, STDOUT, STDERR, LOGS, MOUNT) for a job
type JobInterface struct {
	ID             int64     `json:"id"`
	JobExecutionID int64     `json:"job_execution_id"`
	InterfaceType  string    `json:"interface_type"` // STDIN, STDOUT, STDERR, LOGS, MOUNT
	Path           string    `json:"path"`
	CreatedAt      time.Time `json:"created_at"`
}

// JobInterfacePeer represents a peer connection for a job interface
type JobInterfacePeer struct {
	ID                 int64     `json:"id"`
	JobInterfaceID     int64     `json:"job_interface_id"`
	PeerID             string    `json:"peer_id"`               // Peer ID (libp2p peer identifier)
	PeerWorkflowJobID  *int64    `json:"peer_workflow_job_id"`  // Orchestrator's workflow_job_id
	PeerJobExecutionID *int64    `json:"peer_job_execution_id"` // Executor's job_execution_id (for path construction)
	PeerPath           string    `json:"peer_path"`
	PeerFileName       *string   `json:"peer_file_name"`      // Optional: rename file/folder when transferring
	PeerMountFunction  string    `json:"peer_mount_function"` // INPUT, OUTPUT, BOTH
	DutyAcknowledged   bool      `json:"duty_acknowledged"`
	CreatedAt          time.Time `json:"created_at"`
}

// JobPayment represents a payment for a job execution (x402 protocol)
type JobPayment struct {
	ID               int64   `json:"id"`
	JobExecutionID   *int64  `json:"job_execution_id,omitempty"` // Nullable for P2P invoices
	WalletID         string  `json:"wallet_id"`
	PaymentNetwork   string  `json:"payment_network"`
	PaymentSender    string  `json:"payment_sender"`
	PaymentRecipient string  `json:"payment_recipient"`
	PaymentAmount    float64 `json:"payment_amount"`
	PaymentCurrency  string  `json:"payment_currency"`
	PaymentNonce     string  `json:"payment_nonce"`
	PaymentSignature string  `json:"payment_signature"`
	PaymentTimestamp int64   `json:"payment_timestamp"` // Timestamp used in signature
	PaymentMetadata  *string `json:"payment_metadata,omitempty"` // JSON-encoded metadata (e.g., solana_fee_payer)
	TransactionID    *string `json:"transaction_id,omitempty"`
	Status           string  `json:"status"` // pending, verified, settled, refunded, failed
	VerifiedAt       *int64  `json:"verified_at,omitempty"`
	SettledAt        *int64  `json:"settled_at,omitempty"`
	RefundedAt       *int64  `json:"refunded_at,omitempty"`
	CreatedAt        int64   `json:"created_at"`
}

// InitJobExecutionsTable creates the job execution tables
func (sm *SQLiteManager) InitJobExecutionsTable() error {
	createTableSQL := `
	-- Job executions table
	-- Note: workflow_job_id and service_id are soft references (no FK constraints) to support
	-- distributed P2P execution where job executions may occur on peers that don't have the
	-- workflow_jobs or services records locally (services exist on remote executor peers).
	-- Application-level validation ensures referential integrity when needed.
	CREATE TABLE IF NOT EXISTS job_executions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_job_id INTEGER NOT NULL,
		service_id INTEGER NOT NULL,
		executor_peer_id TEXT NOT NULL,
		ordering_peer_id TEXT NOT NULL,
		remote_job_execution_id INTEGER,
		status TEXT CHECK(status IN ('IDLE', 'READY', 'RUNNING', 'COMPLETED', 'ERRORED', 'CANCELLED')) NOT NULL DEFAULT 'IDLE',
		entrypoint TEXT,
		commands TEXT,
		execution_constraint TEXT CHECK(execution_constraint IN ('NONE', 'INPUTS_READY')) DEFAULT 'NONE',
		constraint_detail TEXT,
		start_received INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		ended_at DATETIME,
		error_message TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_job_executions_workflow_job_id ON job_executions(workflow_job_id);
	CREATE INDEX IF NOT EXISTS idx_job_executions_status ON job_executions(status);
	CREATE INDEX IF NOT EXISTS idx_job_executions_executor_peer_id ON job_executions(executor_peer_id);
	CREATE INDEX IF NOT EXISTS idx_job_executions_ordering_peer_id ON job_executions(ordering_peer_id);
	CREATE INDEX IF NOT EXISTS idx_job_executions_remote_job_id ON job_executions(remote_job_execution_id);

	-- Job interfaces table
	CREATE TABLE IF NOT EXISTS job_interfaces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_execution_id INTEGER NOT NULL,
		interface_type TEXT CHECK(interface_type IN ('STDIN', 'STDOUT', 'STDERR', 'LOGS', 'MOUNT')) NOT NULL,
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
		peer_id TEXT NOT NULL,
		peer_workflow_job_id INTEGER,
		peer_job_execution_id INTEGER,
		peer_path TEXT NOT NULL,
		peer_file_name TEXT,
		peer_mount_function TEXT CHECK(peer_mount_function IN ('INPUT', 'OUTPUT', 'BOTH')) NOT NULL,
		duty_acknowledged BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (job_interface_id) REFERENCES job_interfaces(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_job_interface_id ON job_interface_peers(job_interface_id);
	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_peer_id ON job_interface_peers(peer_id);
	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_peer_workflow_job_id ON job_interface_peers(peer_workflow_job_id);
	CREATE INDEX IF NOT EXISTS idx_job_interface_peers_peer_job_execution_id ON job_interface_peers(peer_job_execution_id);

	-- Job payments table (x402 payment protocol)
	-- Note: job_execution_id is nullable to support P2P invoice payments (not tied to job executions)
	CREATE TABLE IF NOT EXISTS job_payments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_execution_id INTEGER,
		wallet_id TEXT NOT NULL,
		payment_network TEXT NOT NULL,
		payment_sender TEXT NOT NULL,
		payment_recipient TEXT NOT NULL,
		payment_amount REAL NOT NULL,
		payment_currency TEXT NOT NULL DEFAULT 'USDC',
		payment_nonce TEXT NOT NULL UNIQUE,
		payment_signature TEXT NOT NULL,
		payment_timestamp INTEGER NOT NULL,
		payment_metadata TEXT,
		transaction_id TEXT,
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'verified', 'settled', 'refunded', 'failed')),
		verified_at INTEGER,
		settled_at INTEGER,
		refunded_at INTEGER,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		FOREIGN KEY (job_execution_id) REFERENCES job_executions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_payments_job_execution ON job_payments(job_execution_id);
	CREATE INDEX IF NOT EXISTS idx_job_payments_nonce ON job_payments(payment_nonce);
	CREATE INDEX IF NOT EXISTS idx_job_payments_status ON job_payments(status);
	CREATE INDEX IF NOT EXISTS idx_job_payments_wallet ON job_payments(wallet_id);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create job executions tables: %v", err), "database")
		return err
	}

	// Migration: Add payment_metadata column if it doesn't exist (for Solana fee payer and other metadata)
	_, err = sm.db.Exec(`
		ALTER TABLE job_payments ADD COLUMN payment_metadata TEXT;
	`)
	if err != nil {
		// Ignore error if column already exists (expected on existing databases)
		if !strings.Contains(err.Error(), "duplicate column name") {
			sm.logger.Warn(fmt.Sprintf("Failed to add payment_metadata column (may already exist): %v", err), "database")
		}
	} else {
		sm.logger.Info("Added payment_metadata column to job_payments table", "database")
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
	query := `SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       remote_job_execution_id, status, entrypoint, commands, execution_constraint, constraint_detail,
		       start_received, started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE id = ?`

	result, err := QueryRowSingle(sm.db, query,
		func(row *sql.Row) (*JobExecution, error) {
			var job JobExecution
			var startedAt, endedAt sql.NullTime
			var errorMsg sql.NullString
			var remoteJobID sql.NullInt64

			err := row.Scan(
				&job.ID,
				&job.WorkflowJobID,
				&job.ServiceID,
				&job.ExecutorPeerID,
				&job.OrderingPeerID,
				&remoteJobID,
				&job.Status,
				&job.Entrypoint,
				&job.Commands,
				&job.ExecutionConstraint,
				&job.ConstraintDetail,
				&job.StartReceived,
				&startedAt,
				&endedAt,
				&errorMsg,
				&job.CreatedAt,
				&job.UpdatedAt,
			)
			if err != nil {
				return nil, err
			}

			job.RemoteJobExecutionID = ScanNullableInt64(remoteJobID)
			if startedAt.Valid {
				job.StartedAt = startedAt.Time
			}
			if endedAt.Valid {
				job.EndedAt = endedAt.Time
			}
			job.ErrorMessage = ScanNullableString(errorMsg)

			return &job, nil
		},
		sm.logger, "database", id)

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("job execution not found")
	}

	return result, nil
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
	} else if status == "COMPLETED" {
		// Clear error message on successful completion
		_, err = sm.db.Exec(`
			UPDATE job_executions
			SET status = ?, ended_at = CURRENT_TIMESTAMP, error_message = '', updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, status, id)
	} else if status == "ERRORED" || status == "CANCELLED" {
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

// UpdateRemoteJobExecutionID updates the remote job execution ID for a job
func (sm *SQLiteManager) UpdateRemoteJobExecutionID(localID int64, remoteID int64) error {
	_, err := sm.db.Exec(`
		UPDATE job_executions
		SET remote_job_execution_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, remoteID, localID)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update remote_job_execution_id: %v", err), "database")
		return err
	}

	sm.logger.Debug(fmt.Sprintf("Updated remote_job_execution_id for job %d: remote_id=%d", localID, remoteID), "database")
	return nil
}

// MarkJobStartReceived marks a job as having received the start command (Phase 2 complete)
// This is called by HandleJobStart to indicate that interfaces have been created
func (sm *SQLiteManager) MarkJobStartReceived(id int64) error {
	_, err := sm.db.Exec(`
		UPDATE job_executions
		SET start_received = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to mark start_received for job %d: %v", id, err), "database")
		return err
	}

	sm.logger.Debug(fmt.Sprintf("Marked start_received=true for job %d", id), "database")
	return nil
}

// GetReadyJobs retrieves all jobs with READY status
func (sm *SQLiteManager) GetReadyJobs() ([]*JobExecution, error) {
	query := `SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       remote_job_execution_id, status, entrypoint, commands, execution_constraint, constraint_detail,
		       start_received, started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE status = 'READY'
		ORDER BY created_at ASC`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*JobExecution, error) {
			var job JobExecution
			var startedAt, endedAt sql.NullTime
			var errorMsg sql.NullString
			var remoteJobID sql.NullInt64

			err := rows.Scan(
				&job.ID,
				&job.WorkflowJobID,
				&job.ServiceID,
				&job.ExecutorPeerID,
				&job.OrderingPeerID,
				&remoteJobID,
				&job.Status,
				&job.Entrypoint,
				&job.Commands,
				&job.ExecutionConstraint,
				&job.ConstraintDetail,
				&job.StartReceived,
				&startedAt,
				&endedAt,
				&errorMsg,
				&job.CreatedAt,
				&job.UpdatedAt,
			)
			if err != nil {
				return nil, err
			}

			job.RemoteJobExecutionID = ScanNullableInt64(remoteJobID)
			if startedAt.Valid {
				job.StartedAt = startedAt.Time
			}
			if endedAt.Valid {
				job.EndedAt = endedAt.Time
			}
			job.ErrorMessage = ScanNullableString(errorMsg)

			return &job, nil
		},
		sm.logger, "database")
}

// GetJobsByStatus gets all job executions with a specific status
func (sm *SQLiteManager) GetJobsByStatus(status string) ([]*JobExecution, error) {
	query := `SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       remote_job_execution_id, status, entrypoint, commands, execution_constraint, constraint_detail,
		       start_received, started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE status = ?
		ORDER BY created_at ASC`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*JobExecution, error) {
			var job JobExecution
			var startedAt, endedAt sql.NullTime
			var errorMsg sql.NullString
			var remoteJobID sql.NullInt64

			err := rows.Scan(
				&job.ID,
				&job.WorkflowJobID,
				&job.ServiceID,
				&job.ExecutorPeerID,
				&job.OrderingPeerID,
				&remoteJobID,
				&job.Status,
				&job.Entrypoint,
				&job.Commands,
				&job.ExecutionConstraint,
				&job.ConstraintDetail,
				&job.StartReceived,
				&startedAt,
				&endedAt,
				&errorMsg,
				&job.CreatedAt,
				&job.UpdatedAt,
			)
			if err != nil {
				return nil, err
			}

			job.RemoteJobExecutionID = ScanNullableInt64(remoteJobID)
			if startedAt.Valid {
				job.StartedAt = startedAt.Time
			}
			if endedAt.Valid {
				job.EndedAt = endedAt.Time
			}
			job.ErrorMessage = ScanNullableString(errorMsg)

			return &job, nil
		},
		sm.logger, "database", status)
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
			job_interface_id, peer_id, peer_workflow_job_id, peer_job_execution_id,
			peer_path, peer_file_name, peer_mount_function, duty_acknowledged
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		peer.JobInterfaceID,
		peer.PeerID,
		peer.PeerWorkflowJobID,
		peer.PeerJobExecutionID,
		peer.PeerPath,
		peer.PeerFileName,
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
		SELECT id, job_interface_id, peer_id, peer_workflow_job_id, peer_job_execution_id,
		       peer_path, peer_file_name, peer_mount_function, duty_acknowledged, created_at
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
		var peerWorkflowJobID sql.NullInt64
		var peerJobExecutionID sql.NullInt64
		var peerFileName sql.NullString

		err := rows.Scan(
			&peer.ID,
			&peer.JobInterfaceID,
			&peer.PeerID,
			&peerWorkflowJobID,
			&peerJobExecutionID,
			&peer.PeerPath,
			&peerFileName,
			&peer.PeerMountFunction,
			&peer.DutyAcknowledged,
			&peer.CreatedAt,
		)
		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan job interface peer: %v", err), "database")
			continue
		}

		if peerWorkflowJobID.Valid {
			peer.PeerWorkflowJobID = &peerWorkflowJobID.Int64
		}
		if peerJobExecutionID.Valid {
			peer.PeerJobExecutionID = &peerJobExecutionID.Int64
		}
		if peerFileName.Valid {
			peer.PeerFileName = &peerFileName.String
		}

		peers = append(peers, &peer)
	}

	return peers, nil
}

// UpdateJobInterfacePeerJobExecutionID updates the peer_job_execution_id for a job interface peer
// This is called when a data transfer request is received to record the sender's job_execution_id
// so that hierarchical paths can be constructed correctly for input checking and Docker mounts
func (sm *SQLiteManager) UpdateJobInterfacePeerJobExecutionID(jobExecutionID int64, peerID string, peerJobExecutionID int64, peerWorkflowJobID int64) error {
	result, err := sm.db.Exec(`
		UPDATE job_interface_peers
		SET peer_job_execution_id = ?
		WHERE job_interface_id IN (
			SELECT id FROM job_interfaces WHERE job_execution_id = ?
		)
		AND peer_id = ?
		AND peer_workflow_job_id = ?
		AND peer_mount_function = 'OUTPUT'
	`, peerJobExecutionID, jobExecutionID, peerID, peerWorkflowJobID)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update peer_job_execution_id: %v", err), "database")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	sm.logger.Info(fmt.Sprintf("Updated peer_job_execution_id=%d for job %d from peer %s workflow_job %d (%d rows affected)",
		peerJobExecutionID, jobExecutionID, peerID[:8], peerWorkflowJobID, rowsAffected), "database")

	return nil
}

// GetSenderDestinationPath finds the destination path that a sender job used when sending data to a receiver job.
// This queries the sender's job_interface_peers to find the peer_path they used as the transfer destination.
func (sm *SQLiteManager) GetSenderDestinationPath(senderJobID int64, receiverJobID int64) (string, error) {
	var peerPath string
	err := sm.db.QueryRow(`
		SELECT jip.peer_path
		FROM job_interface_peers jip
		JOIN job_interfaces ji ON jip.job_interface_id = ji.id
		WHERE ji.job_execution_id = ?
		  AND jip.peer_job_id = ?
		  AND jip.peer_mount_function = 'INPUT'
		LIMIT 1
	`, senderJobID, receiverJobID).Scan(&peerPath)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // No matching record found
		}
		return "", err
	}

	return peerPath, nil
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

			// Check if all input provider peers have acknowledged
			// With fixed workflow definition, OUTPUT peers send data to us
			for _, peer := range peers {
				if peer.PeerMountFunction == "OUTPUT" && !peer.DutyAcknowledged {
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

// GetJobExecutionByWorkflowJobAndOrderingPeer retrieves a specific job execution by workflow_job_id and ordering_peer_id
// This ensures uniqueness when the same workflow_job_id might have multiple executions from different orchestrators
func (sm *SQLiteManager) GetJobExecutionByWorkflowJobAndOrderingPeer(workflowJobID int64, orderingPeerID string) (*JobExecution, error) {
	var job JobExecution
	var startedAt, endedAt sql.NullTime
	var errorMsg sql.NullString
	var remoteJobID sql.NullInt64

	err := sm.db.QueryRow(`
		SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       remote_job_execution_id, status, entrypoint, commands, execution_constraint, constraint_detail,
		       start_received, started_at, ended_at, error_message, created_at, updated_at
		FROM job_executions
		WHERE workflow_job_id = ? AND ordering_peer_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, workflowJobID, orderingPeerID).Scan(
		&job.ID,
		&job.WorkflowJobID,
		&job.ServiceID,
		&job.ExecutorPeerID,
		&job.OrderingPeerID,
		&remoteJobID,
		&job.Status,
		&job.Entrypoint,
		&job.Commands,
		&job.ExecutionConstraint,
		&job.ConstraintDetail,
		&job.StartReceived,
		&startedAt,
		&endedAt,
		&errorMsg,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job execution not found for workflow_job_id=%d, ordering_peer_id=%s", workflowJobID, orderingPeerID)
	}
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job execution: %v", err), "database")
		return nil, err
	}

	if remoteJobID.Valid {
		job.RemoteJobExecutionID = &remoteJobID.Int64
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

// GetJobExecutionsByWorkflowJob retrieves all job executions for a workflow job
// Note: This uses workflow_job_id as a soft reference - the workflow_job record may not exist
// locally for cross-peer executions, and that's expected in a distributed P2P system.
func (sm *SQLiteManager) GetJobExecutionsByWorkflowJob(workflowJobID int64) ([]*JobExecution, error) {
	rows, err := sm.db.Query(`
		SELECT id, workflow_job_id, service_id, executor_peer_id, ordering_peer_id,
		       remote_job_execution_id, status, entrypoint, commands, execution_constraint, constraint_detail,
		       start_received, started_at, ended_at, error_message, created_at, updated_at
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
		var remoteJobID sql.NullInt64

		err := rows.Scan(
			&job.ID,
			&job.WorkflowJobID,
			&job.ServiceID,
			&job.ExecutorPeerID,
			&job.OrderingPeerID,
			&remoteJobID,
			&job.Status,
			&job.Entrypoint,
			&job.Commands,
			&job.ExecutionConstraint,
			&job.ConstraintDetail,
			&job.StartReceived,
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

		if remoteJobID.Valid {
			job.RemoteJobExecutionID = &remoteJobID.Int64
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

// GetJobExecutionsByWorkflowID retrieves all job executions for a workflow
// This joins workflow_jobs and job_executions to get all executions for all jobs in a workflow
func (sm *SQLiteManager) GetJobExecutionsByWorkflowID(workflowID int64) ([]*JobExecution, error) {
	rows, err := sm.db.Query(`
		SELECT
			je.id, je.workflow_job_id, je.service_id, je.executor_peer_id, je.ordering_peer_id,
			je.remote_job_execution_id, je.status, je.entrypoint, je.commands, je.execution_constraint, je.constraint_detail,
			je.start_received, je.started_at, je.ended_at, je.error_message, je.created_at, je.updated_at
		FROM job_executions je
		INNER JOIN workflow_jobs wj ON je.workflow_job_id = wj.id
		WHERE wj.workflow_id = ?
		ORDER BY je.created_at DESC
	`, workflowID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get job executions for workflow: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	var jobs []*JobExecution
	for rows.Next() {
		var job JobExecution
		var startedAt, endedAt sql.NullTime
		var errorMsg sql.NullString
		var remoteJobID sql.NullInt64

		err := rows.Scan(
			&job.ID,
			&job.WorkflowJobID,
			&job.ServiceID,
			&job.ExecutorPeerID,
			&job.OrderingPeerID,
			&remoteJobID,
			&job.Status,
			&job.Entrypoint,
			&job.Commands,
			&job.ExecutionConstraint,
			&job.ConstraintDetail,
			&job.StartReceived,
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

		if remoteJobID.Valid {
			job.RemoteJobExecutionID = &remoteJobID.Int64
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

// Helper function to marshal string maps to JSON
func MarshalStringMap(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper function to unmarshal JSON to string maps
func UnmarshalStringMap(data string) (map[string]string, error) {
	if data == "" || data == "{}" {
		return map[string]string{}, nil
	}
	var m map[string]string
	err := json.Unmarshal([]byte(data), &m)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ==================== Job Payment Functions ====================

// CreateJobPayment creates a new payment record for a job execution
func (sm *SQLiteManager) CreateJobPayment(payment *JobPayment) (int64, error) {
	result, err := sm.db.Exec(`
		INSERT INTO job_payments (
			job_execution_id, wallet_id, payment_network, payment_sender, payment_recipient,
			payment_amount, payment_currency, payment_nonce, payment_signature, payment_timestamp,
			payment_metadata, transaction_id, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		payment.JobExecutionID,
		payment.WalletID,
		payment.PaymentNetwork,
		payment.PaymentSender,
		payment.PaymentRecipient,
		payment.PaymentAmount,
		payment.PaymentCurrency,
		payment.PaymentNonce,
		payment.PaymentSignature,
		payment.PaymentTimestamp,
		payment.PaymentMetadata,
		payment.TransactionID,
		payment.Status,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create job payment: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get payment ID: %v", err)
	}

	return id, nil
}

// GetJobPayment retrieves a payment by ID
func (sm *SQLiteManager) GetJobPayment(paymentID int64) (*JobPayment, error) {
	payment := &JobPayment{}
	err := sm.db.QueryRow(`
		SELECT id, job_execution_id, wallet_id, payment_network, payment_sender, payment_recipient,
		       payment_amount, payment_currency, payment_nonce, payment_signature, payment_timestamp,
		       payment_metadata, transaction_id, status, verified_at, settled_at, refunded_at, created_at
		FROM job_payments WHERE id = ?
	`, paymentID).Scan(
		&payment.ID,
		&payment.JobExecutionID,
		&payment.WalletID,
		&payment.PaymentNetwork,
		&payment.PaymentSender,
		&payment.PaymentRecipient,
		&payment.PaymentAmount,
		&payment.PaymentCurrency,
		&payment.PaymentNonce,
		&payment.PaymentSignature,
		&payment.PaymentTimestamp,
		&payment.PaymentMetadata,
		&payment.TransactionID,
		&payment.Status,
		&payment.VerifiedAt,
		&payment.SettledAt,
		&payment.RefundedAt,
		&payment.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get job payment: %v", err)
	}

	return payment, nil
}

// GetJobPaymentByJobID retrieves a payment for a specific job execution
func (sm *SQLiteManager) GetJobPaymentByJobID(jobExecutionID int64) (*JobPayment, error) {
	payment := &JobPayment{}
	err := sm.db.QueryRow(`
		SELECT id, job_execution_id, wallet_id, payment_network, payment_sender, payment_recipient,
		       payment_amount, payment_currency, payment_nonce, payment_signature, payment_timestamp,
		       payment_metadata, transaction_id, status, verified_at, settled_at, refunded_at, created_at
		FROM job_payments WHERE job_execution_id = ?
	`, jobExecutionID).Scan(
		&payment.ID,
		&payment.JobExecutionID,
		&payment.WalletID,
		&payment.PaymentNetwork,
		&payment.PaymentSender,
		&payment.PaymentRecipient,
		&payment.PaymentAmount,
		&payment.PaymentCurrency,
		&payment.PaymentNonce,
		&payment.PaymentSignature,
		&payment.PaymentTimestamp,
		&payment.PaymentMetadata,
		&payment.TransactionID,
		&payment.Status,
		&payment.VerifiedAt,
		&payment.SettledAt,
		&payment.RefundedAt,
		&payment.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get job payment by job ID: %v", err)
	}

	return payment, nil
}

// UpdateJobPaymentStatus updates the payment status and relevant timestamp
func (sm *SQLiteManager) UpdateJobPaymentStatus(paymentID int64, status string) error {
	now := time.Now().Unix()
	var query string

	switch status {
	case "verified":
		query = "UPDATE job_payments SET status = ?, verified_at = ? WHERE id = ?"
	case "settled":
		query = "UPDATE job_payments SET status = ?, settled_at = ? WHERE id = ?"
	case "refunded":
		query = "UPDATE job_payments SET status = ?, refunded_at = ? WHERE id = ?"
	case "failed":
		query = "UPDATE job_payments SET status = ? WHERE id = ?"
		_, err := sm.db.Exec(query, status, paymentID)
		return err
	default:
		return fmt.Errorf("unknown payment status: %s", status)
	}

	_, err := sm.db.Exec(query, status, now, paymentID)
	if err != nil {
		return fmt.Errorf("failed to update payment status: %v", err)
	}

	return nil
}

// UpdateJobPaymentExecutionID updates the job_execution_id for a payment
// This is used when payment is created before job_execution (with temp ID 0)
func (sm *SQLiteManager) UpdateJobPaymentExecutionID(paymentID int64, jobExecutionID int64) error {
	_, err := sm.db.Exec(`
		UPDATE job_payments SET job_execution_id = ? WHERE id = ?
	`, jobExecutionID, paymentID)
	if err != nil {
		return fmt.Errorf("failed to update payment job_execution_id: %v", err)
	}
	return nil
}

// UpdateJobPaymentTransaction updates the transaction ID for a payment
func (sm *SQLiteManager) UpdateJobPaymentTransaction(paymentID int64, transactionID string) error {
	_, err := sm.db.Exec(`
		UPDATE job_payments SET transaction_id = ? WHERE id = ?
	`, transactionID, paymentID)

	if err != nil {
		return fmt.Errorf("failed to update payment transaction ID: %v", err)
	}

	return nil
}
