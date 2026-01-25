package crypto

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestNewInvoiceCryptoManager(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	icm := NewInvoiceCryptoManager(keyPair)
	if icm == nil {
		t.Fatal("NewInvoiceCryptoManager returned nil")
	}

	if icm.keyPair != keyPair {
		t.Error("InvoiceCryptoManager.keyPair not set correctly")
	}
}

func TestEncryptDecryptInvoice(t *testing.T) {
	// Generate keypairs for sender and recipient
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	// Create crypto managers
	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	// Original plaintext
	plaintext := []byte(`{"invoice_id":"INV-001","amount":100.50,"currency":"USDC"}`)

	// Sender encrypts for recipient
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed: %v", err)
	}

	// Verify outputs are not empty
	if len(ciphertext) == 0 {
		t.Error("Ciphertext is empty")
	}
	if nonce == [24]byte{} {
		t.Error("Nonce is zero")
	}
	if ephemeralPubKey == [32]byte{} {
		t.Error("Ephemeral public key is zero")
	}
	if len(signature) == 0 {
		t.Error("Signature is empty")
	}

	// Recipient decrypts
	decrypted, err := recipientCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		signature,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("DecryptInvoice failed: %v", err)
	}

	// Verify decrypted matches original
	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("Decrypted plaintext does not match original:\n  expected: %s\n  got: %s",
			string(plaintext), string(decrypted))
	}
}

func TestEncryptDecryptLargePayload(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	// Create a large payload (e.g., invoice with lots of metadata)
	plaintext := make([]byte, 64*1024) // 64KB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	// Encrypt and decrypt
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed for large payload: %v", err)
	}

	decrypted, err := recipientCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		signature,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("DecryptInvoice failed for large payload: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Large payload decryption failed")
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	wrongKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate wrong keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	wrongCrypto := NewInvoiceCryptoManager(wrongKeyPair)

	plaintext := []byte("Secret invoice data")

	// Encrypt for recipient
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed: %v", err)
	}

	// Try to decrypt with wrong key
	_, err = wrongCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		signature,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Error("Expected decryption to fail with wrong key, but it succeeded")
	}
}

func TestDecryptWithTamperedCiphertext(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	plaintext := []byte("Secret invoice data")

	// Encrypt
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed: %v", err)
	}

	// Tamper with ciphertext
	tamperedCiphertext := make([]byte, len(ciphertext))
	copy(tamperedCiphertext, ciphertext)
	tamperedCiphertext[0] ^= 0xFF // Flip bits in first byte

	// Try to decrypt tampered ciphertext
	_, err = recipientCrypto.DecryptInvoice(
		tamperedCiphertext,
		nonce,
		ephemeralPubKey,
		signature,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Error("Expected decryption to fail with tampered ciphertext, but it succeeded")
	}
}

func TestDecryptWithInvalidSignature(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	plaintext := []byte("Secret invoice data")

	// Encrypt
	ciphertext, nonce, ephemeralPubKey, _, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed: %v", err)
	}

	// Create invalid signature
	invalidSignature := make([]byte, ed25519.SignatureSize)

	// Try to decrypt with invalid signature
	_, err = recipientCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		invalidSignature,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Error("Expected decryption to fail with invalid signature, but it succeeded")
	}
}

func TestDecryptWithWrongSenderPublicKey(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	wrongKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate wrong keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	plaintext := []byte("Secret invoice data")

	// Encrypt
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed: %v", err)
	}

	// Try to decrypt with wrong sender public key (signature verification should fail)
	_, err = recipientCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		signature,
		wrongKeyPair.PublicKey, // Wrong sender public key
	)
	if err == nil {
		t.Error("Expected decryption to fail with wrong sender public key, but it succeeded")
	}
}

func TestEncryptUniqueNonces(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	plaintext := []byte("Test data")

	// Encrypt multiple times and verify nonces are unique
	nonces := make(map[[24]byte]bool)
	for i := 0; i < 100; i++ {
		_, nonce, _, _, err := senderCrypto.EncryptInvoice(
			plaintext,
			recipientKeyPair.PublicKey,
		)
		if err != nil {
			t.Fatalf("EncryptInvoice failed on iteration %d: %v", i, err)
		}

		if nonces[nonce] {
			t.Errorf("Duplicate nonce generated on iteration %d", i)
		}
		nonces[nonce] = true
	}
}

