package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/websocket"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/google/shlex"
)

// Request types for Docker service creation

// CreateFromRegistryRequest represents a request to create a Docker service from a registry
type CreateFromRegistryRequest struct {
	ServiceName      string                       `json:"service_name"`
	ImageName        string                       `json:"image_name"`
	ImageTag         string                       `json:"image_tag"`
	Description      string                       `json:"description"`
	Username         string                       `json:"username,omitempty"`
	Password         string                       `json:"password,omitempty"`
	CustomInterfaces []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
}

// CreateFromGitRequest represents a request to create a Docker service from a Git repository
type CreateFromGitRequest struct {
	ServiceName      string                       `json:"service_name"`
	RepoURL          string                       `json:"repo_url"`
	Branch           string                       `json:"branch"`
	Username         string                       `json:"username,omitempty"`
	Password         string                       `json:"password,omitempty"`
	Description      string                       `json:"description"`
	CustomInterfaces []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
}

// CreateFromLocalRequest represents a request to create a Docker service from a local directory
type CreateFromLocalRequest struct {
	ServiceName      string                       `json:"service_name"`
	LocalPath        string                       `json:"local_path"`
	Description      string                       `json:"description"`
	CustomInterfaces []*database.ServiceInterface `json:"custom_interfaces,omitempty"`
}

// handleCreateDockerFromRegistry handles POST /api/services/docker/from-registry
func (s *APIServer) handleCreateDockerFromRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateFromRegistryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServiceName == "" || req.ImageName == "" {
		http.Error(w, "service_name and image_name are required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Creating Docker service from registry: %s:%s", req.ImageName, req.ImageTag), "api")

	// Create service
	service, suggestions, err := s.dockerService.CreateFromRegistry(
		req.ServiceName,
		req.ImageName,
		req.ImageTag,
		req.Description,
		req.Username,
		req.Password,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create Docker service from registry: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to create service: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Docker service created successfully: %s (ID: %d)", req.ServiceName, service.ID), "api")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"service":             service,
		"suggested_interfaces": suggestions,
	})
}

// handleCreateDockerFromGit handles POST /api/services/docker/from-git
func (s *APIServer) handleCreateDockerFromGit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateFromGitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServiceName == "" || req.RepoURL == "" {
		http.Error(w, "service_name and repo_url are required", http.StatusBadRequest)
		return
	}

	// Generate operation ID
	operationID := fmt.Sprintf("docker-git-%d", time.Now().UnixNano())

	s.logger.Info(fmt.Sprintf("Starting async Docker service creation from Git: %s (operation: %s)", req.RepoURL, operationID), "api")

	// Return immediately with operation ID
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"operation_id": operationID,
		"message":      "Docker service creation started",
	})

	// Run Docker service creation asynchronously
	go s.createDockerServiceFromGitAsync(operationID, req)
}

