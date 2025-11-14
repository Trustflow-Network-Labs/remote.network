package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RelaySessionResponse represents a relay session detail
type RelaySessionResponse struct {
	SessionID       string  `json:"session_id"`
	ClientNodeID    string  `json:"client_node_id"`
	ClientPeerID    string  `json:"client_peer_id"`
	SessionType     string  `json:"session_type"`
	StartTime       int64   `json:"start_time"`
	DurationSeconds int64   `json:"duration_seconds"`
	IngressBytes    int64   `json:"ingress_bytes"`
	EgressBytes     int64   `json:"egress_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	Earnings        float64 `json:"earnings"`
	LastKeepalive   int64   `json:"last_keepalive"`
}

// RelayCandidateResponse represents an available relay candidate
type RelayCandidateResponse struct {
	NodeID          string  `json:"node_id"`
	PeerID          string  `json:"peer_id,omitempty"`
	Endpoint        string  `json:"endpoint"`
	Latency         string  `json:"latency"`
	LatencyMs       int64   `json:"latency_ms"`
	ReputationScore float64 `json:"reputation_score"`
	PricingPerGB    float64 `json:"pricing_per_gb"`
	Capacity        int     `json:"capacity"`
	LastSeen        int64   `json:"last_seen"`
	IsConnected     bool    `json:"is_connected"`
	IsPreferred     bool    `json:"is_preferred"`
	IsAvailable     bool    `json:"is_available"`          // False if ping/pong failed
	UnavailableMsg  string  `json:"unavailable_msg"`        // Reason why relay is unavailable
}

// ConnectRelayRequest represents a relay connection request
type ConnectRelayRequest struct {
	PeerID string `json:"peer_id"`
}

// PreferRelayRequest represents a preferred relay request
type PreferRelayRequest struct {
	PeerID string `json:"peer_id"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// SuccessResponse represents a success response
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// handleRelayGetSessions returns all active relay sessions (relay mode only)
func (s *APIServer) handleRelayGetSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get stats to check if relay mode is enabled
	stats := s.peerManager.GetStats()
	relayMode := false
	if val, ok := stats["relay_mode"]; ok {
		if boolVal, ok := val.(bool); ok {
			relayMode = boolVal
		}
	}

	if !relayMode {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Node is not in relay mode"})
		return
	}

	// Get session details from relay peer
	// Access via peer manager
	relayPeer := s.peerManager.GetRelayPeer()
	if relayPeer == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Relay peer not initialized"})
		return
	}

	sessions := relayPeer.GetSessionDetails()

	// Convert to response format
	response := make([]RelaySessionResponse, 0, len(sessions))
	for _, session := range sessions {
		sessionResp := RelaySessionResponse{}

		if val, ok := session["session_id"]; ok {
			if str, ok := val.(string); ok {
				sessionResp.SessionID = str
			}
		}
		if val, ok := session["client_node_id"]; ok {
			if str, ok := val.(string); ok {
				sessionResp.ClientNodeID = str
			}
		}
		if val, ok := session["client_peer_id"]; ok {
			if str, ok := val.(string); ok {
				sessionResp.ClientPeerID = str
			}
		}
		if val, ok := session["session_type"]; ok {
			if str, ok := val.(string); ok {
				sessionResp.SessionType = str
			}
		}
		if val, ok := session["start_time"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.StartTime = intVal
			}
		}
		if val, ok := session["duration_seconds"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.DurationSeconds = intVal
			}
		}
		if val, ok := session["ingress_bytes"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.IngressBytes = intVal
			}
		}
		if val, ok := session["egress_bytes"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.EgressBytes = intVal
			}
		}
		if val, ok := session["total_bytes"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.TotalBytes = intVal
			}
		}
		if val, ok := session["earnings"]; ok {
			if floatVal, ok := val.(float64); ok {
				sessionResp.Earnings = floatVal
			}
		}
		if val, ok := session["last_keepalive"]; ok {
			if intVal, ok := val.(int64); ok {
				sessionResp.LastKeepalive = intVal
			}
		}

		response = append(response, sessionResp)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleRelayDisconnectSession disconnects a specific relay session
func (s *APIServer) handleRelayDisconnectSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session ID from URL path
	// Expected format: /api/relay/sessions/{sessionID}/disconnect
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid URL format"})
		return
	}

	sessionID := parts[4]

	// Get relay peer
	relayPeer := s.peerManager.GetRelayPeer()
	if relayPeer == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Relay peer not initialized"})
		return
	}

	// Disconnect session
	err := relayPeer.DisconnectSession(sessionID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SuccessResponse{
		Success: true,
		Message: "Session disconnected successfully",
	})
}

