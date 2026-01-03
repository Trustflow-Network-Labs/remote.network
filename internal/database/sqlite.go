package database

import (
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

// SQLiteManager handles all database operations
type SQLiteManager struct {
	dir string
	cm  *utils.ConfigManager
	db  *sql.DB
	logger *utils.LogsManager

	// Specialized managers
	Relay *RelayDB
}

// NewSQLiteManager creates a new enhanced SQLite manager with peer metadata support
func NewSQLiteManager(cm *utils.ConfigManager) (*SQLiteManager, error) {
	paths := utils.GetAppPaths("")
	sqlm := &SQLiteManager{
		dir: paths.DataDir,
		cm:  cm,
		logger: utils.NewLogsManager(cm),
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

	// Initialize services table
	if err := sqlm.InitServicesTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize services table: %v", err)
	}

	// Initialize blacklist table
	if err := sqlm.InitBlacklistTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize blacklist table: %v", err)
	}

	// Initialize workflows table
	if err := sqlm.InitWorkflowsTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize workflows table: %v", err)
	}

	// Initialize workflow nodes and UI state tables
	if err := sqlm.InitWorkflowNodesTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize workflow nodes table: %v", err)
	}

	// Initialize job executions table
	if err := sqlm.InitJobExecutionsTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize job executions table: %v", err)
	}

	// Initialize data transfers table
	if err := sqlm.InitDataTransfersTable(); err != nil {
		return nil, fmt.Errorf("failed to initialize data transfers table: %v", err)
	}

	// Initialize payment invoices table
	if err := sqlm.InitPaymentInvoicesTable(); err != nil {
		return nil, fmt.Errorf("failed to init payment_invoices table: %w", err)
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

	// Initialize relay database manager
	var err error
	sqlm.Relay, err = NewRelayDB(sqlm.db, logsManager)
	if err != nil {
		return fmt.Errorf("failed to initialize relay database manager: %v", err)
	}

	logsManager.Info("Database managers initialized successfully", "database")
	return nil
}

// createOriginalTables creates the original database tables (keeping existing functionality)
func (sqlm *SQLiteManager) createOriginalTables(db *sql.DB, logsManager *utils.LogsManager) error {
	// Note: settings and blacklisted_nodes tables removed - unused in DHT-only architecture
	// All settings come from config file, no blacklist implementation exists
	logsManager.Info("Database initialization complete (unused tables removed)", "database")
	return nil
}

// GetDB returns the database connection for direct access if needed
func (sqlm *SQLiteManager) GetDB() *sql.DB {
	return sqlm.db
}

// Close closes all database connections and managers
func (sqlm *SQLiteManager) Close() error {
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