package p2p

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ChatMessageHandler handles chat message sending with relay support and retry logic
type ChatMessageHandler struct {
	logger        *utils.LogsManager
	config        *utils.ConfigManager
	quicPeer      *QUICPeer
	dbManager     *database.SQLiteManager
	cryptoManager *crypto.ChatCryptoManager
	knownPeers    *KnownPeersManager
	metadataQuery *MetadataQueryService
	ourPeerID     string
	maxRetries    int
	retryBackoff  time.Duration

	// Relay connection cache (peerID -> relay info)
	relayCache   map[string]*RelayCache
	relayCacheMu sync.RWMutex
}

// NewChatMessageHandler creates a new chat message handler
func NewChatMessageHandler(
	logger *utils.LogsManager,
	config *utils.ConfigManager,
	quicPeer *QUICPeer,
	cryptoManager *crypto.ChatCryptoManager,
) *ChatMessageHandler {
	// Reuse invoice retry config for chat messages
	maxRetries := config.GetConfigInt("invoice_max_retries", 3, 0, 10)
	retryBackoff := time.Duration(config.GetConfigInt("invoice_retry_backoff_ms", 2000, 500, 10000)) * time.Millisecond

	return &ChatMessageHandler{
		logger:        logger,
		config:        config,
		quicPeer:      quicPeer,
		cryptoManager: cryptoManager,
		maxRetries:    maxRetries,
		retryBackoff:  retryBackoff,
		relayCache:    make(map[string]*RelayCache),
	}
}

// SetDependencies sets the additional dependencies needed for relay forwarding
func (cmh *ChatMessageHandler) SetDependencies(
	dbManager *database.SQLiteManager,
	knownPeers *KnownPeersManager,
	metadataQuery *MetadataQueryService,
	ourPeerID string,
) {
	cmh.dbManager = dbManager
	cmh.knownPeers = knownPeers
	cmh.metadataQuery = metadataQuery
	cmh.ourPeerID = ourPeerID
}

// ensureConnection ensures a connection exists to a peer.
// If no connection exists, it queries DHT for peer metadata and attempts to connect.
// Returns the connection method (direct or relay) or an error.
func (cmh *ChatMessageHandler) ensureConnection(peerID string) (ConnectionMethod, error) {
	// Check if we already have a connection
	if _, exists := cmh.quicPeer.GetAddressByPeerID(peerID); exists {
		cmh.logger.Debug(fmt.Sprintf("Existing connection found for peer %s", peerID[:8]), "chat_message_handler")
		return ConnectionMethodDirect, nil
	}

	cmh.logger.Debug(fmt.Sprintf("No existing connection to peer %s, looking up metadata", peerID[:8]), "chat_message_handler")

	// Get peer's public key from known peers
	peer, err := cmh.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return ConnectionMethodNone, fmt.Errorf("peer %s not found or has no public key", peerID[:8])
	}

	// Query DHT for metadata
	metadata, err := cmh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		return ConnectionMethodNone, fmt.Errorf("failed to query metadata: %v", err)
	}

	// Determine connection method based on metadata
	if metadata.NetworkInfo.NodeType == "public" && metadata.NetworkInfo.PublicIP != "" && metadata.NetworkInfo.PublicPort > 0 {
		// Public peer - connect directly
		addr := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
		cmh.logger.Debug(fmt.Sprintf("Peer %s is public, connecting to %s", peerID[:8], addr), "chat_message_handler")

		conn, err := cmh.quicPeer.ConnectToPeer(addr)
		if err != nil {
			return ConnectionMethodNone, fmt.Errorf("failed to connect to public peer: %v", err)
		}

		// Wait for identity exchange to complete (connection is not usable until then)
		// The connection should be registered by peer ID after identity exchange
		// Give it a short time to complete
		for i := 0; i < 10; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, exists := cmh.quicPeer.GetAddressByPeerID(peerID); exists {
				cmh.logger.Info(fmt.Sprintf("✅ Connected directly to peer %s at %s", peerID[:8], conn.RemoteAddr().String()), "chat_message_handler")
				return ConnectionMethodDirect, nil
			}
		}

		// Identity exchange may not have completed yet, but connection is established
		cmh.logger.Debug(fmt.Sprintf("Connection established to %s, identity exchange pending", addr), "chat_message_handler")
		return ConnectionMethodDirect, nil
	}

	// Check if peer is accessible via relay
	if metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.RelayAddress != "" && metadata.NetworkInfo.RelaySessionID != "" {
		cmh.logger.Debug(fmt.Sprintf("Peer %s is behind NAT, will use relay at %s", peerID[:8], metadata.NetworkInfo.RelayAddress), "chat_message_handler")
		return ConnectionMethodRelay, nil
	}

	return ConnectionMethodNone, fmt.Errorf("peer %s is not connectable (node_type: %s, using_relay: %t)",
		peerID[:8], metadata.NetworkInfo.NodeType, metadata.NetworkInfo.UsingRelay)
}

