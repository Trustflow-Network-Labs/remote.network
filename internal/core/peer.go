package core

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)


type TopicState struct {
	Name         string
	InfoHash     [20]byte
	Peers        map[string]krpc.NodeAddr // key: "IP:Port", value: krpc.NodeAddr
	LastAnnounce time.Time
	LastRefresh  time.Time
	mutex        sync.RWMutex
}

type PeerManager struct {
	config         *utils.ConfigManager
	logger         *utils.LogsManager
	dht            *p2p.DHTPeer
	quic           *p2p.QUICPeer
	relayPeer      *p2p.RelayPeer
	relayManager   *p2p.RelayManager
	trafficMonitor *p2p.RelayTrafficMonitor
	natDetector    *p2p.NATDetector
	topologyMgr    *p2p.NATTopologyManager
	dbManager      *database.SQLiteManager
	topics         map[string]*TopicState
	ctx            context.Context
	cancel         context.CancelFunc
	mutex          sync.RWMutex
	running        bool
	maintenanceTicker *time.Ticker
}

func NewPeerManager(config *utils.ConfigManager, logger *utils.LogsManager) (*PeerManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize DHT peer
	dht, err := p2p.NewDHTPeer(config, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT peer: %v", err)
	}

	// Initialize QUIC peer
	quic, err := p2p.NewQUICPeer(config, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create QUIC peer: %v", err)
	}

	// Initialize database manager
	dbManager, err := database.NewSQLiteManager(config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize database manager: %v", err)
	}

	// Initialize NAT detector
	quicPort := config.GetConfigInt("quic_port", 30906, 1024, 65535)
	natDetector := p2p.NewNATDetector(config, logger, quicPort)

	// Initialize NAT topology manager
	topologyMgr := p2p.NewNATTopologyManager(config, logger, dbManager)

	// Initialize relay traffic monitor
	trafficMonitor := p2p.NewRelayTrafficMonitor(config, logger, dbManager)

	// Initialize relay peer if relay mode is enabled
	var relayPeer *p2p.RelayPeer
	var relayManager *p2p.RelayManager

	if config.GetConfigBool("relay_mode", false) {
		relayPeer = p2p.NewRelayPeer(config, logger, dbManager)
		logger.Info("Relay mode enabled - node will act as relay", "core")
	} else {
		// For NAT peers, initialize relay manager for connecting to relays
		relayManager = p2p.NewRelayManager(config, logger, dbManager, quic, dht)
		logger.Info("Relay manager initialized for NAT peer", "core")
	}

	pm := &PeerManager{
		config:         config,
		logger:         logger,
		dht:            dht,
		quic:           quic,
		relayPeer:      relayPeer,
		relayManager:   relayManager,
		trafficMonitor: trafficMonitor,
		natDetector:    natDetector,
		topologyMgr:    topologyMgr,
		dbManager:      dbManager,
		topics:         make(map[string]*TopicState),
		ctx:            ctx,
		cancel:         cancel,
	}

	// Set dependencies on QUIC peer
	quic.SetDependencies(dht, dbManager, relayPeer)

	// Set up relay discovery callback for NAT peers
	if relayManager != nil {
		quic.SetRelayDiscoveryCallback(func(metadata *database.PeerMetadata) {
			if err := relayManager.AddRelayCandidate(metadata); err != nil {
				logger.Debug(fmt.Sprintf("Failed to add relay candidate %s: %v", metadata.NodeID, err), "core")
			} else {
				logger.Debug(fmt.Sprintf("Added relay candidate: %s", metadata.NodeID), "core")
			}
		})
	}

	// Set up connection failure callback for peer reachability verification
	quic.SetConnectionFailureCallback(func(addr string) {
		pm.HandleConnectionFailure(addr)
	})

	// Set up peer discovery callback for QUIC metadata exchange
	dht.SetPeerDiscoveredCallback(func(peerAddr string, topic string) {
		if err := quic.RequestPeerMetadata(peerAddr, topic); err != nil {
			logger.Error(fmt.Sprintf("Failed to request metadata from %s for topic %s: %v", peerAddr, topic, err), "core")
		}
	})

	return pm, nil
}

