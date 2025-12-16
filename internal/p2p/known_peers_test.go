package p2p

import (
	"sync"
	"testing"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

func setupTestKnownPeersManager() *KnownPeersManager {
	// Create config manager for logger
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)

	// Create in-memory known peers manager
	kpm := NewKnownPeersManager(logger)

	return kpm
}

func TestStoreKnownPeer(t *testing.T) {
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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
	kpm := setupTestKnownPeersManager()

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

	// Get the original to verify source
	original, _ := kpm.GetKnownPeer("conflict-test", "test")
	if original.Source != "source1" {
		t.Errorf("Expected initial source 'source1', got '%s'", original.Source)
	}

	// Update with same key but different values
	// Note: In-memory impl keeps original Source on conflict (like ON CONFLICT DO UPDATE)
	peer2 := &KnownPeer{
		PeerID:    "conflict-test",
		PublicKey: []byte{4, 5, 6},
		Topic:     "test",
		Source:    "source2",
		IsStore:   true,
	}

	if err := kpm.StoreKnownPeer(peer2); err != nil {
		t.Fatalf("Failed to update peer: %v", err)
	}

	// Verify the update - Source should remain original, IsStore should be updated
	retrieved, err := kpm.GetKnownPeer("conflict-test", "test")
	if err != nil {
		t.Fatalf("Failed to get peer: %v", err)
	}

	// Source should be kept from original (ON CONFLICT behavior)
	if retrieved.Source != "source1" {
		t.Errorf("Expected source 'source1' (original preserved), got '%s'", retrieved.Source)
	}

	// IsStore should be updated
	if !retrieved.IsStore {
		t.Error("Expected IsStore to be updated to true")
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

func TestGetRelayPeers(t *testing.T) {
	kpm := setupTestKnownPeersManager()

	// Store peers with different relay status
	peers := []*KnownPeer{
		{PeerID: "relay1", PublicKey: []byte{1}, Topic: "test", IsRelay: true},
		{PeerID: "normal", PublicKey: []byte{2}, Topic: "test", IsRelay: false},
		{PeerID: "relay2", PublicKey: []byte{3}, Topic: "test", IsRelay: true},
		{PeerID: "relay3", PublicKey: []byte{4}, Topic: "other", IsRelay: true},
	}

	for _, peer := range peers {
		if err := kpm.StoreKnownPeer(peer); err != nil {
			t.Fatalf("Failed to store peer: %v", err)
		}
	}

	// Get relay peers for "test" topic
	relays, err := kpm.GetRelayPeers("test")
	if err != nil {
		t.Fatalf("Failed to get relay peers: %v", err)
	}

	if len(relays) != 2 {
		t.Errorf("Expected 2 relay peers for topic 'test', got %d", len(relays))
	}
}

func TestGetKnownPeerByNodeID(t *testing.T) {
	kpm := setupTestKnownPeersManager()

	peer := &KnownPeer{
		PeerID:    "peer123",
		DHTNodeID: "dht456",
		PublicKey: []byte{1, 2, 3},
		Topic:     "test",
	}

	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Find by peer_id
	found, err := kpm.GetKnownPeerByNodeID("peer123", "test")
	if err != nil {
		t.Fatalf("Failed to get by peer_id: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find peer by peer_id")
	}

	// Find by dht_node_id
	found, err = kpm.GetKnownPeerByNodeID("dht456", "test")
	if err != nil {
		t.Fatalf("Failed to get by dht_node_id: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find peer by dht_node_id")
	}

	// Should not find non-existent
	found, err = kpm.GetKnownPeerByNodeID("nonexistent", "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if found != nil {
		t.Error("Should not find non-existent peer")
	}
}

func TestUpdatePeerServiceCounts(t *testing.T) {
	kpm := setupTestKnownPeersManager()

	peer := &KnownPeer{
		PeerID:     "service-test",
		PublicKey:  []byte{1, 2, 3},
		Topic:      "test",
		FilesCount: 0,
		AppsCount:  0,
	}

	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Update counts
	err := kpm.UpdatePeerServiceCounts("service-test", "test", 5, 10)
	if err != nil {
		t.Fatalf("Failed to update service counts: %v", err)
	}

	// Verify
	retrieved, _ := kpm.GetKnownPeer("service-test", "test")
	if retrieved.FilesCount != 5 {
		t.Errorf("Expected FilesCount=5, got %d", retrieved.FilesCount)
	}
	if retrieved.AppsCount != 10 {
		t.Errorf("Expected AppsCount=10, got %d", retrieved.AppsCount)
	}

	// Test updating non-existent peer
	err = kpm.UpdatePeerServiceCounts("nonexistent", "test", 1, 1)
	if err == nil {
		t.Error("Expected error when updating non-existent peer")
	}
}

func TestGetKnownPeersCountByTopic(t *testing.T) {
	kpm := setupTestKnownPeersManager()

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

	// Count for topic1
	count, err := kpm.GetKnownPeersCountByTopic("topic1")
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 peers for topic1, got %d", count)
	}

	// Count for topic2
	count, err = kpm.GetKnownPeersCountByTopic("topic2")
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 peer for topic2, got %d", count)
	}

	// Count for non-existent topic
	count, err = kpm.GetKnownPeersCountByTopic("nonexistent")
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 peers for nonexistent topic, got %d", count)
	}
}

func TestConcurrentAccess(t *testing.T) {
	kpm := setupTestKnownPeersManager()

	// Test concurrent writes and reads
	var wg sync.WaitGroup
	numGoroutines := 50

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peer := &KnownPeer{
				PeerID:    string(rune('a' + (id % 26))),
				PublicKey: []byte{byte(id)},
				Topic:     "test",
			}
			_ = kpm.StoreKnownPeer(peer)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = kpm.GetAllKnownPeers()
			_, _ = kpm.GetKnownPeersCount()
			_, _ = kpm.GetKnownPeersByTopic("test")
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify data integrity
	count, err := kpm.GetKnownPeersCount()
	if err != nil {
		t.Fatalf("Failed to get count after concurrent access: %v", err)
	}

	// Should have between 1 and 26 peers (due to key collisions from mod 26)
	if count < 1 || count > 26 {
		t.Errorf("Unexpected peer count after concurrent access: %d", count)
	}
}

func TestCopyPeerIsolation(t *testing.T) {
	kpm := setupTestKnownPeersManager()

	peer := &KnownPeer{
		PeerID:    "isolation-test",
		PublicKey: []byte{1, 2, 3},
		Topic:     "test",
	}

	if err := kpm.StoreKnownPeer(peer); err != nil {
		t.Fatalf("Failed to store peer: %v", err)
	}

	// Get a copy
	retrieved, _ := kpm.GetKnownPeer("isolation-test", "test")

	// Modify the copy
	retrieved.PublicKey[0] = 99
	retrieved.PeerID = "modified"

	// Get another copy and verify original is unchanged
	retrieved2, _ := kpm.GetKnownPeer("isolation-test", "test")

	if retrieved2.PublicKey[0] != 1 {
		t.Error("Original peer data was modified through copy")
	}

	if retrieved2.PeerID != "isolation-test" {
		t.Error("Original peer ID was modified through copy")
	}
}
