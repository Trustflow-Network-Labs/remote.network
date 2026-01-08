package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/payment"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// RelaySession represents an active relay session
type RelaySession struct {
	SessionID         string
	ClientPeerID      string // Persistent Ed25519-based peer ID
	ClientNodeID      string // DHT node ID (may change on restart)
	RelayNodeID       string
	SessionType       string // "coordination" or "full_relay"
	Connection        *quic.Conn
	ReceiveStream     *quic.Stream // Pre-opened stream from NAT peer for receiving forwarded messages
	StartTime         time.Time
	LastKeepalive     time.Time
	KeepaliveInterval time.Duration
	IngressBytes      int64
	EgressBytes       int64
	PaymentID         int64  // x402 payment escrow ID (0 if no payment)
	EstimatedBytes    int64  // Estimated data transfer for payment calculation
	mutex             sync.RWMutex
}

// PendingRelayRequest tracks a request waiting for response
type PendingRelayRequest struct {
	Stream        *quic.Stream
	RequestTime   time.Time
	SourcePeerID  string
	TargetPeerID  string
}

// RelayPeer manages relay functionality
type RelayPeer struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	dbManager       *database.SQLiteManager
	escrowManager   *payment.EscrowManager // x402 payment escrow manager
	ctx             context.Context
	cancel          context.CancelFunc

	// Relay mode configuration
	isRelayMode     bool
	maxConnections  int
	pricingPerGB    float64
	freeCoordination bool
	maxCoordMsgSize int64

	// Active sessions
	sessions        map[string]*RelaySession // key: sessionID
	clientSessions  map[string][]*RelaySession // key: clientNodeID
	sessionsMutex   sync.RWMutex

	// Registered clients (NAT peers using this relay)
	registeredClients map[string]*RelaySession // key: clientNodeID
	clientsMutex      sync.RWMutex

	// Pending requests awaiting responses (for request-response correlation)
	pendingRequests map[string]*PendingRelayRequest // key: correlationID (sourcePeerID:targetPeerID:timestamp)
	pendingMutex    sync.RWMutex
}

// NewRelayPeer creates a new relay peer
func NewRelayPeer(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager) *RelayPeer {
	ctx, cancel := context.WithCancel(context.Background())

	// Read relay configuration
	isRelayMode := config.GetConfigBool("relay_mode", false)
	maxConnections := config.GetConfigInt("relay_max_connections", 100, 1, 1000)
	freeCoordination := config.GetConfigBool("relay_free_coordination", true)
	maxCoordMsgSize := config.GetConfigInt64("relay_max_coordination_msg_size", 10240, 1024, 1048576)

	// Get pricing from services database
	// Relay is a service like file storage, so pricing comes from the service configuration
	pricingPerGB := 0.0
	services, err := dbManager.GetAllServices()
	if err == nil {
		// Find relay service
		for _, service := range services {
			if service.Type == "relay" {
				pricingPerGB = service.Pricing
				logger.Debug(fmt.Sprintf("Found relay service with pricing: %.4f/GB", pricingPerGB), "relay")
				break
			}
		}
	}

	// Fall back to config if no relay service found
	if pricingPerGB == 0.0 {
		pricingPerGB = config.GetConfigFloat64("relay_pricing_per_gb", 0.001, 0, 1.0)
		logger.Debug(fmt.Sprintf("No relay service found, using config pricing: %.4f/GB", pricingPerGB), "relay")
	}

	// Initialize x402 payment infrastructure for relay
	x402Client := payment.NewX402Client(config, logger)
	escrowManager := payment.NewEscrowManager(dbManager, x402Client, config, logger)

	rp := &RelayPeer{
		config:            config,
		logger:            logger,
		dbManager:         dbManager,
		escrowManager:     escrowManager,
		ctx:               ctx,
		cancel:            cancel,
		isRelayMode:       isRelayMode,
		maxConnections:    maxConnections,
		pricingPerGB:      pricingPerGB,
		freeCoordination:  freeCoordination,
		maxCoordMsgSize:   maxCoordMsgSize,
		sessions:          make(map[string]*RelaySession),
		clientSessions:    make(map[string][]*RelaySession),
		registeredClients: make(map[string]*RelaySession),
		pendingRequests:   make(map[string]*PendingRelayRequest),
	}

	if isRelayMode {
		logger.Info(fmt.Sprintf("Relay mode enabled: max_connections=%d, pricing=%.4f/GB, free_coordination=%v",
			maxConnections, pricingPerGB, freeCoordination), "relay")
	}

	return rp
}

// IsRelayMode returns true if this node is running in relay mode
func (rp *RelayPeer) IsRelayMode() bool {
	return rp.isRelayMode
}

