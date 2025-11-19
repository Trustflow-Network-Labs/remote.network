<template>
  <div class="service-details-page">
    <!-- Page Header -->
    <div class="page-header">
      <div class="header-content">
        <Button
          icon="pi pi-arrow-left"
          text
          @click="goBack"
          class="back-button"
          :label="$t('message.common.back')"
        />
        <h1>{{ $t('message.services.serviceDetails') }}</h1>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="loading-container">
      <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="error-container">
      <Message severity="error" :closable="false">
        {{ error }}
      </Message>
    </div>

    <!-- Service Details Content -->
    <div v-else-if="service" class="details-container">
      <!-- General Information Card -->
      <Card class="info-card">
        <template #title>
          <div class="card-title-with-actions">
            <span>{{ service.name }}</span>
            <div class="title-actions">
              <Tag :value="service.service_type || service.type" />
              <Tag
                :value="getStatusLabel(service.status)"
                :severity="getStatusSeverity(service.status)"
              />
            </div>
          </div>
        </template>
        <template #content>
          <div class="info-grid">
            <div class="info-item">
              <label>{{ $t('message.services.serviceDescription') }}</label>
              <p>{{ service.description || $t('message.common.noDescription') }}</p>
            </div>

            <div class="info-item">
              <label>{{ $t('message.services.pricing') }}</label>
              <p>{{ formatPricing(service) }}</p>
            </div>

            <div class="info-item">
              <label>{{ $t('message.common.createdAt') }}</label>
              <p>{{ formatDate(service.created_at) }}</p>
            </div>

            <div class="info-item" v-if="service.updated_at">
              <label>{{ $t('message.common.updatedAt') }}</label>
              <p>{{ formatDate(service.updated_at) }}</p>
            </div>
          </div>
        </template>
      </Card>

      <!-- Data Service Details Card (only for DATA type) -->
      <Card v-if="service.service_type === 'DATA' && dataDetails" class="info-card">
        <template #title>
          <i class="pi pi-database"></i> {{ $t('message.services.dataServiceDetails') }}
        </template>
        <template #content>
          <div class="info-grid">
            <div class="info-item">
              <label>{{ $t('message.services.files') }}</label>
              <p>{{ dataDetails.file_path }}</p>
            </div>

            <div class="info-item">
              <label>{{ $t('message.services.originalSize') }}</label>
              <p>{{ formatBytes(dataDetails.original_size_bytes) }}</p>
            </div>

            <div class="info-item">
              <label>{{ $t('message.services.compressedSize') }}</label>
              <p>{{ formatBytes(dataDetails.size_bytes) }} ({{ compressionRatio }}%)</p>
            </div>

            <div class="info-item">
              <label>{{ $t('message.services.compressionType') }}</label>
              <p>{{ dataDetails.compression_type }}</p>
            </div>

            <div class="info-item full-width">
              <label>{{ $t('message.services.fileHash') }}</label>
              <div class="hash-container">
                <code class="hash-text">{{ dataDetails.hash }}</code>
                <Button
                  icon="pi pi-copy"
                  text
                  rounded
                  size="small"
                  @click="copyToClipboard(dataDetails.hash)"
                  :title="$t('message.common.copy')"
                />
              </div>
            </div>

            <div class="info-item full-width" v-if="passphrase">
              <label>{{ $t('message.services.passphrase') }}</label>
              <div class="passphrase-container">
                <code v-if="showPassphrase" class="passphrase-text">{{ passphrase }}</code>
                <code v-else class="passphrase-text">••••••••••••••••••••••••••••••••</code>
                <Button
                  :icon="showPassphrase ? 'pi pi-eye-slash' : 'pi pi-eye'"
                  text
                  rounded
                  size="small"
                  @click="togglePassphrase"
                  :title="showPassphrase ? $t('message.common.hide') : $t('message.common.show')"
                />
                <Button
                  icon="pi pi-copy"
                  text
                  rounded
                  size="small"
                  @click="copyToClipboard(passphrase)"
                  :title="$t('message.common.copy')"
                />
              </div>
            </div>
          </div>
        </template>
      </Card>

      <!-- Interfaces Card -->
      <Card v-if="serviceInterfaces.length > 0" class="info-card">
        <template #title>
          <i class="pi pi-link"></i> {{ $t('message.services.interfaces') }}
        </template>
        <template #content>
          <div class="interfaces-list">
            <div
              v-for="(iface, index) in serviceInterfaces"
              :key="index"
              class="interface-item"
            >
              <div class="interface-badge" :class="`badge-${iface.interface_type.toLowerCase()}`">
                {{ iface.interface_type }}
              </div>
              <div class="interface-details">
                <div v-if="iface.path" class="interface-path">
                  <code>{{ iface.path }}</code>
                </div>
                <div v-if="iface.description" class="interface-description">
                  {{ iface.description }}
                </div>
              </div>
            </div>
          </div>
        </template>
      </Card>

      <!-- Actions Card -->
      <Card class="actions-card">
        <template #title>
          <i class="pi pi-cog"></i> {{ $t('message.common.actions') }}
        </template>
        <template #content>
          <div class="actions-buttons">
            <Button
              :label="$t('message.services.changeStatus')"
              icon="pi pi-sync"
              @click="confirmChangeStatus"
              outlined
            />
            <Button
              :label="$t('message.common.delete')"
              icon="pi pi-trash"
              severity="danger"
              @click="confirmDeleteService"
              outlined
            />
          </div>
        </template>
      </Card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import ProgressSpinner from 'primevue/progressspinner'
