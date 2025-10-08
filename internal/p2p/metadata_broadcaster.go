package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MetadataBroadcaster handles broadcasting metadata changes to all known peers
type MetadataBroadcaster struct {
	config      *utils.ConfigManager
	logger      *utils.LogsManager
	dbManager   *database.SQLiteManager
	quicPeer    *QUICPeer
	dhtPeer     *DHTPeer
	holePuncher *HolePuncher

	// Broadcast tracking
	sequenceNum   int64
	sequenceMutex sync.Mutex

	// Deduplication (prevent broadcast loops)
	recentBroadcasts map[string]int64 // key: "nodeID:sequence"
	dedupeWindow     time.Duration
	dedupeMutex      sync.RWMutex

	// Delivery tracking
	pendingDeliveries map[string]*DeliveryTask
	deliveryMutex     sync.RWMutex

	// Context
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics
	metricsMutex    sync.RWMutex
	totalBroadcasts int64
	successDeliveries int64
	failedDeliveries  int64
	deliveriesByStrategy map[string]int64 // "direct", "public", "lan", "relay", "holepunch"
}

// DeliveryTask tracks an attempt to deliver a metadata update to a peer
type DeliveryTask struct {
	TargetPeerID string
	Message      *QUICMessage
	Attempts     int
	LastAttempt  time.Time
	Status       string // "pending", "delivered", "failed"
	Strategy     string // Which delivery strategy to use
}

// NewMetadataBroadcaster creates a new metadata broadcaster
func NewMetadataBroadcaster(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	dbManager *database.SQLiteManager,
	quicPeer *QUICPeer,
	dhtPeer *DHTPeer,
	holePuncher *HolePuncher,
) *MetadataBroadcaster {
	ctx, cancel := context.WithCancel(context.Background())

	dedupeWindow := config.GetConfigDuration("metadata_broadcast_dedupe_window", 5*time.Minute)

	return &MetadataBroadcaster{
		config:               config,
		logger:               logger,
		dbManager:            dbManager,
		quicPeer:             quicPeer,
		dhtPeer:              dhtPeer,
		holePuncher:          holePuncher,
		sequenceNum:          0,
		recentBroadcasts:     make(map[string]int64),
		dedupeWindow:         dedupeWindow,
		pendingDeliveries:    make(map[string]*DeliveryTask),
		ctx:                  ctx,
		cancel:               cancel,
		deliveriesByStrategy: make(map[string]int64),
	}
}

// Start starts the metadata broadcaster
func (mb *MetadataBroadcaster) Start() error {
	mb.logger.Info("Starting metadata broadcaster...", "metadata-broadcaster")

	// Start retry worker
	go mb.retryWorker()

	// Start cleanup worker for deduplication map
	go mb.cleanupWorker()

	return nil
}

// Stop stops the metadata broadcaster
func (mb *MetadataBroadcaster) Stop() error {
	mb.logger.Info("Stopping metadata broadcaster...", "metadata-broadcaster")
	mb.cancel()
	return nil
}

// BroadcastMetadataChange broadcasts a metadata change to all known peers
func (mb *MetadataBroadcaster) BroadcastMetadataChange(
	changeType string,
	metadata *database.PeerMetadata,
	relayUpdate *RelayUpdateInfo,
	networkUpdate *NetworkUpdateInfo,
) error {
	if !mb.config.GetConfigBool("metadata_broadcast_enabled", true) {
		mb.logger.Debug("Metadata broadcast disabled", "metadata-broadcaster")
		return nil
	}

	mb.logger.Info(fmt.Sprintf("Broadcasting metadata change: type=%s", changeType), "metadata-broadcaster")

	// Generate sequence number
	sequence := mb.nextSequence()

	// Get TTL from config
	ttl := mb.config.GetConfigInt("metadata_broadcast_ttl", 3, 1, 10)

	// Create broadcast message
	msg := CreatePeerMetadataUpdate(
		mb.dhtPeer.NodeID(),
		metadata.Topic,
		metadata.Version,
		changeType,
		sequence,
		ttl,
		metadata,
		relayUpdate,
		networkUpdate,
	)

	// Record our own broadcast to prevent loops
	mb.recordBroadcast(mb.dhtPeer.NodeID(), sequence)

	// Get all peers from database
	peers, err := mb.getAllKnownPeers(metadata.Topic)
	if err != nil {
		return fmt.Errorf("failed to get known peers: %v", err)
	}

	mb.logger.Info(fmt.Sprintf("Broadcasting to %d peers", len(peers)), "metadata-broadcaster")

	// Record broadcast
	mb.recordBroadcastMetric()

	// Send to all peers
	for _, peer := range peers {
		// Skip self
		if peer.NodeID == mb.dhtPeer.NodeID() {
			continue
		}

		// Schedule delivery
		mb.scheduleDelivery(peer, msg)
	}

	return nil
}

