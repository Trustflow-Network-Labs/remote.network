<template>
  <div class="login-container">
    <div class="login-box">
      <div class="logo-container">
        <img class="logo" alt="Remote Network logo" src="../../assets/images/logo.png" />
      </div>
      <h1>{{ $t('message.auth.welcome') }}</h1>
      <p class="subtitle">{{ $t('message.auth.selectPrivateKey') }}</p>

      <!-- Node Endpoint Selection -->
      <div class="node-endpoint">
        <label for="endpoint">{{ $t('message.auth.authMethod') }}:</label>
        <div class="endpoint-input-group">
          <input
            id="endpoint"
            v-model="inputEndpoint"
            type="text"
            placeholder="localhost:30069"
            :disabled="loading"
          />
          <button class="test-button" @click="testConnection" :disabled="loading || testing">
            {{ testing ? $t('message.common.loading') : 'Test' }}
          </button>
        </div>
        <p v-if="connectionStatus" :class="['connection-status', connectionStatus.type]">
          {{ connectionStatus.message }}
        </p>
      </div>

      <div class="auth-methods">
        <button class="auth-button primary" @click="connectEd25519" :disabled="loading">
          <span class="icon">🔑</span>
          <span>{{ $t('message.auth.ed25519Auth') }}</span>
        </button>
      </div>

      <div v-if="error" class="error-message">{{ error }}</div>
      <div v-if="loading" class="loading-message">{{ $t('message.auth.authenticating') }}</div>
    </div>

    <!-- Ed25519 file upload modal -->
    <div v-if="showEd25519Modal" class="modal">
      <div class="modal-content">
        <h2>{{ $t('message.auth.ed25519Auth') }}</h2>
        <p>{{ $t('message.auth.chooseKeyFile') }}</p>
        <input type="file" @change="handleEd25519File" accept=".key,.bin,.txt,.pem" />
        <button @click="showEd25519Modal = false" class="close-button">{{ $t('message.common.cancel') }}</button>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../../stores/auth'
import { useConnectionStore } from '../../stores/connection'
import { authenticateEd25519, loadPrivateKeyFromFile } from '../../services/auth/ed25519'
import { initializeWebSocket } from '../../services/websocket'

const router = useRouter()
const { t } = useI18n()
const authStore = useAuthStore()
const connectionStore = useConnectionStore()

const loading = ref(false)
const testing = ref(false)
const error = ref<string | null>(null)
const showEd25519Modal = ref(false)
const inputEndpoint = ref(connectionStore.nodeEndpoint)
const connectionStatus = ref<{ type: string; message: string } | null>(null)

onMounted(() => {
  // Test connection on mount
  testConnection()
})

async function testConnection() {
  // Update store with current input value
  connectionStore.setNodeEndpoint(inputEndpoint.value)

  testing.value = true
  connectionStatus.value = null
  error.value = null

  try {
    const success = await connectionStore.checkConnection()
    if (success) {
      connectionStatus.value = {
        type: 'success',
        message: '✓ Connected to node',
      }
    } else {
      connectionStatus.value = {
        type: 'error',
        message: '✗ Cannot reach node',
      }
    }
  } catch (err: any) {
    connectionStatus.value = {
      type: 'error',
      message: '✗ Connection failed',
    }
  } finally {
    testing.value = false
  }
}

async function connectEd25519() {
  // Update store with current input value before authenticating
  connectionStore.setNodeEndpoint(inputEndpoint.value)

  // Show file picker for private key
  showEd25519Modal.value = true
}

async function handleEd25519File(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]

  if (!file) return

  loading.value = true
  error.value = null

  try {
    // Load private key from file
    console.log('Loading private key from file...')
    const privateKeyHex = await loadPrivateKeyFromFile(file)
    console.log('Private key loaded, length:', privateKeyHex.length)

    // Authenticate with Ed25519
    console.log('Authenticating with Ed25519...')
    const result = await authenticateEd25519(privateKeyHex)
    console.log('Auth result:', result)

    if (result.success && result.token && result.peer_id) {
      console.log('Authentication successful, updating store and redirecting...')
      // Update auth store
      authStore.setAuth(result.token, '', result.peer_id, 'ed25519')

      // Initialize WebSocket connection
      initializeWebSocket(connectionStore.nodeEndpoint, result.token)
      console.log('WebSocket initialized')

      // Redirect to dashboard
      await router.push('/dashboard')
      console.log('Redirected to dashboard')
    } else {
      console.error('Auth failed:', result.error)
      error.value = result.error || t('message.auth.authenticationFailed')
    }
  } catch (err: any) {
    console.error('Exception during auth:', err)
    error.value = err.message || t('message.auth.authenticationFailed')
  } finally {
    loading.value = false
    showEd25519Modal.value = false
  }
}

