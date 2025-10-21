package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// RelayManager manages persistent relay connections for NAT peers
type RelayManager struct {
	config            *utils.ConfigManager
	logger            *utils.LogsManager
	dbManager         *database.SQLiteManager
	keyPair           *crypto.KeyPair
	quicPeer          *QUICPeer
	dhtPeer           *DHTPeer
	metadataPublisher *MetadataPublisher
	metadataFetcher   *MetadataFetcher

	// Relay selection
	selector *RelaySelector

	// Current relay connection
	currentRelay     *RelayCandidate
	currentRelayConn *quic.Conn
	relayMutex       sync.RWMutex

	// Session management
	sessionID       string
	sessionStart    time.Time
	ingressBytes    int64
	egressBytes     int64
	trafficMutex    sync.RWMutex
	keepaliveStop   chan struct{}

	// Context
	ctx    context.Context
	cancel context.CancelFunc

	// Periodic re-evaluation
	evaluationTicker *time.Ticker
	evaluationStop   chan struct{}
}

// NewRelayManager creates a new relay manager for NAT peers
func NewRelayManager(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager, keyPair *crypto.KeyPair, quicPeer *QUICPeer, dhtPeer *DHTPeer, metadataPublisher *MetadataPublisher, metadataFetcher *MetadataFetcher) *RelayManager {
	ctx, cancel := context.WithCancel(context.Background())

	selector := NewRelaySelector(config, logger, dbManager, quicPeer)

	return &RelayManager{
		config:            config,
		logger:            logger,
		dbManager:         dbManager,
		keyPair:           keyPair,
		quicPeer:          quicPeer,
		dhtPeer:           dhtPeer,
		metadataPublisher: metadataPublisher,
		metadataFetcher:   metadataFetcher,
		selector:          selector,
		ctx:               ctx,
		cancel:            cancel,
		keepaliveStop:     make(chan struct{}),
		evaluationStop:    make(chan struct{}),
	}
}

// Start starts the relay manager
func (rm *RelayManager) Start() error {
	rm.logger.Info("Starting relay manager for NAT peer...", "relay-manager")

	// Start periodic relay evaluation
	evaluationInterval := rm.config.GetConfigDuration("relay_evaluation_interval", 5*time.Minute)
	rm.evaluationTicker = time.NewTicker(evaluationInterval)

	go rm.periodicRelayEvaluation()

	// Perform initial relay selection
	go func() {
		// Wait a bit for peers to be discovered via identity exchange
		time.Sleep(10 * time.Second)

		// First, discover relay candidates from database
		rm.logger.Info("Initial relay discovery: loading candidates from database...", "relay-manager")
		candidatesFound := rm.rediscoverRelayCandidates()
		rm.logger.Info(fmt.Sprintf("Initial relay discovery: found %d candidates", candidatesFound), "relay-manager")

		// Then attempt to select and connect
		if err := rm.SelectAndConnectRelay(); err != nil {
			rm.logger.Error(fmt.Sprintf("Initial relay selection failed: %v", err), "relay-manager")
		}
	}()

	return nil
}

// Stop stops the relay manager
func (rm *RelayManager) Stop() {
	rm.logger.Info("Stopping relay manager...", "relay-manager")

	rm.cancel()

	// Stop periodic evaluation
	if rm.evaluationTicker != nil {
		rm.evaluationTicker.Stop()
	}
	close(rm.evaluationStop)

	// Disconnect from current relay
	rm.DisconnectRelay()

	rm.logger.Info("Relay manager stopped", "relay-manager")
}

// AddRelayCandidate adds a discovered relay peer
func (rm *RelayManager) AddRelayCandidate(metadata *database.PeerMetadata) error {
	return rm.selector.AddCandidate(metadata)
}

