package p2p

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// InvoiceHandler handles incoming invoice-related QUIC messages
type InvoiceHandler struct {
	db           *database.SQLiteManager
	logger       *utils.LogsManager
	peerID       string
	eventEmitter EventEmitter // For WebSocket notifications

	// E2E encryption support
	invoiceCrypto     *crypto.InvoiceCryptoManager
	knownPeers        *KnownPeersManager
	requireEncryption bool // If true, refuse to accept unencrypted invoices

	// Timestamp tolerance for replay protection (default: 5 minutes)
	timestampTolerance time.Duration
}

// EventEmitter interface for WebSocket notifications
type EventEmitter interface {
	EmitInvoiceReceived(invoice *database.PaymentInvoice)
	EmitInvoiceAccepted(invoiceID string)
	EmitInvoiceRejected(invoiceID string)
	EmitInvoiceSettled(invoiceID string)
}

// NewInvoiceHandler creates a new invoice handler
func NewInvoiceHandler(
	db *database.SQLiteManager,
	logger *utils.LogsManager,
	peerID string,
	eventEmitter EventEmitter,
) *InvoiceHandler {
	return &InvoiceHandler{
		db:                 db,
		logger:             logger,
		peerID:             peerID,
		eventEmitter:       eventEmitter,
		timestampTolerance: 5 * time.Minute, // Default: 5 minutes for replay protection
	}
}

// SetInvoiceCrypto sets the invoice crypto manager for E2E decryption
func (ih *InvoiceHandler) SetInvoiceCrypto(invoiceCrypto *crypto.InvoiceCryptoManager) {
	ih.invoiceCrypto = invoiceCrypto
}

// SetKnownPeersManager sets the known peers manager for public key lookups
func (ih *InvoiceHandler) SetKnownPeersManager(knownPeers *KnownPeersManager) {
	ih.knownPeers = knownPeers
}

// SetRequireEncryption sets whether to require encryption for invoices
func (ih *InvoiceHandler) SetRequireEncryption(require bool) {
	ih.requireEncryption = require
}

// HandleInvoiceRequest processes an incoming invoice request from a peer
func (ih *InvoiceHandler) HandleInvoiceRequest(msg *QUICMessage, remoteAddr string, remotePeerID string) *QUICMessage {
	ih.logger.Debug(fmt.Sprintf("Handling invoice request from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Parse invoice data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal invoice data: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid invoice data")
	}

	var invoiceData InvoiceRequestData
	if err := json.Unmarshal(dataBytes, &invoiceData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse invoice request: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid invoice data")
	}

	// Validate that this invoice is intended for us
	if invoiceData.ToPeerID != ih.peerID {
		ih.logger.Warn(fmt.Sprintf("Invoice %s not intended for us (to: %s, us: %s)",
			invoiceData.InvoiceID, invoiceData.ToPeerID[:8], ih.peerID[:8]), "invoice_handler")
		return CreateInvoiceResponse(invoiceData.InvoiceID, false, "Invoice not for this peer")
	}

	// Validate sender matches from_peer_id
	if invoiceData.FromPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Invoice sender mismatch (claimed: %s, actual: %s)",
			invoiceData.FromPeerID[:8], remotePeerID[:8]), "invoice_handler")
		return CreateInvoiceResponse(invoiceData.InvoiceID, false, "Sender mismatch")
	}

	// Convert expiration timestamp
	var expiresAt *time.Time
	if invoiceData.ExpiresAt > 0 {
		exp := time.Unix(invoiceData.ExpiresAt, 0)
		expiresAt = &exp
	}

	// Create invoice in database
	invoice := &database.PaymentInvoice{
		InvoiceID:         invoiceData.InvoiceID,
		FromPeerID:        invoiceData.FromPeerID,
		ToPeerID:          invoiceData.ToPeerID,
		FromWalletAddress: invoiceData.FromWalletAddress,
		Amount:            invoiceData.Amount,
		Currency:          invoiceData.Currency,
		Network:           invoiceData.Network,
		Description:       invoiceData.Description,
		Status:            "pending",
		CreatedAt:         time.Now(),
		ExpiresAt:         expiresAt,
		Metadata:          invoiceData.Metadata,
	}

	if err := ih.db.CreatePaymentInvoice(invoice); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to create invoice %s: %v", invoiceData.InvoiceID, err), "invoice_handler")
		return CreateInvoiceResponse(invoiceData.InvoiceID, false, "Failed to store invoice")
	}

	ih.logger.Info(fmt.Sprintf("Invoice %s received from %s: %.6f %s",
		invoiceData.InvoiceID, remotePeerID[:8], invoiceData.Amount, invoiceData.Currency), "invoice_handler")

	// Emit WebSocket notification
	if ih.eventEmitter != nil {
		ih.eventEmitter.EmitInvoiceReceived(invoice)
	}

	// Send acknowledgment
	return CreateInvoiceResponse(invoiceData.InvoiceID, true, "Invoice received")
}