// HandleKeyExchangeAck processes a key exchange ACK received via store-and-forward delivery.
// This completes the key exchange that was initiated when the peer was offline.
func (cmh *ChatMessageHandler) HandleKeyExchangeAck(conversationID string, toPeerID string, ackBytes []byte) error {
	// Parse the ACK message
	ackMsg, err := UnmarshalQUICMessage(ackBytes)
	if err != nil {
		return fmt.Errorf("failed to parse ACK message: %v", err)
	}

	var ackData ChatKeyExchangeAckData
	if err := ackMsg.GetDataAs(&ackData); err != nil {
		return fmt.Errorf("failed to parse ACK data: %v", err)
	}

	// Validate recipient
	if ackData.ToPeerID != cmh.ourPeerID {
		return fmt.Errorf("key exchange ACK not for us")
	}

	// Validate sender
	if ackData.FromPeerID != toPeerID {
		return fmt.Errorf("key exchange ACK sender mismatch")
	}

	// Get remote peer's public key
	peer, err := cmh.knownPeers.GetKnownPeer(toPeerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return fmt.Errorf("failed to get remote peer public key: %v", err)
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(ackData.EphemeralPubKey) != 32 {
		return fmt.Errorf("invalid ephemeral public key size: %d", len(ackData.EphemeralPubKey))
	}
	copy(ephemeralPubKey[:], ackData.EphemeralPubKey)

	// Complete the key exchange by initializing sender chains with receiver's ephemeral key
	if err := cmh.cryptoManager.CompleteKeyExchange(
		ackData.ConversationID,
		peer.PublicKey,
		ephemeralPubKey,
		ackData.IdentitySignature,
	); err != nil {
		return fmt.Errorf("failed to complete key exchange: %v", err)
	}

	// Save updated ratchet state to database - mark as complete for initiator
	if err := cmh.saveRatchetStateComplete(conversationID, true); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_message_handler")
	}

	cmh.logger.Info(fmt.Sprintf("✅ Key exchange completed with peer %s (via store-and-forward)", toPeerID[:8]), "chat_message_handler")
	return nil
}

// InitiateKeyExchange initiates a key exchange with a remote peer
func (cmh *ChatMessageHandler) InitiateKeyExchange(
	conversationID string,
	toPeerID string,
	remotePeerPubKey ed25519.PublicKey,
) error {
	cmh.logger.Debug(fmt.Sprintf("Initiating key exchange with peer %s", toPeerID[:8]), "chat_message_handler")

	// Initiate key exchange and get our ephemeral public key + signature
	ephemeralPubKey, signature, err := cmh.cryptoManager.InitiateKeyExchange(conversationID, remotePeerPubKey)
	if err != nil {
		return fmt.Errorf("failed to initiate key exchange: %v", err)
	}

	// Save ratchet state to database after initialization
	if err := cmh.saveRatchetState(conversationID); err != nil {
		return fmt.Errorf("failed to save ratchet state: %v", err)
	}

	cmh.logger.Info(fmt.Sprintf("🔑 Ratchet state created and saved for conversation %s", conversationID[:8]), "chat_message_handler")

	// Create key exchange message
	keyExchangeData := &ChatKeyExchangeData{
		ConversationID:    conversationID,
		FromPeerID:        cmh.ourPeerID,
		ToPeerID:          toPeerID,
		EphemeralPubKey:   ephemeralPubKey[:],
		IdentitySignature: signature,
		Timestamp:         time.Now().Unix(),
	}

	// Send key exchange message (with retry)
	return cmh.sendKeyExchangeWithRetry(toPeerID, keyExchangeData)
}

