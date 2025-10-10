package crypto

import (
	"crypto/ed25519"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateKeypair(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Verify key sizes
	if len(keyPair.PublicKey) != ed25519.PublicKeySize {
		t.Errorf("Invalid public key size: expected %d, got %d",
			ed25519.PublicKeySize, len(keyPair.PublicKey))
	}

	if len(keyPair.PrivateKey) != ed25519.PrivateKeySize {
		t.Errorf("Invalid private key size: expected %d, got %d",
			ed25519.PrivateKeySize, len(keyPair.PrivateKey))
	}
}

func TestSaveAndLoadKeys(t *testing.T) {
	// Create temporary directory for test
	tempDir := t.TempDir()

	// Generate keypair
	originalKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Save keys
	err = SaveKeys(originalKeyPair, tempDir)
	if err != nil {
		t.Fatalf("Failed to save keys: %v", err)
	}

	// Verify files exist
	publicKeyPath := filepath.Join(tempDir, "ed25519_public.key")
	privateKeyPath := filepath.Join(tempDir, "ed25519_private.key")

	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Errorf("Public key file was not created")
	}

	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Errorf("Private key file was not created")
	}

	// Load keys
	loadedKeyPair, err := LoadKeys(tempDir)
	if err != nil {
		t.Fatalf("Failed to load keys: %v", err)
	}

	// Verify loaded keys match original
	if !bytesEqual(originalKeyPair.PublicKey, loadedKeyPair.PublicKey) {
		t.Errorf("Loaded public key does not match original")
	}

	if !bytesEqual(originalKeyPair.PrivateKey, loadedKeyPair.PrivateKey) {
		t.Errorf("Loaded private key does not match original")
	}
}

func TestLoadKeysNonexistent(t *testing.T) {
	tempDir := t.TempDir()

	// Try to load from empty directory
	_, err := LoadKeys(tempDir)
	if err == nil {
		t.Errorf("Expected error when loading nonexistent keys, got nil")
	}
}

func TestLoadOrGenerateKeys(t *testing.T) {
	tempDir := t.TempDir()

	// First call should generate new keys
	keyPair1, err := LoadOrGenerateKeys(tempDir)
	if err != nil {
		t.Fatalf("Failed to load or generate keys (first call): %v", err)
	}

	// Second call should load existing keys
	keyPair2, err := LoadOrGenerateKeys(tempDir)
	if err != nil {
		t.Fatalf("Failed to load or generate keys (second call): %v", err)
	}

	// Verify both keypairs are identical
	if !bytesEqual(keyPair1.PublicKey, keyPair2.PublicKey) {
		t.Errorf("Public keys do not match between first and second call")
	}

	if !bytesEqual(keyPair1.PrivateKey, keyPair2.PrivateKey) {
		t.Errorf("Private keys do not match between first and second call")
	}
}

func TestDerivePeerID(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	peerID := DerivePeerID(keyPair.PublicKey)

	// Verify peer ID format (hex-encoded SHA1)
	if len(peerID) != 40 { // SHA1 is 20 bytes, hex-encoded = 40 chars
		t.Errorf("Invalid peer ID length: expected 40, got %d", len(peerID))
	}

	// Verify peer ID is deterministic
	peerID2 := DerivePeerID(keyPair.PublicKey)
	if peerID != peerID2 {
		t.Errorf("Peer ID is not deterministic")
	}

	// Verify peer ID matches manual calculation
	expectedHash := sha1.Sum(keyPair.PublicKey)
	expectedPeerID := hex.EncodeToString(expectedHash[:])
	if peerID != expectedPeerID {
		t.Errorf("Peer ID does not match expected SHA1: expected %s, got %s",
			expectedPeerID, peerID)
	}
}

