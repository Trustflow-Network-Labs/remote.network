package p2p

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	peer_store "github.com/anacrolix/dht/v2/peer-store"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

type DHTPeer struct {
	server *dht.Server
	nodeID krpc.ID
	config *utils.ConfigManager
	logger *utils.LogsManager
	ctx    context.Context
	cancel context.CancelFunc
	port   int
	conn   *net.UDPConn
	// Loop prevention for peer exchange
	recentExchanges map[string]time.Time
	exchangeMutex   sync.RWMutex
	// Topic mapping for infohash to topic resolution
	infohashToTopic map[string]string
	topicMutex      sync.RWMutex
	// Callback for QUIC metadata exchange
	onPeerDiscovered func(peerAddr string, topic string)
}

func NewDHTPeer(config *utils.ConfigManager, logger *utils.LogsManager) (*DHTPeer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	port := config.GetConfigInt("dht_port", 30609, 1024, 65535)

	// Create UDP connection
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create UDP connection: %v", err)
	}

	// Detect node type and external IP for logging and secure node ID generation
	nodeTypeManager := utils.NewNodeTypeManager()
	externalIP, _ := nodeTypeManager.GetExternalIP()
	isPublic, _ := nodeTypeManager.IsPublicNode()

	var nodeID krpc.ID
	if isPublic && externalIP != "" {
		logger.Info(fmt.Sprintf("Node detected as PUBLIC with external IP: %s", externalIP), "dht")
		// For public nodes, generate cryptographically secure node ID
		nodeID = generateSecureNodeID(net.ParseIP(externalIP))
		logger.Debug(fmt.Sprintf("Generated secure node ID: %x for IP: %s", nodeID, externalIP), "dht")
	} else {
		logger.Info("Node detected as PRIVATE (behind NAT) - using random node ID", "dht")
		// For private/NAT nodes, use random ID as they bypass security validation anyway
		nodeID = krpc.RandomNodeID()
	}

	// Create DHTPeer instance first
	dhtPeer := &DHTPeer{
		nodeID:          nodeID,
		config:          config,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		port:            port,
		conn:            conn,
		recentExchanges: make(map[string]time.Time),
		infohashToTopic: make(map[string]string),
	}

	// Configure DHT server with explicit PeerStore
	serverConfig := &dht.ServerConfig{
		NodeId:    nodeID,
		Conn:      conn,
		PeerStore: &peer_store.InMemory{}, // Explicitly set an in-memory peer store
		OnQuery: func(query *krpc.Msg, source net.Addr) bool {
			logger.Debug(fmt.Sprintf("Received DHT query: %s from %s", query.Q, source), "dht")
			return true
		},
		OnAnnouncePeer: func(infoHash metainfo.Hash, ip net.IP, port int, portOk bool) {
			logger.Info(fmt.Sprintf("Peer announced: infohash=%x, ip=%s, port=%d", infoHash, ip, port), "dht")

			// Trigger peer exchange with the announcing peer
			if portOk && port > 0 {
				peerAddr := krpc.NodeAddr{
					IP:   ip,
					Port: port,
				}

				// Check if we're subscribed to this topic
				topic, err := dhtPeer.InfoHashToTopic(infoHash)
				if err != nil {
					// We're not subscribed to this topic, skip peer exchange
					logger.Debug(fmt.Sprintf("Ignoring announce from %s:%d - %v", ip.String(), port, err), "dht")
					return
				}

				// Exchange peers with the announcing peer (with loop prevention)
				go func() {
					exchangedCount := dhtPeer.exchangeTopicPeersWithPeer(peerAddr, topic)
					if exchangedCount > 0 {
						logger.Info(fmt.Sprintf("Exchanged %d peers with announcing peer %s:%d for topic '%s'",
							exchangedCount, ip.String(), port, topic), "dht")
					}
				}()
			}
		},
		StartingNodes: func() ([]dht.Addr, error) {
			return getBootstrapAddrs(config)
		},
	}

	server, err := dht.NewServer(serverConfig)
	if err != nil {
		cancel()
		conn.Close()
		return nil, fmt.Errorf("failed to create DHT server: %v", err)
	}

	// Set the server in the DHTPeer instance
	dhtPeer.server = server

	return dhtPeer, nil
}

// SetPeerDiscoveredCallback sets the callback function for when peers are discovered
func (d *DHTPeer) SetPeerDiscoveredCallback(callback func(peerAddr string, topic string)) {
	d.onPeerDiscovered = callback
}

func getBootstrapAddrs(config *utils.ConfigManager) ([]dht.Addr, error) {
	// Default nodes combining our custom nodes with the global DHT bootstrap nodes
	defaultNodes := []string{
		// Our custom bootstrap nodes (prioritized first)
		"159.65.253.245:30609",
		"167.86.116.185:30609",
		// Default global bootstrap nodes from anacrolix/dht
		"router.utorrent.com:6881",
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"dht.aelitis.com:6881",
		"router.silotis.us:6881",
		"dht.libtorrent.org:25401",
		"dht.anacrolix.link:42069",
		"router.bittorrent.cloud:42069",
	}

	// Get nodes from config (which now includes our custom nodes)
	bootstrapNodes := config.GetBootstrapNodes("dht_bootstrap_nodes", defaultNodes)

	// Deduplicate nodes
	seen := make(map[string]bool)
	var uniqueNodes []string
	for _, node := range bootstrapNodes {
		if !seen[node] {
			seen[node] = true
			uniqueNodes = append(uniqueNodes, node)
		}
	}

	var addrs []dht.Addr
	for _, node := range uniqueNodes {
		udpAddr, err := net.ResolveUDPAddr("udp", node)
		if err != nil {
			continue // Skip invalid nodes
		}
		addrs = append(addrs, dht.NewAddr(udpAddr))
	}

	return addrs, nil
}

