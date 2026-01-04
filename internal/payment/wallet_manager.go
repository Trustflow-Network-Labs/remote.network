package payment

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/mr-tron/base58"
	"golang.org/x/crypto/scrypt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// WalletManager manages multiple cryptocurrency wallets
type WalletManager struct {
	walletsDir string // From utils.GetAppPaths("").DataDir + "/wallets"
	wallets    map[string]*Wallet  // walletID -> Wallet
	mu         sync.RWMutex
	paths      *utils.AppPaths
	config     *utils.ConfigManager
	logger     *utils.LogsManager
}

// Wallet represents a cryptocurrency wallet
type Wallet struct {
	ID         string `json:"id"`
	Network    string `json:"network"`    // "eip155:8453" (Base), "eip155:84532" (Base Sepolia), etc.
	Address    string `json:"address"`
	PrivateKey []byte `json:"private_key"` // Encrypted
	CreatedAt  int64  `json:"created_at"`
}

// walletFile represents the encrypted wallet file format
type walletFile struct {
	ID              string `json:"id"`
	Network         string `json:"network"`
	Address         string `json:"address"`
	EncryptedKey    string `json:"encrypted_key"` // Hex-encoded encrypted private key
	Salt            string `json:"salt"`           // Hex-encoded salt for key derivation
	Nonce           string `json:"nonce"`          // Hex-encoded nonce for AES-GCM
	CreatedAt       int64  `json:"created_at"`
}

// NewWalletManager creates a new wallet manager using AppPaths (like keystore)
func NewWalletManager(config *utils.ConfigManager) (*WalletManager, error) {
	paths := utils.GetAppPaths("")
	walletsDir := filepath.Join(paths.DataDir, "wallets")

	// Create wallets directory if it doesn't exist
	if err := os.MkdirAll(walletsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallets directory: %v", err)
	}

	wm := &WalletManager{
		walletsDir: walletsDir,
		wallets:    make(map[string]*Wallet),
		paths:      paths,
		config:     config,
		logger:     utils.NewLogsManager(config),
	}

	// Load existing wallets
	if err := wm.loadWallets(); err != nil {
		return nil, fmt.Errorf("failed to load existing wallets: %v", err)
	}

	return wm, nil
}

// CreateWallet creates a new wallet for the specified network
func (wm *WalletManager) CreateWallet(network string, passphrase string) (*Wallet, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Validate network
	if !isValidNetwork(network) {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	// Generate wallet based on network type
	var wallet *Wallet
	var err error

	if strings.HasPrefix(network, "eip155:") {
		// EVM-compatible chain (Ethereum, Base, etc.)
		wallet, err = wm.createEVMWallet(network)
	} else if strings.HasPrefix(network, "solana:") {
		// Solana network
		wallet, err = wm.createSolanaWallet(network)
	} else {
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %v", err)
	}

	// Encrypt and save wallet
	if err := wm.saveWallet(wallet, passphrase); err != nil {
		return nil, fmt.Errorf("failed to save wallet: %v", err)
	}

	// Store in memory (without private key for security)
	wm.wallets[wallet.ID] = &Wallet{
		ID:        wallet.ID,
		Network:   wallet.Network,
		Address:   wallet.Address,
		CreatedAt: wallet.CreatedAt,
	}

	return wallet, nil
}

// createEVMWallet creates a new EVM-compatible wallet
func (wm *WalletManager) createEVMWallet(network string) (*Wallet, error) {
	// Generate new ECDSA private key
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	// Derive address from public key
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("failed to cast public key to ECDSA")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA).Hex()

	// Generate wallet ID (first 8 chars of address + timestamp)
	walletID := fmt.Sprintf("%s-%d", strings.ToLower(address[2:10]), time.Now().Unix())

	wallet := &Wallet{
		ID:         walletID,
		Network:    network,
		Address:    address,
		PrivateKey: crypto.FromECDSA(privateKey),
		CreatedAt:  time.Now().Unix(),
	}

	return wallet, nil
}

