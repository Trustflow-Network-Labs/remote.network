package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

// SolanaSPLSigner handles Solana SPL token payment signatures
type SolanaSPLSigner struct {
	rpcClient                 *rpc.Client
	associatedTokenProgramID solana.PublicKey
}

// NewSolanaSPLSigner creates a new Solana SPL token signer
func NewSolanaSPLSigner(rpcEndpoint string) *SolanaSPLSigner {
	return NewSolanaSPLSignerWithConfig(rpcEndpoint, nil)
}

// NewSolanaSPLSignerWithConfig creates a new Solana SPL token signer with config
func NewSolanaSPLSignerWithConfig(rpcEndpoint string, config interface{}) *SolanaSPLSigner {
	// Default Associated Token Program ID
	defaultATAProgram := "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"

	// Try to get from config
	ataProgramID := defaultATAProgram
	if cfg, ok := config.(interface {
		GetConfigString(key string, defaultValue string) string
	}); ok {
		ataProgramID = cfg.GetConfigString("solana_associated_token_program", defaultATAProgram)
	}

	ataProgram := solana.MustPublicKeyFromBase58(ataProgramID)

	return &SolanaSPLSigner{
		rpcClient:                 rpc.New(rpcEndpoint),
		associatedTokenProgramID: ataProgram,
	}
}

// CreateSPLTransferTransaction creates a partially-signed SPL token transfer transaction
// compatible with x402 facilitator requirements.
// Returns the base64-encoded serialized transaction ready for x402 payment header
//
// The transaction includes exactly 3 instructions as required by x402:
// 1. SetComputeUnitLimit
// 2. SetComputeUnitPrice
// 3. TransferChecked (SPL token transfer with decimals)
//
// IMPORTANT: The recipient MUST have their Associated Token Account (ATA) for the token already created.
// New wallets receiving SPL tokens for the first time need to initialize their token account first.
// This can be done by:
//   - Sending themselves a tiny amount of the token from another wallet
//   - Using the Solana CLI: spl-token create-account <mint-address>
//   - Using wallet apps that auto-create token accounts
//
// Parameters:
//   - feePayer: The facilitator's public key (will complete signing and pay fees)
//   - clientKey: The client's private key (signs the transaction)
//   - tokenMint: The SPL token mint address
//   - recipient: The payment recipient's public key
//   - amount: The token amount in smallest units
//   - decimals: The token's decimal places
func (s *SolanaSPLSigner) CreateSPLTransferTransaction(
	ctx context.Context,
	feePayer solana.PublicKey,
	clientKey solana.PrivateKey,
	tokenMint solana.PublicKey,
	recipient solana.PublicKey,
	amount uint64,
	decimals uint8,
) (string, error) {
	client := clientKey.PublicKey()

	// Get the client's associated token account for this mint
	sourceATA, _, err := solana.FindAssociatedTokenAddress(client, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to find source ATA: %v", err)
	}

	// Get the recipient's associated token account for this mint
	destinationATA, _, err := solana.FindAssociatedTokenAddress(recipient, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to find destination ATA: %v", err)
	}

	// Get latest blockhash
	latestBlockhash, err := s.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get latest blockhash: %v", err)
	}

	// Build compute budget instructions (required by x402)
	computebudget := solana.MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111")

	// Instruction 1: SetComputeUnitLimit
	cuLimitData := []byte{0x02} // SetComputeUnitLimit discriminator
	cuLimitData = append(cuLimitData, 0x00, 0x09, 0x3d, 0x00) // 400000 units (little-endian u32)
	cuLimitIx := solana.NewInstruction(
		computebudget,
		solana.AccountMetaSlice{},
		cuLimitData,
	)

	// Instruction 2: SetComputeUnitPrice
	cuPriceData := []byte{0x03} // SetComputeUnitPrice discriminator
	cuPriceData = append(cuPriceData, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // 0 micro-lamports (little-endian u64)
	cuPriceIx := solana.NewInstruction(
		computebudget,
		solana.AccountMetaSlice{},
		cuPriceData,
	)

	// Instruction 3: TransferChecked (SPL token transfer with decimals verification)
	transferIx, err := token.NewTransferCheckedInstruction(
		amount,
		decimals,
		sourceATA,
		tokenMint,
		destinationATA,
		client,
		[]solana.PublicKey{}, // No additional signers
	).ValidateAndBuild()
	if err != nil {
		return "", fmt.Errorf("failed to create transfer instruction: %v", err)
	}

	// Create transaction with all 3 instructions
	tx, err := solana.NewTransaction(
		[]solana.Instruction{cuLimitIx, cuPriceIx, transferIx},
		latestBlockhash.Value.Blockhash,
		solana.TransactionPayer(feePayer), // Facilitator will pay fees
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %v", err)
	}

	// Client partially signs the transaction (only signs for their own key, not the fee payer)
	// The facilitator will complete the signing with their fee payer key
	_, err = tx.PartialSign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(client) {
			return &clientKey
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Serialize transaction to bytes
	serialized, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %v", err)
	}

	// Return base64-encoded transaction
	return base64.StdEncoding.EncodeToString(serialized), nil
}

