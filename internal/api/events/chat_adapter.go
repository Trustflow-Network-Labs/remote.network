package events

import (
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

// ChatEmitterAdapter adapts the Emitter to the ChatEventEmitter interface
// This allows ChatHandler to emit events without depending on the events package
type ChatEmitterAdapter struct {
	emitter *Emitter
}

// NewChatEmitterAdapter creates a new chat event emitter adapter
func NewChatEmitterAdapter(emitter *Emitter) *ChatEmitterAdapter {
	return &ChatEmitterAdapter{
		emitter: emitter,
	}
}

// EmitMessageCreated emits a message created event (status: created)
// This is called immediately after storing the message, before sending to recipient
func (cea *ChatEmitterAdapter) EmitMessageCreated(message *database.ChatMessage) {
	cea.emitter.EmitMessageCreated(message)
}

// EmitMessageReceived emits a message received event
func (cea *ChatEmitterAdapter) EmitMessageReceived(message *database.ChatMessage) {
	cea.emitter.EmitChatMessageReceived(message)
}

// EmitMessageDelivered emits a message delivered event
func (cea *ChatEmitterAdapter) EmitMessageDelivered(messageID, conversationID string) {
	// Get current timestamp for delivered event
	timestamp := time.Now().Unix()
	cea.emitter.EmitChatMessageStatusUpdate(messageID, conversationID, "delivered", timestamp)
}

// EmitMessageRead emits a message read event
func (cea *ChatEmitterAdapter) EmitMessageRead(messageID, conversationID string) {
	// Get current timestamp for read event
	timestamp := time.Now().Unix()
	cea.emitter.EmitChatMessageStatusUpdate(messageID, conversationID, "read", timestamp)
}

// EmitConversationCreated emits a conversation created event
func (cea *ChatEmitterAdapter) EmitConversationCreated(conversation *database.ChatConversation) {
	cea.emitter.EmitChatConversationCreated(conversation)
}

// EmitConversationUpdated emits a conversation updated event
func (cea *ChatEmitterAdapter) EmitConversationUpdated(conversation *database.ChatConversation) {
	cea.emitter.EmitChatConversationUpdated(conversation)
}

// EmitGroupInviteReceived emits a group invite received event
func (cea *ChatEmitterAdapter) EmitGroupInviteReceived(groupID, groupName, inviterPeerID string) {
	// Not yet implemented in emitter, stub for now
	cea.emitter.logger.Debug("Group invite received (not yet implemented)", "chat_adapter")
}
