package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	ws "github.com/Trustflow-Network-Labs/remote-network-node/internal/api/websocket"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/system"
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

	// NAT and Relay status (from DHT metadata)
	IsBehindNAT      bool   `json:"is_behind_nat"`       // Is this peer behind NAT?
	NATType          string `json:"nat_type"`            // NAT type: "full_cone", "restricted", etc.
	UsingRelay       bool   `json:"using_relay"`         // Is this peer currently using a relay?
	ConnectedRelayID string `json:"connected_relay_id"`  // PeerID of the connected relay
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
	knownPeers, err := s.peerManager.GetKnownPeers().GetAllKnownPeers()
	if err != nil {
		s.logger.Error("Failed to get known peers", "api")
		http.Error(w, "Failed to fetch peers", http.StatusInternalServerError)
		return
	}

	s.logger.Info(fmt.Sprintf("API /peers: Retrieved %d peers from database", len(knownPeers)), "api")

	// Convert to response format
	peers := make([]PeerInfo, len(knownPeers))
	for i, peer := range knownPeers {
		s.logger.Debug(fmt.Sprintf("API /peers: Peer %s - files=%d, apps=%d, behind_nat=%v, using_relay=%v", peer.PeerID[:8], peer.FilesCount, peer.AppsCount, peer.IsBehindNAT, peer.UsingRelay), "api")
		peers[i] = PeerInfo{
			PeerID:           peer.PeerID,
			DHTNodeID:        peer.DHTNodeID,
			IsRelay:          peer.IsRelay,
			IsStore:          peer.IsStore,
			FilesCount:       peer.FilesCount,
			AppsCount:        peer.AppsCount,
			LastSeen:         peer.LastSeen,
			Topic:            peer.Topic,
			Source:           peer.Source,
			IsBehindNAT:      peer.IsBehindNAT,
			NATType:          peer.NATType,
			UsingRelay:       peer.UsingRelay,
			ConnectedRelayID: peer.ConnectedRelayID,
		}
	}

	response := PeersResponse{
		Peers: peers,
		Total: len(peers),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// PeerCapabilitiesResponse represents a peer's system capabilities
type PeerCapabilitiesResponse struct {
	PeerID       string                     `json:"peer_id"`
	Capabilities *system.SystemCapabilities `json:"capabilities,omitempty"`
	Error        string                     `json:"error,omitempty"`
}

// handlePeerCapabilities returns the system capabilities for a specific peer
// GET /api/peers/{peer_id}/capabilities
func (s *APIServer) handlePeerCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract peer_id from URL path: /api/peers/{peer_id}/capabilities
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/peers/"), "/")
	if len(parts) < 2 || parts[1] != "capabilities" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	peerID := parts[0]

	if peerID == "" {
		http.Error(w, "peer_id is required", http.StatusBadRequest)
		return
	}

	s.logger.Info(fmt.Sprintf("API /peers/%s/capabilities: Fetching capabilities from DHT", peerID[:min(8, len(peerID))]), "api")

	// Get the peer's public key from known_peers database
	// We need this to query DHT for metadata
	topics := s.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := "remote-network-mesh"
	if len(topics) > 0 {
		topic = topics[0]
	}

	knownPeer, err := s.peerManager.GetKnownPeers().GetKnownPeer(peerID, topic)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get peer %s from database: %v", peerID[:min(8, len(peerID))], err), "api")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerCapabilitiesResponse{
			PeerID: peerID,
			Error:  "Failed to find peer in database",
		})
		return
	}

	if knownPeer == nil {
		s.logger.Warn(fmt.Sprintf("Peer %s not found in database", peerID[:min(8, len(peerID))]), "api")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerCapabilitiesResponse{
			PeerID: peerID,
			Error:  "Peer not found",
		})
		return
	}

	// Fetch metadata from DHT using the peer's public key
	metadataQuery := s.peerManager.GetMetadataQueryService()
	if metadataQuery == nil {
		s.logger.Error("Metadata query service not available", "api")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerCapabilitiesResponse{
			PeerID: peerID,
			Error:  "Metadata service not available",
		})
		return
	}

	metadata, err := metadataQuery.QueryMetadata(peerID, knownPeer.PublicKey)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Failed to fetch metadata for peer %s from DHT: %v", peerID[:min(8, len(peerID))], err), "api")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PeerCapabilitiesResponse{
			PeerID: peerID,
			Error:  fmt.Sprintf("Failed to fetch metadata from DHT: %v", err),
		})
		return
	}

	// Extract capabilities from extensions - check for both new (capability_summary) and legacy (system_capabilities) formats
	var capabilities *system.SystemCapabilities
	var isFromSummary bool
	if metadata.Extensions != nil {
		// First check for new compact capability_summary format (BEP44 size-optimized)
		if capSummaryRaw, ok := metadata.Extensions["capability_summary"]; ok {
			if capSummaryMap, ok := capSummaryRaw.(map[string]interface{}); ok {
				capabilities = parseCapabilitySummary(capSummaryMap)
				isFromSummary = true
			}
		}
		// Fall back to legacy system_capabilities format
		if capabilities == nil {
			if sysCapsRaw, ok := metadata.Extensions["system_capabilities"]; ok {
				if sysCapsMap, ok := sysCapsRaw.(map[string]interface{}); ok {
					capabilities = parseSystemCapabilities(sysCapsMap)
				}
			}
		}
	}

	// Return DHT summary immediately for fast UI response
	response := PeerCapabilitiesResponse{
		PeerID:       peerID,
		Capabilities: capabilities,
	}

	if capabilities == nil {
		response.Error = "No system capabilities found in peer metadata"
	} else if isFromSummary {
		// Indicate that some fields may be missing since we only have the DHT summary
		s.logger.Debug(fmt.Sprintf("Returning DHT summary for peer %s, fetching full capabilities in background", peerID[:min(8, len(peerID))]), "api")
	}

	s.logger.Info(fmt.Sprintf("API /peers/%s/capabilities: Returning DHT summary immediately", peerID[:min(8, len(peerID))]), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	// If we only have a summary, fetch full capabilities in background and push via WebSocket
	if isFromSummary && capabilities != nil {
		go func() {
			s.logger.Info(fmt.Sprintf("Background: Fetching full capabilities for peer %s via QUIC", peerID[:min(8, len(peerID))]), "api")

			fullCaps, err := s.peerManager.FetchPeerCapabilities(peerID)
			if err != nil {
				s.logger.Debug(fmt.Sprintf("Background: QUIC fetch failed for peer %s: %v", peerID[:min(8, len(peerID))], err), "api")
				// Failed to fetch - client will keep using DHT summary
				return
			}

			s.logger.Info(fmt.Sprintf("Background: Successfully fetched full capabilities for peer %s, broadcasting update", peerID[:min(8, len(peerID))]), "api")

			// Broadcast capabilities update via WebSocket
			if s.wsHub != nil {
				updatePayload := PeerCapabilitiesResponse{
					PeerID:       peerID,
					Capabilities: fullCaps,
				}
				if err := s.wsHub.BroadcastPayload(ws.MessageTypePeerCapabilitiesUpdated, updatePayload); err != nil {
					s.logger.Error(fmt.Sprintf("Failed to broadcast capabilities update for peer %s: %v", peerID[:min(8, len(peerID))], err), "api")
				}
			}
		}()
	}
}

