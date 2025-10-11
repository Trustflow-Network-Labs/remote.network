package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	_ "modernc.org/sqlite"
)

func setupTestKnownPeersDB(t *testing.T) (*KnownPeersManager, *sql.DB) {
	// Create in-memory database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create config manager for logger
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)

	// Create known peers manager
	kpm, err := NewKnownPeersManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create KnownPeersManager: %v", err)
	}

	return kpm, db
}

func TestStoreKnownPeer(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	peer := &KnownPeer{
		PeerID:    "abc123",
		PublicKey: []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Topic:     "test-topic",
		Source:    "test",
	}

	err := kpm.StoreKnownPeer(peer)
	if err != nil {
		t.Fatalf("Failed to store known peer: %v", err)
	}

	// Verify peer was stored
	retrieved, err := kpm.GetKnownPeer("abc123", "test-topic")
	if err != nil {
		t.Fatalf("Failed to get known peer: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Expected peer to be retrieved, got nil")
	}

	if retrieved.PeerID != peer.PeerID {
		t.Errorf("Expected peer_id %s, got %s", peer.PeerID, retrieved.PeerID)
	}

	if retrieved.Topic != peer.Topic {
		t.Errorf("Expected topic %s, got %s", peer.Topic, retrieved.Topic)
	}
}

func TestUpdateLastSeen(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Store initial peer
	peer := &KnownPeer{
		PeerID:    "test123",
		PublicKey: []byte{1, 2, 3},
		Topic:     "test-topic",
		LastSeen:  time.Now().Add(-1 * time.Hour),
	}

	err := kpm.StoreKnownPeer(peer)
	if err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Update last_seen
	err = kpm.UpdateLastSeen("test123", "test-topic")
	if err != nil {
		t.Fatalf("Failed to update last_seen: %v", err)
	}

	// Verify last_seen was updated
	retrieved, err := kpm.GetKnownPeer("test123", "test-topic")
	if err != nil {
		t.Fatalf("Failed to get peer: %v", err)
	}

	if time.Since(retrieved.LastSeen) > 1*time.Second {
		t.Errorf("LastSeen was not updated recently: %v", retrieved.LastSeen)
	}
}

