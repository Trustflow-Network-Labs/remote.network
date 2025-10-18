<template>
  <div class="dashboard">
    <header class="dashboard-header">
      <h1>Dashboard</h1>
      <button @click="logout" class="logout-button">Logout</button>
    </header>

    <div class="dashboard-content">
      <div class="info-card">
        <h2>Connected</h2>
        <p>Peer ID: {{ authStore.peerId }}</p>
        <p>Wallet: {{ authStore.walletAddress }}</p>
        <p>Provider: {{ authStore.provider }}</p>
      </div>

      <div class="info-card">
        <h2>Node Status</h2>
        <p>Coming soon...</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = useRouter()
const authStore = useAuthStore()

function logout() {
  authStore.clearAuth()
  router.push('/login')
}
</script>

<style scoped>
.dashboard {
  min-height: 100vh;
  background: #0a0a0a;
  padding: 24px;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 32px;
}

h1 {
  color: #e94560;
  font-size: 32px;
}

.logout-button {
  padding: 12px 24px;
  background: #e94560;
  color: white;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
}

.logout-button:hover {
  background: #ff5575;
}

.dashboard-content {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 24px;
}

.info-card {
  background: #16213e;
  border-radius: 16px;
  padding: 24px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.2);
}

.info-card h2 {
  color: #e94560;
  font-size: 20px;
  margin-bottom: 16px;
}

.info-card p {
  color: #a0a0a0;
  margin-bottom: 8px;
  word-break: break-all;
}
</style>
