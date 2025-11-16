package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// handleAddWorkflowNode adds a new node to a workflow
// POST /api/workflows/:id/nodes
func (s *APIServer) handleAddWorkflowNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	var node database.WorkflowNode
	err = json.NewDecoder(r.Body).Decode(&node)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Set workflow ID from path
	node.WorkflowID = workflowID

	// Validate required fields
	if node.ServiceID == 0 || node.ServiceName == "" || node.ServiceType == "" {
		http.Error(w, "Missing required fields: service_id, service_name, service_type", http.StatusBadRequest)
		return
	}

	err = s.dbManager.AddWorkflowNode(&node)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to add workflow node: %v", err), "api")
		http.Error(w, "Failed to add workflow node", http.StatusInternalServerError)
		return
	}

	// Broadcast workflow update via WebSocket
	s.eventEmitter.BroadcastWorkflowUpdate()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"node": node,
	})
}

// handleGetWorkflowNodes gets all nodes for a workflow
// GET /api/workflows/:id/nodes
func (s *APIServer) handleGetWorkflowNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	s.logger.Debug(fmt.Sprintf("Getting workflow nodes for workflow_id=%d", workflowID), "api")

	// Get workflow nodes from the table (now includes interfaces)
	// The workflow definition JSON is only built during execution via BuildWorkflowDefinition
	// During editing, nodes are stored in workflow_nodes table
	nodes, err := s.dbManager.GetWorkflowNodes(workflowID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow nodes: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow nodes", http.StatusInternalServerError)
		return
	}

	s.logger.Debug(fmt.Sprintf("Found %d workflow nodes for workflow_id=%d", len(nodes), workflowID), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes": nodes,
		"total": len(nodes),
	})
}

// handleUpdateWorkflowNodeGUIProps updates node position
// PUT /api/workflows/:id/nodes/:nodeId/gui-props
func (s *APIServer) handleUpdateWorkflowNodeGUIProps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract IDs from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 6 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	nodeID, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		http.Error(w, "Invalid node ID", http.StatusBadRequest)
		return
	}

	var props struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	err = json.NewDecoder(r.Body).Decode(&props)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err = s.dbManager.UpdateWorkflowNodeGUIProps(nodeID, props.X, props.Y)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to update node GUI props: %v", err), "api")
		http.Error(w, "Failed to update node position", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleDeleteWorkflowNode deletes a node from a workflow
// DELETE /api/workflows/:id/nodes/:nodeId
func (s *APIServer) handleDeleteWorkflowNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract node ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	nodeID, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		http.Error(w, "Invalid node ID", http.StatusBadRequest)
		return
	}

	err = s.dbManager.DeleteWorkflowNode(nodeID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to delete workflow node: %v", err), "api")
		http.Error(w, "Failed to delete workflow node", http.StatusInternalServerError)
		return
	}

	// Broadcast workflow update via WebSocket
	s.eventEmitter.BroadcastWorkflowUpdate()

	w.WriteHeader(http.StatusNoContent)
}

// handleGetWorkflowUIState gets UI state for a workflow
// GET /api/workflows/:id/ui-state
func (s *APIServer) handleGetWorkflowUIState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	state, err := s.dbManager.GetWorkflowUIState(workflowID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow UI state: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow UI state", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// handleUpdateWorkflowUIState updates UI state for a workflow
// PUT /api/workflows/:id/ui-state
func (s *APIServer) handleUpdateWorkflowUIState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	var state database.WorkflowUIState
	err = json.NewDecoder(r.Body).Decode(&state)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err = s.dbManager.UpdateWorkflowUIState(workflowID, &state)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to update workflow UI state: %v", err), "api")
		http.Error(w, "Failed to update workflow UI state", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// handleGetWorkflowConnections gets all connections for a workflow
// GET /api/workflows/:id/connections
func (s *APIServer) handleGetWorkflowConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	connections, err := s.dbManager.GetWorkflowConnections(workflowID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow connections: %v", err), "api")
		http.Error(w, "Failed to get workflow connections", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connections": connections,
	})
}

// handleAddWorkflowConnection adds a connection to a workflow
// POST /api/workflows/:id/connections
func (s *APIServer) handleAddWorkflowConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	var conn database.WorkflowConnection
	err = json.NewDecoder(r.Body).Decode(&conn)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	conn.WorkflowID = workflowID

	err = s.dbManager.AddWorkflowConnection(&conn)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to add workflow connection: %v", err), "api")
		http.Error(w, "Failed to add workflow connection", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connection": conn,
	})
}

// handleDeleteWorkflowConnection deletes a connection
// DELETE /api/workflows/:workflow_id/connections/:connection_id
func (s *APIServer) handleDeleteWorkflowConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract IDs from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	connectionID, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		http.Error(w, "Invalid connection ID", http.StatusBadRequest)
		return
	}

	err = s.dbManager.DeleteWorkflowConnection(connectionID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to delete workflow connection: %v", err), "api")
		http.Error(w, "Failed to delete workflow connection", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}
