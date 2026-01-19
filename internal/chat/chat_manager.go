package chat

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ChatManager manages chat conversations and messages
type ChatManager struct {
	db                  *database.SQLiteManager
	logger              *utils.LogsManager
	config              *utils.ConfigManager
	localPeerID         string
	cryptoManager       *crypto.ChatCryptoManager
	chatMessageHandler  *p2p.ChatMessageHandler
	knownPeers          *p2p.KnownPeersManager
	cleanupRunning      bool
}

// NewChatManager creates a new chat manager
func NewChatManager(
	db *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	localPeerID string,
) *ChatManager {
	return &ChatManager{
		db:          db,
		logger:      logger,
		config:      config,
		localPeerID: localPeerID,
	}
}

// SetDependencies sets the dependencies for the chat manager
// This is called after the chat handler is set up
func (cm *ChatManager) SetDependencies(
	cryptoManager *crypto.ChatCryptoManager,
	chatMessageHandler *p2p.ChatMessageHandler,
	knownPeers *p2p.KnownPeersManager,
) {
	cm.cryptoManager = cryptoManager
	cm.chatMessageHandler = chatMessageHandler
	cm.knownPeers = knownPeers
}

// CreateOrGetConversation creates or retrieves a 1-on-1 conversation with a peer
func (cm *ChatManager) CreateOrGetConversation(peerID string) (string, error) {
	// Check if conversation already exists
	conv, err := cm.db.GetConversationByPeerID(peerID)
	if err != nil {
		return "", fmt.Errorf("failed to check existing conversation: %v", err)
	}

	if conv != nil {
		return conv.ConversationID, nil
	}

	// Create new conversation
	conversationID := uuid.New().String()
	newConv := &database.ChatConversation{
		ConversationID:   conversationID,
		ConversationType: "1on1",
		PeerID:           peerID,
	}

	if err := cm.db.CreateConversation(newConv); err != nil {
		return "", fmt.Errorf("failed to create conversation: %v", err)
	}

	cm.logger.Info(fmt.Sprintf("💬 Created conversation %s with peer %s", conversationID[:8], peerID[:8]), "chat_manager")

	return conversationID, nil
}

// SendMessage sends an encrypted message in a conversation
func (cm *ChatManager) SendMessage(conversationID, plaintextContent string) (string, error) {
	// Get conversation to determine peer ID
	conv, err := cm.db.GetConversation(conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to get conversation: %v", err)
	}
	if conv == nil {
		return "", fmt.Errorf("conversation not found")
	}

	// Only support 1-on-1 for now
	if conv.ConversationType != "1on1" {
		return "", fmt.Errorf("group messages not yet implemented")
	}

	// Check if we have a ratchet state (key exchange completed)
	hasRatchet, err := cm.db.HasRatchetState(conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to check ratchet state: %v", err)
	}

	if !hasRatchet {
		// Need to initiate key exchange first
		cm.logger.Info(fmt.Sprintf("Initiating key exchange with peer %s", conv.PeerID[:8]), "chat_manager")

		// Get peer's public key
		peer, err := cm.knownPeers.GetKnownPeer(conv.PeerID, "remote-network-mesh")
		if err != nil || peer == nil {
			return "", fmt.Errorf("peer not found or unavailable")
		}

		// Initiate key exchange
		if err := cm.chatMessageHandler.InitiateKeyExchange(conversationID, conv.PeerID, peer.PublicKey); err != nil {
			return "", fmt.Errorf("failed to initiate key exchange: %v", err)
		}

		// Wait for key exchange to complete (ACK) with polling and timeout
		timeout := time.After(10 * time.Second)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				return "", fmt.Errorf("key exchange timed out after 10 seconds - peer may be offline or unreachable")
			case <-ticker.C:
				// Check if key exchange is complete (ACK received)
				isComplete, err := cm.db.IsKeyExchangeComplete(conversationID)
				if err != nil {
					cm.logger.Warn(fmt.Sprintf("Error checking key exchange status: %v", err), "chat_manager")
					continue
				}
				if isComplete {
					cm.logger.Info(fmt.Sprintf("Key exchange completed successfully with peer %s", conv.PeerID[:8]), "chat_manager")
					goto keyExchangeComplete
				}
			}
		}
	}