// handleRelayBlacklistSession blacklists and disconnects a peer
func (s *APIServer) handleRelayBlacklistSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session ID from URL path
	// Expected format: /api/relay/sessions/{sessionID}/blacklist
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid URL format"})
		return
	}

	sessionID := parts[4]

	// Get relay peer
	relayPeer := s.peerManager.GetRelayPeer()
	if relayPeer == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Relay peer not initialized"})
		return
	}

	// Get session details to extract client node ID
	sessions := relayPeer.GetSessionDetails()
	var clientNodeID string
	for _, session := range sessions {
		if val, ok := session["session_id"]; ok {
			if str, ok := val.(string); ok && str == sessionID {
				if nodeVal, ok := session["client_node_id"]; ok {
					if nodeStr, ok := nodeVal.(string); ok {
						clientNodeID = nodeStr
						break
					}
				}
			}
		}
	}

	if clientNodeID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Session not found"})
		return
	}

	// Blacklist the peer
	err := s.dbManager.AddToBlacklist(clientNodeID, "Manual blacklist from relay session")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to blacklist peer: " + err.Error()})
		return
	}

	// Disconnect the session
	err = relayPeer.DisconnectSession(sessionID)
	if err != nil {
		s.logger.Warn("Failed to disconnect session after blacklist: "+err.Error(), "api")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SuccessResponse{
		Success: true,
		Message: "Peer blacklisted and disconnected successfully",
	})
}

// handleRelayGetCandidates returns available relay candidates (NAT mode only)
func (s *APIServer) handleRelayGetCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get relay manager
	relayManager := s.peerManager.GetRelayManager()
	if relayManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Node is not in NAT mode"})
		return
	}

	// Get current relay and preferred relay
	currentRelay := relayManager.GetCurrentRelay()

	stats := s.peerManager.GetStats()
	myPeerID := ""
	if val, ok := stats["peer_id"]; ok {
		if str, ok := val.(string); ok {
			myPeerID = str
		}
	}

	preferredRelayPeerID, _ := relayManager.GetPreferredRelay(myPeerID)

	// Get all candidates
	candidates := relayManager.GetSelector().GetCandidates()

	response := make([]RelayCandidateResponse, 0, len(candidates))
	for _, candidate := range candidates {
		candResp := RelayCandidateResponse{
			NodeID:          candidate.NodeID,
			PeerID:          candidate.PeerID, // Persistent Ed25519-based peer ID
			Endpoint:        candidate.Endpoint,
			Latency:         candidate.Latency.String(),
			LatencyMs:       candidate.Latency.Milliseconds(),
			ReputationScore: candidate.ReputationScore,
			PricingPerGB:    candidate.PricingPerGB,
			Capacity:        candidate.Capacity,
			LastSeen:        candidate.LastSeen.Unix(),
			IsConnected:     currentRelay != nil && currentRelay.NodeID == candidate.NodeID,
			IsPreferred:     preferredRelayPeerID != "" && preferredRelayPeerID == candidate.PeerID, // Match by persistent peer_id
			IsAvailable:     candidate.IsAvailable,
			UnavailableMsg:  candidate.UnavailableMsg,
		}

		response = append(response, candResp)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleRelayConnect connects to a specific relay
func (s *APIServer) handleRelayConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConnectRelayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.PeerID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "peer_id is required"})
		return
	}

	// Get relay manager
	relayManager := s.peerManager.GetRelayManager()
	if relayManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Node is not in NAT mode"})
		return
	}

	// Connect to specific relay asynchronously to avoid HTTP timeout
	// Frontend will receive connection status via WebSocket RELAY_CANDIDATES message
	go func() {
		err := relayManager.ConnectToSpecificRelay(req.PeerID)
		if err != nil {
			s.logger.Warn("Failed to connect to relay: "+err.Error(), "api")
		} else {
			s.logger.Info("Successfully connected to relay "+req.PeerID, "api")
			// Trigger immediate WebSocket update for responsive UI
			s.eventEmitter.TriggerRelayBroadcast()
		}
	}()

	// Return immediately with 202 Accepted
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(SuccessResponse{
		Success: true,
		Message: "Relay connection initiated",
	})
}

// handleRelayDisconnect disconnects from current relay
func (s *APIServer) handleRelayDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get relay manager
	relayManager := s.peerManager.GetRelayManager()
	if relayManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Node is not in NAT mode"})
		return
	}

	// Disconnect from relay
	relayManager.DisconnectRelay()

	// Trigger immediate WebSocket update for responsive UI
	s.eventEmitter.TriggerRelayBroadcast()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SuccessResponse{
		Success: true,
		Message: "Disconnected from relay successfully",
	})
}

// handleRelayPrefer sets a relay as preferred
func (s *APIServer) handleRelayPrefer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PreferRelayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.PeerID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "peer_id is required"})
		return
	}

	// Get relay manager
	relayManager := s.peerManager.GetRelayManager()
	if relayManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Node is not in NAT mode"})
		return
	}

	// Get my peer ID
	stats := s.peerManager.GetStats()
	myPeerID := ""
	if val, ok := stats["peer_id"]; ok {
		if str, ok := val.(string); ok {
			myPeerID = str
		}
	}

	if myPeerID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to get peer ID"})
		return
	}

	// Set preferred relay
	err := relayManager.SetPreferredRelay(myPeerID, req.PeerID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to set preferred relay: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SuccessResponse{
		Success: true,
		Message: "Preferred relay set successfully",
	})
}