// SendChatMessageWithRetry sends an encrypted chat message with retry logic
func (cmh *ChatMessageHandler) SendChatMessageWithRetry(
	conversationID string,
	toPeerID string,
	plaintextContent string,
) (string, error) {
	// Generate message ID
	messageID := uuid.New().String()

	// Check if ratchet exists for this conversation
	hasRatchet, err := cmh.dbManager.HasRatchetState(conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to check ratchet state: %v", err)
	}

	if !hasRatchet {
		return "", fmt.Errorf("key exchange required: no ratchet state found for conversation")
	}

	// Load ratchet state from database if not in memory
	if err := cmh.loadRatchetState(conversationID); err != nil {
		return "", fmt.Errorf("failed to load ratchet state: %v", err)
	}

	// Encrypt message using Double Ratchet
	ciphertext, nonce, messageNum, dhPubKey, err := cmh.cryptoManager.Encrypt1on1Message(
		conversationID,
		[]byte(plaintextContent),
	)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt message: %v", err)
	}

	// Save updated ratchet state
	if err := cmh.saveRatchetState(conversationID); err != nil {
		return "", fmt.Errorf("failed to save ratchet state: %v", err)
	}

	// Store encrypted message in database (status: pending)
	chatMessage := &database.ChatMessage{
		MessageID:        messageID,
		ConversationID:   conversationID,
		SenderPeerID:     cmh.ourPeerID,
		EncryptedContent: ciphertext,
		Nonce:            nonce[:],
		MessageNumber:    messageNum,
		Timestamp:        time.Now().Unix(),
		Status:           "pending",
		DecryptedContent: plaintextContent, // Store plaintext for display
	}

	if err := cmh.dbManager.StoreMessage(chatMessage); err != nil {
		return "", fmt.Errorf("failed to store message: %v", err)
	}

	// Create message data
	messageData := &ChatMessageData{
		MessageID:        messageID,
		ConversationID:   conversationID,
		SenderPeerID:     cmh.ourPeerID,
		EncryptedContent: ciphertext,
		Nonce:            nonce[:],
		MessageNumber:    messageNum,
		DHPubKey:         dhPubKey[:],
		Timestamp:        chatMessage.Timestamp,
	}

	// Marshal message for store-and-forward
	msg := CreateChatMessage(messageData)
	msgBytes, marshalErr := msg.Marshal()
	if marshalErr != nil {
		return "", fmt.Errorf("failed to marshal message: %v", marshalErr)
	}

	// Attempt delivery with retry
	var lastErr error
	for attempt := 0; attempt <= cmh.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := cmh.retryBackoff * time.Duration(1<<uint(attempt-1))
			cmh.logger.Info(fmt.Sprintf("Retrying message %s send to peer %s after %v (attempt %d/%d)",
				messageID[:8], toPeerID[:8], backoff, attempt+1, cmh.maxRetries+1), "chat_message_handler")
			time.Sleep(backoff)
		}

		// First, ensure we have a connection (or determine connection method)
		connMethod, connErr := cmh.ensureConnection(toPeerID)
		if connErr != nil {
			cmh.logger.Debug(fmt.Sprintf("Failed to establish connection to peer %s: %v", toPeerID[:8], connErr), "chat_message_handler")
			// Try store-and-forward for offline public peers
			if cmh.quicPeer.localStoreForward != nil {
				isPublic, typeErr := cmh.quicPeer.localStoreForward.IsPublicPeer(toPeerID)
				if typeErr == nil && isPublic {
					if storeErr := cmh.quicPeer.localStoreForward.TryStoreMessage(toPeerID, msgBytes); storeErr == nil {
						cmh.logger.Info(fmt.Sprintf("💬 Message %s queued for offline public peer %s via local store-and-forward", messageID[:8], toPeerID[:8]), "chat_message_handler")
						if err := cmh.dbManager.UpdateMessageStatus(messageID, "sent"); err != nil {
							cmh.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_message_handler")
						}
						return messageID, nil
					}
				}
			}
			lastErr = connErr
			continue
		}

		// Route based on connection method
		switch connMethod {
		case ConnectionMethodDirect:
			err := cmh.sendChatMessageDirect(toPeerID, messageData)
			if err == nil {
				cmh.logger.Info(fmt.Sprintf("💬 Message %s sent successfully to peer %s (direct)", messageID[:8], toPeerID[:8]), "chat_message_handler")
				if err := cmh.dbManager.UpdateMessageStatus(messageID, "sent"); err != nil {
					cmh.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_message_handler")
				}
				return messageID, nil
			}
			cmh.logger.Debug(fmt.Sprintf("Direct send failed for message %s to peer %s: %v", messageID[:8], toPeerID[:8], err), "chat_message_handler")
			lastErr = err

		case ConnectionMethodRelay:
			err := cmh.sendChatMessageViaRelay(toPeerID, messageData)
			if err == nil {
				cmh.logger.Info(fmt.Sprintf("💬 Message %s sent via relay to peer %s", messageID[:8], toPeerID[:8]), "chat_message_handler")
				if err := cmh.dbManager.UpdateMessageStatus(messageID, "sent"); err != nil {
					cmh.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_message_handler")
				}
				return messageID, nil
			}
			cmh.logger.Debug(fmt.Sprintf("Relay send failed for message %s to peer %s: %v", messageID[:8], toPeerID[:8], err), "chat_message_handler")
			cmh.invalidateRelayCache(toPeerID)
			lastErr = err

		default:
			lastErr = fmt.Errorf("peer %s is not connectable", toPeerID[:8])
		}
	}

	// Update status to failed
	if err := cmh.dbManager.UpdateMessageStatus(messageID, "failed"); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to update message status: %v", err), "chat_message_handler")
	}

	return messageID, fmt.Errorf("failed to send message after %d attempts: %v", cmh.maxRetries+1, lastErr)
}

// SendDeliveryConfirmation sends a delivery confirmation for a received message
func (cmh *ChatMessageHandler) SendDeliveryConfirmation(toPeerID, messageID, conversationID string) error {
	msg := CreateChatDeliveryConfirmation(messageID, conversationID, cmh.ourPeerID, time.Now().Unix())
	msgBytes, err := msg.Marshal()
	if err != nil {
		cmh.logger.Debug(fmt.Sprintf("Failed to marshal delivery confirmation: %v", err), "chat_message_handler")
		return err
	}

	// Try direct send (no retry for confirmations to avoid overhead)
	if err := cmh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		cmh.logger.Debug(fmt.Sprintf("Failed to send delivery confirmation: %v", err), "chat_message_handler")
		return err
	}

	return nil
}

