package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/dependencies"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	composetypes "github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// sanitizeImageName converts a service name to a valid Docker image name
// Docker image names must be lowercase and can only contain [a-z0-9-_.]
func sanitizeImageName(name string) string {
	// 1. Convert to lowercase (REQUIRED by Docker)
	name = strings.ToLower(name)

	// 2. Replace spaces and invalid characters with hyphens
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, name)

	// 3. Remove leading/trailing separators
	name = strings.Trim(name, "-_.")

	// 4. Collapse multiple consecutive hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	return name
}

// secureRandomString generates a cryptographically secure random hex string
func (ds *DockerService) secureRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

// generateContainerName creates a unique container name for job execution
// Format: job_<execution_id>_<service_name>_<random>
func (ds *DockerService) generateContainerName(jobExecutionID int64, serviceName string) string {
	// Sanitize service name for container name
	base := sanitizeImageName(serviceName)
	// Create random suffix for uniqueness
	rnd, err := ds.secureRandomString(6)
	if err != nil {
		// Fallback to timestamp-based suffix if random generation fails
		rnd = fmt.Sprintf("%d", jobExecutionID%1000000)
	}
	// Format: job_<id>_<service>_<random>
	return fmt.Sprintf("job_%d_%s_%s", jobExecutionID, base, rnd)
}

// SuggestedInterface represents an auto-detected interface suggestion
type SuggestedInterface struct {
	InterfaceType string `json:"interface_type"` // STDIN, STDOUT, STDERR, LOGS, MOUNT
	Path          string `json:"path"`           // Optional path for MOUNT interfaces
	MountFunction string `json:"mount_function"` // INPUT, OUTPUT, BOTH (for MOUNT interfaces)
	Description   string `json:"description"`    // Human-readable description
}

// EventBroadcaster interface for broadcasting Docker operation events
type EventBroadcaster interface {
	BroadcastDockerPullProgress(serviceName, imageName, status, progress string, progressDetail map[string]interface{})
	BroadcastDockerBuildOutput(serviceName, imageName, stream, errorMsg string, errorDetail map[string]interface{})
}

// DockerService handles Docker service operations
type DockerService struct {
	dbManager        *database.SQLiteManager
	logger           *utils.LogsManager
	configMgr        *utils.ConfigManager
	appPaths         *utils.AppPaths
	depManager       *dependencies.DependencyManager
	gitService       *GitService
	eventBroadcaster EventBroadcaster // Optional event broadcaster for real-time updates
}

// NewDockerService creates a new DockerService instance
func NewDockerService(
	db *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	paths *utils.AppPaths,
	depMgr *dependencies.DependencyManager,
	gitSvc *GitService,
) *DockerService {
	return &DockerService{
		dbManager:  db,
		logger:     logger,
		configMgr:  config,
		appPaths:   paths,
		depManager: depMgr,
		gitService: gitSvc,
	}
}

// SetEventBroadcaster sets the event broadcaster for real-time Docker operation updates
func (ds *DockerService) SetEventBroadcaster(broadcaster EventBroadcaster) {
	ds.eventBroadcaster = broadcaster
}

