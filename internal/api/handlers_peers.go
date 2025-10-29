package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PeerInfo represents a known peer
type PeerInfo struct {
	PeerID     string    `json:"peer_id"`
	DHTNodeID  string    `json:"dht_node_id"`
	IsRelay    bool      `json:"is_relay"`
	IsStore    bool      `json:"is_store"`
	FilesCount int       `json:"files_count"` // Count of ACTIVE DATA services
	AppsCount  int       `json:"apps_count"`  // Count of ACTIVE DOCKER + STANDALONE services
	LastSeen   time.Time `json:"last_seen"`
	Topic      string    `json:"topic"`
	Source     string    `json:"source"`
}

// PeersResponse represents the list of known peers
type PeersResponse struct {
	Peers []PeerInfo `json:"peers"`
	Total int        `json:"total"`
}

// handlePeers returns the list of known peers
func (s *APIServer) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Add JWT authentication middleware

	// Get known peers from database
	knownPeers, err := s.dbManager.KnownPeers.GetAllKnownPeers()
	if err != nil {
		s.logger.Error("Failed to get known peers", "api")
		http.Error(w, "Failed to fetch peers", http.StatusInternalServerError)
		return
	}

	s.logger.Info(fmt.Sprintf("API /peers: Retrieved %d peers from database", len(knownPeers)), "api")

	// Convert to response format
	peers := make([]PeerInfo, len(knownPeers))
	for i, peer := range knownPeers {
		s.logger.Debug(fmt.Sprintf("API /peers: Peer %s - files=%d, apps=%d", peer.PeerID[:8], peer.FilesCount, peer.AppsCount), "api")
		peers[i] = PeerInfo{
			PeerID:     peer.PeerID,
			DHTNodeID:  peer.DHTNodeID,
			IsRelay:    peer.IsRelay,
			IsStore:    peer.IsStore,
			FilesCount: peer.FilesCount,
			AppsCount:  peer.AppsCount,
			LastSeen:   peer.LastSeen,
			Topic:      peer.Topic,
			Source:     peer.Source,
		}
	}

	response := PeersResponse{
		Peers: peers,
		Total: len(peers),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
