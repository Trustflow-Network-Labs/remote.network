package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
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
	StartTime         time.Time
	LastKeepalive     time.Time
	KeepaliveInterval time.Duration
	IngressBytes      int64
	EgressBytes       int64
	mutex             sync.RWMutex
}

// RelayPeer manages relay functionality
type RelayPeer struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	dbManager       *database.SQLiteManager
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

	rp := &RelayPeer{
		config:            config,
		logger:            logger,
		dbManager:         dbManager,
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

	// Create new session
	sessionID := uuid.New().String()
	keepaliveInterval := rp.config.GetConfigInt("relay_connection_keepalive", 30, 10, 300)

	session := &RelaySession{
		SessionID:         sessionID,
		ClientPeerID:      data.PeerID,  // Persistent Ed25519-based peer ID
		ClientNodeID:      data.NodeID,  // DHT node ID
		RelayNodeID:       "", // Will be set by caller
		SessionType:       "full_relay",
		Connection:        conn,
		StartTime:         time.Now(),
		LastKeepalive:     time.Now(),
		KeepaliveInterval: time.Duration(keepaliveInterval) * time.Second,
		IngressBytes:      0,
		EgressBytes:       0,
	}

	// Store session
	rp.sessionsMutex.Lock()
	rp.sessions[sessionID] = session
	rp.clientSessions[data.NodeID] = append(rp.clientSessions[data.NodeID], session)
	rp.sessionsMutex.Unlock()

	rp.clientsMutex.Lock()
	rp.registeredClients[data.NodeID] = session
	rp.clientsMutex.Unlock()

	// Note: Session is now stored in-memory only (no database record)

	rp.logger.Info(fmt.Sprintf("Relay session created: %s for client %s (type: %s)",
		sessionID, data.NodeID, session.SessionType), "relay")

	// Start keepalive monitor
	go rp.monitorSession(session)

	return CreateRelayAccept("", data.NodeID, sessionID, keepaliveInterval, rp.pricingPerGB)
}

// HandleRelayForward processes relay forwarding requests
func (rp *RelayPeer) HandleRelayForward(msg *QUICMessage, remoteAddr string) error {
	var data RelayForwardData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay forward: %v", err)
	}

	// Validate session
	rp.sessionsMutex.RLock()
	session, exists := rp.sessions[data.SessionID]
	rp.sessionsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("session not found: %s", data.SessionID)
	}

	// Determine if this is coordination (free) or relay (paid) traffic
	isCoordination := data.MessageType == "hole_punch"
	if isCoordination && rp.freeCoordination {
		// Free hole punching coordination
		if data.PayloadSize > rp.maxCoordMsgSize {
			return fmt.Errorf("coordination message too large: %d bytes (max: %d)",
				data.PayloadSize, rp.maxCoordMsgSize)
		}

		rp.logger.Debug(fmt.Sprintf("Forwarding coordination message: %s -> %s (%d bytes)",
			data.SourceNodeID, data.TargetNodeID, data.PayloadSize), "relay")
	} else {
		// Paid relay traffic
		rp.logger.Debug(fmt.Sprintf("Forwarding relay data: %s -> %s (%d bytes, billable)",
			data.SourceNodeID, data.TargetNodeID, data.PayloadSize), "relay")
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
			data.SourceNodeID,
			session.RelayNodeID,
			trafficType,
			data.PayloadSize,
			0,
			time.Now(),
			time.Time{},
		)
	}

	// Forward to target (implementation depends on routing logic)
	// This will be implemented in the message forwarding section

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
		data.SourceNodeID, data.TargetNodeID, data.DataSize, data.SequenceNum), "relay")

	// Update traffic counters in-memory (this is paid traffic)
	session.mutex.Lock()
	session.IngressBytes += data.DataSize
	session.LastKeepalive = time.Now()
	session.mutex.Unlock()

	// Record as billable relay traffic (database logging only)
	if rp.dbManager != nil && rp.dbManager.Relay != nil {
		rp.dbManager.Relay.RecordTraffic(
			data.SessionID,
			data.SourceNodeID,
			session.RelayNodeID,
			"relay",
			data.DataSize,
			0,
			time.Now(),
			time.Time{},
		)
	}

	// Forward to target
	return rp.forwardDataToTarget(data.TargetNodeID, data.Data)
}

// HandleRelayHolePunch processes hole punching coordination
func (rp *RelayPeer) HandleRelayHolePunch(msg *QUICMessage, remoteAddr string) error {
	var data RelayHolePunchData
	if err := msg.GetDataAs(&data); err != nil {
		return fmt.Errorf("failed to parse relay hole punch: %v", err)
	}

	rp.logger.Info(fmt.Sprintf("Hole punch coordination: %s <-> %s (strategy: %s)",
		data.InitiatorNodeID, data.TargetNodeID, data.Strategy), "relay")

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
			data.InitiatorNodeID,
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

	for {
		select {
		case <-rp.ctx.Done():
			return
		case <-ticker.C:
			session.mutex.RLock()
			lastKeepalive := session.LastKeepalive
			session.mutex.RUnlock()

			// Check if session is still alive
			if time.Since(lastKeepalive) > session.KeepaliveInterval*2 {
				rp.logger.Warn(fmt.Sprintf("Session %s timed out (client: %s)",
					session.SessionID, session.ClientNodeID), "relay")

				// Terminate session
				rp.terminateSession(session.SessionID, "keepalive timeout")
				return
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

// forwardDataToTarget forwards data to target client (stub for now)
func (rp *RelayPeer) forwardDataToTarget(targetNodeID string, data []byte) error {
	// This will be implemented in the message forwarding section
	// For now, just log
	rp.logger.Debug(fmt.Sprintf("Forward data to %s (%d bytes)", targetNodeID, len(data)), "relay")
	return nil
}

// forwardHolePunchToTarget forwards hole punch coordination to target (stub for now)
func (rp *RelayPeer) forwardHolePunchToTarget(data *RelayHolePunchData) error {
	// This will be implemented in the message forwarding section
	rp.logger.Debug(fmt.Sprintf("Forward hole punch coordination to %s", data.TargetNodeID), "relay")
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
