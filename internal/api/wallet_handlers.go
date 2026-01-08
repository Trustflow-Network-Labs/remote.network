package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mr-tron/base58"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/payment"
)

// handleListWallets returns all wallets
func (s *APIServer) handleListWallets(w http.ResponseWriter, r *http.Request) {
	wallets := s.walletManager.ListWallets()

	// Get default wallet ID
	defaultWalletID, err := s.walletManager.GetDefaultWalletID()
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Failed to get default wallet: %v", err), "api")
	}

	// Optionally include balances
	includeBalance := r.URL.Query().Get("balance") == "true"

	type WalletResponse struct {
		WalletID  string                 `json:"wallet_id"`
		Network   string                 `json:"network"`
		Address   string                 `json:"address"`
		CreatedAt int64                  `json:"created_at"`
		IsDefault bool                   `json:"is_default"`
		Balance   *payment.WalletBalance `json:"balance,omitempty"`
	}

	response := make([]WalletResponse, 0, len(wallets))
	for _, wallet := range wallets {
		// Normalize network to full CAIP-2 format (converts Solana aliases to full format)
		normalizedNetwork := payment.NormalizeSolanaNetwork(wallet.Network)

		walletResp := WalletResponse{
			WalletID:  wallet.ID,
			Network:   normalizedNetwork,
			Address:   wallet.Address,
			CreatedAt: wallet.CreatedAt,
			IsDefault: wallet.ID == defaultWalletID,
		}

		if includeBalance {
			balance, err := s.walletManager.GetBalance(wallet.ID)
			if err == nil {
				walletResp.Balance = balance
			}
		}

		response = append(response, walletResp)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"wallets":         response,
		"count":           len(response),
		"default_wallet":  defaultWalletID,
	})
}

// handleCreateWallet creates a new wallet
func (s *APIServer) handleCreateWallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Network    string `json:"network"`
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate inputs
	if req.Network == "" {
		http.Error(w, "Network is required", http.StatusBadRequest)
		return
	}
	if req.Passphrase == "" {
		http.Error(w, "Passphrase is required", http.StatusBadRequest)
		return
	}

	// Create wallet
	wallet, err := s.walletManager.CreateWallet(req.Network, req.Passphrase)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create wallet: %v", err), http.StatusInternalServerError)
		return
	}

	// Normalize network to full CAIP-2 format for response
	normalizedNetwork := payment.NormalizeSolanaNetwork(wallet.Network)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"wallet_id": wallet.ID,
		"address":   wallet.Address,
		"network":   normalizedNetwork,
	})
}

// handleImportWallet imports a wallet from private key
func (s *APIServer) handleImportWallet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PrivateKey string `json:"private_key"`
		Network    string `json:"network"`
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate inputs
	if req.PrivateKey == "" || req.Network == "" || req.Passphrase == "" {
		http.Error(w, "Private key, network, and passphrase are required", http.StatusBadRequest)
		return
	}

	// Import wallet
	wallet, err := s.walletManager.ImportWallet(req.PrivateKey, req.Network, req.Passphrase)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to import wallet: %v", err), http.StatusInternalServerError)
		return
	}

	// Normalize network to full CAIP-2 format for response
	normalizedNetwork := payment.NormalizeSolanaNetwork(wallet.Network)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"wallet_id": wallet.ID,
		"address":   wallet.Address,
		"network":   normalizedNetwork,
	})
}

// handleDeleteWallet deletes a wallet
func (s *APIServer) handleDeleteWallet(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")

	var req struct {
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Passphrase is optional - if empty, wallet manager will try to retrieve from keystore
	if req.Passphrase == "" {
		s.logger.Debug(fmt.Sprintf("No passphrase provided for wallet deletion %s - will attempt to use stored passphrase", walletID), "api")
	}

	// Delete wallet
	if err := s.walletManager.DeleteWallet(walletID, req.Passphrase); err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete wallet: %v", err), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Wallet deleted successfully",
	})
}

