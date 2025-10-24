package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"services": services,
		"total":    len(services),
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
	if service.Type == "" || service.Name == "" || service.Endpoint == "" {
		http.Error(w, "Missing required fields: type, name, endpoint", http.StatusBadRequest)
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

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

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

	err = s.dbManager.DeleteService(id)
	if err != nil {
		s.logger.Error("Failed to delete service", "api")
		http.Error(w, "Failed to delete service", http.StatusInternalServerError)
		return
	}

	// Broadcast service update via WebSocket
	s.eventEmitter.BroadcastServiceUpdate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Service deleted successfully",
	})
}
