package p2p

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
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

	// E2E encryption for invoices
	invoiceCrypto     *crypto.InvoiceCryptoManager
	requireEncryption bool // If true, refuse to send/receive unencrypted invoices

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
	requireEncryption := config.GetConfigBool("invoice_require_encryption", false) // Default: accept both

	return &InvoiceMessageHandler{
		logger:            logger,
		config:            config,
		quicPeer:          quicPeer,
		maxRetries:        maxRetries,
		retryBackoff:      retryBackoff,
		requireEncryption: requireEncryption,
		relayCache:        make(map[string]*RelayCache),
	}
}

// SetInvoiceCrypto sets the invoice crypto manager for E2E encryption
func (imh *InvoiceMessageHandler) SetInvoiceCrypto(invoiceCrypto *crypto.InvoiceCryptoManager) {
	imh.invoiceCrypto = invoiceCrypto
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

		// Check if peer is public (don't try relay for public peers)
		if imh.quicPeer.localStoreForward != nil {
			isPublic, typeErr := imh.quicPeer.localStoreForward.IsPublicPeer(toPeerID)
			if typeErr == nil && isPublic {
				imh.logger.Debug(fmt.Sprintf("Peer %s is public, invoice queued for local store-and-forward", toPeerID[:8]), "invoice_message_handler")
				lastErr = err
				continue
			}
		}

		// Try via relay if direct fails (for NAT peers only)
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

		// Check if peer is public (don't try relay for public peers)
		if imh.quicPeer.localStoreForward != nil {
			isPublic, typeErr := imh.quicPeer.localStoreForward.IsPublicPeer(toPeerID)
			if typeErr == nil && isPublic {
				imh.logger.Debug(fmt.Sprintf("Peer %s is public, response queued for local store-and-forward", toPeerID[:8]), "invoice_message_handler")
				lastErr = err
				continue
			}
		}

		// Try via relay (for NAT peers only)
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

		// Check if peer is public (don't try relay for public peers)
		// For public peers, the message is already queued locally by SendMessageToPeer
		if imh.quicPeer.localStoreForward != nil {
			isPublic, typeErr := imh.quicPeer.localStoreForward.IsPublicPeer(toPeerID)
			if typeErr == nil && isPublic {
				// Message already queued for later delivery
				imh.logger.Debug(fmt.Sprintf("Peer %s is public, message queued for local store-and-forward", toPeerID[:8]), "invoice_message_handler")
				lastErr = err
				continue
			}
		}

		// Try via relay (for NAT peers only)
		relayErr := imh.sendInvoiceNotificationViaRelay(toPeerID, invoiceID, status, message)
		if relayErr == nil {
			return nil
		}

		lastErr = relayErr
		imh.invalidateRelayCache(toPeerID)
	}

	return fmt.Errorf("failed to send invoice notification after %d attempts: %v", imh.maxRetries+1, lastErr)
}

// Direct sending methods (delegate to QUICPeer with optional encryption)

func (imh *InvoiceMessageHandler) sendInvoiceRequestDirect(toPeerID string, invoiceData *InvoiceRequestData) error {
	// Try to send encrypted if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			return imh.sendEncryptedInvoiceRequest(toPeerID, invoiceData, recipientPubKey)
		}
		imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, falling back to unencrypted: %v", toPeerID[:8], err), "invoice_message_handler")
	}

	// Fall back to unencrypted
	return imh.quicPeer.SendInvoiceRequestDirect(toPeerID, invoiceData)
}

func (imh *InvoiceMessageHandler) sendInvoiceResponseDirect(toPeerID string, invoiceID string, accepted bool, message string) error {
	// Try to send encrypted if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			return imh.sendEncryptedInvoiceResponse(toPeerID, invoiceID, accepted, message, recipientPubKey)
		}
		imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, falling back to unencrypted: %v", toPeerID[:8], err), "invoice_message_handler")
	}

	// Fall back to unencrypted
	return imh.quicPeer.SendInvoiceResponseDirect(toPeerID, invoiceID, accepted, message)
}

