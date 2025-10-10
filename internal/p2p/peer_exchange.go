package p2p

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeerInfo represents minimal peer information for exchange
type PeerInfo struct {
	NodeID         string              `json:"node_id"`
	PublicIP       string              `json:"public_ip,omitempty"`
	PrivateIP      string              `json:"private_ip,omitempty"`
	PublicPort     int                 `json:"public_port,omitempty"`
	PrivatePort    int                 `json:"private_port,omitempty"`
	NodeType       string              `json:"node_type,omitempty"`
	IsRelay        bool                `json:"is_relay"`
	UsingRelay     bool                `json:"using_relay,omitempty"`
	RelayNodeID    string              `json:"relay_node_id,omitempty"`
	RelaySessionID string              `json:"relay_session_id,omitempty"`
	RelayAddress   string              `json:"relay_address,omitempty"`
	Protocols      []database.Protocol `json:"protocols,omitempty"`
	LastSeen       int64               `json:"last_seen"` // Unix timestamp
}

// PeerExchange handles peer list selection and exchange
type PeerExchange struct {
	dbManager *database.SQLiteManager
	config    *utils.ConfigManager
	logger    *utils.LogsManager
	ourNodeID string
}

// NewPeerExchange creates a new peer exchange handler
func NewPeerExchange(dbManager *database.SQLiteManager, config *utils.ConfigManager, logger *utils.LogsManager, ourNodeID string) *PeerExchange {
	return &PeerExchange{
		dbManager: dbManager,
		config:    config,
		logger:    logger,
		ourNodeID: ourNodeID,
	}
}

// SelectPeersToShare intelligently selects peers to share with a requester
func (pe *PeerExchange) SelectPeersToShare(requester *database.PeerMetadata, topic string) []*PeerInfo {
	// Get configuration
	minPeers := pe.config.GetConfigInt("peer_exchange_min", 20, 5, 100)
	maxPeers := pe.config.GetConfigInt("peer_exchange_max", 20, 5, 100)
	maxAgeSec := pe.config.GetConfigInt("peer_exchange_max_age_seconds", 7200, 300, 86400) // 2 hours default
	maxAge := time.Duration(maxAgeSec) * time.Second

	// Get all peers from database
	allPeers, err := pe.dbManager.PeerMetadata.GetPeersByTopic(topic)
	if err != nil {
		pe.logger.Warn("Failed to get peers for exchange: "+err.Error(), "peer-exchange")
		return []*PeerInfo{}
	}

	// Filter valid peers
	validPeers := pe.filterValidPeers(allPeers, requester, maxAge)

	pe.logger.Debug("Peer exchange: "+string(rune(len(validPeers)))+" valid peers available", "peer-exchange")

	// If we have fewer than minPeers, send them all
	if len(validPeers) <= minPeers {
		pe.logger.Debug("Sending all peers (below minimum threshold)", "peer-exchange")
		return pe.convertToPeerInfo(validPeers)
	}

	// Otherwise, use smart selection
	pe.logger.Debug("Using smart peer selection", "peer-exchange")
	selectedPeers := pe.smartPeerSelection(validPeers, requester, maxPeers)

	return pe.convertToPeerInfo(selectedPeers)
}

// filterValidPeers removes invalid/stale/self peers
func (pe *PeerExchange) filterValidPeers(allPeers []*database.PeerMetadata, requester *database.PeerMetadata, maxAge time.Duration) []*database.PeerMetadata {
	var valid []*database.PeerMetadata
	now := time.Now()

	for _, peer := range allPeers {
		// Skip ourselves
		if peer.NodeID == pe.ourNodeID {
			continue
		}

		// Skip requester (don't send peer back to itself)
		if requester != nil && peer.NodeID == requester.NodeID {
			continue
		}

		// Skip peers with no network info
		if peer.NetworkInfo.PublicIP == "" && peer.NetworkInfo.PrivateIP == "" {
			continue
		}

		// Skip stale peers
		if now.Sub(peer.LastSeen) > maxAge {
			continue
		}

		// Skip NAT peers without complete relay information
		// Only share NAT peers that have established relay connections
		if peer.NetworkInfo.UsingRelay {
			// NAT peer must have relay connection details to be useful
			if peer.NetworkInfo.ConnectedRelay == "" || peer.NetworkInfo.RelaySessionID == "" {
				continue
			}
		}

		valid = append(valid, peer)
	}

	return valid
}

