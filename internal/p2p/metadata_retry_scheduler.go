package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataRetryScheduler manages retry logic for failed metadata fetches and cleanup of stale peers
type MetadataRetryScheduler struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	metadataFetcher *MetadataFetcher
	knownPeers      *KnownPeersManager

	pendingFetches map[string]*PendingFetch
	fetchMutex     sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

// PendingFetch represents a metadata fetch that needs retrying
type PendingFetch struct {
	PeerID       string
	PublicKey    []byte
	FirstAttempt time.Time
	LastAttempt  time.Time
	AttemptCount int
	OnSuccess    func(*database.PeerMetadata)
}

// NewMetadataRetryScheduler creates a new metadata retry scheduler
func NewMetadataRetryScheduler(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	metadataFetcher *MetadataFetcher,
	knownPeers *KnownPeersManager,
) *MetadataRetryScheduler {
	ctx, cancel := context.WithCancel(context.Background())

	mrs := &MetadataRetryScheduler{
		config:          config,
		logger:          logger,
		metadataFetcher: metadataFetcher,
		knownPeers:      knownPeers,
		pendingFetches:  make(map[string]*PendingFetch),
		ctx:             ctx,
		cancel:          cancel,
	}

	logger.Info("Metadata retry scheduler initialized", "metadata-retry")
	return mrs
}

// ScheduleFetch schedules a metadata fetch with automatic retries
// The onSuccess callback will be called when metadata is successfully fetched
func (mrs *MetadataRetryScheduler) ScheduleFetch(peerID string, publicKey []byte, onSuccess func(*database.PeerMetadata)) error {
	mrs.fetchMutex.Lock()
	defer mrs.fetchMutex.Unlock()

	// Check if already scheduled
	if existing, exists := mrs.pendingFetches[peerID]; exists {
		mrs.logger.Debug(fmt.Sprintf("Metadata fetch already scheduled for peer %s (attempts: %d)",
			peerID[:8], existing.AttemptCount), "metadata-retry")
		return nil
	}

	// Try immediate fetch first
	metadata, err := mrs.metadataFetcher.GetPeerMetadata(publicKey)
	if err == nil {
		// Success on first try
		mrs.logger.Debug(fmt.Sprintf("Metadata fetched immediately for peer %s", peerID[:8]), "metadata-retry")
		if onSuccess != nil {
			onSuccess(metadata)
		}
		return nil
	}

	// Failed - schedule for retry
	now := time.Now()
	mrs.pendingFetches[peerID] = &PendingFetch{
		PeerID:       peerID,
		PublicKey:    publicKey,
		FirstAttempt: now,
		LastAttempt:  now,
		AttemptCount: 1,
		OnSuccess:    onSuccess,
	}

	mrs.logger.Debug(fmt.Sprintf("Scheduled metadata fetch retry for peer %s: %v", peerID[:8], err), "metadata-retry")
	return nil
}

// StartRetryLoop starts the periodic retry loop in a goroutine
func (mrs *MetadataRetryScheduler) StartRetryLoop() {
	retryInterval := time.Duration(mrs.config.GetConfigInt("metadata_fetch_retry_interval", 180, 60, 3600)) * time.Second

	mrs.logger.Info(fmt.Sprintf("Starting metadata retry loop (interval: %v)", retryInterval), "metadata-retry")

	go func() {
		ticker := time.NewTicker(retryInterval)
		defer ticker.Stop()

		for {
			select {
			case <-mrs.ctx.Done():
				mrs.logger.Info("Metadata retry loop stopped", "metadata-retry")
				return
			case <-ticker.C:
				mrs.retryPendingFetches()
			}
		}
	}()
}

