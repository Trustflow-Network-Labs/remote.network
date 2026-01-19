package p2p

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ChatHandler handles incoming chat-related QUIC messages
type ChatHandler struct {
	db                 *database.SQLiteManager
	logger             *utils.LogsManager
	peerID             string
	eventEmitter       ChatEventEmitter
	cryptoManager      *crypto.ChatCryptoManager
	chatMessageHandler *ChatMessageHandler

	// Conversation locks for thread-safe ratchet operations
	conversationLocks sync.Map // conversationID -> *sync.Mutex
}

// ChatEventEmitter interface for WebSocket notifications
type ChatEventEmitter interface {
	EmitMessageReceived(message *database.ChatMessage)
	EmitMessageDelivered(messageID string)
	EmitMessageRead(messageID string)
	EmitConversationCreated(conversation *database.ChatConversation)
	EmitConversationUpdated(conversation *database.ChatConversation)
	EmitGroupInviteReceived(groupID, groupName, inviterPeerID string)
}

// NewChatHandler creates a new chat handler
func NewChatHandler(
	db *database.SQLiteManager,
	logger *utils.LogsManager,
	peerID string,
	eventEmitter ChatEventEmitter,
	cryptoManager *crypto.ChatCryptoManager,
) *ChatHandler {
	return &ChatHandler{
		db:            db,
		logger:        logger,
		peerID:        peerID,
		eventEmitter:  eventEmitter,
		cryptoManager: cryptoManager,
	}
}

// SetChatMessageHandler sets the chat message handler for sending confirmations
func (ch *ChatHandler) SetChatMessageHandler(handler *ChatMessageHandler) {
	ch.chatMessageHandler = handler
}

// getConversationLock returns a mutex for the given conversation ID
func (ch *ChatHandler) getConversationLock(conversationID string) *sync.Mutex {
	lockInterface, _ := ch.conversationLocks.LoadOrStore(conversationID, &sync.Mutex{})
	return lockInterface.(*sync.Mutex)
}

// HandleChatKeyExchange processes an incoming key exchange request
func (ch *ChatHandler) HandleChatKeyExchange(msg *QUICMessage, remotePeerID string, remotePeerPubKey ed25519.PublicKey) *QUICMessage {
	ch.logger.Debug(fmt.Sprintf("Handling key exchange from %s", remotePeerID[:8]), "chat_handler")

	// Parse key exchange data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal key exchange data: %v", err), "chat_handler")
		return nil
	}

	var keyExchangeData ChatKeyExchangeData
	if err := json.Unmarshal(dataBytes, &keyExchangeData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse key exchange: %v", err), "chat_handler")
		return nil
	}

	// Validate recipient
	if keyExchangeData.ToPeerID != ch.peerID {
		ch.logger.Warn(fmt.Sprintf("Key exchange not for us (to: %s, us: %s)",
			keyExchangeData.ToPeerID[:8], ch.peerID[:8]), "chat_handler")
		return nil
	}

	// Validate sender
	if keyExchangeData.FromPeerID != remotePeerID {
		ch.logger.Warn("Key exchange sender mismatch", "chat_handler")
		return nil
	}

	// Check if timestamp is recent (within 5 minutes) to prevent replay attacks
	messageTime := time.Unix(keyExchangeData.Timestamp, 0)
	if time.Since(messageTime) > 5*time.Minute {
		ch.logger.Warn(fmt.Sprintf("Key exchange too old: %v", time.Since(messageTime)), "chat_handler")
		return nil
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(keyExchangeData.EphemeralPubKey) != 32 {
		ch.logger.Warn(fmt.Sprintf("Invalid ephemeral public key size: %d", len(keyExchangeData.EphemeralPubKey)), "chat_handler")
		return nil
	}
	copy(ephemeralPubKey[:], keyExchangeData.EphemeralPubKey)

	// Accept key exchange and initialize ratchet
	ourEphemeralPubKey, ourSignature, err := ch.cryptoManager.AcceptKeyExchange(
		keyExchangeData.ConversationID,
		remotePeerPubKey,
		ephemeralPubKey,
		keyExchangeData.IdentitySignature,
	)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to accept key exchange: %v", err), "chat_handler")
		return nil
	}

	// Create or get conversation
	conv, err := ch.db.GetConversation(keyExchangeData.ConversationID)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to get conversation: %v", err), "chat_handler")
		return nil
	}

	if conv == nil {
		// Create new conversation
		conv = &database.ChatConversation{
			ConversationID:   keyExchangeData.ConversationID,
			ConversationType: "1on1",
			PeerID:           remotePeerID,
		}

		if err := ch.db.CreateConversation(conv); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to create conversation: %v", err), "chat_handler")
			return nil
		}

		ch.logger.Info(fmt.Sprintf("💬 Created conversation %s with peer %s",
			conv.ConversationID[:8], remotePeerID[:8]), "chat_handler")

		// Emit event
		if ch.eventEmitter != nil {
			ch.eventEmitter.EmitConversationCreated(conv)
		}
	}

	// Save ratchet state to database (encrypted) - mark as complete for receiver
	if err := ch.saveRatchetStateComplete(keyExchangeData.ConversationID, true); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_handler")
	}

	// Return ACK with our ephemeral public key
	ackData := &ChatKeyExchangeAckData{
		ConversationID:    keyExchangeData.ConversationID,
		FromPeerID:        ch.peerID,
		ToPeerID:          remotePeerID,
		EphemeralPubKey:   ourEphemeralPubKey[:],
		IdentitySignature: ourSignature,
		Timestamp:         time.Now().Unix(),
	}

	ch.logger.Info(fmt.Sprintf("✅ Key exchange completed with peer %s", remotePeerID[:8]), "chat_handler")

	return CreateChatKeyExchangeAck(ackData)
}