func (imh *InvoiceMessageHandler) sendInvoiceNotificationDirect(toPeerID string, invoiceID string, status string, message string) error {
	// Try to send encrypted if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			return imh.sendEncryptedInvoiceNotification(toPeerID, invoiceID, status, message, recipientPubKey)
		}
		imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, falling back to unencrypted: %v", toPeerID[:8], err), "invoice_message_handler")
	}

	// Fall back to unencrypted
	return imh.quicPeer.SendInvoiceNotificationDirect(toPeerID, invoiceID, status, message)
}

// getRecipientPublicKey retrieves the Ed25519 public key for a peer from known peers
func (imh *InvoiceMessageHandler) getRecipientPublicKey(peerID string) (ed25519.PublicKey, error) {
	peer, err := imh.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil {
		return nil, fmt.Errorf("peer not found: %v", err)
	}
	if peer == nil || len(peer.PublicKey) == 0 {
		return nil, fmt.Errorf("peer %s has no public key", peerID[:8])
	}
	return peer.PublicKey, nil
}

// sendEncryptedInvoiceRequest encrypts and sends an invoice request
func (imh *InvoiceMessageHandler) sendEncryptedInvoiceRequest(toPeerID string, invoiceData *InvoiceRequestData, recipientPubKey ed25519.PublicKey) error {
	// Serialize invoice data to JSON
	plaintext, err := json.Marshal(invoiceData)
	if err != nil {
		return fmt.Errorf("failed to marshal invoice request: %v", err)
	}

	// Encrypt the invoice data
	ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt invoice request: %v", err)
	}

	// Create encrypted message
	encryptedData := &EncryptedInvoiceRequestData{
		SenderPeerID:     imh.ourPeerID,
		EphemeralPubKey:  ephemeralPubKey[:],
		EncryptedPayload: ciphertext,
		Nonce:            nonce[:],
		Signature:        signature,
		Timestamp:        time.Now().Unix(),
	}

	msg := CreateEncryptedInvoiceRequest(encryptedData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal encrypted invoice request: %v", err)
	}

	if err := imh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		return fmt.Errorf("failed to send encrypted invoice request to peer %s: %v", toPeerID[:8], err)
	}

	imh.logger.Info(fmt.Sprintf("Sent encrypted invoice request %s to peer %s", invoiceData.InvoiceID, toPeerID[:8]), "invoice_message_handler")
	return nil
}

// sendEncryptedInvoiceResponse encrypts and sends an invoice response
func (imh *InvoiceMessageHandler) sendEncryptedInvoiceResponse(toPeerID string, invoiceID string, accepted bool, message string, recipientPubKey ed25519.PublicKey) error {
	// Serialize response data to JSON
	responseData := &InvoiceResponseData{
		InvoiceID: invoiceID,
		Accepted:  accepted,
		Message:   message,
	}
	plaintext, err := json.Marshal(responseData)
	if err != nil {
		return fmt.Errorf("failed to marshal invoice response: %v", err)
	}

	// Encrypt the response data
	ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt invoice response: %v", err)
	}

	// Create encrypted message
	encryptedData := &EncryptedInvoiceResponseData{
		SenderPeerID:     imh.ourPeerID,
		EphemeralPubKey:  ephemeralPubKey[:],
		EncryptedPayload: ciphertext,
		Nonce:            nonce[:],
		Signature:        signature,
		Timestamp:        time.Now().Unix(),
	}

	msg := CreateEncryptedInvoiceResponse(encryptedData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal encrypted invoice response: %v", err)
	}

	if err := imh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		return fmt.Errorf("failed to send encrypted invoice response to peer %s: %v", toPeerID[:8], err)
	}

	status := "rejected"
	if accepted {
		status = "accepted"
	}
	imh.logger.Info(fmt.Sprintf("Sent encrypted invoice %s response to peer %s: %s", invoiceID, toPeerID[:8], status), "invoice_message_handler")
	return nil
}

