import { ref } from 'vue'
import type { Ref } from 'vue'

// WebSocket connection states
export enum ConnectionState {
  CONNECTING = 'connecting',
  CONNECTED = 'connected',
  DISCONNECTED = 'disconnected',
  ERROR = 'error'
}

// Message types (matching backend)
export enum MessageType {
  // Data updates
  NODE_STATUS = 'node.status',
  PEERS_UPDATED = 'peers.updated',
  RELAY_SESSIONS = 'relay.sessions',
  RELAY_CANDIDATES = 'relay.candidates',
  SERVICES_UPDATED = 'services.updated',
  WORKFLOWS_UPDATED = 'workflows.updated',
  BLACKLIST_UPDATED = 'blacklist.updated',

  // File upload messages
  FILE_UPLOAD_START = 'file.upload.start',
  FILE_UPLOAD_CHUNK = 'file.upload.chunk',
  FILE_UPLOAD_PAUSE = 'file.upload.pause',
  FILE_UPLOAD_RESUME = 'file.upload.resume',
  FILE_UPLOAD_PROGRESS = 'file.upload.progress',
  FILE_UPLOAD_COMPLETE = 'file.upload.complete',
  FILE_UPLOAD_ERROR = 'file.upload.error',

  // Service discovery messages
  SERVICE_SEARCH_REQUEST = 'service.search.request',
  SERVICE_SEARCH_RESPONSE = 'service.search.response',

  // Docker operation messages
  DOCKER_PULL_PROGRESS = 'docker.pull.progress',
  DOCKER_BUILD_OUTPUT = 'docker.build.output',

  // Control messages
  PING = 'ping',
  PONG = 'pong',
  ERROR = 'error',
  CONNECTED = 'connected'
}

// WebSocket message structure
export interface WebSocketMessage {
  type: MessageType
  payload?: any
  timestamp: number
}

// Message handler type
export type MessageHandler = (payload: any) => void

// WebSocket service class
export class WebSocketService {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private maxReconnectAttempts = 10
  private reconnectDelay = 1000 // Start at 1 second
  private maxReconnectDelay = 30000 // Max 30 seconds
  private reconnectTimer: number | null = null
  private pingTimer: number | null = null
  private handlers: Map<MessageType, Set<MessageHandler>> = new Map()
  private baseUrl: string
  private token: string

  // Reactive state
  public connectionState: Ref<ConnectionState> = ref(ConnectionState.DISCONNECTED)
  public lastError: Ref<string | null> = ref(null)
  public reconnecting: Ref<boolean> = ref(false)

  constructor(baseUrl: string, token: string) {
    this.baseUrl = baseUrl
    this.token = token
  }