// CreateNativeSOLTransfer creates a signed SOL transfer transaction
// Returns the base64-encoded serialized transaction
func (s *SolanaSPLSigner) CreateNativeSOLTransfer(
	ctx context.Context,
	privateKey solana.PrivateKey,
	recipient solana.PublicKey,
	lamports uint64,
) (string, error) {
	// Get the payer's public key
	payer := privateKey.PublicKey()

	// Get latest blockhash
	recent, err := s.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get latest blockhash: %v", err)
	}

	// Create system transfer instruction
	transferInstruction := system.NewTransferInstruction(
		lamports,
		payer,
		recipient,
	).Build()

	// Create transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{transferInstruction},
		recent.Value.Blockhash,
		solana.TransactionPayer(payer),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %v", err)
	}

	// Sign transaction
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(payer) {
			return &privateKey
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Serialize transaction
	serialized, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %v", err)
	}

	// Return base64-encoded transaction
	return base64.StdEncoding.EncodeToString(serialized), nil
}

// GetSolanaRPCEndpoint returns the appropriate Solana RPC endpoint for a network
func GetSolanaRPCEndpoint(network string) string {
	switch network {
	// CAIP-2 format (full genesis hash)
	case "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": // Mainnet
		return rpc.MainNetBeta_RPC
	case "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": // Devnet
		return rpc.DevNet_RPC
	case "solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z": // Testnet
		return rpc.TestNet_RPC
	// Common aliases
	case "solana:mainnet-beta", "solana:mainnet":
		return rpc.MainNetBeta_RPC
	case "solana:devnet":
		return rpc.DevNet_RPC
	case "solana:testnet":
		return rpc.TestNet_RPC
	default:
		return rpc.DevNet_RPC // Default to devnet
	}
}

// GetRPCEndpoint is deprecated, use GetSolanaRPCEndpoint instead
// Kept for backward compatibility
func GetRPCEndpoint(network string) string {
	return GetSolanaRPCEndpoint(network)
}

// NormalizeSolanaNetwork converts Solana network aliases to full CAIP-2 format
// This is needed for facilitator compatibility which expects full genesis hashes
func NormalizeSolanaNetwork(network string) string {
	switch network {
	// Convert aliases to full CAIP-2 format
	case "solana:mainnet-beta", "solana:mainnet":
		return "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp" // Mainnet
	case "solana:devnet":
		return "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1" // Devnet
	case "solana:testnet":
		return "solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z" // Testnet
	default:
		// Already in full CAIP-2 format or unknown, return as-is
		return network
	}
}

// IsSolanaNetwork checks if a network identifier is a Solana network
func IsSolanaNetwork(network string) bool {
	switch network {
	case "solana:mainnet-beta", "solana:mainnet",
		"solana:devnet",
		"solana:testnet",
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1",
		"solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z":
		return true
	default:
		return false
	}
}

// CheckTokenAccountExists checks if a wallet has an Associated Token Account for a specific token
func (s *SolanaSPLSigner) CheckTokenAccountExists(ctx context.Context, wallet solana.PublicKey, tokenMint solana.PublicKey) (bool, error) {
	// Calculate the Associated Token Account address
	ata, _, err := solana.FindAssociatedTokenAddress(wallet, tokenMint)
	if err != nil {
		return false, fmt.Errorf("failed to find ATA address: %v", err)
	}

	// Query the account
	accountInfo, err := s.rpcClient.GetAccountInfo(ctx, ata)
	if err != nil {
		// Check if error is "account not found" - this is normal for new wallets
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "could not find") {
			// Account doesn't exist - this is not an error
			return false, nil
		}
		// Other RPC errors
		return false, fmt.Errorf("failed to query account: %v", err)
	}

	// Account exists if we got valid account info
	return accountInfo != nil && accountInfo.Value != nil, nil
}

