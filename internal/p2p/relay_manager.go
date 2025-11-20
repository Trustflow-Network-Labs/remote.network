package p2p

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
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
	metadataQuery     *MetadataQueryService // For subscribing to relay discoveries

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

	// Callbacks
	onRelayReconnected func() // Called after successful relay (re)connection
}

// NewRelayManager creates a new relay manager for NAT peers
func NewRelayManager(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager, keyPair *crypto.KeyPair, quicPeer *QUICPeer, dhtPeer *DHTPeer, metadataPublisher *MetadataPublisher, metadataFetcher *MetadataFetcher, metadataQuery *MetadataQueryService) *RelayManager {
	ctx, cancel := context.WithCancel(context.Background())

	// Pass our peer ID to selector so it can check for preferred relay
	selector := NewRelaySelector(config, logger, dbManager, quicPeer, keyPair.PeerID())

	return &RelayManager{
		config:            config,
		logger:            logger,
		dbManager:         dbManager,
		keyPair:           keyPair,
		quicPeer:          quicPeer,
		dhtPeer:           dhtPeer,
		metadataPublisher: metadataPublisher,
		metadataFetcher:   metadataFetcher,
		metadataQuery:     metadataQuery,
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

	// Subscribe to relay discoveries from metadata query service
	// This allows auto-discovery of relays as their metadata becomes available in DHT
	if rm.metadataQuery != nil {
		rm.metadataQuery.SetRelayDiscoveryCallback(func(metadata *database.PeerMetadata) {
			rm.logger.Debug(fmt.Sprintf("Metadata query service discovered relay: %s", metadata.PeerID[:8]), "relay-manager")

			// Add to relay selector if not already a candidate
			if !rm.selector.HasCandidate(metadata.PeerID) {
				if err := rm.selector.AddCandidate(metadata); err == nil {
					rm.logger.Info(fmt.Sprintf("✅ Auto-added relay from metadata discovery: %s (endpoint: %s)",
						metadata.PeerID[:8], metadata.NetworkInfo.RelayEndpoint), "relay-manager")

					// Measure latency asynchronously for the newly discovered relay
					go func(peerID, endpoint string) {
						rm.logger.Debug(fmt.Sprintf("Measuring latency for newly discovered relay %s...", peerID[:8]), "relay-manager")

						// Use PeerID for measurement lookup
						if _, err := rm.selector.MeasureCandidate(peerID); err != nil {
							rm.logger.Warn(fmt.Sprintf("Failed to measure latency for relay %s: %v", peerID[:8], err), "relay-manager")
							return
						}

						rm.logger.Info(fmt.Sprintf("✅ Measured latency for newly discovered relay %s", peerID[:8]), "relay-manager")

						// Check if we have an active relay
						rm.relayMutex.RLock()
						hasActiveRelay := rm.currentRelay != nil
						rm.relayMutex.RUnlock()

						if hasActiveRelay {
							// OPTION C: We have an active relay, disconnect immediately after measurement
							rm.quicPeer.DisconnectFromPeer(endpoint)
							rm.logger.Debug(fmt.Sprintf("Cleaned up connection after measuring relay %s (have active relay)", peerID[:8]), "relay-manager")
						} else {
							// OPTION B: No active relay, keep connection and let SelectAndConnectRelay() reuse it
							rm.logger.Info("No relay connected, attempting to select and connect after measurement...", "relay-manager")
							if err := rm.SelectAndConnectRelay(); err != nil {
								rm.logger.Warn(fmt.Sprintf("Failed to connect to relay: %v", err), "relay-manager")
								// Cleanup connection since we failed to use it
								rm.quicPeer.DisconnectFromPeer(endpoint)
							} else {
								// SelectAndConnectRelay succeeded - cleanup non-selected relays
								rm.relayMutex.RLock()
								selectedRelay := rm.currentRelay
								rm.relayMutex.RUnlock()

								// Disconnect if this relay wasn't selected
								if selectedRelay == nil || selectedRelay.PeerID != peerID {
									rm.quicPeer.DisconnectFromPeer(endpoint)
									rm.logger.Debug(fmt.Sprintf("Cleaned up connection after measuring relay %s (not selected)", peerID[:8]), "relay-manager")
								}
							}
						}
					}(metadata.PeerID, metadata.NetworkInfo.RelayEndpoint)
				} else {
					rm.logger.Debug(fmt.Sprintf("Failed to add auto-discovered relay %s: %v", metadata.PeerID[:8], err), "relay-manager")
				}
			} else {
				rm.logger.Debug(fmt.Sprintf("Relay %s already in candidates, skipping", metadata.PeerID[:8]), "relay-manager")
			}
		})
		rm.logger.Info("Subscribed to relay discoveries from metadata query service", "relay-manager")
	}

	// Start periodic relay evaluation
	evaluationInterval := rm.config.GetConfigDuration("relay_evaluation_interval", 30*time.Minute)
	rm.evaluationTicker = time.NewTicker(evaluationInterval)

	go rm.periodicRelayEvaluation()

	// Note: No initial relay selection needed here
	// Relays will be discovered via metadata query service callbacks (line 86-143)
	// Each discovery triggers async measurement and SelectAndConnectRelay() if no relay is connected
	// This prevents race conditions and allows natural, event-driven relay selection

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

// SelectAndConnectRelay selects the best relay from existing measurements and establishes connection
// Note: This function does NOT measure latencies - it assumes measurements are already done
// For periodic evaluation that requires fresh measurements, use EvaluateAndSelectRelay() instead
func (rm *RelayManager) SelectAndConnectRelay() error {
	candidateCount := rm.selector.GetCandidateCount()
	rm.logger.Info(fmt.Sprintf("🎯 Selecting best relay peer from %d candidates (using existing measurements)...", candidateCount), "relay-manager")

	// Select best relay from existing latency measurements
	bestRelay := rm.selector.SelectBestRelay()
	if bestRelay == nil {
		rm.logger.Warn(fmt.Sprintf("❌ No suitable relay found (had %d candidates)", candidateCount), "relay-manager")
		return fmt.Errorf("no suitable relay found")
	}

	rm.logger.Info(fmt.Sprintf("🏆 Best relay selected: %s (endpoint: %s, latency: %v)",
		bestRelay.PeerID[:8], bestRelay.Endpoint, bestRelay.Latency), "relay-manager")

	// Check if we should switch
	rm.relayMutex.RLock()
	currentRelay := rm.currentRelay
	rm.relayMutex.RUnlock()

	if currentRelay != nil {
		if !rm.selector.ShouldSwitchRelay(currentRelay, bestRelay) {
			rm.logger.Info(fmt.Sprintf("Current relay %s is still optimal", currentRelay.PeerID[:8]), "relay-manager")
			return nil
		}
		rm.logger.Info(fmt.Sprintf("Switching from relay %s to %s", currentRelay.PeerID[:8], bestRelay.PeerID[:8]), "relay-manager")
	}

	// Connect to new relay
	return rm.ConnectToRelay(bestRelay)
}

// EvaluateAndSelectRelay measures all relay candidates, selects the best, and connects
// Used for initial selection and periodic re-evaluation
func (rm *RelayManager) EvaluateAndSelectRelay() error {
	candidateCount := rm.selector.GetCandidateCount()
	rm.logger.Info(fmt.Sprintf("🔍 Evaluating %d relay candidates (measuring latencies)...", candidateCount), "relay-manager")

	// Capture existing connections BEFORE measurement to avoid closing them
	// (e.g., ongoing file downloads, service connections to peers)
	existingConnections := rm.quicPeer.GetConnectionAddressesMap()
	rm.logger.Debug(fmt.Sprintf("Pre-measurement: %d existing connections to preserve", len(existingConnections)), "relay-manager")

	// Measure latency to all candidates (may reuse existing connections)
	measuredEndpoints := rm.selector.MeasureAllCandidates()

	// Select best relay from measurements and connect
	err := rm.SelectAndConnectRelay()

	// Get current relay to preserve its connection
	rm.relayMutex.RLock()
	currentRelay := rm.currentRelay
	rm.relayMutex.RUnlock()

	// Filter: only cleanup NEW connections created during measurement
	// Exclude: 1) pre-existing connections, 2) current relay connection
	newConnections := make([]string, 0, len(measuredEndpoints))
	for _, endpoint := range measuredEndpoints {
		// Skip if this was a pre-existing connection
		if existingConnections[endpoint] {
			rm.logger.Debug(fmt.Sprintf("Preserving pre-existing connection to %s", endpoint), "relay-manager")
			continue
		}
		// Skip if this is the current relay
		if currentRelay != nil && endpoint == currentRelay.Endpoint {
			continue
		}
		newConnections = append(newConnections, endpoint)
	}

	// Cleanup only NEW connections created during measurement
	if len(newConnections) > 0 {
		disconnected := rm.quicPeer.DisconnectFromPeers(newConnections, "")
		rm.logger.Debug(fmt.Sprintf("Cleaned up %d new connections (preserved %d pre-existing)",
			disconnected, len(existingConnections)), "relay-manager")
	}

	return err
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

	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
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

	// Update LastSeen to prevent this relay from being marked as stale
	rm.selector.UpdateCandidateLastSeen(relay.NodeID)

	// Reset failure count on successful connection
	relay.FailureCount = 0
	relay.LastFailure = time.Time{}

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

	// Open receive stream for relay to forward messages to us
	if err := rm.openReceiveStream(conn, sessionID); err != nil {
		rm.logger.Error(fmt.Sprintf("Failed to open receive stream: %v", err), "relay-manager")
		conn.CloseWithError(0, "failed to open receive stream")
		return fmt.Errorf("failed to open receive stream: %v", err)
	}

	// Start keepalive
	go rm.sendKeepalives()

	// Trigger relay reconnection callback (for transfer resumption, etc.)
	if rm.onRelayReconnected != nil {
		go rm.onRelayReconnected() // Run async to avoid blocking connection
	}

	return nil
}

// openReceiveStream opens a long-lived stream for receiving forwarded messages from relay
func (rm *RelayManager) openReceiveStream(conn *quic.Conn, sessionID string) error {
	rm.logger.Debug("Opening receive stream to relay for incoming message forwarding...", "relay-manager")

	// Open stream with timeout
	streamOpenTimeout := rm.config.GetConfigDuration("quic_stream_open_timeout", 5*time.Second)
	streamCtx, cancel := context.WithTimeout(rm.ctx, streamOpenTimeout)
	stream, err := conn.OpenStreamSync(streamCtx)
	cancel()

	if err != nil {
		return fmt.Errorf("failed to open receive stream: %v", err)
	}

	// Send relay_receive_stream_ready message
	readyMsg := CreateRelayReceiveStreamReady(sessionID, rm.keyPair.PeerID())
	msgBytes, err := readyMsg.Marshal()
	if err != nil {
		stream.Close()
		return fmt.Errorf("failed to marshal receive stream ready message: %v", err)
	}

	if _, err := stream.Write(msgBytes); err != nil {
		stream.Close()
		return fmt.Errorf("failed to send receive stream ready message: %v", err)
	}

	rm.logger.Info(fmt.Sprintf("✅ Receive stream opened and registered with relay (session: %s)", sessionID), "relay-manager")

	// Start listening for incoming messages on this stream
	go rm.listenForRelayedMessages(stream, sessionID)

	return nil
}

// listenForRelayedMessages continuously reads messages from the receive stream
func (rm *RelayManager) listenForRelayedMessages(stream *quic.Stream, sessionID string) {
	defer (*stream).Close()

	rm.logger.Info(fmt.Sprintf("Started listening for relayed messages (session: %s)", sessionID), "relay-manager")

	for {
		select {
		case <-rm.ctx.Done():
			rm.logger.Debug("Context cancelled, stopping relay message listener", "relay-manager")
			return
		default:
		}

		// Read length prefix (4 bytes)
		lengthBuf := make([]byte, 4)
		if _, err := (*stream).Read(lengthBuf); err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to read message length from relay receive stream: %v", err), "relay-manager")
			// Stream closed or error - trigger reconnection
			go rm.handleRelayConnectionLoss()
			return
		}

		// Parse length
		messageLength := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

		// DEBUG: Log message length
		rm.logger.Info(fmt.Sprintf("DEBUG: Received message length prefix: %d bytes", messageLength), "relay-manager")

		if messageLength <= 0 || messageLength > 10*1024*1024 { // Max 10MB
			rm.logger.Warn(fmt.Sprintf("Invalid message length from relay: %d bytes", messageLength), "relay-manager")
			continue
		}

		// Read the full message
		messageBuf := make([]byte, messageLength)
		totalRead := 0
		for totalRead < messageLength {
			n, err := (*stream).Read(messageBuf[totalRead:])
			if err != nil {
				rm.logger.Warn(fmt.Sprintf("Failed to read message data from relay: %v", err), "relay-manager")
				go rm.handleRelayConnectionLoss()
				return
			}
			totalRead += n
		}

		// Record ingress traffic
		rm.RecordIngressTraffic(int64(4 + messageLength))

		// Parse and handle the message
		msg, err := UnmarshalQUICMessage(messageBuf)
		if err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to unmarshal relayed message (%d bytes): %v", len(messageBuf), err), "relay-manager")
			continue
		}

		// DEBUG: Log received message type and size
		rm.logger.Info(fmt.Sprintf("DEBUG: Successfully unmarshaled message type=%s, size=%d bytes", msg.Type, len(messageBuf)), "relay-manager")
		rm.logger.Debug(fmt.Sprintf("Received relayed message via relay: type=%s", msg.Type), "relay-manager")

		// Route message to appropriate handler based on type
		if err := rm.handleRelayedMessage(msg); err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to handle relayed message (type=%s): %v", msg.Type, err), "relay-manager")
		}
	}
}

