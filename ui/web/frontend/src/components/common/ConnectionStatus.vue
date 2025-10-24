<template>
  <div class="connection-status" :class="statusClass" v-if="showIndicator">
    <i :class="iconClass"></i>
    <span>{{ statusText }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useWebSocket } from '../../composables/useWebSocket'
import { ConnectionState } from '../../services/websocket'

const { connectionState, reconnecting } = useWebSocket()

const statusClass = computed(() => {
  switch (connectionState.value) {
    case ConnectionState.CONNECTED:
      return 'status-connected'
    case ConnectionState.CONNECTING:
      return 'status-connecting'
    case ConnectionState.DISCONNECTED:
      return 'status-disconnected'
    case ConnectionState.ERROR:
      return 'status-error'
    default:
      return ''
  }
})

const iconClass = computed(() => {
  switch (connectionState.value) {
    case ConnectionState.CONNECTED:
      return 'pi pi-check-circle'
    case ConnectionState.CONNECTING:
      return 'pi pi-spin pi-spinner'
    case ConnectionState.DISCONNECTED:
      return 'pi pi-exclamation-circle'
    case ConnectionState.ERROR:
      return 'pi pi-times-circle'
    default:
      return 'pi pi-circle'
  }
})

const statusText = computed(() => {
  if (reconnecting.value) {
    return 'Reconnecting...'
  }

  switch (connectionState.value) {
    case ConnectionState.CONNECTED:
      return 'Connected'
    case ConnectionState.CONNECTING:
      return 'Connecting...'
    case ConnectionState.DISCONNECTED:
      return 'Disconnected'
    case ConnectionState.ERROR:
      return 'Connection Error'
    default:
      return ''
  }
})

const showIndicator = computed(() => {
  // Only show when not connected (don't clutter UI when everything is working)
  return connectionState.value !== ConnectionState.CONNECTED
})
</script>

<style scoped lang="scss">
.connection-status {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 1rem;
  border-radius: 4px;
  font-size: 0.875rem;
  font-weight: 500;

  i {
    font-size: 1rem;
  }

  &.status-connected {
    background-color: #d4edda;
    color: #155724;
  }

  &.status-connecting {
    background-color: #fff3cd;
    color: #856404;
  }

  &.status-disconnected {
    background-color: #f8d7da;
    color: #721c24;
  }

  &.status-error {
    background-color: #f8d7da;
    color: #721c24;
  }
}
</style>
