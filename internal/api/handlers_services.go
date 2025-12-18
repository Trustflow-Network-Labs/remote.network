package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
)

// handleGetServices returns all services offered by this node
func (s *APIServer) handleGetServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	services, err := s.dbManager.GetAllServices()
	if err != nil {
		s.logger.Error("Failed to get services", "api")
		http.Error(w, "Failed to retrieve services", http.StatusInternalServerError)
		return
	}

	// Enhance each service with its interfaces and Docker details (if applicable)
	type EnhancedService struct {
		*database.OfferedService
		DockerDetails *database.DockerServiceDetails `json:"docker_details,omitempty"`
	}

	enhancedServices := make([]EnhancedService, len(services))
	for i := range services {
		enhancedServices[i] = EnhancedService{OfferedService: services[i]}

		// Add interfaces
		interfaces, err := s.dbManager.GetServiceInterfaces(services[i].ID)
		if err == nil {
			enhancedServices[i].Interfaces = interfaces
		} else {
			// If error, just set empty array (don't fail the whole request)
			enhancedServices[i].Interfaces = []*database.ServiceInterface{}
		}

		// Add Docker details for Docker services
		if services[i].ServiceType == types.ServiceTypeDocker {
			dockerDetails, err := s.dbManager.GetDockerServiceDetails(services[i].ID)
			if err == nil {
				enhancedServices[i].DockerDetails = dockerDetails
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"services": enhancedServices,
		"total":    len(enhancedServices),
	})
}

// handleGetService returns a specific service by ID
func (s *APIServer) handleGetService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/services/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	service, err := s.dbManager.GetService(id)
	if err != nil {
		s.logger.Error("Failed to get service", "api")
		http.Error(w, "Failed to retrieve service", http.StatusInternalServerError)
		return
	}

	if service == nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service": service,
	})
}

// handleGetServiceInterfaces returns all interfaces for a service
func (s *APIServer) handleGetServiceInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path (format: /api/services/{id}/interfaces)
	path := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	interfaces, err := s.dbManager.GetServiceInterfaces(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get service interfaces: %v", err), "api")
		http.Error(w, "Failed to retrieve service interfaces", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"interfaces": interfaces,
		"total":      len(interfaces),
	})
}

// handleAddService adds a new service
func (s *APIServer) handleAddService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var service database.OfferedService
	err := json.NewDecoder(r.Body).Decode(&service)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if service.Type == "" || service.Name == "" {
		http.Error(w, "Missing required fields: type, name", http.StatusBadRequest)
		return
	}

	// Endpoint is required for non-DATA services
	if service.ServiceType != types.ServiceTypeData && service.Endpoint == "" {
		http.Error(w, "Missing required field: endpoint", http.StatusBadRequest)
		return
	}

	// Validate service type
	validTypes := map[string]bool{
		"storage":    true,
		"docker":     true,
		"standalone": true,
		"relay":      true,
	}

	if !validTypes[service.Type] {
		http.Error(w, "Invalid service type", http.StatusBadRequest)
		return
	}

	// Set default status if not provided
	if service.Status == "" {
		service.Status = "available"
	}

	// Initialize capabilities map if nil
	if service.Capabilities == nil {
		service.Capabilities = make(map[string]interface{})
	}

	err = s.dbManager.AddService(&service)
	if err != nil {
		s.logger.Error("Failed to add service", "api")
		http.Error(w, "Failed to add service", http.StatusInternalServerError)
		return
	}

	// Create default interfaces based on service type
	if err := s.createDefaultServiceInterfaces(&service); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create default service interfaces: %v", err), "api")
		// Don't fail the request, just log the error
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in DHT with new service counts
	s.updatePeerMetadataAfterServiceChange()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service": service,
		"message": "Service added successfully",
	})
}

// handleUpdateService updates an existing service
func (s *APIServer) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/services/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Check if service exists
	existing, err := s.dbManager.GetService(id)
	if err != nil {
		s.logger.Error("Failed to get service", "api")
		http.Error(w, "Failed to retrieve service", http.StatusInternalServerError)
		return
	}

	if existing == nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	var updates database.OfferedService
	err = json.NewDecoder(r.Body).Decode(&updates)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Update fields (keeping ID)
	updates.ID = id

	// Use existing values if not provided in update
	if updates.Type == "" {
		updates.Type = existing.Type
	}
	if updates.Name == "" {
		updates.Name = existing.Name
	}
	if updates.Endpoint == "" {
		updates.Endpoint = existing.Endpoint
	}
	if updates.Status == "" {
		updates.Status = existing.Status
	}
	if updates.Capabilities == nil {
		updates.Capabilities = existing.Capabilities
	}

	err = s.dbManager.UpdateService(&updates)
	if err != nil {
		s.logger.Error("Failed to update service", "api")
		http.Error(w, "Failed to update service", http.StatusInternalServerError)
		return
	}

	// Fetch updated service
	updatedService, err := s.dbManager.GetService(id)
	if err != nil {
		s.logger.Error("Failed to get updated service", "api")
		http.Error(w, "Failed to retrieve updated service", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service": updatedService,
		"message": "Service updated successfully",
	})
}

