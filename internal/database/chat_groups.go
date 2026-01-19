package database

import (
	"database/sql"
	"fmt"
	"time"
)

// ChatGroupMember represents a member of a group chat
type ChatGroupMember struct {
	ConversationID string `json:"conversation_id"`
	PeerID         string `json:"peer_id"`
	JoinedAt       int64  `json:"joined_at"`
	IsAdmin        bool   `json:"is_admin"`
	LeftAt         int64  `json:"left_at"` // 0 if still active
}

// InitChatGroupMembersTable creates the chat_group_members table
func (sqlm *SQLiteManager) InitChatGroupMembersTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_group_members (
		conversation_id TEXT NOT NULL,
		peer_id TEXT NOT NULL,
		joined_at INTEGER NOT NULL,
		is_admin INTEGER DEFAULT 0,
		left_at INTEGER DEFAULT 0,
		UNIQUE(conversation_id, peer_id),
		FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_group_members_conversation ON chat_group_members(conversation_id);
	CREATE INDEX IF NOT EXISTS idx_group_members_active ON chat_group_members(conversation_id, left_at) WHERE left_at = 0;
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_group_members table: %v", err)
	}

	sqlm.logger.Info("chat_group_members table initialized", "database")
	return nil
}

// AddGroupMember adds a member to a group
func (sqlm *SQLiteManager) AddGroupMember(member *ChatGroupMember) error {
	member.JoinedAt = time.Now().Unix()

	query := `
	INSERT INTO chat_group_members (
		conversation_id, peer_id, joined_at, is_admin, left_at
	) VALUES (?, ?, ?, ?, ?)
	`

	_, err := sqlm.db.Exec(query,
		member.ConversationID,
		member.PeerID,
		member.JoinedAt,
		boolToInt(member.IsAdmin),
		member.LeftAt,
	)

	if err != nil {
		return fmt.Errorf("failed to add group member: %v", err)
	}

	return nil
}

// GetGroupMembers retrieves all active members of a group
func (sqlm *SQLiteManager) GetGroupMembers(conversationID string) ([]*ChatGroupMember, error) {
	query := `
	SELECT conversation_id, peer_id, joined_at, is_admin, left_at
	FROM chat_group_members
	WHERE conversation_id = ? AND left_at = 0
	ORDER BY joined_at ASC
	`

	rows, err := sqlm.db.Query(query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group members: %v", err)
	}
	defer rows.Close()

	var members []*ChatGroupMember
	for rows.Next() {
		var member ChatGroupMember
		var isAdmin int

		err := rows.Scan(
			&member.ConversationID,
			&member.PeerID,
			&member.JoinedAt,
			&isAdmin,
			&member.LeftAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group member: %v", err)
		}

		member.IsAdmin = isAdmin != 0
		members = append(members, &member)
	}

	return members, nil
}

// IsGroupMember checks if a peer is an active member of a group
func (sqlm *SQLiteManager) IsGroupMember(conversationID, peerID string) (bool, error) {
	query := `
	SELECT COUNT(*) FROM chat_group_members
	WHERE conversation_id = ? AND peer_id = ? AND left_at = 0
	`

	var count int
	err := sqlm.db.QueryRow(query, conversationID, peerID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check group membership: %v", err)
	}

	return count > 0, nil
}

// RemoveGroupMember marks a member as having left the group
func (sqlm *SQLiteManager) RemoveGroupMember(conversationID, peerID string) error {
	query := `
	UPDATE chat_group_members
	SET left_at = ?
	WHERE conversation_id = ? AND peer_id = ? AND left_at = 0
	`

	_, err := sqlm.db.Exec(query, time.Now().Unix(), conversationID, peerID)
	if err != nil {
		return fmt.Errorf("failed to remove group member: %v", err)
	}

	return nil
}

// ChatSenderKey represents a sender key for group encryption
type ChatSenderKey struct {
	ConversationID  string `json:"conversation_id"`
	SenderPeerID    string `json:"sender_peer_id"`
	EncryptedChainKey []byte `json:"encrypted_chain_key"` // Encrypted chain key
	Nonce           []byte `json:"nonce"`                 // Encryption nonce
	SigningKey      []byte `json:"signing_key,omitempty"` // Only for our own sender key
	MessageNumber   int    `json:"message_number"`
	UpdatedAt       int64  `json:"updated_at"`
}