// CreateFromRegistry creates a Docker service from a registry image
func (ds *DockerService) CreateFromRegistry(
	serviceName, imageName, imageTag, description string,
	username, password string,
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ds.logger.Info(fmt.Sprintf("Creating Docker service from registry: %s:%s", imageName, imageTag), "docker")

	// Check Docker dependencies
	if err := ds.checkDependencies(); err != nil {
		return nil, nil, fmt.Errorf("docker dependencies not met: %w", err)
	}

	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		ds.logger.Error(fmt.Sprintf("Failed to create Docker client: %v", err), "docker")
		return nil, nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Construct full image name
	fullImageName := imageName
	if imageTag != "" {
		fullImageName = imageName + ":" + imageTag
	} else {
		fullImageName = imageName + ":latest"
		imageTag = "latest"
	}

	// Pull the image
	if err := ds.pullImage(cli, serviceName, fullImageName, username, password); err != nil {
		return nil, nil, fmt.Errorf("failed to pull image: %w", err)
	}

	// Auto-detect interfaces from image metadata
	suggestedInterfaces, entrypoint, cmd, err := ds.detectInterfacesFromImage(cli, fullImageName)
	if err != nil {
		ds.logger.Warn(fmt.Sprintf("Failed to auto-detect interfaces: %v", err), "docker")
		// Continue anyway, user can define manually
	}

	// Convert entrypoint and cmd to JSON strings for storage
	entrypointJSON, _ := json.Marshal(entrypoint)
	cmdJSON, _ := json.Marshal(cmd)

	// Create service record
	service := &database.OfferedService{
		ServiceType: "DOCKER",
		Type:        "docker",
		Name:        serviceName,
		Description: description,
		Status:      "ACTIVE",
		Capabilities: map[string]interface{}{
			"source":     "registry",
			"image_name": fullImageName,
		},
		PricingAmount:   0.0,
		PricingType:     "ONE_TIME",
		PricingInterval: 1,
		PricingUnit:     "MONTHS",
	}

	if err := ds.dbManager.AddService(service); err != nil {
		return nil, nil, fmt.Errorf("failed to create service record: %w", err)
	}

	// Add Docker service details
	details := &database.DockerServiceDetails{
		ServiceID:  service.ID,
		ImageName:  imageName,
		ImageTag:   imageTag,
		Source:     "registry",
		Entrypoint: string(entrypointJSON),
		Cmd:        string(cmdJSON),
	}

	if err := ds.dbManager.AddDockerServiceDetails(details); err != nil {
		// Rollback service creation
		_ = ds.dbManager.DeleteService(service.ID)
		return nil, nil, fmt.Errorf("failed to save Docker service details: %w", err)
	}

	// Add custom interfaces if provided, otherwise use suggested
	interfacesToAdd := customInterfaces
	if len(interfacesToAdd) == 0 && len(suggestedInterfaces) > 0 {
		// Convert suggestions to actual interfaces
		for _, suggested := range suggestedInterfaces {
			interfacesToAdd = append(interfacesToAdd, &database.ServiceInterface{
				ServiceID:     service.ID,
				InterfaceType: suggested.InterfaceType,
				Path:          suggested.Path,
				MountFunction: suggested.MountFunction,
				Description:   suggested.Description,
			})
		}
	}

	for _, iface := range interfacesToAdd {
		iface.ServiceID = service.ID
		if err := ds.dbManager.AddServiceInterface(iface); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to add service interface: %v", err), "docker")
		}
	}

	ds.logger.Info(fmt.Sprintf("Docker service created successfully: %s (ID: %d)", serviceName, service.ID), "docker")
	return service, suggestedInterfaces, nil
}

