package database

import (
	"database/sql"
	"fmt"
	"time"
)

// ChatConversation represents a chat conversation (1-on-1 or group)
type ChatConversation struct {
	ConversationID  string `json:"conversation_id"`
	ConversationType string `json:"conversation_type"` // '1on1' or 'group'
	PeerID          string `json:"peer_id,omitempty"`  // For 1-on-1 chats
	GroupName       string `json:"group_name,omitempty"` // For group chats
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastMessageAt   int64  `json:"last_message_at"`
	UnreadCount     int    `json:"unread_count"`
}

// InitChatConversationsTable creates the chat_conversations table
func (sqlm *SQLiteManager) InitChatConversationsTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS chat_conversations (
		conversation_id TEXT PRIMARY KEY,
		conversation_type TEXT NOT NULL CHECK(conversation_type IN ('1on1', 'group')),
		peer_id TEXT,
		group_name TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		last_message_at INTEGER DEFAULT 0,
		unread_count INTEGER DEFAULT 0,
		CHECK (
			(conversation_type = '1on1' AND peer_id IS NOT NULL AND group_name IS NULL) OR
			(conversation_type = 'group' AND peer_id IS NULL AND group_name IS NOT NULL)
		)
	);
	CREATE INDEX IF NOT EXISTS idx_conversations_updated ON chat_conversations(updated_at DESC);
	CREATE INDEX IF NOT EXISTS idx_conversations_peer ON chat_conversations(peer_id) WHERE peer_id IS NOT NULL;
	`

	if _, err := sqlm.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create chat_conversations table: %v", err)
	}

	sqlm.logger.Info("chat_conversations table initialized", "database")
	return nil
}

// CreateConversation creates a new conversation
func (sqlm *SQLiteManager) CreateConversation(conv *ChatConversation) error {
	now := time.Now().Unix()
	conv.CreatedAt = now
	conv.UpdatedAt = now

	query := `
	INSERT INTO chat_conversations (
		conversation_id, conversation_type, peer_id, group_name,
		created_at, updated_at, last_message_at, unread_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := sqlm.db.Exec(query,
		conv.ConversationID,
		conv.ConversationType,
		nullString(conv.PeerID),
		nullString(conv.GroupName),
		conv.CreatedAt,
		conv.UpdatedAt,
		conv.LastMessageAt,
		conv.UnreadCount,
	)

	if err != nil {
		return fmt.Errorf("failed to create conversation: %v", err)
	}

	return nil
}

// GetConversation retrieves a conversation by ID
func (sqlm *SQLiteManager) GetConversation(conversationID string) (*ChatConversation, error) {
	query := `
	SELECT conversation_id, conversation_type, peer_id, group_name,
	       created_at, updated_at, last_message_at, unread_count
	FROM chat_conversations
	WHERE conversation_id = ?
	`

	var conv ChatConversation
	var peerID, groupName sql.NullString

	err := sqlm.db.QueryRow(query, conversationID).Scan(
		&conv.ConversationID,
		&conv.ConversationType,
		&peerID,
		&groupName,
		&conv.CreatedAt,
		&conv.UpdatedAt,
		&conv.LastMessageAt,
		&conv.UnreadCount,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Conversation not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %v", err)
	}

	if peerID.Valid {
		conv.PeerID = peerID.String
	}
	if groupName.Valid {
		conv.GroupName = groupName.String
	}

	return &conv, nil
}

// GetConversationByPeerID retrieves a 1-on-1 conversation with a specific peer
func (sqlm *SQLiteManager) GetConversationByPeerID(peerID string) (*ChatConversation, error) {
	query := `
	SELECT conversation_id, conversation_type, peer_id, group_name,
	       created_at, updated_at, last_message_at, unread_count
	FROM chat_conversations
	WHERE conversation_type = '1on1' AND peer_id = ?
	`

	var conv ChatConversation
	var peerIDNullable, groupName sql.NullString

	err := sqlm.db.QueryRow(query, peerID).Scan(
		&conv.ConversationID,
		&conv.ConversationType,
		&peerIDNullable,
		&groupName,
		&conv.CreatedAt,
		&conv.UpdatedAt,
		&conv.LastMessageAt,
		&conv.UnreadCount,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Conversation not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation by peer: %v", err)
	}

	if peerIDNullable.Valid {
		conv.PeerID = peerIDNullable.String
	}
	if groupName.Valid {
		conv.GroupName = groupName.String
	}

	return &conv, nil
}

