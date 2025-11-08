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

	nodes, err := s.dbManager.GetWorkflowNodes(workflowID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow nodes: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow nodes", http.StatusInternalServerError)
		return
	}

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
