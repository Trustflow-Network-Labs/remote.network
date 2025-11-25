package types

import "time"

// TransferPackage version
const TransferManifestVersion = "1.0"

// Manifest file name within transfer package (unique name to avoid false positives with user data)
const TransferManifestFileName = ".remote_network_transfer_manifest.json"

// TransferManifest describes the contents of a transfer package
// Used for DOCKER and STANDALONE service output transfers
type TransferManifest struct {
	Version           string          `json:"version"`
	SourcePeerID      string          `json:"source_peer_id"`
	DestinationPeerID string          `json:"destination_peer_id"`
	WorkflowJobID     int64           `json:"workflow_job_id"`
	JobExecutionID    int64           `json:"job_execution_id"`
	CreatedAt         time.Time       `json:"created_at"`
	Entries           []ManifestEntry `json:"entries"`
}

// ManifestEntry describes a single file/output in the transfer package
type ManifestEntry struct {
	// Type of interface this output came from (STDOUT, STDERR, LOGS, MOUNT)
	Type string `json:"type"`

	// MountPath is the container mount path (only for MOUNT type, e.g., "/app")
	MountPath string `json:"mount_path,omitempty"`

	// ArchivePath is the path within the tar.gz archive
	ArchivePath string `json:"archive_path"`

	// DestinationPath is where the file should be placed on the receiver
	// Relative to the job's workflow directory (e.g., "input/stdout.txt" or "mounts/app/output.md")
	DestinationPath string `json:"destination_path"`

	// SizeBytes is the size of the file in bytes
	SizeBytes int64 `json:"size_bytes"`

	// Hash is the BLAKE3 hash (CID format) of the file
	Hash string `json:"hash"`
}

// NewTransferManifest creates a new transfer manifest
func NewTransferManifest(sourcePeerID, destinationPeerID string, workflowJobID, jobExecutionID int64) *TransferManifest {
	return &TransferManifest{
		Version:           TransferManifestVersion,
		SourcePeerID:      sourcePeerID,
		DestinationPeerID: destinationPeerID,
		WorkflowJobID:     workflowJobID,
		JobExecutionID:    jobExecutionID,
		CreatedAt:         time.Now(),
		Entries:           make([]ManifestEntry, 0),
	}
}

// AddEntry adds a manifest entry
func (m *TransferManifest) AddEntry(entry ManifestEntry) {
	m.Entries = append(m.Entries, entry)
}

// HasEntries returns true if the manifest has at least one entry
func (m *TransferManifest) HasEntries() bool {
	return len(m.Entries) > 0
}
