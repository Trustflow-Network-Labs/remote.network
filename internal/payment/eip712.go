package payment

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// EIP712Domain represents the EIP-712 domain separator
type EIP712Domain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract common.Address
}

// X402PaymentAuthorization represents the EIP-712 typed data for x402 payment authorization
type X402PaymentAuthorization struct {
	From        common.Address `json:"from"`
	To          common.Address `json:"to"`
	Value       *big.Int       `json:"value"`
	ValidAfter  *big.Int       `json:"validAfter"`
	ValidBefore *big.Int       `json:"validBefore"`
	Nonce       string         `json:"nonce"`
}

// GetEIP712TypedData creates EIP-712 typed data for x402 payment authorization (ERC-3009 format)
func GetEIP712TypedData(sig *PaymentSignature, chainID int64, tokenName string, tokenVersion string, verifyingContract string) (apitypes.TypedData, error) {
	// Convert amount based on currency decimals
	var multiplier float64
	switch sig.Currency {
	case "USDC", "USDT":
		multiplier = 1e6 // 6 decimals
	case "ETH":
		multiplier = 1e18 // 18 decimals
	default:
		multiplier = 1e6 // Default to 6 decimals for stablecoins
	}

	amountWei := new(big.Float).SetFloat64(sig.Amount * multiplier)
	amountInt, _ := amountWei.Int(nil)

	// Create EIP-712 domain with token contract parameters (ERC-3009 format)
	domain := apitypes.TypedDataDomain{
		Name:              tokenName,                        // e.g., "USD Coin"
		Version:           tokenVersion,                     // e.g., "2"
		ChainId:           math.NewHexOrDecimal256(chainID),
		VerifyingContract: verifyingContract,                // Token contract address
	}

	// Define the EIP-3009 TransferWithAuthorization type
	types := apitypes.Types{
		"EIP712Domain": []apitypes.Type{
			{Name: "name", Type: "string"},
			{Name: "version", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "verifyingContract", Type: "address"},
		},
		"TransferWithAuthorization": []apitypes.Type{
			{Name: "from", Type: "address"},
			{Name: "to", Type: "address"},
			{Name: "value", Type: "uint256"},
			{Name: "validAfter", Type: "uint256"},
			{Name: "validBefore", Type: "uint256"},
			{Name: "nonce", Type: "bytes32"},
		},
	}

	// Create nonce as bytes32 (hash of the nonce string)
	nonceHash := crypto.Keccak256Hash([]byte(sig.Nonce))

	// Create message
	message := apitypes.TypedDataMessage{
		"from":        sig.Sender,
		"to":          sig.Recipient,
		"value":       fmt.Sprintf("%d", amountInt),
		"validAfter":  fmt.Sprintf("%d", sig.Timestamp-3600),
		"validBefore": fmt.Sprintf("%d", sig.Timestamp+3600),
		"nonce":       nonceHash.Hex(),
	}

	typedData := apitypes.TypedData{
		Types:       types,
		PrimaryType: "TransferWithAuthorization", // ERC-3009 standard type name
		Domain:      domain,
		Message:     message,
	}

	return typedData, nil
}

// HashEIP712TypedData hashes the EIP-712 typed data according to the spec
func HashEIP712TypedData(typedData apitypes.TypedData) (common.Hash, error) {
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to hash domain: %v", err)
	}

	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to hash message: %v", err)
	}

	// EIP-712 final hash: keccak256("\x19\x01" ‖ domainSeparator ‖ hashStruct(message))
	rawData := []byte(fmt.Sprintf("\x19\x01%s%s", string(domainSeparator), string(typedDataHash)))
	return crypto.Keccak256Hash(rawData), nil
}

// GetChainIDFromNetwork extracts chain ID from CAIP-2 network identifier
func GetChainIDFromNetwork(network string) (int64, error) {
	// Parse CAIP-2 format: eip155:<chainID>
	if len(network) < 8 || network[:7] != "eip155:" {
		return 0, fmt.Errorf("invalid network format: %s", network)
	}

	chainIDStr := network[7:]
	chainID := new(big.Int)
	chainID, ok := chainID.SetString(chainIDStr, 10)
	if !ok {
		return 0, fmt.Errorf("invalid chain ID: %s", chainIDStr)
	}

	return chainID.Int64(), nil
}