// ListConversations lists all conversations with pagination, sorted by last update
func (sqlm *SQLiteManager) ListConversations(limit, offset int) ([]*ChatConversation, error) {
	query := `
	SELECT conversation_id, conversation_type, peer_id, group_name,
	       created_at, updated_at, last_message_at, unread_count
	FROM chat_conversations
	ORDER BY updated_at DESC
	LIMIT ? OFFSET ?
	`

	rows, err := sqlm.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %v", err)
	}
	defer rows.Close()

	var conversations []*ChatConversation
	for rows.Next() {
		var conv ChatConversation
		var peerID, groupName sql.NullString

		err := rows.Scan(
			&conv.ConversationID,
			&conv.ConversationType,
			&peerID,
			&groupName,
			&conv.CreatedAt,
			&conv.UpdatedAt,
			&conv.LastMessageAt,
			&conv.UnreadCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan conversation: %v", err)
		}

		if peerID.Valid {
			conv.PeerID = peerID.String
		}
		if groupName.Valid {
			conv.GroupName = groupName.String
		}

		conversations = append(conversations, &conv)
	}

	return conversations, nil
}

// UpdateConversation updates a conversation's metadata
func (sqlm *SQLiteManager) UpdateConversation(conv *ChatConversation) error {
	conv.UpdatedAt = time.Now().Unix()

	query := `
	UPDATE chat_conversations
	SET conversation_type = ?, peer_id = ?, group_name = ?,
	    updated_at = ?, last_message_at = ?, unread_count = ?
	WHERE conversation_id = ?
	`

	_, err := sqlm.db.Exec(query,
		conv.ConversationType,
		nullString(conv.PeerID),
		nullString(conv.GroupName),
		conv.UpdatedAt,
		conv.LastMessageAt,
		conv.UnreadCount,
		conv.ConversationID,
	)

	if err != nil {
		return fmt.Errorf("failed to update conversation: %v", err)
	}

	return nil
}

// IncrementUnreadCount increments the unread count for a conversation
func (sqlm *SQLiteManager) IncrementUnreadCount(conversationID string) error {
	query := `
	UPDATE chat_conversations
	SET unread_count = unread_count + 1, updated_at = ?
	WHERE conversation_id = ?
	`

	_, err := sqlm.db.Exec(query, time.Now().Unix(), conversationID)
	if err != nil {
		return fmt.Errorf("failed to increment unread count: %v", err)
	}

	return nil
}

// ResetUnreadCount resets the unread count for a conversation to 0
func (sqlm *SQLiteManager) ResetUnreadCount(conversationID string) error {
	query := `
	UPDATE chat_conversations
	SET unread_count = 0, updated_at = ?
	WHERE conversation_id = ?
	`

	_, err := sqlm.db.Exec(query, time.Now().Unix(), conversationID)
	if err != nil {
		return fmt.Errorf("failed to reset unread count: %v", err)
	}

	return nil
}

// DeleteConversation deletes a conversation and all associated messages
func (sqlm *SQLiteManager) DeleteConversation(conversationID string) error {
	// Delete conversation (messages will be cascade deleted due to foreign key)
	query := `DELETE FROM chat_conversations WHERE conversation_id = ?`

	_, err := sqlm.db.Exec(query, conversationID)
	if err != nil {
		return fmt.Errorf("failed to delete conversation: %v", err)
	}

	return nil
}

// nullString returns sql.NullString from a string
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
