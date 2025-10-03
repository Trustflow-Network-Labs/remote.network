package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	_ "modernc.org/sqlite"
)

// SQLiteManager handles all database operations including peer metadata
type SQLiteManager struct {
	dir string
	cm  *utils.ConfigManager
	db  *sql.DB

	// Specialized managers
	PeerMetadata *PeerMetadataDB
	Relay        *RelayDB
}

// NewSQLiteManager creates a new enhanced SQLite manager with peer metadata support
func NewSQLiteManager(cm *utils.ConfigManager) (*SQLiteManager, error) {
	paths := utils.GetAppPaths("")
	sqlm := &SQLiteManager{
		dir: paths.DataDir,
		cm:  cm,
	}

	// Create database connection
	db, err := sqlm.CreateConnection()
	if err != nil {
		return nil, fmt.Errorf("failed to create database connection: %v", err)
	}
	sqlm.db = db

	// Initialize specialized managers
	if err := sqlm.initializeManagers(); err != nil {
		return nil, fmt.Errorf("failed to initialize database managers: %v", err)
	}

	return sqlm, nil
}

// CreateConnection creates and configures the database connection
func (sqlm *SQLiteManager) CreateConnection() (*sql.DB, error) {
	logsManager := utils.NewLogsManager(sqlm.cm)
	defer logsManager.Close()

	// Make sure we have os specific path separator since we are adding this path to host's path
	dbFileName := sqlm.cm.GetConfigWithDefault("database_file", "./remote-network.db")
	switch runtime.GOOS {
	case "linux", "darwin":
		dbFileName = filepath.ToSlash(dbFileName)
	case "windows":
		dbFileName = filepath.FromSlash(dbFileName)
	default:
		err := fmt.Errorf("unsupported OS type `%s`", runtime.GOOS)
		return nil, err
	}

	// create db file path
	path := filepath.Join(sqlm.dir, dbFileName)

	// Check if DB exists
	var newDB bool = false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		newDB = true
	}

	// Init db connection with enhanced settings for concurrent access
	db, err := sql.Open("sqlite",
		fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1&_synchronous=NORMAL", path))
	if err != nil {
		message := fmt.Sprintf("Can not create database connection. (%s)", err.Error())
		logsManager.Log("error", message, "database")
		return nil, err
	}

	// Configure connection pool for concurrent access (enhanced from original)
	db.SetMaxOpenConns(10)           // Allow multiple concurrent connections
	db.SetMaxIdleConns(5)            // Keep some connections idle for quick access
	db.SetConnMaxLifetime(0)         // Connections never expire (SQLite handles this)

	// Explicitly enable foreign key enforcement
	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		message := fmt.Sprintf("Failed to enable foreign keys: %s", err.Error())
		logsManager.Log("error", message, "database")
		return nil, err
	}

	// Enable WAL mode for better concurrent access
	_, err = db.Exec("PRAGMA journal_mode = WAL;")
	if err != nil {
		message := fmt.Sprintf("Failed to enable WAL mode: %s", err.Error())
		logsManager.Log("warning", message, "database")
	}

	if newDB {
		// Create original database structure (keeping existing functionality)
		if err := sqlm.createOriginalTables(db, logsManager); err != nil {
			return nil, err
		}
	}

	return db, nil
}

// initializeManagers sets up specialized database managers
func (sqlm *SQLiteManager) initializeManagers() error {
	logsManager := utils.NewLogsManager(sqlm.cm)
	defer logsManager.Close()

	// Initialize peer metadata manager
	var err error
	sqlm.PeerMetadata, err = NewPeerMetadataDB(sqlm.db, logsManager)
	if err != nil {
		return fmt.Errorf("failed to initialize peer metadata manager: %v", err)
	}

	// Initialize relay database manager
	sqlm.Relay, err = NewRelayDB(sqlm.db, logsManager)
	if err != nil {
		return fmt.Errorf("failed to initialize relay database manager: %v", err)
	}

	logsManager.Info("Database managers initialized successfully", "database")
	return nil
}

