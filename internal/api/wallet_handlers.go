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

	// Optionally include balances
	includeBalance := r.URL.Query().Get("balance") == "true"

	type WalletResponse struct {
		WalletID  string                 `json:"wallet_id"`
		Network   string                 `json:"network"`
		Address   string                 `json:"address"`
		CreatedAt int64                  `json:"created_at"`
		Balance   *payment.WalletBalance `json:"balance,omitempty"`
	}

	response := make([]WalletResponse, 0, len(wallets))
	for _, wallet := range wallets {
		walletResp := WalletResponse{
			WalletID:  wallet.ID,
			Network:   wallet.Network,
			Address:   wallet.Address,
			CreatedAt: wallet.CreatedAt,
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
		"wallets": response,
		"count":   len(response),
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"wallet_id": wallet.ID,
		"address":   wallet.Address,
		"network":   wallet.Network,
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"wallet_id": wallet.ID,
		"address":   wallet.Address,
		"network":   wallet.Network,
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

	if req.Passphrase == "" {
		http.Error(w, "Passphrase is required", http.StatusBadRequest)
		return
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
