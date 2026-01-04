package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// InvoiceMessageHandler handles invoice message sending with relay support and retry logic
type InvoiceMessageHandler struct {
	logger        *utils.LogsManager
	config        *utils.ConfigManager
	quicPeer      *QUICPeer
	dbManager     *database.SQLiteManager
	knownPeers    *KnownPeersManager
	metadataQuery *MetadataQueryService
	ourPeerID     string
	maxRetries    int
	retryBackoff  time.Duration

	// Relay connection cache (peerID -> relay info)
	relayCache   map[string]*RelayCache
	relayCacheMu sync.RWMutex
}

// NewInvoiceMessageHandler creates a new invoice message handler
func NewInvoiceMessageHandler(
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	quicPeer *QUICPeer,
) *InvoiceMessageHandler {
	maxRetries := config.GetConfigInt("invoice_max_retries", 3, 0, 10)
	retryBackoff := time.Duration(config.GetConfigInt("invoice_retry_backoff_ms", 2000, 500, 10000)) * time.Millisecond

	return &InvoiceMessageHandler{
		logger:       logger,
		config:       config,
		quicPeer:     quicPeer,
		maxRetries:   maxRetries,
		retryBackoff: retryBackoff,
		relayCache:   make(map[string]*RelayCache),
	}
}

// SetDependencies sets the additional dependencies needed for relay forwarding
func (imh *InvoiceMessageHandler) SetDependencies(
	dbManager *database.SQLiteManager,
	knownPeers *KnownPeersManager,
	metadataQuery *MetadataQueryService,
	ourPeerID string,
) {
	imh.dbManager = dbManager
	imh.knownPeers = knownPeers
	imh.metadataQuery = metadataQuery
	imh.ourPeerID = ourPeerID
}

// SendInvoiceRequestWithRetry sends an invoice request with exponential backoff retry
func (imh *InvoiceMessageHandler) SendInvoiceRequestWithRetry(
	toPeerID string,
	invoiceData *InvoiceRequestData,
	invoiceID string,
) error {
	var lastErr error

	for attempt := 0; attempt <= imh.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: retryBackoff * 2^(attempt-1)
			backoff := imh.retryBackoff * time.Duration(1<<uint(attempt-1))
			imh.logger.Info(fmt.Sprintf("Retrying invoice %s send to peer %s after %v (attempt %d/%d)",
				invoiceID, toPeerID[:8], backoff, attempt+1, imh.maxRetries+1), "invoice_message_handler")
			time.Sleep(backoff)
		}

		// Try direct connection first
		err := imh.sendInvoiceRequestDirect(toPeerID, invoiceData)
		if err == nil {
			imh.logger.Info(fmt.Sprintf("Invoice %s sent successfully to peer %s (attempt %d)",
				invoiceID, toPeerID[:8], attempt+1), "invoice_message_handler")
			return nil
		}

		imh.logger.Debug(fmt.Sprintf("Direct send failed for invoice %s to peer %s: %v",
			invoiceID, toPeerID[:8], err), "invoice_message_handler")

		// Try via relay if direct fails
		relayErr := imh.sendInvoiceRequestViaRelay(toPeerID, invoiceData)
		if relayErr == nil {
			imh.logger.Info(fmt.Sprintf("Invoice %s sent via relay to peer %s (attempt %d)",
				invoiceID, toPeerID[:8], attempt+1), "invoice_message_handler")
			return nil
		}

		lastErr = relayErr
		imh.logger.Warn(fmt.Sprintf("Both direct and relay send failed for invoice %s to peer %s: %v",
			invoiceID, toPeerID[:8], relayErr), "invoice_message_handler")

		// Invalidate relay cache for next attempt
		imh.invalidateRelayCache(toPeerID)
	}

	return fmt.Errorf("failed to send invoice after %d attempts: %v", imh.maxRetries+1, lastErr)
}

