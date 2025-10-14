package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// RelayCandidate represents a potential relay peer with metrics
type RelayCandidate struct {
	NodeID          string
	Endpoint        string
	Latency         time.Duration
	ReputationScore float64
	PricingPerGB    float64
	Capacity        int
	LastSeen        time.Time
	Metadata        *database.PeerMetadata
}

// RelaySelector selects the best relay peer for NAT traversal
type RelaySelector struct {
	config     *utils.ConfigManager
	logger     *utils.LogsManager
	dbManager  *database.SQLiteManager
	quicPeer   *QUICPeer

	// Relay candidates
	candidates     map[string]*RelayCandidate
	candidatesMutex sync.RWMutex

	// Current best relay
	bestRelay      *RelayCandidate
	bestRelayMutex sync.RWMutex
}

// NewRelaySelector creates a new relay selector
func NewRelaySelector(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager, quicPeer *QUICPeer) *RelaySelector {
	return &RelaySelector{
		config:     config,
		logger:     logger,
		dbManager:  dbManager,
		quicPeer:   quicPeer,
		candidates: make(map[string]*RelayCandidate),
	}
}

// AddCandidate adds a relay candidate from discovered peer metadata
func (rs *RelaySelector) AddCandidate(metadata *database.PeerMetadata) error {
	// Only consider peers advertising relay service
	if !metadata.NetworkInfo.IsRelay {
		return nil
	}

	// Skip if relay endpoint is empty
	if metadata.NetworkInfo.RelayEndpoint == "" {
		return fmt.Errorf("relay candidate has empty endpoint")
	}

	rs.candidatesMutex.Lock()
	defer rs.candidatesMutex.Unlock()

	candidate := &RelayCandidate{
		NodeID:          metadata.NodeID,
		Endpoint:        metadata.NetworkInfo.RelayEndpoint,
		ReputationScore: float64(metadata.NetworkInfo.ReputationScore) / 10000.0, // Convert from basis points to 0.0-1.0
		PricingPerGB:    float64(metadata.NetworkInfo.RelayPricing) / 1000000.0,  // Convert from micro-units
		Capacity:        metadata.NetworkInfo.RelayCapacity,
		LastSeen:        metadata.LastSeen,
		Metadata:        metadata,
		Latency:         time.Duration(0), // Will be measured
	}

	rs.candidates[metadata.NodeID] = candidate
	totalCandidates := len(rs.candidates)
	rs.logger.Debug(fmt.Sprintf("Added relay candidate: %s (endpoint: %s, reputation: %.2f, pricing: %.4f)",
		metadata.NodeID, candidate.Endpoint, candidate.ReputationScore, candidate.PricingPerGB), "relay-selector")
	rs.logger.Info(fmt.Sprintf("Total relay candidates: %d", totalCandidates), "relay-selector")

	return nil
}

// RemoveCandidate removes a relay candidate
func (rs *RelaySelector) RemoveCandidate(nodeID string) {
	rs.candidatesMutex.Lock()
	defer rs.candidatesMutex.Unlock()

	delete(rs.candidates, nodeID)
	rs.logger.Debug(fmt.Sprintf("Removed relay candidate: %s", nodeID), "relay-selector")
}

// GetCandidateCount returns the number of available relay candidates
func (rs *RelaySelector) GetCandidateCount() int {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()
	return len(rs.candidates)
}

// MeasureLatency measures latency to a relay candidate using QUIC ping
func (rs *RelaySelector) MeasureLatency(candidate *RelayCandidate) (time.Duration, error) {
	startTime := time.Now()

	// Send ping message via QUIC
	err := rs.quicPeer.Ping(candidate.Endpoint)
	if err != nil {
		return 0, fmt.Errorf("failed to ping relay %s: %v", candidate.NodeID, err)
	}

	latency := time.Since(startTime)

	// Update candidate latency
	rs.candidatesMutex.Lock()
	if c, exists := rs.candidates[candidate.NodeID]; exists {
		c.Latency = latency
	}
	rs.candidatesMutex.Unlock()

	rs.logger.Debug(fmt.Sprintf("Measured latency to %s: %v", candidate.NodeID, latency), "relay-selector")
	return latency, nil
}

