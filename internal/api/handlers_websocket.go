package api

import (
	"fmt"
	"net/http"

	ws "github.com/Trustflow-Network-Labs/remote-network-node/internal/api/websocket"
)

// handleWebSocket handles WebSocket connection upgrades with JWT authentication
func (s *APIServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract JWT token from query parameter
	token := r.URL.Query().Get("token")
	if token == "" {
		s.logger.Warn("WebSocket connection attempt without token", "api")
		http.Error(w, "Missing authentication token", http.StatusUnauthorized)
		return
	}

	// Validate JWT token
	claims, err := s.jwtManager.ValidateToken(token)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("WebSocket authentication failed: %v", err), "api")
		http.Error(w, "Invalid authentication token", http.StatusUnauthorized)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error(fmt.Sprintf("WebSocket upgrade failed: %v", err), "api")
		return
	}

	// Create new client
	client := ws.NewClient(conn, s.wsHub, claims.PeerID, s.wsLogger)

	// Register client with hub (via register channel)
	s.wsHub.RegisterClient(client)

	// Start client's read and write pumps
	client.Start()
}
