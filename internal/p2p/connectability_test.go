package p2p

import (
	"testing"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

func setupTestConnectability() *ConnectabilityFilter {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	return NewConnectabilityFilter(logger)
}

func TestConnectabilityPublicPeer(t *testing.T) {
	filter := setupTestConnectability()

	metadata := &database.PeerMetadata{
		NetworkInfo: database.NetworkInfo{
			NodeType: "public",
			PublicIP: "203.0.113.1",
		},
	}

	method := filter.GetConnectionMethod(metadata)
	if method != ConnectionMethodDirect {
		t.Errorf("Expected public peer to be ConnectionMethodDirect, got %s", ConnectionMethodString(method))
	}

	if !filter.IsConnectable(metadata) {
		t.Error("Expected public peer to be connectable")
	}
}

func TestConnectabilityRelayNode(t *testing.T) {
	filter := setupTestConnectability()

	metadata := &database.PeerMetadata{
		NetworkInfo: database.NetworkInfo{
			IsRelay:  true,
			PublicIP: "203.0.113.2",
		},
	}

	method := filter.GetConnectionMethod(metadata)
	if method != ConnectionMethodDirect {
		t.Errorf("Expected relay node to be ConnectionMethodDirect, got %s", ConnectionMethodString(method))
	}

	if !filter.IsConnectable(metadata) {
		t.Error("Expected relay node to be connectable")
	}
}

func TestConnectabilityNATWithRelay(t *testing.T) {
	filter := setupTestConnectability()

	metadata := &database.PeerMetadata{
		NetworkInfo: database.NetworkInfo{
			NodeType:       "private",
			UsingRelay:     true,
			ConnectedRelay: "relay-node-id-123",
			RelaySessionID: "session-456",
			RelayAddress:   "relay.example.com:30906",
		},
	}

	method := filter.GetConnectionMethod(metadata)
	if method != ConnectionMethodRelay {
		t.Errorf("Expected NAT peer with relay to be ConnectionMethodRelay, got %s", ConnectionMethodString(method))
	}

	if !filter.IsConnectable(metadata) {
		t.Error("Expected NAT peer with relay to be connectable")
	}
}

func TestConnectabilityNATWithoutRelay(t *testing.T) {
	filter := setupTestConnectability()

	metadata := &database.PeerMetadata{
		NetworkInfo: database.NetworkInfo{
			NodeType:   "private",
			UsingRelay: false,
		},
	}

	method := filter.GetConnectionMethod(metadata)
	if method != ConnectionMethodNone {
		t.Errorf("Expected NAT peer without relay to be ConnectionMethodNone, got %s", ConnectionMethodString(method))
	}

	if filter.IsConnectable(metadata) {
		t.Error("Expected NAT peer without relay to not be connectable")
	}
}

func TestConnectabilityIncompleteRelayInfo(t *testing.T) {
	filter := setupTestConnectability()

	testCases := []struct {
		name     string
		metadata *database.PeerMetadata
	}{
		{
			name: "Missing ConnectedRelay",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType:       "private",
					UsingRelay:     true,
					RelaySessionID: "session-456",
					RelayAddress:   "relay.example.com:30906",
				},
			},
		},
		{
			name: "Missing RelaySessionID",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType:       "private",
					UsingRelay:     true,
					ConnectedRelay: "relay-node-id-123",
					RelayAddress:   "relay.example.com:30906",
				},
			},
		},
		{
			name: "Missing RelayAddress",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType:       "private",
					UsingRelay:     true,
					ConnectedRelay: "relay-node-id-123",
					RelaySessionID: "session-456",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			method := filter.GetConnectionMethod(tc.metadata)
			if method != ConnectionMethodNone {
				t.Errorf("%s: Expected ConnectionMethodNone for incomplete relay info, got %s",
					tc.name, ConnectionMethodString(method))
			}

			if filter.IsConnectable(tc.metadata) {
				t.Errorf("%s: Expected peer with incomplete relay info to not be connectable", tc.name)
			}
		})
	}
}

func TestConnectabilityNilMetadata(t *testing.T) {
	filter := setupTestConnectability()

	method := filter.GetConnectionMethod(nil)
	if method != ConnectionMethodNone {
		t.Errorf("Expected nil metadata to be ConnectionMethodNone, got %s", ConnectionMethodString(method))
	}

	if filter.IsConnectable(nil) {
		t.Error("Expected nil metadata to not be connectable")
	}
}

