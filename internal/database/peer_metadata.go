package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PeerMetadataDB handles SQLite operations for peer metadata with concurrent access safety
type PeerMetadataDB struct {
	db     *sql.DB
	logger *utils.LogsManager

	// Mutex for protecting concurrent database operations
	mutex sync.RWMutex

	// Prepared statements for performance
	insertStmt    *sql.Stmt
	updateStmt    *sql.Stmt
	selectStmt    *sql.Stmt
	deleteStmt    *sql.Stmt
	cleanupStmt   *sql.Stmt

	// Connection pool settings
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
}

// PeerMetadata represents comprehensive peer information
type PeerMetadata struct {
	// Core Identity
	NodeID    string    `json:"node_id" db:"node_id"`
	Topic     string    `json:"topic" db:"topic"`
	Version   int       `json:"version" db:"version"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`

	// Network Connectivity
	NetworkInfo NetworkInfo `json:"network_info" db:"network_info"`

	// Capabilities & Services
	Capabilities []string              `json:"capabilities" db:"capabilities"`
	Services     map[string]Service    `json:"services" db:"services"`
	Extensions   map[string]interface{} `json:"extensions" db:"extensions"`

	// Database metadata
	LastSeen  time.Time `json:"last_seen" db:"last_seen"`
	Source    string    `json:"source" db:"source"` // "dht", "quic", "manual"
}

// NetworkInfo contains network connectivity details
type NetworkInfo struct {
	// Public connectivity
	PublicIP   string `json:"public_ip,omitempty"`
	PublicPort int    `json:"public_port,omitempty"`

	// Private connectivity (NAT)
	PrivateIP   string `json:"private_ip,omitempty"`
	PrivatePort int    `json:"private_port,omitempty"`

	// Node characteristics
	NodeType       string   `json:"node_type"`               // "public" or "private"
	NATType        string   `json:"nat_type,omitempty"`      // "full_cone", "restricted", "symmetric", etc.
	SupportsUPnP   bool     `json:"supports_upnp,omitempty"`
	RelayEndpoints []string `json:"relay_endpoints,omitempty"`

	// Relay service info (if node offers relay services)
	IsRelay         bool    `json:"is_relay,omitempty"`
	RelayEndpoint   string  `json:"relay_endpoint,omitempty"`   // "IP:Port" for relay connections
	RelayPricing    float64 `json:"relay_pricing,omitempty"`    // Price per GB
	RelayCapacity   int     `json:"relay_capacity,omitempty"`   // Max concurrent connections
	ReputationScore float64 `json:"reputation_score,omitempty"` // Relay reputation (0.0 - 1.0)

	// NAT peer relay connection info (if using relay)
	UsingRelay      bool   `json:"using_relay,omitempty"`
	ConnectedRelay  string `json:"connected_relay,omitempty"`  // Relay NodeID
	RelaySessionID  string `json:"relay_session_id,omitempty"` // Session ID for routing
	RelayAddress    string `json:"relay_address,omitempty"`    // How to reach this peer via relay

	// Protocol support
	Protocols []Protocol `json:"protocols"`
}