// HandleChatKeyExchangeAck processes an acknowledgment of our key exchange
func (ch *ChatHandler) HandleChatKeyExchangeAck(msg *QUICMessage, remotePeerID string, remotePeerPubKey ed25519.PublicKey) {
	ch.logger.Debug(fmt.Sprintf("Handling key exchange ACK from %s", remotePeerID[:8]), "chat_handler")

	// Parse ACK data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal key exchange ACK data: %v", err), "chat_handler")
		return
	}

	var ackData ChatKeyExchangeAckData
	if err := json.Unmarshal(dataBytes, &ackData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse key exchange ACK: %v", err), "chat_handler")
		return
	}

	// Validate recipient
	if ackData.ToPeerID != ch.peerID {
		ch.logger.Warn("Key exchange ACK not for us", "chat_handler")
		return
	}

	// Validate sender
	if ackData.FromPeerID != remotePeerID {
		ch.logger.Warn("Key exchange ACK sender mismatch", "chat_handler")
		return
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(ackData.EphemeralPubKey) != 32 {
		ch.logger.Warn(fmt.Sprintf("Invalid ephemeral public key size: %d", len(ackData.EphemeralPubKey)), "chat_handler")
		return
	}
	copy(ephemeralPubKey[:], ackData.EphemeralPubKey)

	// Complete the key exchange by performing DH ratchet with receiver's ephemeral key
	if err := ch.cryptoManager.CompleteKeyExchange(
		ackData.ConversationID,
		remotePeerPubKey,
		ephemeralPubKey,
		ackData.IdentitySignature,
	); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to complete key exchange: %v", err), "chat_handler")
		return
	}

	// Save updated ratchet state to database - mark as complete for initiator
	if err := ch.saveRatchetStateComplete(ackData.ConversationID, true); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_handler")
	}

	ch.logger.Info(fmt.Sprintf("✅ Key exchange completed with peer %s", remotePeerID[:8]), "chat_handler")
}