// createSolanaWallet creates a new Solana wallet for the specified network
func (wm *WalletManager) createSolanaWallet(network string) (*Wallet, error) {
	// Generate Ed25519 keypair using solana-go
	account := solana.NewWallet()
	privateKey := account.PrivateKey
	publicKey := account.PublicKey()

	// Derive Solana address (base58 encoded)
	address := publicKey.String()

	// Generate wallet ID (same pattern as EVM)
	walletID := fmt.Sprintf("%s_%d", address[:8], time.Now().Unix())

	wallet := &Wallet{
		ID:         walletID,
		Network:    network,
		Address:    address,
		PrivateKey: privateKey,
		CreatedAt:  time.Now().Unix(),
	}

	return wallet, nil
}

// ImportWallet imports an existing wallet from a private key
func (wm *WalletManager) ImportWallet(privateKeyHex string, network string, passphrase string) (*Wallet, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Validate network
	if !isValidNetwork(network) {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	var wallet *Wallet
	var err error

	if strings.HasPrefix(network, "eip155:") {
		// EVM-compatible chain
		// Remove 0x prefix if present for EVM
		privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
		wallet, err = wm.importEVMWallet(privateKeyHex, network)
	} else if strings.HasPrefix(network, "solana:") {
		// Solana network (supports base58 or hex)
		wallet, err = wm.importSolanaWallet(privateKeyHex, network)
	} else {
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to import wallet: %v", err)
	}

	// Encrypt and save wallet
	if err := wm.saveWallet(wallet, passphrase); err != nil {
		return nil, fmt.Errorf("failed to save wallet: %v", err)
	}

	// Store in memory (without private key for security)
	wm.wallets[wallet.ID] = &Wallet{
		ID:        wallet.ID,
		Network:   wallet.Network,
		Address:   wallet.Address,
		CreatedAt: wallet.CreatedAt,
	}

	return wallet, nil
}

// importEVMWallet imports an EVM wallet from private key
func (wm *WalletManager) importEVMWallet(privateKeyHex string, network string) (*Wallet, error) {
	// Decode private key
	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %v", err)
	}

	// Parse ECDSA private key
	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid ECDSA private key: %v", err)
	}

	// Derive address
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("failed to cast public key to ECDSA")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA).Hex()

	// Generate wallet ID
	walletID := fmt.Sprintf("%s-%d", strings.ToLower(address[2:10]), time.Now().Unix())

	wallet := &Wallet{
		ID:         walletID,
		Network:    network,
		Address:    address,
		PrivateKey: privateKeyBytes,
		CreatedAt:  time.Now().Unix(),
	}

	return wallet, nil
}

// importSolanaWallet imports a Solana wallet from private key
func (wm *WalletManager) importSolanaWallet(privateKeyStr string, network string) (*Wallet, error) {
	// Decode private key (base58, hex, or JSON byte array)
	var privateKey []byte
	var err error

	// Try base58 first (standard Solana format)
	decoded, err := base58.Decode(privateKeyStr)
	if err == nil && len(decoded) == 64 {
		privateKey = decoded
	} else if strings.HasPrefix(privateKeyStr, "[") && strings.HasSuffix(privateKeyStr, "]") {
		// Try JSON byte array format (Solflare/Phantom export format)
		var byteArray []byte
		if err := json.Unmarshal([]byte(privateKeyStr), &byteArray); err == nil && len(byteArray) == 64 {
			privateKey = byteArray
		} else {
			return nil, errors.New("invalid Solana private key format (JSON array must be 64 bytes)")
		}
	} else {
		// Try hex format
		decoded, err = hex.DecodeString(strings.TrimPrefix(privateKeyStr, "0x"))
		if err != nil || len(decoded) != 64 {
			return nil, errors.New("invalid Solana private key format (expected 64 bytes in base58, hex, or JSON array)")
		}
		privateKey = decoded
	}

	// Derive public key and address from private key
	wallet := solana.PrivateKey(privateKey)
	publicKey := wallet.PublicKey()
	address := publicKey.String()

	// Generate wallet ID
	walletID := fmt.Sprintf("%s_%d", address[:8], time.Now().Unix())

	return &Wallet{
		ID:         walletID,
		Network:    network,
		Address:    address,
		PrivateKey: privateKey,
		CreatedAt:  time.Now().Unix(),
	}, nil
}