// SelectAndConnectRelay selects the best relay and establishes connection
func (rm *RelayManager) SelectAndConnectRelay() error {
	candidateCount := rm.selector.GetCandidateCount()
	rm.logger.Info(fmt.Sprintf("🎯 Selecting best relay peer from %d candidates...", candidateCount), "relay-manager")

	// Measure latency to all candidates
	rm.selector.MeasureAllCandidates()

	// Select best relay
	bestRelay := rm.selector.SelectBestRelay()
	if bestRelay == nil {
		rm.logger.Warn(fmt.Sprintf("❌ No suitable relay found (had %d candidates)", candidateCount), "relay-manager")
		return fmt.Errorf("no suitable relay found")
	}

	rm.logger.Info(fmt.Sprintf("🏆 Best relay selected: %s (endpoint: %s, latency: %v)",
		bestRelay.NodeID, bestRelay.Endpoint, bestRelay.Latency), "relay-manager")

	// Check if we should switch
	rm.relayMutex.RLock()
	currentRelay := rm.currentRelay
	rm.relayMutex.RUnlock()

	if currentRelay != nil {
		if !rm.selector.ShouldSwitchRelay(currentRelay, bestRelay) {
			rm.logger.Info(fmt.Sprintf("Current relay %s is still optimal", currentRelay.NodeID), "relay-manager")
			return nil
		}
		rm.logger.Info(fmt.Sprintf("Switching from relay %s to %s", currentRelay.NodeID, bestRelay.NodeID), "relay-manager")
	}

	// Connect to new relay
	return rm.ConnectToRelay(bestRelay)
}

// ConnectToRelay establishes connection to a specific relay
func (rm *RelayManager) ConnectToRelay(relay *RelayCandidate) error {
	rm.logger.Info(fmt.Sprintf("Connecting to relay: %s (endpoint: %s, latency: %v)",
		relay.NodeID, relay.Endpoint, relay.Latency), "relay-manager")

	// Disconnect from current relay if any
	rm.DisconnectRelay()

	// Connect via QUIC
	conn, err := rm.quicPeer.ConnectToPeer(relay.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to connect to relay %s: %v", relay.NodeID, err)
	}

	// Send relay registration message with timeout
	streamOpenTimeout := rm.config.GetConfigDuration("quic_stream_open_timeout", 5*time.Second)
	streamCtx, cancel := context.WithTimeout(rm.ctx, streamOpenTimeout)
	stream, err := conn.OpenStreamSync(streamCtx)
	cancel()

	if err != nil {
		conn.CloseWithError(0, "failed to open stream")
		return fmt.Errorf("failed to open stream to relay: %v", err)
	}

	// Get our network info for registration
	nodeTypeManager := utils.NewNodeTypeManager()
	publicIP, _ := nodeTypeManager.GetExternalIP()
	privateIP, _ := nodeTypeManager.GetLocalIP()
	publicEndpoint := fmt.Sprintf("%s:%d", publicIP, rm.config.GetConfigInt("quic_port", 30906, 1024, 65535))
	privateEndpoint := fmt.Sprintf("%s:%d", privateIP, rm.config.GetConfigInt("quic_port", 30906, 1024, 65535))

	registerMsg := CreateRelayRegister(
		rm.keyPair.PeerID(), // Persistent Ed25519-based peer ID
		rm.dhtPeer.NodeID(), // DHT node ID
		"",    // topic - will be filled by relay if needed
		"nat", // natType
		publicEndpoint,
		privateEndpoint,
		true, // requiresRelay
	)
	msgBytes, err := registerMsg.Marshal()
	if err != nil {
		stream.Close()
		conn.CloseWithError(0, "failed to marshal message")
		return fmt.Errorf("failed to marshal register message: %v", err)
	}

	_, err = stream.Write(msgBytes)
	if err != nil {
		stream.Close()
		conn.CloseWithError(0, "failed to send message")
		return fmt.Errorf("failed to send register message: %v", err)
	}

	// Wait for acceptance
	buffer := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := stream.Read(buffer)
	stream.Close()

	if err != nil {
		conn.CloseWithError(0, "no response from relay")
		return fmt.Errorf("failed to read relay response: %v", err)
	}

	response, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		conn.CloseWithError(0, "invalid response")
		return fmt.Errorf("failed to parse relay response: %v", err)
	}

	if response.Type == MessageTypeRelayReject {
		conn.CloseWithError(0, "relay rejected connection")
		return fmt.Errorf("relay rejected connection")
	}

	if response.Type != MessageTypeRelayAccept {
		conn.CloseWithError(0, "unexpected response")
		return fmt.Errorf("unexpected response from relay: %s", response.Type)
	}

	// Parse relay accept response to get session ID
	var acceptData RelayAcceptData
	if err := response.GetDataAs(&acceptData); err != nil {
		conn.CloseWithError(0, "invalid accept data")
		return fmt.Errorf("failed to parse relay accept data: %v", err)
	}

	sessionID := acceptData.SessionID
	if sessionID == "" {
		conn.CloseWithError(0, "no session ID in response")
		return fmt.Errorf("relay did not provide session ID")
	}

	rm.logger.Debug(fmt.Sprintf("Received session ID from relay: %s", sessionID), "relay-manager")

	// Update current relay
	rm.relayMutex.Lock()
	rm.currentRelay = relay
	rm.currentRelayConn = conn
	rm.sessionID = sessionID
	rm.sessionStart = time.Now()
	rm.relayMutex.Unlock()

	// Reset traffic counters
	rm.trafficMutex.Lock()
	rm.ingressBytes = 0
	rm.egressBytes = 0
	rm.trafficMutex.Unlock()

	// Update metadata in database to include relay info
	if err := rm.updateOurMetadataWithRelay(relay, sessionID); err != nil {
		rm.logger.Warn(fmt.Sprintf("Failed to update metadata with relay info: %v", err), "relay-manager")
	}

	rm.logger.Info(fmt.Sprintf("Successfully connected to relay %s (session: %s)", relay.NodeID, sessionID), "relay-manager")

	// Start keepalive
	go rm.sendKeepalives()

	return nil
}

