package p2p

import (
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// KnownPeer represents a minimal peer entry (peer_id, public_key)
// This is the new lightweight storage model for Phase 2
type KnownPeer struct {
	PeerID     string    // SHA1(public_key) - 40 hex chars (Ed25519-derived peer_id)
	DHTNodeID  string    // DHT node_id - 40 hex chars (for DHT routing, may differ from PeerID)
	PublicKey  []byte    // Ed25519 public key - 32 bytes
	IsRelay    bool      // Is this peer a relay? (from identity exchange)
	IsStore    bool      // Has BEP_44 storage enabled? (from identity exchange)
	FilesCount int       // Count of ACTIVE DATA services (from peer metadata)
	AppsCount  int       // Count of ACTIVE DOCKER + STANDALONE services (from peer metadata)
	LastSeen   time.Time // Last time we saw this peer
	Topic      string    // Topic this peer is associated with
	FirstSeen  time.Time // When we first discovered this peer
	Source     string    // "bootstrap", "peer_exchange", "dht", etc.

	// NAT and Relay status (from DHT metadata)
	IsBehindNAT      bool   // Is this peer behind NAT? (NodeType == "private")
	NATType          string // NAT type: "full_cone", "restricted", "port_restricted", "symmetric", ""
	UsingRelay       bool   // Is this peer currently using a relay?
	ConnectedRelayID string // PeerID of the relay this peer is connected to (if using relay)
}

// KnownPeersManager manages in-memory known peers storage
type KnownPeersManager struct {
	mu     sync.RWMutex
	peers  map[string]*KnownPeer // key: "peer_id:topic"
	logger *utils.LogsManager
}

// makeKnownPeerKey creates a composite key from peer_id and topic
func makeKnownPeerKey(peerID, topic string) string {
	return peerID + ":" + topic
}

// NewKnownPeersManager creates a new in-memory known peers manager
func NewKnownPeersManager(logger *utils.LogsManager) *KnownPeersManager {
	kpm := &KnownPeersManager{
		peers:  make(map[string]*KnownPeer),
		logger: logger,
	}

	logger.Debug("Initialized in-memory known peers manager", "p2p")
	return kpm
}

// StoreKnownPeer stores or updates a known peer
func (kpm *KnownPeersManager) StoreKnownPeer(peer *KnownPeer) error {
	now := time.Now()
	if peer.LastSeen.IsZero() {
		peer.LastSeen = now
	}
	if peer.FirstSeen.IsZero() {
		peer.FirstSeen = now
	}
	if peer.Source == "" {
		peer.Source = "peer_exchange"
	}

	key := makeKnownPeerKey(peer.PeerID, peer.Topic)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	// Check if peer already exists
	existing, exists := kpm.peers[key]
	if exists {
		// Update existing peer (like ON CONFLICT DO UPDATE)
		existing.DHTNodeID = peer.DHTNodeID
		existing.LastSeen = peer.LastSeen
		existing.IsStore = peer.IsStore
		// Note: NAT/Relay fields are NOT updated here to avoid overwriting
		// correct values with zero values. Use UpdatePeerNetworkInfo() instead.
		// Keep original FirstSeen, Source, and NAT/Relay fields
	} else {
		// Insert new peer (make a copy to avoid external modifications)
		newPeer := &KnownPeer{
			PeerID:           peer.PeerID,
			DHTNodeID:        peer.DHTNodeID,
			PublicKey:        make([]byte, len(peer.PublicKey)),
			IsRelay:          peer.IsRelay,
			IsStore:          peer.IsStore,
			FilesCount:       peer.FilesCount,
			AppsCount:        peer.AppsCount,
			LastSeen:         peer.LastSeen,
			Topic:            peer.Topic,
			FirstSeen:        peer.FirstSeen,
			Source:           peer.Source,
			IsBehindNAT:      peer.IsBehindNAT,
			NATType:          peer.NATType,
			UsingRelay:       peer.UsingRelay,
			ConnectedRelayID: peer.ConnectedRelayID,
		}
		copy(newPeer.PublicKey, peer.PublicKey)
		kpm.peers[key] = newPeer
	}

	kpm.logger.Debug(fmt.Sprintf("Stored known peer: %s (topic: %s, source: %s)",
		peer.PeerID, peer.Topic, peer.Source), "p2p")

	return nil
}

// UpdateLastSeen updates only the last_seen timestamp for a peer
func (kpm *KnownPeersManager) UpdateLastSeen(peerID, topic string) error {
	key := makeKnownPeerKey(peerID, topic)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	peer, exists := kpm.peers[key]
	if !exists {
		return nil // Silently ignore if peer doesn't exist (matches SQL behavior)
	}

	peer.LastSeen = time.Now()
	return nil
}

// UpdatePeerServiceCounts updates only the service counts for a peer
// This should be called when we have confirmed service counts from DHT metadata
func (kpm *KnownPeersManager) UpdatePeerServiceCounts(peerID, topic string, filesCount, appsCount int) error {
	key := makeKnownPeerKey(peerID, topic)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	peer, exists := kpm.peers[key]
	if !exists {
		return fmt.Errorf("peer not found: %s (topic: %s)", peerID, topic)
	}

	peer.FilesCount = filesCount
	peer.AppsCount = appsCount

	kpm.logger.Debug(fmt.Sprintf("Updated service counts for peer %s (files: %d, apps: %d)",
		peerID[:8], filesCount, appsCount), "p2p")

	return nil
}

// UpdatePeerNetworkInfo updates NAT and relay connection status for a peer
// This should be called when we have network info from DHT metadata
func (kpm *KnownPeersManager) UpdatePeerNetworkInfo(peerID, topic string, isBehindNAT bool, natType string, usingRelay bool, connectedRelayID string) error {
	key := makeKnownPeerKey(peerID, topic)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	peer, exists := kpm.peers[key]
	if !exists {
		return fmt.Errorf("peer not found: %s (topic: %s)", peerID, topic)
	}

	peer.IsBehindNAT = isBehindNAT
	peer.NATType = natType
	peer.UsingRelay = usingRelay
	peer.ConnectedRelayID = connectedRelayID

	kpm.logger.Debug(fmt.Sprintf("Updated network info for peer %s (behind_nat: %v, nat_type: %s, using_relay: %v, relay: %s)",
		peerID[:8], isBehindNAT, natType, usingRelay, connectedRelayID), "p2p")

	return nil
}

// GetKnownPeerByNodeID retrieves a known peer by DHT node_id or peer_id
// This is useful when you have the DHT node_id from peer_metadata and need the public key
func (kpm *KnownPeersManager) GetKnownPeerByNodeID(nodeID, topic string) (*KnownPeer, error) {
	// First try as peer_id (Ed25519-derived)
	peer, err := kpm.GetKnownPeer(nodeID, topic)
	if err != nil {
		return nil, err
	}
	if peer != nil {
		return peer, nil
	}

	// Try as dht_node_id (DHT routing node_id) - requires iteration
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	for _, p := range kpm.peers {
		if p.DHTNodeID == nodeID && p.Topic == topic {
			// Return a copy to prevent external modifications
			return kpm.copyPeer(p), nil
		}
	}

	return nil, nil // Not found by either ID
}

// GetKnownPeer retrieves a single known peer by peer_id
func (kpm *KnownPeersManager) GetKnownPeer(peerID, topic string) (*KnownPeer, error) {
	key := makeKnownPeerKey(peerID, topic)

	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	peer, exists := kpm.peers[key]
	if !exists {
		return nil, nil // Peer not found
	}

	// Return a copy to prevent external modifications
	return kpm.copyPeer(peer), nil
}

// GetAllKnownPeers retrieves all known peers (sorted by last_seen DESC)
func (kpm *KnownPeersManager) GetAllKnownPeers() ([]*KnownPeer, error) {
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	peers := make([]*KnownPeer, 0, len(kpm.peers))
	for _, p := range kpm.peers {
		peers = append(peers, kpm.copyPeer(p))
	}

	// Sort by last_seen DESC
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen)
	})

	return peers, nil
}

