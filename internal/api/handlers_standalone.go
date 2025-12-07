package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// Request types for Standalone service creation

// CreateStandaloneFromLocalRequest represents a request to create a standalone service from a local executable
type CreateStandaloneFromLocalRequest struct {
	ServiceName          string                       `json:"service_name"`
	ExecutablePath       string                       `json:"executable_path"`
	Arguments            []string                     `json:"arguments,omitempty"`
	WorkingDirectory     string                       `json:"working_directory,omitempty"`
	EnvironmentVariables map[string]string            `json:"environment_variables,omitempty"`
	TimeoutSeconds       int                          `json:"timeout_seconds,omitempty"`
	RunAsUser            string                       `json:"run_as_user,omitempty"`
	Description          string                       `json:"description"`
	Capabilities         map[string]interface{}       `json:"capabilities,omitempty"`
	CustomInterfaces     []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
}

// CreateStandaloneFromGitRequest represents a request to create a standalone service from a Git repository
type CreateStandaloneFromGitRequest struct {
	ServiceName          string                       `json:"service_name"`
	RepoURL              string                       `json:"repo_url"`
	Branch               string                       `json:"branch,omitempty"`
	ExecutablePath       string                       `json:"executable_path"` // Relative path within repo
	BuildCommand         string                       `json:"build_command,omitempty"`
	Arguments            []string                     `json:"arguments,omitempty"`
	WorkingDirectory     string                       `json:"working_directory,omitempty"`
	EnvironmentVariables map[string]string            `json:"environment_variables,omitempty"`
	TimeoutSeconds       int                          `json:"timeout_seconds,omitempty"`
	RunAsUser            string                       `json:"run_as_user,omitempty"`
	Username             string                       `json:"username,omitempty"`
	Password             string                       `json:"password,omitempty"`
	Description          string                       `json:"description"`
	Capabilities         map[string]interface{}       `json:"capabilities,omitempty"`
	CustomInterfaces     []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
}

// handleCreateStandaloneFromLocal handles POST /api/services/standalone/from-local
func (s *APIServer) handleCreateStandaloneFromLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateStandaloneFromLocalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServiceName == "" || req.ExecutablePath == "" {
		http.Error(w, "service_name and executable_path are required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Creating standalone service from local: %s", req.ExecutablePath), "api")

	// Create service
	service, suggestions, err := s.standaloneService.CreateFromLocal(
		req.ServiceName,
		req.ExecutablePath,
		req.Arguments,
		req.WorkingDirectory,
		req.EnvironmentVariables,
		req.TimeoutSeconds,
		req.RunAsUser,
		req.Description,
		req.Capabilities,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create standalone service from local: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to create service: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Standalone service created successfully: %s (ID: %d)", req.ServiceName, service.ID), "api")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"service":              service,
		"suggested_interfaces": suggestions,
	})
}

// Note: Upload functionality is handled via WebSocket
// See internal/api/websocket/file_upload_handler.go for chunked file upload implementation
// The upload flow is:
// 1. Client sends FILE_UPLOAD_START message via WebSocket
// 2. Client sends FILE_UPLOAD_CHUNK messages with base64-encoded data
// 3. Client sends FILE_UPLOAD_END message when complete
// 4. Server processes uploaded files and creates standalone service
// This allows for large file uploads with progress tracking and resumability

// handleCreateStandaloneFromGit handles POST /api/services/standalone/from-git
func (s *APIServer) handleCreateStandaloneFromGit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateStandaloneFromGitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServiceName == "" || req.RepoURL == "" || req.ExecutablePath == "" {
		http.Error(w, "service_name, repo_url, and executable_path are required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Creating standalone service from Git: %s", req.RepoURL), "api")

	// Create service (this may take a while due to cloning and building)
	service, suggestions, err := s.standaloneService.CreateFromGit(
		req.ServiceName,
		req.RepoURL,
		req.Branch,
		req.ExecutablePath,
		req.BuildCommand,
		req.Arguments,
		req.WorkingDirectory,
		req.EnvironmentVariables,
		req.TimeoutSeconds,
		req.RunAsUser,
		req.Username,
		req.Password,
		req.Description,
		req.Capabilities,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create standalone service from Git: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to create service: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Standalone service created successfully from Git: %s (ID: %d)", req.ServiceName, service.ID), "api")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"service":              service,
		"suggested_interfaces": suggestions,
	})
}

