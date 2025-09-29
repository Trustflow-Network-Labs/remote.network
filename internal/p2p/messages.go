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

	// Legacy support
	MessageTypeEcho             MessageType = "echo"
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
}

// MetadataResponseData contains peer metadata in response
type MetadataResponseData struct {
	Topic    string                    `json:"topic"`
	NodeID   string                    `json:"node_id"`
	Metadata *database.PeerMetadata    `json:"metadata,omitempty"`
	Error    string                    `json:"error,omitempty"`
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