func TestEncryptUniqueEphemeralKeys(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	plaintext := []byte("Test data")

	// Encrypt multiple times and verify ephemeral keys are unique (forward secrecy)
	ephemeralKeys := make(map[[32]byte]bool)
	for i := 0; i < 100; i++ {
		_, _, ephemeralPubKey, _, err := senderCrypto.EncryptInvoice(
			plaintext,
			recipientKeyPair.PublicKey,
		)
		if err != nil {
			t.Fatalf("EncryptInvoice failed on iteration %d: %v", i, err)
		}

		if ephemeralKeys[ephemeralPubKey] {
			t.Errorf("Duplicate ephemeral key generated on iteration %d", i)
		}
		ephemeralKeys[ephemeralPubKey] = true
	}
}

func TestGetPublicKey(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	icm := NewInvoiceCryptoManager(keyPair)
	pubKey := icm.GetPublicKey()

	if !bytes.Equal(pubKey, keyPair.PublicKey) {
		t.Error("GetPublicKey() returned wrong public key")
	}
}

func TestGetPeerID(t *testing.T) {
	keyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	icm := NewInvoiceCryptoManager(keyPair)
	peerID := icm.GetPeerID()
	expectedPeerID := keyPair.PeerID()

	if peerID != expectedPeerID {
		t.Errorf("GetPeerID() returned %s, expected %s", peerID, expectedPeerID)
	}
}

func TestDeriveInvoiceKey(t *testing.T) {
	// Test that deriveInvoiceKey produces consistent output
	sharedSecret := make([]byte, 32)
	for i := range sharedSecret {
		sharedSecret[i] = byte(i)
	}

	key1, err := deriveInvoiceKey(sharedSecret)
	if err != nil {
		t.Fatalf("deriveInvoiceKey failed: %v", err)
	}

	key2, err := deriveInvoiceKey(sharedSecret)
	if err != nil {
		t.Fatalf("deriveInvoiceKey failed on second call: %v", err)
	}

	// Same input should produce same output
	if key1 != key2 {
		t.Error("deriveInvoiceKey is not deterministic")
	}

	// Key should be 32 bytes (256 bits)
	if len(key1) != 32 {
		t.Errorf("Derived key has wrong length: expected 32, got %d", len(key1))
	}

	// Different input should produce different output
	differentSecret := make([]byte, 32)
	for i := range differentSecret {
		differentSecret[i] = byte(255 - i)
	}

	key3, err := deriveInvoiceKey(differentSecret)
	if err != nil {
		t.Fatalf("deriveInvoiceKey failed for different input: %v", err)
	}

	if key1 == key3 {
		t.Error("Different shared secrets produced same key")
	}
}

func TestEncryptEmptyPayload(t *testing.T) {
	// Generate keypairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender keypair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient keypair: %v", err)
	}

	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	// Empty plaintext
	plaintext := []byte{}

	// Encrypt
	ciphertext, nonce, ephemeralPubKey, signature, err := senderCrypto.EncryptInvoice(
		plaintext,
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("EncryptInvoice failed for empty payload: %v", err)
	}

	// Decrypt
	decrypted, err := recipientCrypto.DecryptInvoice(
		ciphertext,
		nonce,
		ephemeralPubKey,
		signature,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("DecryptInvoice failed for empty payload: %v", err)
	}

	if len(decrypted) != 0 {
		t.Error("Decrypted empty payload is not empty")
	}
}

// Benchmark encryption
func BenchmarkEncryptInvoice(b *testing.B) {
	senderKeyPair, _ := GenerateKeypair()
	recipientKeyPair, _ := GenerateKeypair()
	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)

	plaintext := []byte(`{"invoice_id":"INV-001","amount":100.50,"currency":"USDC","description":"Service payment"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _, _ = senderCrypto.EncryptInvoice(plaintext, recipientKeyPair.PublicKey)
	}
}

// Benchmark decryption
func BenchmarkDecryptInvoice(b *testing.B) {
	senderKeyPair, _ := GenerateKeypair()
	recipientKeyPair, _ := GenerateKeypair()
	senderCrypto := NewInvoiceCryptoManager(senderKeyPair)
	recipientCrypto := NewInvoiceCryptoManager(recipientKeyPair)

	plaintext := []byte(`{"invoice_id":"INV-001","amount":100.50,"currency":"USDC","description":"Service payment"}`)
	ciphertext, nonce, ephemeralPubKey, signature, _ := senderCrypto.EncryptInvoice(plaintext, recipientKeyPair.PublicKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = recipientCrypto.DecryptInvoice(ciphertext, nonce, ephemeralPubKey, signature, senderKeyPair.PublicKey)
	}
}
