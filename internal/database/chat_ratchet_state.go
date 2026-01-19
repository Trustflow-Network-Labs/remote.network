package database

import (
	"database/sql"
	"fmt"
	"time"
)

// ChatRatchetState represents the encrypted Double Ratchet state for a conversation
type ChatRatchetState struct {
	ConversationID        string `json:"conversation_id"`
	EncryptedState        []byte `json:"encrypted_state"`         // Encrypted ratchet state
	Nonce                 []byte `json:"nonce"`                    // Nonce used for encryption
	SendMessageNum        int    `json:"send_message_num"`        // Current send counter
	RecvMessageNum        int    `json:"recv_message_num"`        // Current receive counter
	RemoteDHPubKey        []byte `json:"remote_dh_pub_key"`       // Remote DH public key (32 bytes)
	KeyExchangeComplete   bool   `json:"key_exchange_complete"`   // True when initiator received ACK or receiver accepted
	UpdatedAt             int64  `json:"updated_at"`
}

// InitChatRatchetStateTable creates the chat_ratchet_state table
func (sqlm *SQLiteManager) InitChatRatchetStateTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_ratchet_state (
		conversation_id TEXT PRIMARY KEY,
		encrypted_state BLOB NOT NULL,
		nonce BLOB NOT NULL,
		send_message_num INTEGER DEFAULT 0,
		recv_message_num INTEGER DEFAULT 0,
		remote_dh_pub_key BLOB NOT NULL,
		key_exchange_complete INTEGER DEFAULT 0,
		updated_at INTEGER NOT NULL,
		FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
	);
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_ratchet_state table: %v", err)
	}

	sqlm.logger.Info("chat_ratchet_state table initialized", "database")
	return nil
}

// StoreRatchetState stores the encrypted ratchet state for a conversation
func (sqlm *SQLiteManager) StoreRatchetState(state *ChatRatchetState) error {
	state.UpdatedAt = time.Now().Unix()

	query := `
	INSERT INTO chat_ratchet_state (
		conversation_id, encrypted_state, nonce, send_message_num,
		recv_message_num, remote_dh_pub_key, key_exchange_complete, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(conversation_id) DO UPDATE SET
		encrypted_state = excluded.encrypted_state,
		nonce = excluded.nonce,
		send_message_num = excluded.send_message_num,
		recv_message_num = excluded.recv_message_num,
		remote_dh_pub_key = excluded.remote_dh_pub_key,
		key_exchange_complete = excluded.key_exchange_complete,
		updated_at = excluded.updated_at
	`

	_, err := sqlm.db.Exec(query,
		state.ConversationID,
		state.EncryptedState,
		state.Nonce,
		state.SendMessageNum,
		state.RecvMessageNum,
		state.RemoteDHPubKey,
		state.KeyExchangeComplete,
		state.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to store ratchet state: %v", err)
	}

	return nil
}

// GetRatchetState retrieves the encrypted ratchet state for a conversation
func (sqlm *SQLiteManager) GetRatchetState(conversationID string) (*ChatRatchetState, error) {
	query := `
	SELECT conversation_id, encrypted_state, nonce, send_message_num,
	       recv_message_num, remote_dh_pub_key, key_exchange_complete, updated_at
	FROM chat_ratchet_state
	WHERE conversation_id = ?
	`

	var state ChatRatchetState
	err := sqlm.db.QueryRow(query, conversationID).Scan(
		&state.ConversationID,
		&state.EncryptedState,
		&state.Nonce,
		&state.SendMessageNum,
		&state.RecvMessageNum,
		&state.RemoteDHPubKey,
		&state.KeyExchangeComplete,
		&state.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Ratchet state not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get ratchet state: %v", err)
	}

	return &state, nil
}

// HasRatchetState checks if a ratchet state exists for a conversation
func (sqlm *SQLiteManager) HasRatchetState(conversationID string) (bool, error) {
	query := `SELECT COUNT(*) FROM chat_ratchet_state WHERE conversation_id = ?`

	var count int
	err := sqlm.db.QueryRow(query, conversationID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check ratchet state: %v", err)
	}

	return count > 0, nil
}

// IsKeyExchangeComplete checks if key exchange is complete for a conversation
func (sqlm *SQLiteManager) IsKeyExchangeComplete(conversationID string) (bool, error) {
	query := `SELECT key_exchange_complete FROM chat_ratchet_state WHERE conversation_id = ?`

	var complete bool
	err := sqlm.db.QueryRow(query, conversationID).Scan(&complete)
	if err == sql.ErrNoRows {
		return false, nil // No ratchet state = not complete
	}
	if err != nil {
		return false, fmt.Errorf("failed to check key exchange status: %v", err)
	}

	return complete, nil
}