func (d *DHTPeer) Start() error {
	d.logger.Info(fmt.Sprintf("Starting DHT peer with Node ID: %x on port %d", d.nodeID, d.port), "dht")

	// Add our custom bootstrap nodes directly to the DHT routing table
	d.addCustomBootstrapNodes()

	// Bootstrap the DHT asynchronously to avoid blocking
	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
		defer cancel()

		d.logger.Info("Bootstrapping DHT...", "dht")
		stats, err := d.server.BootstrapContext(ctx)
		if err != nil {
			d.logger.Error(fmt.Sprintf("Bootstrap failed: %v", err), "dht")
		} else {
			d.logger.Info(fmt.Sprintf("Bootstrap completed: %+v", stats), "dht")
		}

		// Start table maintainer after bootstrap is complete
		// This automatically maintains routing table health by pinging questionable nodes
		d.logger.Info("Starting DHT table maintainer...", "dht")
		go d.server.TableMaintainer()
		d.logger.Info("DHT table maintainer started", "dht")
	}()

	// Start periodic maintenance
	go d.periodicMaintenance()

	// Start periodic bootstrap pinging to handle timing issues
	go d.periodicBootstrapPing()

	return nil
}

func (d *DHTPeer) addCustomBootstrapNodes() {
	// Check existing nodes in routing table first
	existingNodes := d.server.Nodes()
	d.logger.Info(fmt.Sprintf("DHT routing table currently has %d nodes", len(existingNodes)), "dht")

	// Log existing nodes for debugging
	for i, node := range existingNodes {
		if i < 5 { // Only log first 5 to avoid spam
			d.logger.Debug(fmt.Sprintf("Existing node %d: %s (ID: %x)", i+1, node.Addr.String(), node.ID), "dht")
		}
	}

	// Get bootstrap addresses from config (now includes all global bootstrap nodes)
	bootstrapAddrs, err := getBootstrapAddrs(d.config)
	if err != nil {
		d.logger.Error(fmt.Sprintf("Failed to get bootstrap addresses: %v", err), "dht")
		return
	}

	// Detect our external IP to avoid adding ourselves
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, _ := nodeTypeManager.GetExternalIP()

	d.logger.Info(fmt.Sprintf("Adding %d bootstrap nodes to DHT routing table with ping retries", len(bootstrapAddrs)), "dht")

	for i, bootstrapAddr := range bootstrapAddrs {
		nodeAddr := bootstrapAddr.String()

		// Skip if this is exactly our own address (IP:Port)
		dhtPort := d.config.GetConfigInt("dht_port", 30609, 1024, 65535)
		ourFullDHTAddress := fmt.Sprintf("%s:%d", ourExternalIP, dhtPort)
		if ourExternalIP != "" && nodeAddr == ourFullDHTAddress {
			d.logger.Debug(fmt.Sprintf("Skipping self-add to routing table: %s (exact self-match)", nodeAddr), "dht")
			continue
		}

		// Check if this node is already in the routing table
		nodeExists := false
		for _, existingNode := range existingNodes {
			if existingNode.Addr.IP.Equal(bootstrapAddr.IP()) && existingNode.Addr.Port == bootstrapAddr.Port() {
				nodeExists = true
				nodeType := "global"
				if i < 2 { // First 2 are our custom nodes
					nodeType = "custom"
				}
				d.logger.Debug(fmt.Sprintf("Bootstrap node %s (%s) already exists in routing table (ID: %x)", nodeAddr, nodeType, existingNode.ID), "dht")
				break
			}
		}

		if nodeExists {
			continue
		}

		// Ping with retries to handle timing issues where bootstrap nodes might not be ready
		nodeType := "global"
		if i < 2 { // First 2 are our custom nodes
			nodeType = "custom"
		}

		success := d.pingBootstrapNodeWithRetries(bootstrapAddr, nodeType)
		if !success {
			d.logger.Warn(fmt.Sprintf("Failed to ping %s bootstrap node %s after retries, skipping AddNode", nodeType, nodeAddr), "dht")
			continue
		}

		d.logger.Info(fmt.Sprintf("Successfully pinged bootstrap node %s, adding to routing table", nodeAddr), "dht")

		// Ping one more time to get the node ID for AddNode
		udpAddr := &net.UDPAddr{
			IP:   bootstrapAddr.IP(),
			Port: bootstrapAddr.Port(),
		}

		pingResult := d.server.Ping(udpAddr)
		var nodeID krpc.ID
		if pingResult.Err == nil && pingResult.Reply.R != nil {
			nodeID = pingResult.Reply.R.ID
			d.logger.Debug(fmt.Sprintf("Extracted node ID %x from ping response for %s", nodeID, nodeAddr), "dht")
		} else {
			// Fallback to random ID if we can't get the actual ID
			nodeID = krpc.RandomNodeID()
			d.logger.Debug(fmt.Sprintf("Could not extract node ID from ping response for %s, using random ID %x", nodeAddr, nodeID), "dht")
		}

		// Create NodeInfo with the actual node ID from ping response
		nodeInfo := krpc.NodeInfo{
			ID: nodeID,
			Addr: krpc.NodeAddr{
				IP:   bootstrapAddr.IP(),
				Port: bootstrapAddr.Port(),
			},
		}

		// Add to DHT routing table (should succeed since we just pinged successfully)
		if err := d.server.AddNode(nodeInfo); err != nil {
			d.logger.Warn(fmt.Sprintf("Failed to add %s bootstrap node %s to routing table: %v", nodeType, nodeAddr, err), "dht")
		} else {
			d.logger.Info(fmt.Sprintf("Added %s bootstrap node %s to DHT routing table (ID: %x)", nodeType, nodeAddr, nodeInfo.ID), "dht")

			// Trigger QUIC metadata exchange for the bootstrap node itself
			// Bootstrap nodes are peers too and need their metadata stored
			if d.onPeerDiscovered != nil {
				quicPort := d.config.GetConfigInt("quic_port", 30906, 1024, 65535)
				bootstrapQuicAddr := fmt.Sprintf("%s:%d", nodeInfo.Addr.IP.String(), quicPort)
				d.logger.Debug(fmt.Sprintf("Triggering QUIC metadata exchange with bootstrap node %s", bootstrapQuicAddr), "dht")
				go d.onPeerDiscovered(bootstrapQuicAddr, "remote-network-mesh")
			}

			// A3.1: Exchange topic peers with successfully added bootstrap node
			// This allows us to discover topic peers from bootstrap nodes that have them
			go func(nodeAddr krpc.NodeAddr) {
				time.Sleep(100 * time.Millisecond) // Brief delay to ensure routing table is updated

				// Get current subscribed topics and exchange peers for each
				topics := []string{"remote-network-mesh"} // TODO: Get from actual subscribed topics
				for _, topic := range topics {
					peerCount := d.exchangeTopicPeersWithPeer(nodeAddr, topic)
					if peerCount > 0 {
						d.logger.Info(fmt.Sprintf("Bootstrap peer exchange: got %d topic peers for '%s' from %s", peerCount, topic, nodeAddr.IP.String()), "dht")
					}
				}
			}(nodeInfo.Addr)
		}
	}

	// Log final routing table size
	finalNodes := d.server.Nodes()
	d.logger.Info(fmt.Sprintf("DHT routing table after adding bootstrap nodes: %d nodes", len(finalNodes)), "dht")
}

