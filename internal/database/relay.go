package database

import (
	"context"
	"database/sql"
	"fmt"
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

	// Relay service info table
	createServiceTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_service_info (
		relay_node_id TEXT PRIMARY KEY,
		public_ip TEXT NOT NULL,
		public_port INTEGER NOT NULL,
		pricing_per_gb REAL NOT NULL DEFAULT 0.001,
		available_bandwidth INTEGER DEFAULT 0,
		reputation_score REAL DEFAULT 0.0,
		total_sessions INTEGER DEFAULT 0,
		total_bytes_relayed INTEGER DEFAULT 0,
		last_seen INTEGER NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_relay_service_last_seen ON relay_service_info(last_seen);
	CREATE INDEX IF NOT EXISTS idx_relay_service_reputation ON relay_service_info(reputation_score);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createServiceTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_service_info table: %v", err)
	}

	// Active relay sessions table
	createSessionsTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_sessions (
		session_id TEXT PRIMARY KEY,
		client_node_id TEXT NOT NULL,
		relay_node_id TEXT NOT NULL,
		session_type TEXT NOT NULL CHECK(session_type IN ('coordination', 'full_relay')),
		start_time INTEGER NOT NULL,
		end_time INTEGER,
		keepalive_interval INTEGER DEFAULT 30,
		last_keepalive INTEGER,
		ingress_bytes INTEGER DEFAULT 0,
		egress_bytes INTEGER DEFAULT 0,
		status TEXT DEFAULT 'active' CHECK(status IN ('active', 'inactive', 'terminated')),
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_relay_sessions_client ON relay_sessions(client_node_id);
	CREATE INDEX IF NOT EXISTS idx_relay_sessions_relay ON relay_sessions(relay_node_id);
	CREATE INDEX IF NOT EXISTS idx_relay_sessions_status ON relay_sessions(status);
	CREATE INDEX IF NOT EXISTS idx_relay_sessions_time ON relay_sessions(start_time, end_time);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createSessionsTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_sessions table: %v", err)
	}

	// Billing summary table
	createBillingTableSQL := `
	CREATE TABLE IF NOT EXISTS relay_billing (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		peer_id TEXT NOT NULL,
		relay_node_id TEXT NOT NULL,
		period_start INTEGER NOT NULL,
		period_end INTEGER NOT NULL,
		total_bytes INTEGER DEFAULT 0,
		coordination_bytes INTEGER DEFAULT 0,
		relay_bytes INTEGER DEFAULT 0,
		amount_due REAL DEFAULT 0.0,
		currency TEXT DEFAULT 'USD',
		payment_status TEXT DEFAULT 'unpaid' CHECK(payment_status IN ('unpaid', 'paid', 'waived')),
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
	CREATE INDEX IF NOT EXISTS idx_relay_billing_peer ON relay_billing(peer_id);
	CREATE INDEX IF NOT EXISTS idx_relay_billing_relay_node ON relay_billing(relay_node_id);
	CREATE INDEX IF NOT EXISTS idx_relay_billing_period ON relay_billing(period_start, period_end);
	CREATE INDEX IF NOT EXISTS idx_relay_billing_status ON relay_billing(payment_status);
	`

	if _, err := rdb.db.ExecContext(context.Background(), createBillingTableSQL); err != nil {
		return fmt.Errorf("failed to create relay_billing table: %v", err)
	}

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

// CreateSession creates a new relay session
func (rdb *RelayDB) CreateSession(sessionID, clientNodeID, relayNodeID, sessionType string, keepaliveInterval int) error {
	query := `
		INSERT INTO relay_sessions (
			session_id, client_node_id, relay_node_id, session_type,
			start_time, keepalive_interval, last_keepalive, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'active')
	`

	now := time.Now().Unix()
	_, err := rdb.db.Exec(query, sessionID, clientNodeID, relayNodeID, sessionType,
		now, keepaliveInterval, now)

	if err != nil {
		return fmt.Errorf("failed to create relay session: %v", err)
	}

	rdb.logger.Debug(fmt.Sprintf("Created relay session: %s (client: %s, relay: %s, type: %s)",
		sessionID, clientNodeID, relayNodeID, sessionType), "relay-db")

	return nil
}

// UpdateSessionTraffic updates traffic counters for an active session
func (rdb *RelayDB) UpdateSessionTraffic(sessionID string, ingressBytes, egressBytes int64) error {
	query := `
		UPDATE relay_sessions
		SET ingress_bytes = ingress_bytes + ?,
		    egress_bytes = egress_bytes + ?,
		    last_keepalive = ?
		WHERE session_id = ? AND status = 'active'
	`

	_, err := rdb.db.Exec(query, ingressBytes, egressBytes, time.Now().Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to update session traffic: %v", err)
	}

	return nil
}

// CloseSession closes an active relay session
func (rdb *RelayDB) CloseSession(sessionID string) error {
	query := `
		UPDATE relay_sessions
		SET end_time = ?,
		    status = 'terminated'
		WHERE session_id = ? AND status = 'active'
	`

	_, err := rdb.db.Exec(query, time.Now().Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to close relay session: %v", err)
	}

	rdb.logger.Debug(fmt.Sprintf("Closed relay session: %s", sessionID), "relay-db")
	return nil
}

// GetSessionStats returns statistics for a session
func (rdb *RelayDB) GetSessionStats(sessionID string) (map[string]interface{}, error) {
	query := `
		SELECT client_node_id, relay_node_id, session_type, start_time, end_time,
		       ingress_bytes, egress_bytes, status
		FROM relay_sessions
		WHERE session_id = ?
	`

	var clientNodeID, relayNodeID, sessionType, status string
	var startTime, ingressBytes, egressBytes int64
	var endTime sql.NullInt64

	err := rdb.db.QueryRow(query, sessionID).Scan(
		&clientNodeID, &relayNodeID, &sessionType, &startTime, &endTime,
		&ingressBytes, &egressBytes, &status,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
		return nil, fmt.Errorf("failed to get session stats: %v", err)
	}

	stats := map[string]interface{}{
		"session_id":     sessionID,
		"client_node_id": clientNodeID,
		"relay_node_id":  relayNodeID,
		"session_type":   sessionType,
		"start_time":     time.Unix(startTime, 0),
		"ingress_bytes":  ingressBytes,
		"egress_bytes":   egressBytes,
		"status":         status,
	}

	if endTime.Valid {
		stats["end_time"] = time.Unix(endTime.Int64, 0)
		stats["duration"] = endTime.Int64 - startTime
	}

	return stats, nil
}

// UpsertRelayService creates or updates relay service information
func (rdb *RelayDB) UpsertRelayService(relayNodeID, publicIP string, publicPort int, pricingPerGB float64) error {
	query := `
		INSERT INTO relay_service_info (
			relay_node_id, public_ip, public_port, pricing_per_gb, last_seen, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(relay_node_id) DO UPDATE SET
			public_ip = excluded.public_ip,
			public_port = excluded.public_port,
			pricing_per_gb = excluded.pricing_per_gb,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at
	`

	now := time.Now().Unix()
	_, err := rdb.db.Exec(query, relayNodeID, publicIP, publicPort, pricingPerGB, now, now)

	if err != nil {
		return fmt.Errorf("failed to upsert relay service: %v", err)
	}

	return nil
}

// GetAvailableRelays returns available relay services
func (rdb *RelayDB) GetAvailableRelays(maxAge time.Duration) ([]map[string]interface{}, error) {
	query := `
		SELECT relay_node_id, public_ip, public_port, pricing_per_gb,
		       reputation_score, total_sessions, last_seen
		FROM relay_service_info
		WHERE last_seen >= ?
		ORDER BY reputation_score DESC, pricing_per_gb ASC
		LIMIT 10
	`

	cutoff := time.Now().Add(-maxAge).Unix()
	rows, err := rdb.db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to get available relays: %v", err)
	}
	defer rows.Close()

	var relays []map[string]interface{}
	for rows.Next() {
		var relayNodeID, publicIP string
		var publicPort, totalSessions int
		var lastSeen int64
		var pricingPerGB, reputationScore float64

		err := rows.Scan(&relayNodeID, &publicIP, &publicPort, &pricingPerGB,
			&reputationScore, &totalSessions, &lastSeen)
		if err != nil {
			continue
		}

		relays = append(relays, map[string]interface{}{
			"relay_node_id":    relayNodeID,
			"public_ip":        publicIP,
			"public_port":      publicPort,
			"pricing_per_gb":   pricingPerGB,
			"reputation_score": reputationScore,
			"total_sessions":   totalSessions,
			"last_seen":        time.Unix(lastSeen, 0),
		})
	}

	return relays, nil
}

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

