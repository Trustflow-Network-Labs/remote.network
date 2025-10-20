<template>
  <AppLayout>
    <div class="dashboard">
      <div class="dashboard-header">
        <h1>{{ $t('message.dashboard.title') }}</h1>
      </div>

    <div class="dashboard-grid">
      <!-- Node Status Card -->
      <Card class="status-card">
        <template #title>
          <div class="card-title">
            <i class="pi pi-server"></i>
            {{ $t('message.dashboard.nodeStatus') }}
          </div>
        </template>
        <template #content>
          <div v-if="nodeStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>
          <div v-else-if="nodeStore.error" class="error-message">
            <i class="pi pi-exclamation-triangle"></i>
            {{ nodeStore.error }}
          </div>
          <div v-else class="info-list">
            <div class="info-item">
              <span class="label">{{ $t('message.dashboard.peerId') }}:</span>
              <span class="value">{{ nodeStore.peerId || 'N/A' }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.dashboard.dhtNodeId') }}:</span>
              <span class="value">{{ nodeStore.dhtNodeId || 'N/A' }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.dashboard.uptime') }}:</span>
              <span class="value">{{ nodeStore.uptimeFormatted }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.dashboard.knownPeers') }}:</span>
              <span class="value">{{ peersStore.totalPeers }}</span>
            </div>
          </div>
        </template>
      </Card>

      <!-- Services Summary Card -->
      <Card class="status-card">
        <template #title>
          <div class="card-title">
            <i class="pi pi-box"></i>
            {{ $t('message.dashboard.activeServices') }}
          </div>
        </template>
        <template #content>
          <div v-if="servicesStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>
          <div v-else class="info-list">
            <div class="info-item">
              <span class="label">{{ $t('message.common.total') }}:</span>
              <span class="value">{{ servicesStore.totalServices }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.services.statuses.available') }}:</span>
              <span class="value">{{ servicesStore.availableServices.length }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.services.types.storage') }}:</span>
              <span class="value">{{ servicesStore.servicesByType('storage').length }}</span>
            </div>
          </div>
        </template>
      </Card>

      <!-- Peers Summary Card -->
      <Card class="status-card">
        <template #title>
          <div class="card-title">
            <i class="pi pi-users"></i>
            {{ $t('message.peers.title') }}
          </div>
        </template>
        <template #content>
          <div v-if="peersStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>
          <div v-else class="info-list">
            <div class="info-item">
              <span class="label">{{ $t('message.common.total') }}:</span>
              <span class="value">{{ peersStore.totalPeers }}</span>
            </div>
            <div class="info-item">
              <span class="label">{{ $t('message.peers.isRelay') }}:</span>
              <span class="value">{{ peersStore.relayPeers.length }}</span>
            </div>
            <div class="info-item">
              <span class="label">Blacklisted:</span>
              <span class="value">{{ peersStore.blacklist.length }}</span>
            </div>
          </div>
        </template>
      </Card>

      <!-- Workflows Summary Card -->
      <Card class="status-card">
        <template #title>
          <div class="card-title">
            <i class="pi pi-sitemap"></i>
            {{ $t('message.workflows.title') }}
          </div>
        </template>
        <template #content>
          <div v-if="workflowsStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>
          <div v-else class="info-list">
            <div class="info-item">
              <span class="label">{{ $t('message.common.total') }}:</span>
              <span class="value">{{ workflowsStore.totalWorkflows }}</span>
            </div>
            <div class="info-item">
              <span class="label">Active:</span>
              <span class="value">{{ workflowsStore.activeWorkflows.length }}</span>
            </div>
          </div>
        </template>
      </Card>
    </div>

    <!-- Quick Actions -->
    <Card class="actions-card mt-3">
      <template #title>Quick Actions</template>
      <template #content>
        <div class="actions-grid">
          <Button
            label="Create Workflow"
            icon="pi pi-plus"
            @click="router.push('/workflows')"
            class="p-button-outlined"
          />
          <Button
            label="Add Service"
            icon="pi pi-box"
            @click="router.push('/configuration')"
            class="p-button-outlined"
          />
          <Button
            label="View Peers"
            icon="pi pi-users"
            @click="router.push('/peers')"
            class="p-button-outlined"
          />
        </div>
      </template>
    </Card>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'

import AppLayout from '../layout/AppLayout.vue'
import { useNodeStore } from '../../stores/node'
import { usePeersStore } from '../../stores/peers'
import { useServicesStore } from '../../stores/services'
import { useWorkflowsStore } from '../../stores/workflows'

const router = useRouter()
const { t } = useI18n()
const nodeStore = useNodeStore()
const peersStore = usePeersStore()
const servicesStore = useServicesStore()
const workflowsStore = useWorkflowsStore()

let refreshInterval: number | null = null

async function loadDashboardData() {
  await Promise.all([
    nodeStore.fetchNodeStatus(),
    peersStore.fetchPeers(),
    peersStore.fetchBlacklist(),
    servicesStore.fetchServices(),
    workflowsStore.fetchWorkflows()
  ])
}

onMounted(async () => {
  await loadDashboardData()

  // Refresh data every 30 seconds
  refreshInterval = window.setInterval(() => {
    loadDashboardData()
  }, 30000)
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.dashboard {
  min-height: 100vh;
  padding: vars.$spacing-lg;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: vars.$spacing-xl;

  h1 {
    color: vars.$color-primary;
    font-size: vars.$font-size-xxl;
    margin: 0;
  }
}

.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: vars.$spacing-lg;
  margin-bottom: vars.$spacing-lg;
}

.status-card {
  .card-title {
    display: flex;
    align-items: center;
    gap: vars.$spacing-sm;
    color: vars.$color-primary;
    font-size: vars.$font-size-lg;

    i {
      font-size: vars.$font-size-xl;
    }
  }
}

.info-list {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-md;
}

.info-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: vars.$spacing-sm 0;
  border-bottom: 1px solid rgba(vars.$color-border, 0.3);

  &:last-child {
    border-bottom: none;
  }

  .label {
    color: vars.$color-text-secondary;
    font-size: vars.$font-size-sm;
  }

  .value {
    color: vars.$color-text;
    font-weight: 500;
    font-size: vars.$font-size-md;
    word-break: break-all;
    text-align: right;
    max-width: 60%;
  }
}

.loading {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: vars.$spacing-xl;
}

.error-message {
  color: vars.$color-error;
  display: flex;
  align-items: center;
  gap: vars.$spacing-sm;
  padding: vars.$spacing-md;

  i {
    font-size: vars.$font-size-lg;
  }
}

.actions-card {
  // Background handled by PrimeVue
}

.actions-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: vars.$spacing-md;
}
</style>
