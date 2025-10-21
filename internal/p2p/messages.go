package p2p

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// MessageType defines the type of QUIC message
type MessageType string

const (
	// Peer discovery and metadata
	MessageTypeMetadataRequest  MessageType = "metadata_request"
	MessageTypeMetadataResponse MessageType = "metadata_response"
	MessageTypePeerAnnounce     MessageType = "peer_announce"

	// Connection management
	MessageTypePing             MessageType = "ping"
	MessageTypePong             MessageType = "pong"
	MessageTypeDisconnect       MessageType = "disconnect"

	// Service discovery (future)
	MessageTypeServiceRequest   MessageType = "service_request"
	MessageTypeServiceResponse  MessageType = "service_response"
	MessageTypeServiceCatalogue MessageType = "service_catalogue"

	// Relay system
	MessageTypeRelayRegister      MessageType = "relay_register"
	MessageTypeRelayAccept        MessageType = "relay_accept"
	MessageTypeRelayReject        MessageType = "relay_reject"
	MessageTypeRelayForward       MessageType = "relay_forward"
	MessageTypeRelayData          MessageType = "relay_data"
	MessageTypeRelayHolePunch     MessageType = "relay_hole_punch"
	MessageTypeRelayDisconnect    MessageType = "relay_disconnect"
	MessageTypeRelaySessionQuery  MessageType = "relay_session_query"
	MessageTypeRelaySessionStatus MessageType = "relay_session_status"

	// Hole Punching (DCUtR-style protocol)
	MessageTypeHolePunchConnect MessageType = "hole_punch_connect"
	MessageTypeHolePunchSync    MessageType = "hole_punch_sync"

	// Metadata Broadcast
	MessageTypePeerMetadataUpdate    MessageType = "peer_metadata_update"
	MessageTypePeerMetadataUpdateAck MessageType = "peer_metadata_update_ack"

	// Phase 3: DHT-based peer exchange
	MessageTypeIdentityExchange  MessageType = "identity_exchange"
	MessageTypeKnownPeersRequest MessageType = "known_peers_request"
	MessageTypeKnownPeersResponse MessageType = "known_peers_response"

	// Legacy support
	MessageTypeEcho MessageType = "echo"
)

// QUICMessage represents a structured message sent over QUIC
type QUICMessage struct {
	Type      MessageType `json:"type"`
	Version   int         `json:"version"`
	Timestamp time.Time   `json:"timestamp"`
	RequestID string      `json:"request_id,omitempty"` // For request/response correlation
	Data      interface{} `json:"data,omitempty"`
}

// MetadataRequestData contains metadata request parameters
type MetadataRequestData struct {
	Topic    string                   `json:"topic"`
	NodeID   string                   `json:"node_id"`
	Includes []string                 `json:"includes,omitempty"` // What metadata to include: "network", "services", "capabilities"
	// Bidirectional exchange: requester includes their own metadata
	MyMetadata *database.PeerMetadata `json:"my_metadata,omitempty"` // Requester's metadata for bidirectional exchange
	// Note: Peer exchange removed in Phase 5 - now handled by identity_exchange.go (Phase 3)
}

// MetadataResponseData contains peer metadata in response
type MetadataResponseData struct {
	Topic      string                 `json:"topic"`
	NodeID     string                 `json:"node_id"`
	Metadata   *database.PeerMetadata `json:"metadata,omitempty"`
	Error      string                 `json:"error,omitempty"`
	// Note: Peer exchange removed in Phase 5 - now handled by identity_exchange.go (Phase 3)
}

// PeerAnnounceData contains peer announcement information
type PeerAnnounceData struct {
	Topic      string                 `json:"topic"`
	NodeID     string                 `json:"node_id"`
	Metadata   *database.PeerMetadata `json:"metadata"`
	Action     string                 `json:"action"` // "join", "leave", "update"
}

// PingData contains ping message data
type PingData struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message,omitempty"`
}