// ListWallets returns all available wallets (without private keys)
func (wm *WalletManager) ListWallets() []*Wallet {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	wallets := make([]*Wallet, 0, len(wm.wallets))
	for _, wallet := range wm.wallets {
		wallets = append(wallets, &Wallet{
			ID:        wallet.ID,
			Network:   wallet.Network,
			Address:   wallet.Address,
			CreatedAt: wallet.CreatedAt,
		})
	}

	return wallets
}

// GetWalletAddress retrieves a wallet's address without requiring passphrase
func (wm *WalletManager) GetWalletAddress(walletID string) (string, string, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	wallet, exists := wm.wallets[walletID]
	if !exists {
		return "", "", ErrWalletNotFound
	}

	return wallet.Address, wallet.Network, nil
}

// FindWalletByAddress finds a wallet ID by its address and network
func (wm *WalletManager) FindWalletByAddress(address string, network string) (string, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	for _, wallet := range wm.wallets {
		if wallet.Address == address && wallet.Network == network {
			return wallet.ID, nil
		}
	}

	return "", fmt.Errorf("no wallet found for address %s on network %s", address, network)
}

// GetWallet retrieves and decrypts a wallet by ID
func (wm *WalletManager) GetWallet(walletID string, passphrase string) (*Wallet, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// Check if wallet exists
	if _, exists := wm.wallets[walletID]; !exists {
		return nil, ErrWalletNotFound
	}

	// Load and decrypt wallet file
	walletPath := filepath.Join(wm.walletsDir, walletID+".json")
	return wm.loadAndDecryptWallet(walletPath, passphrase)
}

// SignPayment signs payment data using the specified wallet (defaults to EIP-712)
func (wm *WalletManager) SignPayment(walletID string, paymentData *PaymentData, passphrase string) (*PaymentSignature, error) {
	// Default to EIP-712 (standard x402 format)
	return wm.SignPaymentWithFormat(walletID, paymentData, passphrase, SignatureFormatEIP712)
}

// SignPaymentWithFormat signs payment data using the specified wallet and signature format
func (wm *WalletManager) SignPaymentWithFormat(walletID string, paymentData *PaymentData, passphrase string, format SignatureFormat) (*PaymentSignature, error) {
	// Get wallet with decrypted private key
	wallet, err := wm.GetWallet(walletID, passphrase)
	if err != nil {
		return nil, err
	}

	// Validate currency/network match
	if !strings.HasPrefix(paymentData.Network, strings.Split(wallet.Network, ":")[0]+":") {
		return nil, ErrNetworkMismatch
	}

	// Generate nonce
	nonce := generateNonce()

	// Get x402 version from config (default to v2)
	x402Version := "v2"
	if wm.config != nil {
		x402Version = wm.config.GetConfigWithDefault("x402_version", "v2")
	}

	// Create payment signature
	sig := &PaymentSignature{
		Version:         x402Version,
		Network:         paymentData.Network,
		Sender:          wallet.Address,
		Recipient:       paymentData.Recipient,
		Amount:          paymentData.Amount,
		Currency:        paymentData.Currency,
		Nonce:           nonce,
		Timestamp:       time.Now().Unix(),
		Metadata:        paymentData.Metadata,
		SignatureFormat: format,
	}

	// Sign the payment data based on network and format
	if strings.HasPrefix(wallet.Network, "eip155:") {
		// EVM signature - choose format based on parameter
		signature, err := wm.signEVMPaymentWithFormat(wallet, sig, format)
		if err != nil {
			return nil, fmt.Errorf("failed to sign payment: %v", err)
		}
		sig.Signature = signature
	} else if strings.HasPrefix(wallet.Network, "solana:") {
		// Solana SPL token transaction
		signature, err := wm.signSolanaSPLPayment(wallet, sig)
		if err != nil {
			return nil, fmt.Errorf("failed to sign payment: %v", err)
		}
		sig.Signature = signature
	} else {
		return nil, fmt.Errorf("unsupported network for signing: %s", wallet.Network)
	}

	return sig, nil
}