import Message from 'primevue/message'
import { api } from '../services/api'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

// State
const loading = ref(true)
const error = ref<string | null>(null)
const service = ref<any>(null)
const dataDetails = ref<any>(null)
const passphrase = ref<string | null>(null)
const showPassphrase = ref(false)
const serviceInterfaces = ref<any[]>([])

// Computed
const compressionRatio = computed(() => {
  if (!dataDetails.value) return '0'
  const ratio = (dataDetails.value.size_bytes / dataDetails.value.original_size_bytes) * 100
  return ratio.toFixed(1)
})

// Methods
const goBack = () => {
  router.push('/services')
}

const fetchServiceDetails = async () => {
  try {
    loading.value = true
    error.value = null

    const serviceId = Number(route.params.id)
    const response = await api.getService(serviceId)

    if (response && response.service) {
      service.value = response.service

      // Fetch service interfaces
      try {
        const interfacesResponse = await api.getServiceInterfaces(serviceId)
        if (interfacesResponse && interfacesResponse.interfaces) {
          serviceInterfaces.value = interfacesResponse.interfaces
        }
      } catch (err) {
        console.error('Failed to fetch service interfaces:', err)
      }

      // Fetch additional details for DATA services
      if (service.value.service_type === 'DATA') {
        try {
          const passphraseResponse = await api.getServicePassphrase(serviceId)
          if (passphraseResponse) {
            passphrase.value = passphraseResponse.passphrase
            dataDetails.value = passphraseResponse.data_details
          }
        } catch (err) {
          console.error('Failed to fetch data details:', err)
        }
      }
    }
  } catch (err: any) {
    console.error('Failed to fetch service details:', err)
    error.value = err.response?.data?.error || t('message.services.errors.fetchFailed')
  } finally {
    loading.value = false
  }
}

const togglePassphrase = () => {
  showPassphrase.value = !showPassphrase.value
}

const copyToClipboard = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    toast.add({
      severity: 'success',
      summary: t('message.common.copied'),
      detail: t('message.common.copiedToClipboard'),
      life: 3000
    })
  } catch (err) {
    console.error('Failed to copy:', err)
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: t('message.common.copyFailed'),
      life: 3000
    })
  }
}

const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 Bytes'
  const k = 1024
  const sizes = ['Bytes', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i]
}

const formatDate = (dateString: string): string => {
  return new Date(dateString).toLocaleString()
}

const formatPricing = (svc: any): string => {
  if (!svc.pricing_amount) return t('message.services.free')

  const amount = svc.pricing_amount
  const type = svc.pricing_type || 'ONE_TIME'
  const tokenLabel = amount === 1 ? 'token' : 'tokens'

  if (type === 'ONE_TIME') {
    return `${amount} ${tokenLabel}`
  }

  const interval = svc.pricing_interval || 1
  const unit = svc.pricing_unit || 'MONTHS'
  const unitStr = interval > 1 ? `${interval} ${unit.toLowerCase()}` : unit.toLowerCase().slice(0, -1)

  return `${amount} ${tokenLabel}/${unitStr}`
}

const getStatusLabel = (status: string): string => {
  return status || 'UNKNOWN'
}

const getStatusSeverity = (status: string): 'success' | 'warn' | 'danger' | 'info' => {
  switch (status) {
    case 'ACTIVE':
      return 'success'
    case 'INACTIVE':
      return 'warn'
    case 'ERROR':
      return 'danger'
    default:
      return 'info'
  }
}

const confirmChangeStatus = () => {
  const newStatus = service.value.status === 'ACTIVE' ? 'INACTIVE' : 'ACTIVE'

  confirm.require({
    message: t('message.services.confirmChangeStatusMessage', { status: newStatus }),
    header: t('message.services.confirmChangeStatusTitle'),
    icon: 'pi pi-exclamation-triangle',
    accept: async () => {
      try {
        await api.updateServiceStatus(service.value.id, newStatus)

        service.value.status = newStatus

        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.services.statusChanged'),
          life: 3000
        })
      } catch (err: any) {
        console.error('Failed to change status:', err)
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: err.response?.data?.error || t('message.services.errors.statusChangeFailed'),
          life: 3000
        })
      }
    }
  })
}

