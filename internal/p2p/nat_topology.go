package p2p

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// NetworkTopology represents network topology information for NAT traversal
type NetworkTopology struct {
	LocalSubnet      string   // Local subnet CIDR (e.g., "192.168.1.0/24")
	PublicIP         string   // Public IP address
	PeersOnSameNAT   []string // List of peer IDs behind the same NAT
	DetectionTime    time.Time
}

// NATTopologyManager manages network topology detection
type NATTopologyManager struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	dbManager       *database.SQLiteManager
	localTopology   *NetworkTopology
	peerTopologies  map[string]*NetworkTopology // key: peerID
	topologyMutex   sync.RWMutex
}

// NewNATTopologyManager creates a new topology manager
func NewNATTopologyManager(config *utils.ConfigManager, logger *utils.LogsManager, dbManager *database.SQLiteManager) *NATTopologyManager {
	return &NATTopologyManager{
		config:         config,
		logger:         logger,
		dbManager:      dbManager,
		peerTopologies: make(map[string]*NetworkTopology),
	}
}

// DetectLocalTopology detects the local network topology
func (ntm *NATTopologyManager) DetectLocalTopology(natResult *NATDetectionResult) (*NetworkTopology, error) {
	ntm.logger.Info("Detecting local network topology...", "nat-topology")

	topology := &NetworkTopology{
		DetectionTime: time.Now(),
		PeersOnSameNAT: make([]string, 0),
	}

	// Get local subnet
	localSubnet, err := ntm.getLocalSubnet(natResult.PrivateIP)
	if err != nil {
		ntm.logger.Warn(fmt.Sprintf("Failed to detect local subnet: %v", err), "nat-topology")
	} else {
		topology.LocalSubnet = localSubnet
		ntm.logger.Debug(fmt.Sprintf("Local subnet: %s", localSubnet), "nat-topology")
	}

	// Store public IP from NAT detection
	topology.PublicIP = natResult.PublicIP

	// Store local topology
	ntm.topologyMutex.Lock()
	ntm.localTopology = topology
	ntm.topologyMutex.Unlock()

	ntm.logger.Info(fmt.Sprintf("Local topology detected: Public IP=%s, Subnet=%s",
		topology.PublicIP, topology.LocalSubnet), "nat-topology")

	return topology, nil
}

// getLocalSubnet determines the local subnet from a local IP address
func (ntm *NATTopologyManager) getLocalSubnet(localIP string) (string, error) {
	ip := net.ParseIP(localIP)
	if ip == nil {
		return "", fmt.Errorf("invalid IP address: %s", localIP)
	}

	// Get network interfaces
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	// Find the interface with matching IP
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			// Check if this is the matching IP
			if ipNet.IP.Equal(ip) {
				// Return the network CIDR
				return ipNet.String(), nil
			}
		}
	}

	// Fallback: infer subnet from IP class
	return ntm.inferSubnetFromIP(localIP), nil
}

// inferSubnetFromIP infers a subnet based on private IP ranges
func (ntm *NATTopologyManager) inferSubnetFromIP(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}

	// Check common private IP ranges and infer typical subnet
	if ip[0] == 10 {
		// Class A private (10.0.0.0/8) - assume /24 subnet
		return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
	} else if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		// Class B private (172.16.0.0/12) - assume /24 subnet
		return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
	} else if ip[0] == 192 && ip[1] == 168 {
		// Class C private (192.168.0.0/16) - assume /24 subnet
		return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
	}

	return ""
}

// ComparePeerTopology compares a peer's topology with local topology
func (ntm *NATTopologyManager) ComparePeerTopology(peerID string, peerMetadata *database.PeerMetadata) bool {
	ntm.topologyMutex.RLock()
	localTopology := ntm.localTopology
	ntm.topologyMutex.RUnlock()

	if localTopology == nil {
		return false
	}

	// Check if peer has same public IP
	if peerMetadata.NetworkInfo.PublicIP == localTopology.PublicIP {
		// Same public IP - likely behind same NAT
		ntm.logger.Info(fmt.Sprintf("Peer %s appears to be behind same NAT (same public IP: %s)",
			peerID, localTopology.PublicIP), "nat-topology")

		// Store peer topology
		peerTopology := &NetworkTopology{
			LocalSubnet:    ntm.inferSubnetFromIP(peerMetadata.NetworkInfo.PrivateIP),
			PublicIP:       peerMetadata.NetworkInfo.PublicIP,
			PeersOnSameNAT: []string{},
			DetectionTime:  time.Now(),
		}

		ntm.topologyMutex.Lock()
		ntm.peerTopologies[peerID] = peerTopology
		ntm.localTopology.PeersOnSameNAT = append(ntm.localTopology.PeersOnSameNAT, peerID)
		ntm.topologyMutex.Unlock()

		return true
	}

	return false
}

