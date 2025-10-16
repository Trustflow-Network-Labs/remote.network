package database

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// TestBencodeSafeConversion tests that PeerMetadata can be converted to bencode-safe format and back
func TestBencodeSafeConversion(t *testing.T) {
	now := time.Now()

	// Create test metadata
	original := &PeerMetadata{
		NodeID:    "test-node-123",
		Topic:     "test-topic",
		Version:   1,
		Timestamp: now,
		NetworkInfo: NetworkInfo{
			PublicIP:   "1.2.3.4",
			PublicPort: 30609,
			NodeType:   "public",
			IsRelay:    true,
		},
		Capabilities: []string{"relay", "storage"},
	}

	// Convert to bencode-safe
	bencodeSafe := original.ToBencodeSafe()

	// Verify timestamp was converted
	if bencodeSafe.Timestamp != now.Unix() {
		t.Errorf("Expected timestamp %d, got %d", now.Unix(), bencodeSafe.Timestamp)
	}

	// Verify bencode can encode it
	encoded, err := bencode.Marshal(bencodeSafe)
	if err != nil {
		t.Fatalf("Failed to bencode: %v", err)
	}

	t.Logf("Bencoded size: %d bytes", len(encoded))
	t.Logf("Bencoded data: %s", string(encoded[:min(100, len(encoded))]))

	// Verify it can be decoded
	var decoded BencodePeerMetadata
	err = bencode.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Convert back to PeerMetadata
	restored := FromBencodeSafe(&decoded)

	// Verify fields match
	if restored.NodeID != original.NodeID {
		t.Errorf("NodeID mismatch: expected %s, got %s", original.NodeID, restored.NodeID)
	}

	if restored.Topic != original.Topic {
		t.Errorf("Topic mismatch: expected %s, got %s", original.Topic, restored.Topic)
	}

	// Timestamp should be within 1 second (Unix precision)
	if abs(restored.Timestamp.Unix()-original.Timestamp.Unix()) > 1 {
		t.Errorf("Timestamp mismatch: expected %v, got %v", original.Timestamp.Unix(), restored.Timestamp.Unix())
	}

	if restored.NetworkInfo.PublicIP != original.NetworkInfo.PublicIP {
		t.Errorf("PublicIP mismatch: expected %s, got %s", original.NetworkInfo.PublicIP, restored.NetworkInfo.PublicIP)
	}

	t.Logf("✓ Bencode-safe conversion successful")
}

// TestDecodeBencodedMetadata tests the helper function
func TestDecodeBencodedMetadata(t *testing.T) {
	now := time.Now()

	original := &PeerMetadata{
		NodeID:    "decode-test",
		Topic:     "test-topic",
		Version:   1,
		Timestamp: now,
		NetworkInfo: NetworkInfo{
			PublicIP: "5.6.7.8",
			IsRelay:  false,
		},
	}

	// Convert to bencode-safe and encode
	bencodeSafe := original.ToBencodeSafe()
	encoded, err := bencode.Marshal(bencodeSafe)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode using helper function
	decoded, err := DecodeBencodedMetadata(encoded)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	if decoded.NodeID != original.NodeID {
		t.Errorf("NodeID mismatch: expected %s, got %s", original.NodeID, decoded.NodeID)
	}

	t.Logf("✓ DecodeBencodedMetadata successful")
}

// TestBencodeNoTimeCorruption verifies that time.Time fields don't corrupt bencode output
func TestBencodeNoTimeCorruption(t *testing.T) {
	bencodeSafe := &BencodePeerMetadata{
		NodeID:    "test",
		Topic:     "topic",
		Version:   1,
		Timestamp: time.Now().Unix(),
		NetworkInfo: BencodeNetworkInfo{
			PublicIP: "1.2.3.4",
			NodeType: "public",
		},
	}

	// Encode twice and verify identical output
	encoded1, err1 := bencode.Marshal(bencodeSafe)
	if err1 != nil {
		t.Fatalf("First encode failed: %v", err1)
	}

	encoded2, err2 := bencode.Marshal(bencodeSafe)
	if err2 != nil {
		t.Fatalf("Second encode failed: %v", err2)
	}

	if string(encoded1) != string(encoded2) {
		t.Errorf("Bencode output not deterministic!")
		t.Logf("First:  %s", string(encoded1))
		t.Logf("Second: %s", string(encoded2))
	} else {
		t.Logf("✓ Bencode output is deterministic")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}
