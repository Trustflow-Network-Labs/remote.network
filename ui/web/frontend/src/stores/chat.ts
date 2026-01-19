import { defineStore } from 'pinia'
import { api, type ChatConversation, type ChatMessage } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface ChatState {
  conversations: ChatConversation[]
  messages: Record<string, ChatMessage[]> // conversationID -> messages[]
  activeConversationID: string | null
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
    loading: false,
    error: null,
    totalUnreadCount: 0
  }),

  getters: {
    activeConversation: (state) =>
      state.conversations.find(c => c.conversation_id === state.activeConversationID),

    activeMessages: (state) =>
      state.activeConversationID ? (state.messages[state.activeConversationID] || []) : [],

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

      try {
        const response = await api.sendMessage(conversationID, content)
        const message = response.message

        // Add message to local list
        if (!this.messages[conversationID]) {
          this.messages[conversationID] = []
        }
        this.messages[conversationID].push(message)

        // Update conversation last_message_at
        const conversation = this.conversations.find(c => c.conversation_id === conversationID)
        if (conversation) {
          conversation.last_message_at = message.timestamp
          conversation.last_message = message
        }

        return message
      } catch (error: any) {
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

      try {
        const response = await api.sendGroupMessage(groupID, content)

        // Refresh messages to get the sent message
        await this.fetchMessages(groupID)

        return response
      } catch (error: any) {
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

    // Restore active conversation from localStorage
    restoreActiveConversation(): string | null {
      const savedConversationID = localStorage.getItem('activeConversationID')
      if (savedConversationID) {
        // Verify the conversation still exists
        const exists = this.conversations.some(c => c.conversation_id === savedConversationID)
        if (exists) {
          this.activeConversationID = savedConversationID
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