// SendReadReceipt sends a read receipt for a message
func (cmh *ChatMessageHandler) SendReadReceipt(toPeerID, messageID, conversationID string) error {
	msg := CreateChatReadReceipt(messageID, conversationID, cmh.ourPeerID, time.Now().Unix())
	msgBytes, err := msg.Marshal()
	if err != nil {
		cmh.logger.Debug(fmt.Sprintf("Failed to marshal read receipt: %v", err), "chat_message_handler")
		return err
	}

	// Try direct send (no retry for read receipts)
	if err := cmh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		cmh.logger.Debug(fmt.Sprintf("Failed to send read receipt: %v", err), "chat_message_handler")
		return err
	}

	return nil
}

// SendGroupInvite sends a group invitation to a peer
func (cmh *ChatMessageHandler) SendGroupInvite(toPeerID string, inviteData *ChatGroupInviteData) error {
	msg := CreateChatGroupInvite(inviteData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal group invite: %v", err)
	}

	// Try direct first, then relay
	if err := cmh.quicPeer.SendMessageToPeer(toPeerID, msgBytes); err != nil {
		cmh.logger.Debug(fmt.Sprintf("Direct group invite failed to peer %s: %v, trying relay", toPeerID[:8], err), "chat_message_handler")

		// Try via relay
		if relayErr := cmh.sendMessageViaRelay(toPeerID, msgBytes, "chat_group_invite"); relayErr != nil {
			return fmt.Errorf("failed to send group invite: direct=%v, relay=%v", err, relayErr)
		}
	}

	cmh.logger.Info(fmt.Sprintf("👥 Sent group invite to peer %s for group %s", toPeerID[:8], inviteData.ConversationID[:8]), "chat_message_handler")
	return nil
}

// sendKeyExchangeWithRetry sends a key exchange message with retry
func (cmh *ChatMessageHandler) sendKeyExchangeWithRetry(toPeerID string, keyExchangeData *ChatKeyExchangeData) error {
	var lastErr error

	// Marshal message bytes for store-and-forward
	msg := CreateChatKeyExchange(keyExchangeData)
	msgBytes, marshalErr := msg.Marshal()
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal key exchange: %v", marshalErr)
	}

	for attempt := 0; attempt <= cmh.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := cmh.retryBackoff * time.Duration(1<<uint(attempt-1))
			cmh.logger.Info(fmt.Sprintf("Retrying key exchange to peer %s after %v (attempt %d/%d)",
				toPeerID[:8], backoff, attempt+1, cmh.maxRetries+1), "chat_message_handler")
			time.Sleep(backoff)
		}

		// First, ensure we have a connection (or determine connection method)
		connMethod, connErr := cmh.ensureConnection(toPeerID)
		if connErr != nil {
			cmh.logger.Debug(fmt.Sprintf("Failed to establish connection to peer %s: %v", toPeerID[:8], connErr), "chat_message_handler")
			// Try store-and-forward for offline public peers
			if cmh.quicPeer.localStoreForward != nil {
				isPublic, typeErr := cmh.quicPeer.localStoreForward.IsPublicPeer(toPeerID)
				if typeErr == nil && isPublic {
					if storeErr := cmh.quicPeer.localStoreForward.TryStoreMessage(toPeerID, msgBytes); storeErr == nil {
						cmh.logger.Info(fmt.Sprintf("🔑 Key exchange queued for offline public peer %s via local store-and-forward", toPeerID[:8]), "chat_message_handler")
						return nil
					}
				}
			}
			lastErr = connErr
			continue
		}

		// Route based on connection method
		switch connMethod {
		case ConnectionMethodDirect:
			// Try direct connection
			err := cmh.sendKeyExchangeDirect(toPeerID, keyExchangeData)
			if err == nil {
				cmh.logger.Info(fmt.Sprintf("🔑 Key exchange sent successfully to peer %s (direct)", toPeerID[:8]), "chat_message_handler")
				return nil
			}
			cmh.logger.Debug(fmt.Sprintf("Direct key exchange failed to peer %s: %v", toPeerID[:8], err), "chat_message_handler")
			lastErr = err

		case ConnectionMethodRelay:
			// Send via relay
			err := cmh.sendKeyExchangeViaRelay(toPeerID, keyExchangeData)
			if err == nil {
				cmh.logger.Info(fmt.Sprintf("🔑 Key exchange sent via relay to peer %s", toPeerID[:8]), "chat_message_handler")
				return nil
			}
			cmh.logger.Debug(fmt.Sprintf("Relay key exchange failed to peer %s: %v", toPeerID[:8], err), "chat_message_handler")
			cmh.invalidateRelayCache(toPeerID)
			lastErr = err

		default:
			lastErr = fmt.Errorf("peer %s is not connectable", toPeerID[:8])
		}
	}

	return fmt.Errorf("failed to send key exchange after %d attempts: %v", cmh.maxRetries+1, lastErr)
}

