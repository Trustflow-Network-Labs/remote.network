package dependencies

import (
	"fmt"
	"os/exec"
	"os/user"
	"slices"
	"strings"
)

// installLinuxDependencies installs missing dependencies on Linux
func (dm *DependencyManager) installLinuxDependencies(missing []string) error {
	fmt.Println("Detected Linux system...")

	if dm.contains(missing, "Docker") || dm.contains(missing, "Docker Compose") || dm.contains(missing, "Docker Buildx") {
		fmt.Println("Installing Docker and related packages...")

		// Detect package manager
		if _, err := exec.LookPath("apt"); err == nil {
			// Debian/Ubuntu
			commands := []string{
				"sudo apt update",
				"sudo apt install -y docker.io docker-compose-plugin docker-buildx-plugin",
			}
			for _, cmdStr := range commands {
				fmt.Printf("Executing: %s\n", cmdStr)
				cmd := exec.Command("sh", "-c", cmdStr)
				cmd.Stdout = nil
				cmd.Stderr = nil
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("failed to execute '%s': %v", cmdStr, err)
				}
			}
		} else if _, err := exec.LookPath("dnf"); err == nil {
			// Fedora
			commands := []string{
				"sudo dnf install -y docker docker-compose",
			}
			for _, cmdStr := range commands {
				fmt.Printf("Executing: %s\n", cmdStr)
				cmd := exec.Command("sh", "-c", cmdStr)
				cmd.Stdout = nil
				cmd.Stderr = nil
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("failed to execute '%s': %v", cmdStr, err)
				}
			}
		} else {
			return fmt.Errorf("unsupported Linux distribution (no apt or dnf found)")
		}

		fmt.Println("✅ Docker packages installed successfully.")
	}

	return nil
}

// initLinuxDependencies initializes Docker on Linux
func (dm *DependencyManager) initLinuxDependencies() error {
	// Check if Docker service is running
	if !dm.isDockerRunningLinux() {
		fmt.Println("Starting Docker service...")
		cmd := exec.Command("sudo", "systemctl", "start", "docker")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to start Docker service: %v", err)
		}

		// Wait and check again
		if !dm.isDockerRunningLinux() {
			return fmt.Errorf("docker service failed to start")
		}

		fmt.Println("✅ Docker service started successfully.")
	}

	// Add user to docker group if not already
	if err := dm.addUserToDockerGroupLinux(); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
		fmt.Println("You may need to log out and log back in for group changes to take effect.")
	}

	return nil
}

// isDockerRunningLinux checks if Docker service is running on Linux
func (dm *DependencyManager) isDockerRunningLinux() bool {
	cmd := exec.Command("systemctl", "is-active", "docker")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(strings.ToLower(string(output))) == "active"
}

// addUserToDockerGroupLinux adds the current user to the docker group
func (dm *DependencyManager) addUserToDockerGroupLinux() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %v", err)
	}

	// Check if user is already in docker group
	if err := dm.isUserInDockerGroupLinux(currentUser.Username); err == nil {
		return nil // Already in group
	}

	fmt.Printf("Adding user '%s' to docker group...\n", currentUser.Username)

	cmd := exec.Command("sudo", "usermod", "-aG", "docker", currentUser.Username)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add user to docker group: %v", err)
	}

	fmt.Println("✅ User added to docker group successfully.")
	fmt.Println("⚠️  Note: You need to log out and log back in for this change to take effect.")

	return nil
}

// isUserInDockerGroupLinux checks if a user is in the docker group
func (dm *DependencyManager) isUserInDockerGroupLinux(username string) error {
	cmd := exec.Command("id", "-Gn", username)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get user groups: %v", err)
	}

	groups := strings.Fields(string(output))
	if slices.Contains(groups, "docker") {
		return nil
	}

	return fmt.Errorf("user is not in docker group")
}