// CreateFromGitRepo creates a Docker service from a Git repository
func (ds *DockerService) CreateFromGitRepo(
	serviceName, repoURL, branch, username, password, description string,
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ds.logger.Info(fmt.Sprintf("Creating Docker service from Git repo: %s (branch: %s)", repoURL, branch), "docker")

	// Check Docker dependencies
	if err := ds.checkDependencies(); err != nil {
		return nil, nil, fmt.Errorf("docker dependencies not met: %w", err)
	}

	// Clone or pull repository
	repoPath, err := ds.gitService.CloneOrPull(repoURL, branch, username, password)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	// Check for Docker files
	dockerFiles, err := ds.gitService.CheckDockerFiles(repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to scan for Docker files: %w", err)
	}

	if !dockerFiles.HasDockerfile && !dockerFiles.HasCompose {
		return nil, nil, fmt.Errorf("no Dockerfile or docker-compose.yml found in repository")
	}

	// Get current commit hash
	commitHash, err := ds.gitService.GetCurrentCommitHash(repoPath)
	if err != nil {
		ds.logger.Warn(fmt.Sprintf("Failed to get commit hash: %v", err), "docker")
		commitHash = "unknown"
	}

	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		ds.logger.Error(fmt.Sprintf("Failed to create Docker client: %v", err), "docker")
		return nil, nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	var imageName string
	var dockerfilePath, composePath string
	var suggestedInterfaces []SuggestedInterface
	var entrypoint, cmd []string
	var entrypointJSON, cmdJSON []byte

	// Prefer docker-compose if available
	if dockerFiles.HasCompose {
		composePath = dockerFiles.Composes[0]
		ds.logger.Info(fmt.Sprintf("Building from docker-compose: %s", composePath), "docker")

		// Parse compose file
		project, err := ds.parseCompose(composePath, "")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse docker-compose: %w", err)
		}

		// Build all services in compose
		for _, svc := range project.Services {
			if svc.Build != nil {
				svcImageName := svc.Image
				if svcImageName == "" {
					sanitizedName := sanitizeImageName(serviceName)
					svcImageName = fmt.Sprintf("%s-%s:latest", sanitizedName, svc.Name)
				}

				if err := ds.buildImage(cli, serviceName, svc.Build.Context, svcImageName, svc.Build.Dockerfile); err != nil {
					return nil, nil, fmt.Errorf("failed to build service %s: %w", svc.Name, err)
				}

				// Use first service image as primary
				if imageName == "" {
					imageName = svcImageName
				}
			}
		}

		// Auto-detect interfaces from compose
		suggestedInterfaces = ds.detectInterfacesFromCompose(project)
	} else {
		// Build from Dockerfile
		dockerfilePath = dockerFiles.Dockerfiles[0]
		contextDir := filepath.Dir(dockerfilePath)
		sanitizedName := sanitizeImageName(serviceName)
		imageName = fmt.Sprintf("%s:latest", sanitizedName)

		ds.logger.Info(fmt.Sprintf("Building from Dockerfile: %s", dockerfilePath), "docker")

		if err := ds.buildImage(cli, serviceName, contextDir, imageName, "Dockerfile"); err != nil {
			return nil, nil, fmt.Errorf("failed to build image: %w", err)
		}

		// Auto-detect interfaces from image
		suggestedInterfaces, entrypoint, cmd, err = ds.detectInterfacesFromImage(cli, imageName)
		if err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to auto-detect interfaces: %v", err), "docker")
		}

		// Convert entrypoint and cmd to JSON strings for storage
		entrypointJSON, _ = json.Marshal(entrypoint)
		cmdJSON, _ = json.Marshal(cmd)
	}

	// Create service record
	service := &database.OfferedService{
		ServiceType: "DOCKER",
		Type:        "docker",
		Name:        serviceName,
		Description: description,
		Status:      "ACTIVE",
		Capabilities: map[string]interface{}{
			"source":      "git",
			"repo_url":    repoURL,
			"branch":      branch,
			"commit_hash": commitHash,
			"image_name":  imageName,
		},
		PricingAmount:   0.0,
		PricingType:     "ONE_TIME",
		PricingInterval: 1,
		PricingUnit:     "MONTHS",
	}

	if err := ds.dbManager.AddService(service); err != nil {
		return nil, nil, fmt.Errorf("failed to create service record: %w", err)
	}

	// Extract image name and tag
	parts := strings.Split(imageName, ":")
	imgName := parts[0]
	imgTag := "latest"
	if len(parts) > 1 {
		imgTag = parts[1]
	}

	// Add Docker service details
	details := &database.DockerServiceDetails{
		ServiceID:      service.ID,
		ImageName:      imgName,
		ImageTag:       imgTag,
		DockerfilePath: dockerfilePath,
		ComposePath:    composePath,
		Source:         "git",
		GitRepoURL:     repoURL,
		GitCommitHash:  commitHash,
		Entrypoint:     string(entrypointJSON),
		Cmd:            string(cmdJSON),
	}

	if err := ds.dbManager.AddDockerServiceDetails(details); err != nil {
		_ = ds.dbManager.DeleteService(service.ID)
		return nil, nil, fmt.Errorf("failed to save Docker service details: %w", err)
	}

	// Add interfaces
	interfacesToAdd := customInterfaces
	if len(interfacesToAdd) == 0 && len(suggestedInterfaces) > 0 {
		for _, suggested := range suggestedInterfaces {
			interfacesToAdd = append(interfacesToAdd, &database.ServiceInterface{
				ServiceID:     service.ID,
				InterfaceType: suggested.InterfaceType,
				Path:          suggested.Path,
				MountFunction: suggested.MountFunction,
				Description:   suggested.Description,
			})
		}
	}

	for _, iface := range interfacesToAdd {
		iface.ServiceID = service.ID
		if err := ds.dbManager.AddServiceInterface(iface); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to add service interface: %v", err), "docker")
		}
	}

	ds.logger.Info(fmt.Sprintf("Docker service created from Git: %s (ID: %d)", serviceName, service.ID), "docker")
	return service, suggestedInterfaces, nil
}

