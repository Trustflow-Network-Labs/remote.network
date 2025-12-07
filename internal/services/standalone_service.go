package services

import (
	"fmt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// StandaloneService handles standalone executable service operations
type StandaloneService struct {
	dbManager        *database.SQLiteManager
	logger           *utils.LogsManager
	configMgr        *utils.ConfigManager
	appPaths         *utils.AppPaths
	gitService       *GitService
	eventBroadcaster EventBroadcaster
}

// NewStandaloneService creates a new StandaloneService instance
func NewStandaloneService(
	db *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	paths *utils.AppPaths,
	gitSvc *GitService,
) *StandaloneService {
	return &StandaloneService{
		dbManager:  db,
		logger:     logger,
		configMgr:  config,
		appPaths:   paths,
		gitService: gitSvc,
	}
}

// SetEventBroadcaster sets the event broadcaster for real-time operation updates
func (ss *StandaloneService) SetEventBroadcaster(broadcaster EventBroadcaster) {
	ss.eventBroadcaster = broadcaster
}

// CreateFromLocal creates a standalone service from an existing executable on the filesystem
func (ss *StandaloneService) CreateFromLocal(
	serviceName string,
	executablePath string,
	args []string,
	workingDir string,
	envVars map[string]string,
	timeout int,
	runAsUser string,
	description string,
	capabilities map[string]interface{},
	customInterfaces []*database.ServiceInterface,
) (*database.OfferedService, []SuggestedInterface, error) {
	ss.logger.Info(fmt.Sprintf("Creating standalone service from local executable: %s", executablePath), "standalone")

	// 1. Validate executable exists and is executable
	if err := ss.validateExecutable(executablePath); err != nil {
		return nil, nil, fmt.Errorf("invalid executable: %w", err)
	}

	// 2. Calculate executable hash for verification
	hash, err := ss.calculateFileHash(executablePath)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to calculate executable hash: %v", err), "standalone")
		hash = "" // Continue without hash
	}

	// 3. Detect platform compatibility if not provided
	if capabilities == nil {
		capabilities = make(map[string]interface{})
	}

	// Add executable hash to capabilities if available
	if hash != "" {
		capabilities["executable_hash"] = hash
	}
	capabilities["executable_path"] = executablePath
	capabilities["source"] = "local"

	// Set default timeout if not specified
	if timeout == 0 {
		timeout = 600 // Default 10 minutes
	}

	// 4. Create service record
	service := &database.OfferedService{
		ServiceType:  "STANDALONE",
		Type:         "standalone",
		Name:         serviceName,
		Description:  description,
		Capabilities: capabilities,
		Status:       "ACTIVE",
	}

	if err := ss.dbManager.AddService(service); err != nil {
		ss.logger.Error(fmt.Sprintf("Failed to create service: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create service: %w", err)
	}

	// 5. Create standalone_service_details record
	argsJSON, err := database.MarshalStringSlice(args)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	envVarsJSON, err := database.MarshalStringMap(envVars)
	if err != nil {
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
		Source:               "local",
	}

	if err := ss.dbManager.AddStandaloneServiceDetails(details); err != nil {
		// Rollback service creation
		ss.dbManager.DeleteService(service.ID)
		ss.logger.Error(fmt.Sprintf("Failed to create standalone service details: %v", err), "standalone")
		return nil, nil, fmt.Errorf("failed to create standalone service details: %w", err)
	}

	// 6. Suggest default interfaces
	suggestedInterfaces := ss.suggestDefaultInterfaces()

	// 7. Add custom interfaces
	for _, iface := range customInterfaces {
		iface.ServiceID = service.ID
		if err := ss.dbManager.AddServiceInterface(iface); err != nil {
			ss.logger.Warn(fmt.Sprintf("Failed to add custom interface: %v", err), "standalone")
		}
	}

	// 8. Load interfaces for response
	interfaces, err := ss.dbManager.GetServiceInterfaces(service.ID)
	if err != nil {
		ss.logger.Warn(fmt.Sprintf("Failed to load service interfaces: %v", err), "standalone")
	}
	service.Interfaces = interfaces

	// 9. Broadcast service update if broadcaster is set
	if ss.eventBroadcaster != nil {
		// TODO: Add broadcast method for standalone services
	}

	ss.logger.Info(fmt.Sprintf("Successfully created standalone service: %s (ID: %d)", serviceName, service.ID), "standalone")

	return service, suggestedInterfaces, nil
}
