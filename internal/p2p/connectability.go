package p2p

import (
	"fmt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ConnectionMethod indicates how to connect to a peer
type ConnectionMethod int

const (
	ConnectionMethodNone ConnectionMethod = iota // Not connectable
	ConnectionMethodDirect                       // Direct connection (public peer)
	ConnectionMethodRelay                        // Via relay (NAT peer with relay)
)

// ConnectabilityFilter determines if and how a peer can be reached
type ConnectabilityFilter struct {
	logger *utils.LogsManager
}

// NewConnectabilityFilter creates a new connectability filter
func NewConnectabilityFilter(logger *utils.LogsManager) *ConnectabilityFilter {
	return &ConnectabilityFilter{
		logger: logger,
	}
}

// IsConnectable checks if a peer is connectable based on its metadata
// Returns true if the peer can be reached (directly or via relay)
func (cf *ConnectabilityFilter) IsConnectable(metadata *database.PeerMetadata) bool {
	return cf.GetConnectionMethod(metadata) != ConnectionMethodNone
}

// GetConnectionMethod determines how to connect to a peer
func (cf *ConnectabilityFilter) GetConnectionMethod(metadata *database.PeerMetadata) ConnectionMethod {
	if metadata == nil {
		return ConnectionMethodNone
	}

	// Case 1: Public peer - always directly connectable
	if metadata.NetworkInfo.NodeType == "public" && metadata.NetworkInfo.PublicIP != "" {
		cf.logger.Debug(fmt.Sprintf("Peer is public and connectable directly (IP: %s)",
			metadata.NetworkInfo.PublicIP), "connectability")
		return ConnectionMethodDirect
	}

	// Case 2: Relay node - always directly connectable
	if metadata.NetworkInfo.IsRelay && metadata.NetworkInfo.PublicIP != "" {
		cf.logger.Debug("Peer is a relay node, connectable directly", "connectability")
		return ConnectionMethodDirect
	}

	// Case 3: NAT peer - only connectable if it has relay info
	if metadata.NetworkInfo.NodeType == "private" || metadata.NetworkInfo.UsingRelay {
		// Check if relay info is complete
		if !metadata.NetworkInfo.UsingRelay {
			cf.logger.Debug("NAT peer without relay info - not connectable", "connectability")
			return ConnectionMethodNone
		}

		// Validate relay information is complete
		if metadata.NetworkInfo.ConnectedRelay == "" {
			cf.logger.Debug("NAT peer with incomplete relay info (no relay_node_id) - not connectable", "connectability")
			return ConnectionMethodNone
		}

		if metadata.NetworkInfo.RelaySessionID == "" {
			cf.logger.Debug("NAT peer with incomplete relay info (no session_id) - not connectable", "connectability")
			return ConnectionMethodNone
		}

		if metadata.NetworkInfo.RelayAddress == "" {
			cf.logger.Debug("NAT peer with incomplete relay info (no relay_address) - not connectable", "connectability")
			return ConnectionMethodNone
		}

		cf.logger.Debug(fmt.Sprintf("NAT peer connectable via relay (relay: %s, session: %s)",
			metadata.NetworkInfo.ConnectedRelay[:8], metadata.NetworkInfo.RelaySessionID), "connectability")
		return ConnectionMethodRelay
	}

	// Case 4: Unknown/unsupported configuration
	cf.logger.Debug(fmt.Sprintf("Peer has unsupported configuration (node_type: %s, using_relay: %t) - not connectable",
		metadata.NetworkInfo.NodeType, metadata.NetworkInfo.UsingRelay), "connectability")
	return ConnectionMethodNone
}

// FilterConnectablePeers filters a list of peers to only connectable ones
// Returns a map of peer_id -> connection method
func (cf *ConnectabilityFilter) FilterConnectablePeers(metadataMap map[string]*database.PeerMetadata) map[string]ConnectionMethod {
	connectable := make(map[string]ConnectionMethod)

	for peerID, metadata := range metadataMap {
		method := cf.GetConnectionMethod(metadata)
		if method != ConnectionMethodNone {
			connectable[peerID] = method
		} else {
			cf.logger.Debug(fmt.Sprintf("Filtered out peer %s: not connectable", peerID[:8]), "connectability")
		}
	}

	cf.logger.Info(fmt.Sprintf("Filtered %d/%d peers as connectable", len(connectable), len(metadataMap)), "connectability")

	return connectable
}

// GetConnectabilityReason returns a human-readable reason for connectability status
func (cf *ConnectabilityFilter) GetConnectabilityReason(metadata *database.PeerMetadata) string {
	if metadata == nil {
		return "metadata is nil"
	}

	method := cf.GetConnectionMethod(metadata)

	switch method {
	case ConnectionMethodDirect:
		if metadata.NetworkInfo.IsRelay {
			return "relay node with public IP"
		}
		return fmt.Sprintf("public peer (IP: %s)", metadata.NetworkInfo.PublicIP)

	case ConnectionMethodRelay:
		return fmt.Sprintf("NAT peer with relay (relay: %s, session: %s)",
			metadata.NetworkInfo.ConnectedRelay[:8], metadata.NetworkInfo.RelaySessionID)

	case ConnectionMethodNone:
		if metadata.NetworkInfo.NodeType == "private" && !metadata.NetworkInfo.UsingRelay {
			return "NAT peer without relay info"
		}

		if metadata.NetworkInfo.UsingRelay {
			if metadata.NetworkInfo.ConnectedRelay == "" {
				return "NAT peer with incomplete relay info (missing relay_node_id)"
			}
			if metadata.NetworkInfo.RelaySessionID == "" {
				return "NAT peer with incomplete relay info (missing session_id)"
			}
			if metadata.NetworkInfo.RelayAddress == "" {
				return "NAT peer with incomplete relay info (missing relay_address)"
			}
		}

		if metadata.NetworkInfo.NodeType == "public" && metadata.NetworkInfo.PublicIP == "" {
			return "public peer without IP address"
		}

		return "unknown configuration"

	default:
		return "unknown"
	}
}

// ValidateMetadata performs basic validation on peer metadata
func (cf *ConnectabilityFilter) ValidateMetadata(metadata *database.PeerMetadata) error {
	if metadata == nil {
		return fmt.Errorf("metadata is nil")
	}

	// Check node type
	if metadata.NetworkInfo.NodeType != "public" && metadata.NetworkInfo.NodeType != "private" {
		return fmt.Errorf("invalid node_type: %s (expected 'public' or 'private')", metadata.NetworkInfo.NodeType)
	}

	// Public peers must have public IP
	if metadata.NetworkInfo.NodeType == "public" && metadata.NetworkInfo.PublicIP == "" {
		return fmt.Errorf("public peer missing public_ip")
	}

	// Relay nodes must be public
	if metadata.NetworkInfo.IsRelay && metadata.NetworkInfo.NodeType != "public" {
		return fmt.Errorf("relay node must be public (got: %s)", metadata.NetworkInfo.NodeType)
	}

	// NAT peers using relay must have complete relay info
	if metadata.NetworkInfo.UsingRelay {
		if metadata.NetworkInfo.ConnectedRelay == "" {
			return fmt.Errorf("NAT peer using_relay but missing connected_relay")
		}
		if metadata.NetworkInfo.RelaySessionID == "" {
			return fmt.Errorf("NAT peer using_relay but missing relay_session_id")
		}
		if metadata.NetworkInfo.RelayAddress == "" {
			return fmt.Errorf("NAT peer using_relay but missing relay_address")
		}
	}

	return nil
}

// ConnectionMethodString returns a string representation of the connection method
func ConnectionMethodString(method ConnectionMethod) string {
	switch method {
	case ConnectionMethodNone:
		return "none"
	case ConnectionMethodDirect:
		return "direct"
	case ConnectionMethodRelay:
		return "relay"
	default:
		return "unknown"
	}
}
