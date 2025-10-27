package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"golang.org/x/crypto/pbkdf2"
)

// FileProcessor handles post-upload file processing: compression, encryption, and hashing
type FileProcessor struct {
	dbManager    *database.SQLiteManager
	logger       *utils.LogsManager
	configMgr    *utils.ConfigManager
	storageDir   string
}

// NewFileProcessor creates a new file processor
func NewFileProcessor(dbManager *database.SQLiteManager, logger *utils.LogsManager, configMgr *utils.ConfigManager) *FileProcessor {
	storageDir := configMgr.GetConfigWithDefault("data_service_storage_dir", "./data/services")

	// Ensure storage directory exists
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		logger.Error(fmt.Sprintf("Failed to create storage directory: %v", err), "file_processor")
	}

	return &FileProcessor{
		dbManager:  dbManager,
		logger:     logger,
		configMgr:  configMgr,
		storageDir: storageDir,
	}
}

// ProcessUploadedFile processes files after all uploads in a group complete
// Steps: reconstruct directory structure, compress, encrypt, hash, store
func (fp *FileProcessor) ProcessUploadedFile(uploadGroupID string, serviceID int64) error {
	fp.logger.Info(fmt.Sprintf("Processing uploaded files for service %d, upload group %s", serviceID, uploadGroupID), "file_processor")

	// Get all upload sessions in the group
	sessions, err := fp.dbManager.GetUploadSessionsByGroup(uploadGroupID)
	if err != nil || len(sessions) == 0 {
		return fmt.Errorf("no upload sessions found for group: %s", uploadGroupID)
	}

	// Create a temporary directory to reconstruct file structure
	reconstructDir := filepath.Join(fp.storageDir, fmt.Sprintf("reconstruct_%s", uploadGroupID))
	if err := os.MkdirAll(reconstructDir, 0755); err != nil {
		return fmt.Errorf("failed to create reconstruction directory: %w", err)
	}
	defer os.RemoveAll(reconstructDir) // Clean up after processing

	// Reconstruct directory structure from uploaded files
	var totalOriginalSize int64
	fp.logger.Info(fmt.Sprintf("Reconstructing %d files...", len(sessions)), "file_processor")

	for i, session := range sessions {
		// Check if temp file exists
		if _, err := os.Stat(session.TempFilePath); os.IsNotExist(err) {
			fp.logger.Error(fmt.Sprintf("Temp file not found: %s", session.TempFilePath), "file_processor")
			return fmt.Errorf("temp file not found: %s", session.TempFilePath)
		}

		// Clean and validate the file path to prevent issues with special characters
		// Remove any leading slashes and clean the path
		cleanPath := filepath.Clean(session.FilePath)
		if filepath.IsAbs(cleanPath) {
			// Make it relative by removing the leading separator
			cleanPath = cleanPath[1:]
		}

		// Create the file in the reconstructed directory with its original path
		targetPath := filepath.Join(reconstructDir, cleanPath)
		targetDir := filepath.Dir(targetPath)

		fp.logger.Info(fmt.Sprintf("Reconstructing file %d/%d: %s -> %s", i+1, len(sessions), session.FilePath, targetPath), "file_processor")

		// Create parent directories
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			fp.logger.Error(fmt.Sprintf("Failed to create directory %s: %v", targetDir, err), "file_processor")
			return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
		}

		// Copy temp file to target path
		if err := fp.copyFile(session.TempFilePath, targetPath); err != nil {
			fp.logger.Error(fmt.Sprintf("Failed to copy file %s: %v", session.FilePath, err), "file_processor")
			return fmt.Errorf("failed to copy file %s: %w", session.FilePath, err)
		}

		// Get file size
		fileInfo, err := os.Stat(targetPath)
		if err != nil {
			fp.logger.Error(fmt.Sprintf("Failed to get file info for %s: %v", targetPath, err), "file_processor")
			return fmt.Errorf("failed to get file info for %s: %w", session.FilePath, err)
		}
		totalOriginalSize += fileInfo.Size()

		fp.logger.Info(fmt.Sprintf("Successfully reconstructed file %d/%d: %s (%d bytes)", i+1, len(sessions), session.FilePath, fileInfo.Size()), "file_processor")
	}

	fp.logger.Info(fmt.Sprintf("File reconstruction complete: %d files, %d total bytes", len(sessions), totalOriginalSize), "file_processor")

	originalSize := totalOriginalSize

	// Step 1: Compress the reconstructed directory structure
	fp.logger.Info(fmt.Sprintf("Compressing %d files from directory: %s", len(sessions), reconstructDir), "file_processor")
	compressedPath := filepath.Join(fp.storageDir, fmt.Sprintf("%s.tar.gz", uploadGroupID))
	if err := utils.Compress(reconstructDir, compressedPath); err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to compress files: %v", err), "file_processor")
		return fmt.Errorf("failed to compress files: %w", err)
	}
	defer os.Remove(compressedPath) // Clean up after encryption

	// Get compressed file size
	compressedInfo, err := os.Stat(compressedPath)
	if err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to get compressed file info: %v", err), "file_processor")
		return fmt.Errorf("failed to get compressed file info: %w", err)
	}
	compressedSize := compressedInfo.Size()
	compressionRatio := float64(compressedSize) / float64(originalSize) * 100.0
	fp.logger.Info(fmt.Sprintf("Compression complete: %d bytes -> %d bytes (%.1f%% of original)", originalSize, compressedSize, compressionRatio), "file_processor")

	// Step 2: Generate encryption key and passphrase
	fp.logger.Info("Generating encryption key...", "file_processor")
	passphrase, keyData, err := fp.generateEncryptionKey()
	if err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to generate encryption key: %v", err), "file_processor")
		return fmt.Errorf("failed to generate encryption key: %w", err)
	}
	fp.logger.Info("Encryption key generated successfully", "file_processor")

	// Store encryption key in database
	// Format: passphrase|keydata (separated by |)
	encryptionKey := &database.EncryptionKey{
		ServiceID:      serviceID,
		PassphraseHash: hashPassphrase(passphrase),
		KeyData:        passphrase + "|" + hex.EncodeToString(keyData), // Store both passphrase and key data
	}
	if err := fp.dbManager.AddEncryptionKey(encryptionKey); err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to store encryption key: %v", err), "file_processor")
		return fmt.Errorf("failed to store encryption key: %w", err)
	}
	fp.logger.Info("Encryption key stored in database", "file_processor")

	// Step 3: Encrypt the compressed file
	fp.logger.Info(fmt.Sprintf("Encrypting compressed file: %s -> %s", compressedPath, fmt.Sprintf("service_%d_encrypted.dat", serviceID)), "file_processor")
	encryptedPath := filepath.Join(fp.storageDir, fmt.Sprintf("service_%d_encrypted.dat", serviceID))
	if err := fp.encryptFile(compressedPath, encryptedPath, keyData); err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to encrypt file: %v", err), "file_processor")
		return fmt.Errorf("failed to encrypt file: %w", err)
	}

	// Get encrypted file size
	encryptedInfo, err := os.Stat(encryptedPath)
	if err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to get encrypted file info: %v", err), "file_processor")
		return fmt.Errorf("failed to get encrypted file info: %w", err)
	}
	encryptedSize := encryptedInfo.Size()
	fp.logger.Info(fmt.Sprintf("Encryption complete: %d bytes (compressed) -> %d bytes (encrypted)", compressedSize, encryptedSize), "file_processor")

	// Step 4: Calculate BLAKE3 hash of encrypted file
	fp.logger.Info("Calculating BLAKE3 hash of encrypted file...", "file_processor")
	fileHash, err := utils.HashFileToCID(encryptedPath)
	if err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to calculate file hash: %v", err), "file_processor")
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}
	fp.logger.Info(fmt.Sprintf("File hash calculated: %s", fileHash), "file_processor")

	// Step 5: Store data service details
	// Use the first file's name as the service name, or a generic name if multiple files
	fileCount := len(sessions)
	serviceName := sessions[0].Filename
	if fileCount > 1 {
		serviceName = fmt.Sprintf("%d files", fileCount)
	}
	fp.logger.Info(fmt.Sprintf("Storing data service details: service_id=%d, files=%d, name=%s", serviceID, fileCount, serviceName), "file_processor")

	dataDetails := &database.DataServiceDetails{
		ServiceID:         serviceID,
		FilePath:          serviceName,
		EncryptedPath:     encryptedPath,
		Hash:              fileHash,
		CompressionType:   "tar.gz",
		EncryptionKeyID:   &encryptionKey.ID,
		SizeBytes:         encryptedSize,
		OriginalSizeBytes: originalSize,
		UploadCompleted:   true,
	}
	if err := fp.dbManager.AddDataServiceDetails(dataDetails); err != nil {
		fp.logger.Error(fmt.Sprintf("Failed to store data service details: %v", err), "file_processor")
		return fmt.Errorf("failed to store data service details: %w", err)
	}
	fp.logger.Info("Data service details stored successfully", "file_processor")

	// Step 6: Clean up all temp files in the upload group
	fp.logger.Info(fmt.Sprintf("Cleaning up %d temporary files...", len(sessions)), "file_processor")
	for _, session := range sessions {
		if err := os.Remove(session.TempFilePath); err != nil {
			fp.logger.Warn(fmt.Sprintf("Failed to remove temp file %s: %v", session.TempFilePath, err), "file_processor")
		}
		if err := fp.dbManager.DeleteUploadSession(session.SessionID); err != nil {
			fp.logger.Warn(fmt.Sprintf("Failed to delete upload session %s: %v", session.SessionID, err), "file_processor")
		}
	}

	// Clean up upload group directory
	if len(sessions) > 0 {
		uploadGroupDir := filepath.Dir(sessions[0].TempFilePath)
		if err := os.RemoveAll(uploadGroupDir); err != nil {
			fp.logger.Warn(fmt.Sprintf("Failed to remove upload group directory %s: %v", uploadGroupDir, err), "file_processor")
		}
	}

	fp.logger.Info(fmt.Sprintf("✓ File processing completed successfully!\n"+
		"  Files: %d\n"+
		"  Original size: %d bytes\n"+
		"  Compressed: %d bytes (%.1f%%)\n"+
		"  Encrypted: %d bytes\n"+
		"  Hash: %s",
		fileCount, originalSize, compressedSize, compressionRatio, encryptedSize, fileHash), "file_processor")

	return nil
}