func (pm *PeerManager) Start() error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if pm.running {
		return fmt.Errorf("peer manager is already running")
	}

	pm.logger.Info("Starting Peer Manager...", "core")

	// Perform NAT detection before starting network services
	pm.logger.Info("Performing NAT detection...", "core")
	natResult, err := pm.natDetector.DetectNATType()
	if err != nil {
		pm.logger.Warn(fmt.Sprintf("NAT detection failed: %v", err), "core")
	} else {
		pm.logger.Info(fmt.Sprintf("NAT Type: %s (Difficulty: %s, Requires Relay: %v)",
			natResult.NATType.String(),
			natResult.NATType.HolePunchingDifficulty(),
			natResult.NATType.RequiresRelay()), "core")

		// Detect local network topology
		topology, err := pm.topologyMgr.DetectLocalTopology(natResult)
		if err != nil {
			pm.logger.Warn(fmt.Sprintf("Topology detection failed: %v", err), "core")
		} else {
			pm.logger.Info(fmt.Sprintf("Network Topology: Public IP=%s, Subnet=%s",
				topology.PublicIP, topology.LocalSubnet), "core")
		}
	}

	// Start QUIC peer FIRST - it needs to be ready to accept connections
	// before DHT begins peer discovery
	if err := pm.quic.Start(); err != nil {
		return fmt.Errorf("failed to start QUIC peer: %v", err)
	}

	// Start DHT peer - this will immediately begin peer discovery
	if err := pm.dht.Start(); err != nil {
		return fmt.Errorf("failed to start DHT peer: %v", err)
	}

	// Start relay manager for NAT peers (after DHT/QUIC are running)
	if pm.relayManager != nil {
		if err := pm.relayManager.Start(); err != nil {
			pm.logger.Warn(fmt.Sprintf("Failed to start relay manager: %v", err), "core")
		} else {
			pm.logger.Info("Relay manager started for NAT peer", "core")
		}
	}

	// Subscribe to configured topics
	topics := pm.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})

	// Release the mutex before subscribing to topics to avoid deadlock
	pm.mutex.Unlock()

	for _, topic := range topics {
		if err := pm.SubscribeToTopic(topic); err != nil {
			pm.logger.Error(fmt.Sprintf("Failed to subscribe to topic '%s': %v", topic, err), "core")
		}
	}

	// Re-acquire mutex to set running state
	pm.mutex.Lock()

	// Start background tasks
	go pm.periodicAnnounce()
	go pm.periodicDiscovery()
	go pm.periodicMaintenance()
	go pm.periodicPeerReachabilityCheck()

	pm.running = true
	pm.logger.Info("Peer Manager started successfully", "core")

	return nil
}

func (pm *PeerManager) SubscribeToTopic(topic string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if _, exists := pm.topics[topic]; exists {
		return fmt.Errorf("already subscribed to topic '%s'", topic)
	}

	pm.logger.Info(fmt.Sprintf("Subscribing to topic: %s", topic), "core")

	topicState := &TopicState{
		Name:     topic,
		InfoHash: pm.dht.TopicToInfoHash(topic),
		Peers:    make(map[string]krpc.NodeAddr),
	}

	pm.topics[topic] = topicState

	// Announce ourselves for this topic
	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	if err := pm.dht.AnnounceForTopic(topic, quicPort); err != nil {
		return fmt.Errorf("failed to announce for topic '%s': %v", topic, err)
	}

	topicState.LastAnnounce = time.Now()

	// Discover existing peers
	go pm.discoverTopicPeers(topic)

	return nil
}

