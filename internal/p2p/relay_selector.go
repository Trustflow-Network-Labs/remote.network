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
	PeerID          string // Persistent Ed25519-based peer ID
	NodeID          string // DHT node ID (may change on restart)
	Endpoint        string
	Latency         time.Duration
	ReputationScore float64
	PricingPerGB    float64
	Capacity        int
	LastSeen        time.Time
	Metadata        *database.PeerMetadata
	FailureCount    int       // Number of consecutive connection failures
	LastFailure     time.Time // Timestamp of last connection failure
	IsAvailable     bool      // False if ping/pong failed, true if responsive
	UnavailableMsg  string    // Reason why relay is unavailable (e.g., "ping timeout")
}

// RelaySelector selects the best relay peer for NAT traversal
type RelaySelector struct {
	config     *utils.ConfigManager
	logger     *utils.LogsManager
	dbManager  *database.SQLiteManager
	quicPeer   *QUICPeer
	myPeerID   string // Our persistent peer ID for looking up preferences

	// Relay candidates
	candidates     map[string]*RelayCandidate
	candidatesMutex sync.RWMutex

	// Current best relay
	bestRelay      *RelayCandidate
	bestRelayMutex sync.RWMutex
}

// NewRelaySelector creates a new relay selector
func NewRelaySelector(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager, quicPeer *QUICPeer, myPeerID string) *RelaySelector {
	return &RelaySelector{
		config:     config,
		logger:     logger,
		dbManager:  dbManager,
		quicPeer:   quicPeer,
		myPeerID:   myPeerID,
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
		PeerID:          metadata.PeerID, // Persistent Ed25519-based peer ID
		NodeID:          metadata.NodeID, // DHT node ID
		Endpoint:        metadata.NetworkInfo.RelayEndpoint,
		ReputationScore: float64(metadata.NetworkInfo.ReputationScore) / 10000.0, // Convert from basis points to 0.0-1.0
		PricingPerGB:    float64(metadata.NetworkInfo.RelayPricing) / 1000000.0,  // Convert from micro-units
		Capacity:        metadata.NetworkInfo.RelayCapacity,
		LastSeen:        metadata.LastSeen,
		Metadata:        metadata,
		Latency:         time.Duration(0), // Will be measured
		IsAvailable:     true,               // Assume available until proven otherwise
		UnavailableMsg:  "",
	}

	// Use PeerID as the map key (persistent identifier)
	rs.candidates[metadata.PeerID] = candidate
	totalCandidates := len(rs.candidates)
	rs.logger.Debug(fmt.Sprintf("Added relay candidate: %s (PeerID: %s, endpoint: %s, reputation: %.2f, pricing: %.4f)",
		metadata.NodeID, metadata.PeerID[:8], candidate.Endpoint, candidate.ReputationScore, candidate.PricingPerGB), "relay-selector")
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

// ClearCandidates removes all relay candidates
// This is used when network configuration changes to force fresh relay discovery
func (rs *RelaySelector) ClearCandidates() {
	rs.candidatesMutex.Lock()
	defer rs.candidatesMutex.Unlock()

	count := len(rs.candidates)
	rs.candidates = make(map[string]*RelayCandidate)
	rs.logger.Info(fmt.Sprintf("Cleared %d relay candidates for fresh discovery", count), "relay-selector")
}

// UpdateCandidateLastSeen updates the LastSeen timestamp for a candidate
// This prevents actively-used relays from being removed as stale
func (rs *RelaySelector) UpdateCandidateLastSeen(peerID string) {
	rs.candidatesMutex.Lock()
	defer rs.candidatesMutex.Unlock()

	if candidate, ok := rs.candidates[peerID]; ok {
		candidate.LastSeen = time.Now()
		rs.logger.Debug(fmt.Sprintf("Updated LastSeen for relay candidate: %s", peerID[:8]), "relay-selector")
	}
}

// GetCandidateCount returns the number of available relay candidates
func (rs *RelaySelector) GetCandidateCount() int {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()
	return len(rs.candidates)
}

// HasCandidate checks if a relay candidate already exists by PeerID
// This prevents duplicate additions from metadata discovery callbacks
func (rs *RelaySelector) HasCandidate(peerID string) bool {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()

	// Check by PeerID (map key)
	_, exists := rs.candidates[peerID]
	return exists
}

// MeasureLatency measures latency to a relay candidate using QUIC ping
func (rs *RelaySelector) MeasureLatency(candidate *RelayCandidate) (time.Duration, error) {
	startTime := time.Now()

	// Send ping message via QUIC
	err := rs.quicPeer.Ping(candidate.Endpoint)
	if err != nil {
		// Mark relay as unavailable when ping fails
		rs.candidatesMutex.Lock()
		if c, exists := rs.candidates[candidate.PeerID]; exists {
			c.IsAvailable = false
			c.UnavailableMsg = fmt.Sprintf("Ping timeout: %v", err)
			c.LastFailure = time.Now()
			c.FailureCount++
		}
		rs.candidatesMutex.Unlock()

		rs.logger.Warn(fmt.Sprintf("Relay %s (PeerID: %s) marked as unavailable: ping failed", candidate.NodeID[:8], candidate.PeerID[:8]), "relay-selector")
		return 0, fmt.Errorf("failed to ping relay %s: %v", candidate.NodeID, err)
	}

	latency := time.Since(startTime)

	// Update candidate latency and mark as available (successful ping)
	rs.candidatesMutex.Lock()
	if c, exists := rs.candidates[candidate.PeerID]; exists {
		c.Latency = latency
		c.IsAvailable = true
		c.UnavailableMsg = ""
		c.FailureCount = 0 // Reset failure count on success
	}
	rs.candidatesMutex.Unlock()

	rs.logger.Debug(fmt.Sprintf("Measured latency to %s (PeerID: %s): %v", candidate.NodeID, candidate.PeerID[:8], latency), "relay-selector")
	return latency, nil
}

// MeasureCandidate measures latency to a single relay candidate by PeerID or NodeID
// Used for on-demand measurement upon relay discovery or re-evaluation
// Returns the measured latency and any error encountered
func (rs *RelaySelector) MeasureCandidate(peerIDOrNodeID string) (time.Duration, error) {
	// Look up candidate by PeerID or NodeID
	rs.candidatesMutex.RLock()
	var candidate *RelayCandidate
	for _, c := range rs.candidates {
		if c.PeerID == peerIDOrNodeID || c.NodeID == peerIDOrNodeID {
			candidate = c
			break
		}
	}
	rs.candidatesMutex.RUnlock()

	if candidate == nil {
		return 0, fmt.Errorf("relay candidate %s not found", peerIDOrNodeID)
	}

	rs.logger.Debug(fmt.Sprintf("Measuring latency for relay candidate %s (endpoint: %s)", candidate.PeerID[:8], candidate.Endpoint), "relay-selector")

	// Measure latency
	latency, err := rs.MeasureLatency(candidate)
	if err != nil {
		rs.logger.Warn(fmt.Sprintf("Failed to measure latency for relay %s: %v", candidate.PeerID[:8], err), "relay-selector")
		return 0, err
	}

	rs.logger.Info(fmt.Sprintf("✅ Measured relay %s latency: %v", candidate.PeerID[:8], latency), "relay-selector")
	return latency, nil
}

// MeasureAllCandidates measures latency to all relay candidates using hybrid batching
// Returns list of measured endpoints for cleanup by caller
// Batching approach: measure N relays at a time to balance speed and resource usage
func (rs *RelaySelector) MeasureAllCandidates() []string {
	rs.candidatesMutex.RLock()
	candidateList := make([]*RelayCandidate, 0, len(rs.candidates))
	for _, candidate := range rs.candidates {
		candidateList = append(candidateList, candidate)
	}
	rs.candidatesMutex.RUnlock()

	if len(candidateList) == 0 {
		rs.logger.Debug("No relay candidates to measure", "relay-selector")
		return []string{}
	}

	// Get batch size from config (default: 3 relays per batch)
	batchSize := rs.config.GetConfigInt("relay_batch_size", 3, 1, 20)

	rs.logger.Info(fmt.Sprintf("Measuring latency for %d relay candidates (batch size: %d)...", len(candidateList), batchSize), "relay-selector")

	measuredEndpoints := make([]string, 0, len(candidateList))
	totalMeasured := 0

	// Process candidates in batches
	for i := 0; i < len(candidateList); i += batchSize {
		end := i + batchSize
		if end > len(candidateList) {
			end = len(candidateList)
		}
		batch := candidateList[i:end]

		rs.logger.Debug(fmt.Sprintf("Processing batch %d-%d of %d candidates", i+1, end, len(candidateList)), "relay-selector")

		// PHASE 1: Pre-establish connections for this batch
		var wg sync.WaitGroup
		for _, candidate := range batch {
			wg.Add(1)
			go func(c *RelayCandidate) {
				defer wg.Done()
				_, err := rs.quicPeer.ConnectToPeer(c.Endpoint)
				if err != nil {
					rs.logger.Debug(fmt.Sprintf("Failed to pre-establish connection to relay %s: %v", c.NodeID, err), "relay-selector")
				} else {
					rs.logger.Debug(fmt.Sprintf("Pre-established connection to relay %s", c.NodeID), "relay-selector")
				}
			}(candidate)
		}
		wg.Wait()

		// PHASE 2: Measure latency for this batch over existing connections
		for _, candidate := range batch {
			wg.Add(1)
			go func(c *RelayCandidate) {
				defer wg.Done()
				if _, err := rs.MeasureLatency(c); err != nil {
					rs.logger.Debug(fmt.Sprintf("Failed to measure latency to %s: %v", c.NodeID, err), "relay-selector")
				} else {
					totalMeasured++
				}
			}(candidate)
		}
		wg.Wait()

		// Collect endpoints from this batch
		for _, candidate := range batch {
			measuredEndpoints = append(measuredEndpoints, candidate.Endpoint)
		}

		rs.logger.Debug(fmt.Sprintf("Completed batch %d-%d (%d/%d candidates measured)", i+1, end, totalMeasured, len(candidateList)), "relay-selector")
	}

	rs.logger.Info(fmt.Sprintf("✅ Measured latency for %d/%d relay candidates", totalMeasured, len(candidateList)), "relay-selector")
	return measuredEndpoints
}

// SelectBestRelay selects the best relay based on preferred relay (if set), then latency, reputation, and pricing
func (rs *RelaySelector) SelectBestRelay() *RelayCandidate {
	rs.candidatesMutex.RLock()
	defer rs.candidatesMutex.RUnlock()

	if len(rs.candidates) == 0 {
		rs.logger.Debug("No relay candidates available", "relay-selector")
		return nil
	}

	// Get selection criteria from config
	// Max latency set to 5s to match ping timeout (allowing relays that respond within timeout window)
	maxLatency := rs.config.GetConfigDuration("relay_max_latency", 5*time.Second)
	minReputation := rs.config.GetConfigFloat64("relay_min_reputation", 0.3, 0.0, 1.0)
	maxPricing := rs.config.GetConfigFloat64("relay_max_pricing", 0.01, 0.0, 1.0)

	// STEP 1: Check for preferred relay first
	relayDB, err := database.NewRelayDB(rs.dbManager.GetDB(), rs.logger)
	if err == nil {
		preferredPeerID, err := relayDB.GetPreferredRelay(rs.myPeerID)
		if err == nil && preferredPeerID != "" {
			rs.logger.Info(fmt.Sprintf("🎯 Checking for preferred relay: %s", preferredPeerID[:8]), "relay-selector")

			// Find preferred relay in candidates
			for _, candidate := range rs.candidates {
				if candidate.PeerID == preferredPeerID {
					// Check if preferred relay is available
					if !candidate.IsAvailable {
						rs.logger.Warn(fmt.Sprintf("Preferred relay %s is unavailable (%s), using fallback selection",
							preferredPeerID[:8], candidate.UnavailableMsg), "relay-selector")
						break
					}

					// Check if preferred relay meets minimum criteria
					if candidate.Latency == 0 {
						rs.logger.Warn(fmt.Sprintf("Preferred relay %s has no latency measurement, skipping", preferredPeerID[:8]), "relay-selector")
						break
					}

					if candidate.Latency > maxLatency {
						rs.logger.Warn(fmt.Sprintf("Preferred relay %s exceeds max latency (%v > %v), using fallback selection",
							preferredPeerID[:8], candidate.Latency, maxLatency), "relay-selector")
						break
					}

					if candidate.ReputationScore < minReputation {
						rs.logger.Warn(fmt.Sprintf("Preferred relay %s below min reputation (%.2f < %.2f), using fallback selection",
							preferredPeerID[:8], candidate.ReputationScore, minReputation), "relay-selector")
						break
					}

					if candidate.PricingPerGB > maxPricing {
						rs.logger.Warn(fmt.Sprintf("Preferred relay %s exceeds max pricing (%.4f > %.4f), using fallback selection",
							preferredPeerID[:8], candidate.PricingPerGB, maxPricing), "relay-selector")
						break
					}

					// Preferred relay meets criteria - use it!
					rs.logger.Info(fmt.Sprintf("✅ Selected PREFERRED relay: %s (latency: %v, reputation: %.2f, pricing: %.4f)",
						preferredPeerID[:8], candidate.Latency, candidate.ReputationScore, candidate.PricingPerGB), "relay-selector")

					rs.bestRelayMutex.Lock()
					rs.bestRelay = candidate
					rs.bestRelayMutex.Unlock()

					return candidate
				}
			}

			if preferredPeerID != "" {
				rs.logger.Warn(fmt.Sprintf("Preferred relay %s not found in candidates or doesn't meet criteria, falling back to score-based selection", preferredPeerID[:8]), "relay-selector")
			}
		}
	}

	// STEP 2: Check for last used relay
	if err == nil {
		lastUsedPeerID, err := relayDB.GetLastUsedRelay(rs.myPeerID)
		if err == nil && lastUsedPeerID != "" {
			rs.logger.Info(fmt.Sprintf("🔄 Checking for last used relay: %s", lastUsedPeerID[:8]), "relay-selector")

			// Find last used relay in candidates
			for _, candidate := range rs.candidates {
				if candidate.PeerID == lastUsedPeerID {
					// Check if last used relay is available
					if !candidate.IsAvailable {
						rs.logger.Warn(fmt.Sprintf("Last used relay %s is unavailable (%s), using fallback selection",
							lastUsedPeerID[:8], candidate.UnavailableMsg), "relay-selector")
						break
					}

					// Check if last used relay meets minimum criteria
					if candidate.Latency == 0 {
						rs.logger.Warn(fmt.Sprintf("Last used relay %s has no latency measurement, skipping", lastUsedPeerID[:8]), "relay-selector")
						break
					}

					if candidate.Latency > maxLatency {
						rs.logger.Warn(fmt.Sprintf("Last used relay %s exceeds max latency (%v > %v), using fallback selection",
							lastUsedPeerID[:8], candidate.Latency, maxLatency), "relay-selector")
						break
					}

					if candidate.ReputationScore < minReputation {
						rs.logger.Warn(fmt.Sprintf("Last used relay %s below min reputation (%.2f < %.2f), using fallback selection",
							lastUsedPeerID[:8], candidate.ReputationScore, minReputation), "relay-selector")
						break
					}

					if candidate.PricingPerGB > maxPricing {
						rs.logger.Warn(fmt.Sprintf("Last used relay %s exceeds max pricing (%.4f > %.4f), using fallback selection",
							lastUsedPeerID[:8], candidate.PricingPerGB, maxPricing), "relay-selector")
						break
					}

					// Last used relay meets criteria - use it!
					rs.logger.Info(fmt.Sprintf("✅ Selected LAST USED relay: %s (latency: %v, reputation: %.2f, pricing: %.4f)",
						lastUsedPeerID[:8], candidate.Latency, candidate.ReputationScore, candidate.PricingPerGB), "relay-selector")

					rs.bestRelayMutex.Lock()
					rs.bestRelay = candidate
					rs.bestRelayMutex.Unlock()

					return candidate
				}
			}

			if lastUsedPeerID != "" {
				rs.logger.Warn(fmt.Sprintf("Last used relay %s not found in candidates or doesn't meet criteria, falling back to score-based selection", lastUsedPeerID[:8]), "relay-selector")
			}
		}
	}

	// STEP 3: Fallback to score-based selection
	var bestCandidate *RelayCandidate
	var bestScore float64 = -1

	for _, candidate := range rs.candidates {
		// Skip unavailable relays (ping failed/timeout)
		if !candidate.IsAvailable {
			continue
		}

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

	// Check if new relay is the preferred relay or last used relay - always switch to these if available
	relayDB, err := database.NewRelayDB(rs.dbManager.GetDB(), rs.logger)
	if err == nil {
		preferredPeerID, err := relayDB.GetPreferredRelay(rs.myPeerID)
		if err == nil && preferredPeerID != "" {
			// If new relay is preferred and current is not, ALWAYS switch
			if newRelay.PeerID == preferredPeerID && currentRelay.PeerID != preferredPeerID {
				rs.logger.Info(fmt.Sprintf("🔄 Switching to PREFERRED relay %s (from %s)",
					newRelay.PeerID[:8], currentRelay.PeerID[:8]), "relay-selector")
				return true
			}

			// If current relay is already preferred, don't switch
			if currentRelay.PeerID == preferredPeerID {
				rs.logger.Debug("Current relay is already preferred, staying connected", "relay-selector")
				return false
			}
		}

		// Check if new relay is the last used relay - allow switching to restore last used relay
		lastUsedPeerID, err := relayDB.GetLastUsedRelay(rs.myPeerID)
		if err == nil && lastUsedPeerID != "" {
			// If new relay is last used and current is not, ALLOW switch (to restore last used after restart)
			if newRelay.PeerID == lastUsedPeerID && currentRelay.PeerID != lastUsedPeerID {
				rs.logger.Info(fmt.Sprintf("🔄 Switching to LAST USED relay %s (from %s)",
					newRelay.PeerID[:8], currentRelay.PeerID[:8]), "relay-selector")
				return true
			}

			// If current relay is already last used, don't switch based on performance
			if currentRelay.PeerID == lastUsedPeerID {
				rs.logger.Debug("Current relay is already last used, staying connected", "relay-selector")
				return false
			}
		}
	}

	// DISABLED: Autonomous switching based on performance improvement
	// Periodic evaluation will continue to measure latencies, but will NOT
	// trigger automatic switches unless the user manually switches or current relay fails
	rs.logger.Debug("Autonomous switching based on performance is disabled - staying with current relay", "relay-selector")
	return false
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