// pingBootstrapNodeWithRetries pings a bootstrap node with multiple retry attempts
func (d *DHTPeer) pingBootstrapNodeWithRetries(bootstrapAddr dht.Addr, nodeType string) bool {
	nodeAddr := bootstrapAddr.String()
	maxRetries := 3
	baseDelay := 2 * time.Second

	// Convert dht.Addr to *net.UDPAddr for ping
	udpAddr := &net.UDPAddr{
		IP:   bootstrapAddr.IP(),
		Port: bootstrapAddr.Port(),
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.logger.Debug(fmt.Sprintf("Ping attempt %d/%d for %s bootstrap node %s", attempt, maxRetries, nodeType, nodeAddr), "dht")

		pingResult := d.server.Ping(udpAddr)
		if pingResult.Err == nil {
			d.logger.Info(fmt.Sprintf("Successfully pinged %s bootstrap node %s on attempt %d/%d", nodeType, nodeAddr, attempt, maxRetries), "dht")
			return true
		}

		d.logger.Debug(fmt.Sprintf("Ping attempt %d/%d failed for %s bootstrap node %s: %v", attempt, maxRetries, nodeType, nodeAddr, pingResult.Err), "dht")

		// Wait before next retry (exponential backoff)
		if attempt < maxRetries {
			delay := time.Duration(attempt) * baseDelay
			d.logger.Debug(fmt.Sprintf("Waiting %v before retry %d for %s bootstrap node %s", delay, attempt+1, nodeType, nodeAddr), "dht")
			time.Sleep(delay)
		}
	}

	d.logger.Warn(fmt.Sprintf("All %d ping attempts failed for %s bootstrap node %s", maxRetries, nodeType, nodeAddr), "dht")
	return false
}

// periodicBootstrapPing periodically pings bootstrap nodes to maintain connections
func (d *DHTPeer) periodicBootstrapPing() {
	// Start periodic pinging after initial bootstrap delay
	time.Sleep(30 * time.Second)

	pingInterval := 2 * time.Minute // Ping every 2 minutes
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	d.logger.Info(fmt.Sprintf("Starting periodic bootstrap ping every %v", pingInterval), "dht")

	for {
		select {
		case <-d.ctx.Done():
			d.logger.Debug("Stopping periodic bootstrap ping", "dht")
			return
		case <-ticker.C:
			d.logger.Debug("Performing periodic bootstrap ping...", "dht")
			d.performPeriodicBootstrapPing()
		}
	}
}

