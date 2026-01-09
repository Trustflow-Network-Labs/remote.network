package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"

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

	// Normalize both networks to full CAIP-2 format for comparison (handles Solana aliases)
	normalizedWalletNetwork := NormalizeSolanaNetwork(walletNetwork)
	normalizedRequestNetwork := NormalizeSolanaNetwork(network)

	// Verify wallet network matches invoice network
	if normalizedWalletNetwork != normalizedRequestNetwork {
		return "", fmt.Errorf("wallet network %s does not match invoice network %s", walletNetwork, network)
	}

	// Validate that the network is in the allowed list (intersection of facilitator and app supported)
	ctx := context.Background()
	allowedNetworks, err := im.GetAllowedNetworks(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to validate network: %v", err)
	}

	networkAllowed := false
	for _, allowedNet := range allowedNetworks {
		if allowedNet.CAIP2 == network {
			networkAllowed = true
			break
		}
	}
	if !networkAllowed {
		return "", fmt.Errorf("network %s is not supported by both app and facilitator", network)
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
		FromWalletID:      fromWalletID,
		FromWalletAddress: walletAddress,
		Amount:            amount,
		Currency:          currency,
		Network:           network,
		Description:       description,
		Status:            "pending",
		CreatedAt:         time.Now(),
		ExpiresAt:         expiresAt,
	}

	// Verify QUIC peer is available before creating invoice
	if im.quicPeer == nil {
		return "", fmt.Errorf("cannot create invoice: QUIC peer not initialized")
	}

	if err := im.db.CreatePaymentInvoice(invoice); err != nil {
		return "", fmt.Errorf("failed to create invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s created: %s → %s (%.6f %s)",
		invoiceID, fromPeerID[:8], toPeerID[:8], amount, currency), "invoice_manager")

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

	// Send invoice to recipient via QUIC with retry
	if err := im.sendInvoiceRequest(toPeerID, invoiceReqData); err != nil {
		im.logger.Warn(fmt.Sprintf("Failed to send invoice to peer %s after all retries: %v", toPeerID[:8], err), "invoice_manager")

		// Mark invoice as failed so user can see it and take action (delete/resend)
		failureReason := fmt.Sprintf("Failed to deliver after %d retry attempts: %v", im.maxRetries, err)
		if markErr := im.db.MarkInvoiceFailed(invoiceID, failureReason); markErr != nil {
			im.logger.Error(fmt.Sprintf("Failed to mark invoice %s as failed: %v", invoiceID, markErr), "invoice_manager")
		} else {
			im.logger.Info(fmt.Sprintf("Invoice %s marked as failed (delivery failed)", invoiceID), "invoice_manager")
		}

		// Return invoice ID with error so UI can show the failed invoice
		return invoiceID, fmt.Errorf("failed to deliver invoice after %d attempts: %v", im.maxRetries+1, err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s delivered successfully to peer %s", invoiceID, toPeerID[:8]), "invoice_manager")
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

	// Normalize both networks to full CAIP-2 format for comparison (handles Solana aliases)
	normalizedWalletNetwork := NormalizeSolanaNetwork(walletNetwork)
	normalizedInvoiceNetwork := NormalizeSolanaNetwork(invoice.Network)

	// Verify wallet network matches invoice network
	if normalizedWalletNetwork != normalizedInvoiceNetwork {
		return fmt.Errorf("wallet network %s does not match invoice network %s",
			walletNetwork, invoice.Network)
	}

	// Validate that the network is in the allowed list (intersection of facilitator and app supported)
	ctx := context.Background()
	allowedNetworks, err := im.GetAllowedNetworks(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate network: %v", err)
	}

	networkAllowed := false
	for _, allowedNet := range allowedNetworks {
		if allowedNet.CAIP2 == invoice.Network {
			networkAllowed = true
			break
		}
	}
	if !networkAllowed {
		return fmt.Errorf("network %s is not supported by both app and facilitator", invoice.Network)
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

		// Check if recipient has a token account for this currency (e.g., USDC)
		// If not, ask facilitator to create it (facilitator pays ~0.002 SOL)
		recipientHasTokenAccount, err := im.walletManager.CheckSolanaTokenAccountByAddress(
			invoice.FromWalletAddress,
			normalizedNetwork,
			invoice.Currency,
		)
		if err != nil {
			im.logger.Warn(fmt.Sprintf("Failed to check if recipient has token account: %v - proceeding anyway", err), "invoice_manager")
		} else if !recipientHasTokenAccount {
			im.logger.Info(fmt.Sprintf("Recipient %s does not have %s token account, asking facilitator to create it",
				invoice.FromWalletAddress[:8], invoice.Currency), "invoice_manager")

			// Ask facilitator to create token account for recipient
			txSignature, err := im.createTokenAccountViaFacilitator(
				invoice.FromWalletAddress, // Recipient's address
				normalizedNetwork,          // Network
				invoice.Currency,           // Token symbol (e.g., "USDC")
				feePayer,                   // Facilitator's address (will pay)
			)
			if err != nil {
				return fmt.Errorf("failed to create recipient's token account via facilitator: %v", err)
			}

			im.logger.Info(fmt.Sprintf("Facilitator created %s token account for recipient %s (tx: %s)",
				invoice.Currency, invoice.FromWalletAddress[:8], txSignature), "invoice_manager")
		} else {
			im.logger.Debug(fmt.Sprintf("Recipient %s already has %s token account",
				invoice.FromWalletAddress[:8], invoice.Currency), "invoice_manager")
		}
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

	// Update invoice with payment details, payer's wallet ID and address
	if err := im.db.UpdatePaymentInvoiceAccepted(invoiceID, string(paymentSigJSON), paymentID, walletID, walletAddress); err != nil {
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

// DeleteInvoice deletes an invoice (only for failed/expired/rejected invoices)
func (im *InvoiceManager) DeleteInvoice(invoiceID string) error {
	invoice, err := im.db.GetPaymentInvoice(invoiceID)
	if err != nil {
		return fmt.Errorf("invoice not found: %v", err)
	}

	// Only allow deleting failed, expired, or rejected invoices
	if invoice.Status != "failed" && invoice.Status != "expired" && invoice.Status != "rejected" {
		return fmt.Errorf("cannot delete invoice with status: %s", invoice.Status)
	}

	if err := im.db.DeletePaymentInvoice(invoiceID); err != nil {
		return fmt.Errorf("failed to delete invoice: %v", err)
	}

	im.logger.Info(fmt.Sprintf("Invoice %s deleted", invoiceID), "invoice_manager")
	return nil
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

// AllowedNetwork represents a network that is allowed for invoice payments
type AllowedNetwork struct {
	CAIP2       string `json:"caip2"`        // CAIP-2 network identifier (e.g., "eip155:8453")
	DisplayName string `json:"display_name"` // Human-readable name (e.g., "Base Mainnet")
}

// getAppSupportedNetworks returns all networks supported by this application
func getAppSupportedNetworks() []string {
	return []string{
		// Base networks
		"eip155:8453",
		"eip155:84532",
		// Ethereum networks
		"eip155:1",
		"eip155:11155111",
		// Polygon networks
		"eip155:137",
		"eip155:80002",
		// Avalanche networks
		"eip155:43114",
		"eip155:43113",
		// Solana networks
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1",
		"solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z",
	}
}

// GetAllowedNetworks returns networks that are supported by both the facilitator and the app
func (im *InvoiceManager) GetAllowedNetworks(ctx context.Context) ([]AllowedNetwork, error) {
	// Get app-supported networks
	appSupportedNetworks := make(map[string]bool)
	for _, network := range getAppSupportedNetworks() {
		appSupportedNetworks[network] = true
	}

	// Query facilitator for supported networks
	facilitatorSupported, err := im.escrowManager.x402Client.GetSupported(ctx)
	if err != nil {
		im.logger.Warn(fmt.Sprintf("Failed to query facilitator for supported networks: %v", err), "invoice_manager")
		// On error, return only app-supported networks (fail-open approach)
		return im.getAllNetworksWithLabels(appSupportedNetworks), nil
	}

	// Extract unique networks from facilitator response
	facilitatorNetworks := make(map[string]bool)
	for _, kind := range facilitatorSupported.Kinds {
		if kind.Network != "" {
			// Normalize Solana network aliases to full CAIP-2 format
			normalizedNetwork := NormalizeSolanaNetwork(kind.Network)
			facilitatorNetworks[normalizedNetwork] = true
		}
	}

	// Compute intersection: networks supported by BOTH app AND facilitator
	allowedNetworks := make(map[string]bool)
	for network := range appSupportedNetworks {
		if facilitatorNetworks[network] {
			allowedNetworks[network] = true
		}
	}

	im.logger.Debug(fmt.Sprintf("Allowed networks: %d app-supported, %d facilitator-supported, %d allowed",
		len(appSupportedNetworks), len(facilitatorNetworks), len(allowedNetworks)), "invoice_manager")

	return im.getAllNetworksWithLabels(allowedNetworks), nil
}

// getAllNetworksWithLabels converts a map of network identifiers to AllowedNetwork structs with display names
func (im *InvoiceManager) getAllNetworksWithLabels(networks map[string]bool) []AllowedNetwork {
	// Map CAIP-2 identifiers to human-readable names
	displayNames := map[string]string{
		"eip155:8453":                                "Base Mainnet",
		"eip155:84532":                               "Base Sepolia",
		"eip155:1":                                   "Ethereum Mainnet",
		"eip155:11155111":                            "Ethereum Sepolia",
		"eip155:137":                                 "Polygon Mainnet",
		"eip155:80002":                               "Polygon Amoy",
		"eip155:43114":                               "Avalanche C-Chain",
		"eip155:43113":                               "Avalanche Fuji",
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": "Solana Mainnet",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": "Solana Devnet",
		"solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z":  "Solana Testnet",
	}

	result := make([]AllowedNetwork, 0, len(networks))
	for network := range networks {
		displayName := displayNames[network]
		if displayName == "" {
			displayName = network // Fallback to CAIP-2 identifier
		}
		result = append(result, AllowedNetwork{
			CAIP2:       network,
			DisplayName: displayName,
		})
	}

	return result
}

// createTokenAccountViaFacilitator creates a Solana token account for a recipient
// The facilitator signs and broadcasts the transaction, paying for the account creation
func (im *InvoiceManager) createTokenAccountViaFacilitator(
	recipientAddress string,
	network string,
	tokenSymbol string,
	facilitatorAddress string,
) (string, error) {
	// Get token mint address
	tokenMint := GetTokenContractAddressFromConfig(im.config, network, tokenSymbol)
	if tokenMint == "" {
		return "", fmt.Errorf("%s mint not configured for network: %s", tokenSymbol, network)
	}

	// Get RPC endpoint
	rpcURL := GetSolanaRPCEndpoint(network)
	if rpcURL == "" {
		return "", fmt.Errorf("no RPC endpoint configured for network: %s", network)
	}

	// Create signer
	signer := NewSolanaSPLSigner(rpcURL)

	// Parse addresses
	recipientPubKey, err := solana.PublicKeyFromBase58(recipientAddress)
	if err != nil {
		return "", fmt.Errorf("invalid recipient address: %v", err)
	}

	facilitatorPubKey, err := solana.PublicKeyFromBase58(facilitatorAddress)
	if err != nil {
		return "", fmt.Errorf("invalid facilitator address: %v", err)
	}

	tokenMintPubKey, err := solana.PublicKeyFromBase58(tokenMint)
	if err != nil {
		return "", fmt.Errorf("invalid token mint address: %v", err)
	}

	// Create the token account creation transaction (unsigned, facilitator will sign)
	ctx := context.Background()
	unsignedTx, err := signer.CreateTokenAccountTransactionForFacilitator(
		ctx,
		facilitatorPubKey,
		recipientPubKey,
		tokenMintPubKey,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create token account transaction: %v", err)
	}

	// Send to facilitator to sign and broadcast
	// We'll use the x402 client to send this
	txSignature, err := im.escrowManager.GetX402Client().CreateTokenAccount(
		ctx,
		network,
		recipientAddress,
		tokenSymbol,
		unsignedTx,
	)
	if err != nil {
		return "", fmt.Errorf("facilitator failed to create token account: %v", err)
	}

	return txSignature, nil
}
