package utils

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
)

// TransferPackageBuilder builds transfer packages for DOCKER/STANDALONE service outputs
type TransferPackageBuilder struct {
	manifest       *types.TransferManifest
	tempDir        string
	stagingDir     string
	logger         *LogsManager
	sourcePeerID   string
	destPeerID     string
	workflowJobID  int64
	jobExecutionID int64
}

// NewTransferPackageBuilder creates a new transfer package builder
func NewTransferPackageBuilder(
	sourcePeerID, destPeerID string,
	workflowJobID, jobExecutionID int64,
	logger *LogsManager,
) (*TransferPackageBuilder, error) {
	// Create temp directory for staging
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("transfer-pkg-%d-", jobExecutionID))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	stagingDir := filepath.Join(tempDir, "staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	return &TransferPackageBuilder{
		manifest:       types.NewTransferManifest(sourcePeerID, destPeerID, workflowJobID, jobExecutionID),
		tempDir:        tempDir,
		stagingDir:     stagingDir,
		logger:         logger,
		sourcePeerID:   sourcePeerID,
		destPeerID:     destPeerID,
		workflowJobID:  workflowJobID,
		jobExecutionID: jobExecutionID,
	}, nil
}

// StageFileResult contains metadata about a staged file
type StageFileResult struct {
	StagingPath string
	Hash        string
	SizeBytes   int64
}

// stageFile is an internal helper that checks existence → copies → hashes → returns metadata
// This common pattern is used in AddStdOutput and AddMountOutput
func (b *TransferPackageBuilder) stageFile(sourcePath string, archivePath string) (*StageFileResult, error) {
	// Check if source file exists
	fileInfo, err := os.Stat(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("source file not found: %w", err)
		}
		return nil, fmt.Errorf("failed to stat source file: %w", err)
	}

	// Copy file to staging directory
	stagingPath := filepath.Join(b.stagingDir, archivePath)
	if err := CopyFile(sourcePath, stagingPath); err != nil {
		return nil, fmt.Errorf("failed to copy file to staging: %w", err)
	}

	// Calculate hash
	hash, err := HashFileToCID(stagingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash file: %w", err)
	}

	return &StageFileResult{
		StagingPath: stagingPath,
		Hash:        hash,
		SizeBytes:   fileInfo.Size(),
	}, nil
}

// AddStdOutput adds a standard output file (STDOUT, STDERR, or LOGS) to the package
func (b *TransferPackageBuilder) AddStdOutput(interfaceType, sourcePath, destPath string) error {
	// Validate interface type
	if interfaceType != types.InterfaceTypeStdout &&
		interfaceType != types.InterfaceTypeStderr &&
		interfaceType != types.InterfaceTypeLogs {
		return fmt.Errorf("invalid interface type for std output: %s", interfaceType)
	}

	// Determine archive path based on interface type
	var archivePath string
	switch interfaceType {
	case types.InterfaceTypeStdout:
		archivePath = "stdout.txt"
	case types.InterfaceTypeStderr:
		archivePath = "stderr.txt"
	case types.InterfaceTypeLogs:
		archivePath = "logs.txt"
	}

	// Stage the file (check exists → copy → hash)
	result, err := b.stageFile(sourcePath, archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			b.logger.Warn(fmt.Sprintf("Source file not found, skipping: %s", sourcePath), "transfer_package")
			return nil // Skip missing files
		}
		return err
	}

	// Add entry to manifest
	b.manifest.AddEntry(types.ManifestEntry{
		Type:            interfaceType,
		ArchivePath:     archivePath,
		DestinationPath: destPath,
		SizeBytes:       result.SizeBytes,
		Hash:            result.Hash,
	})

	b.logger.Info(fmt.Sprintf("Added %s to transfer package: %s (%d bytes)",
		interfaceType, archivePath, result.SizeBytes), "transfer_package")

	return nil
}

