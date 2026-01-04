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
		// If we have an invoice ID, the invoice was created but delivery failed
		if invoiceID != "" {
			s.logger.Warn(fmt.Sprintf("Invoice created but delivery failed: %s - %v", invoiceID, err), "api")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // Return 200 with invoice_id but indicate delivery failure
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":          false,
				"invoice_id":       invoiceID,
				"message":          "Invoice created but delivery failed. Invoice marked as failed.",
				"delivery_error":   fmt.Sprintf("%v", err),
			})
			return
		}

		// No invoice ID means invoice creation failed completely
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

// handleDeleteInvoice deletes a failed invoice
func (s *APIServer) handleDeleteInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := strings.TrimPrefix(r.URL.Path, "/api/invoices/")
	invoiceID = strings.TrimSuffix(invoiceID, "/delete")

	s.logger.Info(fmt.Sprintf("Deleting invoice %s", invoiceID), "api")

	// Get invoice to check status
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

	// Only allow deleting failed or expired invoices
	if invoice.Status != "failed" && invoice.Status != "expired" && invoice.Status != "rejected" {
		s.logger.Warn(fmt.Sprintf("Cannot delete invoice %s with status %s", invoiceID, invoice.Status), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Cannot delete invoice with status: %s", invoice.Status),
		})
		return
	}

	if err := s.invoiceManager.DeleteInvoice(invoiceID); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to delete invoice %s: %v", invoiceID, err), "api")
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
		"success": true,
		"message": "Invoice deleted",
	})
}

// handleResendInvoice resends a failed invoice
func (s *APIServer) handleResendInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := strings.TrimPrefix(r.URL.Path, "/api/invoices/")
	invoiceID = strings.TrimSuffix(invoiceID, "/resend")

	s.logger.Info(fmt.Sprintf("Resending invoice %s", invoiceID), "api")

	// Get invoice to check status and recreate
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

	// Only allow resending failed invoices
	if invoice.Status != "failed" {
		s.logger.Warn(fmt.Sprintf("Cannot resend invoice %s with status %s", invoiceID, invoice.Status), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Cannot resend invoice with status: %s (only failed invoices can be resent)", invoice.Status),
		})
		return
	}

	// Calculate new expiration (24 hours from now)
	expiresInHours := 24

	// Verify wallet still exists
	if invoice.FromWalletID == "" {
		s.logger.Warn(fmt.Sprintf("Invoice %s has no wallet ID (old invoice format)", invoiceID), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Cannot resend: invoice missing wallet ID (created with old version)",
		})
		return
	}

	// Delete old invoice first
	if err := s.invoiceManager.DeleteInvoice(invoiceID); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to delete old invoice %s: %v", invoiceID, err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to delete old invoice: %v", err),
		})
		return
	}

	// Create new invoice with same details using the stored wallet ID
	newInvoiceID, err := s.invoiceManager.CreateInvoice(
		invoice.FromPeerID,
		invoice.ToPeerID,
		invoice.FromWalletID,
		invoice.Amount,
		invoice.Currency,
		invoice.Network,
		invoice.Description,
		expiresInHours,
	)

	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to resend invoice: %v", err), "api")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to resend invoice: %v", err),
		})
		return
	}

	s.logger.Info(fmt.Sprintf("Invoice resent successfully: old=%s, new=%s", invoiceID, newInvoiceID), "api")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"message":        "Invoice resent successfully",
		"new_invoice_id": newInvoiceID,
	})
}

// handleGetAllowedNetworks returns networks allowed for invoice payments (intersection of facilitator and app supported networks)
func (s *APIServer) handleGetAllowedNetworks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	allowedNetworks, err := s.invoiceManager.GetAllowedNetworks(ctx)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to get allowed networks: %v", err), "api")
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
		"success":  true,
		"networks": allowedNetworks,
	})
}