// generateEncryptionKey generates a random passphrase and derives an AES-256 key
func (fp *FileProcessor) generateEncryptionKey() (passphrase string, keyData []byte, err error) {
	// Generate a random 32-byte passphrase
	passphraseBytes := make([]byte, 32)
	if _, err := rand.Read(passphraseBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate passphrase: %w", err)
	}
	passphrase = hex.EncodeToString(passphraseBytes)

	// Derive AES-256 key using PBKDF2
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive key using PBKDF2 with SHA-256
	keyData = pbkdf2.Key([]byte(passphrase), salt, 100000, 32, sha256.New)

	// Prepend salt to keyData for storage (first 16 bytes = salt, rest = derived key params)
	fullKeyData := append(salt, keyData...)

	return passphrase, fullKeyData, nil
}

// encryptFile encrypts a file using AES-256-GCM
func (fp *FileProcessor) encryptFile(inputPath, outputPath string, keyData []byte) error {
	// Extract salt and derive key
	if len(keyData) < 48 {
		return fmt.Errorf("invalid key data length")
	}

	// Key data format: [16 bytes salt][32 bytes key]
	key := keyData[16:48]

	// Read input file
	plaintext, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt data
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// Write encrypted data to output file
	if err := os.WriteFile(outputPath, ciphertext, 0644); err != nil {
		return fmt.Errorf("failed to write encrypted file: %w", err)
	}

	return nil
}

