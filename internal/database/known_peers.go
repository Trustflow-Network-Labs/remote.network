package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// KnownPeer represents a minimal peer entry (peer_id, public_key)
// This is the new lightweight storage model for Phase 2
type KnownPeer struct {
	PeerID     string    // SHA1(public_key) - 40 hex chars (Ed25519-derived peer_id)
	DHTNodeID  string    // DHT node_id - 40 hex chars (for DHT routing, may differ from PeerID)
	PublicKey  []byte    // Ed25519 public key - 32 bytes
	IsRelay    bool      // Is this peer a relay? (from identity exchange)
	IsStore    bool      // Has BEP_44 storage enabled? (from identity exchange)
	LastSeen   time.Time // Last time we saw this peer
	Topic      string    // Topic this peer is associated with
	FirstSeen  time.Time // When we first discovered this peer
	Source     string    // "bootstrap", "peer_exchange", "dht", etc.
}

// KnownPeersManager manages the minimal known_peers storage
type KnownPeersManager struct {
	db     *sql.DB
	logger *utils.LogsManager

	// Prepared statements for performance
	insertStmt       *sql.Stmt
	updateStmt       *sql.Stmt
	getStmt          *sql.Stmt
	getAllStmt       *sql.Stmt
	getByTopicStmt   *sql.Stmt
	deleteStmt       *sql.Stmt
	cleanupStaleStmt *sql.Stmt
}

// NewKnownPeersManager creates a new known peers manager
func NewKnownPeersManager(db *sql.DB, logger *utils.LogsManager) (*KnownPeersManager, error) {
	kpm := &KnownPeersManager{
		db:     db,
		logger: logger,
	}

	// Create tables
	if err := kpm.createTables(); err != nil {
		return nil, err
	}

	// Prepare statements
	if err := kpm.prepareStatements(); err != nil {
		return nil, err
	}

	return kpm, nil
}

// createTables creates the known_peers table
func (kpm *KnownPeersManager) createTables() error {
	createTableSQL := `
-- Minimal peer storage: only (peer_id, public_key, is_relay, is_store, last_seen, topic)
-- Full metadata is queried from DHT on-demand and cached separately
CREATE TABLE IF NOT EXISTS known_peers (
	"peer_id" TEXT NOT NULL,
	"dht_node_id" TEXT,            -- DHT routing node_id (may differ from peer_id)
	"public_key" BLOB NOT NULL,
	"is_relay" INTEGER NOT NULL DEFAULT 0,  -- 0=false, 1=true (from identity exchange)
	"is_store" INTEGER NOT NULL DEFAULT 0,  -- 0=false, 1=true (has BEP_44 storage enabled)
	"last_seen" INTEGER NOT NULL,  -- Unix timestamp
	"topic" TEXT NOT NULL,
	"first_seen" INTEGER NOT NULL, -- Unix timestamp
	"source" TEXT NOT NULL DEFAULT 'peer_exchange',

	PRIMARY KEY(peer_id, topic)
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_known_peers_last_seen ON known_peers(last_seen);
CREATE INDEX IF NOT EXISTS idx_known_peers_topic ON known_peers(topic);
CREATE INDEX IF NOT EXISTS idx_known_peers_source ON known_peers(source);
CREATE INDEX IF NOT EXISTS idx_known_peers_peer_id ON known_peers(peer_id);
CREATE INDEX IF NOT EXISTS idx_known_peers_dht_node_id ON known_peers(dht_node_id);
CREATE INDEX IF NOT EXISTS idx_known_peers_is_relay ON known_peers(is_relay);
CREATE INDEX IF NOT EXISTS idx_known_peers_is_store ON known_peers(is_store);
CREATE INDEX IF NOT EXISTS idx_known_peers_relay_store ON known_peers(is_relay, is_store);
`

	_, err := kpm.db.ExecContext(context.Background(), createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create known_peers table: %v", err)
	}

	kpm.logger.Debug("Created known_peers table successfully (with is_store column)", "database")
	return nil
}