// MeasureAllCandidates measures latency to all relay candidates
func (rs *RelaySelector) MeasureAllCandidates() {
	rs.candidatesMutex.RLock()
	candidateList := make([]*RelayCandidate, 0, len(rs.candidates))
	for _, candidate := range rs.candidates {
		candidateList = append(candidateList, candidate)
	}
	rs.candidatesMutex.RUnlock()

	// Measure latency concurrently
	var wg sync.WaitGroup
	for _, candidate := range candidateList {
		wg.Add(1)
		go func(c *RelayCandidate) {
			defer wg.Done()
			if _, err := rs.MeasureLatency(c); err != nil {
				rs.logger.Debug(fmt.Sprintf("Failed to measure latency to %s: %v", c.NodeID, err), "relay-selector")
			}
		}(candidate)
	}
	wg.Wait()

	rs.logger.Debug(fmt.Sprintf("Measured latency for %d relay candidates", len(candidateList)), "relay-selector")
}

// SelectBestRelay selects the best relay based on latency, reputation, and pricing
func (rs *RelaySelector) SelectBestRelay() *RelayCandidate {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()

	if len(rs.candidates) == 0 {
		rs.logger.Debug("No relay candidates available", "relay-selector")
		return nil
	}

	// Get selection criteria from config
	maxLatency := rs.config.GetConfigDuration("relay_max_latency", 500*time.Millisecond)
	minReputation := rs.config.GetConfigFloat64("relay_min_reputation", 0.3, 0.0, 1.0)
	maxPricing := rs.config.GetConfigFloat64("relay_max_pricing", 0.01, 0.0, 1.0)

	var bestCandidate *RelayCandidate
	var bestScore float64 = -1

	for _, candidate := range rs.candidates {
		// Skip if latency not measured yet
		if candidate.Latency == 0 {
			continue
		}

		// Filter by criteria
		if candidate.Latency > maxLatency {
			rs.logger.Debug(fmt.Sprintf("Candidate %s exceeds max latency (%v > %v)",
				candidate.NodeID, candidate.Latency, maxLatency), "relay-selector")
			continue
		}

		if candidate.ReputationScore < minReputation {
			rs.logger.Debug(fmt.Sprintf("Candidate %s below min reputation (%.2f < %.2f)",
				candidate.NodeID, candidate.ReputationScore, minReputation), "relay-selector")
			continue
		}

		if candidate.PricingPerGB > maxPricing {
			rs.logger.Debug(fmt.Sprintf("Candidate %s exceeds max pricing (%.4f > %.4f)",
				candidate.NodeID, candidate.PricingPerGB, maxPricing), "relay-selector")
			continue
		}

		// Calculate selection score
		// Lower latency = better, higher reputation = better, lower pricing = better
		latencyScore := 1.0 - (float64(candidate.Latency.Milliseconds()) / float64(maxLatency.Milliseconds()))
		reputationScore := candidate.ReputationScore
		pricingScore := 1.0 - (candidate.PricingPerGB / maxPricing)

		// Weighted score: 50% latency, 30% reputation, 20% pricing
		score := (latencyScore * 0.5) + (reputationScore * 0.3) + (pricingScore * 0.2)

		rs.logger.Debug(fmt.Sprintf("Candidate %s score: %.3f (latency: %.3f, reputation: %.3f, pricing: %.3f)",
			candidate.NodeID, score, latencyScore, reputationScore, pricingScore), "relay-selector")

		if score > bestScore {
			bestScore = score
			bestCandidate = candidate
		}
	}

	if bestCandidate != nil {
		rs.logger.Info(fmt.Sprintf("Selected best relay: %s (latency: %v, reputation: %.2f, score: %.3f)",
			bestCandidate.NodeID, bestCandidate.Latency, bestCandidate.ReputationScore, bestScore), "relay-selector")

		// Update best relay
		rs.bestRelayMutex.Lock()
		rs.bestRelay = bestCandidate
		rs.bestRelayMutex.Unlock()
	} else {
		rs.logger.Warn("No suitable relay candidate found after filtering", "relay-selector")
	}

	return bestCandidate
}