// HandleRelayRegister processes relay registration requests from NAT peers
func (rp *RelayPeer) HandleRelayRegister(msg *QUICMessage, conn *quic.Conn, remoteAddr string) *QUICMessage {
	if !rp.isRelayMode {
		rp.logger.Warn(fmt.Sprintf("Relay registration from %s rejected: relay mode disabled", remoteAddr), "relay")
		return CreateRelayReject("", "", "relay mode not enabled on this node")
	}

	var data RelayRegisterData
	if err := msg.GetDataAs(&data); err != nil {
		rp.logger.Error(fmt.Sprintf("Failed to parse relay register from %s: %v", remoteAddr, err), "relay")
		return CreateRelayReject("", data.NodeID, "invalid registration data")
	}

	rp.logger.Info(fmt.Sprintf("Relay registration request from %s (NAT: %s, requires_relay: %v)",
		data.NodeID, data.NATType, data.RequiresRelay), "relay")

	// Check if we can accept more connections
	rp.sessionsMutex.RLock()
	currentConnections := len(rp.sessions)
	rp.sessionsMutex.RUnlock()

	if currentConnections >= rp.maxConnections {
		rp.logger.Warn(fmt.Sprintf("Relay registration from %s rejected: max connections reached (%d/%d)",
			data.NodeID, currentConnections, rp.maxConnections), "relay")
		return CreateRelayReject("", data.NodeID, "relay at maximum capacity")
	}

	// PAYMENT VERIFICATION (x402 protocol)
	// Calculate required payment based on estimated data size and relay pricing
	// Note: If pricingPerGB = 0, relay is free and doesn't require payment
	var paymentID int64
	billableGB := float64(data.EstimatedBytes) / (1024 * 1024 * 1024)
	requiredAmount := billableGB * rp.pricingPerGB

	// Payment is mandatory for paid relays (requiredAmount > 0)
	if requiredAmount > 0 {
		// Payment signature is mandatory
		if data.PaymentSignature == nil {
			rp.logger.Warn(fmt.Sprintf("Relay registration from %s rejected: payment required (estimated: %d bytes, %.6f USDC)",
				data.NodeID, data.EstimatedBytes, requiredAmount), "relay")
			return CreateRelayReject("", data.NodeID, fmt.Sprintf("Payment signature required (%.6f USDC)", requiredAmount))
		}

		// Convert payment signature from interface{} to *payment.PaymentSignature
		paymentSig, ok := data.PaymentSignature.(*payment.PaymentSignature)
		if !ok {
			// Try to unmarshal if it's a map
			if sigMap, isMap := data.PaymentSignature.(map[string]interface{}); isMap {
				paymentSig = &payment.PaymentSignature{}
				if network, ok := sigMap["network"].(string); ok {
					paymentSig.Network = network
				}
				if sender, ok := sigMap["sender"].(string); ok {
					paymentSig.Sender = sender
				}
				if recipient, ok := sigMap["recipient"].(string); ok {
					paymentSig.Recipient = recipient
				}
				if amount, ok := sigMap["amount"].(float64); ok {
					paymentSig.Amount = amount
				}
				if currency, ok := sigMap["currency"].(string); ok {
					paymentSig.Currency = currency
				}
				if nonce, ok := sigMap["nonce"].(string); ok {
					paymentSig.Nonce = nonce
				}
				if signature, ok := sigMap["signature"].(string); ok {
					paymentSig.Signature = signature
				}
				if timestamp, ok := sigMap["timestamp"].(float64); ok {
					paymentSig.Timestamp = int64(timestamp)
				}
				if metadata, ok := sigMap["metadata"].(map[string]interface{}); ok {
					paymentSig.Metadata = metadata
				}
			} else {
				rp.logger.Error(fmt.Sprintf("Invalid payment signature type from %s", data.NodeID), "relay")
				return CreateRelayReject("", data.NodeID, "Invalid payment signature format")
			}
		}

		// Create escrow (verifies payment with facilitator)
		// Use temporary job ID of 0 (relay doesn't use job_executions table)
		tempJobID := int64(0)
		var err error
		paymentID, err = rp.escrowManager.CreateEscrow(tempJobID, paymentSig, requiredAmount)
		if err != nil {
			rp.logger.Error(fmt.Sprintf("Relay payment verification failed for %s: %v", data.NodeID, err), "relay")
			return CreateRelayReject("", data.NodeID, fmt.Sprintf("Payment verification failed: %v", err))
		}

		rp.logger.Info(fmt.Sprintf("Relay payment verified (ID: %d) for client %s (amount: %.6f %s, estimated: %d bytes)",
			paymentID, data.NodeID, paymentSig.Amount, paymentSig.Currency, data.EstimatedBytes), "relay")
	}

	// Create new session
	sessionID := uuid.New().String()
	keepaliveInterval := rp.config.GetConfigDuration("relay_connection_keepalive", 30*time.Second)

	session := &RelaySession{
		SessionID:         sessionID,
		ClientPeerID:      data.PeerID,  // Persistent Ed25519-based peer ID
		ClientNodeID:      data.NodeID,  // DHT node ID
		RelayNodeID:       "", // Will be set by caller
		SessionType:       "full_relay",
		Connection:        conn,
		StartTime:         time.Now(),
		LastKeepalive:     time.Now(),
		KeepaliveInterval: keepaliveInterval,
		IngressBytes:      0,
		EgressBytes:       0,
		PaymentID:         paymentID,         // Store payment ID for settlement/refund
		EstimatedBytes:    data.EstimatedBytes, // Store estimated bytes
	}

	// Store session
	rp.sessionsMutex.Lock()
	rp.sessions[sessionID] = session
	rp.clientSessions[data.PeerID] = append(rp.clientSessions[data.PeerID], session)
	rp.sessionsMutex.Unlock()

	rp.clientsMutex.Lock()
	rp.registeredClients[data.PeerID] = session  // Index by Peer ID (persistent identity)
	rp.clientsMutex.Unlock()

	// Note: Session is now stored in-memory only (no database record)

	rp.logger.Info(fmt.Sprintf("Relay session created: %s for client %s (type: %s)",
		sessionID, data.NodeID, session.SessionType), "relay")

	// Start keepalive monitor
	go rp.monitorSession(session)

	return CreateRelayAccept("", data.NodeID, sessionID, int(keepaliveInterval.Seconds()), rp.pricingPerGB)
}

