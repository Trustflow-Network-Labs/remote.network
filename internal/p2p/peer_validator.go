package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeerValidator validates stale known_peers by querying DHT
// Removes peers that haven't been seen in 24h+ and are no longer in DHT
type PeerValidator struct {
	metadataFetcher *MetadataFetcher
	dbManager       *database.SQLiteManager
	logger          *utils.LogsManager
	config          *utils.ConfigManager

	// Validation control
	validationTicker *time.Ticker
	stopChan         chan struct{}
	running          bool
	runningMutex     sync.Mutex
}

// NewPeerValidator creates a new peer validator
func NewPeerValidator(
	metadataFetcher *MetadataFetcher,
	dbManager *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *PeerValidator {
	return &PeerValidator{
		metadataFetcher: metadataFetcher,
		dbManager:       dbManager,
		logger:          logger,
		config:          config,
		stopChan:        make(chan struct{}),
		running:         false,
	}
}

// ValidateStalePeers checks known_peers table for stale entries (24h+ old)
// Queries DHT to verify if peer is still active, removes if not found
func (pv *PeerValidator) ValidateStalePeers(topic string) error {
	pv.logger.Info("Starting stale peer validation", "peer-validator")

	// Get all known peers for topic
	knownPeers, err := pv.dbManager.KnownPeers.GetKnownPeersByTopic(topic)
	if err != nil {
		return fmt.Errorf("failed to get known peers: %v", err)
	}

	staleThreshold := time.Now().Add(-24 * time.Hour)
	staleCount := 0
	validatedCount := 0
	removedCount := 0

	for _, peer := range knownPeers {
		// Only validate peers not seen in 24+ hours
		if peer.LastSeen.After(staleThreshold) {
			continue
		}

		staleCount++
		pv.logger.Debug(fmt.Sprintf("Validating stale peer %s (last_seen: %v ago)",
			peer.PeerID[:8], time.Since(peer.LastSeen).Round(time.Minute)), "peer-validator")

		// Query DHT to check if peer still exists
		// Use public key to fetch metadata from DHT
		metadata, err := pv.metadataFetcher.GetPeerMetadata(peer.PublicKey)
		if err != nil || metadata == nil {
			// Peer not found in DHT - remove from known_peers
			pv.logger.Info(fmt.Sprintf("Removing stale peer %s (not found in DHT, last_seen: %v ago)",
				peer.PeerID[:8], time.Since(peer.LastSeen).Round(time.Hour)), "peer-validator")

			if err := pv.dbManager.KnownPeers.DeleteKnownPeer(peer.PeerID, topic); err != nil {
				pv.logger.Warn(fmt.Sprintf("Failed to remove stale peer %s: %v", peer.PeerID[:8], err), "peer-validator")
			} else {
				removedCount++
			}
			continue
		}

		// Peer still exists in DHT - update last_seen timestamp
		validatedCount++
		peer.LastSeen = time.Now()
		if err := pv.dbManager.KnownPeers.StoreKnownPeer(peer); err != nil {
			pv.logger.Warn(fmt.Sprintf("Failed to update validated peer %s: %v", peer.PeerID[:8], err), "peer-validator")
		} else {
			pv.logger.Debug(fmt.Sprintf("Validated peer %s (found in DHT, updated last_seen)", peer.PeerID[:8]), "peer-validator")
		}
	}

	pv.logger.Info(fmt.Sprintf("Stale peer validation complete (stale: %d, validated: %d, removed: %d)",
		staleCount, validatedCount, removedCount), "peer-validator")

	return nil
}

// StartPeriodicValidation starts periodic stale peer validation
// Runs validation every N hours (default: 6 hours)
func (pv *PeerValidator) StartPeriodicValidation(topic string) {
	pv.runningMutex.Lock()
	if pv.running {
		pv.logger.Warn("Periodic validation already running", "peer-validator")
		pv.runningMutex.Unlock()
		return
	}
	pv.running = true
	pv.runningMutex.Unlock()

	// Get validation interval from config (default: 6 hours)
	intervalHours := pv.config.GetConfigInt("peer_validation_interval_hours", 6, 1, 24)
	interval := time.Duration(intervalHours) * time.Hour

	pv.logger.Info(fmt.Sprintf("Starting periodic stale peer validation (interval: %v)", interval), "peer-validator")

	// Create ticker
	pv.validationTicker = time.NewTicker(interval)

	// Start background goroutine
	go func() {
		for {
			select {
			case <-pv.validationTicker.C:
				pv.logger.Debug("Periodic validation triggered", "peer-validator")

				err := pv.ValidateStalePeers(topic)
				if err != nil {
					pv.logger.Warn(fmt.Sprintf("Periodic validation failed: %v", err), "peer-validator")
				} else {
					pv.logger.Debug("Periodic validation completed successfully", "peer-validator")
				}

			case <-pv.stopChan:
				pv.logger.Info("Stopping periodic peer validation", "peer-validator")
				pv.runningMutex.Lock()
				pv.running = false
				pv.runningMutex.Unlock()
				return
			}
		}
	}()
}

// StopPeriodicValidation stops the periodic validation goroutine
func (pv *PeerValidator) StopPeriodicValidation() {
	pv.runningMutex.Lock()
	defer pv.runningMutex.Unlock()

	if !pv.running {
		return
	}

	if pv.validationTicker != nil {
		pv.validationTicker.Stop()
	}

	close(pv.stopChan)
	pv.running = false
	pv.logger.Info("Periodic peer validation stopped", "peer-validator")
}

// GetStats returns validation statistics
func (pv *PeerValidator) GetStats() map[string]interface{} {
	pv.runningMutex.Lock()
	defer pv.runningMutex.Unlock()

	return map[string]interface{}{
		"running": pv.running,
	}
}