// HandleInvoiceResponse processes an invoice acceptance/rejection response
func (ih *InvoiceHandler) HandleInvoiceResponse(msg *QUICMessage, remoteAddr string, remotePeerID string) {
	ih.logger.Debug(fmt.Sprintf("Handling invoice response from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Parse response data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal invoice response: %v", err), "invoice_handler")
		return
	}

	var responseData InvoiceResponseData
	if err := json.Unmarshal(dataBytes, &responseData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse invoice response: %v", err), "invoice_handler")
		return
	}

	// Get invoice
	invoice, err := ih.db.GetPaymentInvoice(responseData.InvoiceID)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Invoice %s not found: %v", responseData.InvoiceID, err), "invoice_handler")
		return
	}

	// Validate that we created this invoice
	if invoice.FromPeerID != ih.peerID {
		ih.logger.Warn(fmt.Sprintf("Invoice %s not created by us", responseData.InvoiceID), "invoice_handler")
		return
	}

	// Validate that response is from the recipient
	if invoice.ToPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Invoice response from wrong peer (expected: %s, got: %s)",
			invoice.ToPeerID[:8], remotePeerID[:8]), "invoice_handler")
		return
	}

	if responseData.Accepted {
		ih.logger.Info(fmt.Sprintf("Invoice %s accepted by %s", responseData.InvoiceID, remotePeerID[:8]), "invoice_handler")
		// Note: Status will be updated to 'accepted' when we receive the settlement notification
		// Emit WebSocket notification
		if ih.eventEmitter != nil {
			ih.eventEmitter.EmitInvoiceAccepted(responseData.InvoiceID)
		}
	} else {
		// Update invoice status to rejected and store rejection reason
		if err := ih.db.UpdatePaymentInvoiceStatusWithReason(responseData.InvoiceID, "rejected", responseData.Message); err != nil {
			ih.logger.Warn(fmt.Sprintf("Failed to update invoice status: %v", err), "invoice_handler")
		}
		ih.logger.Info(fmt.Sprintf("Invoice %s rejected by %s: %s",
			responseData.InvoiceID, remotePeerID[:8], responseData.Message), "invoice_handler")

		// Emit WebSocket notification
		if ih.eventEmitter != nil {
			ih.eventEmitter.EmitInvoiceRejected(responseData.InvoiceID)
		}
	}
}

// HandleInvoiceNotify processes invoice status notifications (settled, expired, etc.)
func (ih *InvoiceHandler) HandleInvoiceNotify(msg *QUICMessage, remoteAddr string, remotePeerID string) {
	ih.logger.Debug(fmt.Sprintf("Handling invoice notification from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Parse notification data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal invoice notification: %v", err), "invoice_handler")
		return
	}

	var notifyData InvoiceNotifyData
	if err := json.Unmarshal(dataBytes, &notifyData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse invoice notification: %v", err), "invoice_handler")
		return
	}

	// Get invoice
	invoice, err := ih.db.GetPaymentInvoice(notifyData.InvoiceID)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Invoice %s not found: %v", notifyData.InvoiceID, err), "invoice_handler")
		return
	}

	// Validate that notification is from a party involved in the invoice
	if invoice.FromPeerID != remotePeerID && invoice.ToPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Invoice notification from uninvolved peer: %s", remotePeerID[:8]), "invoice_handler")
		return
	}

	// Update invoice status
	if err := ih.db.UpdatePaymentInvoiceStatus(notifyData.InvoiceID, notifyData.Status); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to update invoice status: %v", err), "invoice_handler")
		return
	}

	ih.logger.Info(fmt.Sprintf("Invoice %s status updated to %s: %s",
		notifyData.InvoiceID, notifyData.Status, notifyData.Message), "invoice_handler")

	// Emit WebSocket notification based on status
	if ih.eventEmitter != nil {
		switch notifyData.Status {
		case "settled":
			ih.eventEmitter.EmitInvoiceSettled(notifyData.InvoiceID)
		case "expired":
			// Could add EmitInvoiceExpired if needed
		}
	}
}

