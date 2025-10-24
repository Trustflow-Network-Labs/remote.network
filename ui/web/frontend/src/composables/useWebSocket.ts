import { computed, onMounted, onUnmounted } from 'vue'
import {
  getWebSocketService,
  initializeWebSocket,
  disconnectWebSocket,
  ConnectionState,
  MessageType
} from '../services/websocket'
import type { MessageHandler } from '../services/websocket'

// Composable for using WebSocket in components
export function useWebSocket() {
  const service = getWebSocketService()

  if (!service) {
    console.warn('[useWebSocket] WebSocket service not initialized')
  }

  // Reactive connection state
  const connectionState = computed(() => service?.connectionState.value || ConnectionState.DISCONNECTED)
  const lastError = computed(() => service?.lastError.value || null)
  const reconnecting = computed(() => service?.reconnecting.value || false)

  // Connection status helpers
  const isConnected = computed(() => connectionState.value === ConnectionState.CONNECTED)
  const isConnecting = computed(() => connectionState.value === ConnectionState.CONNECTING)
  const isDisconnected = computed(() => connectionState.value === ConnectionState.DISCONNECTED)
  const hasError = computed(() => connectionState.value === ConnectionState.ERROR)

  // Subscribe to message type
  function subscribe(type: MessageType, handler: MessageHandler): () => void {
    if (!service) {
      console.warn('[useWebSocket] Cannot subscribe: service not initialized')
      return () => {}
    }

    return service.subscribe(type, handler)
  }

  return {
    connectionState,
    lastError,
    reconnecting,
    isConnected,
    isConnecting,
    isDisconnected,
    hasError,
    subscribe,
    MessageType
  }
}

// Composable for initializing WebSocket connection
export function useWebSocketConnection(baseUrl: string, token: string | null) {
  let service: ReturnType<typeof getWebSocketService> = null

  onMounted(() => {
    if (token) {
      console.log('[useWebSocketConnection] Initializing WebSocket connection')
      service = initializeWebSocket(baseUrl, token)
    } else {
      console.warn('[useWebSocketConnection] No token available, skipping WebSocket connection')
    }
  })

  onUnmounted(() => {
    // Don't disconnect on component unmount - WebSocket should persist across route changes
    // Only disconnect when explicitly logging out
  })

  return {
    service,
    disconnect: disconnectWebSocket
  }
}
