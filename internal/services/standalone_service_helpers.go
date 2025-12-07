package services

import (
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// validateExecutable checks if file exists and is executable
func (ss *StandaloneService) validateExecutable(path string) error {
	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("executable not found: %s", path)
		}
		return fmt.Errorf("cannot access executable: %w", err)
	}

	// Check if it's a regular file (not a directory)
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", path)
	}

	// Check if file has execute permissions (Unix-like systems)
	mode := info.Mode()
	if mode&0111 == 0 { // Check if any execute bit is set
		ss.logger.Warn(fmt.Sprintf("File %s does not have execute permissions", path), "standalone")
		// Don't fail - we'll try to set permissions later
	}

	return nil
}

// calculateFileHash computes SHA256 hash of executable
func (ss *StandaloneService) calculateFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	hashBytes := hash.Sum(nil)
	return hex.EncodeToString(hashBytes), nil
}

// detectExecutablePlatform detects OS/arch for executable
func (ss *StandaloneService) detectExecutablePlatform(path string) (osName string, arch string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Try ELF (Linux, BSD, etc.)
	if elfFile, err := elf.NewFile(file); err == nil {
		osName = "linux"
		switch elfFile.Machine {
		case elf.EM_X86_64:
			arch = "amd64"
		case elf.EM_386:
			arch = "386"
		case elf.EM_AARCH64:
			arch = "arm64"
		case elf.EM_ARM:
			arch = "arm"
		default:
			arch = "unknown"
		}
		return osName, arch, nil
	}

	// Reset file pointer
	file.Seek(0, 0)

	// Try Mach-O (macOS)
	if machoFile, err := macho.NewFile(file); err == nil {
		osName = "darwin"
		switch machoFile.Cpu {
		case macho.CpuAmd64:
			arch = "amd64"
		case macho.CpuArm64:
			arch = "arm64"
		case macho.Cpu386:
			arch = "386"
		default:
			arch = "unknown"
		}
		return osName, arch, nil
	}

	// Reset file pointer
	file.Seek(0, 0)

	// Try PE (Windows)
	if peFile, err := pe.NewFile(file); err == nil {
		osName = "windows"
		switch peFile.Machine {
		case pe.IMAGE_FILE_MACHINE_AMD64:
			arch = "amd64"
		case pe.IMAGE_FILE_MACHINE_I386:
			arch = "386"
		case pe.IMAGE_FILE_MACHINE_ARM64:
			arch = "arm64"
		case pe.IMAGE_FILE_MACHINE_ARMNT:
			arch = "arm"
		default:
			arch = "unknown"
		}
		return osName, arch, nil
	}

	// Could be a script - try to detect from shebang
	file.Seek(0, 0)
	header := make([]byte, 256)
	n, _ := file.Read(header)
	if n > 2 && header[0] == '#' && header[1] == '!' {
		// Script file - platform depends on interpreter
		shebang := string(header[:n])
		if strings.Contains(shebang, "python") || strings.Contains(shebang, "node") ||
		   strings.Contains(shebang, "bash") || strings.Contains(shebang, "sh") {
			return "any", "any", nil // Scripts are generally platform-agnostic
		}
	}

	return "", "", fmt.Errorf("unknown executable format")
}

// suggestDefaultInterfaces returns standard STDIN/STDOUT/STDERR/LOGS
func (ss *StandaloneService) suggestDefaultInterfaces() []SuggestedInterface {
	return []SuggestedInterface{
		{
			InterfaceType: "STDIN",
			Path:          "",
			MountFunction: "",
			Description:   "Standard input stream for the process",
		},
		{
			InterfaceType: "STDOUT",
			Path:          "",
			MountFunction: "",
			Description:   "Standard output stream from the process",
		},
		{
			InterfaceType: "STDERR",
			Path:          "",
			MountFunction: "",
			Description:   "Standard error stream from the process",
		},
		{
			InterfaceType: "LOGS",
			Path:          "",
			MountFunction: "",
			Description:   "Combined logs from the process execution",
		},
	}
}

// setExecutablePermissions sets correct file permissions
func (ss *StandaloneService) setExecutablePermissions(path string) error {
	// Get current permissions
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat file: %w", err)
	}

	// Add execute permission for owner
	newMode := info.Mode() | 0100
	if err := os.Chmod(path, newMode); err != nil {
		return fmt.Errorf("failed to set execute permissions: %w", err)
	}

	ss.logger.Info(fmt.Sprintf("Set execute permissions on %s", path), "standalone")
	return nil
}

// buildEnvVars combines system and custom environment variables
func (ss *StandaloneService) buildEnvVars(custom map[string]string) []string {
	// Start with current environment
	envVars := os.Environ()

	// Add custom environment variables
	for key, value := range custom {
		envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
	}

	return envVars
}

// validateWorkingDirectory checks if the working directory is valid
func (ss *StandaloneService) validateWorkingDirectory(path string) error {
	if path == "" {
		return nil // No working directory specified - will use executable's directory
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("working directory does not exist: %s", path)
		}
		return fmt.Errorf("cannot access working directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("working directory path is not a directory: %s", path)
	}

	return nil
}

// getExecutableDirectory returns the directory containing the executable
func (ss *StandaloneService) getExecutableDirectory(executablePath string) string {
	return filepath.Dir(executablePath)
}
