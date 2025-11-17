package dependencies

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// installWindowsDependencies installs missing dependencies on Windows
func (dm *DependencyManager) installWindowsDependencies(missing []string) error {
	fmt.Println("Detected Windows system...")

	if dm.contains(missing, "Docker") {
		fmt.Println("Opening Docker Desktop download page...")
		fmt.Println("Please download and install Docker Desktop from the browser.")

		err := exec.Command("rundll32", "url.dll,FileProtocolHandler", "https://www.docker.com/products/docker-desktop/").Run()
		if err != nil {
			fmt.Printf("⚠️  Failed to open browser: %v\n", err)
			fmt.Println("Please manually visit: https://www.docker.com/products/docker-desktop/")
		}

		fmt.Println("\nAfter installing Docker Desktop, please restart your computer.")
		fmt.Println("Then run this command again with --check-dependencies flag.")
		return fmt.Errorf("manual installation required - please install Docker Desktop and restart")
	}

	return nil
}

// initWindowsDependencies initializes Docker on Windows
func (dm *DependencyManager) initWindowsDependencies() error {
	// Check symlink support (required for Docker on Windows)
	if err := dm.checkSymlinkSupport(); err != nil {
		fmt.Println("❌ Symlink creation is not supported for your current user.")
		fmt.Println("To fix this, you can do one of the following:")

		fmt.Println("\n➡  OPTION 1: Enable Developer Mode (recommended)")
		fmt.Println("  Run this in PowerShell (as Administrator):")
		fmt.Println(`  reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock" /t REG_DWORD /f /v "AllowDevelopmentWithoutDevLicense" /d "1"`)
		fmt.Println("  OR open Settings → Privacy & Security → For Developers → Enable Developer Mode")

		fmt.Println("\n➡  OPTION 2: Grant SeCreateSymbolicLinkPrivilege to your user (advanced)")
		fmt.Println("  Open Local Security Policy (secpol.msc) → Local Policies → User Rights Assignment")
		fmt.Println("  → Find 'Create symbolic links' → Add your user → Restart")

		fmt.Println("\nAfter applying one of the options above, please restart your computer and try again.")

		return fmt.Errorf("symlink creation is not permitted for the current user")
	}

	// Try to start Docker Desktop
	fmt.Println("Attempting to start Docker Desktop...")

	candidates := []string{
		`Start-Process "Docker Desktop" -Verb runAs`,
		`Start-Process "C:\Program Files\Docker\Docker\Docker Desktop.exe" -Verb runAs`,
	}

	for _, cmd := range candidates {
		err := exec.Command("powershell", "-Command", cmd).Run()
		if err == nil {
			fmt.Println("✅ Docker Desktop started successfully.")
			fmt.Println("⚠️  Note: It may take a few moments for Docker to fully start.")
			return nil
		}
	}

	fmt.Println("⚠️  Could not automatically start Docker Desktop.")
	fmt.Println("Please start Docker Desktop manually from the Start menu.")

	return nil
}

// checkSymlinkSupport checks if the user can create symlinks on Windows
func (dm *DependencyManager) checkSymlinkSupport() error {
	// Try to create a dummy symlink and remove it
	tmpDir := os.TempDir()
	target := filepath.Join(tmpDir, "symlink_target.txt")
	link := filepath.Join(tmpDir, "symlink_link.txt")

	_ = os.WriteFile(target, []byte("test"), 0644)
	defer os.Remove(target)

	err := os.Symlink(target, link)
	defer os.Remove(link)

	if err != nil {
		return fmt.Errorf("symlinks not supported: %w", err)
	}
	return nil
}
