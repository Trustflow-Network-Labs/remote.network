package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// NetworkState represents the current network state
type NetworkState struct {
	LocalIP       string
	ExternalIP    string
	NATType       NATType
	LocalSubnet   string
	DetectionTime time.Time
}

// NetworkStateMonitor monitors for network changes and triggers appropriate actions
type NetworkStateMonitor struct {
	config            *utils.ConfigManager
	logger            *utils.LogsManager
	nodeTypeManager   *utils.NodeTypeManager
	natDetector       *NATDetector
	topologyMgr       *NATTopologyManager
	metadataPublisher *MetadataPublisher
	relayManager      *RelayManager // nil for public/relay nodes

	currentState  *NetworkState
	previousState *NetworkState
	stateMutex    sync.RWMutex

	ctx           context.Context
	cancel        context.CancelFunc
	monitorTicker *time.Ticker
	running       bool
	runningMutex  sync.Mutex

	// Change detection flags
	consecutiveFailures int
	maxFailuresBeforeAlert int
}

// NewNetworkStateMonitor creates a new network state monitor
func NewNetworkStateMonitor(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	natDetector *NATDetector,
	topologyMgr *NATTopologyManager,
	metadataPublisher *MetadataPublisher,
	relayManager *RelayManager,
) *NetworkStateMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	return &NetworkStateMonitor{
		config:                 config,
		logger:                 logger,
		nodeTypeManager:        utils.NewNodeTypeManager(),
		natDetector:            natDetector,
		topologyMgr:            topologyMgr,
		metadataPublisher:      metadataPublisher,
		relayManager:           relayManager,
		ctx:                    ctx,
		cancel:                 cancel,
		maxFailuresBeforeAlert: 3,
	}
}

// Start begins monitoring for network state changes
func (nsm *NetworkStateMonitor) Start() error {
	nsm.runningMutex.Lock()
	defer nsm.runningMutex.Unlock()

	if nsm.running {
		return fmt.Errorf("network state monitor already running")
	}

	nsm.logger.Info("Starting network state monitor...", "network-monitor")

	// Capture initial state
	initialState, err := nsm.captureCurrentState()
	if err != nil {
		nsm.logger.Warn(fmt.Sprintf("Failed to capture initial network state: %v", err), "network-monitor")
		// Continue anyway - we'll try again in the monitoring loop
		initialState = &NetworkState{DetectionTime: time.Now()}
	}

	nsm.stateMutex.Lock()
	nsm.currentState = initialState
	nsm.previousState = nil // No previous state on startup
	nsm.stateMutex.Unlock()

	nsm.logger.Info(fmt.Sprintf("Initial network state captured: LocalIP=%s, ExternalIP=%s, NATType=%s",
		initialState.LocalIP, initialState.ExternalIP, initialState.NATType.String()), "network-monitor")

	// Get monitoring interval from config (default: 5 minutes)
	intervalMinutes := nsm.config.GetConfigInt("network_monitor_interval_minutes", 5, 1, 60)
	interval := time.Duration(intervalMinutes) * time.Minute

	nsm.logger.Info(fmt.Sprintf("Network state monitoring interval: %v", interval), "network-monitor")

	// Start periodic monitoring
	nsm.monitorTicker = time.NewTicker(interval)
	nsm.running = true

	go nsm.monitorLoop()

	nsm.logger.Info("Network state monitor started successfully", "network-monitor")
	return nil
}

// Stop stops the network state monitor
func (nsm *NetworkStateMonitor) Stop() {
	nsm.runningMutex.Lock()
	defer nsm.runningMutex.Unlock()

	if !nsm.running {
		return
	}

	nsm.logger.Info("Stopping network state monitor...", "network-monitor")

	if nsm.monitorTicker != nil {
		nsm.monitorTicker.Stop()
	}

	nsm.cancel()
	nsm.running = false

	nsm.logger.Info("Network state monitor stopped", "network-monitor")
}

// monitorLoop is the main monitoring loop
func (nsm *NetworkStateMonitor) monitorLoop() {
	for {
		select {
		case <-nsm.ctx.Done():
			nsm.logger.Debug("Network state monitor loop stopped", "network-monitor")
			return

		case <-nsm.monitorTicker.C:
			nsm.logger.Debug("Network state check triggered", "network-monitor")
			nsm.checkForChanges()
		}
	}
}

