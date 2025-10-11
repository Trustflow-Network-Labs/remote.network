package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// MigrationManager handles database schema migrations
type MigrationManager struct {
	db     *sql.DB
	logger *utils.LogsManager
}

// NewMigrationManager creates a new migration manager
func NewMigrationManager(db *sql.DB, logger *utils.LogsManager) *MigrationManager {
	return &MigrationManager{
		db:     db,
		logger: logger,
	}
}

// MigrateToDHTMetadataStorage migrates from old schema to new DHT-based schema
// This is a Phase 2 migration for the new architecture
func (mm *MigrationManager) MigrateToDHTMetadataStorage() error {
	mm.logger.Info("Starting migration to DHT metadata storage schema", "database")

	// Check if migration is needed
	needsMigration, err := mm.needsMigration()
	if err != nil {
		return fmt.Errorf("failed to check migration status: %v", err)
	}

	if !needsMigration {
		mm.logger.Info("Migration not needed - new schema already exists", "database")
		return nil
	}

	// Start transaction
	tx, err := mm.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Step 1: Backup old peer_metadata table
	mm.logger.Info("Step 1/5: Backing up peer_metadata table", "database")
	if err := mm.backupPeerMetadata(tx); err != nil {
		return fmt.Errorf("failed to backup peer_metadata: %v", err)
	}

	// Step 2: Create new tables (known_peers and metadata_cache)
	mm.logger.Info("Step 2/5: Creating new tables (known_peers, metadata_cache)", "database")
	if err := mm.createNewTables(tx); err != nil {
		return fmt.Errorf("failed to create new tables: %v", err)
	}

	// Step 3: Migrate essential data to known_peers
	mm.logger.Info("Step 3/5: Migrating essential data to known_peers", "database")
	if err := mm.migrateToKnownPeers(tx); err != nil {
		return fmt.Errorf("failed to migrate to known_peers: %v", err)
	}

	// Step 4: Create migration log
	mm.logger.Info("Step 4/5: Creating migration log", "database")
	if err := mm.createMigrationLog(tx); err != nil {
		return fmt.Errorf("failed to create migration log: %v", err)
	}

	// Step 5: Commit transaction
	mm.logger.Info("Step 5/5: Committing migration transaction", "database")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration: %v", err)
	}

	mm.logger.Info("Successfully completed migration to DHT metadata storage schema", "database")
	return nil
}

// needsMigration checks if migration is required
func (mm *MigrationManager) needsMigration() (bool, error) {
	// Check if known_peers table exists
	var tableName string
	err := mm.db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='known_peers'
	`).Scan(&tableName)

	if err == sql.ErrNoRows {
		// known_peers doesn't exist, migration needed
		return true, nil
	}

	if err != nil {
		return false, err
	}

	// known_peers exists, no migration needed
	return false, nil
}

// backupPeerMetadata creates a backup of the peer_metadata table
func (mm *MigrationManager) backupPeerMetadata(tx *sql.Tx) error {
	// Check if peer_metadata exists
	var exists bool
	err := tx.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='peer_metadata'
	`).Scan(&exists)

	if err != nil {
		return err
	}

	if !exists {
		mm.logger.Debug("peer_metadata table doesn't exist, skipping backup", "database")
		return nil
	}

	// Create backup table
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS peer_metadata_backup AS
		SELECT * FROM peer_metadata
	`)
	if err != nil {
		return fmt.Errorf("failed to create backup: %v", err)
	}

	// Count backed up rows
	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM peer_metadata_backup").Scan(&count)
	if err == nil {
		mm.logger.Info(fmt.Sprintf("Backed up %d peer_metadata rows", count), "database")
	}

	return nil
}

// createNewTables creates the new known_peers and metadata_cache tables
func (mm *MigrationManager) createNewTables(tx *sql.Tx) error {
	// Create known_peers table
	_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS known_peers (
	"peer_id" TEXT NOT NULL,
	"public_key" BLOB NOT NULL,
	"last_seen" INTEGER NOT NULL,
	"topic" TEXT NOT NULL,
	"first_seen" INTEGER NOT NULL,
	"source" TEXT NOT NULL DEFAULT 'migration',
	PRIMARY KEY(peer_id, topic)
);

CREATE INDEX IF NOT EXISTS idx_known_peers_last_seen ON known_peers(last_seen);
CREATE INDEX IF NOT EXISTS idx_known_peers_topic ON known_peers(topic);
CREATE INDEX IF NOT EXISTS idx_known_peers_source ON known_peers(source);
CREATE INDEX IF NOT EXISTS idx_known_peers_peer_id ON known_peers(peer_id);
`)
	if err != nil {
		return fmt.Errorf("failed to create known_peers: %v", err)
	}

	// Create metadata_cache table
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS metadata_cache (
	"peer_id" TEXT PRIMARY KEY,
	"metadata_json" TEXT NOT NULL,
	"cached_at" INTEGER NOT NULL,
	"ttl_seconds" INTEGER NOT NULL DEFAULT 300
);

