package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/ethereum/go-ethereum/crypto"
)

// EscrowManager manages payment escrows for jobs and relay sessions
type EscrowManager struct {
	db         *database.SQLiteManager
	x402Client *X402Client
	logger     *utils.LogsManager
	config     *utils.ConfigManager
}

// NewEscrowManager creates a new escrow manager
func NewEscrowManager(db *database.SQLiteManager, x402Client *X402Client, config *utils.ConfigManager, logger *utils.LogsManager) *EscrowManager {
	return &EscrowManager{
		db:         db,
		x402Client: x402Client,
		logger:     logger,
		config:     config,
	}
}

// GetX402Client returns the X402 client for querying facilitator
func (em *EscrowManager) GetX402Client() *X402Client {
	return em.x402Client
}

// CreateEscrow creates and verifies a payment escrow for a job or relay session
func (em *EscrowManager) CreateEscrow(
	jobExecutionID int64,
	paymentSig *PaymentSignature,
	requiredAmount float64,
) (paymentID int64, err error) {
	// Validate payment signature
	if paymentSig == nil {
		return 0, ErrPaymentRequired
	}

	// Check minimum payment amount (only for job payments in USDC, not for P2P invoices)
	// jobExecutionID = 0 indicates a P2P invoice payment
	isJobPayment := jobExecutionID > 0
	if isJobPayment && paymentSig.Currency == "USDC" {
		minAmount := em.config.GetConfigFloat64("x402_min_payment_amount", 0.01, 0.0, 1000000.0)
		if paymentSig.Amount < minAmount {
			return 0, fmt.Errorf("%w: %.6f < %.6f USDC", ErrInsufficientPayment, paymentSig.Amount, minAmount)
		}
	}

	// For all payments, ensure amount > 0 and meets required amount
	if paymentSig.Amount <= 0 {
		return 0, fmt.Errorf("%w: amount must be greater than 0", ErrInsufficientPayment)
	}

	// Check payment amount meets required amount
	if paymentSig.Amount < requiredAmount {
		return 0, fmt.Errorf("%w: payment %.6f < %.6f required (service price may have changed since workflow creation)",
			ErrInsufficientPayment, paymentSig.Amount, requiredAmount)
	}

	// Log if overpayment detected (price decreased since workflow creation)
	if paymentSig.Amount > requiredAmount {
		em.logger.Info(fmt.Sprintf("Payment overpaid: %.6f > %.6f required (service price may have decreased). Will settle only %.6f at completion.",
			paymentSig.Amount, requiredAmount, requiredAmount), "escrow")
	}

	// Verify payment with facilitator
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use x402 client for payment verification
	var transactionID string
	var isValid bool
	var errorMsg string

	em.logger.Info("Using x402 facilitator for payment verification", "escrow")

	verifyReq, err := em.convertToX402Request(paymentSig, requiredAmount, jobExecutionID)
	if err != nil {
		em.logger.Error(fmt.Sprintf("Failed to convert payment to X402 format: %v", err), "escrow")
		return 0, fmt.Errorf("failed to convert payment: %v", err)
	}

	resp, err := em.x402Client.VerifyPayment(ctx, verifyReq)
	if err != nil {
		em.logger.Error(fmt.Sprintf("Facilitator verification failed: %v", err), "escrow")
		return 0, fmt.Errorf("%w: %v", ErrPaymentVerificationFailed, err)
	}

	// Check both IsValid (X402 format) and Valid (backward compatibility)
	isValid = resp.IsValid || resp.Valid
	if !isValid {
		errorMsg = resp.Error
		if resp.InvalidReason != "" {
			errorMsg = resp.InvalidReason
		}
	}

	// Generate local transaction tracking ID (facilitator doesn't return one from /verify)
	// Use hash of signature + nonce to create unique, deterministic ID
	trackingData := fmt.Sprintf("%s:%s:%d", paymentSig.Signature, paymentSig.Nonce, paymentSig.Timestamp)
	trackingHash := crypto.Keccak256Hash([]byte(trackingData))
	transactionID = trackingHash.Hex()

	em.logger.Info(fmt.Sprintf("Generated tracking ID: %s", transactionID), "escrow")

	// Check validation result
	if !isValid {
		if errorMsg == "" {
			errorMsg = "payment verification failed"
		}
		em.logger.Error(fmt.Sprintf("Payment rejected by facilitator: %s", errorMsg), "escrow")
		return 0, fmt.Errorf("%w: %s", ErrFacilitatorRejected, errorMsg)
	}

	// Create payment record in database
	// For P2P invoices, jobExecutionID will be 0, so we set it to nil
	var jobExecIDPtr *int64
	if jobExecutionID != 0 {
		jobExecIDPtr = &jobExecutionID
	}

	// Serialize metadata to JSON if present (e.g., solana_fee_payer)
	var metadataJSON *string
	if len(paymentSig.Metadata) > 0 {
		metadataBytes, err := json.Marshal(paymentSig.Metadata)
		if err != nil {
			em.logger.Warn(fmt.Sprintf("Failed to serialize payment metadata: %v", err), "escrow")
		} else {
			metadataStr := string(metadataBytes)
			metadataJSON = &metadataStr
		}
	}

	payment := &database.JobPayment{
		JobExecutionID:   jobExecIDPtr,
		WalletID:         paymentSig.Sender, // Use sender address as wallet ID for tracking
		PaymentNetwork:   paymentSig.Network,
		PaymentSender:    paymentSig.Sender,
		PaymentRecipient: paymentSig.Recipient,
		PaymentAmount:    paymentSig.Amount,
		PaymentCurrency:  paymentSig.Currency,
		PaymentNonce:     paymentSig.Nonce,
		PaymentSignature: paymentSig.Signature,
		PaymentTimestamp: paymentSig.Timestamp, // Store original timestamp for settlement
		PaymentMetadata:  metadataJSON,         // Store metadata as JSON
		TransactionID:    &transactionID,
		Status:           "verified",
	}

	paymentID, err = em.db.CreateJobPayment(payment)
	if err != nil {
		em.logger.Error(fmt.Sprintf("Failed to create payment record: %v", err), "escrow")
		return 0, fmt.Errorf("failed to create payment record: %v", err)
	}

	// Update status to verified with timestamp
	if err := em.db.UpdateJobPaymentStatus(paymentID, "verified"); err != nil {
		em.logger.Error(fmt.Sprintf("Failed to update payment status: %v", err), "escrow")
		// Don't fail - payment is already created
	}

	em.logger.Info(fmt.Sprintf("Payment escrow created (ID: %d, Transaction: %s, Amount: %.6f %s)",
		paymentID, transactionID, paymentSig.Amount, paymentSig.Currency), "escrow")

	return paymentID, nil
}