keyExchangeComplete:

	// Send encrypted message
	messageID, err := cm.chatMessageHandler.SendChatMessageWithRetry(conversationID, conv.PeerID, plaintextContent)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %v", err)
	}

	cm.logger.Info(fmt.Sprintf("💬 Sent message %s in conversation %s", messageID[:8], conversationID[:8]), "chat_manager")

	return messageID, nil
}

// ListConversations retrieves conversations with pagination
func (cm *ChatManager) ListConversations(limit, offset int) ([]*database.ChatConversation, error) {
	return cm.db.ListConversations(limit, offset)
}

// GetConversation retrieves a specific conversation
func (cm *ChatManager) GetConversation(conversationID string) (*database.ChatConversation, error) {
	return cm.db.GetConversation(conversationID)
}

// ListMessages retrieves messages in a conversation with pagination
func (cm *ChatManager) ListMessages(conversationID string, limit, offset int) ([]*database.ChatMessage, error) {
	messages, err := cm.db.ListMessages(conversationID, limit, offset)
	if err != nil {
		return nil, err
	}

	// Messages are already decrypted when received/sent and stored with DecryptedContent
	// No need to decrypt again - the plaintext is already in the struct
	// (Note: DecryptedContent is not stored in DB, it's set in memory when fetching)

	return messages, nil
}

// MarkMessageAsRead marks a message as read and sends read receipt
func (cm *ChatManager) MarkMessageAsRead(messageID string) error {
	// Get message to find conversation and sender
	msg, err := cm.db.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("failed to get message: %v", err)
	}
	if msg == nil {
		return fmt.Errorf("message not found")
	}

	// Mark as read in database
	if err := cm.db.MarkMessageAsRead(messageID); err != nil {
		return fmt.Errorf("failed to mark message as read: %v", err)
	}

	// Send read receipt to sender (if we're not the sender)
	if msg.SenderPeerID != cm.localPeerID {
		if err := cm.chatMessageHandler.SendReadReceipt(msg.SenderPeerID, messageID, msg.ConversationID); err != nil {
			cm.logger.Debug(fmt.Sprintf("Failed to send read receipt: %v", err), "chat_manager")
			// Don't fail the operation if receipt sending fails
		}
	}

	return nil
}

// MarkConversationAsRead marks all messages in a conversation as read
func (cm *ChatManager) MarkConversationAsRead(conversationID string) error {
	// Get all unread messages in this conversation
	messages, err := cm.db.ListMessages(conversationID, 1000, 0)
	if err != nil {
		return fmt.Errorf("failed to list messages: %v", err)
	}

	// Send read receipts for each unread message from other senders
	for _, msg := range messages {
		if msg.Status != "read" && msg.SenderPeerID != cm.localPeerID {
			// Send read receipt to sender
			if err := cm.chatMessageHandler.SendReadReceipt(msg.SenderPeerID, msg.MessageID, conversationID); err != nil {
				cm.logger.Debug(fmt.Sprintf("Failed to send read receipt for message %s: %v", msg.MessageID[:8], err), "chat_manager")
				// Don't fail the operation if receipt sending fails
			}
		}
	}

	// Update all messages to read status in database
	return cm.db.MarkConversationMessagesAsRead(conversationID)
}

// DeleteConversation deletes a conversation and all its messages
func (cm *ChatManager) DeleteConversation(conversationID string) error {
	// Delete conversation (cascade deletes messages, ratchet state, etc.)
	if err := cm.db.DeleteConversation(conversationID); err != nil {
		return fmt.Errorf("failed to delete conversation: %v", err)
	}

	cm.logger.Info(fmt.Sprintf("🗑️  Deleted conversation %s", conversationID[:8]), "chat_manager")

	return nil
}

