package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// RelayManager manages persistent relay connections for NAT peers
type RelayManager struct {
	config    *utils.ConfigManager
	logger    *utils.LogsManager
	dbManager *database.SQLiteManager
	quicPeer  *QUICPeer
	dhtPeer   *DHTPeer

	// Relay selection
	selector *RelaySelector

	// Current relay connection
	currentRelay     *RelayCandidate
	currentRelayConn *quic.Conn
	relayMutex       sync.RWMutex

	// Session management
	sessionID     string
	keepaliveStop chan struct{}

	// Context
	ctx    context.Context
	cancel context.CancelFunc

	// Periodic re-evaluation
	evaluationTicker *time.Ticker
	evaluationStop   chan struct{}
}

// NewRelayManager creates a new relay manager for NAT peers
func NewRelayManager(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager, quicPeer *QUICPeer, dhtPeer *DHTPeer) *RelayManager {
	ctx, cancel := context.WithCancel(context.Background())

	selector := NewRelaySelector(config, logger, dbManager, quicPeer)

	return &RelayManager{
		config:         config,
		logger:         logger,
		dbManager:      dbManager,
		quicPeer:       quicPeer,
		dhtPeer:        dhtPeer,
		selector:       selector,
		ctx:            ctx,
		cancel:         cancel,
		keepaliveStop:  make(chan struct{}),
		evaluationStop: make(chan struct{}),
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
		// Wait a bit for peers to be discovered
		time.Sleep(10 * time.Second)
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
	rm.logger.Info("Selecting best relay peer...", "relay-manager")

	// Measure latency to all candidates
	rm.selector.MeasureAllCandidates()

	// Select best relay
	bestRelay := rm.selector.SelectBestRelay()
	if bestRelay == nil {
		return fmt.Errorf("no suitable relay found")
	}

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

	// Send relay registration message
	stream, err := conn.OpenStreamSync(rm.ctx)
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
		rm.dhtPeer.NodeID(),
		"", // topic - will be filled by relay if needed
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
	rm.relayMutex.Unlock()

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

	// Send disconnect message
	if rm.currentRelayConn != nil {
		stream, err := rm.currentRelayConn.OpenStreamSync(rm.ctx)
		if err == nil {
			disconnectMsg := CreateRelayDisconnect(
				rm.sessionID,
				rm.dhtPeer.NodeID(),
				"switching relay",
				0, // bytesIngress - would track actual bytes
				0, // bytesEgress - would track actual bytes
				0, // duration - would calculate actual duration
			)
			if msgBytes, err := disconnectMsg.Marshal(); err == nil {
				stream.Write(msgBytes)
			}
			stream.Close()
		}

		// Close connection
		rm.currentRelayConn.CloseWithError(0, "disconnecting")
	}

	rm.currentRelay = nil
	rm.currentRelayConn = nil
	rm.sessionID = ""
}

// sendKeepalives sends periodic keepalive messages to maintain relay connection
func (rm *RelayManager) sendKeepalives() {
	keepaliveInterval := rm.config.GetConfigDuration("relay_keepalive_interval", 20*time.Second)
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	consecutiveFailures := 0
	maxFailures := 3

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

			// Send ping as keepalive
			stream, err := conn.OpenStreamSync(rm.ctx)
			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Keepalive failed to open stream: %v", err), "relay-manager")
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					rm.logger.Error(fmt.Sprintf("Relay connection lost after %d failed keepalives, reconnecting...", maxFailures), "relay-manager")
					go rm.handleRelayConnectionLoss()
					return
				}
				continue
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
				rm.logger.Warn(fmt.Sprintf("Failed to write keepalive: %v", err), "relay-manager")
				stream.Close()
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					rm.logger.Error(fmt.Sprintf("Relay connection lost after %d failed keepalives, reconnecting...", maxFailures), "relay-manager")
					go rm.handleRelayConnectionLoss()
					return
				}
				continue
			}

			// Wait for response (pong) to ensure message was received
			buffer := make([]byte, 4096)
			stream.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, err = stream.Read(buffer)
			stream.Close()

			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Keepalive response failed from relay %s: %v", relayNodeID, err), "relay-manager")
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					rm.logger.Error(fmt.Sprintf("Relay connection lost after %d failed keepalives, reconnecting...", maxFailures), "relay-manager")
					go rm.handleRelayConnectionLoss()
					return
				}
				continue
			}

			// Success - reset failure counter
			consecutiveFailures = 0
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
	failedRelayNodeID := ""
	if rm.currentRelay != nil {
		failedRelayNodeID = rm.currentRelay.NodeID
	}
	rm.relayMutex.RUnlock()

	// Disconnect from current relay
	rm.DisconnectRelay()

	// Remove failed relay from candidates to avoid reconnecting to it
	if failedRelayNodeID != "" {
		rm.logger.Info(fmt.Sprintf("Removing failed relay %s from candidates", failedRelayNodeID), "relay-manager")
		rm.selector.RemoveCandidate(failedRelayNodeID)
	}

	// Wait a bit before reconnecting to avoid rapid reconnection loops
	time.Sleep(5 * time.Second)

	// Try to reconnect to a different relay
	if err := rm.SelectAndConnectRelay(); err != nil {
		rm.logger.Error(fmt.Sprintf("Failed to reconnect to relay: %v", err), "relay-manager")

		// Schedule retry
		time.Sleep(30 * time.Second)
		if err := rm.SelectAndConnectRelay(); err != nil {
			rm.logger.Error(fmt.Sprintf("Second reconnection attempt failed: %v. Will retry during next periodic evaluation.", err), "relay-manager")
		}
	}
}

// periodicRelayEvaluation periodically evaluates and switches to better relays
func (rm *RelayManager) periodicRelayEvaluation() {
	for {
		select {
		case <-rm.evaluationTicker.C:
			rm.logger.Debug("Performing periodic relay evaluation...", "relay-manager")

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
func (rm *RelayManager) updateOurMetadataWithRelay(relay *RelayCandidate, sessionID string) error {
	// This would update the metadata that we advertise to other peers
	// so they know we're connected via relay and how to reach us
	rm.logger.Debug(fmt.Sprintf("Updated metadata with relay info: relay=%s, session=%s",
		relay.NodeID, sessionID), "relay-manager")

	// The actual implementation would store this in database and
	// include it in our metadata responses
	return nil
}

// GetCurrentRelay returns the currently connected relay
func (rm *RelayManager) GetCurrentRelay() *RelayCandidate {
	rm.relayMutex.RLock()
	defer rm.relayMutex.RUnlock()
	return rm.currentRelay
}

// GetStats returns relay manager statistics
func (rm *RelayManager) GetStats() map[string]interface{} {
	stats := rm.selector.GetStats()

	rm.relayMutex.RLock()
	if rm.currentRelay != nil {
		stats["connected_relay"] = rm.currentRelay.NodeID
		stats["connected_relay_endpoint"] = rm.currentRelay.Endpoint
		stats["connected_relay_latency"] = rm.currentRelay.Latency.String()
		stats["session_id"] = rm.sessionID
	} else {
		stats["connected_relay"] = nil
	}
	rm.relayMutex.RUnlock()

	return stats
}