// HandleRelayedInvoiceRequest processes an incoming relayed invoice request
// Returns the response message instead of writing to stream (for relay forwarding)
func (ih *InvoiceHandler) HandleRelayedInvoiceRequest(msg *QUICMessage, remotePeerID string) *QUICMessage {
	return ih.HandleInvoiceRequest(msg, "relay", remotePeerID)
}

// HandleRelayedInvoiceResponse processes a relayed invoice response
func (ih *InvoiceHandler) HandleRelayedInvoiceResponse(msg *QUICMessage, remotePeerID string) {
	ih.HandleInvoiceResponse(msg, "relay", remotePeerID)
}

// HandleRelayedInvoiceNotify processes a relayed invoice notification
func (ih *InvoiceHandler) HandleRelayedInvoiceNotify(msg *QUICMessage, remotePeerID string) {
	ih.HandleInvoiceNotify(msg, "relay", remotePeerID)
}

// ============================================================================
// Encrypted Invoice Handlers
// ============================================================================

// HandleEncryptedInvoiceRequest processes an E2E encrypted invoice request
func (ih *InvoiceHandler) HandleEncryptedInvoiceRequest(msg *QUICMessage, remoteAddr string, remotePeerID string) *QUICMessage {
	ih.logger.Debug(fmt.Sprintf("Handling encrypted invoice request from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Check if crypto is available
	if ih.invoiceCrypto == nil {
		ih.logger.Warn("Received encrypted invoice but crypto manager not initialized", "invoice_handler")
		return CreateInvoiceResponse("", false, "Encryption not supported")
	}

	// Parse encrypted data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal encrypted invoice data: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid encrypted data")
	}

	var encryptedData EncryptedInvoiceRequestData
	if err := json.Unmarshal(dataBytes, &encryptedData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse encrypted invoice request: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid encrypted data")
	}

	// Validate timestamp for replay protection
	timestamp := time.Unix(encryptedData.Timestamp, 0)
	if time.Since(timestamp) > ih.timestampTolerance {
		ih.logger.Warn(fmt.Sprintf("Encrypted invoice timestamp too old: %v", timestamp), "invoice_handler")
		return CreateInvoiceResponse("", false, "Message timestamp expired")
	}

	// Get sender's public key for signature verification
	senderPubKey, err := ih.getSenderPublicKey(encryptedData.SenderPeerID)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Could not get public key for sender %s: %v", encryptedData.SenderPeerID[:8], err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Unknown sender")
	}

	// Validate sender matches the remote peer
	if encryptedData.SenderPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Sender mismatch: claimed %s, actual %s", encryptedData.SenderPeerID[:8], remotePeerID[:8]), "invoice_handler")
		return CreateInvoiceResponse("", false, "Sender mismatch")
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(encryptedData.EphemeralPubKey) != 32 {
		ih.logger.Warn("Invalid ephemeral public key size", "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid encryption parameters")
	}
	copy(ephemeralPubKey[:], encryptedData.EphemeralPubKey)

	// Convert nonce to array
	var nonce [24]byte
	if len(encryptedData.Nonce) != 24 {
		ih.logger.Warn("Invalid nonce size", "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid encryption parameters")
	}
	copy(nonce[:], encryptedData.Nonce)

	// Decrypt the invoice data
	plaintext, err := ih.invoiceCrypto.DecryptInvoice(
		encryptedData.EncryptedPayload,
		nonce,
		ephemeralPubKey,
		encryptedData.Signature,
		senderPubKey,
	)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to decrypt invoice request: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Decryption failed")
	}

	// Parse the decrypted invoice data
	var invoiceData InvoiceRequestData
	if err := json.Unmarshal(plaintext, &invoiceData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse decrypted invoice data: %v", err), "invoice_handler")
		return CreateInvoiceResponse("", false, "Invalid invoice data")
	}

	// Create a synthetic QUICMessage with the decrypted data
	decryptedMsg := &QUICMessage{
		Type:      MessageTypeInvoiceRequest,
		Version:   msg.Version,
		Timestamp: msg.Timestamp,
		RequestID: msg.RequestID,
		Data:      invoiceData,
	}

	ih.logger.Info(fmt.Sprintf("Successfully decrypted invoice request %s from %s", invoiceData.InvoiceID, remotePeerID[:8]), "invoice_handler")

	// Process with the standard handler
	return ih.HandleInvoiceRequest(decryptedMsg, remoteAddr, remotePeerID)
}