// sendEncryptedInvoiceNotification encrypts and sends an invoice notification
func (imh *InvoiceMessageHandler) sendEncryptedInvoiceNotification(toPeerID string, invoiceID string, status string, message string, recipientPubKey ed25519.PublicKey) error {
	// Serialize notification data to JSON
	notifyData := &InvoiceNotifyData{
		InvoiceID: invoiceID,
		Status:    status,
		Message:   message,
	}
	plaintext, err := json.Marshal(notifyData)
	if err != nil {
		return fmt.Errorf("failed to marshal invoice notification: %v", err)
	}

	// Encrypt the notification data
	ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt invoice notification: %v", err)
	}

	// Create encrypted message
	encryptedData := &EncryptedInvoiceNotifyData{
		SenderPeerID:     imh.ourPeerID,
		EphemeralPubKey:  ephemeralPubKey[:],
		EncryptedPayload: ciphertext,
		Nonce:            nonce[:],
		Signature:        signature,
		Timestamp:        time.Now().Unix(),
	}

	msg := CreateEncryptedInvoiceNotify(encryptedData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal encrypted invoice notification: %v", err)
	}

	if err := imh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		return fmt.Errorf("failed to send encrypted invoice notification to peer %s: %v", toPeerID[:8], err)
	}

	imh.logger.Info(fmt.Sprintf("Sent encrypted invoice %s notification to peer %s: %s", invoiceID, toPeerID[:8], status), "invoice_message_handler")
	return nil
}

// Relay sending methods

func (imh *InvoiceMessageHandler) sendInvoiceRequestViaRelay(toPeerID string, invoiceData *InvoiceRequestData) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice request to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	// Check dependencies
	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	var msgBytes []byte
	var messageType string

	// Try to encrypt if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			// Serialize and encrypt
			plaintext, err := json.Marshal(invoiceData)
			if err != nil {
				return fmt.Errorf("failed to marshal invoice request: %v", err)
			}

			ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
			if err != nil {
				return fmt.Errorf("failed to encrypt invoice request: %v", err)
			}

			encryptedData := &EncryptedInvoiceRequestData{
				SenderPeerID:     imh.ourPeerID,
				EphemeralPubKey:  ephemeralPubKey[:],
				EncryptedPayload: ciphertext,
				Nonce:            nonce[:],
				Signature:        signature,
				Timestamp:        time.Now().Unix(),
			}

			msg := CreateEncryptedInvoiceRequest(encryptedData)
			msgBytes, err = msg.Marshal()
			if err != nil {
				return fmt.Errorf("failed to marshal encrypted invoice request: %v", err)
			}
			messageType = "encrypted_invoice_request"
		} else {
			imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, sending unencrypted via relay: %v", toPeerID[:8], err), "invoice_message_handler")
		}
	}

	// Fall back to unencrypted if encryption failed or not available
	if msgBytes == nil {
		msg := CreateInvoiceRequest(invoiceData)
		var err error
		msgBytes, err = msg.Marshal()
		if err != nil {
			return fmt.Errorf("failed to marshal invoice request: %v", err)
		}
		messageType = "invoice_request"
	}

	// Send via relay with retry logic
	return imh.sendMessageViaRelay(toPeerID, msgBytes, messageType)
}

