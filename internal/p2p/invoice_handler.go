package p2p

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// InvoiceHandler handles incoming invoice-related QUIC messages
type InvoiceHandler struct {
	db           *database.SQLiteManager
	logger       *utils.LogsManager
	peerID       string
	eventEmitter EventEmitter // For WebSocket notifications
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
		db:           db,
		logger:       logger,
		peerID:       peerID,
		eventEmitter: eventEmitter,
	}
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
		// Update invoice status to rejected
		if err := ih.db.UpdatePaymentInvoiceStatus(responseData.InvoiceID, "rejected"); err != nil {
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
