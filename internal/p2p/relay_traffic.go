package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// TrafficType represents the type of relay traffic
type TrafficType string

const (
	TrafficTypeCoordination TrafficType = "coordination" // Free hole-punching coordination
	TrafficTypeRelay        TrafficType = "relay"        // Paid relay traffic
)

// TrafficRecord represents a single traffic measurement
type TrafficRecord struct {
	SessionID    string
	PeerID       string
	TrafficType  TrafficType
	IngressBytes int64
	EgressBytes  int64
	Timestamp    time.Time
}

// TrafficStats represents aggregated traffic statistics
type TrafficStats struct {
	TotalCoordinationBytes int64
	TotalRelayBytes        int64
	TotalFreeBytes         int64
	TotalBillableBytes     int64
	TotalSessions          int
	LastUpdated            time.Time
}

// RelayTrafficMonitor monitors and tracks relay traffic
type RelayTrafficMonitor struct {
	config    *utils.ConfigManager
	logger    *utils.LogsManager
	dbManager *database.SQLiteManager

	// Traffic batch for efficient database writes
	trafficBatch      []TrafficRecord
	batchMutex        sync.Mutex
	batchSize         int
	batchInterval     time.Duration
	batchTicker       *time.Ticker
	stopChan          chan struct{}

	// Real-time statistics
	stats      *TrafficStats
	statsMutex sync.RWMutex
}

// NewRelayTrafficMonitor creates a new relay traffic monitor
func NewRelayTrafficMonitor(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager) *RelayTrafficMonitor {
	batchSize := config.GetConfigInt("relay_traffic_batch_size", 100, 10, 1000)
	batchInterval := config.GetConfigDuration("relay_batch_interval", 30*time.Second)

	rtm := &RelayTrafficMonitor{
		config:        config,
		logger:        logger,
		dbManager:     dbManager,
		trafficBatch:  make([]TrafficRecord, 0, batchSize),
		batchSize:     batchSize,
		batchInterval: batchInterval,
		stopChan:      make(chan struct{}),
		stats: &TrafficStats{
			LastUpdated: time.Now(),
		},
	}

	// Start batch processing
	rtm.batchTicker = time.NewTicker(batchInterval)
	go rtm.processBatchedTraffic()

	logger.Info(fmt.Sprintf("Relay traffic monitor initialized (batch_size: %d, interval: %v)",
		batchSize, batchInterval), "relay-traffic")

	return rtm
}

// RecordTraffic records traffic for a relay session
func (rtm *RelayTrafficMonitor) RecordTraffic(sessionID, peerID string, trafficType TrafficType, ingressBytes, egressBytes int64) {
	record := TrafficRecord{
		SessionID:    sessionID,
		PeerID:       peerID,
		TrafficType:  trafficType,
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
		Timestamp:    time.Now(),
	}

	// Update real-time stats
	rtm.updateStats(record)

	// Add to batch
	rtm.batchMutex.Lock()
	rtm.trafficBatch = append(rtm.trafficBatch, record)

	// Flush if batch is full
	if len(rtm.trafficBatch) >= rtm.batchSize {
		rtm.flushBatchUnsafe()
	}
	rtm.batchMutex.Unlock()

	rtm.logger.Debug(fmt.Sprintf("Recorded %s traffic: session=%s, peer=%s, in=%d, out=%d",
		trafficType, sessionID, peerID, ingressBytes, egressBytes), "relay-traffic")
}

// RecordCoordinationTraffic records free hole-punching coordination traffic
func (rtm *RelayTrafficMonitor) RecordCoordinationTraffic(sessionID, peerID string, bytes int64) {
	rtm.RecordTraffic(sessionID, peerID, TrafficTypeCoordination, bytes, 0)
}

// RecordRelayTraffic records paid relay traffic
func (rtm *RelayTrafficMonitor) RecordRelayTraffic(sessionID, peerID string, ingressBytes, egressBytes int64) {
	rtm.RecordTraffic(sessionID, peerID, TrafficTypeRelay, ingressBytes, egressBytes)
}

// updateStats updates real-time statistics
func (rtm *RelayTrafficMonitor) updateStats(record TrafficRecord) {
	rtm.statsMutex.Lock()
	defer rtm.statsMutex.Unlock()

	totalBytes := record.IngressBytes + record.EgressBytes

	if record.TrafficType == TrafficTypeCoordination {
		rtm.stats.TotalCoordinationBytes += totalBytes
		rtm.stats.TotalFreeBytes += totalBytes
	} else {
		rtm.stats.TotalRelayBytes += totalBytes
		rtm.stats.TotalBillableBytes += totalBytes
	}

	rtm.stats.LastUpdated = time.Now()
}

// processBatchedTraffic processes batched traffic records periodically
func (rtm *RelayTrafficMonitor) processBatchedTraffic() {
	for {
		select {
		case <-rtm.batchTicker.C:
			rtm.batchMutex.Lock()
			if len(rtm.trafficBatch) > 0 {
				rtm.flushBatchUnsafe()
			}
			rtm.batchMutex.Unlock()

		case <-rtm.stopChan:
			// Final flush before shutdown
			rtm.batchMutex.Lock()
			if len(rtm.trafficBatch) > 0 {
				rtm.flushBatchUnsafe()
			}
			rtm.batchMutex.Unlock()
			return
		}
	}
}

