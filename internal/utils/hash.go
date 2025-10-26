package utils

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// HashFileToCID calculates the BLAKE3 hash of a file and returns it as a hex string (CID-like format)
func HashFileToCID(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	hash := hasher.Sum(nil)
	return hex.EncodeToString(hash), nil
}

// HashBytes calculates the BLAKE3 hash of a byte slice
func HashBytes(data []byte) string {
	hasher := blake3.New()
	hasher.Write(data)
	hash := hasher.Sum(nil)
	return hex.EncodeToString(hash)
}

// HashString calculates the BLAKE3 hash of a string
func HashString(data string) string {
	return HashBytes([]byte(data))
}

// VerifyFileHash verifies that a file matches the expected BLAKE3 hash
func VerifyFileHash(filePath, expectedHash string) (bool, error) {
	actualHash, err := HashFileToCID(filePath)
	if err != nil {
		return false, err
	}
	return actualHash == expectedHash, nil
}
