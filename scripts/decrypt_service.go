package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

func RunDecryptService(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: go run main.go decrypt-service <service_id> <output_dir>")
		fmt.Println("Example: go run main.go decrypt-service 1 ./decrypted_output")
		os.Exit(1)
	}

	serviceID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fmt.Printf("Invalid service ID: %v\n", err)
		os.Exit(1)
	}

	outputDir := args[1]

	// Get database path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	dbPath := filepath.Join(homeDir, "Library/Application Support/remote-network/remote-network.db")

	// Connect to database (using modernc.org/sqlite driver name)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get service details
	var filePath, encryptedPath, hash, compressionType string
	var sizeBytes, originalSizeBytes int64
	var encryptionKeyID *int64

	query := `SELECT file_path, encrypted_path, hash, compression_type, encryption_key_id,
	                 size_bytes, original_size_bytes
	          FROM data_service_details WHERE service_id = ?`
	err = db.QueryRow(query, serviceID).Scan(&filePath, &encryptedPath, &hash, &compressionType,
		&encryptionKeyID, &sizeBytes, &originalSizeBytes)
	if err != nil {
		fmt.Printf("Failed to get service details: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Service Details ===")
	fmt.Printf("Service ID: %d\n", serviceID)
	fmt.Printf("File(s): %s\n", filePath)
	fmt.Printf("Encrypted Size: %d bytes (%.2f KB)\n", sizeBytes, float64(sizeBytes)/1024)
	fmt.Printf("Original Size: %d bytes (%.2f KB)\n", originalSizeBytes, float64(originalSizeBytes)/1024)
	fmt.Printf("Compression: %s\n", compressionType)
	fmt.Printf("Hash: %s\n", hash)
	fmt.Println()

	// Get encryption key
	if encryptionKeyID == nil {
		fmt.Println("No encryption key found")
		os.Exit(1)
	}

	var keyData string
	keyQuery := `SELECT key_data FROM encryption_keys WHERE id = ?`
	err = db.QueryRow(keyQuery, *encryptionKeyID).Scan(&keyData)
	if err != nil {
		fmt.Printf("Failed to get encryption key: %v\n", err)
		os.Exit(1)
	}

	// Extract passphrase and key from stored format: passphrase|keydata
	parts := splitKeyData(keyData)
	if len(parts) != 2 {
		fmt.Println("Invalid key data format")
		os.Exit(1)
	}

	passphrase := parts[0]
	keyDataHex := parts[1]

	fmt.Println("=== Decryption Info ===")
	fmt.Printf("Passphrase: %s\n", passphrase)
	fmt.Printf("Passphrase Hash: %s\n", hashPassphrase(passphrase))
	fmt.Println()

	// Decode key data
	keyBytes, err := hex.DecodeString(keyDataHex)
	if err != nil {
		fmt.Printf("Failed to decode key data: %v\n", err)
		os.Exit(1)
	}

	// Decrypt the file
	fmt.Println("=== Decryption Process ===")
	fmt.Printf("Reading encrypted file: %s\n", encryptedPath)

	decryptedPath := filepath.Join(outputDir, "decrypted.tar.gz")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	if err := decryptFile(encryptedPath, decryptedPath, keyBytes); err != nil {
		fmt.Printf("Failed to decrypt file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Decrypted to: %s\n", decryptedPath)

	// Get decrypted file size
	decryptedInfo, err := os.Stat(decryptedPath)
	if err != nil {
		fmt.Printf("Failed to stat decrypted file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Decrypted size: %d bytes (%.2f KB)\n", decryptedInfo.Size(), float64(decryptedInfo.Size())/1024)
	fmt.Println()

	// Decompress the tar.gz
	fmt.Println("=== Decompression Process ===")
	extractDir := filepath.Join(outputDir, "extracted")
	if err := decompress(decryptedPath, extractDir); err != nil {
		fmt.Printf("Failed to decompress: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Extracted to: %s\n", extractDir)

	// List extracted files
	fmt.Println("\n=== Extracted Files ===")
	err = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel(extractDir, path)
			fmt.Printf("  %s (%d bytes)\n", relPath, info.Size())
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Failed to list files: %v\n", err)
	}

	fmt.Println("\n✓ Decryption and decompression completed successfully!")
}

// decryptFile decrypts a file using AES-256-GCM
func decryptFile(encryptedPath, outputPath string, keyData []byte) error {
	// Extract key from keyData
	// Key data format: [16 bytes salt][32 bytes key]
	if len(keyData) < 48 {
		return fmt.Errorf("invalid key data length: %d", len(keyData))
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

// decompress decompresses a tar.gz archive
func decompress(archivePath, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Use system tar command
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", targetDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract archive: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// splitKeyData splits the stored key data into passphrase and key
func splitKeyData(keyData string) []string {
	for i := 0; i < len(keyData); i++ {
		if keyData[i] == '|' {
			return []string{keyData[:i], keyData[i+1:]}
		}
	}
	return []string{keyData}
}

// hashPassphrase creates a SHA-256 hash of the passphrase for verification
func hashPassphrase(passphrase string) string {
	hash := sha256.Sum256([]byte(passphrase))
	return hex.EncodeToString(hash[:])
}
