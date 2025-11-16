<template>
  <AppLayout>
    <div class="services">
      <!-- Peer-specific view -->
      <div v-if="isPeerView">
        <div class="services-header">
          <div class="header-left">
            <Button
              icon="pi pi-arrow-left"
              text
              rounded
              @click="router.push('/peers')"
              :title="$t('message.common.back')"
            />
            <h1>{{ pageTitle }}</h1>
          </div>
        </div>

        <div class="peer-services-section">
          <div class="peer-info">
            <i class="pi pi-user"></i>
            <span>{{ $t('message.peers.peerId') }}: <code class="peer-id">{{ shortenedPeerId }}</code></span>
            <i class="pi pi-copy copy-icon" @click="copyPeerId" :title="$t('message.common.copy')"></i>
          </div>

          <div class="info-message">
            <i class="pi pi-info-circle"></i>
            <p>{{ $t('message.services.peerServicesPlaceholder') }}</p>
          </div>
        </div>
      </div>

      <!-- Main services view -->
      <div v-else>
        <!-- Action buttons -->
        <div class="services-controls">
          <div class="services-controls-buttons">
            <div class="input-box">
              <button class="btn" @click="router.push('/services/add')">
                <i class="pi pi-plus-circle"></i> {{ $t('message.services.addLocalService') }}
              </button>
            </div>
            <div class="input-box">
              <button class="btn light" @click="showSearchRemoteDialog = true">
                <i class="pi pi-search"></i> {{ $t('message.services.searchRemoteService') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Local Services Section -->
        <div class="services-section">
          <div class="section-title">
            <i class="pi pi-box"></i> {{ $t('message.services.localServices') }}
          </div>

          <div v-if="servicesStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>

          <DataTable
            v-else
            :value="servicesStore.services"
            :paginator="servicesStore.services.length > 10"
            :rows="10"
            class="services-table"
            :rowsPerPageOptions="[10, 20, 50, 100]"
            responsiveLayout="scroll"
            sortField="created_at"
            :sortOrder="-1"
            @row-click="onRowClick"
            :rowHover="true"
          >
            <template #empty>
              <div class="empty-state">
                <i class="pi pi-inbox"></i>
                <p>{{ $t('message.services.noLocalServices') }}</p>
              </div>
            </template>

            <Column field="name" :header="$t('message.services.serviceName')" :sortable="true"></Column>
            <Column field="description" :header="$t('message.services.serviceDescription')">
              <template #body="slotProps">
                {{ truncateDescription(slotProps.data.description) }}
              </template>
            </Column>
            <Column field="service_type" :header="$t('message.services.type')" :sortable="true">
              <template #body="slotProps">
                <Tag :value="getServiceTypeLabel(slotProps.data.service_type || slotProps.data.type)" />
              </template>
            </Column>
            <Column field="status" :header="$t('message.services.status')" :sortable="true">
              <template #body="slotProps">
                <Tag
                  :value="getStatusLabel(slotProps.data.status)"
                  :severity="getStatusSeverity(slotProps.data.status)"
                />
              </template>
            </Column>
            <Column field="pricing" :header="$t('message.services.pricing')">
              <template #body="slotProps">
                {{ formatPricing(slotProps.data) }}
              </template>
            </Column>
            <Column :header="$t('message.common.actions')" :exportable="false" style="min-width:10rem">
              <template #body="slotProps">
                <Button
                  :label="$t('message.services.details')"
                  icon="pi pi-info-circle"
                  text
                  size="small"
                  @click.stop="viewServiceDetails(slotProps.data)"
                />
                <Button
                  :label="$t('message.services.changeStatus')"
                  icon="pi pi-sync"
                  text
                  size="small"
                  @click.stop="confirmChangeStatus(slotProps.data)"
                />
                <Button
                  :label="$t('message.services.viewPassphrase')"
                  icon="pi pi-key"
                  text
                  size="small"
                  @click.stop="viewPassphrase(slotProps.data)"
                  v-if="slotProps.data.service_type === 'DATA'"
                />
                <Button
                  icon="pi pi-trash"
                  text
                  rounded
                  severity="danger"
                  @click.stop="confirmDeleteService(slotProps.data)"
                />
              </template>
            </Column>
          </DataTable>
        </div>

        <!-- Remote Services Section -->
        <div class="services-section">
          <div class="section-title">
            <i class="pi pi-cloud"></i> {{ $t('message.services.remoteServices') }}
          </div>

          <!-- Search and Filter Panel -->
          <div class="search-filter-panel">
            <div class="search-box">
              <InputText
                v-model="searchQuery"
                :placeholder="$t('message.services.searchPlaceholder')"
                class="search-input"
              />
              <Button
                icon="pi pi-search"
                @click="performSearch"
                :loading="servicesStore.remoteLoading"
                class="search-button"
              />
            </div>

            <div class="filter-row">
              <div class="service-type-filter">
                <MultiSelect
                  v-model="selectedServiceTypes"
                  :options="serviceTypeOptions"
                  optionLabel="label"
                  optionValue="value"
                  :placeholder="$t('message.services.selectServiceTypes')"
                  :showToggleAll="true"
                  class="service-type-multiselect"
                />
              </div>

              <div class="peer-filter">
                <MultiSelect
                  v-model="selectedPeers"
                  :options="peerOptions"
                  optionLabel="label"
                  optionValue="value"
                  :placeholder="$t('message.services.selectPeers')"
                  :showToggleAll="true"
                  class="peer-multiselect"
                />
              </div>
            </div>
          </div>

          <div v-if="servicesStore.remoteLoading && servicesStore.remoteServices.length === 0" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
            <p style="margin-top: 1rem;">{{ $t('message.services.searchingRemoteServices') }}</p>
          </div>

          <DataTable
            v-if="servicesStore.remoteServices.length > 0"
            :value="servicesStore.remoteServices"
            :paginator="servicesStore.remoteServices.length > 10"
            :rows="10"
            class="services-table"
            :rowsPerPageOptions="[10, 20, 50, 100]"
            responsiveLayout="scroll"
          >
            <template #empty>
              <div class="empty-state">
                <i class="pi pi-inbox"></i>
                <p>{{ $t('message.services.noRemoteServices') }}</p>
              </div>
            </template>

            <Column field="name" :header="$t('message.services.serviceName')" :sortable="true"></Column>
            <Column field="description" :header="$t('message.services.serviceDescription')">
              <template #body="slotProps">
                {{ truncateDescription(slotProps.data.description) }}
              </template>
            </Column>
            <Column field="service_type" :header="$t('message.services.type')" :sortable="true">
              <template #body="slotProps">
                <Tag :value="getServiceTypeLabel(slotProps.data.service_type || slotProps.data.type)" />
              </template>
            </Column>
            <Column field="pricing" :header="$t('message.services.pricing')">
              <template #body="slotProps">
                {{ formatPricing(slotProps.data) }}
              </template>
            </Column>
            <Column field="peer_id" :header="$t('message.peers.peerId')">
              <template #body="slotProps">
                <div class="stat-value-with-copy">
                  <span class="stat-value">{{ shorten(slotProps.data.peer_id, 6, 6) }}</span>
                  <i
                    class="pi pi-copy copy-icon"
                    @click.stop="copyRemotePeerId(slotProps.data.peer_id)"
                    :title="$t('message.common.copy')"
                  ></i>
                </div>
              </template>
            </Column>
            <Column :header="$t('message.common.actions')" :exportable="false" style="min-width:8rem">
              <template #body="slotProps">
                <Button
                  :label="$t('message.services.addToWorkflow')"
                  icon="pi pi-plus"
                  text
                  size="small"
                  @click="addToWorkflow(slotProps.data)"
                />
              </template>
            </Column>
          </DataTable>
        </div>
      </div>

      <!-- Add Data Service Dialog (Legacy - for DATA only) -->
      <AddDataServiceDialog
        :visible="showAddDataServiceDialog"
        @update:visible="showAddDataServiceDialog = $event"
        @service-added="onServiceAdded"
      />

      <!-- Passphrase Dialog -->
      <Dialog
        v-model:visible="showPassphraseDialog"
        :header="$t('message.services.viewPassphrase')"
        :modal="true"
        :style="{ width: '500px' }"
      >
        <div class="passphrase-content">
          <p class="passphrase-label">{{ $t('message.services.serviceName') }}: <strong>{{ selectedService?.name }}</strong></p>
          <div class="passphrase-box">
            <code>{{ currentPassphrase }}</code>
            <Button
              icon="pi pi-copy"
              text
              rounded
              @click="copyPassphrase"
              :title="$t('message.common.copy')"
            />
          </div>
        </div>

        <template #footer>
          <Button :label="$t('message.common.close')" icon="pi pi-times" @click="showPassphraseDialog = false" />
        </template>
      </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'

import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import MultiSelect from 'primevue/multiselect'

import AppLayout from '../layout/AppLayout.vue'
import AddDataServiceDialog from './AddDataServiceDialog.vue'
import { useServicesStore } from '../../stores/services'
import { usePeersStore } from '../../stores/peers'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

const servicesStore = useServicesStore()
const peersStore = usePeersStore()
const { copyToClipboard } = useClipboard()
const { shorten } = useTextUtils()

// Search and filter state
const searchQuery = ref('')
const selectedServiceTypes = ref<string[]>([])
const selectedPeers = ref<string[]>([])

// Service type options for MultiSelect
const serviceTypeOptions = computed(() => [
  { label: t('message.services.types.data'), value: 'DATA' },
  { label: t('message.services.types.docker'), value: 'DOCKER' },
  { label: t('message.services.types.standalone'), value: 'STANDALONE' }
])

// Peer options for MultiSelect
const peerOptions = computed(() => {
  return peersStore.peers.map(peer => ({
    label: `${shorten(peer.peer_id, 6, 6)} ${peer.is_relay ? '(Relay)' : ''}`,
    value: peer.peer_id
  }))
})

// Check if we're viewing a specific peer's services
const isPeerView = computed(() => !!route.params.peerId)
const peerId = computed(() => route.params.peerId as string || '')
const shortenedPeerId = computed(() => shorten(peerId.value, 6, 6))

const pageTitle = computed(() => {
  if (isPeerView.value) {
    return t('message.services.peerServicesTitle')
  }
  return t('message.services.title')
})

// Dialog states
const showAddDataServiceDialog = ref(false)
const showSearchRemoteDialog = ref(false)
const showPassphraseDialog = ref(false)
const currentPassphrase = ref('')
const selectedService = ref<any>(null)

// Helper functions
function truncateDescription(description: string | undefined): string {
  if (!description) return '-'
  const maxLength = 60
  return description.length > maxLength
    ? description.substring(0, maxLength) + '...'
    : description
}

function getServiceTypeLabel(type: string): string {
  const typeMap: Record<string, string> = {
    'DATA': t('message.services.types.data'),
    'DOCKER': t('message.services.types.docker'),
    'STANDALONE': t('message.services.types.standalone'),
    'RELAY': t('message.services.types.relay'),
    // Legacy types
    'storage': t('message.services.types.storage'),
    'docker': t('message.services.types.docker'),
    'standalone': t('message.services.types.standalone'),
    'relay': t('message.services.types.relay')
  }
  return typeMap[type] || type
}

function getStatusLabel(status: string): string {
  const statusMap: Record<string, string> = {
    'ACTIVE': t('message.services.statuses.active'),
    'INACTIVE': t('message.services.statuses.inactive'),
    // Legacy statuses
    'available': t('message.services.statuses.available'),
    'busy': t('message.services.statuses.busy'),
    'offline': t('message.services.statuses.offline')
  }
  return statusMap[status] || status
}

function getStatusSeverity(status: string): string {
  switch (status.toUpperCase()) {
    case 'ACTIVE':
    case 'AVAILABLE':
      return 'success'
    case 'BUSY':
      return 'warning'
    case 'INACTIVE':
    case 'OFFLINE':
      return 'danger'
    default:
      return 'info'
  }
}

function formatPricing(service: any): string {
  const amount = service.pricing_amount || service.pricing || 0
  const type = service.pricing_type || 'ONE_TIME'

  if (type === 'ONE_TIME') {
    return `${amount} tokens`
  }

  const interval = service.pricing_interval || 1
  const unit = service.pricing_unit || 'MONTHS'
  const unitLabel = t(`message.services.${unit.toLowerCase()}`)

  return `${amount} tokens / ${interval} ${unitLabel}`
}

// Service actions
function confirmChangeStatus(service: any) {
  const newStatus = service.status === 'ACTIVE' ? 'INACTIVE' : 'ACTIVE'
  const message = newStatus === 'ACTIVE'
    ? `Activate service "${service.name}"?`
    : `Deactivate service "${service.name}"?`

  confirm.require({
    message,
    header: t('message.common.confirm'),
    icon: 'pi pi-question-circle',
    accept: async () => {
      try {
        await servicesStore.changeServiceStatus(service.id, newStatus)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.services.statusUpdateSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.services.statusUpdateError'),
          life: 3000
        })
      }
    }
  })
}