// HandleRelayReceiveStreamReady processes receive stream ready notification from NAT peers
func (rp *RelayPeer) HandleRelayReceiveStreamReady(msg *QUICMessage, stream *quic.Stream) error {
	var data RelayReceiveStreamReadyData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay receive stream ready: %v", err)
	}

	rp.logger.Debug(fmt.Sprintf("Receive stream ready from peer %s (session: %s)",
		data.PeerID, data.SessionID), "relay")

	// Look up session by peer ID
	rp.clientsMutex.RLock()
	session, exists := rp.registeredClients[data.PeerID]
	rp.clientsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("no session found for peer %s", data.PeerID)
	}

	// Verify session ID matches
	if session.SessionID != data.SessionID {
		return fmt.Errorf("session ID mismatch: expected %s, got %s", session.SessionID, data.SessionID)
	}

	// Store the receive stream in the session
	session.mutex.Lock()
	session.ReceiveStream = stream
	session.mutex.Unlock()

	rp.logger.Info(fmt.Sprintf("✅ Receive stream registered for peer %s (session: %s)",
		data.PeerID, data.SessionID), "relay")

	return nil
}

// HandleRelayForward processes relay forwarding requests
func (rp *RelayPeer) HandleRelayForward(msg *QUICMessage, remoteAddr string, stream *quic.Stream) error {
	var data RelayForwardData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay forward: %v", err)
	}

	// Check if this is a response (coming back from target to source)
	if data.MessageType == "service_response" {
		return rp.handleRelayResponse(&data, stream)
	}

	// This is a request - handle forwarding and track for response
	return rp.handleRelayRequest(&data, stream, remoteAddr)
}

