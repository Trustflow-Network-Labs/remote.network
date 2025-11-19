package types

import "time"

// WorkflowDefinition represents a parsed workflow from JSON
type WorkflowDefinition struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Jobs        []*WorkflowJob         `json:"jobs"`
	RawDef      map[string]interface{} `json:"raw_definition"` // Original JSON definition
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// WorkflowJob represents a job definition within a workflow
type WorkflowJob struct {
	ID                  int64              `json:"id"`
	WorkflowID          int64              `json:"workflow_id"`
	JobName             string             `json:"job_name"`
	ServiceID           int64              `json:"service_id"`
	ServiceType         string             `json:"service_type"` // DATA, DOCKER, STANDALONE
	ExecutorPeerID      string             `json:"executor_peer_id"`
	Entrypoint          []string           `json:"entrypoint,omitempty"`
	Commands            []string           `json:"commands,omitempty"`
	ExecutionConstraint string             `json:"execution_constraint"` // NONE, INPUTS_READY
	Interfaces          []*JobInterface    `json:"interfaces"`
	Passphrase          string             `json:"passphrase,omitempty"` // For encrypted DATA services (memory-only, not persisted)
	Status              string             `json:"status"`       // pending, running, completed, failed
	Result              map[string]interface{} `json:"result,omitempty"`
	Error               string             `json:"error,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

// JobInterface represents an interface (STDIN, STDOUT, MOUNT) for a job
type JobInterface struct {
	Type          string            `json:"type"`  // STDIN, STDOUT, MOUNT
	Path          string            `json:"path"`
	InterfacePeers []*InterfacePeer `json:"interface_peers"`
}

// InterfacePeer represents a peer connection for a job interface
type InterfacePeer struct {
	PeerNodeID        string `json:"peer_node_id"`    // Ed25519 peer ID
	PeerJobID         *int64 `json:"peer_job_id"`     // Connected job ID (nullable)
	PeerPath          string `json:"peer_path"`
	PeerMountFunction string `json:"peer_mount_function"` // INPUT, OUTPUT, BOTH
	DutyAcknowledged  bool   `json:"duty_acknowledged"`
}

// WorkflowStatus represents the overall status of a workflow execution
type WorkflowStatus struct {
	WorkflowID       int64     `json:"workflow_id"`
	Status           string    `json:"status"` // pending, running, completed, failed, cancelled
	CompletedJobs    int       `json:"completed_jobs"`
	TotalJobs        int       `json:"total_jobs"`
	CurrentJobID     *int64    `json:"current_job_id,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
}
