package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// RelayDB manages relay traffic and billing data
type RelayDB struct {
	db     *sql.DB
	logger *utils.LogsManager
}

// NewRelayDB creates a new relay database manager
func NewRelayDB(db *sql.DB, logger *utils.LogsManager) (*RelayDB, error) {
	rdb := &RelayDB{
		db:     db,
		logger: logger,
	}

	// Create tables
	if err := rdb.createTables(); err != nil {
		return nil, err
	}

	logger.Info("Relay database manager initialized", "relay-db")
	return rdb, nil
}

// createTables creates relay-related database tables
func (rdb *RelayDB) createTables() error {
	// Relay traffic log table
	createTrafficTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_traffic (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		peer_id TEXT NOT NULL,
		relay_node_id TEXT NOT NULL,
		traffic_type TEXT NOT NULL CHECK(traffic_type IN ('coordination', 'relay')),
		ingress_bytes INTEGER DEFAULT 0,
		egress_bytes INTEGER DEFAULT 0,
		start_time INTEGER NOT NULL,
		end_time INTEGER,
		protocol TEXT,
		billing_status TEXT DEFAULT 'unpaid' CHECK(billing_status IN ('unpaid', 'paid', 'free')),
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_session ON relay_traffic(session_id);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_peer ON relay_traffic(peer_id);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_relay_node ON relay_traffic(relay_node_id);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_type ON relay_traffic(traffic_type);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_billing ON relay_traffic(billing_status);
	CREATE INDEX IF NOT EXISTS idx_relay_traffic_time ON relay_traffic(start_time, end_time);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createTrafficTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_traffic table: %v", err)
	}

	// Peer preferences table for storing preferred relay
	createPreferencesTableSQL := `
	CREATE TABLE IF NOT EXISTS peer_preferences (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		peer_id TEXT NOT NULL UNIQUE,
		preferred_relay_peer_id TEXT,
		last_used_relay_peer_id TEXT,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_peer_preferences_peer ON peer_preferences(peer_id);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createPreferencesTableSQL); err != nil {
		return fmt.Errorf("failed to create peer_preferences table: %v", err)
	}

	// Migration: Add last_used_relay_peer_id column if it doesn't exist (for existing databases)
	_, err := rdb.db.ExecContext(context.Background(), `
		ALTER TABLE peer_preferences ADD COLUMN last_used_relay_peer_id TEXT;
	`)
	if err != nil {
		// Ignore error if column already exists (expected on existing databases)
		if !strings.Contains(err.Error(), "duplicate column name") {
			rdb.logger.Warn(fmt.Sprintf("Failed to add last_used_relay_peer_id column (may already exist): %v", err), "relay-db")
		}
	} else {
		rdb.logger.Info("Added last_used_relay_peer_id column to peer_preferences table", "relay-db")
	}

	// Store-and-forward: Pending messages table
	createPendingMessagesTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_pending_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id TEXT UNIQUE NOT NULL,
		correlation_id TEXT,
		source_peer_id TEXT NOT NULL,
		target_peer_id TEXT NOT NULL,
		relay_node_id TEXT NOT NULL,
		message_type TEXT NOT NULL,
		payload BLOB NOT NULL,
		payload_size INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		delivery_attempts INTEGER DEFAULT 0,
		last_delivery_attempt INTEGER,
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'expired', 'failed')),
		delivered_at INTEGER,
		priority INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_rpm_target_peer ON relay_pending_messages(target_peer_id, status, expires_at);
	CREATE INDEX IF NOT EXISTS idx_rpm_expires ON relay_pending_messages(expires_at, status);
	CREATE INDEX IF NOT EXISTS idx_rpm_message_id ON relay_pending_messages(message_id);
	CREATE INDEX IF NOT EXISTS idx_rpm_created ON relay_pending_messages(created_at);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createPendingMessagesTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_pending_messages table: %v", err)
	}

	// Store-and-forward: Storage limits tracking table
	createStorageLimitsTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_storage_limits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		peer_id TEXT UNIQUE NOT NULL,
		message_count INTEGER DEFAULT 0,
		total_bytes INTEGER DEFAULT 0,
		max_message_count INTEGER,
		max_total_bytes INTEGER,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_rsl_peer ON relay_storage_limits(peer_id);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createStorageLimitsTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_storage_limits table: %v", err)
	}

	// Note: relay_service_info table removed - relay discovery uses DHT metadata, not database
	// Note: relay_sessions table removed - sessions are in-memory only per DHT-only architecture
	// Note: relay_billing table removed - no billing implementation exists

	rdb.logger.Info("Relay database tables created successfully", "relay-db")
	return nil
}

// RecordTraffic records relay traffic for a session
func (rdb *RelayDB) RecordTraffic(sessionID, peerID, relayNodeID, trafficType string, ingressBytes, egressBytes int64, startTime, endTime time.Time) error {
	query := `
		INSERT INTO relay_traffic (
			session_id, peer_id, relay_node_id, traffic_type,
			ingress_bytes, egress_bytes, start_time, end_time,
			billing_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	// Coordination traffic is free
	billingStatus := "unpaid"
	if trafficType == "coordination" {
		billingStatus = "free"
	}

	var endTimeUnix *int64
	if !endTime.IsZero() {
		unix := endTime.Unix()
		endTimeUnix = &unix
	}

	_, err := rdb.db.Exec(query, sessionID, peerID, relayNodeID, trafficType,
		ingressBytes, egressBytes, startTime.Unix(), endTimeUnix, billingStatus)

	if err != nil {
		return fmt.Errorf("failed to record relay traffic: %v", err)
	}

	return nil
}

// Note: Session management methods removed - relay sessions are now stored in-memory only.
// All session operations (CreateSession, UpdateSessionTraffic, UpdateSessionKeepalive,
// CloseSession, GetSessionStats, HasActiveSession) use the in-memory maps in RelayPeer struct.

// Note: Relay service discovery methods removed (UpsertRelayService, GetAvailableRelays).
// Relay discovery uses DHT metadata only per DHT-only architecture.

// GetPeerTrafficSummary returns traffic summary for a peer
func (rdb *RelayDB) GetPeerTrafficSummary(peerID string, since time.Time) (map[string]interface{}, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN traffic_type = 'coordination' THEN ingress_bytes + egress_bytes ELSE 0 END), 0) as coordination_bytes,
			COALESCE(SUM(CASE WHEN traffic_type = 'relay' THEN ingress_bytes + egress_bytes ELSE 0 END), 0) as relay_bytes,
			COUNT(DISTINCT session_id) as session_count
		FROM relay_traffic
		WHERE peer_id = ? AND start_time >= ?
	`

	var coordinationBytes, relayBytes int64
	var sessionCount int

	err := rdb.db.QueryRow(query, peerID, since.Unix()).Scan(
		&coordinationBytes, &relayBytes, &sessionCount,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get peer traffic summary: %v", err)
	}

	return map[string]interface{}{
		"peer_id":            peerID,
		"coordination_bytes": coordinationBytes,
		"relay_bytes":        relayBytes,
		"total_bytes":        coordinationBytes + relayBytes,
		"session_count":      sessionCount,
		"free_bytes":         coordinationBytes,
		"billable_bytes":     relayBytes,
	}, nil
}

// CleanupOldSessions is now a no-op since sessions are in-memory only
// Sessions are automatically cleaned up when they timeout or disconnect
func (rdb *RelayDB) CleanupOldSessions(maxAge time.Duration) (int64, error) {
	// Note: Sessions are now stored in-memory only and cleaned up automatically
	// No database cleanup needed
	return 0, nil
}

// GetStats returns relay database statistics (traffic only)
// Note: Active sessions should be counted from in-memory RelayPeer.sessions map
// Note: Available relays should be queried from known_peers table (is_relay=1)
func (rdb *RelayDB) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	// Total traffic (kept for billing/analytics)
	var totalIngress, totalEgress int64
	rdb.db.QueryRow("SELECT COALESCE(SUM(ingress_bytes), 0), COALESCE(SUM(egress_bytes), 0) FROM relay_traffic").
		Scan(&totalIngress, &totalEgress)
	stats["total_ingress_bytes"] = totalIngress
	stats["total_egress_bytes"] = totalEgress
	stats["total_bytes"] = totalIngress + totalEgress

	return stats
}

// SetPreferredRelay sets the preferred relay for a peer
func (rdb *RelayDB) SetPreferredRelay(peerID, preferredRelayPeerID string) error {
	query := `
		INSERT INTO peer_preferences (peer_id, preferred_relay_peer_id, updated_at)
		VALUES (?, ?, strftime('%s', 'now'))
		ON CONFLICT(peer_id) DO UPDATE SET
			preferred_relay_peer_id = excluded.preferred_relay_peer_id,
			updated_at = excluded.updated_at
	`

	// Retry logic for handling transient SQLite BUSY errors
	maxRetries := 3
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := rdb.db.Exec(query, peerID, preferredRelayPeerID)
		if err == nil {
			rdb.logger.Debug(fmt.Sprintf("Set preferred relay for peer %s to %s", peerID, preferredRelayPeerID), "relay-db")
			return nil
		}

		// Check if it's a BUSY error
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			if attempt < maxRetries-1 {
				rdb.logger.Debug(fmt.Sprintf("Database busy, retrying in %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries), "relay-db")
				time.Sleep(retryDelay)
				retryDelay *= 2 // Exponential backoff
				continue
			}
		}

		return fmt.Errorf("failed to set preferred relay: %v", err)
	}

	return fmt.Errorf("failed to set preferred relay after %d attempts", maxRetries)
}

// GetPreferredRelay gets the preferred relay for a peer
func (rdb *RelayDB) GetPreferredRelay(peerID string) (string, error) {
	query := `SELECT preferred_relay_peer_id FROM peer_preferences WHERE peer_id = ?`

	var preferredRelayPeerID sql.NullString
	err := rdb.db.QueryRow(query, peerID).Scan(&preferredRelayPeerID)

	if err == sql.ErrNoRows {
		// No preference set
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("failed to get preferred relay: %v", err)
	}

	if !preferredRelayPeerID.Valid {
		return "", nil
	}

	return preferredRelayPeerID.String, nil
}

// ClearPreferredRelay clears the preferred relay for a peer
func (rdb *RelayDB) ClearPreferredRelay(peerID string) error {
	query := `DELETE FROM peer_preferences WHERE peer_id = ?`

	_, err := rdb.db.Exec(query, peerID)
	if err != nil {
		return fmt.Errorf("failed to clear preferred relay: %v", err)
	}

	rdb.logger.Debug(fmt.Sprintf("Cleared preferred relay for peer %s", peerID), "relay-db")
	return nil
}

// SetLastUsedRelay sets the last used relay for a peer
func (rdb *RelayDB) SetLastUsedRelay(peerID, lastUsedRelayPeerID string) error {
	query := `
		INSERT INTO peer_preferences (peer_id, last_used_relay_peer_id, updated_at)
		VALUES (?, ?, strftime('%s', 'now'))
		ON CONFLICT(peer_id) DO UPDATE SET
			last_used_relay_peer_id = excluded.last_used_relay_peer_id,
			updated_at = excluded.updated_at
	`

	// Retry logic for handling transient SQLite BUSY errors
	maxRetries := 3
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := rdb.db.Exec(query, peerID, lastUsedRelayPeerID)
		if err == nil {
			rdb.logger.Debug(fmt.Sprintf("Set last used relay for peer %s to %s", peerID, lastUsedRelayPeerID), "relay-db")
			return nil
		}

		// Check if it's a BUSY error
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			if attempt < maxRetries-1 {
				rdb.logger.Debug(fmt.Sprintf("Database busy, retrying in %v (attempt %d/%d)", retryDelay, attempt+1, maxRetries), "relay-db")
				time.Sleep(retryDelay)
				retryDelay *= 2 // Exponential backoff
				continue
			}
		}

		return fmt.Errorf("failed to set last used relay: %v", err)
	}

	return fmt.Errorf("failed to set last used relay after %d attempts", maxRetries)
}

// GetLastUsedRelay gets the last used relay for a peer
func (rdb *RelayDB) GetLastUsedRelay(peerID string) (string, error) {
	query := `SELECT last_used_relay_peer_id FROM peer_preferences WHERE peer_id = ?`

	var lastUsedRelayPeerID sql.NullString
	err := rdb.db.QueryRow(query, peerID).Scan(&lastUsedRelayPeerID)

	if err == sql.ErrNoRows {
		// No last used relay set
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("failed to get last used relay: %v", err)
	}

	if !lastUsedRelayPeerID.Valid {
		return "", nil
	}

	return lastUsedRelayPeerID.String, nil
}

// Close closes the relay database manager
func (rdb *RelayDB) Close() error {
	// No resources to clean up currently
	return nil
}

// ========================================
// Store-and-Forward Message Storage
// ========================================

// PendingMessage represents a message waiting for delivery
type PendingMessage struct {
	ID                   int64
	MessageID            string
	CorrelationID        string
	SourcePeerID         string
	TargetPeerID         string
	RelayNodeID          string
	MessageType          string
	Payload              []byte
	PayloadSize          int64
	CreatedAt            time.Time
	ExpiresAt            time.Time
	DeliveryAttempts     int
	LastDeliveryAttempt  time.Time
	Status               string
	DeliveredAt          time.Time
	Priority             int
}

// StorageUsage represents current storage usage for a peer
type StorageUsage struct {
	PeerID        string
	MessageCount  int64
	TotalBytes    int64
	MaxMessages   int64
	MaxBytes      int64
}

// StorePendingMessage stores a message for later delivery
func (rdb *RelayDB) StorePendingMessage(msg *PendingMessage) error {
	query := `
		INSERT INTO relay_pending_messages (
			message_id, correlation_id, source_peer_id, target_peer_id, relay_node_id,
			message_type, payload, payload_size, created_at, expires_at, status, priority
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := rdb.db.Exec(query,
		msg.MessageID,
		msg.CorrelationID,
		msg.SourcePeerID,
		msg.TargetPeerID,
		msg.RelayNodeID,
		msg.MessageType,
		msg.Payload,
		msg.PayloadSize,
		msg.CreatedAt.Unix(),
		msg.ExpiresAt.Unix(),
		msg.Status,
		msg.Priority,
	)

	if err != nil {
		return fmt.Errorf("failed to store pending message: %v", err)
	}

	rdb.logger.Debug(fmt.Sprintf("Stored pending message %s (type: %s, size: %d bytes, expires: %s)",
		msg.MessageID, msg.MessageType, msg.PayloadSize, msg.ExpiresAt.Format(time.RFC3339)), "relay-db")

	return nil
}

