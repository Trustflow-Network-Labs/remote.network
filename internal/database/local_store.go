package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// LocalStoreDB manages local store-and-forward data for public peers
type LocalStoreDB struct {
	db     *sql.DB
	logger *utils.LogsManager
}

// NewLocalStoreDB creates a new local store database manager
func NewLocalStoreDB(db *sql.DB, logger *utils.LogsManager) (*LocalStoreDB, error) {
	ldb := &LocalStoreDB{
		db:     db,
		logger: logger,
	}

	// Create tables
	if err := ldb.createTables(); err != nil {
		return nil, err
	}

	logger.Info("Local store database manager initialized", "local-store-db")
	return ldb, nil
}

// createTables creates local store-and-forward database tables
func (ldb *LocalStoreDB) createTables() error {
	// Local pending messages table
	createPendingMessagesTableSQL := `
	CREATE TABLE IF NOT EXISTS local_pending_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id TEXT UNIQUE NOT NULL,
		correlation_id TEXT,
		source_peer_id TEXT NOT NULL,
		target_peer_id TEXT NOT NULL,
		message_type TEXT NOT NULL,
		payload BLOB NOT NULL,
		payload_size INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		delivery_attempts INTEGER DEFAULT 0,
		last_delivery_attempt INTEGER,
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'expired', 'failed')),
		delivered_at INTEGER,
		priority INTEGER DEFAULT 0,
		failure_reason TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_lpm_target_peer ON local_pending_messages(target_peer_id, status, expires_at);
	CREATE INDEX IF NOT EXISTS idx_lpm_expires ON local_pending_messages(expires_at, status);
	CREATE INDEX IF NOT EXISTS idx_lpm_message_id ON local_pending_messages(message_id);
	CREATE INDEX IF NOT EXISTS idx_lpm_created ON local_pending_messages(created_at);
	CREATE INDEX IF NOT EXISTS idx_lpm_status ON local_pending_messages(status);
	`

	if _, err := ldb.db.ExecContext(context.Background(), createPendingMessagesTableSQL); err != nil {
		return fmt.Errorf("failed to create local_pending_messages table: %v", err)
	}

	// Local storage limits tracking table
	createStorageLimitsTableSQL := `
	CREATE TABLE IF NOT EXISTS local_storage_limits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		peer_id TEXT UNIQUE NOT NULL,
		message_count INTEGER DEFAULT 0,
		total_bytes INTEGER DEFAULT 0,
		max_message_count INTEGER,
		max_total_bytes INTEGER,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_lsl_peer ON local_storage_limits(peer_id);
	`

	if _, err := ldb.db.ExecContext(context.Background(), createStorageLimitsTableSQL); err != nil {
		return fmt.Errorf("failed to create local_storage_limits table: %v", err)
	}

	ldb.logger.Info("Local store database tables created successfully", "local-store-db")
	return nil
}

// ========================================
// Store-and-Forward Message Storage
// ========================================

// LocalPendingMessage represents a message waiting for delivery to a public peer
type LocalPendingMessage struct {
	ID                  int64
	MessageID           string
	CorrelationID       string
	SourcePeerID        string
	TargetPeerID        string
	MessageType         string
	Payload             []byte
	PayloadSize         int64
	CreatedAt           time.Time
	ExpiresAt           time.Time
	DeliveryAttempts    int
	LastDeliveryAttempt time.Time
	Status              string
	DeliveredAt         time.Time
	Priority            int
	FailureReason       string
}

// LocalStorageUsage represents current storage usage for a peer
type LocalStorageUsage struct {
	PeerID       string
	MessageCount int64
	TotalBytes   int64
	MaxMessages  int64
	MaxBytes     int64
}