// handleDeleteService deletes a service
func (s *APIServer) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/services/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get service details before deleting to check if it's a Docker service
	service, err := s.dbManager.GetService(id)
	if err != nil {
		s.logger.Error("Failed to get service details", "api")
		http.Error(w, "Failed to retrieve service", http.StatusInternalServerError)
		return
	}

	if service == nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	// If it's a Docker service, clean up Docker resources
	if service.ServiceType == types.ServiceTypeDocker {
		dockerDetails, err := s.dbManager.GetDockerServiceDetails(id)
		if err == nil && dockerDetails != nil && dockerDetails.ImageName != "" {
			imageName := fmt.Sprintf("%s:%s", dockerDetails.ImageName, dockerDetails.ImageTag)
			s.logger.Info(fmt.Sprintf("Cleaning up Docker image for service %d: %s", id, imageName), "api")

			// Remove Docker image (don't fail deletion if image removal fails)
			if err := s.dockerService.RemoveImage(imageName); err != nil {
				s.logger.Warn(fmt.Sprintf("Failed to remove Docker image %s: %v", imageName, err), "api")
			}
		}
	}

	err = s.dbManager.DeleteService(id)
	if err != nil {
		s.logger.Error("Failed to delete service", "api")
		http.Error(w, "Failed to delete service", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in DHT with new service counts
	s.updatePeerMetadataAfterServiceChange()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Service deleted successfully",
	})
}

// handleUpdateServiceStatus updates a service status
func (s *APIServer) handleUpdateServiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path (format: /api/services/{id}/status)
	path := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Parse request body
	var requestBody struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate status
	if requestBody.Status != "ACTIVE" && requestBody.Status != "INACTIVE" {
		http.Error(w, "Invalid status value. Must be ACTIVE or INACTIVE", http.StatusBadRequest)
		return
	}

	// Update status in database
	err = s.dbManager.UpdateServiceStatus(id, requestBody.Status)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to update service status: %v", err), "api")
		http.Error(w, "Failed to update service status", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	// Update peer metadata in DHT with new service counts
	s.updatePeerMetadataAfterServiceChange()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Service status updated successfully",
		"status":  requestBody.Status,
	})
}

// handleGetServicePassphrase retrieves the passphrase for a data service
func (s *APIServer) handleGetServicePassphrase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path (format: /api/services/{id}/passphrase)
	path := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "Invalid service ID", http.StatusBadRequest)
		return
	}

	// Get service to verify it exists and is a DATA service
	service, err := s.dbManager.GetService(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get service: %v", err), "api")
		http.Error(w, "Failed to retrieve service", http.StatusInternalServerError)
		return
	}

	if service == nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	if service.ServiceType != types.ServiceTypeData {
		http.Error(w, "Passphrase only available for DATA services", http.StatusBadRequest)
		return
	}

	// Get passphrase from file processor
	if s.fileProcessor == nil {
		http.Error(w, "File processor not available", http.StatusInternalServerError)
		return
	}

	passphrase, err := s.fileProcessor.GetPassphrase(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get passphrase: %v", err), "api")
		http.Error(w, "Failed to retrieve passphrase", http.StatusInternalServerError)
		return
	}

	// Get data service details
	dataDetails, err := s.dbManager.GetDataServiceDetails(id)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get data service details: %v", err), "api")
		http.Error(w, "Failed to retrieve data service details", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"passphrase":   passphrase,
		"data_details": dataDetails,
	})
}

// handleProcessUploadedFile triggers file processing after upload completion
func (s *APIServer) handleProcessUploadedFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var requestBody struct {
		SessionID string `json:"session_id"`
		ServiceID int64  `json:"service_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.SessionID == "" || requestBody.ServiceID == 0 {
		http.Error(w, "Missing required fields: session_id, service_id", http.StatusBadRequest)
		return
	}

	// Process file asynchronously
	if s.fileProcessor == nil {
		http.Error(w, "File processor not available", http.StatusInternalServerError)
		return
	}

	go func() {
		if err := s.fileProcessor.ProcessUploadedFile(requestBody.SessionID, requestBody.ServiceID); err != nil {
			s.logger.Error(fmt.Sprintf("Failed to process uploaded file: %v", err), "api")
		} else {
			// Broadcast service update after successful processing
			s.eventEmitter.BroadcastServiceUpdate()
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "File processing started",
	})
}

// updatePeerMetadataAfterServiceChange triggers an update to peer metadata in DHT
// This should be called after any service addition, deletion, or status change
func (s *APIServer) updatePeerMetadataAfterServiceChange() {
	if s.peerManager == nil {
		return
	}

	metadataPublisher := s.peerManager.GetMetadataPublisher()
	if metadataPublisher == nil {
		return
	}

	// Run in goroutine to avoid blocking the HTTP response
	go func() {
		if err := metadataPublisher.UpdateServiceMetadata(); err != nil {
			s.logger.Error(fmt.Sprintf("Failed to update peer metadata after service change: %v", err), "api")
		} else {
			s.logger.Debug("Successfully updated peer metadata after service change", "api")
		}
	}()
}

// createDefaultServiceInterfaces creates default interfaces for a service based on its type
// Only DATA services get auto-created interfaces. DOCKER and STANDALONE interfaces are user-defined.
func (s *APIServer) createDefaultServiceInterfaces(service *database.OfferedService) error {
	if service.ServiceType != types.ServiceTypeData {
		// DOCKER and STANDALONE interfaces will be defined by user in service creation wizard
		return nil
	}

	// DATA services only provide output (the data file itself)
	stdoutInterface := &database.ServiceInterface{
		ServiceID:     service.ID,
		InterfaceType: "STDOUT",
		Path:          "",
	}

	if err := s.dbManager.AddServiceInterface(stdoutInterface); err != nil {
		return fmt.Errorf("failed to add STDOUT interface: %w", err)
	}

	s.logger.Info(fmt.Sprintf("Created STDOUT interface for DATA service %d", service.ID), "api")
	return nil
}
