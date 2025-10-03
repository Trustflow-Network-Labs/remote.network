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
