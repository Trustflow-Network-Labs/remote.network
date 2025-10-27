package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// OfferedService represents a service offered by this node (stored in database)
type OfferedService struct {
	ID              int64                  `json:"id"`
	ServiceType     string                 `json:"service_type"`     // "DATA", "DOCKER", "STANDALONE"
	Type            string                 `json:"type"`             // Legacy: "storage", "docker", "standalone", "relay"
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	Endpoint        string                 `json:"endpoint"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	Status          string                 `json:"status"`           // "ACTIVE", "INACTIVE"
	PricingAmount   float64                `json:"pricing_amount"`
	PricingType     string                 `json:"pricing_type"`     // "ONE_TIME", "RECURRING"
	PricingInterval int                    `json:"pricing_interval"` // number of units
	PricingUnit     string                 `json:"pricing_unit"`     // "SECONDS", "MINUTES", "HOURS", "DAYS", "WEEKS", "MONTHS", "YEARS"
	Pricing         float64                `json:"pricing"`          // Legacy field for backwards compatibility
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// InitServicesTable creates the services table if it doesn't exist
func (sm *SQLiteManager) InitServicesTable() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS services (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_type TEXT CHECK(service_type IN ('DATA', 'DOCKER', 'STANDALONE')) DEFAULT 'DATA',
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT,
		endpoint TEXT,
		capabilities TEXT,
		status TEXT CHECK(status IN ('ACTIVE', 'INACTIVE')) NOT NULL DEFAULT 'ACTIVE',
		pricing_amount REAL DEFAULT 0.0,
		pricing_type TEXT CHECK(pricing_type IN ('ONE_TIME', 'RECURRING')) DEFAULT 'ONE_TIME',
		pricing_interval INTEGER DEFAULT 1,
		pricing_unit TEXT CHECK(pricing_unit IN ('SECONDS', 'MINUTES', 'HOURS', 'DAYS', 'WEEKS', 'MONTHS', 'YEARS')) DEFAULT 'MONTHS',
		pricing REAL DEFAULT 0.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_services_service_type ON services(service_type);
	CREATE INDEX IF NOT EXISTS idx_services_type ON services(type);
	CREATE INDEX IF NOT EXISTS idx_services_status ON services(status);
	CREATE INDEX IF NOT EXISTS idx_services_name ON services(name);

	-- Data service details table
	CREATE TABLE IF NOT EXISTS data_service_details (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id INTEGER NOT NULL,
		file_path TEXT NOT NULL,
		encrypted_path TEXT,
		hash TEXT NOT NULL,
		compression_type TEXT DEFAULT 'tar.gz',
		encryption_key_id INTEGER,
		size_bytes INTEGER NOT NULL,
		original_size_bytes INTEGER NOT NULL,
		upload_completed BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE,
		FOREIGN KEY(encryption_key_id) REFERENCES encryption_keys(id) ON DELETE SET NULL
	);

	CREATE INDEX IF NOT EXISTS idx_data_service_details_service_id ON data_service_details(service_id);
	CREATE INDEX IF NOT EXISTS idx_data_service_details_hash ON data_service_details(hash);

	-- Encryption keys table
	CREATE TABLE IF NOT EXISTS encryption_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id INTEGER NOT NULL,
		passphrase_hash TEXT NOT NULL,
		key_data TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_encryption_keys_service_id ON encryption_keys(service_id);

	-- Upload sessions table
	CREATE TABLE IF NOT EXISTS upload_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id INTEGER NOT NULL,
		session_id TEXT NOT NULL UNIQUE,
		upload_group_id TEXT NOT NULL,
		filename TEXT NOT NULL,
		file_path TEXT NOT NULL,
		file_index INTEGER NOT NULL,
		total_files INTEGER NOT NULL,
		chunk_index INTEGER DEFAULT 0,
		total_chunks INTEGER NOT NULL,
		bytes_uploaded INTEGER DEFAULT 0,
		total_bytes INTEGER NOT NULL,
		chunk_size INTEGER NOT NULL,
		temp_file_path TEXT NOT NULL,
		status TEXT CHECK(status IN ('IN_PROGRESS', 'PAUSED', 'COMPLETED', 'FAILED')) DEFAULT 'IN_PROGRESS',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_upload_sessions_session_id ON upload_sessions(session_id);
	CREATE INDEX IF NOT EXISTS idx_upload_sessions_service_id ON upload_sessions(service_id);
	CREATE INDEX IF NOT EXISTS idx_upload_sessions_upload_group_id ON upload_sessions(upload_group_id);
	CREATE INDEX IF NOT EXISTS idx_upload_sessions_status ON upload_sessions(status);

	-- Placeholder tables for future service types
	CREATE TABLE IF NOT EXISTS docker_service_details (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id INTEGER NOT NULL,
		image_name TEXT NOT NULL,
		image_tag TEXT,
		dockerfile_path TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS standalone_service_details (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id INTEGER NOT NULL,
		executable_path TEXT NOT NULL,
		runtime TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
	);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error("Failed to create services table", "database")
		return err
	}

	sm.logger.Info("Services tables initialized", "database")
	return nil
}

// AddService adds a new service to the database
func (sm *SQLiteManager) AddService(service *OfferedService) error {
	capabilitiesJSON, err := json.Marshal(service.Capabilities)
	if err != nil {
		return err
	}

	// Set defaults if not provided
	if service.ServiceType == "" {
		service.ServiceType = "DATA"
	}
	if service.Status == "" {
		service.Status = "ACTIVE"
	}
	if service.PricingType == "" {
		service.PricingType = "ONE_TIME"
	}
	if service.PricingInterval == 0 {
		service.PricingInterval = 1
	}
	if service.PricingUnit == "" {
		service.PricingUnit = "MONTHS"
	}

	query := `
		INSERT INTO services (
			service_type, type, name, description, endpoint, capabilities, status,
			pricing_amount, pricing_type, pricing_interval, pricing_unit, pricing
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		service.ServiceType,
		service.Type,
		service.Name,
		service.Description,
		service.Endpoint,
		string(capabilitiesJSON),
		service.Status,
		service.PricingAmount,
		service.PricingType,
		service.PricingInterval,
		service.PricingUnit,
		service.Pricing,
	)

	if err != nil {
		sm.logger.Error("Failed to add service", "database")
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	service.ID = id
	service.CreatedAt = time.Now()
	service.UpdatedAt = time.Now()

	sm.logger.Info("Service added successfully", "database")
	return nil
}

// GetService retrieves a service by ID
func (sm *SQLiteManager) GetService(id int64) (*OfferedService, error) {
	query := `
		SELECT id, service_type, type, name, description, endpoint, capabilities, status,
		       pricing_amount, pricing_type, pricing_interval, pricing_unit, pricing,
		       created_at, updated_at
		FROM services
		WHERE id = ?
	`

	var service OfferedService
	var capabilitiesJSON string

	err := sm.db.QueryRow(query, id).Scan(
		&service.ID,
		&service.ServiceType,
		&service.Type,
		&service.Name,
		&service.Description,
		&service.Endpoint,
		&capabilitiesJSON,
		&service.Status,
		&service.PricingAmount,
		&service.PricingType,
		&service.PricingInterval,
		&service.PricingUnit,
		&service.Pricing,
		&service.CreatedAt,
		&service.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		sm.logger.Error("Failed to get service", "database")
		return nil, err
	}

	// Parse capabilities JSON
	if capabilitiesJSON != "" {
		err = json.Unmarshal([]byte(capabilitiesJSON), &service.Capabilities)
		if err != nil {
			sm.logger.Error("Failed to parse service capabilities", "database")
			return nil, err
		}
	}

	return &service, nil
}

// GetAllServices retrieves all services
func (sm *SQLiteManager) GetAllServices() ([]*OfferedService, error) {
	query := `
		SELECT id, service_type, type, name, description, endpoint, capabilities, status,
		       pricing_amount, pricing_type, pricing_interval, pricing_unit, pricing,
		       created_at, updated_at
		FROM services
		ORDER BY created_at DESC
	`

	rows, err := sm.db.Query(query)
	if err != nil {
		sm.logger.Error("Failed to get services", "database")
		return nil, err
	}
	defer rows.Close()

	var services []*OfferedService

	for rows.Next() {
		var service OfferedService
		var capabilitiesJSON string

		err := rows.Scan(
			&service.ID,
			&service.ServiceType,
			&service.Type,
			&service.Name,
			&service.Description,
			&service.Endpoint,
			&capabilitiesJSON,
			&service.Status,
			&service.PricingAmount,
			&service.PricingType,
			&service.PricingInterval,
			&service.PricingUnit,
			&service.Pricing,
			&service.CreatedAt,
			&service.UpdatedAt,
		)

		if err != nil {
			sm.logger.Error("Failed to scan service", "database")
			continue
		}

		// Parse capabilities JSON
		if capabilitiesJSON != "" {
			err = json.Unmarshal([]byte(capabilitiesJSON), &service.Capabilities)
			if err != nil {
				sm.logger.Error("Failed to parse service capabilities", "database")
				continue
			}
		}

		services = append(services, &service)
	}

	return services, nil
}

// UpdateService updates an existing service
func (sm *SQLiteManager) UpdateService(service *OfferedService) error {
	capabilitiesJSON, err := json.Marshal(service.Capabilities)
	if err != nil {
		return err
	}

	query := `
		UPDATE services
		SET service_type = ?, type = ?, name = ?, description = ?, endpoint = ?, capabilities = ?,
		    status = ?, pricing_amount = ?, pricing_type = ?, pricing_interval = ?, pricing_unit = ?,
		    pricing = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := sm.db.Exec(
		query,
		service.ServiceType,
		service.Type,
		service.Name,
		service.Description,
		service.Endpoint,
		string(capabilitiesJSON),
		service.Status,
		service.PricingAmount,
		service.PricingType,
		service.PricingInterval,
		service.PricingUnit,
		service.Pricing,
		service.ID,
	)

	if err != nil {
		sm.logger.Error("Failed to update service", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	service.UpdatedAt = time.Now()
	sm.logger.Info("Service updated successfully", "database")
	return nil
}

// DeleteService deletes a service by ID and its associated files
func (sm *SQLiteManager) DeleteService(id int64) error {
	// First, get the data service details to find encrypted file path
	dataDetails, err := sm.GetDataServiceDetails(id)
	if err != nil && err != sql.ErrNoRows {
		sm.logger.Error(fmt.Sprintf("Failed to get data service details for deletion: %v", err), "database")
		// Continue with deletion even if we can't get details
	}

	// Delete physical encrypted file if it exists
	if dataDetails != nil && dataDetails.EncryptedPath != "" {
		if err := os.Remove(dataDetails.EncryptedPath); err != nil && !os.IsNotExist(err) {
			sm.logger.Warn(fmt.Sprintf("Failed to delete encrypted file %s: %v", dataDetails.EncryptedPath, err), "database")
			// Log warning but don't fail the deletion
		} else if err == nil {
			sm.logger.Info(fmt.Sprintf("Deleted encrypted file: %s", dataDetails.EncryptedPath), "database")
		}
	}

	// Delete from database (foreign keys will cascade)
	query := `DELETE FROM services WHERE id = ?`

	result, err := sm.db.Exec(query, id)
	if err != nil {
		sm.logger.Error("Failed to delete service from database", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	sm.logger.Info(fmt.Sprintf("Service %d deleted successfully (including physical files)", id), "database")
	return nil
}

// UpdateServiceStatus updates the status of a service
func (sm *SQLiteManager) UpdateServiceStatus(id int64, status string) error {
	query := `UPDATE services SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`

	result, err := sm.db.Exec(query, status, id)
	if err != nil {
		sm.logger.Error("Failed to update service status", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	sm.logger.Info("Service status updated successfully", "database")
	return nil
}