// SettleEscrow settles an escrow on successful job/relay completion
func (em *EscrowManager) SettleEscrow(paymentID int64, actualAmount float64) error {
	// Get payment record
	payment, err := em.db.GetJobPayment(paymentID)
	if err != nil {
		return fmt.Errorf("failed to get payment: %v", err)
	}

	if payment == nil {
		return ErrEscrowNotFound
	}

	// Check status
	if payment.Status == "settled" {
		return ErrEscrowAlreadySettled
	}

	if payment.Status == "refunded" {
		return ErrEscrowAlreadyRefunded
	}

	if payment.Status != "verified" && payment.Status != "pending" {
		return fmt.Errorf("%w: %s", ErrEscrowInvalidState, payment.Status)
	}

	// Settle with facilitator
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use actual amount if provided, otherwise use full payment amount
	settlementAmount := payment.PaymentAmount
	if actualAmount > 0 && actualAmount <= payment.PaymentAmount {
		settlementAmount = actualAmount
	}

	// Reconstruct payment signature from database for settlement
	// Deserialize metadata if present
	var metadata map[string]interface{}
	if payment.PaymentMetadata != nil && *payment.PaymentMetadata != "" {
		if err := json.Unmarshal([]byte(*payment.PaymentMetadata), &metadata); err != nil {
			em.logger.Warn(fmt.Sprintf("Failed to deserialize payment metadata: %v", err), "escrow")
		}
	}

	paymentSig := &PaymentSignature{
		Network:   payment.PaymentNetwork,
		Sender:    payment.PaymentSender,
		Recipient: payment.PaymentRecipient,
		Amount:    settlementAmount,
		Currency:  payment.PaymentCurrency,
		Nonce:     payment.PaymentNonce,
		Signature: payment.PaymentSignature,
		Timestamp: payment.PaymentTimestamp, // Use original timestamp from database
		Metadata:  metadata,                 // Restore metadata for settlement
	}

	// Use x402 client for payment settlement
	em.logger.Info(fmt.Sprintf("Using x402 facilitator for settlement (payment %d)", paymentID), "escrow")

	// Convert payment to x402 format for settlement
	// Use 0 for jobExecutionID if it's nil (P2P payments)
	jobExecID := int64(0)
	if payment.JobExecutionID != nil {
		jobExecID = *payment.JobExecutionID
	}
	settleReq, err := em.convertToX402Request(paymentSig, settlementAmount, jobExecID)
	if err != nil {
		em.logger.Error(fmt.Sprintf("Failed to convert payment to X402 format for settlement: %v", err), "escrow")
		return fmt.Errorf("failed to convert payment: %v", err)
	}

	// Send settlement request to facilitator
	settleResp, err := em.x402Client.SettlePayment(ctx, settleReq)
	if err != nil {
		em.logger.Error(fmt.Sprintf("Failed to settle payment %d: %v", paymentID, err), "escrow")
		return fmt.Errorf("failed to settle payment: %v", err)
	}

	// Update transaction ID with blockchain transaction hash from settlement
	if settleResp.Transaction != "" {
		if payment.TransactionID == nil {
			payment.TransactionID = new(string)
		}
		*payment.TransactionID = settleResp.Transaction
		em.logger.Info(fmt.Sprintf("Settlement successful: blockchain tx=%s", settleResp.Transaction), "escrow")
	}

	// Update database status
	if err := em.db.UpdateJobPaymentStatus(paymentID, "settled"); err != nil {
		em.logger.Error(fmt.Sprintf("Failed to update payment status to settled: %v", err), "escrow")
		return fmt.Errorf("failed to update payment status: %v", err)
	}

	em.logger.Info(fmt.Sprintf("Payment settled (ID: %d, Transaction: %s, Amount: %.6f)",
		paymentID, *payment.TransactionID, settlementAmount), "escrow")

	return nil
}

