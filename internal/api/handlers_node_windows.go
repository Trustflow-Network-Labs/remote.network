//go:build windows

package api

import "syscall"

const (
	// DETACHED_PROCESS is a Windows creation flag for detached processes
	// This constant is not defined in syscall package, so we define it here
	DETACHED_PROCESS = 0x00000008
)

// getDetachedProcessAttr returns platform-specific process attributes for detached process creation
// On Windows, this creates a new process group and detaches the process from the parent
func getDetachedProcessAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS,
	}
}
