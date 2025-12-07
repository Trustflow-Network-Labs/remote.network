package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// CreateFromUpload creates a standalone service from an uploaded executable
func (ss *StandaloneService) CreateFromUpload(
	serviceName string,
	uploadedFile io.Reader,
	filename string,
	args []string,
	workingDir string,
	envVars map[string]string,
	timeout int,
	runAsUser string,
	description string,
	capabilities map[string]interface{},
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ss.logger.Info(fmt.Sprintf("Creating standalone service from uploaded file: %s", filename), "standalone")

	// 1. Create upload directory: {dataDir}/standalone_services/{serviceName}/
	serviceDir := filepath.Join(ss.appPaths.DataDir, "standalone_services", serviceName)
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create service directory: %w", err)
	}

	// 2. Save uploaded file with secure permissions
	executablePath := filepath.Join(serviceDir, filename)
	outFile, err := os.OpenFile(executablePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create executable file: %w", err)
	}
	defer outFile.Close()

	// Copy uploaded content to file
	written, err := io.Copy(outFile, uploadedFile)
	if err != nil {
		// Clean up on error
		os.RemoveAll(serviceDir)
		return nil, nil, fmt.Errorf("failed to write uploaded file: %w", err)
	}

	ss.logger.Info(fmt.Sprintf("Saved uploaded file: %s (%d bytes)", executablePath, written), "standalone")

	// 3. Calculate SHA256 hash
	hash, err := ss.calculateFileHash(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to calculate executable hash: %v", err), "standalone")
		hash = "" // Continue without hash
	}

	// 4. Validate file (detect platform, check if executable)
	if err := ss.validateExecutable(executablePath); err != nil {
		// Try to set executable permissions if validation failed
		if setErr := ss.setExecutablePermissions(executablePath); setErr != nil {
			os.RemoveAll(serviceDir)
			return nil, nil, fmt.Errorf("invalid executable and failed to set permissions: %w", err)
		}
		// Retry validation
		if err := ss.validateExecutable(executablePath); err != nil {
			os.RemoveAll(serviceDir)
			return nil, nil, fmt.Errorf("invalid executable after setting permissions: %w", err)
		}
	}

	// 5. Detect platform if not provided in capabilities
	if capabilities == nil {
		capabilities = make(map[string]interface{})
	}

	// Detect platform and architecture
	osName, arch, err := ss.detectExecutablePlatform(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to detect platform: %v", err), "standalone")
	} else {
		if _, hasOS := capabilities["platform"]; !hasOS {
			capabilities["platform"] = osName
		}
		if _, hasArch := capabilities["architectures"]; !hasArch {
			capabilities["architectures"] = []string{arch}
		}
	}

	// Add executable hash and path to capabilities
	if hash != "" {
		capabilities["executable_hash"] = hash
	}
	capabilities["executable_path"] = executablePath
	capabilities["source"] = "upload"

	// Set default timeout if not specified
	if timeout == 0 {
		timeout = 600 // Default 10 minutes
	}

	// 6. Create service record
	service := &database.OfferedService{
		ServiceType:  "STANDALONE",
		Type:         "standalone",
		Name:         serviceName,
		Description:  description,
		Capabilities: capabilities,
		Status:       "ACTIVE",
	}

	if err := ss.dbManager.AddService(service); err != nil {
		os.RemoveAll(serviceDir)
		ss.logger.Error(fmt.Sprintf("Failed to create service: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create service: %w", err)
	}

	// 7. Create standalone_service_details record with hash
	argsJSON, err := database.MarshalStringSlice(args)
	if err != nil {
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		return nil, nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	envVarsJSON, err := database.MarshalStringMap(envVars)
	if err != nil {
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		return nil, nil, fmt.Errorf("failed to marshal environment variables: %w", err)
	}

	details := &database.StandaloneServiceDetails{
		ServiceID:            service.ID,
		ExecutablePath:       executablePath,
		Arguments:            argsJSON,
		WorkingDirectory:     workingDir,
		EnvironmentVariables: envVarsJSON,
		TimeoutSeconds:       timeout,
		RunAsUser:            runAsUser,
		Source:               "upload",
		UploadHash:           hash,
	}

	if err := ss.dbManager.AddStandaloneServiceDetails(details); err != nil {
		// Rollback service creation
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		ss.logger.Error(fmt.Sprintf("Failed to create standalone service details: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create standalone service details: %w", err)
	}

	// 8. Suggest default interfaces
	suggestedInterfaces := ss.suggestDefaultInterfaces()

	// 9. Add custom interfaces
	for _, iface := range customInterfaces {
		iface.ServiceID = service.ID
		if err := ss.dbManager.AddServiceInterface(iface); err != nil {
			ss.logger.Warn(fmt.Sprintf("Failed to add custom interface: %v", err), "standalone")
		}
	}

	// 10. Load interfaces for response
	interfaces, err := ss.dbManager.GetServiceInterfaces(service.ID)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to load service interfaces: %v", err), "standalone")
	}
	service.Interfaces = interfaces

	// 11. Broadcast service update if broadcaster is set
	if ss.eventBroadcaster != nil {
		// TODO: Add broadcast method for standalone services
	}

	ss.logger.Info(fmt.Sprintf("Successfully created standalone service from upload: %s (ID: %d)", serviceName, service.ID), "standalone")

	return service, suggestedInterfaces, nil
}

// CreateFromGit creates a standalone service from a Git repository
func (ss *StandaloneService) CreateFromGit(
	serviceName string,
	repoURL string,
	branch string,
	executableRelativePath string, // Path within repo to executable
	buildCommand string,            // Optional: e.g., "make" or "go build"
	args []string,
	workingDir string,
	envVars map[string]string,
	timeout int,
	runAsUser string,
	username string,
	password string,
	description string,
	capabilities map[string]interface{},
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ss.logger.Info(fmt.Sprintf("Creating standalone service from Git repository: %s", repoURL), "standalone")

	// 1. Create service directory: {dataDir}/standalone_services/{serviceName}/
	serviceDir := filepath.Join(ss.appPaths.DataDir, "standalone_services", serviceName)
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create service directory: %w", err)
	}

	// 2. Clone repository using GitService
	// CloneOrPull returns the path to the cloned repository
	repoDir, err := ss.gitService.CloneOrPull(repoURL, branch, username, password)
	if err != nil {
		os.RemoveAll(serviceDir)
		ss.logger.Error(fmt.Sprintf("Failed to clone repository: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	ss.logger.Info(fmt.Sprintf("Cloned repository to: %s", repoDir), "standalone")

	// Get commit hash for reproducibility
	commitHash, err := ss.gitService.GetCurrentCommitHash(repoDir)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to get commit hash: %v", err), "standalone")
		commitHash = "unknown"
	}

	// 3. If buildCommand provided, execute build
	if buildCommand != "" {
		ss.logger.Info(fmt.Sprintf("Executing build command: %s", buildCommand), "standalone")
		if err := ss.executeBuildCommand(repoDir, buildCommand); err != nil {
			os.RemoveAll(serviceDir)
			return nil, nil, fmt.Errorf("build failed: %w", err)
		}
		ss.logger.Info("Build completed successfully", "standalone")
	}

	// 4. Validate executable exists at specified path
	executablePath := filepath.Join(repoDir, executableRelativePath)
	if err := ss.validateExecutable(executablePath); err != nil {
		// Try to set executable permissions
		if setErr := ss.setExecutablePermissions(executablePath); setErr != nil {
			os.RemoveAll(serviceDir)
			return nil, nil, fmt.Errorf("invalid executable at %s: %w", executableRelativePath, err)
		}
		// Retry validation
		if err := ss.validateExecutable(executablePath); err != nil {
			os.RemoveAll(serviceDir)
			return nil, nil, fmt.Errorf("invalid executable at %s after setting permissions: %w", executableRelativePath, err)
		}
	}

	// 5. Calculate executable hash
	hash, err := ss.calculateFileHash(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to calculate executable hash: %v", err), "standalone")
		hash = ""
	}

	// 6. Detect platform if not provided
	if capabilities == nil {
		capabilities = make(map[string]interface{})
	}

	osName, arch, err := ss.detectExecutablePlatform(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to detect platform: %v", err), "standalone")
	} else {
		if _, hasOS := capabilities["platform"]; !hasOS {
			capabilities["platform"] = osName
		}
		if _, hasArch := capabilities["architectures"]; !hasArch {
			capabilities["architectures"] = []string{arch}
		}
	}

	// Add metadata to capabilities
	if hash != "" {
		capabilities["executable_hash"] = hash
	}
	capabilities["executable_path"] = executablePath
	capabilities["source"] = "git"
	capabilities["git_repo_url"] = repoURL
	capabilities["git_commit_hash"] = commitHash

	// Set default timeout if not specified
	if timeout == 0 {
		timeout = 600 // Default 10 minutes
	}

	// 7. Create service record
	service := &database.OfferedService{
		ServiceType:  "STANDALONE",
		Type:         "standalone",
		Name:         serviceName,
		Description:  description,
		Capabilities: capabilities,
		Status:       "ACTIVE",
	}

	if err := ss.dbManager.AddService(service); err != nil {
		os.RemoveAll(serviceDir)
		ss.logger.Error(fmt.Sprintf("Failed to create service: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create service: %w", err)
	}

	// 8. Create standalone_service_details record
	argsJSON, err := database.MarshalStringSlice(args)
	if err != nil {
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		return nil, nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	envVarsJSON, err := database.MarshalStringMap(envVars)
	if err != nil {
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		return nil, nil, fmt.Errorf("failed to marshal environment variables: %w", err)
	}

	details := &database.StandaloneServiceDetails{
		ServiceID:            service.ID,
		ExecutablePath:       executablePath,
		Arguments:            argsJSON,
		WorkingDirectory:     workingDir,
		EnvironmentVariables: envVarsJSON,
		TimeoutSeconds:       timeout,
		RunAsUser:            runAsUser,
		Source:               "git",
		GitRepoURL:           repoURL,
		GitCommitHash:        commitHash,
	}

	if err := ss.dbManager.AddStandaloneServiceDetails(details); err != nil {
		ss.dbManager.DeleteService(service.ID)
		os.RemoveAll(serviceDir)
		ss.logger.Error(fmt.Sprintf("Failed to create standalone service details: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create standalone service details: %w", err)
	}

	// 9. Suggest default interfaces
	suggestedInterfaces := ss.suggestDefaultInterfaces()

	// 10. Add custom interfaces
	for _, iface := range customInterfaces {
		iface.ServiceID = service.ID
		if err := ss.dbManager.AddServiceInterface(iface); err != nil {
			ss.logger.Warn(fmt.Sprintf("Failed to add custom interface: %v", err), "standalone")
		}
	}

	// 11. Load interfaces for response
	interfaces, err := ss.dbManager.GetServiceInterfaces(service.ID)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to load service interfaces: %v", err), "standalone")
	}
	service.Interfaces = interfaces

	// 12. Broadcast service update if broadcaster is set
	if ss.eventBroadcaster != nil {
		// TODO: Add broadcast method for standalone services
	}

	ss.logger.Info(fmt.Sprintf("Successfully created standalone service from Git: %s (ID: %d)", serviceName, service.ID), "standalone")

	return service, suggestedInterfaces, nil
}

// FinalizeUploadedService finalizes an uploaded standalone service with configuration
// This is called after WebSocket upload completes to add execution configuration
func (ss *StandaloneService) FinalizeUploadedService(
	serviceID int64,
	executablePath string,
	args []string,
	workingDir string,
	envVars map[string]string,
	timeout int,
	runAsUser string,
	capabilities map[string]interface{},
	customInterfaces []*database.ServiceInterface,
) ([]SuggestedInterface, error) {
	ss.logger.Info(fmt.Sprintf("Finalizing uploaded standalone service: %d", serviceID), "standalone")

	// 1. Validate executable exists and is executable
	if err := ss.validateExecutable(executablePath); err != nil {
		// Try to set executable permissions
		if setErr := ss.setExecutablePermissions(executablePath); setErr != nil {
			return nil, fmt.Errorf("invalid executable and failed to set permissions: %w", err)
		}
		// Retry validation
		if err := ss.validateExecutable(executablePath); err != nil {
			return nil, fmt.Errorf("invalid executable after setting permissions: %w", err)
		}
	}

	// 2. Calculate executable hash
	hash, err := ss.calculateFileHash(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to calculate executable hash: %v", err), "standalone")
		hash = ""
	}

	// 3. Detect platform if not provided
	if capabilities == nil {
		capabilities = make(map[string]interface{})
	}

	osName, arch, err := ss.detectExecutablePlatform(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to detect platform: %v", err), "standalone")
	} else {
		if _, hasOS := capabilities["platform"]; !hasOS {
			capabilities["platform"] = osName
		}
		if _, hasArch := capabilities["architectures"]; !hasArch {
			capabilities["architectures"] = []string{arch}
		}
	}

	// Add metadata to capabilities
	if hash != "" {
		capabilities["executable_hash"] = hash
	}
	capabilities["executable_path"] = executablePath
	capabilities["source"] = "upload"

	// Set default timeout if not specified
	if timeout == 0 {
		timeout = 600 // Default 10 minutes
	}

	// 4. Update service capabilities
	service, err := ss.dbManager.GetService(serviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	service.Capabilities = capabilities
	if err := ss.dbManager.UpdateService(service); err != nil {
		return nil, fmt.Errorf("failed to update service capabilities: %w", err)
	}

	// 5. Create standalone_service_details record
	argsJSON, err := database.MarshalStringSlice(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	envVarsJSON, err := database.MarshalStringMap(envVars)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal environment variables: %w", err)
	}

	details := &database.StandaloneServiceDetails{
		ServiceID:            serviceID,
		ExecutablePath:       executablePath,
		Arguments:            argsJSON,
		WorkingDirectory:     workingDir,
		EnvironmentVariables: envVarsJSON,
		TimeoutSeconds:       timeout,
		RunAsUser:            runAsUser,
		Source:               "upload",
		UploadHash:           hash,
	}

	if err := ss.dbManager.AddStandaloneServiceDetails(details); err != nil {
		return nil, fmt.Errorf("failed to create standalone service details: %w", err)
	}

	// 6. Suggest default interfaces
	suggestedInterfaces := ss.suggestDefaultInterfaces()

	// 7. Add custom interfaces
	for _, iface := range customInterfaces {
		iface.ServiceID = serviceID
		if err := ss.dbManager.AddServiceInterface(iface); err != nil {
			ss.logger.Warn(fmt.Sprintf("Failed to add custom interface: %v", err), "standalone")
		}
	}

	// 8. Update service status to ACTIVE
	service.Status = "ACTIVE"
	if err := ss.dbManager.UpdateService(service); err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to update service status: %v", err), "standalone")
	}

	ss.logger.Info(fmt.Sprintf("Successfully finalized uploaded standalone service: %d", serviceID), "standalone")

	return suggestedInterfaces, nil
}

// executeBuildCommand executes a build command in the specified directory
func (ss *StandaloneService) executeBuildCommand(workDir string, command string) error {
	// Use the execution service to run the build command
	config := &StandaloneExecutionConfig{
		Executable: "/bin/sh",
		Arguments:  []string{"-c", command},
		WorkingDir: workDir,
		Timeout:    5 * 60 * 1000 * 1000 * 1000, // 5 minutes in nanoseconds
	}

	result := ss.ExecuteService(config)

	if result.Error != nil {
		return fmt.Errorf("build command failed: %w", result.Error)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("build command exited with code %d: %s", result.ExitCode, result.Stderr)
	}

	return nil
}