// InitChatSenderKeysTable creates the chat_sender_keys table
func (sqlm *SQLiteManager) InitChatSenderKeysTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_sender_keys (
		conversation_id TEXT NOT NULL,
		sender_peer_id TEXT NOT NULL,
		encrypted_chain_key BLOB NOT NULL,
		nonce BLOB NOT NULL,
		signing_key BLOB,
		message_number INTEGER DEFAULT 0,
		updated_at INTEGER NOT NULL,
		UNIQUE(conversation_id, sender_peer_id),
		FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_sender_keys_conversation ON chat_sender_keys(conversation_id);
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_sender_keys table: %v", err)
	}

	sqlm.logger.Info("chat_sender_keys table initialized", "database")
	return nil
}

// StoreSenderKey stores or updates a sender key for a group member
func (sqlm *SQLiteManager) StoreSenderKey(key *ChatSenderKey) error {
	key.UpdatedAt = time.Now().Unix()

	query := `
	INSERT INTO chat_sender_keys (
		conversation_id, sender_peer_id, encrypted_chain_key, nonce,
		signing_key, message_number, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(conversation_id, sender_peer_id) DO UPDATE SET
		encrypted_chain_key = excluded.encrypted_chain_key,
		nonce = excluded.nonce,
		signing_key = excluded.signing_key,
		message_number = excluded.message_number,
		updated_at = excluded.updated_at
	`

	_, err := sqlm.db.Exec(query,
		key.ConversationID,
		key.SenderPeerID,
		key.EncryptedChainKey,
		key.Nonce,
		key.SigningKey,
		key.MessageNumber,
		key.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to store sender key: %v", err)
	}

	return nil
}

// GetSenderKey retrieves a sender key for a specific peer in a group
func (sqlm *SQLiteManager) GetSenderKey(conversationID, senderPeerID string) (*ChatSenderKey, error) {
	query := `
	SELECT conversation_id, sender_peer_id, encrypted_chain_key, nonce,
	       signing_key, message_number, updated_at
	FROM chat_sender_keys
	WHERE conversation_id = ? AND sender_peer_id = ?
	`

	var key ChatSenderKey
	err := sqlm.db.QueryRow(query, conversationID, senderPeerID).Scan(
		&key.ConversationID,
		&key.SenderPeerID,
		&key.EncryptedChainKey,
		&key.Nonce,
		&key.SigningKey,
		&key.MessageNumber,
		&key.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get sender key: %v", err)
	}

	return &key, nil
}

// GetAllSenderKeys retrieves all sender keys for a group
func (sqlm *SQLiteManager) GetAllSenderKeys(conversationID string) ([]*ChatSenderKey, error) {
	query := `
	SELECT conversation_id, sender_peer_id, encrypted_chain_key, nonce,
	       signing_key, message_number, updated_at
	FROM chat_sender_keys
	WHERE conversation_id = ?
	`

	rows, err := sqlm.db.Query(query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender keys: %v", err)
	}
	defer rows.Close()

	var keys []*ChatSenderKey
	for rows.Next() {
		var key ChatSenderKey
		err := rows.Scan(
			&key.ConversationID,
			&key.SenderPeerID,
			&key.EncryptedChainKey,
			&key.Nonce,
			&key.SigningKey,
			&key.MessageNumber,
			&key.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sender key: %v", err)
		}

		keys = append(keys, &key)
	}

	return keys, nil
}

// DeleteSenderKey deletes a sender key (when member leaves group)
func (sqlm *SQLiteManager) DeleteSenderKey(conversationID, senderPeerID string) error {
	query := `DELETE FROM chat_sender_keys WHERE conversation_id = ? AND sender_peer_id = ?`

	_, err := sqlm.db.Exec(query, conversationID, senderPeerID)
	if err != nil {
		return fmt.Errorf("failed to delete sender key: %v", err)
	}

	return nil
}

// DeleteSenderKeysForGroup deletes all sender keys for a group (when leaving group)
func (sqlm *SQLiteManager) DeleteSenderKeysForGroup(conversationID string) error {
	query := `DELETE FROM chat_sender_keys WHERE conversation_id = ?`

	_, err := sqlm.db.Exec(query, conversationID)
	if err != nil {
		return fmt.Errorf("failed to delete sender keys for group: %v", err)
	}

	return nil
}

// boolToInt converts bool to int for SQLite storage
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
