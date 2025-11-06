import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../services/api'

export const useConnectionStore = defineStore('connection', () => {
  // State
  const nodeEndpoint = ref<string>(
    localStorage.getItem('node_endpoint') || 'http://localhost:30069'
  )
  const connected = ref<boolean>(false)
  const checking = ref<boolean>(false)

  // Computed
  const isLocal = computed(() => {
    return nodeEndpoint.value.includes('localhost') || nodeEndpoint.value.includes('127.0.0.1')
  })

  // Actions
  function setNodeEndpoint(endpoint: string) {
    // Normalize endpoint (ensure http:// prefix)
    if (!endpoint.startsWith('http://') && !endpoint.startsWith('https://')) {
      endpoint = 'http://' + endpoint
    }

    nodeEndpoint.value = endpoint
    localStorage.setItem('node_endpoint', endpoint)

    // Update API client base URL
    api.setBaseURL(endpoint)

    // Reset connection status
    connected.value = false
  }

  async function checkConnection(): Promise<boolean> {
    checking.value = true
    try {
      await api.health()
      connected.value = true
      return true
    } catch (error) {
      connected.value = false
      return false
    } finally {
      checking.value = false
    }
  }

  function reset() {
    nodeEndpoint.value = 'http://localhost:30069'
    connected.value = false
    localStorage.removeItem('node_endpoint')
    api.setBaseURL('http://localhost:30069')
  }

  return {
    nodeEndpoint,
    connected,
    checking,
    isLocal,
    setNodeEndpoint,
    checkConnection,
    reset,
  }
})