// GetBestRelay returns the current best relay
func (rs *RelaySelector) GetBestRelay() *RelayCandidate {
	rs.bestRelayMutex.RLock()
	defer rs.bestRelayMutex.RUnlock()
	return rs.bestRelay
}

// ShouldSwitchRelay determines if we should switch to a new relay
func (rs *RelaySelector) ShouldSwitchRelay(currentRelay *RelayCandidate, newRelay *RelayCandidate) bool {
	if currentRelay == nil {
		return true
	}

	if newRelay == nil {
		return false
	}

	// Get switch threshold from config (e.g., 20% improvement required)
	switchThreshold := rs.config.GetConfigFloat64("relay_switch_threshold", 0.2, 0.0, 1.0)

	// Calculate improvement in latency
	latencyImprovement := float64(currentRelay.Latency-newRelay.Latency) / float64(currentRelay.Latency)

	// Calculate improvement in reputation
	reputationImprovement := (newRelay.ReputationScore - currentRelay.ReputationScore) / currentRelay.ReputationScore

	// Overall improvement score
	improvement := (latencyImprovement * 0.7) + (reputationImprovement * 0.3)

	shouldSwitch := improvement > switchThreshold

	if shouldSwitch {
		rs.logger.Info(fmt.Sprintf("Should switch relay: %.1f%% improvement (latency: %.1f%%, reputation: %.1f%%)",
			improvement*100, latencyImprovement*100, reputationImprovement*100), "relay-selector")
	}

	return shouldSwitch
}

// GetCandidates returns all relay candidates
func (rs *RelaySelector) GetCandidates() []*RelayCandidate {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()

	candidates := make([]*RelayCandidate, 0, len(rs.candidates))
	for _, candidate := range rs.candidates {
		candidates = append(candidates, candidate)
	}
	return candidates
}

// CleanupStaleCandidates removes candidates that haven't been seen recently
func (rs *RelaySelector) CleanupStaleCandidates(maxAge time.Duration) int {
	rs.candidatesMutex.Lock()
	defer rs.candidatesMutex.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for nodeID, candidate := range rs.candidates {
		if candidate.LastSeen.Before(cutoff) {
			delete(rs.candidates, nodeID)
			removed++
			rs.logger.Debug(fmt.Sprintf("Removed stale relay candidate: %s (last seen: %v ago)",
				nodeID, time.Since(candidate.LastSeen)), "relay-selector")
		}
	}

	if removed > 0 {
		rs.logger.Info(fmt.Sprintf("Cleaned up %d stale relay candidates", removed), "relay-selector")
	}

	return removed
}

// GetStats returns relay selector statistics
func (rs *RelaySelector) GetStats() map[string]interface{} {
	rs.candidatesMutex.RLock()
	candidateCount := len(rs.candidates)
	rs.candidatesMutex.RUnlock()

	rs.bestRelayMutex.RLock()
	var bestRelayID string
	var bestRelayLatency time.Duration
	if rs.bestRelay != nil {
		bestRelayID = rs.bestRelay.NodeID
		bestRelayLatency = rs.bestRelay.Latency
	}
	rs.bestRelayMutex.RUnlock()

	return map[string]interface{}{
		"candidate_count":    candidateCount,
		"best_relay_id":      bestRelayID,
		"best_relay_latency": bestRelayLatency.String(),
	}
}
