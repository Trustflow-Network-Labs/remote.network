package database

import (
	"database/sql"
	"time"
)

// BlacklistedPeer represents a peer that has been blacklisted
type BlacklistedPeer struct {
	PeerID      string    `json:"peer_id"`
	Reason      string    `json:"reason"`
	BlacklistedAt time.Time `json:"blacklisted_at"`
}

// InitBlacklistTable creates the blacklist table if it doesn't exist
func (sm *SQLiteManager) InitBlacklistTable() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS blacklist (
		peer_id TEXT PRIMARY KEY,
		reason TEXT,
		blacklisted_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_blacklist_blacklisted_at ON blacklist(blacklisted_at);
	`

	_, err := sm.db.Exec(createTableSQL)
	if err != nil {
		sm.logger.Error("Failed to create blacklist table", "database")
		return err
	}

	sm.logger.Info("Blacklist table initialized", "database")
	return nil
}

// AddToBlacklist adds a peer to the blacklist
func (sm *SQLiteManager) AddToBlacklist(peerID string, reason string) error {
	query := `
		INSERT OR REPLACE INTO blacklist (peer_id, reason, blacklisted_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`

	_, err := sm.db.Exec(query, peerID, reason)
	if err != nil {
		sm.logger.Error("Failed to add peer to blacklist", "database")
		return err
	}

	sm.logger.Info("Peer added to blacklist", "database")
	return nil
}

// RemoveFromBlacklist removes a peer from the blacklist
func (sm *SQLiteManager) RemoveFromBlacklist(peerID string) error {
	query := `DELETE FROM blacklist WHERE peer_id = ?`

	result, err := sm.db.Exec(query, peerID)
	if err != nil {
		sm.logger.Error("Failed to remove peer from blacklist", "database")
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	sm.logger.Info("Peer removed from blacklist", "database")
	return nil
}

// IsBlacklisted checks if a peer is blacklisted
func (sm *SQLiteManager) IsBlacklisted(peerID string) (bool, error) {
	query := `SELECT COUNT(*) FROM blacklist WHERE peer_id = ?`

	var count int
	err := sm.db.QueryRow(query, peerID).Scan(&count)
	if err != nil {
		sm.logger.Error("Failed to check blacklist status", "database")
		return false, err
	}

	return count > 0, nil
}

// GetBlacklist retrieves all blacklisted peers
func (sm *SQLiteManager) GetBlacklist() ([]*BlacklistedPeer, error) {
	query := `
		SELECT peer_id, reason, blacklisted_at
		FROM blacklist
		ORDER BY blacklisted_at DESC
	`

	rows, err := sm.db.Query(query)
	if err != nil {
		sm.logger.Error("Failed to get blacklist", "database")
		return nil, err
	}
	defer rows.Close()

	var blacklist []*BlacklistedPeer

	for rows.Next() {
		var peer BlacklistedPeer

		err := rows.Scan(
			&peer.PeerID,
			&peer.Reason,
			&peer.BlacklistedAt,
		)

		if err != nil {
			sm.logger.Error("Failed to scan blacklisted peer", "database")
			continue
		}

		blacklist = append(blacklist, &peer)
	}

	return blacklist, nil
}

// GetBlacklistedPeerIDs returns just the peer IDs (useful for quick lookups)
func (sm *SQLiteManager) GetBlacklistedPeerIDs() ([]string, error) {
	query := `SELECT peer_id FROM blacklist`

	rows, err := sm.db.Query(query)
	if err != nil {
		sm.logger.Error("Failed to get blacklisted peer IDs", "database")
		return nil, err
	}
	defer rows.Close()

	var peerIDs []string

	for rows.Next() {
		var peerID string
		err := rows.Scan(&peerID)
		if err != nil {
			sm.logger.Error("Failed to scan peer ID", "database")
			continue
		}
		peerIDs = append(peerIDs, peerID)
	}

	return peerIDs, nil
}