// performPeriodicBootstrapPing pings bootstrap nodes and adds them if they're not in routing table
func (d *DHTPeer) performPeriodicBootstrapPing() {
	// Get bootstrap addresses from config
	bootstrapAddrs, err := getBootstrapAddrs(d.config)
	if err != nil {
		d.logger.Error(fmt.Sprintf("Failed to get bootstrap addresses for periodic ping: %v", err), "dht")
		return
	}

	// Get current routing table nodes
	existingNodes := d.server.Nodes()
	existingNodeMap := make(map[string]bool)
	for _, node := range existingNodes {
		nodeKey := fmt.Sprintf("%s:%d", node.Addr.IP.String(), node.Addr.Port)
		existingNodeMap[nodeKey] = true
	}

	// Detect our external IP to avoid pinging ourselves
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, _ := nodeTypeManager.GetExternalIP()

	successfulPings := 0
	addedNodes := 0

	for i, bootstrapAddr := range bootstrapAddrs {
		nodeAddr := bootstrapAddr.String()

		// Skip if this is exactly our own address (IP:Port)
		dhtPort := d.config.GetConfigInt("dht_port", 30609, 1024, 65535)
		ourFullDHTAddress := fmt.Sprintf("%s:%d", ourExternalIP, dhtPort)
		if ourExternalIP != "" && nodeAddr == ourFullDHTAddress {
			continue
		}

		nodeType := "global"
		if i < 2 { // First 2 are our custom nodes
			nodeType = "custom"
		}

		// Check if this node is already in routing table
		nodeKey := fmt.Sprintf("%s:%d", bootstrapAddr.IP().String(), bootstrapAddr.Port())
		if existingNodeMap[nodeKey] {
			// Node exists, just ping to verify it's still responsive
			udpAddr := &net.UDPAddr{
				IP:   bootstrapAddr.IP(),
				Port: bootstrapAddr.Port(),
			}

			pingResult := d.server.Ping(udpAddr)
			if pingResult.Err == nil {
				successfulPings++
				d.logger.Debug(fmt.Sprintf("Periodic ping successful for existing %s bootstrap node %s", nodeType, nodeAddr), "dht")
			} else {
				d.logger.Debug(fmt.Sprintf("Periodic ping failed for existing %s bootstrap node %s: %v", nodeType, nodeAddr, pingResult.Err), "dht")
			}
			continue
		}

		// Node not in routing table, try to ping and add it
		success := d.pingBootstrapNodeWithRetries(bootstrapAddr, nodeType)
		if success {
			successfulPings++

			// Get node ID and add to routing table
			udpAddr := &net.UDPAddr{
				IP:   bootstrapAddr.IP(),
				Port: bootstrapAddr.Port(),
			}

			pingResult := d.server.Ping(udpAddr)
			var nodeID krpc.ID
			if pingResult.Err == nil && pingResult.Reply.R != nil {
				nodeID = pingResult.Reply.R.ID
			} else {
				nodeID = krpc.RandomNodeID()
			}

			nodeInfo := krpc.NodeInfo{
				ID: nodeID,
				Addr: krpc.NodeAddr{
					IP:   bootstrapAddr.IP(),
					Port: bootstrapAddr.Port(),
				},
			}

			if err := d.server.AddNode(nodeInfo); err == nil {
				addedNodes++
				d.logger.Info(fmt.Sprintf("Periodic ping: Added %s bootstrap node %s to routing table (ID: %x)", nodeType, nodeAddr, nodeInfo.ID), "dht")
			}
		}
	}

	d.logger.Debug(fmt.Sprintf("Periodic bootstrap ping completed: %d successful pings, %d nodes added", successfulPings, addedNodes), "dht")
}

