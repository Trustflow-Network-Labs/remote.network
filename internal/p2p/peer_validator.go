package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeerValidator validates stale known_peers by querying DHT
// Removes peers that haven't been seen in 24h+ and are no longer in DHT
type PeerValidator struct {
	metadataFetcher *MetadataFetcher
	knownPeers      *KnownPeersManager
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
	knownPeers *KnownPeersManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *PeerValidator {
	return &PeerValidator{
		metadataFetcher: metadataFetcher,
		knownPeers:      knownPeers,
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
	knownPeers, err := pv.knownPeers.GetKnownPeersByTopic(topic)
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

			if err := pv.knownPeers.DeleteKnownPeer(peer.PeerID, topic); err != nil {
				pv.logger.Warn(fmt.Sprintf("Failed to remove stale peer %s: %v", peer.PeerID[:8], err), "peer-validator")
			} else {
				removedCount++
			}
			continue
		}

		// Peer still exists in DHT - update last_seen timestamp, dht_node_id, and service counts
		validatedCount++
		peer.LastSeen = time.Now()
		peer.DHTNodeID = metadata.NodeID // Update DHT node ID from fresh metadata

		// Update peer identity (dht_node_id, last_seen)
		if err := pv.knownPeers.StoreKnownPeer(peer); err != nil {
			pv.logger.Warn(fmt.Sprintf("Failed to update validated peer identity %s: %v", peer.PeerID[:8], err), "peer-validator")
			continue
		}

		// Update service counts separately from DHT metadata
		if err := pv.knownPeers.UpdatePeerServiceCounts(peer.PeerID, peer.Topic, metadata.FilesCount, metadata.AppsCount); err != nil {
			pv.logger.Warn(fmt.Sprintf("Failed to update service counts for validated peer %s: %v", peer.PeerID[:8], err), "peer-validator")
		} else {
			pv.logger.Debug(fmt.Sprintf("Validated peer %s (found in DHT, updated last_seen, dht_node_id, and service counts: files=%d, apps=%d)",
				peer.PeerID[:8], metadata.FilesCount, metadata.AppsCount), "peer-validator")
		}

		// Update NAT/relay network info from DHT metadata
		// A peer is behind NAT if NodeType is "private" OR if it's using a relay
		isBehindNAT := metadata.NetworkInfo.NodeType == "private" || metadata.NetworkInfo.UsingRelay
		pv.logger.Debug(fmt.Sprintf("Peer %s network info: node_type=%s, using_relay=%v, behind_nat=%v",
			peer.PeerID[:8], metadata.NetworkInfo.NodeType, metadata.NetworkInfo.UsingRelay, isBehindNAT), "peer-validator")
		if err := pv.knownPeers.UpdatePeerNetworkInfo(
			peer.PeerID,
			peer.Topic,
			isBehindNAT,
			metadata.NetworkInfo.NATType,
			metadata.NetworkInfo.UsingRelay,
			metadata.NetworkInfo.ConnectedRelay,
		); err != nil {
			pv.logger.Warn(fmt.Sprintf("Failed to update network info for validated peer %s: %v", peer.PeerID[:8], err), "peer-validator")
		}
	}

	pv.logger.Info(fmt.Sprintf("Stale peer validation complete (stale: %d, validated: %d, removed: %d)",
		staleCount, validatedCount, removedCount), "peer-validator")

	return nil
}

// RefreshAllPeersNetworkInfo refreshes NAT/relay info for all known peers
// This should be called once at startup to populate network info for existing peers
func (pv *PeerValidator) RefreshAllPeersNetworkInfo(topic string) {
	pv.logger.Info("Starting initial network info refresh for all peers...", "peer-validator")

	// Get all known peers
	peers, err := pv.knownPeers.GetAllKnownPeers()
	if err != nil {
		pv.logger.Error(fmt.Sprintf("Failed to get peers for network info refresh: %v", err), "peer-validator")
		return
	}

	updatedCount := 0
	for _, peer := range peers {
		// Skip peers without public key (can't query DHT)
		if len(peer.PublicKey) == 0 {
			continue
		}

		// Fetch fresh metadata from DHT
		metadata, err := pv.metadataFetcher.GetPeerMetadata(peer.PublicKey)
		if err != nil {
			pv.logger.Debug(fmt.Sprintf("Could not fetch metadata for peer %s: %v", peer.PeerID[:8], err), "peer-validator")
			continue
		}

		// Update network info
		isBehindNAT := metadata.NetworkInfo.NodeType == "private" || metadata.NetworkInfo.UsingRelay
		if err := pv.knownPeers.UpdatePeerNetworkInfo(
			peer.PeerID,
			peer.Topic,
			isBehindNAT,
			metadata.NetworkInfo.NATType,
			metadata.NetworkInfo.UsingRelay,
			metadata.NetworkInfo.ConnectedRelay,
		); err != nil {
			pv.logger.Warn(fmt.Sprintf("Failed to update network info for peer %s: %v", peer.PeerID[:8], err), "peer-validator")
		} else {
			updatedCount++
		}
	}

	pv.logger.Info(fmt.Sprintf("Network info refresh complete: updated %d/%d peers", updatedCount, len(peers)), "peer-validator")
}

// StartPeriodicValidation starts periodic stale peer validation and network info refresh
// Runs validation every N hours (default: 6 hours)
// Runs network info refresh every M minutes (default: 30 minutes)
func (pv *PeerValidator) StartPeriodicValidation(topic string) {
	pv.runningMutex.Lock()
	if pv.running {
		pv.logger.Warn("Periodic validation already running", "peer-validator")
		pv.runningMutex.Unlock()
		return
	}
	pv.running = true
	pv.runningMutex.Unlock()

	// Run initial network info refresh in background
	go pv.RefreshAllPeersNetworkInfo(topic)

	// Get validation interval from config (default: 6 hours)
	intervalHours := pv.config.GetConfigInt("peer_validation_interval_hours", 6, 1, 24)
	validationInterval := time.Duration(intervalHours) * time.Hour

	// Get network info refresh interval from config (default: 30 minutes)
	// This should be more frequent than validation to catch relay status changes quickly
	refreshMinutes := pv.config.GetConfigInt("peer_network_refresh_interval_minutes", 30, 5, 240)
	refreshInterval := time.Duration(refreshMinutes) * time.Minute

	pv.logger.Info(fmt.Sprintf("Starting periodic stale peer validation (interval: %v) and network info refresh (interval: %v)",
		validationInterval, refreshInterval), "peer-validator")

	// Create ticker for stale peer validation
	pv.validationTicker = time.NewTicker(validationInterval)

	// Create ticker for network info refresh
	networkRefreshTicker := time.NewTicker(refreshInterval)

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

			case <-networkRefreshTicker.C:
				pv.logger.Debug("Periodic network info refresh triggered", "peer-validator")

				// Refresh network info for all peers to catch relay status changes
				pv.RefreshAllPeersNetworkInfo(topic)

			case <-pv.stopChan:
				pv.logger.Info("Stopping periodic peer validation and network refresh", "peer-validator")
				if networkRefreshTicker != nil {
					networkRefreshTicker.Stop()
				}
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