// scheduleDelivery creates a delivery task and attempts initial delivery
func (mb *MetadataBroadcaster) scheduleDelivery(peer *database.PeerMetadata, msg *QUICMessage) {
	task := &DeliveryTask{
		TargetPeerID: peer.NodeID,
		Message:      msg,
		Attempts:     0,
		LastAttempt:  time.Time{},
		Status:       "pending",
	}

	// Store task
	mb.deliveryMutex.Lock()
	mb.pendingDeliveries[peer.NodeID] = task
	mb.deliveryMutex.Unlock()

	// Attempt delivery in background
	go mb.attemptDelivery(peer, task)
}

// attemptDelivery tries to deliver a message to a peer using the best strategy
func (mb *MetadataBroadcaster) attemptDelivery(peer *database.PeerMetadata, task *DeliveryTask) {
	task.Attempts++
	task.LastAttempt = time.Now()

	timeout := mb.config.GetConfigDuration("metadata_broadcast_timeout", 10*time.Second)
	ctx, cancel := context.WithTimeout(mb.ctx, timeout)
	defer cancel()

	var err error
	var strategy string

	// Try strategies in order of preference
	// 1. Try existing direct connection
	if err = mb.sendToDirectConnection(ctx, peer.NodeID, task.Message); err == nil {
		strategy = "direct"
		mb.logger.Debug(fmt.Sprintf("Delivered to %s via direct connection", peer.NodeID), "metadata-broadcaster")
		mb.markDelivered(task, strategy)
		return
	}

	// 2. Try LAN peer
	if mb.holePuncher != nil && mb.isLANPeer(peer) {
		if err = mb.sendToLANPeer(ctx, peer, task.Message); err == nil {
			strategy = "lan"
			mb.logger.Debug(fmt.Sprintf("Delivered to %s via LAN", peer.NodeID), "metadata-broadcaster")
			mb.markDelivered(task, strategy)
			return
		}
	}

	// 3. Try public peer (direct dial)
	if !peer.NetworkInfo.UsingRelay && peer.NetworkInfo.PublicIP != "" {
		if err = mb.sendToPublicPeer(ctx, peer, task.Message); err == nil {
			strategy = "public"
			mb.logger.Debug(fmt.Sprintf("Delivered to %s via public dial", peer.NodeID), "metadata-broadcaster")
			mb.markDelivered(task, strategy)
			return
		}
	}

	// 4. Try via relay (for NAT peers using relay)
	if peer.NetworkInfo.UsingRelay && peer.NetworkInfo.ConnectedRelay != "" {
		if err = mb.sendViaRelay(ctx, peer, task.Message); err == nil {
			strategy = "relay"
			mb.logger.Debug(fmt.Sprintf("Delivered to %s via relay", peer.NodeID), "metadata-broadcaster")
			mb.markDelivered(task, strategy)
			return
		}
	}

	// 5. Try hole punch (if enabled and we have hole puncher)
	if mb.config.GetConfigBool("metadata_broadcast_hole_punch_enabled", true) && mb.holePuncher != nil {
		if err = mb.sendViaHolePunch(ctx, peer, task.Message); err == nil {
			strategy = "holepunch"
			mb.logger.Debug(fmt.Sprintf("Delivered to %s via hole punch", peer.NodeID), "metadata-broadcaster")
			mb.markDelivered(task, strategy)
			return
		}
	}

	// All strategies failed
	maxRetries := mb.config.GetConfigInt("metadata_broadcast_max_retries", 3, 0, 10)
	if task.Attempts >= maxRetries {
		mb.logger.Warn(fmt.Sprintf("Failed to deliver to %s after %d attempts, giving up", peer.NodeID, task.Attempts), "metadata-broadcaster")
		mb.markFailed(task)
	} else {
		mb.logger.Debug(fmt.Sprintf("Failed to deliver to %s (attempt %d/%d), will retry", peer.NodeID, task.Attempts, maxRetries), "metadata-broadcaster")
		// Leave in pending state for retry worker
	}
}