// createDockerServiceFromGitAsync creates a Docker service from Git asynchronously with WebSocket progress updates
func (s *APIServer) createDockerServiceFromGitAsync(operationID string, req CreateFromGitRequest) {
	// Emit start event
	startMsg, _ := websocket.NewMessage(websocket.MessageTypeDockerOperationStart, websocket.DockerOperationStartPayload{
		OperationID:   operationID,
		OperationType: "build",
		ServiceName:   req.ServiceName,
		ImageName:     "",
		Message:       fmt.Sprintf("Starting Docker service creation from Git: %s", req.RepoURL),
	})
	s.wsHub.Broadcast(startMsg)

	// Emit progress: Cloning repository
	progressMsg, _ := websocket.NewMessage(websocket.MessageTypeDockerOperationProgress, websocket.DockerOperationProgressPayload{
		OperationID: operationID,
		Message:     "Cloning Git repository...",
		Percentage:  10,
	})
	s.wsHub.Broadcast(progressMsg)

	// Create service
	service, suggestions, err := s.dockerService.CreateFromGitRepo(
		req.ServiceName,
		req.RepoURL,
		req.Branch,
		req.Username,
		req.Password,
		req.Description,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create Docker service from Git (operation: %s): %v", operationID, err), "api")

		// Emit error event
		errorMsg, _ := websocket.NewMessage(websocket.MessageTypeDockerOperationError, websocket.DockerOperationErrorPayload{
			OperationID: operationID,
			Error:       err.Error(),
		})
		s.wsHub.Broadcast(errorMsg)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Docker service created from Git successfully: %s (ID: %d, operation: %s)", req.ServiceName, service.ID, operationID), "api")

	// Convert service to map for JSON
	serviceMap := map[string]interface{}{
		"id":               service.ID,
		"name":             service.Name,
		"description":      service.Description,
		"service_type":     service.ServiceType,
		"type":             service.Type,
		"status":           service.Status,
		"pricing_amount":   service.PricingAmount,
		"pricing_type":     service.PricingType,
		"pricing_interval": service.PricingInterval,
		"pricing_unit":     service.PricingUnit,
		"capabilities":     service.Capabilities,
		"created_at":       service.CreatedAt,
		"updated_at":       service.UpdatedAt,
	}

	// Convert suggestions to interface{} slice
	suggestionsMap := make([]map[string]interface{}, len(suggestions))
	for i, s := range suggestions {
		suggestionsMap[i] = map[string]interface{}{
			"interface_type": s.InterfaceType,
			"path":           s.Path,
			"description":    s.Description,
		}
	}

	// Emit completion event
	completeMsg, _ := websocket.NewMessage(websocket.MessageTypeDockerOperationComplete, websocket.DockerOperationCompletePayload{
		OperationID:         operationID,
		ServiceID:           service.ID,
		ServiceName:         service.Name,
		ImageName:           fmt.Sprintf("%v:%v", service.Capabilities["image_name"], "latest"),
		SuggestedInterfaces: suggestionsMap,
		Service:             serviceMap,
	})
	s.wsHub.Broadcast(completeMsg)
}

// handleCreateDockerFromLocal handles POST /api/services/docker/from-local
func (s *APIServer) handleCreateDockerFromLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateFromLocalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode request: %v", err), "api")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ServiceName == "" || req.LocalPath == "" {
		http.Error(w, "service_name and local_path are required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Creating Docker service from local directory: %s", req.LocalPath), "api")

	// Create service
	service, suggestions, err := s.dockerService.CreateFromLocalDirectory(
		req.ServiceName,
		req.LocalPath,
		req.Description,
		req.CustomInterfaces,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create Docker service from local: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to create service: %v", err), http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in background
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Docker service created from local successfully: %s (ID: %d)", req.ServiceName, service.ID), "api")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"service":             service,
		"suggested_interfaces": suggestions,
	})
}

// handleGetDockerServiceDetails handles GET /api/services/docker/{id}/details
func (s *APIServer) handleGetDockerServiceDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/services/docker/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get Docker service details
	details, err := s.dbManager.GetDockerServiceDetails(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get Docker service details: %v", err), "api")
		http.Error(w, "Failed to retrieve Docker service details", http.StatusInternalServerError)
		return
	}

	if details == nil {
		http.Error(w, "Docker service details not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"details": details,
	})
}