// DisconnectRelay disconnects from current relay
func (rm *RelayManager) DisconnectRelay() {
	rm.relayMutex.Lock()
	defer rm.relayMutex.Unlock()

	if rm.currentRelay == nil {
		return
	}

	rm.logger.Info(fmt.Sprintf("Disconnecting from relay %s", rm.currentRelay.NodeID), "relay-manager")

	// Stop keepalives
	select {
	case rm.keepaliveStop <- struct{}{}:
	default:
	}

	// Send disconnect message with timeout
	if rm.currentRelayConn != nil {
		streamOpenTimeout := rm.config.GetConfigDuration("quic_stream_open_timeout", 5*time.Second)
		streamCtx, cancel := context.WithTimeout(rm.ctx, streamOpenTimeout)
		stream, err := rm.currentRelayConn.OpenStreamSync(streamCtx)
		cancel()

		if err == nil {
			// Get actual traffic stats and duration
			rm.trafficMutex.RLock()
			ingress := rm.ingressBytes
			egress := rm.egressBytes
			rm.trafficMutex.RUnlock()

			duration := int64(0)
			if !rm.sessionStart.IsZero() {
				duration = int64(time.Since(rm.sessionStart).Seconds())
			}

			disconnectMsg := CreateRelayDisconnect(
				rm.sessionID,
				rm.dhtPeer.NodeID(),
				"switching relay",
				ingress,
				egress,
				duration,
			)
			if msgBytes, err := disconnectMsg.Marshal(); err == nil {
				stream.Write(msgBytes)
			}
			stream.Close()
		}

		// Close connection
		rm.currentRelayConn.CloseWithError(0, "disconnecting")
	}

	// Update metadata to reflect relay disconnection - publish to DHT
	if rm.metadataPublisher != nil {
		if err := rm.metadataPublisher.NotifyRelayDisconnected(); err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to publish relay disconnection to DHT: %v", err), "relay-manager")
		} else {
			rm.logger.Info("Published relay disconnection to DHT", "relay-manager")
		}
	}

	rm.currentRelay = nil
	rm.currentRelayConn = nil
	rm.sessionID = ""
	rm.sessionStart = time.Time{}

	// Clear traffic counters
	rm.trafficMutex.Lock()
	rm.ingressBytes = 0
	rm.egressBytes = 0
	rm.trafficMutex.Unlock()
}