// sendKeyExchangeDirect sends a key exchange message directly and waits for ACK
func (cmh *ChatMessageHandler) sendKeyExchangeDirect(toPeerID string, keyExchangeData *ChatKeyExchangeData) error {
	msg := CreateChatKeyExchange(keyExchangeData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal key exchange: %v", err)
	}

	// Send message and wait for ACK response on the same stream
	responseBytes, err := cmh.quicPeer.SendMessageWithResponse(toPeerID, msgBytes)
	if err != nil {
		return fmt.Errorf("failed to send key exchange: %v", err)
	}

	// Parse the ACK response
	ackMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		return fmt.Errorf("failed to parse key exchange ACK: %v", err)
	}

	if ackMsg.Type != MessageTypeChatKeyExchangeAck {
		return fmt.Errorf("unexpected response type: %s (expected chat_key_exchange_ack)", ackMsg.Type)
	}

	// Parse ACK data
	var ackData ChatKeyExchangeAckData
	if err := ackMsg.GetDataAs(&ackData); err != nil {
		return fmt.Errorf("failed to parse key exchange ACK data: %v", err)
	}

	// Validate recipient
	if ackData.ToPeerID != cmh.ourPeerID {
		return fmt.Errorf("key exchange ACK not for us")
	}

	// Validate sender
	if ackData.FromPeerID != toPeerID {
		return fmt.Errorf("key exchange ACK sender mismatch")
	}

	// Get remote peer's public key
	peer, err := cmh.knownPeers.GetKnownPeer(toPeerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return fmt.Errorf("failed to get remote peer public key: %v", err)
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(ackData.EphemeralPubKey) != 32 {
		return fmt.Errorf("invalid ephemeral public key size: %d", len(ackData.EphemeralPubKey))
	}
	copy(ephemeralPubKey[:], ackData.EphemeralPubKey)

	// Complete the key exchange by performing DH ratchet with receiver's ephemeral key
	if err := cmh.cryptoManager.CompleteKeyExchange(
		ackData.ConversationID,
		peer.PublicKey,
		ephemeralPubKey,
		ackData.IdentitySignature,
	); err != nil {
		return fmt.Errorf("failed to complete key exchange: %v", err)
	}

	// Save updated ratchet state to database - mark as complete for initiator
	if err := cmh.saveRatchetStateComplete(keyExchangeData.ConversationID, true); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_message_handler")
	}

	cmh.logger.Info(fmt.Sprintf("✅ Key exchange completed with peer %s (direct)", toPeerID[:8]), "chat_message_handler")
	return nil
}

// sendKeyExchangeViaRelay sends a key exchange message via relay and waits for ACK
// Unlike other relay messages, key exchange uses request-response pattern through the relay
func (cmh *ChatMessageHandler) sendKeyExchangeViaRelay(toPeerID string, keyExchangeData *ChatKeyExchangeData) error {
	if cmh.dbManager == nil || cmh.metadataQuery == nil || cmh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	msg := CreateChatKeyExchange(keyExchangeData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal key exchange: %v", err)
	}

	// Get relay info with caching
	relayAddress, relaySessionID, err := cmh.getRelayInfo(toPeerID)
	if err != nil {
		return err
	}

	// Wrap message in relay forward
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: cmh.ourPeerID,
		TargetPeerID: toPeerID,
		MessageType:  "chat_key_exchange",
		Payload:      msgBytes,
		PayloadSize:  int64(len(msgBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Connect to relay
	cmh.logger.Debug(fmt.Sprintf("Connecting to relay %s for key exchange to peer %s", relayAddress, toPeerID[:8]), "chat_message_handler")
	conn, err := cmh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		relayAddr := conn.RemoteAddr().String()
		cmh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "chat_message_handler")
		cmh.quicPeer.DisconnectFromPeer(relayAddr)
		return fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send key exchange message to relay
	cmh.logger.Debug(fmt.Sprintf("Sending key exchange via relay to peer %s", toPeerID[:8]), "chat_message_handler")
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return fmt.Errorf("failed to send via relay: %v", err)
	}

	// Wait for ACK response routed back through relay (not just "success")
	cmh.logger.Debug(fmt.Sprintf("Waiting for key exchange ACK via relay from peer %s (timeout: 15s)", toPeerID[:8]), "chat_message_handler")
	responseBuf := make([]byte, 4096) // Larger buffer for ACK message
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, err := stream.Read(responseBuf)
	if err != nil {
		return fmt.Errorf("failed to read key exchange ACK via relay: %v", err)
	}

	// Parse the ACK response
	ackMsg, err := UnmarshalQUICMessage(responseBuf[:n])
	if err != nil {
		return fmt.Errorf("failed to parse key exchange ACK: %v", err)
	}

	if ackMsg.Type != MessageTypeChatKeyExchangeAck {
		return fmt.Errorf("unexpected response type: %s (expected chat_key_exchange_ack)", ackMsg.Type)
	}

	// Parse ACK data
	var ackData ChatKeyExchangeAckData
	if err := ackMsg.GetDataAs(&ackData); err != nil {
		return fmt.Errorf("failed to parse key exchange ACK data: %v", err)
	}

	// Validate recipient
	if ackData.ToPeerID != cmh.ourPeerID {
		return fmt.Errorf("key exchange ACK not for us")
	}

	// Validate sender
	if ackData.FromPeerID != toPeerID {
		return fmt.Errorf("key exchange ACK sender mismatch")
	}

	// Get remote peer's public key
	peer, err := cmh.knownPeers.GetKnownPeer(toPeerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return fmt.Errorf("failed to get remote peer public key: %v", err)
	}

	// Convert ephemeral public key to array
	var ephemeralPubKey [32]byte
	if len(ackData.EphemeralPubKey) != 32 {
		return fmt.Errorf("invalid ephemeral public key size: %d", len(ackData.EphemeralPubKey))
	}
	copy(ephemeralPubKey[:], ackData.EphemeralPubKey)

	// Complete the key exchange by performing DH ratchet with receiver's ephemeral key
	if err := cmh.cryptoManager.CompleteKeyExchange(
		ackData.ConversationID,
		peer.PublicKey,
		ephemeralPubKey,
		ackData.IdentitySignature,
	); err != nil {
		return fmt.Errorf("failed to complete key exchange: %v", err)
	}

	// Save updated ratchet state to database - mark as complete for initiator
	if err := cmh.saveRatchetStateComplete(keyExchangeData.ConversationID, true); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to save ratchet state: %v", err), "chat_message_handler")
	}

	cmh.logger.Info(fmt.Sprintf("✅ Key exchange completed with peer %s (via relay)", toPeerID[:8]), "chat_message_handler")
	return nil
}