func (d *DHTPeer) announceDirectlyToDiscoveredPeers(topic string, discoveredPeers []krpc.NodeAddr) {
	// Send direct DHT announce_peer queries to peers we discovered for this topic
	// This ensures bidirectional discovery - they know about us too
	//
	// Protocol: 1) get_peers to obtain token, 2) announce_peer with token

	topicHash := d.TopicToInfoHash(topic)
	quicPort := d.config.GetConfigInt("quic_port", 30906, 1024, 65535)

	// Detect our external IP to avoid self-announce
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, _ := nodeTypeManager.GetExternalIP()

	d.logger.Info(fmt.Sprintf("Sending direct announce queries to %d discovered peers for topic '%s'", len(discoveredPeers), topic), "dht")

	for i, peerAddr := range discoveredPeers {
		// Validate the peer address first
		if peerAddr.IP == nil {
			d.logger.Warn(fmt.Sprintf("Skipping peer with nil IP: %+v", peerAddr), "dht")
			continue
		}

		peerAddrStr := fmt.Sprintf("%s:%d", peerAddr.IP.String(), peerAddr.Port)

		// Skip if this is exactly our own address (IP:Port)
		dhtPort := d.config.GetConfigInt("dht_port", 30609, 1024, 65535)
		ourFullDHTAddress := fmt.Sprintf("%s:%d", ourExternalIP, dhtPort)
		if ourExternalIP != "" && peerAddrStr == ourFullDHTAddress {
			d.logger.Debug(fmt.Sprintf("Skipping self-announce to discovered peer %s (exact self-match)", peerAddrStr), "dht")
			continue
		}

		// Convert to UDP address for DHT communication
		// IMPORTANT: Use DHT port (30609), not QUIC port (30906) from discovery
		// The peer announcement uses QUIC port, but DHT queries must go to DHT port
		udpAddr := &net.UDPAddr{
			IP:   peerAddr.IP,
			Port: dhtPort, // Use DHT port, not peerAddr.Port (which is QUIC port)
		}

		// Create DHT address
		dhtAddr := dht.NewAddr(udpAddr)

		// Convert topicHash to krpc.ID for proper type
		var infoHash krpc.ID
		copy(infoHash[:], topicHash[:])

		// Step 1: Send get_peers query to obtain token
		ctx1, cancel1 := context.WithTimeout(d.ctx, 5*time.Second)

		getPeersInput := dht.QueryInput{
			MsgArgs: krpc.MsgArgs{
				InfoHash: infoHash,
			},
		}

		d.logger.Debug(fmt.Sprintf("Sending get_peers query to discovered peer %s to obtain token for topic '%s'", peerAddrStr, topic), "dht")

		getPeersResult := d.server.Query(ctx1, dhtAddr, "get_peers", getPeersInput)
		cancel1()

		if getPeersResult.Err != nil {
			d.logger.Warn(fmt.Sprintf("Failed to get token from peer %s: %v", peerAddrStr, getPeersResult.Err), "dht")
			continue
		}

		// Extract token from get_peers response
		if getPeersResult.Reply.R.Token == nil {
			d.logger.Warn(fmt.Sprintf("No token received from peer %s", peerAddrStr), "dht")
			continue
		}

		token := *getPeersResult.Reply.R.Token
		d.logger.Debug(fmt.Sprintf("Received token from peer %s: %s", peerAddrStr, token), "dht")

		// Step 2: Send announce_peer query with the obtained token
		ctx2, cancel2 := context.WithTimeout(d.ctx, 5*time.Second)

		announceInput := dht.QueryInput{
			MsgArgs: krpc.MsgArgs{
				InfoHash: infoHash,
				Port:     &quicPort,
				Token:    token,
			},
		}

		d.logger.Info(fmt.Sprintf("Sending announce_peer query to discovered peer %s for topic '%s' (infohash: %x, port: %d)",
			peerAddrStr, topic, topicHash, quicPort), "dht")

		result := d.server.Query(ctx2, dhtAddr, "announce_peer", announceInput)
		cancel2()

		if result.Err != nil {
			d.logger.Warn(fmt.Sprintf("Direct announce to discovered peer %s failed: %v", peerAddrStr, result.Err), "dht")
		} else {
			d.logger.Info(fmt.Sprintf("Direct announce to discovered peer %s succeeded for topic '%s'", peerAddrStr, topic), "dht")

			// A3.3: Exchange topic peers after successful announce_peer
			// Since we successfully announced, the peer now knows about us - good time to get their peers
			go func(announcedPeer krpc.NodeAddr, topic string) {
				time.Sleep(100 * time.Millisecond) // Brief delay to let the peer process our announcement

				peerCount := d.exchangeTopicPeersWithPeer(announcedPeer, topic)
				if peerCount > 0 {
					d.logger.Info(fmt.Sprintf("Post-announce peer exchange: got %d topic peers for '%s' from %s", peerCount, topic, announcedPeer.IP.String()), "dht")
				}
			}(peerAddr, topic)
		}

		// Limit to first few peers to avoid spam
		if i >= 4 { // Only announce to first 5 discovered peers
			d.logger.Debug(fmt.Sprintf("Limited direct announces to first 5 discovered peers for topic '%s'", topic), "dht")
			break
		}
	}
}

