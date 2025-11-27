package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// handleGetWorkflows returns all workflows
func (s *APIServer) handleGetWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workflows, err := s.dbManager.GetAllWorkflows()
	if err != nil {
		s.logger.Error("Failed to get workflows", "api")
		http.Error(w, "Failed to retrieve workflows", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"workflows": workflows,
		"total":     len(workflows),
	})
}

// handleGetWorkflow returns a specific workflow
func (s *APIServer) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	workflow, err := s.dbManager.GetWorkflowByID(id)
	if err != nil {
		if err.Error() == "workflow not found" {
			http.Error(w, "Workflow not found", http.StatusNotFound)
		} else {
			s.logger.Error("Failed to get workflow", "api")
			http.Error(w, "Failed to retrieve workflow", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workflow)
}

// handleCreateWorkflow creates a new workflow
func (s *APIServer) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var workflow database.Workflow
	err := json.NewDecoder(r.Body).Decode(&workflow)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if workflow.Name == "" {
		http.Error(w, "Missing required field: name", http.StatusBadRequest)
		return
	}

	if workflow.Definition == nil {
		workflow.Definition = make(map[string]interface{})
	}

	err = s.dbManager.AddWorkflow(&workflow)
	if err != nil {
		s.logger.Error("Failed to create workflow", "api")
		http.Error(w, "Failed to create workflow", http.StatusInternalServerError)
		return
	}

	// Broadcast workflow update via WebSocket
	s.eventEmitter.BroadcastWorkflowUpdate()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(workflow)
}

// handleUpdateWorkflow updates an existing workflow
func (s *APIServer) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	var workflow database.Workflow
	err = json.NewDecoder(r.Body).Decode(&workflow)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if workflow.Name == "" {
		http.Error(w, "Missing required field: name", http.StatusBadRequest)
		return
	}

	if workflow.Definition == nil {
		workflow.Definition = make(map[string]interface{})
	}

	err = s.dbManager.UpdateWorkflow(id, &workflow)
	if err != nil {
		if err.Error() == "workflow not found" {
			http.Error(w, "Workflow not found", http.StatusNotFound)
		} else {
			s.logger.Error("Failed to update workflow", "api")
			http.Error(w, "Failed to update workflow", http.StatusInternalServerError)
		}
		return
	}

	// Fetch the updated workflow to return to client
	updatedWorkflow, err := s.dbManager.GetWorkflowByID(id)
	if err != nil {
		s.logger.Error("Failed to fetch updated workflow", "api")
		http.Error(w, "Failed to retrieve updated workflow", http.StatusInternalServerError)
		return
	}

	// Broadcast workflow update via WebSocket
	s.eventEmitter.BroadcastWorkflowUpdate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updatedWorkflow)
}

// handleDeleteWorkflow deletes a workflow
func (s *APIServer) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	err = s.dbManager.DeleteWorkflow(id)
	if err != nil {
		if err.Error() == "workflow not found" {
			http.Error(w, "Workflow not found", http.StatusNotFound)
		} else {
			s.logger.Error("Failed to delete workflow", "api")
			http.Error(w, "Failed to delete workflow", http.StatusInternalServerError)
		}
		return
	}

	// Broadcast workflow update via WebSocket
	s.eventEmitter.BroadcastWorkflowUpdate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Workflow deleted successfully",
	})
}

