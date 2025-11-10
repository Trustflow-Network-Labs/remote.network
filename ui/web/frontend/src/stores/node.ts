import { defineStore } from 'pinia'
import { api } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'
import type { MessageHandler } from '../services/websocket'

export interface NodeStats {
  uptime: number
  knownPeers: number
  activeConnections?: number
  [key: string]: any
}

export interface NodeState {
  peerId: string | null
  dhtNodeId: string | null
  stats: NodeStats
  loading: boolean
  error: string | null
}

export const useNodeStore = defineStore('node', {
  state: (): NodeState => ({
    peerId: null,
    dhtNodeId: null,
    stats: {
      uptime: 0,
      knownPeers: 0
    },
    loading: false,
    error: null
  }),

  // Track unsubscribe function
  _wsUnsubscribe: null as (() => void) | null,

  getters: {
    isNodeReady: (state) => state.peerId !== null,
    uptimeFormatted: (state) => {
      const seconds = state.stats.uptime
      const hours = Math.floor(seconds / 3600)
      const minutes = Math.floor((seconds % 3600) / 60)
      const secs = seconds % 60
      return `${hours}h ${minutes}m ${secs}s`
    }
  },

  actions: {
    async fetchNodeStatus() {
      this.loading = true
      this.error = null

      try {
        const response = await api.getNodeStatus()
        this.peerId = response.peer_id
        this.dhtNodeId = response.dht_node_id
        this.stats = {
          uptime: response.uptime_seconds || 0,
          knownPeers: response.known_peers || 0,
          ...response.stats
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    updateStats(stats: Partial<NodeStats>) {
      this.stats = { ...this.stats, ...stats }
    },

    resetNode() {
      this.peerId = null
      this.dhtNodeId = null
      this.stats = {
        uptime: 0,
        knownPeers: 0
      }
      this.error = null
    },

    // Initialize WebSocket subscription
    initializeWebSocket() {
      const wsService = getWebSocketService()
      if (!wsService) {
        return
      }

      // Subscribe to node status updates
      const unsubscribe = wsService.subscribe(MessageType.NODE_STATUS, this.handleNodeStatusUpdate.bind(this))

      // Store unsubscribe function
      ;(this as any)._wsUnsubscribe = unsubscribe
    },

    // Cleanup WebSocket subscription
    cleanupWebSocket() {
      const unsubscribe = (this as any)._wsUnsubscribe
      if (unsubscribe) {
        unsubscribe()
        ;(this as any)._wsUnsubscribe = null
      }
    },

    // Handle WebSocket node status update
    handleNodeStatusUpdate(payload: any) {
      this.peerId = payload.peer_id
      this.dhtNodeId = payload.dht_node_id
      // Spread stats first to preserve all backend data, then override with clean extracted values
      this.stats = {
        ...payload.stats,
        uptime: payload.uptime_seconds || 0,
        knownPeers: payload.known_peers || 0
      }
      this.loading = false
      this.error = null
    }
  }
})