// prepareStatements prepares frequently used SQL statements
func (kpm *KnownPeersManager) prepareStatements() error {
	var err error

	// Insert new peer only (never overwrite existing entries)
	// If peer exists, only update last_seen timestamp via separate UpdateLastSeen call
	kpm.insertStmt, err = kpm.db.Prepare(`
		INSERT INTO known_peers (peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id, topic) DO UPDATE SET
			last_seen = excluded.last_seen,
			is_store = excluded.is_store
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %v", err)
	}

	// Update last_seen only
	kpm.updateStmt, err = kpm.db.Prepare(`
		UPDATE known_peers
		SET last_seen = ?
		WHERE peer_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare update statement: %v", err)
	}

	// Get single peer
	kpm.getStmt, err = kpm.db.Prepare(`
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		WHERE peer_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare get statement: %v", err)
	}

	// Get all peers
	kpm.getAllStmt, err = kpm.db.Prepare(`
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		ORDER BY last_seen DESC
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare getAll statement: %v", err)
	}

	// Get peers by topic
	kpm.getByTopicStmt, err = kpm.db.Prepare(`
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		WHERE topic = ?
		ORDER BY last_seen DESC
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare getByTopic statement: %v", err)
	}

	// Delete peer
	kpm.deleteStmt, err = kpm.db.Prepare(`
		DELETE FROM known_peers
		WHERE peer_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare delete statement: %v", err)
	}

	// Cleanup stale peers
	kpm.cleanupStaleStmt, err = kpm.db.Prepare(`
		DELETE FROM known_peers
		WHERE last_seen < ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare cleanup statement: %v", err)
	}

	kpm.logger.Debug("Prepared known_peers statements successfully", "database")
	return nil
}

// StoreKnownPeer stores or updates a known peer
func (kpm *KnownPeersManager) StoreKnownPeer(peer *KnownPeer) error {
	now := time.Now()
	if peer.LastSeen.IsZero() {
		peer.LastSeen = now
	}
	if peer.FirstSeen.IsZero() {
		peer.FirstSeen = now
	}
	if peer.Source == "" {
		peer.Source = "peer_exchange"
	}

	// Convert bool to int for SQLite
	isRelayInt := 0
	if peer.IsRelay {
		isRelayInt = 1
	}

	isStoreInt := 0
	if peer.IsStore {
		isStoreInt = 1
	}

	_, err := kpm.insertStmt.Exec(
		peer.PeerID,
		peer.DHTNodeID,
		peer.PublicKey,
		isRelayInt,
		isStoreInt,
		peer.LastSeen.Unix(),
		peer.Topic,
		peer.FirstSeen.Unix(),
		peer.Source,
	)

	if err != nil {
		return fmt.Errorf("failed to store known peer: %v", err)
	}

	kpm.logger.Debug(fmt.Sprintf("Stored known peer: %s (topic: %s, source: %s)",
		peer.PeerID, peer.Topic, peer.Source), "database")

	return nil
}

// UpdateLastSeen updates only the last_seen timestamp for a peer
func (kpm *KnownPeersManager) UpdateLastSeen(peerID, topic string) error {
	now := time.Now().Unix()

	_, err := kpm.updateStmt.Exec(now, peerID, topic)
	if err != nil {
		return fmt.Errorf("failed to update last_seen: %v", err)
	}

	return nil
}

// GetKnownPeerByNodeID retrieves a known peer by DHT node_id or peer_id
// This is useful when you have the DHT node_id from peer_metadata and need the public key
func (kpm *KnownPeersManager) GetKnownPeerByNodeID(nodeID, topic string) (*KnownPeer, error) {
	// First try as peer_id (Ed25519-derived)
	peer, err := kpm.GetKnownPeer(nodeID, topic)
	if err != nil {
		return nil, err
	}
	if peer != nil {
		return peer, nil
	}

	// Try as dht_node_id (DHT routing node_id)
	query := `
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		WHERE dht_node_id = ? AND topic = ?
	`

	var lastSeenUnix, firstSeenUnix int64
	var isRelayInt, isStoreInt int
	var dhtNodeID sql.NullString
	var foundPeer KnownPeer

	err = kpm.db.QueryRow(query, nodeID, topic).Scan(
		&foundPeer.PeerID,
		&dhtNodeID,
		&foundPeer.PublicKey,
		&isRelayInt,
		&isStoreInt,
		&lastSeenUnix,
		&foundPeer.Topic,
		&firstSeenUnix,
		&foundPeer.Source,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Not found by either ID
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get known peer by dht_node_id: %v", err)
	}

	if dhtNodeID.Valid {
		foundPeer.DHTNodeID = dhtNodeID.String
	}
	foundPeer.IsRelay = (isRelayInt == 1)
	foundPeer.IsStore = (isStoreInt == 1)
	foundPeer.LastSeen = time.Unix(lastSeenUnix, 0)
	foundPeer.FirstSeen = time.Unix(firstSeenUnix, 0)

	return &foundPeer, nil
}

// GetKnownPeer retrieves a single known peer by peer_id
func (kpm *KnownPeersManager) GetKnownPeer(peerID, topic string) (*KnownPeer, error) {
	var peer KnownPeer
	var lastSeenUnix, firstSeenUnix int64
	var isRelayInt, isStoreInt int
	var dhtNodeID sql.NullString

	err := kpm.getStmt.QueryRow(peerID, topic).Scan(
		&peer.PeerID,
		&dhtNodeID,
		&peer.PublicKey,
		&isRelayInt,
		&isStoreInt,
		&lastSeenUnix,
		&peer.Topic,
		&firstSeenUnix,
		&peer.Source,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Peer not found
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get known peer: %v", err)
	}

	if dhtNodeID.Valid {
		peer.DHTNodeID = dhtNodeID.String
	}
	peer.IsRelay = (isRelayInt == 1)
	peer.IsStore = (isStoreInt == 1)
	peer.LastSeen = time.Unix(lastSeenUnix, 0)
	peer.FirstSeen = time.Unix(firstSeenUnix, 0)

	return &peer, nil
}

// GetAllKnownPeers retrieves all known peers (sorted by last_seen DESC)
func (kpm *KnownPeersManager) GetAllKnownPeers() ([]*KnownPeer, error) {
	rows, err := kpm.getAllStmt.Query()
	if err != nil {
		return nil, fmt.Errorf("failed to query all known peers: %v", err)
	}
	defer rows.Close()

	return kpm.scanKnownPeers(rows)
}

// GetKnownPeersByTopic retrieves all known peers for a specific topic
func (kpm *KnownPeersManager) GetKnownPeersByTopic(topic string) ([]*KnownPeer, error) {
	rows, err := kpm.getByTopicStmt.Query(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to query known peers by topic: %v", err)
	}
	defer rows.Close()

	return kpm.scanKnownPeers(rows)
}

// GetRecentKnownPeers retrieves the N most recently seen peers
func (kpm *KnownPeersManager) GetRecentKnownPeers(limit int, topic string) ([]*KnownPeer, error) {
	query := `
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		WHERE topic = ?
		ORDER BY last_seen DESC
		LIMIT ?
	`

	rows, err := kpm.db.Query(query, topic, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent known peers: %v", err)
	}
	defer rows.Close()

	return kpm.scanKnownPeers(rows)
}

// DeleteKnownPeer deletes a known peer
func (kpm *KnownPeersManager) DeleteKnownPeer(peerID, topic string) error {
	_, err := kpm.deleteStmt.Exec(peerID, topic)
	if err != nil {
		return fmt.Errorf("failed to delete known peer: %v", err)
	}

	kpm.logger.Debug(fmt.Sprintf("Deleted known peer: %s (topic: %s)", peerID, topic), "database")
	return nil
}

// CleanupStalePeers removes peers that haven't been seen in the specified duration
func (kpm *KnownPeersManager) CleanupStalePeers(maxAge time.Duration) (int, error) {
	cutoffTime := time.Now().Add(-maxAge).Unix()

	result, err := kpm.cleanupStaleStmt.Exec(cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup stale peers: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()

	if rowsAffected > 0 {
		kpm.logger.Info(fmt.Sprintf("Cleaned up %d stale known peers (max_age: %v)",
			rowsAffected, maxAge), "database")
	}

	return int(rowsAffected), nil
}

// GetKnownPeersCount returns the total number of known peers
func (kpm *KnownPeersManager) GetKnownPeersCount() (int, error) {
	var count int
	err := kpm.db.QueryRow("SELECT COUNT(*) FROM known_peers").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get known peers count: %v", err)
	}
	return count, nil
}

// GetKnownPeersCountByTopic returns the count of known peers for a topic
func (kpm *KnownPeersManager) GetKnownPeersCountByTopic(topic string) (int, error) {
	var count int
	err := kpm.db.QueryRow("SELECT COUNT(*) FROM known_peers WHERE topic = ?", topic).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get known peers count by topic: %v", err)
	}
	return count, nil
}

// scanKnownPeers is a helper to scan multiple rows into KnownPeer structs
func (kpm *KnownPeersManager) scanKnownPeers(rows *sql.Rows) ([]*KnownPeer, error) {
	var peers []*KnownPeer

	for rows.Next() {
		var peer KnownPeer
		var lastSeenUnix, firstSeenUnix int64
		var isRelayInt, isStoreInt int
		var dhtNodeID sql.NullString

		err := rows.Scan(
			&peer.PeerID,
			&dhtNodeID,
			&peer.PublicKey,
			&isRelayInt,
			&isStoreInt,
			&lastSeenUnix,
			&peer.Topic,
			&firstSeenUnix,
			&peer.Source,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan known peer row: %v", err)
		}

		if dhtNodeID.Valid {
			peer.DHTNodeID = dhtNodeID.String
		}

		peer.IsRelay = (isRelayInt == 1)
		peer.IsStore = (isStoreInt == 1)
		peer.LastSeen = time.Unix(lastSeenUnix, 0)
		peer.FirstSeen = time.Unix(firstSeenUnix, 0)

		peers = append(peers, &peer)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating known peers rows: %v", err)
	}

	return peers, nil
}

// GetRelayPeers returns all relay peers for a topic
func (kpm *KnownPeersManager) GetRelayPeers(topic string) ([]*KnownPeer, error) {
	query := `
		SELECT peer_id, dht_node_id, public_key, is_relay, is_store, last_seen, topic, first_seen, source
		FROM known_peers
		WHERE topic = ? AND is_relay = 1
		ORDER BY last_seen DESC
	`

	rows, err := kpm.db.Query(query, topic)
	if err != nil {
		return nil, fmt.Errorf("failed to query relay peers: %v", err)
	}
	defer rows.Close()

	return kpm.scanKnownPeers(rows)
}

// PublicKeyHex returns the public key as a hex string (for debugging)
func (kp *KnownPeer) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey)
}

// Close closes prepared statements
func (kpm *KnownPeersManager) Close() error {
	statements := []*sql.Stmt{
		kpm.insertStmt,
		kpm.updateStmt,
		kpm.getStmt,
		kpm.getAllStmt,
		kpm.getByTopicStmt,
		kpm.deleteStmt,
		kpm.cleanupStaleStmt,
	}

	for _, stmt := range statements {
		if stmt != nil {
			stmt.Close()
		}
	}

	kpm.logger.Debug("Closed known_peers prepared statements", "database")
	return nil
}
