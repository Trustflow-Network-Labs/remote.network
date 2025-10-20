import { defineStore } from 'pinia'
import { api } from '../services/api'

export interface Peer {
  peer_id: string
  dht_node_id: string
  is_relay: boolean
  is_store: boolean
  last_seen: string
  topic: string
  source: string
}

export interface PeersState {
  peers: Peer[]
  blacklist: string[]
  loading: boolean
  error: string | null
}

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
        console.error('Failed to fetch peers:', error)
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
        console.error('Failed to fetch blacklist:', error)
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
        console.error('Failed to add peer to blacklist:', error)
        throw error
      }
    },

    async removeFromBlacklist(peerId: string) {
      try {
        await api.removeFromBlacklist(peerId)
        this.blacklist = this.blacklist.filter(id => id !== peerId)
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to remove peer from blacklist:', error)
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
    }
  }
})
