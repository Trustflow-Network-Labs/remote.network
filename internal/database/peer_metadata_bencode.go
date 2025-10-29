package database

import (
	"fmt"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// BencodePeerMetadata is a bencode-safe version of PeerMetadata
// All time.Time fields are converted to Unix timestamps (int64)
// This ensures proper BEP_44 signature validation
type BencodePeerMetadata struct {
	// Core Identity
	PeerID    string `bencode:"peer_id"`    // Persistent Ed25519-based peer ID
	NodeID    string `bencode:"node_id"`    // DHT routing node ID
	Topic     string `bencode:"topic"`
	Version   int    `bencode:"version"`
	Timestamp int64  `bencode:"timestamp"` // Unix timestamp

	// Network Connectivity
	NetworkInfo BencodeNetworkInfo `bencode:"network_info"`

	// Capabilities & Service Statistics
	Capabilities []string                 `bencode:"capabilities"`
	FilesCount   int                      `bencode:"files_count"` // Count of ACTIVE DATA services
	AppsCount    int                      `bencode:"apps_count"`  // Count of ACTIVE DOCKER + STANDALONE services
	Extensions   map[string]interface{}   `bencode:"extensions,omitempty"`
}

// BencodeNetworkInfo is a bencode-safe version of NetworkInfo
type BencodeNetworkInfo struct {
	// Public connectivity
	PublicIP   string `bencode:"public_ip,omitempty"`
	PublicPort int    `bencode:"public_port,omitempty"`

	// Private connectivity (NAT)
	PrivateIP   string `bencode:"private_ip,omitempty"`
	PrivatePort int    `bencode:"private_port,omitempty"`

	// Node characteristics
	NodeType       string              `bencode:"node_type"`
	NATType        string              `bencode:"nat_type,omitempty"`
	SupportsUPnP   bool                `bencode:"supports_upnp,omitempty"`
	RelayEndpoints []string            `bencode:"relay_endpoints,omitempty"`

	// Relay service info
	IsRelay         bool   `bencode:"is_relay,omitempty"`
	RelayEndpoint   string `bencode:"relay_endpoint,omitempty"`
	RelayPricing    int    `bencode:"relay_pricing,omitempty"`
	RelayCapacity   int    `bencode:"relay_capacity,omitempty"`
	ReputationScore int    `bencode:"reputation_score,omitempty"`

	// NAT peer relay connection info
	UsingRelay     bool   `bencode:"using_relay,omitempty"`
	ConnectedRelay string `bencode:"connected_relay,omitempty"`
	RelaySessionID string `bencode:"relay_session_id,omitempty"`
	RelayAddress   string `bencode:"relay_address,omitempty"`

	// Protocol support
	Protocols []BencodeProtocol `bencode:"protocols"`
}

// BencodeProtocol is a bencode-safe version of Protocol
type BencodeProtocol struct {
	Name string                 `bencode:"name"`
	Port int                    `bencode:"port"`
	Meta map[string]interface{} `bencode:"meta,omitempty"`
}

// ToBencodeSafe converts PeerMetadata to a bencode-safe format
// This is used before publishing to DHT to ensure proper signature validation
func (pm *PeerMetadata) ToBencodeSafe() *BencodePeerMetadata {
	// Convert protocols
	bencProtocols := make([]BencodeProtocol, len(pm.NetworkInfo.Protocols))
	for i, p := range pm.NetworkInfo.Protocols {
		bencProtocols[i] = BencodeProtocol{
			Name: p.Name,
			Port: p.Port,
			Meta: p.Meta,
		}
	}

	return &BencodePeerMetadata{
		PeerID:       pm.PeerID,
		NodeID:       pm.NodeID,
		Topic:        pm.Topic,
		Version:      pm.Version,
		Timestamp:    pm.Timestamp.Unix(), // Convert to Unix timestamp
		Capabilities: pm.Capabilities,
		FilesCount:   pm.FilesCount,
		AppsCount:    pm.AppsCount,
		Extensions:   pm.Extensions,
		NetworkInfo: BencodeNetworkInfo{
			PublicIP:        pm.NetworkInfo.PublicIP,
			PublicPort:      pm.NetworkInfo.PublicPort,
			PrivateIP:       pm.NetworkInfo.PrivateIP,
			PrivatePort:     pm.NetworkInfo.PrivatePort,
			NodeType:        pm.NetworkInfo.NodeType,
			NATType:         pm.NetworkInfo.NATType,
			SupportsUPnP:    pm.NetworkInfo.SupportsUPnP,
			RelayEndpoints:  pm.NetworkInfo.RelayEndpoints,
			IsRelay:         pm.NetworkInfo.IsRelay,
			RelayEndpoint:   pm.NetworkInfo.RelayEndpoint,
			RelayPricing:    pm.NetworkInfo.RelayPricing,
			RelayCapacity:   pm.NetworkInfo.RelayCapacity,
			ReputationScore: pm.NetworkInfo.ReputationScore,
			UsingRelay:      pm.NetworkInfo.UsingRelay,
			ConnectedRelay:  pm.NetworkInfo.ConnectedRelay,
			RelaySessionID:  pm.NetworkInfo.RelaySessionID,
			RelayAddress:    pm.NetworkInfo.RelayAddress,
			Protocols:       bencProtocols,
		},
	}
}

// FromBencodeSafe converts a bencode-safe format back to PeerMetadata
// This is used after retrieving from DHT to reconstruct the original structure
func FromBencodeSafe(bpm *BencodePeerMetadata) *PeerMetadata {
	// Convert protocols back
	protocols := make([]Protocol, len(bpm.NetworkInfo.Protocols))
	for i, p := range bpm.NetworkInfo.Protocols {
		protocols[i] = Protocol{
			Name: p.Name,
			Port: p.Port,
			Meta: p.Meta,
		}
	}

	return &PeerMetadata{
		PeerID:       bpm.PeerID,
		NodeID:       bpm.NodeID,
		Topic:        bpm.Topic,
		Version:      bpm.Version,
		Timestamp:    UnixToTime(bpm.Timestamp), // Convert Unix timestamp back to time.Time
		Capabilities: bpm.Capabilities,
		FilesCount:   bpm.FilesCount,
		AppsCount:    bpm.AppsCount,
		Extensions:   bpm.Extensions,
		NetworkInfo: NetworkInfo{
			PublicIP:        bpm.NetworkInfo.PublicIP,
			PublicPort:      bpm.NetworkInfo.PublicPort,
			PrivateIP:       bpm.NetworkInfo.PrivateIP,
			PrivatePort:     bpm.NetworkInfo.PrivatePort,
			NodeType:        bpm.NetworkInfo.NodeType,
			NATType:         bpm.NetworkInfo.NATType,
			SupportsUPnP:    bpm.NetworkInfo.SupportsUPnP,
			RelayEndpoints:  bpm.NetworkInfo.RelayEndpoints,
			IsRelay:         bpm.NetworkInfo.IsRelay,
			RelayEndpoint:   bpm.NetworkInfo.RelayEndpoint,
			RelayPricing:    bpm.NetworkInfo.RelayPricing,
			RelayCapacity:   bpm.NetworkInfo.RelayCapacity,
			ReputationScore: bpm.NetworkInfo.ReputationScore,
			UsingRelay:      bpm.NetworkInfo.UsingRelay,
			ConnectedRelay:  bpm.NetworkInfo.ConnectedRelay,
			RelaySessionID:  bpm.NetworkInfo.RelaySessionID,
			RelayAddress:    bpm.NetworkInfo.RelayAddress,
			Protocols:       protocols,
		},
	}
}

// UnixToTime converts Unix timestamp to time.Time
func UnixToTime(unix int64) time.Time {
	return time.Unix(unix, 0)
}

// DecodeBencodedMetadata decodes bencoded bytes into PeerMetadata
// This is used when retrieving metadata from DHT
func DecodeBencodedMetadata(data []byte) (*PeerMetadata, error) {
	var bencMeta BencodePeerMetadata

	// Use bencode to unmarshal
	err := bencode.Unmarshal(data, &bencMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to decode bencode data: %v", err)
	}

	// Convert to PeerMetadata
	return FromBencodeSafe(&bencMeta), nil
}
