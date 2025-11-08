package types

import "time"

// Job status constants
const (
	JobStatusIdle      = "IDLE"
	JobStatusReady     = "READY"
	JobStatusRunning   = "RUNNING"
	JobStatusCompleted = "COMPLETED"
	JobStatusErrored   = "ERRORED"
	JobStatusCancelled = "CANCELLED"
)

// Execution constraint constants
const (
	ExecutionConstraintNone        = "NONE"
	ExecutionConstraintInputsReady = "INPUTS_READY"
)

// Interface type constants
const (
	InterfaceTypeStdin  = "STDIN"
	InterfaceTypeStdout = "STDOUT"
	InterfaceTypeMount  = "MOUNT"
)

// Mount function constants
const (
	MountFunctionProvider = "PROVIDER"
	MountFunctionReceiver = "RECEIVER"
)

// Service type constants
const (
	ServiceTypeData       = "DATA"
	ServiceTypeDocker     = "DOCKER"
	ServiceTypeStandalone = "STANDALONE"
)

// JobExecutionRequest represents a request to execute a job on a peer
type JobExecutionRequest struct {
	WorkflowID          int64              `json:"workflow_id"`
	WorkflowJobID       int64              `json:"workflow_job_id"`
	JobName             string             `json:"job_name"`
	ServiceID           int64              `json:"service_id"`
	ServiceType         string             `json:"service_type"`
	Entrypoint          []string           `json:"entrypoint,omitempty"`
	Commands            []string           `json:"commands,omitempty"`
	ExecutionConstraint string             `json:"execution_constraint"`
	Interfaces          []*JobInterface    `json:"interfaces"`
	OrderingPeerID      string             `json:"ordering_peer_id"` // Who requested this job
	RequestedAt         time.Time          `json:"requested_at"`
}

// JobExecutionResponse represents a response to a job execution request
type JobExecutionResponse struct {
	JobExecutionID int64     `json:"job_execution_id"`
	WorkflowJobID  int64     `json:"workflow_job_id"`
	Accepted       bool      `json:"accepted"`
	Message        string    `json:"message,omitempty"`
	ExecutorPeerID string    `json:"executor_peer_id"`
	RespondedAt    time.Time `json:"responded_at"`
}

// JobStatusUpdate represents an update to a job's status
type JobStatusUpdate struct {
	JobExecutionID int64                  `json:"job_execution_id"`
	WorkflowJobID  int64                  `json:"workflow_job_id"`
	Status         string                 `json:"status"`
	ErrorMessage   string                 `json:"error_message,omitempty"`
	Result         map[string]interface{} `json:"result,omitempty"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// JobDataTransferRequest represents a request to transfer job data
type JobDataTransferRequest struct {
	JobExecutionID     int64  `json:"job_execution_id"`
	InterfaceType      string `json:"interface_type"` // STDIN, STDOUT, MOUNT
	SourcePeerID       string `json:"source_peer_id"`
	DestinationPeerID  string `json:"destination_peer_id"`
	SourcePath         string `json:"source_path"`
	DestinationPath    string `json:"destination_path"`
	DataHash           string `json:"data_hash,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Passphrase         string `json:"passphrase,omitempty"` // For encrypted data (not stored, memory-only)
	Encrypted          bool   `json:"encrypted,omitempty"`  // Whether data is encrypted
}

// JobDataTransferResponse represents a response to a data transfer request
type JobDataTransferResponse struct {
	JobExecutionID int64  `json:"job_execution_id"`
	Accepted       bool   `json:"accepted"`
	Message        string `json:"message,omitempty"`
	TransferID     string `json:"transfer_id,omitempty"`
}

// JobDataChunk represents a chunk of data being transferred
type JobDataChunk struct {
	TransferID  string `json:"transfer_id"`
	ChunkIndex  int    `json:"chunk_index"`
	TotalChunks int    `json:"total_chunks"`
	Data        []byte `json:"data"`
	IsLast      bool   `json:"is_last"`
}

// JobDataTransferComplete represents completion of a data transfer
type JobDataTransferComplete struct {
	TransferID        string `json:"transfer_id"`
	JobExecutionID    int64  `json:"job_execution_id"`
	Success           bool   `json:"success"`
	BytesTransferred  int64  `json:"bytes_transferred"`
	ErrorMessage      string `json:"error_message,omitempty"`
	CompletedAt       time.Time `json:"completed_at"`
}
