package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// WorkflowNode represents a service/node in a workflow design
type WorkflowNode struct {
	ID              int64                  `json:"id"`
	WorkflowID      int64                  `json:"workflow_id"`
	ServiceID       int64                  `json:"service_id"`
	PeerID          string                 `json:"peer_id"` // App Peer ID that provides the service
	ServiceName     string                 `json:"service_name"`
	ServiceType     string                 `json:"service_type"`             // "DATA", "DOCKER", "STANDALONE"
	Order           int                    `json:"order"`                    // Execution order
	GUIX            int                    `json:"gui_x"`                    // X position in UI
	GUIY            int                    `json:"gui_y"`                    // Y position in UI
	InputMapping    map[string]interface{} `json:"input_mapping,omitempty"`  // Input connections
	OutputMapping   map[string]interface{} `json:"output_mapping,omitempty"` // Output connections
	Interfaces      []interface{}          `json:"interfaces,omitempty"`     // Service interfaces from remote service search
	Entrypoint      []string               `json:"entrypoint,omitempty"`     // Docker entrypoint from remote service (for DOCKER services)
	Cmd             []string               `json:"cmd,omitempty"`            // Docker cmd from remote service (for DOCKER services)
	PricingAmount   float64                `json:"pricing_amount,omitempty"`
	PricingType     string                 `json:"pricing_type,omitempty"`     // "ONE_TIME", "RECURRING"
	PricingInterval int                    `json:"pricing_interval,omitempty"` // number of units
	PricingUnit     string                 `json:"pricing_unit,omitempty"`     // "SECONDS", "MINUTES", "HOURS", "DAYS", "WEEKS", "MONTHS", "YEARS"
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// WorkflowUIState represents UI-specific preferences for a workflow
type WorkflowUIState struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	SnapToGrid bool      `json:"snap_to_grid"`
	ZoomLevel  float64   `json:"zoom_level"`  // For future use
	PanX       float64   `json:"pan_x"`       // For future use
	PanY       float64   `json:"pan_y"`       // For future use
	SelfPeerX  float64   `json:"self_peer_x"` // Self-peer card X position
	SelfPeerY  float64   `json:"self_peer_y"` // Self-peer card Y position
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WorkflowConnection represents a connection between workflow nodes
type WorkflowConnection struct {
	ID                  int64     `json:"id"`
	WorkflowID          int64     `json:"workflow_id"`
	FromNodeID          *int64    `json:"from_node_id"`                    // Source node ID (null if self-peer)
	FromInterfaceType   string    `json:"from_interface_type"`             // STDIN, STDOUT, MOUNT
	ToNodeID            *int64    `json:"to_node_id"`                      // Destination node ID (null if self-peer)
	ToInterfaceType     string    `json:"to_interface_type"`               // STDIN, STDOUT, MOUNT
	DestinationFileName *string   `json:"destination_file_name,omitempty"` // Optional: rename file/folder at destination
	CreatedAt           time.Time `json:"created_at"`
}

// InitWorkflowNodesTable creates the workflow_nodes and workflow_ui_state tables
func (sm *SQLiteManager) InitWorkflowNodesTable() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS workflow_nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_id INTEGER NOT NULL,
		service_id INTEGER NOT NULL,
		peer_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		service_type TEXT NOT NULL,
		"order" INTEGER NOT NULL DEFAULT 0,
		gui_x INTEGER NOT NULL DEFAULT 0,
		gui_y INTEGER NOT NULL DEFAULT 0,
		input_mapping TEXT,
		output_mapping TEXT,
		interfaces TEXT,
		entrypoint TEXT,
		cmd TEXT,
		pricing_amount REAL DEFAULT 0.0,
		pricing_type TEXT,
		pricing_interval INTEGER DEFAULT 1,
		pricing_unit TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS workflow_ui_state (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_id INTEGER NOT NULL UNIQUE,
		snap_to_grid BOOLEAN NOT NULL DEFAULT 0,
		zoom_level REAL NOT NULL DEFAULT 1.0,
		pan_x REAL NOT NULL DEFAULT 0.0,
		pan_y REAL NOT NULL DEFAULT 0.0,
		self_peer_x REAL NOT NULL DEFAULT 50.0,
		self_peer_y REAL NOT NULL DEFAULT 50.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS workflow_connections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workflow_id INTEGER NOT NULL,
		from_node_id INTEGER,
		from_interface_type TEXT CHECK(from_interface_type IN ('STDIN', 'STDOUT', 'STDERR', 'LOGS', 'MOUNT')) NOT NULL,
		to_node_id INTEGER,
		to_interface_type TEXT CHECK(to_interface_type IN ('STDIN', 'STDOUT', 'STDERR', 'LOGS', 'MOUNT')) NOT NULL,
		destination_file_name TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE,
		FOREIGN KEY (from_node_id) REFERENCES workflow_nodes(id) ON DELETE CASCADE,
		FOREIGN KEY (to_node_id) REFERENCES workflow_nodes(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_workflow_nodes_workflow_id ON workflow_nodes(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_nodes_order ON workflow_nodes("order");
	CREATE INDEX IF NOT EXISTS idx_workflow_ui_state_workflow_id ON workflow_ui_state(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_connections_workflow_id ON workflow_connections(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_connections_from_node_id ON workflow_connections(from_node_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_connections_to_node_id ON workflow_connections(to_node_id);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create workflow_nodes tables: %v", err), "database")
		return err
	}

	// Migration: Add new columns to existing tables
	migrations := []string{
		`ALTER TABLE workflow_nodes ADD COLUMN entrypoint TEXT`,
		`ALTER TABLE workflow_nodes ADD COLUMN cmd TEXT`,
		`ALTER TABLE workflow_connections ADD COLUMN destination_file_name TEXT`,
	}

	for _, migration := range migrations {
		_, err := sm.db.Exec(migration)
		if err != nil {
			// Ignore "duplicate column" errors - column already exists
			if !isDuplicateColumnError(err) {
				sm.logger.Warn(fmt.Sprintf("Migration warning (non-fatal): %v", err), "database")
			}
		}
	}

	sm.logger.Info("Workflow nodes tables initialized successfully", "database")
	return nil
}

// isDuplicateColumnError checks if the error is a duplicate column error
func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return len(errStr) > 0 && (contains(errStr, "duplicate column") || contains(errStr, "already exists"))
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c1, c2 := s[i+j], substr[j]
			// Case-insensitive comparison
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// AddWorkflowNode adds a new node to a workflow
func (sm *SQLiteManager) AddWorkflowNode(node *WorkflowNode) error {
	inputMappingJSON := []byte("{}")
	if node.InputMapping != nil {
		var err error
		inputMappingJSON, err = json.Marshal(node.InputMapping)
		if err != nil {
			return fmt.Errorf("failed to marshal input mapping: %v", err)
		}
	}

	outputMappingJSON := []byte("{}")
	if node.OutputMapping != nil {
		var err error
		outputMappingJSON, err = json.Marshal(node.OutputMapping)
		if err != nil {
			return fmt.Errorf("failed to marshal output mapping: %v", err)
		}
	}

	interfacesJSON := []byte("[]")
	if node.Interfaces != nil {
		var err error
		interfacesJSON, err = json.Marshal(node.Interfaces)
		if err != nil {
			return fmt.Errorf("failed to marshal interfaces: %v", err)
		}
	}

	// Marshal entrypoint and cmd as JSON arrays
	var entrypointJSON, cmdJSON []byte
	if node.Entrypoint != nil {
		entrypointJSON, _ = json.Marshal(node.Entrypoint)
	}
	if node.Cmd != nil {
		cmdJSON, _ = json.Marshal(node.Cmd)
	}

	result, err := sm.db.Exec(`
		INSERT INTO workflow_nodes (workflow_id, service_id, peer_id, service_name, service_type, "order", gui_x, gui_y, input_mapping, output_mapping, interfaces, entrypoint, cmd, pricing_amount, pricing_type, pricing_interval, pricing_unit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.WorkflowID, node.ServiceID, node.PeerID, node.ServiceName, node.ServiceType, node.Order, node.GUIX, node.GUIY, inputMappingJSON, outputMappingJSON, interfacesJSON, entrypointJSON, cmdJSON, node.PricingAmount, node.PricingType, node.PricingInterval, node.PricingUnit)

	if err != nil {
		return fmt.Errorf("failed to add workflow node: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %v", err)
	}

	node.ID = id
	node.CreatedAt = time.Now()
	node.UpdatedAt = time.Now()

	return nil
}

// GetWorkflowNodes retrieves all nodes for a workflow
func (sm *SQLiteManager) GetWorkflowNodes(workflowID int64) ([]*WorkflowNode, error) {
	query := `SELECT id, workflow_id, service_id, peer_id, service_name, service_type, "order", gui_x, gui_y,
		       input_mapping, output_mapping, interfaces, entrypoint, cmd, pricing_amount, pricing_type, pricing_interval, pricing_unit, created_at, updated_at
		FROM workflow_nodes
		WHERE workflow_id = ?
		ORDER BY "order" ASC`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*WorkflowNode, error) {
			node := &WorkflowNode{}
			var inputMappingStr, outputMappingStr, interfacesStr, entrypointStr, cmdStr sql.NullString
			var pricingType, pricingUnit sql.NullString
			var pricingInterval sql.NullInt64

			err := rows.Scan(
				&node.ID, &node.WorkflowID, &node.ServiceID, &node.PeerID, &node.ServiceName,
				&node.ServiceType, &node.Order, &node.GUIX, &node.GUIY,
				&inputMappingStr, &outputMappingStr, &interfacesStr, &entrypointStr, &cmdStr, &node.PricingAmount, &pricingType, &pricingInterval, &pricingUnit,
				&node.CreatedAt, &node.UpdatedAt,
			)
			if err != nil {
				return nil, err
			}

			// Parse JSON mappings
			if inputMappingStr.Valid {
				if err := json.Unmarshal([]byte(inputMappingStr.String), &node.InputMapping); err != nil {
					node.InputMapping = make(map[string]interface{})
				}
			}
			if outputMappingStr.Valid {
				if err := json.Unmarshal([]byte(outputMappingStr.String), &node.OutputMapping); err != nil {
					node.OutputMapping = make(map[string]interface{})
				}
			}
			if interfacesStr.Valid {
				if err := json.Unmarshal([]byte(interfacesStr.String), &node.Interfaces); err != nil {
					node.Interfaces = make([]interface{}, 0)
				}
			} else {
				node.Interfaces = make([]interface{}, 0)
			}

			// Parse entrypoint and cmd JSON arrays
			if entrypointStr.Valid {
				if err := json.Unmarshal([]byte(entrypointStr.String), &node.Entrypoint); err != nil {
					node.Entrypoint = nil
				}
			}
			if cmdStr.Valid {
				if err := json.Unmarshal([]byte(cmdStr.String), &node.Cmd); err != nil {
					node.Cmd = nil
				}
			}

			// Handle nullable pricing fields
			node.PricingType = ScanNullableString(pricingType)
			node.PricingUnit = ScanNullableString(pricingUnit)
			if pricingInterval.Valid {
				node.PricingInterval = int(pricingInterval.Int64)
			}

			return node, nil
		},
		sm.logger, "database", workflowID)
}

// UpdateWorkflowNodeGUIProps updates the GUI position of a workflow node
func (sm *SQLiteManager) UpdateWorkflowNodeGUIProps(nodeID int64, x, y int) error {
	_, err := sm.db.Exec(`
		UPDATE workflow_nodes
		SET gui_x = ?, gui_y = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, x, y, nodeID)

	if err != nil {
		return fmt.Errorf("failed to update workflow node GUI props: %v", err)
	}

	return nil
}

// UpdateWorkflowNodeConfig updates the entrypoint and cmd of a workflow node
func (sm *SQLiteManager) UpdateWorkflowNodeConfig(nodeID int64, entrypoint, cmd []string) error {
	// Marshal entrypoint and cmd as JSON arrays
	var entrypointJSON, cmdJSON []byte
	var err error

	if len(entrypoint) > 0 {
		entrypointJSON, err = json.Marshal(entrypoint)
		if err != nil {
			return fmt.Errorf("failed to marshal entrypoint: %v", err)
		}
	}

	if len(cmd) > 0 {
		cmdJSON, err = json.Marshal(cmd)
		if err != nil {
			return fmt.Errorf("failed to marshal cmd: %v", err)
		}
	}

	_, err = sm.db.Exec(`
		UPDATE workflow_nodes
		SET entrypoint = ?, cmd = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, entrypointJSON, cmdJSON, nodeID)

	if err != nil {
		return fmt.Errorf("failed to update workflow node config: %v", err)
	}

	sm.logger.Info(fmt.Sprintf("Workflow node config updated: node_id=%d", nodeID), "database")
	return nil
}

// DeleteWorkflowNode removes a node from a workflow
func (sm *SQLiteManager) DeleteWorkflowNode(nodeID int64) error {
	_, err := sm.db.Exec("DELETE FROM workflow_nodes WHERE id = ?", nodeID)
	if err != nil {
		return fmt.Errorf("failed to delete workflow node: %v", err)
	}
	return nil
}

// GetWorkflowUIState retrieves UI state for a workflow
func (sm *SQLiteManager) GetWorkflowUIState(workflowID int64) (*WorkflowUIState, error) {
	query := `SELECT id, workflow_id, snap_to_grid, zoom_level, pan_x, pan_y, self_peer_x, self_peer_y, created_at, updated_at
		FROM workflow_ui_state
		WHERE workflow_id = ?`

	state, err := QueryRowSingle(sm.db, query,
		func(row *sql.Row) (*WorkflowUIState, error) {
			var s WorkflowUIState
			err := row.Scan(
				&s.ID, &s.WorkflowID, &s.SnapToGrid, &s.ZoomLevel,
				&s.PanX, &s.PanY, &s.SelfPeerX, &s.SelfPeerY, &s.CreatedAt, &s.UpdatedAt,
			)
			return &s, err
		},
		sm.logger, "database", workflowID)

	if err != nil {
		return nil, fmt.Errorf("failed to get workflow UI state: %v", err)
	}

	if state == nil {
		// Create default UI state
		return sm.CreateWorkflowUIState(workflowID)
	}

	return state, nil
}

// CreateWorkflowUIState creates a new UI state for a workflow
func (sm *SQLiteManager) CreateWorkflowUIState(workflowID int64) (*WorkflowUIState, error) {
	result, err := sm.db.Exec(`
		INSERT INTO workflow_ui_state (workflow_id, snap_to_grid, zoom_level, pan_x, pan_y, self_peer_x, self_peer_y)
		VALUES (?, 0, 1.0, 0.0, 0.0, 50.0, 50.0)
	`, workflowID)

	if err != nil {
		return nil, fmt.Errorf("failed to create workflow UI state: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %v", err)
	}

	return &WorkflowUIState{
		ID:         id,
		WorkflowID: workflowID,
		SnapToGrid: false,
		ZoomLevel:  1.0,
		PanX:       0.0,
		PanY:       0.0,
		SelfPeerX:  50.0,
		SelfPeerY:  50.0,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

// UpdateWorkflowUIState updates UI state for a workflow
func (sm *SQLiteManager) UpdateWorkflowUIState(workflowID int64, state *WorkflowUIState) error {
	_, err := sm.db.Exec(`
		UPDATE workflow_ui_state
		SET snap_to_grid = ?, zoom_level = ?, pan_x = ?, pan_y = ?, self_peer_x = ?, self_peer_y = ?, updated_at = CURRENT_TIMESTAMP
		WHERE workflow_id = ?
	`, state.SnapToGrid, state.ZoomLevel, state.PanX, state.PanY, state.SelfPeerX, state.SelfPeerY, workflowID)

	if err != nil {
		// Try to create if doesn't exist
		if err == sql.ErrNoRows {
			_, err = sm.CreateWorkflowUIState(workflowID)
			if err != nil {
				return err
			}
			// Retry update
			return sm.UpdateWorkflowUIState(workflowID, state)
		}
		return fmt.Errorf("failed to update workflow UI state: %v", err)
	}

	return nil
}

// AddWorkflowConnection adds a connection between workflow nodes
func (sm *SQLiteManager) AddWorkflowConnection(conn *WorkflowConnection) error {
	result, err := sm.db.Exec(`
		INSERT INTO workflow_connections (
			workflow_id, from_node_id, from_interface_type, to_node_id, to_interface_type, destination_file_name
		) VALUES (?, ?, ?, ?, ?, ?)
	`, conn.WorkflowID, conn.FromNodeID, conn.FromInterfaceType, conn.ToNodeID, conn.ToInterfaceType, conn.DestinationFileName)

	if err != nil {
		return fmt.Errorf("failed to add workflow connection: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	conn.ID = id
	return nil
}

// GetWorkflowConnections retrieves all connections for a workflow
func (sm *SQLiteManager) GetWorkflowConnections(workflowID int64) ([]*WorkflowConnection, error) {
	query := `SELECT id, workflow_id, from_node_id, from_interface_type, to_node_id, to_interface_type, destination_file_name, created_at
		FROM workflow_connections
		WHERE workflow_id = ?`

	return QueryRows(sm.db, query,
		func(rows *sql.Rows) (*WorkflowConnection, error) {
			var conn WorkflowConnection
			var destinationFileName sql.NullString
			err := rows.Scan(
				&conn.ID,
				&conn.WorkflowID,
				&conn.FromNodeID,
				&conn.FromInterfaceType,
				&conn.ToNodeID,
				&conn.ToInterfaceType,
				&destinationFileName,
				&conn.CreatedAt,
			)
			if err != nil {
				return nil, err
			}

			if destinationFileName.Valid {
				conn.DestinationFileName = &destinationFileName.String
			}

			return &conn, nil
		},
		sm.logger, "database", workflowID)
}

// DeleteWorkflowConnection deletes a connection by ID
func (sm *SQLiteManager) DeleteWorkflowConnection(id int64) error {
	_, err := sm.db.Exec(`DELETE FROM workflow_connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete workflow connection: %v", err)
	}
	return nil
}

// UpdateWorkflowConnection updates a connection's destination_file_name
func (sm *SQLiteManager) UpdateWorkflowConnection(id int64, destinationFileName *string) error {
	_, err := sm.db.Exec(`
		UPDATE workflow_connections
		SET destination_file_name = ?
		WHERE id = ?
	`, destinationFileName, id)
	if err != nil {
		return fmt.Errorf("failed to update workflow connection: %v", err)
	}
	return nil
}

// DeleteAllWorkflowConnections deletes all connections for a workflow
func (sm *SQLiteManager) DeleteAllWorkflowConnections(workflowID int64) error {
	_, err := sm.db.Exec(`DELETE FROM workflow_connections WHERE workflow_id = ?`, workflowID)
	if err != nil {
		return fmt.Errorf("failed to delete workflow connections: %v", err)
	}
	return nil
}