// flushBatchUnsafe flushes the traffic batch to database (must be called with lock held)
func (rtm *RelayTrafficMonitor) flushBatchUnsafe() {
	if len(rtm.trafficBatch) == 0 {
		return
	}

	if rtm.dbManager == nil || rtm.dbManager.Relay == nil {
		rtm.logger.Warn("Cannot flush traffic batch: database not available", "relay-traffic")
		rtm.trafficBatch = rtm.trafficBatch[:0]
		return
	}

	// Write records to database
	for _, record := range rtm.trafficBatch {
		err := rtm.dbManager.Relay.RecordTraffic(
			record.SessionID,
			record.PeerID,
			"", // relayNodeID will be set by database layer
			string(record.TrafficType),
			record.IngressBytes,
			record.EgressBytes,
			record.Timestamp,
			time.Time{}, // endTime is empty for ongoing traffic
		)

		if err != nil {
			rtm.logger.Error(fmt.Sprintf("Failed to record traffic: %v", err), "relay-traffic")
		}
	}

	rtm.logger.Debug(fmt.Sprintf("Flushed %d traffic records to database", len(rtm.trafficBatch)), "relay-traffic")

	// Clear batch
	rtm.trafficBatch = rtm.trafficBatch[:0]
}

// GetStats returns current traffic statistics
func (rtm *RelayTrafficMonitor) GetStats() *TrafficStats {
	rtm.statsMutex.RLock()
	defer rtm.statsMutex.RUnlock()

	// Return a copy
	return &TrafficStats{
		TotalCoordinationBytes: rtm.stats.TotalCoordinationBytes,
		TotalRelayBytes:        rtm.stats.TotalRelayBytes,
		TotalFreeBytes:         rtm.stats.TotalFreeBytes,
		TotalBillableBytes:     rtm.stats.TotalBillableBytes,
		TotalSessions:          rtm.stats.TotalSessions,
		LastUpdated:            rtm.stats.LastUpdated,
	}
}

// GetPeerTrafficSummary returns traffic summary for a specific peer
func (rtm *RelayTrafficMonitor) GetPeerTrafficSummary(peerID string, since time.Time) (map[string]interface{}, error) {
	if rtm.dbManager == nil || rtm.dbManager.Relay == nil {
		return nil, fmt.Errorf("database not available")
	}

	return rtm.dbManager.Relay.GetPeerTrafficSummary(peerID, since)
}

// IsCoordinationTraffic checks if a message should be classified as coordination traffic
func (rtm *RelayTrafficMonitor) IsCoordinationTraffic(messageType string, payloadSize int64) bool {
	maxCoordMsgSize := rtm.config.GetConfigInt64("relay_max_coordination_msg_size", 10240, 1024, 1048576)

	// Hole punch messages are always coordination
	if messageType == "hole_punch" || messageType == string(MessageTypeRelayHolePunch) {
		return payloadSize <= maxCoordMsgSize
	}

	return false
}

// CalculateBilling calculates billing for a peer based on their traffic
func (rtm *RelayTrafficMonitor) CalculateBilling(peerID string, since time.Time) (map[string]interface{}, error) {
	summary, err := rtm.GetPeerTrafficSummary(peerID, since)
	if err != nil {
		return nil, err
	}

	// Get pricing configuration
	pricingPerGB := rtm.config.GetConfigFloat64("relay_pricing_per_gb", 0.001, 0, 1.0)

	// Extract traffic data
	relayBytes, _ := summary["relay_bytes"].(int64)
	coordinationBytes, _ := summary["coordination_bytes"].(int64)

	// Calculate cost (relay traffic only, coordination is free)
	relayGB := float64(relayBytes) / (1024 * 1024 * 1024)
	cost := relayGB * pricingPerGB

	billing := map[string]interface{}{
		"peer_id":            peerID,
		"period_start":       since,
		"period_end":         time.Now(),
		"coordination_bytes": coordinationBytes,
		"relay_bytes":        relayBytes,
		"total_bytes":        coordinationBytes + relayBytes,
		"free_bytes":         coordinationBytes,
		"billable_bytes":     relayBytes,
		"billable_gb":        relayGB,
		"pricing_per_gb":     pricingPerGB,
		"amount_due":         cost,
		"currency":           "USD",
	}

	return billing, nil
}

// GetTrafficBreakdown returns detailed traffic breakdown by type
func (rtm *RelayTrafficMonitor) GetTrafficBreakdown() map[string]interface{} {
	stats := rtm.GetStats()

	totalBytes := stats.TotalCoordinationBytes + stats.TotalRelayBytes
	var coordinationPercent, relayPercent float64

	if totalBytes > 0 {
		coordinationPercent = float64(stats.TotalCoordinationBytes) / float64(totalBytes) * 100
		relayPercent = float64(stats.TotalRelayBytes) / float64(totalBytes) * 100
	}

	return map[string]interface{}{
		"coordination_bytes":   stats.TotalCoordinationBytes,
		"coordination_percent": coordinationPercent,
		"relay_bytes":          stats.TotalRelayBytes,
		"relay_percent":        relayPercent,
		"total_free_bytes":     stats.TotalFreeBytes,
		"total_billable_bytes": stats.TotalBillableBytes,
		"total_bytes":          totalBytes,
		"last_updated":         stats.LastUpdated,
	}
}

// Stop stops the traffic monitor
func (rtm *RelayTrafficMonitor) Stop() {
	rtm.logger.Info("Stopping relay traffic monitor...", "relay-traffic")

	if rtm.batchTicker != nil {
		rtm.batchTicker.Stop()
	}

	close(rtm.stopChan)

	rtm.logger.Info("Relay traffic monitor stopped", "relay-traffic")
}
