package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleCreateInvoice creates a new payment invoice
func (s *APIServer) handleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToPeerID       string  `json:"to_peer_id"`
		FromWalletID   string  `json:"from_wallet_id"`
		Amount         float64 `json:"amount"`
		Currency       string  `json:"currency"`
		Network        string  `json:"network"`
		Description    string  `json:"description"`
		ExpiresInHours int     `json:"expires_in_hours"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode create invoice request: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}

	// Validate
	if req.ToPeerID == "" || req.FromWalletID == "" || req.Amount <= 0 || req.Currency == "" || req.Network == "" {
		s.logger.Warn(fmt.Sprintf("Invalid invoice parameters: to_peer_id=%s, from_wallet_id=%s, amount=%f, currency=%s, network=%s",
			req.ToPeerID, req.FromWalletID, req.Amount, req.Currency, req.Network), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Invalid invoice parameters (missing to_peer_id, from_wallet_id, amount, currency, or network)",
		})
		return
	}

	// Default expiration
	if req.ExpiresInHours == 0 {
		req.ExpiresInHours = 24
	}

	s.logger.Info(fmt.Sprintf("Creating invoice: from=%s, to=%s, wallet=%s, amount=%.6f %s",
		s.localPeerID[:8], req.ToPeerID[:8], req.FromWalletID, req.Amount, req.Currency), "api")

	// Create invoice
	invoiceID, err := s.invoiceManager.CreateInvoice(
		s.localPeerID,
		req.ToPeerID,
		req.FromWalletID,
		req.Amount,
		req.Currency,
		req.Network,
		req.Description,
		req.ExpiresInHours,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to create invoice: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("%v", err),
		})
		return
	}

	s.logger.Info(fmt.Sprintf("Invoice created successfully: %s", invoiceID), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"invoice_id": invoiceID,
	})
}

// handleListInvoices lists invoices
func (s *APIServer) handleListInvoices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit == 0 {
		limit = 50
	}

	invoices, err := s.invoiceManager.ListInvoices(s.localPeerID, status, limit, offset)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to list invoices: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("%v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"invoices": invoices,
		"count":    len(invoices),
	})
}

// handleGetInvoice gets invoice details
func (s *APIServer) handleGetInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := strings.TrimPrefix(r.URL.Path, "/api/invoices/")

	invoice, err := s.invoiceManager.GetInvoice(invoiceID)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Invoice not found: %s", invoiceID), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Invoice not found",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(invoice)
}

// handleAcceptInvoice accepts and pays an invoice
func (s *APIServer) handleAcceptInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := strings.TrimPrefix(r.URL.Path, "/api/invoices/")
	invoiceID = strings.TrimSuffix(invoiceID, "/accept")

	var req struct {
		WalletID   string `json:"wallet_id"`
		Passphrase string `json:"passphrase"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode accept invoice request: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}

	if req.WalletID == "" || req.Passphrase == "" {
		s.logger.Warn(fmt.Sprintf("Missing wallet_id or passphrase in accept invoice request for %s", invoiceID), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Wallet ID and passphrase are required",
		})
		return
	}

	s.logger.Info(fmt.Sprintf("Accepting invoice %s with wallet %s", invoiceID, req.WalletID), "api")

	if err := s.invoiceManager.AcceptInvoice(invoiceID, req.WalletID, req.Passphrase); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to accept invoice %s: %v", invoiceID, err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("%v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Invoice accepted and payment initiated",
	})
}

// handleRejectInvoice rejects an invoice
func (s *APIServer) handleRejectInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := strings.TrimPrefix(r.URL.Path, "/api/invoices/")
	invoiceID = strings.TrimSuffix(invoiceID, "/reject")

	var req struct {
		Reason string `json:"reason"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to decode reject invoice request: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Invalid request body",
		})
		return
	}

	s.logger.Info(fmt.Sprintf("Rejecting invoice %s (reason: %s)", invoiceID, req.Reason), "api")

	if err := s.invoiceManager.RejectInvoice(invoiceID, req.Reason); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to reject invoice %s: %v", invoiceID, err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("%v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Invoice rejected",
	})
}
