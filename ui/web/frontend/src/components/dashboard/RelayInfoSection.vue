<template>
  <div class="relay-info-section">
    <div class="relay-info-title">
      <i class="pi pi-share-alt"></i> {{ $t('message.dashboard.relayInfo') }}
    </div>

    <!-- Relay Mode View -->
    <div v-if="isRelayMode" class="relay-mode-view">
      <!-- Relay Stats Card -->
      <div class="relay-stats-card">
        <div class="stat-row">
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.activeSessions') }}:</span>
            <span class="stat-value">{{ relayStats.active_sessions || 0 }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.maxConnections') }}:</span>
            <span class="stat-value">{{ relayStats.max_connections || 0 }}</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.pricingPerGb') }}:</span>
            <span class="stat-value">{{ relayStats.pricing_per_gb || 0 }} tokens</span>
          </div>
          <div class="stat-item">
            <span class="stat-label">{{ $t('message.dashboard.totalTraffic') }}:</span>
            <span class="stat-value">{{ formatBytes(relayStats.total_bytes || 0) }}</span>
          </div>
        </div>
      </div>

      <!-- Connected Clients Table -->
      <div class="sessions-table">
        <h3>{{ $t('message.dashboard.connectedClients') }}</h3>
        <DataTable
          v-if="sessions.length > 0"
          :value="sessions"
          :paginator="sessions.length > 10"
          :rows="10"
          :rowsPerPageOptions="[10, 20, 50, 100]"
          responsiveLayout="scroll"
          filterDisplay="row"
          v-model:filters="sessionsFilters"
        >
          <Column field="remote_peer_id" :header="$t('message.dashboard.peerId')" filterMatchMode="contains" :showFilterMenu="false">
            <template #body="slotProps">
              <div class="id-with-copy">
                <span>{{ shorten(slotProps.data.remote_peer_id, 6, 6) }}</span>
                <i class="pi pi-copy copy-icon" @click="copyToClipboard(slotProps.data.remote_peer_id)" :title="$t('message.common.copy')"></i>
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
          <Column field="duration_seconds" :header="$t('message.dashboard.sessionDuration')" sortable>
            <template #body="slotProps">
              {{ formatDuration(slotProps.data.duration_seconds) }}
            </template>
          </Column>
          <Column field="ingress_bytes" :header="$t('message.dashboard.trafficIngress')" sortable>
            <template #body="slotProps">
              {{ formatBytes(slotProps.data.ingress_bytes) }}
            </template>
          </Column>
          <Column field="egress_bytes" :header="$t('message.dashboard.trafficEgress')" sortable>
            <template #body="slotProps">
              {{ formatBytes(slotProps.data.egress_bytes) }}
            </template>
          </Column>
          <Column field="total_bytes" :header="$t('message.dashboard.trafficTotal')" sortable>
            <template #body="slotProps">
              {{ formatBytes(slotProps.data.total_bytes) }}
            </template>
          </Column>
          <Column field="earnings" :header="$t('message.dashboard.earnings')" sortable>
            <template #body="slotProps">
              {{ (slotProps.data.earnings || 0).toFixed(4) }} tokens
            </template>
          </Column>
          <Column :header="$t('message.common.actions')">
            <template #body="slotProps">
              <Button
                icon="pi pi-times"
                class="p-button-sm p-button-danger p-button-text"
                :label="$t('message.dashboard.disconnect')"
                @click="confirmDisconnect(slotProps.data)"
              />
              <Button
                icon="pi pi-ban"
                class="p-button-sm p-button-danger p-button-text"
                :label="$t('message.dashboard.blacklist')"
                @click="confirmBlacklist(slotProps.data)"
              />
            </template>
          </Column>
        </DataTable>
        <div v-else class="no-data">
          <i class="pi pi-info-circle"></i>
          {{ $t('message.dashboard.noActiveSessions') }}
        </div>
      </div>
    </div>

    <!-- NAT Mode View -->
    <div v-else class="nat-mode-view">
      <!-- Current Relay Info Card -->
      <div v-if="currentRelay" class="current-relay-card">
        <h3>{{ $t('message.dashboard.currentRelay') }}</h3>
        <div class="relay-info-grid">
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.peerId') }}:</span>
            <div class="info-value-with-copy">
              <span>{{ shorten(currentRelay.peer_id, 6, 6) }}</span>
              <i class="pi pi-copy copy-icon" @click="copyToClipboard(currentRelay.peer_id)" :title="$t('message.common.copy')"></i>
            </div>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.relayLatency') }}:</span>
            <span class="info-value" :class="getLatencyClass(currentRelay.latency_ms)">{{ currentRelay.latency }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.relayPricing') }}:</span>
            <span class="info-value">{{ currentRelay.pricing_per_gb }} tokens/GB</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.relayEndpoint') }}:</span>
            <span class="info-value">{{ currentRelay.endpoint }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.sessionDuration') }}:</span>
            <span class="info-value">{{ formatDuration(currentRelay.duration_seconds) }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.trafficIngress') }}:</span>
            <span class="info-value">{{ formatBytes(currentRelay.ingress_bytes) }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.trafficEgress') }}:</span>
            <span class="info-value">{{ formatBytes(currentRelay.egress_bytes) }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.trafficTotal') }}:</span>
            <span class="info-value">{{ formatBytes(currentRelay.total_bytes) }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ $t('message.dashboard.currentCost') }}:</span>
            <span class="info-value">{{ currentRelay.current_cost?.toFixed(4) || '0.0000' }} tokens</span>
          </div>
          <div class="info-item info-item-action">
            <Button
              :label="$t('message.dashboard.disconnectFromRelay')"
              icon="pi pi-times"
              class="p-button-sm p-button-danger"
              @click="confirmDisconnectRelay"
            />
          </div>
        </div>
      </div>
      <div v-else class="no-relay-card">
        <i class="pi pi-info-circle"></i>
        {{ $t('message.dashboard.noRelay') }}
      </div>

      <!-- Available Relays Table -->
      <div class="candidates-table">
        <h3>{{ $t('message.dashboard.availableRelays') }}</h3>
        <DataTable
          v-if="candidates.length > 0"
          :value="candidates"
          :paginator="candidates.length > 10"
          :rows="10"
          :rowsPerPageOptions="[10, 20, 50, 100]"
          responsiveLayout="scroll"
          sortField="latency_ms"
          :sortOrder="1"
          filterDisplay="row"
          v-model:filters="candidatesFilters"
        >
          <Column field="peer_id" :header="$t('message.dashboard.peerId')" filterMatchMode="contains" :showFilterMenu="false">
            <template #body="slotProps">
              <div class="id-with-copy">
                <span>{{ shorten(slotProps.data.peer_id, 6, 6) }}</span>
                <i class="pi pi-copy copy-icon" @click="copyToClipboard(slotProps.data.peer_id)" :title="$t('message.common.copy')"></i>
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
          <Column field="latency_ms" :header="$t('message.dashboard.relayLatency')" sortable>
            <template #body="slotProps">
              <span :class="getLatencyClass(slotProps.data.latency_ms)">{{ slotProps.data.latency }}</span>
            </template>
          </Column>
          <Column field="pricing_per_gb" :header="$t('message.dashboard.pricingPerGb')" sortable>
            <template #body="slotProps">
              {{ slotProps.data.pricing_per_gb }} tokens/GB
            </template>
          </Column>
          <Column field="reputation_score" :header="$t('message.dashboard.reputationScore')" sortable>
            <template #body="slotProps">
              {{ slotProps.data.reputation_score?.toFixed(2) ?? '0.00' }}
            </template>
          </Column>
          <Column field="capacity" :header="$t('message.dashboard.capacity')" sortable>
            <template #body="slotProps">
              {{ slotProps.data.capacity }}
            </template>
          </Column>
          <Column field="status_priority" :header="$t('message.dashboard.status')" sortable>
            <template #body="slotProps">
              <span v-if="slotProps.data.is_connected" class="status-badge connected">
                {{ $t('message.dashboard.connected') }}
              </span>
              <span v-else-if="slotProps.data.is_preferred" class="status-badge preferred">
                {{ $t('message.dashboard.preferred') }}
              </span>
              <span v-else class="status-badge available">
                {{ $t('message.dashboard.available') }}
              </span>
            </template>
          </Column>
          <Column :header="$t('message.common.actions')">
            <template #body="slotProps">
              <Button
                v-if="!slotProps.data.is_connected"
                icon="pi pi-link"
                class="p-button-sm p-button-success p-button-text"
                :label="$t('message.dashboard.connect')"
                @click="confirmConnect(slotProps.data)"
              />
              <Button
                :icon="slotProps.data.is_preferred ? 'pi pi-star-fill' : 'pi pi-star'"
                class="p-button-sm p-button-text"
                :class="{ 'p-button-warning': slotProps.data.is_preferred }"
                :label="slotProps.data.is_preferred ? $t('message.dashboard.preferred') : $t('message.dashboard.setPreferred')"
                :disabled="slotProps.data.is_preferred"
                @click="setPreferred(slotProps.data)"
              />
            </template>
          </Column>
        </DataTable>
        <div v-else class="no-data">
          <i class="pi pi-info-circle"></i>
          {{ $t('message.dashboard.noRelayCandidates') }}
        </div>
      </div>
    </div>

    <!-- Confirm Dialogs -->
    <Dialog v-model:visible="showDisconnectDialog" :header="$t('message.common.confirm')" :modal="true" style="width: 400px">
      <p>{{ $t('message.dashboard.disconnectConfirm') }}</p>
      <template #footer>
        <Button :label="$t('message.common.cancel')" icon="pi pi-times" @click="showDisconnectDialog = false" class="p-button-text" />
        <Button :label="$t('message.dashboard.disconnect')" icon="pi pi-check" @click="disconnectSession" class="p-button-danger" />
      </template>
    </Dialog>

    <Dialog v-model:visible="showBlacklistDialog" :header="$t('message.common.confirm')" :modal="true" style="width: 400px">
      <p>{{ $t('message.dashboard.blacklistConfirm') }}</p>
      <template #footer>
        <Button :label="$t('message.common.cancel')" icon="pi pi-times" @click="showBlacklistDialog = false" class="p-button-text" />
        <Button :label="$t('message.dashboard.blacklist')" icon="pi pi-check" @click="blacklistSession" class="p-button-danger" />
      </template>
    </Dialog>

    <Dialog v-model:visible="showConnectDialog" :header="$t('message.common.confirm')" :modal="true" style="width: 400px">
      <p>{{ $t('message.dashboard.connectConfirm') }}</p>
      <template #footer>
        <Button :label="$t('message.common.cancel')" icon="pi pi-times" @click="showConnectDialog = false" class="p-button-text" />
        <Button :label="$t('message.dashboard.connect')" icon="pi pi-check" @click="connectRelay" class="p-button-success" />
      </template>
    </Dialog>

    <Dialog v-model:visible="showDisconnectRelayDialog" :header="$t('message.common.confirm')" :modal="true" style="width: 400px">
      <p>{{ $t('message.dashboard.disconnectRelayConfirm') }}</p>
      <template #footer>
        <Button :label="$t('message.common.cancel')" icon="pi pi-times" @click="showDisconnectRelayDialog = false" class="p-button-text" />
        <Button :label="$t('message.dashboard.disconnect')" icon="pi pi-check" @click="disconnectFromRelay" class="p-button-danger" />
      </template>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import { FilterMatchMode } from '@primevue/core/api'
import { api } from '../../services/api'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'
import { getWebSocketService, MessageType } from '../../services/websocket'

interface Props {
  isRelayMode: boolean
  relayStats: any
}

const props = defineProps<Props>()
const { t } = useI18n()
const toast = useToast()
const { copyToClipboard: copyText } = useClipboard()
const { shorten } = useTextUtils()

// State
const sessions = ref<any[]>([])
const candidates = ref<any[]>([])
const currentRelay = ref<any>(null)
const selectedSession = ref<any>(null)
const selectedCandidate = ref<any>(null)
const showDisconnectDialog = ref(false)
const showBlacklistDialog = ref(false)
const showConnectDialog = ref(false)
const showDisconnectRelayDialog = ref(false)

// WebSocket unsubscribe functions
let unsubscribeSessions: (() => void) | null = null
let unsubscribeCandidates: (() => void) | null = null

// Filter states
const sessionsFilters = ref({
  remote_peer_id: { value: null, matchMode: FilterMatchMode.CONTAINS }
})

const candidatesFilters = ref({
  peer_id: { value: null, matchMode: FilterMatchMode.CONTAINS }
})

// Watch for changes to current relay session from WebSocket NODE_STATUS updates
watch(() => props.relayStats.current_relay_session, (newSession) => {
  if (newSession) {
    currentRelay.value = {
      peer_id: newSession.relay_peer_id,
      node_id: newSession.relay_node_id,
      endpoint: newSession.endpoint,
      latency: newSession.latency,
      latency_ms: newSession.latency_ms,
      pricing_per_gb: newSession.pricing,
      duration_seconds: newSession.duration_seconds,
      ingress_bytes: newSession.ingress_bytes,
      egress_bytes: newSession.egress_bytes,
      total_bytes: newSession.total_bytes,
      current_cost: newSession.current_cost
    }
  } else {
    currentRelay.value = null
  }
}, { deep: true })

// Load data
async function loadRelayData() {
  if (props.isRelayMode) {
    // Load sessions for relay mode
    try {
      const data = await api.getRelaySessions()
      sessions.value = data || []
    } catch (error: any) {
      console.error('Failed to load relay sessions:', error)
    }
  } else {
    // Load candidates for NAT mode
    try {
      const data = await api.getRelayCandidates()
      candidates.value = data || []

      // Get current relay session info from stats (includes traffic data)
      if (props.relayStats.current_relay_session) {
        const session = props.relayStats.current_relay_session
        currentRelay.value = {
          peer_id: session.relay_peer_id, // Persistent Ed25519-based peer ID
          node_id: session.relay_node_id, // DHT node ID
          endpoint: session.endpoint,
          latency: session.latency,
          latency_ms: session.latency_ms,
          pricing_per_gb: session.pricing,
          duration_seconds: session.duration_seconds,
          ingress_bytes: session.ingress_bytes,
          egress_bytes: session.egress_bytes,
          total_bytes: session.total_bytes,
          current_cost: session.current_cost
        }
      } else {
        currentRelay.value = null
      }
    } catch (error: any) {
      console.error('Failed to load relay candidates:', error)
    }
  }
}

// Relay Mode Functions
function confirmDisconnect(session: any) {
  selectedSession.value = session
  showDisconnectDialog.value = true
}

function confirmBlacklist(session: any) {
  selectedSession.value = session
  showBlacklistDialog.value = true
}

async function disconnectSession() {
  try {
    await api.disconnectRelaySession(selectedSession.value.session_id)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.dashboard.sessionDisconnected'),
      life: 3000
    })
    showDisconnectDialog.value = false
    await loadRelayData()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message,
      life: 5000
    })
  }
}

async function blacklistSession() {
  try {
    await api.blacklistRelaySession(selectedSession.value.session_id)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.dashboard.peerBlacklisted'),
      life: 3000
    })
    showBlacklistDialog.value = false
    await loadRelayData()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message,
      life: 5000
    })
  }
}