// Protocol describes a supported network protocol
type Protocol struct {
	Name string                 `json:"name"` // "quic", "tcp", "udp"
	Port int                    `json:"port"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// Service describes capabilities or services offered by the peer
type Service struct {
	Type         string                 `json:"type"`         // "storage", "gpu", "docker", "wasm"
	Endpoint     string                 `json:"endpoint"`
	Capabilities map[string]interface{} `json:"capabilities"`
	Status       string                 `json:"status"`       // "available", "busy", "offline"
}

// NewPeerMetadataDB creates a new peer metadata database manager with proper connection pooling
func NewPeerMetadataDB(db *sql.DB, logger *utils.LogsManager) (*PeerMetadataDB, error) {
	pmdb := &PeerMetadataDB{
		db:              db,
		logger:          logger,
		maxOpenConns:    10,                // Allow multiple concurrent connections
		maxIdleConns:    5,                 // Keep some connections idle for quick access
		connMaxLifetime: 30 * time.Minute,  // Refresh connections periodically
	}

	// Configure connection pool for concurrent access
	pmdb.configureConnectionPool()

	// Create tables if they don't exist
	if err := pmdb.createTables(); err != nil {
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// Prepare frequently used statements
	if err := pmdb.prepareStatements(); err != nil {
		return nil, fmt.Errorf("failed to prepare statements: %v", err)
	}

	return pmdb, nil
}

// configureConnectionPool sets up the database connection pool for concurrent access
func (pmdb *PeerMetadataDB) configureConnectionPool() {
	// Configure connection pool to handle concurrent goroutines
	pmdb.db.SetMaxOpenConns(pmdb.maxOpenConns)
	pmdb.db.SetMaxIdleConns(pmdb.maxIdleConns)
	pmdb.db.SetConnMaxLifetime(pmdb.connMaxLifetime)

	pmdb.logger.Info(fmt.Sprintf("Configured database connection pool: max_open=%d, max_idle=%d, max_lifetime=%v",
		pmdb.maxOpenConns, pmdb.maxIdleConns, pmdb.connMaxLifetime), "database")
}

// createTables creates the peer metadata tables
func (pmdb *PeerMetadataDB) createTables() error {
	createPeerMetadataTableSQL := `
CREATE TABLE IF NOT EXISTS peer_metadata (
	"id" INTEGER PRIMARY KEY AUTOINCREMENT,
	"node_id" TEXT NOT NULL,
	"topic" TEXT NOT NULL,
	"version" INTEGER NOT NULL DEFAULT 1,
	"timestamp" TEXT NOT NULL,
	"last_seen" TEXT NOT NULL,
	"source" TEXT NOT NULL DEFAULT 'quic',

	-- Network connectivity (JSON)
	"network_info" TEXT NOT NULL,

	-- Capabilities and services (JSON)
	"capabilities" TEXT,
	"services" TEXT,
	"extensions" TEXT,

	-- Constraints
	UNIQUE("node_id", "topic")
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_peer_metadata_node_id ON peer_metadata("node_id");
CREATE INDEX IF NOT EXISTS idx_peer_metadata_topic ON peer_metadata("topic");
CREATE INDEX IF NOT EXISTS idx_peer_metadata_last_seen ON peer_metadata("last_seen");
CREATE INDEX IF NOT EXISTS idx_peer_metadata_source ON peer_metadata("source");

-- Table for peer connectivity status tracking
CREATE TABLE IF NOT EXISTS peer_connectivity (
	"id" INTEGER PRIMARY KEY AUTOINCREMENT,
	"node_id" TEXT NOT NULL,
	"topic" TEXT NOT NULL,
	"last_connection_attempt" TEXT,
	"last_successful_connection" TEXT,
	"connection_failures" INTEGER DEFAULT 0,
	"is_reachable" BOOLEAN DEFAULT TRUE,
	"rtt_ms" INTEGER DEFAULT 0,
	"created_at" TEXT NOT NULL,
	"updated_at" TEXT NOT NULL,

	UNIQUE("node_id", "topic"),
	FOREIGN KEY("node_id", "topic") REFERENCES peer_metadata("node_id", "topic") ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_peer_connectivity_reachable ON peer_connectivity("is_reachable");
CREATE INDEX IF NOT EXISTS idx_peer_connectivity_failures ON peer_connectivity("connection_failures");
`

	_, err := pmdb.db.ExecContext(context.Background(), createPeerMetadataTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create peer_metadata tables: %v", err)
	}

	pmdb.logger.Debug("Created peer metadata tables successfully", "database")
	return nil
}

// prepareStatements prepares frequently used SQL statements
func (pmdb *PeerMetadataDB) prepareStatements() error {
	var err error

	// Insert or replace peer metadata
	pmdb.insertStmt, err = pmdb.db.Prepare(`
		INSERT OR REPLACE INTO peer_metadata
		(node_id, topic, version, timestamp, last_seen, source, network_info, capabilities, services, extensions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %v", err)
	}

	// Update last seen timestamp
	pmdb.updateStmt, err = pmdb.db.Prepare(`
		UPDATE peer_metadata
		SET last_seen = ?, source = ?
		WHERE node_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare update statement: %v", err)
	}

	// Select peer metadata by node_id and topic
	pmdb.selectStmt, err = pmdb.db.Prepare(`
		SELECT node_id, topic, version, timestamp, last_seen, source,
		       network_info, capabilities, services, extensions
		FROM peer_metadata
		WHERE node_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare select statement: %v", err)
	}

	// Delete peer metadata
	pmdb.deleteStmt, err = pmdb.db.Prepare(`
		DELETE FROM peer_metadata
		WHERE node_id = ? AND topic = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare delete statement: %v", err)
	}

	// Cleanup old metadata (older than specified duration)
	pmdb.cleanupStmt, err = pmdb.db.Prepare(`
		DELETE FROM peer_metadata
		WHERE last_seen < ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare cleanup statement: %v", err)
	}

	pmdb.logger.Debug("Prepared SQL statements successfully", "database")
	return nil
}

// StorePeerMetadata stores or updates peer metadata with proper locking
func (pmdb *PeerMetadataDB) StorePeerMetadata(metadata *PeerMetadata) error {
	pmdb.mutex.Lock()
	defer pmdb.mutex.Unlock()
	// Serialize JSON fields
	networkInfoJSON, err := json.Marshal(metadata.NetworkInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal network_info: %v", err)
	}

	capabilitiesJSON, err := json.Marshal(metadata.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %v", err)
	}

	servicesJSON, err := json.Marshal(metadata.Services)
	if err != nil {
		return fmt.Errorf("failed to marshal services: %v", err)
	}

	extensionsJSON, err := json.Marshal(metadata.Extensions)
	if err != nil {
		return fmt.Errorf("failed to marshal extensions: %v", err)
	}

	// Set current time for last_seen if not provided
	if metadata.LastSeen.IsZero() {
		metadata.LastSeen = time.Now()
	}

	// Execute insert/replace
	_, err = pmdb.insertStmt.Exec(
		metadata.NodeID,
		metadata.Topic,
		metadata.Version,
		metadata.Timestamp.Format(time.RFC3339),
		metadata.LastSeen.Format(time.RFC3339),
		metadata.Source,
		string(networkInfoJSON),
		string(capabilitiesJSON),
		string(servicesJSON),
		string(extensionsJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to store peer metadata: %v", err)
	}

	pmdb.logger.Debug(fmt.Sprintf("Stored metadata for node %s, topic %s", metadata.NodeID, metadata.Topic), "database")
	return nil
}

// GetPeerMetadata retrieves peer metadata by node_id and topic with read lock
func (pmdb *PeerMetadataDB) GetPeerMetadata(nodeID, topic string) (*PeerMetadata, error) {
	pmdb.mutex.RLock()
	defer pmdb.mutex.RUnlock()
	var metadata PeerMetadata
	var timestampStr, lastSeenStr string
	var networkInfoJSON, capabilitiesJSON, servicesJSON, extensionsJSON string

	err := pmdb.selectStmt.QueryRow(nodeID, topic).Scan(
		&metadata.NodeID,
		&metadata.Topic,
		&metadata.Version,
		&timestampStr,
		&lastSeenStr,
		&metadata.Source,
		&networkInfoJSON,
		&capabilitiesJSON,
		&servicesJSON,
		&extensionsJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to get peer metadata: %v", err)
	}

	// Parse timestamps
	metadata.Timestamp, err = time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse timestamp: %v", err)
	}

	metadata.LastSeen, err = time.Parse(time.RFC3339, lastSeenStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse last_seen: %v", err)
	}

	// Unmarshal JSON fields
	if err := json.Unmarshal([]byte(networkInfoJSON), &metadata.NetworkInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network_info: %v", err)
	}

	if err := json.Unmarshal([]byte(capabilitiesJSON), &metadata.Capabilities); err != nil {
		return nil, fmt.Errorf("failed to unmarshal capabilities: %v", err)
	}

	if err := json.Unmarshal([]byte(servicesJSON), &metadata.Services); err != nil {
		return nil, fmt.Errorf("failed to unmarshal services: %v", err)
	}

	if err := json.Unmarshal([]byte(extensionsJSON), &metadata.Extensions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal extensions: %v", err)
	}

	return &metadata, nil
}

// GetPeersByTopic retrieves all peers for a specific topic with read lock
func (pmdb *PeerMetadataDB) GetPeersByTopic(topic string) ([]*PeerMetadata, error) {
	pmdb.mutex.RLock()
	defer pmdb.mutex.RUnlock()
	query := `
		SELECT node_id, topic, version, timestamp, last_seen, source,
		       network_info, capabilities, services, extensions
		FROM peer_metadata
		WHERE topic = ?
		ORDER BY last_seen DESC
	`

	rows, err := pmdb.db.Query(query, topic)
	if err != nil {
		return nil, fmt.Errorf("failed to query peers by topic: %v", err)
	}
	defer rows.Close()

	var peers []*PeerMetadata
	for rows.Next() {
		var metadata PeerMetadata
		var timestampStr, lastSeenStr string
		var networkInfoJSON, capabilitiesJSON, servicesJSON, extensionsJSON string

		err := rows.Scan(
			&metadata.NodeID,
			&metadata.Topic,
			&metadata.Version,
			&timestampStr,
			&lastSeenStr,
			&metadata.Source,
			&networkInfoJSON,
			&capabilitiesJSON,
			&servicesJSON,
			&extensionsJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		// Parse timestamps
		metadata.Timestamp, _ = time.Parse(time.RFC3339, timestampStr)
		metadata.LastSeen, _ = time.Parse(time.RFC3339, lastSeenStr)

		// Unmarshal JSON fields
		json.Unmarshal([]byte(networkInfoJSON), &metadata.NetworkInfo)
		json.Unmarshal([]byte(capabilitiesJSON), &metadata.Capabilities)
		json.Unmarshal([]byte(servicesJSON), &metadata.Services)
		json.Unmarshal([]byte(extensionsJSON), &metadata.Extensions)

		peers = append(peers, &metadata)
	}

	return peers, nil
}

// UpdateLastSeen updates the last seen timestamp for a peer with write lock
func (pmdb *PeerMetadataDB) UpdateLastSeen(nodeID, topic, source string) error {
	pmdb.mutex.Lock()
	defer pmdb.mutex.Unlock()
	_, err := pmdb.updateStmt.Exec(
		time.Now().Format(time.RFC3339),
		source,
		nodeID,
		topic,
	)

	if err != nil {
		return fmt.Errorf("failed to update last seen: %v", err)
	}

	return nil
}

// DeletePeerMetadata removes peer metadata with write lock
func (pmdb *PeerMetadataDB) DeletePeerMetadata(nodeID, topic string) error {
	pmdb.mutex.Lock()
	defer pmdb.mutex.Unlock()
	_, err := pmdb.deleteStmt.Exec(nodeID, topic)
	if err != nil {
		return fmt.Errorf("failed to delete peer metadata: %v", err)
	}

	pmdb.logger.Debug(fmt.Sprintf("Deleted metadata for node %s, topic %s", nodeID, topic), "database")
	return nil
}

// CleanupOldMetadata removes metadata older than the specified duration with write lock
func (pmdb *PeerMetadataDB) CleanupOldMetadata(maxAge time.Duration) (int64, error) {
	pmdb.mutex.Lock()
	defer pmdb.mutex.Unlock()
	cutoffTime := time.Now().Add(-maxAge)
	result, err := pmdb.cleanupStmt.Exec(cutoffTime.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old metadata: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %v", err)
	}

	if rowsAffected > 0 {
		pmdb.logger.Info(fmt.Sprintf("Cleaned up %d old peer metadata records", rowsAffected), "database")
	}

	return rowsAffected, nil
}

// Close closes prepared statements and database connection
func (pmdb *PeerMetadataDB) Close() error {
	if pmdb.insertStmt != nil {
		pmdb.insertStmt.Close()
	}
	if pmdb.updateStmt != nil {
		pmdb.updateStmt.Close()
	}
	if pmdb.selectStmt != nil {
		pmdb.selectStmt.Close()
	}
	if pmdb.deleteStmt != nil {
		pmdb.deleteStmt.Close()
	}
	if pmdb.cleanupStmt != nil {
		pmdb.cleanupStmt.Close()
	}

	return nil
}

// GetStats returns database statistics
func (pmdb *PeerMetadataDB) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	// Count total peers
	var totalPeers int
	pmdb.db.QueryRow("SELECT COUNT(*) FROM peer_metadata").Scan(&totalPeers)
	stats["total_peers"] = totalPeers

	// Count peers by topic
	rows, err := pmdb.db.Query("SELECT topic, COUNT(*) FROM peer_metadata GROUP BY topic")
	if err == nil {
		defer rows.Close()
		topicCounts := make(map[string]int)
		for rows.Next() {
			var topic string
			var count int
			if rows.Scan(&topic, &count) == nil {
				topicCounts[topic] = count
			}
		}
		stats["peers_by_topic"] = topicCounts
	}

	return stats
}