CREATE INDEX IF NOT EXISTS idx_metadata_cache_cached_at ON metadata_cache(cached_at);
`)
	if err != nil {
		return fmt.Errorf("failed to create metadata_cache: %v", err)
	}

	mm.logger.Debug("Created new tables successfully", "database")
	return nil
}

// migrateToKnownPeers migrates essential data from peer_metadata to known_peers
func (mm *MigrationManager) migrateToKnownPeers(tx *sql.Tx) error {
	// Check if peer_metadata exists
	var exists bool
	err := tx.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='peer_metadata'
	`).Scan(&exists)

	if err != nil {
		return err
	}

	if !exists {
		mm.logger.Debug("peer_metadata table doesn't exist, skipping data migration", "database")
		return nil
	}

	// Migrate data: node_id -> peer_id, empty public_key (will be filled during handshake)
	// Note: We can't derive public keys from node_id in old schema, they'll be exchanged later
	_, err = tx.Exec(`
		INSERT INTO known_peers (peer_id, public_key, last_seen, topic, first_seen, source)
		SELECT
			node_id AS peer_id,
			X'' AS public_key,  -- Empty, will be filled during QUIC handshake
			CAST(strftime('%s', last_seen) AS INTEGER) AS last_seen,
			topic,
			CAST(strftime('%s', last_seen) AS INTEGER) AS first_seen,  -- Use last_seen as first_seen
			'migration' AS source
		FROM peer_metadata
		ON CONFLICT(peer_id, topic) DO NOTHING
	`)

	if err != nil {
		return fmt.Errorf("failed to migrate data: %v", err)
	}

	// Count migrated rows
	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM known_peers WHERE source = 'migration'").Scan(&count)
	if err == nil {
		mm.logger.Info(fmt.Sprintf("Migrated %d peers to known_peers", count), "database")
	}

	return nil
}

// createMigrationLog creates a log entry for this migration
func (mm *MigrationManager) createMigrationLog(tx *sql.Tx) error {
	// Create migrations table if it doesn't exist
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			applied_at INTEGER NOT NULL,
			description TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %v", err)
	}

	// Insert migration log
	now := time.Now().Unix()
	_, err = tx.Exec(`
		INSERT INTO migrations (name, applied_at, description)
		VALUES (?, ?, ?)
	`, "dht_metadata_storage", now, "Migrated to DHT-based metadata storage with known_peers and metadata_cache")

	if err != nil {
		return fmt.Errorf("failed to insert migration log: %v", err)
	}

	mm.logger.Debug("Created migration log entry", "database")
	return nil
}

// RollbackMigration rolls back the migration (for emergency use)
func (mm *MigrationManager) RollbackMigration() error {
	mm.logger.Warn("Rolling back DHT metadata storage migration", "database")

	tx, err := mm.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin rollback transaction: %v", err)
	}
	defer tx.Rollback()

	// Drop new tables
	_, err = tx.Exec("DROP TABLE IF EXISTS known_peers")
	if err != nil {
		return fmt.Errorf("failed to drop known_peers: %v", err)
	}

	_, err = tx.Exec("DROP TABLE IF EXISTS metadata_cache")
	if err != nil {
		return fmt.Errorf("failed to drop metadata_cache: %v", err)
	}

	// Restore from backup if it exists
	var backupExists bool
	err = tx.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='peer_metadata_backup'
	`).Scan(&backupExists)

	if err != nil {
		return err
	}

	if backupExists {
		// Drop current peer_metadata (if exists)
		_, err = tx.Exec("DROP TABLE IF EXISTS peer_metadata")
		if err != nil {
			return fmt.Errorf("failed to drop peer_metadata: %v", err)
		}

		// Rename backup to peer_metadata
		_, err = tx.Exec("ALTER TABLE peer_metadata_backup RENAME TO peer_metadata")
		if err != nil {
			return fmt.Errorf("failed to restore backup: %v", err)
		}

		mm.logger.Info("Restored peer_metadata from backup", "database")
	}

	// Remove migration log
	_, err = tx.Exec("DELETE FROM migrations WHERE name = 'dht_metadata_storage'")
	if err != nil {
		mm.logger.Warn(fmt.Sprintf("Failed to remove migration log: %v", err), "database")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollback: %v", err)
	}

	mm.logger.Info("Successfully rolled back migration", "database")
	return nil
}

// GetMigrationStatus returns the status of migrations
func (mm *MigrationManager) GetMigrationStatus() (map[string]interface{}, error) {
	status := make(map[string]interface{})

	// Check if migrations table exists
	var migrationsExists bool
	err := mm.db.QueryRow(`
		SELECT COUNT(*) > 0 FROM sqlite_master
		WHERE type='table' AND name='migrations'
	`).Scan(&migrationsExists)

	if err != nil {
		return nil, err
	}

	status["migrations_table_exists"] = migrationsExists

	if !migrationsExists {
		status["applied_migrations"] = []string{}
		return status, nil
	}

	// Get applied migrations
	rows, err := mm.db.Query("SELECT name, applied_at, description FROM migrations ORDER BY applied_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var migrations []map[string]interface{}
	for rows.Next() {
		var name, description string
		var appliedAt int64

		err := rows.Scan(&name, &appliedAt, &description)
		if err != nil {
			continue
		}

		migrations = append(migrations, map[string]interface{}{
			"name":        name,
			"applied_at":  time.Unix(appliedAt, 0).Format(time.RFC3339),
			"description": description,
		})
	}

	status["applied_migrations"] = migrations
	status["migration_count"] = len(migrations)

	// Check if DHT migration is applied
	var dhtMigrationApplied bool
	err = mm.db.QueryRow("SELECT COUNT(*) > 0 FROM migrations WHERE name = 'dht_metadata_storage'").Scan(&dhtMigrationApplied)
	if err != nil {
		dhtMigrationApplied = false
	}
	status["dht_metadata_storage_applied"] = dhtMigrationApplied

	return status, nil
}
