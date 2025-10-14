package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
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

	mp.logger.Info(fmt.Sprintf("Publishing initial metadata to DHT (peer_id: %s, seq: %d)",
		mp.keyPair.PeerID(), mp.currentSequence), "metadata-publisher")

	// Publish to DHT
	err := mp.bep44Manager.PutMutable(mp.keyPair, metadata, mp.currentSequence)
	if err != nil {
		return fmt.Errorf("failed to publish metadata to DHT: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully published metadata to DHT (seq: %d)", mp.currentSequence), "metadata-publisher")
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

	mp.logger.Info(fmt.Sprintf("Updating metadata in DHT (peer_id: %s, seq: %d)",
		mp.keyPair.PeerID(), mp.currentSequence), "metadata-publisher")

	// Publish updated metadata to DHT
	err := mp.bep44Manager.PutMutable(mp.keyPair, metadata, mp.currentSequence)
	if err != nil {
		return fmt.Errorf("failed to update metadata in DHT: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata in DHT (seq: %d)", mp.currentSequence), "metadata-publisher")
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

	// Republish with same sequence number (no changes, just keeping it alive)
	err := mp.bep44Manager.PutMutable(mp.keyPair, metadata, sequence)
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

// NotifyRelayConnected is a helper function to update metadata when relay connection is established
// This is specifically for NAT peers that connect to a relay (Phase 3, Step 12)
func (mp *MetadataPublisher) NotifyRelayConnected(relayNodeID, relaySessionID, relayAddress string) error {
	mp.metadataMutex.Lock()
	defer mp.metadataMutex.Unlock()

	if mp.currentMetadata == nil {
		return fmt.Errorf("no metadata initialized")
	}

	mp.logger.Info(fmt.Sprintf("Updating metadata with relay info (relay: %s, session: %s)",
		relayNodeID, relaySessionID), "metadata-publisher")

	// Update metadata with relay information
	mp.currentMetadata.NetworkInfo.UsingRelay = true
	mp.currentMetadata.NetworkInfo.ConnectedRelay = relayNodeID
	mp.currentMetadata.NetworkInfo.RelaySessionID = relaySessionID
	mp.currentMetadata.NetworkInfo.RelayAddress = relayAddress
	mp.currentMetadata.Timestamp = time.Now()

	// Increment sequence and publish
	mp.currentSequence++

	err := mp.bep44Manager.PutMutable(mp.keyPair, mp.currentMetadata, mp.currentSequence)
	if err != nil {
		return fmt.Errorf("failed to update metadata with relay info: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata with relay info (seq: %d)", mp.currentSequence), "metadata-publisher")

	// Metadata is now stored only in DHT - no local database storage

	// Start periodic republishing if not already running
	// This handles NAT nodes that deferred initial publishing until relay connection
	go mp.StartPeriodicRepublish()

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

	err := mp.bep44Manager.PutMutable(mp.keyPair, mp.currentMetadata, mp.currentSequence)
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

	err := mp.bep44Manager.PutMutable(mp.keyPair, mp.currentMetadata, mp.currentSequence)
	if err != nil {
		return fmt.Errorf("failed to update metadata with new IPs: %v", err)
	}

	mp.logger.Info(fmt.Sprintf("Successfully updated metadata with new IPs (seq: %d)", mp.currentSequence), "metadata-publisher")

	// Metadata is now stored only in DHT - no local database storage
	return nil
}
