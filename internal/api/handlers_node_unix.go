//go:build !windows

package api

import "syscall"

// getDetachedProcessAttr returns platform-specific process attributes for detached process creation
// On Unix systems, this creates a new session using Setsid
func getDetachedProcessAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // Create new session (Unix-specific)
	}
}
