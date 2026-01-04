package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PaymentInvoice represents a P2P payment invoice
type PaymentInvoice struct {
	ID                int64                  `json:"id"`
	InvoiceID         string                 `json:"invoice_id"`
	FromPeerID        string                 `json:"from_peer_id"`
	ToPeerID          string                 `json:"to_peer_id"`
	FromWalletID      string                 `json:"from_wallet_id,omitempty"`      // Wallet ID of invoice creator
	FromWalletAddress string                 `json:"from_wallet_address,omitempty"` // Wallet address of invoice creator
	ToWalletID        string                 `json:"to_wallet_id,omitempty"`        // Wallet ID of payer (set when accepted)
	ToWalletAddress   string                 `json:"to_wallet_address,omitempty"`   // Wallet address of payer (set when accepted)
	Amount            float64                `json:"amount"`
	Currency          string                 `json:"currency"`
	Network           string                 `json:"network"`
	Description       string                 `json:"description"`
	Status            string                 `json:"status"`
	PaymentSignature  string                 `json:"payment_signature,omitempty"`
	TransactionID     string                 `json:"transaction_id,omitempty"`
	PaymentID         *int64                 `json:"payment_id,omitempty"`
	CreatedAt         time.Time              `json:"-"`
	ExpiresAt         *time.Time             `json:"-"`
	AcceptedAt        *time.Time             `json:"-"`
	RejectedAt        *time.Time             `json:"-"`
	SettledAt         *time.Time             `json:"-"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// MarshalJSON customizes JSON marshaling to convert timestamps to Unix seconds
func (p *PaymentInvoice) MarshalJSON() ([]byte, error) {
	type Alias PaymentInvoice
	return json.Marshal(&struct {
		*Alias
		CreatedAt  int64  `json:"created_at"`
		ExpiresAt  *int64 `json:"expires_at,omitempty"`
		AcceptedAt *int64 `json:"accepted_at,omitempty"`
		RejectedAt *int64 `json:"rejected_at,omitempty"`
		SettledAt  *int64 `json:"settled_at,omitempty"`
	}{
		Alias:      (*Alias)(p),
		CreatedAt:  p.CreatedAt.Unix(),
		ExpiresAt:  timeToUnix(p.ExpiresAt),
		AcceptedAt: timeToUnix(p.AcceptedAt),
		RejectedAt: timeToUnix(p.RejectedAt),
		SettledAt:  timeToUnix(p.SettledAt),
	})
}

// timeToUnix converts *time.Time to *int64 Unix timestamp
func timeToUnix(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	unix := t.Unix()
	return &unix
}

// InitPaymentInvoicesTable creates the payment_invoices table
func (sm *SQLiteManager) InitPaymentInvoicesTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS payment_invoices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		invoice_id TEXT NOT NULL UNIQUE,
		from_peer_id TEXT NOT NULL,
		to_peer_id TEXT NOT NULL,
		from_wallet_id TEXT,
		from_wallet_address TEXT,
		to_wallet_id TEXT,
		to_wallet_address TEXT,
		amount REAL NOT NULL,
		currency TEXT NOT NULL,
		network TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		payment_signature TEXT,
		transaction_id TEXT,
		payment_id INTEGER,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		expires_at INTEGER,
		accepted_at INTEGER,
		rejected_at INTEGER,
		settled_at INTEGER,
		metadata TEXT,
		FOREIGN KEY (payment_id) REFERENCES job_payments(id) ON DELETE SET NULL
	);

	CREATE INDEX IF NOT EXISTS idx_invoices_from_peer ON payment_invoices(from_peer_id);
	CREATE INDEX IF NOT EXISTS idx_invoices_to_peer ON payment_invoices(to_peer_id);
	CREATE INDEX IF NOT EXISTS idx_invoices_status ON payment_invoices(status);
	`

	_, err := sm.db.Exec(query)
	return err
}

