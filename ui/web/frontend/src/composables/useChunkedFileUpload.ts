import { ref, computed } from 'vue'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface UploadProgress {
  sessionId: string
  chunksUploaded: number
  totalChunks: number
  bytesUploaded: number
  totalBytes: number
  percentage: number
  speed: number // bytes per second
  eta: number // seconds remaining
  status: 'idle' | 'uploading' | 'paused' | 'completed' | 'error'
}

export interface UploadOptions {
  file: File
  serviceId: number
  chunkSize?: number
  onProgress?: (progress: UploadProgress) => void
  onComplete?: (hash: string) => void
  onError?: (error: Error) => void
}

interface ChunkUploadState {
  startTime: number
  lastUpdateTime: number
  lastBytesUploaded: number
}

export function useChunkedFileUpload() {
  const uploadProgress = ref<UploadProgress | null>(null)
  const isUploading = computed(() => uploadProgress.value?.status === 'uploading')
  const isPaused = computed(() => uploadProgress.value?.status === 'paused')
  const isCompleted = computed(() => uploadProgress.value?.status === 'completed')
  const hasError = computed(() => uploadProgress.value?.status === 'error')

  let currentFile: File | null = null
  let currentChunkSize = 1024 * 1024 // 1MB default
  let currentSessionId: string | null = null
  let currentChunkIndex = 0
  let totalChunks = 0
  let uploadState: ChunkUploadState | null = null
  let progressCallback: ((progress: UploadProgress) => void) | null = null
  let completeCallback: ((hash: string) => void) | null = null
  let errorCallback: ((error: Error) => void) | null = null
  let wsUnsubscribe: (() => void) | null = null

  /**
   * Start uploading a file
   */
  const upload = async (options: UploadOptions): Promise<void> => {
    const { file, serviceId, chunkSize = 1024 * 1024, onProgress, onComplete, onError } = options

    // Reset state
    currentFile = file
    currentChunkSize = chunkSize
    currentChunkIndex = 0
    currentSessionId = null
    progressCallback = onProgress || null
    completeCallback = onComplete || null
    errorCallback = onError || null

    // Calculate total chunks
    totalChunks = Math.ceil(file.size / chunkSize)

    // Initialize progress
    uploadProgress.value = {
      sessionId: '',
      chunksUploaded: 0,
      totalChunks,
      bytesUploaded: 0,
      totalBytes: file.size,
      percentage: 0,
      speed: 0,
      eta: 0,
      status: 'uploading'
    }

    uploadState = {
      startTime: Date.now(),
      lastUpdateTime: Date.now(),
      lastBytesUploaded: 0
    }

    // Get WebSocket service
    const wsService = getWebSocketService()
    if (!wsService) {
      const error = new Error('WebSocket service not available')
      handleError(error)
      return
    }

    // Subscribe to WebSocket messages
    setupWebSocketListeners(wsService)

    // Send upload start message
    wsService.send({
      type: MessageType.FILE_UPLOAD_START,
      payload: {
        service_id: serviceId,
        filename: file.name,
        total_size: file.size,
        total_chunks: totalChunks,
        chunk_size: chunkSize
      }
    })

    // Note: Chunk upload will be triggered by the FILE_UPLOAD_PROGRESS message with session_id
  }

  /**
   * Setup WebSocket listeners for upload events
   */
  const setupWebSocketListeners = (wsService: any) => {
    // Clean up previous subscription
    if (wsUnsubscribe) {
      wsUnsubscribe()
    }

    // Subscribe to progress updates
    const unsubProgress = wsService.subscribe(MessageType.FILE_UPLOAD_PROGRESS, (payload: any) => {
      if (!uploadProgress.value) return

      currentSessionId = payload.session_id
      uploadProgress.value.sessionId = payload.session_id
      uploadProgress.value.chunksUploaded = payload.chunks_received
      uploadProgress.value.bytesUploaded = payload.bytes_uploaded
      uploadProgress.value.percentage = payload.percentage

      // Calculate speed and ETA
      if (uploadState) {
        const now = Date.now()
        const timeDelta = (now - uploadState.lastUpdateTime) / 1000 // seconds
        const bytesDelta = payload.bytes_uploaded - uploadState.lastBytesUploaded

        if (timeDelta > 0) {
          uploadProgress.value.speed = bytesDelta / timeDelta
          const remainingBytes = uploadProgress.value.totalBytes - uploadProgress.value.bytesUploaded
          uploadProgress.value.eta = uploadProgress.value.speed > 0 ? remainingBytes / uploadProgress.value.speed : 0

          uploadState.lastUpdateTime = now
          uploadState.lastBytesUploaded = payload.bytes_uploaded
        }
      }

      // Trigger callback
      if (progressCallback && uploadProgress.value) {
        progressCallback(uploadProgress.value)
      }

      // Upload next chunk if not completed
      if (payload.chunks_received < totalChunks && uploadProgress.value.status === 'uploading') {
        currentChunkIndex = payload.chunks_received
        uploadNextChunk()
      }
    })

    // Subscribe to completion
    const unsubComplete = wsService.subscribe(MessageType.FILE_UPLOAD_COMPLETE, (payload: any) => {
      if (!uploadProgress.value) return

      uploadProgress.value.status = 'completed'
      uploadProgress.value.percentage = 100

      if (completeCallback) {
        completeCallback(payload.file_hash)
      }

      cleanup()
    })

    // Subscribe to errors
    const unsubError = wsService.subscribe(MessageType.FILE_UPLOAD_ERROR, (payload: any) => {
      const error = new Error(payload.error || 'Upload failed')
      handleError(error)
    })

    // Combine unsubscribe functions
    wsUnsubscribe = () => {
      unsubProgress()
      unsubComplete()
      unsubError()
    }
  }

  /**
   * Upload the next chunk
   */
  const uploadNextChunk = async () => {
    if (!currentFile || currentChunkIndex >= totalChunks || !currentSessionId) {
      return
    }

    const wsService = getWebSocketService()
    if (!wsService) {
      handleError(new Error('WebSocket service not available'))
      return
    }

    // Calculate chunk boundaries
    const start = currentChunkIndex * currentChunkSize
    const end = Math.min(start + currentChunkSize, currentFile.size)
    const chunk = currentFile.slice(start, end)

    // Read chunk as base64
    const reader = new FileReader()
    reader.onload = () => {
      if (reader.result) {
        // Convert ArrayBuffer to base64
        const base64 = arrayBufferToBase64(reader.result as ArrayBuffer)

        // Send chunk
        wsService.send({
          type: MessageType.FILE_UPLOAD_CHUNK,
          payload: {
            session_id: currentSessionId,
            chunk_index: currentChunkIndex,
            data: base64
          }
        })
      }
    }

    reader.onerror = () => {
      handleError(new Error('Failed to read file chunk'))
    }

    reader.readAsArrayBuffer(chunk)
  }

  /**
   * Pause the upload
   */
  const pause = () => {
    if (!currentSessionId || !uploadProgress.value) return

    const wsService = getWebSocketService()
    if (!wsService) return

    wsService.send({
      type: MessageType.FILE_UPLOAD_PAUSE,
      payload: {
        session_id: currentSessionId
      }
    })

    uploadProgress.value.status = 'paused'
  }

  /**
   * Resume the upload
   */
  const resume = () => {
    if (!currentSessionId || !uploadProgress.value) return

    const wsService = getWebSocketService()
    if (!wsService) return

    wsService.send({
      type: MessageType.FILE_UPLOAD_RESUME,
      payload: {
        session_id: currentSessionId
      }
    })

    uploadProgress.value.status = 'uploading'
  }

  /**
   * Cancel the upload
   */
  const cancel = () => {
    if (uploadProgress.value) {
      uploadProgress.value.status = 'error'
    }
    cleanup()
  }

  /**
   * Handle upload error
   */
  const handleError = (error: Error) => {
    if (uploadProgress.value) {
      uploadProgress.value.status = 'error'
    }

    if (errorCallback) {
      errorCallback(error)
    }

    cleanup()
  }

  /**
   * Cleanup upload state
   */
  const cleanup = () => {
    if (wsUnsubscribe) {
      wsUnsubscribe()
      wsUnsubscribe = null
    }

    currentFile = null
    currentSessionId = null
    currentChunkIndex = 0
    totalChunks = 0
    uploadState = null
    progressCallback = null
    completeCallback = null
    errorCallback = null
  }

  /**
   * Convert ArrayBuffer to base64
   */
  const arrayBufferToBase64 = (buffer: ArrayBuffer): string => {
    let binary = ''
    const bytes = new Uint8Array(buffer)
    const len = bytes.byteLength
    for (let i = 0; i < len; i++) {
      binary += String.fromCharCode(bytes[i])
    }
    return btoa(binary)
  }

  /**
   * Format bytes to human-readable string
   */
  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return '0 Bytes'
    const k = 1024
    const sizes = ['Bytes', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i]
  }

  /**
   * Format seconds to human-readable time
   */
  const formatTime = (seconds: number): string => {
    if (seconds < 60) return `${Math.round(seconds)}s`
    if (seconds < 3600) return `${Math.round(seconds / 60)}m ${Math.round(seconds % 60)}s`
    return `${Math.round(seconds / 3600)}h ${Math.round((seconds % 3600) / 60)}m`
  }

  return {
    uploadProgress,
    isUploading,
    isPaused,
    isCompleted,
    hasError,
    upload,
    pause,
    resume,
    cancel,
    formatBytes,
    formatTime
  }
}