// PongData contains pong response data
type PongData struct {
	PingID    string    `json:"ping_id"`
	Timestamp time.Time `json:"timestamp"`
	RTT       int64     `json:"rtt_ms,omitempty"` // Round trip time in milliseconds
}

// ServiceRequestData contains service request information (future)
type ServiceRequestData struct {
	ServiceType string                 `json:"service_type"`
	Parameters  map[string]interface{} `json:"parameters"`
	Timeout     int                    `json:"timeout_seconds,omitempty"`
}

// ServiceResponseData contains service response information (future)
type ServiceResponseData struct {
	RequestID string                 `json:"request_id"`
	Status    string                 `json:"status"` // "success", "error", "timeout"
	Result    map[string]interface{} `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// EchoData contains echo message data (legacy)
type EchoData struct {
	Message string `json:"message"`
}

// NewQUICMessage creates a new QUIC message with standard fields
func NewQUICMessage(msgType MessageType, data interface{}) *QUICMessage {
	return &QUICMessage{
		Type:      msgType,
		Version:   1,
		Timestamp: time.Now(),
		Data:      data,
	}
}

// NewRequestMessage creates a new request message with request ID
func NewRequestMessage(msgType MessageType, requestID string, data interface{}) *QUICMessage {
	return &QUICMessage{
		Type:      msgType,
		Version:   1,
		Timestamp: time.Now(),
		RequestID: requestID,
		Data:      data,
	}
}

// Marshal serializes the message to JSON bytes
func (msg *QUICMessage) Marshal() ([]byte, error) {
	return json.Marshal(msg)
}

// Unmarshal deserializes JSON bytes to a QUIC message
func UnmarshalQUICMessage(data []byte) (*QUICMessage, error) {
	var msg QUICMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal QUIC message: %v", err)
	}
	return &msg, nil
}

// GetDataAs unmarshals the Data field into the specified type
func (msg *QUICMessage) GetDataAs(target interface{}) error {
	if msg.Data == nil {
		return fmt.Errorf("message data is nil")
	}

	// Re-marshal and unmarshal to convert interface{} to specific type
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal message data: %v", err)
	}

	if err := json.Unmarshal(dataBytes, target); err != nil {
		return fmt.Errorf("failed to unmarshal message data to target type: %v", err)
	}

	return nil
}

// IsRequest returns true if this message expects a response
func (msg *QUICMessage) IsRequest() bool {
	switch msg.Type {
	case MessageTypeMetadataRequest, MessageTypeServiceRequest, MessageTypePing:
		return true
	default:
		return false
	}
}

// IsResponse returns true if this message is a response to a request
func (msg *QUICMessage) IsResponse() bool {
	switch msg.Type {
	case MessageTypeMetadataResponse, MessageTypeServiceResponse, MessageTypePong:
		return true
	default:
		return false
	}
}

// CreateMetadataRequest creates a metadata request message
func CreateMetadataRequest(nodeID, topic string, includes []string) *QUICMessage {
	return NewQUICMessage(MessageTypeMetadataRequest, &MetadataRequestData{
		Topic:    topic,
		NodeID:   nodeID,
		Includes: includes,
	})
}

// CreateBidirectionalMetadataRequest creates a metadata request with requester's metadata for bidirectional exchange
func CreateBidirectionalMetadataRequest(nodeID, topic string, includes []string, myMetadata *database.PeerMetadata) *QUICMessage {
	return NewQUICMessage(MessageTypeMetadataRequest, &MetadataRequestData{
		Topic:      topic,
		NodeID:     nodeID,
		Includes:   includes,
		MyMetadata: myMetadata,
	})
}

// CreateMetadataResponse creates a metadata response message
func CreateMetadataResponse(requestID string, metadata *database.PeerMetadata, err error) *QUICMessage {
	data := &MetadataResponseData{
		Metadata: metadata,
	}

	if metadata != nil {
		data.Topic = metadata.Topic
		data.NodeID = metadata.NodeID
	}

	if err != nil {
		data.Error = err.Error()
	}

	msg := NewQUICMessage(MessageTypeMetadataResponse, data)
	msg.RequestID = requestID
	return msg
}

// CreatePeerAnnounce creates a peer announcement message
func CreatePeerAnnounce(action string, metadata *database.PeerMetadata) *QUICMessage {
	return NewQUICMessage(MessageTypePeerAnnounce, &PeerAnnounceData{
		Topic:    metadata.Topic,
		NodeID:   metadata.NodeID,
		Metadata: metadata,
		Action:   action,
	})
}

// CreatePing creates a ping message
func CreatePing(id, message string) *QUICMessage {
	return NewQUICMessage(MessageTypePing, &PingData{
		ID:        id,
		Timestamp: time.Now(),
		Message:   message,
	})
}

// CreatePong creates a pong response message
func CreatePong(pingID string, rtt int64) *QUICMessage {
	return NewQUICMessage(MessageTypePong, &PongData{
		PingID:    pingID,
		Timestamp: time.Now(),
		RTT:       rtt,
	})
}

// CreateEcho creates an echo message (legacy support)
func CreateEcho(message string) *QUICMessage {
	return NewQUICMessage(MessageTypeEcho, &EchoData{
		Message: message,
	})
}

// Validate performs basic validation on the message
func (msg *QUICMessage) Validate() error {
	if msg.Type == "" {
		return fmt.Errorf("message type is required")
	}

	if msg.Version == 0 {
		return fmt.Errorf("message version is required")
	}

	if msg.Timestamp.IsZero() {
		return fmt.Errorf("message timestamp is required")
	}

	// Type-specific validation
	switch msg.Type {
	case MessageTypeMetadataRequest:
		var data MetadataRequestData
		if err := msg.GetDataAs(&data); err != nil {
			return fmt.Errorf("invalid metadata request data: %v", err)
		}
		if data.Topic == "" || data.NodeID == "" {
			return fmt.Errorf("metadata request requires topic and node_id")
		}

	case MessageTypeMetadataResponse:
		if msg.RequestID == "" {
			return fmt.Errorf("metadata response requires request_id")
		}

	case MessageTypePeerAnnounce:
		var data PeerAnnounceData
		if err := msg.GetDataAs(&data); err != nil {
			return fmt.Errorf("invalid peer announce data: %v", err)
		}
		if data.Topic == "" || data.NodeID == "" || data.Action == "" {
			return fmt.Errorf("peer announce requires topic, node_id, and action")
		}

	case MessageTypePing:
		var data PingData
		if err := msg.GetDataAs(&data); err != nil {
			return fmt.Errorf("invalid ping data: %v", err)
		}
		if data.ID == "" {
			return fmt.Errorf("ping requires ID")
		}

	case MessageTypePong:
		var data PongData
		if err := msg.GetDataAs(&data); err != nil {
			return fmt.Errorf("invalid pong data: %v", err)
		}
		if data.PingID == "" {
			return fmt.Errorf("pong requires ping_id")
		}
	}

	return nil
}

// Relay message data structures

// RelayRegisterData contains relay registration request
type RelayRegisterData struct {
	PeerID          string  `json:"peer_id"`           // Persistent Ed25519-based peer ID
	NodeID          string  `json:"node_id"`           // DHT node ID (may change on restart)
	Topic           string  `json:"topic"`
	NATType         string  `json:"nat_type"`
	PublicEndpoint  string  `json:"public_endpoint,omitempty"`
	PrivateEndpoint string  `json:"private_endpoint,omitempty"`
	RequiresRelay   bool    `json:"requires_relay"`
}

// RelayAcceptData contains relay registration acceptance
type RelayAcceptData struct {
	RelayNodeID     string  `json:"relay_node_id"`
	ClientNodeID    string  `json:"client_node_id"`
	SessionID       string  `json:"session_id"`
	KeepAliveInterval int   `json:"keepalive_interval_seconds"`
	PricingPerGB    float64 `json:"pricing_per_gb,omitempty"`
}

// RelayRejectData contains relay registration rejection
type RelayRejectData struct {
	RelayNodeID  string `json:"relay_node_id"`
	ClientNodeID string `json:"client_node_id"`
	Reason       string `json:"reason"`
}

// RelayForwardData contains relay message forwarding request
type RelayForwardData struct {
	SessionID      string `json:"session_id"`
	SourceNodeID   string `json:"source_node_id"`
	TargetNodeID   string `json:"target_node_id"`
	MessageType    string `json:"message_type"` // "hole_punch", "data"
	Payload        []byte `json:"payload"`
	PayloadSize    int64  `json:"payload_size"`
}

// RelayDataData contains actual data being relayed
type RelayDataData struct {
	SessionID    string `json:"session_id"`
	SourceNodeID string `json:"source_node_id"`
	TargetNodeID string `json:"target_node_id"`
	Data         []byte `json:"data"`
	DataSize     int64  `json:"data_size"`
	SequenceNum  int64  `json:"sequence_num,omitempty"`
}

// RelayHolePunchData contains hole punching coordination data
type RelayHolePunchData struct {
	SessionID       string `json:"session_id"`
	InitiatorNodeID string `json:"initiator_node_id"`
	TargetNodeID    string `json:"target_node_id"`
	InitiatorEndpoint string `json:"initiator_endpoint"`
	TargetEndpoint    string `json:"target_endpoint"`
	CoordinationTime  time.Time `json:"coordination_time"`
	Strategy        string `json:"strategy"` // "simultaneous", "sequential"
}

// RelayDisconnectData contains relay disconnection notification
type RelayDisconnectData struct {
	SessionID    string `json:"session_id"`
	NodeID       string `json:"node_id"`
	Reason       string `json:"reason"`
	BytesIngress int64  `json:"bytes_ingress"`
	BytesEgress  int64  `json:"bytes_egress"`
	Duration     int64  `json:"duration_seconds"`
}

// RelaySessionQueryData contains relay session query request
type RelaySessionQueryData struct {
	ClientNodeID string `json:"client_node_id"` // NAT peer we're asking about
	QueryNodeID  string `json:"query_node_id"`  // Who is asking (for logging)
}

// RelaySessionStatusData contains relay session status response
type RelaySessionStatusData struct {
	ClientNodeID  string `json:"client_node_id"`
	HasSession    bool   `json:"has_session"`
	SessionID     string `json:"session_id,omitempty"`
	SessionActive bool   `json:"session_active"`
	LastKeepalive int64  `json:"last_keepalive,omitempty"` // Unix timestamp
}

// HolePunchConnectData contains hole punch CONNECT message data
// Sent by both initiator and receiver to exchange observed addresses and measure RTT
type HolePunchConnectData struct {
	NodeID string `json:"node_id"` // Sender's node ID

	// Observed addresses (public addresses where this peer can be reached)
	// Format: "IP:Port" (e.g., "203.0.113.1:30906")
	ObservedAddrs []string `json:"observed_addrs"`

	// Private addresses for LAN detection
	PrivateAddrs []string `json:"private_addrs,omitempty"`

	// Network information for connection strategy
	PublicIP  string `json:"public_ip"`           // Public IP address
	PrivateIP string `json:"private_ip"`          // Private IP address
	NATType   string `json:"nat_type,omitempty"`  // NAT type classification
	IsRelay   bool   `json:"is_relay,omitempty"`  // Is this peer a relay?

	// Timing information
	SendTime int64 `json:"send_time"` // Unix timestamp in milliseconds when message sent
}

// HolePunchSyncData contains hole punch SYNC message data
// Sent by initiator to signal that hole punching should begin
type HolePunchSyncData struct {
	RTT int64 `json:"rtt"` // Measured RTT in milliseconds
}

// CreateRelayRegister creates a relay registration request
func CreateRelayRegister(peerID, nodeID, topic, natType, publicEndpoint, privateEndpoint string, requiresRelay bool) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayRegister, &RelayRegisterData{
		PeerID:          peerID,
		NodeID:          nodeID,
		Topic:           topic,
		NATType:         natType,
		PublicEndpoint:  publicEndpoint,
		PrivateEndpoint: privateEndpoint,
		RequiresRelay:   requiresRelay,
	})
}

// CreateRelayAccept creates a relay acceptance response
func CreateRelayAccept(relayNodeID, clientNodeID, sessionID string, keepAliveInterval int, pricingPerGB float64) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayAccept, &RelayAcceptData{
		RelayNodeID:       relayNodeID,
		ClientNodeID:      clientNodeID,
		SessionID:         sessionID,
		KeepAliveInterval: keepAliveInterval,
		PricingPerGB:      pricingPerGB,
	})
}

// CreateRelayReject creates a relay rejection response
func CreateRelayReject(relayNodeID, clientNodeID, reason string) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayReject, &RelayRejectData{
		RelayNodeID:  relayNodeID,
		ClientNodeID: clientNodeID,
		Reason:       reason,
	})
}

// CreateRelayForward creates a relay forward request
func CreateRelayForward(sessionID, sourceNodeID, targetNodeID, messageType string, payload []byte) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    sessionID,
		SourceNodeID: sourceNodeID,
		TargetNodeID: targetNodeID,
		MessageType:  messageType,
		Payload:      payload,
		PayloadSize:  int64(len(payload)),
	})
}

// CreateRelayData creates a relay data message
func CreateRelayData(sessionID, sourceNodeID, targetNodeID string, data []byte, sequenceNum int64) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayData, &RelayDataData{
		SessionID:    sessionID,
		SourceNodeID: sourceNodeID,
		TargetNodeID: targetNodeID,
		Data:         data,
		DataSize:     int64(len(data)),
		SequenceNum:  sequenceNum,
	})
}

// CreateRelayHolePunch creates a hole punching coordination message
func CreateRelayHolePunch(sessionID, initiatorNodeID, targetNodeID, initiatorEndpoint, targetEndpoint, strategy string) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayHolePunch, &RelayHolePunchData{
		SessionID:         sessionID,
		InitiatorNodeID:   initiatorNodeID,
		TargetNodeID:      targetNodeID,
		InitiatorEndpoint: initiatorEndpoint,
		TargetEndpoint:    targetEndpoint,
		CoordinationTime:  time.Now(),
		Strategy:          strategy,
	})
}

// CreateRelayDisconnect creates a relay disconnection message
func CreateRelayDisconnect(sessionID, nodeID, reason string, bytesIngress, bytesEgress, duration int64) *QUICMessage {
	return NewQUICMessage(MessageTypeRelayDisconnect, &RelayDisconnectData{
		SessionID:    sessionID,
		NodeID:       nodeID,
		Reason:       reason,
		BytesIngress: bytesIngress,
		BytesEgress:  bytesEgress,
		Duration:     duration,
	})
}

// CreateRelaySessionQuery creates a relay session query request
func CreateRelaySessionQuery(clientNodeID, queryNodeID string) *QUICMessage {
	return NewQUICMessage(MessageTypeRelaySessionQuery, &RelaySessionQueryData{
		ClientNodeID: clientNodeID,
		QueryNodeID:  queryNodeID,
	})
}

// CreateRelaySessionStatus creates a relay session status response
func CreateRelaySessionStatus(clientNodeID string, hasSession bool, sessionID string, sessionActive bool, lastKeepalive int64) *QUICMessage {
	return NewQUICMessage(MessageTypeRelaySessionStatus, &RelaySessionStatusData{
		ClientNodeID:  clientNodeID,
		HasSession:    hasSession,
		SessionID:     sessionID,
		SessionActive: sessionActive,
		LastKeepalive: lastKeepalive,
	})
}

// CreateHolePunchConnect creates a hole punch CONNECT message
func CreateHolePunchConnect(nodeID string, observedAddrs, privateAddrs []string, publicIP, privateIP, natType string, isRelay bool) *QUICMessage {
	return NewQUICMessage(MessageTypeHolePunchConnect, &HolePunchConnectData{
		NodeID:        nodeID,
		ObservedAddrs: observedAddrs,
		PrivateAddrs:  privateAddrs,
		PublicIP:      publicIP,
		PrivateIP:     privateIP,
		NATType:       natType,
		IsRelay:       isRelay,
		SendTime:      time.Now().UnixMilli(),
	})
}

// CreateHolePunchSync creates a hole punch SYNC message
func CreateHolePunchSync(rtt int64) *QUICMessage {
	return NewQUICMessage(MessageTypeHolePunchSync, &HolePunchSyncData{
		RTT: rtt,
	})
}

// ============================================================================
// Metadata Broadcast Messages
// ============================================================================

// PeerMetadataUpdateData contains metadata update information
type PeerMetadataUpdateData struct {
	NodeID    string    `json:"node_id"`    // Who is sending
	Topic     string    `json:"topic"`      // Topic context
	Version   int       `json:"version"`    // Metadata version
	Timestamp time.Time `json:"timestamp"`  // When change occurred

	// What changed
	ChangeType string `json:"change_type"` // "relay", "network", "capability", "full"

	// Full metadata (for new peers or "full" change type)
	Metadata *database.PeerMetadata `json:"metadata,omitempty"`

	// Partial updates (more efficient for small changes)
	RelayUpdate   *RelayUpdateInfo   `json:"relay_update,omitempty"`
	NetworkUpdate *NetworkUpdateInfo `json:"network_update,omitempty"`

	// Broadcast metadata
	Sequence int64 `json:"sequence"` // For deduplication
	TTL      int   `json:"ttl"`      // Prevent infinite loops

	// Note: Peer exchange removed in Phase 5 - now handled by identity_exchange.go (Phase 3)
}

// RelayUpdateInfo contains relay connection change information
type RelayUpdateInfo struct {
	UsingRelay     bool   `json:"using_relay"`
	ConnectedRelay string `json:"connected_relay"` // New relay NodeID
	SessionID      string `json:"session_id"`      // New session ID
	RelayAddress   string `json:"relay_address"`   // How to reach via relay
}

// NetworkUpdateInfo contains network configuration changes
type NetworkUpdateInfo struct {
	PublicIP    string `json:"public_ip,omitempty"`
	PublicPort  int    `json:"public_port,omitempty"`
	PrivateIP   string `json:"private_ip,omitempty"`
	PrivatePort int    `json:"private_port,omitempty"`
	NATType     string `json:"nat_type,omitempty"`
}

// PeerMetadataUpdateAckData contains metadata update acknowledgment
type PeerMetadataUpdateAckData struct {
	NodeID   string `json:"node_id"`           // Who is acknowledging
	Sequence int64  `json:"sequence"`          // Sequence being acknowledged
	Status   string `json:"status"`            // "received", "applied", "error"
	Error    string `json:"error,omitempty"`   // Error message if status is "error"
}

// CreatePeerMetadataUpdate creates a metadata update message
func CreatePeerMetadataUpdate(
	nodeID string,
	topic string,
	version int,
	changeType string,
	sequence int64,
	ttl int,
	metadata *database.PeerMetadata,
	relayUpdate *RelayUpdateInfo,
	networkUpdate *NetworkUpdateInfo,
) *QUICMessage {
	return NewQUICMessage(MessageTypePeerMetadataUpdate, &PeerMetadataUpdateData{
		NodeID:        nodeID,
		Topic:         topic,
		Version:       version,
		Timestamp:     time.Now(),
		ChangeType:    changeType,
		Metadata:      metadata,
		RelayUpdate:   relayUpdate,
		NetworkUpdate: networkUpdate,
		Sequence:      sequence,
		TTL:           ttl,
	})
}

// CreatePeerMetadataUpdateAck creates a metadata update acknowledgment message
func CreatePeerMetadataUpdateAck(nodeID string, sequence int64, status string, errorMsg string) *QUICMessage {
	return NewQUICMessage(MessageTypePeerMetadataUpdateAck, &PeerMetadataUpdateAckData{
		NodeID:   nodeID,
		Sequence: sequence,
		Status:   status,
		Error:    errorMsg,
	})
}

// ============================================================================
// Phase 3: DHT-based Identity and Peer Exchange Messages
// ============================================================================

// IdentityExchangeData contains peer identity information for handshake
// This is exchanged immediately after QUIC connection establishment
type IdentityExchangeData struct {
	PeerID    string `json:"peer_id"`     // SHA1(public_key) - 40 hex chars
	DHTNodeID string `json:"dht_node_id"` // DHT routing node_id (20 bytes hex)
	PublicKey []byte `json:"public_key"`  // Ed25519 public key - 32 bytes
	NodeType  string `json:"node_type"`   // "public" or "private"
	IsRelay   bool   `json:"is_relay"`    // Is this peer offering relay services?
	IsStore   bool   `json:"is_store"`    // Has BEP_44 storage enabled?
	Topic     string `json:"topic"`       // Topic this connection is for
}

// KnownPeersRequestData requests known peers from remote peer
type KnownPeersRequestData struct {
	Topic    string   `json:"topic"`
	MaxPeers int      `json:"max_peers"` // Maximum peers to return (default: 50)
	Exclude  []string `json:"exclude,omitempty"` // Peer IDs to exclude (e.g., ourselves)
}

// KnownPeersResponseData contains list of known peers
type KnownPeersResponseData struct {
	Topic string            `json:"topic"`
	Peers []*KnownPeerEntry `json:"peers"`
	Count int               `json:"count"` // Total number of known peers
}

// KnownPeerEntry represents a minimal peer entry for exchange
// Includes essential identity info - full metadata can be queried from DHT later
type KnownPeerEntry struct {
	PeerID    string `json:"peer_id"`     // SHA1(public_key)
	DHTNodeID string `json:"dht_node_id"` // DHT routing node_id
	PublicKey []byte `json:"public_key"`  // Ed25519 public key
	IsRelay   bool   `json:"is_relay"`    // Is this peer a relay?
	IsStore   bool   `json:"is_store"`    // Has BEP_44 storage enabled?
}

// CreateIdentityExchange creates an identity exchange message
func CreateIdentityExchange(peerID string, dhtNodeID string, publicKey []byte, nodeType string, isRelay bool, isStore bool, topic string) *QUICMessage {
	return NewQUICMessage(MessageTypeIdentityExchange, &IdentityExchangeData{
		PeerID:    peerID,
		DHTNodeID: dhtNodeID,
		PublicKey: publicKey,
		NodeType:  nodeType,
		IsRelay:   isRelay,
		IsStore:   isStore,
		Topic:     topic,
	})
}

// CreateKnownPeersRequest creates a request for known peers
func CreateKnownPeersRequest(topic string, maxPeers int, exclude []string) *QUICMessage {
	if maxPeers <= 0 {
		maxPeers = 50 // Default limit
	}
	return NewQUICMessage(MessageTypeKnownPeersRequest, &KnownPeersRequestData{
		Topic:    topic,
		MaxPeers: maxPeers,
		Exclude:  exclude,
	})
}

// CreateKnownPeersResponse creates a response with known peers
func CreateKnownPeersResponse(topic string, peers []*KnownPeerEntry, totalCount int) *QUICMessage {
	return NewQUICMessage(MessageTypeKnownPeersResponse, &KnownPeersResponseData{
		Topic: topic,
		Peers: peers,
		Count: totalCount,
	})
}