// handleExecuteWorkflow executes a workflow
func (s *APIServer) handleExecuteWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path (/api/workflows/:id/execute)
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "execute" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	// Check if workflow exists
	_, err = s.dbManager.GetWorkflowByID(id)
	if err != nil {
		if err.Error() == "workflow not found" {
			http.Error(w, "Workflow not found", http.StatusNotFound)
		} else {
			s.logger.Error("Failed to get workflow", "api")
			http.Error(w, "Failed to retrieve workflow", http.StatusInternalServerError)
		}
		return
	}

	// Get local peer ID
	localPeerID := s.peerManager.GetPeerID()
	if localPeerID == "" {
		s.logger.Error("Local peer ID not available", "api")
		http.Error(w, "Local peer not initialized", http.StatusServiceUnavailable)
		return
	}

	// Build workflow definition from nodes and connections
	s.logger.Info(fmt.Sprintf("Building workflow definition for workflow %d", id), "api")
	err = s.dbManager.BuildWorkflowDefinition(id, localPeerID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to build workflow definition: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to build workflow definition: %v", err), http.StatusBadRequest)
		return
	}

	// Get WorkflowManager from PeerManager
	workflowManager := s.peerManager.GetWorkflowManager()
	if workflowManager == nil {
		s.logger.Error("Workflow manager not initialized", "api")
		http.Error(w, "Workflow manager not initialized - job system may not be started", http.StatusServiceUnavailable)
		return
	}

	// Execute workflow asynchronously (validation happens inside)
	go func() {
		s.logger.Info(fmt.Sprintf("Starting workflow %d execution asynchronously", id), "api")

		workflowJob, err := workflowManager.ExecuteWorkflow(id)
		if err != nil {
			s.logger.Error(fmt.Sprintf("Workflow %d execution failed: %v", id, err), "api")
			// Error is already logged in workflow execution record
			return
		}

		s.logger.Info(fmt.Sprintf("Workflow %d execution completed with job ID %d", id, workflowJob.ID), "api")
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Workflow execution started - validation and execution in progress",
		"workflow_id": id,
		"note":        "Check workflow status via GET /api/workflows/:id for execution progress",
	})
}

// handleGetWorkflowJobs returns all jobs for a specific workflow
func (s *APIServer) handleGetWorkflowJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path (/api/workflows/:id/jobs)
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "jobs" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	jobs, err := s.dbManager.GetWorkflowJobs(id)
	if err != nil {
		s.logger.Error("Failed to get workflow jobs", "api")
		http.Error(w, "Failed to retrieve workflow jobs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// handleGetWorkflowExecutions returns all job executions for a workflow
func (s *APIServer) handleGetWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path (/api/workflows/:id/executions)
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "executions" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	executions, err := s.dbManager.GetJobExecutionsByWorkflowID(id)
	if err != nil {
		s.logger.Error("Failed to get workflow executions", "api")
		http.Error(w, "Failed to retrieve workflow executions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"executions": executions,
		"total":      len(executions),
	})
}

// handleGetJobExecutionInterfaces returns all interfaces for a job execution
// GET /api/job-executions/:id/interfaces
func (s *APIServer) handleGetJobExecutionInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract job execution ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "interfaces" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid job execution ID", http.StatusBadRequest)
		return
	}

	interfaces, err := s.dbManager.GetJobInterfaces(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get job interfaces: %v", err), "api")
		http.Error(w, "Failed to retrieve job interfaces", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"interfaces": interfaces,
		"total":      len(interfaces),
	})
}

// handleGetWorkflowExecutionInstances returns all execution instances for a workflow
// GET /api/workflows/:id/execution-instances
func (s *APIServer) handleGetWorkflowExecutionInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract workflow ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "execution-instances" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	workflowID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid workflow ID", http.StatusBadRequest)
		return
	}

	executions, err := s.dbManager.GetWorkflowExecutionsByWorkflowID(workflowID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow executions: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow executions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"executions": executions,
		"total":      len(executions),
	})
}

// handleGetWorkflowExecutionJobs returns all jobs for a specific workflow execution
// GET /api/workflow-executions/:execution_id/jobs
func (s *APIServer) handleGetWorkflowExecutionJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract execution ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "jobs" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	executionID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid execution ID", http.StatusBadRequest)
		return
	}

	jobs, err := s.dbManager.GetWorkflowJobsByExecution(executionID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow jobs: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow jobs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// handleGetWorkflowExecutionStatus returns the status of a workflow execution
// GET /api/workflow-executions/:execution_id/status
func (s *APIServer) handleGetWorkflowExecutionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract execution ID from path
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[3] != "status" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	executionID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid execution ID", http.StatusBadRequest)
		return
	}

	// Get workflow execution
	execution, err := s.dbManager.GetWorkflowExecutionByID(executionID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow execution: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow execution", http.StatusInternalServerError)
		return
	}

	// Get jobs for this execution
	jobs, err := s.dbManager.GetWorkflowJobsByExecution(executionID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get workflow jobs: %v", err), "api")
		http.Error(w, "Failed to retrieve workflow jobs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"execution": execution,
		"jobs":      jobs,
		"total":     len(jobs),
	})
}