// HandleChatMessage processes an incoming encrypted chat message
func (ch *ChatHandler) HandleChatMessage(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling chat message from %s", remotePeerID[:8]), "chat_handler")

	// Parse message data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal message data: %v", err), "chat_handler")
		return
	}

	var messageData ChatMessageData
	if err := json.Unmarshal(dataBytes, &messageData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse chat message: %v", err), "chat_handler")
		return
	}

	// Validate sender
	if messageData.SenderPeerID != remotePeerID {
		ch.logger.Warn("Message sender mismatch", "chat_handler")
		return
	}

	// Lock conversation for thread-safe ratchet operations
	lock := ch.getConversationLock(messageData.ConversationID)
	lock.Lock()
	defer lock.Unlock()

	// Load ratchet state from database
	if err := ch.loadRatchetState(messageData.ConversationID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to load ratchet state: %v", err), "chat_handler")
		return
	}

	// Convert nonce and DH public key
	var nonce [24]byte
	var dhPubKey [32]byte
	if len(messageData.Nonce) != 24 {
		ch.logger.Warn(fmt.Sprintf("Invalid nonce size: %d", len(messageData.Nonce)), "chat_handler")
		return
	}
	if len(messageData.DHPubKey) != 32 {
		ch.logger.Warn(fmt.Sprintf("Invalid DH public key size: %d", len(messageData.DHPubKey)), "chat_handler")
		return
	}
	copy(nonce[:], messageData.Nonce)
	copy(dhPubKey[:], messageData.DHPubKey)

	// Decrypt message
	plaintext, err := ch.cryptoManager.Decrypt1on1Message(
		messageData.ConversationID,
		messageData.EncryptedContent,
		nonce,
		messageData.MessageNumber,
		dhPubKey,
	)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to decrypt message %s: %v", messageData.MessageID[:8], err), "chat_handler")
		// Decryption failed - out-of-order messages are handled automatically by Double Ratchet
		// using persisted skipped keys. If decryption still fails, the message is corrupted.
		return
	}

	// Save updated ratchet state
	if err := ch.saveRatchetState(messageData.ConversationID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_handler")
	}

	// Store encrypted message in database
	chatMessage := &database.ChatMessage{
		MessageID:        messageData.MessageID,
		ConversationID:   messageData.ConversationID,
		SenderPeerID:     messageData.SenderPeerID,
		EncryptedContent: messageData.EncryptedContent,
		Nonce:            messageData.Nonce,
		MessageNumber:    messageData.MessageNumber,
		Timestamp:        messageData.Timestamp,
		Status:           "delivered",
		DeliveredAt:      time.Now().Unix(),
		DecryptedContent: string(plaintext), // For display only (not stored in DB)
	}

	if err := ch.db.StoreMessage(chatMessage); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to store message: %v", err), "chat_handler")
		return
	}

	// Increment unread count
	if err := ch.db.IncrementUnreadCount(messageData.ConversationID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to increment unread count: %v", err), "chat_handler")
	}

	ch.logger.Info(fmt.Sprintf("💬 Received message %s from %s (conversation: %s)",
		messageData.MessageID[:8], remotePeerID[:8], messageData.ConversationID[:8]), "chat_handler")

	// Emit WebSocket event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitMessageReceived(chatMessage)
	}

	// Send delivery confirmation
	if ch.chatMessageHandler != nil {
		if err := ch.chatMessageHandler.SendDeliveryConfirmation(remotePeerID, messageData.MessageID, messageData.ConversationID); err != nil {
			ch.logger.Debug(fmt.Sprintf("Failed to send delivery confirmation: %v", err), "chat_handler")
			// Don't fail the operation if confirmation sending fails
		}
	}
}

// HandleChatDeliveryConfirmation processes a delivery confirmation
func (ch *ChatHandler) HandleChatDeliveryConfirmation(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling delivery confirmation from %s", remotePeerID[:8]), "chat_handler")

	// Parse confirmation data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal delivery confirmation data: %v", err), "chat_handler")
		return
	}

	var confirmData ChatDeliveryConfirmationData
	if err := json.Unmarshal(dataBytes, &confirmData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse delivery confirmation: %v", err), "chat_handler")
		return
	}

	// Update message status to delivered
	if err := ch.db.UpdateMessageStatus(confirmData.MessageID, "delivered"); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_handler")
		return
	}

	ch.logger.Debug(fmt.Sprintf("✓ Message %s delivered to %s", confirmData.MessageID[:8], remotePeerID[:8]), "chat_handler")

	// Emit WebSocket event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitMessageDelivered(confirmData.MessageID)
	}
}