// handleRelayRequest processes a relay forward request from requester to target
func (rp *RelayPeer) handleRelayRequest(data *RelayForwardData, stream *quic.Stream, remoteAddr string) error {
	// Validate session
	rp.sessionsMutex.RLock()
	session, exists := rp.sessions[data.SessionID]
	rp.sessionsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("session not found: %s", data.SessionID)
	}

	// Store the stream for response routing (correlation ID: source->target)
	correlationID := fmt.Sprintf("%s:%s", data.SourcePeerID, data.TargetPeerID)
	rp.pendingMutex.Lock()
	rp.pendingRequests[correlationID] = &PendingRelayRequest{
		Stream:       stream,
		RequestTime:  time.Now(),
		SourcePeerID: data.SourcePeerID,
		TargetPeerID: data.TargetPeerID,
	}
	rp.pendingMutex.Unlock()

	rp.logger.Debug(fmt.Sprintf("Stored pending request %s for response routing", correlationID), "relay")

	// Determine if this is coordination (free) or relay (paid) traffic
	// Coordination includes: hole_punch, service_search, service_response
	isCoordination := data.MessageType == "hole_punch" ||
		data.MessageType == "service_search" ||
		data.MessageType == "service_response"

	if isCoordination && rp.freeCoordination {
		// Free coordination traffic (hole punching, service discovery)
		if data.PayloadSize > rp.maxCoordMsgSize {
			return fmt.Errorf("coordination message too large: %d bytes (max: %d)",
				data.PayloadSize, rp.maxCoordMsgSize)
		}

		rp.logger.Debug(fmt.Sprintf("Forwarding coordination message (%s): %s -> %s (%d bytes)",
			data.MessageType, data.SourcePeerID, data.TargetPeerID, data.PayloadSize), "relay")
	} else {
		// Paid relay traffic (bulk data transfer)
		rp.logger.Debug(fmt.Sprintf("Forwarding relay data: %s -> %s (%d bytes, billable)",
			data.SourcePeerID, data.TargetPeerID, data.PayloadSize), "relay")
	}

	// Update traffic counters in-memory
	session.mutex.Lock()
	session.IngressBytes += data.PayloadSize
	session.LastKeepalive = time.Now()
	session.mutex.Unlock()

	// Record traffic for billing (database logging only)
	if rp.dbManager != nil && rp.dbManager.Relay != nil {
		trafficType := "relay"
		if isCoordination {
			trafficType = "coordination"
		}

		rp.dbManager.Relay.RecordTraffic(
			data.SessionID,
			data.SourcePeerID,
			session.RelayNodeID,
			trafficType,
			data.PayloadSize,
			0,
			time.Now(),
			time.Time{},
		)
	}

	// For service search requests, inject the source peer ID into the payload
	// so the target peer knows who to respond to
	forwardPayload := data.Payload
	if data.MessageType == "service_search" {
		// Unmarshal the service search request
		msg, err := UnmarshalQUICMessage(data.Payload)
		if err == nil && msg.Type == MessageTypeServiceRequest {
			// Inject source peer ID into the request
			var request ServiceSearchRequest
			if err := msg.GetDataAs(&request); err == nil {
				request.SourcePeerID = data.SourcePeerID
				// Update the message data
				msg.Data = request
				// Re-marshal the message
				if modifiedPayload, err := msg.Marshal(); err == nil {
					forwardPayload = modifiedPayload
					rp.logger.Debug(fmt.Sprintf("Injected source peer ID %s into service search request", data.SourcePeerID[:8]), "relay")
				}
			}
		}
	}

	// For job status requests and updates, and invoice messages, inject source peer ID so target knows who to respond to
	// This is critical for relay forwarding where the connection context is lost
	if data.MessageType == "job_status_request" || data.MessageType == "job_request" ||
	   data.MessageType == "job_status_update" || data.MessageType == "job_data_transfer_request" ||
	   data.MessageType == "capabilities_request" || data.MessageType == "invoice_request" ||
	   data.MessageType == "invoice_response" || data.MessageType == "invoice_notify" {
		msg, err := UnmarshalQUICMessage(data.Payload)
		if err == nil {
			// Inject source peer ID into message envelope
			msg.SourcePeerID = data.SourcePeerID

			if modifiedPayload, err := msg.Marshal(); err == nil {
				forwardPayload = modifiedPayload
				rp.logger.Debug(fmt.Sprintf("Injected source peer ID %s into %s message",
					data.SourcePeerID[:8], data.MessageType), "relay")
			}
		}
	}

	// Forward the payload to the target client
	if err := rp.forwardDataToTarget(data.TargetPeerID, forwardPayload); err != nil {
		rp.logger.Error(fmt.Sprintf("Failed to forward to target %s: %v", data.TargetPeerID, err), "relay")

		// Send error response back to source through the stream
		errorMsg := fmt.Sprintf("error: failed to forward to target: %v", err)
		if stream != nil {
			(*stream).Write([]byte(errorMsg))
		}

		return fmt.Errorf("failed to forward to target: %v", err)
	}

	// Determine if this is a request-response pattern (stream must stay open for response)
	// or one-way message (can confirm with "success")
	isRequestResponse := data.MessageType == "service_search" ||
		data.MessageType == "capabilities_request" ||
		data.MessageType == "job_status_request" ||
		data.MessageType == "job_request" ||
		data.MessageType == "job_data_transfer_request" ||
		data.MessageType == "job_start"

	// For one-way messages (invoice notifications, job updates, etc.), send delivery confirmation
	// For request-response messages, keep stream open - response will be routed back later
	if !isRequestResponse && stream != nil {
		(*stream).Write([]byte("success"))
	}

	// Update egress counter
	session.mutex.Lock()
	session.EgressBytes += data.PayloadSize
	session.mutex.Unlock()

	return nil
}