func (pm *PeerManager) UnsubscribeFromTopic(topic string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	topicState, exists := pm.topics[topic]
	if !exists {
		return fmt.Errorf("not subscribed to topic '%s'", topic)
	}

	pm.logger.Info(fmt.Sprintf("Unsubscribing from topic: %s", topic), "core")

	// Close connections to peers in this topic
	topicState.mutex.Lock()
	for peerAddr := range topicState.Peers {
		// Note: We don't close QUIC connections here as they might be used by other topics
		pm.logger.Debug(fmt.Sprintf("Removing peer %s from topic %s", peerAddr, topic), "core")
	}
	topicState.mutex.Unlock()

	delete(pm.topics, topic)
	return nil
}

func (pm *PeerManager) SendMessageToTopic(topic string, message []byte) error {
	pm.mutex.RLock()
	topicState, exists := pm.topics[topic]
	pm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("not subscribed to topic '%s'", topic)
	}

	topicState.mutex.RLock()
	var peers []net.Addr
	for _, peer := range topicState.Peers {
		// Convert krpc.NodeAddr to net.Addr for external interface
		netAddr := &net.UDPAddr{
			IP:   peer.IP,
			Port: peer.Port,
		}
		peers = append(peers, netAddr)
	}
	topicState.mutex.RUnlock()

	if len(peers) == 0 {
		pm.logger.Warn(fmt.Sprintf("No peers found for topic '%s'", topic), "core")
		return nil
	}

	pm.logger.Info(fmt.Sprintf("Broadcasting message to %d peers in topic '%s'", len(peers), topic), "core")

	return pm.quic.BroadcastMessage(peers, message)
}

func (pm *PeerManager) GetTopicPeers(topic string) ([]net.Addr, error) {
	pm.mutex.RLock()
	topicState, exists := pm.topics[topic]
	pm.mutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("not subscribed to topic '%s'", topic)
	}

	topicState.mutex.RLock()
	defer topicState.mutex.RUnlock()

	var peers []net.Addr
	for _, peer := range topicState.Peers {
		// Convert krpc.NodeAddr to net.Addr for external interface
		netAddr := &net.UDPAddr{
			IP:   peer.IP,
			Port: peer.Port,
		}
		peers = append(peers, netAddr)
	}

	return peers, nil
}

func (pm *PeerManager) GetTopics() []string {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var topics []string
	for topic := range pm.topics {
		topics = append(topics, topic)
	}

	return topics
}

func (pm *PeerManager) periodicAnnounce() {
	announceInterval := pm.config.GetConfigDuration("topic_announce_interval", 60*time.Second)
	ticker := time.NewTicker(announceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.mutex.RLock()
			topics := make(map[string]*TopicState)
			for name, state := range pm.topics {
				topics[name] = state
			}
			pm.mutex.RUnlock()

			quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)

			for topic, state := range topics {
				if time.Since(state.LastAnnounce) >= announceInterval {
					pm.logger.Debug(fmt.Sprintf("Re-announcing for topic: %s", topic), "core")
					if err := pm.dht.AnnounceForTopic(topic, quicPort); err != nil {
						pm.logger.Error(fmt.Sprintf("Failed to re-announce for topic '%s': %v", topic, err), "core")
					} else {
						state.LastAnnounce = time.Now()
					}
				}
			}
		}
	}
}

func (pm *PeerManager) periodicDiscovery() {
	discoveryInterval := pm.config.GetConfigDuration("peer_discovery_interval", 30*time.Second)
	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.mutex.RLock()
			topics := make([]string, 0, len(pm.topics))
			for topic := range pm.topics {
				topics = append(topics, topic)
			}
			pm.mutex.RUnlock()

			for _, topic := range topics {
				go pm.discoverTopicPeers(topic)
			}
		}
	}
}