// handleGetStandaloneDetails handles GET /api/services/standalone/:id/details
func (s *APIServer) handleGetStandaloneDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract service ID from path
	// This assumes the router passes the ID as a path parameter
	// You may need to adjust based on your router setup
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		// Try extracting from path
		parts := splitPath(r.URL.Path)
		if len(parts) >= 5 { // /api/services/standalone/:id/details
			idStr = parts[3]
		}
	}

	serviceID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get service
	service, err := s.dbManager.GetService(serviceID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get service: %v", err), "api")
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	if service.ServiceType != "STANDALONE" {
		http.Error(w, "Service is not a standalone service", http.StatusBadRequest)
		return
	}

	// Get standalone details
	details, err := s.dbManager.GetStandaloneServiceDetails(serviceID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get standalone service details: %v", err), "api")
		http.Error(w, "Failed to get service details", http.StatusInternalServerError)
		return
	}

	// Get interfaces
	interfaces, err := s.dbManager.GetServiceInterfaces(serviceID)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Failed to get service interfaces: %v", err), "api")
	}
	service.Interfaces = interfaces

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"service": service,
		"details": details,
	})
}

// handleFinalizeStandaloneUpload handles POST /api/services/standalone/:id/finalize
// Called after WebSocket upload completes to add configuration
func (s *APIServer) handleFinalizeStandaloneUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract service ID from path
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		parts := splitPath(r.URL.Path)
		if len(parts) >= 5 { // /api/services/standalone/:id/finalize
			idStr = parts[3]
		}
	}

	serviceID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Parse request body
	var req struct {
		ExecutablePath       string                       `json:"executable_path"`
		Arguments            []string                     `json:"arguments,omitempty"`
		WorkingDirectory     string                       `json:"working_directory,omitempty"`
		EnvironmentVariables map[string]string            `json:"environment_variables,omitempty"`
		TimeoutSeconds       int                          `json:"timeout_seconds,omitempty"`
		RunAsUser            string                       `json:"run_as_user,omitempty"`
		Capabilities         map[string]interface{}       `json:"capabilities,omitempty"`
		CustomInterfaces     []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get service to verify it exists and is standalone
	service, err := s.dbManager.GetService(serviceID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get service: %v", err), "api")
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	if service.ServiceType != "STANDALONE" {
		http.Error(w, "Service is not a standalone service", http.StatusBadRequest)
		return
	}

	// Finalize the uploaded service with configuration
	suggestions, err := s.standaloneService.FinalizeUploadedService(
		serviceID,
		req.ExecutablePath,
		req.Arguments,
		req.WorkingDirectory,
		req.EnvironmentVariables,
		req.TimeoutSeconds,
		req.RunAsUser,
		req.Capabilities,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to finalize standalone service: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to finalize service: %v", err), http.StatusInternalServerError)
		return
	}

	// Get updated service
	service, err = s.dbManager.GetService(serviceID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get updated service: %v", err), "api")
		http.Error(w, "Failed to retrieve updated service", http.StatusInternalServerError)
		return
	}

	// Get interfaces
	interfaces, err := s.dbManager.GetServiceInterfaces(serviceID)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Failed to get service interfaces: %v", err), "api")
	}
	service.Interfaces = interfaces

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Standalone service finalized successfully: %d", serviceID), "api")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"service":              service,
		"suggested_interfaces": suggestions,
	})
}

// handleDeleteStandaloneService handles DELETE /api/services/standalone/:id
func (s *APIServer) handleDeleteStandaloneService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract service ID from path
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		parts := splitPath(r.URL.Path)
		if len(parts) >= 4 { // /api/services/standalone/:id
			idStr = parts[3]
		}
	}

	serviceID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get service to verify it's standalone
	service, err := s.dbManager.GetService(serviceID)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get service: %v", err), "api")
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	if service.ServiceType != "STANDALONE" {
		http.Error(w, "Service is not a standalone service", http.StatusBadRequest)
		return
	}

	// Delete service (CASCADE will delete details and interfaces)
	if err := s.dbManager.DeleteService(serviceID); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to delete service: %v", err), "api")
		http.Error(w, "Failed to delete service", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Standalone service deleted successfully: %d", serviceID), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Service deleted successfully",
	})
}

// Helper function to split URL path
func splitPath(path string) []string {
	parts := []string{}
	for _, part := range []byte(path) {
		if part == '/' {
			continue
		}
		parts = append(parts, string(part))
	}
	// Actually split properly
	result := []string{}
	current := ""
	for _, char := range path {
		if char == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