// sendChatMessageDirect sends a chat message directly
func (cmh *ChatMessageHandler) sendChatMessageDirect(toPeerID string, messageData *ChatMessageData) error {
	msg := CreateChatMessage(messageData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal chat message: %v", err)
	}
	return cmh.quicPeer.SendMessageToPeer(toPeerID, msgBytes)
}

// sendChatMessageViaRelay sends a chat message via relay
func (cmh *ChatMessageHandler) sendChatMessageViaRelay(toPeerID string, messageData *ChatMessageData) error {
	if cmh.dbManager == nil || cmh.metadataQuery == nil || cmh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	msg := CreateChatMessage(messageData)
	msgBytes, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal chat message: %v", err)
	}

	return cmh.sendMessageViaRelay(toPeerID, msgBytes, "chat_message")
}

// sendMessageViaRelay sends a one-way message via relay (no response expected)
func (cmh *ChatMessageHandler) sendMessageViaRelay(peerID string, messageBytes []byte, messageType string) error {
	// Get relay info with caching
	relayAddress, relaySessionID, err := cmh.getRelayInfo(peerID)
	if err != nil {
		return err
	}

	// Wrap message in relay forward
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: cmh.ourPeerID,
		TargetPeerID: peerID,
		MessageType:  messageType,
		Payload:      messageBytes,
		PayloadSize:  int64(len(messageBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Connect to relay
	cmh.logger.Debug(fmt.Sprintf("Connecting to relay %s for %s to peer %s", relayAddress, messageType, peerID[:8]), "chat_message_handler")
	conn, err := cmh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		relayAddr := conn.RemoteAddr().String()
		cmh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "chat_message_handler")
		cmh.quicPeer.DisconnectFromPeer(relayAddr)
		return fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send message to relay
	cmh.logger.Debug(fmt.Sprintf("Sending %s via relay to peer %s", messageType, peerID[:8]), "chat_message_handler")
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return fmt.Errorf("failed to send via relay: %v", err)
	}

	// Wait for relay's delivery confirmation response
	// Timeout must be long enough for relay to:
	// 1. Forward message to target peer (if online)
	// 2. Store message for later delivery (if offline)
	cmh.logger.Debug(fmt.Sprintf("Waiting for relay delivery confirmation for %s to peer %s (timeout: 15s)", messageType, peerID[:8]), "chat_message_handler")
	responseBuf := make([]byte, 512)
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, err := stream.Read(responseBuf)
	if err != nil {
		return fmt.Errorf("failed to read relay delivery confirmation: %v (destination peer may be offline)", err)
	}

	responseStr := string(responseBuf[:n])
	cmh.logger.Debug(fmt.Sprintf("Received relay response for %s to peer %s: %s", messageType, peerID[:8], responseStr), "chat_message_handler")

	if n > 6 && responseStr[:6] == "error:" {
		return fmt.Errorf("relay delivery failed: %s", responseStr[7:])
	}

	if responseStr != "success" {
		return fmt.Errorf("unexpected relay response: %s", responseStr)
	}

	cmh.logger.Info(fmt.Sprintf("✅ Sent %s to peer %s via relay successfully", messageType, peerID[:8]), "chat_message_handler")
	return nil
}

