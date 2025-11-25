package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// DataTransfer represents a P2P file transfer with resumption support
type DataTransfer struct {
	ID                int64     `json:"id"`
	TransferID        string    `json:"transfer_id"`
	JobExecutionID    int64     `json:"job_execution_id"`
	SourcePeerID      string    `json:"source_peer_id"`
	DestinationPeerID string    `json:"destination_peer_id"`
	FilePath          string    `json:"file_path"`
	TotalChunks       int       `json:"total_chunks"`
	ChunkSize         int       `json:"chunk_size"`
	TotalBytes        int64     `json:"total_bytes"`
	FileHash          string    `json:"file_hash"`
	Encrypted         bool      `json:"encrypted"`

	// Progress tracking (stored as JSON arrays)
	ChunksSent     []int  `json:"chunks_sent"`
	ChunksReceived []int  `json:"chunks_received"`
	ChunksAcked    []int  `json:"chunks_acked"`
	BytesTransferred int64 `json:"bytes_transferred"`

	// State
	Status        string    `json:"status"`         // active, paused, completed, failed
	Direction     string    `json:"direction"`      // sender, receiver
	LastActivity  time.Time `json:"last_activity"`
	LastCheckpoint time.Time `json:"last_checkpoint"`
	ErrorMessage  string    `json:"error_message,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InitDataTransfersTable creates the data_transfers table
func (sm *SQLiteManager) InitDataTransfersTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS data_transfers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		transfer_id TEXT UNIQUE NOT NULL,
		job_execution_id INTEGER NOT NULL,
		source_peer_id TEXT NOT NULL,
		destination_peer_id TEXT NOT NULL,
		file_path TEXT NOT NULL,
		total_chunks INTEGER NOT NULL,
		chunk_size INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		file_hash TEXT,
		encrypted BOOLEAN DEFAULT 0,

		chunks_sent TEXT,
		chunks_received TEXT,
		chunks_acked TEXT,
		bytes_transferred INTEGER DEFAULT 0,

		status TEXT DEFAULT 'active',
		direction TEXT NOT NULL,
		last_activity TIMESTAMP,
		last_checkpoint TIMESTAMP,
		error_message TEXT,

		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_data_transfers_transfer_id ON data_transfers(transfer_id);
	CREATE INDEX IF NOT EXISTS idx_data_transfers_job_execution_id ON data_transfers(job_execution_id);
	CREATE INDEX IF NOT EXISTS idx_data_transfers_status ON data_transfers(status);
	CREATE INDEX IF NOT EXISTS idx_data_transfers_last_activity ON data_transfers(last_activity);
	`

	_, err := sm.db.Exec(query)
	if err != nil {
		sm.logger.Error("Failed to create data_transfers table", "database")
		return err
	}

	sm.logger.Info("Data transfers table initialized successfully", "database")
	return nil
}

// CreateDataTransfer creates a new data transfer record
func (sm *SQLiteManager) CreateDataTransfer(transfer *DataTransfer) error {
	// Marshal chunk arrays to JSON
	chunksSentJSON, _ := json.Marshal(transfer.ChunksSent)
	chunksReceivedJSON, _ := json.Marshal(transfer.ChunksReceived)
	chunksAckedJSON, _ := json.Marshal(transfer.ChunksAcked)

	query := `
		INSERT INTO data_transfers (
			transfer_id, job_execution_id, source_peer_id, destination_peer_id,
			file_path, total_chunks, chunk_size, total_bytes, file_hash, encrypted,
			chunks_sent, chunks_received, chunks_acked, bytes_transferred,
			status, direction, last_activity, last_checkpoint
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		transfer.TransferID,
		transfer.JobExecutionID,
		transfer.SourcePeerID,
		transfer.DestinationPeerID,
		transfer.FilePath,
		transfer.TotalChunks,
		transfer.ChunkSize,
		transfer.TotalBytes,
		transfer.FileHash,
		transfer.Encrypted,
		string(chunksSentJSON),
		string(chunksReceivedJSON),
		string(chunksAckedJSON),
		transfer.BytesTransferred,
		transfer.Status,
		transfer.Direction,
		time.Now(),
		time.Now(),
	)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create data transfer: %v", err), "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	transfer.ID = id
	transfer.CreatedAt = time.Now()
	transfer.UpdatedAt = time.Now()

	sm.logger.Info(fmt.Sprintf("Data transfer created: %s (ID: %d)", transfer.TransferID, transfer.ID), "database")
	return nil
}

// UpdateTransferProgress updates chunk progress for a transfer
func (sm *SQLiteManager) UpdateTransferProgress(transferID string, chunksSent, chunksReceived, chunksAcked []int, bytesTransferred int64) error {
	chunksSentJSON, _ := json.Marshal(chunksSent)
	chunksReceivedJSON, _ := json.Marshal(chunksReceived)
	chunksAckedJSON, _ := json.Marshal(chunksAcked)

	query := `
		UPDATE data_transfers
		SET chunks_sent = ?,
		    chunks_received = ?,
		    chunks_acked = ?,
		    bytes_transferred = ?,
		    last_activity = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	result, err := sm.db.Exec(
		query,
		string(chunksSentJSON),
		string(chunksReceivedJSON),
		string(chunksAckedJSON),
		bytesTransferred,
		time.Now(),
		transferID,
	)

	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update transfer progress: %v", err), "database")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("transfer not found: %s", transferID)
	}

	return nil
}

