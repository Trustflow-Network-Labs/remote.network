<template>
  <div class="peers">
    <header class="peers-header">
      <h1>Peers</h1>
      <button @click="router.push('/dashboard')" class="back-button">Back to Dashboard</button>
    </header>

    <div class="peers-content">
      <div class="info-card">
        <h2>Known Peers</h2>
        <p>Peer list coming soon...</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()
</script>

<style scoped>
.peers {
  min-height: 100vh;
  background: #0a0a0a;
  padding: 24px;
}

.peers-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 32px;
}

h1 {
  color: #e94560;
  font-size: 32px;
}

.back-button {
  padding: 12px 24px;
  background: #0f3460;
  color: white;
  border: 1px solid #1a4d7f;
  border-radius: 8px;
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
}

.back-button:hover {
  background: #1a5a8f;
}

.peers-content {
  display: flex;
  flex-direction: column;
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
}
</style>