// periodicMaintenance performs periodic database maintenance tasks
func (pm *PeerManager) periodicMaintenance() {
	// Run maintenance every hour by default
	maintenanceInterval := pm.config.GetConfigDuration("maintenance_interval", 1*time.Hour)
	pm.maintenanceTicker = time.NewTicker(maintenanceInterval)
	defer pm.maintenanceTicker.Stop()

	// Run initial maintenance after 5 minutes
	time.Sleep(5 * time.Minute)
	pm.runMaintenance()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-pm.maintenanceTicker.C:
			pm.runMaintenance()
		}
	}
}

// runMaintenance executes database maintenance tasks
func (pm *PeerManager) runMaintenance() {
	pm.logger.Info("Running periodic database maintenance...", "core")

	if pm.dbManager != nil {
		if err := pm.dbManager.PerformMaintenance(); err != nil {
			pm.logger.Error(fmt.Sprintf("Database maintenance failed: %v", err), "core")
		} else {
			pm.logger.Info("Database maintenance completed successfully", "core")
		}
	}
}

func (pm *PeerManager) discoverTopicPeers(topic string) {
	pm.logger.Info(fmt.Sprintf("Discovering peers for topic: %s", topic), "core")

	peers, err := pm.dht.FindPeersForTopic(topic)
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to discover peers for topic '%s': %v", topic, err), "core")
		return
	}

	pm.logger.Info(fmt.Sprintf("Discovery completed for topic '%s': found %d peers", topic, len(peers)), "core")

	if len(peers) == 0 {
		return
	}

	pm.mutex.RLock()
	topicState, exists := pm.topics[topic]
	pm.mutex.RUnlock()

	if !exists {
		return // Topic was unsubscribed
	}

	topicState.mutex.Lock()
	newPeerCount := 0
	for _, peer := range peers {
		// Use IP:Port as the key for storage
		peerKey := fmt.Sprintf("%s:%d", peer.IP.String(), peer.Port)
		if _, exists := topicState.Peers[peerKey]; !exists {
			topicState.Peers[peerKey] = peer
			newPeerCount++
			pm.logger.Debug(fmt.Sprintf("Added new peer %s for topic '%s'", peerKey, topic), "core")

			// Trigger metadata exchange for newly discovered peer
			quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)
			quicAddr := fmt.Sprintf("%s:%d", peer.IP.String(), quicPort)
			go func(addr, topicName string) {
				if err := pm.quic.RequestPeerMetadata(addr, topicName); err != nil {
					pm.logger.Error(fmt.Sprintf("Failed to request metadata from discovered peer %s for topic %s: %v", addr, topicName, err), "core")
				}
			}(quicAddr, topic)
		}
	}
	topicState.LastRefresh = time.Now()
	topicState.mutex.Unlock()

	if newPeerCount > 0 {
		pm.logger.Info(fmt.Sprintf("Discovered %d new peers for topic '%s' (total: %d)",
			newPeerCount, topic, len(topicState.Peers)), "core")
	}
}

func (pm *PeerManager) GetStats() map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := map[string]interface{}{
		"running":          pm.running,
		"dht_stats":        pm.dht.GetStats(),
		"dht_node_id":      pm.dht.NodeID(),
		"quic_connections": pm.quic.GetConnectionCount(),
		"topics":           make(map[string]interface{}),
	}

	// Add NAT detection info
	if pm.natDetector != nil {
		natResult := pm.natDetector.GetLastResult()
		if natResult != nil {
			stats["nat_type"] = natResult.NATType.String()
			stats["nat_difficulty"] = natResult.NATType.HolePunchingDifficulty()
			stats["requires_relay"] = natResult.NATType.RequiresRelay()
			stats["public_endpoint"] = fmt.Sprintf("%s:%d", natResult.PublicIP, natResult.PublicPort)
		}
	}

	// Add topology info
	if pm.topologyMgr != nil {
		stats["topology"] = pm.topologyMgr.GetTopologyStats()
	}

	// Add relay info
	if pm.relayPeer != nil {
		stats["relay_mode"] = true
		stats["relay_stats"] = pm.relayPeer.GetStats()
	}
	if pm.relayManager != nil {
		stats["relay_manager_stats"] = pm.relayManager.GetStats()
	}
	if pm.trafficMonitor != nil {
		stats["traffic_stats"] = pm.trafficMonitor.GetStats()
	}

	topicStats := make(map[string]interface{})
	for name, state := range pm.topics {
		state.mutex.RLock()
		topicStats[name] = map[string]interface{}{
			"peer_count":    len(state.Peers),
			"infohash":      fmt.Sprintf("%x", state.InfoHash),
			"last_announce": state.LastAnnounce,
			"last_refresh":  state.LastRefresh,
		}
		state.mutex.RUnlock()
	}
	stats["topics"] = topicStats

	return stats
}