// SendInvoiceResponseWithRetry sends an invoice response with retry
func (imh *InvoiceMessageHandler) SendInvoiceResponseWithRetry(
	toPeerID string,
	invoiceID string,
	accepted bool,
	message string,
) error {
	var lastErr error

	for attempt := 0; attempt <= imh.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := imh.retryBackoff * time.Duration(1<<uint(attempt-1))
			imh.logger.Info(fmt.Sprintf("Retrying invoice response send (attempt %d/%d)",
				attempt+1, imh.maxRetries+1), "invoice_message_handler")
			time.Sleep(backoff)
		}

		// Try direct first
		err := imh.sendInvoiceResponseDirect(toPeerID, invoiceID, accepted, message)
		if err == nil {
			return nil
		}

		// Try via relay
		relayErr := imh.sendInvoiceResponseViaRelay(toPeerID, invoiceID, accepted, message)
		if relayErr == nil {
			return nil
		}

		lastErr = relayErr
		imh.invalidateRelayCache(toPeerID)
	}

	return fmt.Errorf("failed to send invoice response after %d attempts: %v", imh.maxRetries+1, lastErr)
}

// SendInvoiceNotificationWithRetry sends an invoice notification with retry
func (imh *InvoiceMessageHandler) SendInvoiceNotificationWithRetry(
	toPeerID string,
	invoiceID string,
	status string,
	message string,
) error {
	var lastErr error

	for attempt := 0; attempt <= imh.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := imh.retryBackoff * time.Duration(1<<uint(attempt-1))
			imh.logger.Info(fmt.Sprintf("Retrying invoice notification send (attempt %d/%d)",
				attempt+1, imh.maxRetries+1), "invoice_message_handler")
			time.Sleep(backoff)
		}

		// Try direct first
		err := imh.sendInvoiceNotificationDirect(toPeerID, invoiceID, status, message)
		if err == nil {
			return nil
		}

		// Try via relay
		relayErr := imh.sendInvoiceNotificationViaRelay(toPeerID, invoiceID, status, message)
		if relayErr == nil {
			return nil
		}

		lastErr = relayErr
		imh.invalidateRelayCache(toPeerID)
	}

	return fmt.Errorf("failed to send invoice notification after %d attempts: %v", imh.maxRetries+1, lastErr)
}

// Direct sending methods (delegate to QUICPeer)

func (imh *InvoiceMessageHandler) sendInvoiceRequestDirect(toPeerID string, invoiceData *InvoiceRequestData) error {
	return imh.quicPeer.SendInvoiceRequestDirect(toPeerID, invoiceData)
}

func (imh *InvoiceMessageHandler) sendInvoiceResponseDirect(toPeerID string, invoiceID string, accepted bool, message string) error {
	return imh.quicPeer.SendInvoiceResponseDirect(toPeerID, invoiceID, accepted, message)
}

func (imh *InvoiceMessageHandler) sendInvoiceNotificationDirect(toPeerID string, invoiceID string, status string, message string) error {
	return imh.quicPeer.SendInvoiceNotificationDirect(toPeerID, invoiceID, status, message)
}

// Relay sending methods

func (imh *InvoiceMessageHandler) sendInvoiceRequestViaRelay(toPeerID string, invoiceData *InvoiceRequestData) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice request to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	// Check dependencies
	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	// Create invoice request message
	msg := CreateInvoiceRequest(invoiceData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal invoice request: %v", err)
	}

	// Send via relay with retry logic
	return imh.sendMessageViaRelay(toPeerID, msgBytes, "invoice_request")
}

func (imh *InvoiceMessageHandler) sendInvoiceResponseViaRelay(toPeerID string, invoiceID string, accepted bool, message string) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice response to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	msg := CreateInvoiceResponse(invoiceID, accepted, message)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal invoice response: %v", err)
	}

	return imh.sendMessageViaRelay(toPeerID, msgBytes, "invoice_response")
}

func (imh *InvoiceMessageHandler) sendInvoiceNotificationViaRelay(toPeerID string, invoiceID string, status string, message string) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice notification to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	msg := CreateInvoiceNotify(invoiceID, status, message)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal invoice notification: %v", err)
	}

	return imh.sendMessageViaRelay(toPeerID, msgBytes, "invoice_notify")
}

