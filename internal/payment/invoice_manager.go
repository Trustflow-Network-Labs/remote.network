package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

type InvoiceManager struct {
	db             *database.SQLiteManager
	walletManager  *WalletManager
	escrowManager  *EscrowManager
	logger         *utils.LogsManager
	config         *utils.ConfigManager
	localPeerID    string
	quicPeer       interface{} // Will be set after initialization
	maxRetries     int
	retryBackoff   time.Duration
}

func NewInvoiceManager(
	db *database.SQLiteManager,
	walletManager *WalletManager,
	escrowManager *EscrowManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *InvoiceManager {
	maxRetries := config.GetConfigInt("invoice_max_retries", 3, 0, 10)
	retryBackoff := time.Duration(config.GetConfigInt("invoice_retry_backoff_ms", 2000, 500, 10000)) * time.Millisecond

	return &InvoiceManager{
		db:            db,
		walletManager: walletManager,
		escrowManager: escrowManager,
		logger:        logger,
		config:        config,
		maxRetries:    maxRetries,
		retryBackoff:  retryBackoff,
	}
}

// SetDependencies sets circular dependencies
func (im *InvoiceManager) SetDependencies(quicPeer interface{}, localPeerID string) {
	im.quicPeer = quicPeer
	im.localPeerID = localPeerID
}

// CreateInvoice creates a new payment invoice
func (im *InvoiceManager) CreateInvoice(
	fromPeerID string,
	toPeerID string,
	fromWalletID string,
	amount float64,
	currency string,
	network string,
	description string,
	expiresInHours int,
) (string, error) {
	// Generate invoice ID
	invoiceID, err := im.generateInvoiceID()
	if err != nil {
		return "", fmt.Errorf("failed to generate invoice ID: %v", err)
	}

	// Get wallet address
	walletAddress, walletNetwork, err := im.walletManager.GetWalletAddress(fromWalletID)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet address: %v", err)
	}

	// Verify wallet network matches invoice network
	if walletNetwork != network {
		return "", fmt.Errorf("wallet network %s does not match invoice network %s", walletNetwork, network)
	}

	// Calculate expiration
	var expiresAt *time.Time
	if expiresInHours > 0 {
		exp := time.Now().Add(time.Duration(expiresInHours) * time.Hour)
		expiresAt = &exp
	}

	// Create invoice
	invoice := &database.PaymentInvoice{
		InvoiceID:         invoiceID,
		FromPeerID:        fromPeerID,
		ToPeerID:          toPeerID,
		FromWalletAddress: walletAddress,
		Amount:            amount,
		Currency:          currency,
		Network:           network,
		Description:       description,
		Status:            "pending",
		CreatedAt:         time.Now(),
		ExpiresAt:         expiresAt,
	}

	if err := im.db.CreatePaymentInvoice(invoice); err != nil {
		return "", fmt.Errorf("failed to create invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s created: %s → %s (%.6f %s)",
		invoiceID, fromPeerID[:8], toPeerID[:8], amount, currency), "invoice_manager")

	// Send invoice to recipient via QUIC
	if im.quicPeer != nil {
		// Create invoice request data
		invoiceReqData := &InvoiceRequestData{
			InvoiceID:         invoiceID,
			FromPeerID:        fromPeerID,
			ToPeerID:          toPeerID,
			FromWalletAddress: walletAddress,
			Amount:            amount,
			Currency:          currency,
			Network:           network,
			Description:       description,
			ExpiresAt:         0,
			Metadata:          make(map[string]interface{}),
		}
		if expiresAt != nil {
			invoiceReqData.ExpiresAt = expiresAt.Unix()
		}

		// Type assert and send with retry
		if err := im.sendInvoiceRequest(toPeerID, invoiceReqData); err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to send invoice to peer %s after all retries: %v", toPeerID[:8], err), "invoice_manager")

			// Mark invoice as failed permanently
			if markErr := im.db.MarkInvoiceFailed(invoiceID, fmt.Sprintf("Failed to deliver after %d retry attempts: %v", im.maxRetries, err)); markErr != nil {
				im.logger.Error(fmt.Sprintf("Failed to mark invoice %s as failed: %v", invoiceID, markErr), "invoice_manager")
			} else {
				im.logger.Info(fmt.Sprintf("Invoice %s marked as failed (delivery unsuccessful)", invoiceID), "invoice_manager")
			}

			// Return error to inform caller
			return "", fmt.Errorf("invoice created but delivery failed after %d attempts: %v", im.maxRetries+1, err)
		}
	}

	return invoiceID, nil
}

