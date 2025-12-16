package p2p

import (
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeriodicDiscovery handles periodic DHT peer discovery
// Runs discovery every N hours to find new peers in the network
type PeriodicDiscovery struct {
	discoveryService *PeerDiscoveryService
	knownPeers       *KnownPeersManager
	logger           *utils.LogsManager
	config           *utils.ConfigManager

	// Discovery control
	discoveryTicker *time.Ticker
	stopChan        chan struct{}
	running         bool
	runningMutex    sync.Mutex

	// Discovery state
	lastDiscoveryTime time.Time
	totalPeersFound   int
	statsMutex        sync.RWMutex
}

// NewPeriodicDiscovery creates a new periodic discovery manager
func NewPeriodicDiscovery(
	discoveryService *PeerDiscoveryService,
	knownPeers *KnownPeersManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *PeriodicDiscovery {
	return &PeriodicDiscovery{
		discoveryService: discoveryService,
		knownPeers:       knownPeers,
		logger:           logger,
		config:           config,
		stopChan:         make(chan struct{}),
		running:          false,
	}
}

// RunDiscovery performs a single DHT discovery operation for the given topic
// Returns the number of new peers found
func (pd *PeriodicDiscovery) RunDiscovery(topic string) (int, error) {
	pd.logger.Info(fmt.Sprintf("Starting DHT peer discovery for topic: %s", topic), "periodic-discovery")

	startTime := time.Now()

	// Get current known peers count (before discovery)
	beforeCount, err := pd.knownPeers.GetKnownPeersCountByTopic(topic)
	if err != nil {
		pd.logger.Warn(fmt.Sprintf("Failed to get initial peer count: %v", err), "periodic-discovery")
		beforeCount = 0
	}

	// Run DHT discovery
	// This will:
	// 1. Query DHT for peers in the topic's infohash
	// 2. Store discovered peers in known_peers database
	// 3. Return list of discovered peers
	discoveredPeers, err := pd.discoveryService.DiscoverPeers(DiscoveryOptions{
		Topic:              topic,
		MaxPeers:           100,
		Timeout:            30 * time.Second,
		IncludeUnreachable: false,
		CacheOnly:          false,
	})
	if err != nil {
		return 0, fmt.Errorf("DHT discovery failed: %v", err)
	}

	// Get new known peers count (after discovery)
	afterCount, err := pd.knownPeers.GetKnownPeersCountByTopic(topic)
	if err != nil {
		pd.logger.Warn(fmt.Sprintf("Failed to get final peer count: %v", err), "periodic-discovery")
		afterCount = beforeCount
	}

	newPeersFound := afterCount - beforeCount
	duration := time.Since(startTime)

	pd.logger.Info(fmt.Sprintf("DHT discovery completed (topic: %s, discovered: %d, new: %d, duration: %v)",
		topic, len(discoveredPeers), newPeersFound, duration.Round(time.Millisecond)), "periodic-discovery")

	// Update statistics
	pd.statsMutex.Lock()
	pd.lastDiscoveryTime = time.Now()
	pd.totalPeersFound += newPeersFound
	pd.statsMutex.Unlock()

	return newPeersFound, nil
}

// StartPeriodicDiscovery starts periodic DHT discovery for the given topic
// Runs discovery every N hours (default: 3 hours)
func (pd *PeriodicDiscovery) StartPeriodicDiscovery(topic string) {
	pd.runningMutex.Lock()
	if pd.running {
		pd.logger.Warn("Periodic discovery already running", "periodic-discovery")
		pd.runningMutex.Unlock()
		return
	}
	pd.running = true
	pd.runningMutex.Unlock()

	// Get discovery interval from config (default: 3 hours)
	intervalHours := pd.config.GetConfigInt("periodic_discovery_interval_hours", 3, 1, 24)
	interval := time.Duration(intervalHours) * time.Hour

	pd.logger.Info(fmt.Sprintf("Starting periodic DHT discovery (topic: %s, interval: %v)", topic, interval), "periodic-discovery")

	// Create ticker
	pd.discoveryTicker = time.NewTicker(interval)

	// Start background goroutine
	go func() {
		for {
			select {
			case <-pd.discoveryTicker.C:
				pd.logger.Debug("Periodic discovery triggered", "periodic-discovery")

				newPeers, err := pd.RunDiscovery(topic)
				if err != nil {
					pd.logger.Warn(fmt.Sprintf("Periodic discovery failed: %v", err), "periodic-discovery")
				} else {
					pd.logger.Debug(fmt.Sprintf("Periodic discovery completed (new peers: %d)", newPeers), "periodic-discovery")
				}

			case <-pd.stopChan:
				pd.logger.Info("Stopping periodic DHT discovery", "periodic-discovery")
				pd.runningMutex.Lock()
				pd.running = false
				pd.runningMutex.Unlock()
				return
			}
		}
	}()
}

// StopPeriodicDiscovery stops the periodic discovery goroutine
func (pd *PeriodicDiscovery) StopPeriodicDiscovery() {
	pd.runningMutex.Lock()
	defer pd.runningMutex.Unlock()

	if !pd.running {
		return
	}

	if pd.discoveryTicker != nil {
		pd.discoveryTicker.Stop()
	}

	close(pd.stopChan)
	pd.running = false
	pd.logger.Info("Periodic DHT discovery stopped", "periodic-discovery")
}

// GetStats returns discovery statistics
func (pd *PeriodicDiscovery) GetStats() map[string]interface{} {
	pd.runningMutex.Lock()
	running := pd.running
	pd.runningMutex.Unlock()

	pd.statsMutex.RLock()
	defer pd.statsMutex.RUnlock()

	var timeSinceLastDiscovery time.Duration
	if !pd.lastDiscoveryTime.IsZero() {
		timeSinceLastDiscovery = time.Since(pd.lastDiscoveryTime)
	}

	return map[string]interface{}{
		"running":                   running,
		"last_discovery_time":       pd.lastDiscoveryTime,
		"time_since_last_discovery": timeSinceLastDiscovery.Round(time.Second),
		"total_peers_found":         pd.totalPeersFound,
	}
}