// retryPendingFetches attempts to retry all pending metadata fetches
func (mrs *MetadataRetryScheduler) retryPendingFetches() {
	mrs.fetchMutex.Lock()
	defer mrs.fetchMutex.Unlock()

	maxRetries := mrs.config.GetConfigInt("metadata_fetch_retry_count", 5, 1, 20)
	maxAge := time.Duration(mrs.config.GetConfigInt("metadata_fetch_max_age", 3600, 600, 86400)) * time.Second

	now := time.Now()
	toDelete := make([]string, 0)

	mrs.logger.Debug(fmt.Sprintf("Retrying %d pending metadata fetches", len(mrs.pendingFetches)), "metadata-retry")

	for peerID, fetch := range mrs.pendingFetches {
		// Check if peer is too old - delete from database
		age := now.Sub(fetch.FirstAttempt)
		if age > maxAge {
			mrs.logger.Info(fmt.Sprintf("Peer %s exceeded max age (%v) without metadata - deleting",
				peerID[:8], maxAge), "metadata-retry")

			// Delete from database - get topic from config
			topics := mrs.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
			if len(topics) > 0 {
				if err := mrs.knownPeers.DeleteKnownPeer(peerID, topics[0]); err != nil {
					mrs.logger.Error(fmt.Sprintf("Failed to delete stale peer %s: %v", peerID[:8], err), "metadata-retry")
				}
			}

			toDelete = append(toDelete, peerID)
			continue
		}

		// Check if exceeded max retries - delete from database
		if fetch.AttemptCount >= maxRetries {
			mrs.logger.Info(fmt.Sprintf("Peer %s exceeded max retries (%d) without metadata - deleting",
				peerID[:8], maxRetries), "metadata-retry")

			// Delete from database - get topic from config
			topics := mrs.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
			if len(topics) > 0 {
				if err := mrs.knownPeers.DeleteKnownPeer(peerID, topics[0]); err != nil {
					mrs.logger.Error(fmt.Sprintf("Failed to delete stale peer %s: %v", peerID[:8], err), "metadata-retry")
				}
			}

			toDelete = append(toDelete, peerID)
			continue
		}

		// Attempt fetch
		metadata, err := mrs.metadataFetcher.GetPeerMetadata(fetch.PublicKey)
		if err != nil {
			// Failed - increment attempt count
			fetch.AttemptCount++
			fetch.LastAttempt = now
			mrs.logger.Debug(fmt.Sprintf("Metadata fetch retry %d/%d failed for peer %s: %v",
				fetch.AttemptCount, maxRetries, peerID[:8], err), "metadata-retry")
			continue
		}

		// Success - call callback and remove from pending
		mrs.logger.Info(fmt.Sprintf("Metadata fetched successfully for peer %s after %d attempts",
			peerID[:8], fetch.AttemptCount), "metadata-retry")

		if fetch.OnSuccess != nil {
			// Call callback outside the lock to avoid deadlock
			go fetch.OnSuccess(metadata)
		}

		toDelete = append(toDelete, peerID)
	}

	// Remove completed/expired fetches
	for _, peerID := range toDelete {
		delete(mrs.pendingFetches, peerID)
	}

	if len(toDelete) > 0 {
		mrs.logger.Debug(fmt.Sprintf("Removed %d completed/expired fetches from pending list", len(toDelete)), "metadata-retry")
	}
}

// GetPendingCount returns the number of pending fetches
func (mrs *MetadataRetryScheduler) GetPendingCount() int {
	mrs.fetchMutex.RLock()
	defer mrs.fetchMutex.RUnlock()
	return len(mrs.pendingFetches)
}

// CancelFetch cancels a pending fetch for a peer
func (mrs *MetadataRetryScheduler) CancelFetch(peerID string) {
	mrs.fetchMutex.Lock()
	defer mrs.fetchMutex.Unlock()

	if _, exists := mrs.pendingFetches[peerID]; exists {
		delete(mrs.pendingFetches, peerID)
		mrs.logger.Debug(fmt.Sprintf("Cancelled metadata fetch for peer %s", peerID[:8]), "metadata-retry")
	}
}

// Stop stops the retry scheduler
func (mrs *MetadataRetryScheduler) Stop() {
	mrs.logger.Info("Stopping metadata retry scheduler", "metadata-retry")
	mrs.cancel()

	// Clear pending fetches
	mrs.fetchMutex.Lock()
	mrs.pendingFetches = make(map[string]*PendingFetch)
	mrs.fetchMutex.Unlock()
}