func (pm *PeerManager) Stop() error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if !pm.running {
		return nil
	}

	pm.logger.Info("Stopping Peer Manager...", "core")

	pm.cancel()

	// Stop maintenance ticker
	if pm.maintenanceTicker != nil {
		pm.maintenanceTicker.Stop()
	}

	// Stop relay components
	if pm.relayManager != nil {
		pm.relayManager.Stop()
	}
	if pm.relayPeer != nil {
		pm.relayPeer.Stop()
	}
	if pm.trafficMonitor != nil {
		pm.trafficMonitor.Stop()
	}

	// Stop QUIC peer
	if err := pm.quic.Stop(); err != nil {
		pm.logger.Error(fmt.Sprintf("Error stopping QUIC peer: %v", err), "core")
	}

	// Stop DHT peer
	if err := pm.dht.Stop(); err != nil {
		pm.logger.Error(fmt.Sprintf("Error stopping DHT peer: %v", err), "core")
	}

	pm.running = false
	pm.logger.Info("Peer Manager stopped", "core")

	return nil
}

// periodicPeerReachabilityCheck periodically verifies peer reachability and removes unreachable peers
func (pm *PeerManager) periodicPeerReachabilityCheck() {
	// Check frequently (default 5 minutes) to find stale peers (older than 24h)
	checkInterval := pm.config.GetConfigDuration("peer_reachability_check_interval", 5*time.Minute)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pm.logger.Info("Starting periodic peer reachability check...", "core")
			pm.verifyAllPeerReachability()

		case <-pm.ctx.Done():
			pm.logger.Debug("Stopping peer reachability check", "core")
			return
		}
	}
}

// verifyAllPeerReachability checks peers that haven't been seen recently and removes unreachable ones
func (pm *PeerManager) verifyAllPeerReachability() {
	pm.logger.Info("Verifying peer reachability for stale peers...", "core")

	// Get all topics to iterate through peer metadata
	pm.mutex.RLock()
	topics := make([]string, 0, len(pm.topics))
	for topic := range pm.topics {
		topics = append(topics, topic)
	}
	pm.mutex.RUnlock()

	// Get the age threshold - peers older than this need verification
	maxAge := pm.config.GetConfigDuration("peer_metadata_max_age", 24*time.Hour)
	cutoff := time.Now().Add(-maxAge)

	reachableCount := 0
	unreachableCount := 0
	checkedPeers := 0
	skippedPeers := 0

	for _, topic := range topics {
		// Get all peer metadata for this topic
		allMetadata, err := pm.dbManager.PeerMetadata.GetPeersByTopic(topic)
		if err != nil {
			pm.logger.Error(fmt.Sprintf("Failed to get peer metadata for topic %s: %v", topic, err), "core")
			continue
		}

		for _, metadata := range allMetadata {
			// Only verify peers that are older than the cutoff (1 day by default)
			if metadata.LastSeen.After(cutoff) {
				// Peer is fresh, skip verification
				skippedPeers++
				continue
			}

			checkedPeers++
			pm.logger.Debug(fmt.Sprintf("Peer %s is stale (last seen: %v ago), verifying reachability",
				metadata.NodeID, time.Since(metadata.LastSeen)), "core")

			reachable := pm.verifyPeerReachability(metadata)
			if reachable {
				reachableCount++
				// Update LastSeen to prevent re-checking for another day
				if err := pm.dbManager.PeerMetadata.UpdateLastSeen(metadata.NodeID, topic, "reachability-check"); err != nil {
					pm.logger.Error(fmt.Sprintf("Failed to update LastSeen for peer %s: %v", metadata.NodeID, err), "core")
				} else {
					pm.logger.Debug(fmt.Sprintf("Peer %s is reachable, updated LastSeen", metadata.NodeID), "core")
				}
			} else {
				unreachableCount++
				// Remove unreachable peer
				if err := pm.dbManager.PeerMetadata.DeletePeerMetadata(metadata.NodeID, topic); err != nil {
					pm.logger.Error(fmt.Sprintf("Failed to delete unreachable peer %s: %v", metadata.NodeID, err), "core")
				} else {
					endpoint := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
					pm.logger.Info(fmt.Sprintf("Removed unreachable peer: %s (endpoint: %s, last seen: %v ago)",
						metadata.NodeID, endpoint, time.Since(metadata.LastSeen)), "core")
				}
			}
		}
	}

	pm.logger.Info(fmt.Sprintf("Peer reachability check complete: %d peers checked, %d still reachable (LastSeen updated), %d removed, %d skipped (fresh)",
		checkedPeers, reachableCount, unreachableCount, skippedPeers), "core")
}