func (imh *InvoiceMessageHandler) sendInvoiceResponseViaRelay(toPeerID string, invoiceID string, accepted bool, message string) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice response to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	var msgBytes []byte
	var messageType string

	// Try to encrypt if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			// Serialize and encrypt
			responseData := &InvoiceResponseData{
				InvoiceID: invoiceID,
				Accepted:  accepted,
				Message:   message,
			}
			plaintext, err := json.Marshal(responseData)
			if err != nil {
				return fmt.Errorf("failed to marshal invoice response: %v", err)
			}

			ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
			if err != nil {
				return fmt.Errorf("failed to encrypt invoice response: %v", err)
			}

			encryptedData := &EncryptedInvoiceResponseData{
				SenderPeerID:     imh.ourPeerID,
				EphemeralPubKey:  ephemeralPubKey[:],
				EncryptedPayload: ciphertext,
				Nonce:            nonce[:],
				Signature:        signature,
				Timestamp:        time.Now().Unix(),
			}

			msg := CreateEncryptedInvoiceResponse(encryptedData)
			msgBytes, err = msg.Marshal()
			if err != nil {
				return fmt.Errorf("failed to marshal encrypted invoice response: %v", err)
			}
			messageType = "encrypted_invoice_response"
		} else {
			imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, sending unencrypted via relay: %v", toPeerID[:8], err), "invoice_message_handler")
		}
	}

	// Fall back to unencrypted if encryption failed or not available
	if msgBytes == nil {
		msg := CreateInvoiceResponse(invoiceID, accepted, message)
		var err error
		msgBytes, err = msg.Marshal()
		if err != nil {
			return fmt.Errorf("failed to marshal invoice response: %v", err)
		}
		messageType = "invoice_response"
	}

	return imh.sendMessageViaRelay(toPeerID, msgBytes, messageType)
}

func (imh *InvoiceMessageHandler) sendInvoiceNotificationViaRelay(toPeerID string, invoiceID string, status string, message string) error {
	imh.logger.Debug(fmt.Sprintf("Sending invoice notification to peer %s via relay", toPeerID[:8]), "invoice_message_handler")

	if imh.dbManager == nil || imh.metadataQuery == nil || imh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	var msgBytes []byte
	var messageType string

	// Try to encrypt if crypto is available
	if imh.invoiceCrypto != nil && imh.knownPeers != nil {
		recipientPubKey, err := imh.getRecipientPublicKey(toPeerID)
		if err == nil {
			// Serialize and encrypt
			notifyData := &InvoiceNotifyData{
				InvoiceID: invoiceID,
				Status:    status,
				Message:   message,
			}
			plaintext, err := json.Marshal(notifyData)
			if err != nil {
				return fmt.Errorf("failed to marshal invoice notification: %v", err)
			}

			ciphertext, nonce, ephemeralPubKey, signature, err := imh.invoiceCrypto.EncryptInvoice(plaintext, recipientPubKey)
			if err != nil {
				return fmt.Errorf("failed to encrypt invoice notification: %v", err)
			}

			encryptedData := &EncryptedInvoiceNotifyData{
				SenderPeerID:     imh.ourPeerID,
				EphemeralPubKey:  ephemeralPubKey[:],
				EncryptedPayload: ciphertext,
				Nonce:            nonce[:],
				Signature:        signature,
				Timestamp:        time.Now().Unix(),
			}

			msg := CreateEncryptedInvoiceNotify(encryptedData)
			msgBytes, err = msg.Marshal()
			if err != nil {
				return fmt.Errorf("failed to marshal encrypted invoice notification: %v", err)
			}
			messageType = "encrypted_invoice_notify"
		} else {
			imh.logger.Debug(fmt.Sprintf("Could not get public key for peer %s, sending unencrypted via relay: %v", toPeerID[:8], err), "invoice_message_handler")
		}
	}

	// Fall back to unencrypted if encryption failed or not available
	if msgBytes == nil {
		msg := CreateInvoiceNotify(invoiceID, status, message)
		var err error
		msgBytes, err = msg.Marshal()
		if err != nil {
			return fmt.Errorf("failed to marshal invoice notification: %v", err)
		}
		messageType = "invoice_notify"
	}

	return imh.sendMessageViaRelay(toPeerID, msgBytes, messageType)
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