// sendMessageViaRelay sends a one-way message via relay (no response expected)
func (imh *InvoiceMessageHandler) sendMessageViaRelay(peerID string, messageBytes []byte, messageType string) error {
	// Get relay info with caching
	relayAddress, relaySessionID, err := imh.getRelayInfo(peerID)
	if err != nil {
		return err
	}

	// Wrap message in relay forward
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: imh.ourPeerID,
		TargetPeerID: peerID,
		MessageType:  messageType,
		Payload:      messageBytes,
		PayloadSize:  int64(len(messageBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Connect to relay
	conn, err := imh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		// Clean up stale connection
		relayAddr := conn.RemoteAddr().String()
		imh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "invoice_message_handler")
		imh.quicPeer.DisconnectFromPeer(relayAddr)
		return fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send message to relay
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return fmt.Errorf("failed to send via relay: %v", err)
	}

	// Wait for relay's delivery confirmation response
	// The relay returns "success" if delivered, or "error: ..." if destination peer is offline
	responseBuf := make([]byte, 512)
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := stream.Read(responseBuf)
	if err != nil {
		return fmt.Errorf("failed to read relay delivery confirmation: %v (destination peer may be offline)", err)
	}

	responseStr := string(responseBuf[:n])
	if n > 6 && responseStr[:6] == "error:" {
		// Relay failed to deliver (destination peer not connected)
		return fmt.Errorf("relay delivery failed: %s", responseStr[7:])
	}

	if responseStr != "success" {
		return fmt.Errorf("unexpected relay response: %s", responseStr)
	}

	imh.logger.Debug(fmt.Sprintf("Sent %s to peer %s via relay with delivery confirmation", messageType, peerID[:8]), "invoice_message_handler")
	return nil
}

// getRelayInfo retrieves relay connection info for a peer with caching
func (imh *InvoiceMessageHandler) getRelayInfo(peerID string) (relayAddress string, relaySessionID string, err error) {
	// Check cache first
	imh.relayCacheMu.RLock()
	cached, exists := imh.relayCache[peerID]
	imh.relayCacheMu.RUnlock()

	// Use cached data if it exists and is fresh (< 1 minute old)
	if exists && time.Since(cached.CachedAt) < 1*time.Minute {
		imh.logger.Debug(fmt.Sprintf("Using cached relay info for peer %s (age: %v)", peerID[:8], time.Since(cached.CachedAt)), "invoice_message_handler")
		return cached.RelayAddress, cached.RelaySessionID, nil
	}

	// Cache miss or expired - query metadata
	imh.logger.Debug(fmt.Sprintf("Querying relay metadata for peer %s (cache miss or expired)", peerID[:8]), "invoice_message_handler")

	// Get peer from database
	peer, err := imh.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return "", "", fmt.Errorf("peer %s not found or has no public key", peerID[:8])
	}

	// Query DHT for metadata
	metadata, err := imh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		// Invalidate cache on error
		imh.relayCacheMu.Lock()
		delete(imh.relayCache, peerID)
		imh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("failed to query peer metadata: %v", err)
	}

	// Validate relay info
	if !metadata.NetworkInfo.UsingRelay || metadata.NetworkInfo.RelayAddress == "" || metadata.NetworkInfo.RelaySessionID == "" {
		// Invalidate cache if peer is not using relay
		imh.relayCacheMu.Lock()
		delete(imh.relayCache, peerID)
		imh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("peer not accessible via relay")
	}

	// Update cache
	imh.relayCacheMu.Lock()
	imh.relayCache[peerID] = &RelayCache{
		RelayAddress:   metadata.NetworkInfo.RelayAddress,
		RelaySessionID: metadata.NetworkInfo.RelaySessionID,
		CachedAt:       time.Now(),
	}
	imh.relayCacheMu.Unlock()

	imh.logger.Debug(fmt.Sprintf("Cached relay info for peer %s: relay=%s, session=%s",
		peerID[:8], metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID[:8]), "invoice_message_handler")

	return metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID, nil
}

// invalidateRelayCache invalidates cached relay info for a peer
func (imh *InvoiceMessageHandler) invalidateRelayCache(peerID string) {
	imh.relayCacheMu.Lock()
	defer imh.relayCacheMu.Unlock()

	if _, exists := imh.relayCache[peerID]; exists {
		delete(imh.relayCache, peerID)
		imh.logger.Debug(fmt.Sprintf("Invalidated relay cache for peer %s", peerID[:8]), "invoice_message_handler")
	}
}