// IsPeerOnSameNAT checks if a peer is behind the same NAT
func (ntm *NATTopologyManager) IsPeerOnSameNAT(peerID string) bool {
	ntm.topologyMutex.RLock()
	defer ntm.topologyMutex.RUnlock()

	if ntm.localTopology == nil {
		return false
	}

	for _, id := range ntm.localTopology.PeersOnSameNAT {
		if id == peerID {
			return true
		}
	}

	return false
}

// IsPeerOnSameSubnet checks if a peer is on the same local subnet
func (ntm *NATTopologyManager) IsPeerOnSameSubnet(peerMetadata *database.PeerMetadata) bool {
	ntm.topologyMutex.RLock()
	localTopology := ntm.localTopology
	ntm.topologyMutex.RUnlock()

	if localTopology == nil || localTopology.LocalSubnet == "" {
		return false
	}

	// Parse local subnet
	_, localNet, err := net.ParseCIDR(localTopology.LocalSubnet)
	if err != nil {
		return false
	}

	// Parse peer private IP
	peerIP := net.ParseIP(peerMetadata.NetworkInfo.PrivateIP)
	if peerIP == nil {
		return false
	}

	// Check if peer IP is in local subnet
	return localNet.Contains(peerIP)
}

// GetLocalTopology returns the local network topology
func (ntm *NATTopologyManager) GetLocalTopology() *NetworkTopology {
	ntm.topologyMutex.RLock()
	defer ntm.topologyMutex.RUnlock()
	return ntm.localTopology
}

// GetPeersOnSameNAT returns list of peers behind the same NAT
func (ntm *NATTopologyManager) GetPeersOnSameNAT() []string {
	ntm.topologyMutex.RLock()
	defer ntm.topologyMutex.RUnlock()

	if ntm.localTopology == nil {
		return []string{}
	}

	// Return a copy to prevent external modification
	peers := make([]string, len(ntm.localTopology.PeersOnSameNAT))
	copy(peers, ntm.localTopology.PeersOnSameNAT)
	return peers
}

// GetTopologyStats returns statistics about network topology
func (ntm *NATTopologyManager) GetTopologyStats() map[string]interface{} {
	ntm.topologyMutex.RLock()
	defer ntm.topologyMutex.RUnlock()

	stats := make(map[string]interface{})

	if ntm.localTopology != nil {
		stats["local_subnet"] = ntm.localTopology.LocalSubnet
		stats["public_ip"] = ntm.localTopology.PublicIP
		stats["peers_same_nat"] = len(ntm.localTopology.PeersOnSameNAT)
		stats["detection_time"] = ntm.localTopology.DetectionTime
	}

	stats["known_peer_topologies"] = len(ntm.peerTopologies)

	return stats
}

// ConnectionStrategy represents the connection strategy to use
type ConnectionStrategy int

const (
	StrategyUnknown      ConnectionStrategy = iota
	StrategyDirectLocal  // Direct connection on same LAN
	StrategyDirectPublic // Direct connection via public IPs
	StrategyHolePunch    // UDP hole punching
	StrategyRelay        // Relay through public node
)

// String returns the string representation of connection strategy
func (cs ConnectionStrategy) String() string {
	switch cs {
	case StrategyDirectLocal:
		return "Direct (Local)"
	case StrategyDirectPublic:
		return "Direct (Public)"
	case StrategyHolePunch:
		return "Hole Punching"
	case StrategyRelay:
		return "Relay"
	default:
		return "Unknown"
	}
}

// IsFree returns true if this connection strategy is free (no relay costs)
func (cs ConnectionStrategy) IsFree() bool {
	return cs != StrategyRelay
}
