package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// StandaloneExecutionConfig contains configuration for executing a standalone service
type StandaloneExecutionConfig struct {
	ServiceID      int64
	JobExecutionID int64
	Executable     string
	Arguments      []string
	WorkingDir     string
	Inputs         []string              // Content for STDIN (one entry per line)
	Outputs        []string              // Paths for output files
	Mounts         map[string]string     // Host path -> Container path mapping
	EnvVars        map[string]string
	Timeout        time.Duration
	RunAsUser      string                // "user:group" or "uid:gid"
}

// StandaloneExecutionResult contains the results of executing a standalone service
type StandaloneExecutionResult struct {
	ExitCode     int
	Stdout       string
	Stderr       string
	Logs         string
	Error        error
	Duration     time.Duration
	OutputFiles  map[string]string // Path -> Content (for small files)
}

// ExecuteService executes a standalone application
func (ss *StandaloneService) ExecuteService(
	config *StandaloneExecutionConfig,
) *StandaloneExecutionResult {
	startTime := time.Now()

	result := &StandaloneExecutionResult{
		ExitCode:    -1,
		OutputFiles: make(map[string]string),
	}

	ss.logger.Info(fmt.Sprintf("Executing standalone service: %s (job_execution_id=%d)",
		config.Executable, config.JobExecutionID), "standalone-exec")

	// 1. Validate executable
	if err := ss.validateExecutable(config.Executable); err != nil {
		result.Error = fmt.Errorf("invalid executable: %w", err)
		result.Logs = fmt.Sprintf("Error: %v", result.Error)
		return result
	}

	// 2. Setup working directory
	workingDir := config.WorkingDir
	if workingDir == "" {
		workingDir = ss.getExecutableDirectory(config.Executable)
	}

	if err := ss.validateWorkingDirectory(workingDir); err != nil {
		result.Error = fmt.Errorf("invalid working directory: %w", err)
		result.Logs = fmt.Sprintf("Error: %v", result.Error)
		return result
	}

	// 3. Setup execution context with timeout
	ctx := context.Background()
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}

	// 4. Execute the process
	execResult := ss.executeProcess(ctx, config, workingDir)

	// 5. Calculate duration
	execResult.Duration = time.Since(startTime)

	ss.logger.Info(fmt.Sprintf("Standalone service execution completed: exit_code=%d, duration=%v",
		execResult.ExitCode, execResult.Duration), "standalone-exec")

	return execResult
}

// executeProcess is the core process execution wrapper
func (ss *StandaloneService) executeProcess(
	ctx context.Context,
	config *StandaloneExecutionConfig,
	workingDir string,
) *StandaloneExecutionResult {
	result := &StandaloneExecutionResult{
		ExitCode:    -1,
		OutputFiles: make(map[string]string),
	}

	// Build command
	cmd := exec.CommandContext(ctx, config.Executable, config.Arguments...)
	cmd.Dir = workingDir

	// Setup environment variables
	cmd.Env = ss.buildEnvVars(config.EnvVars)

	// Setup STDIN
	var stdinPipe io.WriteCloser
	if len(config.Inputs) > 0 {
		var err error
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			result.Error = fmt.Errorf("failed to create stdin pipe: %w", err)
			result.Logs = fmt.Sprintf("Error: %v", result.Error)
			return result
		}
	}

	// Setup STDOUT and STDERR capture
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Platform-specific: Set user/group if specified (Unix only)
	if config.RunAsUser != "" && runtime.GOOS != "windows" {
		if err := ss.setProcessCredentials(cmd, config.RunAsUser); err != nil {
			result.Error = fmt.Errorf("failed to set process credentials: %w", err)
			result.Logs = fmt.Sprintf("Error: %v", result.Error)
			return result
		}
	}

	// Start the process
	ss.logger.Debug(fmt.Sprintf("Starting process: %s %v", config.Executable, config.Arguments), "standalone-exec")
	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("failed to start process: %w", err)
		result.Logs = fmt.Sprintf("Error: %v", result.Error)
		return result
	}

	// Write to STDIN if inputs provided
	if stdinPipe != nil {
		go func() {
			defer stdinPipe.Close()
			for _, input := range config.Inputs {
				fmt.Fprintln(stdinPipe, input)
			}
		}()
	}

	// Wait for completion
	err := cmd.Wait()

	// Capture output
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	result.Logs = fmt.Sprintf("STDOUT:\n%s\n\nSTDERR:\n%s\n", result.Stdout, result.Stderr)

	// Get exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			ss.logger.Warn(fmt.Sprintf("Process exited with non-zero code: %d", result.ExitCode), "standalone-exec")
		} else if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("process execution timed out")
			result.ExitCode = -1
			result.Logs += fmt.Sprintf("\nError: %v", result.Error)
		} else {
			result.Error = fmt.Errorf("process execution failed: %w", err)
			result.ExitCode = -1
			result.Logs += fmt.Sprintf("\nError: %v", result.Error)
		}
	} else {
		result.ExitCode = 0
	}

	// Read output files if specified
	for _, outputPath := range config.Outputs {
		content, err := ss.readOutputFile(outputPath)
		if err != nil {
			ss.logger.Warn(fmt.Sprintf("Failed to read output file %s: %v", outputPath, err), "standalone-exec")
			result.Logs += fmt.Sprintf("\nWarning: Failed to read output file %s: %v", outputPath, err)
		} else {
			result.OutputFiles[outputPath] = content
		}
	}

	return result
}

// setProcessCredentials sets the user/group for process execution (Unix only)
func (ss *StandaloneService) setProcessCredentials(cmd *exec.Cmd, runAsUser string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("user switching not supported on Windows")
	}

	// Parse user:group or uid:gid
	parts := strings.Split(runAsUser, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid run_as_user format: expected 'user:group' or 'uid:gid', got '%s'", runAsUser)
	}

	// For now, we'll log a warning that this feature requires root privileges
	// Actual implementation would need to parse UID/GID and set syscall.Credential
	ss.logger.Warn(fmt.Sprintf("User switching requested (%s) but requires root privileges - running as current user", runAsUser), "standalone-exec")

	// TODO: Implement proper user switching with syscall.Credential
	// This requires the process to be running as root, which may not be desirable
	// Alternative: Use sudo or su commands

	// Example implementation (commented out as it requires root):
	/*
	uid, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		// Try looking up user by name
		u, err := user.Lookup(parts[0])
		if err != nil {
			return fmt.Errorf("failed to lookup user %s: %w", parts[0], err)
		}
		uid, _ = strconv.ParseUint(u.Uid, 10, 32)
	}

	gid, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		// Try looking up group by name
		g, err := user.LookupGroup(parts[1])
		if err != nil {
			return fmt.Errorf("failed to lookup group %s: %w", parts[1], err)
		}
		gid, _ = strconv.ParseUint(g.Gid, 10, 32)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}
	*/

	return nil
}

// readOutputFile reads content from an output file
func (ss *StandaloneService) readOutputFile(path string) (string, error) {
	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	// Only read files up to 10MB to avoid memory issues
	maxSize := int64(10 * 1024 * 1024)
	if info.Size() > maxSize {
		return "", fmt.Errorf("output file too large: %d bytes (max %d bytes)", info.Size(), maxSize)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// WriteOutputToFile writes execution output to a file
func (ss *StandaloneService) WriteOutputToFile(outputPath string, content string) error {
	// Ensure directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	ss.logger.Debug(fmt.Sprintf("Wrote output to file: %s (%d bytes)", outputPath, len(content)), "standalone-exec")
	return nil
}

// Suppress unused variable warning
var _ = syscall.SIGTERM