// verifyPeerReachability checks if a specific peer is reachable
func (pm *PeerManager) verifyPeerReachability(metadata *database.PeerMetadata) bool {
	// Case 1: Regular peer or relay peer (not using relay) - direct ping
	if !metadata.NetworkInfo.UsingRelay {
		endpoint := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
		if metadata.NetworkInfo.PublicIP == "" || metadata.NetworkInfo.PublicPort == 0 {
			pm.logger.Debug(fmt.Sprintf("Peer %s has no public endpoint, marking unreachable", metadata.NodeID), "core")
			return false
		}

		err := pm.quic.Ping(endpoint)
		if err != nil {
			pm.logger.Debug(fmt.Sprintf("Direct ping to peer %s (%s) failed: %v", metadata.NodeID, endpoint, err), "core")
			return false
		}

		pm.logger.Debug(fmt.Sprintf("Peer %s is reachable via direct ping", metadata.NodeID), "core")
		return true
	}

	// Case 2: NAT peer using relay - verify through its relay
	relayNodeID := metadata.NetworkInfo.ConnectedRelay
	if relayNodeID == "" {
		pm.logger.Debug(fmt.Sprintf("NAT peer %s has no relay info, marking unreachable", metadata.NodeID), "core")
		return false
	}

	// Get relay peer metadata - try to find it in any topic
	var relayMetadata *database.PeerMetadata
	pm.mutex.RLock()
	topics := make([]string, 0, len(pm.topics))
	for topic := range pm.topics {
		topics = append(topics, topic)
	}
	pm.mutex.RUnlock()

	for _, topic := range topics {
		rm, err := pm.dbManager.PeerMetadata.GetPeerMetadata(relayNodeID, topic)
		if err == nil {
			relayMetadata = rm
			break
		}
	}

	if relayMetadata == nil {
		pm.logger.Debug(fmt.Sprintf("NAT peer %s relay %s not found in database, marking unreachable", metadata.NodeID, relayNodeID), "core")
		return false
	}

	// First verify relay is reachable
	relayEndpoint := relayMetadata.NetworkInfo.RelayEndpoint
	if relayEndpoint == "" {
		relayEndpoint = fmt.Sprintf("%s:%d", relayMetadata.NetworkInfo.PublicIP, relayMetadata.NetworkInfo.PublicPort)
	}
	if relayEndpoint == "" {
		pm.logger.Debug(fmt.Sprintf("Relay %s has no endpoint, NAT peer %s marked unreachable", relayNodeID, metadata.NodeID), "core")
		return false
	}

	err := pm.quic.Ping(relayEndpoint)
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Relay %s unreachable, NAT peer %s marked unreachable", relayNodeID, metadata.NodeID), "core")
		// Also remove the unreachable relay from all topics
		for _, topic := range topics {
			if err := pm.dbManager.PeerMetadata.DeletePeerMetadata(relayNodeID, topic); err != nil {
				pm.logger.Debug(fmt.Sprintf("Failed to delete unreachable relay %s from topic %s: %v", relayNodeID, topic, err), "core")
			}
		}
		pm.logger.Info(fmt.Sprintf("Removed unreachable relay: %s", relayNodeID), "core")
		return false
	}

	// Query relay to verify NAT peer has active session
	hasActiveSession, err := pm.quic.QueryRelayForSession(relayEndpoint, metadata.NodeID, pm.dht.NodeID())
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Failed to query relay %s for NAT peer %s session: %v", relayNodeID, metadata.NodeID, err), "core")
		// If query fails but relay is reachable, consider it a communication error, not peer unreachability
		// Don't remove the peer - it will be checked again next cycle
		return true
	}

	if !hasActiveSession {
		pm.logger.Debug(fmt.Sprintf("NAT peer %s has no active session on relay %s", metadata.NodeID, relayNodeID), "core")
		return false
	}

	pm.logger.Debug(fmt.Sprintf("NAT peer %s has active session on relay %s", metadata.NodeID, relayNodeID), "core")
	return true
}