// NAT Mode Functions
function confirmConnect(candidate: any) {
  selectedCandidate.value = candidate
  showConnectDialog.value = true
}

function confirmDisconnectRelay() {
  showDisconnectRelayDialog.value = true
}

async function connectRelay() {
  // Close dialog immediately for non-blocking UI
  showConnectDialog.value = false

  // Show loading toast
  toast.add({
    severity: 'info',
    summary: t('message.common.loading'),
    detail: t('message.dashboard.connectingToRelay'),
    life: 2000
  })

  try {
    await api.connectToRelay(selectedCandidate.value.peer_id)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.dashboard.relayConnected'),
      life: 3000
    })
    await loadRelayData()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message,
      life: 5000
    })
  }
}

async function disconnectFromRelay() {
  // Close dialog immediately for non-blocking UI
  showDisconnectRelayDialog.value = false

  // Show loading toast
  toast.add({
    severity: 'info',
    summary: t('message.common.loading'),
    detail: t('message.dashboard.disconnectingRelay'),
    life: 2000
  })

  try {
    await api.disconnectFromRelay()
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.dashboard.relayDisconnected'),
      life: 3000
    })
    await loadRelayData()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message,
      life: 5000
    })
  }
}

async function setPreferred(candidate: any) {
  try {
    await api.setPreferredRelay(candidate.peer_id)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.dashboard.preferredSet'),
      life: 3000
    })
    await loadRelayData()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.response?.data?.error || error.message,
      life: 5000
    })
  }
}