// CreateFromLocalDirectory creates a Docker service from a local directory
func (ds *DockerService) CreateFromLocalDirectory(
	serviceName, localPath, description string,
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ds.logger.Info(fmt.Sprintf("Creating Docker service from local directory: %s", localPath), "docker")

	// Check Docker dependencies
	if err := ds.checkDependencies(); err != nil {
		return nil, nil, fmt.Errorf("docker dependencies not met: %w", err)
	}

	// Validate path exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("local path does not exist: %s", localPath)
	}

	// Check for Docker files
	dockerFiles, err := ds.gitService.CheckDockerFiles(localPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to scan for Docker files: %w", err)
	}

	if !dockerFiles.HasDockerfile && !dockerFiles.HasCompose {
		return nil, nil, fmt.Errorf("no Dockerfile or docker-compose.yml found in directory")
	}

	// Create Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		ds.logger.Error(fmt.Sprintf("Failed to create Docker client: %v", err), "docker")
		return nil, nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	var imageName string
	var dockerfilePath, composePath string
	var suggestedInterfaces []SuggestedInterface
	var entrypoint, cmd []string
	var entrypointJSON, cmdJSON []byte

	// Prefer docker-compose if available
	if dockerFiles.HasCompose {
		composePath = dockerFiles.Composes[0]
		ds.logger.Info(fmt.Sprintf("Building from docker-compose: %s", composePath), "docker")

		// Parse compose file
		project, err := ds.parseCompose(composePath, "")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse docker-compose: %w", err)
		}

		// Build all services
		for _, svc := range project.Services {
			if svc.Build != nil {
				svcImageName := svc.Image
				if svcImageName == "" {
					sanitizedName := sanitizeImageName(serviceName)
					svcImageName = fmt.Sprintf("%s-%s:latest", sanitizedName, svc.Name)
				}

				if err := ds.buildImage(cli, serviceName, svc.Build.Context, svcImageName, svc.Build.Dockerfile); err != nil {
					return nil, nil, fmt.Errorf("failed to build service %s: %w", svc.Name, err)
				}

				if imageName == "" {
					imageName = svcImageName
				}
			}
		}

		suggestedInterfaces = ds.detectInterfacesFromCompose(project)
	} else {
		// Build from Dockerfile
		dockerfilePath = dockerFiles.Dockerfiles[0]
		contextDir := filepath.Dir(dockerfilePath)
		sanitizedName := sanitizeImageName(serviceName)
		imageName = fmt.Sprintf("%s:latest", sanitizedName)

		ds.logger.Info(fmt.Sprintf("Building from Dockerfile: %s", dockerfilePath), "docker")

		if err := ds.buildImage(cli, serviceName, contextDir, imageName, "Dockerfile"); err != nil {
			return nil, nil, fmt.Errorf("failed to build image: %w", err)
		}

		suggestedInterfaces, entrypoint, cmd, err = ds.detectInterfacesFromImage(cli, imageName)
		if err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to auto-detect interfaces: %v", err), "docker")
		}

		// Convert entrypoint and cmd to JSON strings for storage
		entrypointJSON, _ = json.Marshal(entrypoint)
		cmdJSON, _ = json.Marshal(cmd)
	}

	// Copy Dockerfile/compose to service storage for persistence
	serviceStoragePath := filepath.Join(ds.appPaths.DataDir, "docker_services", fmt.Sprintf("service_%s", serviceName))
	if err := os.MkdirAll(serviceStoragePath, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create service storage: %w", err)
	}

	// Copy files
	if dockerfilePath != "" {
		destPath := filepath.Join(serviceStoragePath, "Dockerfile")
		if err := ds.copyFile(dockerfilePath, destPath); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to copy Dockerfile: %v", err), "docker")
		}
		dockerfilePath = destPath
	}
	if composePath != "" {
		destPath := filepath.Join(serviceStoragePath, "docker-compose.yml")
		if err := ds.copyFile(composePath, destPath); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to copy compose file: %v", err), "docker")
		}
		composePath = destPath
	}

	// Create service record
	service := &database.OfferedService{
		ServiceType: "DOCKER",
		Type:        "docker",
		Name:        serviceName,
		Description: description,
		Status:      "ACTIVE",
		Capabilities: map[string]interface{}{
			"source":     "local",
			"local_path": localPath,
			"image_name": imageName,
		},
		PricingAmount:   0.0,
		PricingType:     "ONE_TIME",
		PricingInterval: 1,
		PricingUnit:     "MONTHS",
	}

	if err := ds.dbManager.AddService(service); err != nil {
		return nil, nil, fmt.Errorf("failed to create service record: %w", err)
	}

	// Extract image name and tag
	parts := strings.Split(imageName, ":")
	imgName := parts[0]
	imgTag := "latest"
	if len(parts) > 1 {
		imgTag = parts[1]
	}

	// Add Docker service details
	details := &database.DockerServiceDetails{
		ServiceID:        service.ID,
		ImageName:        imgName,
		ImageTag:         imgTag,
		DockerfilePath:   dockerfilePath,
		ComposePath:      composePath,
		Source:           "local",
		LocalContextPath: localPath,
		Entrypoint:       string(entrypointJSON),
		Cmd:              string(cmdJSON),
	}

	if err := ds.dbManager.AddDockerServiceDetails(details); err != nil {
		_ = ds.dbManager.DeleteService(service.ID)
		return nil, nil, fmt.Errorf("failed to save Docker service details: %w", err)
	}

	// Add interfaces
	interfacesToAdd := customInterfaces
	if len(interfacesToAdd) == 0 && len(suggestedInterfaces) > 0 {
		for _, suggested := range suggestedInterfaces {
			interfacesToAdd = append(interfacesToAdd, &database.ServiceInterface{
				ServiceID:     service.ID,
				InterfaceType: suggested.InterfaceType,
				Path:          suggested.Path,
				MountFunction: suggested.MountFunction,
				Description:   suggested.Description,
			})
		}
	}

	for _, iface := range interfacesToAdd {
		iface.ServiceID = service.ID
		if err := ds.dbManager.AddServiceInterface(iface); err != nil {
			ds.logger.Warn(fmt.Sprintf("Failed to add service interface: %v", err), "docker")
		}
	}

	ds.logger.Info(fmt.Sprintf("Docker service created from local directory: %s (ID: %d)", serviceName, service.ID), "docker")
	return service, suggestedInterfaces, nil
}