// UpdateTransferCheckpoint saves a checkpoint timestamp
func (sm *SQLiteManager) UpdateTransferCheckpoint(transferID string) error {
	query := `
		UPDATE data_transfers
		SET last_checkpoint = ?,
		    last_activity = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	_, err := sm.db.Exec(query, time.Now(), time.Now(), transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to update transfer checkpoint: %v", err), "database")
		return err
	}

	return nil
}

// GetTransferByID retrieves a transfer by transfer ID
func (sm *SQLiteManager) GetTransferByID(transferID string) (*DataTransfer, error) {
	query := `
		SELECT id, transfer_id, job_execution_id, source_peer_id, destination_peer_id,
		       file_path, total_chunks, chunk_size, total_bytes, file_hash, encrypted,
		       chunks_sent, chunks_received, chunks_acked, bytes_transferred,
		       status, direction, last_activity, last_checkpoint, error_message,
		       created_at, updated_at
		FROM data_transfers
		WHERE transfer_id = ?
	`

	var transfer DataTransfer
	var chunksSentJSON, chunksReceivedJSON, chunksAckedJSON sql.NullString
	var lastActivity, lastCheckpoint sql.NullTime
	var errorMsg sql.NullString

	err := sm.db.QueryRow(query, transferID).Scan(
		&transfer.ID,
		&transfer.TransferID,
		&transfer.JobExecutionID,
		&transfer.SourcePeerID,
		&transfer.DestinationPeerID,
		&transfer.FilePath,
		&transfer.TotalChunks,
		&transfer.ChunkSize,
		&transfer.TotalBytes,
		&transfer.FileHash,
		&transfer.Encrypted,
		&chunksSentJSON,
		&chunksReceivedJSON,
		&chunksAckedJSON,
		&transfer.BytesTransferred,
		&transfer.Status,
		&transfer.Direction,
		&lastActivity,
		&lastCheckpoint,
		&errorMsg,
		&transfer.CreatedAt,
		&transfer.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("transfer not found: %s", transferID)
		}
		sm.logger.Error(fmt.Sprintf("Failed to get transfer: %v", err), "database")
		return nil, err
	}

	// Unmarshal chunk arrays
	if chunksSentJSON.Valid {
		json.Unmarshal([]byte(chunksSentJSON.String), &transfer.ChunksSent)
	}
	if chunksReceivedJSON.Valid {
		json.Unmarshal([]byte(chunksReceivedJSON.String), &transfer.ChunksReceived)
	}
	if chunksAckedJSON.Valid {
		json.Unmarshal([]byte(chunksAckedJSON.String), &transfer.ChunksAcked)
	}

	if lastActivity.Valid {
		transfer.LastActivity = lastActivity.Time
	}
	if lastCheckpoint.Valid {
		transfer.LastCheckpoint = lastCheckpoint.Time
	}
	if errorMsg.Valid {
		transfer.ErrorMessage = errorMsg.String
	}

	return &transfer, nil
}

// GetActiveTransfers retrieves all active transfers
func (sm *SQLiteManager) GetActiveTransfers() ([]*DataTransfer, error) {
	query := `
		SELECT id, transfer_id, job_execution_id, source_peer_id, destination_peer_id,
		       file_path, total_chunks, chunk_size, total_bytes, file_hash, encrypted,
		       chunks_sent, chunks_received, chunks_acked, bytes_transferred,
		       status, direction, last_activity, last_checkpoint, error_message,
		       created_at, updated_at
		FROM data_transfers
		WHERE status = 'active'
		ORDER BY created_at DESC
	`

	rows, err := sm.db.Query(query)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get active transfers: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	return sm.scanTransfers(rows)
}

// GetStalledTransfers retrieves transfers that haven't had activity within timeout
func (sm *SQLiteManager) GetStalledTransfers(timeout time.Duration) ([]*DataTransfer, error) {
	stallTime := time.Now().Add(-timeout)

	query := `
		SELECT id, transfer_id, job_execution_id, source_peer_id, destination_peer_id,
		       file_path, total_chunks, chunk_size, total_bytes, file_hash, encrypted,
		       chunks_sent, chunks_received, chunks_acked, bytes_transferred,
		       status, direction, last_activity, last_checkpoint, error_message,
		       created_at, updated_at
		FROM data_transfers
		WHERE status = 'active'
		  AND last_activity < ?
		ORDER BY last_activity ASC
	`

	rows, err := sm.db.Query(query, stallTime)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to get stalled transfers: %v", err), "database")
		return nil, err
	}
	defer rows.Close()

	return sm.scanTransfers(rows)
}

// HasPendingTransfersForJob checks if there are any active/pending incoming transfers for a job
func (sm *SQLiteManager) HasPendingTransfersForJob(jobExecutionID int64) (bool, error) {
	query := `
		SELECT COUNT(*) FROM data_transfers
		WHERE job_execution_id = ?
		  AND direction = 'incoming'
		  AND status IN ('active', 'pending', 'paused')
	`

	var count int
	err := sm.db.QueryRow(query, jobExecutionID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check pending transfers: %v", err)
	}

	return count > 0, nil
}

// CompleteTransfer marks a transfer as completed
func (sm *SQLiteManager) CompleteTransfer(transferID string) error {
	query := `
		UPDATE data_transfers
		SET status = 'completed',
		    last_activity = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	result, err := sm.db.Exec(query, time.Now(), transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to complete transfer: %v", err), "database")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("transfer not found: %s", transferID)
	}

	sm.logger.Info(fmt.Sprintf("Transfer completed: %s", transferID), "database")
	return nil
}

