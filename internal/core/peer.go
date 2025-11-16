package core

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/services"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
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
	config                *utils.ConfigManager
	logger                *utils.LogsManager
	keyPair               *crypto.KeyPair
	dht                   *p2p.DHTPeer
	quic                  *p2p.QUICPeer
	relayPeer             *p2p.RelayPeer
	relayManager          *p2p.RelayManager
	trafficMonitor        *p2p.RelayTrafficMonitor
	natDetector           *p2p.NATDetector
	topologyMgr           *p2p.NATTopologyManager
	networkStateMonitor   *p2p.NetworkStateMonitor
	holePuncher           *p2p.HolePuncher
	// Phase 4: DHT-based metadata services (replaces MetadataBroadcaster)
	metadataPublisher       *p2p.MetadataPublisher
	metadataFetcher         *p2p.MetadataFetcher
	metadataQuery           *p2p.MetadataQueryService
	metadataRetryScheduler  *p2p.MetadataRetryScheduler
	peerValidator           *p2p.PeerValidator
	periodicDiscoveryMgr    *p2p.PeriodicDiscovery
	connectabilityFilter    *p2p.ConnectabilityFilter
	peerDiscovery           *p2p.PeerDiscoveryService
	dbManager             *database.SQLiteManager
	// Job and workflow management
	jobManager            *JobManager
	workflowManager       *WorkflowManager
	topics                map[string]*TopicState
	ctx                   context.Context
	cancel                context.CancelFunc
	mutex                 sync.RWMutex
	running               bool
	startTime             time.Time
	maintenanceTicker     *time.Ticker
}