// handleValidateGitRepo handles POST /api/services/docker/validate-git
func (s *APIServer) handleValidateGitRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RepoURL  string `json:"repo_url"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.RepoURL == "" {
		http.Error(w, "repo_url is required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Validating Git repository: %s", req.RepoURL), "api")

	// Validate repository
	err := s.gitService.ValidateRepo(req.RepoURL, req.Username, req.Password)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Git repository validation failed: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // Return 200 with validation result
		json.NewEncoder(w).Encode(map[string]interface{}{
			"valid": false,
			"error": err.Error(),
		})
		return
	}

	s.logger.Info(fmt.Sprintf("Git repository validated successfully: %s", req.RepoURL), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid": true,
	})
}

// handleGetSuggestedInterfaces handles GET /api/services/docker/{id}/interfaces/suggested
func (s *APIServer) handleGetSuggestedInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/services/docker/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get Docker service details
	details, err := s.dbManager.GetDockerServiceDetails(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get Docker service details: %v", err), "api")
		http.Error(w, "Failed to retrieve Docker service details", http.StatusInternalServerError)
		return
	}

	if details == nil {
		http.Error(w, "Docker service not found", http.StatusNotFound)
		return
	}

	s.logger.Info(fmt.Sprintf("Getting suggested interfaces for Docker service ID: %d", id), "api")

	// Re-detect interfaces based on source
	var suggestions interface{}

	switch details.Source {
	case "registry":
		// For registry images, detect from image metadata
		imageName := fmt.Sprintf("%s:%s", details.ImageName, details.ImageTag)
		cli, err := s.dockerService.GetDockerClient()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create Docker client: %v", err), http.StatusInternalServerError)
			return
		}
		defer cli.Close()

		detected, _, _, err := s.dockerService.DetectInterfacesFromImageName(cli, imageName)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to detect interfaces: %v", err), http.StatusInternalServerError)
			return
		}
		suggestions = detected

	case "git", "local":
		// For git/local, re-parse the Dockerfile or compose
		if details.ComposePath != "" {
			project, err := s.dockerService.ParseComposeFile(details.ComposePath, "")
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to parse compose file: %v", err), http.StatusInternalServerError)
				return
			}
			suggestions = s.dockerService.DetectInterfacesFromComposeProject(project)
		} else if details.ImageName != "" {
			// Image was built, detect from it
			cli, err := s.dockerService.GetDockerClient()
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to create Docker client: %v", err), http.StatusInternalServerError)
				return
			}
			defer cli.Close()

			imageName := fmt.Sprintf("%s:%s", details.ImageName, details.ImageTag)
			detected, _, _, err := s.dockerService.DetectInterfacesFromImageName(cli, imageName)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to detect interfaces: %v", err), http.StatusInternalServerError)
				return
			}
			suggestions = detected
		}

	default:
		http.Error(w, "Unknown Docker service source", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"suggested_interfaces": suggestions,
	})
}

// handleUpdateServiceInterfaces handles PUT /api/services/docker/{id}/interfaces
func (s *APIServer) handleUpdateServiceInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/services/docker/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Interfaces []*database.ServiceInterface `json:"interfaces"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Updating interfaces for Docker service ID: %d", id), "api")

	// Delete existing interfaces
	existingInterfaces, err := s.dbManager.GetServiceInterfaces(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get existing interfaces: %v", err), "api")
		http.Error(w, "Failed to retrieve existing interfaces", http.StatusInternalServerError)
		return
	}

	for _, iface := range existingInterfaces {
		if err := s.dbManager.DeleteServiceInterface(iface.ID); err != nil {
			s.logger.Error(fmt.Sprintf("Failed to delete interface: %v", err), "api")
			http.Error(w, "Failed to delete existing interfaces", http.StatusInternalServerError)
			return
		}
	}

	// Add new interfaces
	for _, iface := range req.Interfaces {
		iface.ServiceID = id
		if err := s.dbManager.AddServiceInterface(iface); err != nil {
			s.logger.Error(fmt.Sprintf("Failed to add interface: %v", err), "api")
			http.Error(w, "Failed to add new interfaces", http.StatusInternalServerError)
			return
		}
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Updated %d interfaces for Docker service ID: %d", len(req.Interfaces), id), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Interfaces updated successfully",
	})
}