// sendKeepalives sends periodic keepalive messages to maintain relay connection
func (rm *RelayManager) sendKeepalives() {
	keepaliveInterval := rm.config.GetConfigDuration("relay_keepalive_interval", 5*time.Second)
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.relayMutex.RLock()
			conn := rm.currentRelayConn
			sessionID := rm.sessionID
			relayNodeID := ""
			if rm.currentRelay != nil {
				relayNodeID = rm.currentRelay.NodeID
			}
			rm.relayMutex.RUnlock()

			if conn == nil {
				return
			}

			// Send ping as keepalive with timeout to prevent blocking indefinitely
			streamOpenTimeout := rm.config.GetConfigDuration("quic_stream_open_timeout", 5*time.Second)
			streamCtx, cancel := context.WithTimeout(rm.ctx, streamOpenTimeout)
			stream, err := conn.OpenStreamSync(streamCtx)
			cancel() // Clean up timeout context

			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Keepalive failed to open stream: %v - triggering reconnection", err), "relay-manager")
				go rm.handleRelayConnectionLoss()
				return
			}

			pingMsg := CreatePing(sessionID, "keepalive")
			msgBytes, err := pingMsg.Marshal()
			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Failed to marshal keepalive: %v", err), "relay-manager")
				stream.Close()
				continue
			}

			_, err = stream.Write(msgBytes)
			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Failed to write keepalive: %v - triggering reconnection", err), "relay-manager")
				stream.Close()
				go rm.handleRelayConnectionLoss()
				return
			}

			// Wait for response (pong) to ensure message was received
			buffer := make([]byte, 4096)
			stream.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, err = stream.Read(buffer)
			stream.Close()

			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Keepalive response failed from relay %s: %v - triggering reconnection", relayNodeID, err), "relay-manager")
				go rm.handleRelayConnectionLoss()
				return
			}

			// Success
			rm.logger.Debug("Sent keepalive to relay", "relay-manager")

		case <-rm.keepaliveStop:
			return

		case <-rm.ctx.Done():
			return
		}
	}
}

