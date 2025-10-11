package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataQueryService handles on-demand metadata queries from DHT with caching
type MetadataQueryService struct {
	bep44Manager  *BEP44Manager
	dbManager     *database.SQLiteManager
	logger        *utils.LogsManager
	config        *utils.ConfigManager

	// In-flight query tracking to prevent duplicate queries
	inflightQueries map[string]*queryResult
	inflightMutex   sync.RWMutex
}

// queryResult tracks an in-flight query
type queryResult struct {
	metadata *database.PeerMetadata
	err      error
	ready    chan struct{}
}

// NewMetadataQueryService creates a new metadata query service
func NewMetadataQueryService(
	bep44Manager *BEP44Manager,
	dbManager *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *MetadataQueryService {
	return &MetadataQueryService{
		bep44Manager:    bep44Manager,
		dbManager:       dbManager,
		logger:          logger,
		config:          config,
		inflightQueries: make(map[string]*queryResult),
	}
}

// QueryMetadata queries metadata for a peer (cache-first, then DHT)
// This is the main entry point for getting peer metadata on-demand
func (mqs *MetadataQueryService) QueryMetadata(peerID string, publicKey []byte) (*database.PeerMetadata, error) {
	// Step 1: Check cache first
	cached, exists, err := mqs.dbManager.MetadataCache.GetCachedMetadata(peerID)
	if err != nil {
		mqs.logger.Warn(fmt.Sprintf("Cache lookup failed for %s: %v", peerID[:8], err), "metadata-query")
	}

	if exists && cached != nil {
		mqs.logger.Debug(fmt.Sprintf("Cache HIT for peer %s", peerID[:8]), "metadata-query")
		return cached, nil
	}

	mqs.logger.Debug(fmt.Sprintf("Cache MISS for peer %s, querying DHT", peerID[:8]), "metadata-query")

	// Step 2: Check if there's already an in-flight query for this peer
	result := mqs.getOrCreateInflightQuery(peerID)

	if result == nil {
		// We created a new query, execute it
		return mqs.executeQuery(peerID, publicKey)
	}

	// Wait for existing query to complete
	mqs.logger.Debug(fmt.Sprintf("Waiting for in-flight query for peer %s", peerID[:8]), "metadata-query")
	<-result.ready

	return result.metadata, result.err
}

// getOrCreateInflightQuery gets or creates an in-flight query tracking entry
func (mqs *MetadataQueryService) getOrCreateInflightQuery(peerID string) *queryResult {
	mqs.inflightMutex.Lock()
	defer mqs.inflightMutex.Unlock()

	// Check if query already in flight
	if result, exists := mqs.inflightQueries[peerID]; exists {
		return result
	}

	// Create new query tracking entry
	result := &queryResult{
		ready: make(chan struct{}),
	}
	mqs.inflightQueries[peerID] = result

	return nil // Signal that we should execute the query
}

// executeQuery executes the DHT query and updates cache
func (mqs *MetadataQueryService) executeQuery(peerID string, publicKey []byte) (*database.PeerMetadata, error) {
	startTime := time.Now()

	// Get the in-flight query result (we know it exists because we just created it)
	mqs.inflightMutex.RLock()
	result := mqs.inflightQueries[peerID]
	mqs.inflightMutex.RUnlock()

	defer func() {
		// Mark query as complete
		close(result.ready)

		// Remove from in-flight tracking after a short delay
		time.AfterFunc(1*time.Second, func() {
			mqs.inflightMutex.Lock()
			delete(mqs.inflightQueries, peerID)
			mqs.inflightMutex.Unlock()
		})
	}()

	// Query DHT using BEP_44
	mqs.logger.Info(fmt.Sprintf("Querying DHT for peer %s metadata", peerID[:8]), "metadata-query")

	mutableData, err := mqs.bep44Manager.GetMutable(publicKey)
	if err != nil {
		result.err = fmt.Errorf("DHT query failed: %v", err)
		mqs.logger.Warn(fmt.Sprintf("DHT query failed for %s: %v", peerID[:8], err), "metadata-query")
		return nil, result.err
	}

	// Parse metadata from mutable data
	metadata, err := mqs.parseMetadata(mutableData.Value)
	if err != nil {
		result.err = fmt.Errorf("failed to parse metadata: %v", err)
		mqs.logger.Warn(fmt.Sprintf("Metadata parsing failed for %s: %v", peerID[:8], err), "metadata-query")
		return nil, result.err
	}

	// Cache the metadata (5-minute TTL)
	cacheTTL := 5 * time.Minute
	if err := mqs.dbManager.MetadataCache.CacheMetadata(peerID, metadata, cacheTTL); err != nil {
		mqs.logger.Warn(fmt.Sprintf("Failed to cache metadata for %s: %v", peerID[:8], err), "metadata-query")
		// Don't fail the query if caching fails
	}

	queryDuration := time.Since(startTime)
	mqs.logger.Info(fmt.Sprintf("Successfully queried metadata for %s from DHT (took %v)",
		peerID[:8], queryDuration), "metadata-query")

	result.metadata = metadata
	return metadata, nil
}

// parseMetadata parses bencoded metadata from DHT into PeerMetadata struct
func (mqs *MetadataQueryService) parseMetadata(data []byte) (*database.PeerMetadata, error) {
	// For now, we'll use JSON parsing since our metadata publisher uses bencode.Marshal
	// which in our implementation wraps the PeerMetadata struct
	// In a production system, you'd properly bencode/decode the struct

	var metadata database.PeerMetadata
	// TODO: Implement proper bencode parsing
	// For now, this is a placeholder that will be implemented when integrating with the publisher

	return &metadata, fmt.Errorf("metadata parsing not yet implemented - requires bencode deserialization")
}

// BatchQueryMetadata queries metadata for multiple peers in parallel
func (mqs *MetadataQueryService) BatchQueryMetadata(peers []*database.KnownPeer) map[string]*database.PeerMetadata {
	results := make(map[string]*database.PeerMetadata)
	resultsMutex := sync.Mutex{}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent queries

	for _, peer := range peers {
		wg.Add(1)

		go func(p *database.KnownPeer) {
			defer wg.Done()

			semaphore <- struct{}{} // Acquire
			defer func() { <-semaphore }() // Release

			metadata, err := mqs.QueryMetadata(p.PeerID, p.PublicKey)
			if err != nil {
				mqs.logger.Debug(fmt.Sprintf("Batch query failed for %s: %v", p.PeerID[:8], err), "metadata-query")
				return
			}

			resultsMutex.Lock()
			results[p.PeerID] = metadata
			resultsMutex.Unlock()
		}(peer)
	}

	wg.Wait()

	mqs.logger.Debug(fmt.Sprintf("Batch query completed: %d/%d successful", len(results), len(peers)), "metadata-query")

	return results
}

// InvalidateCache invalidates cached metadata for a specific peer
// Used when we know the peer's metadata has changed (e.g., relay disconnect)
func (mqs *MetadataQueryService) InvalidateCache(peerID string) error {
	return mqs.dbManager.MetadataCache.DeleteCachedMetadata(peerID)
}

// GetCacheStats returns cache statistics
func (mqs *MetadataQueryService) GetCacheStats() (map[string]interface{}, error) {
	return mqs.dbManager.MetadataCache.GetCacheStats()
}
