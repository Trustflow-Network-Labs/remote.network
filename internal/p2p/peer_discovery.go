package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeerDiscoveryService discovers and filters connectable peers
type PeerDiscoveryService struct {
	metadataQuery        *MetadataQueryService
	connectabilityFilter *ConnectabilityFilter
	knownPeers           *KnownPeersManager
	logger               *utils.LogsManager
	config               *utils.ConfigManager
}

// DiscoveredPeer represents a peer that has been discovered and filtered
type DiscoveredPeer struct {
	PeerID           string
	PublicKey        []byte
	Metadata         *database.PeerMetadata
	ConnectionMethod ConnectionMethod
	DiscoveredAt     time.Time
}

// DiscoveryOptions configures peer discovery behavior
type DiscoveryOptions struct {
	Topic              string
	MaxPeers           int           // Maximum number of peers to discover
	IncludeUnreachable bool          // Include peers that are not connectable
	CacheOnly          bool          // Only use cached metadata, don't query DHT
	Timeout            time.Duration // Timeout for entire discovery operation
}

// NewPeerDiscoveryService creates a new peer discovery service
func NewPeerDiscoveryService(
	metadataQuery *MetadataQueryService,
	connectabilityFilter *ConnectabilityFilter,
	knownPeers *KnownPeersManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *PeerDiscoveryService {
	return &PeerDiscoveryService{
		metadataQuery:        metadataQuery,
		connectabilityFilter: connectabilityFilter,
		knownPeers:           knownPeers,
		logger:               logger,
		config:               config,
	}
}

// DiscoverPeers discovers and filters connectable peers based on options
func (pds *PeerDiscoveryService) DiscoverPeers(options DiscoveryOptions) ([]*DiscoveredPeer, error) {
	startTime := time.Now()

	// Set defaults
	if options.MaxPeers == 0 {
		options.MaxPeers = 50
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}

	pds.logger.Info(fmt.Sprintf("Starting peer discovery (topic: %s, max: %d)",
		options.Topic, options.MaxPeers), "peer-discovery")

	// Step 1: Get known peers from database
	knownPeers, err := pds.getKnownPeersForDiscovery(options.Topic, options.MaxPeers*2)
	if err != nil {
		return nil, fmt.Errorf("failed to get known peers: %v", err)
	}

	pds.logger.Debug(fmt.Sprintf("Retrieved %d known peers from database", len(knownPeers)), "peer-discovery")

	if len(knownPeers) == 0 {
		pds.logger.Info("No known peers found for discovery", "peer-discovery")
		return []*DiscoveredPeer{}, nil
	}

	// Step 2: Query metadata for each peer (with timeout)
	ctx, cancel := pds.createDiscoveryContext(options.Timeout)
	defer cancel()

	discovered := pds.queryPeerMetadata(ctx, knownPeers, options)

	// Step 3: Filter by connectability
	if !options.IncludeUnreachable {
		discovered = pds.filterConnectablePeers(discovered)
	}

	// Step 4: Limit to max peers
	if len(discovered) > options.MaxPeers {
		discovered = discovered[:options.MaxPeers]
	}

	discoveryDuration := time.Since(startTime)
	pds.logger.Info(fmt.Sprintf("Peer discovery completed: %d peers discovered in %v",
		len(discovered), discoveryDuration), "peer-discovery")

	return discovered, nil
}

// getKnownPeersForDiscovery retrieves known peers suitable for discovery
func (pds *PeerDiscoveryService) getKnownPeersForDiscovery(topic string, limit int) ([]*KnownPeer, error) {
	// Get recent known peers (sorted by last_seen)
	knownPeers, err := pds.knownPeers.GetRecentKnownPeers(limit, topic)
	if err != nil {
		return nil, err
	}

	// Filter out peers without public keys (migrated data)
	var validPeers []*KnownPeer
	for _, peer := range knownPeers {
		if len(peer.PublicKey) == 0 {
			pds.logger.Debug(fmt.Sprintf("Skipping peer %s without public key", peer.PeerID[:8]), "peer-discovery")
			continue
		}
		validPeers = append(validPeers, peer)
	}

	return validPeers, nil
}

// createDiscoveryContext creates a context with timeout for discovery
func (pds *PeerDiscoveryService) createDiscoveryContext(timeout time.Duration) (chan struct{}, func()) {
	ctx := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		close(ctx)
	})

	cancel := func() {
		timer.Stop()
		select {
		case <-ctx:
			// Already closed
		default:
			close(ctx)
		}
	}

	return ctx, cancel
}