const confirmDeleteService = () => {
  confirm.require({
    message: t('message.services.confirmDeleteMessage', { name: service.value.name }),
    header: t('message.services.confirmDeleteTitle'),
    icon: 'pi pi-exclamation-triangle',
    accept: async () => {
      try {
        await api.deleteService(service.value.id)

        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.services.serviceDeleted'),
          life: 3000
        })

        router.push('/services')
      } catch (err: any) {
        console.error('Failed to delete service:', err)
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: err.response?.data?.error || t('message.services.errors.deleteFailed'),
          life: 3000
        })
      }
    }
  })
}

// Lifecycle
onMounted(() => {
  fetchServiceDetails()
})
</script>

<style scoped lang="scss">
@use '../scss/variables' as vars;

.service-details-page {
  padding: vars.$spacing-lg;
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  margin-bottom: vars.$spacing-xl;

  .header-content {
    display: flex;
    align-items: center;
    gap: vars.$spacing-md;

    h1 {
      font-size: vars.$font-size-xxl;
      font-weight: 600;
      color: vars.$color-text;
      margin: 0;
    }

    .back-button {
      color: vars.$color-primary;
    }
  }
}

.loading-container,
.error-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 400px;
}

.details-container {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-lg;
}

.info-card,
.actions-card {
  :deep(.p-card-title) {
    display: flex;
    align-items: center;
    gap: vars.$spacing-sm;
    color: vars.$color-text;
  }

  .card-title-with-actions {
    display: flex;
    justify-content: space-between;
    align-items: center;
    width: 100%;

    .title-actions {
      display: flex;
      gap: vars.$spacing-sm;
    }
  }
}

.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: vars.$spacing-lg;

  .info-item {
    display: flex;
    flex-direction: column;
    gap: vars.$spacing-xs;

    &.full-width {
      grid-column: 1 / -1;
    }

    label {
      font-weight: 600;
      color: vars.$color-text-secondary;
      font-size: vars.$font-size-sm;
      text-transform: uppercase;
    }

    p {
      color: vars.$color-text;
      margin: 0;
      word-break: break-word;
    }

    code {
      background: vars.$color-surface;
      padding: vars.$spacing-xs vars.$spacing-sm;
      border-radius: vars.$border-radius-sm;
      color: vars.$color-primary;
      font-family: 'Courier New', monospace;
      font-size: vars.$font-size-sm;
      word-break: break-all;
    }

    .hash-container,
    .passphrase-container {
      display: flex;
      align-items: center;
      gap: vars.$spacing-sm;
      background: vars.$color-surface;
      padding: vars.$spacing-sm;
      border-radius: vars.$border-radius-sm;

      .hash-text,
      .passphrase-text {
        flex: 1;
        background: transparent;
        padding: 0;
      }
    }
  }
}

.actions-buttons {
  display: flex;
  gap: vars.$spacing-md;
  flex-wrap: wrap;
}

.interfaces-list {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-md;

  .interface-item {
    display: flex;
    align-items: flex-start;
    gap: vars.$spacing-md;
    padding: vars.$spacing-md;
    background: vars.$color-surface;
    border-radius: vars.$border-radius-sm;
    border-left: 3px solid vars.$color-primary;

    .interface-badge {
      padding: vars.$spacing-xs vars.$spacing-sm;
      border-radius: vars.$border-radius-sm;
      font-size: vars.$font-size-sm;
      font-weight: 600;
      text-transform: uppercase;
      white-space: nowrap;

      &.badge-stdin {
        background: rgba(59, 130, 246, 0.2);
        color: rgb(59, 130, 246);
      }

      &.badge-stdout {
        background: rgba(34, 197, 94, 0.2);
        color: rgb(34, 197, 94);
      }

      &.badge-stderr {
        background: rgba(239, 68, 68, 0.2);
        color: rgb(239, 68, 68);
      }

      &.badge-logs {
        background: rgba(168, 85, 247, 0.2);
        color: rgb(168, 85, 247);
      }

      &.badge-mount {
        background: rgba(251, 146, 60, 0.2);
        color: rgb(251, 146, 60);
      }
    }

    .interface-details {
      flex: 1;

      .interface-path {
        margin-bottom: vars.$spacing-xs;

        code {
          background: rgba(vars.$color-primary, 0.1);
          padding: vars.$spacing-xs vars.$spacing-sm;
          border-radius: vars.$border-radius-sm;
          color: vars.$color-primary;
          font-family: 'Courier New', monospace;
          font-size: vars.$font-size-sm;
        }
      }

      .interface-description {
        color: vars.$color-text-secondary;
        font-size: vars.$font-size-sm;
      }
    }
  }
}
</style>
