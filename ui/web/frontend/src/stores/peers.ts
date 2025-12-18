import { defineStore } from 'pinia'
import { api } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface Peer {
  peer_id: string
  dht_node_id: string
  is_relay: boolean
  is_store: boolean
  last_seen: string
  topic: string
  source: string
  // NAT and Relay status
  is_behind_nat: boolean
  nat_type: string
  using_relay: boolean
  connected_relay_id: string
}

export interface PeersState {
  peers: Peer[]
  blacklist: string[]
  loading: boolean
  error: string | null
}

// Track unsubscribe functions outside the store
let _wsUnsubscribePeers: (() => void) | null = null
let _wsUnsubscribeBlacklist: (() => void) | null = null

export const usePeersStore = defineStore('peers', {
  state: (): PeersState => ({
    peers: [],
    blacklist: [],
    loading: false,
    error: null
  }),

  getters: {
    totalPeers: (state) => state.peers.length,
    relayPeers: (state) => state.peers.filter(p => p.is_relay),
    storePeers: (state) => state.peers.filter(p => p.is_store),
    isBlacklisted: (state) => (peerId: string) => state.blacklist.includes(peerId),
    nonBlacklistedPeers: (state) => state.peers.filter(p => !state.blacklist.includes(p.peer_id))
  },

  actions: {
    async fetchPeers() {
      this.loading = true
      this.error = null

      try {
        const response = await api.getPeers()
        this.peers = response.peers || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async fetchBlacklist() {
      this.loading = true
      this.error = null

      try {
        const response = await api.getBlacklist()
        this.blacklist = response.blacklist || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async addToBlacklist(peerId: string) {
      try {
        await api.addToBlacklist(peerId)
        if (!this.blacklist.includes(peerId)) {
          this.blacklist.push(peerId)
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async removeFromBlacklist(peerId: string) {
      try {
        await api.removeFromBlacklist(peerId)
        this.blacklist = this.blacklist.filter(id => id !== peerId)
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    updatePeer(peer: Peer) {
      const index = this.peers.findIndex(p => p.peer_id === peer.peer_id)
      if (index !== -1) {
        this.peers[index] = peer
      } else {
        this.peers.push(peer)
      }
    },

    removePeer(peerId: string) {
      this.peers = this.peers.filter(p => p.peer_id !== peerId)
    },

    // Initialize WebSocket subscriptions
    initializeWebSocket() {
      const wsService = getWebSocketService()
      if (!wsService) {
        return
      }

      // Subscribe to peers updates
      const unsubscribePeers = wsService.subscribe(
        MessageType.PEERS_UPDATED,
        this.handlePeersUpdate.bind(this)
      )

      // Subscribe to blacklist updates
      const unsubscribeBlacklist = wsService.subscribe(
        MessageType.BLACKLIST_UPDATED,
        this.handleBlacklistUpdate.bind(this)
      )

      // Store unsubscribe functions
      _wsUnsubscribePeers = unsubscribePeers
      _wsUnsubscribeBlacklist = unsubscribeBlacklist
    },

    // Cleanup WebSocket subscriptions
    cleanupWebSocket() {
      if (_wsUnsubscribePeers) {
        _wsUnsubscribePeers()
        _wsUnsubscribePeers = null
      }

      if (_wsUnsubscribeBlacklist) {
        _wsUnsubscribeBlacklist()
        _wsUnsubscribeBlacklist = null
      }
    },

    // Handle WebSocket peers update
    handlePeersUpdate(payload: any) {
      if (payload && payload.peers) {
        this.peers = payload.peers.map((peer: any) => ({
          peer_id: peer.peer_id || peer.id,
          dht_node_id: peer.dht_node_id ?? '',
          is_relay: peer.is_relay ?? false,
          is_store: peer.is_store ?? false,
          last_seen: new Date(peer.last_seen * 1000).toISOString(),
          topic: peer.topic ?? '',
          source: peer.source ?? '',
          is_behind_nat: peer.is_behind_nat ?? false,
          nat_type: peer.nat_type ?? '',
          using_relay: peer.using_relay ?? false,
          connected_relay_id: peer.connected_relay_id ?? ''
        }))
        this.loading = false
        this.error = null
      }
    },

    // Handle WebSocket blacklist update
    handleBlacklistUpdate(payload: any) {
      if (payload && payload.blacklist) {
        this.blacklist = payload.blacklist.map((entry: any) => entry.peer_id)
        this.loading = false
        this.error = null
      }
    }
  }
})
