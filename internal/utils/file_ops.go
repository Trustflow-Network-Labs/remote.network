package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ValidateExecutable checks if a file exists, is a regular file, and has execute permissions
func ValidateExecutable(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("executable not found: %s", path)
		}
		return fmt.Errorf("failed to stat executable: %w", err)
	}

	if fileInfo.IsDir() {
		return fmt.Errorf("path is a directory, not an executable: %s", path)
	}

	// Check if file has execute permissions
	mode := fileInfo.Mode()
	if mode&0111 == 0 {
		return fmt.Errorf("file is not executable (missing execute permissions): %s", path)
	}

	return nil
}

// ValidateDirectory checks if a directory exists and is accessible
func ValidateDirectory(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory not found: %s", path)
		}
		return fmt.Errorf("failed to stat directory: %w", err)
	}

	if !fileInfo.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	return nil
}

// ValidateRegularFile checks if a file exists and is a regular file (not a directory)
func ValidateRegularFile(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", path)
		}
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if fileInfo.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", path)
	}

	return nil
}

// FileExists checks if a file exists with proper error handling
func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DirectoryExists checks if a directory exists
func DirectoryExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// SetExecutablePermissions adds execute permission to a file (mode 0755)
func SetExecutablePermissions(path string) error {
	return os.Chmod(path, 0755)
}

// CopyFile safely copies a file from src to dst with error handling
// This replaces the copyFile function in transfer_package.go
func CopyFile(src, dst string) error {
	// Open source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	// Create destination directory if needed
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create destination file
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy contents
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := destFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync destination file: %w", err)
	}

	return nil
}

// CopyDirectory recursively copies a directory from src to dst
func CopyDirectory(src, dst string) error {
	// Get properties of source directory
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source directory: %w", err)
	}

	// Create destination directory
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Read directory contents
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	// Copy each entry
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectory
			if err := CopyDirectory(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Copy file
			if err := CopyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}