// exchangeTopicPeersWithPeer queries a peer for topic-specific peers and adds them to our peer store
func (d *DHTPeer) exchangeTopicPeersWithPeer(peerAddr krpc.NodeAddr, topic string) int {
	// Loop prevention: check if we've recently exchanged with this peer for this topic
	exchangeKey := fmt.Sprintf("%s:%d:%s", peerAddr.IP.String(), peerAddr.Port, topic)

	d.exchangeMutex.RLock()
	lastExchange, exists := d.recentExchanges[exchangeKey]
	d.exchangeMutex.RUnlock()

	// Prevent exchange if we did it recently (within 5 minutes)
	if exists && time.Since(lastExchange) < 5*time.Minute {
		d.logger.Debug(fmt.Sprintf("Skipping peer exchange with %s for topic '%s' (recent exchange: %v ago)",
			peerAddr.IP.String(), topic, time.Since(lastExchange)), "dht")
		return 0
	}

	// Update exchange timestamp
	d.exchangeMutex.Lock()
	d.recentExchanges[exchangeKey] = time.Now()
	// Clean up old entries to prevent memory leak
	for key, timestamp := range d.recentExchanges {
		if time.Since(timestamp) > 10*time.Minute {
			delete(d.recentExchanges, key)
		}
	}
	d.exchangeMutex.Unlock()

	topicHash := d.TopicToInfoHash(topic)

	// Convert to DHT address for querying
	udpAddr := &net.UDPAddr{
		IP:   peerAddr.IP,
		Port: peerAddr.Port,
	}
	dhtAddr := dht.NewAddr(udpAddr)

	// Convert topicHash to krpc.ID for query
	var infoHash krpc.ID
	copy(infoHash[:], topicHash[:])

	// Query peer for topic-specific peers using infohash
	ctx, cancel := context.WithTimeout(d.ctx, 5*time.Second)
	defer cancel()

	getPeersInput := dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			InfoHash: infoHash,
		},
	}

	peerAddrStr := fmt.Sprintf("%s:%d", peerAddr.IP.String(), peerAddr.Port)
	d.logger.Debug(fmt.Sprintf("Requesting topic peers from peer %s for topic '%s'", peerAddrStr, topic), "dht")

	result := d.server.Query(ctx, dhtAddr, "get_peers", getPeersInput)
	if result.Err != nil {
		d.logger.Debug(fmt.Sprintf("Failed to get topic peers from %s: %v", peerAddrStr, result.Err), "dht")
		return 0
	}

	// Extract topic peers from response
	if result.Reply.R == nil {
		d.logger.Debug(fmt.Sprintf("Peer %s returned invalid response for '%s'", peerAddrStr, topic), "dht")
		return 0
	}

	// Step 2: Announce ourselves to this peer using the token from get_peers response
	if result.Reply.R.Token != nil {
		quicPort := d.config.GetConfigInt("quic_port", 30906, 1024, 65535)
		token := *result.Reply.R.Token

		announceCtx, announceCancel := context.WithTimeout(d.ctx, 5*time.Second)
		announceInput := dht.QueryInput{
			MsgArgs: krpc.MsgArgs{
				InfoHash: infoHash,
				Port:     &quicPort,
				Token:    token,
			},
		}

		d.logger.Debug(fmt.Sprintf("Announcing ourselves to peer %s for topic '%s' (port: %d)", peerAddrStr, topic, quicPort), "dht")
		announceResult := d.server.Query(announceCtx, dhtAddr, "announce_peer", announceInput)
		announceCancel()

		if announceResult.Err != nil {
			d.logger.Debug(fmt.Sprintf("Failed to announce to peer %s for topic '%s': %v", peerAddrStr, topic, announceResult.Err), "dht")
		} else {
			d.logger.Debug(fmt.Sprintf("Successfully announced to peer %s for topic '%s'", peerAddrStr, topic), "dht")
		}
	} else {
		d.logger.Debug(fmt.Sprintf("No token received from peer %s, skipping announce", peerAddrStr), "dht")
	}

	// Step 3: Process the topic peers they shared with us
	if result.Reply.R.Values == nil {
		d.logger.Debug(fmt.Sprintf("Peer %s returned no topic peers for '%s'", peerAddrStr, topic), "dht")
		return 0
	}

	// Detect our external IP to avoid adding ourselves
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, _ := nodeTypeManager.GetExternalIP()

	peerStore := d.server.PeerStore()
	if peerStore == nil {
		d.logger.Warn("PeerStore is nil, cannot add discovered topic peers", "dht")
		return 0
	}

	// Add topic peers to our peer store (DHT naturally filters by topic via infohash)
	addedCount := 0
	quicPort := d.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	ourFullAddress := fmt.Sprintf("%s:%d", ourExternalIP, quicPort)

	for _, topicPeer := range result.Reply.R.Values {
		topicPeerKey := fmt.Sprintf("%s:%d", topicPeer.IP.String(), topicPeer.Port)

		// Check if this is exactly our own address (precise match)
		skipAddPeer := false
		if ourExternalIP != "" && topicPeerKey == ourFullAddress {
			d.logger.Debug(fmt.Sprintf("Skipping AddPeer for exact self-match: %s", topicPeerKey), "dht")
			skipAddPeer = true
		}

		// Add to peer store only if not exact self-match
		if !skipAddPeer {
			peerStore.AddPeer(topicHash, topicPeer)
			d.logger.Info(fmt.Sprintf("Added topic peer %s to peer store for topic '%s' (from %s)", topicPeerKey, topic, peerAddrStr), "dht")
			addedCount++
		}

		// Trigger QUIC metadata exchange regardless of AddPeer status
		// This allows NAT peers with same public IP:port to exchange metadata
		if d.onPeerDiscovered != nil {
			quicAddr := fmt.Sprintf("%s:%d", topicPeer.IP.String(), quicPort)
			go func(addr, topicName string, isExactMatch bool) {
				if isExactMatch {
					d.logger.Debug(fmt.Sprintf("Attempting QUIC metadata exchange with potential self/NAT peer: %s for topic '%s'", addr, topicName), "dht")
				} else {
					d.logger.Debug(fmt.Sprintf("Triggering QUIC metadata exchange with %s for topic '%s'", addr, topicName), "dht")
				}
				d.onPeerDiscovered(addr, topicName)
			}(quicAddr, topic, skipAddPeer)
		}
	}

	d.logger.Info(fmt.Sprintf("Exchanged topic peers with %s: added %d peers for topic '%s'", peerAddrStr, addedCount, topic), "dht")

	// Note: QUIC metadata exchange is now triggered per-peer in the loop above

	return addedCount
}

func (d *DHTPeer) logRoutingTableStatus() {
	nodes := d.server.Nodes()
	stats := d.server.Stats()

	d.logger.Info(fmt.Sprintf("DHT Routing Table Status - Total Nodes: %d, Stats: Nodes=%d, GoodNodes=%d, BadNodes=%d",
		len(nodes), stats.Nodes, stats.GoodNodes, stats.BadNodes), "dht")

	// Log a few sample nodes for debugging
	for i, node := range nodes {
		if i < 3 { // Only log first 3 to avoid spam
			d.logger.Debug(fmt.Sprintf("Routing table node %d: %s (ID: %x)", i+1, node.Addr.String(), node.ID), "dht")
		}
	}
}

func (d *DHTPeer) periodicMaintenance() {
	maintenanceInterval := d.config.GetConfigDuration("peer_maintenance_interval", 5*time.Minute)
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.logger.Debug("Performing DHT maintenance...", "dht")

			// Log current routing table status
			d.logRoutingTableStatus()

			ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
			_, err := d.server.BootstrapContext(ctx)
			cancel()

			if err != nil {
				d.logger.Warn(fmt.Sprintf("Maintenance bootstrap failed: %v", err), "dht")
			} else {
				stats := d.server.Stats()
				d.logger.Debug(fmt.Sprintf("DHT Stats after maintenance - Nodes: %d, GoodNodes: %d, BadNodes: %d",
					stats.Nodes, stats.GoodNodes, stats.BadNodes), "dht")
			}
		}
	}
}

