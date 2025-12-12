<template>
  <AppLayout>
    <div class="peers">
      <!-- Filter Section -->
      <div class="filter-section">
        <div class="filter-buttons">
          <div
            class="filter-button"
            :class="{ active: activeFilter === 'active' }"
            @click="toggleFilter('active')"
          >
            <div class="filter-button-content">
              <div class="filter-button-icon">
                <OverlayBadge :value="nonBlacklistedCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-users" size="large"
                    style="background-color: #56a452; color: #fff" />
                </OverlayBadge>
              </div>
              <div class="filter-button-label">
                {{ $t('message.peers.filterActive') }}
              </div>
            </div>
          </div>

          <div
            class="filter-button"
            :class="{ active: activeFilter === 'blacklisted' }"
            @click="toggleFilter('blacklisted')"
          >
            <div class="filter-button-content">
              <div class="filter-button-icon">
                <OverlayBadge :value="blacklistedCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-ban" size="large"
                    style="background-color: rgb(205, 81, 36); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="filter-button-label">
                {{ $t('message.peers.filterBlacklisted') }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="peersStore.loading" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <div v-else class="peers-table-section">
        <DataTable
          :value="filteredPeers"
          :paginator="filteredPeers.length > 10"
          :rows="10"
          class="peers-table"
          :rowsPerPageOptions="[10, 20, 50, 100]"
          responsiveLayout="scroll"
          sortField="last_seen"
          :sortOrder="-1"
          filterDisplay="row"
          v-model:filters="peersFilters"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-inbox"></i>
              <p>{{ $t('message.peers.noPeers') }}</p>
            </div>
          </template>

          <Column field="peer_id" :header="$t('message.peers.peerId')" filterMatchMode="contains" :showFilterMenu="false">
            <template #body="slotProps">
              <div class="id-with-copy">
                <span>{{ shorten(slotProps.data.peer_id, 6, 6) }}</span>
                <i class="pi pi-copy copy-icon" @click="copyPeerId(slotProps.data.peer_id)" :title="$t('message.common.copy')"></i>
              </div>
            </template>
            <template #filter="{ filterModel, filterCallback }">
              <InputText
                v-model="filterModel.value"
                type="text"
                @input="filterCallback()"
                placeholder="Search by Peer ID"
                class="p-column-filter"
              />
            </template>
          </Column>

          <Column field="is_relay" :header="$t('message.peers.isRelay')" :sortable="true">
            <template #body="slotProps">
              <span v-if="slotProps.data.is_relay" class="status-badge relay">
                Yes
              </span>
              <span v-else class="status-badge no-relay">
                No
              </span>
            </template>
          </Column>

          <Column field="last_seen" :header="$t('message.peers.lastSeen')" :sortable="true">
            <template #body="slotProps">
              {{ formatDate(slotProps.data.last_seen) }}
            </template>
          </Column>

          <Column field="files_count" header="Files" sortable>
            <template #body="slotProps">
              {{ slotProps.data.files_count }}
            </template>
          </Column>

          <Column field="apps_count" header="Apps" sortable>
            <template #body="slotProps">
              {{ slotProps.data.apps_count }}
            </template>
          </Column>

          <Column :header="$t('message.common.actions')">
            <template #body="slotProps">
              <Button
                icon="pi pi-server"
                class="p-button-sm p-button-secondary p-button-text"
                title="View Capabilities"
                @click="viewPeerCapabilities(slotProps.data)"
              />
              <Button
                icon="pi pi-box"
                class="p-button-sm p-button-info p-button-text"
                :label="$t('message.peers.viewServices')"
                @click="viewPeerServices(slotProps.data)"
              />
              <Button
                v-if="!peersStore.isBlacklisted(slotProps.data.peer_id)"
                icon="pi pi-ban"
                class="p-button-sm p-button-danger p-button-text"
                :label="$t('message.dashboard.blacklist')"
                @click="blacklistPeer(slotProps.data)"
              />
              <Button
                v-else
                icon="pi pi-check"
                class="p-button-sm p-button-success p-button-text"
                @click="unblacklistPeer(slotProps.data)"
              />
            </template>
          </Column>
        </DataTable>
      </div>

      <!-- Peer Capabilities Dialog -->
      <Dialog
        v-model:visible="capabilitiesDialogVisible"
        :header="'Peer Capabilities: ' + shorten(selectedPeerId, 6, 6)"
        :style="{ width: '600px' }"
        :modal="true"
        :closable="true"
      >
        <div v-if="capabilitiesLoading" class="capabilities-loading">
          <ProgressSpinner style="width:40px;height:40px" strokeWidth="4" />
          <p>Loading capabilities from DHT...</p>
        </div>

        <div v-else-if="capabilitiesError" class="capabilities-error">
          <i class="pi pi-exclamation-triangle"></i>
          <p>{{ capabilitiesError }}</p>
        </div>

        <div v-else-if="peerCapabilities" class="capabilities-content">
          <!-- System Info -->
          <div class="capabilities-section">
            <h4><i class="pi pi-desktop"></i> System</h4>
            <div class="capabilities-grid">
              <div class="cap-item">
                <span class="cap-label">Platform</span>
                <span class="cap-value">{{ peerCapabilities.platform }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Architecture</span>
                <span class="cap-value">{{ peerCapabilities.architecture }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Kernel</span>
                <span class="cap-value">{{ peerCapabilities.kernel_version }}</span>
              </div>
            </div>
          </div>

          <!-- CPU Info -->
          <div class="capabilities-section">
            <h4><i class="pi pi-microchip-ai"></i> CPU</h4>
            <div class="capabilities-grid">
              <div class="cap-item full-width">
                <span class="cap-label">Model</span>
                <span class="cap-value">{{ peerCapabilities.cpu_model }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Cores</span>
                <span class="cap-value">{{ peerCapabilities.cpu_cores }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Threads</span>
                <span class="cap-value">{{ peerCapabilities.cpu_threads }}</span>
              </div>
            </div>
          </div>

          <!-- Memory Info -->
          <div class="capabilities-section">
            <h4><i class="pi pi-database"></i> Memory</h4>
            <div class="capabilities-grid">
              <div class="cap-item">
                <span class="cap-label">Total</span>
                <span class="cap-value">{{ formatMemory(peerCapabilities.total_memory_mb) }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Available</span>
                <span class="cap-value">{{ formatMemory(peerCapabilities.available_memory_mb) }}</span>
              </div>
            </div>
          </div>

          <!-- Disk Info -->
          <div class="capabilities-section">
            <h4><i class="pi pi-save"></i> Disk</h4>
            <div class="capabilities-grid">
              <div class="cap-item">
                <span class="cap-label">Total</span>
                <span class="cap-value">{{ formatDisk(peerCapabilities.total_disk_mb) }}</span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Available</span>
                <span class="cap-value">{{ formatDisk(peerCapabilities.available_disk_mb) }}</span>
              </div>
            </div>
          </div>

          <!-- GPU Info -->
          <div class="capabilities-section" v-if="peerCapabilities.gpus && peerCapabilities.gpus.length > 0">
            <h4><i class="pi pi-bolt"></i> GPU(s)</h4>
            <div v-for="(gpu, index) in peerCapabilities.gpus" :key="index" class="gpu-card">
              <div class="gpu-header">
                <span class="gpu-vendor-badge" :class="gpu.vendor">{{ gpu.vendor.toUpperCase() }}</span>
                <span class="gpu-name">{{ gpu.name }}</span>
              </div>
              <div class="capabilities-grid">
                <div class="cap-item">
                  <span class="cap-label">Memory</span>
                  <span class="cap-value">{{ formatMemory(gpu.memory_mb) }}</span>
                </div>
                <div class="cap-item" v-if="gpu.driver_version">
                  <span class="cap-label">Driver</span>
                  <span class="cap-value">{{ gpu.driver_version }}</span>
                </div>
              </div>
            </div>
          </div>
          <div class="capabilities-section" v-else>
            <h4><i class="pi pi-bolt"></i> GPU</h4>
            <p class="no-gpu">No GPU detected</p>
          </div>

          <!-- Software Info -->
          <div class="capabilities-section">
            <h4><i class="pi pi-code"></i> Software</h4>
            <div class="capabilities-grid">
              <div class="cap-item">
                <span class="cap-label">Docker</span>
                <span class="cap-value" :class="{ 'available': peerCapabilities.has_docker }">
                  {{ peerCapabilities.has_docker ? (peerCapabilities.docker_version || 'Yes') : 'No' }}
                </span>
              </div>
              <div class="cap-item">
                <span class="cap-label">Python</span>
                <span class="cap-value" :class="{ 'available': peerCapabilities.has_python }">
                  {{ peerCapabilities.has_python ? (peerCapabilities.python_version || 'Yes') : 'No' }}
                </span>
              </div>
            </div>
          </div>
        </div>

        <template #footer>
          <Button label="Close" icon="pi pi-times" @click="capabilitiesDialogVisible = false" text />
        </template>
      </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import { FilterMatchMode } from '@primevue/core/api'

import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import Avatar from 'primevue/avatar'
import OverlayBadge from 'primevue/overlaybadge'
import InputText from 'primevue/inputtext'
import Dialog from 'primevue/dialog'

import AppLayout from '../layout/AppLayout.vue'
import { usePeersStore } from '../../stores/peers'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'
import { api, type SystemCapabilities } from '../../services/api'

const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()
const peersStore = usePeersStore()
const { copyToClipboard } = useClipboard()
const { shorten } = useTextUtils()

// Filter state
const activeFilter = ref<'all' | 'active' | 'blacklisted'>('all')

const peersFilters = ref({
  peer_id: { value: null, matchMode: FilterMatchMode.CONTAINS }
})

// Capabilities dialog state
const capabilitiesDialogVisible = ref(false)
const capabilitiesLoading = ref(false)
const capabilitiesError = ref<string | null>(null)
const selectedPeerId = ref('')
const peerCapabilities = ref<SystemCapabilities | null>(null)

// Computed properties
const filteredPeers = computed(() => {
  let peers = []
  if (activeFilter.value === 'active') {
    peers = peersStore.peers.filter(p => !peersStore.isBlacklisted(p.peer_id))
  } else if (activeFilter.value === 'blacklisted') {
    peers = peersStore.peers.filter(p => peersStore.isBlacklisted(p.peer_id))
  } else {
    peers = peersStore.peers
  }

  // Add sortable fields for Files and Apps
  return peers.map(p => ({
    ...p,
    files_count: getFileServicesCount(p),
    apps_count: getAppServicesCount(p)
  }))
})

const nonBlacklistedCount = computed(() => {
  return peersStore.peers.filter(p => !peersStore.isBlacklisted(p.peer_id)).length
})

const blacklistedCount = computed(() => {
  return peersStore.peers.filter(p => peersStore.isBlacklisted(p.peer_id)).length
})

// Functions
function toggleFilter(filter: 'active' | 'blacklisted') {
  if (activeFilter.value === filter) {
    activeFilter.value = 'all' // Toggle off
  } else {
    activeFilter.value = filter // Toggle on
  }
}

function formatDate(dateString: string): string {
  if (!dateString) return 'N/A'
  const date = new Date(dateString)
  return date.toLocaleString()
}

async function copyPeerId(peerId: string) {
  const success = await copyToClipboard(peerId)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

function getFileServicesCount(peer: any): number {
  return peer.files_count || 0
}

function getAppServicesCount(peer: any): number {
  return peer.apps_count || 0
}

function viewPeerServices(peer: any) {
  router.push(`/services/peer/${peer.peer_id}`)
}

async function viewPeerCapabilities(peer: any) {
  selectedPeerId.value = peer.peer_id
  capabilitiesDialogVisible.value = true
  capabilitiesLoading.value = true
  capabilitiesError.value = null
  peerCapabilities.value = null

  try {
    const response = await api.getPeerCapabilities(peer.peer_id)
    if (response.error) {
      capabilitiesError.value = response.error
    } else if (response.capabilities) {
      peerCapabilities.value = response.capabilities
    } else {
      capabilitiesError.value = 'No capabilities data available'
    }
  } catch (error: any) {
    capabilitiesError.value = error.message || 'Failed to fetch capabilities'
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

function blacklistPeer(peer: any) {
  confirm.require({
    message: t('message.configuration.blacklistConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await peersStore.addToBlacklist(peer.peer_id)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.configuration.blacklistSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.configuration.blacklistError'),
          life: 3000
        })
      }
    }
  })
}

function unblacklistPeer(peer: any) {
  confirm.require({
    message: t('message.configuration.unblacklistConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-question-circle',
    acceptClass: 'p-button-success',
    accept: async () => {
      try {
        await peersStore.removeFromBlacklist(peer.peer_id)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.configuration.unblacklistSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.configuration.unblacklistError'),
          life: 3000
        })
      }
    }
  })
}

onMounted(async () => {
  await peersStore.fetchPeers()
  await peersStore.fetchBlacklist()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.peers {
  min-height: 100vh;
  padding: vars.$spacing-lg;

  // Badge styling - white background, black text
  :deep(.p-badge) {
    background-color: #fff !important;
    color: #000 !important;
  }
}

.filter-section {
  padding: 1rem 0;

  .filter-buttons {
    display: flex;
    flex-direction: row;
    flex-wrap: wrap;
    justify-content: flex-start;
    align-content: flex-start;
    align-items: flex-start;
    width: 100%;
    max-width: 100%;

    .filter-button {
      width: 10rem;
      min-width: 10rem;
      height: 5rem;
      background-color: rgb(38, 49, 65);
      border-radius: 4px;
      cursor: pointer;
      margin: 1rem 1rem 1rem 0;
      padding: .5rem;
      transition: background-color 0.2s ease, border-color 0.2s ease;
      border: 2px solid transparent;

      display: flex;
      flex-direction: column;
      flex-wrap: nowrap;
      justify-content: center;
      align-content: center;
      align-items: center;

      &.active {
        border-color: rgb(205, 81, 36);
        background-color: rgb(49, 64, 92);
      }

      &:hover {
        background-color: rgb(49, 64, 92);
      }

      .filter-button-content {
        width: 100%;
        display: flex;
        flex-direction: row;
        flex-wrap: nowrap;
        justify-content: flex-start;
        align-content: center;
        align-items: center;

        .filter-button-icon {
          padding: 0 .25rem;
        }

        .filter-button-label {
          padding-left: 1rem;
          font-size: .85rem;
        }
      }
    }
  }
}

.loading {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: vars.$spacing-xl;
}

.peers-table-section {
  margin-bottom: 1.5rem;
}

.empty-state {
  text-align: center;
  padding: vars.$spacing-xl;
  color: vars.$color-text-secondary;

  i {
    font-size: 3rem;
    margin-bottom: vars.$spacing-md;
  }

  p {
    font-size: vars.$font-size-lg;
    margin: 0;
  }
}

.id-with-copy {
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

.status-badge {
  padding: 0.25rem 0.5rem;
  border-radius: 3px;
  font-size: 0.85rem;
  font-weight: 500;
  display: inline-block;

  &.relay {
    background-color: #4caf50;
    color: white;
  }

  &.no-relay {
    background-color: #757575;
    color: white;
  }
}

// Capabilities Dialog Styles
.capabilities-loading,
.capabilities-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 2rem;
  text-align: center;

  i {
    font-size: 2.5rem;
    color: #f59e0b;
    margin-bottom: 1rem;
  }

  p {
    color: vars.$color-text-secondary;
    margin: 0.5rem 0 0 0;
  }
}

.capabilities-content {
  .capabilities-section {
    margin-bottom: 1.5rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);

    &:last-child {
      border-bottom: none;
      margin-bottom: 0;
    }

    h4 {
      margin: 0 0 0.75rem 0;
      font-size: 0.9rem;
      font-weight: 600;
      color: #94a3b8;
      display: flex;
      align-items: center;
      gap: 0.5rem;

      i {
        font-size: 1rem;
      }
    }
  }

  .capabilities-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
  }

  .cap-item {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;

    &.full-width {
      grid-column: span 2;
    }

    .cap-label {
      font-size: 0.75rem;
      color: #64748b;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    .cap-value {
      font-size: 0.9rem;
      color: #e2e8f0;
      font-family: monospace;

      &.available {
        color: #4ade80;
      }
    }
  }

  .gpu-card {
    background-color: rgba(255, 255, 255, 0.05);
    border-radius: 6px;
    padding: 0.75rem;
    margin-bottom: 0.5rem;

    &:last-child {
      margin-bottom: 0;
    }

    .gpu-header {
      display: flex;
      align-items: center;
      gap: 0.75rem;
      margin-bottom: 0.75rem;

      .gpu-vendor-badge {
        padding: 0.2rem 0.5rem;
        border-radius: 4px;
        font-size: 0.7rem;
        font-weight: 700;
        text-transform: uppercase;

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

      .gpu-name {
        font-size: 0.85rem;
        color: #e2e8f0;
      }
    }
  }

  .no-gpu {
    color: #64748b;
    font-style: italic;
    margin: 0;
  }
}
</style>