// sendToDirectConnection attempts to send via existing QUIC connection
func (mb *MetadataBroadcaster) sendToDirectConnection(ctx context.Context, peerID string, msg *QUICMessage) error {
	// Check if we have an existing connection
	connections := mb.quicPeer.GetConnections()

	// Try to find connection by matching peer metadata
	metadata, err := mb.dbManager.PeerMetadata.GetPeerMetadata(peerID, "")
	if err != nil {
		return fmt.Errorf("no metadata for peer: %v", err)
	}

	quicPort := mb.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	possibleAddrs := []string{
		fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, quicPort),
		fmt.Sprintf("%s:%d", metadata.NetworkInfo.PrivateIP, quicPort),
	}

	for _, conn := range connections {
		for _, addr := range possibleAddrs {
			if conn == addr {
				// Found existing connection, send message
				return mb.sendMessage(ctx, addr, msg)
			}
		}
	}

	return fmt.Errorf("no existing connection")
}

// sendToLANPeer attempts to send via LAN direct dial
func (mb *MetadataBroadcaster) sendToLANPeer(ctx context.Context, peer *database.PeerMetadata, msg *QUICMessage) error {
	quicPort := mb.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	addr := fmt.Sprintf("%s:%d", peer.NetworkInfo.PrivateIP, quicPort)
	return mb.sendMessage(ctx, addr, msg)
}

// sendToPublicPeer attempts to send via direct dial to public address
func (mb *MetadataBroadcaster) sendToPublicPeer(ctx context.Context, peer *database.PeerMetadata, msg *QUICMessage) error {
	quicPort := mb.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	addr := fmt.Sprintf("%s:%d", peer.NetworkInfo.PublicIP, quicPort)
	return mb.sendMessage(ctx, addr, msg)
}

// sendViaRelay attempts to send via relay forwarding
func (mb *MetadataBroadcaster) sendViaRelay(ctx context.Context, peer *database.PeerMetadata, msg *QUICMessage) error {
	// Get relay metadata
	relayMetadata, err := mb.dbManager.PeerMetadata.GetPeerMetadata(peer.NetworkInfo.ConnectedRelay, "")
	if err != nil {
		return fmt.Errorf("relay not found: %v", err)
	}

	// Connect to relay
	quicPort := mb.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	relayAddr := fmt.Sprintf("%s:%d", relayMetadata.NetworkInfo.PublicIP, quicPort)

	conn, err := mb.quicPeer.ConnectToPeer(relayAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Marshal the update message first
	updateBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal update: %v", err)
	}

	// Wrap message in relay forward
	forwardMsg := CreateRelayForward(
		peer.NetworkInfo.RelaySessionID, // session ID from peer's metadata
		mb.dhtPeer.NodeID(),              // source (us)
		peer.NodeID,                      // destination
		"peer_metadata_update",           // message type
		updateBytes,                      // payload
	)

	msgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal forward: %v", err)
	}

	_, err = stream.Write(msgBytes)
	return err
}