  // Connect to WebSocket server
  public connect(): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      return
    }

    if (this.ws && this.ws.readyState === WebSocket.CONNECTING) {
      return
    }

    this.connectionState.value = ConnectionState.CONNECTING
    this.lastError.value = null

    // Build WebSocket URL with token
    const wsUrl = this.baseUrl.replace('http://', 'ws://').replace('https://', 'wss://')
    const url = `${wsUrl}/api/ws?token=${encodeURIComponent(this.token)}`

    try {
      this.ws = new WebSocket(url)

      this.ws.onopen = this.onOpen.bind(this)
      this.ws.onmessage = this.onMessage.bind(this)
      this.ws.onerror = this.onError.bind(this)
      this.ws.onclose = this.onClose.bind(this)
    } catch (error) {
      console.error('[WebSocket] Connection error:', error)
      this.handleConnectionError(error as Error)
    }
  }

  // Disconnect from WebSocket server
  public disconnect(): void {

    // Clear reconnect timer
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }

    // Clear ping timer
    if (this.pingTimer) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }

    // Close WebSocket
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }

    this.connectionState.value = ConnectionState.DISCONNECTED
    this.reconnecting.value = false
    this.reconnectAttempts = 0
  }

  // Subscribe to message type
  public subscribe(type: MessageType, handler: MessageHandler): () => void {
    if (!this.handlers.has(type)) {
      this.handlers.set(type, new Set())
    }

    this.handlers.get(type)!.add(handler)

    // Return unsubscribe function
    return () => {
      const handlers = this.handlers.get(type)
      if (handlers) {
        handlers.delete(handler)
        if (handlers.size === 0) {
          this.handlers.delete(type)
        }
      }
    }
  }

  // Send message to server
  public send(message: Partial<WebSocketMessage>): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn('[WebSocket] Cannot send message: not connected')
      return
    }

    const fullMessage: WebSocketMessage = {
      type: message.type!,
      payload: message.payload,
      timestamp: Date.now()
    }

    this.ws.send(JSON.stringify(fullMessage))
  }

  // Handle WebSocket open event
  private onOpen(): void {
    this.connectionState.value = ConnectionState.CONNECTED
    this.reconnecting.value = false
    this.reconnectAttempts = 0
    this.reconnectDelay = 1000 // Reset delay

    // Start ping timer (every 30 seconds)
    this.pingTimer = window.setInterval(() => {
      this.send({ type: MessageType.PING })
    }, 30000)
  }

  // Handle WebSocket message event
  private onMessage(event: MessageEvent): void {
    try {
      const message: WebSocketMessage = JSON.parse(event.data)

      // Handle control messages
      if (message.type === MessageType.CONNECTED) {
        return
      }

      if (message.type === MessageType.PONG) {
        // Pong received, connection is alive
        return
      }

      if (message.type === MessageType.ERROR) {
        console.error('[WebSocket] Server error:', message.payload)
        this.lastError.value = message.payload?.error || 'Unknown server error'
        return
      }

      // Dispatch message to handlers
      const handlers = this.handlers.get(message.type)
      if (handlers) {
        handlers.forEach(handler => {
          try {
            handler(message.payload)
          } catch (error) {
            console.error(`[WebSocket] Handler error for ${message.type}:`, error)
          }
        })
      }
    } catch (error) {
      console.error('[WebSocket] Failed to parse message:', error)
    }
  }

  // Handle WebSocket error event
  private onError(event: Event): void {
    console.error('[WebSocket] Error:', event)
    this.connectionState.value = ConnectionState.ERROR
    this.lastError.value = 'WebSocket connection error'
  }

  // Handle WebSocket close event
  private onClose(event: CloseEvent): void {

    // Clear ping timer
    if (this.pingTimer) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }

    this.connectionState.value = ConnectionState.DISCONNECTED
    this.ws = null

    // Attempt reconnection with exponential backoff
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.scheduleReconnect()
    } else {
      console.error('[WebSocket] Max reconnect attempts reached')
      this.lastError.value = 'Failed to reconnect after multiple attempts'
      this.reconnecting.value = false
    }
  }

  // Handle connection error
  private handleConnectionError(error: Error): void {
    console.error('[WebSocket] Connection failed:', error)
    this.connectionState.value = ConnectionState.ERROR
    this.lastError.value = error.message

    // Schedule reconnect
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.scheduleReconnect()
    }
  }

  // Schedule reconnection with exponential backoff
  private scheduleReconnect(): void {
    this.reconnecting.value = true
    this.reconnectAttempts++

    // Calculate delay with exponential backoff
    const delay = Math.min(
      this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1),
      this.maxReconnectDelay
    )

    console.warn(
      `[WebSocket] Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`
    )

    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }

  // Computed properties for convenience
  public get isConnected(): boolean {
    return this.connectionState.value === ConnectionState.CONNECTED
  }

  public get isConnecting(): boolean {
    return this.connectionState.value === ConnectionState.CONNECTING
  }

  public get isDisconnected(): boolean {
    return this.connectionState.value === ConnectionState.DISCONNECTED
  }

  public get hasError(): boolean {
    return this.connectionState.value === ConnectionState.ERROR
  }
}

// Global WebSocket service instance
let wsService: WebSocketService | null = null

// Initialize WebSocket service
export function initializeWebSocket(baseUrl: string, token: string): WebSocketService {
  if (wsService) {
    wsService.disconnect()
  }

  wsService = new WebSocketService(baseUrl, token)
  wsService.connect()

  return wsService
}

// Get WebSocket service instance
export function getWebSocketService(): WebSocketService | null {
  return wsService
}

// Disconnect and cleanup
export function disconnectWebSocket(): void {
  if (wsService) {
    wsService.disconnect()
    wsService = null
  }
}
