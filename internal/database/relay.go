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
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_peer_preferences_peer ON peer_preferences(peer_id);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createPreferencesTableSQL); err != nil {
		return fmt.Errorf("failed to create peer_preferences table: %v", err)
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

// Close closes the relay database manager
func (rdb *RelayDB) Close() error {
	// No resources to clean up currently
	return nil
}
