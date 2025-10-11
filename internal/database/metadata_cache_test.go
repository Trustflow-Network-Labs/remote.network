package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	_ "modernc.org/sqlite"
)

func setupTestMetadataCache(t *testing.T) (*MetadataCache, *sql.DB) {
	// Create in-memory database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Create config manager for logger
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)

	// Create metadata cache (skip periodic cleanup for tests)
	mc := &MetadataCache{
		db:     db,
		logger: logger,
	}

	// Create table
	if err := mc.createTable(); err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Prepare statements
	if err := mc.prepareStatements(); err != nil {
		t.Fatalf("Failed to prepare statements: %v", err)
	}

	return mc, db
}

func createTestMetadata(peerID string, topic string) *PeerMetadata {
	return &PeerMetadata{
		NodeID:  peerID,
		Topic:   topic,
		Version: 1,
		NetworkInfo: NetworkInfo{
			PublicIP:   "1.2.3.4",
			PublicPort: 30906,
			NodeType:   "public",
		},
		Timestamp: time.Now(),
		LastSeen:  time.Now(),
	}
}

func TestCacheMetadata(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	metadata := createTestMetadata("peer123", "test-topic")

	// Cache metadata with 5-minute TTL
	err := mc.CacheMetadata("peer123", metadata, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to cache metadata: %v", err)
	}

	// Verify it was cached
	retrieved, isCached, err := mc.GetCachedMetadata("peer123")
	if err != nil {
		t.Fatalf("Failed to get cached metadata: %v", err)
	}

	if !isCached {
		t.Error("Metadata should be cached")
	}

	if retrieved == nil {
		t.Fatal("Retrieved metadata is nil")
	}

	if retrieved.NodeID != "peer123" {
		t.Errorf("Expected NodeID 'peer123', got '%s'", retrieved.NodeID)
	}
}

func TestGetCachedMetadataMiss(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Try to get non-existent metadata
	retrieved, isCached, err := mc.GetCachedMetadata("nonexistent")
	if err != nil {
		t.Fatalf("GetCachedMetadata should not error on miss: %v", err)
	}

	if isCached {
		t.Error("Metadata should not be cached")
	}

	if retrieved != nil {
		t.Error("Retrieved metadata should be nil on cache miss")
	}
}

func TestCacheExpiration(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	metadata := createTestMetadata("expire-test", "test-topic")

	// Cache with 1 second TTL (more reliable for testing)
	err := mc.CacheMetadata("expire-test", metadata, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to cache metadata: %v", err)
	}

	// Verify it's cached immediately
	_, isCached, err := mc.GetCachedMetadata("expire-test")
	if err != nil {
		t.Fatalf("Failed to get cached metadata: %v", err)
	}
	if !isCached {
		t.Error("Metadata should be cached initially")
	}

	// Wait for expiration (1s TTL + 500ms buffer)
	time.Sleep(1500 * time.Millisecond)

	// Should now be expired
	_, isCached, err = mc.GetCachedMetadata("expire-test")
	if err != nil {
		t.Fatalf("Failed to check expired metadata: %v", err)
	}
	if isCached {
		t.Error("Metadata should be expired")
	}
}

func TestDeleteCachedMetadata(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	metadata := createTestMetadata("delete-test", "test-topic")

	// Cache metadata
	err := mc.CacheMetadata("delete-test", metadata, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to cache metadata: %v", err)
	}

	// Verify it exists
	_, isCached, err := mc.GetCachedMetadata("delete-test")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}
	if !isCached {
		t.Fatal("Metadata should be cached")
	}

	// Delete it
	err = mc.DeleteCachedMetadata("delete-test")
	if err != nil {
		t.Fatalf("Failed to delete metadata: %v", err)
	}

	// Verify it's gone
	_, isCached, err = mc.GetCachedMetadata("delete-test")
	if err != nil {
		t.Fatalf("Failed to check after deletion: %v", err)
	}
	if isCached {
		t.Error("Metadata should not be cached after deletion")
	}
}

func TestInvalidateCache(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Cache multiple entries
	for i := 0; i < 5; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Verify count
	count, err := mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected 5 cached entries, got %d", count)
	}

	// Invalidate all
	err = mc.InvalidateCache()
	if err != nil {
		t.Fatalf("Failed to invalidate cache: %v", err)
	}

	// Verify cache is empty
	count, err = mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count after invalidation: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 cached entries after invalidation, got %d", count)
	}
}