// sendViaHolePunch attempts to send via hole punching
func (mb *MetadataBroadcaster) sendViaHolePunch(ctx context.Context, peer *database.PeerMetadata, msg *QUICMessage) error {
	if mb.holePuncher == nil {
		return fmt.Errorf("hole puncher not available")
	}

	// Attempt hole punch
	if err := mb.holePuncher.DirectConnect(peer.NodeID); err != nil {
		return fmt.Errorf("hole punch failed: %v", err)
	}

	// Now try direct connection
	return mb.sendToDirectConnection(ctx, peer.NodeID, msg)
}

// sendMessage sends a message to an address
func (mb *MetadataBroadcaster) sendMessage(ctx context.Context, addr string, msg *QUICMessage) error {
	conn, err := mb.quicPeer.ConnectToPeer(addr)
	if err != nil {
		return err
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	msgBytes, err := msg.Marshal()
	if err != nil {
		return err
	}

	_, err = stream.Write(msgBytes)
	return err
}

// isLANPeer checks if peer is on same LAN (uses same logic as hole puncher)
func (mb *MetadataBroadcaster) isLANPeer(peer *database.PeerMetadata) bool {
	// If hole puncher is available, use its LAN detection
	if mb.holePuncher != nil {
		// The hole puncher has a proper isLANPeer implementation
		// We delegate to it for consistency
		// Note: This is a private method, so we replicate the logic here
	}

	// Get our own network info
	nodeTypeManager := utils.NewNodeTypeManager()
	ourPublicIP, err := nodeTypeManager.GetExternalIP()
	if err != nil {
		return false
	}
	ourPrivateIP, err := nodeTypeManager.GetLocalIP()
	if err != nil {
		return false
	}

	// Check 1: Same public IP (behind same NAT gateway)
	if peer.NetworkInfo.PublicIP != ourPublicIP {
		return false
	}

	// Check 2: Private IPs in same subnet
	subnetMask := mb.config.GetConfigInt("hole_punch_lan_subnet_mask", 24, 8, 32)
	ourSubnet := getSubnet(ourPrivateIP, subnetMask)
	peerSubnet := getSubnet(peer.NetworkInfo.PrivateIP, subnetMask)

	if ourSubnet == "" || peerSubnet == "" {
		return false
	}

	return ourSubnet == peerSubnet
}

// getAllKnownPeers gets all peers from database for a topic
func (mb *MetadataBroadcaster) getAllKnownPeers(topic string) ([]*database.PeerMetadata, error) {
	return mb.dbManager.PeerMetadata.GetPeersByTopic(topic)
}

// nextSequence generates the next sequence number
func (mb *MetadataBroadcaster) nextSequence() int64 {
	mb.sequenceMutex.Lock()
	defer mb.sequenceMutex.Unlock()
	mb.sequenceNum++
	return mb.sequenceNum
}

// shouldProcessUpdate checks if we should process an update (deduplication)
func (mb *MetadataBroadcaster) ShouldProcessUpdate(nodeID string, sequence int64) bool {
	mb.dedupeMutex.RLock()
	defer mb.dedupeMutex.RUnlock()

	key := fmt.Sprintf("%s:%d", nodeID, sequence)
	if _, seen := mb.recentBroadcasts[key]; seen {
		return false // Already seen
	}

	return true
}

// recordBroadcast records a broadcast for deduplication
func (mb *MetadataBroadcaster) recordBroadcast(nodeID string, sequence int64) {
	mb.dedupeMutex.Lock()
	defer mb.dedupeMutex.Unlock()

	key := fmt.Sprintf("%s:%d", nodeID, sequence)
	mb.recentBroadcasts[key] = time.Now().Unix()
}

// markDelivered marks a delivery as successful
func (mb *MetadataBroadcaster) markDelivered(task *DeliveryTask, strategy string) {
	task.Status = "delivered"
	task.Strategy = strategy

	mb.deliveryMutex.Lock()
	delete(mb.pendingDeliveries, task.TargetPeerID)
	mb.deliveryMutex.Unlock()

	mb.recordDeliverySuccess(strategy)
}

// markFailed marks a delivery as failed
func (mb *MetadataBroadcaster) markFailed(task *DeliveryTask) {
	task.Status = "failed"

	mb.deliveryMutex.Lock()
	delete(mb.pendingDeliveries, task.TargetPeerID)
	mb.deliveryMutex.Unlock()

	mb.recordDeliveryFailure()
}

// retryWorker periodically retries failed deliveries
func (mb *MetadataBroadcaster) retryWorker() {
	retryInterval := mb.config.GetConfigDuration("metadata_broadcast_retry_interval", 30*time.Second)
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-mb.ctx.Done():
			return
		case <-ticker.C:
			mb.retryPendingDeliveries()
		}
	}
}