func (d *DHTPeer) AnnounceForTopic(topic string, port int) error {
	topicHash := d.TopicToInfoHash(topic)
	d.logger.Info(fmt.Sprintf("Announcing peer for topic '%s' (infohash: %x) on port %d", topic, topicHash, port), "dht")

	// Note: Peer discovery now happens dynamically through peer exchange
	// Bootstrap nodes will share their topic peers when successfully added to routing table
	// Discovered topic peers will share their peers during discovery and announcement

	announce, err := d.server.AnnounceTraversal(topicHash,
		dht.AnnouncePeer(dht.AnnouncePeerOpts{
			Port:        port,
			ImpliedPort: false,
		}),
		dht.Scrape(),
	)
	if err != nil {
		return fmt.Errorf("failed to start announce for topic '%s': %v", topic, err)
	}

	// Drive the traversal in background using the proper pattern
	go func() {
		defer announce.Close()

		d.logger.Info(fmt.Sprintf("Running announce traversal for topic '%s'", topic), "dht")

		// Monitor progress and wait for completion with longer timeout
		progressTicker := time.NewTicker(10 * time.Second)
		defer progressTicker.Stop()

		timeoutTimer := time.NewTimer(60 * time.Second) // Longer timeout
		defer timeoutTimer.Stop()

	announce_loop:
		for {
			select {
			case <-announce.Finished():
				d.logger.Info(fmt.Sprintf("Announce traversal finished for topic '%s'", topic), "dht")
				break announce_loop
			case <-progressTicker.C:
				// Show progress every 10 seconds
				numContacted := announce.NumContacted()
				d.logger.Info(fmt.Sprintf("Announce progress for topic '%s': contacted %d nodes so far", topic, numContacted), "dht")
			case <-timeoutTimer.C:
				numContacted := announce.NumContacted()
				d.logger.Warn(fmt.Sprintf("Announce traversal timeout for topic '%s' after 60s, contacted %d nodes, stopping", topic, numContacted), "dht")
				announce.StopTraversing()
				break announce_loop
			case <-d.ctx.Done():
				d.logger.Debug(fmt.Sprintf("Announce traversal cancelled for topic '%s'", topic), "dht")
				announce.StopTraversing()
				return
			}
		}

		// Get final stats
		numContacted := announce.NumContacted()
		d.logger.Info(fmt.Sprintf("Announce traversal final stats for topic '%s': contacted %d nodes total",
			topic, numContacted), "dht")

		// Also collect any peers found during traversal
		peerCount := 0
		for peersValues := range announce.Peers {
			peerCount += len(peersValues.Peers)
			d.logger.Debug(fmt.Sprintf("Found %d peers from %s for topic '%s' (total peers: %d)",
				len(peersValues.Peers), peersValues.NodeInfo.Addr, topic, peerCount), "dht")
		}

		if peerCount > 0 {
			d.logger.Info(fmt.Sprintf("Topic '%s' announce found %d total peers", topic, peerCount), "dht")
		}
	}()

	return nil
}

