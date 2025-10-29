package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/services"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataPublisher handles publishing peer metadata to DHT via BEP_44
type MetadataPublisher struct {
	bep44Manager *BEP44Manager
	keyPair      *crypto.KeyPair
	logger       *utils.LogsManager
	config       *utils.ConfigManager
	dbManager    *database.SQLiteManager

	// Current metadata state
	currentMetadata *database.PeerMetadata
	currentSequence int64
	metadataMutex   sync.RWMutex

	// Republish control
	republishTicker   *time.Ticker
	stopChan          chan struct{}
	republishRunning  bool
	republishMutex    sync.Mutex

	// Callback for NAT peer metadata publishing (called after relay connection)
	onNATMetadataPublished func()
	callbackMutex          sync.RWMutex
}

// NewMetadataPublisher creates a new metadata publisher
func NewMetadataPublisher(
	bep44Manager *BEP44Manager,
	keyPair *crypto.KeyPair,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	dbManager *database.SQLiteManager,
) *MetadataPublisher {
	return &MetadataPublisher{
		bep44Manager:    bep44Manager,
		keyPair:         keyPair,
		logger:          logger,
		config:          config,
		dbManager:       dbManager,
		currentSequence: 0,
		stopChan:        make(chan struct{}),
	}
}

// PublishMetadata publishes initial metadata to DHT
// This should be called during node startup (Phase 1, Steps 1-2)
func (mp *MetadataPublisher) PublishMetadata(metadata *database.PeerMetadata) error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	// Store current metadata
	mp.currentMetadata = metadata
	mp.currentSequence = 0 // Initial publish starts at sequence 0

	mp.logger.Info(fmt.Sprintf("Metadata publish starting: peer_id=%s, seq=%d, files=%d, apps=%d",
		mp.keyPair.PeerID(), mp.currentSequence, metadata.FilesCount, metadata.AppsCount), "metadata-publisher")

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0] // Use first topic

	// Publish to DHT with store peer discovery
	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, metadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		mp.logger.Error(fmt.Sprintf("Metadata publish FAILED: %v", err), "metadata-publisher")
		return fmt.Errorf("failed to publish metadata to DHT: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Metadata publish SUCCESS (seq=%d)", mp.currentSequence), "metadata-publisher")

	// Verify published metadata can be retrieved
	mp.logger.Debug("Verifying published metadata retrieval...", "metadata-publisher")
	time.Sleep(1 * time.Second) // Give DHT nodes time to process the PUT

	retrievedMetadata, err := mp.bep44Manager.GetMutable(mp.keyPair.PublicKey)
	if err != nil {
		mp.logger.Error(fmt.Sprintf("Metadata publish VERIFICATION FAILED: Cannot retrieve just-published metadata: %v", err), "metadata-publisher")
		// Don't fail the publish operation - metadata was sent, retrieval might work later
	} else {
		mp.logger.Info(fmt.Sprintf("Metadata publish VERIFICATION SUCCESS: Retrieved metadata seq=%d", retrievedMetadata.Sequence), "metadata-publisher")
	}

	return nil
}

// UpdateMetadata updates existing metadata in DHT with incremented sequence number
// This should be called when metadata changes (e.g., relay connection established)
func (mp *MetadataPublisher) UpdateMetadata(metadata *database.PeerMetadata) error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	// Increment sequence number
	mp.currentSequence++
	mp.currentMetadata = metadata

	mp.logger.Info(fmt.Sprintf("Metadata update starting: peer_id=%s, seq=%d",
		mp.keyPair.PeerID(), mp.currentSequence), "metadata-publisher")

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	// Publish updated metadata to DHT with store peer discovery
	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, metadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		mp.logger.Error(fmt.Sprintf("Metadata update FAILED: %v", err), "metadata-publisher")
		return fmt.Errorf("failed to update metadata in DHT: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Metadata update SUCCESS (seq=%d)", mp.currentSequence), "metadata-publisher")

	// Verify updated metadata can be retrieved
	mp.logger.Debug("Verifying updated metadata retrieval...", "metadata-publisher")
	time.Sleep(1 * time.Second) // Give DHT nodes time to process the PUT

	retrievedMetadata, err := mp.bep44Manager.GetMutable(mp.keyPair.PublicKey)
	if err != nil {
		mp.logger.Error(fmt.Sprintf("Metadata update VERIFICATION FAILED: Cannot retrieve just-updated metadata: %v", err), "metadata-publisher")
	} else {
		mp.logger.Info(fmt.Sprintf("Metadata update VERIFICATION SUCCESS: Retrieved metadata seq=%d", retrievedMetadata.Sequence), "metadata-publisher")
	}

	return nil
}