async function viewPassphrase(service: any) {
  try {
    selectedService.value = service
    const passphrase = await servicesStore.getServicePassphrase(service.id)
    currentPassphrase.value = passphrase
    showPassphraseDialog.value = true
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || 'Failed to retrieve passphrase',
      life: 3000
    })
  }
}

async function copyPassphrase() {
  const success = await copyToClipboard(currentPassphrase.value)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

function confirmDeleteService(service: any) {
  confirm.require({
    message: t('message.services.deleteConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await servicesStore.deleteService(service.id)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.services.deleteSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.services.deleteError'),
          life: 3000
        })
      }
    }
  })
}

function addToWorkflow(_service: any) {
  // TODO: Implement add to workflow functionality
  toast.add({
    severity: 'info',
    summary: t('message.common.info'),
    detail: 'Add to workflow functionality coming soon',
    life: 3000
  })
}

function onServiceAdded() {
  // Refresh services list after adding a new one
  servicesStore.fetchServices()
}

// Perform remote service search
function performSearch() {
  const serviceTypes = selectedServiceTypes.value.length > 0 ? selectedServiceTypes.value : []
  const peerIds = selectedPeers.value.length > 0 ? selectedPeers.value : []
  servicesStore.searchRemoteServices(searchQuery.value, serviceTypes, peerIds)
}