// getRelayInfo retrieves relay connection info for a peer with caching
func (cmh *ChatMessageHandler) getRelayInfo(peerID string) (relayAddress string, relaySessionID string, err error) {
	// Check cache first
	cmh.relayCacheMu.RLock()
	cached, exists := cmh.relayCache[peerID]
	cmh.relayCacheMu.RUnlock()

	// Use cached data if it exists and is fresh (< 1 minute old)
	if exists && time.Since(cached.CachedAt) < 1*time.Minute {
		cmh.logger.Debug(fmt.Sprintf("Using cached relay info for peer %s (age: %v)", peerID[:8], time.Since(cached.CachedAt)), "chat_message_handler")
		return cached.RelayAddress, cached.RelaySessionID, nil
	}

	// Cache miss or expired - query metadata
	cmh.logger.Debug(fmt.Sprintf("Querying relay metadata for peer %s (cache miss or expired)", peerID[:8]), "chat_message_handler")

	// Get peer from database
	peer, err := cmh.knownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return "", "", fmt.Errorf("peer %s not found or has no public key", peerID[:8])
	}

	// Query DHT for metadata
	metadata, err := cmh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		// Invalidate cache on error
		cmh.relayCacheMu.Lock()
		delete(cmh.relayCache, peerID)
		cmh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("failed to query metadata: %v", err)
	}

	// Validate relay information
	if !metadata.NetworkInfo.UsingRelay || metadata.NetworkInfo.RelayAddress == "" || metadata.NetworkInfo.RelaySessionID == "" {
		cmh.relayCacheMu.Lock()
		delete(cmh.relayCache, peerID)
		cmh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("peer not accessible via relay")
	}

	// Update cache
	cmh.relayCacheMu.Lock()
	cmh.relayCache[peerID] = &RelayCache{
		RelayAddress:   metadata.NetworkInfo.RelayAddress,
		RelaySessionID: metadata.NetworkInfo.RelaySessionID,
		CachedAt:       time.Now(),
	}
	cmh.relayCacheMu.Unlock()

	cmh.logger.Debug(fmt.Sprintf("Cached relay info for peer %s: relay=%s, session=%s",
		peerID[:8], metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID[:8]), "chat_message_handler")

	return metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID, nil
}

// invalidateRelayCache invalidates the relay cache for a peer
func (cmh *ChatMessageHandler) invalidateRelayCache(peerID string) {
	cmh.relayCacheMu.Lock()
	delete(cmh.relayCache, peerID)
	cmh.relayCacheMu.Unlock()
}

// saveRatchetState encrypts and saves the ratchet state to database
// Preserves the existing key_exchange_complete flag to avoid overwriting it
func (cmh *ChatMessageHandler) saveRatchetState(conversationID string) error {
	// Get ratchet from crypto manager
	ratchet, err := cmh.cryptoManager.GetRatchet(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get ratchet: %v", err)
	}

	// Get current key exchange completion status from database
	existingState, err := cmh.dbManager.GetRatchetState(conversationID)
	keyExchangeComplete := false
	if err == nil && existingState != nil {
		keyExchangeComplete = existingState.KeyExchangeComplete
	}

	// Encrypt ratchet state
	encryptedState, nonce, err := cmh.cryptoManager.EncryptRatchetForStorage(ratchet)
	if err != nil {
		return fmt.Errorf("failed to encrypt ratchet: %v", err)
	}

	// Save main ratchet state to database
	// Preserve existing key_exchange_complete flag
	state := &database.ChatRatchetState{
		ConversationID:      conversationID,
		EncryptedState:      encryptedState,
		Nonce:               nonce[:],
		SendMessageNum:      ratchet.SendMessageNum,
		RecvMessageNum:      ratchet.RecvMessageNum,
		RemoteDHPubKey:      ratchet.RemoteDHPubKey[:],
		KeyExchangeComplete: keyExchangeComplete, // Preserve existing value
	}

	if err := cmh.dbManager.StoreRatchetState(state); err != nil {
		return err
	}

	// Delete all old skipped keys for this conversation
	if err := cmh.dbManager.DeleteAllSkippedKeysForConversation(conversationID); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to delete old skipped keys: %v", err), "chat_message_handler")
	}

	// Save current skipped keys to database (encrypted)
	for messageNum, messageKey := range ratchet.SkippedKeys {
		// Encrypt the message key
		encryptedKey, keyNonce, err := cmh.cryptoManager.EncryptMessageKeyForStorage(messageKey)
		if err != nil {
			cmh.logger.Warn(fmt.Sprintf("Failed to encrypt skipped key: %v", err), "chat_message_handler")
			continue
		}

		skippedKey := &database.ChatSkippedKey{
			ConversationID: conversationID,
			MessageNumber:  messageNum,
			EncryptedKey:   encryptedKey,
			Nonce:          keyNonce[:],
		}

		if err := cmh.dbManager.StoreSkippedKey(skippedKey); err != nil {
			cmh.logger.Warn(fmt.Sprintf("Failed to store skipped key for message %d: %v", messageNum, err), "chat_message_handler")
		}
	}

	return nil
}

