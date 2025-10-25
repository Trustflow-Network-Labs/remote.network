package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/argon2"
)

// Keystore represents an encrypted storage for sensitive keys
type Keystore struct {
	Version int    `json:"version"` // Keystore format version
	Salt    []byte `json:"salt"`    // Salt for key derivation (32 bytes)
	Nonce   []byte `json:"nonce"`   // Nonce for AES-GCM (12 bytes)
	Data    []byte `json:"data"`    // Encrypted data
}

// KeystoreData contains the decrypted keystore contents
type KeystoreData struct {
	Ed25519PrivateKey []byte `json:"ed25519_private_key"` // 64 bytes
	Ed25519PublicKey  []byte `json:"ed25519_public_key"`  // 32 bytes
	JWTSecret         []byte `json:"jwt_secret"`          // 32 bytes
}

const (
	// Argon2id parameters (recommended by OWASP)
	argon2Time      = 3           // Number of iterations
	argon2Memory    = 64 * 1024   // Memory in KiB (64 MB)
	argon2Threads   = 4           // Number of threads
	argon2KeyLength = 32          // Output key length (256 bits for AES-256)

	// Salt and nonce sizes
	saltSize  = 32 // 256 bits
	nonceSize = 12 // 96 bits (standard for AES-GCM)

	// Current keystore format version
	keystoreVersion = 1
)

// deriveKey derives an encryption key from a passphrase using Argon2id
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey(
		[]byte(passphrase),
		salt,
		argon2Time,
		argon2Memory,
		argon2Threads,
		argon2KeyLength,
	)
}

// CreateKeystore creates a new encrypted keystore
func CreateKeystore(passphrase string, ed25519PrivKey ed25519.PrivateKey, ed25519PubKey ed25519.PublicKey, jwtSecret []byte) (*Keystore, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase cannot be empty")
	}

	if len(ed25519PrivKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key size: %d", len(ed25519PrivKey))
	}

	if len(ed25519PubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 public key size: %d", len(ed25519PubKey))
	}

	if len(jwtSecret) != 32 {
		return nil, fmt.Errorf("JWT secret must be 32 bytes, got %d", len(jwtSecret))
	}

	// Generate random salt
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %v", err)
	}

	// Derive encryption key from passphrase
	key := deriveKey(passphrase, salt)

	// Create AES-256-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	// Generate random nonce
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Prepare data to encrypt
	data := &KeystoreData{
		Ed25519PrivateKey: ed25519PrivKey,
		Ed25519PublicKey:  ed25519PubKey,
		JWTSecret:         jwtSecret,
	}

	// Serialize to JSON
	plaintext, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal keystore data: %v", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return &Keystore{
		Version: keystoreVersion,
		Salt:    salt,
		Nonce:   nonce,
		Data:    ciphertext,
	}, nil
}

// UnlockKeystore decrypts the keystore using the passphrase
func UnlockKeystore(ks *Keystore, passphrase string) (*KeystoreData, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("passphrase cannot be empty")
	}

	if ks.Version != keystoreVersion {
		return nil, fmt.Errorf("unsupported keystore version: %d", ks.Version)
	}

	if len(ks.Salt) != saltSize {
		return nil, fmt.Errorf("invalid salt size: %d", len(ks.Salt))
	}

	if len(ks.Nonce) != nonceSize {
		return nil, fmt.Errorf("invalid nonce size: %d", len(ks.Nonce))
	}

	// Derive decryption key
	key := deriveKey(passphrase, ks.Salt)

	// Create AES-256-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	// Decrypt
	plaintext, err := gcm.Open(nil, ks.Nonce, ks.Data, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (incorrect passphrase?): %v", err)
	}

	// Deserialize
	var data KeystoreData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal keystore data: %v", err)
	}

	// Validate key sizes
	if len(data.Ed25519PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("corrupted keystore: invalid Ed25519 private key size")
	}

	if len(data.Ed25519PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("corrupted keystore: invalid Ed25519 public key size")
	}

	if len(data.JWTSecret) != 32 {
		return nil, fmt.Errorf("corrupted keystore: invalid JWT secret size")
	}

	return &data, nil
}

// SaveKeystore saves the keystore to a file
func SaveKeystore(ks *Keystore, path string) error {
	// Serialize to JSON
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal keystore: %v", err)
	}

	// Write to file with secure permissions (only owner can read/write)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write keystore file: %v", err)
	}

	return nil
}

// LoadKeystore loads a keystore from a file
func LoadKeystore(path string) (*Keystore, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read keystore file: %v", err)
	}

	// Deserialize
	var ks Keystore
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal keystore: %v", err)
	}

	return &ks, nil
}

// ChangePassphrase changes the passphrase of an existing keystore
func ChangePassphrase(ks *Keystore, oldPassphrase, newPassphrase string) (*Keystore, error) {
	// Decrypt with old passphrase
	data, err := UnlockKeystore(ks, oldPassphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to unlock with old passphrase: %v", err)
	}

	// Re-encrypt with new passphrase
	newKS, err := CreateKeystore(
		newPassphrase,
		data.Ed25519PrivateKey,
		data.Ed25519PublicKey,
		data.JWTSecret,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new keystore: %v", err)
	}

	return newKS, nil
}

// GenerateJWTSecret generates a cryptographically secure 256-bit random secret
func GenerateJWTSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("failed to generate JWT secret: %v", err)
	}
	return secret, nil
}