</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: linear-gradient(135deg, vars.$color-background 0%, rgb(35, 48, 68) 100%);
}

.login-box {
  background: vars.$color-surface;
  border-radius: vars.$border-radius-lg;
  padding: 48px;
  box-shadow: vars.$shadow-lg;
  min-width: 400px;
  border: 1px solid vars.$color-border;
}

.logo-container {
  display: flex;
  justify-content: center;
  margin-bottom: 24px;

  .logo {
    width: 120px;
    height: auto;
  }
}

h1 {
  font-size: vars.$font-size-xxl;
  color: vars.$color-primary;
  margin-bottom: 8px;
  text-align: center;
}

.subtitle {
  color: vars.$color-text-secondary;
  text-align: center;
  margin-bottom: 32px;
}

.auth-methods {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-md;
}

.auth-button {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: vars.$spacing-md vars.$spacing-lg;
  border: none;
  border-radius: vars.$border-radius-md;
  font-size: vars.$font-size-md;
  font-weight: 500;
  cursor: pointer;
  transition: vars.$transition-normal;
}

.auth-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.auth-button.primary {
  background: vars.$color-primary;
  color: vars.$color-text;
}

.auth-button.primary:hover:not(:disabled) {
  background: vars.$color-primary-hover;
}

.icon {
  font-size: 24px;
}

.error-message {
  margin-top: vars.$spacing-md;
  padding: 12px;
  background: vars.$color-error;
  color: white;
  border-radius: vars.$border-radius-md;
  text-align: center;
}

.loading-message {
  margin-top: vars.$spacing-md;
  text-align: center;
  color: vars.$color-text-secondary;
}

.modal {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: vars.$z-index-modal;
}

.modal-content {
  background: vars.$color-surface;
  padding: 32px;
  border-radius: vars.$border-radius-lg;
  min-width: 300px;
  border: 1px solid vars.$color-border;
}

.modal-content h2 {
  color: vars.$color-primary;
  margin-bottom: vars.$spacing-md;
}

.modal-content p {
  color: vars.$color-text-secondary;
  margin-bottom: 20px;
}

.modal-content input[type="file"] {
  width: 100%;
  padding: 12px;
  margin-bottom: vars.$spacing-md;
  background: vars.$color-background;
  border: 1px solid vars.$color-border;
  border-radius: vars.$border-radius-md;
  color: vars.$color-text;
}

.modal-content button {
  width: 100%;
  padding: 12px;
  background: vars.$color-primary;
  color: vars.$color-text;
  border: none;
  border-radius: vars.$border-radius-md;
  cursor: pointer;
  font-size: vars.$font-size-md;
  transition: vars.$transition-normal;
}

.modal-content button:hover {
  background: vars.$color-primary-hover;
}

.node-endpoint {
  margin-bottom: 32px;
}

.node-endpoint label {
  display: block;
  color: vars.$color-text-secondary;
  margin-bottom: vars.$spacing-sm;
  font-size: vars.$font-size-sm;
}

.endpoint-input-group {
  display: flex;
  gap: vars.$spacing-sm;
}

.endpoint-input-group input {
  flex: 1;
  padding: 12px 16px;
  background: vars.$color-background;
  border: 1px solid vars.$color-border;
  border-radius: vars.$border-radius-md;
  color: vars.$color-text;
  font-size: vars.$font-size-sm;
}

.endpoint-input-group input:focus {
  outline: none;
  border-color: vars.$color-primary;
}

.endpoint-input-group input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.test-button {
  padding: 12px 24px;
  background: vars.$color-background;
  border: 1px solid vars.$color-border;
  border-radius: vars.$border-radius-md;
  color: vars.$color-text;
  cursor: pointer;
  font-size: vars.$font-size-sm;
  transition: vars.$transition-normal;
}

.test-button:hover:not(:disabled) {
  background: vars.$color-secondary;
  border-color: vars.$color-primary;
}

.test-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.connection-status {
  margin-top: vars.$spacing-sm;
  padding: vars.$spacing-sm 12px;
  border-radius: vars.$border-radius-sm;
  font-size: vars.$font-size-sm;
}

.connection-status.success {
  background: rgba(76, 175, 80, 0.2);
  color: vars.$color-success;
}

.connection-status.error {
  background: rgba(244, 67, 54, 0.2);
  color: vars.$color-error;
}
</style>