async function copyPeerId() {
  const success = await copyToClipboard(peerId.value)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

async function copyRemotePeerId(peerIdToCopy: string) {
  const success = await copyToClipboard(peerIdToCopy)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

// Navigate to service details page
function viewServiceDetails(service: any) {
  router.push(`/services/service/${service.id}`)
}

// Handle row click event
function onRowClick(event: any) {
  if (event.data) {
    router.push(`/services/service/${event.data.id}`)
  }
}

onMounted(async () => {
  // Only fetch local services if not viewing peer-specific services
  if (!isPeerView.value) {
    await servicesStore.fetchServices()
    // Fetch peers for the filter
    await peersStore.fetchPeers()
  }
  // TODO: Fetch peer-specific services when backend API is ready
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.services {
  position: relative;
  width: 100%;
  height: 100vh;
  overflow: auto;
  padding: 1rem;

  // Badge styling - white background, black text
  :deep(.p-badge) {
    background-color: #fff !important;
    color: #000 !important;
  }

  // Make table rows clickable with cursor pointer
  :deep(.services-table .p-datatable-tbody > tr) {
    cursor: pointer;
  }
}

.services-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: vars.$spacing-xl;

  .header-left {
    display: flex;
    align-items: center;
    gap: vars.$spacing-md;
  }

  h1 {
    color: vars.$color-primary;
    font-size: vars.$font-size-xxl;
    margin: 0;
  }
}

.services-controls {
  display: flex;
  flex-direction: row;
  flex-wrap: nowrap;
  justify-content: flex-end;
  align-content: center;
  align-items: center;
  width: 100%;
  padding-bottom: 1.5rem;
  border-bottom: 2px solid rgb(49, 64, 92);

  .services-controls-buttons {
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

.services-section {
  padding: 1rem 0;

  .section-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
    }
  }
}

.loading {
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: vars.$spacing-xl;
  text-align: center;
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

.search-filter-panel {
  margin-bottom: 1.5rem;
  padding: 1rem;
  background-color: rgb(38, 49, 65);
  border-radius: 4px;
  display: flex;
  flex-direction: column;
  gap: 1rem;

  .search-box {
    display: flex;
    gap: 0.5rem;
    align-items: center;

    .search-input {
      flex: 1;
    }

    .search-button {
      min-width: 80px;
    }
  }

  .filter-row {
    display: flex;
    gap: 1rem;
    flex-wrap: wrap;

    @media (max-width: 768px) {
      flex-direction: column;
    }
  }

  .service-type-filter {
    flex: 1;
    min-width: 250px;

    .service-type-multiselect {
      width: 100%;
    }
  }

  .peer-filter {
    flex: 1;
    min-width: 250px;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;

    .filter-label {
      font-weight: 500;
      font-size: 0.9rem;
    }

    .peer-multiselect {
      width: 100%;
    }
  }
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

.passphrase-content {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-lg;

  .passphrase-label {
    margin: 0;
    color: vars.$color-text;
  }

  .passphrase-box {
    display: flex;
    align-items: center;
    gap: vars.$spacing-md;
    padding: vars.$spacing-md;
    background-color: rgb(38, 49, 65);
    border-radius: 4px;
    border: 1px solid rgba(vars.$color-primary, 0.2);

    code {
      flex: 1;
      font-family: 'Courier New', monospace;
      font-size: vars.$font-size-md;
      color: vars.$color-primary;
      word-break: break-all;
    }
  }
}

.peer-services-section {
  background-color: rgb(38, 49, 65);
  padding: 1.5rem;
  border-radius: 4px;

  .peer-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 1.5rem;
    padding: 1rem;
    background-color: rgb(49, 64, 92);
    border-radius: 4px;

    i.pi-user {
      color: vars.$color-primary;
      font-size: 1.2rem;
    }

    span {
      color: vars.$color-text;
      font-size: vars.$font-size-md;
    }

    .peer-id {
      font-family: 'Courier New', monospace;
      font-size: vars.$font-size-sm;
      background: rgba(vars.$color-primary, 0.1);
      padding: vars.$spacing-xs;
      border-radius: vars.$border-radius-sm;
    }

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

  .info-message {
    display: flex;
    align-items: flex-start;
    gap: vars.$spacing-md;
    padding: vars.$spacing-lg;
    background-color: rgba(vars.$color-primary, 0.1);
    border-radius: vars.$border-radius-md;
    color: vars.$color-text-secondary;

    i {
      font-size: 1.5rem;
      color: vars.$color-primary;
      margin-top: 0.2rem;
    }

    p {
      margin: 0;
      line-height: 1.6;
    }
  }
}
</style>
