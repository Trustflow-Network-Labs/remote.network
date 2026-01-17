package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// LocalStoreForward handles local store-and-forward for messages to public peers
type LocalStoreForward struct {
	config        *utils.ConfigManager
	logger        *utils.LogsManager
	dbManager     *database.SQLiteManager
	quicPeer      *QUICPeer
	knownPeers    *KnownPeersManager
	metadataQuery *MetadataQueryService
	ourPeerID     string

	// Configuration
	enabled                bool
	maxMessages            int64
	maxBytes               int64
	maxMessageSize         int64
	cleanupInterval        time.Duration
	retryInterval          time.Duration
	maxDeliveryAttempts    int
	deliveredRetention     time.Duration

	// Peer type cache (peerID -> isPublic, timestamp)
	peerTypeCache   map[string]*peerTypeInfo
	peerTypeCacheMu sync.RWMutex

	// Context for background tasks
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// peerTypeInfo caches peer type detection results
type peerTypeInfo struct {
	IsPublic bool
	CachedAt time.Time
}

// NewLocalStoreForward creates a new local store-and-forward service
func NewLocalStoreForward(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	dbManager *database.SQLiteManager,
	quicPeer *QUICPeer,
	knownPeers *KnownPeersManager,
	metadataQuery *MetadataQueryService,
	ourPeerID string,
) *LocalStoreForward {
	ctx, cancel := context.WithCancel(context.Background())

	lsf := &LocalStoreForward{
		config:              config,
		logger:              logger,
		dbManager:           dbManager,
		quicPeer:            quicPeer,
		knownPeers:          knownPeers,
		metadataQuery:       metadataQuery,
		ourPeerID:           ourPeerID,
		enabled:             config.GetConfigBool("local_store_enabled", true),
		maxMessages:         int64(config.GetConfigInt("local_store_max_messages", 1000, 1, 10000)),
		maxBytes:            int64(config.GetConfigInt("local_store_max_bytes", 104857600, 1024, 1073741824)), // 100 MB default
		maxMessageSize:      int64(config.GetConfigInt("local_store_max_message_size", 10485760, 1024, 104857600)), // 10 MB default
		cleanupInterval:     time.Duration(config.GetConfigInt("local_store_cleanup_interval_minutes", 60, 1, 1440)) * time.Minute,
		retryInterval:       time.Duration(config.GetConfigInt("local_store_retry_interval_minutes", 5, 1, 60)) * time.Minute,
		maxDeliveryAttempts: config.GetConfigInt("local_store_max_delivery_attempts", 10, 1, 100),
		deliveredRetention:  time.Duration(config.GetConfigInt("local_store_delivered_retention_hours", 24, 1, 720)) * time.Hour,
		peerTypeCache:       make(map[string]*peerTypeInfo),
		ctx:                 ctx,
		cancel:              cancel,
	}

	return lsf
}

// Start starts the background tasks for local store-and-forward
func (lsf *LocalStoreForward) Start() {
	if !lsf.enabled {
		lsf.logger.Info("Local store-and-forward is disabled", "local-store-forward")
		return
	}

	lsf.logger.Info("Starting local store-and-forward background tasks", "local-store-forward")

	// Start cleanup goroutine
	lsf.wg.Add(1)
	go lsf.runMessageCleanup()

	// Start periodic delivery goroutine
	lsf.wg.Add(1)
	go lsf.runPeriodicDeliveryAttempts()
}

// Stop stops the background tasks
func (lsf *LocalStoreForward) Stop() {
	lsf.logger.Info("Stopping local store-and-forward", "local-store-forward")
	lsf.cancel()
	lsf.wg.Wait()
}

// ========================================
// Message Storage
// ========================================

// TryStoreMessage attempts to store a message for later delivery to a public peer
// This is called when direct message delivery fails (peer not connected)
func (lsf *LocalStoreForward) TryStoreMessage(targetPeerID string, messageBytes []byte) error {
	if !lsf.enabled {
		return fmt.Errorf("local store-and-forward is disabled")
	}

	// Parse message to check type and extract metadata
	msg, err := UnmarshalQUICMessage(messageBytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	messageType := string(msg.Type)

	// Check if message type is eligible for store-and-forward
	if !lsf.IsEligibleForStore(messageType) {
		lsf.logger.Debug(fmt.Sprintf("Message type %s is not eligible for local store-and-forward", messageType), "local-store-forward")
		return fmt.Errorf("message type %s not eligible for store-and-forward", messageType)
	}

	// Check if target peer is a public peer (not NAT)
	isPublic, err := lsf.IsPublicPeer(targetPeerID)
	if err != nil {
		lsf.logger.Debug(fmt.Sprintf("Failed to determine peer type for %s: %v", targetPeerID[:8], err), "local-store-forward")
		return fmt.Errorf("failed to determine peer type: %v", err)
	}

	if !isPublic {
		lsf.logger.Debug(fmt.Sprintf("Peer %s is not public (using relay), skipping local store", targetPeerID[:8]), "local-store-forward")
		return fmt.Errorf("peer is not public (uses relay)")
	}

	// Check storage limits before storing
	canStore, reason := lsf.CanStoreMessage(targetPeerID, int64(len(messageBytes)))
	if !canStore {
		lsf.logger.Warn(fmt.Sprintf("Cannot store message for peer %s: %s", targetPeerID[:8], reason), "local-store-forward")
		return fmt.Errorf("storage limit exceeded: %s", reason)
	}

	// Store the message
	return lsf.StoreMessage(targetPeerID, msg, messageBytes)
}

// IsPublicPeer checks if a peer is a public peer (not behind NAT)
func (lsf *LocalStoreForward) IsPublicPeer(peerID string) (bool, error) {
	// Check cache first (5 minute TTL)
	lsf.peerTypeCacheMu.RLock()
	cached, exists := lsf.peerTypeCache[peerID]
	lsf.peerTypeCacheMu.RUnlock()

	if exists && time.Since(cached.CachedAt) < 5*time.Minute {
		lsf.logger.Debug(fmt.Sprintf("Using cached peer type for %s: public=%v (age: %v)",
			peerID[:8], cached.IsPublic, time.Since(cached.CachedAt)), "local-store-forward")
		return cached.IsPublic, nil
	}

	// Cache miss or expired - query metadata
	lsf.logger.Debug(fmt.Sprintf("Querying peer type for %s (cache miss or expired)", peerID[:8]), "local-store-forward")

	// Get peer from database
	peer, err := lsf.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return false, fmt.Errorf("peer %s not found or has no public key", peerID[:8])
	}

	// Query DHT for metadata
	metadata, err := lsf.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		return false, fmt.Errorf("failed to query peer metadata: %v", err)
	}

	// Determine if peer is public based on network info
	// Public peer: UsingRelay == false AND NodeType == "public"
	isPublic := !metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.NodeType == "public"

	// Update cache
	lsf.peerTypeCacheMu.Lock()
	lsf.peerTypeCache[peerID] = &peerTypeInfo{
		IsPublic: isPublic,
		CachedAt: time.Now(),
	}
	lsf.peerTypeCacheMu.Unlock()

	lsf.logger.Debug(fmt.Sprintf("Peer %s type: public=%v (UsingRelay=%v, NodeType=%s)",
		peerID[:8], isPublic, metadata.NetworkInfo.UsingRelay, metadata.NetworkInfo.NodeType), "local-store-forward")

	return isPublic, nil
}