func TestGetAllKnownPeers(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Store multiple peers
	peers := []*KnownPeer{
		{PeerID: "peer1", PublicKey: []byte{1}, Topic: "topic1"},
		{PeerID: "peer2", PublicKey: []byte{2}, Topic: "topic1"},
		{PeerID: "peer3", PublicKey: []byte{3}, Topic: "topic2"},
	}

	for _, peer := range peers {
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Get all peers
	all, err := kpm.GetAllKnownPeers()
	if err != nil {
		t.Fatalf("Failed to get all peers: %v", err)
	}

	if len(all) != 3 {
		t.Errorf("Expected 3 peers, got %d", len(all))
	}
}

func TestGetKnownPeersByTopic(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Store peers for different topics
	peers := []*KnownPeer{
		{PeerID: "peer1", PublicKey: []byte{1}, Topic: "topic1"},
		{PeerID: "peer2", PublicKey: []byte{2}, Topic: "topic1"},
		{PeerID: "peer3", PublicKey: []byte{3}, Topic: "topic2"},
	}

	for _, peer := range peers {
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Get peers for topic1
	topic1Peers, err := kpm.GetKnownPeersByTopic("topic1")
	if err != nil {
		t.Fatalf("Failed to get peers by topic: %v", err)
	}

	if len(topic1Peers) != 2 {
		t.Errorf("Expected 2 peers for topic1, got %d", len(topic1Peers))
	}

	// Get peers for topic2
	topic2Peers, err := kpm.GetKnownPeersByTopic("topic2")
	if err != nil {
		t.Fatalf("Failed to get peers by topic: %v", err)
	}

	if len(topic2Peers) != 1 {
		t.Errorf("Expected 1 peer for topic2, got %d", len(topic2Peers))
	}
}

func TestGetRecentKnownPeers(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Store peers with different last_seen times
	now := time.Now()
	peers := []*KnownPeer{
		{PeerID: "peer1", PublicKey: []byte{1}, Topic: "test", LastSeen: now.Add(-1 * time.Hour)},
		{PeerID: "peer2", PublicKey: []byte{2}, Topic: "test", LastSeen: now.Add(-30 * time.Minute)},
		{PeerID: "peer3", PublicKey: []byte{3}, Topic: "test", LastSeen: now},
	}

	for _, peer := range peers {
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Get 2 most recent peers
	recent, err := kpm.GetRecentKnownPeers(2, "test")
	if err != nil {
		t.Fatalf("Failed to get recent peers: %v", err)
	}

	if len(recent) != 2 {
		t.Errorf("Expected 2 recent peers, got %d", len(recent))
	}

	// First should be most recent
	if recent[0].PeerID != "peer3" {
		t.Errorf("Expected most recent peer to be peer3, got %s", recent[0].PeerID)
	}
}

func TestDeleteKnownPeer(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	peer := &KnownPeer{
		PeerID:    "delete-me",
		PublicKey: []byte{1, 2, 3},
		Topic:     "test-topic",
	}

	// Store peer
	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Verify it exists
	retrieved, err := kpm.GetKnownPeer("delete-me", "test-topic")
	if err != nil {
		t.Fatalf("Failed to get peer: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected peer to exist before deletion")
	}

	// Delete peer
	if err := kpm.DeleteKnownPeer("delete-me", "test-topic"); err != nil {
		t.Fatalf("Failed to delete peer: %v", err)
	}

	// Verify it's deleted
	retrieved, err = kpm.GetKnownPeer("delete-me", "test-topic")
	if err != nil {
		t.Fatalf("Failed to get peer after deletion: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected peer to be deleted")
	}
}

func TestCleanupStalePeers(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Store peers with different ages
	now := time.Now()
	peers := []*KnownPeer{
		{PeerID: "fresh", PublicKey: []byte{1}, Topic: "test", LastSeen: now},
		{PeerID: "stale1", PublicKey: []byte{2}, Topic: "test", LastSeen: now.Add(-2 * time.Hour)},
		{PeerID: "stale2", PublicKey: []byte{3}, Topic: "test", LastSeen: now.Add(-3 * time.Hour)},
	}

	for _, peer := range peers {
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Cleanup peers older than 1 hour
	cleaned, err := kpm.CleanupStalePeers(1 * time.Hour)
	if err != nil {
		t.Fatalf("Failed to cleanup stale peers: %v", err)
	}

	if cleaned != 2 {
		t.Errorf("Expected 2 stale peers to be cleaned, got %d", cleaned)
	}

	// Verify fresh peer still exists
	fresh, err := kpm.GetKnownPeer("fresh", "test")
	if err != nil {
		t.Fatalf("Failed to get fresh peer: %v", err)
	}
	if fresh == nil {
		t.Error("Fresh peer should not have been cleaned up")
	}

	// Verify stale peers are gone
	stale, err := kpm.GetKnownPeer("stale1", "test")
	if err != nil {
		t.Fatalf("Failed to check stale peer: %v", err)
	}
	if stale != nil {
		t.Error("Stale peer should have been cleaned up")
	}
}

func TestGetKnownPeersCount(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	// Initially should be 0
	count, err := kpm.GetKnownPeersCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 peers initially, got %d", count)
	}

	// Add 3 peers
	for i := 0; i < 3; i++ {
		peer := &KnownPeer{
			PeerID:    string(rune('a' + i)),
			PublicKey: []byte{byte(i)},
			Topic:     "test",
		}
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Count should be 3
	count, err = kpm.GetKnownPeersCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 peers, got %d", count)
	}
}

func TestConflictHandling(t *testing.T) {
	kpm, db := setupTestKnownPeersDB(t)
	defer db.Close()

	peer := &KnownPeer{
		PeerID:    "conflict-test",
		PublicKey: []byte{1, 2, 3},
		Topic:     "test",
		Source:    "source1",
	}

	// Store initial peer
	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Update with different source
	peer.Source = "source2"
	peer.PublicKey = []byte{4, 5, 6}

	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to update peer: %v", err)
	}

	// Verify it was updated
	retrieved, err := kpm.GetKnownPeer("conflict-test", "test")
	if err != nil {
		t.Fatalf("Failed to get peer: %v", err)
	}

	if retrieved.Source != "source2" {
		t.Errorf("Expected source 'source2', got '%s'", retrieved.Source)
	}

	if len(retrieved.PublicKey) != 3 {
		t.Errorf("Expected public key to be updated")
	}
}

func TestPublicKeyHex(t *testing.T) {
	peer := &KnownPeer{
		PeerID:    "test",
		PublicKey: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}

	hex := peer.PublicKeyHex()
	expected := "deadbeef"

	if hex != expected {
		t.Errorf("Expected public key hex '%s', got '%s'", expected, hex)
	}
}
