package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecutionConfig holds configuration for container execution
type ExecutionConfig struct {
	ServiceID int64
	Inputs    []string          // STDIN inputs
	Outputs   []string          // STDOUT output paths
	Mounts    map[string]string // Host path -> Container path
	EnvVars   map[string]string // Environment variables
	Timeout   time.Duration     // Execution timeout (0 = no timeout)
}

// ExecutionResult holds the results of container execution
type ExecutionResult struct {
	ContainerID string
	ExitCode    int
	Stdout      string
	Stderr      string
	Error       error
	Duration    time.Duration
}

// ExecuteService runs a Docker service with the specified configuration
func (ds *DockerService) ExecuteService(config *ExecutionConfig) (*ExecutionResult, error) {
	ds.logger.Info(fmt.Sprintf("Executing Docker service ID: %d", config.ServiceID), "docker")

	result := &ExecutionResult{}

	// Check dependencies
	if err := ds.checkDependencies(); err != nil {
		result.Error = fmt.Errorf("Docker dependencies not met: %w", err)
		return result, result.Error
	}

	// Get service details
	service, err := ds.dbManager.GetService(config.ServiceID)
	if err != nil {
		result.Error = fmt.Errorf("failed to get service: %w", err)
		return result, result.Error
	}

	if service == nil {
		result.Error = fmt.Errorf("service not found: %d", config.ServiceID)
		return result, result.Error
	}

	dockerDetails, err := ds.dbManager.GetDockerServiceDetails(config.ServiceID)
	if err != nil {
		result.Error = fmt.Errorf("failed to get Docker service details: %w", err)
		return result, result.Error
	}

	if dockerDetails == nil {
		result.Error = fmt.Errorf("Docker service details not found: %d", config.ServiceID)
		return result, result.Error
	}

	// Get service interfaces
	interfaces, err := ds.dbManager.GetServiceInterfaces(config.ServiceID)
	if err != nil {
		result.Error = fmt.Errorf("failed to get service interfaces: %w", err)
		return result, result.Error
	}

	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		result.Error = fmt.Errorf("failed to create Docker client: %w", err)
		return result, result.Error
	}
	defer cli.Close()

	// Execute based on service source
	if dockerDetails.ComposePath != "" {
		// Execute docker-compose stack
		return ds.executeComposeService(cli, dockerDetails, config, interfaces)
	}

	// Execute single container
	return ds.executeSingleContainer(cli, dockerDetails, config, interfaces)
}

// executeSingleContainer runs a single Docker container
func (ds *DockerService) executeSingleContainer(
	cli *client.Client,
	details *database.DockerServiceDetails,
	config *ExecutionConfig,
	interfaces []*database.ServiceInterface,
) (*ExecutionResult, error) {
	result := &ExecutionResult{}
	startTime := time.Now()

	imageName := fmt.Sprintf("%s:%s", details.ImageName, details.ImageTag)

	// Ensure image exists
	exists, err := ds.imageExistsLocally(cli, imageName)
	if err != nil {
		result.Error = fmt.Errorf("failed to check image: %w", err)
		return result, result.Error
	}

	if !exists {
		result.Error = fmt.Errorf("image not found locally: %s", imageName)
		return result, result.Error
	}

	// Build container configuration
	containerConfig, hostConfig, networkConfig := ds.buildContainerConfig(imageName, config, interfaces)

	// Create context with timeout
	ctx := context.Background()
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}

	// Create container
	ds.logger.Info(fmt.Sprintf("Creating container from image: %s", imageName), "docker")
	resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, "")
	if err != nil {
		result.Error = fmt.Errorf("failed to create container: %w", err)
		return result, result.Error
	}

	result.ContainerID = resp.ID

	// Ensure cleanup
	defer func() {
		// Remove container after execution
		removeCtx := context.Background()
		if err := cli.ContainerRemove(removeCtx, resp.ID, container.RemoveOptions{Force: true}); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to remove container %s: %v", resp.ID, err), "docker")
		}
	}()

	// Attach to container for I/O
	attachResp, err := cli.ContainerAttach(ctx, resp.ID, container.AttachOptions{
		Stream: true,
		Stdin:  len(config.Inputs) > 0,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to attach to container: %w", err)
		return result, result.Error
	}
	defer attachResp.Close()

	// Start container
	ds.logger.Info(fmt.Sprintf("Starting container: %s", resp.ID), "docker")
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		result.Error = fmt.Errorf("failed to start container: %w", err)
		return result, result.Error
	}

	// Write inputs if any
	if len(config.Inputs) > 0 {
		for _, input := range config.Inputs {
			if _, err := attachResp.Conn.Write([]byte(input + "\n")); err != nil {
				ds.logger.Warn(fmt.Sprintf("Failed to write input: %v", err), "docker")
			}
		}
		attachResp.CloseWrite()
	}

	// Capture outputs
	stdoutBuf := new(strings.Builder)
	stderrBuf := new(strings.Builder)

	// Use stdcopy to demultiplex stdout and stderr
	go func() {
		_, err := stdcopy.StdCopy(stdoutBuf, stderrBuf, attachResp.Reader)
		if err != nil && err != io.EOF {
			ds.logger.Warn(fmt.Sprintf("Error reading container output: %v", err), "docker")
		}
	}()

	// Wait for container to finish
	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			result.Error = fmt.Errorf("error waiting for container: %w", err)
			return result, result.Error
		}
	case status := <-statusCh:
		result.ExitCode = int(status.StatusCode)
		ds.logger.Info(fmt.Sprintf("Container exited with code: %d", status.StatusCode), "docker")
	case <-ctx.Done():
		result.Error = fmt.Errorf("container execution timeout")
		return result, result.Error
	}

	// Get outputs
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	result.Duration = time.Since(startTime)

	// Write outputs to files if requested
	for _, outputPath := range config.Outputs {
		outputFile := filepath.Join(ds.appPaths.DataDir, "job_outputs", outputPath)
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to create output directory: %v", err), "docker")
			continue
		}
		if err := os.WriteFile(outputFile, []byte(result.Stdout), 0644); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to write output file: %v", err), "docker")
		}
	}

	ds.logger.Info(fmt.Sprintf("Container execution completed in %v", result.Duration), "docker")
	return result, nil
}

