package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// WorkflowNode represents a service/node in a workflow design
type WorkflowNode struct {
	ID             int64                  `json:"id"`
	WorkflowID     int64                  `json:"workflow_id"`
	ServiceID      int64                  `json:"service_id"`
	PeerID         string                 `json:"peer_id"`       // App Peer ID that provides the service
	ServiceName    string                 `json:"service_name"`
	ServiceType    string                 `json:"service_type"`  // "DATA", "DOCKER", "STANDALONE"
	Order          int                    `json:"order"`         // Execution order
	GUIX           int                    `json:"gui_x"`         // X position in UI
	GUIY           int                    `json:"gui_y"`         // Y position in UI
	InputMapping   map[string]interface{} `json:"input_mapping,omitempty"`  // Input connections
	OutputMapping  map[string]interface{} `json:"output_mapping,omitempty"` // Output connections
	PricingAmount  float64                `json:"pricing_amount,omitempty"`
	PricingType    string                 `json:"pricing_type,omitempty"`     // "ONE_TIME", "RECURRING"
	PricingInterval int                   `json:"pricing_interval,omitempty"` // number of units
	PricingUnit    string                 `json:"pricing_unit,omitempty"`     // "SECONDS", "MINUTES", "HOURS", "DAYS", "WEEKS", "MONTHS", "YEARS"
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// WorkflowUIState represents UI-specific preferences for a workflow
type WorkflowUIState struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	SnapToGrid bool      `json:"snap_to_grid"`
	ZoomLevel  float64   `json:"zoom_level"`  // For future use
	PanX       float64   `json:"pan_x"`       // For future use
	PanY       float64   `json:"pan_y"`       // For future use
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_workflow_nodes_workflow_id ON workflow_nodes(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_nodes_order ON workflow_nodes("order");
	CREATE INDEX IF NOT EXISTS idx_workflow_ui_state_workflow_id ON workflow_ui_state(workflow_id);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("Failed to create workflow_nodes tables: %v", err), "database")
		return err
	}

	sm.logger.Info("Workflow nodes tables initialized successfully", "database")
	return nil
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

	result, err := sm.db.Exec(`
		INSERT INTO workflow_nodes (workflow_id, service_id, peer_id, service_name, service_type, "order", gui_x, gui_y, input_mapping, output_mapping, pricing_amount, pricing_type, pricing_interval, pricing_unit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.WorkflowID, node.ServiceID, node.PeerID, node.ServiceName, node.ServiceType, node.Order, node.GUIX, node.GUIY, inputMappingJSON, outputMappingJSON, node.PricingAmount, node.PricingType, node.PricingInterval, node.PricingUnit)

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
	rows, err := sm.db.Query(`
		SELECT id, workflow_id, service_id, peer_id, service_name, service_type, "order", gui_x, gui_y,
		       input_mapping, output_mapping, pricing_amount, pricing_type, pricing_interval, pricing_unit, created_at, updated_at
		FROM workflow_nodes
		WHERE workflow_id = ?
		ORDER BY "order" ASC
	`, workflowID)

	if err != nil {
		return nil, fmt.Errorf("failed to query workflow nodes: %v", err)
	}
	defer rows.Close()

	var nodes []*WorkflowNode
	for rows.Next() {
		node := &WorkflowNode{}
		var inputMappingStr, outputMappingStr sql.NullString
		var pricingType, pricingUnit sql.NullString
		var pricingInterval sql.NullInt64

		err := rows.Scan(
			&node.ID, &node.WorkflowID, &node.ServiceID, &node.PeerID, &node.ServiceName,
			&node.ServiceType, &node.Order, &node.GUIX, &node.GUIY,
			&inputMappingStr, &outputMappingStr, &node.PricingAmount, &pricingType, &pricingInterval, &pricingUnit,
			&node.CreatedAt, &node.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan workflow node: %v", err)
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

		// Handle nullable pricing fields
		if pricingType.Valid {
			node.PricingType = pricingType.String
		}
		if pricingUnit.Valid {
			node.PricingUnit = pricingUnit.String
		}
		if pricingInterval.Valid {
			node.PricingInterval = int(pricingInterval.Int64)
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
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
	state := &WorkflowUIState{}
	err := sm.db.QueryRow(`
		SELECT id, workflow_id, snap_to_grid, zoom_level, pan_x, pan_y, created_at, updated_at
		FROM workflow_ui_state
		WHERE workflow_id = ?
	`, workflowID).Scan(
		&state.ID, &state.WorkflowID, &state.SnapToGrid, &state.ZoomLevel,
		&state.PanX, &state.PanY, &state.CreatedAt, &state.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// Create default UI state
		return sm.CreateWorkflowUIState(workflowID)
	} else if err != nil {
		return nil, fmt.Errorf("failed to get workflow UI state: %v", err)
	}

	return state, nil
}

// CreateWorkflowUIState creates a new UI state for a workflow
func (sm *SQLiteManager) CreateWorkflowUIState(workflowID int64) (*WorkflowUIState, error) {
	result, err := sm.db.Exec(`
		INSERT INTO workflow_ui_state (workflow_id, snap_to_grid, zoom_level, pan_x, pan_y)
		VALUES (?, 0, 1.0, 0.0, 0.0)
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
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

// UpdateWorkflowUIState updates UI state for a workflow
func (sm *SQLiteManager) UpdateWorkflowUIState(workflowID int64, state *WorkflowUIState) error {
	_, err := sm.db.Exec(`
		UPDATE workflow_ui_state
		SET snap_to_grid = ?, zoom_level = ?, pan_x = ?, pan_y = ?, updated_at = CURRENT_TIMESTAMP
		WHERE workflow_id = ?
	`, state.SnapToGrid, state.ZoomLevel, state.PanX, state.PanY, workflowID)

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
