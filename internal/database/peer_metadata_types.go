package database

import (
	"time"
)

// PeerMetadata represents comprehensive peer information stored in DHT
// This is no longer persisted in local database - only used for DHT storage/retrieval
type PeerMetadata struct {
	// Core Identity
	PeerID    string    `json:"peer_id"`    // Persistent Ed25519-based peer ID (SHA1 of public key)
	NodeID    string    `json:"node_id"`    // DHT routing node ID (may change on restart)
	Topic     string    `json:"topic"`
	Version   int       `json:"version"`
	Timestamp time.Time `json:"timestamp"`

	// Network Connectivity
	NetworkInfo NetworkInfo `json:"network_info"`

	// Capabilities & Services
	Capabilities []string               `json:"capabilities"`
	Services     map[string]Service     `json:"services"`
	Extensions   map[string]interface{} `json:"extensions"`

	// Metadata (not stored in DHT, used for local tracking)
	LastSeen time.Time `json:"last_seen,omitempty"`
	Source   string    `json:"source,omitempty"` // "dht", "quic", "manual"
}

// NetworkInfo contains network connectivity details
type NetworkInfo struct {
	// Public connectivity
	PublicIP   string `json:"public_ip,omitempty"`
	PublicPort int    `json:"public_port,omitempty"`

	// Private connectivity (NAT)
	PrivateIP   string `json:"private_ip,omitempty"`
	PrivatePort int    `json:"private_port,omitempty"`

	// Node characteristics
	NodeType       string   `json:"node_type"`               // "public" or "private"
	NATType        string   `json:"nat_type,omitempty"`      // "full_cone", "restricted", "symmetric", etc.
	SupportsUPnP   bool     `json:"supports_upnp,omitempty"`
	RelayEndpoints []string `json:"relay_endpoints,omitempty"`

	// Relay service info (if node offers relay services)
	IsRelay         bool   `json:"is_relay,omitempty"`
	RelayEndpoint   string `json:"relay_endpoint,omitempty"`   // "IP:Port" for relay connections
	RelayPricing    int    `json:"relay_pricing,omitempty"`    // Price per GB (scaled: value * 1000000)
	RelayCapacity   int    `json:"relay_capacity,omitempty"`   // Max concurrent connections
	ReputationScore int    `json:"reputation_score,omitempty"` // Relay reputation (scaled: 0-10000)

	// NAT peer relay connection info (if using relay)
	UsingRelay     bool   `json:"using_relay,omitempty"`
	ConnectedRelay string `json:"connected_relay,omitempty"`  // Relay NodeID
	RelaySessionID string `json:"relay_session_id,omitempty"` // Session ID for routing
	RelayAddress   string `json:"relay_address,omitempty"`    // How to reach this peer via relay

	// Protocol support
	Protocols []Protocol `json:"protocols"`
}

// Protocol describes a supported network protocol
type Protocol struct {
	Name string                 `json:"name"` // "quic", "tcp", "udp"
	Port int                    `json:"port"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// Service describes capabilities or services offered by the peer
type Service struct {
	Type         string                 `json:"type"`         // "storage", "gpu", "docker", "wasm"
	Endpoint     string                 `json:"endpoint"`
	Capabilities map[string]interface{} `json:"capabilities"`
	Status       string                 `json:"status"`       // "available", "busy", "offline"
}