// CreateGroup creates a new group conversation
func (cm *ChatManager) CreateGroup(groupName string, memberPeerIDs []string) (string, error) {
	// Validate member list
	if len(memberPeerIDs) < 2 {
		return "", fmt.Errorf("group must have at least 2 members")
	}

	// Create conversation
	conversationID := uuid.New().String()
	conv := &database.ChatConversation{
		ConversationID:   conversationID,
		ConversationType: "group",
		GroupName:        groupName,
	}

	if err := cm.db.CreateConversation(conv); err != nil {
		return "", fmt.Errorf("failed to create group conversation: %v", err)
	}

	// Add creator as admin
	creatorMember := &database.ChatGroupMember{
		ConversationID: conversationID,
		PeerID:         cm.localPeerID,
		IsAdmin:        true,
	}
	if err := cm.db.AddGroupMember(creatorMember); err != nil {
		return "", fmt.Errorf("failed to add creator to group: %v", err)
	}

	// Add other members
	for _, memberID := range memberPeerIDs {
		if memberID == cm.localPeerID {
			continue // Already added as admin
		}

		member := &database.ChatGroupMember{
			ConversationID: conversationID,
			PeerID:         memberID,
			IsAdmin:        false,
		}
		if err := cm.db.AddGroupMember(member); err != nil {
			cm.logger.Warn(fmt.Sprintf("Failed to add member %s to group: %v", memberID[:8], err), "chat_manager")
		}
	}

	// Initialize group sender keys
	if err := cm.cryptoManager.CreateGroupSenderKeys(conversationID, cm.localPeerID); err != nil {
		return "", fmt.Errorf("failed to create group sender keys: %v", err)
	}

	cm.logger.Info(fmt.Sprintf("👥 Created group '%s' (%s) with %d members", groupName, conversationID[:8], len(memberPeerIDs)+1), "chat_manager")

	// TODO: Send group creation messages to all members

	return conversationID, nil
}

// SendGroupMessage sends a message to a group
func (cm *ChatManager) SendGroupMessage(groupID, plaintextContent string) (string, error) {
	// TODO: Implement group message sending with Sender Keys
	return "", fmt.Errorf("group messages not yet implemented")
}

// InviteToGroup invites a peer to an existing group
func (cm *ChatManager) InviteToGroup(groupID, peerID string) error {
	// Verify group exists and is a group conversation
	conv, err := cm.db.GetConversation(groupID)
	if err != nil {
		return fmt.Errorf("failed to get group: %v", err)
	}
	if conv == nil {
		return fmt.Errorf("group not found")
	}
	if conv.ConversationType != "group" {
		return fmt.Errorf("conversation is not a group")
	}

	// Check if requester is a member (and preferably admin)
	isMember, err := cm.db.IsGroupMember(groupID, cm.localPeerID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %v", err)
	}
	if !isMember {
		return fmt.Errorf("not a member of this group")
	}

	// Check if peer is already a member
	isAlreadyMember, err := cm.db.IsGroupMember(groupID, peerID)
	if err != nil {
		return fmt.Errorf("failed to check peer membership: %v", err)
	}
	if isAlreadyMember {
		return fmt.Errorf("peer is already a member")
	}

	// Add peer to group
	member := &database.ChatGroupMember{
		ConversationID: groupID,
		PeerID:         peerID,
		IsAdmin:        false,
	}
	if err := cm.db.AddGroupMember(member); err != nil {
		return fmt.Errorf("failed to add member to group: %v", err)
	}

	// Send group invite to the peer
	inviteData := &p2p.ChatGroupInviteData{
		ConversationID: groupID,
		GroupName:      conv.GroupName,
		InviterPeerID:  cm.localPeerID,
		InviteePeerIDs: []string{peerID},
		Timestamp:      time.Now().Unix(),
	}

	if err := cm.chatMessageHandler.SendGroupInvite(peerID, inviteData); err != nil {
		cm.logger.Warn(fmt.Sprintf("Failed to send group invite to %s: %v", peerID[:8], err), "chat_manager")
		// Don't fail - member is added locally, invite may arrive later
	}

	cm.logger.Info(fmt.Sprintf("👥 Invited peer %s to group '%s' (%s)", peerID[:8], conv.GroupName, groupID[:8]), "chat_manager")

	return nil
}