// createOriginalTables creates the original database tables (keeping existing functionality)
func (sqlm *SQLiteManager) createOriginalTables(db *sql.DB, logsManager *utils.LogsManager) error {
	// This contains all the original table creation SQL from the trustflow-node
	// I'm keeping this minimal since we're focusing on the remote-network-node
	// but maintaining compatibility for future features

	createSettingsTableSql := `
CREATE TABLE IF NOT EXISTS settings (
	"key" TEXT PRIMARY KEY,
	"description" TEXT DEFAULT NULL,
	"setting_type" VARCHAR(10) CHECK( "setting_type" IN ('STRING', 'INTEGER', 'REAL', 'BOOLEAN', 'JSON') ) NOT NULL DEFAULT 'STRING',
	"value_string" TEXT DEFAULT NULL,
	"value_integer" INTEGER DEFAULT NULL,
	"value_real" REAL DEFAULT NULL,
	"value_boolean" INTEGER CHECK( "value_boolean" IN (0, 1) ) NOT NULL DEFAULT 0,
	"value_json" TEXT DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS settings_key_idx ON settings ("key");

INSERT INTO settings ("key", "description", "setting_type", "value_string") VALUES ('node_identifier', 'Node Identifier', 'STRING', '') ON CONFLICT(key) DO UPDATE SET "description" = 'Node Identifier', "setting_type" = 'STRING', "value_string" = '';
INSERT INTO settings ("key", "description", "setting_type", "value_boolean") VALUES ('enable_peer_metadata', 'Enable peer metadata exchange via QUIC', 'BOOLEAN', 1) ON CONFLICT(key) DO UPDATE SET "description" = 'Enable peer metadata exchange via QUIC', "setting_type" = 'BOOLEAN', "value_boolean" = 1;
INSERT INTO settings ("key", "description", "setting_type", "value_integer") VALUES ('metadata_cleanup_hours', 'Hours after which peer metadata is considered stale', 'INTEGER', 24) ON CONFLICT(key) DO UPDATE SET "description" = 'Hours after which peer metadata is considered stale', "setting_type" = 'INTEGER', "value_integer" = 24;
`

	_, err := db.ExecContext(context.Background(), createSettingsTableSql)
	if err != nil {
		message := fmt.Sprintf("Can not create `settings` table. (%s)", err.Error())
		logsManager.Log("error", message, "database")
		return err
	}

	// Create blacklisted nodes table for peer management
	createBlacklistedNodesTableSql := `
CREATE TABLE IF NOT EXISTS blacklisted_nodes (
	"node_id" TEXT PRIMARY KEY,
	"reason" TEXT DEFAULT NULL,
	"timestamp" TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS blacklisted_nodes_node_id_idx ON blacklisted_nodes ("node_id");
`
	_, err = db.ExecContext(context.Background(), createBlacklistedNodesTableSql)
	if err != nil {
		message := fmt.Sprintf("Can not create `blacklisted_nodes` table. (%s)", err.Error())
		logsManager.Log("error", message, "database")
		return err
	}

	logsManager.Info("Original database tables created successfully", "database")
	return nil
}

// GetDB returns the database connection for direct access if needed
func (sqlm *SQLiteManager) GetDB() *sql.DB {
	return sqlm.db
}

// Close closes all database connections and managers
func (sqlm *SQLiteManager) Close() error {
	if sqlm.PeerMetadata != nil {
		if err := sqlm.PeerMetadata.Close(); err != nil {
			return fmt.Errorf("failed to close peer metadata manager: %v", err)
		}
	}

	if sqlm.Relay != nil {
		if err := sqlm.Relay.Close(); err != nil {
			return fmt.Errorf("failed to close relay manager: %v", err)
		}
	}

	if sqlm.db != nil {
		return sqlm.db.Close()
	}

	return nil
}

// GetStats returns comprehensive database statistics
func (sqlm *SQLiteManager) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	// Database connection stats
	dbStats := sqlm.db.Stats()
	stats["connection_stats"] = map[string]interface{}{
		"max_open_connections": dbStats.MaxOpenConnections,
		"open_connections":     dbStats.OpenConnections,
		"in_use":              dbStats.InUse,
		"idle":                dbStats.Idle,
	}

	// Peer metadata stats
	if sqlm.PeerMetadata != nil {
		stats["peer_metadata"] = sqlm.PeerMetadata.GetStats()
	}

	// Relay stats
	if sqlm.Relay != nil {
		stats["relay"] = sqlm.Relay.GetStats()
	}

	return stats
}

// PerformMaintenance runs database maintenance tasks
func (sqlm *SQLiteManager) PerformMaintenance() error {
	logsManager := utils.NewLogsManager(sqlm.cm)
	defer logsManager.Close()

	// Cleanup old peer metadata
	if sqlm.PeerMetadata != nil {
		cleanupHours := sqlm.cm.GetConfigInt("metadata_cleanup_hours", 24, 1, 168) // 1 hour to 1 week
		maxAge := time.Duration(cleanupHours) * time.Hour

		cleaned, err := sqlm.PeerMetadata.CleanupOldMetadata(maxAge)
		if err != nil {
			logsManager.Log("error", fmt.Sprintf("Failed to cleanup old metadata: %v", err), "database")
			return err
		}

		if cleaned > 0 {
			logsManager.Info(fmt.Sprintf("Maintenance: cleaned up %d old peer metadata records", cleaned), "database")
		}
	}

	// Cleanup old relay sessions
	if sqlm.Relay != nil {
		sessionMaxAge := time.Duration(sqlm.cm.GetConfigInt("relay_session_cleanup_hours", 24, 1, 168)) * time.Hour
		cleaned, err := sqlm.Relay.CleanupOldSessions(sessionMaxAge)
		if err != nil {
			logsManager.Log("error", fmt.Sprintf("Failed to cleanup old relay sessions: %v", err), "database")
		} else if cleaned > 0 {
			logsManager.Info(fmt.Sprintf("Maintenance: cleaned up %d old relay sessions", cleaned), "database")
		}
	}

	// SQLite optimization
	_, err := sqlm.db.Exec("PRAGMA optimize;")
	if err != nil {
		logsManager.Log("warning", fmt.Sprintf("Failed to optimize database: %v", err), "database")
	}

	// Vacuum if needed (only run occasionally)
	_, err = sqlm.db.Exec("PRAGMA incremental_vacuum(100);")
	if err != nil {
		logsManager.Log("warning", fmt.Sprintf("Failed to vacuum database: %v", err), "database")
	}

	return nil
}