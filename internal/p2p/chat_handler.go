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

// PendingSenderKey stores a sender key that arrived before group creation
type PendingSenderKey struct {
	SenderPeerID  string
	ChainKey      [32]byte
	MessageNumber int
	Timestamp     int64
}

// PendingGroupMessage stores a group message that arrived before group creation
type PendingGroupMessage struct {
	Msg          *QUICMessage
	RemotePeerID string
	ReceivedAt   time.Time
}

// ChatHandler handles incoming chat-related QUIC messages
type ChatHandler struct {
	db                 *database.SQLiteManager
	logger             *utils.LogsManager
	peerID             string
	eventEmitter       ChatEventEmitter
	cryptoManager      *crypto.ChatCryptoManager
	chatMessageHandler *ChatMessageHandler
	knownPeers         *KnownPeersManager

	// Conversation locks for thread-safe ratchet operations
	conversationLocks sync.Map // conversationID -> *sync.Mutex

	// Pending sender keys for groups we haven't joined yet
	pendingSenderKeys   map[string][]*PendingSenderKey // groupID -> list of pending sender keys
	pendingSenderKeysMu sync.Mutex

	// Pending group messages for groups we haven't joined yet
	pendingGroupMessages   map[string][]*PendingGroupMessage // groupID -> list of pending messages
	pendingGroupMessagesMu sync.Mutex
}

// ChatEventEmitter interface for WebSocket notifications
type ChatEventEmitter interface {
	EmitMessageCreated(message *database.ChatMessage)
	EmitMessageReceived(message *database.ChatMessage)
	EmitMessageDelivered(messageID, conversationID string)
	EmitMessageRead(messageID, conversationID string)
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
		db:                   db,
		logger:               logger,
		peerID:               peerID,
		eventEmitter:         eventEmitter,
		cryptoManager:        cryptoManager,
		pendingSenderKeys:    make(map[string][]*PendingSenderKey),
		pendingGroupMessages: make(map[string][]*PendingGroupMessage),
	}
}

// SetChatMessageHandler sets the chat message handler for sending confirmations
func (ch *ChatHandler) SetChatMessageHandler(handler *ChatMessageHandler) {
	ch.chatMessageHandler = handler
}

// SetKnownPeers sets the known peers manager for public key lookup
func (ch *ChatHandler) SetKnownPeers(knownPeers *KnownPeersManager) {
	ch.knownPeers = knownPeers
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
		ch.eventEmitter.EmitMessageDelivered(confirmData.MessageID, confirmData.ConversationID)
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
		ch.eventEmitter.EmitMessageRead(receiptData.MessageID, receiptData.ConversationID)
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

	// Save our sender key to database for persistence
	chainKey, messageNum, err := ch.cryptoManager.GetOurSenderKeyState(groupData.ConversationID)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to get sender key state: %v", err), "chat_handler")
		return
	}
	encryptedKey, nonce, err := ch.cryptoManager.EncryptSenderKeyForStorage(chainKey)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to encrypt sender key: %v", err), "chat_handler")
		return
	}
	senderKeyRecord := &database.ChatSenderKey{
		ConversationID:    groupData.ConversationID,
		SenderPeerID:      ch.peerID,
		EncryptedChainKey: encryptedKey,
		Nonce:             nonce[:],
		MessageNumber:     messageNum,
	}
	if err := ch.db.StoreSenderKey(senderKeyRecord); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to save sender key to DB: %v", err), "chat_handler")
	}

	ch.logger.Info(fmt.Sprintf("👥 Joined group '%s' (%s) created by %s",
		groupData.GroupName, groupData.ConversationID[:8], remotePeerID[:8]), "chat_handler")

	// Distribute our sender key to all other members (reuse chainKey and messageNum from above)
	if ch.chatMessageHandler != nil {
		senderKeyData := &ChatSenderKeyDistributionData{
			ConversationID: groupData.ConversationID,
			SenderPeerID:   ch.peerID,
			ChainKey:       chainKey[:],
			MessageNumber:  messageNum,
			Timestamp:      time.Now().Unix(),
		}

		for _, memberID := range groupData.MemberPeerIDs {
			if memberID == ch.peerID {
				continue // Don't send to ourselves
			}

			if err := ch.chatMessageHandler.SendSenderKeyDistribution(memberID, senderKeyData); err != nil {
				ch.logger.Debug(fmt.Sprintf("Failed to send sender key to member %s: %v", memberID[:8], err), "chat_handler")
			} else {
				ch.logger.Debug(fmt.Sprintf("Sent sender key to member %s", memberID[:8]), "chat_handler")
			}
		}
	}

	// Process any pending sender keys that arrived before the group_create
	ch.processPendingSenderKeys(groupData.ConversationID)

	// Process any pending group messages that arrived before the group_create
	ch.processPendingGroupMessages(groupData.ConversationID)

	// Emit event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitConversationCreated(conv)
	}
}

