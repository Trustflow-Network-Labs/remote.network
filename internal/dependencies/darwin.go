package dependencies

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// installDarwinDependencies installs missing dependencies on macOS
func (dm *DependencyManager) installDarwinDependencies(missing []string) error {
	fmt.Println("Detected macOS system...")

	// Check if Homebrew is installed
	if _, err := exec.LookPath("brew"); err != nil {
		fmt.Println("❌ Homebrew is not installed.")
		fmt.Println("Please install Homebrew first: https://brew.sh")
		return fmt.Errorf("homebrew is required but not installed")
	}

	if dm.contains(missing, "Docker") || dm.contains(missing, "Colima") ||
		dm.contains(missing, "Docker Compose") || dm.contains(missing, "Docker Buildx") {

		fmt.Println("Installing Colima and Docker CLI tools via Homebrew...")

		commands := []string{
			"brew install colima",
			"brew install docker docker-compose docker-buildx",
			"mkdir -p ~/.docker/cli-plugins",
			"ln -sfn $(brew --prefix)/opt/docker-compose/bin/docker-compose ~/.docker/cli-plugins/docker-compose",
			"ln -sfn $(brew --prefix)/opt/docker-buildx/bin/docker-buildx ~/.docker/cli-plugins/docker-buildx",
		}

		for _, cmdStr := range commands {
			fmt.Printf("Executing: %s\n", cmdStr)
			cmd := exec.Command("sh", "-c", cmdStr)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				// Don't fail on mkdir or ln errors (might already exist)
				if !strings.Contains(cmdStr, "mkdir") && !strings.Contains(cmdStr, "ln -sfn") {
					return fmt.Errorf("failed to execute '%s': %v", cmdStr, err)
				}
			}
		}

		fmt.Println("✅ Docker tools installed successfully.")
	}

	return nil
}

// initDarwinDependencies initializes Docker (via Colima) on macOS
func (dm *DependencyManager) initDarwinDependencies() error {
	// Check if Colima is running
	if !dm.isColimaRunning() {
		fmt.Println("Starting Colima...")

		// Get storage path for volume mounting using utils.GetAppPaths
		paths := utils.GetAppPaths("")
		storagePath := paths.DataDir
		quotedPath := strconv.Quote(storagePath)

		cmd := exec.Command("sh", "-c", fmt.Sprintf("colima start --mount %s:w", quotedPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to start Colima: %v", err)
		}

		// Wait and check again
		if !dm.isColimaRunning() {
			return fmt.Errorf("colima failed to start")
		}

		fmt.Println("✅ Colima started successfully.")
	}

	// Set Docker host environment variable
	homeDir := os.Getenv("HOME")
	dockerHost := fmt.Sprintf("unix://%s/.colima/docker.sock", homeDir)
	os.Setenv("DOCKER_HOST", dockerHost)

	// Check and patch Docker API version if needed
	maxApiVersion := dm.patchDockerAPIVersion()

	// Inform user about environment variables
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📝 Important: Add these to your shell profile:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("export DOCKER_HOST=\"%s\"\n", dockerHost)
	if maxApiVersion != "" {
		fmt.Printf("export DOCKER_API_VERSION=%s\n", maxApiVersion)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	return nil
}

// isColimaRunning checks if Colima is running
func (dm *DependencyManager) isColimaRunning() bool {
	cmd := exec.Command("colima", "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(output)), "running")
}

// patchDockerAPIVersion detects and sets the correct Docker API version
func (dm *DependencyManager) patchDockerAPIVersion() string {
	// Set a very high API version to trigger version error
	os.Setenv("DOCKER_API_VERSION", "99.999")

	cmd := exec.Command("docker", "ps")
	output, err := cmd.CombinedOutput()

	if err != nil && strings.Contains(strings.ToLower(string(output)), "maximum supported api version is") {
		// Extract the maximum supported version
		re := regexp.MustCompile(`[Mm]aximum supported API version is ([\d.]+)`)
		match := re.FindStringSubmatch(string(output))
		if len(match) > 1 {
			maxApiVersion := match[1]
			os.Setenv("DOCKER_API_VERSION", maxApiVersion)
			fmt.Printf("ℹ️  Set DOCKER_API_VERSION to %s\n", maxApiVersion)
			return maxApiVersion
		}
	}

	// If no error or couldn't extract version, unset the variable
	os.Unsetenv("DOCKER_API_VERSION")
	return ""
}