// handleRelayResponse processes a relay forward response from target back to requester
func (rp *RelayPeer) handleRelayResponse(data *RelayForwardData, responseStream *quic.Stream) error {
	// Look up the pending request (correlation ID: target->source, reversed from request)
	correlationID := fmt.Sprintf("%s:%s", data.TargetPeerID, data.SourcePeerID)

	rp.pendingMutex.Lock()
	pending, exists := rp.pendingRequests[correlationID]
	if exists {
		delete(rp.pendingRequests, correlationID)
	}
	rp.pendingMutex.Unlock()

	if !exists {
		rp.logger.Warn(fmt.Sprintf("No pending request found for response %s (may have timed out)", correlationID), "relay")
		return fmt.Errorf("no pending request for correlation ID: %s", correlationID)
	}

	rp.logger.Debug(fmt.Sprintf("Found pending request %s, routing response back to requester", correlationID), "relay")

	// Send the raw payload (unwrapped) back to the requester
	// The requester expects the actual response message (e.g., capabilities_response),
	// not another relay_forward wrapper
	if _, err := (*pending.Stream).Write(data.Payload); err != nil {
		rp.logger.Error(fmt.Sprintf("Failed to write response to requester stream: %v", err), "relay")
		return fmt.Errorf("failed to write response to requester: %v", err)
	}

	rp.logger.Info(fmt.Sprintf("Successfully routed %s response from %s back to %s (%d bytes)",
		data.MessageType, data.SourcePeerID[:8], data.TargetPeerID[:8], data.PayloadSize), "relay")

	return nil
}

// HandleRelayData processes relay data messages
func (rp *RelayPeer) HandleRelayData(msg *QUICMessage, remoteAddr string) error {
	var data RelayDataData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay data: %v", err)
	}

	// Validate session
	rp.sessionsMutex.RLock()
	session, exists := rp.sessions[data.SessionID]
	rp.sessionsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("session not found: %s", data.SessionID)
	}

	rp.logger.Debug(fmt.Sprintf("Relay data: %s -> %s (%d bytes, seq: %d)",
		data.SourcePeerID, data.TargetPeerID, data.DataSize, data.SequenceNum), "relay")

	// Update traffic counters in-memory (this is paid traffic)
	session.mutex.Lock()
	session.IngressBytes += data.DataSize
	session.LastKeepalive = time.Now()
	session.mutex.Unlock()

	// Record as billable relay traffic (database logging only)
	if rp.dbManager != nil && rp.dbManager.Relay != nil {
		rp.dbManager.Relay.RecordTraffic(
			data.SessionID,
			data.SourcePeerID,
			session.RelayNodeID,
			"relay",
			data.DataSize,
			0,
			time.Now(),
			time.Time{},
		)
	}

	// Forward to target
	return rp.forwardDataToTarget(data.TargetPeerID, data.Data)
}

// HandleRelayHolePunch processes hole punching coordination
func (rp *RelayPeer) HandleRelayHolePunch(msg *QUICMessage, remoteAddr string) error {
	var data RelayHolePunchData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay hole punch: %v", err)
	}

	rp.logger.Info(fmt.Sprintf("Hole punch coordination: %s <-> %s (strategy: %s)",
		data.InitiatorPeerID, data.TargetPeerID, data.Strategy), "relay")

	// Validate session
	rp.sessionsMutex.RLock()
	session, exists := rp.sessions[data.SessionID]
	rp.sessionsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("session not found: %s", data.SessionID)
	}

	// This is free coordination traffic
	messageSize := int64(len(data.InitiatorEndpoint) + len(data.TargetEndpoint) + 100) // Approximate

	session.mutex.Lock()
	session.IngressBytes += messageSize
	session.LastKeepalive = time.Now()
	session.mutex.Unlock()

	// Record as free coordination traffic
	if rp.dbManager != nil && rp.dbManager.Relay != nil {
		rp.dbManager.Relay.RecordTraffic(
			data.SessionID,
			data.InitiatorPeerID,
			session.RelayNodeID,
			"coordination",
			messageSize,
			0,
			time.Now(),
			time.Time{},
		)
	}

	// Forward coordination to target
	return rp.forwardHolePunchToTarget(&data)
}