// handleRelayConnectionLoss handles relay connection failures and attempts reconnection
func (rm *RelayManager) handleRelayConnectionLoss() {
	rm.logger.Info("Handling relay connection loss...", "relay-manager")

	// Get the failed relay info before disconnecting
	rm.relayMutex.RLock()
	failedRelay := rm.currentRelay
	failedRelayNodeID := ""
	if failedRelay != nil {
		failedRelayNodeID = failedRelay.NodeID
	}
	rm.relayMutex.RUnlock()

	// Step 1: Disconnect from current relay (closes stale QUIC connection)
	rm.DisconnectRelay()

	// Step 2-4: Try reconnecting to the SAME relay with a NEW QUIC connection
	// This handles NAT UDP mapping expiration (30-min timeout) where the relay
	// is still reachable but the old NAT mapping is stale
	if failedRelay != nil {
		rm.logger.Info(fmt.Sprintf("Attempting to reconnect to same relay %s with new QUIC connection (may be NAT mapping timeout)...", failedRelayNodeID), "relay-manager")

		// Wait briefly to allow old connection cleanup
		time.Sleep(2 * time.Second)

		// Try connecting to the same relay with a new QUIC connection
		if err := rm.ConnectToRelay(failedRelay); err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to reconnect to same relay %s: %v", failedRelayNodeID, err), "relay-manager")
			// Will try different relay below
		} else {
			// Successfully reconnected to same relay with new connection
			rm.logger.Info(fmt.Sprintf("Successfully reconnected to same relay %s with new QUIC connection", failedRelayNodeID), "relay-manager")
			return
		}

		// Step 5: If reconnection to same relay failed, remove it and try a different relay
		rm.logger.Info(fmt.Sprintf("Removing failed relay %s from candidates", failedRelayNodeID), "relay-manager")
		rm.selector.RemoveCandidate(failedRelayNodeID)

		// Log remaining candidates
		remainingCount := rm.selector.GetCandidateCount()
		rm.logger.Info(fmt.Sprintf("Remaining relay candidates after removal: %d", remainingCount), "relay-manager")
	}

	// Wait a bit before trying a different relay
	rm.logger.Debug("Waiting 5 seconds before attempting connection to different relay...", "relay-manager")
	time.Sleep(5 * time.Second)

	// Try to connect to a different relay
	rm.logger.Info("Attempting to connect to a different relay...", "relay-manager")
	if err := rm.SelectAndConnectRelay(); err != nil {
		rm.logger.Error(fmt.Sprintf("Failed to reconnect to relay: %v", err), "relay-manager")

		// Check if we have no relay candidates left
		if rm.selector.GetCandidateCount() == 0 {
			rm.logger.Warn("No relay candidates available, attempting to re-discover relays...", "relay-manager")
			if rediscovered := rm.rediscoverRelayCandidates(); rediscovered > 0 {
				rm.logger.Info(fmt.Sprintf("Re-discovered %d relay candidates from peer metadata", rediscovered), "relay-manager")
			} else {
				rm.logger.Warn("No relay candidates found in peer metadata, will retry during next periodic evaluation", "relay-manager")
			}
		}

		// Schedule retry
		rm.logger.Info("Waiting 30 seconds before second reconnection attempt...", "relay-manager")
		time.Sleep(30 * time.Second)
		rm.logger.Info("Second reconnection attempt starting...", "relay-manager")
		if err := rm.SelectAndConnectRelay(); err != nil {
			rm.logger.Error(fmt.Sprintf("Second reconnection attempt failed: %v. Will retry during next periodic evaluation.", err), "relay-manager")

			// Final check - if still no candidates, try rediscovery again
			if rm.selector.GetCandidateCount() == 0 {
				rm.logger.Warn("Still no relay candidates after retry, re-attempting discovery...", "relay-manager")
				rm.rediscoverRelayCandidates()
			}
		}
	}
}

// rediscoverRelayCandidates queries the peer metadata database for relay peers and re-adds them
func (rm *RelayManager) rediscoverRelayCandidates() int {
	rm.logger.Info("Re-discovering relay candidates from peer metadata database...", "relay-manager")

	// Get all peers for our subscribed topics
	topics := rm.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	if len(topics) == 0 {
		rm.logger.Warn("No topics configured, cannot re-discover relays", "relay-manager")
		return 0
	}

	addedCount := 0
	for _, topic := range topics {
		// Get relay peers from known_peers (which now has is_relay flag)
		relayPeers, err := rm.dbManager.KnownPeers.GetRelayPeers(topic)
		if err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to get relay peers for topic %s: %v", topic, err), "relay-manager")
			continue
		}

		rm.logger.Debug(fmt.Sprintf("Found %d relay peers from known_peers for topic %s", len(relayPeers), topic), "relay-manager")

		// Fetch metadata from DHT and add to relay selector
		for _, peer := range relayPeers {
			rm.logger.Debug(fmt.Sprintf("Found relay peer: %s", peer.PeerID[:8]), "relay-manager")

			// Fetch metadata from DHT
			metadata, err := rm.metadataFetcher.GetPeerMetadata(peer.PublicKey)
			if err != nil {
				rm.logger.Debug(fmt.Sprintf("Failed to fetch metadata for relay %s: %v", peer.PeerID[:8], err), "relay-manager")
				continue
			}

			// Verify it's actually a relay
			rm.logger.Info(fmt.Sprintf("🔍 Checking relay metadata: peer=%s, is_relay=%v, relay_endpoint=%s, node_type=%s",
				peer.PeerID[:8], metadata.NetworkInfo.IsRelay, metadata.NetworkInfo.RelayEndpoint, metadata.NetworkInfo.NodeType), "relay-manager")

			if !metadata.NetworkInfo.IsRelay {
				rm.logger.Warn(fmt.Sprintf("❌ Peer %s metadata shows is_relay=false - REJECTING", peer.PeerID[:8]), "relay-manager")
				continue
			}

			// Add to relay selector
			if err := rm.selector.AddCandidate(metadata); err != nil {
				rm.logger.Debug(fmt.Sprintf("Failed to add relay candidate %s: %v", peer.PeerID[:8], err), "relay-manager")
			} else {
				addedCount++
				rm.logger.Info(fmt.Sprintf("✅ Added relay candidate: %s (endpoint: %s, is_relay: true)",
					peer.PeerID[:8], metadata.NetworkInfo.RelayEndpoint), "relay-manager")
			}
		}
	}

	if addedCount > 0 {
		rm.logger.Info(fmt.Sprintf("Re-discovered and added %d relay candidates", addedCount), "relay-manager")
	} else {
		rm.logger.Warn("No relay peers found in database during re-discovery", "relay-manager")
	}

	return addedCount
}

