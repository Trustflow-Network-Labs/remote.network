package websocket

import (
	"encoding/json"
	"time"
)

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// Data update message types
	MessageTypeNodeStatus       MessageType = "node.status"
	MessageTypePeersUpdated     MessageType = "peers.updated"
	MessageTypeRelaySessions    MessageType = "relay.sessions"
	MessageTypeRelayCandidates  MessageType = "relay.candidates"
	MessageTypeServicesUpdated  MessageType = "services.updated"
	MessageTypeWorkflowsUpdated MessageType = "workflows.updated"
	MessageTypeExecutionUpdated MessageType = "execution.updated"
	MessageTypeJobStatusUpdated MessageType = "job.status.updated"
	MessageTypeBlacklistUpdated MessageType = "blacklist.updated"

	// File upload message types
	MessageTypeFileUploadStart    MessageType = "file.upload.start"
	MessageTypeFileUploadChunk    MessageType = "file.upload.chunk"
	MessageTypeFileUploadPause    MessageType = "file.upload.pause"
	MessageTypeFileUploadResume   MessageType = "file.upload.resume"
	MessageTypeFileUploadProgress MessageType = "file.upload.progress"
	MessageTypeFileUploadComplete MessageType = "file.upload.complete"
	MessageTypeFileUploadError    MessageType = "file.upload.error"

	// Docker operation message types
	MessageTypeDockerPullProgress   MessageType = "docker.pull.progress"
	MessageTypeDockerBuildOutput    MessageType = "docker.build.output"
	MessageTypeDockerOperationStart MessageType = "docker.operation.start"
	MessageTypeDockerOperationProgress MessageType = "docker.operation.progress"
	MessageTypeDockerOperationComplete MessageType = "docker.operation.complete"
	MessageTypeDockerOperationError MessageType = "docker.operation.error"

	// Standalone service operation message types
	MessageTypeStandaloneOperationStart    MessageType = "standalone.operation.start"
	MessageTypeStandaloneOperationProgress MessageType = "standalone.operation.progress"
	MessageTypeStandaloneOperationComplete MessageType = "standalone.operation.complete"
	MessageTypeStandaloneOperationError    MessageType = "standalone.operation.error"

	// Service discovery message types
	MessageTypeServiceSearchRequest  MessageType = "service.search.request"
	MessageTypeServiceSearchResponse MessageType = "service.search.response"

	// Peer capabilities message types
	MessageTypePeerCapabilitiesUpdated MessageType = "peer.capabilities.updated"

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

// DockerPullProgressPayload reports Docker image pull progress
type DockerPullProgressPayload struct {
	ServiceName    string                 `json:"service_name"`
	ImageName      string                 `json:"image_name"`
	Status         string                 `json:"status"`
	Progress       string                 `json:"progress,omitempty"`
	ProgressDetail map[string]interface{} `json:"progress_detail,omitempty"`
}

// DockerBuildOutputPayload reports Docker image build output
type DockerBuildOutputPayload struct {
	ServiceName string `json:"service_name"`
	ImageName   string `json:"image_name"`
	Stream      string `json:"stream,omitempty"`
	Error       string `json:"error,omitempty"`
	ErrorDetail map[string]interface{} `json:"error_detail,omitempty"`
}

// ServiceSearchRequestPayload contains service search criteria
type ServiceSearchRequestPayload struct {
	Query       string   `json:"query"`        // Search query (phrases)
	ServiceType string   `json:"service_type"` // Comma-separated service types (empty = all types)
	PeerIDs     []string `json:"peer_ids"`     // Peer IDs to query (empty = all peers)
	ActiveOnly  bool     `json:"active_only"`  // Only return active services
}

// ServiceSearchResponsePayload contains remote service search results
type ServiceSearchResponsePayload struct {
	Services []RemoteServiceInfo `json:"services"`
	Error    string              `json:"error,omitempty"`
	Complete bool                `json:"complete"` // Indicates if this is the final response
}

