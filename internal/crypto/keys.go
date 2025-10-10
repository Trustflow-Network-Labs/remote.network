package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// KeyPair represents an Ed25519 keypair for BEP_44
type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateKeypair generates a new Ed25519 keypair
func GenerateKeypair() (*KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Ed25519 keypair: %v", err)
	}

	return &KeyPair{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, nil
}

// SaveKeys saves the keypair to disk with proper permissions
func SaveKeys(keyPair *KeyPair, dirPath string) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return fmt.Errorf("failed to create keys directory: %v", err)
	}

	// Save public key (0644 - readable by all)
	publicKeyPath := filepath.Join(dirPath, "ed25519_public.key")
	if err := os.WriteFile(publicKeyPath, keyPair.PublicKey, 0644); err != nil {
		return fmt.Errorf("failed to save public key: %v", err)
	}

	// Save private key (0600 - readable only by owner)
	privateKeyPath := filepath.Join(dirPath, "ed25519_private.key")
	if err := os.WriteFile(privateKeyPath, keyPair.PrivateKey, 0600); err != nil {
		return fmt.Errorf("failed to save private key: %v", err)
	}

	return nil
}

// LoadKeys loads an existing keypair from disk
func LoadKeys(dirPath string) (*KeyPair, error) {
	publicKeyPath := filepath.Join(dirPath, "ed25519_public.key")
	privateKeyPath := filepath.Join(dirPath, "ed25519_private.key")

	// Check if both files exist
	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("public key file not found at %s", publicKeyPath)
	}
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("private key file not found at %s", privateKeyPath)
	}

	// Load public key
	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key: %v", err)
	}

	// Load private key
	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %v", err)
	}

	// Validate key sizes
	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: expected %d, got %d",
			ed25519.PublicKeySize, len(publicKeyBytes))
	}
	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: expected %d, got %d",
			ed25519.PrivateKeySize, len(privateKeyBytes))
	}

	return &KeyPair{
		PublicKey:  ed25519.PublicKey(publicKeyBytes),
		PrivateKey: ed25519.PrivateKey(privateKeyBytes),
	}, nil
}

// LoadOrGenerateKeys loads existing keys or generates new ones if not found
func LoadOrGenerateKeys(dirPath string) (*KeyPair, error) {
	// Try to load existing keys
	keyPair, err := LoadKeys(dirPath)
	if err == nil {
		return keyPair, nil
	}

	// Keys don't exist, generate new ones
	keyPair, err = GenerateKeypair()
	if err != nil {
		return nil, err
	}

	// Save the new keys
	if err := SaveKeys(keyPair, dirPath); err != nil {
		return nil, err
	}

	return keyPair, nil
}

// DerivePeerID derives a peer ID from a public key using SHA1
// This is the application-layer peer identity (not DHT node ID)
func DerivePeerID(publicKey ed25519.PublicKey) string {
	hash := sha1.Sum(publicKey)
	return hex.EncodeToString(hash[:])
}

// DeriveStorageKey derives the DHT storage key from a public key
// In BEP_44, the storage key is SHA1(public_key)
// This is the same as peer ID, but semantically different:
// - Peer ID: application identity
// - Storage Key: DHT lookup key
func DeriveStorageKey(publicKey ed25519.PublicKey) [20]byte {
	return sha1.Sum(publicKey)
}

// Sign signs a message with the private key (Ed25519 signature)
func (kp *KeyPair) Sign(message []byte) []byte {
	return ed25519.Sign(kp.PrivateKey, message)
}

// Verify verifies a signature against a message using the public key
func (kp *KeyPair) Verify(message, signature []byte) bool {
	return ed25519.Verify(kp.PublicKey, message, signature)
}

// VerifyWithPublicKey verifies a signature using a standalone public key
func VerifyWithPublicKey(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// PublicKeyBytes returns the public key as a byte slice
func (kp *KeyPair) PublicKeyBytes() []byte {
	return []byte(kp.PublicKey)
}

// PrivateKeyBytes returns the private key as a byte slice
func (kp *KeyPair) PrivateKeyBytes() []byte {
	return []byte(kp.PrivateKey)
}

// PeerID returns the derived peer ID for this keypair
func (kp *KeyPair) PeerID() string {
	return DerivePeerID(kp.PublicKey)
}

// StorageKey returns the DHT storage key for this keypair
func (kp *KeyPair) StorageKey() [20]byte {
	return DeriveStorageKey(kp.PublicKey)
}