// GetKnownPeersByTopic retrieves all known peers for a specific topic
func (kpm *KnownPeersManager) GetKnownPeersByTopic(topic string) ([]*KnownPeer, error) {
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	var peers []*KnownPeer
	for _, p := range kpm.peers {
		if p.Topic == topic {
			peers = append(peers, kpm.copyPeer(p))
		}
	}

	// Sort by last_seen DESC
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen)
	})

	return peers, nil
}

// GetRecentKnownPeers retrieves the N most recently seen peers
func (kpm *KnownPeersManager) GetRecentKnownPeers(limit int, topic string) ([]*KnownPeer, error) {
	peers, err := kpm.GetKnownPeersByTopic(topic)
	if err != nil {
		return nil, err
	}

	// Already sorted by GetKnownPeersByTopic, just apply limit
	if limit > 0 && len(peers) > limit {
		peers = peers[:limit]
	}

	return peers, nil
}

// DeleteKnownPeer deletes a known peer
func (kpm *KnownPeersManager) DeleteKnownPeer(peerID, topic string) error {
	key := makeKnownPeerKey(peerID, topic)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	delete(kpm.peers, key)

	kpm.logger.Debug(fmt.Sprintf("Deleted known peer: %s (topic: %s)", peerID, topic), "p2p")
	return nil
}