// HandleEncryptedInvoiceResponse processes an E2E encrypted invoice response
func (ih *InvoiceHandler) HandleEncryptedInvoiceResponse(msg *QUICMessage, remoteAddr string, remotePeerID string) {
	ih.logger.Debug(fmt.Sprintf("Handling encrypted invoice response from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Check if crypto is available
	if ih.invoiceCrypto == nil {
		ih.logger.Warn("Received encrypted invoice response but crypto manager not initialized", "invoice_handler")
		return
	}

	// Parse encrypted data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal encrypted invoice response: %v", err), "invoice_handler")
		return
	}

	var encryptedData EncryptedInvoiceResponseData
	if err := json.Unmarshal(dataBytes, &encryptedData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse encrypted invoice response: %v", err), "invoice_handler")
		return
	}

	// Validate timestamp for replay protection
	timestamp := time.Unix(encryptedData.Timestamp, 0)
	if time.Since(timestamp) > ih.timestampTolerance {
		ih.logger.Warn(fmt.Sprintf("Encrypted invoice response timestamp too old: %v", timestamp), "invoice_handler")
		return
	}

	// Get sender's public key for signature verification
	senderPubKey, err := ih.getSenderPublicKey(encryptedData.SenderPeerID)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Could not get public key for sender %s: %v", encryptedData.SenderPeerID[:8], err), "invoice_handler")
		return
	}

	// Validate sender matches the remote peer
	if encryptedData.SenderPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Sender mismatch: claimed %s, actual %s", encryptedData.SenderPeerID[:8], remotePeerID[:8]), "invoice_handler")
		return
	}

	// Convert ephemeral public key and nonce to arrays
	var ephemeralPubKey [32]byte
	var nonce [24]byte
	if len(encryptedData.EphemeralPubKey) != 32 || len(encryptedData.Nonce) != 24 {
		ih.logger.Warn("Invalid encryption parameters", "invoice_handler")
		return
	}
	copy(ephemeralPubKey[:], encryptedData.EphemeralPubKey)
	copy(nonce[:], encryptedData.Nonce)

	// Decrypt the response data
	plaintext, err := ih.invoiceCrypto.DecryptInvoice(
		encryptedData.EncryptedPayload,
		nonce,
		ephemeralPubKey,
		encryptedData.Signature,
		senderPubKey,
	)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to decrypt invoice response: %v", err), "invoice_handler")
		return
	}

	// Parse the decrypted response data
	var responseData InvoiceResponseData
	if err := json.Unmarshal(plaintext, &responseData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse decrypted invoice response: %v", err), "invoice_handler")
		return
	}

	// Create a synthetic QUICMessage with the decrypted data
	decryptedMsg := &QUICMessage{
		Type:      MessageTypeInvoiceResponse,
		Version:   msg.Version,
		Timestamp: msg.Timestamp,
		RequestID: msg.RequestID,
		Data:      responseData,
	}

	ih.logger.Info(fmt.Sprintf("Successfully decrypted invoice response for %s from %s", responseData.InvoiceID, remotePeerID[:8]), "invoice_handler")

	// Process with the standard handler
	ih.HandleInvoiceResponse(decryptedMsg, remoteAddr, remotePeerID)
}