// queryPeerMetadata queries metadata for all known peers
func (pds *PeerDiscoveryService) queryPeerMetadata(
	ctx chan struct{},
	knownPeers []*KnownPeer,
	options DiscoveryOptions,
) []*DiscoveredPeer {
	var discovered []*DiscoveredPeer
	var mutex sync.Mutex

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent queries

	for _, peer := range knownPeers {
		wg.Add(1)

		go func(p *KnownPeer) {
			defer wg.Done()

			// Check timeout
			select {
			case <-ctx:
				pds.logger.Debug("Discovery timeout reached", "peer-discovery")
				return
			default:
			}

			// Acquire semaphore
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx:
				return
			}

			// Query metadata from DHT
			var metadata *database.PeerMetadata
			var err error

			if options.CacheOnly {
				// CacheOnly mode no longer supported (no database cache)
				pds.logger.Debug(fmt.Sprintf("CacheOnly mode skipped for %s (cache removed)", p.PeerID[:8]), "peer-discovery")
				return
			}

			// Query DHT for metadata
			metadata, err = pds.metadataQuery.QueryMetadata(p.PeerID, p.PublicKey)
			if err != nil {
				pds.logger.Debug(fmt.Sprintf("Failed to query metadata for %s: %v", p.PeerID[:8], err), "peer-discovery")
				return
			}

			// Validate metadata
			if err := pds.connectabilityFilter.ValidateMetadata(metadata); err != nil {
				pds.logger.Debug(fmt.Sprintf("Invalid metadata for %s: %v", p.PeerID[:8], err), "peer-discovery")
				return
			}

			// Create discovered peer
			discoveredPeer := &DiscoveredPeer{
				PeerID:           p.PeerID,
				PublicKey:        p.PublicKey,
				Metadata:         metadata,
				ConnectionMethod: pds.connectabilityFilter.GetConnectionMethod(metadata),
				DiscoveredAt:     time.Now(),
			}

			mutex.Lock()
			discovered = append(discovered, discoveredPeer)
			mutex.Unlock()

			pds.logger.Debug(fmt.Sprintf("Discovered peer %s (method: %s)",
				p.PeerID[:8], ConnectionMethodString(discoveredPeer.ConnectionMethod)), "peer-discovery")
		}(peer)
	}

	wg.Wait()

	return discovered
}

// filterConnectablePeers filters to only connectable peers
func (pds *PeerDiscoveryService) filterConnectablePeers(peers []*DiscoveredPeer) []*DiscoveredPeer {
	var connectable []*DiscoveredPeer

	for _, peer := range peers {
		if peer.ConnectionMethod != ConnectionMethodNone {
			connectable = append(connectable, peer)
		} else {
			pds.logger.Debug(fmt.Sprintf("Filtered out non-connectable peer %s: %s",
				peer.PeerID[:8], pds.connectabilityFilter.GetConnectabilityReason(peer.Metadata)), "peer-discovery")
		}
	}

	pds.logger.Debug(fmt.Sprintf("Filtered %d/%d peers as connectable", len(connectable), len(peers)), "peer-discovery")

	return connectable
}

// DiscoverConnectablePeers is a convenience method for discovering only connectable peers
func (pds *PeerDiscoveryService) DiscoverConnectablePeers(topic string, maxPeers int) ([]*DiscoveredPeer, error) {
	return pds.DiscoverPeers(DiscoveryOptions{
		Topic:              topic,
		MaxPeers:           maxPeers,
		IncludeUnreachable: false,
		CacheOnly:          false,
		Timeout:            30 * time.Second,
	})
}

// GetConnectablePeersCount returns the count of connectable peers for a topic
func (pds *PeerDiscoveryService) GetConnectablePeersCount(topic string) (int, error) {
	// Quick discovery with cache only
	discovered, err := pds.DiscoverPeers(DiscoveryOptions{
		Topic:              topic,
		MaxPeers:           1000,
		IncludeUnreachable: false,
		CacheOnly:          true,
		Timeout:            5 * time.Second,
	})
	if err != nil {
		return 0, err
	}

	return len(discovered), nil
}

// RefreshPeerMetadata forces a metadata refresh for a specific peer
func (pds *PeerDiscoveryService) RefreshPeerMetadata(peerID string, publicKey []byte) (*DiscoveredPeer, error) {
	pds.logger.Debug(fmt.Sprintf("Refreshing metadata for peer %s", peerID[:8]), "peer-discovery")

	// Query fresh metadata from DHT (no cache to invalidate)
	metadata, err := pds.metadataQuery.QueryMetadata(peerID, publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh metadata: %v", err)
	}

	// Validate metadata
	if err := pds.connectabilityFilter.ValidateMetadata(metadata); err != nil {
		return nil, fmt.Errorf("invalid metadata: %v", err)
	}

	discoveredPeer := &DiscoveredPeer{
		PeerID:           peerID,
		PublicKey:        publicKey,
		Metadata:         metadata,
		ConnectionMethod: pds.connectabilityFilter.GetConnectionMethod(metadata),
		DiscoveredAt:     time.Now(),
	}

	pds.logger.Info(fmt.Sprintf("Refreshed metadata for peer %s (method: %s)",
		peerID[:8], ConnectionMethodString(discoveredPeer.ConnectionMethod)), "peer-discovery")

	return discoveredPeer, nil
}

// GetDiscoveryStats returns statistics about peer discovery
func (pds *PeerDiscoveryService) GetDiscoveryStats(topic string) (map[string]interface{}, error) {
	// Get known peers count
	knownPeersCount, err := pds.knownPeers.GetKnownPeersCount()
	if err != nil {
		return nil, err
	}

	// Cache stats no longer available (database cache removed)
	cacheStats := map[string]interface{}{
		"note": "Database-level caching removed",
	}

	// Quick connectable peers count (cache only)
	connectableCount, err := pds.GetConnectablePeersCount(topic)
	if err != nil {
		pds.logger.Warn(fmt.Sprintf("Failed to get connectable peers count: %v", err), "peer-discovery")
		connectableCount = -1 // Indicate error
	}

	stats := map[string]interface{}{
		"known_peers_count":     knownPeersCount,
		"connectable_peers_count": connectableCount,
		"cache_stats":           cacheStats,
		"topic":                 topic,
	}

	return stats, nil
}