// scoredPeer holds a peer and its selection score
type scoredPeer struct {
	peer  *database.PeerMetadata
	score float64
}

// smartPeerSelection implements stratified weighted random selection
func (pe *PeerExchange) smartPeerSelection(peers []*database.PeerMetadata, requester *database.PeerMetadata, limit int) []*database.PeerMetadata {
	// Score all peers
	scored := make([]scoredPeer, 0, len(peers))
	for _, peer := range peers {
		score := pe.calculatePeerScore(peer, requester)
		if score > 0 {
			scored = append(scored, scoredPeer{peer, score})
		}
	}

	if len(scored) == 0 {
		return []*database.PeerMetadata{}
	}

	// Stratify into buckets
	relays := pe.filterByType(scored, peerTypeRelay)
	natWithRelay := pe.filterByType(scored, peerTypeNATWithRelay)
	publicPeers := pe.filterByType(scored, peerTypePublic)
	others := pe.filterByType(scored, peerTypeOther)

	// Create deterministic RNG based on requester and our node ID
	var seed int64
	if requester != nil {
		seed = pe.hashNodeIDs(requester.NodeID, pe.ourNodeID)
	} else {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	// Select from each bucket with allocation:
	// - Relays: 25% (critical infrastructure)
	// - NAT with relay: 40% (most common, helps NAT-NAT discovery)
	// - Public peers: 20% (directly reachable)
	// - Others: 15% (variety)

	relayQuota := max(1, limit*25/100)      // At least 1 relay if available
	natQuota := limit * 40 / 100
	publicQuota := limit * 20 / 100
	otherQuota := limit * 15 / 100

	selected := make([]*database.PeerMetadata, 0, limit)
	selected = append(selected, pe.weightedSelect(relays, relayQuota, rng)...)
	selected = append(selected, pe.weightedSelect(natWithRelay, natQuota, rng)...)
	selected = append(selected, pe.weightedSelect(publicPeers, publicQuota, rng)...)
	selected = append(selected, pe.weightedSelect(others, otherQuota, rng)...)

	// If we didn't fill the quota, add more randomly from all buckets
	if len(selected) < limit {
		remaining := limit - len(selected)
		allScored := append(append(append(relays, natWithRelay...), publicPeers...), others...)
		// Remove already selected
		allScored = pe.removeSelected(allScored, selected)
		selected = append(selected, pe.weightedSelect(allScored, remaining, rng)...)
	}

	// Trim to exact limit
	if len(selected) > limit {
		selected = selected[:limit]
	}

	pe.logger.Debug("Selected peers: "+string(rune(len(selected)))+" (relays: "+string(rune(countRelays(selected)))+", NAT: "+string(rune(countNAT(selected)))+")", "peer-exchange")

	return selected
}

// calculatePeerScore assigns a score to a peer based on its usefulness
func (pe *PeerExchange) calculatePeerScore(peer *database.PeerMetadata, requester *database.PeerMetadata) float64 {
	score := 1.0

	// 1. Relay nodes are most valuable (help everyone connect)
	if peer.NetworkInfo.IsRelay {
		score *= 5.0
	}

	// 2. NAT peers with relay are valuable (can be reached via relay)
	if peer.NetworkInfo.UsingRelay && peer.NetworkInfo.ConnectedRelay != "" {
		score *= 3.0
	}

	// 3. Public non-relay peers are moderately valuable (directly reachable)
	if !peer.NetworkInfo.IsRelay && !peer.NetworkInfo.UsingRelay && peer.NetworkInfo.PublicIP != "" {
		score *= 2.0
	}

	// 4. Fresh peers get bonus (recently active)
	timeSinceLastSeen := time.Since(peer.LastSeen)
	if timeSinceLastSeen < 5*time.Minute {
		score *= 2.0
	} else if timeSinceLastSeen < 30*time.Minute {
		score *= 1.5
	} else if timeSinceLastSeen > 1*time.Hour {
		score *= 0.7 // Slight penalty for older peers
	}

	// 5. If requester provided, add diversity bonuses
	if requester != nil {
		// Different relay = different network cluster = more diversity
		if peer.NetworkInfo.ConnectedRelay != "" &&
		   peer.NetworkInfo.ConnectedRelay != requester.NetworkInfo.ConnectedRelay {
			score *= 1.5
		}

		// Different subnet = geographic/network diversity
		if !pe.sameSubnet(peer.NetworkInfo.PublicIP, requester.NetworkInfo.PublicIP) {
			score *= 1.3
		}

		// Same LAN = less valuable to share (requester likely knows already)
		if pe.sameLAN(peer, requester) {
			score *= 0.5
		}
	}

	return score
}

// Peer type classification
type peerType int

const (
	peerTypeRelay peerType = iota
	peerTypeNATWithRelay
	peerTypePublic
	peerTypeOther
)

// filterByType filters scored peers by type
func (pe *PeerExchange) filterByType(peers []scoredPeer, pt peerType) []scoredPeer {
	var filtered []scoredPeer
	for _, sp := range peers {
		switch pt {
		case peerTypeRelay:
			if sp.peer.NetworkInfo.IsRelay {
				filtered = append(filtered, sp)
			}
		case peerTypeNATWithRelay:
			if !sp.peer.NetworkInfo.IsRelay &&
			   sp.peer.NetworkInfo.UsingRelay &&
			   sp.peer.NetworkInfo.ConnectedRelay != "" {
				filtered = append(filtered, sp)
			}
		case peerTypePublic:
			if !sp.peer.NetworkInfo.IsRelay &&
			   !sp.peer.NetworkInfo.UsingRelay &&
			   sp.peer.NetworkInfo.PublicIP != "" {
				filtered = append(filtered, sp)
			}
		case peerTypeOther:
			if !sp.peer.NetworkInfo.IsRelay &&
			   !sp.peer.NetworkInfo.UsingRelay &&
			   sp.peer.NetworkInfo.PublicIP == "" {
				filtered = append(filtered, sp)
			}
		}
	}
	return filtered
}

// weightedSelect performs weighted random selection
func (pe *PeerExchange) weightedSelect(peers []scoredPeer, count int, rng *rand.Rand) []*database.PeerMetadata {
	if len(peers) == 0 {
		return []*database.PeerMetadata{}
	}

	if len(peers) <= count {
		// Return all if we have fewer than requested
		result := make([]*database.PeerMetadata, len(peers))
		for i, sp := range peers {
			result[i] = sp.peer
		}
		return result
	}

	// Calculate total score
	totalScore := 0.0
	for _, sp := range peers {
		totalScore += sp.score
	}

	selected := make([]*database.PeerMetadata, 0, count)
	remaining := make([]scoredPeer, len(peers))
	copy(remaining, peers)

	for i := 0; i < count && len(remaining) > 0; i++ {
		// Recalculate total for remaining peers
		total := 0.0
		for _, sp := range remaining {
			total += sp.score
		}

		// Random selection weighted by score
		r := rng.Float64() * total
		cumulative := 0.0

		for j, sp := range remaining {
			cumulative += sp.score
			if cumulative >= r {
				selected = append(selected, sp.peer)
				// Remove selected peer from remaining
				remaining = append(remaining[:j], remaining[j+1:]...)
				break
			}
		}
	}

	return selected
}

// removeSelected removes already selected peers from scored list
func (pe *PeerExchange) removeSelected(scored []scoredPeer, selected []*database.PeerMetadata) []scoredPeer {
	selectedMap := make(map[string]bool)
	for _, peer := range selected {
		selectedMap[peer.NodeID] = true
	}

	var remaining []scoredPeer
	for _, sp := range scored {
		if !selectedMap[sp.peer.NodeID] {
			remaining = append(remaining, sp)
		}
	}
	return remaining
}

// hashNodeIDs creates a deterministic seed from two node IDs
func (pe *PeerExchange) hashNodeIDs(nodeID1, nodeID2 string) int64 {
	// Sort to ensure same result regardless of order
	combined := nodeID1 + nodeID2
	if nodeID2 < nodeID1 {
		combined = nodeID2 + nodeID1
	}

	hash := sha256.Sum256([]byte(combined))
	return int64(binary.BigEndian.Uint64(hash[:8]))
}

// sameSubnet checks if two IPs are in the same /24 subnet
func (pe *PeerExchange) sameSubnet(ip1, ip2 string) bool {
	if ip1 == "" || ip2 == "" {
		return false
	}

	parsedIP1 := net.ParseIP(ip1)
	parsedIP2 := net.ParseIP(ip2)

	if parsedIP1 == nil || parsedIP2 == nil {
		return false
	}

	// Compare first 3 octets for IPv4 /24
	if parsedIP1.To4() != nil && parsedIP2.To4() != nil {
		ip1Parts := strings.Split(ip1, ".")
		ip2Parts := strings.Split(ip2, ".")

		if len(ip1Parts) >= 3 && len(ip2Parts) >= 3 {
			return ip1Parts[0] == ip2Parts[0] &&
			       ip1Parts[1] == ip2Parts[1] &&
			       ip1Parts[2] == ip2Parts[2]
		}
	}

	return false
}

// sameLAN checks if two peers are on the same LAN
func (pe *PeerExchange) sameLAN(peer1, peer2 *database.PeerMetadata) bool {
	// Same private IP subnet
	if peer1.NetworkInfo.PrivateIP != "" && peer2.NetworkInfo.PrivateIP != "" {
		if pe.sameSubnet(peer1.NetworkInfo.PrivateIP, peer2.NetworkInfo.PrivateIP) {
			return true
		}
	}

	// Same public IP (behind same NAT)
	if peer1.NetworkInfo.PublicIP != "" && peer2.NetworkInfo.PublicIP != "" {
		if peer1.NetworkInfo.PublicIP == peer2.NetworkInfo.PublicIP {
			return true
		}
	}

	return false
}

// convertToPeerInfo converts full metadata to lightweight PeerInfo
func (pe *PeerExchange) convertToPeerInfo(peers []*database.PeerMetadata) []*PeerInfo {
	result := make([]*PeerInfo, len(peers))
	for i, peer := range peers {
		result[i] = &PeerInfo{
			NodeID:         peer.NodeID,
			PublicIP:       peer.NetworkInfo.PublicIP,
			PrivateIP:      peer.NetworkInfo.PrivateIP,
			PublicPort:     peer.NetworkInfo.PublicPort,
			PrivatePort:    peer.NetworkInfo.PrivatePort,
			NodeType:       peer.NetworkInfo.NodeType,
			IsRelay:        peer.NetworkInfo.IsRelay,
			UsingRelay:     peer.NetworkInfo.UsingRelay,
			RelayNodeID:    peer.NetworkInfo.ConnectedRelay,
			RelaySessionID: peer.NetworkInfo.RelaySessionID,
			RelayAddress:   peer.NetworkInfo.RelayAddress,
			Protocols:      peer.NetworkInfo.Protocols,
			LastSeen:       peer.LastSeen.Unix(),
		}
	}
	return result
}

// Helper functions for counting
func countRelays(peers []*database.PeerMetadata) int {
	count := 0
	for _, peer := range peers {
		if peer.NetworkInfo.IsRelay {
			count++
		}
	}
	return count
}

func countNAT(peers []*database.PeerMetadata) int {
	count := 0
	for _, peer := range peers {
		if peer.NetworkInfo.UsingRelay {
			count++
		}
	}
	return count
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