// LeaveGroup removes the local peer from a group
func (cm *ChatManager) LeaveGroup(groupID string) error {
	// Verify group exists and is a group conversation
	conv, err := cm.db.GetConversation(groupID)
	if err != nil {
		return fmt.Errorf("failed to get group: %v", err)
	}
	if conv == nil {
		return fmt.Errorf("group not found")
	}
	if conv.ConversationType != "group" {
		return fmt.Errorf("conversation is not a group")
	}

	// Check if we are a member
	isMember, err := cm.db.IsGroupMember(groupID, cm.localPeerID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %v", err)
	}
	if !isMember {
		return fmt.Errorf("not a member of this group")
	}

	// Remove ourselves from the group
	if err := cm.db.RemoveGroupMember(groupID, cm.localPeerID); err != nil {
		return fmt.Errorf("failed to leave group: %v", err)
	}

	// Clean up sender keys for this group (from database)
	if err := cm.db.DeleteSenderKeysForGroup(groupID); err != nil {
		cm.logger.Warn(fmt.Sprintf("Failed to remove sender keys for group %s: %v", groupID[:8], err), "chat_manager")
	}

	cm.logger.Info(fmt.Sprintf("👋 Left group '%s' (%s)", conv.GroupName, groupID[:8]), "chat_manager")

	// TODO: Notify other group members that we left

	return nil
}

// GetGroupMembers returns all members of a group
func (cm *ChatManager) GetGroupMembers(groupID string) ([]*database.ChatGroupMember, error) {
	return cm.db.GetGroupMembers(groupID)
}

// StartCleanupRoutine starts the background cleanup task
func (cm *ChatManager) StartCleanupRoutine() {
	if cm.cleanupRunning {
		return
	}

	cm.cleanupRunning = true
	cm.logger.Info("Starting chat cleanup routine", "chat_manager")

	go func() {
		ticker := time.NewTicker(24 * time.Hour) // Run daily
		defer ticker.Stop()

		for range ticker.C {
			cm.runCleanup()
		}
	}()
}

// runCleanup performs periodic cleanup tasks
func (cm *ChatManager) runCleanup() {
	cm.logger.Debug("Running chat cleanup", "chat_manager")

	// Get TTL from config (default 30 days)
	ttlDays := cm.config.GetConfigInt("chat_message_ttl_days", 30, 1, 365)

	// Cleanup expired messages
	deleted, err := cm.db.CleanupExpiredChatMessages(ttlDays)
	if err != nil {
		cm.logger.Warn(fmt.Sprintf("Failed to cleanup expired messages: %v", err), "chat_manager")
	} else if deleted > 0 {
		cm.logger.Info(fmt.Sprintf("🧹 Cleaned up %d expired chat messages (TTL: %d days)", deleted, ttlDays), "chat_manager")
	}

	// Cleanup old skipped keys (older than 7 days)
	deletedKeys, err := cm.db.CleanupOldSkippedKeys()
	if err != nil {
		cm.logger.Warn(fmt.Sprintf("Failed to cleanup skipped keys: %v", err), "chat_manager")
	} else if deletedKeys > 0 {
		cm.logger.Info(fmt.Sprintf("🧹 Cleaned up %d old skipped keys", deletedKeys), "chat_manager")
	}
}

// InitiateKeyExchange manually initiates key exchange with a peer
func (cm *ChatManager) InitiateKeyExchange(peerID string) error {
	// Create or get conversation
	conversationID, err := cm.CreateOrGetConversation(peerID)
	if err != nil {
		return err
	}

	// Check if key exchange already done
	hasRatchet, err := cm.db.HasRatchetState(conversationID)
	if err != nil {
		return fmt.Errorf("failed to check ratchet state: %v", err)
	}

	if hasRatchet {
		return fmt.Errorf("key exchange already completed")
	}

	// Get peer's public key
	peer, err := cm.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil {
		return fmt.Errorf("peer not found or unavailable")
	}

	// Initiate key exchange
	return cm.chatMessageHandler.InitiateKeyExchange(conversationID, peerID, peer.PublicKey)
}

// GetUnreadCount returns the total number of unread messages
func (cm *ChatManager) GetUnreadCount() (int, error) {
	conversations, err := cm.db.ListConversations(1000, 0) // Get all conversations
	if err != nil {
		return 0, err
	}

	totalUnread := 0
	for _, conv := range conversations {
		totalUnread += conv.UnreadCount
	}

	return totalUnread, nil
}