// captureCurrentState captures the current network state
func (nsm *NetworkStateMonitor) captureCurrentState() (*NetworkState, error) {
	state := &NetworkState{
		DetectionTime: time.Now(),
	}

	// Get local IP
	localIP, err := nsm.nodeTypeManager.GetLocalIP()
	if err != nil {
		nsm.logger.Debug(fmt.Sprintf("Failed to get local IP: %v", err), "network-monitor")
		// Continue - we'll use empty string
	} else {
		state.LocalIP = localIP
	}

	// Get external IP
	externalIP, err := nsm.nodeTypeManager.GetExternalIP()
	if err != nil {
		nsm.logger.Debug(fmt.Sprintf("Failed to get external IP: %v", err), "network-monitor")
		// Continue - we'll use empty string
	} else {
		state.ExternalIP = externalIP
	}

	// Get NAT type from last detection result
	natResult := nsm.natDetector.GetLastResult()
	if natResult != nil {
		state.NATType = natResult.NATType
	} else {
		state.NATType = NATTypeUnknown
	}

	// Get local subnet from topology manager
	topology := nsm.topologyMgr.GetLocalTopology()
	if topology != nil {
		state.LocalSubnet = topology.LocalSubnet
	}

	return state, nil
}

// checkForChanges checks for network state changes and triggers appropriate actions
func (nsm *NetworkStateMonitor) checkForChanges() {
	// Capture current network state
	newState, err := nsm.captureCurrentState()
	if err != nil {
		nsm.consecutiveFailures++
		nsm.logger.Warn(fmt.Sprintf("Failed to capture network state (failure %d/%d): %v",
			nsm.consecutiveFailures, nsm.maxFailuresBeforeAlert, err), "network-monitor")

		if nsm.consecutiveFailures >= nsm.maxFailuresBeforeAlert {
			nsm.logger.Error(fmt.Sprintf("Network state monitoring failing consistently (%d consecutive failures)",
				nsm.consecutiveFailures), "network-monitor")
		}
		return
	}

	// Reset failure counter on success
	if nsm.consecutiveFailures > 0 {
		nsm.logger.Info("Network state monitoring recovered", "network-monitor")
		nsm.consecutiveFailures = 0
	}

	// Get previous state for comparison
	nsm.stateMutex.RLock()
	previousState := nsm.currentState
	nsm.stateMutex.RUnlock()

	// Update current state
	nsm.stateMutex.Lock()
	nsm.previousState = previousState
	nsm.currentState = newState
	nsm.stateMutex.Unlock()

	// Skip change detection on first run (no previous state to compare)
	if previousState == nil {
		nsm.logger.Debug("Skipping change detection (no previous state)", "network-monitor")
		return
	}

	// Detect changes
	localIPChanged := previousState.LocalIP != newState.LocalIP
	externalIPChanged := previousState.ExternalIP != newState.ExternalIP
	natTypeChanged := previousState.NATType != newState.NATType
	subnetChanged := previousState.LocalSubnet != newState.LocalSubnet

	// Check if any changes occurred
	if !localIPChanged && !externalIPChanged && !natTypeChanged && !subnetChanged {
		nsm.logger.Debug("No network state changes detected", "network-monitor")
		return
	}

	// Log detected changes
	nsm.logChanges(previousState, newState, localIPChanged, externalIPChanged, natTypeChanged, subnetChanged)

	// Handle changes based on what changed
	nsm.handleNetworkChanges(previousState, newState, localIPChanged, externalIPChanged, natTypeChanged, subnetChanged)
}

// logChanges logs the detected network changes
func (nsm *NetworkStateMonitor) logChanges(
	previousState, newState *NetworkState,
	localIPChanged, externalIPChanged, natTypeChanged, subnetChanged bool,
) {
	nsm.logger.Info("=== NETWORK STATE CHANGES DETECTED ===", "network-monitor")

	if localIPChanged {
		nsm.logger.Info(fmt.Sprintf("Local IP changed: %s -> %s",
			previousState.LocalIP, newState.LocalIP), "network-monitor")
	}

	if externalIPChanged {
		nsm.logger.Info(fmt.Sprintf("External IP changed: %s -> %s",
			previousState.ExternalIP, newState.ExternalIP), "network-monitor")
	}

	if natTypeChanged {
		nsm.logger.Info(fmt.Sprintf("NAT type changed: %s -> %s",
			previousState.NATType.String(), newState.NATType.String()), "network-monitor")
	}

	if subnetChanged {
		nsm.logger.Info(fmt.Sprintf("Local subnet changed: %s -> %s",
			previousState.LocalSubnet, newState.LocalSubnet), "network-monitor")
	}

	nsm.logger.Info("======================================", "network-monitor")
}