// processPendingSenderKeys processes any sender keys that were queued before group creation
func (ch *ChatHandler) processPendingSenderKeys(groupID string) {
	ch.pendingSenderKeysMu.Lock()
	pendingKeys, exists := ch.pendingSenderKeys[groupID]
	if exists {
		delete(ch.pendingSenderKeys, groupID)
	}
	ch.pendingSenderKeysMu.Unlock()

	if !exists || len(pendingKeys) == 0 {
		return
	}

	ch.logger.Debug(fmt.Sprintf("Processing %d pending sender keys for group %s", len(pendingKeys), groupID[:8]), "chat_handler")

	for _, pending := range pendingKeys {
		// Add sender key to crypto manager
		if err := ch.cryptoManager.AddGroupSenderKey(groupID, pending.SenderPeerID, pending.ChainKey, pending.MessageNumber); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to add pending sender key from %s: %v", pending.SenderPeerID[:8], err), "chat_handler")
			continue
		}

		// Save to database
		encryptedKey, nonce, err := ch.cryptoManager.EncryptSenderKeyForStorage(pending.ChainKey)
		if err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to encrypt pending sender key: %v", err), "chat_handler")
			continue
		}
		senderKeyRecord := &database.ChatSenderKey{
			ConversationID:    groupID,
			SenderPeerID:      pending.SenderPeerID,
			EncryptedChainKey: encryptedKey,
			Nonce:             nonce[:],
			MessageNumber:     pending.MessageNumber,
		}
		if err := ch.db.StoreSenderKey(senderKeyRecord); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to save pending sender key: %v", err), "chat_handler")
			continue
		}

		ch.logger.Info(fmt.Sprintf("🔑 Processed pending sender key from %s for group %s (msgNum: %d)",
			pending.SenderPeerID[:8], groupID[:8], pending.MessageNumber), "chat_handler")
	}
}

// processPendingGroupMessages processes any group messages that were queued before group creation
func (ch *ChatHandler) processPendingGroupMessages(groupID string) {
	ch.pendingGroupMessagesMu.Lock()
	pendingMessages, exists := ch.pendingGroupMessages[groupID]
	if exists {
		delete(ch.pendingGroupMessages, groupID)
	}
	ch.pendingGroupMessagesMu.Unlock()

	if !exists || len(pendingMessages) == 0 {
		return
	}

	ch.logger.Debug(fmt.Sprintf("Processing %d pending group messages for group %s", len(pendingMessages), groupID[:8]), "chat_handler")

	for _, pending := range pendingMessages {
		// Process the message by calling HandleChatGroupMessage recursively
		// The message will now pass membership check since we joined the group
		ch.HandleChatGroupMessage(pending.Msg, pending.RemotePeerID)
	}
}

