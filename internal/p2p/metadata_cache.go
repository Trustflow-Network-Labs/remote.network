package p2p

import (
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataCache provides in-memory caching of DHT-retrieved metadata with reference counting
// Metadata is cleaned immediately when no longer in use (RefCount = 0)
type MetadataCache struct {
	cache  map[string]*CachedMetadata
	mutex  sync.RWMutex
	logger *utils.LogsManager
}

// CachedMetadata represents a cached peer metadata entry with reference counting
type CachedMetadata struct {
	Metadata  *database.PeerMetadata
	CachedAt  time.Time
	RefCount  int  // Number of active users of this metadata
}

// NewMetadataCache creates a new in-memory metadata cache
func NewMetadataCache(logger *utils.LogsManager) *MetadataCache {
	return &MetadataCache{
		cache:  make(map[string]*CachedMetadata),
		logger: logger,
	}
}

// Acquire gets metadata from cache and increments reference count
// Returns nil if not found in cache
func (mc *MetadataCache) Acquire(nodeID string) *database.PeerMetadata {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	if cached, exists := mc.cache[nodeID]; exists {
		cached.RefCount++
		mc.logger.Debug("Acquired cached metadata for "+nodeID[:8]+" (refs: "+string(rune(cached.RefCount))+")", "metadata-cache")
		return cached.Metadata
	}

	return nil
}

// Release decrements reference count and removes from cache if zero
func (mc *MetadataCache) Release(nodeID string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	if cached, exists := mc.cache[nodeID]; exists {
		cached.RefCount--
		mc.logger.Debug("Released cached metadata for "+nodeID[:8]+" (refs: "+string(rune(cached.RefCount))+")", "metadata-cache")

		if cached.RefCount <= 0 {
			delete(mc.cache, nodeID)
			mc.logger.Debug("Removed cached metadata for "+nodeID[:8]+" (no active refs)", "metadata-cache")
		}
	}
}

// Store adds metadata to cache with initial reference count of 0
// Caller should call Acquire() immediately if they want to use it
func (mc *MetadataCache) Store(nodeID string, metadata *database.PeerMetadata) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	mc.cache[nodeID] = &CachedMetadata{
		Metadata:  metadata,
		CachedAt:  time.Now(),
		RefCount:  0,
	}

	mc.logger.Debug("Stored metadata in cache for "+nodeID[:8], "metadata-cache")
}

// Get retrieves metadata without affecting reference count (read-only)
// Useful for checking if metadata exists in cache
func (mc *MetadataCache) Get(nodeID string) *database.PeerMetadata {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	if cached, exists := mc.cache[nodeID]; exists {
		return cached.Metadata
	}

	return nil
}

// Invalidate removes metadata from cache regardless of reference count
// Use with caution - should only be used when metadata is known to be stale
func (mc *MetadataCache) Invalidate(nodeID string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	if _, exists := mc.cache[nodeID]; exists {
		delete(mc.cache, nodeID)
		mc.logger.Info("Invalidated cached metadata for "+nodeID[:8], "metadata-cache")
	}
}

// GetCacheSize returns the current number of cached entries
func (mc *MetadataCache) GetCacheSize() int {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	return len(mc.cache)
}

// GetStats returns cache statistics
func (mc *MetadataCache) GetStats() map[string]interface{} {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	totalRefs := 0
	for _, cached := range mc.cache {
		totalRefs += cached.RefCount
	}

	return map[string]interface{}{
		"cache_size":         len(mc.cache),
		"total_ref_count":    totalRefs,
	}
}

// Clear removes all entries from cache (used for testing/cleanup)
func (mc *MetadataCache) Clear() {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	mc.cache = make(map[string]*CachedMetadata)
	mc.logger.Info("Cleared metadata cache", "metadata-cache")
}