// CleanupStalePeers removes peers that haven't been seen in the specified duration
func (kpm *KnownPeersManager) CleanupStalePeers(maxAge time.Duration) (int, error) {
	cutoffTime := time.Now().Add(-maxAge)

	kpm.mu.Lock()
	defer kpm.mu.Unlock()

	var keysToDelete []string
	for key, p := range kpm.peers {
		if p.LastSeen.Before(cutoffTime) {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(kpm.peers, key)
	}

	if len(keysToDelete) > 0 {
		kpm.logger.Info(fmt.Sprintf("Cleaned up %d stale known peers (max_age: %v)",
			len(keysToDelete), maxAge), "p2p")
	}

	return len(keysToDelete), nil
}

// GetKnownPeersCount returns the total number of known peers
func (kpm *KnownPeersManager) GetKnownPeersCount() (int, error) {
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	return len(kpm.peers), nil
}

// GetKnownPeersCountByTopic returns the count of known peers for a topic
func (kpm *KnownPeersManager) GetKnownPeersCountByTopic(topic string) (int, error) {
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	count := 0
	for _, p := range kpm.peers {
		if p.Topic == topic {
			count++
		}
	}

	return count, nil
}

// GetRelayPeers returns all relay peers for a topic
func (kpm *KnownPeersManager) GetRelayPeers(topic string) ([]*KnownPeer, error) {
	kpm.mu.RLock()
	defer kpm.mu.RUnlock()

	var peers []*KnownPeer
	for _, p := range kpm.peers {
		if p.Topic == topic && p.IsRelay {
			peers = append(peers, kpm.copyPeer(p))
		}
	}

	// Sort by last_seen DESC
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen)
	})

	return peers, nil
}

// copyPeer creates a deep copy of a KnownPeer to prevent external modifications
func (kpm *KnownPeersManager) copyPeer(p *KnownPeer) *KnownPeer {
	peerCopy := &KnownPeer{
		PeerID:           p.PeerID,
		DHTNodeID:        p.DHTNodeID,
		PublicKey:        make([]byte, len(p.PublicKey)),
		IsRelay:          p.IsRelay,
		IsStore:          p.IsStore,
		FilesCount:       p.FilesCount,
		AppsCount:        p.AppsCount,
		LastSeen:         p.LastSeen,
		Topic:            p.Topic,
		FirstSeen:        p.FirstSeen,
		Source:           p.Source,
		IsBehindNAT:      p.IsBehindNAT,
		NATType:          p.NATType,
		UsingRelay:       p.UsingRelay,
		ConnectedRelayID: p.ConnectedRelayID,
	}
	peerCopy.PublicKey = append(peerCopy.PublicKey[:0], p.PublicKey...)
	return peerCopy
}

// PublicKeyHex returns the public key as a hex string (for debugging)
func (kp *KnownPeer) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey)
}

// Close is a no-op for in-memory storage (kept for interface compatibility)
func (kpm *KnownPeersManager) Close() error {
	kpm.logger.Debug("Closed in-memory known peers manager", "p2p")
	return nil
}