// Republish republishes current metadata to keep it alive in DHT
// BEP_44 data needs to be republished periodically (typically every ~60 minutes)
func (mp *MetadataPublisher) Republish() error {
	mp.metadataMutex.RLock()
	metadata := mp.currentMetadata
	sequence := mp.currentSequence
	mp.metadataMutex.RUnlock()

	if metadata == nil {
		return fmt.Errorf("no metadata to republish")
	}

	mp.logger.Debug(fmt.Sprintf("Republishing metadata to DHT (seq: %d)", sequence), "metadata-publisher")

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	// Republish with same sequence number (no changes, just keeping it alive)
	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, metadata, sequence, mp.dbManager, topic)
	if err != nil {
		return fmt.Errorf("failed to republish metadata: %v", err)
	}

	mp.logger.Debug(fmt.Sprintf("Successfully republished metadata (seq: %d)", sequence), "metadata-publisher")
	return nil
}

// StartPeriodicRepublish starts the periodic republishing goroutine
// Republishes every N minutes to keep metadata alive in DHT
func (mp *MetadataPublisher) StartPeriodicRepublish() {
	mp.republishMutex.Lock()
	defer mp.republishMutex.Unlock()

	// Check if already running
	if mp.republishRunning {
		mp.logger.Debug("Periodic republishing already running", "metadata-publisher")
		return
	}

	// Get republish interval from config (default: 30 minutes)
	intervalMinutes := mp.config.GetConfigInt("metadata_republish_interval_minutes", 30, 10, 120)
	interval := time.Duration(intervalMinutes) * time.Minute

	mp.logger.Info(fmt.Sprintf("Starting periodic metadata republishing (interval: %v)", interval), "metadata-publisher")

	// Create ticker
	mp.republishTicker = time.NewTicker(interval)
	mp.republishRunning = true

	// Start background goroutine
	go func() {
		for {
			select {
			case <-mp.republishTicker.C:
				mp.logger.Debug("Periodic republish triggered", "metadata-publisher")

				err := mp.Republish()
				if err != nil {
					mp.logger.Warn(fmt.Sprintf("Periodic republish failed: %v", err), "metadata-publisher")
				} else {
					mp.logger.Debug("Periodic republish completed successfully", "metadata-publisher")
				}

			case <-mp.stopChan:
				mp.logger.Info("Stopping periodic metadata republishing", "metadata-publisher")
				mp.republishMutex.Lock()
				mp.republishRunning = false
				mp.republishMutex.Unlock()
				return
			}
		}
	}()
}

// StopPeriodicRepublish stops the periodic republishing goroutine
func (mp *MetadataPublisher) StopPeriodicRepublish() {
	if mp.republishTicker != nil {
		mp.republishTicker.Stop()
	}

	close(mp.stopChan)
	mp.logger.Info("Periodic metadata republishing stopped", "metadata-publisher")
}

// GetCurrentSequence returns the current sequence number (for debugging)
func (mp *MetadataPublisher) GetCurrentSequence() int64 {
	mp.metadataMutex.RLock()
	defer mp.metadataMutex.RUnlock()
	return mp.currentSequence
}

// GetCurrentMetadata returns a copy of the current metadata (for debugging)
func (mp *MetadataPublisher) GetCurrentMetadata() *database.PeerMetadata {
	mp.metadataMutex.RLock()
	defer mp.metadataMutex.RUnlock()

	if mp.currentMetadata == nil {
		return nil
	}

	// Return a copy to prevent external modification
	metadataCopy := *mp.currentMetadata
	return &metadataCopy
}

// SetInitialMetadata stores initial metadata without publishing to DHT
// This is used for NAT peers that need to defer publishing until relay connection
func (mp *MetadataPublisher) SetInitialMetadata(metadata *database.PeerMetadata) {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	mp.currentMetadata = metadata
	mp.currentSequence = 0 // Will be used when NotifyRelayConnected publishes

	mp.logger.Debug(fmt.Sprintf("Stored initial metadata for deferred publishing (peer_id: %s)",
		mp.keyPair.PeerID()), "metadata-publisher")
}

// SetNATMetadataPublishedCallback sets a callback to be called after NAT peer metadata is published
// This allows PeerManager to connect to known peers after metadata is available in DHT
func (mp *MetadataPublisher) SetNATMetadataPublishedCallback(callback func()) {
	mp.callbackMutex.Lock()
	defer mp.callbackMutex.Unlock()
	mp.onNATMetadataPublished = callback
}