// GetTokenContractAddress returns the token contract address for a given network and currency
// Reads from configuration file with fallback to hardcoded defaults
func GetTokenContractAddress(networkID string, currency string) string {
	// Fallback token contracts (used if config not available)
	tokenContractsFallback := map[string]map[string]string{
		"eip155:84532": {
			"USDC": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		},
		"eip155:8453": {
			"USDC": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		},
		"eip155:11155111": {
			"USDC": "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
		},
		"eip155:1": {
			"USDC": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		},
		// Solana - CAIP-2 format
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": {
			"USDC": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		},
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": {
			"USDC": "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
		},
		// Solana - common aliases
		"solana:mainnet-beta": {
			"USDC": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		},
		"solana:devnet": {
			"USDC": "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
		},
	}

	// Try to get from fallback map
	if networkTokens, ok := tokenContractsFallback[networkID]; ok {
		if contract, ok := networkTokens[currency]; ok {
			return contract
		}
	}
	return ""
}

// GetTokenContractAddressFromConfig returns the token contract address reading from config
func GetTokenContractAddressFromConfig(config interface{}, networkID string, currency string) string {
	// Map network IDs to config key suffixes
	networkConfigMap := map[string]string{
		// EVM networks
		"eip155:84532":    "base_sepolia",
		"eip155:8453":     "base",
		"eip155:11155111": "ethereum_sepolia",
		"eip155:1":        "ethereum",
		// Solana networks - CAIP-2 format
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": "solana",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": "solana_devnet",
		// Solana networks - common aliases
		"solana:mainnet-beta": "solana",
		"solana:devnet":       "solana_devnet",
	}

	networkSuffix, ok := networkConfigMap[networkID]
	if !ok {
		// Fallback to GetTokenContractAddress
		return GetTokenContractAddress(networkID, currency)
	}

	// Build config key: token_{currency}_{network}
	configKey := fmt.Sprintf("token_%s_%s", currency, networkSuffix)

	// Try to get from config if config manager is available
	if cfg, ok := config.(interface {
		GetConfigString(key string, defaultValue string) string
	}); ok {
		value := cfg.GetConfigString(configKey, "")
		if value != "" {
			return value
		}
	}

	// Fallback to hardcoded defaults
	return GetTokenContractAddress(networkID, currency)
}

// GetTokenEIP712Parameters returns the EIP-712 domain parameters for a token contract
// Reads from configuration file with fallback to hardcoded defaults
func GetTokenEIP712Parameters(networkID string, currency string) (name string, version string) {
	// Hardcoded fallback parameters
	type TokenParams struct {
		Name    string
		Version string
	}

	tokenParamsFallback := map[string]map[string]TokenParams{
		"eip155:84532": {
			"USDC": {Name: "USDC", Version: "2"}, // Official EIP-712 domain name from Circle
		},
		"eip155:8453": {
			"USDC": {Name: "USDC", Version: "2"}, // Official EIP-712 domain name from Circle
		},
		"eip155:11155111": {
			"USDC": {Name: "USDC", Version: "2"}, // Official EIP-712 domain name from Circle
		},
		"eip155:1": {
			"USDC": {Name: "USDC", Version: "2"}, // Official EIP-712 domain name from Circle
		},
	}

	if networkTokens, ok := tokenParamsFallback[networkID]; ok {
		if params, ok := networkTokens[currency]; ok {
			return params.Name, params.Version
		}
	}

	// Default fallback - use official EIP-712 domain name from Circle
	return "USDC", "2"
}