func NewPeerManager(config *utils.ConfigManager, logger *utils.LogsManager, keyPair *crypto.KeyPair) (*PeerManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize paths first - needed for TLS certificates
	paths := utils.GetAppPaths("")

	// KeyPair is now passed in from encrypted keystore (loaded in root.go)
	logger.Info(fmt.Sprintf("Using Ed25519 keypair (peer_id: %s)", keyPair.PeerID()), "core")

	// Initialize DHT peer
	dht, err := p2p.NewDHTPeer(config, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT peer: %v", err)
	}

	// Initialize QUIC peer with Ed25519-based TLS certificates
	quic, err := p2p.NewQUICPeer(config, logger, paths, keyPair)
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

	// Initialize hole puncher for NAT traversal (will be set on QUIC peer later)
	var holePuncher *p2p.HolePuncher

	// Initialize relay peer if relay mode is enabled
	var relayPeer *p2p.RelayPeer
	var relayManager *p2p.RelayManager

	// Phase 4: Initialize DHT-based metadata services first
	// These services work alongside the existing broadcaster/PEX for gradual transition
	bep44Manager := p2p.NewBEP44Manager(dht, logger, config)
	logger.Info("BEP_44 manager initialized for DHT mutable data", "core")

	// Initialize metadata publisher for publishing our own metadata to DHT
	metadataPublisher := p2p.NewMetadataPublisher(bep44Manager, keyPair, logger, config, dbManager)
	logger.Info("Metadata publisher initialized for DHT updates", "core")

	// Initialize identity exchanger for Phase 3 identity + known peers exchange
	identityExchanger := p2p.NewIdentityExchanger(keyPair, dht.NodeID(), dbManager, logger, config)
	logger.Info("Identity exchanger initialized for QUIC handshakes", "core")

	// Set identity exchanger on QUIC peer
	quic.SetIdentityExchanger(identityExchanger)

	// Initialize service query handler for service discovery
	serviceQueryHandler := p2p.NewServiceQueryHandler(dbManager, logger)
	quic.SetServiceQueryHandler(serviceQueryHandler)
	logger.Info("Service query handler initialized for service discovery", "core")

	// Initialize metadata fetcher for DHT-only metadata retrieval with priority routing
	metadataFetcher := p2p.NewMetadataFetcher(bep44Manager, logger)
	logger.Info("Metadata fetcher initialized (DHT priority queries)", "core")

	metadataQuery := p2p.NewMetadataQueryService(bep44Manager, dbManager, logger, config, metadataFetcher)
	logger.Info("Metadata query service initialized with DHT store priority", "core")

	// Set metadata fetcher on identity exchanger for service count preservation
	identityExchanger.SetMetadataFetcher(metadataFetcher)

	// Initialize metadata retry scheduler for failed metadata fetches
	metadataRetryScheduler := p2p.NewMetadataRetryScheduler(config, logger, metadataFetcher, dbManager)
	logger.Info("Metadata retry scheduler initialized", "core")

	// Initialize relay peer/manager (needs metadata publisher and fetcher)
	if config.GetConfigBool("relay_mode", false) {
		relayPeer = p2p.NewRelayPeer(config, logger, dbManager)
		logger.Info("Relay mode enabled - node will act as relay", "core")
	} else {
		// For NAT peers, initialize relay manager for connecting to relays
		relayManager = p2p.NewRelayManager(config, logger, dbManager, keyPair, quic, dht, metadataPublisher, metadataFetcher, metadataQuery)
		logger.Info("Relay manager initialized for NAT peer", "core")
	}

	// Initialize peer validator for stale peer cleanup (validates 24h+ old peers via DHT)
	peerValidator := p2p.NewPeerValidator(metadataFetcher, dbManager, logger, config)
	logger.Info("Peer validator initialized (stale peer cleanup via DHT)", "core")

	connectabilityFilter := p2p.NewConnectabilityFilter(logger)
	logger.Info("Connectability filter initialized (peer reachability detection)", "core")

	peerDiscovery := p2p.NewPeerDiscoveryService(metadataQuery, connectabilityFilter, dbManager, logger, config)
	logger.Info("Peer discovery service initialized (on-demand peer filtering)", "core")

	// Initialize periodic discovery for 3-hour DHT rediscovery
	periodicDiscoveryMgr := p2p.NewPeriodicDiscovery(peerDiscovery, dbManager, logger, config)
	logger.Info("Periodic discovery initialized (3-hour DHT rediscovery)", "core")

	// Initialize network state monitor for IP/NAT change detection
	// Will be started after initial detection in Start()
	networkStateMonitor := p2p.NewNetworkStateMonitor(config, logger, natDetector, topologyMgr, metadataPublisher, relayManager)
	logger.Info("Network state monitor initialized (IP/NAT change detection)", "core")

	pm := &PeerManager{
		config:                  config,
		logger:                  logger,
		keyPair:                 keyPair,
		dht:                     dht,
		quic:                    quic,
		relayPeer:               relayPeer,
		relayManager:            relayManager,
		trafficMonitor:          trafficMonitor,
		natDetector:             natDetector,
		topologyMgr:             topologyMgr,
		networkStateMonitor:     networkStateMonitor,
		holePuncher:             holePuncher,
		metadataPublisher:       metadataPublisher,
		metadataFetcher:         metadataFetcher,
		metadataQuery:           metadataQuery,
		metadataRetryScheduler:  metadataRetryScheduler,
		peerValidator:           peerValidator,
		periodicDiscoveryMgr:    periodicDiscoveryMgr,
		connectabilityFilter:    connectabilityFilter,
		peerDiscovery:           peerDiscovery,
		dbManager:               dbManager,
		topics:                  make(map[string]*TopicState),
		ctx:                     ctx,
		cancel:                  cancel,
	}

	// Set dependencies on QUIC peer
	quic.SetDependencies(dht, dbManager, relayPeer)

	// Initialize and set hole puncher if not in relay mode
	if !config.GetConfigBool("relay_mode", false) && config.GetConfigBool("hole_punch_enabled", true) {
		pm.holePuncher = p2p.NewHolePuncher(config, logger, quic, dht, dbManager, metadataFetcher, natDetector)
		quic.SetHolePuncher(pm.holePuncher)
		logger.Info("Hole puncher initialized for NAT traversal", "core")
	}

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

	// Set up connection ready callback - triggered after identity exchange completes
	quic.SetConnectionReadyCallback(func(peerID string, remoteAddr string) {
		logger.Info(fmt.Sprintf("Connection ready: peer %s at %s (identity exchange complete)", peerID[:8], remoteAddr), "core")
		// Can be used to trigger additional actions when peer is authenticated and ready
	})

	// Note: Connection failure callback removed - peer cleanup handled by PeerValidator
	// Note: QUIC metadata exchange removed - metadata comes from DHT only
	// Peer discovery stores peer IDs in known_peers during identity exchange
	// Metadata is fetched from DHT on-demand when needed

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

	// Start hole puncher for NAT traversal (after NAT detection is complete)
	if pm.holePuncher != nil {
		if err := pm.holePuncher.Start(); err != nil {
			pm.logger.Warn(fmt.Sprintf("Failed to start hole puncher: %v", err), "core")
		} else {
			pm.logger.Info("Hole puncher started for NAT traversal", "core")
		}
	}

	// Connect to bootstrap peers for identity exchange (Phase 1: Bootstrap)
	// This must happen BEFORE metadata publishing to get known_peers
	pm.logger.Info("Connecting to bootstrap peers for identity exchange...", "core")
	pm.connectToBootstrapPeers()

	// Publish initial metadata to DHT (Phase 4: DHT metadata architecture)
	// For NAT peers, this creates and stores metadata via SetInitialMetadata()
	// The metadata MUST be ready before relay manager starts
	pm.logger.Info("Publishing initial metadata to DHT...", "core")
	if err := pm.publishInitialMetadata(); err != nil {
		pm.logger.Warn(fmt.Sprintf("Failed to publish initial metadata: %v", err), "core")
		// Don't fail startup if DHT publishing fails
	}

	// Start relay manager for NAT peers (after metadata is initialized)
	// This ensures metadata exists when relay connection is established
	// and NotifyRelayConnected() can successfully update and publish it
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

	// Start network state monitor for IP/NAT change detection
	if pm.networkStateMonitor != nil {
		if err := pm.networkStateMonitor.Start(); err != nil {
			pm.logger.Warn(fmt.Sprintf("Failed to start network state monitor: %v", err), "core")
		} else {
			pm.logger.Info("Network state monitor started for IP/NAT change detection", "core")
		}
	}

	// Start background tasks
	pm.logger.Debug("Starting periodic announce goroutine", "core")
	go pm.periodicAnnounce()

	pm.logger.Debug("Starting periodic discovery goroutine", "core")
	go pm.periodicDiscovery()

	pm.logger.Debug("Starting periodic maintenance goroutine", "core")
	go pm.periodicMaintenance()

	pm.logger.Debug("Starting periodic bootstrap re-query goroutine", "core")
	go pm.periodicBootstrapRequeryLoop()

	pm.startTime = time.Now()
	pm.running = true
	pm.logger.Info("Peer Manager started successfully with 5 background services", "core")

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
	pm.logger.Debug(fmt.Sprintf("Spawning initial discovery goroutine for new topic subscription: %s", topic), "core")
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

	pm.logger.Debug(fmt.Sprintf("Periodic discovery loop started (interval: %v)", discoveryInterval), "core")

	for {
		select {
		case <-pm.ctx.Done():
			pm.logger.Debug("Periodic discovery loop stopped", "core")
			return
		case <-ticker.C:
			pm.mutex.RLock()
			topics := make([]string, 0, len(pm.topics))
			for topic := range pm.topics {
				topics = append(topics, topic)
			}
			pm.mutex.RUnlock()

			pm.logger.Info(fmt.Sprintf("Periodic discovery tick - spawning %d discovery goroutines", len(topics)), "core")

			for _, topic := range topics {
				pm.logger.Debug(fmt.Sprintf("Spawning discovery goroutine for topic: %s", topic), "core")
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

// periodicBootstrapRequeryLoop performs periodic bootstrap re-queries to discover new peers
// Runs independently of DHT discovery with its own interval (default: 3 hours)
func (pm *PeerManager) periodicBootstrapRequeryLoop() {
	// Get bootstrap re-query interval from config (default: 3 hours)
	intervalHours := pm.config.GetConfigInt("periodic_bootstrap_requery_interval_hours", 3, 1, 24)
	interval := time.Duration(intervalHours) * time.Hour

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	pm.logger.Info(fmt.Sprintf("Starting periodic bootstrap re-query loop (interval: %v)", interval), "core")

	// Wait for initial delay before first re-query (same as periodic discovery interval)
	// This gives time for the network to stabilize after startup
	time.Sleep(interval)

	for {
		select {
		case <-pm.ctx.Done():
			pm.logger.Info("Stopping periodic bootstrap re-query loop", "core")
			return
		case <-ticker.C:
			pm.logger.Debug("Periodic bootstrap re-query triggered", "core")

			// Get all subscribed topics and re-query for each
			pm.mutex.RLock()
			topics := make([]string, 0, len(pm.topics))
			for topic := range pm.topics {
				topics = append(topics, topic)
			}
			pm.mutex.RUnlock()

			for _, topic := range topics {
				pm.periodicBootstrapRequery(topic)
			}
		}
	}
}

func (pm *PeerManager) discoverTopicPeers(topic string) {
	pm.logger.Debug(fmt.Sprintf("Discovery goroutine started for topic: %s", topic), "core")

	peers, err := pm.dht.FindPeersForTopic(topic)
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to discover peers for topic '%s': %v", topic, err), "core")
		pm.logger.Debug(fmt.Sprintf("Discovery goroutine ended (error) for topic: %s", topic), "core")
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
			pm.logger.Debug(fmt.Sprintf("Discovered peer %s for topic '%s' (will connect after metadata is available)", peerKey, topic), "core")

			// Note: Metadata-first strategy - we do NOT attempt immediate connections
			// Instead, discovered peers will be connected to via:
			// 1. Metadata queries (periodic discovery fetches metadata from DHT)
			// 2. connectToKnownPeers() connects to peers with available metadata
			// This ensures we always have metadata before attempting connections
		}
	}
	topicState.LastRefresh = time.Now()
	topicState.mutex.Unlock()

	if newPeerCount > 0 {
		pm.logger.Info(fmt.Sprintf("Discovered %d new peers for topic '%s' (total: %d)",
			newPeerCount, topic, len(topicState.Peers)), "core")
	}

	pm.logger.Debug(fmt.Sprintf("Discovery goroutine ended for topic: %s (new: %d, total: %d)",
		topic, newPeerCount, len(topicState.Peers)), "core")
}

func (pm *PeerManager) GetStats() map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	nodeID := pm.dht.NodeID()
	peerID := pm.keyPair.PeerID() // Persistent peer ID derived from Ed25519 public key
	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)

	// Calculate uptime in seconds
	var uptimeSeconds float64
	if !pm.startTime.IsZero() {
		uptimeSeconds = time.Since(pm.startTime).Seconds()
	}

	stats := map[string]interface{}{
		"running":          pm.running,
		"uptime_seconds":   uptimeSeconds,
		"dht_stats":        pm.dht.GetStats(),
		"dht_node_id":      nodeID,      // DHT node ID (changes on restart)
		"peer_id":          peerID,      // Persistent peer ID (based on Ed25519 keypair)
		"quic_port":        quicPort,    // Configured QUIC port (for private endpoint)
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

	// Add node type and relay info
	if pm.relayPeer != nil {
		// Node is acting as a relay server
		stats["node_type"] = "public"
		stats["relay_mode"] = true
		relayStats := pm.relayPeer.GetStats()
		stats["relay_stats"] = relayStats

		// Also include key relay stats at top level for easier frontend access
		if pricingPerGB, ok := relayStats["pricing_per_gb"].(float64); ok {
			stats["pricing_per_gb"] = pricingPerGB
		}
		if maxConn, ok := relayStats["max_connections"].(int); ok {
			stats["max_connections"] = maxConn
		}
		if activeSessions, ok := relayStats["active_sessions"].(int); ok {
			stats["active_sessions"] = activeSessions
		}
		if totalBytes, ok := relayStats["total_bytes"].(int64); ok {
			stats["total_bytes"] = totalBytes
		}
	} else if pm.relayManager != nil {
		// Node is a NAT peer using relays (client mode)
		stats["node_type"] = "private"
		stats["relay_mode"] = false
	} else {
		// Node is a public peer (no relay server, no relay manager)
		stats["node_type"] = "public"
		stats["relay_mode"] = false
	}

	if pm.relayManager != nil {
		relayManagerStats := pm.relayManager.GetStats()
		stats["relay_manager_stats"] = relayManagerStats

		// Also include current relay session at top level for NAT mode
		if connectedRelay, ok := relayManagerStats["connected_relay"]; ok && connectedRelay != nil {
			stats["current_relay_session"] = map[string]interface{}{
				"relay_peer_id":    relayManagerStats["connected_relay_peer_id"], // Persistent Ed25519-based peer ID
				"relay_node_id":    relayManagerStats["connected_relay"],         // DHT node ID
				"endpoint":         relayManagerStats["connected_relay_endpoint"],
				"latency":          relayManagerStats["connected_relay_latency"],
				"latency_ms":       relayManagerStats["connected_relay_latency_ms"],
				"pricing":          relayManagerStats["connected_relay_pricing"],
				"duration_seconds": relayManagerStats["session_duration_seconds"],
				"ingress_bytes":    relayManagerStats["session_ingress_bytes"],
				"egress_bytes":     relayManagerStats["session_egress_bytes"],
				"total_bytes":      relayManagerStats["session_total_bytes"],
				"current_cost":     relayManagerStats["session_current_cost"],
			}
		}
	}
	if pm.trafficMonitor != nil {
		stats["traffic_stats"] = pm.trafficMonitor.GetStats()
	}

	// Add hole puncher metrics
	if pm.holePuncher != nil {
		stats["hole_punch_metrics"] = pm.holePuncher.GetMetrics()
	}

	// Add network state monitor stats
	if pm.networkStateMonitor != nil {
		stats["network_monitor"] = pm.networkStateMonitor.GetStats()
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

// GetDBManager returns the database manager instance
func (pm *PeerManager) GetDBManager() *database.SQLiteManager {
	return pm.dbManager
}

// GetRelayPeer returns the relay peer instance
func (pm *PeerManager) GetRelayPeer() *p2p.RelayPeer {
	return pm.relayPeer
}

// GetRelayManager returns the relay manager instance
func (pm *PeerManager) GetRelayManager() *p2p.RelayManager {
	return pm.relayManager
}

// InitializeJobSystem initializes the job and workflow management system
func (pm *PeerManager) InitializeJobSystem() error {
	pm.logger.Info("Initializing job and workflow management system", "core")

	// Create JobManager
	pm.jobManager = NewJobManager(pm.ctx, pm.dbManager, pm.config, pm)
	pm.logger.Info("JobManager created", "core")

	// Create WorkflowManager
	pm.workflowManager = NewWorkflowManager(pm.ctx, pm.dbManager, pm.config, pm.jobManager, pm)
	pm.logger.Info("WorkflowManager created", "core")

	// Create JobMessageHandler for QUIC communication
	jobHandler := p2p.NewJobMessageHandler(pm.config, pm.quic)
	pm.quic.SetJobHandler(jobHandler)
	pm.logger.Info("JobMessageHandler created and set on QUIC peer", "core")

	// Set dependencies for relay forwarding
	jobHandler.SetDependencies(pm.dbManager, pm.metadataQuery, pm.GetPeerID())
	pm.logger.Info("JobMessageHandler dependencies set (relay forwarding enabled)", "core")

	// Set up callbacks for job operations
	jobHandler.SetCallbacks(
		pm.handleJobRequest,
		pm.handleJobStatusUpdate,
		pm.handleJobStatusRequest,
		pm.handleJobDataTransferRequest,
		pm.handleJobDataChunk,
		pm.handleJobDataTransferComplete,
		pm.handleJobCancel,
	)
	pm.logger.Info("JobMessageHandler callbacks configured", "core")

	// Set job handler on data worker
	if pm.jobManager.dataWorker != nil {
		pm.jobManager.dataWorker.SetJobHandler(jobHandler)
		pm.jobManager.dataWorker.SetPeerID(pm.GetPeerID())
		pm.logger.Info("JobHandler and PeerID set on DataServiceWorker", "core")
	}

	// Set up relay reconnection callback for transfer resumption
	if pm.relayManager != nil {
		pm.relayManager.SetRelayReconnectedCallback(func() {
			pm.logger.Info("Relay reconnected - checking for active transfers to resume", "core")
			pm.checkAndResumeActiveTransfers()
		})
		pm.logger.Info("Relay reconnection callback configured for transfer resumption", "core")
	}

	// Check for active transfers from previous session and attempt to resume them
	pm.logger.Info("Checking for active transfers from previous session", "core")
	pm.checkAndResumeActiveTransfers()

	return nil
}

// StartJobSystem starts the job and workflow management system
func (pm *PeerManager) StartJobSystem() error {
	if pm.jobManager == nil || pm.workflowManager == nil {
		return fmt.Errorf("job system not initialized")
	}

	pm.logger.Info("Starting job and workflow management system", "core")

	// Start JobManager
	if err := pm.jobManager.Start(); err != nil {
		return fmt.Errorf("failed to start JobManager: %v", err)
	}

	// Start WorkflowManager
	if err := pm.workflowManager.Start(); err != nil {
		return fmt.Errorf("failed to start WorkflowManager: %v", err)
	}

	pm.logger.Info("Job and workflow management system started successfully", "core")
	return nil
}

// GetJobManager returns the job manager instance
func (pm *PeerManager) GetJobManager() *JobManager {
	return pm.jobManager
}

// GetWorkflowManager returns the workflow manager instance
func (pm *PeerManager) GetWorkflowManager() *WorkflowManager {
	return pm.workflowManager
}

// GetJobHandler returns the job message handler instance
func (pm *PeerManager) GetJobHandler() *p2p.JobMessageHandler {
	if pm.quic == nil {
		return nil
	}
	return pm.quic.GetJobHandler()
}

// GetQUICPeer returns the QUIC peer instance
func (pm *PeerManager) GetQUICPeer() *p2p.QUICPeer {
	return pm.quic
}

// GetMetadataQueryService returns the metadata query service instance
func (pm *PeerManager) GetMetadataQueryService() *p2p.MetadataQueryService {
	return pm.metadataQuery
}

// Job message handler callbacks
func (pm *PeerManager) handleJobRequest(request *types.JobExecutionRequest, peerID string) (*types.JobExecutionResponse, error) {
	pm.logger.Info(fmt.Sprintf("Received job request from peer %s for workflow %d", peerID[:8], request.WorkflowID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobRequest(request, peerID)
	}

	return &types.JobExecutionResponse{
		WorkflowJobID: request.WorkflowJobID,
		Accepted:      false,
		Message:       "job manager not initialized",
	}, fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobStatusUpdate(update *types.JobStatusUpdate, peerID string) error {
	pm.logger.Info(fmt.Sprintf("Received job status update from peer %s for job %d: status=%s", peerID[:8], update.JobExecutionID, update.Status), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobStatusUpdate(update, peerID)
	}

	return fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobStatusRequest(request *types.JobStatusRequest, peerID string) (*types.JobStatusResponse, error) {
	pm.logger.Debug(fmt.Sprintf("Received job status request from peer %s for job %d", peerID[:8], request.JobExecutionID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobStatusRequest(request, peerID)
	}

	return nil, fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobDataTransferRequest(request *types.JobDataTransferRequest, peerID string) (*types.JobDataTransferResponse, error) {
	pm.logger.Info(fmt.Sprintf("Received job data transfer request from peer %s for workflow job %d", peerID[:8], request.WorkflowJobID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobDataTransferRequest(request, peerID)
	}

	return &types.JobDataTransferResponse{
		WorkflowJobID: request.WorkflowJobID,
		Accepted:      false,
		Message:       "job manager not initialized",
	}, fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobDataChunk(chunk *types.JobDataChunk, peerID string) error {
	pm.logger.Debug(fmt.Sprintf("Received job data chunk from peer %s for transfer %s", peerID[:8], chunk.TransferID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobDataChunk(chunk, peerID)
	}

	return fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobDataTransferComplete(complete *types.JobDataTransferComplete, peerID string) error {
	pm.logger.Info(fmt.Sprintf("Received job data transfer complete from peer %s for transfer %s", peerID[:8], complete.TransferID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobDataTransferComplete(complete, peerID)
	}

	return fmt.Errorf("job manager not initialized")
}

func (pm *PeerManager) handleJobCancel(request *types.JobCancelRequest, peerID string) (*types.JobCancelResponse, error) {
	pm.logger.Info(fmt.Sprintf("Received job cancel request from peer %s for job %d", peerID[:8], request.JobExecutionID), "core")

	// Delegate to JobManager
	if pm.jobManager != nil {
		return pm.jobManager.HandleJobCancel(request, peerID)
	}

	return &types.JobCancelResponse{
		JobExecutionID: request.JobExecutionID,
		Cancelled:      false,
		Message:        "job manager not initialized",
	}, fmt.Errorf("job manager not initialized")
}

// checkAndResumeActiveTransfers checks for active transfers and resumes stalled ones
func (pm *PeerManager) checkAndResumeActiveTransfers() {
	// Get all active transfers from database
	activeTransfers, err := pm.dbManager.GetActiveTransfers()
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to get active transfers: %v", err), "core")
		return
	}

	if len(activeTransfers) == 0 {
		pm.logger.Debug("No active transfers found", "core")
		return
	}

	pm.logger.Info(fmt.Sprintf("Found %d active transfer(s) to potentially resume", len(activeTransfers)), "core")

	// For each active sender transfer, check if we should resume
	for _, transfer := range activeTransfers {
		if transfer.Direction != "sender" {
			pm.logger.Debug(fmt.Sprintf("Transfer %s is receiver-side, skipping (receiver is passive)", transfer.TransferID), "core")
			continue // Only resume sender-side transfers (receiver is passive)
		}

		// Check if transfer is stalled (no activity for 30+ seconds)
		if time.Since(transfer.LastActivity) < 30*time.Second {
			pm.logger.Debug(fmt.Sprintf("Transfer %s is recent (last activity: %s ago), skipping",
				transfer.TransferID, time.Since(transfer.LastActivity)), "core")
			continue
		}

		pm.logger.Info(fmt.Sprintf("Found stalled transfer %s (last activity: %s ago, %d/%d chunks sent, %d acked)",
			transfer.TransferID, time.Since(transfer.LastActivity), len(transfer.ChunksSent), transfer.TotalChunks, len(transfer.ChunksAcked)), "core")

		// CRITICAL: Query DHT for fresh peer metadata before resuming
		// NAT peers may have reconnected to different relay
		pm.logger.Debug(fmt.Sprintf("Querying DHT for fresh metadata of peer %s before resuming transfer", transfer.DestinationPeerID[:8]), "core")

		// Get peer's public key from known_peers
		peer, err := pm.dbManager.KnownPeers.GetKnownPeer(transfer.DestinationPeerID, "")
		if err != nil {
			pm.logger.Error(fmt.Sprintf("Failed to get peer %s from known_peers: %v", transfer.DestinationPeerID[:8], err), "core")
			// Mark very old transfers as failed
			if time.Since(transfer.LastActivity) > 5*time.Minute {
				pm.dbManager.FailTransfer(transfer.TransferID, fmt.Sprintf("Cannot get peer info: %v", err))
			}
			continue
		}

		// Fetch fresh metadata from DHT
		metadata, err := pm.metadataFetcher.GetPeerMetadata(peer.PublicKey)
		if err != nil {
			pm.logger.Warn(fmt.Sprintf("Failed to fetch metadata for peer %s: %v", transfer.DestinationPeerID[:8], err), "core")
			// Don't fail transfer yet - might just be temporary DHT issue
			// Only fail if it's been >5 minutes
			if time.Since(transfer.LastActivity) > 5*time.Minute {
				pm.dbManager.FailTransfer(transfer.TransferID, fmt.Sprintf("Cannot fetch peer metadata: %v", err))
			}
			continue
		}

		// Check if peer is using relay and if relay changed
		if metadata.NetworkInfo.UsingRelay {
			pm.logger.Debug(fmt.Sprintf("Peer %s is using relay: %s (session: %s)",
				transfer.DestinationPeerID[:8], metadata.NetworkInfo.ConnectedRelay[:8], metadata.NetworkInfo.RelaySessionID), "core")

			// Note: We don't have the old relay session ID stored in transfer record
			// But the connection attempt will use the fresh metadata, so it will work
			pm.logger.Info(fmt.Sprintf("Peer %s relay info refreshed from DHT", transfer.DestinationPeerID[:8]), "core")
		}

		// Attempt to resume transfer
		if pm.jobManager != nil && pm.jobManager.dataWorker != nil {
			pm.logger.Info(fmt.Sprintf("Attempting to resume transfer %s to peer %s",
				transfer.TransferID, transfer.DestinationPeerID[:8]), "core")

			err := pm.jobManager.dataWorker.ResumeTransfer(transfer, metadata)
			if err != nil {
				pm.logger.Error(fmt.Sprintf("Failed to resume transfer %s: %v", transfer.TransferID, err), "core")

				// Mark very old transfers as failed
				if time.Since(transfer.LastActivity) > 5*time.Minute {
					pm.dbManager.FailTransfer(transfer.TransferID, fmt.Sprintf("Resume failed: %v", err))
				}
			} else {
				pm.logger.Info(fmt.Sprintf("Successfully initiated resume for transfer %s", transfer.TransferID), "core")
			}
		} else {
			pm.logger.Warn("JobManager or DataWorker not available, cannot resume transfers", "core")
		}
	}
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

	// Stop hole puncher
	if pm.holePuncher != nil {
		if err := pm.holePuncher.Stop(); err != nil {
			pm.logger.Warn(fmt.Sprintf("Error stopping hole puncher: %v", err), "core")
		}
	}

	// Stop metadata publisher
	if pm.metadataPublisher != nil {
		pm.metadataPublisher.StopPeriodicRepublish()
		pm.logger.Info("Metadata publisher stopped", "core")
	}

	// Stop network state monitor
	if pm.networkStateMonitor != nil {
		pm.networkStateMonitor.Stop()
		pm.logger.Info("Network state monitor stopped", "core")
	}

	// Stop job and workflow managers
	if pm.workflowManager != nil {
		pm.workflowManager.Stop()
		pm.logger.Info("Workflow manager stopped", "core")
	}
	if pm.jobManager != nil {
		pm.jobManager.Stop()
		pm.logger.Info("Job manager stopped", "core")
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

// publishInitialMetadata creates and publishes our initial metadata to the DHT
func (pm *PeerManager) publishInitialMetadata() error {
	// Get our node information
	nodeTypeManager := utils.NewNodeTypeManager()
	publicIP, _ := nodeTypeManager.GetExternalIP()
	privateIP, _ := nodeTypeManager.GetLocalIP()
	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)

	// Get the first subscribed topic (or default)
	topics := pm.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	if len(topics) == 0 {
		return fmt.Errorf("no topics configured")
	}
	topic := topics[0]

	// Determine node type
	nodeType := "public"
	isRelayNode := false
	if pm.relayManager != nil {
		nodeType = "private" // NAT peer
	} else {
		// Check if relay mode is enabled for public nodes
		isRelayNode = pm.config.GetConfigBool("relay_mode", false)
	}

	// Create initial metadata
	networkInfo := database.NetworkInfo{
		PublicIP:    publicIP,
		PrivateIP:   privateIP,
		PublicPort:  quicPort,
		PrivatePort: quicPort,
		NodeType:    nodeType,
		Protocols: []database.Protocol{
			{Name: "quic", Port: quicPort},
		},
		UsingRelay:     false, // Will be updated when relay connects
		ConnectedRelay: "",
		RelaySessionID: "",
		RelayAddress:   "",
	}

	// If this is a relay node, add relay service information
	if isRelayNode {
		networkInfo.IsRelay = true
		networkInfo.RelayEndpoint = fmt.Sprintf("%s:%d", publicIP, quicPort)

		// Get relay pricing from config (default: 0.001 per GB = 1000 micro-units)
		relayPricing := pm.config.GetConfigFloat64("relay_pricing_per_gb", 0.001, 0.0, 1.0)
		networkInfo.RelayPricing = int(relayPricing * 1000000.0) // Convert to micro-units

		// Get relay capacity from config (default: 100 concurrent sessions)
		networkInfo.RelayCapacity = pm.config.GetConfigInt("relay_capacity", 100, 1, 10000)

		// Initial reputation score (0.5 = 5000 basis points)
		networkInfo.ReputationScore = 5000

		pm.logger.Info(fmt.Sprintf("Relay service enabled: endpoint=%s, pricing=%.4f, capacity=%d",
			networkInfo.RelayEndpoint, relayPricing, networkInfo.RelayCapacity), "core")
	}

	// Count local services for metadata
	serviceCounts, err := services.CountLocalServices(pm.dbManager)
	if err != nil {
		pm.logger.Warn(fmt.Sprintf("Failed to count local services: %v", err), "core")
		serviceCounts = &services.ServiceCounts{} // Default to 0
	}
	pm.logger.Info(fmt.Sprintf("Counted local services for metadata: files=%d, apps=%d", serviceCounts.FilesCount, serviceCounts.AppsCount), "core")

	metadata := &database.PeerMetadata{
		PeerID:       pm.keyPair.PeerID(), // Persistent Ed25519-based peer ID
		NodeID:       pm.dht.NodeID(),     // DHT routing node ID
		Topic:        topic,
		Version:      1,
		NetworkInfo:  networkInfo,
		Capabilities: []string{"metadata_exchange"},
		FilesCount:   serviceCounts.FilesCount, // Count of ACTIVE DATA services
		AppsCount:    serviceCounts.AppsCount,  // Count of ACTIVE DOCKER + STANDALONE services
		Extensions:   make(map[string]interface{}),
		Timestamp:    time.Now(),
		LastSeen:     time.Now(),
		Source:       "self_publish",
	}

	// Conditional metadata publishing based on node type
	// - Public nodes: Publish immediately (they are directly reachable)
	// - Relay nodes: Publish immediately (they provide relay service)
	// - NAT nodes: Defer publishing until relay connection is established
	if nodeType == "public" || isRelayNode {
		// Publish to DHT immediately for public/relay nodes
		if err := pm.metadataPublisher.PublishMetadata(metadata); err != nil {
			return fmt.Errorf("failed to publish initial metadata: %v", err)
		}

		// Start periodic republishing to keep metadata alive in DHT
		pm.metadataPublisher.StartPeriodicRepublish()

		pm.logger.Info(fmt.Sprintf("Published initial metadata to DHT (node_id: %s, type: %s)", metadata.NodeID, nodeType), "core")

		// Connect to known peers after metadata publishing (metadata-first strategy)
		go pm.connectToKnownPeers(topic)
	} else {
		// NAT node - defer publishing until relay connection is established
		pm.logger.Info(fmt.Sprintf("NAT node detected - deferring metadata publishing until relay connection (node_id: %s)", metadata.NodeID), "core")

		// Store the metadata for later publishing when relay connects
		// The relay manager will call metadataPublisher.NotifyRelayConnected() to publish with relay info
		pm.metadataPublisher.SetInitialMetadata(metadata)

		// Set callback to connect to known peers after NAT metadata is published
		pm.metadataPublisher.SetNATMetadataPublishedCallback(func() {
			pm.logger.Info("NAT metadata published to DHT, connecting to known peers...", "core")
			pm.connectToKnownPeers(topic)
		})
	}

	// Note: Local metadata storage removed - metadata is DHT-only now

	// Start peer validator for stale peer cleanup (validates 24h+ old peers every 6 hours)
	pm.peerValidator.StartPeriodicValidation(topic)
	pm.logger.Info("Started peer validator for stale peer cleanup", "core")

	// Start periodic discovery for DHT rediscovery (discovers new peers every 3 hours)
	pm.periodicDiscoveryMgr.StartPeriodicDiscovery(topic)
	pm.logger.Info("Started periodic discovery for DHT rediscovery", "core")

	// Start metadata retry scheduler for failed metadata fetches
	pm.metadataRetryScheduler.StartRetryLoop()
	pm.logger.Info("Started metadata retry scheduler", "core")

	return nil
}

// connectToBootstrapPeers connects to bootstrap peers for initial identity exchange
// This happens BEFORE metadata publishing to populate known_peers database
func (pm *PeerManager) connectToBootstrapPeers() {
	defaultBootstrap := []string{"159.65.253.245:30609", "167.86.116.185:30609"}
	// Use custom_bootstrap_nodes (our network nodes with QUIC servers)
	// NOT dht_bootstrap_nodes (which includes global BitTorrent DHT nodes without QUIC)
	bootstrapNodes := pm.config.GetBootstrapNodes("custom_bootstrap_nodes", defaultBootstrap)
	if len(bootstrapNodes) == 0 {
		pm.logger.Warn("No custom bootstrap nodes configured", "core")
		return
	}

	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	pm.logger.Info(fmt.Sprintf("Bootstrap phase starting: connecting to %d custom bootstrap peers (QUIC port: %d)",
		len(bootstrapNodes), quicPort), "core")

	// Get our external IP to avoid self-connections
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, err := nodeTypeManager.GetExternalIP()
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Could not determine external IP: %v (self-connection detection disabled)", err), "core")
	} else {
		pm.logger.Debug(fmt.Sprintf("Our external IP: %s (will skip self-connections)", ourExternalIP), "core")
	}

	var successCount, failureCount, skippedCount int

	for i, bootstrap := range bootstrapNodes {
		// Parse bootstrap address (format: IP:DHT_PORT)
		// We need to connect to QUIC port instead
		host, _, err := net.SplitHostPort(bootstrap)
		if err != nil {
			failureCount++
			pm.logger.Debug(fmt.Sprintf("Bootstrap attempt %d/%d FAILED: invalid address %s: %v",
				i+1, len(bootstrapNodes), bootstrap, err), "core")
			continue
		}

		// Skip self-connections
		if ourExternalIP != "" && host == ourExternalIP {
			skippedCount++
			pm.logger.Debug(fmt.Sprintf("Bootstrap attempt %d/%d SKIPPED: self-connection to %s",
				i+1, len(bootstrapNodes), bootstrap), "core")
			continue
		}

		// Connect to bootstrap peer via QUIC port
		quicAddr := fmt.Sprintf("%s:%d", host, quicPort)
		pm.logger.Debug(fmt.Sprintf("Bootstrap attempt %d/%d: connecting to %s...",
			i+1, len(bootstrapNodes), quicAddr), "core")

		conn, err := pm.quic.ConnectToPeer(quicAddr)
		if err != nil {
			failureCount++
			pm.logger.Warn(fmt.Sprintf("Bootstrap attempt %d/%d FAILED to %s: %v",
				i+1, len(bootstrapNodes), quicAddr, err), "core")
			continue
		}

		successCount++
		pm.logger.Info(fmt.Sprintf("Bootstrap attempt %d/%d SUCCESS: connected to %s (identity exchange completed)",
			i+1, len(bootstrapNodes), quicAddr), "core")

		// Connection will be managed by QUIC layer, identity exchange happens automatically
		_ = conn
	}

	pm.logger.Info(fmt.Sprintf("Bootstrap connection phase complete: %d successful, %d failed, %d skipped out of %d total",
		successCount, failureCount, skippedCount, len(bootstrapNodes)), "core")

	// Give time for identity exchanges to complete and peers to be stored
	pm.logger.Debug("Waiting 2 seconds for identity exchanges to complete...", "core")
	time.Sleep(2 * time.Second)

	// Verify peers were stored in database
	topics := pm.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	if len(topics) > 0 {
		topic := topics[0]
		peerCount, err := pm.dbManager.KnownPeers.GetKnownPeersCountByTopic(topic)
		if err != nil {
			pm.logger.Error(fmt.Sprintf("Post-bootstrap verification failed: cannot query known_peers: %v", err), "core")
		} else {
			pm.logger.Info(fmt.Sprintf("Post-bootstrap verification: %d peers in known_peers database", peerCount), "core")

			if peerCount == 0 && successCount > 0 {
				pm.logger.Error("CRITICAL: No peers in known_peers after successful bootstrap connections! Identity exchange may have failed.", "core")
			} else if peerCount > 0 {
				pm.logger.Info(fmt.Sprintf("Bootstrap verification SUCCESS: %d peers successfully stored", peerCount), "core")
			}
		}
	}
}

// periodicBootstrapRequery periodically re-connects to bootstrap peers to exchange updated known_peers lists
// This allows peers to discover new peers that joined the network after their initial bootstrap
// Should be called as part of periodic discovery (every 3 hours alongside DHT rediscovery)
func (pm *PeerManager) periodicBootstrapRequery(topic string) {
	pm.logger.Info("Performing periodic bootstrap re-query for updated peer lists...", "core")

	defaultBootstrap := []string{"159.65.253.245:30609", "167.86.116.185:30609"}
	bootstrapNodes := pm.config.GetBootstrapNodes("custom_bootstrap_nodes", defaultBootstrap)
	if len(bootstrapNodes) == 0 {
		pm.logger.Warn("No custom bootstrap nodes configured for re-query", "core")
		return
	}

	// Get current peer count from database
	initialPeers, err := pm.dbManager.KnownPeers.GetKnownPeersByTopic(topic)
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to get initial peer count: %v", err), "core")
		return
	}
	initialCount := len(initialPeers)
	pm.logger.Debug(fmt.Sprintf("Current known peers count: %d", initialCount), "core")

	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	pm.logger.Info(fmt.Sprintf("Re-querying %d bootstrap peers for updated peer lists...", len(bootstrapNodes)), "core")

	successCount := 0
	for _, bootstrap := range bootstrapNodes {
		// Parse bootstrap address (format: IP:DHT_PORT)
		host, _, err := net.SplitHostPort(bootstrap)
		if err != nil {
			pm.logger.Debug(fmt.Sprintf("Failed to parse bootstrap address %s: %v", bootstrap, err), "core")
			continue
		}

		// Re-connect to bootstrap peer via QUIC port
		quicAddr := fmt.Sprintf("%s:%d", host, quicPort)
		pm.logger.Debug(fmt.Sprintf("Re-connecting to bootstrap peer at %s...", quicAddr), "core")

		conn, err := pm.quic.ConnectToPeer(quicAddr)
		if err != nil {
			pm.logger.Debug(fmt.Sprintf("Failed to re-connect to bootstrap peer %s: %v", quicAddr, err), "core")
			continue
		}

		pm.logger.Info(fmt.Sprintf("Successfully re-connected to bootstrap peer %s (identity/peer exchange in progress)", quicAddr), "core")
		successCount++

		// Connection will be managed by QUIC layer
		// Identity exchange happens automatically, which includes known_peers exchange
		_ = conn
	}

	pm.logger.Info(fmt.Sprintf("Bootstrap re-query complete: %d/%d successful", successCount, len(bootstrapNodes)), "core")

	// Give time for identity/peer exchanges to complete
	time.Sleep(2 * time.Second)

	// Check if we discovered new peers
	updatedPeers, err := pm.dbManager.KnownPeers.GetKnownPeersByTopic(topic)
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to get updated peer count: %v", err), "core")
		return
	}
	updatedCount := len(updatedPeers)
	newPeerCount := updatedCount - initialCount

	if newPeerCount > 0 {
		pm.logger.Info(fmt.Sprintf("Discovered %d new peers from bootstrap re-query (total: %d)", newPeerCount, updatedCount), "core")

		// Connect to newly discovered peers via metadata-first strategy
		pm.logger.Info("Connecting to newly discovered peers...", "core")
		pm.connectToKnownPeers(topic)
	} else {
		pm.logger.Debug(fmt.Sprintf("No new peers discovered from bootstrap re-query (total: %d)", updatedCount), "core")
	}
}

// connectToKnownPeers connects to peers from the known_peers database
// This should be called after metadata publishing is complete
// Implements metadata-first connection strategy - fetches metadata before attempting connection
func (pm *PeerManager) connectToKnownPeers(topic string) {
	pm.logger.Info("Connecting to known peers with metadata-first strategy...", "core")

	// Get all known peers for the topic from database
	knownPeers, err := pm.dbManager.KnownPeers.GetKnownPeersByTopic(topic)
	if err != nil {
		pm.logger.Error(fmt.Sprintf("Failed to get known peers from database: %v", err), "core")
		return
	}

	pm.logger.Info(fmt.Sprintf("Found %d known peers to connect to", len(knownPeers)), "core")

	// Count how many connection goroutines we'll spawn
	goroutineCount := 0
	for _, peer := range knownPeers {
		// Skip bootstrap peers (already connected)
		if peer.Source == "bootstrap" {
			pm.logger.Debug(fmt.Sprintf("Skipping bootstrap peer %s (already connected)", peer.PeerID[:8]), "core")
			continue
		}
		goroutineCount++
	}

	pm.logger.Info(fmt.Sprintf("Spawning %d goroutines for peer connections", goroutineCount), "core")

	// Connect to each peer with metadata-first strategy
	for _, peer := range knownPeers {
		// Skip bootstrap peers (already connected)
		if peer.Source == "bootstrap" {
			continue
		}

		// Connect with metadata-first strategy
		pm.logger.Debug(fmt.Sprintf("Spawning connection goroutine for peer %s", peer.PeerID[:8]), "core")
		go pm.connectToPeerWithMetadata(peer.PeerID, peer.PublicKey)
	}
}

// connectToPeerWithMetadata attempts to connect to a peer using metadata-first strategy
// Fetches metadata from DHT first, schedules retry if unavailable
func (pm *PeerManager) connectToPeerWithMetadata(peerID string, publicKey []byte) {
	pm.logger.Debug(fmt.Sprintf("Attempting metadata-first connection to peer %s", peerID[:8]), "core")

	// Try to fetch metadata from DHT
	metadata, err := pm.metadataFetcher.GetPeerMetadata(publicKey)
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Metadata not available for peer %s, scheduling retry: %v", peerID[:8], err), "core")

		// Schedule retry via MetadataRetryScheduler
		pm.metadataRetryScheduler.ScheduleFetch(peerID, publicKey, func(metadata *database.PeerMetadata) {
			// Callback when metadata becomes available
			pm.logger.Info(fmt.Sprintf("Metadata now available for peer %s, attempting connection", peerID[:8]), "core")
			pm.connectWithMetadata(metadata, peerID)
		})
		return
	}

	// Metadata available - proceed with connection
	pm.connectWithMetadata(metadata, peerID)
}

// connectWithMetadata determines connection strategy based on metadata and connects
// Uses LAN detection for same-subnet peers, direct connection for public peers
func (pm *PeerManager) connectWithMetadata(metadata *database.PeerMetadata, peerID string) {
	quicPort := pm.config.GetConfigInt("quic_port", 30906, 1024, 65535)

	var connectionAddr string
	var connectionMethod string

	// Determine connection strategy based on metadata
	if pm.holePuncher != nil && pm.holePuncher.IsLANPeer(metadata) {
		// LAN peer detected - use private IP for direct connection
		connectionAddr = fmt.Sprintf("%s:%d", metadata.NetworkInfo.PrivateIP, quicPort)
		connectionMethod = "LAN"
		pm.logger.Info(fmt.Sprintf("Peer %s is on same LAN, connecting via private IP %s",
			peerID[:8], metadata.NetworkInfo.PrivateIP), "core")
	} else if metadata.NetworkInfo.NodeType == "public" || metadata.NetworkInfo.IsRelay {
		// Public or relay peer - use public IP for direct connection
		connectionAddr = fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, quicPort)
		connectionMethod = "DIRECT"
		pm.logger.Debug(fmt.Sprintf("Peer %s is public/relay, connecting via public IP %s",
			peerID[:8], metadata.NetworkInfo.PublicIP), "core")
	} else if metadata.NetworkInfo.UsingRelay {
		// NAT peer using relay - cannot connect directly
		pm.logger.Debug(fmt.Sprintf("Peer %s is NAT peer using relay, skipping direct connection", peerID[:8]), "core")
		return
	} else {
		// NAT peer without relay - not ready for connection yet
		pm.logger.Debug(fmt.Sprintf("Peer %s is NAT peer without relay, skipping connection", peerID[:8]), "core")
		return
	}

	// Attempt QUIC connection
	pm.logger.Debug(fmt.Sprintf("Connecting to peer %s via %s at %s", peerID[:8], connectionMethod, connectionAddr), "core")

	conn, err := pm.quic.ConnectToPeer(connectionAddr)
	if err != nil {
		pm.logger.Debug(fmt.Sprintf("Failed to connect to peer %s (%s): %v", peerID[:8], connectionMethod, err), "core")
		return
	}

	pm.logger.Info(fmt.Sprintf("Successfully connected to peer %s via %s (identity exchange in progress)", peerID[:8], connectionMethod), "core")

	// Connection successful - identity exchange will happen automatically via QUIC handshake
	_ = conn // Connection is managed by QUIC layer
}

// GetMetadataPublisher returns the metadata publisher for updating peer metadata
func (pm *PeerManager) GetMetadataPublisher() *p2p.MetadataPublisher {
	return pm.metadataPublisher
}

// GetQUIC returns the QUIC peer for direct QUIC operations
func (pm *PeerManager) GetQUIC() *p2p.QUICPeer {
	return pm.quic
}

func (pm *PeerManager) GetDHT() *p2p.DHTPeer {
	return pm.dht
}

func (pm *PeerManager) GetMetadataQuery() *p2p.MetadataQueryService {
	return pm.metadataQuery
}

// GetPeerID returns the persistent Ed25519-based peer ID
func (pm *PeerManager) GetPeerID() string {
	return pm.keyPair.PeerID()
}