// HandleConnectionFailure handles a connection failure to an address by looking up the peer and verifying reachability
func (pm *PeerManager) HandleConnectionFailure(addr string) {
	// Parse address to get IP and port
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Failed to parse address %s: %v", addr, err), "core")
		return
	}

	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	// Find which peer has this address
	pm.mutex.RLock()
	topics := make([]string, 0, len(pm.topics))
	for topic := range pm.topics {
		topics = append(topics, topic)
	}
	pm.mutex.RUnlock()

	for _, topic := range topics {
		allMetadata, err := pm.dbManager.PeerMetadata.GetPeersByTopic(topic)
		if err != nil {
			continue
		}

		for _, metadata := range allMetadata {
			// Check if this peer matches the failed address
			if metadata.NetworkInfo.PublicIP == host && metadata.NetworkInfo.PublicPort == port {
				pm.logger.Debug(fmt.Sprintf("Connection failure to %s matched peer %s in topic %s", addr, metadata.NodeID, topic), "core")
				pm.VerifyPeerReachabilityOnFailure(metadata.NodeID, topic)
				return
			}
		}
	}

	pm.logger.Debug(fmt.Sprintf("Connection failure to %s but no matching peer found in database", addr), "core")
}

// VerifyPeerReachabilityOnFailure verifies and cleans up peer on connection failure
// This is called when a connection attempt to a peer fails
func (pm *PeerManager) VerifyPeerReachabilityOnFailure(nodeID string, topic string) {
	metadata, err := pm.dbManager.PeerMetadata.GetPeerMetadata(nodeID, topic)
	if err != nil || metadata == nil {
		pm.logger.Debug(fmt.Sprintf("Peer %s not in database for topic %s, skipping reachability check", nodeID, topic), "core")
		return
	}

	pm.logger.Debug(fmt.Sprintf("Connection to peer %s failed, verifying reachability", nodeID), "core")

	reachable := pm.verifyPeerReachability(metadata)
	if reachable {
		pm.logger.Debug(fmt.Sprintf("Peer %s is still reachable despite connection failure, keeping it", nodeID), "core")
		return
	}

	// Peer is unreachable, remove it
	if err := pm.dbManager.PeerMetadata.DeletePeerMetadata(nodeID, topic); err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to delete unreachable peer %s: %v", nodeID, err), "core")
	} else {
		pm.logger.Info(fmt.Sprintf("Removed unreachable peer after connection failure: %s (topic: %s)", nodeID, topic), "core")
	}
}