// IsEligibleForStore checks if a message type is eligible for store-and-forward
func (lsf *LocalStoreForward) IsEligibleForStore(messageType string) bool {
	eligibleTypes := map[string]bool{
		"invoice_request":         true,
		"invoice_response":        true,
		"invoice_notify":          true,
		"job_status_update":       true,
		"job_response":            true,
		"service_response":        true,
		"capabilities_response":   true,
	}

	return eligibleTypes[messageType]
}

// CanStoreMessage checks if we can store a message for a peer (storage limits)
func (lsf *LocalStoreForward) CanStoreMessage(peerID string, messageSize int64) (bool, string) {
	// Check per-message size limit
	if messageSize > lsf.maxMessageSize {
		return false, fmt.Sprintf("message size (%d bytes) exceeds max (%d bytes)", messageSize, lsf.maxMessageSize)
	}

	// Get current storage usage
	usage, err := lsf.dbManager.LocalStore.GetStorageUsage(peerID)
	if err != nil {
		lsf.logger.Warn(fmt.Sprintf("Failed to get storage usage for peer %s: %v", peerID[:8], err), "local-store-forward")
		return true, "" // Allow storage on error (fail open)
	}

	// Check per-peer message count limit
	if usage.MessageCount >= lsf.maxMessages {
		return false, fmt.Sprintf("message count limit reached (%d/%d)", usage.MessageCount, lsf.maxMessages)
	}

	// Check per-peer total bytes limit
	if usage.TotalBytes+messageSize > lsf.maxBytes {
		return false, fmt.Sprintf("total bytes limit would be exceeded (%d + %d > %d)",
			usage.TotalBytes, messageSize, lsf.maxBytes)
	}

	return true, ""
}