// handleNetworkChanges handles the detected network changes
func (nsm *NetworkStateMonitor) handleNetworkChanges(
	previousState, newState *NetworkState,
	localIPChanged, externalIPChanged, natTypeChanged, subnetChanged bool,
) {
	// Determine if we need to re-detect NAT type
	needsNATRedetection := externalIPChanged || localIPChanged

	// Determine if we need to re-detect topology
	needsTopologyRedetection := localIPChanged || subnetChanged || natTypeChanged

	// Re-detect NAT type if needed
	if needsNATRedetection {
		nsm.logger.Info("Re-detecting NAT type due to IP changes...", "network-monitor")
		natResult, err := nsm.natDetector.DetectNATType()
		if err != nil {
			nsm.logger.Error(fmt.Sprintf("NAT re-detection failed: %v", err), "network-monitor")
		} else {
			nsm.logger.Info(fmt.Sprintf("NAT re-detection complete: %s (Difficulty: %s, Requires Relay: %v)",
				natResult.NATType.String(),
				natResult.NATType.HolePunchingDifficulty(),
				natResult.NATType.RequiresRelay()), "network-monitor")

			// Update state with new NAT type
			nsm.stateMutex.Lock()
			nsm.currentState.NATType = natResult.NATType
			newState.NATType = natResult.NATType
			nsm.stateMutex.Unlock()

			// Check if NAT type changed as a result of re-detection
			if natResult.NATType != previousState.NATType {
				natTypeChanged = true
				nsm.logger.Info(fmt.Sprintf("NAT type changed after re-detection: %s -> %s",
					previousState.NATType.String(), natResult.NATType.String()), "network-monitor")
			}

			// Re-detect topology with new NAT result
			if needsTopologyRedetection {
				nsm.logger.Info("Re-detecting network topology...", "network-monitor")
				topology, err := nsm.topologyMgr.DetectLocalTopology(natResult)
				if err != nil {
					nsm.logger.Error(fmt.Sprintf("Topology re-detection failed: %v", err), "network-monitor")
				} else {
					nsm.logger.Info(fmt.Sprintf("Topology re-detection complete: Public IP=%s, Subnet=%s",
						topology.PublicIP, topology.LocalSubnet), "network-monitor")

					// Update state with new subnet
					nsm.stateMutex.Lock()
					nsm.currentState.LocalSubnet = topology.LocalSubnet
					newState.LocalSubnet = topology.LocalSubnet
					nsm.stateMutex.Unlock()
				}
			}
		}
	}

	// Handle changes based on node type
	nsm.handleMetadataUpdate(previousState, newState, localIPChanged, externalIPChanged, natTypeChanged)
}

// handleMetadataUpdate handles metadata updates based on node type and changes
func (nsm *NetworkStateMonitor) handleMetadataUpdate(
	previousState, newState *NetworkState,
	localIPChanged, externalIPChanged, natTypeChanged bool,
) {
	// Determine if this is a NAT node (has relay manager)
	isNATNode := nsm.relayManager != nil

	if isNATNode {
		nsm.handleNATNodeChanges(previousState, newState, localIPChanged, externalIPChanged, natTypeChanged)
	} else {
		nsm.handlePublicNodeChanges(previousState, newState, localIPChanged, externalIPChanged, natTypeChanged)
	}
}

// handlePublicNodeChanges handles network changes for public/relay nodes
func (nsm *NetworkStateMonitor) handlePublicNodeChanges(
	previousState, newState *NetworkState,
	localIPChanged, externalIPChanged, natTypeChanged bool,
) {
	nsm.logger.Info("Handling network changes for PUBLIC/RELAY node", "network-monitor")

	// Log the change details
	if localIPChanged {
		nsm.logger.Info(fmt.Sprintf("Local IP changed: %s -> %s", previousState.LocalIP, newState.LocalIP), "network-monitor")
	}
	if externalIPChanged {
		nsm.logger.Info(fmt.Sprintf("External IP changed: %s -> %s", previousState.ExternalIP, newState.ExternalIP), "network-monitor")
	}

	// For public nodes, just update metadata with new IP if either IP changed
	if localIPChanged || externalIPChanged {
		nsm.logger.Info("Updating metadata with new IP addresses for public node...", "network-monitor")

		newPublicIP := newState.ExternalIP
		if newPublicIP == "" {
			newPublicIP = newState.LocalIP // Fallback to local IP if external IP unavailable
		}

		newPrivateIP := newState.LocalIP

		// Update metadata via MetadataPublisher
		err := nsm.metadataPublisher.NotifyIPChange(newPublicIP, newPrivateIP)
		if err != nil {
			nsm.logger.Error(fmt.Sprintf("Failed to update metadata with new IPs: %v", err), "network-monitor")
		} else {
			nsm.logger.Info("Successfully updated metadata with new IP addresses", "network-monitor")
		}
	}

	// If NAT type changed from None to something else, log warning
	if natTypeChanged && previousState.NATType == NATTypeNone && newState.NATType != NATTypeNone {
		nsm.logger.Warn(fmt.Sprintf("Public node now appears to be behind NAT (%s) - this may require reconfiguration",
			newState.NATType.String()), "network-monitor")
	}
}