// GetPendingMessagesForPeer retrieves all pending messages for a target peer
func (rdb *RelayDB) GetPendingMessagesForPeer(targetPeerID string) ([]*PendingMessage, error) {
	query := `
		SELECT id, message_id, correlation_id, source_peer_id, target_peer_id, relay_node_id,
		       message_type, payload, payload_size, created_at, expires_at, delivery_attempts,
		       last_delivery_attempt, status, delivered_at, priority
		FROM relay_pending_messages
		WHERE target_peer_id = ? AND status = 'pending' AND expires_at > ?
		ORDER BY priority DESC, created_at ASC
		LIMIT 100
	`

	now := time.Now().Unix()
	rows, err := rdb.db.Query(query, targetPeerID, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending messages: %v", err)
	}
	defer rows.Close()

	var messages []*PendingMessage
	for rows.Next() {
		msg := &PendingMessage{}
		var createdAt, expiresAt int64
		var lastDeliveryAttempt, deliveredAt sql.NullInt64
		var correlationID sql.NullString

		err := rows.Scan(
			&msg.ID,
			&msg.MessageID,
			&correlationID,
			&msg.SourcePeerID,
			&msg.TargetPeerID,
			&msg.RelayNodeID,
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
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan pending message: %v", err)
		}

		if correlationID.Valid {
			msg.CorrelationID = correlationID.String
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

// MarkMessageDelivered marks a message as successfully delivered
func (rdb *RelayDB) MarkMessageDelivered(messageID string) error {
	query := `
		UPDATE relay_pending_messages
		SET status = 'delivered', delivered_at = ?
		WHERE message_id = ?
	`

	_, err := rdb.db.Exec(query, time.Now().Unix(), messageID)
	if err != nil {
		return fmt.Errorf("failed to mark message as delivered: %v", err)
	}

	return nil
}

// MarkMessageExpired marks a message as expired
func (rdb *RelayDB) MarkMessageExpired(messageID string) error {
	query := `
		UPDATE relay_pending_messages
		SET status = 'expired'
		WHERE message_id = ?
	`

	_, err := rdb.db.Exec(query, messageID)
	if err != nil {
		return fmt.Errorf("failed to mark message as expired: %v", err)
	}

	return nil
}

// IncrementDeliveryAttempt increments the delivery attempt counter
func (rdb *RelayDB) IncrementDeliveryAttempt(messageID string) error {
	query := `
		UPDATE relay_pending_messages
		SET delivery_attempts = delivery_attempts + 1,
		    last_delivery_attempt = ?
		WHERE message_id = ?
	`

	_, err := rdb.db.Exec(query, time.Now().Unix(), messageID)
	if err != nil {
		return fmt.Errorf("failed to increment delivery attempt: %v", err)
	}

	return nil
}

// DeleteExpiredMessages deletes messages that have expired or been delivered/expired beyond retention period
// Returns: (expiredPending, deliveredOld, expiredOld, error)
func (rdb *RelayDB) DeleteExpiredMessages(now time.Time, deliveredRetention time.Duration) (int64, int64, int64, error) {
	// 1. Delete expired pending messages (current behavior)
	queryExpiredPending := `
		DELETE FROM relay_pending_messages
		WHERE status = 'pending' AND expires_at < ?
	`
	result1, err := rdb.db.Exec(queryExpiredPending, now.Unix())
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to delete expired pending messages: %v", err)
	}
	expiredPending, _ := result1.RowsAffected()

	// 2. Delete delivered messages older than retention period (privacy/disk space)
	retentionCutoff := now.Add(-deliveredRetention).Unix()
	queryDelivered := `
		DELETE FROM relay_pending_messages
		WHERE status = 'delivered' AND delivered_at < ?
	`
	result2, err := rdb.db.Exec(queryDelivered, retentionCutoff)
	if err != nil {
		return expiredPending, 0, 0, fmt.Errorf("failed to delete old delivered messages: %v", err)
	}
	deliveredOld, _ := result2.RowsAffected()

	// 3. Delete expired messages older than retention period (privacy/disk space)
	// Use expires_at as proxy for when message was marked expired
	queryExpiredOld := `
		DELETE FROM relay_pending_messages
		WHERE status = 'expired' AND expires_at < ?
	`
	result3, err := rdb.db.Exec(queryExpiredOld, retentionCutoff)
	if err != nil {
		return expiredPending, deliveredOld, 0, fmt.Errorf("failed to delete old expired messages: %v", err)
	}
	expiredOld, _ := result3.RowsAffected()

	return expiredPending, deliveredOld, expiredOld, nil
}

// GetMessageStatus retrieves the status of a specific message
func (rdb *RelayDB) GetMessageStatus(messageID string) (map[string]interface{}, error) {
	query := `
		SELECT message_id, status, created_at, expires_at, delivered_at, message_type
		FROM relay_pending_messages
		WHERE message_id = ?
	`

	var msgID, status, messageType string
	var createdAt, expiresAt int64
	var deliveredAt sql.NullInt64

	err := rdb.db.QueryRow(query, messageID).Scan(
		&msgID, &status, &createdAt, &expiresAt, &deliveredAt, &messageType,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get message status: %v", err)
	}

	result := map[string]interface{}{
		"message_id":   msgID,
		"status":       status,
		"created_at":   time.Unix(createdAt, 0),
		"expires_at":   time.Unix(expiresAt, 0),
		"message_type": messageType,
	}

	if deliveredAt.Valid {
		result["delivered_at"] = time.Unix(deliveredAt.Int64, 0)
	}

	return result, nil
}

// GetStorageUsage retrieves current storage usage for a peer
func (rdb *RelayDB) GetStorageUsage(peerID string) (StorageUsage, error) {
	query := `SELECT peer_id, message_count, total_bytes, max_message_count, max_total_bytes FROM relay_storage_limits WHERE peer_id = ?`

	var usage StorageUsage
	var maxMessages, maxBytes sql.NullInt64

	err := rdb.db.QueryRow(query, peerID).Scan(
		&usage.PeerID,
		&usage.MessageCount,
		&usage.TotalBytes,
		&maxMessages,
		&maxBytes,
	)

	if err == sql.ErrNoRows {
		// No record exists, return zero usage
		return StorageUsage{
			PeerID:       peerID,
			MessageCount: 0,
			TotalBytes:   0,
		}, nil
	}

	if err != nil {
		return StorageUsage{}, fmt.Errorf("failed to get storage usage: %v", err)
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
func (rdb *RelayDB) IncrementStorageUsage(peerID string, messageCount int, bytes int64) error {
	query := `
		INSERT INTO relay_storage_limits (peer_id, message_count, total_bytes, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			message_count = message_count + excluded.message_count,
			total_bytes = total_bytes + excluded.total_bytes,
			updated_at = excluded.updated_at
	`

	_, err := rdb.db.Exec(query, peerID, messageCount, bytes, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("failed to increment storage usage: %v", err)
	}

	return nil
}

// DecrementStorageUsage decrements storage usage counters for a peer
func (rdb *RelayDB) DecrementStorageUsage(peerID string, messageCount int, bytes int64) error {
	query := `
		UPDATE relay_storage_limits
		SET message_count = MAX(0, message_count - ?),
		    total_bytes = MAX(0, total_bytes - ?),
		    updated_at = ?
		WHERE peer_id = ?
	`

	_, err := rdb.db.Exec(query, messageCount, bytes, time.Now().Unix(), peerID)
	if err != nil {
		return fmt.Errorf("failed to decrement storage usage: %v", err)
	}

	return nil
}