// RefundEscrow refunds an escrow on job/relay failure
func (em *EscrowManager) RefundEscrow(paymentID int64, reason string) error {
	// Get payment record
	payment, err := em.db.GetJobPayment(paymentID)
	if err != nil {
		return fmt.Errorf("failed to get payment: %v", err)
	}

	if payment == nil {
		return ErrEscrowNotFound
	}

	// Check status
	if payment.Status == "refunded" {
		return ErrEscrowAlreadyRefunded
	}

	if payment.Status == "settled" {
		return ErrEscrowAlreadySettled
	}

	if payment.Status != "verified" && payment.Status != "pending" {
		return fmt.Errorf("%w: %s", ErrEscrowInvalidState, payment.Status)
	}

	// If no transaction ID, can't refund
	if payment.TransactionID == nil || *payment.TransactionID == "" {
		return fmt.Errorf("payment has no transaction ID")
	}

	// Refund with facilitator
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := em.x402Client.RefundPayment(ctx, *payment.TransactionID, payment.PaymentAmount, reason); err != nil {
		em.logger.Error(fmt.Sprintf("Failed to refund payment %d: %v", paymentID, err), "escrow")
		return fmt.Errorf("failed to refund payment: %v", err)
	}

	// Update database status
	if err := em.db.UpdateJobPaymentStatus(paymentID, "refunded"); err != nil {
		em.logger.Error(fmt.Sprintf("Failed to update payment status to refunded: %v", err), "escrow")
		return fmt.Errorf("failed to update payment status: %v", err)
	}

	em.logger.Info(fmt.Sprintf("Payment refunded (ID: %d, Transaction: %s, Reason: %s)",
		paymentID, *payment.TransactionID, reason), "escrow")

	return nil
}

// GetEscrowStatus gets the current status of an escrow
func (em *EscrowManager) GetEscrowStatus(paymentID int64) (string, error) {
	payment, err := em.db.GetJobPayment(paymentID)
	if err != nil {
		return "", fmt.Errorf("failed to get payment: %v", err)
	}

	if payment == nil {
		return "", ErrEscrowNotFound
	}

	return payment.Status, nil
}