// periodicRelayEvaluation periodically evaluates and switches to better relays
func (rm *RelayManager) periodicRelayEvaluation() {
	for {
		select {
		case <-rm.evaluationTicker.C:
			rm.logger.Debug("Performing periodic relay evaluation...", "relay-manager")

			// Check if we have relay candidates, if not try to rediscover
			if rm.selector.GetCandidateCount() == 0 {
				rm.logger.Warn("No relay candidates available during periodic evaluation, attempting rediscovery...", "relay-manager")
				rm.rediscoverRelayCandidates()
			}

			// Re-evaluate and potentially switch relay
			if err := rm.SelectAndConnectRelay(); err != nil {
				rm.logger.Debug(fmt.Sprintf("Relay evaluation: %v", err), "relay-manager")
			}

		case <-rm.evaluationStop:
			return

		case <-rm.ctx.Done():
			return
		}
	}
}

// updateOurMetadataWithRelay updates our peer metadata to include relay connection info
// In DHT-only architecture, this directly publishes to DHT without local database storage
func (rm *RelayManager) updateOurMetadataWithRelay(relay *RelayCandidate, sessionID string) error {
	rm.logger.Debug(fmt.Sprintf("Publishing relay connection to DHT: relay=%s, session=%s", relay.NodeID, sessionID), "relay-manager")

	// Publish updated metadata to DHT with relay connection info
	if rm.metadataPublisher != nil {
		if err := rm.metadataPublisher.NotifyRelayConnected(relay.NodeID, sessionID, relay.Endpoint); err != nil {
			return fmt.Errorf("failed to publish relay metadata to DHT: %v", err)
		}
		rm.logger.Info(fmt.Sprintf("Published relay metadata to DHT: relay=%s, session=%s", relay.NodeID, sessionID), "relay-manager")
	} else {
		return fmt.Errorf("metadata publisher not available")
	}

	return nil
}

// GetCurrentRelay returns the currently connected relay
func (rm *RelayManager) GetCurrentRelay() *RelayCandidate {
	rm.relayMutex.RLock()
	defer rm.relayMutex.RUnlock()
	return rm.currentRelay
}

// GetSelector returns the relay selector
func (rm *RelayManager) GetSelector() *RelaySelector {
	return rm.selector
}