// DeleteWallet removes a wallet from storage after passphrase verification
func (wm *WalletManager) DeleteWallet(walletID string, passphrase string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Check if wallet exists
	_, exists := wm.wallets[walletID]
	if !exists {
		return fmt.Errorf("wallet %s not found", walletID)
	}

	// Verify passphrase by attempting to decrypt
	walletPath := filepath.Join(wm.walletsDir, walletID+".json")
	_, err := wm.loadAndDecryptWallet(walletPath, passphrase)
	if err != nil {
		return fmt.Errorf("invalid passphrase: %v", err)
	}

	// Remove wallet from storage
	if err := os.Remove(walletPath); err != nil {
		return fmt.Errorf("failed to delete wallet file: %v", err)
	}

	// Remove from in-memory cache
	delete(wm.wallets, walletID)

	return nil
}

// GetBalance queries blockchain for wallet balance
func (wm *WalletManager) GetBalance(walletID string) (*WalletBalance, error) {
	wm.mu.RLock()
	wallet, exists := wm.wallets[walletID]
	wm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("wallet %s not found", walletID)
	}

	// Determine network type
	if strings.HasPrefix(wallet.Network, "eip155:") {
		return wm.getEVMBalance(walletID, wallet)
	} else if strings.HasPrefix(wallet.Network, "solana:") {
		return wm.getSolanaBalance(walletID, wallet)
	}

	return nil, fmt.Errorf("unsupported network: %s", wallet.Network)
}

// getEVMBalance queries ERC-20 USDC token balance on EVM chains
func (wm *WalletManager) getEVMBalance(walletID string, wallet *Wallet) (*WalletBalance, error) {
	rpcURL := wm.getRPCEndpoint(wallet.Network)
	if rpcURL == "" {
		return nil, fmt.Errorf("no RPC endpoint configured for network: %s", wallet.Network)
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %v", err)
	}
	defer client.Close()

	// Get USDC token contract address from config
	usdcContract := GetTokenContractAddressFromConfig(wm.config, wallet.Network, "USDC")
	if usdcContract == "" {
		return nil, fmt.Errorf("USDC contract not configured for network: %s", wallet.Network)
	}

	// ERC-20 balanceOf(address) function signature
	// balanceOf(address) -> 0x70a08231
	balanceOfHash := crypto.Keccak256Hash([]byte("balanceOf(address)"))
	balanceOfSignature := balanceOfHash[:4]

	// Encode wallet address as bytes32 (left-padded)
	walletAddr := common.HexToAddress(wallet.Address)
	paddedAddress := common.LeftPadBytes(walletAddr.Bytes(), 32)

	// Combine signature + padded address
	callData := append(balanceOfSignature, paddedAddress...)

	// Create call message
	msg := ethereum.CallMsg{
		To:   &common.Address{},
		Data: callData,
	}
	// Set the contract address
	contractAddr := common.HexToAddress(usdcContract)
	msg.To = &contractAddr

	// Call contract
	result, err := client.CallContract(context.Background(), msg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query USDC balance: %v", err)
	}

	// Parse result as uint256
	balance := new(big.Int).SetBytes(result)

	// Convert to USDC (6 decimals)
	balanceFloat := new(big.Float).SetInt(balance)
	balanceFloat.Quo(balanceFloat, big.NewFloat(1e6))
	balanceValue, _ := balanceFloat.Float64()

	return &WalletBalance{
		WalletID:  walletID,
		Network:   wallet.Network,
		Address:   wallet.Address,
		Balance:   balanceValue,
		Currency:  "USDC",
		UpdatedAt: time.Now().Unix(),
	}, nil
}

