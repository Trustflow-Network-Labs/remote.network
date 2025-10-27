package websocket

import (
	"encoding/json"
	"time"
)

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// Data update message types
	MessageTypeNodeStatus      MessageType = "node.status"
	MessageTypePeersUpdated    MessageType = "peers.updated"
	MessageTypeRelaySessions   MessageType = "relay.sessions"
	MessageTypeRelayCandidates MessageType = "relay.candidates"
	MessageTypeServicesUpdated MessageType = "services.updated"
	MessageTypeWorkflowsUpdated MessageType = "workflows.updated"
	MessageTypeBlacklistUpdated MessageType = "blacklist.updated"

	// File upload message types
	MessageTypeFileUploadStart    MessageType = "file.upload.start"
	MessageTypeFileUploadChunk    MessageType = "file.upload.chunk"
	MessageTypeFileUploadPause    MessageType = "file.upload.pause"
	MessageTypeFileUploadResume   MessageType = "file.upload.resume"
	MessageTypeFileUploadProgress MessageType = "file.upload.progress"
	MessageTypeFileUploadComplete MessageType = "file.upload.complete"
	MessageTypeFileUploadError    MessageType = "file.upload.error"

	// Control message types
	MessageTypePing            MessageType = "ping"
	MessageTypePong            MessageType = "pong"
	MessageTypeError           MessageType = "error"
	MessageTypeConnected       MessageType = "connected"
)

// Message is the base structure for all WebSocket messages
type Message struct {
	Type      MessageType     `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

// NewMessage creates a new message with the given type and payload
func NewMessage(msgType MessageType, payload interface{}) (*Message, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &Message{
		Type:      msgType,
		Payload:   payloadBytes,
		Timestamp: time.Now().Unix(),
	}, nil
}

// NodeStatusPayload contains node status information
type NodeStatusPayload struct {
	PeerID     string                 `json:"peer_id"`
	DHTNodeID  string                 `json:"dht_node_id"`
	Uptime     int64                  `json:"uptime_seconds"`
	Stats      map[string]interface{} `json:"stats"`
	KnownPeers int                    `json:"known_peers"`
}

// PeersPayload contains peer list information
type PeersPayload struct {
	Peers []PeerInfo `json:"peers"`
}

type PeerInfo struct {
	ID            string                 `json:"id"`
	Addresses     []string               `json:"addresses"`
	LastSeen      int64                  `json:"last_seen"`
	IsRelay       bool                   `json:"is_relay"`
	CanBeRelay    bool                   `json:"can_be_relay"`
	ConnectionQuality int                `json:"connection_quality"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// RelaySessionsPayload contains active relay session information
type RelaySessionsPayload struct {
	Sessions []RelaySession `json:"sessions"`
}

type RelaySession struct {
	SessionID       string  `json:"session_id"`
	RemotePeerID    string  `json:"remote_peer_id"`
	StartTime       int64   `json:"start_time"`
	DurationSeconds int64   `json:"duration_seconds"`
	BytesSent       int64   `json:"bytes_sent"`
	BytesRecv       int64   `json:"bytes_recv"`
	Earnings        float64 `json:"earnings"`
}

// RelayCandidatesPayload contains available relay candidates
type RelayCandidatesPayload struct {
	Candidates []RelayCandidate `json:"candidates"`
}

type RelayCandidate struct {
	NodeID          string  `json:"node_id"`
	PeerID          string  `json:"peer_id,omitempty"`
	Endpoint        string  `json:"endpoint"`
	Latency         string  `json:"latency"`
	LatencyMs       int64   `json:"latency_ms"`
	ReputationScore float64 `json:"reputation_score"`
	PricingPerGB    float64 `json:"pricing_per_gb"`
	Capacity        int     `json:"capacity"`
	LastSeen        int64   `json:"last_seen"`
	IsConnected     bool    `json:"is_connected"`
	IsPreferred     bool    `json:"is_preferred"`
}

// ServicesPayload contains service list information
type ServicesPayload struct {
	Services []ServiceInfo `json:"services"`
}

type ServiceInfo struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Type        string                 `json:"type"`
	Config      map[string]interface{} `json:"config,omitempty"`
	Status      string                 `json:"status"`
	CreatedAt   int64                  `json:"created_at"`
	UpdatedAt   int64                  `json:"updated_at"`
}

// WorkflowsPayload contains workflow list information
type WorkflowsPayload struct {
	Workflows []WorkflowInfo `json:"workflows"`
}

type WorkflowInfo struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Status      string                 `json:"status"`
	JobCount    int                    `json:"job_count"`
	Config      map[string]interface{} `json:"config,omitempty"`
	CreatedAt   int64                  `json:"created_at"`
	UpdatedAt   int64                  `json:"updated_at"`
}

// BlacklistPayload contains blacklist information
type BlacklistPayload struct {
	Blacklist []BlacklistEntry `json:"blacklist"`
}

type BlacklistEntry struct {
	PeerID    string `json:"peer_id"`
	Reason    string `json:"reason,omitempty"`
	AddedAt   int64  `json:"added_at"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// ErrorPayload contains error information
type ErrorPayload struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// ConnectedPayload is sent when a client successfully connects
type ConnectedPayload struct {
	Message string `json:"message"`
	PeerID  string `json:"peer_id"`
}

// FileUploadStartPayload initiates a file upload session
type FileUploadStartPayload struct {
	ServiceID     int64  `json:"service_id"`
	UploadGroupID string `json:"upload_group_id"`
	Filename      string `json:"filename"`
	FilePath      string `json:"file_path"`
	FileIndex     int    `json:"file_index"`
	TotalFiles    int    `json:"total_files"`
	TotalSize     int64  `json:"total_size"`
	TotalChunks   int    `json:"total_chunks"`
	ChunkSize     int    `json:"chunk_size"`
}

// FileUploadChunkPayload contains a chunk of uploaded file data
type FileUploadChunkPayload struct {
	SessionID  string `json:"session_id"`
	ChunkIndex int    `json:"chunk_index"`
	Data       string `json:"data"` // base64 encoded chunk data
}

// FileUploadPausePayload pauses an upload session
type FileUploadPausePayload struct {
	SessionID string `json:"session_id"`
}

// FileUploadResumePayload resumes an upload session
type FileUploadResumePayload struct {
	SessionID string `json:"session_id"`
}

// FileUploadProgressPayload reports upload progress
type FileUploadProgressPayload struct {
	SessionID      string  `json:"session_id"`
	ChunksReceived int     `json:"chunks_received"`
	BytesUploaded  int64   `json:"bytes_uploaded"`
	Percentage     float64 `json:"percentage"`
}

// FileUploadCompletePayload signals successful upload completion
type FileUploadCompletePayload struct {
	SessionID string `json:"session_id"`
	FileHash  string `json:"file_hash"`
}

// FileUploadErrorPayload reports an upload error
type FileUploadErrorPayload struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
}
