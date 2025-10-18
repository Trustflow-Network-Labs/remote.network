import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export type AuthProvider = 'ed25519' | 'metamask' | 'keplr' | 'email'

export interface AuthState {
  token: string | null
  walletAddress: string | null
  peerId: string | null
  provider: AuthProvider | null
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem('auth_token'))
  const walletAddress = ref<string | null>(localStorage.getItem('wallet_address'))
  const peerId = ref<string | null>(localStorage.getItem('peer_id'))
  const provider = ref<AuthProvider | null>(localStorage.getItem('auth_provider') as AuthProvider | null)

  const isAuthenticated = computed(() => !!token.value)

  function setAuth(authToken: string, address: string, peerIdValue: string, authProvider: AuthProvider) {
    token.value = authToken
    walletAddress.value = address
    peerId.value = peerIdValue
    provider.value = authProvider

    localStorage.setItem('auth_token', authToken)
    localStorage.setItem('wallet_address', address)
    localStorage.setItem('peer_id', peerIdValue)
    localStorage.setItem('auth_provider', authProvider)
  }

  function clearAuth() {
    token.value = null
    walletAddress.value = null
    peerId.value = null
    provider.value = null

    localStorage.removeItem('auth_token')
    localStorage.removeItem('wallet_address')
    localStorage.removeItem('peer_id')
    localStorage.removeItem('auth_provider')
  }

  return {
    token,
    walletAddress,
    peerId,
    provider,
    isAuthenticated,
    setAuth,
    clearAuth
  }
})
