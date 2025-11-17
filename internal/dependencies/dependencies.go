package dependencies

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// DependencyManager handles checking and installing Docker-related dependencies
type DependencyManager struct {
	config *utils.ConfigManager
	logger *utils.LogsManager
}

// NewDependencyManager creates a new dependency manager instance
func NewDependencyManager(config *utils.ConfigManager, logger *utils.LogsManager) *DependencyManager {
	return &DependencyManager{
		config: config,
		logger: logger,
	}
}

// CheckDependencies checks if all required Docker dependencies are installed
// Returns true if all dependencies are available, false otherwise
func (dm *DependencyManager) CheckDependencies() bool {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔍 Checking Docker dependencies...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	missing := dm.GetMissingDependencies()

	if len(missing) == 0 {
		fmt.Println("✅ All Docker dependencies are installed.")
		return true
	}

	fmt.Printf("⚠️  Missing dependencies: %s\n", strings.Join(missing, ", "))
	fmt.Println("\nDocker services will be disabled until dependencies are installed.")
	fmt.Println("You can install dependencies by running:")
	fmt.Println("  ./remote-network start --check-dependencies")
	fmt.Println("\nOr install manually:")
	dm.printInstallInstructions(missing)

	return false
}

// CheckAndInstallDependencies checks dependencies and offers to install them
// Returns true if all dependencies are available (were already installed or successfully installed)
func (dm *DependencyManager) CheckAndInstallDependencies() bool {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔍 Checking Docker dependencies...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	missing := dm.GetMissingDependencies()

	if len(missing) == 0 {
		fmt.Println("✅ All Docker dependencies are installed.")

		// Try to initialize/start services if needed
		if err := dm.InitializeDependencies(); err != nil {
			fmt.Printf("⚠️  Warning: %v\n", err)
			fmt.Println("Docker services may not work correctly.")
			return false
		}

		// Check if Docker is responsive
		if !dm.isDockerResponsive() {
			fmt.Println("⚠️  Docker is not responsive.")
			fmt.Println("Please start Docker manually and try again.")
			return false
		}

		fmt.Println("✅ Docker is running and responsive.")
		return true
	}

	fmt.Printf("⚠️  Missing dependencies: %s\n", strings.Join(missing, ", "))
	fmt.Println("\nDocker services require these dependencies to function.")

	// Prompt user for installation
	fmt.Print("\nWould you like to install missing dependencies now? (yes/no): ")
	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		fmt.Println("⚠️  Skipping dependency installation.")
		fmt.Println("Docker services will be disabled.")
		return false
	}

	// Install dependencies
	if err := dm.installDependencies(missing); err != nil {
		fmt.Printf("❌ Failed to install dependencies: %v\n", err)
		return false
	}

	// Initialize/start services
	if err := dm.InitializeDependencies(); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
		fmt.Println("Docker services may not work correctly.")
		return false
	}

	// Final check
	if !dm.isDockerResponsive() {
		fmt.Println("⚠️  Docker is not responsive after installation.")
		fmt.Println("You may need to log out and log back in, or restart your system.")
		return false
	}

	fmt.Println("✅ Docker dependencies installed and running successfully!")
	return true
}

// GetMissingDependencies returns a list of missing Docker dependencies
func (dm *DependencyManager) GetMissingDependencies() []string {
	missing := []string{}

	// Check for Docker CLI
	if _, err := exec.LookPath("docker"); err != nil {
		missing = append(missing, "Docker")
	} else {
		// Check for Docker Compose subcommand
		if !dm.dockerSubcommandExists("compose") {
			missing = append(missing, "Docker Compose")
		}

		// Check for Docker Buildx subcommand
		if !dm.dockerSubcommandExists("buildx") {
			missing = append(missing, "Docker Buildx")
		}
	}

	// macOS-specific: Check for Colima
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("colima"); err != nil {
			missing = append(missing, "Colima")
		}
	}

	return missing
}

// isDockerResponsive checks if Docker is running and responsive
func (dm *DependencyManager) isDockerResponsive() bool {
	cmd := exec.Command("docker", "info")
	err := cmd.Run()
	return err == nil
}

// dockerSubcommandExists checks if a Docker subcommand is available
func (dm *DependencyManager) dockerSubcommandExists(subcmd string) bool {
	cmd := exec.Command("docker", subcmd, "--help")
	err := cmd.Run()
	return err == nil
}

// installDependencies installs missing dependencies based on the OS
func (dm *DependencyManager) installDependencies(missing []string) error {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📦 Installing dependencies...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	var err error
	switch runtime.GOOS {
	case "linux":
		err = dm.installLinuxDependencies(missing)
	case "darwin":
		err = dm.installDarwinDependencies(missing)
	case "windows":
		err = dm.installWindowsDependencies(missing)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err != nil {
		dm.logger.Error(fmt.Sprintf("Failed to install dependencies: %v", err), "dependencies")
	}

	return err
}

// InitializeDependencies initializes and starts Docker services if needed
func (dm *DependencyManager) InitializeDependencies() error {
	switch runtime.GOOS {
	case "linux":
		return dm.initLinuxDependencies()
	case "darwin":
		return dm.initDarwinDependencies()
	case "windows":
		return dm.initWindowsDependencies()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// printInstallInstructions prints manual installation instructions
func (dm *DependencyManager) printInstallInstructions(missing []string) {
	switch runtime.GOOS {
	case "linux":
		fmt.Println("\n  Ubuntu/Debian:")
		fmt.Println("    sudo apt update")
		fmt.Println("    sudo apt install -y docker.io docker-compose-plugin")
		fmt.Println("    sudo systemctl start docker")
		fmt.Println("    sudo usermod -aG docker $USER")
		fmt.Println("\n  Fedora:")
		fmt.Println("    sudo dnf install -y docker docker-compose")
		fmt.Println("    sudo systemctl start docker")
		fmt.Println("    sudo usermod -aG docker $USER")

	case "darwin":
		fmt.Println("\n  Using Homebrew:")
		fmt.Println("    brew install colima docker docker-compose docker-buildx")
		fmt.Println("    mkdir -p ~/.docker/cli-plugins")
		fmt.Println("    ln -sfn $(brew --prefix)/opt/docker-compose/bin/docker-compose ~/.docker/cli-plugins/docker-compose")
		fmt.Println("    ln -sfn $(brew --prefix)/opt/docker-buildx/bin/docker-buildx ~/.docker/cli-plugins/docker-buildx")
		fmt.Println("    colima start")

	case "windows":
		fmt.Println("\n  Download and install Docker Desktop:")
		fmt.Println("    https://www.docker.com/products/docker-desktop/")
	}

	fmt.Println()
}

// contains checks if a slice contains a string (case-insensitive)
func (dm *DependencyManager) contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}