// retryPendingDeliveries retries all pending deliveries
func (mb *MetadataBroadcaster) retryPendingDeliveries() {
	mb.deliveryMutex.RLock()
	tasks := make([]*DeliveryTask, 0, len(mb.pendingDeliveries))
	for _, task := range mb.pendingDeliveries {
		if task.Status == "pending" && time.Since(task.LastAttempt) > 30*time.Second {
			tasks = append(tasks, task)
		}
	}
	mb.deliveryMutex.RUnlock()

	for _, task := range tasks {
		// Get peer metadata
		peer, err := mb.dbManager.PeerMetadata.GetPeerMetadata(task.TargetPeerID, "")
		if err != nil {
			mb.logger.Debug(fmt.Sprintf("Peer %s not found for retry, skipping", task.TargetPeerID), "metadata-broadcaster")
			mb.markFailed(task)
			continue
		}

		go mb.attemptDelivery(peer, task)
	}
}

// cleanupWorker periodically cleans up old deduplication entries
func (mb *MetadataBroadcaster) cleanupWorker() {
	ticker := time.NewTicker(mb.dedupeWindow)
	defer ticker.Stop()

	for {
		select {
		case <-mb.ctx.Done():
			return
		case <-ticker.C:
			mb.cleanupDeduplication()
		}
	}
}

// cleanupDeduplication removes old deduplication entries
func (mb *MetadataBroadcaster) cleanupDeduplication() {
	mb.dedupeMutex.Lock()
	defer mb.dedupeMutex.Unlock()

	now := time.Now().Unix()
	cutoff := now - int64(mb.dedupeWindow.Seconds())

	for key, timestamp := range mb.recentBroadcasts {
		if timestamp < cutoff {
			delete(mb.recentBroadcasts, key)
		}
	}
}

// Metrics recording
func (mb *MetadataBroadcaster) recordBroadcastMetric() {
	mb.metricsMutex.Lock()
	defer mb.metricsMutex.Unlock()
	mb.totalBroadcasts++
}

func (mb *MetadataBroadcaster) recordDeliverySuccess(strategy string) {
	mb.metricsMutex.Lock()
	defer mb.metricsMutex.Unlock()
	mb.successDeliveries++
	mb.deliveriesByStrategy[strategy]++
}

func (mb *MetadataBroadcaster) recordDeliveryFailure() {
	mb.metricsMutex.Lock()
	defer mb.metricsMutex.Unlock()
	mb.failedDeliveries++
}

// GetMetrics returns broadcaster metrics
func (mb *MetadataBroadcaster) GetMetrics() map[string]interface{} {
	mb.metricsMutex.RLock()
	defer mb.metricsMutex.RUnlock()

	mb.deliveryMutex.RLock()
	pendingCount := len(mb.pendingDeliveries)
	mb.deliveryMutex.RUnlock()

	successRate := float64(0)
	totalDeliveries := mb.successDeliveries + mb.failedDeliveries
	if totalDeliveries > 0 {
		successRate = float64(mb.successDeliveries) / float64(totalDeliveries) * 100
	}

	return map[string]interface{}{
		"total_broadcasts":     mb.totalBroadcasts,
		"success_deliveries":   mb.successDeliveries,
		"failed_deliveries":    mb.failedDeliveries,
		"pending_deliveries":   pendingCount,
		"success_rate":         fmt.Sprintf("%.1f%%", successRate),
		"deliveries_by_strategy": mb.deliveriesByStrategy,
	}
}
