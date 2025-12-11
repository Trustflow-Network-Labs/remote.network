package utils

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Compress compresses a file or directory to a tar.gz archive
func Compress(sourcePath, targetPath string) error {
	// Create the target file
	targetFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	defer targetFile.Close()

	// Create gzip writer
	gzipWriter := gzip.NewWriter(targetFile)
	defer gzipWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Check if source is a file or directory
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	if sourceInfo.IsDir() {
		// Compress directory
		return compressDirectory(sourcePath, tarWriter)
	} else {
		// Compress single file
		return compressFile(sourcePath, sourceInfo.Name(), tarWriter)
	}
}

// compressDirectory compresses a directory recursively
func compressDirectory(dirPath string, tarWriter *tar.Writer) error {
	return filepath.Walk(dirPath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header: %w", err)
		}

		// Set the name to be relative to the base directory
		relPath, err := filepath.Rel(dirPath, filePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		header.Name = relPath

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}

		// If it's a file, write its content
		if !info.IsDir() {
			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("failed to open file: %w", err)
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return fmt.Errorf("failed to write file content: %w", err)
			}
		}

		return nil
	})
}

// compressFile compresses a single file
func compressFile(filePath, fileName string, tarWriter *tar.Writer) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file info
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Create tar header
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("failed to create tar header: %w", err)
	}
	header.Name = fileName

	// Write header
	if err := tarWriter.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}

	// Write file content
	if _, err := io.Copy(tarWriter, file); err != nil {
		return fmt.Errorf("failed to write file content: %w", err)
	}

	return nil
}

// Decompress decompresses a tar.gz archive to a target directory
func Decompress(archivePath, targetDir string) error {
	// Open the archive file
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Construct target path
		targetPath := filepath.Join(targetDir, header.Name)

		// Check if it's a directory
		if header.Typeflag == tar.TypeDir {
			// Create directory
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directories if needed
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Create file
		targetFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		// Copy content
		if _, err := io.Copy(targetFile, tarReader); err != nil {
			targetFile.Close()
			return fmt.Errorf("failed to write file content: %w", err)
		}
		targetFile.Close()
	}

	return nil
}

// DecompressWithRename decompresses a tar.gz archive to a target directory with optional renaming.
// If newName is provided (non-empty), the root file or folder will be renamed:
// - For a single file archive: the file is renamed to newName
// - For a single root folder archive: the folder is renamed to newName
// - For archives with multiple root entries: rename is ignored (files extracted as-is)
func DecompressWithRename(archivePath, targetDir, newName string) error {
	// If no rename requested, use standard Decompress
	if newName == "" {
		return Decompress(archivePath, targetDir)
	}

	// Open the archive file
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// First pass: determine root entries to understand archive structure
	var headers []*tar.Header
	var contents [][]byte
	rootEntries := make(map[string]bool)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Store header
		headerCopy := *header
		headers = append(headers, &headerCopy)

		// Read content for files
		if header.Typeflag != tar.TypeDir {
			content, err := io.ReadAll(tarReader)
			if err != nil {
				return fmt.Errorf("failed to read file content: %w", err)
			}
			contents = append(contents, content)
		} else {
			contents = append(contents, nil)
		}

		// Track root entries (first path component)
		// Skip "." entries as they represent the archive root directory (don't count for rename)
		if header.Name == "." {
			// Don't add to rootEntries but still store header/content for extraction
		} else {
			rootName := strings.Split(header.Name, string(os.PathSeparator))[0]
			// Also handle forward slash which is common in tar archives
			if slashPos := strings.Index(header.Name, "/"); slashPos > 0 && (rootName == header.Name || strings.Index(header.Name, string(os.PathSeparator)) == -1) {
				rootName = header.Name[:slashPos]
			}
			if rootName == "" {
				rootName = header.Name
			}
			rootEntries[rootName] = true
		}
	}

	// Determine if we can rename
	canRename := len(rootEntries) == 1
	var originalRoot string
	if canRename {
		for root := range rootEntries {
			originalRoot = root
		}
	}

	// Extract files with potential renaming
	for i, header := range headers {
		// Skip "." directory entry - it's just the archive root marker
		if header.Name == "." {
			continue
		}

		targetPath := header.Name

		// Apply renaming if applicable
		if canRename && originalRoot != "" {
			if header.Name == originalRoot {
				// This is the root entry itself
				targetPath = newName
			} else if strings.HasPrefix(header.Name, originalRoot+string(os.PathSeparator)) {
				// This is inside the root folder
				targetPath = newName + header.Name[len(originalRoot):]
			} else if strings.HasPrefix(header.Name, originalRoot+"/") {
				// Handle Unix-style paths
				targetPath = newName + header.Name[len(originalRoot):]
			}
		}

		targetPath = filepath.Join(targetDir, targetPath)

		// Check if it's a directory
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directories if needed
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Create file
		targetFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		// Write content
		if contents[i] != nil {
			if _, err := targetFile.Write(contents[i]); err != nil {
				targetFile.Close()
				return fmt.Errorf("failed to write file content: %w", err)
			}
		}
		targetFile.Close()
	}

	return nil
}