// parseCapabilitySummary converts a compact capability_summary map to SystemCapabilities struct
// The summary uses short bencode keys (p, a, c, m, d, g, gm, gv, dk, py) and GB instead of MB
// Some fields will be partial (e.g., no GPU names/UUIDs, no kernel version)
func parseCapabilitySummary(m map[string]interface{}) *system.SystemCapabilities {
	caps := &system.SystemCapabilities{}

	// Map short bencode keys to SystemCapabilities fields
	caps.Platform = toString(m["p"])
	if caps.Platform == "" {
		caps.Platform = toString(m["platform"]) // JSON fallback
	}
	caps.Architecture = toString(m["a"])
	if caps.Architecture == "" {
		caps.Architecture = toString(m["arch"]) // JSON fallback
	}
	caps.CPUCores = toInt(m["c"])
	if caps.CPUCores == 0 {
		caps.CPUCores = toInt(m["cpu_cores"]) // JSON fallback
	}

	// Memory in GB (convert back to MB for compatibility)
	memGB := toInt(m["m"])
	if memGB == 0 {
		memGB = toInt(m["memory_gb"]) // JSON fallback
	}
	caps.TotalMemoryMB = int64(memGB) * 1024

	// Disk in GB (convert back to MB for compatibility)
	diskGB := toInt(m["d"])
	if diskGB == 0 {
		diskGB = toInt(m["disk_gb"]) // JSON fallback
	}
	caps.AvailableDiskMB = int64(diskGB) * 1024

	// Docker availability
	caps.HasDocker = toBool(m["dk"])
	if !caps.HasDocker {
		caps.HasDocker = toBool(m["has_docker"]) // JSON fallback
	}

	// Python availability
	caps.HasPython = toBool(m["py"])
	if !caps.HasPython {
		caps.HasPython = toBool(m["has_python"]) // JSON fallback
	}

	// GPU information (partial - only count, total memory, and vendor)
	gpuCount := toInt(m["g"])
	if gpuCount == 0 {
		gpuCount = toInt(m["gpu_count"]) // JSON fallback
	}
	gpuMemGB := toInt(m["gm"])
	if gpuMemGB == 0 {
		gpuMemGB = toInt(m["gpu_memory_gb"]) // JSON fallback
	}
	gpuVendor := toString(m["gv"])
	if gpuVendor == "" {
		gpuVendor = toString(m["gpu_vendor"]) // JSON fallback
	}

	// Create placeholder GPUs based on summary info
	if gpuCount > 0 {
		// Distribute total memory across GPUs (approximate)
		memPerGPU := int64(gpuMemGB) * 1024 / int64(gpuCount)
		for i := 0; i < gpuCount; i++ {
			caps.GPUs = append(caps.GPUs, system.GPUInfo{
				Index:    i,
				Vendor:   gpuVendor,
				MemoryMB: memPerGPU,
				// Name, UUID, DriverVersion not available in summary
			})
		}
	}

	return caps
}

