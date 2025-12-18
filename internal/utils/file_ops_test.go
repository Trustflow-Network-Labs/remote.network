package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateExecutable(t *testing.T) {
	// Create temp directory for tests
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("valid executable", func(t *testing.T) {
		execPath := filepath.Join(tmpDir, "test_exec")
		if err := os.WriteFile(execPath, []byte("#!/bin/sh\necho test"), 0755); err != nil {
			t.Fatalf("Failed to create test executable: %v", err)
		}

		if err := ValidateExecutable(execPath); err != nil {
			t.Errorf("ValidateExecutable() failed for valid executable: %v", err)
		}
	})

	t.Run("file without execute permissions", func(t *testing.T) {
		noExecPath := filepath.Join(tmpDir, "no_exec")
		if err := os.WriteFile(noExecPath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		if err := ValidateExecutable(noExecPath); err == nil {
			t.Error("ValidateExecutable() should fail for file without execute permissions")
		}
	})

	t.Run("directory instead of file", func(t *testing.T) {
		dirPath := filepath.Join(tmpDir, "test_dir")
		if err := os.Mkdir(dirPath, 0755); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}

		if err := ValidateExecutable(dirPath); err == nil {
			t.Error("ValidateExecutable() should fail for directory")
		}
	})

	t.Run("non-existent file", func(t *testing.T) {
		if err := ValidateExecutable("/nonexistent/path"); err == nil {
			t.Error("ValidateExecutable() should fail for non-existent file")
		}
	})
}

func TestValidateDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("valid directory", func(t *testing.T) {
		if err := ValidateDirectory(tmpDir); err != nil {
			t.Errorf("ValidateDirectory() failed for valid directory: %v", err)
		}
	})

	t.Run("file instead of directory", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "test_file")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		if err := ValidateDirectory(filePath); err == nil {
			t.Error("ValidateDirectory() should fail for regular file")
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		if err := ValidateDirectory("/nonexistent/path"); err == nil {
			t.Error("ValidateDirectory() should fail for non-existent directory")
		}
	})
}

func TestValidateRegularFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("valid regular file", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "test_file")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		if err := ValidateRegularFile(filePath); err != nil {
			t.Errorf("ValidateRegularFile() failed for valid file: %v", err)
		}
	})

	t.Run("directory instead of file", func(t *testing.T) {
		if err := ValidateRegularFile(tmpDir); err == nil {
			t.Error("ValidateRegularFile() should fail for directory")
		}
	})

	t.Run("non-existent file", func(t *testing.T) {
		if err := ValidateRegularFile("/nonexistent/path"); err == nil {
			t.Error("ValidateRegularFile() should fail for non-existent file")
		}
	})
}

func TestFileExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("existing file", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "test_file")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		exists, err := FileExists(filePath)
		if err != nil {
			t.Errorf("FileExists() returned error: %v", err)
		}
		if !exists {
			t.Error("FileExists() should return true for existing file")
		}
	})

	t.Run("non-existent file", func(t *testing.T) {
		exists, err := FileExists("/nonexistent/path")
		if err != nil {
			t.Errorf("FileExists() returned error for non-existent file: %v", err)
		}
		if exists {
			t.Error("FileExists() should return false for non-existent file")
		}
	})
}

func TestDirectoryExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("existing directory", func(t *testing.T) {
		exists, err := DirectoryExists(tmpDir)
		if err != nil {
			t.Errorf("DirectoryExists() returned error: %v", err)
		}
		if !exists {
			t.Error("DirectoryExists() should return true for existing directory")
		}
	})

	t.Run("file instead of directory", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "test_file")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		exists, err := DirectoryExists(filePath)
		if err != nil {
			t.Errorf("DirectoryExists() returned error: %v", err)
		}
		if exists {
			t.Error("DirectoryExists() should return false for regular file")
		}
	})

	t.Run("non-existent directory", func(t *testing.T) {
		exists, err := DirectoryExists("/nonexistent/path")
		if err != nil {
			t.Errorf("DirectoryExists() returned error for non-existent path: %v", err)
		}
		if exists {
			t.Error("DirectoryExists() should return false for non-existent directory")
		}
	})
}

func TestSetExecutablePermissions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test_file")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if err := SetExecutablePermissions(filePath); err != nil {
		t.Errorf("SetExecutablePermissions() failed: %v", err)
	}

	// Verify permissions
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if info.Mode()&0111 == 0 {
		t.Error("SetExecutablePermissions() did not set execute permissions")
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("successful copy", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "source.txt")
		dstPath := filepath.Join(tmpDir, "dest.txt")
		content := []byte("test content")

		if err := os.WriteFile(srcPath, content, 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		if err := CopyFile(srcPath, dstPath); err != nil {
			t.Errorf("CopyFile() failed: %v", err)
		}

		// Verify content
		dstContent, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("Failed to read destination file: %v", err)
		}

		if string(dstContent) != string(content) {
			t.Errorf("File content mismatch: got %s, want %s", dstContent, content)
		}
	})

	t.Run("creates destination directory", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "source2.txt")
		dstPath := filepath.Join(tmpDir, "subdir", "dest2.txt")

		if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		if err := CopyFile(srcPath, dstPath); err != nil {
			t.Errorf("CopyFile() failed to create destination directory: %v", err)
		}

		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			t.Error("Destination file was not created")
		}
	})
}

func TestCopyDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "file_ops_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source directory structure
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	// Create some files
	if err := os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	dstDir := filepath.Join(tmpDir, "dst")

	if err := CopyDirectory(srcDir, dstDir); err != nil {
		t.Errorf("CopyDirectory() failed: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(dstDir, "file1.txt")); os.IsNotExist(err) {
		t.Error("file1.txt was not copied")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "subdir", "file2.txt")); os.IsNotExist(err) {
		t.Error("subdir/file2.txt was not copied")
	}

	// Verify content
	content, err := os.ReadFile(filepath.Join(dstDir, "file1.txt"))
	if err != nil {
		t.Fatalf("Failed to read copied file: %v", err)
	}
	if string(content) != "content1" {
		t.Errorf("File content mismatch: got %s, want content1", content)
	}
}