// GetTokenEIP712ParametersFromConfig returns the EIP-712 domain parameters reading from config
func GetTokenEIP712ParametersFromConfig(config interface{}, networkID string, currency string) (name string, version string) {
	// Map network IDs to config key suffixes
	networkConfigMap := map[string]string{
		"eip155:84532":    "base_sepolia",
		"eip155:8453":     "base",
		"eip155:11155111": "ethereum_sepolia",
		"eip155:1":        "ethereum",
	}

	networkSuffix, ok := networkConfigMap[networkID]
	if !ok {
		// Fallback to GetTokenEIP712Parameters
		return GetTokenEIP712Parameters(networkID, currency)
	}

	// Build config keys
	nameKey := fmt.Sprintf("token_%s_%s_name", currency, networkSuffix)
	versionKey := fmt.Sprintf("token_%s_%s_version", currency, networkSuffix)

	// Try to get from config if config manager is available
	if cfg, ok := config.(interface {
		GetConfigString(key string, defaultValue string) string
	}); ok {
		nameVal := cfg.GetConfigString(nameKey, "")
		versionVal := cfg.GetConfigString(versionKey, "")
		if nameVal != "" && versionVal != "" {
			return nameVal, versionVal
		}
	}

	// Fallback to hardcoded defaults
	return GetTokenEIP712Parameters(networkID, currency)
}

// SignEIP712 signs payment data using EIP-712 typed structured data signing (ERC-3009 format)
// This format is used by x402.org and other standard x402 facilitators
// Uses hardcoded fallback values - prefer SignEIP712WithConfig when config is available
func SignEIP712(privateKey *ecdsa.PrivateKey, sig *PaymentSignature) (string, error) {
	return SignEIP712WithConfig(nil, privateKey, sig)
}

// Logger interface for logging
type Logger interface {
	Info(msg string, category string)
}

// getEVMRPCEndpoint returns the RPC endpoint for an EVM network
func getEVMRPCEndpoint(network string) string {
	switch network {
	// Base
	case "eip155:8453":
		return "https://mainnet.base.org"
	case "eip155:84532":
		return "https://sepolia.base.org"
	// Ethereum
	case "eip155:1":
		return "https://eth.llamarpc.com"
	case "eip155:11155111": // Sepolia
		return "https://ethereum-sepolia-rpc.publicnode.com"
	// Add more networks as needed
	default:
		return ""
	}
}

// isSmartContractWallet checks if an address is a smart contract
func isSmartContractWallet(address string, network string, logger Logger) bool {
	// Get RPC endpoint for EVM networks
	rpcEndpoint := getEVMRPCEndpoint(network)
	if rpcEndpoint == "" {
		if logger != nil {
			logger.Info(fmt.Sprintf("Smart contract check: no RPC endpoint for network %s", network), "payment")
		}
		return false // Assume EOA if we can't check
	}

	if logger != nil {
		logger.Info(fmt.Sprintf("Smart contract check: checking address %s on network %s (RPC: %s)", address, network, rpcEndpoint), "payment")
	}

	// Connect to blockchain
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcEndpoint)
	if err != nil {
		if logger != nil {
			logger.Info(fmt.Sprintf("Smart contract check: failed to connect to RPC: %v", err), "payment")
		}
		return false // Assume EOA if we can't connect
	}
	defer client.Close()

	// Get contract code
	addr := common.HexToAddress(address)
	code, err := client.CodeAt(ctx, addr, nil)
	if err != nil {
		if logger != nil {
			logger.Info(fmt.Sprintf("Smart contract check: failed to get code: %v", err), "payment")
		}
		return false // Assume EOA if we can't check
	}

	isContract := len(code) > 0
	if logger != nil {
		logger.Info(fmt.Sprintf("Smart contract check: address %s is contract=%v (code length: %d)", address, isContract, len(code)), "payment")
	}

	// If code exists, it's a smart contract
	return isContract
}

// SignEIP712WithConfig signs payment data using EIP-712 with config support
func SignEIP712WithConfig(config interface{}, privateKey *ecdsa.PrivateKey, sig *PaymentSignature) (string, error) {
	return SignEIP712WithConfigAndLogger(config, nil, privateKey, sig)
}