// CreateTokenAccount creates an Associated Token Account for a wallet to receive SPL tokens
// This is a separate transaction from payment and is required before a wallet can receive SPL tokens
//
// Parameters:
//   - wallet: The wallet owner's public key
//   - walletPrivateKey: The wallet's private key (to pay for account creation and sign)
//   - tokenMint: The SPL token mint address (e.g., USDC)
//
// Returns the transaction signature
//
// Note: This requires the wallet to have ~0.002 SOL to pay for account rent.
// On devnet, use a faucet to fund the wallet first: https://faucet.solana.com/
func (s *SolanaSPLSigner) CreateTokenAccount(
	ctx context.Context,
	wallet solana.PublicKey,
	walletPrivateKey solana.PrivateKey,
	tokenMint solana.PublicKey,
) (string, error) {
	// Check if account already exists
	exists, err := s.CheckTokenAccountExists(ctx, wallet, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to check if token account exists: %v", err)
	}
	if exists {
		return "", fmt.Errorf("token account already exists for this wallet and token")
	}

	// Get latest blockhash
	latestBlockhash, err := s.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get latest blockhash: %v", err)
	}

	// Create instruction to initialize Associated Token Account
	createATAIx := associatedtokenaccount.NewCreateInstruction(
		wallet,    // Payer (wallet pays for account creation)
		wallet,    // Wallet owner
		tokenMint, // Token mint
	).Build()

	// Create transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{createATAIx},
		latestBlockhash.Value.Blockhash,
		solana.TransactionPayer(wallet), // Wallet pays fees
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %v", err)
	}

	// Sign transaction with wallet's private key
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(wallet) {
			return &walletPrivateKey
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Send transaction
	signature, err := s.rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %v", err)
	}

	return signature.String(), nil
}

// GetTokenAccountAddress returns the Associated Token Account address for a wallet and token
// This is useful for checking balances or displaying the token account address
func (s *SolanaSPLSigner) GetTokenAccountAddress(wallet solana.PublicKey, tokenMint solana.PublicKey) (string, error) {
	ata, _, err := solana.FindAssociatedTokenAddress(wallet, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to find ATA address: %v", err)
	}
	return ata.String(), nil
}

// CreateTokenAccountTransactionForFacilitator creates a transaction to initialize a token account
// where the facilitator is the payer. Returns the base64-encoded unsigned transaction.
// The facilitator will sign and broadcast this transaction.
//
// This is used to initialize token accounts for new wallets without requiring them to have SOL.
// The facilitator pays the ~0.002 SOL rent for account creation.
//
// Parameters:
//   - feePayer: The facilitator's public key (will sign and pay for account creation)
//   - walletOwner: The wallet owner's public key (who will own the token account)
//   - tokenMint: The SPL token mint address (e.g., USDC)
//
// Returns the base64-encoded serialized transaction (unsigned, ready for facilitator to sign)
func (s *SolanaSPLSigner) CreateTokenAccountTransactionForFacilitator(
	ctx context.Context,
	feePayer solana.PublicKey,
	walletOwner solana.PublicKey,
	tokenMint solana.PublicKey,
) (string, error) {
	// Get latest blockhash
	latestBlockhash, err := s.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get latest blockhash: %v", err)
	}

	// Create instruction to initialize Associated Token Account
	createATAIx := associatedtokenaccount.NewCreateInstruction(
		feePayer,    // Payer (facilitator pays for account creation)
		walletOwner, // Wallet owner
		tokenMint,   // Token mint
	).Build()

	// Create transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{createATAIx},
		latestBlockhash.Value.Blockhash,
		solana.TransactionPayer(feePayer), // Facilitator pays fees
	)
	if err != nil {
		return "", fmt.Errorf("failed to create transaction: %v", err)
	}

	// Serialize transaction to bytes (unsigned - facilitator will sign)
	serialized, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %v", err)
	}

	// Return base64-encoded transaction
	return base64.StdEncoding.EncodeToString(serialized), nil
}
