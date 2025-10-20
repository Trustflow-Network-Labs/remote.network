import { defineStore } from 'pinia'
import { api } from '../services/api'

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
          uptime: response.uptime || 0,
          knownPeers: response.known_peers || 0,
          ...response.stats
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to fetch node status:', error)
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
    }
  }
})
