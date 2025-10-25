package keystore

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndUnlockKeystore(t *testing.T) {
	// Generate test keys
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key: %v", err)
	}

	jwtSecret, err := GenerateJWTSecret()
	if err != nil {
		t.Fatalf("Failed to generate JWT secret: %v", err)
	}

	passphrase := "test-passphrase-123"

	// Create keystore
	ks, err := CreateKeystore(passphrase, privKey, pubKey, jwtSecret)
	if err != nil {
		t.Fatalf("Failed to create keystore: %v", err)
	}

	// Verify keystore structure
	if ks.Version != keystoreVersion {
		t.Errorf("Expected version %d, got %d", keystoreVersion, ks.Version)
	}

	if len(ks.Salt) != saltSize {
		t.Errorf("Expected salt size %d, got %d", saltSize, len(ks.Salt))
	}

	if len(ks.Nonce) != nonceSize {
		t.Errorf("Expected nonce size %d, got %d", nonceSize, len(ks.Nonce))
	}

	if len(ks.Data) == 0 {
		t.Error("Encrypted data is empty")
	}

	// Unlock keystore
	data, err := UnlockKeystore(ks, passphrase)
	if err != nil {
		t.Fatalf("Failed to unlock keystore: %v", err)
	}

	// Verify decrypted data matches original
	if !bytes.Equal(data.Ed25519PrivateKey, privKey) {
		t.Error("Ed25519 private key mismatch")
	}

	if !bytes.Equal(data.Ed25519PublicKey, pubKey) {
		t.Error("Ed25519 public key mismatch")
	}

	if !bytes.Equal(data.JWTSecret, jwtSecret) {
		t.Error("JWT secret mismatch")
	}
}

func TestUnlockWithWrongPassphrase(t *testing.T) {
	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	jwtSecret, _ := GenerateJWTSecret()

	ks, err := CreateKeystore("correct-passphrase", privKey, pubKey, jwtSecret)
	if err != nil {
		t.Fatalf("Failed to create keystore: %v", err)
	}

	// Try to unlock with wrong passphrase
	_, err = UnlockKeystore(ks, "wrong-passphrase")
	if err == nil {
		t.Error("Expected error when unlocking with wrong passphrase, got nil")
	}
}

func TestSaveAndLoadKeystore(t *testing.T) {
	tempDir := t.TempDir()
	keystorePath := filepath.Join(tempDir, "keystore.dat")

	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	jwtSecret, _ := GenerateJWTSecret()
	passphrase := "test-passphrase"

	// Create keystore
	ks, err := CreateKeystore(passphrase, privKey, pubKey, jwtSecret)
	if err != nil {
		t.Fatalf("Failed to create keystore: %v", err)
	}

	// Save to file
	if err := SaveKeystore(ks, keystorePath); err != nil {
		t.Fatalf("Failed to save keystore: %v", err)
	}

	// Verify file exists and has correct permissions
	info, err := os.Stat(keystorePath)
	if err != nil {
		t.Fatalf("Failed to stat keystore file: %v", err)
	}

	expectedPerm := os.FileMode(0600)
	if info.Mode().Perm() != expectedPerm {
		t.Errorf("Expected file permissions %v, got %v", expectedPerm, info.Mode().Perm())
	}

	// Load from file
	loadedKS, err := LoadKeystore(keystorePath)
	if err != nil {
		t.Fatalf("Failed to load keystore: %v", err)
	}

	// Verify loaded keystore matches original
	if !bytes.Equal(loadedKS.Salt, ks.Salt) {
		t.Error("Salt mismatch after save/load")
	}

	if !bytes.Equal(loadedKS.Nonce, ks.Nonce) {
		t.Error("Nonce mismatch after save/load")
	}

	if !bytes.Equal(loadedKS.Data, ks.Data) {
		t.Error("Data mismatch after save/load")
	}

	// Unlock loaded keystore
	data, err := UnlockKeystore(loadedKS, passphrase)
	if err != nil {
		t.Fatalf("Failed to unlock loaded keystore: %v", err)
	}

	// Verify data
	if !bytes.Equal(data.Ed25519PrivateKey, privKey) {
		t.Error("Ed25519 private key mismatch after save/load")
	}
}

func TestChangePassphrase(t *testing.T) {
	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	jwtSecret, _ := GenerateJWTSecret()

	oldPassphrase := "old-passphrase"
	newPassphrase := "new-passphrase"

	// Create keystore with old passphrase
	ks, err := CreateKeystore(oldPassphrase, privKey, pubKey, jwtSecret)
	if err != nil {
		t.Fatalf("Failed to create keystore: %v", err)
	}

	// Change passphrase
	newKS, err := ChangePassphrase(ks, oldPassphrase, newPassphrase)
	if err != nil {
		t.Fatalf("Failed to change passphrase: %v", err)
	}

	// Old passphrase should no longer work
	_, err = UnlockKeystore(newKS, oldPassphrase)
	if err == nil {
		t.Error("Old passphrase should not work on new keystore")
	}

	// New passphrase should work
	data, err := UnlockKeystore(newKS, newPassphrase)
	if err != nil {
		t.Fatalf("Failed to unlock with new passphrase: %v", err)
	}

	// Verify data is intact
	if !bytes.Equal(data.Ed25519PrivateKey, privKey) {
		t.Error("Ed25519 private key mismatch after passphrase change")
	}

	if !bytes.Equal(data.Ed25519PublicKey, pubKey) {
		t.Error("Ed25519 public key mismatch after passphrase change")
	}

	if !bytes.Equal(data.JWTSecret, jwtSecret) {
		t.Error("JWT secret mismatch after passphrase change")
	}
}

func TestGenerateJWTSecret(t *testing.T) {
	secret1, err := GenerateJWTSecret()
	if err != nil {
		t.Fatalf("Failed to generate JWT secret: %v", err)
	}

	if len(secret1) != 32 {
		t.Errorf("Expected JWT secret length 32, got %d", len(secret1))
	}

	// Generate another and verify they're different
	secret2, err := GenerateJWTSecret()
	if err != nil {
		t.Fatalf("Failed to generate second JWT secret: %v", err)
	}

	if bytes.Equal(secret1, secret2) {
		t.Error("Two generated JWT secrets should not be identical")
	}
}

func TestInvalidInputs(t *testing.T) {
	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	jwtSecret, _ := GenerateJWTSecret()

	// Empty passphrase
	_, err := CreateKeystore("", privKey, pubKey, jwtSecret)
	if err == nil {
		t.Error("Expected error for empty passphrase")
	}

	// Invalid Ed25519 private key size
	_, err = CreateKeystore("passphrase", []byte("short"), pubKey, jwtSecret)
	if err == nil {
		t.Error("Expected error for invalid Ed25519 private key")
	}

	// Invalid Ed25519 public key size
	_, err = CreateKeystore("passphrase", privKey, []byte("short"), jwtSecret)
	if err == nil {
		t.Error("Expected error for invalid Ed25519 public key")
	}

	// Invalid JWT secret size
	_, err = CreateKeystore("passphrase", privKey, pubKey, []byte("short"))
	if err == nil {
		t.Error("Expected error for invalid JWT secret size")
	}
}