// saveRatchetStateComplete encrypts and saves the ratchet state with explicit completion flag
func (cmh *ChatMessageHandler) saveRatchetStateComplete(conversationID string, keyExchangeComplete bool) error {
	// Get ratchet from crypto manager
	ratchet, err := cmh.cryptoManager.GetRatchet(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get ratchet: %v", err)
	}

	// Encrypt ratchet state
	encryptedState, nonce, err := cmh.cryptoManager.EncryptRatchetForStorage(ratchet)
	if err != nil {
		return fmt.Errorf("failed to encrypt ratchet: %v", err)
	}

	// Save main ratchet state to database with explicit completion flag
	state := &database.ChatRatchetState{
		ConversationID:      conversationID,
		EncryptedState:      encryptedState,
		Nonce:               nonce[:],
		SendMessageNum:      ratchet.SendMessageNum,
		RecvMessageNum:      ratchet.RecvMessageNum,
		RemoteDHPubKey:      ratchet.RemoteDHPubKey[:],
		KeyExchangeComplete: keyExchangeComplete,
	}

	if err := cmh.dbManager.StoreRatchetState(state); err != nil {
		return err
	}

	// Delete all old skipped keys for this conversation
	if err := cmh.dbManager.DeleteAllSkippedKeysForConversation(conversationID); err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to delete old skipped keys: %v", err), "chat_message_handler")
	}

	// Save current skipped keys to database (encrypted)
	for messageNum, messageKey := range ratchet.SkippedKeys {
		// Encrypt the message key
		encryptedKey, keyNonce, err := cmh.cryptoManager.EncryptMessageKeyForStorage(messageKey)
		if err != nil {
			cmh.logger.Warn(fmt.Sprintf("Failed to encrypt skipped key: %v", err), "chat_message_handler")
			continue
		}

		skippedKey := &database.ChatSkippedKey{
			ConversationID: conversationID,
			MessageNumber:  messageNum,
			EncryptedKey:   encryptedKey,
			Nonce:          keyNonce[:],
		}

		if err := cmh.dbManager.StoreSkippedKey(skippedKey); err != nil {
			cmh.logger.Warn(fmt.Sprintf("Failed to store skipped key for message %d: %v", messageNum, err), "chat_message_handler")
		}
	}

	return nil
}

// loadRatchetState loads and decrypts the ratchet state from database
func (cmh *ChatMessageHandler) loadRatchetState(conversationID string) error {
	// Check if already loaded in memory
	if _, err := cmh.cryptoManager.GetRatchet(conversationID); err == nil {
		return nil // Already loaded
	}

	// Load main ratchet state from database
	state, err := cmh.dbManager.GetRatchetState(conversationID)
	if err != nil {
		return fmt.Errorf("failed to get ratchet state: %v", err)
	}

	if state == nil {
		return fmt.Errorf("ratchet state not found")
	}

	// Decrypt ratchet state
	var nonce [24]byte
	copy(nonce[:], state.Nonce)

	ratchet, err := cmh.cryptoManager.DecryptRatchetFromStorage(state.EncryptedState, nonce)
	if err != nil {
		return fmt.Errorf("failed to decrypt ratchet: %v", err)
	}

	// Restore counters
	ratchet.SendMessageNum = state.SendMessageNum
	ratchet.RecvMessageNum = state.RecvMessageNum

	// Load skipped keys from database
	skippedKeys, err := cmh.dbManager.GetAllSkippedKeys(conversationID)
	if err != nil {
		cmh.logger.Warn(fmt.Sprintf("Failed to load skipped keys: %v", err), "chat_message_handler")
	} else {
		// Decrypt and restore skipped keys
		for _, sk := range skippedKeys {
			var keyNonce [24]byte
			copy(keyNonce[:], sk.Nonce)

			messageKey, err := cmh.cryptoManager.DecryptMessageKeyFromStorage(sk.EncryptedKey, keyNonce)
			if err != nil {
				cmh.logger.Warn(fmt.Sprintf("Failed to decrypt skipped key for message %d: %v", sk.MessageNumber, err), "chat_message_handler")
				continue
			}

			ratchet.SkippedKeys[sk.MessageNumber] = messageKey
		}
		cmh.logger.Debug(fmt.Sprintf("Loaded %d skipped keys for conversation %s", len(skippedKeys), conversationID[:8]), "chat_message_handler")
	}

	// Set in crypto manager
	cmh.cryptoManager.SetRatchet(conversationID, ratchet)

	return nil
}
