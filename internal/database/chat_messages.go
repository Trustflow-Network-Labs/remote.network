package database

import (
	"database/sql"
	"fmt"
	"time"
)

// ChatMessage represents an encrypted chat message
type ChatMessage struct {
	MessageID        string `json:"message_id"`
	ConversationID   string `json:"conversation_id"`
	SenderPeerID     string `json:"sender_peer_id"`
	EncryptedContent []byte `json:"encrypted_content"` // Encrypted with Double Ratchet or Sender Keys
	Nonce            []byte `json:"nonce"`              // 24 bytes NaCl nonce
	MessageNumber    int    `json:"message_number"`     // Ratchet counter
	Timestamp        int64  `json:"timestamp"`
	Status           string `json:"status"` // pending, sent, delivered, read, failed
	SentAt           int64  `json:"sent_at"`
	DeliveredAt      int64  `json:"delivered_at"`
	ReadAt           int64  `json:"read_at"`

	// For decrypted display (not stored in DB)
	DecryptedContent string `json:"content,omitempty"`
}

// InitChatMessagesTable creates the chat_messages table
func (sqlm *SQLiteManager) InitChatMessagesTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_messages (
		message_id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		sender_peer_id TEXT NOT NULL,
		encrypted_content BLOB NOT NULL,
		nonce BLOB NOT NULL,
		message_number INTEGER NOT NULL,
		timestamp INTEGER NOT NULL,
		status TEXT DEFAULT 'pending' CHECK(status IN ('pending', 'sent', 'delivered', 'read', 'failed')),
		sent_at INTEGER DEFAULT 0,
		delivered_at INTEGER DEFAULT 0,
		read_at INTEGER DEFAULT 0,
		decrypted_content TEXT DEFAULT '',
		FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_messages_conversation ON chat_messages(conversation_id, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_status ON chat_messages(status, timestamp);
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_messages table: %v", err)
	}

	// Add decrypted_content column to existing tables (migration)
	alterQuery := `ALTER TABLE chat_messages ADD COLUMN decrypted_content TEXT DEFAULT ''`
	_, _ = sqlm.db.Exec(alterQuery) // Ignore error if column already exists

	sqlm.logger.Info("chat_messages table initialized", "database")
	return nil
}

