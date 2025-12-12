<template>
  <AppLayout>
    <main class="dashboard">
      <!-- Top Controls -->
      <div class="dashboard-controls">
        <div class="dashboard-controls-buttons">
          <div class="input-box">
            <button class="btn" @click="router.push('/workflows/create')">
              <i class="pi pi-plus-circle"></i> {{ $t('message.workflows.create') }}
            </button>
          </div>
          <div class="input-box">
            <button class="btn" @click="router.push('/configuration')">
              <i class="pi pi-plus-circle"></i> {{ $t('message.dashboard.addLocalService') }}
            </button>
          </div>
          <div class="input-box">
            <button class="btn light" @click="searchRemoteServices">
              <i class="pi pi-search"></i> {{ $t('message.dashboard.searchRemoteService') }}
            </button>
          </div>
        </div>
      </div>

      <!-- Quick Link Boxes -->
      <div class="quick-links-section">
        <div class="quick-links-title">
          <i class="pi pi-th-large"></i> {{ $t('message.dashboard.quickLinks') }}
        </div>
        <div class="quick-links-list">
          <div class="quick-link-box" @click="router.push('/configuration#services')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="servicesStore.availableServices.length" severity="contrast" size="small">
                  <Avatar icon="pi pi-box" size="large"
                    style="background-color: var(--color-active-services); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.activeServices') }}
              </div>
            </div>
          </div>

          <div class="quick-link-box" @click="router.push('/configuration#services')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="servicesStore.servicesByType('storage').length" severity="contrast" size="small">
                  <Avatar icon="pi pi-database" size="large"
                    style="background-color: var(--color-sold-services); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.soldServices') }}
              </div>
            </div>
          </div>

          <div class="quick-link-box" @click="router.push('/configuration#services')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="servicesStore.servicesByType('storage').length" severity="contrast" size="small">
                  <Avatar icon="pi pi-database" size="large"
                    style="background-color: var(--color-purchased-services); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.purchasedServices') }}
              </div>
            </div>
          </div>

          <div class="quick-link-box" @click="router.push('/workflows')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="workflowsStore.activeWorkflows.length" severity="contrast" size="small">
                  <Avatar icon="pi pi-play" size="large"
                    style="background-color: var(--color-running-workflows); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.runningWorkflows') }}
              </div>
            </div>
          </div>

          <div class="quick-link-box" @click="router.push('/workflows')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="workflowsStore.totalWorkflows - workflowsStore.activeWorkflows.length" severity="contrast" size="small">
                  <Avatar icon="pi pi-objects-column" size="large"
                    style="background-color: var(--color-design-workflows); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.workflowsInDesign') }}
              </div>
            </div>
          </div>

          <div class="quick-link-box" @click="router.push('/peers')">
            <div class="quick-link-box-content">
              <div class="quick-link-box-content-icon">
                <OverlayBadge :value="peersStore.totalPeers" severity="contrast" size="small">
                  <Avatar icon="pi pi-users" size="large"
                    style="background-color: var(--color-known-peers); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="quick-link-box-content-details">
                {{ $t('message.dashboard.knownPeers') }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Node Stats Section -->
      <div class="node-stats-section">
        <div class="node-stats-title">
          <i class="pi pi-server"></i> {{ $t('message.dashboard.nodeStatus') }}
        </div>
        <div v-if="nodeStore.loading" class="loading">
          <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
        </div>
        <div v-else-if="nodeStore.error" class="error-message">
          <i class="pi pi-exclamation-triangle"></i>
          {{ nodeStore.error }}
        </div>
        <div v-else class="node-stats-grid">
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.peerId') }}:</span>
            <div class="stat-value-with-copy">
              <span class="stat-value">{{ shortenedPeerId }}</span>
              <i class="pi pi-copy copy-icon" @click="copyPeerId" :title="$t('message.common.copy')"></i>
            </div>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.dhtNodeId') }}:</span>
            <div class="stat-value-with-copy">
              <span class="stat-value">{{ shortenedDhtNodeId }}</span>
              <i class="pi pi-copy copy-icon" @click="copyDhtNodeId" :title="$t('message.common.copy')"></i>
            </div>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.nodeType') }}:</span>
            <span class="stat-value">{{ nodeType }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.publicEndpoint') }}:</span>
            <span class="stat-value">{{ publicEndpoint }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.privateEndpoint') }}:</span>
            <span class="stat-value">{{ privateEndpoint }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.isRelay') }}:</span>
            <span class="stat-value">{{ isRelay ? 'Yes' : 'No' }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.isStore') }}:</span>
            <span class="stat-value">{{ isStore ? 'Yes' : 'No' }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.uptime') }}:</span>
            <span class="stat-value">{{ uptimeFormatted }}</span>
          </div>
          <div class="stat-item stat-item-action">
            <button
              class="btn restart-btn"
              :class="{ disabled: restarting }"
              :disabled="restarting"
              @click="restartPeer"
            >
              <i :class="restarting ? 'pi pi-spin pi-spinner' : 'pi pi-refresh'"></i>
              {{ restarting ? $t('message.common.loading') : $t('message.navigation.restartPeer') }}
            </button>
          </div>
        </div>
      </div>

      <!-- System Capabilities Section -->
      <div class="node-stats-section">
        <div class="node-stats-title">
          <i class="pi pi-microchip-ai"></i> System Capabilities
        </div>
        <div v-if="capabilitiesLoading" class="loading">
          <ProgressSpinner style="width:40px;height:40px" strokeWidth="4" />
        </div>
        <div v-else-if="systemCapabilities" class="capabilities-grid">
          <!-- System Info -->
          <div class="capability-group">
            <div class="capability-group-title">System</div>
            <div class="capability-item">
              <span class="cap-label">Platform</span>
              <span class="cap-value">{{ systemCapabilities.platform }} / {{ systemCapabilities.architecture }}</span>
            </div>
          </div>

          <!-- CPU -->
          <div class="capability-group">
            <div class="capability-group-title">CPU</div>
            <div class="capability-item">
              <span class="cap-label">Model</span>
              <span class="cap-value">{{ systemCapabilities.cpu_model }}</span>
            </div>
            <div class="capability-item">
              <span class="cap-label">Cores/Threads</span>
              <span class="cap-value">{{ systemCapabilities.cpu_cores }} / {{ systemCapabilities.cpu_threads }}</span>
            </div>
          </div>

          <!-- Memory -->
          <div class="capability-group">
            <div class="capability-group-title">Memory</div>
            <div class="capability-item">
              <span class="cap-label">Total</span>
              <span class="cap-value">{{ formatMemory(systemCapabilities.total_memory_mb) }}</span>
            </div>
            <div class="capability-item">
              <span class="cap-label">Available</span>
              <span class="cap-value">{{ formatMemory(systemCapabilities.available_memory_mb) }}</span>
            </div>
          </div>

          <!-- Disk -->
          <div class="capability-group">
            <div class="capability-group-title">Disk</div>
            <div class="capability-item">
              <span class="cap-label">Total</span>
              <span class="cap-value">{{ formatDisk(systemCapabilities.total_disk_mb) }}</span>
            </div>
            <div class="capability-item">
              <span class="cap-label">Available</span>
              <span class="cap-value">{{ formatDisk(systemCapabilities.available_disk_mb) }}</span>
            </div>
          </div>

          <!-- GPU -->
          <div class="capability-group">
            <div class="capability-group-title">GPU</div>
            <div v-if="systemCapabilities.gpus && systemCapabilities.gpus.length > 0">
              <div v-for="(gpu, index) in systemCapabilities.gpus" :key="index" class="capability-item">
                <span class="cap-label gpu-vendor" :class="gpu.vendor">{{ gpu.vendor.toUpperCase() }}</span>
                <span class="cap-value">{{ gpu.name }} ({{ formatMemory(gpu.memory_mb) }})</span>
              </div>
            </div>
            <div v-else class="capability-item">
              <span class="cap-value no-gpu">No GPU detected</span>
            </div>
          </div>

          <!-- Software -->
          <div class="capability-group">
            <div class="capability-group-title">Software</div>
            <div class="capability-item">
              <span class="cap-label">Docker</span>
              <span class="cap-value" :class="{ available: systemCapabilities.has_docker }">
                {{ systemCapabilities.has_docker ? (systemCapabilities.docker_version || 'Yes') : 'No' }}
              </span>
            </div>
            <div class="capability-item">
              <span class="cap-label">Python</span>
              <span class="cap-value" :class="{ available: systemCapabilities.has_python }">
                {{ systemCapabilities.has_python ? (systemCapabilities.python_version || 'Yes') : 'No' }}
              </span>
            </div>
          </div>
        </div>
        <div v-else class="no-capabilities">
          <p>Unable to load system capabilities</p>
        </div>
      </div>

      <!-- Relay Info Section -->
      <RelayInfoSection
        v-if="showRelaySection"
        :is-relay-mode="isRelay"
        :relay-stats="nodeStore.stats"
      />
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import ProgressSpinner from 'primevue/progressspinner'
import Avatar from 'primevue/avatar'
import OverlayBadge from 'primevue/overlaybadge'

import AppLayout from '../layout/AppLayout.vue'
import RelayInfoSection from './RelayInfoSection.vue'
import { useAuthStore } from '../../stores/auth'
import { useNodeStore } from '../../stores/node'
import { usePeersStore } from '../../stores/peers'
import { useServicesStore } from '../../stores/services'
import { useWorkflowsStore } from '../../stores/workflows'
import { api, type SystemCapabilities } from '../../services/api'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'
import { disconnectWebSocket } from '../../services/websocket'

const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const authStore = useAuthStore()
const nodeStore = useNodeStore()
const peersStore = usePeersStore()
const servicesStore = useServicesStore()
const workflowsStore = useWorkflowsStore()
const restarting = ref(false)

// System capabilities state
const capabilitiesLoading = ref(false)
const systemCapabilities = ref<SystemCapabilities | null>(null)
const { copyToClipboard } = useClipboard()
const { shorten } = useTextUtils()

const nodeType = computed(() => {
  const stats = nodeStore.stats
  if (stats.nat_type) {
    return stats.nat_type
  }
  return 'Unknown'
})

const publicEndpoint = computed(() => {
  const stats = nodeStore.stats
  if (stats.public_endpoint) {
    return stats.public_endpoint
  }
  return 'N/A'
})

const privateEndpoint = computed(() => {
  const stats = nodeStore.stats
  const topology = stats.topology
  if (topology && topology.local_subnet) {
    // Extract IP from subnet (e.g., "192.168.3.108/24" -> "192.168.3.108")
    const ip = topology.local_subnet.split('/')[0]
    // Get QUIC port from stats (default to 4433 if not available)
    const port = stats.quic_port || 4433
    return `${ip}:${port}`
  }
  return 'N/A'
})

const isRelay = computed(() => {
  return nodeStore.stats.relay_mode === true
})

const isStore = computed(() => {
  // Check if BEP_44 store is enabled from stats
  return nodeStore.stats.enable_bep44_store !== false
})

const shortenedPeerId = computed(() => {
  return shorten(nodeStore.peerId || '', 6, 6)
})

const shortenedDhtNodeId = computed(() => {
  return shorten(nodeStore.dhtNodeId || '', 6, 6)
})

const uptimeFormatted = computed(() => {
  const seconds = nodeStore.stats.uptime || 0
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)
  return `${hours}h ${minutes}m ${secs}s`
})

const showRelaySection = computed(() => {
  // Show relay section for relay mode OR any NAT node (private node type)
  // All NAT nodes (Port Restricted, Symmetric, etc.) have relay managers and can use relays
  return nodeStore.stats.relay_mode === true || nodeStore.stats.node_type === 'private'
})

async function copyPeerId() {
  const success = await copyToClipboard(nodeStore.peerId || '')
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

async function copyDhtNodeId() {
  const success = await copyToClipboard(nodeStore.dhtNodeId || '')
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

function searchRemoteServices() {
  // TODO: Implement remote service search
  toast.add({
    severity: 'info',
    summary: t('message.common.info'),
    detail: 'Remote service search coming soon',
    life: 3000
  })
}

async function loadDashboardData() {
  await Promise.all([
    nodeStore.fetchNodeStatus(),
    peersStore.fetchPeers(),
    peersStore.fetchBlacklist(),
    servicesStore.fetchServices(),
    workflowsStore.fetchWorkflows(),
    loadSystemCapabilities()
  ])
}

async function loadSystemCapabilities() {
  capabilitiesLoading.value = true
  try {
    const response = await api.getNodeCapabilities()
    systemCapabilities.value = response.system || null
  } catch (error) {
    console.error('Failed to load system capabilities:', error)
    systemCapabilities.value = null
  } finally {
    capabilitiesLoading.value = false
  }
}

function formatMemory(mb: number): string {
  if (!mb) return 'N/A'
  if (mb >= 1024) {
    return `${(mb / 1024).toFixed(1)} GB`
  }
  return `${mb} MB`
}

function formatDisk(mb: number): string {
  if (!mb) return 'N/A'
  if (mb >= 1024 * 1024) {
    return `${(mb / 1024 / 1024).toFixed(1)} TB`
  }
  if (mb >= 1024) {
    return `${(mb / 1024).toFixed(1)} GB`
  }
  return `${mb} MB`
}

async function restartPeer() {
  if (restarting.value) return

  restarting.value = true

  // Disconnect WebSocket before restarting to prevent reconnection attempts
  disconnectWebSocket()

  try {
    const result = await api.restartNode()

    if (result.success) {
      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: result.message || t('message.navigation.restartSuccess'),
        life: 3000
      })

      // Logout after showing the message to avoid connection errors
      setTimeout(() => {
        authStore.clearAuth()
        router.push('/login')
      }, 1500)
    } else {
      toast.add({
        severity: 'error',
        summary: t('message.common.error'),
        detail: result.error || t('message.navigation.restartFailed'),
        life: 5000
      })
      restarting.value = false
    }
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message || t('message.navigation.restartFailed'),
      life: 5000
    })
    restarting.value = false
  }
}

onMounted(async () => {
  // Load initial data
  await loadDashboardData()

  // Initialize WebSocket subscriptions in all stores
  // (WebSocket connection is already initialized in LoginView after authentication)
  nodeStore.initializeWebSocket()
  peersStore.initializeWebSocket()
  servicesStore.initializeWebSocket()
  workflowsStore.initializeWebSocket()

  // Note: Removed 30-second polling - now using WebSocket for real-time updates
})

onUnmounted(() => {
  // Cleanup WebSocket subscriptions (but don't disconnect - persist across routes)
  nodeStore.cleanupWebSocket()
  peersStore.cleanupWebSocket()
  servicesStore.cleanupWebSocket()
  workflowsStore.cleanupWebSocket()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.dashboard {
  position: relative;
  width: 100%;
  height: 100vh;
  overflow: auto;
  padding: 1rem;

  --color-active-services: #4060c3;
  --color-sold-services: rgba(86, 164, 82, 1);
  --color-purchased-services: rgba(205, 81, 36, 1);
  --color-known-peers: rgba(205, 81, 36, 1);
  --color-running-workflows: rgba(205, 81, 36, 1);
  --color-design-workflows: #4060c3;

  // Badge styling - white background, black text
  :deep(.p-badge) {
    background-color: #fff !important;
    color: #000 !important;
  }
}

.dashboard-controls {
  display: flex;
  flex-direction: row;
  flex-wrap: nowrap;
  justify-content: flex-end;
  align-content: center;
  align-items: center;
  width: 100%;
  padding-bottom: 1.5rem;
  border-bottom: 2px solid rgb(49, 64, 92);

  .dashboard-controls-buttons {
    display: flex;
    flex-direction: row;
    flex-wrap: nowrap;
    justify-content: flex-end;
    align-content: center;
    align-items: center;

    .input-box {
      .btn {
        min-width: 60px;
        height: 30px;
        line-height: 30px;
        border-radius: 3px;
        border: none;
        margin: 0 5px 0 0;
        padding: 0 8px;
        cursor: pointer;
        background-color: rgb(205, 81, 36);
        color: #fff;
        font-size: .9rem;

        i {
          vertical-align: text-bottom;
        }

        &.light {
          background-color: #fff;
          color: rgb(27, 38, 54);

          &:hover {
            background-color: #fff;
            color: rgb(205, 81, 36);
          }
        }

        &:hover {
          background-color: rgb(246, 114, 66);
        }

        &.disabled {
          background-color: #333333;
          cursor: not-allowed;
        }
      }
    }
  }
}

.quick-links-section {
  padding: 1rem 0;

  .quick-links-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
    }
  }

  .quick-links-list {
    display: flex;
    flex-direction: row;
    flex-wrap: wrap;
    justify-content: flex-start;
    align-content: flex-start;
    align-items: flex-start;
    width: 100%;
    max-width: 100%;

    .quick-link-box {
      width: 10rem;
      min-width: 10rem;
      height: 5rem;
      background-color: rgb(38, 49, 65);
      border-radius: 4px;
      cursor: pointer;
      margin: 1rem 1rem 1rem 0;
      padding: .5rem;
      transition: background-color 0.2s ease;

      &:hover {
        background-color: rgb(49, 64, 92);
      }

      display: flex;
      flex-direction: column;
      flex-wrap: nowrap;
      justify-content: center;
      align-content: center;
      align-items: center;

      .quick-link-box-content {
        width: 100%;
        display: flex;
        flex-direction: row;
        flex-wrap: nowrap;
        justify-content: flex-start;
        align-content: center;
        align-items: center;

        .quick-link-box-content-icon {
          padding: 0 .25rem;
        }

        .quick-link-box-content-details {
          padding-left: 1rem;
          font-size: .85rem;
        }
      }
    }
  }
}

.node-stats-section {
  padding: 1rem 0;

  .node-stats-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
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

  .node-stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
    gap: 1rem;
    background-color: rgb(38, 49, 65);
    padding: 1.5rem;
    border-radius: 4px;

    .stat-item {
      display: flex;
      flex-direction: column;
      gap: 0.5rem;

      .stat-label {
        color: vars.$color-text-secondary;
        font-size: vars.$font-size-sm;
      }

      .stat-value {
        color: vars.$color-text;
        font-weight: 500;
        font-size: vars.$font-size-md;
        word-break: break-all;
      }

      .stat-value-with-copy {
        display: flex;
        align-items: center;
        gap: 0.5rem;

        .copy-icon {
          color: vars.$color-text-secondary;
          cursor: pointer;
          font-size: 1rem;
          transition: color 0.2s ease;

          &:hover {
            color: vars.$color-primary;
          }
        }
      }

      &.stat-item-action {
        grid-column: 1 / -1;
        margin-top: 1rem;

        .btn {
          min-width: 60px;
          max-width: 200px;
          height: 30px;
          line-height: 30px;
          border-radius: 3px;
          border: none;
          margin: 0;
          padding: 0 8px;
          cursor: pointer;
          background-color: #4060c3;
          color: #fff;
          font-size: .9rem;
          display: inline-flex;
          align-items: center;
          justify-content: center;
          gap: 0.5rem;

          i {
            vertical-align: text-bottom;
          }

          &:hover {
            background-color: #5070d3;
          }

          &.disabled {
            background-color: #333333;
            cursor: not-allowed;
          }
        }
      }
    }
  }

  // System Capabilities Grid
  .capabilities-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 1rem;
    background-color: rgb(38, 49, 65);
    padding: 1.5rem;
    border-radius: 4px;

    .capability-group {
      .capability-group-title {
        font-size: 0.8rem;
        font-weight: 600;
        color: #94a3b8;
        text-transform: uppercase;
        letter-spacing: 0.05em;
        margin-bottom: 0.5rem;
        border-bottom: 1px solid rgba(255, 255, 255, 0.1);
        padding-bottom: 0.25rem;
      }

      .capability-item {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 0.25rem 0;

        .cap-label {
          font-size: 0.8rem;
          color: #64748b;

          &.gpu-vendor {
            padding: 0.15rem 0.4rem;
            border-radius: 3px;
            font-weight: 700;
            font-size: 0.7rem;

            &.nvidia {
              background-color: #76b900;
              color: #000;
            }

            &.amd {
              background-color: #ed1c24;
              color: #fff;
            }

            &.intel {
              background-color: #0071c5;
              color: #fff;
            }
          }
        }

        .cap-value {
          font-size: 0.85rem;
          color: #e2e8f0;
          font-family: monospace;
          text-align: right;

          &.available {
            color: #4ade80;
          }

          &.no-gpu {
            color: #64748b;
            font-style: italic;
          }
        }
      }
    }
  }

  .no-capabilities {
    background-color: rgb(38, 49, 65);
    padding: 1.5rem;
    border-radius: 4px;
    text-align: center;
    color: #64748b;
  }
}
</style>
