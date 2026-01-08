import { defineStore } from 'pinia'
import { api, type Wallet } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface WalletsState {
  wallets: Wallet[]
  loading: boolean
  error: string | null
  balanceLoading: Map<string, boolean>
}

// Track unsubscribe functions outside the store
let _wsUnsubscribe: (() => void) | null = null

export const useWalletsStore = defineStore('wallets', {
  state: (): WalletsState => ({
    wallets: [],
    loading: false,
    error: null,
    balanceLoading: new Map()
  }),

  getters: {
    totalWallets: (state) => state.wallets.length,
    walletsByNetwork: (state) => (network: string) =>
      state.wallets.filter(w => w.network === network),
    getWallet: (state) => (walletId: string) =>
      state.wallets.find(w => w.wallet_id === walletId),
    networks: (state) => {
      const nets = new Set(state.wallets.map(w => w.network))
      return Array.from(nets)
    }
  },

  actions: {
    async fetchWallets(includeBalance: boolean = false) {
      this.loading = true
      this.error = null

      try {
        const response = await api.listWallets(includeBalance)
        this.wallets = response.wallets || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async createWallet(network: string, passphrase: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.createWallet(network, passphrase)

        // Refresh wallets list to get is_default flag from server
        await this.fetchWallets()

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async importWallet(privateKey: string, network: string, passphrase: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.importWallet(privateKey, network, passphrase)

        // Refresh wallets list to get is_default flag from server
        await this.fetchWallets()

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async deleteWallet(walletId: string, passphrase: string) {
      this.loading = true
      this.error = null

      try {
        await api.deleteWallet(walletId, passphrase)

        // Remove wallet from list
        const index = this.wallets.findIndex(w => w.wallet_id === walletId)
        if (index !== -1) {
          this.wallets.splice(index, 1)
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async refreshBalance(walletId: string) {
      this.balanceLoading.set(walletId, true)
      this.error = null

      try {
        const balance = await api.getWalletBalance(walletId)

        // Update wallet with balance
        const wallet = this.wallets.find(w => w.wallet_id === walletId)
        if (wallet) {
          wallet.balance = balance
        }

        return balance
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.balanceLoading.set(walletId, false)
      }
    },

    async exportWallet(walletId: string, passphrase: string) {
      this.error = null

      try {
        const response = await api.exportWallet(walletId, passphrase)
        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async setDefaultWallet(walletId: string) {
      this.error = null

      try {
        await api.setDefaultWallet(walletId)

        // Update all wallets' is_default flag
        this.wallets = this.wallets.map(w => ({
          ...w,
          is_default: w.wallet_id === walletId
        }))

        return true
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
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

      // Subscribe to wallet updates
      const unsubWallets = ws.subscribe(MessageType.WALLETS_UPDATED, (_payload: any) => {
        console.log('Wallets updated via WebSocket')
        // Refresh wallets list
        this.fetchWallets()
      })

      const unsubCreated = ws.subscribe(MessageType.WALLET_CREATED, (payload: any) => {
        console.log('Wallet created via WebSocket:', payload)
        this.fetchWallets()
      })

      const unsubDeleted = ws.subscribe(MessageType.WALLET_DELETED, (payload: any) => {
        console.log('Wallet deleted via WebSocket:', payload)
        this.fetchWallets()
      })

      const unsubBalance = ws.subscribe(MessageType.WALLET_BALANCE_UPDATE, (payload: any) => {
        console.log('Wallet balance updated via WebSocket:', payload)
        const { wallet_id } = payload
        if (wallet_id) {
          this.refreshBalance(wallet_id)
        }
      })

      // Store combined unsubscribe function
      _wsUnsubscribe = () => {
        unsubWallets()
        unsubCreated()
        unsubDeleted()
        unsubBalance()
      }
    },

    unsubscribeFromUpdates() {
      if (_wsUnsubscribe) {
        _wsUnsubscribe()
        _wsUnsubscribe = null
      }
    }
  }
})
