package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type PIDManager struct {
	dir string
	cm  *ConfigManager
}

func NewPIDManager(cm *ConfigManager) (*PIDManager, error) {
	// Get app data directory - default to current directory if not configured
	dataDir := cm.GetConfigWithDefault("data_dir", ".")
	if dataDir == "" {
		dataDir = "."
	}

	return &PIDManager{
		dir: dataDir,
		cm:  cm,
	}, nil
}

func (p *PIDManager) WritePID(pid int) error {
	// Get PID file path from config or use default
	pidFileName := p.cm.GetConfigWithDefault("pid_path", "remote-network.pid")

	// Make sure we have OS specific path separator
	switch runtime.GOOS {
	case "linux", "darwin":
		pidFileName = filepath.ToSlash(pidFileName)
	case "windows":
		pidFileName = filepath.FromSlash(pidFileName)
	default:
		return fmt.Errorf("unsupported OS type `%s`", runtime.GOOS)
	}

	// Create full PID file path
	path := filepath.Join(p.dir, pidFileName)

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory for PID file: %v", err)
	}

	pidStr := strconv.Itoa(pid)
	return os.WriteFile(path, []byte(pidStr), 0644)
}

func (p *PIDManager) ReadPID() (int, error) {
	// Get PID file path from config or use default
	pidFileName := p.cm.GetConfigWithDefault("pid_path", "remote-network.pid")

	// Make sure we have OS specific path separator
	switch runtime.GOOS {
	case "linux", "darwin":
		pidFileName = filepath.ToSlash(pidFileName)
	case "windows":
		pidFileName = filepath.FromSlash(pidFileName)
	default:
		return 0, fmt.Errorf("unsupported OS type `%s`", runtime.GOOS)
	}

	// Create full PID file path
	path := filepath.Join(p.dir, pidFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, errors.New("PID file does not exist - node is not running")
		}
		return 0, fmt.Errorf("failed to read PID file: %v", err)
	}

	pidStr := string(data)
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID format in file: %v", err)
	}

	return pid, nil
}

func (p *PIDManager) StopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process with PID %d: %v", pid, err)
	}

	if runtime.GOOS == "windows" {
		// On Windows, Kill() is the only option
		return process.Kill()
	} else {
		// On Unix-like systems, try graceful termination first
		err = process.Signal(syscall.SIGTERM)
		if err != nil {
			return fmt.Errorf("failed to send SIGTERM to process %d: %v", pid, err)
		}

		// Wait for graceful termination (10 seconds grace period)
		gracePeriod := 10 * time.Second
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(gracePeriod)

		for {
			select {
			case <-timeout:
				// Grace period expired, force kill
				fmt.Printf("Grace period expired, force killing process %d\n", pid)
				return process.Signal(syscall.SIGKILL)
			case <-ticker.C:
				// Check if process still exists
				err := process.Signal(syscall.Signal(0)) // Signal 0 checks existence
				if err != nil {
					// Process has terminated gracefully
					fmt.Printf("Process %d terminated gracefully\n", pid)
					return nil
				}
			}
		}
	}
}

func (p *PIDManager) RemovePIDFile() error {
	pidFileName := p.cm.GetConfigWithDefault("pid_path", "remote-network.pid")

	switch runtime.GOOS {
	case "linux", "darwin":
		pidFileName = filepath.ToSlash(pidFileName)
	case "windows":
		pidFileName = filepath.FromSlash(pidFileName)
	default:
		return fmt.Errorf("unsupported OS type `%s`", runtime.GOOS)
	}

	path := filepath.Join(p.dir, pidFileName)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove PID file: %v", err)
	}
	return nil
}

func (p *PIDManager) IsProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	if runtime.GOOS == "windows" {
		// On Windows, if FindProcess succeeds, process exists
		return true
	} else {
		// On Unix-like systems, check with signal 0
		err = process.Signal(syscall.Signal(0))
		return err == nil
	}
}