// GetStats returns relay manager statistics
func (rm *RelayManager) GetStats() map[string]interface{} {
	stats := rm.selector.GetStats()

	rm.relayMutex.RLock()
	currentRelay := rm.currentRelay
	sessionID := rm.sessionID
	sessionStart := rm.sessionStart
	rm.relayMutex.RUnlock()

	if currentRelay != nil {
		// Get traffic stats
		rm.trafficMutex.RLock()
		ingress := rm.ingressBytes
		egress := rm.egressBytes
		rm.trafficMutex.RUnlock()

		totalBytes := ingress + egress

		// Calculate session duration
		var durationSeconds int64
		if !sessionStart.IsZero() {
			durationSeconds = int64(time.Since(sessionStart).Seconds())
		}

		// Calculate current cost (using relay's pricing)
		// Note: Pricing comes from relay's service configuration
		var currentCost float64
		if currentRelay.PricingPerGB > 0 {
			billableGB := float64(totalBytes) / (1024 * 1024 * 1024)
			currentCost = billableGB * currentRelay.PricingPerGB
		}

		stats["connected_relay"] = currentRelay.NodeID
		stats["connected_relay_peer_id"] = currentRelay.PeerID // Persistent Ed25519-based peer ID
		stats["connected_relay_endpoint"] = currentRelay.Endpoint
		stats["connected_relay_latency"] = currentRelay.Latency.String()
		stats["connected_relay_latency_ms"] = currentRelay.Latency.Milliseconds()
		stats["connected_relay_pricing"] = currentRelay.PricingPerGB
		stats["session_id"] = sessionID
		stats["session_duration_seconds"] = durationSeconds
		stats["session_ingress_bytes"] = ingress
		stats["session_egress_bytes"] = egress
		stats["session_total_bytes"] = totalBytes
		stats["session_current_cost"] = currentCost
	} else {
		stats["connected_relay"] = nil
	}

	return stats
}

// ConnectToSpecificRelay connects to a user-selected relay by peer ID or node ID
func (rm *RelayManager) ConnectToSpecificRelay(peerIDOrNodeID string) error {
	rm.logger.Info(fmt.Sprintf("Attempting to connect to specific relay: %s", peerIDOrNodeID), "relay-manager")

	// Find the relay candidate with matching ID
	// Match against both PeerID (persistent Ed25519-based) and NodeID (DHT-based) for flexibility
	candidates := rm.selector.GetCandidates()

	var targetCandidate *RelayCandidate
	for _, candidate := range candidates {
		// Match against both PeerID (persistent) and NodeID (DHT)
		if candidate.PeerID == peerIDOrNodeID || candidate.NodeID == peerIDOrNodeID {
			targetCandidate = candidate
			break
		}
	}

	if targetCandidate == nil {
		return fmt.Errorf("relay peer %s not found in candidates", peerIDOrNodeID)
	}

	// Disconnect current relay if connected
	rm.DisconnectRelay()

	// Connect to the specific relay
	return rm.ConnectToRelay(targetCandidate)
}

// SetPreferredRelay saves a relay as the preferred relay for this peer
func (rm *RelayManager) SetPreferredRelay(myPeerID, preferredRelayPeerID string) error {
	rm.logger.Info(fmt.Sprintf("Setting preferred relay: %s", preferredRelayPeerID), "relay-manager")

	// Save to database
	relayDB, err := database.NewRelayDB(rm.dbManager.GetDB(), rm.logger)
	if err != nil {
		return fmt.Errorf("failed to access relay database: %v", err)
	}

	err = relayDB.SetPreferredRelay(myPeerID, preferredRelayPeerID)
	if err != nil {
		return fmt.Errorf("failed to set preferred relay: %v", err)
	}

	rm.logger.Debug(fmt.Sprintf("Preferred relay set to: %s", preferredRelayPeerID), "relay-manager")
	return nil
}

// GetPreferredRelay gets the preferred relay for this peer
func (rm *RelayManager) GetPreferredRelay(myPeerID string) (string, error) {
	relayDB, err := database.NewRelayDB(rm.dbManager.GetDB(), rm.logger)
	if err != nil {
		return "", fmt.Errorf("failed to access relay database: %v", err)
	}

	preferredPeerID, err := relayDB.GetPreferredRelay(myPeerID)
	if err != nil {
		return "", fmt.Errorf("failed to get preferred relay: %v", err)
	}

	return preferredPeerID, nil
}

// RecordIngressTraffic records bytes received through the relay
func (rm *RelayManager) RecordIngressTraffic(bytes int64) {
	rm.trafficMutex.Lock()
	rm.ingressBytes += bytes
	rm.trafficMutex.Unlock()
}

// RecordEgressTraffic records bytes sent through the relay
func (rm *RelayManager) RecordEgressTraffic(bytes int64) {
	rm.trafficMutex.Lock()
	rm.egressBytes += bytes
	rm.trafficMutex.Unlock()
}