// HandleEncryptedInvoiceNotify processes an E2E encrypted invoice notification
func (ih *InvoiceHandler) HandleEncryptedInvoiceNotify(msg *QUICMessage, remoteAddr string, remotePeerID string) {
	ih.logger.Debug(fmt.Sprintf("Handling encrypted invoice notification from %s (%s)", remotePeerID[:8], remoteAddr), "invoice_handler")

	// Check if crypto is available
	if ih.invoiceCrypto == nil {
		ih.logger.Warn("Received encrypted invoice notification but crypto manager not initialized", "invoice_handler")
		return
	}

	// Parse encrypted data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to marshal encrypted invoice notification: %v", err), "invoice_handler")
		return
	}

	var encryptedData EncryptedInvoiceNotifyData
	if err := json.Unmarshal(dataBytes, &encryptedData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse encrypted invoice notification: %v", err), "invoice_handler")
		return
	}

	// Validate timestamp for replay protection
	timestamp := time.Unix(encryptedData.Timestamp, 0)
	if time.Since(timestamp) > ih.timestampTolerance {
		ih.logger.Warn(fmt.Sprintf("Encrypted invoice notification timestamp too old: %v", timestamp), "invoice_handler")
		return
	}

	// Get sender's public key for signature verification
	senderPubKey, err := ih.getSenderPublicKey(encryptedData.SenderPeerID)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Could not get public key for sender %s: %v", encryptedData.SenderPeerID[:8], err), "invoice_handler")
		return
	}

	// Validate sender matches the remote peer
	if encryptedData.SenderPeerID != remotePeerID {
		ih.logger.Warn(fmt.Sprintf("Sender mismatch: claimed %s, actual %s", encryptedData.SenderPeerID[:8], remotePeerID[:8]), "invoice_handler")
		return
	}

	// Convert ephemeral public key and nonce to arrays
	var ephemeralPubKey [32]byte
	var nonce [24]byte
	if len(encryptedData.EphemeralPubKey) != 32 || len(encryptedData.Nonce) != 24 {
		ih.logger.Warn("Invalid encryption parameters", "invoice_handler")
		return
	}
	copy(ephemeralPubKey[:], encryptedData.EphemeralPubKey)
	copy(nonce[:], encryptedData.Nonce)

	// Decrypt the notification data
	plaintext, err := ih.invoiceCrypto.DecryptInvoice(
		encryptedData.EncryptedPayload,
		nonce,
		ephemeralPubKey,
		encryptedData.Signature,
		senderPubKey,
	)
	if err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to decrypt invoice notification: %v", err), "invoice_handler")
		return
	}

	// Parse the decrypted notification data
	var notifyData InvoiceNotifyData
	if err := json.Unmarshal(plaintext, &notifyData); err != nil {
		ih.logger.Warn(fmt.Sprintf("Failed to parse decrypted invoice notification: %v", err), "invoice_handler")
		return
	}

	// Create a synthetic QUICMessage with the decrypted data
	decryptedMsg := &QUICMessage{
		Type:      MessageTypeInvoiceNotify,
		Version:   msg.Version,
		Timestamp: msg.Timestamp,
		RequestID: msg.RequestID,
		Data:      notifyData,
	}

	ih.logger.Info(fmt.Sprintf("Successfully decrypted invoice notification for %s from %s", notifyData.InvoiceID, remotePeerID[:8]), "invoice_handler")

	// Process with the standard handler
	ih.HandleInvoiceNotify(decryptedMsg, remoteAddr, remotePeerID)
}

// getSenderPublicKey retrieves the Ed25519 public key for a peer from known peers
func (ih *InvoiceHandler) getSenderPublicKey(peerID string) (ed25519.PublicKey, error) {
	if ih.knownPeers == nil {
		return nil, fmt.Errorf("known peers manager not initialized")
	}

	peer, err := ih.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil {
		return nil, fmt.Errorf("peer not found: %v", err)
	}
	if peer == nil || len(peer.PublicKey) == 0 {
		return nil, fmt.Errorf("peer %s has no public key", peerID[:8])
	}
	return peer.PublicKey, nil
}

// HandleRelayedEncryptedInvoiceRequest processes an encrypted invoice request via relay
func (ih *InvoiceHandler) HandleRelayedEncryptedInvoiceRequest(msg *QUICMessage, remotePeerID string) *QUICMessage {
	return ih.HandleEncryptedInvoiceRequest(msg, "relay", remotePeerID)
}

// HandleRelayedEncryptedInvoiceResponse processes an encrypted invoice response via relay
func (ih *InvoiceHandler) HandleRelayedEncryptedInvoiceResponse(msg *QUICMessage, remotePeerID string) {
	ih.HandleEncryptedInvoiceResponse(msg, "relay", remotePeerID)
}

// HandleRelayedEncryptedInvoiceNotify processes an encrypted invoice notification via relay
func (ih *InvoiceHandler) HandleRelayedEncryptedInvoiceNotify(msg *QUICMessage, remotePeerID string) {
	ih.HandleEncryptedInvoiceNotify(msg, "relay", remotePeerID)
}