// DeleteRatchetState deletes the ratchet state for a conversation
func (sqlm *SQLiteManager) DeleteRatchetState(conversationID string) error {
	query := `DELETE FROM chat_ratchet_state WHERE conversation_id = ?`

	_, err := sqlm.db.Exec(query, conversationID)
	if err != nil {
		return fmt.Errorf("failed to delete ratchet state: %v", err)
	}

	return nil
}

// ChatSkippedKey represents a skipped message key for out-of-order messages
type ChatSkippedKey struct {
	ConversationID string `json:"conversation_id"`
	MessageNumber  int    `json:"message_number"`
	EncryptedKey   []byte `json:"encrypted_key"` // Encrypted message key
	Nonce          []byte `json:"nonce"`          // Nonce for encryption
	CreatedAt      int64  `json:"created_at"`
}

// InitChatSkippedKeysTable creates the chat_skipped_keys table
func (sqlm *SQLiteManager) InitChatSkippedKeysTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_skipped_keys (
		conversation_id TEXT NOT NULL,
		message_number INTEGER NOT NULL,
		encrypted_key BLOB NOT NULL,
		nonce BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		UNIQUE(conversation_id, message_number),
		FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_skipped_keys_age ON chat_skipped_keys(created_at);
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_skipped_keys table: %v", err)
	}

	sqlm.logger.Info("chat_skipped_keys table initialized", "database")
	return nil
}

// StoreSkippedKey stores a skipped message key for later use
func (sqlm *SQLiteManager) StoreSkippedKey(key *ChatSkippedKey) error {
	key.CreatedAt = time.Now().Unix()

	query := `
	INSERT INTO chat_skipped_keys (
		conversation_id, message_number, encrypted_key, nonce, created_at
	) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(conversation_id, message_number) DO UPDATE SET
		encrypted_key = excluded.encrypted_key,
		nonce = excluded.nonce,
		created_at = excluded.created_at
	`

	_, err := sqlm.db.Exec(query,
		key.ConversationID,
		key.MessageNumber,
		key.EncryptedKey,
		key.Nonce,
		key.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to store skipped key: %v", err)
	}

	return nil
}

// GetSkippedKey retrieves a skipped message key
func (sqlm *SQLiteManager) GetSkippedKey(conversationID string, messageNumber int) (*ChatSkippedKey, error) {
	query := `
	SELECT conversation_id, message_number, encrypted_key, nonce, created_at
	FROM chat_skipped_keys
	WHERE conversation_id = ? AND message_number = ?
	`

	var key ChatSkippedKey
	err := sqlm.db.QueryRow(query, conversationID, messageNumber).Scan(
		&key.ConversationID,
		&key.MessageNumber,
		&key.EncryptedKey,
		&key.Nonce,
		&key.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get skipped key: %v", err)
	}

	return &key, nil
}

// GetAllSkippedKeys retrieves all skipped keys for a conversation
func (sqlm *SQLiteManager) GetAllSkippedKeys(conversationID string) ([]*ChatSkippedKey, error) {
	query := `
	SELECT conversation_id, message_number, encrypted_key, nonce, created_at
	FROM chat_skipped_keys
	WHERE conversation_id = ?
	ORDER BY message_number ASC
	`

	rows, err := sqlm.db.Query(query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to query skipped keys: %v", err)
	}
	defer rows.Close()

	var keys []*ChatSkippedKey
	for rows.Next() {
		var key ChatSkippedKey
		err := rows.Scan(
			&key.ConversationID,
			&key.MessageNumber,
			&key.EncryptedKey,
			&key.Nonce,
			&key.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan skipped key: %v", err)
		}
		keys = append(keys, &key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating skipped keys: %v", err)
	}

	return keys, nil
}

// DeleteSkippedKey deletes a skipped key after it's been used
func (sqlm *SQLiteManager) DeleteSkippedKey(conversationID string, messageNumber int) error {
	query := `DELETE FROM chat_skipped_keys WHERE conversation_id = ? AND message_number = ?`

	_, err := sqlm.db.Exec(query, conversationID, messageNumber)
	if err != nil {
		return fmt.Errorf("failed to delete skipped key: %v", err)
	}

	return nil
}

// DeleteAllSkippedKeysForConversation deletes all skipped keys for a conversation
func (sqlm *SQLiteManager) DeleteAllSkippedKeysForConversation(conversationID string) error {
	query := `DELETE FROM chat_skipped_keys WHERE conversation_id = ?`

	_, err := sqlm.db.Exec(query, conversationID)
	if err != nil {
		return fmt.Errorf("failed to delete all skipped keys: %v", err)
	}

	return nil
}

// CleanupOldSkippedKeys deletes skipped keys older than 7 days
// Returns number of keys deleted
func (sqlm *SQLiteManager) CleanupOldSkippedKeys() (int64, error) {
	cutoffTime := time.Now().AddDate(0, 0, -7).Unix() // 7 days

	query := `DELETE FROM chat_skipped_keys WHERE created_at < ?`

	result, err := sqlm.db.Exec(query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old skipped keys: %v", err)
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}