// getSolanaBalance queries SPL USDC token balance on Solana
func (wm *WalletManager) getSolanaBalance(walletID string, wallet *Wallet) (*WalletBalance, error) {
	rpcURL := wm.getRPCEndpoint(wallet.Network)
	if rpcURL == "" {
		return nil, fmt.Errorf("no RPC endpoint configured for network: %s", wallet.Network)
	}
	wm.logger.Debug(fmt.Sprintf("getSolanaBalance - wallet=%s, network=%s, address=%s, rpcURL=%s", walletID, wallet.Network, wallet.Address, rpcURL), "wallet_manager")

	client := rpc.New(rpcURL)

	pubKey, err := solana.PublicKeyFromBase58(wallet.Address)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Invalid Solana address: %v", err), "wallet_manager")
		return nil, fmt.Errorf("invalid Solana address: %v", err)
	}

	// Get USDC token mint address from config
	usdcMint := GetTokenContractAddressFromConfig(wm.config, wallet.Network, "USDC")
	wm.logger.Debug(fmt.Sprintf("USDC mint from config: %s", usdcMint), "wallet_manager")
	if usdcMint == "" {
		wm.logger.Error(fmt.Sprintf("USDC mint not configured for network: %s", wallet.Network), "wallet_manager")
		return nil, fmt.Errorf("USDC mint not configured for network: %s", wallet.Network)
	}

	tokenMint, err := solana.PublicKeyFromBase58(usdcMint)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Invalid USDC mint address: %v", err), "wallet_manager")
		return nil, fmt.Errorf("invalid USDC mint address: %v", err)
	}

	// Find associated token account for USDC
	ata, _, err := solana.FindAssociatedTokenAddress(pubKey, tokenMint)
	if err != nil {
		wm.logger.Error(fmt.Sprintf("Failed to find associated token account: %v", err), "wallet_manager")
		return nil, fmt.Errorf("failed to find associated token account: %v", err)
	}
	wm.logger.Debug(fmt.Sprintf("Associated token account: %s", ata.String()), "wallet_manager")

	// Query token account balance using getTokenAccountBalance RPC method
	balance, err := client.GetTokenAccountBalance(context.Background(), ata, rpc.CommitmentFinalized)
	wm.logger.Debug(fmt.Sprintf("GetTokenAccountBalance result - error=%v, balance=%+v", err, balance), "wallet_manager")
	if err != nil {
		// Account might not exist (zero balance)
		// Check if it's a "could not find account" error
		if strings.Contains(err.Error(), "could not find account") || strings.Contains(err.Error(), "Account does not exist") {
			return &WalletBalance{
				WalletID:  walletID,
				Network:   wallet.Network,
				Address:   wallet.Address,
				Balance:   0,
				Currency:  "USDC",
				UpdatedAt: time.Now().Unix(),
			}, nil
		}
		return nil, fmt.Errorf("failed to query USDC balance: %v", err)
	}

	// Parse balance from UiAmount (already converted to decimals)
	balanceValue := float64(0)
	if balance.Value.UiAmount != nil {
		balanceValue = *balance.Value.UiAmount
	}

	return &WalletBalance{
		WalletID:  walletID,
		Network:   wallet.Network,
		Address:   wallet.Address,
		Balance:   balanceValue,
		Currency:  "USDC",
		UpdatedAt: time.Now().Unix(),
	}, nil
}