// Helper methods

func (ds *DockerService) checkDependencies() error {
	missing := ds.depManager.GetMissingDependencies()
	if len(missing) > 0 {
		return fmt.Errorf("missing dependencies: %v", missing)
	}
	return nil
}

func (ds *DockerService) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// Public helper methods for API endpoints

// GetDockerClient creates and returns a Docker client
func (ds *DockerService) GetDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// DetectInterfacesFromImageName detects interfaces from a Docker image by name
// Returns suggested interfaces, entrypoint, cmd, and error
func (ds *DockerService) DetectInterfacesFromImageName(cli *client.Client, imageName string) ([]SuggestedInterface, []string, []string, error) {
	return ds.detectInterfacesFromImage(cli, imageName)
}

// ParseComposeFile parses a docker-compose.yml file
func (ds *DockerService) ParseComposeFile(composePath, envFile string) (*composetypes.Project, error) {
	return ds.parseCompose(composePath, envFile)
}

// DetectInterfacesFromComposeProject detects interfaces from a compose project
func (ds *DockerService) DetectInterfacesFromComposeProject(project *composetypes.Project) []SuggestedInterface {
	return ds.detectInterfacesFromCompose(project)
}

// RemoveImage removes a Docker image by name
func (ds *DockerService) RemoveImage(imageName string) error {
	if err := ds.checkDependencies(); err != nil {
		return err
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	ds.logger.Info(fmt.Sprintf("Removing Docker image: %s", imageName), "docker")

	ctx := context.Background()
	_, err = cli.ImageRemove(ctx, imageName, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})

	if err != nil {
		if client.IsErrNotFound(err) {
			ds.logger.Warn(fmt.Sprintf("Image not found: %s", imageName), "docker")
			return nil // Not an error if already removed
		}
		return fmt.Errorf("failed to remove image: %w", err)
	}

	ds.logger.Info(fmt.Sprintf("Docker image removed: %s", imageName), "docker")
	return nil
}
