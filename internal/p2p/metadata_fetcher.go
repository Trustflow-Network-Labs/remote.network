package p2p

import (
	"fmt"
	"sync"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataFetcher fetches peer metadata from DHT with prioritized node queries
// Query order: bootstrap nodes → relay nodes → general DHT
type MetadataFetcher struct {
	bep44Manager   *BEP44Manager
	logger         *utils.LogsManager

	// Priority nodes for faster DHT queries
	bootstrapNodes []string  // Connected bootstrap peer node IDs
	relayNodes     []string  // Connected relay peer node IDs
	nodesMutex     sync.RWMutex
}

// NewMetadataFetcher creates a new metadata fetcher
func NewMetadataFetcher(bep44Manager *BEP44Manager, logger *utils.LogsManager) *MetadataFetcher {
	return &MetadataFetcher{
		bep44Manager:   bep44Manager,
		logger:         logger,
		bootstrapNodes: make([]string, 0),
		relayNodes:     make([]string, 0),
	}
}

// GetPeerMetadata fetches peer metadata from DHT with priority node queries
// Returns metadata or error if not found/failed
// Queries bootstrap and relay nodes first, then falls back to general DHT
func (mf *MetadataFetcher) GetPeerMetadata(publicKey []byte) (*database.PeerMetadata, error) {
	if len(publicKey) != 32 {
		return nil, fmt.Errorf("invalid public key length: %d (expected 32)", len(publicKey))
	}

	mf.nodesMutex.RLock()
	bootstrapNodes := append([]string{}, mf.bootstrapNodes...)
	relayNodes := append([]string{}, mf.relayNodes...)
	mf.nodesMutex.RUnlock()

	// Combine priority nodes: bootstrap nodes first, then relay nodes
	priorityNodes := append(bootstrapNodes, relayNodes...)

	mf.logger.Debug(fmt.Sprintf("Fetching metadata from DHT (priority: %d bootstrap, %d relay nodes)",
		len(bootstrapNodes), len(relayNodes)), "metadata-fetcher")

	// Query DHT via BEP44 with priority routing
	mutableData, err := mf.bep44Manager.GetMutableWithPriority(publicKey, priorityNodes)
	if err != nil {
		mf.logger.Warn(fmt.Sprintf("Failed to fetch metadata from DHT: %v", err), "metadata-fetcher")
		return nil, fmt.Errorf("DHT GET failed: %v", err)
	}

	if mutableData == nil || mutableData.Value == nil {
		mf.logger.Warn("Metadata not found in DHT", "metadata-fetcher")
		return nil, fmt.Errorf("metadata not found in DHT")
	}

	// Parse metadata from BEP44 value
	// BEP44 stores metadata as bencode - must decode through BencodePeerMetadata first
	// to properly handle bencode-safe format (Unix timestamps, etc.)
	mf.logger.Info("🔧 FIXED METADATA DECODER - Using DecodeBencodedMetadata()", "metadata-fetcher")

	metadata, err := database.DecodeBencodedMetadata(mutableData.Value)
	if err != nil {
		mf.logger.Warn(fmt.Sprintf("Failed to decode bencoded metadata: %v", err), "metadata-fetcher")
		return nil, fmt.Errorf("failed to decode metadata: %v", err)
	}

	mf.logger.Info(fmt.Sprintf("✅ Successfully decoded metadata from DHT (node_id: %s, topic: %s, version: %d, is_relay: %v, relay_endpoint: %s, files=%d, apps=%d)",
		metadata.NodeID, metadata.Topic, metadata.Version, metadata.NetworkInfo.IsRelay, metadata.NetworkInfo.RelayEndpoint, metadata.FilesCount, metadata.AppsCount), "metadata-fetcher")

	return metadata, nil
}

// UpdateBootstrapNodes updates the list of connected bootstrap nodes for priority queries
func (mf *MetadataFetcher) UpdateBootstrapNodes(nodes []string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	mf.bootstrapNodes = nodes
	mf.logger.Debug(fmt.Sprintf("Updated bootstrap nodes for DHT priority: %d nodes", len(nodes)), "metadata-fetcher")
}

// UpdateRelayNodes updates the list of connected relay nodes for priority queries
func (mf *MetadataFetcher) UpdateRelayNodes(nodes []string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	mf.relayNodes = nodes
	mf.logger.Debug(fmt.Sprintf("Updated relay nodes for DHT priority: %d nodes", len(nodes)), "metadata-fetcher")
}

// AddBootstrapNode adds a single bootstrap node to priority list
func (mf *MetadataFetcher) AddBootstrapNode(nodeID string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	// Check if already exists
	for _, existing := range mf.bootstrapNodes {
		if existing == nodeID {
			return
		}
	}

	mf.bootstrapNodes = append(mf.bootstrapNodes, nodeID)
	mf.logger.Debug(fmt.Sprintf("Added bootstrap node to DHT priority: %s", nodeID[:8]), "metadata-fetcher")
}

// AddRelayNode adds a single relay node to priority list
func (mf *MetadataFetcher) AddRelayNode(nodeID string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	// Check if already exists
	for _, existing := range mf.relayNodes {
		if existing == nodeID {
			return
		}
	}

	mf.relayNodes = append(mf.relayNodes, nodeID)
	mf.logger.Debug(fmt.Sprintf("Added relay node to DHT priority: %s", nodeID[:8]), "metadata-fetcher")
}

// RemoveBootstrapNode removes a bootstrap node from priority list
func (mf *MetadataFetcher) RemoveBootstrapNode(nodeID string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	for i, existing := range mf.bootstrapNodes {
		if existing == nodeID {
			mf.bootstrapNodes = append(mf.bootstrapNodes[:i], mf.bootstrapNodes[i+1:]...)
			mf.logger.Debug(fmt.Sprintf("Removed bootstrap node from DHT priority: %s", nodeID[:8]), "metadata-fetcher")
			return
		}
	}
}

// RemoveRelayNode removes a relay node from priority list
func (mf *MetadataFetcher) RemoveRelayNode(nodeID string) {
	mf.nodesMutex.Lock()
	defer mf.nodesMutex.Unlock()

	for i, existing := range mf.relayNodes {
		if existing == nodeID {
			mf.relayNodes = append(mf.relayNodes[:i], mf.relayNodes[i+1:]...)
			mf.logger.Debug(fmt.Sprintf("Removed relay node from DHT priority: %s", nodeID[:8]), "metadata-fetcher")
			return
		}
	}
}

// GetPriorityNodeCount returns the number of priority nodes configured
func (mf *MetadataFetcher) GetPriorityNodeCount() (bootstrap int, relay int) {
	mf.nodesMutex.RLock()
	defer mf.nodesMutex.RUnlock()

	return len(mf.bootstrapNodes), len(mf.relayNodes)
}
