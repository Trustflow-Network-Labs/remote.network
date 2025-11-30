package types

// Message type constants for P2P communication
const (
	MessageTypeJobRequest              = "JOB_REQUEST"
	MessageTypeJobResponse             = "JOB_RESPONSE"
	MessageTypeJobStart                = "JOB_START"         // Phase 2: Start execution with complete interfaces
	MessageTypeJobStartResponse        = "JOB_START_RESPONSE" // Acknowledgment of start command
	MessageTypeJobStatusUpdate         = "JOB_STATUS_UPDATE"
	MessageTypeJobDataTransferRequest  = "JOB_DATA_TRANSFER_REQUEST"
	MessageTypeJobDataTransferResponse = "JOB_DATA_TRANSFER_RESPONSE"
	MessageTypeJobDataChunk            = "JOB_DATA_CHUNK"
	MessageTypeJobDataTransferComplete = "JOB_DATA_TRANSFER_COMPLETE"
	MessageTypeJobCancel               = "JOB_CANCEL"
)

// P2PMessage represents a generic P2P message wrapper
type P2PMessage struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp int64       `json:"timestamp"` // Unix timestamp
	MessageID string      `json:"message_id"`
}

// JobCancelRequest represents a request to cancel a running job
type JobCancelRequest struct {
	JobExecutionID int64  `json:"job_execution_id"`
	WorkflowJobID  int64  `json:"workflow_job_id"`
	Reason         string `json:"reason,omitempty"`
	RequestedBy    string `json:"requested_by"` // Peer ID
}

// JobCancelResponse represents a response to a job cancellation request
type JobCancelResponse struct {
	JobExecutionID int64  `json:"job_execution_id"`
	Cancelled      bool   `json:"cancelled"`
	Message        string `json:"message,omitempty"`
}