// DecryptFile decrypts a file using AES-256-GCM
func (fp *FileProcessor) DecryptFile(encryptedPath, outputPath string, passphrase string) error {
	// Get service ID from path (extract from filename)
	// This is a simplified approach - in production, you'd pass serviceID as parameter
	var serviceID int64
	fmt.Sscanf(filepath.Base(encryptedPath), "service_%d_encrypted.dat", &serviceID)

	// Get encryption key from database
	encryptionKey, err := fp.dbManager.GetEncryptionKey(serviceID)
	if err != nil || encryptionKey == nil {
		return fmt.Errorf("encryption key not found for service %d", serviceID)
	}

	// Verify passphrase
	if hashPassphrase(passphrase) != encryptionKey.PassphraseHash {
		return fmt.Errorf("invalid passphrase")
	}

	// Extract key data from stored format: passphrase|keydata
	parts := splitKeyData(encryptionKey.KeyData)
	if len(parts) != 2 {
		return fmt.Errorf("invalid key data format")
	}

	// Decode key data
	keyData, err := hex.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("failed to decode key data: %w", err)
	}

	// Extract key
	if len(keyData) < 48 {
		return fmt.Errorf("invalid key data length")
	}
	key := keyData[16:48]

	// Read encrypted file
	ciphertext, err := os.ReadFile(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to read encrypted file: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt data
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("failed to decrypt: %w", err)
	}

	// Write decrypted data to output file
	if err := os.WriteFile(outputPath, plaintext, 0644); err != nil {
		return fmt.Errorf("failed to write decrypted file: %w", err)
	}

	return nil
}

// copyFile copies a file from src to dst
func (fp *FileProcessor) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// hashPassphrase creates a SHA-256 hash of the passphrase for verification
func hashPassphrase(passphrase string) string {
	hash := sha256.Sum256([]byte(passphrase))
	return hex.EncodeToString(hash[:])
}

// GetPassphrase retrieves the passphrase for a service
func (fp *FileProcessor) GetPassphrase(serviceID int64) (string, error) {
	// Get encryption key from database
	encryptionKey, err := fp.dbManager.GetEncryptionKey(serviceID)
	if err != nil || encryptionKey == nil {
		return "", fmt.Errorf("encryption key not found for service %d", serviceID)
	}

	// Extract passphrase from stored format: passphrase|keydata
	parts := splitKeyData(encryptionKey.KeyData)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid key data format")
	}

	return parts[0], nil
}

// splitKeyData splits the stored key data into passphrase and key
func splitKeyData(keyData string) []string {
	// Find the first occurrence of | separator
	for i := 0; i < len(keyData); i++ {
		if keyData[i] == '|' {
			return []string{keyData[:i], keyData[i+1:]}
		}
	}
	return []string{keyData}
}