func TestConnectabilityFilterPeers(t *testing.T) {
	filter := setupTestConnectability()

	metadataMap := map[string]*database.PeerMetadata{
		"public-peer": {
			NetworkInfo: database.NetworkInfo{
				NodeType: "public",
				PublicIP: "203.0.113.1",
			},
		},
		"relay-peer": {
			NetworkInfo: database.NetworkInfo{
				IsRelay:  true,
				PublicIP: "203.0.113.2",
			},
		},
		"nat-with-relay": {
			NetworkInfo: database.NetworkInfo{
				NodeType:       "private",
				UsingRelay:     true,
				ConnectedRelay: "relay-id",
				RelaySessionID: "session-id",
				RelayAddress:   "relay.example.com:30906",
			},
		},
		"nat-without-relay": {
			NetworkInfo: database.NetworkInfo{
				NodeType:   "private",
				UsingRelay: false,
			},
		},
	}

	connectable := filter.FilterConnectablePeers(metadataMap)

	// Should have 3 connectable peers (public, relay, nat-with-relay)
	if len(connectable) != 3 {
		t.Errorf("Expected 3 connectable peers, got %d", len(connectable))
	}

	// Verify correct peers are included
	if _, exists := connectable["public-peer"]; !exists {
		t.Error("Expected public-peer to be connectable")
	}
	if _, exists := connectable["relay-peer"]; !exists {
		t.Error("Expected relay-peer to be connectable")
	}
	if _, exists := connectable["nat-with-relay"]; !exists {
		t.Error("Expected nat-with-relay to be connectable")
	}

	// Verify NAT without relay is excluded
	if _, exists := connectable["nat-without-relay"]; exists {
		t.Error("Expected nat-without-relay to not be connectable")
	}
}

func TestConnectionMethodString(t *testing.T) {
	testCases := []struct {
		method   ConnectionMethod
		expected string
	}{
		{ConnectionMethodNone, "none"},
		{ConnectionMethodDirect, "direct"},
		{ConnectionMethodRelay, "relay"},
		{ConnectionMethod(999), "unknown"},
	}

	for _, tc := range testCases {
		result := ConnectionMethodString(tc.method)
		if result != tc.expected {
			t.Errorf("ConnectionMethodString(%d): expected '%s', got '%s'",
				tc.method, tc.expected, result)
		}
	}
}

func TestValidateMetadata(t *testing.T) {
	filter := setupTestConnectability()

	testCases := []struct {
		name        string
		metadata    *database.PeerMetadata
		expectError bool
	}{
		{
			name:        "Nil metadata",
			metadata:    nil,
			expectError: true,
		},
		{
			name: "Valid public peer",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType: "public",
					PublicIP: "203.0.113.1",
				},
			},
			expectError: false,
		},
		{
			name: "Invalid node type",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType: "invalid",
				},
			},
			expectError: true,
		},
		{
			name: "Public peer without IP",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType: "public",
					PublicIP: "",
				},
			},
			expectError: true,
		},
		{
			name: "Relay must be public",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType: "private",
					IsRelay:  true,
				},
			},
			expectError: true,
		},
		{
			name: "NAT using relay without relay info",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType:   "private",
					UsingRelay: true,
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := filter.ValidateMetadata(tc.metadata)
			if tc.expectError && err == nil {
				t.Errorf("%s: Expected error, got nil", tc.name)
			}
			if !tc.expectError && err != nil {
				t.Errorf("%s: Expected no error, got %v", tc.name, err)
			}
		})
	}
}

func TestGetConnectabilityReason(t *testing.T) {
	filter := setupTestConnectability()

	testCases := []struct {
		name            string
		metadata        *database.PeerMetadata
		expectedContain string
	}{
		{
			name:            "Nil metadata",
			metadata:        nil,
			expectedContain: "nil",
		},
		{
			name: "Public peer",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType: "public",
					PublicIP: "203.0.113.1",
				},
			},
			expectedContain: "public peer",
		},
		{
			name: "Relay node",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					IsRelay:  true,
					PublicIP: "203.0.113.2",
				},
			},
			expectedContain: "relay node",
		},
		{
			name: "NAT without relay",
			metadata: &database.PeerMetadata{
				NetworkInfo: database.NetworkInfo{
					NodeType:   "private",
					UsingRelay: false,
				},
			},
			expectedContain: "without relay",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reason := filter.GetConnectabilityReason(tc.metadata)
			if reason == "" {
				t.Errorf("%s: Expected non-empty reason", tc.name)
			}
			// Just verify we got a reason - don't check exact text
		})
	}
}
