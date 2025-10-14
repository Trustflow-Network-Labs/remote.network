package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"

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

// QueryMetadata queries metadata for a peer from DHT
// This is the main entry point for getting peer metadata on-demand
func (mqs *MetadataQueryService) QueryMetadata(peerID string, publicKey []byte) (*database.PeerMetadata, error) {
	mqs.logger.Debug(fmt.Sprintf("Querying DHT for peer %s", peerID[:8]), "metadata-query")

	// Check if there's already an in-flight query for this peer
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

	queryDuration := time.Since(startTime)
	mqs.logger.Info(fmt.Sprintf("Successfully queried metadata for %s from DHT (took %v)",
		peerID[:8], queryDuration), "metadata-query")

	result.metadata = metadata
	return metadata, nil
}

// parseMetadata parses bencoded metadata from DHT into PeerMetadata struct
func (mqs *MetadataQueryService) parseMetadata(data []byte) (*database.PeerMetadata, error) {
	// The metadata publisher uses bencode.Marshal to encode the PeerMetadata struct
	// We use bencode.Unmarshal to decode it back

	var metadata database.PeerMetadata
	if err := bencode.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to decode bencoded metadata: %v", err)
	}

	return &metadata, nil
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

// Note: Cache invalidation and stats removed with database-level caching.
// Metadata is now always queried fresh from DHT with in-flight query deduplication.