// StoreMessage stores a message in the local database for later delivery
func (lsf *LocalStoreForward) StoreMessage(targetPeerID string, msg *QUICMessage, payload []byte) error {
	messageID := uuid.New().String()
	messageType := string(msg.Type)
	ttl := lsf.GetMessageTTL(messageType)

	pendingMsg := &database.LocalPendingMessage{
		MessageID:    messageID,
		CorrelationID: msg.RequestID,
		SourcePeerID: lsf.ourPeerID,
		TargetPeerID: targetPeerID,
		MessageType:  messageType,
		Payload:      payload,
		PayloadSize:  int64(len(payload)),
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(ttl),
		Status:       "pending",
		Priority:     0,
	}

	// Store message
	if err := lsf.dbManager.LocalStore.StorePendingMessage(pendingMsg); err != nil {
		return fmt.Errorf("failed to store message: %v", err)
	}

	// Increment storage usage
	if err := lsf.dbManager.LocalStore.IncrementStorageUsage(targetPeerID, 1, int64(len(payload))); err != nil {
		lsf.logger.Warn(fmt.Sprintf("Failed to increment storage usage for peer %s: %v", targetPeerID[:8], err), "local-store-forward")
	}

	lsf.logger.Info(fmt.Sprintf("Stored message %s (type: %s, size: %d bytes) for peer %s (expires: %s)",
		messageID, messageType, len(payload), targetPeerID[:8], pendingMsg.ExpiresAt.Format(time.RFC3339)), "local-store-forward")

	return nil
}

// GetMessageTTL returns the TTL for a message type
func (lsf *LocalStoreForward) GetMessageTTL(messageType string) time.Duration {
	// Check for local_store config override first
	configKey := fmt.Sprintf("local_store_ttl_%s", messageType)
	hours := lsf.config.GetConfigInt(configKey, 0, 0, 720) // Max 30 days, default 0
	if hours > 0 {
		return time.Duration(hours) * time.Hour
	}

	// Fall back to relay TTL config (shared TTL configuration)
	configKey = fmt.Sprintf("relay_ttl_%s", messageType)
	hours = lsf.config.GetConfigInt(configKey, 0, 0, 720) // Max 30 days, default 0
	if hours > 0 {
		return time.Duration(hours) * time.Hour
	}

	// Hardcoded defaults (same as relay store-and-forward)
	defaultTTLs := map[string]time.Duration{
		"invoice_request":         7 * 24 * time.Hour,  // 7 days
		"invoice_response":        7 * 24 * time.Hour,  // 7 days
		"invoice_notify":          7 * 24 * time.Hour,  // 7 days
		"job_status_update":       24 * time.Hour,      // 24 hours
		"job_response":            48 * time.Hour,      // 48 hours
		"service_response":        48 * time.Hour,      // 48 hours
		"capabilities_response":   48 * time.Hour,      // 48 hours
	}

	if ttl, exists := defaultTTLs[messageType]; exists {
		return ttl
	}

	// Default fallback
	return 24 * time.Hour
}

// ========================================
// Message Delivery
// ========================================

// OnPeerConnected is called when a peer connects
// This triggers immediate delivery of any pending messages
func (lsf *LocalStoreForward) OnPeerConnected(peerID string, remoteAddr string) {
	if !lsf.enabled {
		return
	}

	lsf.logger.Debug(fmt.Sprintf("Peer %s connected, checking for pending messages", peerID[:8]), "local-store-forward")

	// Deliver pending messages in background
	go lsf.DeliverPendingMessages(peerID)
}

