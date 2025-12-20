package p2p

import (
	"fmt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/system"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// CapabilitiesHandler handles incoming system capabilities requests via QUIC.
// This allows peers to fetch full SystemCapabilities on-demand, since the DHT
// only stores a compact CapabilitySummary to stay under the BEP44 1000-byte limit.
type CapabilitiesHandler struct {
	capabilities *system.SystemCapabilities // Cached at startup
	logger       *utils.LogsManager
	peerID       string // Our peer ID to include in responses
}

// NewCapabilitiesHandler creates a new capabilities handler
func NewCapabilitiesHandler(
	capabilities *system.SystemCapabilities,
	logger *utils.LogsManager,
	peerID string,
) *CapabilitiesHandler {
	return &CapabilitiesHandler{
		capabilities: capabilities,
		logger:       logger,
		peerID:       peerID,
	}
}

// HandleCapabilitiesRequest processes a capabilities request and returns full system capabilities
func (ch *CapabilitiesHandler) HandleCapabilitiesRequest(msg *QUICMessage, remoteAddr string) *QUICMessage {
	ch.logger.Debug(fmt.Sprintf("Handling capabilities request from %s", remoteAddr), "capabilities")

	// No need to parse request data - it's an empty struct just requesting capabilities
	// Just return our cached capabilities

	if ch.capabilities == nil {
		ch.logger.Warn(fmt.Sprintf("Capabilities request from %s but no capabilities available", remoteAddr), "capabilities")
		return CreateCapabilitiesResponse(nil, ch.peerID, "capabilities not available")
	}

	ch.logger.Info(fmt.Sprintf("Responding to capabilities request from %s", remoteAddr), "capabilities")
	return CreateCapabilitiesResponse(ch.capabilities, ch.peerID, "")
}

// UpdateCapabilities allows updating the cached capabilities (e.g., if system state changes)
func (ch *CapabilitiesHandler) UpdateCapabilities(capabilities *system.SystemCapabilities) {
	ch.capabilities = capabilities
}
