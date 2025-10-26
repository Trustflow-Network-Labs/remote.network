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

// ProcessUploadedFile processes a file after upload completion
// Steps: compress, encrypt, hash, store
func (fp *FileProcessor) ProcessUploadedFile(sessionID string, serviceID int64) error {
	fp.logger.Info(fmt.Sprintf("Processing uploaded file for service %d, session %s", serviceID, sessionID), "file_processor")

	// Get upload session
	session, err := fp.dbManager.GetUploadSession(sessionID)
	if err != nil || session == nil {
		return fmt.Errorf("upload session not found: %s", sessionID)
	}

	// Get temp file path
	tempFilePath := session.TempFilePath
	if _, err := os.Stat(tempFilePath); os.IsNotExist(err) {
		return fmt.Errorf("temp file not found: %s", tempFilePath)
	}

	// Get original file size
	fileInfo, err := os.Stat(tempFilePath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	originalSize := fileInfo.Size()

	// Step 1: Compress the file
	fp.logger.Info("Compressing file...", "file_processor")
	compressedPath := tempFilePath + ".tar.gz"
	if err := utils.Compress(tempFilePath, compressedPath); err != nil {
		return fmt.Errorf("failed to compress file: %w", err)
	}
	defer os.Remove(compressedPath) // Clean up after encryption

	// Get compressed file size
	compressedInfo, err := os.Stat(compressedPath)
	if err != nil {
		return fmt.Errorf("failed to get compressed file info: %w", err)
	}
	compressedSize := compressedInfo.Size()

	// Step 2: Generate encryption key and passphrase
	fp.logger.Info("Generating encryption key...", "file_processor")
	passphrase, keyData, err := fp.generateEncryptionKey()
	if err != nil {
		return fmt.Errorf("failed to generate encryption key: %w", err)
	}

	// Store encryption key in database
	// Format: passphrase|keydata (separated by |)
	encryptionKey := &database.EncryptionKey{
		ServiceID:      serviceID,
		PassphraseHash: hashPassphrase(passphrase),
		KeyData:        passphrase + "|" + hex.EncodeToString(keyData), // Store both passphrase and key data
	}
	if err := fp.dbManager.AddEncryptionKey(encryptionKey); err != nil {
		return fmt.Errorf("failed to store encryption key: %w", err)
	}

	// Step 3: Encrypt the compressed file
	fp.logger.Info("Encrypting file...", "file_processor")
	encryptedPath := filepath.Join(fp.storageDir, fmt.Sprintf("service_%d_encrypted.dat", serviceID))
	if err := fp.encryptFile(compressedPath, encryptedPath, keyData); err != nil {
		return fmt.Errorf("failed to encrypt file: %w", err)
	}

	// Get encrypted file size
	encryptedInfo, err := os.Stat(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to get encrypted file info: %w", err)
	}
	encryptedSize := encryptedInfo.Size()

	// Step 4: Calculate BLAKE3 hash of encrypted file
	fp.logger.Info("Calculating file hash...", "file_processor")
	fileHash, err := utils.HashFileToCID(encryptedPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	// Step 5: Store data service details
	dataDetails := &database.DataServiceDetails{
		ServiceID:         serviceID,
		FilePath:          session.Filename,
		EncryptedPath:     encryptedPath,
		Hash:              fileHash,
		CompressionType:   "tar.gz",
		EncryptionKeyID:   &encryptionKey.ID,
		SizeBytes:         encryptedSize,
		OriginalSizeBytes: originalSize,
		UploadCompleted:   true,
	}
	if err := fp.dbManager.AddDataServiceDetails(dataDetails); err != nil {
		return fmt.Errorf("failed to store data service details: %w", err)
	}

	// Step 6: Clean up temp files
	os.Remove(tempFilePath)
	fp.dbManager.DeleteUploadSession(sessionID)

	fp.logger.Info(fmt.Sprintf("File processing completed: original=%d bytes, compressed=%d bytes, encrypted=%d bytes, hash=%s",
		originalSize, compressedSize, encryptedSize, fileHash), "file_processor")

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
