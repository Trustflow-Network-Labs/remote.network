package utils

import (
	"fmt"
	"path/filepath"
)

// JobPathInfo contains the information needed to construct job-related paths
type JobPathInfo struct {
	OrchestratorPeerID  string // Peer that created the workflow
	WorkflowExecutionID int64  // The workflow_executions.id
	ExecutorPeerID      string // Peer executing the job
	JobExecutionID      int64  // The executor's job_executions.id
}

// PathType represents different types of paths for job data
type PathType string

const (
	PathTypeInput   PathType = "input"
	PathTypeOutput  PathType = "output"
	PathTypeMounts  PathType = "mounts"
	PathTypePackage PathType = "package"
	PathTypeLogs    PathType = "logs"
)

// BuildJobPath constructs a standardized path for job data
// Pattern: /workflows/<orchestrator_peer>/<execution_id>/jobs/<executor_peer>/<job_exec_id>/<path_type>/
func BuildJobPath(baseDir string, info JobPathInfo, pathType PathType) string {
	return filepath.Join(
		baseDir,
		"workflows",
		info.OrchestratorPeerID,
		fmt.Sprintf("%d", info.WorkflowExecutionID),
		"jobs",
		info.ExecutorPeerID,
		fmt.Sprintf("%d", info.JobExecutionID),
		string(pathType),
	)
}

// BuildJobMountPath constructs a path for a specific mount point
// Pattern: /workflows/<orchestrator_peer>/<execution_id>/jobs/<executor_peer>/<job_exec_id>/mounts/<mount_path>/
func BuildJobMountPath(baseDir string, info JobPathInfo, mountPath string) string {
	baseMountPath := BuildJobPath(baseDir, info, PathTypeMounts)
	return filepath.Join(baseMountPath, mountPath)
}

// BuildLocalPeerJobPath constructs a path for "Local Peer" final destination
// Uses job_execution_id = 0 as special marker for local peer
func BuildLocalPeerJobPath(baseDir string, orchestratorPeerID string, workflowExecutionID int64, pathType PathType) string {
	return BuildJobPath(baseDir, JobPathInfo{
		OrchestratorPeerID:  orchestratorPeerID,
		WorkflowExecutionID: workflowExecutionID,
		ExecutorPeerID:      orchestratorPeerID, // Local peer is orchestrator
		JobExecutionID:      0,                   // Special marker for local peer
	}, pathType)
}

// BuildTransferSourcePath constructs the source path for a data transfer
// For DATA services: uses output directory
// For DOCKER services: uses output directory or specific mount paths
func BuildTransferSourcePath(baseDir string, info JobPathInfo, interfaceType string, interfacePath string) string {
	switch interfaceType {
	case "STDOUT", "STDERR", "LOGS":
		// These go to the output directory
		return BuildJobPath(baseDir, info, PathTypeOutput)
	case "MOUNT":
		// Mount points use the specific mount path
		return BuildJobMountPath(baseDir, info, interfacePath)
	default:
		// Default to output
		return BuildJobPath(baseDir, info, PathTypeOutput)
	}
}

// BuildTransferDestinationPath constructs the destination path for a data transfer
// For STDIN: uses input directory
// For MOUNT: uses specific mount path
func BuildTransferDestinationPath(baseDir string, info JobPathInfo, interfaceType string, interfacePath string) string {
	switch interfaceType {
	case "STDIN":
		// STDIN data goes to input directory
		return BuildJobPath(baseDir, info, PathTypeInput)
	case "MOUNT":
		// Mount points use the specific mount path
		return BuildJobMountPath(baseDir, info, interfacePath)
	default:
		// Default to input
		return BuildJobPath(baseDir, info, PathTypeInput)
	}
}

// GetWorkflowsBaseDir returns the base directory for all workflow data
func GetWorkflowsBaseDir(appPaths *AppPaths) string {
	return appPaths.DataDir
}