// AcceptInvoice accepts an invoice and creates payment
func (im *InvoiceManager) AcceptInvoice(
	invoiceID string,
	walletID string,
	passphrase string,
) error {
	// Get invoice
	invoice, err := im.db.GetPaymentInvoice(invoiceID)
	if err != nil {
		return fmt.Errorf("invoice not found: %v", err)
	}

	// Validate status
	if invoice.Status != "pending" {
		return fmt.Errorf("invoice is not pending (status: %s)", invoice.Status)
	}

	// Check expiration
	if invoice.ExpiresAt != nil && time.Now().After(*invoice.ExpiresAt) {
		im.db.UpdatePaymentInvoiceStatus(invoiceID, "expired")
		return fmt.Errorf("invoice has expired")
	}

	// Get wallet address first (without decryption)
	walletAddress, walletNetwork, err := im.walletManager.GetWalletAddress(walletID)
	if err != nil {
		return fmt.Errorf("failed to get wallet address: %v", err)
	}

	// Verify wallet network matches invoice network
	if walletNetwork != invoice.Network {
		return fmt.Errorf("wallet network %s does not match invoice network %s",
			walletNetwork, invoice.Network)
	}

	// Verify invoice has recipient wallet address
	if invoice.FromWalletAddress == "" {
		return fmt.Errorf("invoice is missing recipient wallet address (may be from old version)")
	}

	// Determine signature format based on configured facilitator
	facilitatorURL := im.config.GetConfigWithDefault("x402_facilitator_url", "")
	signatureFormat := im.escrowManager.GetSignatureFormatForFacilitator(facilitatorURL)

	im.logger.Debug(fmt.Sprintf("Using signature format %s for facilitator %s", signatureFormat, facilitatorURL), "invoice_manager")

	// Create payment signature using wallet address instead of peer ID
	paymentData := &PaymentData{
		Amount:      invoice.Amount,
		Currency:    invoice.Currency,
		Network:     invoice.Network,
		Recipient:   invoice.FromWalletAddress, // Use wallet address instead of peer ID
		Description: fmt.Sprintf("P2P Invoice: %s", invoice.Description),
		Metadata: map[string]interface{}{
			"invoice_id": invoiceID,
			"type":       "p2p_invoice",
			"from_peer":  invoice.FromPeerID,
			"to_peer":    invoice.ToPeerID,
		},
	}

	// For Solana payments, query facilitator to get the feePayer address
	if IsSolanaNetwork(invoice.Network) {
		// Normalize network to full CAIP-2 format
		normalizedNetwork := NormalizeSolanaNetwork(invoice.Network)

		// Query facilitator for feePayer
		ctx := context.Background()
		feePayer, err := im.escrowManager.GetX402Client().GetSolanaFeePayer(ctx, normalizedNetwork)
		if err != nil {
			return fmt.Errorf("failed to get facilitator fee payer for Solana: %v", err)
		}

		// Add feePayer to metadata so wallet manager can use it
		paymentData.Metadata["solana_fee_payer"] = feePayer
		im.logger.Info(fmt.Sprintf("Using facilitator fee payer for Solana: %s", feePayer), "invoice_manager")
	}

	paymentSig, err := im.walletManager.SignPaymentWithFormat(walletID, paymentData, passphrase, signatureFormat)
	if err != nil {
		return fmt.Errorf("failed to sign payment: %v", err)
	}

	// Serialize payment signature
	paymentSigJSON, err := json.Marshal(paymentSig)
	if err != nil {
		return fmt.Errorf("failed to serialize payment signature: %v", err)
	}

	// Create escrow (job_execution_id = 0 for P2P payments)
	paymentID, err := im.escrowManager.CreateEscrow(0, paymentSig, invoice.Amount)
	if err != nil {
		return fmt.Errorf("failed to create escrow: %v", err)
	}

	// Update invoice with payment details and payer's wallet address
	if err := im.db.UpdatePaymentInvoiceAccepted(invoiceID, string(paymentSigJSON), paymentID, walletAddress); err != nil {
		return fmt.Errorf("failed to update invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s accepted, payment %d created", invoiceID, paymentID), "invoice_manager")

	// Notify sender via QUIC
	if im.quicPeer != nil {
		if err := im.sendInvoiceResponse(invoice.FromPeerID, invoiceID, true, "Invoice accepted and payment initiated"); err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to notify peer of acceptance after all retries: %v", err), "invoice_manager")
			// Note: We don't mark as failed here because the payment was already created
		}
	}

	// Auto-settle immediately for P2P payments
	go im.SettleInvoice(invoiceID)

	return nil
}

// RejectInvoice rejects an invoice
func (im *InvoiceManager) RejectInvoice(invoiceID string, reason string) error {
	invoice, err := im.db.GetPaymentInvoice(invoiceID)
	if err != nil {
		return fmt.Errorf("invoice not found: %v", err)
	}

	if invoice.Status != "pending" {
		return fmt.Errorf("invoice is not pending")
	}

	if err := im.db.UpdatePaymentInvoiceStatus(invoiceID, "rejected"); err != nil {
		return fmt.Errorf("failed to update invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s rejected: %s", invoiceID, reason), "invoice_manager")

	// Notify sender via QUIC
	if im.quicPeer != nil {
		message := "Invoice rejected"
		if reason != "" {
			message = reason
		}
		if err := im.sendInvoiceResponse(invoice.FromPeerID, invoiceID, false, message); err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to notify peer of rejection after all retries: %v", err), "invoice_manager")
			// Note: Invoice is already marked as rejected locally, notification failure is acceptable
		}
	}

	return nil
}

