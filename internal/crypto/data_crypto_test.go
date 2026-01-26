package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

// TestGenerateAndDeriveKey tests the full round-trip of ECDH key exchange
func TestGenerateAndDeriveKey(t *testing.T) {
	// Create sender and recipient key pairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender key pair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient key pair: %v", err)
	}

	// Create crypto managers
	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)
	recipientCrypto := NewDataTransferCryptoManager(recipientKeyPair)

	// Sender generates key exchange
	ephemeralPubKey, signature, timestamp, senderKey, err := senderCrypto.GenerateKeyExchange(
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Failed to generate key exchange: %v", err)
	}

	// Verify ephemeral public key is not zero
	if ephemeralPubKey == [32]byte{} {
		t.Fatal("Ephemeral public key is zero")
	}

	// Verify signature is not empty
	if len(signature) == 0 {
		t.Fatal("Signature is empty")
	}

	// Verify derived key is not zero
	if senderKey == [32]byte{} {
		t.Fatal("Sender derived key is zero")
	}

	// Recipient derives same key
	recipientKey, err := recipientCrypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		timestamp,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Failed to derive decryption key: %v", err)
	}

	// Verify both parties derived the same key
	if senderKey != recipientKey {
		t.Fatal("Sender and recipient derived different keys")
	}
}

// TestSignatureVerification tests that invalid signatures are rejected
func TestSignatureVerification(t *testing.T) {
	// Create sender and recipient key pairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender key pair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient key pair: %v", err)
	}

	// Create crypto managers
	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)
	recipientCrypto := NewDataTransferCryptoManager(recipientKeyPair)

	// Sender generates key exchange
	ephemeralPubKey, signature, timestamp, _, err := senderCrypto.GenerateKeyExchange(
		recipientKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Failed to generate key exchange: %v", err)
	}

	// Test 1: Tampered signature
	tamperedSignature := make([]byte, len(signature))
	copy(tamperedSignature, signature)
	tamperedSignature[0] ^= 0xFF // Flip bits in first byte

	_, err = recipientCrypto.DeriveDecryptionKey(
		ephemeralPubKey,
		tamperedSignature,
		timestamp,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Fatal("Expected error for tampered signature, got nil")
	}
	if err.Error() != "signature verification failed" {
		t.Fatalf("Expected signature verification error, got: %v", err)
	}

	// Test 2: Wrong sender public key
	wrongSenderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate wrong sender key pair: %v", err)
	}

	_, err = recipientCrypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		timestamp,
		wrongSenderKeyPair.PublicKey,
	)
	if err == nil {
		t.Fatal("Expected error for wrong sender public key, got nil")
	}
	if err.Error() != "signature verification failed" {
		t.Fatalf("Expected signature verification error, got: %v", err)
	}
}

// TestReplayProtection tests that old timestamps are rejected
func TestReplayProtection(t *testing.T) {
	// Create sender and recipient key pairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender key pair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient key pair: %v", err)
	}

	recipientCrypto := NewDataTransferCryptoManager(recipientKeyPair)

	// Test 1: Old timestamp (6 minutes ago - outside 5-minute window)
	oldTimestamp := time.Now().Unix() - 360
	var ephemeralPubKey [32]byte
	rand.Read(ephemeralPubKey[:])

	// Create a valid signature for the old timestamp
	signaturePayload := make([]byte, 0, 32+8)
	signaturePayload = append(signaturePayload, ephemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, int64ToBytes(oldTimestamp)...)
	signature := ed25519.Sign(senderKeyPair.PrivateKey, signaturePayload)

	_, err = recipientCrypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		oldTimestamp,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Fatal("Expected error for old timestamp, got nil")
	}
	if err.Error() != "timestamp outside valid window (replay protection)" {
		t.Fatalf("Expected replay protection error, got: %v", err)
	}

	// Test 2: Future timestamp (2 minutes ahead - outside 1-minute tolerance)
	futureTimestamp := time.Now().Unix() + 120
	signaturePayload = make([]byte, 0, 32+8)
	signaturePayload = append(signaturePayload, ephemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, int64ToBytes(futureTimestamp)...)
	signature = ed25519.Sign(senderKeyPair.PrivateKey, signaturePayload)

	_, err = recipientCrypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		futureTimestamp,
		senderKeyPair.PublicKey,
	)
	if err == nil {
		t.Fatal("Expected error for future timestamp, got nil")
	}
	if err.Error() != "timestamp outside valid window (replay protection)" {
		t.Fatalf("Expected replay protection error, got: %v", err)
	}
}