// DeliverPendingMessages delivers all pending messages for a peer
func (lsf *LocalStoreForward) DeliverPendingMessages(targetPeerID string) {
	// Check if peer is connected
	if !lsf.isPeerConnected(targetPeerID) {
		lsf.logger.Debug(fmt.Sprintf("Peer %s not connected, skipping delivery", targetPeerID[:8]), "local-store-forward")
		return
	}

	// Get pending messages
	messages, err := lsf.dbManager.LocalStore.GetPendingMessagesForPeer(targetPeerID)
	if err != nil {
		lsf.logger.Warn(fmt.Sprintf("Failed to get pending messages for peer %s: %v", targetPeerID[:8], err), "local-store-forward")
		return
	}

	if len(messages) == 0 {
		lsf.logger.Debug(fmt.Sprintf("No pending messages for peer %s", targetPeerID[:8]), "local-store-forward")
		return
	}

	lsf.logger.Info(fmt.Sprintf("Delivering %d pending messages to peer %s", len(messages), targetPeerID[:8]), "local-store-forward")

	deliveredCount := 0
	failedCount := 0

	for _, msg := range messages {
		// Check if we've exceeded max delivery attempts
		if msg.DeliveryAttempts >= lsf.maxDeliveryAttempts {
			lsf.logger.Warn(fmt.Sprintf("Message %s exceeded max delivery attempts (%d), marking as failed",
				msg.MessageID, lsf.maxDeliveryAttempts), "local-store-forward")
			lsf.dbManager.LocalStore.MarkMessageFailed(msg.MessageID, "max delivery attempts exceeded")
			lsf.dbManager.LocalStore.DecrementStorageUsage(targetPeerID, 1, msg.PayloadSize)
			failedCount++
			continue
		}

		// Increment delivery attempt counter
		if err := lsf.dbManager.LocalStore.IncrementDeliveryAttempt(msg.MessageID); err != nil {
			lsf.logger.Warn(fmt.Sprintf("Failed to increment delivery attempt for message %s: %v", msg.MessageID, err), "local-store-forward")
		}

		// Check if peer is still connected (might have disconnected during delivery)
		if !lsf.isPeerConnected(targetPeerID) {
			lsf.logger.Debug(fmt.Sprintf("Peer %s disconnected during delivery, stopping", targetPeerID[:8]), "local-store-forward")
			break
		}

		// Try to deliver the message
		err := lsf.quicPeer.SendMessageToPeer(targetPeerID, msg.Payload)
		if err != nil {
			lsf.logger.Debug(fmt.Sprintf("Failed to deliver message %s to peer %s: %v",
				msg.MessageID, targetPeerID[:8], err), "local-store-forward")
			failedCount++

			// Check peer type after 5 failed attempts (peer might have changed to NAT)
			if msg.DeliveryAttempts+1 >= 5 {
				if isPublic, typeErr := lsf.IsPublicPeer(targetPeerID); typeErr == nil && !isPublic {
					lsf.logger.Warn(fmt.Sprintf("Peer %s is no longer public (now using relay), marking message %s as failed",
						targetPeerID[:8], msg.MessageID), "local-store-forward")
					lsf.dbManager.LocalStore.MarkMessageFailed(msg.MessageID, "peer type changed to NAT")
					lsf.dbManager.LocalStore.DecrementStorageUsage(targetPeerID, 1, msg.PayloadSize)
					failedCount++
					continue
				}
			}

			continue
		}

		// Mark as delivered
		if err := lsf.dbManager.LocalStore.MarkMessageDelivered(msg.MessageID); err != nil {
			lsf.logger.Warn(fmt.Sprintf("Failed to mark message %s as delivered: %v", msg.MessageID, err), "local-store-forward")
		}

		// Decrement storage usage
		if err := lsf.dbManager.LocalStore.DecrementStorageUsage(targetPeerID, 1, msg.PayloadSize); err != nil {
			lsf.logger.Warn(fmt.Sprintf("Failed to decrement storage usage for peer %s: %v", targetPeerID[:8], err), "local-store-forward")
		}

		lsf.logger.Info(fmt.Sprintf("Delivered message %s (type: %s) to peer %s",
			msg.MessageID, msg.MessageType, targetPeerID[:8]), "local-store-forward")

		deliveredCount++
	}

	lsf.logger.Info(fmt.Sprintf("Delivery complete for peer %s: %d delivered, %d failed",
		targetPeerID[:8], deliveredCount, failedCount), "local-store-forward")
}