// SettleInvoice settles the escrow for an accepted invoice
func (im *InvoiceManager) SettleInvoice(invoiceID string) error {
	invoice, err := im.db.GetPaymentInvoice(invoiceID)
	if err != nil {
		return fmt.Errorf("invoice not found: %v", err)
	}

	if invoice.Status != "accepted" {
		return fmt.Errorf("invoice is not accepted")
	}

	if invoice.PaymentID == nil {
		return fmt.Errorf("invoice has no payment ID")
	}

	// Settle escrow
	if err := im.escrowManager.SettleEscrow(*invoice.PaymentID, invoice.Amount); err != nil {
		return fmt.Errorf("failed to settle escrow: %v", err)
	}

	// Update invoice status
	if err := im.db.UpdatePaymentInvoiceStatus(invoiceID, "settled"); err != nil {
		return fmt.Errorf("failed to update invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s settled", invoiceID), "invoice_manager")

	// Notify both peers
	if im.quicPeer != nil {
		// Notify the payer (who accepted the invoice)
		if err := im.sendInvoiceNotification(invoice.ToPeerID, invoiceID, "settled", "Payment settled successfully"); err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to notify payer of settlement after all retries: %v", err), "invoice_manager")
		}
		// Notify the payee (who created the invoice)
		if err := im.sendInvoiceNotification(invoice.FromPeerID, invoiceID, "settled", "Payment received"); err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to notify payee of settlement after all retries: %v", err), "invoice_manager")
		}
	}

	return nil
}

// GetInvoice retrieves an invoice
func (im *InvoiceManager) GetInvoice(invoiceID string) (*database.PaymentInvoice, error) {
	return im.db.GetPaymentInvoice(invoiceID)
}

// ListInvoices lists invoices for a peer
func (im *InvoiceManager) ListInvoices(peerID string, status string, limit, offset int) ([]*database.PaymentInvoice, error) {
	return im.db.ListPaymentInvoices(peerID, status, limit, offset)
}

// generateInvoiceID generates a unique invoice ID
func (im *InvoiceManager) generateInvoiceID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "inv_" + hex.EncodeToString(bytes), nil
}

// StartCleanupRoutine starts periodic cleanup of expired invoices
func (im *InvoiceManager) StartCleanupRoutine() {
	ticker := time.NewTicker(6 * time.Hour)
	go func() {
		for range ticker.C {
			count, err := im.db.CleanupExpiredInvoices()
			if err != nil {
				im.logger.Warn(fmt.Sprintf("Failed to cleanup expired invoices: %v", err), "invoice_manager")
			} else if count > 0 {
				im.logger.Info(fmt.Sprintf("Marked %d expired invoices", count), "invoice_manager")
			}
		}
	}()
}

// Helper methods for QUIC message sending

// InvoiceRequestData represents invoice request data for QUIC messages
type InvoiceRequestData struct {
	InvoiceID         string                 `json:"invoice_id"`
	FromPeerID        string                 `json:"from_peer_id"`
	ToPeerID          string                 `json:"to_peer_id"`
	FromWalletAddress string                 `json:"from_wallet_address"`
	Amount            float64                `json:"amount"`
	Currency          string                 `json:"currency"`
	Network           string                 `json:"network"`
	Description       string                 `json:"description"`
	ExpiresAt         int64                  `json:"expires_at,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// QUICInvoiceSender interface for sending invoice messages
type QUICInvoiceSender interface {
	SendInvoiceRequest(toPeerID string, invoiceData *InvoiceRequestData) error
	SendInvoiceResponse(toPeerID string, invoiceID string, accepted bool, message string) error
	SendInvoiceNotification(toPeerID string, invoiceID string, status string, message string) error
}

func (im *InvoiceManager) sendInvoiceRequest(toPeerID string, invoiceData *InvoiceRequestData) error {
	if quicPeer, ok := im.quicPeer.(QUICInvoiceSender); ok {
		return quicPeer.SendInvoiceRequest(toPeerID, invoiceData)
	}
	return fmt.Errorf("quic peer does not implement invoice sending")
}

func (im *InvoiceManager) sendInvoiceResponse(toPeerID string, invoiceID string, accepted bool, message string) error {
	if quicPeer, ok := im.quicPeer.(QUICInvoiceSender); ok {
		return quicPeer.SendInvoiceResponse(toPeerID, invoiceID, accepted, message)
	}
	return fmt.Errorf("quic peer does not implement invoice sending")
}

func (im *InvoiceManager) sendInvoiceNotification(toPeerID string, invoiceID string, status string, message string) error {
	if quicPeer, ok := im.quicPeer.(QUICInvoiceSender); ok {
		return quicPeer.SendInvoiceNotification(toPeerID, invoiceID, status, message)
	}
	return fmt.Errorf("quic peer does not implement invoice sending")
}