// TestKeyUniqueness tests that each transfer gets a unique ephemeral key
func TestKeyUniqueness(t *testing.T) {
	// Create sender and recipient key pairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender key pair: %v", err)
	}

	recipientKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient key pair: %v", err)
	}

	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)

	// Generate two key exchanges
	ephemeralPubKey1, _, _, key1, err := senderCrypto.GenerateKeyExchange(recipientKeyPair.PublicKey)
	if err != nil {
		t.Fatalf("Failed to generate first key exchange: %v", err)
	}

	ephemeralPubKey2, _, _, key2, err := senderCrypto.GenerateKeyExchange(recipientKeyPair.PublicKey)
	if err != nil {
		t.Fatalf("Failed to generate second key exchange: %v", err)
	}

	// Verify ephemeral public keys are different
	if ephemeralPubKey1 == ephemeralPubKey2 {
		t.Fatal("Ephemeral public keys should be unique for each transfer")
	}

	// Verify derived keys are different
	if key1 == key2 {
		t.Fatal("Derived keys should be unique for each transfer")
	}
}

// TestWrongRecipientKey tests that using wrong recipient key produces different keys
func TestWrongRecipientKey(t *testing.T) {
	// Create sender and two different recipient key pairs
	senderKeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate sender key pair: %v", err)
	}

	recipient1KeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient1 key pair: %v", err)
	}

	recipient2KeyPair, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate recipient2 key pair: %v", err)
	}

	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)
	recipient1Crypto := NewDataTransferCryptoManager(recipient1KeyPair)
	recipient2Crypto := NewDataTransferCryptoManager(recipient2KeyPair)

	// Sender generates key exchange for recipient1
	ephemeralPubKey, signature, timestamp, senderKey, err := senderCrypto.GenerateKeyExchange(
		recipient1KeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Failed to generate key exchange: %v", err)
	}

	// Recipient1 can derive the correct key
	recipient1Key, err := recipient1Crypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		timestamp,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Recipient1 failed to derive key: %v", err)
	}

	if senderKey != recipient1Key {
		t.Fatal("Recipient1 derived key does not match sender key")
	}

	// Recipient2 should derive a different key (because ECDH uses different recipient key)
	recipient2Key, err := recipient2Crypto.DeriveDecryptionKey(
		ephemeralPubKey,
		signature,
		timestamp,
		senderKeyPair.PublicKey,
	)
	if err != nil {
		t.Fatalf("Recipient2 failed to derive key: %v", err)
	}

	if senderKey == recipient2Key {
		t.Fatal("Recipient2 should derive a different key than the sender intended")
	}
}

// BenchmarkGenerateKeyExchange benchmarks key exchange generation
func BenchmarkGenerateKeyExchange(b *testing.B) {
	senderKeyPair, _ := GenerateKeypair()
	recipientKeyPair, _ := GenerateKeypair()
	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _, _ = senderCrypto.GenerateKeyExchange(recipientKeyPair.PublicKey)
	}
}

// BenchmarkDeriveDecryptionKey benchmarks key derivation on receiver side
func BenchmarkDeriveDecryptionKey(b *testing.B) {
	senderKeyPair, _ := GenerateKeypair()
	recipientKeyPair, _ := GenerateKeypair()
	senderCrypto := NewDataTransferCryptoManager(senderKeyPair)
	recipientCrypto := NewDataTransferCryptoManager(recipientKeyPair)

	ephemeralPubKey, signature, timestamp, _, _ := senderCrypto.GenerateKeyExchange(
		recipientKeyPair.PublicKey,
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = recipientCrypto.DeriveDecryptionKey(
			ephemeralPubKey,
			signature,
			timestamp,
			senderKeyPair.PublicKey,
		)
	}
}
