package payment

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/gagliardetto/solana-go"
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

// CreateSPLTransferTransaction creates a signed SPL token transfer transaction
// Returns the base64-encoded serialized transaction ready for x402 X-PAYMENT header
func (s *SolanaSPLSigner) CreateSPLTransferTransaction(
	ctx context.Context,
	privateKey solana.PrivateKey,
	tokenMint solana.PublicKey,
	recipient solana.PublicKey,
	amount uint64,
) (string, error) {
	// Get the payer's public key
	payer := privateKey.PublicKey()

	// Get the payer's associated token account for this mint
	payerATA, _, err := solana.FindAssociatedTokenAddress(payer, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to find payer ATA: %v", err)
	}

	// Get the recipient's associated token account for this mint
	recipientATA, _, err := solana.FindAssociatedTokenAddress(recipient, tokenMint)
	if err != nil {
		return "", fmt.Errorf("failed to find recipient ATA: %v", err)
	}

	// Get recent blockhash
	recent, err := s.rpcClient.GetRecentBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get recent blockhash: %v", err)
	}

	// Check if recipient's ATA exists, if not, create it
	instructions := []solana.Instruction{}

	// Check if recipient ATA exists
	accountInfo, err := s.rpcClient.GetAccountInfo(ctx, recipientATA)
	if err != nil || accountInfo == nil || accountInfo.Value == nil {
		// Create associated token account for recipient
		// Use the associated token program from config
		createATAInstruction := solana.NewInstruction(
			s.associatedTokenProgramID, // Associated Token Program ID from config
			solana.AccountMetaSlice{
				{PublicKey: payer, IsSigner: true, IsWritable: true},
				{PublicKey: recipientATA, IsSigner: false, IsWritable: true},
				{PublicKey: recipient, IsSigner: false, IsWritable: false},
				{PublicKey: tokenMint, IsSigner: false, IsWritable: false},
				{PublicKey: solana.SystemProgramID, IsSigner: false, IsWritable: false},
				{PublicKey: solana.TokenProgramID, IsSigner: false, IsWritable: false},
			},
			[]byte{}, // Create instruction - empty data
		)
		instructions = append(instructions, createATAInstruction)
	}

	// Create SPL token transfer instruction
	transferInstruction, err := token.NewTransferInstruction(
		amount,
		payerATA,
		recipientATA,
		payer,
		[]solana.PublicKey{}, // No multi-sig
	).ValidateAndBuild()
	if err != nil {
		return "", fmt.Errorf("failed to create transfer instruction: %v", err)
	}
	instructions = append(instructions, transferInstruction)

	// Create transaction
	tx, err := solana.NewTransaction(
		instructions,
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

	// Get recent blockhash
	recent, err := s.rpcClient.GetRecentBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to get recent blockhash: %v", err)
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
