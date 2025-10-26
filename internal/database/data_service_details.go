package database

import (
	"database/sql"
	"time"
)

// DataServiceDetails represents details about a data service
type DataServiceDetails struct {
	ID                int64     `json:"id"`
	ServiceID         int64     `json:"service_id"`
	FilePath          string    `json:"file_path"`
	EncryptedPath     string    `json:"encrypted_path"`
	Hash              string    `json:"hash"`
	CompressionType   string    `json:"compression_type"`
	EncryptionKeyID   *int64    `json:"encryption_key_id"`
	SizeBytes         int64     `json:"size_bytes"`
	OriginalSizeBytes int64     `json:"original_size_bytes"`
	UploadCompleted   bool      `json:"upload_completed"`
	CreatedAt         time.Time `json:"created_at"`
}

// EncryptionKey represents an encryption key for a service
type EncryptionKey struct {
	ID             int64     `json:"id"`
	ServiceID      int64     `json:"service_id"`
	PassphraseHash string    `json:"passphrase_hash"`
	KeyData        string    `json:"key_data"`
	CreatedAt      time.Time `json:"created_at"`
}

// UploadSession represents an active file upload session
type UploadSession struct {
	ID           int64     `json:"id"`
	ServiceID    int64     `json:"service_id"`
	SessionID    string    `json:"session_id"`
	Filename     string    `json:"filename"`
	ChunkIndex   int       `json:"chunk_index"`
	TotalChunks  int       `json:"total_chunks"`
	BytesUploaded int64    `json:"bytes_uploaded"`
	TotalBytes   int64     `json:"total_bytes"`
	ChunkSize    int       `json:"chunk_size"`
	TempFilePath string    `json:"temp_file_path"`
	Status       string    `json:"status"` // IN_PROGRESS, PAUSED, COMPLETED, FAILED
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AddDataServiceDetails adds data service details to the database
func (sm *SQLiteManager) AddDataServiceDetails(details *DataServiceDetails) error {
	query := `
		INSERT INTO data_service_details (
			service_id, file_path, encrypted_path, hash, compression_type,
			encryption_key_id, size_bytes, original_size_bytes, upload_completed
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		details.ServiceID,
		details.FilePath,
		details.EncryptedPath,
		details.Hash,
		details.CompressionType,
		details.EncryptionKeyID,
		details.SizeBytes,
		details.OriginalSizeBytes,
		details.UploadCompleted,
	)

	if err != nil {
		sm.logger.Error("Failed to add data service details", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	details.ID = id
	details.CreatedAt = time.Now()

	return nil
}

// GetDataServiceDetails retrieves data service details by service ID
func (sm *SQLiteManager) GetDataServiceDetails(serviceID int64) (*DataServiceDetails, error) {
	query := `
		SELECT id, service_id, file_path, encrypted_path, hash, compression_type,
		       encryption_key_id, size_bytes, original_size_bytes, upload_completed, created_at
		FROM data_service_details
		WHERE service_id = ?
	`

	var details DataServiceDetails
	err := sm.db.QueryRow(query, serviceID).Scan(
		&details.ID,
		&details.ServiceID,
		&details.FilePath,
		&details.EncryptedPath,
		&details.Hash,
		&details.CompressionType,
		&details.EncryptionKeyID,
		&details.SizeBytes,
		&details.OriginalSizeBytes,
		&details.UploadCompleted,
		&details.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		sm.logger.Error("Failed to get data service details", "database")
		return nil, err
	}

	return &details, nil
}

// UpdateDataServiceDetails updates data service details
func (sm *SQLiteManager) UpdateDataServiceDetails(details *DataServiceDetails) error {
	query := `
		UPDATE data_service_details
		SET file_path = ?, encrypted_path = ?, hash = ?, compression_type = ?,
		    encryption_key_id = ?, size_bytes = ?, original_size_bytes = ?, upload_completed = ?
		WHERE id = ?
	`

	result, err := sm.db.Exec(
		query,
		details.FilePath,
		details.EncryptedPath,
		details.Hash,
		details.CompressionType,
		details.EncryptionKeyID,
		details.SizeBytes,
		details.OriginalSizeBytes,
		details.UploadCompleted,
		details.ID,
	)

	if err != nil {
		sm.logger.Error("Failed to update data service details", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// AddEncryptionKey adds an encryption key to the database
func (sm *SQLiteManager) AddEncryptionKey(key *EncryptionKey) error {
	query := `
		INSERT INTO encryption_keys (service_id, passphrase_hash, key_data)
		VALUES (?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		key.ServiceID,
		key.PassphraseHash,
		key.KeyData,
	)

	if err != nil {
		sm.logger.Error("Failed to add encryption key", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	key.ID = id
	key.CreatedAt = time.Now()

	return nil
}

// GetEncryptionKey retrieves an encryption key by service ID
func (sm *SQLiteManager) GetEncryptionKey(serviceID int64) (*EncryptionKey, error) {
	query := `
		SELECT id, service_id, passphrase_hash, key_data, created_at
		FROM encryption_keys
		WHERE service_id = ?
	`

	var key EncryptionKey
	err := sm.db.QueryRow(query, serviceID).Scan(
		&key.ID,
		&key.ServiceID,
		&key.PassphraseHash,
		&key.KeyData,
		&key.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		sm.logger.Error("Failed to get encryption key", "database")
		return nil, err
	}

	return &key, nil
}

// CreateUploadSession creates a new upload session
func (sm *SQLiteManager) CreateUploadSession(session *UploadSession) error {
	query := `
		INSERT INTO upload_sessions (
			service_id, session_id, filename, total_chunks, total_bytes,
			chunk_size, temp_file_path, status
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		session.ServiceID,
		session.SessionID,
		session.Filename,
		session.TotalChunks,
		session.TotalBytes,
		session.ChunkSize,
		session.TempFilePath,
		session.Status,
	)

	if err != nil {
		sm.logger.Error("Failed to create upload session", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	session.ID = id
	session.CreatedAt = time.Now()
	session.UpdatedAt = time.Now()

	return nil
}

// GetUploadSession retrieves an upload session by session ID
func (sm *SQLiteManager) GetUploadSession(sessionID string) (*UploadSession, error) {
	query := `
		SELECT id, service_id, session_id, filename, chunk_index, total_chunks,
		       bytes_uploaded, total_bytes, chunk_size, temp_file_path, status,
		       created_at, updated_at
		FROM upload_sessions
		WHERE session_id = ?
	`

	var session UploadSession
	err := sm.db.QueryRow(query, sessionID).Scan(
		&session.ID,
		&session.ServiceID,
		&session.SessionID,
		&session.Filename,
		&session.ChunkIndex,
		&session.TotalChunks,
		&session.BytesUploaded,
		&session.TotalBytes,
		&session.ChunkSize,
		&session.TempFilePath,
		&session.Status,
		&session.CreatedAt,
		&session.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		sm.logger.Error("Failed to get upload session", "database")
		return nil, err
	}

	return &session, nil
}

// UpdateUploadSession updates an upload session
func (sm *SQLiteManager) UpdateUploadSession(session *UploadSession) error {
	query := `
		UPDATE upload_sessions
		SET chunk_index = ?, bytes_uploaded = ?, status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE session_id = ?
	`

	result, err := sm.db.Exec(
		query,
		session.ChunkIndex,
		session.BytesUploaded,
		session.Status,
		session.SessionID,
	)

	if err != nil {
		sm.logger.Error("Failed to update upload session", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	session.UpdatedAt = time.Now()
	return nil
}

// DeleteUploadSession deletes an upload session
func (sm *SQLiteManager) DeleteUploadSession(sessionID string) error {
	query := `DELETE FROM upload_sessions WHERE session_id = ?`

	result, err := sm.db.Exec(query, sessionID)
	if err != nil {
		sm.logger.Error("Failed to delete upload session", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// GetActiveUploadSessions retrieves all active upload sessions
func (sm *SQLiteManager) GetActiveUploadSessions() ([]*UploadSession, error) {
	query := `
		SELECT id, service_id, session_id, filename, chunk_index, total_chunks,
		       bytes_uploaded, total_bytes, chunk_size, temp_file_path, status,
		       created_at, updated_at
		FROM upload_sessions
		WHERE status IN ('IN_PROGRESS', 'PAUSED')
		ORDER BY updated_at DESC
	`

	rows, err := sm.db.Query(query)
	if err != nil {
		sm.logger.Error("Failed to get active upload sessions", "database")
		return nil, err
	}
	defer rows.Close()

	var sessions []*UploadSession

	for rows.Next() {
		var session UploadSession
		err := rows.Scan(
			&session.ID,
			&session.ServiceID,
			&session.SessionID,
			&session.Filename,
			&session.ChunkIndex,
			&session.TotalChunks,
			&session.BytesUploaded,
			&session.TotalBytes,
			&session.ChunkSize,
			&session.TempFilePath,
			&session.Status,
			&session.CreatedAt,
			&session.UpdatedAt,
		)

		if err != nil {
			sm.logger.Error("Failed to scan upload session", "database")
			continue
		}

		sessions = append(sessions, &session)
	}

	return sessions, nil
}
