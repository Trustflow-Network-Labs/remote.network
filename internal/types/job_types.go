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
	InterfaceTypeStdin   = "STDIN"
	InterfaceTypeStdout  = "STDOUT"
	InterfaceTypeStderr  = "STDERR"
	InterfaceTypeLogs    = "LOGS"
	InterfaceTypeMount   = "MOUNT"
	InterfaceTypePackage = "PACKAGE" // Transfer package containing multiple outputs with manifest
)

// Mount function constants
const (
	MountFunctionInput  = "INPUT"   // Provides data TO the container
	MountFunctionOutput = "OUTPUT"  // Receives data FROM the container
	MountFunctionBoth   = "BOTH"    // Bidirectional
)

// Service type constants
const (
	ServiceTypeData       = "DATA"
	ServiceTypeDocker     = "DOCKER"
	ServiceTypeStandalone = "STANDALONE"
)

// Service source type constants
const (
	ServiceSourceRegistry = "registry"
	ServiceSourceGit      = "git"
	ServiceSourceLocal    = "local"
	ServiceSourceUpload   = "upload"
)

// Service status constants
const (
	ServiceStatusActive   = "ACTIVE"
	ServiceStatusInactive = "INACTIVE"
)

// Pricing type constants
const (
	PricingTypeOneTime   = "ONE_TIME"
	PricingTypeRecurring = "RECURRING"
)

// Pricing unit constants
const (
	PricingUnitSeconds = "SECONDS"
	PricingUnitMinutes = "MINUTES"
	PricingUnitHours   = "HOURS"
	PricingUnitDays    = "DAYS"
	PricingUnitWeeks   = "WEEKS"
	PricingUnitMonths  = "MONTHS"
	PricingUnitYears   = "YEARS"
)

// JobExecutionRequest represents a request to execute a job on a peer
type JobExecutionRequest struct {
	WorkflowID          int64              `json:"workflow_id"`
	WorkflowJobID       int64              `json:"workflow_job_id"`
	JobName             string             `json:"job_name"`
	ServiceID           int64              `json:"service_id"`
	ServiceType         string             `json:"service_type"`
	ServiceName         string             `json:"service_name,omitempty"` // For reference/logging
	ExecutorPeerID      string             `json:"executor_peer_id"` // Which peer should execute this job
	Entrypoint          []string           `json:"entrypoint,omitempty"`
	Commands            []string           `json:"commands,omitempty"`
	ExecutionConstraint string             `json:"execution_constraint"`
	Interfaces          []*JobInterface    `json:"interfaces"`
	OrderingPeerID      string             `json:"ordering_peer_id"` // Who requested this job
	RequestedAt         time.Time          `json:"requested_at"`
	// DataService details (for DATA service types only, sent from orchestrator to worker)
	DataServiceFilePath      string `json:"data_service_file_path,omitempty"`
	DataServiceEncryptedPath string `json:"data_service_encrypted_path,omitempty"`
	DataServiceHash          string `json:"data_service_hash,omitempty"`
	DataServiceSizeBytes     int64  `json:"data_service_size_bytes,omitempty"`
	DataServiceEncrypted     bool   `json:"data_service_encrypted,omitempty"`
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

// JobStartRequest represents Phase 2 - start execution with complete interface routing
type JobStartRequest struct {
	WorkflowID     int64           `json:"workflow_id"`
	WorkflowJobID  int64           `json:"workflow_job_id"`
	JobExecutionID int64           `json:"job_execution_id"`
	Interfaces     []*JobInterface `json:"interfaces"` // Complete interfaces with all peer_job_execution_ids populated
}

// JobStartResponse represents acknowledgment of job start command
type JobStartResponse struct {
	JobExecutionID int64  `json:"job_execution_id"`
	WorkflowJobID  int64  `json:"workflow_job_id"`
	Started        bool   `json:"started"`
	Message        string `json:"message,omitempty"`
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

// JobStatusRequest represents a request for job status from executor peer
type JobStatusRequest struct {
	JobExecutionID int64 `json:"job_execution_id"`
	WorkflowJobID  int64 `json:"workflow_job_id"`
}

// JobStatusResponse represents a response to a job status request
type JobStatusResponse struct {
	JobExecutionID int64                  `json:"job_execution_id"`
	WorkflowJobID  int64                  `json:"workflow_job_id"`
	Status         string                 `json:"status"`
	ErrorMessage   string                 `json:"error_message,omitempty"`
	Result         map[string]interface{} `json:"result,omitempty"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Found          bool                   `json:"found"` // Indicates if job was found on executor peer
}

// JobDataTransferRequest represents a request to transfer job data
type JobDataTransferRequest struct {
	TransferID                string `json:"transfer_id"`                            // Unique transfer ID generated by sender
	WorkflowJobID             int64  `json:"workflow_job_id"`                        // Shared workflow job ID both peers understand
	SourceJobExecutionID      int64  `json:"source_job_execution_id"`                // Source job execution ID (sender's job)
	DestinationJobExecutionID int64  `json:"destination_job_execution_id"`           // Destination job execution ID (receiver's job)
	InterfaceType             string `json:"interface_type"`                         // STDIN, STDOUT, MOUNT
	SourcePeerID              string `json:"source_peer_id"`
	DestinationPeerID         string `json:"destination_peer_id"`
	SourcePath                string `json:"source_path"`
	DestinationPath           string `json:"destination_path"`
	DestinationFileName       string `json:"destination_file_name,omitempty"`        // Optional: rename file/folder at destination
	DataHash                  string `json:"data_hash,omitempty"`
	SizeBytes                 int64  `json:"size_bytes,omitempty"`
	Passphrase                string `json:"passphrase,omitempty"`                   // For encrypted data (not stored, memory-only)
	Encrypted                 bool   `json:"encrypted,omitempty"`                    // Whether data is encrypted
}

// JobDataTransferResponse represents a response to a data transfer request
type JobDataTransferResponse struct {
	WorkflowJobID int64  `json:"workflow_job_id"` // Shared workflow job ID both peers understand
	Accepted      bool   `json:"accepted"`
	Message       string `json:"message,omitempty"`
	TransferID    string `json:"transfer_id,omitempty"`
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

// JobDataChunkAck represents batch acknowledgment of received chunks (receiver → sender)
type JobDataChunkAck struct {
	TransferID   string    `json:"transfer_id"`
	ChunkIndexes []int     `json:"chunk_indexes"` // Batch of received chunks (10-20 chunks)
	Timestamp    time.Time `json:"timestamp"`
}

// JobDataTransferResume represents a request to resume a transfer
type JobDataTransferResume struct {
	TransferID     string `json:"transfer_id"`
	ReceivedChunks []int  `json:"received_chunks"`           // Which chunks receiver has
	RequestChunks  []int  `json:"request_chunks,omitempty"`  // Specific chunks to resend
	LastKnownChunk int    `json:"last_known_chunk"`          // Last chunk receiver processed
}

// JobDataTransferStall represents notification that a transfer has stalled
type JobDataTransferStall struct {
	TransferID    string `json:"transfer_id"`
	LastChunkSent int    `json:"last_chunk_sent"` // Last chunk sender successfully sent
	Reason        string `json:"reason"`           // Why the stall occurred
}