// executeComposeService runs a docker-compose stack
func (ds *DockerService) executeComposeService(
	cli *client.Client,
	details *database.DockerServiceDetails,
	config *ExecutionConfig,
	interfaces []*database.ServiceInterface,
) (*ExecutionResult, error) {
	result := &ExecutionResult{}
	startTime := time.Now()

	// Parse compose file
	project, err := ds.parseCompose(details.ComposePath, "")
	if err != nil {
		result.Error = fmt.Errorf("failed to parse compose file: %w", err)
		return result, result.Error
	}

	ds.logger.Info(fmt.Sprintf("Executing docker-compose with %d services", len(project.Services)), "docker")

	// For compose, we'll execute the first service (or all services)
	// This is simplified - full compose orchestration would require more work
	if len(project.Services) == 0 {
		result.Error = fmt.Errorf("no services defined in compose file")
		return result, result.Error
	}

	// Execute first service for now
	svc := project.Services[0]
	imageName := svc.Image
	if imageName == "" {
		imageName = fmt.Sprintf("%s-%s:latest", project.Name, svc.Name)
	}

	// Update details to use the correct image
	tempDetails := &database.DockerServiceDetails{
		ImageName: strings.Split(imageName, ":")[0],
		ImageTag:  "latest",
	}
	if strings.Contains(imageName, ":") {
		parts := strings.Split(imageName, ":")
		tempDetails.ImageName = parts[0]
		tempDetails.ImageTag = parts[1]
	}

	// Execute as single container
	result, err = ds.executeSingleContainer(cli, tempDetails, config, interfaces)
	result.Duration = time.Since(startTime)

	return result, err
}

// buildContainerConfig builds container, host, and network configurations
func (ds *DockerService) buildContainerConfig(
	imageName string,
	config *ExecutionConfig,
	interfaces []*database.ServiceInterface,
) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	// Container config
	containerConfig := &container.Config{
		Image:        imageName,
		AttachStdin:  len(config.Inputs) > 0,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
		OpenStdin:    len(config.Inputs) > 0,
		StdinOnce:    true,
	}

	// Add environment variables
	if config.EnvVars != nil {
		env := []string{}
		for k, v := range config.EnvVars {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		containerConfig.Env = env
	}

	// Host config - mounts and resources
	hostConfig := &container.HostConfig{
		AutoRemove: false, // We'll remove manually for better control
	}

	// Add mounts from config
	var mounts []mount.Mount
	for hostPath, containerPath := range config.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: hostPath,
			Target: containerPath,
		})
	}

	// Add mounts from interfaces
	for _, iface := range interfaces {
		if iface.InterfaceType == "MOUNT" && iface.Path != "" {
			// Create mount point on host
			hostPath := filepath.Join(ds.appPaths.DataDir, "mounts", fmt.Sprintf("service_%d", config.ServiceID), filepath.Base(iface.Path))
			if err := os.MkdirAll(hostPath, 0755); err != nil {
				ds.logger.Warn(fmt.Sprintf("Failed to create mount directory: %v", err), "docker")
				continue
			}

			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: hostPath,
				Target: iface.Path,
			})
		}
	}

	hostConfig.Mounts = mounts

	// Set user for non-Windows platforms
	if runtime.GOOS != "windows" {
		hostConfig.UsernsMode = "host"
	}

	// Network config
	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: make(map[string]*network.EndpointSettings),
	}

	return containerConfig, hostConfig, networkConfig
}