// isPeerConnected checks if a peer is currently connected
func (lsf *LocalStoreForward) isPeerConnected(peerID string) bool {
	_, exists := lsf.quicPeer.GetAddressByPeerID(peerID)
	return exists
}

// ========================================
// Background Tasks
// ========================================

// runMessageCleanup runs periodic cleanup of expired messages
func (lsf *LocalStoreForward) runMessageCleanup() {
	defer lsf.wg.Done()

	ticker := time.NewTicker(lsf.cleanupInterval)
	defer ticker.Stop()

	lsf.logger.Info(fmt.Sprintf("Local store cleanup task started (interval: %v)", lsf.cleanupInterval), "local-store-forward")

	for {
		select {
		case <-ticker.C:
			lsf.logger.Debug("Running local store message cleanup (expired, old delivered)", "local-store-forward")

			// Delete expired messages
			deleted, err := lsf.dbManager.LocalStore.DeleteExpiredMessages(time.Now())
			if err != nil {
				lsf.logger.Warn(fmt.Sprintf("Failed to delete expired messages: %v", err), "local-store-forward")
			} else if deleted > 0 {
				lsf.logger.Info(fmt.Sprintf("Cleaned up %d expired messages", deleted), "local-store-forward")
			}

			// Delete old delivered messages (configurable retention period)
			retentionCutoff := time.Now().Add(-lsf.deliveredRetention)
			deletedDelivered, err := lsf.dbManager.LocalStore.DeleteDeliveredMessages(retentionCutoff)
			if err != nil {
				lsf.logger.Warn(fmt.Sprintf("Failed to delete delivered messages: %v", err), "local-store-forward")
			} else if deletedDelivered > 0 {
				lsf.logger.Info(fmt.Sprintf("🧹 Cleaned up %d delivered messages (retention: %v)", deletedDelivered, lsf.deliveredRetention), "local-store-forward")
			}

		case <-lsf.ctx.Done():
			lsf.logger.Info("Local store cleanup task stopped", "local-store-forward")
			return
		}
	}
}

// runPeriodicDeliveryAttempts runs periodic delivery attempts for stalled messages
func (lsf *LocalStoreForward) runPeriodicDeliveryAttempts() {
	defer lsf.wg.Done()

	ticker := time.NewTicker(lsf.retryInterval)
	defer ticker.Stop()

	lsf.logger.Info(fmt.Sprintf("Local store periodic delivery task started (interval: %v)", lsf.retryInterval), "local-store-forward")

	for {
		select {
		case <-ticker.C:
			lsf.logger.Debug("Running periodic delivery attempts for stalled messages", "local-store-forward")

			// Get peers with stalled messages (> 10 minutes old)
			peers, err := lsf.dbManager.LocalStore.GetPeersWithStalledMessages(10 * time.Minute)
			if err != nil {
				lsf.logger.Warn(fmt.Sprintf("Failed to get peers with stalled messages: %v", err), "local-store-forward")
				continue
			}

			if len(peers) > 0 {
				lsf.logger.Info(fmt.Sprintf("Found %d peers with stalled messages, attempting delivery", len(peers)), "local-store-forward")
			}

			for _, peerID := range peers {
				if lsf.isPeerConnected(peerID) {
					lsf.logger.Debug(fmt.Sprintf("Attempting delivery for stalled messages to peer %s", peerID[:8]), "local-store-forward")
					go lsf.DeliverPendingMessages(peerID)
				}
			}

		case <-lsf.ctx.Done():
			lsf.logger.Info("Local store periodic delivery task stopped", "local-store-forward")
			return
		}
	}
}

// InvalidatePeerTypeCache invalidates the peer type cache for a specific peer
func (lsf *LocalStoreForward) InvalidatePeerTypeCache(peerID string) {
	lsf.peerTypeCacheMu.Lock()
	defer lsf.peerTypeCacheMu.Unlock()

	if _, exists := lsf.peerTypeCache[peerID]; exists {
		delete(lsf.peerTypeCache, peerID)
		lsf.logger.Debug(fmt.Sprintf("Invalidated peer type cache for peer %s", peerID[:8]), "local-store-forward")
	}
}
