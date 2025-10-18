package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// NodeStatusResponse represents node status information
type NodeStatusResponse struct {
	PeerID     string                 `json:"peer_id"`
	DHTNodeID  string                 `json:"dht_node_id"`
	Uptime     int64                  `json:"uptime_seconds"`
	Stats      map[string]interface{} `json:"stats"`
	KnownPeers int                    `json:"known_peers"`
}

// handleNodeStatus returns the current node status
func (s *APIServer) handleNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Add JWT authentication middleware

	// Get node stats from peer manager
	stats := s.peerManager.GetStats()

	// Get known peers count
	peersCount, err := s.dbManager.KnownPeers.GetKnownPeersCount()
	if err != nil {
		s.logger.Warn("Failed to get known peers count", "api")
		peersCount = 0
	}

	response := NodeStatusResponse{
		PeerID:     stats["peer_id"].(string),
		DHTNodeID:  stats["dht_node_id"].(string),
		Uptime:     int64(time.Since(s.startTime).Seconds()),
		Stats:      stats,
		KnownPeers: peersCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