// parseSystemCapabilities converts a map to SystemCapabilities struct
// Handles both JSON-decoded maps (string values) and bencode-decoded maps ([]byte values)
func parseSystemCapabilities(m map[string]interface{}) *system.SystemCapabilities {
	caps := &system.SystemCapabilities{}

	caps.Platform = toString(m["platform"])
	caps.Architecture = toString(m["architecture"])
	caps.KernelVersion = toString(m["kernel_version"])
	caps.CPUModel = toString(m["cpu_model"])
	caps.CPUCores = toInt(m["cpu_cores"])
	caps.CPUThreads = toInt(m["cpu_threads"])
	caps.TotalMemoryMB = toInt64(m["total_memory_mb"])
	caps.AvailableMemoryMB = toInt64(m["available_memory_mb"])
	caps.TotalDiskMB = toInt64(m["total_disk_mb"])
	caps.AvailableDiskMB = toInt64(m["available_disk_mb"])
	caps.HasDocker = toBool(m["has_docker"])
	caps.DockerVersion = toString(m["docker_version"])
	caps.HasPython = toBool(m["has_python"])
	caps.PythonVersion = toString(m["python_version"])

	// Parse GPUs array
	if gpusRaw, ok := m["gpus"].([]interface{}); ok {
		for _, gpuRaw := range gpusRaw {
			if gpuMap, ok := gpuRaw.(map[string]interface{}); ok {
				gpu := system.GPUInfo{
					Index:         toInt(gpuMap["index"]),
					Name:          toString(gpuMap["name"]),
					Vendor:        toString(gpuMap["vendor"]),
					MemoryMB:      toInt64(gpuMap["memory_mb"]),
					UUID:          toString(gpuMap["uuid"]),
					DriverVersion: toString(gpuMap["driver_version"]),
				}
				caps.GPUs = append(caps.GPUs, gpu)
			}
		}
	}

	return caps
}

// toString converts various types to string (handles bencode []byte and regular strings)
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// toBool converts various types to bool
func toBool(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	default:
		return false
	}
}

// toInt converts various numeric types to int
func toInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case float32:
		return int(val)
	default:
		return 0
	}
}

// toInt64 converts various numeric types to int64
func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	case float32:
		return int64(val)
	default:
		return 0
	}
}
