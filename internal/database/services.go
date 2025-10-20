package database

import (
	"database/sql"
	"encoding/json"
	"time"
)

// OfferedService represents a service offered by this node (stored in database)
type OfferedService struct {
	ID           int64                  `json:"id"`
	Type         string                 `json:"type"`          // "storage", "docker", "standalone", "relay"
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Endpoint     string                 `json:"endpoint"`
	Capabilities map[string]interface{} `json:"capabilities"`
	Status       string                 `json:"status"`        // "available", "busy", "offline"
	Pricing      float64                `json:"pricing"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// InitServicesTable creates the services table if it doesn't exist
func (sm *SQLiteManager) InitServicesTable() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS services (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT,
		endpoint TEXT NOT NULL,
		capabilities TEXT,
		status TEXT NOT NULL DEFAULT 'available',
		pricing REAL DEFAULT 0.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_services_type ON services(type);
	CREATE INDEX IF NOT EXISTS idx_services_status ON services(status);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error("Failed to create services table", "database")
		return err
	}

	sm.logger.Info("Services table initialized", "database")
	return nil
}

// AddService adds a new service to the database
func (sm *SQLiteManager) AddService(service *OfferedService) error {
	capabilitiesJSON, err := json.Marshal(service.Capabilities)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO services (type, name, description, endpoint, capabilities, status, pricing)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	result, err := sm.db.Exec(
		query,
		service.Type,
		service.Name,
		service.Description,
		service.Endpoint,
		string(capabilitiesJSON),
		service.Status,
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
		SELECT id, type, name, description, endpoint, capabilities, status, pricing, created_at, updated_at
		FROM services
		WHERE id = ?
	`

	var service OfferedService
	var capabilitiesJSON string

	err := sm.db.QueryRow(query, id).Scan(
		&service.ID,
		&service.Type,
		&service.Name,
		&service.Description,
		&service.Endpoint,
		&capabilitiesJSON,
		&service.Status,
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
		SELECT id, type, name, description, endpoint, capabilities, status, pricing, created_at, updated_at
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
			&service.Type,
			&service.Name,
			&service.Description,
			&service.Endpoint,
			&capabilitiesJSON,
			&service.Status,
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
		SET type = ?, name = ?, description = ?, endpoint = ?, capabilities = ?,
		    status = ?, pricing = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := sm.db.Exec(
		query,
		service.Type,
		service.Name,
		service.Description,
		service.Endpoint,
		string(capabilitiesJSON),
		service.Status,
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

// DeleteService deletes a service by ID
func (sm *SQLiteManager) DeleteService(id int64) error {
	query := `DELETE FROM services WHERE id = ?`

	result, err := sm.db.Exec(query, id)
	if err != nil {
		sm.logger.Error("Failed to delete service", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	sm.logger.Info("Service deleted successfully", "database")
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