// handleNATNodeChanges handles network changes for NAT nodes
func (nsm *NetworkStateMonitor) handleNATNodeChanges(
	previousState, newState *NetworkState,
	localIPChanged, externalIPChanged, natTypeChanged bool,
) {
	nsm.logger.Info("Handling network changes for NAT node", "network-monitor")

	// For NAT nodes, IP or NAT type changes require relay reconnection
	if localIPChanged || externalIPChanged || natTypeChanged {
		nsm.logger.Info("NAT node network configuration changed - initiating relay reconnection...", "network-monitor")

		// Step 1: Disconnect from current relay
		nsm.logger.Info("Step 1: Disconnecting from current relay...", "network-monitor")
		if nsm.relayManager != nil {
			nsm.relayManager.DisconnectCurrentRelay()
			nsm.logger.Info("Disconnected from current relay", "network-monitor")
		}

		// Step 2: Wait briefly for cleanup
		time.Sleep(2 * time.Second)

		// Step 3: Trigger new relay selection and connection
		nsm.logger.Info("Step 2: Initiating new relay selection and connection...", "network-monitor")
		if nsm.relayManager != nil {
			// The relay manager will handle:
			// 1. Selecting a new relay based on current network conditions
			// 2. Establishing connection to the new relay
			// 3. Calling metadataPublisher.NotifyRelayConnected() which will:
			//    - Update metadata with new relay information
			//    - Update IP addresses in metadata
			//    - Publish complete metadata to DHT with incremented sequence
			err := nsm.relayManager.ReconnectRelay()
			if err != nil {
				nsm.logger.Error(fmt.Sprintf("Failed to reconnect to relay: %v", err), "network-monitor")
				nsm.logger.Info("Will retry on next monitoring cycle", "network-monitor")
			} else {
				nsm.logger.Info("Successfully reconnected to relay and published updated metadata", "network-monitor")
			}
		}
	}
}

// GetCurrentState returns the current network state (for debugging/status)
func (nsm *NetworkStateMonitor) GetCurrentState() *NetworkState {
	nsm.stateMutex.RLock()
	defer nsm.stateMutex.RUnlock()

	if nsm.currentState == nil {
		return nil
	}

	// Return a copy to prevent external modification
	stateCopy := *nsm.currentState
	return &stateCopy
}

// GetPreviousState returns the previous network state (for debugging/status)
func (nsm *NetworkStateMonitor) GetPreviousState() *NetworkState {
	nsm.stateMutex.RLock()
	defer nsm.stateMutex.RUnlock()

	if nsm.previousState == nil {
		return nil
	}

	// Return a copy to prevent external modification
	stateCopy := *nsm.previousState
	return &stateCopy
}

// GetStats returns statistics about the network state monitor
func (nsm *NetworkStateMonitor) GetStats() map[string]interface{} {
	nsm.stateMutex.RLock()
	defer nsm.stateMutex.RUnlock()

	stats := make(map[string]interface{})

	nsm.runningMutex.Lock()
	stats["running"] = nsm.running
	nsm.runningMutex.Unlock()

	stats["consecutive_failures"] = nsm.consecutiveFailures

	if nsm.currentState != nil {
		stats["current_state"] = map[string]interface{}{
			"local_ip":       nsm.currentState.LocalIP,
			"external_ip":    nsm.currentState.ExternalIP,
			"nat_type":       nsm.currentState.NATType.String(),
			"local_subnet":   nsm.currentState.LocalSubnet,
			"detection_time": nsm.currentState.DetectionTime,
		}
	}

	if nsm.previousState != nil {
		stats["previous_state"] = map[string]interface{}{
			"local_ip":       nsm.previousState.LocalIP,
			"external_ip":    nsm.previousState.ExternalIP,
			"nat_type":       nsm.previousState.NATType.String(),
			"local_subnet":   nsm.previousState.LocalSubnet,
			"detection_time": nsm.previousState.DetectionTime,
		}
	}

	return stats
}

// ForceCheck forces an immediate network state check (useful for testing or manual triggers)
func (nsm *NetworkStateMonitor) ForceCheck() {
	nsm.logger.Info("Forcing immediate network state check...", "network-monitor")
	nsm.checkForChanges()
}