// HandleChatGroupMessage processes an incoming encrypted group message
func (ch *ChatHandler) HandleChatGroupMessage(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling group message from %s", remotePeerID[:8]), "chat_handler")

	// Parse message data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal group message data: %v", err), "chat_handler")
		return
	}

	var messageData ChatGroupMessageData
	if err := json.Unmarshal(dataBytes, &messageData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse group message: %v", err), "chat_handler")
		return
	}

	// Validate sender
	if messageData.SenderPeerID != remotePeerID {
		ch.logger.Warn("Group message sender mismatch", "chat_handler")
		return
	}

	// Verify we are a member of this group
	isMember, err := ch.db.IsGroupMember(messageData.ConversationID, ch.peerID)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to check group membership: %v", err), "chat_handler")
		return
	}
	if !isMember {
		// Queue the group message for later processing when group_create arrives
		ch.pendingGroupMessagesMu.Lock()
		ch.pendingGroupMessages[messageData.ConversationID] = append(ch.pendingGroupMessages[messageData.ConversationID], &PendingGroupMessage{
			Msg:          msg,
			RemotePeerID: remotePeerID,
			ReceivedAt:   time.Now(),
		})
		ch.pendingGroupMessagesMu.Unlock()
		ch.logger.Debug(fmt.Sprintf("Queued group message from %s for group %s (not yet a member)",
			remotePeerID[:8], messageData.ConversationID[:8]), "chat_handler")
		return
	}

	// Ensure sender keys are loaded into memory
	if !ch.cryptoManager.HasGroupSenderKeys(messageData.ConversationID) {
		// Load sender keys from database
		allKeys, err := ch.db.GetAllSenderKeys(messageData.ConversationID)
		if err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to load sender keys from DB: %v", err), "chat_handler")
			return
		}
		for _, key := range allKeys {
			var keyNonce [24]byte
			copy(keyNonce[:], key.Nonce)
			chainKey, err := ch.cryptoManager.DecryptSenderKeyFromStorage(key.EncryptedChainKey, keyNonce)
			if err != nil {
				ch.logger.Warn(fmt.Sprintf("Failed to decrypt sender key for %s: %v", key.SenderPeerID[:8], err), "chat_handler")
				continue
			}
			isOurKey := key.SenderPeerID == ch.peerID
			if err := ch.cryptoManager.LoadGroupSenderKey(messageData.ConversationID, key.SenderPeerID, chainKey, key.MessageNumber, isOurKey, nil); err != nil {
				ch.logger.Warn(fmt.Sprintf("Failed to load sender key for %s: %v", key.SenderPeerID[:8], err), "chat_handler")
			}
		}
	}

	// Get sender's public key for signature verification
	// Note: We need to get it from known peers since the sender is a remote peer
	peer, peerErr := ch.getSenderPublicKey(remotePeerID)
	if peerErr != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to get sender public key: %v", peerErr), "chat_handler")
		return
	}

	// Convert nonce
	var nonce [24]byte
	if len(messageData.Nonce) != 24 {
		ch.logger.Warn(fmt.Sprintf("Invalid nonce size: %d", len(messageData.Nonce)), "chat_handler")
		return
	}
	copy(nonce[:], messageData.Nonce)

	// Decrypt message using Sender Keys
	plaintext, err := ch.cryptoManager.DecryptGroupMessage(
		messageData.ConversationID,
		messageData.SenderPeerID,
		messageData.EncryptedContent,
		nonce,
		messageData.MessageNumber,
		messageData.Signature,
		peer,
	)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to decrypt group message %s: %v", messageData.MessageID[:8], err), "chat_handler")
		return
	}

	// Persist updated sender key state to database after decryption
	// The decryption advances the chain key and increments message_number
	if ch.chatMessageHandler != nil {
		if err := ch.chatMessageHandler.PersistSenderKeyState(messageData.ConversationID, messageData.SenderPeerID); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to persist sender key state: %v", err), "chat_handler")
		}
	}

	// Store message in database
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
		DecryptedContent: string(plaintext),
	}

	if err := ch.db.StoreMessage(chatMessage); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to store group message: %v", err), "chat_handler")
		return
	}

	// Increment unread count
	if err := ch.db.IncrementUnreadCount(messageData.ConversationID); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to increment unread count: %v", err), "chat_handler")
	}

	ch.logger.Info(fmt.Sprintf("👥 Received group message %s from %s (group: %s)",
		messageData.MessageID[:8], remotePeerID[:8], messageData.ConversationID[:8]), "chat_handler")

	// Emit WebSocket event
	if ch.eventEmitter != nil {
		ch.eventEmitter.EmitMessageReceived(chatMessage)
	}

	// Send delivery confirmation
	if ch.chatMessageHandler != nil {
		if err := ch.chatMessageHandler.SendDeliveryConfirmation(remotePeerID, messageData.MessageID, messageData.ConversationID); err != nil {
			ch.logger.Debug(fmt.Sprintf("Failed to send delivery confirmation: %v", err), "chat_handler")
		}
	}
}

