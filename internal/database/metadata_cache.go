package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataCache manages cached peer metadata with TTL
// Metadata is queried from DHT on-demand and cached for 5 minutes
type MetadataCache struct {
	db     *sql.DB
	logger *utils.LogsManager

	// Prepared statements
	cacheStmt    *sql.Stmt
	getStmt      *sql.Stmt
	deleteStmt   *sql.Stmt
	cleanupStmt  *sql.Stmt
	countStmt    *sql.Stmt
	getAllStmt   *sql.Stmt
}

// CachedMetadata represents a cached peer metadata entry
type CachedMetadata struct {
	PeerID      string
	Metadata    *PeerMetadata // Full metadata from DHT
	CachedAt    time.Time
	TTLSeconds  int
}

// NewMetadataCache creates a new metadata cache manager
func NewMetadataCache(db *sql.DB, logger *utils.LogsManager) (*MetadataCache, error) {
	mc := &MetadataCache{
		db:     db,
		logger: logger,
	}

	// Create table
	if err := mc.createTable(); err != nil {
		return nil, err
	}

	// Prepare statements
	if err := mc.prepareStatements(); err != nil {
		return nil, err
	}

	// Start periodic cleanup
	go mc.startPeriodicCleanup()

	return mc, nil
}

// createTable creates the metadata_cache table
func (mc *MetadataCache) createTable() error {
	createTableSQL := `
-- Metadata cache with 5-minute TTL
-- Full metadata queried from DHT is cached here to reduce query overhead
CREATE TABLE IF NOT EXISTS metadata_cache (
	"peer_id" TEXT PRIMARY KEY,
	"metadata_json" TEXT NOT NULL,  -- JSON-encoded PeerMetadata
	"cached_at" INTEGER NOT NULL,    -- Unix timestamp
	"ttl_seconds" INTEGER NOT NULL DEFAULT 300  -- 5 minutes
);

-- Index for cleanup queries
CREATE INDEX IF NOT EXISTS idx_metadata_cache_cached_at ON metadata_cache(cached_at);
`

	_, err := mc.db.ExecContext(context.Background(), createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create metadata_cache table: %v", err)
	}

	mc.logger.Debug("Created metadata_cache table successfully", "database")
	return nil
}

// prepareStatements prepares frequently used SQL statements
func (mc *MetadataCache) prepareStatements() error {
	var err error

	// Cache metadata
	mc.cacheStmt, err = mc.db.Prepare(`
		INSERT OR REPLACE INTO metadata_cache (peer_id, metadata_json, cached_at, ttl_seconds)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare cache statement: %v", err)
	}

	// Get cached metadata
	mc.getStmt, err = mc.db.Prepare(`
		SELECT peer_id, metadata_json, cached_at, ttl_seconds
		FROM metadata_cache
		WHERE peer_id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare get statement: %v", err)
	}

	// Delete cached entry
	mc.deleteStmt, err = mc.db.Prepare(`
		DELETE FROM metadata_cache
		WHERE peer_id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare delete statement: %v", err)
	}

	// Cleanup expired entries
	mc.cleanupStmt, err = mc.db.Prepare(`
		DELETE FROM metadata_cache
		WHERE (cached_at + ttl_seconds) < ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare cleanup statement: %v", err)
	}

	// Count cached entries
	mc.countStmt, err = mc.db.Prepare(`
		SELECT COUNT(*) FROM metadata_cache
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare count statement: %v", err)
	}

	// Get all cached entries
	mc.getAllStmt, err = mc.db.Prepare(`
		SELECT peer_id, metadata_json, cached_at, ttl_seconds
		FROM metadata_cache
		ORDER BY cached_at DESC
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare getAll statement: %v", err)
	}

	mc.logger.Debug("Prepared metadata_cache statements successfully", "database")
	return nil
}

// CacheMetadata stores metadata in the cache with specified TTL
func (mc *MetadataCache) CacheMetadata(peerID string, metadata *PeerMetadata, ttl time.Duration) error {
	// Serialize metadata to JSON
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %v", err)
	}

	now := time.Now().Unix()
	ttlSeconds := int(ttl.Seconds())

	_, err = mc.cacheStmt.Exec(peerID, string(metadataJSON), now, ttlSeconds)
	if err != nil {
		return fmt.Errorf("failed to cache metadata: %v", err)
	}

	mc.logger.Debug(fmt.Sprintf("Cached metadata for peer %s (ttl: %v)", peerID, ttl), "database")
	return nil
}

// GetCachedMetadata retrieves metadata from cache if not expired
func (mc *MetadataCache) GetCachedMetadata(peerID string) (*PeerMetadata, bool, error) {
	var metadataJSON string
	var cachedAtUnix int64
	var ttlSeconds int
	var retrievedPeerID string

	err := mc.getStmt.QueryRow(peerID).Scan(
		&retrievedPeerID,
		&metadataJSON,
		&cachedAtUnix,
		&ttlSeconds,
	)

	if err == sql.ErrNoRows {
		// Cache miss - not found
		mc.logger.Debug(fmt.Sprintf("Cache miss for peer %s", peerID), "database")
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached metadata: %v", err)
	}

	// Check if expired
	cachedAt := time.Unix(cachedAtUnix, 0)
	expiresAt := cachedAt.Add(time.Duration(ttlSeconds) * time.Second)

	if time.Now().After(expiresAt) {
		// Cache expired - delete and return miss
		mc.logger.Debug(fmt.Sprintf("Cache expired for peer %s (age: %v)",
			peerID, time.Since(cachedAt)), "database")
		mc.DeleteCachedMetadata(peerID)
		return nil, false, nil
	}

	// Deserialize metadata
	var metadata PeerMetadata
	err = json.Unmarshal([]byte(metadataJSON), &metadata)
	if err != nil {
		mc.logger.Warn(fmt.Sprintf("Failed to unmarshal cached metadata for %s: %v", peerID, err), "database")
		mc.DeleteCachedMetadata(peerID)
		return nil, false, nil
	}

	mc.logger.Debug(fmt.Sprintf("Cache hit for peer %s (age: %v, ttl: %ds)",
		peerID, time.Since(cachedAt), ttlSeconds), "database")

	return &metadata, true, nil
}

