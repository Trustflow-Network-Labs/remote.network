import { defineStore } from 'pinia'
import { api, type ChatConversation, type ChatMessage } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

// Grouped conversation represents all conversations with a single peer (or group)
export interface GroupedConversation {
  peer_id: string  // For 1on1: peer_id, for groups: group conversation_id
  conversation_type: '1on1' | 'group'
  group_name?: string
  conversation_ids: string[]  // All conversation IDs with this peer
  last_message_at: number
  unread_count: number
  last_message?: ChatMessage
}

export interface ChatState {
  conversations: ChatConversation[]
  messages: Record<string, ChatMessage[]> // conversationID -> messages[]
  activeConversationID: string | null  // Still track by conversation_id for compatibility
  activePeerID: string | null  // Track active peer for grouped view
  loading: boolean
  error: string | null
  totalUnreadCount: number
}

// Track unsubscribe functions outside the store
let _wsUnsubscribe: (() => void) | null = null

export const useChatStore = defineStore('chat', {
  state: (): ChatState => ({
    conversations: [],
    messages: {},
    activeConversationID: null,
    activePeerID: null,
    loading: false,
    error: null,
    totalUnreadCount: 0
  }),

  getters: {
    // Group conversations by peer_id (for 1on1) or keep separate (for groups)
    groupedConversations: (state): GroupedConversation[] => {
      const grouped = new Map<string, GroupedConversation>()

      // Helper to get the last message for a conversation from stored messages
      const getLastMessage = (conversationId: string): ChatMessage | undefined => {
        const msgs = state.messages[conversationId]
        if (!msgs || msgs.length === 0) return undefined
        // Messages are sorted by timestamp, get the last one
        return msgs[msgs.length - 1]
      }

      for (const conv of state.conversations) {
        const lastMessageAt = conv.last_message_at || 0
        const lastMessage = conv.last_message || getLastMessage(conv.conversation_id)

        if (conv.conversation_type === '1on1' && conv.peer_id) {
          // Group by peer_id for 1on1 conversations
          const existing = grouped.get(conv.peer_id)
          if (existing) {
            existing.conversation_ids.push(conv.conversation_id)
            existing.unread_count += conv.unread_count
            // Update last message if this conversation is more recent
            const existingLastMsgTime = existing.last_message?.timestamp || 0
            const thisLastMsgTime = lastMessage?.timestamp || 0
            if (lastMessageAt > existing.last_message_at || thisLastMsgTime > existingLastMsgTime) {
              existing.last_message_at = Math.max(lastMessageAt, existing.last_message_at)
              if (thisLastMsgTime > existingLastMsgTime) {
                existing.last_message = lastMessage
              }
            }
          } else {
            grouped.set(conv.peer_id, {
              peer_id: conv.peer_id,
              conversation_type: '1on1',
              conversation_ids: [conv.conversation_id],
              last_message_at: lastMessageAt,
              unread_count: conv.unread_count,
              last_message: lastMessage
            })
          }
        } else if (conv.conversation_type === 'group') {
          // Groups stay separate (use conversation_id as key)
          grouped.set(conv.conversation_id, {
            peer_id: conv.conversation_id,
            conversation_type: 'group',
            group_name: conv.group_name,
            conversation_ids: [conv.conversation_id],
            last_message_at: lastMessageAt,
            unread_count: conv.unread_count,
            last_message: lastMessage
          })
        }
      }

      // Sort by last_message_at descending (or by last message timestamp if available)
      return Array.from(grouped.values()).sort((a, b) => {
        const aTime = a.last_message?.timestamp || a.last_message_at
        const bTime = b.last_message?.timestamp || b.last_message_at
        return bTime - aTime
      })
    },

    activeConversation: (state) =>
      state.conversations.find(c => c.conversation_id === state.activeConversationID),

    // Get all messages for the active peer (aggregated from all conversations)
    activeMessages: (state): ChatMessage[] => {
      if (!state.activePeerID) return []

      // Find all conversation IDs for this peer
      const convIds = state.conversations
        .filter(c =>
          (c.conversation_type === '1on1' && c.peer_id === state.activePeerID) ||
          (c.conversation_type === 'group' && c.conversation_id === state.activePeerID)
        )
        .map(c => c.conversation_id)

      // Aggregate all messages from these conversations
      const allMessages: ChatMessage[] = []
      for (const convId of convIds) {
        const msgs = state.messages[convId] || []
        allMessages.push(...msgs)
      }

      // Sort by timestamp ascending
      return allMessages.sort((a, b) => a.timestamp - b.timestamp)
    },

    // Get the primary conversation ID for the active peer (most recent)
    primaryConversationID: (state): string | null => {
      if (!state.activePeerID) return null

      const convs = state.conversations
        .filter(c =>
          (c.conversation_type === '1on1' && c.peer_id === state.activePeerID) ||
          (c.conversation_type === 'group' && c.conversation_id === state.activePeerID)
        )
        .sort((a, b) => (b.last_message_at || 0) - (a.last_message_at || 0))

      return convs[0]?.conversation_id || null
    },

    getConversation: (state) => (conversationID: string) =>
      state.conversations.find(c => c.conversation_id === conversationID),

    getMessages: (state) => (conversationID: string) =>
      state.messages[conversationID] || [],

    getUnreadCount: (state) => (conversationID: string) => {
      const conversation = state.conversations.find(c => c.conversation_id === conversationID)
      return conversation?.unread_count || 0
    },

    totalConversations: (state) => state.conversations.length,

    unreadConversations: (state) =>
      state.conversations.filter(c => c.unread_count > 0),

    get1on1Conversations: (state) =>
      state.conversations.filter(c => c.conversation_type === '1on1'),

    groupConversations: (state) =>
      state.conversations.filter(c => c.conversation_type === 'group')
  },

  actions: {
    async fetchConversations(limit: number = 50, offset: number = 0) {
      this.loading = true
      this.error = null

      try {
        const response = await api.listConversations(limit, offset)
        this.conversations = response.conversations || []

        // Calculate total unread
        this.totalUnreadCount = this.conversations.reduce((sum, c) => sum + c.unread_count, 0)
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async createConversation(peerID: string) {
      this.loading = true
      this.error = null

      try {
        const conversation = await api.createConversation(peerID)

        // Add to list if not already present
        const exists = this.conversations.some(c => c.conversation_id === conversation.conversation_id)
        if (!exists) {
          this.conversations.unshift(conversation)
        }

        // Set as active conversation
        this.activeConversationID = conversation.conversation_id

        return conversation
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async deleteConversation(conversationID: string) {
      this.loading = true
      this.error = null

      try {
        await api.deleteConversation(conversationID)

        // Remove from list
        const index = this.conversations.findIndex(c => c.conversation_id === conversationID)
        if (index !== -1) {
          this.conversations.splice(index, 1)
        }

        // Remove messages
        delete this.messages[conversationID]

        // Clear active if this was active
        if (this.activeConversationID === conversationID) {
          this.setActiveConversation(null) // This will also clear localStorage
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async fetchMessages(conversationID: string, limit: number = 50, offset: number = 0) {
      this.error = null

      try {
        const response = await api.listMessages(conversationID, limit, offset)
        const messages = response.messages || []

        // Sort by timestamp ascending (oldest first)
        messages.sort((a, b) => a.timestamp - b.timestamp)

        // Store messages
        this.messages[conversationID] = messages
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async sendMessage(conversationID: string, content: string) {
      this.error = null

      // Get local peer ID for the optimistic message
      const localPeerID = localStorage.getItem('peer_id') || ''

      // Create optimistic message with temporary ID
      const tempID = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 9)}`
      const optimisticMessage: ChatMessage = {
        message_id: tempID,
        conversation_id: conversationID,
        sender_peer_id: localPeerID,
        content: content,
        status: 'created',
        timestamp: Math.floor(Date.now() / 1000),
        message_number: 0,
      }

      // Add message to local list immediately
      if (!this.messages[conversationID]) {
        this.messages[conversationID] = []
      }
      this.messages[conversationID].push(optimisticMessage)

      // Update conversation last_message_at immediately
      const conversation = this.conversations.find(c => c.conversation_id === conversationID)
      if (conversation) {
        conversation.last_message_at = optimisticMessage.timestamp
        conversation.last_message = optimisticMessage
      }

      // Send message in background (don't await in caller)
      try {
        const response = await api.sendMessage(conversationID, content)
        const message = response.message

        // Replace temporary message with real one
        const messages = this.messages[conversationID]
        if (messages) {
          const tempIndex = messages.findIndex(m => m.message_id === tempID)
          if (tempIndex !== -1) {
            messages[tempIndex] = message
          }
        }

        // Update conversation with real message
        if (conversation) {
          conversation.last_message_at = message.timestamp
          conversation.last_message = message
        }

        return message
      } catch (error: any) {
        // Update optimistic message to failed status
        const messages = this.messages[conversationID]
        if (messages) {
          const tempMessage = messages.find(m => m.message_id === tempID)
          if (tempMessage) {
            tempMessage.status = 'failed'
          }
        }

        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async markMessageAsRead(messageID: string) {
      try {
        await api.markMessageAsRead(messageID)

        // Update message status locally
        for (const conversationID in this.messages) {
          const message = this.messages[conversationID].find(m => m.message_id === messageID)
          if (message) {
            message.status = 'read'
            message.read_at = Date.now() / 1000
            break
          }
        }
      } catch (error: any) {
        console.error('Failed to mark message as read:', error)
      }
    },

    async markConversationAsRead(conversationID: string) {
      try {
        await api.markConversationAsRead(conversationID)

        // Update all messages in conversation to read
        if (this.messages[conversationID]) {
          const now = Date.now() / 1000
          this.messages[conversationID].forEach(msg => {
            if (msg.status !== 'read') {
              msg.status = 'read'
              msg.read_at = now
            }
          })
        }

        // Reset unread count
        const conversation = this.conversations.find(c => c.conversation_id === conversationID)
        if (conversation) {
          this.totalUnreadCount -= conversation.unread_count
          conversation.unread_count = 0
        }
      } catch (error: any) {
        console.error('Failed to mark conversation as read:', error)
      }
    },

    async createGroup(groupName: string, members: string[]) {
      this.loading = true
      this.error = null

      try {
        const conversation = await api.createGroup(groupName, members)

        // Add to list
        this.conversations.unshift(conversation)

        // Set as active
        this.activeConversationID = conversation.conversation_id

        return conversation
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async sendGroupMessage(groupID: string, content: string) {
      this.error = null

      // Get local peer ID for the optimistic message
      const localPeerID = localStorage.getItem('peer_id') || ''

      // Create optimistic message with temporary ID
      const tempID = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 9)}`
      const optimisticMessage: ChatMessage = {
        message_id: tempID,
        conversation_id: groupID,
        sender_peer_id: localPeerID,
        content: content,
        status: 'created',
        timestamp: Math.floor(Date.now() / 1000),
        message_number: 0,
      }

      // Add message to local list immediately
      if (!this.messages[groupID]) {
        this.messages[groupID] = []
      }
      this.messages[groupID].push(optimisticMessage)

      // Update conversation last_message_at immediately
      const conversation = this.conversations.find(c => c.conversation_id === groupID)
      if (conversation) {
        conversation.last_message_at = optimisticMessage.timestamp
        conversation.last_message = optimisticMessage
      }

      // Send message in background (don't await in caller)
      try {
        const response = await api.sendGroupMessage(groupID, content)

        // Replace temporary message with real one by fetching updated messages
        // Group messages come back with just message_id, so we need to fetch
        await this.fetchMessages(groupID)

        return response
      } catch (error: any) {
        // Update optimistic message to failed status
        const messages = this.messages[groupID]
        if (messages) {
          const tempMessage = messages.find(m => m.message_id === tempID)
          if (tempMessage) {
            tempMessage.status = 'failed'
          }
        }

        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async initiateKeyExchange(peerID: string) {
      try {
        await api.initiateKeyExchange(peerID)
      } catch (error: any) {
        console.error('Failed to initiate key exchange:', error)
        throw error
      }
    },

    async fetchUnreadCount() {
      try {
        const response = await api.getUnreadCount()
        this.totalUnreadCount = response.unread_count
      } catch (error: any) {
        console.error('Failed to fetch unread count:', error)
      }
    },

    setActiveConversation(conversationID: string | null) {
      this.activeConversationID = conversationID

      // Also set the peer ID based on the conversation
      if (conversationID) {
        const conversation = this.conversations.find(c => c.conversation_id === conversationID)
        if (conversation) {
          this.activePeerID = conversation.conversation_type === '1on1'
            ? conversation.peer_id || null
            : conversationID
        }
      } else {
        this.activePeerID = null
      }

      // Persist to localStorage
      if (conversationID) {
        localStorage.setItem('activeConversationID', conversationID)
      } else {
        localStorage.removeItem('activeConversationID')
      }

      // If switching to a conversation with messages, mark as read
      if (conversationID) {
        const conversation = this.conversations.find(c => c.conversation_id === conversationID)
        if (conversation && conversation.unread_count > 0) {
          this.markConversationAsRead(conversationID)
        }
      }
    },

    // Set active peer (for grouped view) and fetch all messages
    async setActivePeer(peerID: string | null) {
      this.activePeerID = peerID

      // Persist to localStorage
      if (peerID) {
        localStorage.setItem('activePeerID', peerID)
      } else {
        localStorage.removeItem('activePeerID')
      }

      if (!peerID) {
        this.activeConversationID = null
        return
      }

      // Find all conversations for this peer
      const convs = this.conversations.filter(c =>
        (c.conversation_type === '1on1' && c.peer_id === peerID) ||
        (c.conversation_type === 'group' && c.conversation_id === peerID)
      )

      // Set the primary (most recent) conversation as active
      if (convs.length > 0) {
        const sorted = [...convs].sort((a, b) => (b.last_message_at || 0) - (a.last_message_at || 0))
        this.activeConversationID = sorted[0].conversation_id
      }

      // Fetch messages for all conversations with this peer
      await this.fetchMessagesForPeer(peerID)

      // Mark all conversations as read
      for (const conv of convs) {
        if (conv.unread_count > 0) {
          this.markConversationAsRead(conv.conversation_id)
        }
      }
    },

    // Fetch messages for all conversations with a peer
    async fetchMessagesForPeer(peerID: string) {
      const convs = this.conversations.filter(c =>
        (c.conversation_type === '1on1' && c.peer_id === peerID) ||
        (c.conversation_type === 'group' && c.conversation_id === peerID)
      )

      // Fetch messages for each conversation
      for (const conv of convs) {
        try {
          await this.fetchMessages(conv.conversation_id)
        } catch (error: any) {
          console.error(`Failed to fetch messages for conversation ${conv.conversation_id}:`, error)
        }
      }
    },

    // Restore active peer from localStorage
    restoreActivePeer(): string | null {
      const savedPeerID = localStorage.getItem('activePeerID')
      if (savedPeerID) {
        // Find conversations for this peer
        const convs = this.conversations.filter(c =>
          (c.conversation_type === '1on1' && c.peer_id === savedPeerID) ||
          (c.conversation_type === 'group' && c.conversation_id === savedPeerID)
        )

        if (convs.length > 0) {
          this.activePeerID = savedPeerID

          // Also set the primary (most recent) conversation as active for the title
          const sorted = [...convs].sort((a, b) => (b.last_message_at || 0) - (a.last_message_at || 0))
          this.activeConversationID = sorted[0].conversation_id

          return savedPeerID
        } else {
          // Clean up stale reference
          localStorage.removeItem('activePeerID')
        }
      }
      return null
    },

    // Restore active conversation from localStorage (legacy support)
    restoreActiveConversation(): string | null {
      const savedConversationID = localStorage.getItem('activeConversationID')
      if (savedConversationID) {
        // Verify the conversation still exists
        const exists = this.conversations.some(c => c.conversation_id === savedConversationID)
        if (exists) {
          this.activeConversationID = savedConversationID
          // Also set the peer ID
          const conv = this.conversations.find(c => c.conversation_id === savedConversationID)
          if (conv) {
            this.activePeerID = conv.conversation_type === '1on1'
              ? conv.peer_id || null
              : savedConversationID
          }
          return savedConversationID
        } else {
          // Clean up stale reference
          localStorage.removeItem('activeConversationID')
        }
      }
      return null
    },

    // WebSocket subscriptions
    subscribeToUpdates() {
      if (_wsUnsubscribe) {
        return // Already subscribed
      }

      const ws = getWebSocketService()
      if (!ws) {
        console.warn('WebSocket service not available')
        return
      }

      // Subscribe to conversation updates
      const unsubConversations = ws.subscribe(MessageType.CHAT_CONVERSATIONS_UPDATED, (_payload: any) => {
        console.log('Conversations updated via WebSocket')
        this.fetchConversations()
      })

      const unsubConversationCreated = ws.subscribe(MessageType.CHAT_CONVERSATION_CREATED, (payload: any) => {
        console.log('Conversation created via WebSocket:', payload)
        const conversation = payload as ChatConversation
        const exists = this.conversations.some(c => c.conversation_id === conversation.conversation_id)
        if (!exists) {
          this.conversations.unshift(conversation)
        }
      })

      const unsubConversationUpdated = ws.subscribe(MessageType.CHAT_CONVERSATION_UPDATED, (payload: any) => {
        console.log('Conversation updated via WebSocket:', payload)
        const conversation = payload as ChatConversation
        const index = this.conversations.findIndex(c => c.conversation_id === conversation.conversation_id)
        if (index !== -1) {
          this.conversations[index] = conversation
        }
      })

      // Subscribe to message created (for multi-device sync)
      const unsubMessageCreated = ws.subscribe(MessageType.CHAT_MESSAGE_CREATED, (payload: any) => {
        console.log('Message created via WebSocket:', payload)
        const message = payload as ChatMessage

        // Add to messages list if not already present
        if (!this.messages[message.conversation_id]) {
          this.messages[message.conversation_id] = []
        }

        const exists = this.messages[message.conversation_id].some(m => m.message_id === message.message_id)
        if (!exists) {
          this.messages[message.conversation_id].push(message)
          this.messages[message.conversation_id].sort((a, b) => a.timestamp - b.timestamp)
        }
      })

      // Subscribe to message updates
      const unsubMessageReceived = ws.subscribe(MessageType.CHAT_MESSAGE_RECEIVED, (payload: any) => {
        console.log('Message received via WebSocket:', payload)
        const message = payload as ChatMessage

        // Add to messages list
        if (!this.messages[message.conversation_id]) {
          this.messages[message.conversation_id] = []
        }

        // Check if message already exists (avoid duplicates)
        const exists = this.messages[message.conversation_id].some(m => m.message_id === message.message_id)
        if (!exists) {
          this.messages[message.conversation_id].push(message)

          // Sort messages
          this.messages[message.conversation_id].sort((a, b) => a.timestamp - b.timestamp)
        }

        // Update conversation
        const conversation = this.conversations.find(c => c.conversation_id === message.conversation_id)
        if (conversation) {
          conversation.last_message_at = message.timestamp
          conversation.last_message = message

          // Increment unread if not active conversation
          if (this.activeConversationID !== message.conversation_id) {
            conversation.unread_count++
            this.totalUnreadCount++
          }
        }
      })

      const unsubMessageSent = ws.subscribe(MessageType.CHAT_MESSAGE_SENT, (payload: any) => {
        console.log('Message sent via WebSocket:', payload)
        const message = payload as ChatMessage

        // Update message status if it exists
        if (this.messages[message.conversation_id]) {
          const existing = this.messages[message.conversation_id].find(m => m.message_id === message.message_id)
          if (existing) {
            Object.assign(existing, message)
          }
        }
      })

      const unsubMessageDelivered = ws.subscribe(MessageType.CHAT_MESSAGE_DELIVERED, (payload: any) => {
        console.log('Message delivered via WebSocket:', payload)
        this.updateMessageStatus(payload.message_id, 'delivered', payload.timestamp)
      })

      const unsubMessageRead = ws.subscribe(MessageType.CHAT_MESSAGE_READ, (payload: any) => {
        console.log('Message read via WebSocket:', payload)
        this.updateMessageStatus(payload.message_id, 'read', payload.timestamp)
      })

      const unsubMessageFailed = ws.subscribe(MessageType.CHAT_MESSAGE_FAILED, (payload: any) => {
        console.log('Message failed via WebSocket:', payload)
        this.updateMessageStatus(payload.message_id, 'failed', payload.timestamp)
      })

      const unsubKeyExchangeComplete = ws.subscribe(MessageType.CHAT_KEY_EXCHANGE_COMPLETE, (payload: any) => {
        console.log('Key exchange complete via WebSocket:', payload)
        // Could show a notification or update UI state
      })

      // Store combined unsubscribe function
      _wsUnsubscribe = () => {
        unsubConversations()
        unsubConversationCreated()
        unsubConversationUpdated()
        unsubMessageCreated()
        unsubMessageReceived()
        unsubMessageSent()
        unsubMessageDelivered()
        unsubMessageRead()
        unsubMessageFailed()
        unsubKeyExchangeComplete()
      }
    },

    unsubscribeFromUpdates() {
      if (_wsUnsubscribe) {
        _wsUnsubscribe()
        _wsUnsubscribe = null
      }
    },

    // Helper to update message status
    updateMessageStatus(messageID: string, status: string, timestamp?: number) {
      for (const conversationID in this.messages) {
        const message = this.messages[conversationID].find(m => m.message_id === messageID)
        if (message) {
          message.status = status
          const ts = timestamp || Date.now() / 1000

          if (status === 'sent') message.sent_at = ts
          else if (status === 'delivered') message.delivered_at = ts
          else if (status === 'read') message.read_at = ts

          break
        }
      }
    }
  }
})
