package api

import (
	"encoding/json"
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Workflow updated successfully",
	})
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

	// Create a workflow job
	job, err := s.dbManager.CreateWorkflowJob(id)
	if err != nil {
		s.logger.Error("Failed to create workflow job", "api")
		http.Error(w, "Failed to create workflow job", http.StatusInternalServerError)
		return
	}

	// TODO: Actually execute the workflow asynchronously
	// For now, we just mark it as completed immediately
	go func() {
		// Simulate workflow execution
		// In a real implementation, this would process the workflow definition
		// and coordinate with peers to execute the workflow steps
		result := map[string]interface{}{
			"workflow_id":   workflow.ID,
			"workflow_name": workflow.Name,
			"status":        "simulated_execution",
			"message":       "Workflow execution not yet implemented",
		}
		s.dbManager.UpdateWorkflowJobStatus(job.ID, "completed", result, "")
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job_id":  job.ID,
		"status":  job.Status,
		"message": "Workflow execution started",
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