// DeleteCachedMetadata removes metadata from cache
func (mc *MetadataCache) DeleteCachedMetadata(peerID string) error {
	_, err := mc.deleteStmt.Exec(peerID)
	if err != nil {
		return fmt.Errorf("failed to delete cached metadata: %v", err)
	}

	mc.logger.Debug(fmt.Sprintf("Deleted cached metadata for peer %s", peerID), "database")
	return nil
}

// InvalidateCache removes all cached metadata (force refresh from DHT)
func (mc *MetadataCache) InvalidateCache() error {
	_, err := mc.db.Exec("DELETE FROM metadata_cache")
	if err != nil {
		return fmt.Errorf("failed to invalidate cache: %v", err)
	}

	mc.logger.Info("Invalidated entire metadata cache", "database")
	return nil
}

// CleanupExpired removes expired cache entries
func (mc *MetadataCache) CleanupExpired() (int, error) {
	now := time.Now().Unix()

	result, err := mc.cleanupStmt.Exec(now)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired cache: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()

	if rowsAffected > 0 {
		mc.logger.Debug(fmt.Sprintf("Cleaned up %d expired cache entries", rowsAffected), "database")
	}

	return int(rowsAffected), nil
}

// GetCacheCount returns the number of cached entries
func (mc *MetadataCache) GetCacheCount() (int, error) {
	var count int
	err := mc.countStmt.QueryRow().Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get cache count: %v", err)
	}
	return count, nil
}

// GetAllCachedMetadata retrieves all cached metadata (for debugging)
func (mc *MetadataCache) GetAllCachedMetadata() ([]*CachedMetadata, error) {
	rows, err := mc.getAllStmt.Query()
	if err != nil {
		return nil, fmt.Errorf("failed to query all cached metadata: %v", err)
	}
	defer rows.Close()

	var cached []*CachedMetadata

	for rows.Next() {
		var peerID string
		var metadataJSON string
		var cachedAtUnix int64
		var ttlSeconds int

		err := rows.Scan(&peerID, &metadataJSON, &cachedAtUnix, &ttlSeconds)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cached metadata row: %v", err)
		}

		var metadata PeerMetadata
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			mc.logger.Warn(fmt.Sprintf("Failed to unmarshal cached metadata for %s: %v", peerID, err), "database")
			continue
		}

		cached = append(cached, &CachedMetadata{
			PeerID:     peerID,
			Metadata:   &metadata,
			CachedAt:   time.Unix(cachedAtUnix, 0),
			TTLSeconds: ttlSeconds,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating cached metadata rows: %v", err)
	}

	return cached, nil
}

// GetCacheStats returns statistics about the cache
func (mc *MetadataCache) GetCacheStats() (map[string]interface{}, error) {
	count, err := mc.GetCacheCount()
	if err != nil {
		return nil, err
	}

	// Count expired entries
	var expiredCount int
	now := time.Now().Unix()
	err = mc.db.QueryRow(`
		SELECT COUNT(*) FROM metadata_cache
		WHERE (cached_at + ttl_seconds) < ?
	`, now).Scan(&expiredCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired count: %v", err)
	}

	// Get cache hit rate (we'll need to track this separately in production)
	stats := map[string]interface{}{
		"total_entries":   count,
		"expired_entries": expiredCount,
		"valid_entries":   count - expiredCount,
	}

	return stats, nil
}

// startPeriodicCleanup runs cleanup every minute to remove expired entries
func (mc *MetadataCache) startPeriodicCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	mc.logger.Info("Started periodic metadata cache cleanup (interval: 1 minute)", "database")

	for range ticker.C {
		cleaned, err := mc.CleanupExpired()
		if err != nil {
			mc.logger.Warn(fmt.Sprintf("Periodic cache cleanup failed: %v", err), "database")
		} else if cleaned > 0 {
			mc.logger.Debug(fmt.Sprintf("Periodic cache cleanup removed %d expired entries", cleaned), "database")
		}
	}
}

// Close closes prepared statements
func (mc *MetadataCache) Close() error {
	statements := []*sql.Stmt{
		mc.cacheStmt,
		mc.getStmt,
		mc.deleteStmt,
		mc.cleanupStmt,
		mc.countStmt,
		mc.getAllStmt,
	}

	for _, stmt := range statements {
		if stmt != nil {
			stmt.Close()
		}
	}

	mc.logger.Debug("Closed metadata_cache prepared statements", "database")
	return nil
}

// IsExpired checks if a cached entry is expired (for external checks)
func (cm *CachedMetadata) IsExpired() bool {
	expiresAt := cm.CachedAt.Add(time.Duration(cm.TTLSeconds) * time.Second)
	return time.Now().After(expiresAt)
}

// Age returns how long ago the metadata was cached
func (cm *CachedMetadata) Age() time.Duration {
	return time.Since(cm.CachedAt)
}

// TimeUntilExpiry returns how much time until the cache expires
func (cm *CachedMetadata) TimeUntilExpiry() time.Duration {
	expiresAt := cm.CachedAt.Add(time.Duration(cm.TTLSeconds) * time.Second)
	remaining := time.Until(expiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}