// NotifyRelayConnected is a helper function to update metadata when relay connection is established
// This is specifically for NAT peers that connect to a relay (Phase 3, Step 12)
func (mp *MetadataPublisher) NotifyRelayConnected(relayNodeID, relaySessionID, relayAddress string) error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	if mp.currentMetadata == nil {
		return fmt.Errorf("no metadata initialized")
	}

	mp.logger.Info(fmt.Sprintf("Updating metadata with relay info (relay: %s, session: %s, files=%d, apps=%d)",
		relayNodeID, relaySessionID, mp.currentMetadata.FilesCount, mp.currentMetadata.AppsCount), "metadata-publisher")

	// Update metadata with relay information
	mp.currentMetadata.NetworkInfo.UsingRelay = true
	mp.currentMetadata.NetworkInfo.ConnectedRelay = relayNodeID
	mp.currentMetadata.NetworkInfo.RelaySessionID = relaySessionID
	mp.currentMetadata.NetworkInfo.RelayAddress = relayAddress
	mp.currentMetadata.Timestamp = time.Now()

	// Increment sequence and publish
	mp.currentSequence++

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, mp.currentMetadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		return fmt.Errorf("failed to update metadata with relay info: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata with relay info (seq: %d)", mp.currentSequence), "metadata-publisher")

	// Metadata is now stored only in DHT - no local database storage

	// Start periodic republishing if not already running
	// This handles NAT nodes that deferred initial publishing until relay connection
	go mp.StartPeriodicRepublish()

	// Call NAT metadata published callback if set (allows PeerManager to connect to known peers)
	mp.callbackMutex.RLock()
	callback := mp.onNATMetadataPublished
	mp.callbackMutex.RUnlock()

	if callback != nil {
		mp.logger.Debug("Calling NAT metadata published callback", "metadata-publisher")
		go callback() // Call in goroutine to avoid blocking
	}

	return nil
}

// NotifyRelayDisconnected is a helper function to update metadata when relay connection is lost
func (mp *MetadataPublisher) NotifyRelayDisconnected() error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	if mp.currentMetadata == nil {
		return fmt.Errorf("no metadata initialized")
	}

	mp.logger.Info("Updating metadata: relay disconnected", "metadata-publisher")

	// Remove relay information
	mp.currentMetadata.NetworkInfo.UsingRelay = false
	mp.currentMetadata.NetworkInfo.ConnectedRelay = ""
	mp.currentMetadata.NetworkInfo.RelaySessionID = ""
	mp.currentMetadata.NetworkInfo.RelayAddress = ""
	mp.currentMetadata.Timestamp = time.Now()

	// Increment sequence and publish
	mp.currentSequence++

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, mp.currentMetadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		return fmt.Errorf("failed to update metadata after relay disconnect: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata after relay disconnect (seq: %d)", mp.currentSequence), "metadata-publisher")

	// Metadata is now stored only in DHT - no local database storage
	return nil
}

// NotifyIPChange is a helper function to update metadata when IP address changes
func (mp *MetadataPublisher) NotifyIPChange(newPublicIP, newPrivateIP string) error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	if mp.currentMetadata == nil {
		return fmt.Errorf("no metadata initialized")
	}

	mp.logger.Info(fmt.Sprintf("Updating metadata with new IPs (public: %s, private: %s)",
		newPublicIP, newPrivateIP), "metadata-publisher")

	// Update IP addresses
	if newPublicIP != "" {
		mp.currentMetadata.NetworkInfo.PublicIP = newPublicIP
	}
	if newPrivateIP != "" {
		mp.currentMetadata.NetworkInfo.PrivateIP = newPrivateIP
	}
	mp.currentMetadata.Timestamp = time.Now()

	// Increment sequence and publish
	mp.currentSequence++

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	err := mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, mp.currentMetadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		return fmt.Errorf("failed to update metadata with new IPs: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata with new IPs (seq: %d)", mp.currentSequence), "metadata-publisher")

	// Metadata is now stored only in DHT - no local database storage
	return nil
}

// UpdateServiceMetadata updates metadata with current service counts and republishes to DHT
// This should be called whenever services are added, removed, or their status changes
func (mp *MetadataPublisher) UpdateServiceMetadata() error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	if mp.currentMetadata == nil {
		return fmt.Errorf("no metadata initialized")
	}

	// Count current services
	serviceCounts, err := services.CountLocalServices(mp.dbManager)
	if err != nil {
		mp.logger.Error(fmt.Sprintf("Failed to count services: %v", err), "metadata-publisher")
		return fmt.Errorf("failed to count services: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Updating metadata with service counts: files=%d, apps=%d (previous: files=%d, apps=%d)",
		serviceCounts.FilesCount, serviceCounts.AppsCount, mp.currentMetadata.FilesCount, mp.currentMetadata.AppsCount), "metadata-publisher")

	// Update service counts in metadata
	mp.currentMetadata.FilesCount = serviceCounts.FilesCount
	mp.currentMetadata.AppsCount = serviceCounts.AppsCount
	mp.currentMetadata.Timestamp = time.Now()

	// Increment sequence and publish
	mp.currentSequence++

	// Get topic from config
	topics := mp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	topic := topics[0]

	err = mp.bep44Manager.PutMutableWithDiscovery(mp.keyPair, mp.currentMetadata, mp.currentSequence, mp.dbManager, topic)
	if err != nil {
		return fmt.Errorf("failed to update metadata with service counts: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata with service counts (seq: %d)", mp.currentSequence), "metadata-publisher")

	return nil
}
