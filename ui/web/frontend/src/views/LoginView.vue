<template>
  <div class="login-container">
    <div class="login-box">
      <h1>Remote Network</h1>
      <p class="subtitle">Connect to your node</p>

      <!-- Node Endpoint Selection -->
      <div class="node-endpoint">
        <label for="endpoint">Node Endpoint:</label>
        <div class="endpoint-input-group">
          <input
            id="endpoint"
            v-model="nodeEndpoint"
            type="text"
            placeholder="localhost:6060 or remote-ip:port"
            @blur="updateEndpoint"
            :disabled="loading"
          />
          <button class="test-button" @click="testConnection" :disabled="loading || testing">
            {{ testing ? 'Testing...' : 'Test' }}
          </button>
        </div>
        <p v-if="connectionStatus" :class="['connection-status', connectionStatus.type]">
          {{ connectionStatus.message }}
        </p>
      </div>

      <div class="auth-methods">
        <button class="auth-button primary" @click="connectEd25519" :disabled="loading">
          <span class="icon">🔑</span>
          <span>Sign with Private Key</span>
        </button>
      </div>

      <div v-if="error" class="error-message">{{ error }}</div>
      <div v-if="loading" class="loading-message">Connecting...</div>
    </div>

    <!-- Ed25519 file upload modal -->
    <div v-if="showEd25519Modal" class="modal">
      <div class="modal-content">
        <h2>Ed25519 Authentication</h2>
        <p>Select your Ed25519 private key file (ed25519_private.key)</p>
        <input type="file" @change="handleEd25519File" accept=".key" />
        <button @click="showEd25519Modal = false" class="close-button">Cancel</button>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useConnectionStore } from '../stores/connection'
import { authenticateEd25519, loadPrivateKeyFromFile } from '../services/auth/ed25519'

const router = useRouter()
const authStore = useAuthStore()
const connectionStore = useConnectionStore()

const loading = ref(false)
const testing = ref(false)
const error = ref<string | null>(null)
const showEd25519Modal = ref(false)
const nodeEndpoint = ref(connectionStore.nodeEndpoint)
const connectionStatus = ref<{ type: string; message: string } | null>(null)

onMounted(() => {
  // Test connection on mount
  testConnection()
})

function updateEndpoint() {
  connectionStore.setNodeEndpoint(nodeEndpoint.value)
  connectionStatus.value = null
}

async function testConnection() {
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

      // Redirect to dashboard
      await router.push('/dashboard')
      console.log('Redirected to dashboard')
    } else {
      console.error('Auth failed:', result.error)
      error.value = result.error || 'Authentication failed'
    }
  } catch (err: any) {
    console.error('Exception during auth:', err)
    error.value = err.message || 'Authentication failed'
  } finally {
    loading.value = false
    showEd25519Modal.value = false
  }
}

</script>

<style scoped>
.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: linear-gradient(135deg, #0a0a0a 0%, #1a1a2e 100%);
}

.login-box {
  background: #16213e;
  border-radius: 16px;
  padding: 48px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
  min-width: 400px;
}

h1 {
  font-size: 32px;
  color: #e94560;
  margin-bottom: 8px;
  text-align: center;
}

.subtitle {
  color: #a0a0a0;
  text-align: center;
  margin-bottom: 32px;
}

.auth-methods {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.auth-button {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 24px;
  border: none;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.3s ease;
}

.auth-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.auth-button.primary {
  background: #e94560;
  color: white;
}

.auth-button.primary:hover:not(:disabled) {
  background: #ff5575;
}


.icon {
  font-size: 24px;
}

.error-message {
  margin-top: 16px;
  padding: 12px;
  background: #ff5252;
  color: white;
  border-radius: 8px;
  text-align: center;
}

.loading-message {
  margin-top: 16px;
  text-align: center;
  color: #a0a0a0;
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
}

.modal-content {
  background: #16213e;
  padding: 32px;
  border-radius: 16px;
  min-width: 300px;
}

.modal-content h2 {
  color: #e94560;
  margin-bottom: 16px;
}

.modal-content p {
  color: #a0a0a0;
  margin-bottom: 20px;
}

.modal-content input[type="file"] {
  width: 100%;
  padding: 12px;
  margin-bottom: 16px;
  background: #0f3460;
  border: 1px solid #1a4d7f;
  border-radius: 8px;
  color: white;
}

.modal-content button {
  width: 100%;
  padding: 12px;
  background: #e94560;
  color: white;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  font-size: 16px;
}

.modal-content button:hover {
  background: #ff5575;
}

.node-endpoint {
  margin-bottom: 32px;
}

.node-endpoint label {
  display: block;
  color: #a0a0a0;
  margin-bottom: 8px;
  font-size: 14px;
}

.endpoint-input-group {
  display: flex;
  gap: 8px;
}

.endpoint-input-group input {
  flex: 1;
  padding: 12px 16px;
  background: #0f3460;
  border: 1px solid #1a4d7f;
  border-radius: 8px;
  color: white;
  font-size: 14px;
}

.endpoint-input-group input:focus {
  outline: none;
  border-color: #e94560;
}

.endpoint-input-group input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.test-button {
  padding: 12px 24px;
  background: #0f3460;
  border: 1px solid #1a4d7f;
  border-radius: 8px;
  color: white;
  cursor: pointer;
  font-size: 14px;
  transition: all 0.3s ease;
}

.test-button:hover:not(:disabled) {
  background: #1a5a8f;
}

.test-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.connection-status {
  margin-top: 8px;
  padding: 8px 12px;
  border-radius: 4px;
  font-size: 14px;
}

.connection-status.success {
  background: rgba(76, 175, 80, 0.2);
  color: #4caf50;
}

.connection-status.error {
  background: rgba(244, 67, 54, 0.2);
  color: #f44336;
}
</style>