func (d *DHTPeer) FindPeersForTopic(topic string) ([]krpc.NodeAddr, error) {
	topicHash := d.TopicToInfoHash(topic)
	d.logger.Info(fmt.Sprintf("Finding peers for topic '%s' (infohash: %x)", topic, topicHash), "dht")

	// Convert [20]byte to Int160 using krpc.ID as shown in ChatGPT example
	id := krpc.ID(topicHash).Int160()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(d.ctx, 20*time.Second)
	defer cancel()

	// Use the same bootstrap nodes as the main DHT (includes our custom nodes)
	bs, err := getBootstrapAddrs(d.config)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap addresses: %v", err)
	}

	if len(bs) == 0 {
		return nil, fmt.Errorf("no bootstrap addresses resolved")
	}

	d.logger.Info(fmt.Sprintf("Starting GetPeers query for topic '%s' using %d bootstrap nodes (custom nodes prioritized)", topic, len(bs)), "dht")

	// Detect our external IP to avoid self-querying
	nodeTypeManager := utils.NewNodeTypeManager()
	ourExternalIP, _ := nodeTypeManager.GetExternalIP()

	// Query ALL bootstrap nodes (except ourselves) to maximize discovery
	var peers []krpc.NodeAddr
	var skippedSelf []string
	queriedNodes := 0

	d.logger.Info(fmt.Sprintf("Querying ALL bootstrap nodes for topic '%s'", topic), "dht")

	for i, node := range bs {
		nodeAddr := node.String()

		// Skip if this is exactly our own address (IP:Port)
		dhtPort := d.config.GetConfigInt("dht_port", 30609, 1024, 65535)
		ourFullDHTAddress := fmt.Sprintf("%s:%d", ourExternalIP, dhtPort)
		if ourExternalIP != "" && nodeAddr == ourFullDHTAddress {
			skippedSelf = append(skippedSelf, nodeAddr)
			d.logger.Debug(fmt.Sprintf("Skipping self-query to %s (exact self-match)", nodeAddr), "dht")
			continue
		}

		queriedNodes++
		if i < 2 {
			d.logger.Info(fmt.Sprintf("Querying our bootstrap node %d/%d: %s", i+1, len(bs), nodeAddr), "dht")
		} else {
			d.logger.Info(fmt.Sprintf("Querying fallback bootstrap node %d/%d: %s", i+1, len(bs), nodeAddr), "dht")
		}

		// Query this bootstrap node
		qr := d.server.GetPeers(ctx, node, id, false /* scrape */, dht.QueryRateLimiting{})

		// Check for query errors (but continue with other nodes)
		if err := qr.ToError(); err != nil {
			d.logger.Debug(fmt.Sprintf("GetPeers query error for node %s: %v", nodeAddr, err), "dht")
		}

		// Try to collect peers from PeerStore after each query
		if d.server != nil {
			peerStore := d.server.PeerStore()
			if peerStore != nil {
				storePeers := peerStore.GetPeers(topicHash)
				d.logger.Debug(fmt.Sprintf("PeerStore returned %d peers for topic '%s'", len(storePeers), topic), "dht")
				for i, peer := range storePeers {
					// Debug the raw peer data to understand the malformed address issue
					d.logger.Debug(fmt.Sprintf("Raw peer %d: IP=%v (%T), Port=%d", i, peer.IP, peer.IP, peer.Port), "dht")

					// Validate IP before creating address
					if peer.IP == nil {
						d.logger.Warn(fmt.Sprintf("Skipping peer with nil IP from %s", nodeAddr), "dht")
						continue
					}

					// Avoid duplicates
					duplicate := false
					for _, existingPeer := range peers {
						if existingPeer.IP.Equal(peer.IP) && existingPeer.Port == peer.Port {
							duplicate = true
							break
						}
					}
					if !duplicate {
						peers = append(peers, peer)
						d.logger.Info(fmt.Sprintf("Found valid peer for topic '%s' from %s: %s:%d", topic, nodeAddr, peer.IP.String(), peer.Port), "dht")

						// A3.2: Exchange topic peers with newly discovered topic peer
						// This allows peer network to propagate efficiently
						go func(discoveredPeer krpc.NodeAddr, topic string) {
							time.Sleep(50 * time.Millisecond) // Brief delay to avoid overwhelming the peer

							peerCount := d.exchangeTopicPeersWithPeer(discoveredPeer, topic)
							if peerCount > 0 {
								d.logger.Info(fmt.Sprintf("Topic peer exchange: got %d additional peers for '%s' from discovered peer %s", peerCount, topic, discoveredPeer.IP.String()), "dht")
							}
						}(peer, topic)
					}
				}
			}
		}
	}

	if len(skippedSelf) > 0 {
		d.logger.Info(fmt.Sprintf("Skipped %d self-queries: %v", len(skippedSelf), skippedSelf), "dht")
	}

	d.logger.Info(fmt.Sprintf("Queried %d bootstrap nodes for topic '%s', found %d unique peers", queriedNodes, topic, len(peers)), "dht")

	// Send direct announce queries to discovered peers to ensure they know about us
	if len(peers) > 0 {
		d.announceDirectlyToDiscoveredPeers(topic, peers)
	}

	return peers, nil
}

func (d *DHTPeer) TopicToInfoHash(topic string) [20]byte {
	hasher := sha1.New()
	hasher.Write([]byte(topic))
	var hash [20]byte
	copy(hash[:], hasher.Sum(nil))

	// Register the topic mapping for reverse lookup
	infohashStr := fmt.Sprintf("%x", hash[:])
	d.topicMutex.Lock()
	d.infohashToTopic[infohashStr] = topic
	d.topicMutex.Unlock()

	return hash
}

// InfoHashToTopic converts an infohash back to its topic string
// Returns error if we're not subscribed to this topic
func (d *DHTPeer) InfoHashToTopic(infoHash metainfo.Hash) (string, error) {
	infohashStr := fmt.Sprintf("%x", infoHash[:])

	d.topicMutex.RLock()
	topic, exists := d.infohashToTopic[infohashStr]
	d.topicMutex.RUnlock()

	if exists {
		return topic, nil
	}

	// If no mapping found, we're not subscribed to this topic
	return "", fmt.Errorf("not subscribed to topic with infohash %s", infohashStr)
}

func (d *DHTPeer) Ping(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to resolve address %s: %v", addr, err)
	}

	result := d.server.Ping(udpAddr)
	if result.Err != nil {
		return fmt.Errorf("ping failed: %v", result.Err)
	}

	d.logger.Debug(fmt.Sprintf("Successfully pinged %s: %+v", addr, result), "dht")
	return nil
}

func (d *DHTPeer) GetStats() dht.ServerStats {
	return d.server.Stats()
}

func (d *DHTPeer) NodeID() string {
	return fmt.Sprintf("%x", d.nodeID)
}

// generateSecureNodeID creates a cryptographically secure node ID based on the given IP address
func generateSecureNodeID(ip net.IP) krpc.ID {
	// Start with a random node ID
	nodeID := krpc.RandomNodeID()

	// Make it secure for the given IP address using DHT security algorithm
	dht.SecureNodeId(&nodeID, ip)

	return nodeID
}

func (d *DHTPeer) Stop() error {
	d.logger.Info("Stopping DHT peer...", "dht")
	d.cancel()

	if d.server != nil {
		d.server.Close()
	}

	if d.conn != nil {
		d.conn.Close()
	}

	return nil
}