// getRPCEndpoint returns RPC URL for network
func (wm *WalletManager) getRPCEndpoint(network string) string {
	// Default RPC endpoints
	defaults := map[string]string{
		// EVM networks
		"eip155:8453":     "https://mainnet.base.org",
		"eip155:84532":    "https://sepolia.base.org",
		"eip155:1":        "https://eth.llamarpc.com",
		"eip155:11155111": "https://ethereum-sepolia-rpc.publicnode.com",
		// Solana networks - CAIP-2 format (full genesis hash)
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": "https://api.mainnet-beta.solana.com",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": "https://api.devnet.solana.com",
		// Solana networks - common aliases
		"solana:mainnet-beta": "https://api.mainnet-beta.solana.com",
		"solana:devnet":       "https://api.devnet.solana.com",
		"solana:testnet":      "https://api.testnet.solana.com",
	}

	if endpoint, ok := defaults[network]; ok {
		return endpoint
	}

	return ""
}

// getNativeCurrency returns native currency symbol for network
func (wm *WalletManager) getNativeCurrency(network string) string {
	if strings.HasPrefix(network, "eip155:") {
		return "ETH"
	} else if strings.HasPrefix(network, "solana:") {
		return "SOL"
	}
	return "UNKNOWN"
}

// signEVMPayment signs payment data using an EVM wallet (EIP-191 format for backward compatibility)
func (wm *WalletManager) signEVMPayment(wallet *Wallet, sig *PaymentSignature) (string, error) {
	return wm.signEVMPaymentWithFormat(wallet, sig, SignatureFormatEIP191)
}

// signEVMPaymentWithFormat signs payment data using an EVM wallet with specified format
func (wm *WalletManager) signEVMPaymentWithFormat(wallet *Wallet, sig *PaymentSignature, format SignatureFormat) (string, error) {
	// Parse private key
	privateKey, err := crypto.ToECDSA(wallet.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %v", err)
	}

	// Choose signing method based on format
	switch format {
	case SignatureFormatEIP712:
		// Use EIP-712 typed structured data signing (x402.org, standard x402)
		return SignEIP712WithConfigAndLogger(wm.config, wm.logger, privateKey, sig)
	case SignatureFormatEIP191:
		// Use EIP-191 personal message signing (PayAI)
		return SignEIP191(privateKey, sig)
	default:
		return "", fmt.Errorf("unsupported signature format: %s", format)
	}
}

// signSolanaSPLPayment creates a signed SPL token transfer transaction
// Returns base64-encoded serialized transaction ready for x402 X-PAYMENT header
func (wm *WalletManager) signSolanaSPLPayment(wallet *Wallet, sig *PaymentSignature) (string, error) {
	// Get RPC endpoint for the network
	rpcEndpoint := GetSolanaRPCEndpoint(sig.Network)

	// Create SPL signer with config
	signer := NewSolanaSPLSignerWithConfig(rpcEndpoint, wm.config)

	// Parse addresses
	recipient, err := solana.PublicKeyFromBase58(sig.Recipient)
	if err != nil {
		return "", fmt.Errorf("invalid recipient address: %v", err)
	}

	// Get token mint address for the currency from config
	tokenMintStr := GetTokenContractAddressFromConfig(wm.config, sig.Network, sig.Currency)
	if tokenMintStr == "" {
		return "", fmt.Errorf("unsupported currency %s on network %s", sig.Currency, sig.Network)
	}

	tokenMint, err := solana.PublicKeyFromBase58(tokenMintStr)
	if err != nil {
		return "", fmt.Errorf("invalid token mint address: %v", err)
	}

	// Get facilitator fee payer address from payment metadata
	// This should be provided by querying the facilitator's /supported endpoint
	feePayerStr := ""
	if sig.Metadata != nil {
		if fp, ok := sig.Metadata["solana_fee_payer"].(string); ok {
			feePayerStr = fp
		}
	}

	if feePayerStr == "" {
		return "", fmt.Errorf("solana_fee_payer not provided in payment metadata - this should be queried from facilitator's /supported endpoint")
	}

	feePayer, err := solana.PublicKeyFromBase58(feePayerStr)
	if err != nil {
		return "", fmt.Errorf("invalid facilitator fee payer address: %v", err)
	}

	// Get token decimals and convert amount
	var decimals uint8
	var amountSmallest uint64

	switch sig.Currency {
	case "USDC", "USDT":
		decimals = 6
		amountSmallest = uint64(sig.Amount * 1e6)
	case "SOL":
		decimals = 9
		amountSmallest = uint64(sig.Amount * 1e9)
	default:
		decimals = 6 // Default to 6 decimals
		amountSmallest = uint64(sig.Amount * 1e6)
	}

	// Create and sign the transaction
	privateKey := solana.PrivateKey(wallet.PrivateKey)
	ctx := context.Background()

	if sig.Currency == "SOL" {
		// Native SOL transfer
		return signer.CreateNativeSOLTransfer(ctx, privateKey, recipient, amountSmallest)
	} else {
		// SPL token transfer (USDC, etc.) - x402 compatible with 3 instructions
		return signer.CreateSPLTransferTransaction(ctx, feePayer, privateKey, tokenMint, recipient, amountSmallest, decimals)
	}
}