// FailTransfer marks a transfer as failed with error message
func (sm *SQLiteManager) FailTransfer(transferID string, errorMessage string) error {
	query := `
		UPDATE data_transfers
		SET status = 'failed',
		    error_message = ?,
		    last_activity = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	result, err := sm.db.Exec(query, errorMessage, time.Now(), transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to mark transfer as failed: %v", err), "database")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("transfer not found: %s", transferID)
	}

	sm.logger.Error(fmt.Sprintf("Transfer failed: %s - %s", transferID, errorMessage), "database")
	return nil
}

// PauseTransfer marks a transfer as paused
func (sm *SQLiteManager) PauseTransfer(transferID string) error {
	query := `
		UPDATE data_transfers
		SET status = 'paused',
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	_, err := sm.db.Exec(query, transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to pause transfer: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Transfer paused: %s", transferID), "database")
	return nil
}

// ResumeTransfer marks a paused transfer as active
func (sm *SQLiteManager) ResumeTransfer(transferID string) error {
	query := `
		UPDATE data_transfers
		SET status = 'active',
		    last_activity = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = ?
	`

	_, err := sm.db.Exec(query, time.Now(), transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to resume transfer: %v", err), "database")
		return err
	}

	sm.logger.Info(fmt.Sprintf("Transfer resumed: %s", transferID), "database")
	return nil
}

// DeleteTransfer removes a transfer record
func (sm *SQLiteManager) DeleteTransfer(transferID string) error {
	query := `DELETE FROM data_transfers WHERE transfer_id = ?`

	result, err := sm.db.Exec(query, transferID)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to delete transfer: %v", err), "database")
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("transfer not found: %s", transferID)
	}

	sm.logger.Info(fmt.Sprintf("Transfer deleted: %s", transferID), "database")
	return nil
}

// scanTransfers is a helper to scan multiple transfer rows
func (sm *SQLiteManager) scanTransfers(rows *sql.Rows) ([]*DataTransfer, error) {
	var transfers []*DataTransfer

	for rows.Next() {
		var transfer DataTransfer
		var chunksSentJSON, chunksReceivedJSON, chunksAckedJSON sql.NullString
		var lastActivity, lastCheckpoint sql.NullTime
		var errorMsg sql.NullString

		err := rows.Scan(
			&transfer.ID,
			&transfer.TransferID,
			&transfer.JobExecutionID,
			&transfer.SourcePeerID,
			&transfer.DestinationPeerID,
			&transfer.FilePath,
			&transfer.TotalChunks,
			&transfer.ChunkSize,
			&transfer.TotalBytes,
			&transfer.FileHash,
			&transfer.Encrypted,
			&chunksSentJSON,
			&chunksReceivedJSON,
			&chunksAckedJSON,
			&transfer.BytesTransferred,
			&transfer.Status,
			&transfer.Direction,
			&lastActivity,
			&lastCheckpoint,
			&errorMsg,
			&transfer.CreatedAt,
			&transfer.UpdatedAt,
		)

		if err != nil {
			sm.logger.Error(fmt.Sprintf("Failed to scan transfer row: %v", err), "database")
			continue
		}

		// Unmarshal chunk arrays
		if chunksSentJSON.Valid {
			json.Unmarshal([]byte(chunksSentJSON.String), &transfer.ChunksSent)
		}
		if chunksReceivedJSON.Valid {
			json.Unmarshal([]byte(chunksReceivedJSON.String), &transfer.ChunksReceived)
		}
		if chunksAckedJSON.Valid {
			json.Unmarshal([]byte(chunksAckedJSON.String), &transfer.ChunksAcked)
		}

		if lastActivity.Valid {
			transfer.LastActivity = lastActivity.Time
		}
		if lastCheckpoint.Valid {
			transfer.LastCheckpoint = lastCheckpoint.Time
		}
		if errorMsg.Valid {
			transfer.ErrorMessage = errorMsg.String
		}

		transfers = append(transfers, &transfer)
	}

	return transfers, nil
}