// handleGetWalletBalance queries real-time balance
func (s *APIServer) handleGetWalletBalance(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")
	walletID = strings.TrimSuffix(walletID, "/balance")

	balance, err := s.walletManager.GetBalance(walletID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get balance: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(balance)
}

// handleExportWallet exports wallet private key
func (s *APIServer) handleExportWallet(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")
	walletID = strings.TrimSuffix(walletID, "/export")

	var req struct {
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Passphrase == "" {
		http.Error(w, "Passphrase is required", http.StatusBadRequest)
		return
	}

	// Get and decrypt wallet
	wallet, err := s.walletManager.GetWallet(walletID, req.Passphrase)
	if err != nil {
		http.Error(w, "Invalid passphrase or wallet not found", http.StatusForbidden)
		return
	}

	// Format private key based on network type
	var privateKeyExport interface{}
	if strings.HasPrefix(wallet.Network, "solana:") {
		// Solana: Export as base58-encoded string (Phantom/Backpack/Solflare format)
		privateKeyExport = base58.Encode(wallet.PrivateKey)
	} else {
		// EVM: Export as hex string with 0x prefix
		privateKeyExport = "0x" + hex.EncodeToString(wallet.PrivateKey)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"wallet_id":   walletID,
		"private_key": privateKeyExport,
		"address":     wallet.Address,
		"network":     wallet.Network,
	})
}

// handleCheckTokenAccount checks if a Solana wallet has a token account initialized
func (s *APIServer) handleCheckTokenAccount(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")
	walletID = strings.TrimSuffix(walletID, "/check-token-account")

	// Get token symbol from query params (default to USDC)
	tokenSymbol := r.URL.Query().Get("token")
	if tokenSymbol == "" {
		tokenSymbol = "USDC"
	}

	exists, err := s.walletManager.CheckSolanaTokenAccount(walletID, tokenSymbol)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to check token account: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"wallet_id":     walletID,
		"token":         tokenSymbol,
		"account_exists": exists,
	})
}

// handleInitializeTokenAccount initializes a token account for a Solana wallet
func (s *APIServer) handleInitializeTokenAccount(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")
	walletID = strings.TrimSuffix(walletID, "/initialize-token-account")

	var req struct {
		Token      string `json:"token"`       // e.g., "USDC"
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Default to USDC if not specified
	if req.Token == "" {
		req.Token = "USDC"
	}

	// Passphrase is optional - if empty, wallet manager will try to retrieve from keystore
	if req.Passphrase == "" {
		s.logger.Debug(fmt.Sprintf("No passphrase provided for token account initialization %s - will attempt to use stored passphrase", walletID), "api")
	}

	signature, err := s.walletManager.InitializeSolanaTokenAccount(walletID, req.Token, req.Passphrase)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to initialize token account: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"wallet_id": walletID,
		"token":     req.Token,
		"transaction": signature,
		"message":   fmt.Sprintf("%s token account initialized successfully", req.Token),
	})
}

// handleGetTokenAccountAddress returns the Associated Token Account address for a Solana wallet
func (s *APIServer) handleGetTokenAccountAddress(w http.ResponseWriter, r *http.Request) {
	walletID := strings.TrimPrefix(r.URL.Path, "/api/wallets/")
	walletID = strings.TrimSuffix(walletID, "/token-account-address")

	// Get token symbol from query params (default to USDC)
	tokenSymbol := r.URL.Query().Get("token")
	if tokenSymbol == "" {
		tokenSymbol = "USDC"
	}

	address, err := s.walletManager.GetSolanaTokenAccountAddress(walletID, tokenSymbol)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get token account address: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"wallet_id":            walletID,
		"token":                tokenSymbol,
		"token_account_address": address,
	})
}

// handleSetDefaultWallet sets a wallet as the default
func (s *APIServer) handleSetDefaultWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WalletID string `json:"wallet_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.WalletID == "" {
		http.Error(w, "wallet_id is required", http.StatusBadRequest)
		return
	}

	// Set as default
	if err := s.walletManager.SetDefaultWallet(req.WalletID); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to set default wallet: %v", err), "api")
		http.Error(w, fmt.Sprintf("Failed to set default wallet: %v", err), http.StatusInternalServerError)
		return
	}

	s.logger.Info(fmt.Sprintf("Set wallet %s as default via API", req.WalletID), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"default_wallet_id": req.WalletID,
	})
}