// StorePendingMessage stores a message for later delivery to a public peer
func (ldb *LocalStoreDB) StorePendingMessage(msg *LocalPendingMessage) error {
	query := `
		INSERT INTO local_pending_messages (
			message_id, correlation_id, source_peer_id, target_peer_id,
			message_type, payload, payload_size, created_at, expires_at, status, priority
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	// Retry logic for handling transient SQLite BUSY errors
	maxRetries := 3
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := ldb.db.Exec(query,
			msg.MessageID,
			msg.CorrelationID,
			msg.SourcePeerID,
			msg.TargetPeerID,
			msg.MessageType,
			msg.Payload,
			msg.PayloadSize,
			msg.CreatedAt.Unix(),
			msg.ExpiresAt.Unix(),
			msg.Status,
			msg.Priority,
		)

		if err == nil {
			ldb.logger.Debug(fmt.Sprintf("Stored local pending message %s (type: %s, size: %d bytes, expires: %s)",
				msg.MessageID, msg.MessageType, msg.PayloadSize, msg.ExpiresAt.Format(time.RFC3339)), "local-store-db")
			return nil
		}

		// Check if it's a BUSY error
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			if attempt < maxRetries-1 {
				ldb.logger.Debug(fmt.Sprintf("Database busy, retrying in %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries), "local-store-db")
				time.Sleep(retryDelay)
				retryDelay *= 2 // Exponential backoff
				continue
			}
		}

		// Check if it's a duplicate message (unique constraint violation)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			ldb.logger.Debug(fmt.Sprintf("Message %s already exists in local store", msg.MessageID), "local-store-db")
			return nil // Not an error - message already stored
		}

		return fmt.Errorf("failed to store pending message: %v", err)
	}

	return fmt.Errorf("failed to store pending message after %d attempts", maxRetries)
}

// GetPendingMessagesForPeer retrieves all pending messages for a target peer
func (ldb *LocalStoreDB) GetPendingMessagesForPeer(targetPeerID string) ([]*LocalPendingMessage, error) {
	query := `
		SELECT id, message_id, correlation_id, source_peer_id, target_peer_id,
		       message_type, payload, payload_size, created_at, expires_at, delivery_attempts,
		       last_delivery_attempt, status, delivered_at, priority, failure_reason
		FROM local_pending_messages
		WHERE target_peer_id = ? AND status = 'pending' AND expires_at > ?
		ORDER BY priority DESC, created_at ASC
		LIMIT 100
	`

	now := time.Now().Unix()
	rows, err := ldb.db.Query(query, targetPeerID, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending messages: %v", err)
	}
	defer rows.Close()

	var messages []*LocalPendingMessage
	for rows.Next() {
		msg := &LocalPendingMessage{}
		var createdAt, expiresAt int64
		var lastDeliveryAttempt, deliveredAt sql.NullInt64
		var correlationID, failureReason sql.NullString

		err := rows.Scan(
			&msg.ID,
			&msg.MessageID,
			&correlationID,
			&msg.SourcePeerID,
			&msg.TargetPeerID,
			&msg.MessageType,
			&msg.Payload,
			&msg.PayloadSize,
			&createdAt,
			&expiresAt,
			&msg.DeliveryAttempts,
			&lastDeliveryAttempt,
			&msg.Status,
			&deliveredAt,
			&msg.Priority,
			&failureReason,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan pending message: %v", err)
		}

		if correlationID.Valid {
			msg.CorrelationID = correlationID.String
		}

		if failureReason.Valid {
			msg.FailureReason = failureReason.String
		}

		msg.CreatedAt = time.Unix(createdAt, 0)
		msg.ExpiresAt = time.Unix(expiresAt, 0)

		if lastDeliveryAttempt.Valid {
			msg.LastDeliveryAttempt = time.Unix(lastDeliveryAttempt.Int64, 0)
		}

		if deliveredAt.Valid {
			msg.DeliveredAt = time.Unix(deliveredAt.Int64, 0)
		}

		messages = append(messages, msg)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending messages: %v", err)
	}

	return messages, nil
}

// GetPeersWithStalledMessages returns peer IDs that have messages older than the given duration
func (ldb *LocalStoreDB) GetPeersWithStalledMessages(stalledDuration time.Duration) ([]string, error) {
	query := `
		SELECT DISTINCT target_peer_id
		FROM local_pending_messages
		WHERE status = 'pending'
		  AND expires_at > ?
		  AND (last_delivery_attempt IS NULL OR last_delivery_attempt < ?)
		  AND delivery_attempts < 10
	`

	now := time.Now()
	stalledThreshold := now.Add(-stalledDuration).Unix()

	rows, err := ldb.db.Query(query, now.Unix(), stalledThreshold)
	if err != nil {
		return nil, fmt.Errorf("failed to query stalled messages: %v", err)
	}
	defer rows.Close()

	var peerIDs []string
	for rows.Next() {
		var peerID string
		if err := rows.Scan(&peerID); err != nil {
			return nil, fmt.Errorf("failed to scan peer ID: %v", err)
		}
		peerIDs = append(peerIDs, peerID)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stalled messages: %v", err)
	}

	return peerIDs, nil
}

// MarkMessageDelivered marks a message as successfully delivered
func (ldb *LocalStoreDB) MarkMessageDelivered(messageID string) error {
	query := `
		UPDATE local_pending_messages
		SET status = 'delivered', delivered_at = ?
		WHERE message_id = ?
	`

	// Retry logic for handling transient SQLite BUSY errors
	maxRetries := 3
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := ldb.db.Exec(query, time.Now().Unix(), messageID)
		if err == nil {
			return nil
		}

		// Check if it's a BUSY error
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			if attempt < maxRetries-1 {
				ldb.logger.Debug(fmt.Sprintf("Database busy, retrying in %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries), "local-store-db")
				time.Sleep(retryDelay)
				retryDelay *= 2
				continue
			}
		}

		return fmt.Errorf("failed to mark message as delivered: %v", err)
	}

	return fmt.Errorf("failed to mark message as delivered after %d attempts", maxRetries)
}

// MarkMessageExpired marks a message as expired
func (ldb *LocalStoreDB) MarkMessageExpired(messageID string) error {
	query := `
		UPDATE local_pending_messages
		SET status = 'expired'
		WHERE message_id = ?
	`

	_, err := ldb.db.Exec(query, messageID)
	if err != nil {
		return fmt.Errorf("failed to mark message as expired: %v", err)
	}

	return nil
}

// MarkMessageFailed marks a message as failed with a reason
func (ldb *LocalStoreDB) MarkMessageFailed(messageID string, reason string) error {
	query := `
		UPDATE local_pending_messages
		SET status = 'failed', failure_reason = ?
		WHERE message_id = ?
	`

	_, err := ldb.db.Exec(query, reason, messageID)
	if err != nil {
		return fmt.Errorf("failed to mark message as failed: %v", err)
	}

	return nil
}

// IncrementDeliveryAttempt increments the delivery attempt counter
func (ldb *LocalStoreDB) IncrementDeliveryAttempt(messageID string) error {
	query := `
		UPDATE local_pending_messages
		SET delivery_attempts = delivery_attempts + 1,
		    last_delivery_attempt = ?
		WHERE message_id = ?
	`

	_, err := ldb.db.Exec(query, time.Now().Unix(), messageID)
	if err != nil {
		return fmt.Errorf("failed to increment delivery attempt: %v", err)
	}

	return nil
}

// DeleteExpiredMessages deletes messages that have expired
func (ldb *LocalStoreDB) DeleteExpiredMessages(now time.Time) (int64, error) {
	query := `
		DELETE FROM local_pending_messages
		WHERE status IN ('pending', 'expired', 'failed') AND expires_at < ?
	`

	result, err := ldb.db.Exec(query, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired messages: %v", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %v", err)
	}

	return deleted, nil
}

// DeleteDeliveredMessages deletes messages that have been successfully delivered
func (ldb *LocalStoreDB) DeleteDeliveredMessages(olderThan time.Time) (int64, error) {
	query := `
		DELETE FROM local_pending_messages
		WHERE status = 'delivered' AND delivered_at < ?
	`

	result, err := ldb.db.Exec(query, olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("failed to delete delivered messages: %v", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %v", err)
	}

	return deleted, nil
}

// GetMessageStatus retrieves the status of a specific message
func (ldb *LocalStoreDB) GetMessageStatus(messageID string) (map[string]interface{}, error) {
	query := `
		SELECT message_id, status, created_at, expires_at, delivered_at, message_type, delivery_attempts, failure_reason
		FROM local_pending_messages
		WHERE message_id = ?
	`

	var msgID, status, messageType string
	var createdAt, expiresAt int64
	var deliveredAt sql.NullInt64
	var deliveryAttempts int
	var failureReason sql.NullString

	err := ldb.db.QueryRow(query, messageID).Scan(
		&msgID, &status, &createdAt, &expiresAt, &deliveredAt, &messageType, &deliveryAttempts, &failureReason,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get message status: %v", err)
	}

	result := map[string]interface{}{
		"message_id":        msgID,
		"status":            status,
		"created_at":        time.Unix(createdAt, 0),
		"expires_at":        time.Unix(expiresAt, 0),
		"message_type":      messageType,
		"delivery_attempts": deliveryAttempts,
	}

	if deliveredAt.Valid {
		result["delivered_at"] = time.Unix(deliveredAt.Int64, 0)
	}

	if failureReason.Valid {
		result["failure_reason"] = failureReason.String
	}

	return result, nil
}

// GetStorageUsage retrieves current storage usage for a peer
func (ldb *LocalStoreDB) GetStorageUsage(peerID string) (LocalStorageUsage, error) {
	query := `SELECT peer_id, message_count, total_bytes, max_message_count, max_total_bytes FROM local_storage_limits WHERE peer_id = ?`

	var usage LocalStorageUsage
	var maxMessages, maxBytes sql.NullInt64

	err := ldb.db.QueryRow(query, peerID).Scan(
		&usage.PeerID,
		&usage.MessageCount,
		&usage.TotalBytes,
		&maxMessages,
		&maxBytes,
	)

	if err == sql.ErrNoRows {
		// No record exists, return zero usage
		return LocalStorageUsage{
			PeerID:       peerID,
			MessageCount: 0,
			TotalBytes:   0,
		}, nil
	}

	if err != nil {
		return LocalStorageUsage{}, fmt.Errorf("failed to get storage usage: %v", err)
	}

	if maxMessages.Valid {
		usage.MaxMessages = maxMessages.Int64
	}

	if maxBytes.Valid {
		usage.MaxBytes = maxBytes.Int64
	}

	return usage, nil
}

// IncrementStorageUsage increments storage usage counters for a peer
func (ldb *LocalStoreDB) IncrementStorageUsage(peerID string, messageCount int, bytes int64) error {
	query := `
		INSERT INTO local_storage_limits (peer_id, message_count, total_bytes, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			message_count = message_count + excluded.message_count,
			total_bytes = total_bytes + excluded.total_bytes,
			updated_at = excluded.updated_at
	`

	// Retry logic for handling transient SQLite BUSY errors
	maxRetries := 3
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := ldb.db.Exec(query, peerID, messageCount, bytes, time.Now().Unix())
		if err == nil {
			return nil
		}

		// Check if it's a BUSY error
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			if attempt < maxRetries-1 {
				ldb.logger.Debug(fmt.Sprintf("Database busy, retrying in %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries), "local-store-db")
				time.Sleep(retryDelay)
				retryDelay *= 2
				continue
			}
		}

		return fmt.Errorf("failed to increment storage usage: %v", err)
	}

	return fmt.Errorf("failed to increment storage usage after %d attempts", maxRetries)
}

// DecrementStorageUsage decrements storage usage counters for a peer
func (ldb *LocalStoreDB) DecrementStorageUsage(peerID string, messageCount int, bytes int64) error {
	query := `
		UPDATE local_storage_limits
		SET message_count = MAX(0, message_count - ?),
		    total_bytes = MAX(0, total_bytes - ?),
		    updated_at = ?
		WHERE peer_id = ?
	`

	_, err := ldb.db.Exec(query, messageCount, bytes, time.Now().Unix(), peerID)
	if err != nil {
		return fmt.Errorf("failed to decrement storage usage: %v", err)
	}

	return nil
}

// GetStats returns local store database statistics
func (ldb *LocalStoreDB) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	// Total pending messages
	var totalPending, totalBytes int64
	ldb.db.QueryRow("SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(payload_size), 0) FROM local_pending_messages WHERE status = 'pending'").
		Scan(&totalPending, &totalBytes)
	stats["total_pending"] = totalPending
	stats["total_bytes"] = totalBytes

	// Count by status
	rows, err := ldb.db.Query("SELECT status, COUNT(*) FROM local_pending_messages GROUP BY status")
	if err == nil {
		defer rows.Close()
		statusCounts := make(map[string]int64)
		for rows.Next() {
			var status string
			var count int64
			if err := rows.Scan(&status, &count); err == nil {
				statusCounts[status] = count
			}
		}
		stats["status_counts"] = statusCounts
	}

	// Peers with pending messages
	var peersWithPending int64
	ldb.db.QueryRow("SELECT COUNT(DISTINCT target_peer_id) FROM local_pending_messages WHERE status = 'pending'").
		Scan(&peersWithPending)
	stats["peers_with_pending"] = peersWithPending

	return stats
}

// Close closes the local store database manager
func (ldb *LocalStoreDB) Close() error {
	// No resources to clean up currently
	return nil
}