// CreatePaymentInvoice inserts a new invoice
func (sm *SQLiteManager) CreatePaymentInvoice(invoice *PaymentInvoice) error {
	metadataJSON, _ := json.Marshal(invoice.Metadata)

	query := `
	INSERT INTO payment_invoices (
		invoice_id, from_peer_id, to_peer_id,
		from_wallet_id, from_wallet_address, to_wallet_id, to_wallet_address,
		amount, currency, network, description, status, created_at, expires_at, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var expiresAt *int64
	if invoice.ExpiresAt != nil {
		t := invoice.ExpiresAt.Unix()
		expiresAt = &t
	}

	result, err := sm.db.Exec(query,
		invoice.InvoiceID,
		invoice.FromPeerID,
		invoice.ToPeerID,
		invoice.FromWalletID,
		invoice.FromWalletAddress,
		invoice.ToWalletID,
		invoice.ToWalletAddress,
		invoice.Amount,
		invoice.Currency,
		invoice.Network,
		invoice.Description,
		invoice.Status,
		invoice.CreatedAt.Unix(),
		expiresAt,
		metadataJSON,
	)

	if err != nil {
		return err
	}

	invoice.ID, _ = result.LastInsertId()
	return nil
}

// GetPaymentInvoice retrieves an invoice by ID
func (sm *SQLiteManager) GetPaymentInvoice(invoiceID string) (*PaymentInvoice, error) {
	query := `
	SELECT id, invoice_id, from_peer_id, to_peer_id,
		   from_wallet_id, from_wallet_address, to_wallet_id, to_wallet_address,
		   amount, currency, network, description, status, payment_signature, transaction_id, payment_id,
		   created_at, expires_at, accepted_at, rejected_at, settled_at, metadata
	FROM payment_invoices
	WHERE invoice_id = ?
	`

	invoice := &PaymentInvoice{}
	var fromWalletID, fromWalletAddr, toWalletID, toWalletAddr sql.NullString
	var paymentSigStr, transactionIDStr, metadataStr sql.NullString
	var paymentID sql.NullInt64
	var createdAt, expiresAt, acceptedAt, rejectedAt, settledAt sql.NullInt64

	err := sm.db.QueryRow(query, invoiceID).Scan(
		&invoice.ID,
		&invoice.InvoiceID,
		&invoice.FromPeerID,
		&invoice.ToPeerID,
		&fromWalletID,
		&fromWalletAddr,
		&toWalletID,
		&toWalletAddr,
		&invoice.Amount,
		&invoice.Currency,
		&invoice.Network,
		&invoice.Description,
		&invoice.Status,
		&paymentSigStr,
		&transactionIDStr,
		&paymentID,
		&createdAt,
		&expiresAt,
		&acceptedAt,
		&rejectedAt,
		&settledAt,
		&metadataStr,
	)

	if err != nil {
		return nil, err
	}

	// Populate wallet IDs and addresses
	if fromWalletID.Valid {
		invoice.FromWalletID = fromWalletID.String
	}
	if fromWalletAddr.Valid {
		invoice.FromWalletAddress = fromWalletAddr.String
	}
	if toWalletID.Valid {
		invoice.ToWalletID = toWalletID.String
	}
	if toWalletAddr.Valid {
		invoice.ToWalletAddress = toWalletAddr.String
	}

	// Deserialize payment signature
	if paymentSigStr.Valid && paymentSigStr.String != "" {
		invoice.PaymentSignature = paymentSigStr.String
	}

	// Deserialize transaction ID
	if transactionIDStr.Valid && transactionIDStr.String != "" {
		invoice.TransactionID = transactionIDStr.String
	}

	// Deserialize metadata
	if metadataStr.Valid && metadataStr.String != "" {
		json.Unmarshal([]byte(metadataStr.String), &invoice.Metadata)
	}

	// Convert timestamps
	if createdAt.Valid {
		t := time.Unix(createdAt.Int64, 0)
		invoice.CreatedAt = t
	}
	if expiresAt.Valid {
		t := time.Unix(expiresAt.Int64, 0)
		invoice.ExpiresAt = &t
	}
	if acceptedAt.Valid {
		t := time.Unix(acceptedAt.Int64, 0)
		invoice.AcceptedAt = &t
	}
	if rejectedAt.Valid {
		t := time.Unix(rejectedAt.Int64, 0)
		invoice.RejectedAt = &t
	}
	if settledAt.Valid {
		t := time.Unix(settledAt.Int64, 0)
		invoice.SettledAt = &t
	}

	if paymentID.Valid {
		invoice.PaymentID = &paymentID.Int64
	}

	return invoice, nil
}

// ListPaymentInvoices retrieves invoices for a peer
func (sm *SQLiteManager) ListPaymentInvoices(peerID string, status string, limit, offset int) ([]*PaymentInvoice, error) {
	query := `
	SELECT id, invoice_id, from_peer_id, to_peer_id,
		   from_wallet_id, from_wallet_address, to_wallet_id, to_wallet_address,
		   amount, currency, network, description, status, created_at, expires_at
	FROM payment_invoices
	WHERE (from_peer_id = ? OR to_peer_id = ?)
	`

	args := []interface{}{peerID, peerID}

	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}

	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := sm.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	invoices := make([]*PaymentInvoice, 0)
	for rows.Next() {
		invoice := &PaymentInvoice{}
		var fromWalletID, fromWalletAddr, toWalletID, toWalletAddr sql.NullString
		var createdAt, expiresAt sql.NullInt64

		err := rows.Scan(
			&invoice.ID,
			&invoice.InvoiceID,
			&invoice.FromPeerID,
			&invoice.ToPeerID,
			&fromWalletID,
			&fromWalletAddr,
			&toWalletID,
			&toWalletAddr,
			&invoice.Amount,
			&invoice.Currency,
			&invoice.Network,
			&invoice.Description,
			&invoice.Status,
			&createdAt,
			&expiresAt,
		)

		if err != nil {
			continue
		}

		// Populate wallet IDs and addresses
		if fromWalletID.Valid {
			invoice.FromWalletID = fromWalletID.String
		}
		if fromWalletAddr.Valid {
			invoice.FromWalletAddress = fromWalletAddr.String
		}
		if toWalletID.Valid {
			invoice.ToWalletID = toWalletID.String
		}
		if toWalletAddr.Valid {
			invoice.ToWalletAddress = toWalletAddr.String
		}

		if createdAt.Valid {
			t := time.Unix(createdAt.Int64, 0)
			invoice.CreatedAt = t
		}
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0)
			invoice.ExpiresAt = &t
		}

		invoices = append(invoices, invoice)
	}

	return invoices, nil
}

// UpdatePaymentInvoiceStatus updates invoice status
func (sm *SQLiteManager) UpdatePaymentInvoiceStatus(invoiceID string, status string) error {
	var timestampField string
	switch status {
	case "accepted":
		timestampField = "accepted_at"
	case "rejected":
		timestampField = "rejected_at"
	case "settled":
		timestampField = "settled_at"
	default:
		// For other statuses (expired, cancelled), just update status
		query := `UPDATE payment_invoices SET status = ? WHERE invoice_id = ?`
		_, err := sm.db.Exec(query, status, invoiceID)
		return err
	}

	query := fmt.Sprintf(`
	UPDATE payment_invoices
	SET status = ?, %s = strftime('%%s', 'now')
	WHERE invoice_id = ?
	`, timestampField)

	_, err := sm.db.Exec(query, status, invoiceID)
	return err
}

// UpdatePaymentInvoiceAccepted updates invoice with payment details
func (sm *SQLiteManager) UpdatePaymentInvoiceAccepted(
	invoiceID string,
	paymentSigJSON string,
	paymentID int64,
	toWalletID string,
	toWalletAddress string,
) error {
	query := `
	UPDATE payment_invoices
	SET status = 'accepted',
		payment_signature = ?,
		payment_id = ?,
		to_wallet_id = ?,
		to_wallet_address = ?,
		accepted_at = strftime('%s', 'now')
	WHERE invoice_id = ?
	`

	_, err := sm.db.Exec(query, paymentSigJSON, paymentID, toWalletID, toWalletAddress, invoiceID)
	return err
}

// CleanupExpiredInvoices marks expired invoices
func (sm *SQLiteManager) CleanupExpiredInvoices() (int, error) {
	query := `
	UPDATE payment_invoices
	SET status = 'expired'
	WHERE status = 'pending'
	  AND expires_at IS NOT NULL
	  AND expires_at < strftime('%s', 'now')
	`

	result, err := sm.db.Exec(query)
	if err != nil {
		return 0, err
	}

	rowsAffected, _ := result.RowsAffected()
	return int(rowsAffected), nil
}

// MarkInvoiceFailed marks an invoice as permanently failed (couldn't be delivered)
func (sm *SQLiteManager) MarkInvoiceFailed(invoiceID string, reason string) error {
	query := `
	UPDATE payment_invoices
	SET status = 'failed',
		metadata = json_set(COALESCE(metadata, '{}'), '$.failure_reason', ?)
	WHERE invoice_id = ?
	`

	_, err := sm.db.Exec(query, reason, invoiceID)
	return err
}

// DeletePaymentInvoice deletes an invoice from the database
// Used when invoice delivery fails to prevent orphaned invoices
func (sm *SQLiteManager) DeletePaymentInvoice(invoiceID string) error {
	query := `DELETE FROM payment_invoices WHERE invoice_id = ?`
	_, err := sm.db.Exec(query, invoiceID)
	return err
}