// ==================== Wallet File Management ====================

// saveWallet encrypts and saves a wallet to disk
func (wm *WalletManager) saveWallet(wallet *Wallet, passphrase string) error {
	// Generate salt for key derivation
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %v", err)
	}

	// Derive encryption key from passphrase using scrypt
	encryptionKey, err := scrypt.Key([]byte(passphrase), salt, 32768, 8, 1, 32)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %v", err)
	}

	// Encrypt private key using AES-256-GCM
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %v", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %v", err)
	}

	encryptedKey := gcm.Seal(nil, nonce, wallet.PrivateKey, nil)

	// Create wallet file
	wf := &walletFile{
		ID:           wallet.ID,
		Network:      wallet.Network,
		Address:      wallet.Address,
		EncryptedKey: hex.EncodeToString(encryptedKey),
		Salt:         hex.EncodeToString(salt),
		Nonce:        hex.EncodeToString(nonce),
		CreatedAt:    wallet.CreatedAt,
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal wallet: %v", err)
	}

	// Write to file
	walletPath := filepath.Join(wm.walletsDir, wallet.ID+".json")
	if err := os.WriteFile(walletPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write wallet file: %v", err)
	}

	return nil
}

// loadAndDecryptWallet loads and decrypts a wallet from disk
func (wm *WalletManager) loadAndDecryptWallet(walletPath string, passphrase string) (*Wallet, error) {
	// Read wallet file
	data, err := os.ReadFile(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read wallet file: %v", err)
	}

	// Unmarshal wallet file
	var wf walletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal wallet: %v", err)
	}

	// Decode encrypted key, salt, and nonce
	encryptedKey, err := hex.DecodeString(wf.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted key: %v", err)
	}

	salt, err := hex.DecodeString(wf.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to decode salt: %v", err)
	}

	nonce, err := hex.DecodeString(wf.Nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to decode nonce: %v", err)
	}

	// Derive decryption key
	decryptionKey, err := scrypt.Key([]byte(passphrase), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to derive decryption key: %v", err)
	}

	// Decrypt private key
	block, err := aes.NewCipher(decryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	privateKey, err := gcm.Open(nil, nonce, encryptedKey, nil)
	if err != nil {
		return nil, ErrInvalidPassphrase
	}

	// Create wallet
	wallet := &Wallet{
		ID:         wf.ID,
		Network:    wf.Network,
		Address:    wf.Address,
		PrivateKey: privateKey,
		CreatedAt:  wf.CreatedAt,
	}

	return wallet, nil
}

// loadWallets loads all wallet metadata (without private keys) from disk
func (wm *WalletManager) loadWallets() error {
	files, err := os.ReadDir(wm.walletsDir)
	if err != nil {
		// Directory might not exist yet
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read wallets directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		// Read wallet file
		walletPath := filepath.Join(wm.walletsDir, file.Name())
		data, err := os.ReadFile(walletPath)
		if err != nil {
			continue // Skip invalid files
		}

		// Unmarshal wallet file
		var wf walletFile
		if err := json.Unmarshal(data, &wf); err != nil {
			continue // Skip invalid files
		}

		// Store wallet metadata (without private key)
		wm.wallets[wf.ID] = &Wallet{
			ID:        wf.ID,
			Network:   wf.Network,
			Address:   wf.Address,
			CreatedAt: wf.CreatedAt,
		}
	}

	return nil
}