// getSenderPublicKey retrieves the Ed25519 public key for a peer
func (ch *ChatHandler) getSenderPublicKey(peerID string) (ed25519.PublicKey, error) {
	if ch.knownPeers == nil {
		return nil, fmt.Errorf("known peers manager not initialized")
	}

	peer, err := ch.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil {
		return nil, fmt.Errorf("failed to get known peer: %v", err)
	}
	if peer == nil {
		return nil, fmt.Errorf("peer %s not found in known peers", peerID[:8])
	}
	if len(peer.PublicKey) == 0 {
		return nil, fmt.Errorf("peer %s has no public key", peerID[:8])
	}

	return ed25519.PublicKey(peer.PublicKey), nil
}

// HandleChatSenderKeyDistribution processes a sender key distribution message
func (ch *ChatHandler) HandleChatSenderKeyDistribution(msg *QUICMessage, remotePeerID string) {
	ch.logger.Debug(fmt.Sprintf("Handling sender key distribution from %s", remotePeerID[:8]), "chat_handler")

	// Parse distribution data
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to marshal sender key distribution data: %v", err), "chat_handler")
		return
	}

	var distData ChatSenderKeyDistributionData
	if err := json.Unmarshal(dataBytes, &distData); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to parse sender key distribution: %v", err), "chat_handler")
		return
	}

	// Validate sender
	if distData.SenderPeerID != remotePeerID {
		ch.logger.Warn("Sender key distribution sender mismatch", "chat_handler")
		return
	}

	// Validate chain key length
	if len(distData.ChainKey) != 32 {
		ch.logger.Warn(fmt.Sprintf("Invalid chain key length: %d", len(distData.ChainKey)), "chat_handler")
		return
	}

	// Convert chain key to array
	var chainKey [32]byte
	copy(chainKey[:], distData.ChainKey)

	// Verify we are a member of this group
	isMember, err := ch.db.IsGroupMember(distData.ConversationID, ch.peerID)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to check group membership: %v", err), "chat_handler")
		return
	}
	if !isMember {
		// Queue the sender key for later processing when group_create arrives
		ch.pendingSenderKeysMu.Lock()
		ch.pendingSenderKeys[distData.ConversationID] = append(ch.pendingSenderKeys[distData.ConversationID], &PendingSenderKey{
			SenderPeerID:  distData.SenderPeerID,
			ChainKey:      chainKey,
			MessageNumber: distData.MessageNumber,
			Timestamp:     distData.Timestamp,
		})
		ch.pendingSenderKeysMu.Unlock()
		ch.logger.Debug(fmt.Sprintf("Queued sender key from %s for group %s (not yet a member)",
			remotePeerID[:8], distData.ConversationID[:8]), "chat_handler")
		return
	}

	// Add sender key to crypto manager
	if err := ch.cryptoManager.AddGroupSenderKey(distData.ConversationID, distData.SenderPeerID, chainKey, distData.MessageNumber); err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to add sender key: %v", err), "chat_handler")
		return
	}

	// Save received sender key to database for persistence
	encryptedKey, nonce, err := ch.cryptoManager.EncryptSenderKeyForStorage(chainKey)
	if err != nil {
		ch.logger.Warn(fmt.Sprintf("Failed to encrypt sender key for storage: %v", err), "chat_handler")
	} else {
		senderKeyRecord := &database.ChatSenderKey{
			ConversationID:    distData.ConversationID,
			SenderPeerID:      distData.SenderPeerID,
			EncryptedChainKey: encryptedKey,
			Nonce:             nonce[:],
			MessageNumber:     distData.MessageNumber,
		}
		if err := ch.db.StoreSenderKey(senderKeyRecord); err != nil {
			ch.logger.Warn(fmt.Sprintf("Failed to save received sender key to DB: %v", err), "chat_handler")
		}
	}

	ch.logger.Info(fmt.Sprintf("🔑 Stored sender key from %s for group %s (msgNum: %d)",
		remotePeerID[:8], distData.ConversationID[:8], distData.MessageNumber), "chat_handler")
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