// GetEscrowByJobID gets payment info for a job execution
func (em *EscrowManager) GetEscrowByJobID(jobExecutionID int64) (*database.JobPayment, error) {
	return em.db.GetJobPaymentByJobID(jobExecutionID)
}

// GetSignatureFormatForFacilitator returns the signature format for x402 facilitators
// All x402 facilitators use EIP-712 typed structured data signing for EVM chains
func (em *EscrowManager) GetSignatureFormatForFacilitator(facilitatorURL string) SignatureFormat {
	return SignatureFormatEIP712
}

// convertToX402Request converts PaymentSignature to X402 v2 format with nested structure
func (em *EscrowManager) convertToX402Request(paymentSig *PaymentSignature, requiredAmount float64, jobExecutionID int64) (*FacilitatorVerifyRequest, error) {
	// Keep network ID in CAIP-2 format for x402 v2 (e.g., "eip155:84532")
	// Do NOT convert to name like "base-sepolia" - v2 uses CAIP-2
	// For Solana, normalize aliases to full CAIP-2 format (e.g., "solana:devnet" -> "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1")
	networkID := paymentSig.Network
	if networkID == "" {
		return nil, fmt.Errorf("missing network ID")
	}

	// Normalize Solana network aliases to full CAIP-2 format for facilitator compatibility
	if strings.HasPrefix(networkID, "solana:") {
		networkID = NormalizeSolanaNetwork(networkID)
		em.logger.Debug(fmt.Sprintf("Normalized Solana network: %s -> %s", paymentSig.Network, networkID), "escrow")
	}

	// Convert amounts to wei (for ETH) or smallest unit
	// For ETH: multiply by 10^18
	// For USDC: multiply by 10^6
	valueWei := em.convertToWei(paymentSig.Amount, paymentSig.Currency)
	requiredWei := em.convertToWei(requiredAmount, paymentSig.Currency)

	// Check if this is a Solana network
	isSolanaNetwork := strings.HasPrefix(paymentSig.Network, "solana:")

	// Use domain from signature if available (set during EIP-712 signing), otherwise create from config
	var domain *X402Domain
	if paymentSig.EIP712Domain != nil {
		// Use the domain that was created during signing (for EIP-712)
		domain = paymentSig.EIP712Domain
		em.logger.Info(fmt.Sprintf("Using EIP-712 domain from signature: name=%s, version=%s, chainId=%s, contract=%s",
			domain.Name, domain.Version, domain.ChainId, domain.VerifyingContract), "escrow")
	} else if !isSolanaNetwork {
		// Fallback: create domain from config (for EVM networks only)
		chainID, err := GetChainIDFromNetwork(paymentSig.Network)
		if err != nil {
			return nil, fmt.Errorf("failed to get chain ID: %v", err)
		}

		tokenContract := GetTokenContractAddressFromConfig(em.config, paymentSig.Network, paymentSig.Currency)
		if tokenContract == "" {
			return nil, fmt.Errorf("no token contract found for %s on %s", paymentSig.Currency, paymentSig.Network)
		}

		tokenName, tokenVersion := GetTokenEIP712ParametersFromConfig(em.config, paymentSig.Network, paymentSig.Currency)

		domain = &X402Domain{
			Name:              tokenName,
			Version:           tokenVersion,
			ChainId:           fmt.Sprintf("%d", chainID),
			VerifyingContract: tokenContract,
		}
		em.logger.Info(fmt.Sprintf("Created EIP-712 domain from config: name=%s, version=%s, chainId=%s, contract=%s",
			domain.Name, domain.Version, domain.ChainId, domain.VerifyingContract), "escrow")
	} else {
		// Solana networks don't use EIP-712 domains
		em.logger.Info("Skipping EIP-712 domain for Solana network", "escrow")
	}

	// Create payload - different structure for Solana vs EVM
	var payload interface{}

	if isSolanaNetwork {
		// For Solana: payload is just the base64-encoded signed transaction
		// The signature field contains the transaction
		payload = map[string]interface{}{
			"transaction": paymentSig.Signature,
		}
		em.logger.Debug(fmt.Sprintf("Created Solana payload with transaction: %d bytes", len(paymentSig.Signature)), "escrow")
	} else {
		// For EVM: payload has signature + authorization structure
		nonceHash := crypto.Keccak256Hash([]byte(paymentSig.Nonce))

		authorization := &X402Authorization{
			From:        paymentSig.Sender,
			To:          paymentSig.Recipient,
			Value:       valueWei,
			ValidAfter:  fmt.Sprintf("%d", paymentSig.Timestamp-3600), // 1 hour before
			ValidBefore: fmt.Sprintf("%d", paymentSig.Timestamp+3600), // 1 hour after
			Nonce:       nonceHash.Hex(),
		}

		payload = &X402PayloadWrapper{
			Signature:     paymentSig.Signature,
			Authorization: authorization,
		}
		em.logger.Debug("Created EVM payload with authorization", "escrow")
	}

	// Get token contract address for asset field
	tokenContract := GetTokenContractAddressFromConfig(em.config, paymentSig.Network, paymentSig.Currency)

	// Build resource URL and description
	resourceURL := "https://localhost:30069/api/payment"
	description := "Payment for service"
	if jobExecutionID > 0 {
		resourceURL = fmt.Sprintf("https://localhost:30069/api/jobs/%d/payment", jobExecutionID)
		description = fmt.Sprintf("Payment for job execution %d", jobExecutionID)
	} else if paymentSig.Metadata != nil {
		if invoiceID, ok := paymentSig.Metadata["invoice_id"].(string); ok {
			resourceURL = fmt.Sprintf("https://localhost:30069/api/invoices/%s", invoiceID)
			description = fmt.Sprintf("Payment for invoice %s", invoiceID)
		}
	}

	// Create payment requirements (used in both top-level and accepted)
	extra := &PaymentRequirementsExtra{}

	if domain != nil {
		extra.Name = domain.Name
		extra.Version = domain.Version
	}

	// For Solana, extract feePayer from payment signature metadata
	// The feePayer should have been queried from facilitator and added during signing
	if isSolanaNetwork {
		if paymentSig.Metadata != nil {
			if fp, ok := paymentSig.Metadata["solana_fee_payer"].(string); ok {
				extra.FeePayer = fp
				em.logger.Debug(fmt.Sprintf("Added feePayer to Solana payment requirements: %s", fp), "escrow")
			} else {
				em.logger.Warn("Solana payment missing feePayer in metadata - verification may fail", "escrow")
			}
		}
	}

	paymentRequirements := &FacilitatorPaymentRequirements{
		Scheme:            "exact",
		Network:           networkID, // Use CAIP-2 format (e.g., "eip155:84532" or "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1")
		Asset:             tokenContract,
		Amount:            requiredWei, // v2 uses "amount" not "maxAmountRequired"
		PayTo:             paymentSig.Recipient,
		MaxTimeoutSeconds: 60,
		Extra:             extra,
	}

	// Create resource info
	resourceInfo := &X402ResourceInfo{
		URL:         resourceURL,
		Description: description,
		MimeType:    "application/json",
	}

	// Create X402 v2 request with correct nested structure
	req := &FacilitatorVerifyRequest{
		PaymentPayload: &FacilitatorPaymentPayload{
			X402Version: 2, // v2 protocol
			Resource:    resourceInfo,
			Accepted:    paymentRequirements, // What terms were accepted
			Payload:     payload,             // Solana: {transaction: "..."}, EVM: {signature: "...", authorization: {...}}
		},
		PaymentRequirements: paymentRequirements, // What is required
	}

	return req, nil
}

// NOTE: mapNetworkIDToName removed - x402 v2 uses CAIP-2 network IDs directly (e.g., "eip155:84532")
// No need to convert to human-readable names like "base-sepolia"

// convertToWei converts amount to smallest unit (wei for ETH, base units for others)
func (em *EscrowManager) convertToWei(amount float64, currency string) string {
	var multiplier float64
	switch currency {
	case "ETH":
		multiplier = 1e18 // 10^18 wei per ETH
	case "USDC", "USDT":
		multiplier = 1e6 // 10^6 base units per USDC/USDT
	case "SOL":
		multiplier = 1e9 // 10^9 lamports per SOL
	default:
		multiplier = 1e18 // Default to 18 decimals
	}

	// Convert to integer wei value
	weiValue := uint64(amount * multiplier)
	return fmt.Sprintf("%d", weiValue)
}