func TestCleanupExpired(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Cache some entries with short TTL (1 second)
	for i := 0; i < 3; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 1*time.Second)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Cache some entries with long TTL
	for i := 3; i < 5; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 10*time.Minute)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Wait for short TTL entries to expire (1s + 500ms buffer)
	time.Sleep(1500 * time.Millisecond)

	// Cleanup expired
	cleaned, err := mc.CleanupExpired()
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	if cleaned != 3 {
		t.Errorf("Expected 3 expired entries to be cleaned, got %d", cleaned)
	}

	// Verify remaining count
	count, err := mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 remaining entries, got %d", count)
	}
}

func TestGetAllCachedMetadata(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Cache multiple entries
	expectedCount := 5
	for i := 0; i < expectedCount; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Get all
	all, err := mc.GetAllCachedMetadata()
	if err != nil {
		t.Fatalf("Failed to get all cached metadata: %v", err)
	}

	if len(all) != expectedCount {
		t.Errorf("Expected %d cached entries, got %d", expectedCount, len(all))
	}

	// Verify each entry has required fields
	for _, cached := range all {
		if cached.PeerID == "" {
			t.Error("Cached entry should have peer_id")
		}
		if cached.Metadata == nil {
			t.Error("Cached entry should have metadata")
		}
		if cached.CachedAt.IsZero() {
			t.Error("Cached entry should have cached_at timestamp")
		}
		if cached.TTLSeconds <= 0 {
			t.Error("Cached entry should have positive TTL")
		}
	}
}

func TestGetCacheStats(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Cache some entries
	for i := 0; i < 3; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Get stats
	stats, err := mc.GetCacheStats()
	if err != nil {
		t.Fatalf("Failed to get cache stats: %v", err)
	}

	// Verify stats structure (check actual keys from implementation)
	if stats["total_entries"] == nil {
		t.Error("Stats should include total_entries")
	}

	totalEntries, ok := stats["total_entries"].(int)
	if !ok || totalEntries != 3 {
		t.Errorf("Expected total_entries=3, got %v", stats["total_entries"])
	}

	if stats["expired_entries"] == nil {
		t.Error("Stats should include expired_entries")
	}

	if stats["valid_entries"] == nil {
		t.Error("Stats should include valid_entries")
	}

	// All should be valid (non-expired)
	validEntries, ok := stats["valid_entries"].(int)
	if !ok || validEntries != 3 {
		t.Errorf("Expected valid_entries=3, got %v", stats["valid_entries"])
	}
}

func TestCacheMetadataUpdate(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Cache initial metadata
	metadata1 := createTestMetadata("update-test", "test-topic")
	metadata1.Version = 1

	err := mc.CacheMetadata("update-test", metadata1, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to cache initial metadata: %v", err)
	}

	// Update with new metadata
	metadata2 := createTestMetadata("update-test", "test-topic")
	metadata2.Version = 2

	err = mc.CacheMetadata("update-test", metadata2, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to update cached metadata: %v", err)
	}

	// Verify updated version
	retrieved, isCached, err := mc.GetCachedMetadata("update-test")
	if err != nil {
		t.Fatalf("Failed to get updated metadata: %v", err)
	}

	if !isCached {
		t.Error("Updated metadata should be cached")
	}

	if retrieved.Version != 2 {
		t.Errorf("Expected version 2, got %d", retrieved.Version)
	}

	// Verify count didn't increase (should be update, not insert)
	count, err := mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 entry (updated), got %d", count)
	}
}

func TestCacheDifferentTTLs(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	testCases := []struct {
		name          string
		ttl           time.Duration
		shouldBeCached bool
	}{
		{"1 second", 1 * time.Second, true},
		{"5 minutes", 5 * time.Minute, true},
		{"1 hour", 1 * time.Hour, true},
		{"0 (expires immediately)", 0, false}, // 0 TTL expires immediately
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			peerID := string(rune('a' + i))
			metadata := createTestMetadata(peerID, "test-topic")

			err := mc.CacheMetadata(peerID, metadata, tc.ttl)
			if err != nil {
				t.Fatalf("Failed to cache with TTL %v: %v", tc.ttl, err)
			}

			// Verify cache state based on TTL
			_, isCached, err := mc.GetCachedMetadata(peerID)
			if err != nil {
				t.Fatalf("Failed to get metadata: %v", err)
			}
			if isCached != tc.shouldBeCached {
				t.Errorf("Expected isCached=%v for TTL %v, got %v", tc.shouldBeCached, tc.ttl, isCached)
			}
		})
	}
}