// AddMountOutput adds a mount output file to the package
func (b *TransferPackageBuilder) AddMountOutput(mountPath, sourcePath, destPath string) error {
	// Create archive path: mounts/<mount_basename>/<filename>
	mountBasename := filepath.Base(mountPath)
	fileName := filepath.Base(sourcePath)
	archivePath := filepath.Join("mounts", mountBasename, fileName)

	// Create staging subdirectory
	stagingSubdir := filepath.Join(b.stagingDir, "mounts", mountBasename)
	if err := os.MkdirAll(stagingSubdir, 0755); err != nil {
		return fmt.Errorf("failed to create staging subdirectory: %w", err)
	}

	// Stage the file (check exists → copy → hash)
	result, err := b.stageFile(sourcePath, archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			b.logger.Warn(fmt.Sprintf("Mount source file not found, skipping: %s", sourcePath), "transfer_package")
			return nil // Skip missing files
		}
		return err
	}

	// Add entry to manifest
	b.manifest.AddEntry(types.ManifestEntry{
		Type:            types.InterfaceTypeMount,
		MountPath:       mountPath,
		ArchivePath:     archivePath,
		DestinationPath: destPath,
		SizeBytes:       result.SizeBytes,
		Hash:            result.Hash,
	})

	b.logger.Info(fmt.Sprintf("Added MOUNT output to transfer package: %s -> %s (%d bytes)",
		archivePath, destPath, result.SizeBytes), "transfer_package")

	return nil
}

// AddMountDirectory adds all files from a mount directory to the package
func (b *TransferPackageBuilder) AddMountDirectory(mountPath, sourceDir, destDirBase string) error {
	// Check if source directory exists
	dirInfo, err := os.Stat(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			b.logger.Warn(fmt.Sprintf("Mount source directory not found, skipping: %s", sourceDir), "transfer_package")
			return nil // Skip missing directories
		}
		return fmt.Errorf("failed to stat mount source directory: %w", err)
	}

	if !dirInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", sourceDir)
	}

	// Walk the directory and add all files
	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Calculate relative path from source directory
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Build destination path
		destPath := filepath.Join(destDirBase, relPath)

		// Add file to package
		return b.AddMountOutput(mountPath, path, destPath)
	})

	if err != nil {
		return fmt.Errorf("failed to walk mount directory: %w", err)
	}

	return nil
}

// Build creates the final transfer package archive
// Returns the path to the created archive
func (b *TransferPackageBuilder) Build() (string, error) {
	if !b.manifest.HasEntries() {
		return "", fmt.Errorf("no entries to package")
	}

	// Write manifest to staging directory
	manifestPath := filepath.Join(b.stagingDir, types.TransferManifestFileName)
	manifestData, err := json.MarshalIndent(b.manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return "", fmt.Errorf("failed to write manifest: %w", err)
	}

	// Create output archive path
	archiveName := fmt.Sprintf("transfer_job_%d_to_%s.tar.gz", b.jobExecutionID, b.destPeerID[:8])
	archivePath := filepath.Join(b.tempDir, archiveName)

	// Compress staging directory
	if err := Compress(b.stagingDir, archivePath); err != nil {
		return "", fmt.Errorf("failed to compress transfer package: %w", err)
	}

	b.logger.Info(fmt.Sprintf("Built transfer package: %s (%d entries)", archivePath, len(b.manifest.Entries)), "transfer_package")

	return archivePath, nil
}

// Cleanup removes the temporary directory
func (b *TransferPackageBuilder) Cleanup() {
	if b.tempDir != "" {
		os.RemoveAll(b.tempDir)
	}
}

// GetManifest returns the manifest (for testing/inspection)
func (b *TransferPackageBuilder) GetManifest() *types.TransferManifest {
	return b.manifest
}

// TransferPackageExtractor extracts transfer packages and places files in correct locations
type TransferPackageExtractor struct {
	logger *LogsManager
}

// NewTransferPackageExtractor creates a new transfer package extractor
func NewTransferPackageExtractor(logger *LogsManager) *TransferPackageExtractor {
	return &TransferPackageExtractor{
		logger: logger,
	}
}

