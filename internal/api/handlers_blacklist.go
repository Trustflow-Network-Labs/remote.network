package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleGetBlacklist returns all blacklisted peers
func (s *APIServer) handleGetBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	blacklist, err := s.dbManager.GetBlacklist()
	if err != nil {
		s.logger.Error("Failed to get blacklist", "api")
		http.Error(w, "Failed to retrieve blacklist", http.StatusInternalServerError)
		return
	}

	// Also get just the peer IDs for the frontend stores
	peerIDs, err := s.dbManager.GetBlacklistedPeerIDs()
	if err != nil {
		s.logger.Error("Failed to get blacklisted peer IDs", "api")
		http.Error(w, "Failed to retrieve blacklist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"blacklist": blacklist,
		"peer_ids":  peerIDs,
		"total":     len(blacklist),
	})
}

// handleAddToBlacklist adds a peer to the blacklist
func (s *APIServer) handleAddToBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PeerID string `json:"peer_id"`
		Reason string `json:"reason"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate peer ID
	if req.PeerID == "" {
		http.Error(w, "Missing required field: peer_id", http.StatusBadRequest)
		return
	}

	// Set default reason if not provided
	if req.Reason == "" {
		req.Reason = "Manually blacklisted"
	}

	err = s.dbManager.AddToBlacklist(req.PeerID, req.Reason)
	if err != nil {
		s.logger.Error("Failed to add peer to blacklist", "api")
		http.Error(w, "Failed to blacklist peer", http.StatusInternalServerError)
		return
	}

	// Broadcast blacklist update via WebSocket
	s.eventEmitter.BroadcastBlacklistUpdate()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"peer_id": req.PeerID,
		"reason":  req.Reason,
		"message": "Peer added to blacklist successfully",
	})
}

// handleRemoveFromBlacklist removes a peer from the blacklist
func (s *APIServer) handleRemoveFromBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract peer ID from path
	peerID := strings.TrimPrefix(r.URL.Path, "/api/blacklist/")
	if peerID == "" {
		http.Error(w, "Missing peer ID", http.StatusBadRequest)
		return
	}

	err := s.dbManager.RemoveFromBlacklist(peerID)
	if err != nil {
		s.logger.Error("Failed to remove peer from blacklist", "api")
		http.Error(w, "Failed to remove peer from blacklist", http.StatusInternalServerError)
		return
	}

	// Broadcast blacklist update via WebSocket
	s.eventEmitter.BroadcastBlacklistUpdate()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Peer removed from blacklist successfully",
	})
}

// handleCheckBlacklist checks if a specific peer is blacklisted
func (s *APIServer) handleCheckBlacklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract peer ID from query parameter
	peerID := r.URL.Query().Get("peer_id")
	if peerID == "" {
		http.Error(w, "Missing peer_id query parameter", http.StatusBadRequest)
		return
	}

	isBlacklisted, err := s.dbManager.IsBlacklisted(peerID)
	if err != nil {
		s.logger.Error("Failed to check blacklist status", "api")
		http.Error(w, "Failed to check blacklist status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"peer_id":       peerID,
		"is_blacklisted": isBlacklisted,
	})
}