func TestCacheMetadataJSONSerialization(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Create metadata with complex nested structures
	metadata := &PeerMetadata{
		NodeID:  "json-test",
		Topic:   "test-topic",
		Version: 1,
		NetworkInfo: NetworkInfo{
			PublicIP:   "1.2.3.4",
			PrivateIP:  "192.168.1.100",
			PublicPort: 30906,
			NodeType:   "public",
			Protocols: []Protocol{
				{Name: "quic", Port: 30906},
				{Name: "tcp", Port: 30907},
			},
		},
		Capabilities: []string{"relay", "dht"},
		Services: map[string]Service{
			"relay": {
				Type:     "relay",
				Endpoint: "1.2.3.4:30906",
				Status:   "available",
				Capabilities: map[string]interface{}{
					"max_connections": 100,
				},
			},
		},
		Extensions: map[string]interface{}{
			"custom_field": "value",
			"number":       42.0,
		},
		Timestamp: time.Now(),
		LastSeen:  time.Now(),
		Source:    "test",
	}

	// Cache it
	err := mc.CacheMetadata("json-test", metadata, 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to cache complex metadata: %v", err)
	}

	// Retrieve it
	retrieved, isCached, err := mc.GetCachedMetadata("json-test")
	if err != nil {
		t.Fatalf("Failed to get cached metadata: %v", err)
	}

	if !isCached {
		t.Fatal("Metadata should be cached")
	}

	// Verify complex fields survived serialization
	if retrieved.NodeID != "json-test" {
		t.Errorf("NodeID not preserved")
	}

	if retrieved.NetworkInfo.PrivateIP != "192.168.1.100" {
		t.Errorf("PrivateIP not preserved")
	}

	if len(retrieved.NetworkInfo.Protocols) != 2 {
		t.Errorf("Protocols not preserved: expected 2, got %d", len(retrieved.NetworkInfo.Protocols))
	}

	if len(retrieved.Capabilities) != 2 {
		t.Errorf("Capabilities not preserved: expected 2, got %d", len(retrieved.Capabilities))
	}

	if len(retrieved.Services) != 1 {
		t.Errorf("Services not preserved: expected 1, got %d", len(retrieved.Services))
	}

	if len(retrieved.Extensions) != 2 {
		t.Errorf("Extensions not preserved: expected 2, got %d", len(retrieved.Extensions))
	}
}

func TestGetCacheCount(t *testing.T) {
	mc, db := setupTestMetadataCache(t)
	defer db.Close()

	// Initially should be 0
	count, err := mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected initial count 0, got %d", count)
	}

	// Add entries
	for i := 0; i < 10; i++ {
		peerID := string(rune('a' + i))
		metadata := createTestMetadata(peerID, "test-topic")
		err := mc.CacheMetadata(peerID, metadata, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to cache metadata: %v", err)
		}
	}

	// Count should be 10
	count, err = mc.GetCacheCount()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}
	if count != 10 {
		t.Errorf("Expected count 10, got %d", count)
	}
}

func TestCachedMetadataStruct(t *testing.T) {
	cached := &CachedMetadata{
		PeerID:     "test-peer",
		Metadata:   createTestMetadata("test-peer", "test-topic"),
		CachedAt:   time.Now(),
		TTLSeconds: 300,
	}

	// Verify fields
	if cached.PeerID != "test-peer" {
		t.Errorf("Expected peer_id 'test-peer', got '%s'", cached.PeerID)
	}

	if cached.Metadata == nil {
		t.Error("Metadata should not be nil")
	}

	if cached.CachedAt.IsZero() {
		t.Error("CachedAt should not be zero")
	}

	if cached.TTLSeconds != 300 {
		t.Errorf("Expected TTL 300, got %d", cached.TTLSeconds)
	}
}