// handleRelayedMessage routes relayed messages to appropriate handlers
func (rm *RelayManager) handleRelayedMessage(msg *QUICMessage) error {
	rm.logger.Info(fmt.Sprintf("Received message type %s via relay - routing to handlers", msg.Type), "relay-manager")

	// Extract source peer ID - first try message envelope (injected by relay)
	sourcePeerID := msg.SourcePeerID

	// Fallback: extract from message payload for backwards compatibility
	if sourcePeerID == "" {
		switch msg.Type {
		case MessageTypeServiceRequest:
			var request ServiceSearchRequest
			if err := msg.GetDataAs(&request); err == nil {
				sourcePeerID = request.SourcePeerID
			}
		case MessageTypeJobRequest:
			var request types.JobExecutionRequest
			if err := msg.GetDataAs(&request); err == nil {
				sourcePeerID = request.OrderingPeerID
			}
		case MessageTypeJobDataTransferRequest:
			var request types.JobDataTransferRequest
			if err := msg.GetDataAs(&request); err == nil {
				sourcePeerID = request.SourcePeerID
			}
		}
	}

	// Use a default identifier if source peer ID couldn't be extracted
	if sourcePeerID == "" {
		sourcePeerID = "unknown-relay-source"
		rm.logger.Warn(fmt.Sprintf("Could not extract source peer ID from %s message", msg.Type), "relay-manager")
	} else {
		rm.logger.Debug(fmt.Sprintf("Extracted source peer ID: %s from %s message", sourcePeerID[:8], msg.Type), "relay-manager")
	}

	// Recover from panics in message handlers and generate error responses
	var response *QUICMessage
	func() {
		defer func() {
			if r := recover(); r != nil {
				rm.logger.Error(fmt.Sprintf("PANIC while handling relayed message (type=%s): %v", msg.Type, r), "relay-manager")

				// Generate appropriate error response based on message type
				if sourcePeerID != "unknown-relay-source" {
					switch msg.Type {
					case MessageTypeJobRequest:
						response = CreateJobResponse(&types.JobExecutionResponse{
							Accepted: false,
							Message:  fmt.Sprintf("Internal error while processing job request: %v", r),
						})
					case MessageTypeJobStatusRequest:
						response = CreateJobStatusResponse(&types.JobStatusResponse{
							Found: false,
						})
					case MessageTypeJobDataTransferRequest:
						response = CreateJobDataTransferResponse(&types.JobDataTransferResponse{
							Accepted: false,
							Message:  fmt.Sprintf("Internal error while processing transfer request: %v", r),
						})
					case MessageTypeServiceRequest:
						response = CreateServiceSearchResponse(nil, fmt.Sprintf("Internal error: %v", r))
					}
				}
			}
		}()

		// Delegate to QUIC peer to handle the message
		response = rm.quicPeer.HandleRelayedMessage(msg, sourcePeerID)
	}()

	if response != nil {
		rm.logger.Debug(fmt.Sprintf("Message handled, response type: %s - routing back via relay", response.Type), "relay-manager")

		// Send response back through relay
		if sourcePeerID == "unknown-relay-source" {
			rm.logger.Warn("Cannot route response - source peer ID not available in request", "relay-manager")
			return nil
		}

		if err := rm.sendResponseViaRelay(sourcePeerID, response); err != nil {
			rm.logger.Error(fmt.Sprintf("Failed to send response via relay: %v", err), "relay-manager")
			return err
		}

		// Safe logging - check length before slicing
		peerIDLog := sourcePeerID
		if len(sourcePeerID) > 8 {
			peerIDLog = sourcePeerID[:8]
		}
		rm.logger.Info(fmt.Sprintf("Successfully sent %s response to %s via relay", response.Type, peerIDLog), "relay-manager")
	}

	return nil
}