// RemoteServiceInfo represents a service from a remote peer
type RemoteServiceInfo struct {
	ID              int64                  `json:"id"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	ServiceType     string                 `json:"service_type"`
	Type            string                 `json:"type"`
	Status          string                 `json:"status"`
	PricingAmount   float64                `json:"pricing_amount"`
	PricingType     string                 `json:"pricing_type"`
	PricingInterval int                    `json:"pricing_interval"`
	PricingUnit     string                 `json:"pricing_unit"`
	Capabilities    map[string]interface{} `json:"capabilities,omitempty"`
	Hash            string                 `json:"hash,omitempty"`
	SizeBytes       int64                  `json:"size_bytes,omitempty"`
	Entrypoint      []string               `json:"entrypoint,omitempty"` // For DOCKER services
	Cmd             []string               `json:"cmd,omitempty"`        // For DOCKER services
	PeerID          string                 `json:"peer_id"` // ID of peer offering this service
	PeerName        string                 `json:"peer_name,omitempty"`
	Interfaces      []RemoteServiceInterface `json:"interfaces,omitempty"` // Service interfaces
}

// RemoteServiceInterface represents a service interface from a remote peer
type RemoteServiceInterface struct {
	InterfaceType string `json:"interface_type"`           // STDIN, STDOUT, MOUNT
	Path          string `json:"path,omitempty"`
	MountFunction string `json:"mount_function,omitempty"` // INPUT, OUTPUT, BOTH (for MOUNT interfaces)
}

// DockerOperationStartPayload reports that a Docker operation has started
type DockerOperationStartPayload struct {
	OperationID   string `json:"operation_id"`
	OperationType string `json:"operation_type"` // "build", "pull", "create"
	ServiceName   string `json:"service_name"`
	ImageName     string `json:"image_name,omitempty"`
	Message       string `json:"message"`
}

// DockerOperationProgressPayload reports Docker operation progress
type DockerOperationProgressPayload struct {
	OperationID string  `json:"operation_id"`
	Message     string  `json:"message"`
	Percentage  float64 `json:"percentage,omitempty"`
	Stream      string  `json:"stream,omitempty"`
}

// DockerOperationCompletePayload reports successful Docker operation completion
type DockerOperationCompletePayload struct {
	OperationID         string                   `json:"operation_id"`
	ServiceID           int64                    `json:"service_id"`
	ServiceName         string                   `json:"service_name"`
	ImageName           string                   `json:"image_name"`
	SuggestedInterfaces []map[string]interface{} `json:"suggested_interfaces,omitempty"`
	Service             map[string]interface{}   `json:"service,omitempty"`
}

// DockerOperationErrorPayload reports a Docker operation error
type DockerOperationErrorPayload struct {
	OperationID string `json:"operation_id"`
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
}

// StandaloneOperationStartPayload reports the start of a standalone operation
type StandaloneOperationStartPayload struct {
	OperationID   string `json:"operation_id"`
	OperationType string `json:"operation_type"` // "git-clone", "git-build", "upload"
	ServiceName   string `json:"service_name"`
	RepoURL       string `json:"repo_url,omitempty"`
	Message       string `json:"message"`
}

// StandaloneOperationProgressPayload reports standalone operation progress
type StandaloneOperationProgressPayload struct {
	OperationID string  `json:"operation_id"`
	Message     string  `json:"message"`
	Percentage  float64 `json:"percentage,omitempty"`
	Step        string  `json:"step,omitempty"` // "cloning", "building", "validating"
}

// StandaloneOperationCompletePayload reports successful standalone operation completion
type StandaloneOperationCompletePayload struct {
	OperationID         string                   `json:"operation_id"`
	ServiceID           int64                    `json:"service_id"`
	ServiceName         string                   `json:"service_name"`
	ExecutablePath      string                   `json:"executable_path"`
	CommitHash          string                   `json:"commit_hash,omitempty"`
	SuggestedInterfaces []map[string]interface{} `json:"suggested_interfaces,omitempty"`
	Service             map[string]interface{}   `json:"service,omitempty"`
}

// StandaloneOperationErrorPayload reports a standalone operation error
type StandaloneOperationErrorPayload struct {
	OperationID string `json:"operation_id"`
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
}

// ExecutionUpdatedPayload contains workflow execution status update
type ExecutionUpdatedPayload struct {
	ExecutionID int64  `json:"execution_id"`
	WorkflowID  int64  `json:"workflow_id"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// JobStatusUpdatedPayload contains job status update
type JobStatusUpdatedPayload struct {
	JobExecutionID int64  `json:"job_execution_id"`
	WorkflowJobID  int64  `json:"workflow_job_id"`
	ExecutionID    int64  `json:"execution_id"`
	JobName        string `json:"job_name"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}
