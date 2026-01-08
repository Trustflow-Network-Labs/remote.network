package database

import (
	"database/sql"
	"fmt"
)

// InitAppSettingsTable creates the app_settings table for storing application preferences
func (sqlm *SQLiteManager) InitAppSettingsTable() error {
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
		);
	`

	if _, err := sqlm.db.Exec(createTableSQL); err != nil {
		return fmt.Errorf("failed to create app_settings table: %v", err)
	}

	return nil
}

// GetSetting retrieves a setting value by key
func (sqlm *SQLiteManager) GetSetting(key string) (string, error) {
	var value string
	err := sqlm.db.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil // Setting doesn't exist, return empty string
	}
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %v", key, err)
	}
	return value, nil
}

// SetSetting sets a setting value (inserts or updates)
func (sqlm *SQLiteManager) SetSetting(key string, value string) error {
	_, err := sqlm.db.Exec(`
		INSERT INTO app_settings (key, value, updated_at)
		VALUES (?, ?, strftime('%s', 'now'))
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value)

	if err != nil {
		return fmt.Errorf("failed to set setting %s: %v", key, err)
	}
	return nil
}

// DeleteSetting removes a setting
func (sqlm *SQLiteManager) DeleteSetting(key string) error {
	_, err := sqlm.db.Exec("DELETE FROM app_settings WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete setting %s: %v", key, err)
	}
	return nil
}

// GetDefaultWalletID retrieves the default wallet ID
func (sqlm *SQLiteManager) GetDefaultWalletID() (string, error) {
	return sqlm.GetSetting("default_wallet_id")
}

// SetDefaultWalletID sets the default wallet ID
func (sqlm *SQLiteManager) SetDefaultWalletID(walletID string) error {
	return sqlm.SetSetting("default_wallet_id", walletID)
}