// HandleRelayDisconnect processes relay disconnection
func (rp *RelayPeer) HandleRelayDisconnect(msg *QUICMessage, remoteAddr string) error {
	var data RelayDisconnectData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay disconnect: %v", err)
	}

	rp.logger.Info(fmt.Sprintf("Relay disconnect: session=%s, node=%s, reason=%s, traffic=%.2fMB",
		data.SessionID, data.NodeID, data.Reason,
		float64(data.BytesIngress+data.BytesEgress)/(1024*1024)), "relay")

	// Remove session
	rp.sessionsMutex.Lock()
	session, exists := rp.sessions[data.SessionID]
	if exists {
		delete(rp.sessions, data.SessionID)

		// Remove from client sessions
		if sessions, ok := rp.clientSessions[data.NodeID]; ok {
			for i, s := range sessions {
				if s.SessionID == data.SessionID {
					rp.clientSessions[data.NodeID] = append(sessions[:i], sessions[i+1:]...)
					break
				}
			}
		}
	}
	rp.sessionsMutex.Unlock()

	rp.clientsMutex.Lock()
	delete(rp.registeredClients, data.NodeID)
	rp.clientsMutex.Unlock()

	// Record final traffic for billing (database logging only)
	if rp.dbManager != nil && rp.dbManager.Relay != nil && exists {
		rp.dbManager.Relay.RecordTraffic(
			data.SessionID,
			data.NodeID,
			session.RelayNodeID,
			"relay",
			data.BytesIngress,
			data.BytesEgress,
			session.StartTime,
			time.Now(),
		)
	}

	// SETTLE OR REFUND RELAY PAYMENT (x402 protocol)
	if exists && session.PaymentID > 0 {
		// Calculate actual data transferred
		actualBytes := data.BytesIngress + data.BytesEgress
		actualGB := float64(actualBytes) / (1024 * 1024 * 1024)
		actualAmount := actualGB * rp.pricingPerGB

		// Determine if transfer was successful or failed
		// For relay, we consider it successful if any data was transferred
		// or if disconnection was graceful (not due to error)
		transferSuccess := actualBytes > 0 || data.Reason == "client_disconnect" || data.Reason == "graceful"

		if transferSuccess {
			// Settle payment for actual usage
			if err := rp.escrowManager.SettleEscrow(session.PaymentID, actualAmount); err != nil {
				rp.logger.Error(fmt.Sprintf("Failed to settle relay payment %d: %v", session.PaymentID, err), "relay")
			} else {
				rp.logger.Info(fmt.Sprintf("Relay payment %d settled (actual: %.2f MB, amount: %.6f USDC)",
					session.PaymentID, float64(actualBytes)/(1024*1024), actualAmount), "relay")
			}
		} else {
			// Refund payment on failure
			reason := fmt.Sprintf("Relay transfer failed: %s", data.Reason)
			if err := rp.escrowManager.RefundEscrow(session.PaymentID, reason); err != nil {
				rp.logger.Error(fmt.Sprintf("Failed to refund relay payment %d: %v", session.PaymentID, err), "relay")
			} else {
				rp.logger.Info(fmt.Sprintf("Relay payment %d refunded (reason: %s)", session.PaymentID, reason), "relay")
			}
		}
	}

	return nil
}

// HandleRelaySessionQuery handles session status queries
func (rp *RelayPeer) HandleRelaySessionQuery(msg *QUICMessage, remoteAddr string) *QUICMessage {
	var data RelaySessionQueryData
	if err := msg.GetDataAs(&data); err != nil {
		rp.logger.Error(fmt.Sprintf("Failed to parse relay session query: %v", err), "relay")
		// Return negative response on parse error
		return CreateRelaySessionStatus(data.ClientNodeID, false, "", false, 0)
	}

	rp.logger.Debug(fmt.Sprintf("Session query for client %s from %s", data.ClientNodeID, data.QueryNodeID), "relay")

	// Check if client has an active session (in-memory only)
	rp.sessionsMutex.RLock()
	sessions := rp.clientSessions[data.ClientNodeID]

	if len(sessions) == 0 {
		rp.sessionsMutex.RUnlock()
		rp.logger.Debug(fmt.Sprintf("Client %s has no active session", data.ClientNodeID), "relay")
		return CreateRelaySessionStatus(data.ClientNodeID, false, "", false, 0)
	}

	// Get the first active session (while still holding lock)
	session := sessions[0]
	sessionID := session.SessionID

	// Read session fields while holding lock
	session.mutex.RLock()
	lastKeepalive := session.LastKeepalive.Unix()
	session.mutex.RUnlock()

	rp.sessionsMutex.RUnlock()

	rp.logger.Debug(fmt.Sprintf("Client %s has active session %s", data.ClientNodeID, sessionID), "relay")
	return CreateRelaySessionStatus(data.ClientNodeID, true, sessionID, true, lastKeepalive)
}

// monitorSession monitors a relay session for keepalive
func (rp *RelayPeer) monitorSession(session *RelaySession) {
	ticker := time.NewTicker(session.KeepaliveInterval)
	defer ticker.Stop()

	// More lenient timeout: 4x keepalive interval instead of 2x
	// This accounts for network hiccups, QUIC stream delays, and missed keepalives
	// With default 30s interval: timeout after 120s (2 minutes) instead of 60s
	const timeoutMultiplier = 4

	for {
		select {
		case <-rp.ctx.Done():
			return
		case <-ticker.C:
			session.mutex.RLock()
			lastKeepalive := session.LastKeepalive
			timeSinceLastKeepalive := time.Since(lastKeepalive)
			session.mutex.RUnlock()

			timeoutThreshold := session.KeepaliveInterval * timeoutMultiplier

			// Check if session is still alive
			if timeSinceLastKeepalive > timeoutThreshold {
				rp.logger.Warn(fmt.Sprintf("Session %s timed out after %v without keepalive (client: %s, threshold: %v)",
					session.SessionID, timeSinceLastKeepalive, session.ClientNodeID, timeoutThreshold), "relay")

				// Terminate session
				rp.terminateSession(session.SessionID, "keepalive timeout")
				return
			}

			// Log warning if approaching timeout (at 3x interval)
			if timeSinceLastKeepalive > session.KeepaliveInterval*3 {
				rp.logger.Debug(fmt.Sprintf("Session %s approaching timeout: %v since last keepalive (client: %s, will timeout at %v)",
					session.SessionID, timeSinceLastKeepalive, session.ClientNodeID, timeoutThreshold), "relay")
			}
		}
	}
}