// HandleChatReadReceipt processes a read receipt
func (ch *ChatHandler) HandleChatReadReceipt(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling read receipt from %s", remotePeerID[:8]), "chat_handler")

	// Parse receipt data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal read receipt data: %v", err), "chat_handler")
		return
	}

	var receiptData ChatReadReceiptData
	if err := json.Unmarshal(dataBytes, &receiptData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse read receipt: %v", err), "chat_handler")
		return
	}

	// Update message status to read
	if err := ch.db.UpdateMessageStatus(receiptData.MessageID, "read"); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_handler")
		return
	}

	ch.logger.Debug(fmt.Sprintf("✓✓ Message %s read by %s", receiptData.MessageID[:8], remotePeerID[:8]), "chat_handler")

	// Emit WebSocket event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitMessageRead(receiptData.MessageID)
	}
}

// HandleChatGroupCreate processes a group creation message
func (ch *ChatHandler) HandleChatGroupCreate(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling group create from %s", remotePeerID[:8]), "chat_handler")

	// Parse group create data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal group create data: %v", err), "chat_handler")
		return
	}

	var groupData ChatGroupCreateData
	if err := json.Unmarshal(dataBytes, &groupData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse group create: %v", err), "chat_handler")
		return
	}

	// Validate creator
	if groupData.CreatorPeerID != remotePeerID {
		ch.logger.Warn("Group creator mismatch", "chat_handler")
		return
	}

	// Check if we're in the member list
	isMember := false
	for _, memberID := range groupData.MemberPeerIDs {
		if memberID == ch.peerID {
			isMember = true
			break
		}
	}

	if !isMember {
		ch.logger.Warn("We're not in the group member list", "chat_handler")
		return
	}

	// Create group conversation
	conv := &database.ChatConversation{
		ConversationID:   groupData.ConversationID,
		ConversationType: "group",
		GroupName:        groupData.GroupName,
	}

	if err := ch.db.CreateConversation(conv); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to create group conversation: %v", err), "chat_handler")
		return
	}

	// Add all members
	for _, memberID := range groupData.MemberPeerIDs {
		member := &database.ChatGroupMember{
			ConversationID: groupData.ConversationID,
			PeerID:         memberID,
			IsAdmin:        memberID == groupData.CreatorPeerID,
		}

		if err := ch.db.AddGroupMember(member); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to add group member %s: %v", memberID[:8], err), "chat_handler")
		}
	}

	// Initialize group sender keys
	if err := ch.cryptoManager.CreateGroupSenderKeys(groupData.ConversationID, ch.peerID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to create group sender keys: %v", err), "chat_handler")
		return
	}

	ch.logger.Info(fmt.Sprintf("👥 Joined group '%s' (%s) created by %s",
		groupData.GroupName, groupData.ConversationID[:8], remotePeerID[:8]), "chat_handler")

	// Emit event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitConversationCreated(conv)
	}
}

// HandleChatGroupInvite processes a group invitation
func (ch *ChatHandler) HandleChatGroupInvite(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling group invite from %s", remotePeerID[:8]), "chat_handler")

	// Parse invite data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal group invite data: %v", err), "chat_handler")
		return
	}

	var inviteData ChatGroupInviteData
	if err := json.Unmarshal(dataBytes, &inviteData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse group invite: %v", err), "chat_handler")
		return
	}

	// Check if we're being invited
	isInvited := false
	for _, inviteeID := range inviteData.InviteePeerIDs {
		if inviteeID == ch.peerID {
			isInvited = true
			break
		}
	}

	if !isInvited {
		return
	}

	ch.logger.Info(fmt.Sprintf("📨 Received group invite to '%s' from %s",
		inviteData.GroupName, remotePeerID[:8]), "chat_handler")

	// Emit event for UI to handle acceptance
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitGroupInviteReceived(inviteData.ConversationID, inviteData.GroupName, remotePeerID)
	}
}

// saveRatchetState encrypts and saves the ratchet state to database
func (ch *ChatHandler) saveRatchetState(conversationID string) error {
	return ch.saveRatchetStateComplete(conversationID, false)
}