// StoreMessage stores an encrypted chat message
func (sqlm *SQLiteManager) StoreMessage(msg *ChatMessage) error {
	query := `
	INSERT INTO chat_messages (
		message_id, conversation_id, sender_peer_id, encrypted_content, nonce,
		message_number, timestamp, status, sent_at, delivered_at, read_at, decrypted_content
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := sqlm.db.Exec(query,
		msg.MessageID,
		msg.ConversationID,
		msg.SenderPeerID,
		msg.EncryptedContent,
		msg.Nonce,
		msg.MessageNumber,
		msg.Timestamp,
		msg.Status,
		msg.SentAt,
		msg.DeliveredAt,
		msg.ReadAt,
		msg.DecryptedContent,
	)

	if err != nil {
		return fmt.Errorf("failed to store message: %v", err)
	}

	// Update conversation's last_message_at timestamp
	updateQuery := `
	UPDATE chat_conversations
	SET last_message_at = ?, updated_at = ?
	WHERE conversation_id = ?
	`
	_, err = sqlm.db.Exec(updateQuery, msg.Timestamp, time.Now().Unix(), msg.ConversationID)
	if err != nil {
		return fmt.Errorf("failed to update conversation timestamp: %v", err)
	}

	return nil
}

// GetMessage retrieves a message by ID
func (sqlm *SQLiteManager) GetMessage(messageID string) (*ChatMessage, error) {
	query := `
	SELECT message_id, conversation_id, sender_peer_id, encrypted_content, nonce,
	       message_number, timestamp, status, sent_at, delivered_at, read_at, decrypted_content
	FROM chat_messages
	WHERE message_id = ?
	`

	var msg ChatMessage
	err := sqlm.db.QueryRow(query, messageID).Scan(
		&msg.MessageID,
		&msg.ConversationID,
		&msg.SenderPeerID,
		&msg.EncryptedContent,
		&msg.Nonce,
		&msg.MessageNumber,
		&msg.Timestamp,
		&msg.Status,
		&msg.SentAt,
		&msg.DeliveredAt,
		&msg.ReadAt,
		&msg.DecryptedContent,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %v", err)
	}

	return &msg, nil
}

// ListMessages retrieves messages for a conversation with pagination
func (sqlm *SQLiteManager) ListMessages(conversationID string, limit, offset int) ([]*ChatMessage, error) {
	query := `
	SELECT message_id, conversation_id, sender_peer_id, encrypted_content, nonce,
	       message_number, timestamp, status, sent_at, delivered_at, read_at, decrypted_content
	FROM chat_messages
	WHERE conversation_id = ?
	ORDER BY timestamp DESC
	LIMIT ? OFFSET ?
	`

	rows, err := sqlm.db.Query(query, conversationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %v", err)
	}
	defer rows.Close()

	var messages []*ChatMessage
	for rows.Next() {
		var msg ChatMessage
		err := rows.Scan(
			&msg.MessageID,
			&msg.ConversationID,
			&msg.SenderPeerID,
			&msg.EncryptedContent,
			&msg.Nonce,
			&msg.MessageNumber,
			&msg.Timestamp,
			&msg.Status,
			&msg.SentAt,
			&msg.DeliveredAt,
			&msg.ReadAt,
			&msg.DecryptedContent,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %v", err)
		}

		messages = append(messages, &msg)
	}

	return messages, nil
}

// UpdateMessageStatus updates the status of a message
func (sqlm *SQLiteManager) UpdateMessageStatus(messageID, status string) error {
	now := time.Now().Unix()

	// Build query based on status
	var query string
	var args []interface{}

	switch status {
	case "sent":
		query = `UPDATE chat_messages SET status = ?, sent_at = ? WHERE message_id = ?`
		args = []interface{}{status, now, messageID}
	case "delivered":
		query = `UPDATE chat_messages SET status = ?, delivered_at = ? WHERE message_id = ?`
		args = []interface{}{status, now, messageID}
	case "read":
		query = `UPDATE chat_messages SET status = ?, read_at = ? WHERE message_id = ?`
		args = []interface{}{status, now, messageID}
	case "failed":
		query = `UPDATE chat_messages SET status = ? WHERE message_id = ?`
		args = []interface{}{status, messageID}
	default:
		query = `UPDATE chat_messages SET status = ? WHERE message_id = ?`
		args = []interface{}{status, messageID}
	}

	_, err := sqlm.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update message status: %v", err)
	}

	return nil
}

// MarkMessageAsRead marks a message as read and updates the read_at timestamp
func (sqlm *SQLiteManager) MarkMessageAsRead(messageID string) error {
	return sqlm.UpdateMessageStatus(messageID, "read")
}

// MarkConversationMessagesAsRead marks all messages in a conversation as read
func (sqlm *SQLiteManager) MarkConversationMessagesAsRead(conversationID string) error {
	now := time.Now().Unix()

	query := `
	UPDATE chat_messages
	SET status = 'read', read_at = ?
	WHERE conversation_id = ? AND status != 'read'
	`

	_, err := sqlm.db.Exec(query, now, conversationID)
	if err != nil {
		return fmt.Errorf("failed to mark conversation messages as read: %v", err)
	}

	// Also reset unread count
	return sqlm.ResetUnreadCount(conversationID)
}

// GetPendingMessages retrieves all messages with 'pending' status for retry
func (sqlm *SQLiteManager) GetPendingMessages() ([]*ChatMessage, error) {
	query := `
	SELECT message_id, conversation_id, sender_peer_id, encrypted_content, nonce,
	       message_number, timestamp, status, sent_at, delivered_at, read_at
	FROM chat_messages
	WHERE status = 'pending'
	ORDER BY timestamp ASC
	`

	rows, err := sqlm.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending messages: %v", err)
	}
	defer rows.Close()

	var messages []*ChatMessage
	for rows.Next() {
		var msg ChatMessage
		err := rows.Scan(
			&msg.MessageID,
			&msg.ConversationID,
			&msg.SenderPeerID,
			&msg.EncryptedContent,
			&msg.Nonce,
			&msg.MessageNumber,
			&msg.Timestamp,
			&msg.Status,
			&msg.SentAt,
			&msg.DeliveredAt,
			&msg.ReadAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending message: %v", err)
		}

		messages = append(messages, &msg)
	}

	return messages, nil
}

// DeleteMessage deletes a message by ID
func (sqlm *SQLiteManager) DeleteMessage(messageID string) error {
	query := `DELETE FROM chat_messages WHERE message_id = ?`

	_, err := sqlm.db.Exec(query, messageID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %v", err)
	}

	return nil
}

// DeleteConversationMessages deletes all messages in a conversation
func (sqlm *SQLiteManager) DeleteConversationMessages(conversationID string) error {
	query := `DELETE FROM chat_messages WHERE conversation_id = ?`

	result, err := sqlm.db.Exec(query, conversationID)
	if err != nil {
		return fmt.Errorf("failed to delete conversation messages: %v", err)
	}

	affected, _ := result.RowsAffected()
	sqlm.logger.Debug(fmt.Sprintf("Deleted %d messages from conversation %s", affected, conversationID), "database")

	return nil
}

// CleanupExpiredChatMessages deletes messages older than the specified TTL
// Returns number of messages deleted
func (sqlm *SQLiteManager) CleanupExpiredChatMessages(ttlDays int) (int64, error) {
	cutoffTime := time.Now().AddDate(0, 0, -ttlDays).Unix()

	query := `DELETE FROM chat_messages WHERE timestamp < ?`

	result, err := sqlm.db.Exec(query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired messages: %v", err)
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}