// terminateSession terminates a relay session
func (rp *RelayPeer) terminateSession(sessionID, reason string) {
	rp.sessionsMutex.Lock()
	session, exists := rp.sessions[sessionID]
	if !exists {
		rp.sessionsMutex.Unlock()
		return
	}

	delete(rp.sessions, sessionID)

	// Remove from client sessions
	if sessions, ok := rp.clientSessions[session.ClientNodeID]; ok {
		for i, s := range sessions {
			if s.SessionID == sessionID {
				rp.clientSessions[session.ClientNodeID] = append(sessions[:i], sessions[i+1:]...)
				break
			}
		}
	}
	rp.sessionsMutex.Unlock()

	rp.clientsMutex.Lock()
	delete(rp.registeredClients, session.ClientNodeID)
	rp.clientsMutex.Unlock()

	// Note: Session closed in-memory only (no database record to close)

	rp.logger.Info(fmt.Sprintf("Terminated session %s: %s", sessionID, reason), "relay")
}

// forwardDataToTarget forwards data to target client
// targetPeerID is the persistent Ed25519-based peer ID (not the DHT node ID)
func (rp *RelayPeer) forwardDataToTarget(targetPeerID string, data []byte) error {
	// Look up the target client session by Peer ID
	rp.clientsMutex.RLock()
	session, exists := rp.registeredClients[targetPeerID]
	rp.clientsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("target client not registered: %s", targetPeerID)
	}

	// Get the pre-opened receive stream
	session.mutex.RLock()
	stream := session.ReceiveStream
	session.mutex.RUnlock()

	if stream == nil {
		return fmt.Errorf("no receive stream available for target %s (NAT peer must open receive stream after registration)", targetPeerID)
	}

	// Write length prefix (4 bytes, big-endian) so receiver knows message boundaries
	lengthPrefix := make([]byte, 4)
	lengthPrefix[0] = byte(len(data) >> 24)
	lengthPrefix[1] = byte(len(data) >> 16)
	lengthPrefix[2] = byte(len(data) >> 8)
	lengthPrefix[3] = byte(len(data))

	session.mutex.Lock()
	defer session.mutex.Unlock()

	// Write length prefix
	if _, err := (*stream).Write(lengthPrefix); err != nil {
		// Stream may be closed, clear it from session
		session.ReceiveStream = nil
		return fmt.Errorf("failed to write length prefix to target %s: %v", targetPeerID, err)
	}

	// Write the data to the stream
	if _, err := (*stream).Write(data); err != nil {
		// Stream may be closed, clear it from session
		session.ReceiveStream = nil
		return fmt.Errorf("failed to write data to target %s: %v", targetPeerID, err)
	}

	// Update egress bytes for billing
	session.EgressBytes += int64(len(data))

	rp.logger.Debug(fmt.Sprintf("Forwarded %d bytes to %s via receive stream", len(data), targetPeerID), "relay")
	return nil
}

// forwardHolePunchToTarget forwards hole punch coordination to target
func (rp *RelayPeer) forwardHolePunchToTarget(data *RelayHolePunchData) error {
	// Create a QUIC message containing the hole punch data
	msg := NewQUICMessage(MessageTypeRelayHolePunch, data)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal hole punch message: %v", err)
	}

	// Forward the marshaled message to the target
	if err := rp.forwardDataToTarget(data.TargetPeerID, msgBytes); err != nil {
		return fmt.Errorf("failed to forward hole punch to target: %v", err)
	}

	rp.logger.Debug(fmt.Sprintf("Forwarded hole punch coordination to %s", data.TargetPeerID), "relay")
	return nil
}

// GetStats returns relay statistics
func (rp *RelayPeer) GetStats() map[string]interface{} {
	rp.sessionsMutex.RLock()
	defer rp.sessionsMutex.RUnlock()

	stats := map[string]interface{}{
		"is_relay_mode":      rp.isRelayMode,
		"max_connections":    rp.maxConnections,
		"active_sessions":    len(rp.sessions),
		"registered_clients": len(rp.registeredClients),
		"pricing_per_gb":     rp.pricingPerGB,
		"free_coordination":  rp.freeCoordination,
	}

	// Calculate total traffic
	var totalIngress, totalEgress int64
	for _, session := range rp.sessions {
		session.mutex.RLock()
		totalIngress += session.IngressBytes
		totalEgress += session.EgressBytes
		session.mutex.RUnlock()
	}

	stats["total_ingress_bytes"] = totalIngress
	stats["total_egress_bytes"] = totalEgress
	stats["total_bytes"] = totalIngress + totalEgress

	return stats
}