// saveRatchetStateComplete encrypts and saves the ratchet state to database with completion flag
func (ch *ChatHandler) saveRatchetStateComplete(conversationID string, keyExchangeComplete bool) error {
	// Get ratchet from crypto manager
	ratchet, err := ch.cryptoManager.GetRatchet(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get ratchet: %v", err)
	}

	// Encrypt ratchet state
	encryptedState, nonce, err := ch.cryptoManager.EncryptRatchetForStorage(ratchet)
	if err != nil {
		return fmt.Errorf("failed to encrypt ratchet: %v", err)
	}

	// Save main ratchet state to database
	state := &database.ChatRatchetState{
		ConversationID:      conversationID,
		EncryptedState:      encryptedState,
		Nonce:               nonce[:],
		SendMessageNum:      ratchet.SendMessageNum,
		RecvMessageNum:      ratchet.RecvMessageNum,
		RemoteDHPubKey:      ratchet.RemoteDHPubKey[:],
		KeyExchangeComplete: keyExchangeComplete,
	}

	if err := ch.db.StoreRatchetState(state); err != nil {
		return err
	}

	// Delete all old skipped keys for this conversation
	if err := ch.db.DeleteAllSkippedKeysForConversation(conversationID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to delete old skipped keys: %v", err), "chat_handler")
	}

	// Save current skipped keys to database (encrypted)
	for messageNum, messageKey := range ratchet.SkippedKeys {
		// Encrypt the message key
		encryptedKey, keyNonce, err := ch.cryptoManager.EncryptMessageKeyForStorage(messageKey)
		if err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to encrypt skipped key: %v", err), "chat_handler")
			continue
		}

		skippedKey := &database.ChatSkippedKey{
			ConversationID: conversationID,
			MessageNumber:  messageNum,
			EncryptedKey:   encryptedKey,
			Nonce:          keyNonce[:],
		}

		if err := ch.db.StoreSkippedKey(skippedKey); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to store skipped key for message %d: %v", messageNum, err), "chat_handler")
		}
	}

	return nil
}

// loadRatchetState loads and decrypts the ratchet state from database
func (ch *ChatHandler) loadRatchetState(conversationID string) error {
	// Check if already loaded in memory
	if _, err := ch.cryptoManager.GetRatchet(conversationID); err == nil {
		return nil // Already loaded
	}

	// Load main ratchet state from database
	state, err := ch.db.GetRatchetState(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get ratchet state: %v", err)
	}

	if state == nil {
		return fmt.Errorf("ratchet state not found")
	}

	// Decrypt ratchet state
	var nonce [24]byte
	copy(nonce[:], state.Nonce)

	ratchet, err := ch.cryptoManager.DecryptRatchetFromStorage(state.EncryptedState, nonce)
	if err != nil {
		return fmt.Errorf("failed to decrypt ratchet: %v", err)
	}

	// Restore counters
	ratchet.SendMessageNum = state.SendMessageNum
	ratchet.RecvMessageNum = state.RecvMessageNum

	// Load skipped keys from database
	skippedKeys, err := ch.db.GetAllSkippedKeys(conversationID)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to load skipped keys: %v", err), "chat_handler")
	} else {
		// Decrypt and restore skipped keys
		for _, sk := range skippedKeys {
			var keyNonce [24]byte
			copy(keyNonce[:], sk.Nonce)

			messageKey, err := ch.cryptoManager.DecryptMessageKeyFromStorage(sk.EncryptedKey, keyNonce)
			if err != nil {
				ch.logger.Warn(fmt.Sprintf("Failed to decrypt skipped key for message %d: %v", sk.MessageNumber, err), "chat_handler")
				continue
			}

			ratchet.SkippedKeys[sk.MessageNumber] = messageKey
		}
		ch.logger.Debug(fmt.Sprintf("Loaded %d skipped keys for conversation %s", len(skippedKeys), conversationID[:8]), "chat_handler")
	}

	// Set in crypto manager
	ch.cryptoManager.SetRatchet(conversationID, ratchet)

	return nil
}
