package p2p

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/torrent/bencode"
)

// TestBEP44PutSignature tests if our signature creation works with anacrolix/dht
func TestBEP44PutSignature(t *testing.T) {
	// Generate a test keypair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Test data
	testValue := map[string]string{
		"test": "data",
		"foo":  "bar",
	}
	testSeq := int64(1)

	// Create a Put struct
	var pubKeyArray [32]byte
	copy(pubKeyArray[:], pubKey)

	put := bep44.Put{
		V:   testValue,
		K:   &pubKeyArray,
		Seq: testSeq,
	}

	// Sign it using the library's method
	put.Sign(privKey)

	t.Logf("Created BEP44 Put item:")
	t.Logf("  Seq: %d", put.Seq)
	t.Logf("  Sig: %x...", put.Sig[:16])
	t.Logf("  K: %x...", put.K[:16])

	// Verify the signature using the library's Verify function
	valueBytes, _ := bencode.Marshal(put.V)
	valid := bep44.Verify(pubKey, put.Salt, put.Seq, valueBytes, put.Sig[:])

	if !valid {
		t.Errorf("Signature verification failed!")
	} else {
		t.Logf("Signature verification: SUCCESS")
	}

	// Test target calculation
	target := put.Target()
	t.Logf("Target hash: %x", target)
}

// TestBEP44StoreIntegration tests if the bep44.NewMemory() store works correctly
func TestBEP44StoreIntegration(t *testing.T) {
	// Create a memory store
	store := bep44.NewMemory()

	// Generate a test keypair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate keypair: %v", err)
	}

	// Test data
	testValue := map[string]interface{}{
		"peer_id": "test123",
		"address": "192.168.1.1:30609",
	}
	testSeq := int64(0)

	// Create a Put struct
	var pubKeyArray [32]byte
	copy(pubKeyArray[:], pubKey)

	put := bep44.Put{
		V:   testValue,
		K:   &pubKeyArray,
		Seq: testSeq,
	}

	// Sign it
	put.Sign(privKey)

	target := put.Target()
	t.Logf("Storing data at target: %x", target)

	// Convert Put to Item
	item := put.ToItem()

	// Store the data
	err = store.Put(item)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}
	t.Logf("Data stored successfully")

	// Wait a moment
	time.Sleep(100 * time.Millisecond)

	// Try to retrieve it
	retrievedItem, err := store.Get(target)
	if err != nil {
		t.Fatalf("Failed to retrieve item: %v", err)
	}
	if retrievedItem == nil {
		t.Fatal("Retrieved item is nil - data not found in store")
	}

	t.Logf("Retrieved data:")
	t.Logf("  Seq: %d", retrievedItem.Seq)
	t.Logf("  V: %v", retrievedItem.V)
	t.Logf("  K: %x...", retrievedItem.K[:16])

	// Verify the sequence matches
	if retrievedItem.Seq != testSeq {
		t.Errorf("Expected seq=%d, got seq=%d", testSeq, retrievedItem.Seq)
	} else {
		t.Logf("Sequence number matches: %d", testSeq)
	}
}