// Utility Functions
async function copyToClipboard(text: string) {
  const success = await copyText(text)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

function formatBytes(bytes: number): string {
  if (bytes == null || isNaN(bytes) || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

function formatDuration(seconds: number): string {
  if (seconds == null || isNaN(seconds)) return '0h 0m 0s'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)
  return `${hours}h ${minutes}m ${secs}s`
}

function getLatencyClass(latencyMs: number): string {
  if (latencyMs < 100) return 'latency-good'
  if (latencyMs < 300) return 'latency-moderate'
  return 'latency-poor'
}

onMounted(async () => {
  // Load initial data
  await loadRelayData()

  // Initialize WebSocket subscriptions for real-time relay updates
  const wsService = getWebSocketService()
  if (wsService) {
    // Subscribe to relay sessions (relay mode)
    unsubscribeSessions = wsService.subscribe(MessageType.RELAY_SESSIONS, (payload: any) => {
      if (payload && payload.sessions) {
        sessions.value = payload.sessions
      }
    })

    // Subscribe to relay candidates (NAT mode)
    unsubscribeCandidates = wsService.subscribe(MessageType.RELAY_CANDIDATES, (payload: any) => {
      if (payload && payload.candidates) {
        // Add status_priority for sorting: 1=Connected, 2=Preferred, 3=Available
        candidates.value = payload.candidates.map((c: any) => ({
          ...c,
          status_priority: c.is_connected ? 1 : (c.is_preferred ? 2 : 3)
        }))
        // Note: Current relay session data (duration, traffic, cost) comes from
        // props.relayStats.current_relay_session via NODE_STATUS WebSocket updates,
        // NOT from the candidates list which only has static relay info (latency, pricing, etc.)
      }
    })
  }

  // Note: Removed 30-second polling - now using WebSocket for real-time updates
})

onUnmounted(() => {
  // Cleanup WebSocket subscriptions
  if (unsubscribeSessions) {
    unsubscribeSessions()
    unsubscribeSessions = null
  }
  if (unsubscribeCandidates) {
    unsubscribeCandidates()
    unsubscribeCandidates = null
  }
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.relay-info-section {
  padding: 1rem 0;

  .relay-info-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
    }
  }

  .relay-stats-card {
    background-color: rgb(38, 49, 65);
    padding: 1.5rem;
    border-radius: 4px;
    margin-bottom: 1.5rem;

    .stat-row {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 1rem;

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
        }
      }
    }
  }

  .sessions-table,
  .candidates-table {
    margin-bottom: 1.5rem;

    h3 {
      margin-bottom: 1rem;
      color: vars.$color-text;
    }
  }

  .current-relay-card,
  .no-relay-card {
    background-color: rgb(38, 49, 65);
    padding: 1.5rem;
    border-radius: 4px;
    margin-bottom: 1.5rem;

    h3 {
      margin-bottom: 1rem;
      color: vars.$color-text;
    }
  }

  .no-relay-card {
    text-align: center;
    padding: 2rem;
    color: vars.$color-text-secondary;

    i {
      font-size: 2rem;
      margin-bottom: 0.5rem;
      display: block;
    }
  }

  .relay-info-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 1rem;

    .info-item {
      display: flex;
      flex-direction: column;
      gap: 0.5rem;

      .info-label {
        color: vars.$color-text-secondary;
        font-size: vars.$font-size-sm;
      }

      .info-value {
        color: vars.$color-text;
        font-weight: 500;
        font-size: vars.$font-size-md;
      }

      .info-value-with-copy {
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

      &.info-item-action {
        grid-column: 1 / -1;
        margin-top: 0.5rem;

        .p-button {
          max-width: 200px;
        }
      }
    }
  }

  .id-with-copy {
    display: flex;
    align-items: center;
    gap: 0.5rem;

    .copy-icon {
      color: vars.$color-text-secondary;
      cursor: pointer;
      font-size: 0.9rem;
      transition: color 0.2s ease;

      &:hover {
        color: vars.$color-primary;
      }
    }
  }

  .latency-good {
    color: #4caf50;
  }

  .latency-moderate {
    color: #ff9800;
  }

  .latency-poor {
    color: #f44336;
  }

  .status-badge {
    padding: 0.25rem 0.5rem;
    border-radius: 3px;
    font-size: 0.85rem;
    font-weight: 500;

    &.connected {
      background-color: #4caf50;
      color: white;
    }

    &.preferred {
      background-color: #ff9800;
      color: white;
    }

    &.available {
      background-color: #2196f3;
      color: white;
    }
  }

  .no-data {
    text-align: center;
    padding: 2rem;
    color: vars.$color-text-secondary;
    background-color: rgb(38, 49, 65);
    border-radius: 4px;

    i {
      font-size: 1.5rem;
      margin-bottom: 0.5rem;
      display: block;
    }
  }

  .loading-state {
    text-align: center;
    padding: 1rem 0;
    color: vars.$color-text-secondary;

    i {
      display: block;
      margin-bottom: 1rem;
      color: vars.$color-primary;
    }

    p {
      margin: 0;
    }
  }
}
</style>