func TestDeriveStorageKey(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	storageKey := DeriveStorageKey(keyPair.PublicKey)

	// Verify storage key is 20 bytes (SHA1)
	if len(storageKey) != 20 {
		t.Errorf("Invalid storage key length: expected 20, got %d", len(storageKey))
	}

	// Verify storage key matches peer ID (both are SHA1 of public key)
	peerID := DerivePeerID(keyPair.PublicKey)
	storageKeyHex := hex.EncodeToString(storageKey[:])

	if peerID != storageKeyHex {
		t.Errorf("Storage key does not match peer ID: peer_id=%s, storage_key=%s",
			peerID, storageKeyHex)
	}
}

func TestSignAndVerify(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	message := []byte("Hello, DHT!")

	// Sign message
	signature := keyPair.Sign(message)

	// Verify signature size
	if len(signature) != ed25519.SignatureSize {
		t.Errorf("Invalid signature size: expected %d, got %d",
			ed25519.SignatureSize, len(signature))
	}

	// Verify signature is valid
	if !keyPair.Verify(message, signature) {
		t.Errorf("Valid signature failed verification")
	}

	// Verify tampering is detected
	tamperedMessage := []byte("Hello, DHTx")
	if keyPair.Verify(tamperedMessage, signature) {
		t.Errorf("Tampered message passed verification")
	}

	// Verify wrong signature is detected
	wrongSignature := make([]byte, ed25519.SignatureSize)
	if keyPair.Verify(message, wrongSignature) {
		t.Errorf("Wrong signature passed verification")
	}
}

func TestVerifyWithPublicKey(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	message := []byte("Test message")
	signature := keyPair.Sign(message)

	// Verify with standalone function
	if !VerifyWithPublicKey(keyPair.PublicKey, message, signature) {
		t.Errorf("Valid signature failed verification with standalone function")
	}

	// Verify wrong key is detected
	wrongKeyPair, _ := GenerateKeypair()
	if VerifyWithPublicKey(wrongKeyPair.PublicKey, message, signature) {
		t.Errorf("Signature verified with wrong public key")
	}
}

func TestKeyPairHelperMethods(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Test PublicKeyBytes
	pubKeyBytes := keyPair.PublicKeyBytes()
	if !bytesEqual(pubKeyBytes, keyPair.PublicKey) {
		t.Errorf("PublicKeyBytes() does not match PublicKey")
	}

	// Test PrivateKeyBytes
	privKeyBytes := keyPair.PrivateKeyBytes()
	if !bytesEqual(privKeyBytes, keyPair.PrivateKey) {
		t.Errorf("PrivateKeyBytes() does not match PrivateKey")
	}

	// Test PeerID
	peerID1 := keyPair.PeerID()
	peerID2 := DerivePeerID(keyPair.PublicKey)
	if peerID1 != peerID2 {
		t.Errorf("KeyPair.PeerID() does not match DerivePeerID()")
	}

	// Test StorageKey
	storageKey1 := keyPair.StorageKey()
	storageKey2 := DeriveStorageKey(keyPair.PublicKey)
	if storageKey1 != storageKey2 {
		t.Errorf("KeyPair.StorageKey() does not match DeriveStorageKey()")
	}
}

func TestKeyFilePermissions(t *testing.T) {
	tempDir := t.TempDir()

	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	err = SaveKeys(keyPair, tempDir)
	if err != nil {
		t.Fatalf("Failed to save keys: %v", err)
	}

	// Check public key permissions (should be 0644)
	publicKeyPath := filepath.Join(tempDir, "ed25519_public.key")
	pubInfo, err := os.Stat(publicKeyPath)
	if err != nil {
		t.Fatalf("Failed to stat public key file: %v", err)
	}

	pubMode := pubInfo.Mode().Perm()
	if pubMode != 0644 {
		t.Errorf("Public key has wrong permissions: expected 0644, got %o", pubMode)
	}

	// Check private key permissions (should be 0600)
	privateKeyPath := filepath.Join(tempDir, "ed25519_private.key")
	privInfo, err := os.Stat(privateKeyPath)
	if err != nil {
		t.Fatalf("Failed to stat private key file: %v", err)
	}

	privMode := privInfo.Mode().Perm()
	if privMode != 0600 {
		t.Errorf("Private key has wrong permissions: expected 0600, got %o", privMode)
	}
}

// Helper function to compare byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