// ==================== Multi-Wallet Selection Functions ====================

// GetSupportedNetworks returns unique networks from all wallets
func (wm *WalletManager) GetSupportedNetworks() []string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	seen := make(map[string]bool)
	networks := make([]string, 0)

	for _, wallet := range wm.wallets {
		if !seen[wallet.Network] {
			networks = append(networks, wallet.Network)
			seen[wallet.Network] = true
		}
	}

	return networks
}

// HasCompatibleWallet checks if any wallet matches accepted networks
func (wm *WalletManager) HasCompatibleWallet(acceptedNetworks []string) bool {
	if len(acceptedNetworks) == 0 {
		return true // Empty list means any network accepted
	}

	wm.mu.RLock()
	defer wm.mu.RUnlock()

	for _, wallet := range wm.wallets {
		if isNetworkInList(wallet.Network, acceptedNetworks) {
			return true
		}
	}

	return false
}

// SelectBestWallet selects best wallet for accepted networks
func (wm *WalletManager) SelectBestWallet(acceptedNetworks []string, defaultWalletID string) (*Wallet, string, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// If no restrictions, use default or first available
	if len(acceptedNetworks) == 0 {
		if defaultWalletID != "" {
			if wallet, exists := wm.wallets[defaultWalletID]; exists {
				return wallet, defaultWalletID, nil
			}
		}
		// Return first available wallet
		for walletID, wallet := range wm.wallets {
			return wallet, walletID, nil
		}
		return nil, "", fmt.Errorf("no wallets available")
	}

	// Priority 1: Default wallet if it matches
	if defaultWalletID != "" {
		defaultWallet, exists := wm.wallets[defaultWalletID]
		if exists && isNetworkInList(defaultWallet.Network, acceptedNetworks) {
			return defaultWallet, defaultWalletID, nil
		}
	}

	// Priority 2: First matching wallet
	for walletID, wallet := range wm.wallets {
		if isNetworkInList(wallet.Network, acceptedNetworks) {
			return wallet, walletID, nil
		}
	}

	return nil, "", fmt.Errorf("no compatible wallet found for networks: %v", acceptedNetworks)
}

// isNetworkInList checks if a wallet network matches any accepted network
func isNetworkInList(walletNetwork string, acceptedNetworks []string) bool {
	networkPrefix := strings.Split(walletNetwork, ":")[0] + ":"

	for _, accepted := range acceptedNetworks {
		// Exact match
		if walletNetwork == accepted {
			return true
		}

		// Prefix match (e.g., wallet "eip155:84532" matches accepted "eip155:*")
		if strings.HasPrefix(accepted, networkPrefix) {
			if strings.HasSuffix(accepted, ":*") {
				return true
			}
		}

		// Same network family (e.g., wallet "eip155:84532" matches accepted "eip155:8453")
		if strings.HasPrefix(accepted, networkPrefix) {
			return true
		}
	}

	return false
}

// ==================== Utility Functions ====================

// isValidNetwork checks if a network is supported
func isValidNetwork(network string) bool {
	// EVM networks: eip155:<chain_id>
	if strings.HasPrefix(network, "eip155:") {
		parts := strings.Split(network, ":")
		if len(parts) == 2 {
			// Validate chain ID is numeric
			_, ok := new(big.Int).SetString(parts[1], 10)
			return ok
		}
	}

	// Solana networks: solana:<cluster>
	if strings.HasPrefix(network, "solana:") {
		parts := strings.Split(network, ":")
		if len(parts) == 2 {
			cluster := parts[1]
			// Valid clusters: mainnet-beta, devnet, testnet, or custom RPC
			return cluster != ""
		}
	}

	return false
}

// generateNonce generates a random nonce for payment signatures
func generateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