// SignEIP712WithConfigAndLogger signs payment data using EIP-712 with config and logger support
func SignEIP712WithConfigAndLogger(config interface{}, logger Logger, privateKey *ecdsa.PrivateKey, sig *PaymentSignature) (string, error) {
	// Extract chain ID from network
	chainID, err := GetChainIDFromNetwork(sig.Network)
	if err != nil {
		return "", fmt.Errorf("failed to get chain ID: %v", err)
	}

	// Get token contract address for the payment currency from config
	var tokenContract string
	var tokenName, tokenVersion string

	if config != nil {
		tokenContract = GetTokenContractAddressFromConfig(config, sig.Network, sig.Currency)
		tokenName, tokenVersion = GetTokenEIP712ParametersFromConfig(config, sig.Network, sig.Currency)
	} else {
		tokenContract = GetTokenContractAddress(sig.Network, sig.Currency)
		tokenName, tokenVersion = GetTokenEIP712Parameters(sig.Network, sig.Currency)
	}

	if tokenContract == "" {
		return "", fmt.Errorf("no token contract found for %s on %s", sig.Currency, sig.Network)
	}

	// Store EIP-712 domain in the signature for facilitator verification
	sig.EIP712Domain = &X402Domain{
		Name:              tokenName,
		Version:           tokenVersion,
		ChainId:           fmt.Sprintf("%d", chainID),
		VerifyingContract: tokenContract,
	}

	// Create EIP-712 typed data with token contract parameters
	typedData, err := GetEIP712TypedData(sig, chainID, tokenName, tokenVersion, tokenContract)
	if err != nil {
		return "", fmt.Errorf("failed to create typed data: %v", err)
	}

	// Hash the typed data
	hash, err := HashEIP712TypedData(typedData)
	if err != nil {
		return "", fmt.Errorf("failed to hash typed data: %v", err)
	}

	// Sign the hash
	signature, err := crypto.Sign(hash.Bytes(), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign: %v", err)
	}

	// Adjust v value to Ethereum format (27/28) for smart contract compatibility
	// go-ethereum's crypto.Sign returns v as recovery ID (0 or 1)
	// but EIP-3009 ecrecover expects v in Ethereum format (27 or 28)
	if signature[64] < 27 {
		signature[64] += 27
	}

	// Check if sender is a smart contract wallet
	// For smart contract wallets, we need to format the signature differently
	// so the facilitator uses the bytes signature overload instead of v/r/s
	if logger != nil {
		logger.Info(fmt.Sprintf("Checking if sender %s is a smart contract wallet...", sig.Sender), "payment")
	}

	isSmartContract := isSmartContractWallet(sig.Sender, sig.Network, logger)

	if isSmartContract {
		if logger != nil {
			logger.Info(fmt.Sprintf("Sender %s is a smart contract - appending marker byte to signature", sig.Sender), "payment")
		}
		// For smart contract wallets, we need to trigger the facilitator to use
		// receiveWithAuthorization(..., bytes signature) instead of v/r/s version.
		// The facilitator checks: if len(signature) == 65, use v/r/s, else use bytes.
		//
		// For ERC-1271 smart wallets, the signature format is typically:
		// - Standard 65-byte ECDSA signature (r[32], s[32], v[1]) signed by the wallet's OWNER
		// - The smart contract validates it via isValidSignature()
		//
		// We append 0x01 as a marker to make it 66 bytes, triggering the bytes overload.
		// The USDC contract will extract the first 65 bytes (r, s, v) and validate via ERC-1271.
		signature = append(signature, 0x01) // Marker byte for smart contract wallet
		if logger != nil {
			logger.Info(fmt.Sprintf("Signature length after marker: %d bytes", len(signature)), "payment")
		}
	} else {
		if logger != nil {
			logger.Info(fmt.Sprintf("Sender %s is an EOA - using standard 65-byte signature", sig.Sender), "payment")
		}
	}

	// Return hex-encoded signature with 0x prefix (required by x402 facilitators)
	return "0x" + common.Bytes2Hex(signature), nil
}
