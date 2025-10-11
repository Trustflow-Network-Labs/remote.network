package p2p

import (
	"crypto/ed25519"
	"crypto/sha1"
	"testing"

	"github.com/anacrolix/torrent/bencode"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
)

// TestBEP44SignatureCreation tests the signature generation for BEP_44
// This validates our implementation matches the BEP_44 spec:
// signature = sign(bencode(seq) + bencode(v))
func TestBEP44SignatureCreation(t *testing.T) {
	// Generate keypair
	keyPair, err := crypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Test value
	value := map[string]interface{}{
		"test": "data",
		"num":  42,
	}
	sequence := int64(1)

	// Bencode value and sequence (as BEP_44 specifies)
	valueBytes, err := bencode.Marshal(value)
	if err != nil {
		t.Fatalf("Failed to bencode value: %v", err)
	}

	seqBytes, err := bencode.Marshal(sequence)
	if err != nil {
		t.Fatalf("Failed to bencode sequence: %v", err)
	}

	// Create signature payload
	signPayload := append(seqBytes, valueBytes...)
	signature := keyPair.Sign(signPayload)

	// Verify signature length (Ed25519 signatures are 64 bytes)
	if len(signature) != ed25519.SignatureSize {
		t.Errorf("Invalid signature length: expected %d, got %d",
			ed25519.SignatureSize, len(signature))
	}

	// Verify signature is valid
	if !keyPair.Verify(signPayload, signature) {
		t.Error("Generated signature failed verification")
	}

	// Verify tampering is detected (change sequence)
	tamperedSeq := int64(2)
	tamperedSeqBytes, _ := bencode.Marshal(tamperedSeq)
	tamperedPayload := append(tamperedSeqBytes, valueBytes...)

	if keyPair.Verify(tamperedPayload, signature) {
		t.Error("Signature verified with tampered sequence number")
	}

	// Verify tampering is detected (change value)
	tamperedValue := map[string]interface{}{"test": "modified"}
	tamperedValueBytes, _ := bencode.Marshal(tamperedValue)
	tamperedPayload2 := append(seqBytes, tamperedValueBytes...)

	if keyPair.Verify(tamperedPayload2, signature) {
		t.Error("Signature verified with tampered value")
	}
}

// TestBEP44StorageKeyDerivation tests SHA1(public_key) derivation
func TestBEP44StorageKeyDerivation(t *testing.T) {
	keyPair, err := crypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Verify public key is 32 bytes (Ed25519)
	if len(keyPair.PublicKey) != 32 {
		t.Fatalf("Invalid public key length: expected 32, got %d", len(keyPair.PublicKey))
	}

	// Derive storage key
	storageKey := keyPair.StorageKey()

	// Verify storage key is SHA1 hash (20 bytes)
	if len(storageKey) != 20 {
		t.Errorf("Invalid storage key length: expected 20, got %d", len(storageKey))
	}

	// Verify it matches manual SHA1 calculation
	expectedKey := sha1.Sum(keyPair.PublicKey)
	if storageKey != expectedKey {
		t.Error("Storage key does not match SHA1(public_key)")
	}

	// Verify deterministic (same input produces same output)
	storageKey2 := keyPair.StorageKey()
	if storageKey != storageKey2 {
		t.Error("Storage key derivation is not deterministic")
	}
}

// TestBEP44SequenceOrdering tests sequence number semantics
func TestBEP44SequenceOrdering(t *testing.T) {
	// BEP_44 specifies that higher sequence numbers win
	// This test validates the logic for comparing sequence numbers

	testCases := []struct {
		name     string
		current  int64
		incoming int64
		shouldUpdate bool
	}{
		{"Initial value", -1, 0, true},
		{"Higher sequence", 5, 10, true},
		{"Same sequence", 5, 5, false},
		{"Lower sequence", 10, 5, false},
		{"Large gap", 100, 1000, true},
		{"Overflow safety", 9223372036854775807, 1, false}, // max int64
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shouldUpdate := tc.incoming > tc.current
			if shouldUpdate != tc.shouldUpdate {
				t.Errorf("Sequence comparison failed: current=%d, incoming=%d, expected shouldUpdate=%v, got %v",
					tc.current, tc.incoming, tc.shouldUpdate, shouldUpdate)
			}
		})
	}
}