// IsTransferPackage checks if the extracted directory contains a transfer package manifest
func (e *TransferPackageExtractor) IsTransferPackage(extractDir string) bool {
	manifestPath := filepath.Join(extractDir, types.TransferManifestFileName)
	_, err := os.Stat(manifestPath)
	return err == nil
}

// ExtractAndPlace extracts a transfer package and places files according to manifest
// baseDir is the job's workflow directory (e.g., workflows/{peer}/{wf_job_id}/jobs/{job_id}/)
func (e *TransferPackageExtractor) ExtractAndPlace(archivePath, baseDir string) error {
	// Create temp directory for extraction
	tempExtractDir, err := os.MkdirTemp("", "transfer-extract-")
	if err != nil {
		return fmt.Errorf("failed to create temp extract directory: %w", err)
	}
	defer os.RemoveAll(tempExtractDir)

	// Decompress archive
	if err := Decompress(archivePath, tempExtractDir); err != nil {
		return fmt.Errorf("failed to decompress archive: %w", err)
	}

	// Check if it's a transfer package
	if !e.IsTransferPackage(tempExtractDir) {
		return fmt.Errorf("not a valid transfer package (missing manifest)")
	}

	// Read manifest
	manifestPath := filepath.Join(tempExtractDir, types.TransferManifestFileName)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest types.TransferManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	e.logger.Info(fmt.Sprintf("Extracting transfer package v%s with %d entries",
		manifest.Version, len(manifest.Entries)), "transfer_package")

	// Process each entry
	for _, entry := range manifest.Entries {
		sourcePath := filepath.Join(tempExtractDir, entry.ArchivePath)
		destPath := filepath.Join(baseDir, entry.DestinationPath)

		// Create destination directory
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			e.logger.Error(fmt.Sprintf("Failed to create destination directory %s: %v", destDir, err), "transfer_package")
			continue
		}

		// Copy file to destination
		if err := copyFile(sourcePath, destPath); err != nil {
			e.logger.Error(fmt.Sprintf("Failed to copy %s to %s: %v", entry.ArchivePath, destPath, err), "transfer_package")
			continue
		}

		// Verify hash
		if entry.Hash != "" {
			valid, err := VerifyFileHash(destPath, entry.Hash)
			if err != nil {
				e.logger.Warn(fmt.Sprintf("Failed to verify hash for %s: %v", destPath, err), "transfer_package")
			} else if !valid {
				e.logger.Error(fmt.Sprintf("Hash mismatch for %s", destPath), "transfer_package")
				os.Remove(destPath) // Remove corrupted file
				continue
			}
		}

		e.logger.Info(fmt.Sprintf("Extracted %s -> %s (%d bytes)",
			entry.ArchivePath, destPath, entry.SizeBytes), "transfer_package")
	}

	e.logger.Info(fmt.Sprintf("Transfer package extraction completed: %d entries processed",
		len(manifest.Entries)), "transfer_package")

	return nil
}

// ExtractManifestOnly reads only the manifest from a transfer package without full extraction
func (e *TransferPackageExtractor) ExtractManifestOnly(archivePath string) (*types.TransferManifest, error) {
	// Open the archive
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Find manifest.json
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		// Check if this is the manifest
		if strings.HasSuffix(header.Name, types.TransferManifestFileName) ||
			header.Name == types.TransferManifestFileName {
			// Read manifest content
			manifestData, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, fmt.Errorf("failed to read manifest: %w", err)
			}

			var manifest types.TransferManifest
			if err := json.Unmarshal(manifestData, &manifest); err != nil {
				return nil, fmt.Errorf("failed to parse manifest: %w", err)
			}

			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("manifest not found in archive")
}

// copyFile is deprecated - use CopyFile from file_ops.go instead
// Kept for backward compatibility, delegates to CopyFile
func copyFile(src, dst string) error {
	return CopyFile(src, dst)
}