// handleUpdateDockerServiceConfig handles PUT /api/services/docker/{id}/config
func (s *APIServer) handleUpdateDockerServiceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/services/docker/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Entrypoint string `json:"entrypoint"`
		Cmd        string `json:"cmd"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("Updating config for Docker service ID: %d", id), "api")
	s.logger.Debug(fmt.Sprintf("Raw entrypoint from UI: %q", req.Entrypoint), "api")
	s.logger.Debug(fmt.Sprintf("Raw cmd from UI: %q", req.Cmd), "api")

	// Get existing details
	details, err := s.dbManager.GetDockerServiceDetails(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get Docker service details: %v", err), "api")
		http.Error(w, "Failed to retrieve Docker service details", http.StatusInternalServerError)
		return
	}

	if details == nil {
		http.Error(w, "Docker service not found", http.StatusNotFound)
		return
	}

	// Parse entrypoint - handles shell command format like: markitdown
	// or: "arg with spaces" -o output
	if req.Entrypoint != "" {
		parsed, err := parseShellCommand(req.Entrypoint)
		if err != nil {
			s.logger.Error(fmt.Sprintf("Failed to parse entrypoint: %v", err), "api")
			http.Error(w, "Invalid entrypoint format", http.StatusBadRequest)
			return
		}
		s.logger.Debug(fmt.Sprintf("Parsed entrypoint: %s", parsed), "api")
		details.Entrypoint = parsed
	} else {
		details.Entrypoint = ""
	}

	// Parse cmd - handles shell command format
	if req.Cmd != "" {
		parsed, err := parseShellCommand(req.Cmd)
		if err != nil {
			s.logger.Error(fmt.Sprintf("Failed to parse cmd: %v", err), "api")
			http.Error(w, "Invalid cmd format", http.StatusBadRequest)
			return
		}
		s.logger.Debug(fmt.Sprintf("Parsed cmd: %s", parsed), "api")
		details.Cmd = parsed
	} else {
		details.Cmd = ""
	}

	if err := s.dbManager.UpdateDockerServiceDetails(details); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to update docker service config: %v", err), "api")
		http.Error(w, "Failed to update configuration", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata
	go s.updatePeerMetadataAfterServiceChange()

	s.logger.Info(fmt.Sprintf("Updated config for Docker service ID: %d", id), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Configuration updated successfully",
	})
}

// parseShellCommand parses a shell command string into JSON array format
// Handles both formats:
// - Shell command: markitdown "file.pdf" -o "output.md"
// - Already JSON: ["markitdown", "file.pdf", "-o", "output.md"]
func parseShellCommand(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}

	// Check if input is already a valid JSON array
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]") {
		var arr []string
		if err := json.Unmarshal([]byte(input), &arr); err == nil {
			// It's valid JSON - but check if it looks malformed
			// (contains elements with unmatched quotes like `"Masdar` or `Journal.pdf"`)
			isMalformed := false
			for _, elem := range arr {
				// Check for elements that start or end with quote but not both
				startsWithQuote := strings.HasPrefix(elem, "\"")
				endsWithQuote := strings.HasSuffix(elem, "\"")
				if startsWithQuote != endsWithQuote {
					isMalformed = true
					break
				}
			}
			if !isMalformed {
				// Valid JSON with clean content, return as-is
				return input, nil
			}
			// Malformed JSON content - reconstruct by joining and re-parsing
			// Join all elements with spaces, handling the malformed quotes
			reconstructed := strings.Join(arr, " ")
			// Clean up the malformed quotes (they're literal chars now)
			// and re-parse with shlex
			parts, err := shlex.Split(reconstructed)
			if err != nil {
				// Can't recover, return as-is
				return input, nil
			}
			jsonBytes, _ := json.Marshal(parts)
			return string(jsonBytes), nil
		}
		// Not valid JSON, fall through to shell parsing
	}

	// Parse as shell command using shlex
	parts, err := shlex.Split(input)
	if err != nil {
		return "", err
	}

	// Convert to JSON array
	jsonBytes, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

// shlexJoin converts a JSON array back to shell command format for display
func shlexJoin(jsonArray string) string {
	if jsonArray == "" {
		return ""
	}

	var args []string
	if err := json.Unmarshal([]byte(jsonArray), &args); err != nil {
		return jsonArray // Return as-is if not valid JSON
	}

	// Quote args that need quoting
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			quoted[i] = strconv.Quote(arg)
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