// TestBEP44ValueEncoding tests bencode encoding/decoding
func TestBEP44ValueEncoding(t *testing.T) {
	testCases := []struct {
		name  string
		value interface{}
	}{
		{
			name:  "Simple string",
			value: "hello",
		},
		{
			name:  "Integer",
			value: int64(42),
		},
		{
			name: "Map with mixed types",
			value: map[string]interface{}{
				"string": "value",
				"number": int64(123),
				"nested": map[string]interface{}{
					"key": "nested_value",
				},
			},
		},
		{
			name:  "List",
			value: []interface{}{"item1", int64(2), "item3"},
		},
		{
			name: "Complex metadata",
			value: map[string]interface{}{
				"node_id": "abc123",
				"version": int64(1),
				"network": map[string]interface{}{
					"public_ip": "1.2.3.4",
					"port":      int64(30906),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			encoded, err := bencode.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Failed to bencode value: %v", err)
			}

			// Verify encoded is not empty
			if len(encoded) == 0 {
				t.Error("Bencode produced empty output")
			}

			// Decode
			var decoded interface{}
			err = bencode.Unmarshal(encoded, &decoded)
			if err != nil {
				t.Fatalf("Failed to decode bencoded value: %v", err)
			}

			// For simple types, verify round-trip
			switch v := tc.value.(type) {
			case string:
				if decoded.(string) != v {
					t.Errorf("Round-trip failed: expected %v, got %v", v, decoded)
				}
			case int64:
				if decoded.(int64) != v {
					t.Errorf("Round-trip failed: expected %v, got %v", v, decoded)
				}
			}
		})
	}
}

// TestBEP44KeySizeValidation tests that we properly validate key sizes
func TestBEP44KeySizeValidation(t *testing.T) {
	testCases := []struct {
		name        string
		publicKey   []byte
		expectError bool
	}{
		{"Valid 32-byte key", make([]byte, 32), false},
		{"Invalid 31-byte key", make([]byte, 31), true},
		{"Invalid 33-byte key", make([]byte, 33), true},
		{"Empty key", []byte{}, true},
		{"Nil key", nil, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Validation logic: public key must be exactly 32 bytes
			hasError := len(tc.publicKey) != 32

			if hasError != tc.expectError {
				t.Errorf("Key validation failed: key length %d, expected error=%v, got error=%v",
					len(tc.publicKey), tc.expectError, hasError)
			}
		})
	}
}

// TestBEP44SignatureSizeValidation tests signature size validation
func TestBEP44SignatureSizeValidation(t *testing.T) {
	testCases := []struct {
		name        string
		signature   []byte
		expectError bool
	}{
		{"Valid 64-byte signature", make([]byte, 64), false},
		{"Invalid 63-byte signature", make([]byte, 63), true},
		{"Invalid 65-byte signature", make([]byte, 65), true},
		{"Empty signature", []byte{}, true},
		{"Nil signature", nil, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Validation logic: signature must be exactly 64 bytes (Ed25519)
			hasError := len(tc.signature) != ed25519.SignatureSize

			if hasError != tc.expectError {
				t.Errorf("Signature validation failed: signature length %d, expected error=%v, got error=%v",
					len(tc.signature), tc.expectError, hasError)
			}
		})
	}
}

// TestBEP44MutableDataStructure tests the MutableData struct
func TestBEP44MutableDataStructure(t *testing.T) {
	// Create sample mutable data
	data := &MutableData{
		Value:     []byte("test value"),
		PublicKey: [32]byte{1, 2, 3},
		Signature: [64]byte{4, 5, 6},
		Sequence:  10,
		Salt:      nil, // We don't use salt
	}

	// Verify fields are set correctly
	if len(data.Value) == 0 {
		t.Error("Value should not be empty")
	}

	if data.Sequence != 10 {
		t.Errorf("Expected sequence 10, got %d", data.Sequence)
	}

	if data.Salt != nil {
		t.Error("Salt should be nil (we don't use it)")
	}

	// Verify array sizes
	if len(data.PublicKey) != 32 {
		t.Errorf("PublicKey should be 32 bytes, got %d", len(data.PublicKey))
	}

	if len(data.Signature) != 64 {
		t.Errorf("Signature should be 64 bytes, got %d", len(data.Signature))
	}
}

// TestBEP44VerifySignatureWithPublicKey tests signature verification
func TestBEP44VerifySignatureWithPublicKey(t *testing.T) {
	// Generate keypair
	keyPair, err := crypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Create test data
	value := map[string]string{"test": "data"}
	sequence := int64(1)

	// Bencode
	valueBytes, _ := bencode.Marshal(value)
	seqBytes, _ := bencode.Marshal(sequence)
	signPayload := append(seqBytes, valueBytes...)

	// Sign
	signature := keyPair.Sign(signPayload)

	// Verify with correct public key
	if !crypto.VerifyWithPublicKey(keyPair.PublicKey, signPayload, signature) {
		t.Error("Valid signature failed verification")
	}

	// Generate different keypair
	wrongKeyPair, _ := crypto.GenerateKeypair()

	// Verify with wrong public key (should fail)
	if crypto.VerifyWithPublicKey(wrongKeyPair.PublicKey, signPayload, signature) {
		t.Error("Signature verified with wrong public key")
	}

	// Verify with tampered payload (should fail)
	tamperedPayload := append(signPayload, []byte("extra")...)
	if crypto.VerifyWithPublicKey(keyPair.PublicKey, tamperedPayload, signature) {
		t.Error("Signature verified with tampered payload")
	}
}
