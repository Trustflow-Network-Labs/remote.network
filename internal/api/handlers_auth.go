package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/auth"
)

// AuthRequest represents an authentication request
type AuthRequest struct {
	Challenge string `json:"challenge,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

// AuthResponse represents an authentication response
type AuthResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	PeerID  string `json:"peer_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ChallengeResponse represents a challenge generation response
type ChallengeResponse struct {
	Challenge string `json:"challenge"`
	ExpiresIn int    `json:"expires_in"` // seconds
}

// handleGetChallenge generates a new authentication challenge
func (s *APIServer) handleGetChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	challenge, err := s.challengeManager.GenerateChallenge()
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to generate challenge: %v", err), "api")
		s.sendError(w, "Failed to generate challenge", http.StatusInternalServerError)
		return
	}

	response := ChallengeResponse{
		Challenge: challenge,
		ExpiresIn: 300, // 5 minutes
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleAuthEd25519 handles Ed25519 private key authentication
func (s *APIServer) handleAuthEd25519(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.ed25519Provider == nil {
		s.sendError(w, "Ed25519 authentication not available", http.StatusServiceUnavailable)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate challenge exists and hasn't expired
	if !s.challengeManager.ConsumeChallenge(req.Challenge) {
		s.sendError(w, "Invalid or expired challenge", http.StatusUnauthorized)
		return
	}

	// Create credentials for Ed25519 provider
	creds := &auth.Ed25519Credentials{
		Challenge: req.Challenge,
		Signature: req.Signature,
		PublicKey: req.PublicKey,
	}

	// Authenticate using Ed25519 provider
	peerID, _, err := s.ed25519Provider.Authenticate(creds)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Ed25519 auth failed: %v", err), "api")
		s.sendError(w, fmt.Sprintf("Authentication failed: %v", err), http.StatusUnauthorized)
		return
	}

	// Generate JWT token (valid for 24 hours)
	token, err := s.jwtManager.GenerateToken(peerID, "ed25519", "", 24*time.Hour)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to generate JWT: %v", err), "api")
		s.sendError(w, "Failed to generate authentication token", http.StatusInternalServerError)
		return
	}

	s.logger.Info(fmt.Sprintf("Ed25519 authentication successful for peer: %s", peerID), "api")
	s.sendSuccess(w, token, peerID)
}

// sendError sends a JSON error response
func (s *APIServer) sendError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(AuthResponse{
		Success: false,
		Error:   message,
	})
}

// sendSuccess sends a JSON success response
func (s *APIServer) sendSuccess(w http.ResponseWriter, token, peerID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AuthResponse{
		Success: true,
		Token:   token,
		PeerID:  peerID,
	})
}