// CleanupOldSessions removes old inactive sessions
func (rdb *RelayDB) CleanupOldSessions(maxAge time.Duration) (int64, error) {
	query := `
		DELETE FROM relay_sessions
		WHERE status != 'active' AND end_time < ?
	`

	cutoff := time.Now().Add(-maxAge).Unix()
	result, err := rdb.db.Exec(query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old sessions: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

// GetStats returns relay database statistics
func (rdb *RelayDB) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	// Active sessions count
	var activeSessions int
	rdb.db.QueryRow("SELECT COUNT(*) FROM relay_sessions WHERE status = 'active'").Scan(&activeSessions)
	stats["active_sessions"] = activeSessions

	// Total traffic
	var totalIngress, totalEgress int64
	rdb.db.QueryRow("SELECT COALESCE(SUM(ingress_bytes), 0), COALESCE(SUM(egress_bytes), 0) FROM relay_traffic").
		Scan(&totalIngress, &totalEgress)
	stats["total_ingress_bytes"] = totalIngress
	stats["total_egress_bytes"] = totalEgress
	stats["total_bytes"] = totalIngress + totalEgress

	// Available relays
	var availableRelays int
	rdb.db.QueryRow("SELECT COUNT(*) FROM relay_service_info WHERE last_seen >= ?",
		time.Now().Add(-5*time.Minute).Unix()).Scan(&availableRelays)
	stats["available_relays"] = availableRelays

	return stats
}

// Close closes the relay database manager
func (rdb *RelayDB) Close() error {
	// No resources to clean up currently
	return nil
}