// sendResponseViaRelay sends a response message back through the relay to the original requester
func (rm *RelayManager) sendResponseViaRelay(targetPeerID string, response *QUICMessage) error {
	// Get current relay connection and session
	rm.relayMutex.RLock()
	relayConn := rm.currentRelayConn
	sessionID := rm.sessionID
	rm.relayMutex.RUnlock()

	if relayConn == nil {
		return fmt.Errorf("no active relay connection")
	}

	if sessionID == "" {
		return fmt.Errorf("no active relay session")
	}

	// Marshal the response message
	responseBytes, err := response.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal response: %v", err)
	}

	// Create relay forward message
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    sessionID,
		SourcePeerID: rm.keyPair.PeerID(),
		TargetPeerID: targetPeerID,
		MessageType:  "service_response",
		Payload:      responseBytes,
		PayloadSize:  int64(len(responseBytes)),
	})

	forwardBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Open a stream to the relay
	stream, err := rm.quicPeer.openStreamWithTimeout(relayConn)
	if err != nil {
		return fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send the message
	if _, err := stream.Write(forwardBytes); err != nil {
		return fmt.Errorf("failed to write to relay: %v", err)
	}

	// Record egress traffic
	rm.RecordEgressTraffic(int64(len(forwardBytes)))

	rm.logger.Debug(fmt.Sprintf("Sent response (%d bytes) to relay for forwarding to %s", len(forwardBytes), targetPeerID[:8]), "relay-manager")
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
			n, err := stream.Read(buffer)
			stream.Close()

			if err != nil && (err != io.EOF || n == 0) {
				// Only fail if we got an error other than EOF, or EOF with no data
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
	rm.logger.Info("⚠️  Relay connection lost, starting immediate reconnection procedure", "relay-manager")

	// Get the failed relay info before disconnecting
	rm.relayMutex.RLock()
	failedRelay := rm.currentRelay
	failedRelayNodeID := ""
	if failedRelay != nil {
		failedRelayNodeID = failedRelay.NodeID
	}
	rm.relayMutex.RUnlock()

	if failedRelay == nil {
		rm.logger.Warn("No current relay to reconnect", "relay-manager")
		return
	}

	// PHASE 1: Immediate retry to same relay (3 attempts, 2-second intervals)
	const MAX_RETRIES = 3
	const RETRY_DELAY = 2 * time.Second

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {
		rm.logger.Info(fmt.Sprintf("🔄 Attempting to reconnect to same relay (relay: %s, attempt: %d/%d)",
			failedRelayNodeID[:8], attempt, MAX_RETRIES), "relay-manager")

		// Step 1: Clean disconnect
		rm.DisconnectRelay()

		// Step 2: Brief wait
		time.Sleep(RETRY_DELAY)

		// Step 3: Attempt reconnection
		if err := rm.ConnectToRelay(failedRelay); err == nil {
			rm.logger.Info(fmt.Sprintf("✅ Successfully reconnected to same relay (relay: %s, attempt: %d)",
				failedRelayNodeID[:8], attempt), "relay-manager")
			return
		} else {
			rm.logger.Warn(fmt.Sprintf("❌ Reconnection attempt failed (relay: %s, attempt: %d, error: %v)",
				failedRelayNodeID[:8], attempt, err), "relay-manager")
		}
	}

	// PHASE 2: Mark relay as problematic
	rm.logger.Warn(fmt.Sprintf("Failed to reconnect to relay after %d attempts, marking as offline (relay: %s)",
		MAX_RETRIES, failedRelayNodeID[:8]), "relay-manager")

	// Increment failure count
	failedRelay.FailureCount++
	failedRelay.LastFailure = time.Now()

	// Remove if too many failures
	const MAX_FAILURES = 3
	if failedRelay.FailureCount >= MAX_FAILURES {
		rm.logger.Info(fmt.Sprintf("Removing failed relay from candidates (relay: %s, failure_count: %d)",
			failedRelayNodeID[:8], failedRelay.FailureCount), "relay-manager")
		rm.selector.RemoveCandidate(failedRelay.NodeID)
	}

	// PHASE 3: Select alternative relay from existing measurements (fast failover)
	rm.logger.Info("🔍 Selecting alternative relay for immediate connection (using existing measurements)", "relay-manager")

	if err := rm.SelectAndConnectRelay(); err != nil {
		rm.logger.Error(fmt.Sprintf("❌ Failed to connect to alternative relay: %v", err), "relay-manager")

		// PHASE 4: Rediscovery if no candidates
		if rm.selector.GetCandidateCount() == 0 {
			rm.logger.Warn("No relay candidates available, triggering immediate rediscovery", "relay-manager")
			discovered := rm.rediscoverRelayCandidates()

			if discovered > 0 {
				rm.logger.Info(fmt.Sprintf("Rediscovery completed, measuring and connecting (discovered: %d)", discovered), "relay-manager")
				// Rediscovered candidates have no latency measurements, must evaluate first
				if err := rm.EvaluateAndSelectRelay(); err != nil {
					rm.logger.Error(fmt.Sprintf("❌ Failed to connect after rediscovery: %v", err), "relay-manager")
					// Ensure DHT is updated to reflect no relay connection
					rm.ensureDHTDisconnected()
				}
			} else {
				rm.logger.Error("No relay candidates found after rediscovery", "relay-manager")
				// Ensure DHT is updated to reflect no relay connection
				rm.ensureDHTDisconnected()
			}
		} else {
			// Had candidates but SelectAndConnectRelay failed - ensure DHT reflects disconnected state
			rm.ensureDHTDisconnected()
		}
	} else {
		rm.logger.Info("✅ Successfully connected to alternative relay", "relay-manager")
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
			rm.logger.Info("⏰ Performing periodic relay evaluation (re-measuring all candidates)...", "relay-manager")

			// Check if we have relay candidates, if not try to rediscover
			if rm.selector.GetCandidateCount() == 0 {
				rm.logger.Warn("No relay candidates available during periodic evaluation, attempting rediscovery...", "relay-manager")
				rm.rediscoverRelayCandidates()
			}

			// Re-measure all candidates and potentially switch relay
			if err := rm.EvaluateAndSelectRelay(); err != nil {
				rm.logger.Debug(fmt.Sprintf("Periodic relay evaluation: %v", err), "relay-manager")
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
	rm.logger.Debug(fmt.Sprintf("Publishing relay connection to DHT: relay=%s (peer_id=%s), session=%s", relay.NodeID, relay.PeerID, sessionID), "relay-manager")

	// Publish updated metadata to DHT with relay connection info
	// Use PeerID (persistent identifier) instead of NodeID (which can change on restart)
	if rm.metadataPublisher != nil {
		if err := rm.metadataPublisher.NotifyRelayConnected(relay.PeerID, sessionID, relay.Endpoint); err != nil {
			return fmt.Errorf("failed to publish relay metadata to DHT: %v", err)
		}
		rm.logger.Info(fmt.Sprintf("Published relay metadata to DHT: relay_peer_id=%s, session=%s", relay.PeerID, sessionID), "relay-manager")
	} else {
		return fmt.Errorf("metadata publisher not available")
	}

	return nil
}

// ensureDHTDisconnected ensures DHT metadata is updated to reflect no relay connection
// This is called when all reconnection attempts fail to guarantee DHT consistency
func (rm *RelayManager) ensureDHTDisconnected() {
	if rm.metadataPublisher != nil {
		if err := rm.metadataPublisher.NotifyRelayDisconnected(); err != nil {
			rm.logger.Warn(fmt.Sprintf("Failed to publish relay disconnection to DHT: %v", err), "relay-manager")
		} else {
			rm.logger.Info("Published relay disconnection to DHT after connection loss", "relay-manager")
		}
	}
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

// SetRelayReconnectedCallback sets a callback to be called after successful relay (re)connection
func (rm *RelayManager) SetRelayReconnectedCallback(callback func()) {
	rm.onRelayReconnected = callback
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

// DisconnectCurrentRelay disconnects from the current relay
// This is a wrapper around DisconnectRelay() for use by network state monitor
func (rm *RelayManager) DisconnectCurrentRelay() {
	rm.logger.Info("Disconnecting from current relay (requested by network state monitor)", "relay-manager")
	rm.DisconnectRelay()
}

// ReconnectRelay disconnects from current relay and establishes a new relay connection
// This is used when network configuration changes (IP or NAT type changes)
func (rm *RelayManager) ReconnectRelay() error {
	rm.logger.Info("Reconnecting to relay with new network configuration...", "relay-manager")

	// Step 1: Disconnect from current relay if connected
	rm.relayMutex.RLock()
	hasCurrentRelay := rm.currentRelay != nil
	rm.relayMutex.RUnlock()

	if hasCurrentRelay {
		rm.logger.Info("Disconnecting from current relay before reconnection...", "relay-manager")
		rm.DisconnectRelay()

		// Wait briefly for clean disconnect
		time.Sleep(1 * time.Second)
	}

	// Step 2: Clear relay candidates to force re-discovery with new network conditions
	// This ensures we select the best relay for our new network configuration
	if rm.selector != nil {
		rm.logger.Info("Clearing relay candidates for fresh discovery...", "relay-manager")
		rm.selector.ClearCandidates()
	}

	// Step 3: Rediscover relay candidates
	rm.logger.Info("Rediscovering relay candidates with new network configuration...", "relay-manager")
	relayCount := rm.rediscoverRelayCandidates()
	if relayCount == 0 {
		return fmt.Errorf("no relay candidates found after network change")
	}
	rm.logger.Info(fmt.Sprintf("Discovered %d relay candidates", relayCount), "relay-manager")

	// Step 4: Measure and select best relay for new network conditions
	rm.logger.Info("Measuring latencies and selecting best relay for new network configuration...", "relay-manager")
	err := rm.EvaluateAndSelectRelay()
	if err != nil {
		return fmt.Errorf("failed to connect to relay after network change: %v", err)
	}

	rm.logger.Info("Successfully reconnected to relay with new network configuration", "relay-manager")
	return nil
}