// Stop stops the relay peer
func (rp *RelayPeer) Stop() error {
	rp.logger.Info("Stopping relay peer...", "relay")
	rp.cancel()

	// Terminate all sessions
	rp.sessionsMutex.Lock()
	for sessionID := range rp.sessions {
		rp.terminateSession(sessionID, "relay shutdown")
	}
	rp.sessionsMutex.Unlock()

	return nil
}

// GetClientAddress returns the QUIC connection address for a connected client peer
// Returns empty string if the client is not connected
func (rp *RelayPeer) GetClientAddress(clientPeerID string) string {
	rp.clientsMutex.RLock()
	session, exists := rp.registeredClients[clientPeerID]
	rp.clientsMutex.RUnlock()

	if !exists || session == nil {
		return ""
	}

	session.mutex.RLock()
	conn := session.Connection
	session.mutex.RUnlock()

	if conn == nil {
		return ""
	}

	// Get the remote address from the connection
	remoteAddr := (*conn).RemoteAddr()
	if remoteAddr == nil {
		return ""
	}

	return remoteAddr.String()
}

// GetClientConnection returns the QUIC connection for a registered relay client
// This allows the relay to open new streams on the existing inbound connection
func (rp *RelayPeer) GetClientConnection(clientPeerID string) (*quic.Conn, error) {
	rp.clientsMutex.RLock()
	session, exists := rp.registeredClients[clientPeerID]
	rp.clientsMutex.RUnlock()

	if !exists || session == nil {
		return nil, fmt.Errorf("client not registered")
	}

	session.mutex.RLock()
	conn := session.Connection
	session.mutex.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("no active connection")
	}

	return conn, nil
}

// GetSessionDetails returns detailed information about all active sessions
func (rp *RelayPeer) GetSessionDetails() []map[string]interface{} {
	rp.sessionsMutex.RLock()
	defer rp.sessionsMutex.RUnlock()

	sessions := make([]map[string]interface{}, 0, len(rp.sessions))

	for _, session := range rp.sessions {
		session.mutex.RLock()

		// Calculate session duration
		duration := time.Since(session.StartTime)

		// Calculate total bytes
		totalBytes := session.IngressBytes + session.EgressBytes

		// Calculate earnings (only for relay traffic, coordination is free)
		var earnings float64
		if session.SessionType == "full_relay" {
			// Convert bytes to GB and multiply by pricing
			billableGB := float64(totalBytes) / (1024 * 1024 * 1024)
			earnings = billableGB * rp.pricingPerGB
		}

		sessionDetail := map[string]interface{}{
			"session_id":       session.SessionID,
			"client_peer_id":   session.ClientPeerID, // Persistent Ed25519-based peer ID
			"client_node_id":   session.ClientNodeID, // DHT node ID
			"session_type":     session.SessionType,
			"start_time":       session.StartTime.Unix(),
			"duration_seconds": int64(duration.Seconds()),
			"ingress_bytes":    session.IngressBytes,
			"egress_bytes":     session.EgressBytes,
			"total_bytes":      totalBytes,
			"earnings":         earnings,
			"last_keepalive":   session.LastKeepalive.Unix(),
		}

		session.mutex.RUnlock()
		sessions = append(sessions, sessionDetail)
	}

	return sessions
}

// DisconnectSession terminates a specific relay session
func (rp *RelayPeer) DisconnectSession(sessionID string) error {
	rp.sessionsMutex.Lock()
	defer rp.sessionsMutex.Unlock()

	session, exists := rp.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	rp.logger.Info(fmt.Sprintf("Disconnecting session %s for client %s", sessionID, session.ClientNodeID), "relay")

	// Terminate the session
	rp.terminateSession(sessionID, "manual disconnect")

	return nil
}

// UpdateSessionKeepalive updates the last keepalive timestamp for a session
func (rp *RelayPeer) UpdateSessionKeepalive(sessionID string) {
	rp.sessionsMutex.RLock()
	session, exists := rp.sessions[sessionID]
	rp.sessionsMutex.RUnlock()

	if !exists {
		return
	}

	session.mutex.Lock()
	session.LastKeepalive = time.Now()
	session.mutex.Unlock()

	rp.logger.Debug(fmt.Sprintf("Updated keepalive for session %s (client: %s)", sessionID, session.ClientNodeID), "relay")

	// Note: Keepalive updated in-memory only (no database record to update)
}
