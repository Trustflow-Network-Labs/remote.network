<template>
  <AppLayout>
    <div class="services">
      <div class="services-header">
        <div class="header-left">
          <Button
            v-if="isPeerView"
            icon="pi pi-arrow-left"
            text
            rounded
            @click="router.push('/peers')"
            :title="$t('message.common.back')"
          />
          <h1>{{ pageTitle }}</h1>
        </div>
        <Button
          v-if="!isPeerView"
          :label="$t('message.services.add')"
          icon="pi pi-plus"
          @click="showAddServiceDialog = true"
          severity="success"
        />
      </div>

      <!-- Peer-specific view -->
      <div v-if="isPeerView" class="peer-services-section">
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

      <!-- Local services view -->
      <div v-else>
        <div v-if="servicesStore.loading" class="loading">
          <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
        </div>

        <DataTable
          v-else
          :value="servicesStore.services"
          :paginator="true"
          :rows="10"
          class="services-table"
          :rowsPerPageOptions="[5, 10, 25]"
          responsiveLayout="scroll"
        >
        <template #empty>
          <div class="empty-state">
            <i class="pi pi-inbox"></i>
            <p>{{ $t('message.common.noData') }}</p>
          </div>
        </template>

        <Column field="name" :header="$t('message.workflows.name')" :sortable="true"></Column>
        <Column field="type" :header="$t('message.services.type')" :sortable="true">
          <template #body="slotProps">
            <Tag :value="$t(`message.services.types.${slotProps.data.type}`)" />
          </template>
        </Column>
        <Column field="endpoint" :header="$t('message.services.endpoint')"></Column>
        <Column field="status" :header="$t('message.services.status')" :sortable="true">
          <template #body="slotProps">
            <Tag
              :value="$t(`message.services.statuses.${slotProps.data.status}`)"
              :severity="getStatusSeverity(slotProps.data.status)"
            />
          </template>
        </Column>
        <Column field="pricing" :header="$t('message.services.pricing')">
          <template #body="slotProps">
            {{ slotProps.data.pricing }} tokens
          </template>
        </Column>
        <Column :exportable="false" style="min-width:8rem">
          <template #body="slotProps">
            <Button
              icon="pi pi-pencil"
              text
              rounded
              severity="info"
              @click="editService(slotProps.data)"
            />
            <Button
              icon="pi pi-trash"
              text
              rounded
              severity="danger"
              @click="confirmDeleteService(slotProps.data)"
            />
          </template>
        </Column>
      </DataTable>
      </div>

      <!-- Add Service Dialog -->
      <Dialog
        v-model:visible="showAddServiceDialog"
        :header="$t('message.services.add')"
        :modal="true"
        :style="{ width: '50vw' }"
        :breakpoints="{ '960px': '75vw', '640px': '100vw' }"
      >
        <div class="service-form">
          <div class="field">
            <label for="service-name">{{ $t('message.workflows.name') }}</label>
            <InputText id="service-name" v-model="serviceForm.name" class="w-full" />
          </div>
          <div class="field">
            <label for="service-type">{{ $t('message.services.type') }}</label>
            <Dropdown
              id="service-type"
              v-model="serviceForm.type"
              :options="serviceTypes"
              optionLabel="label"
              optionValue="value"
              class="w-full"
            />
          </div>
          <div class="field">
            <label for="service-endpoint">{{ $t('message.services.endpoint') }}</label>
            <InputText id="service-endpoint" v-model="serviceForm.endpoint" class="w-full" />
          </div>
          <div class="field">
            <label for="service-pricing">{{ $t('message.services.pricing') }}</label>
            <InputNumber id="service-pricing" v-model="serviceForm.pricing" class="w-full" />
          </div>
        </div>

        <template #footer>
          <Button :label="$t('message.common.cancel')" icon="pi pi-times" text @click="showAddServiceDialog = false" />
          <Button :label="$t('message.common.save')" icon="pi pi-check" @click="addService" />
        </template>
      </Dialog>

      <!-- Edit Service Dialog -->
      <Dialog
        v-model:visible="showEditServiceDialog"
        :header="$t('message.services.edit')"
        :modal="true"
        :style="{ width: '50vw' }"
        :breakpoints="{ '960px': '75vw', '640px': '100vw' }"
      >
        <div class="service-form">
          <div class="field">
            <label for="edit-service-name">{{ $t('message.workflows.name') }}</label>
            <InputText id="edit-service-name" v-model="serviceForm.name" class="w-full" />
          </div>
          <div class="field">
            <label for="edit-service-type">{{ $t('message.services.type') }}</label>
            <Dropdown
              id="edit-service-type"
              v-model="serviceForm.type"
              :options="serviceTypes"
              optionLabel="label"
              optionValue="value"
              class="w-full"
            />
          </div>
          <div class="field">
            <label for="edit-service-endpoint">{{ $t('message.services.endpoint') }}</label>
            <InputText id="edit-service-endpoint" v-model="serviceForm.endpoint" class="w-full" />
          </div>
          <div class="field">
            <label for="edit-service-pricing">{{ $t('message.services.pricing') }}</label>
            <InputNumber id="edit-service-pricing" v-model="serviceForm.pricing" class="w-full" />
          </div>
        </div>

        <template #footer>
          <Button :label="$t('message.common.cancel')" icon="pi pi-times" text @click="showEditServiceDialog = false" />
          <Button :label="$t('message.common.save')" icon="pi pi-check" @click="updateService" />
        </template>
      </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
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
import InputNumber from 'primevue/inputnumber'
import Dropdown from 'primevue/dropdown'

import AppLayout from '../layout/AppLayout.vue'
import { useServicesStore } from '../../stores/services'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

const servicesStore = useServicesStore()
const { copyToClipboard } = useClipboard()
const { shorten } = useTextUtils()

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

const showAddServiceDialog = ref(false)
const showEditServiceDialog = ref(false)

const serviceForm = reactive({
  id: null as number | null,
  name: '',
  type: 'storage',
  endpoint: '',
  pricing: 0
})

const serviceTypes = [
  { label: t('message.services.types.storage'), value: 'storage' },
  { label: t('message.services.types.docker'), value: 'docker' },
  { label: t('message.services.types.standalone'), value: 'standalone' },
  { label: t('message.services.types.relay'), value: 'relay' }
]

function getStatusSeverity(status: string): string {
  switch (status) {
    case 'available': return 'success'
    case 'busy': return 'warning'
    case 'offline': return 'danger'
    default: return 'info'
  }
}

function editService(service: any) {
  serviceForm.id = service.id
  serviceForm.name = service.name
  serviceForm.type = service.type
  serviceForm.endpoint = service.endpoint
  serviceForm.pricing = service.pricing
  showEditServiceDialog.value = true
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

async function addService() {
  try {
    await servicesStore.addService({
      name: serviceForm.name,
      type: serviceForm.type,
      endpoint: serviceForm.endpoint,
      pricing: serviceForm.pricing,
      status: 'available',
      capabilities: {}
    })
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.services.addSuccess'),
      life: 3000
    })
    showAddServiceDialog.value = false
    resetServiceForm()
  } catch (error) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: t('message.services.addError'),
      life: 3000
    })
  }
}

async function updateService() {
  if (!serviceForm.id) return

  try {
    await servicesStore.updateService(serviceForm.id, {
      name: serviceForm.name,
      type: serviceForm.type,
      endpoint: serviceForm.endpoint,
      pricing: serviceForm.pricing,
      status: 'available',
      capabilities: {}
    })
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.services.updateSuccess'),
      life: 3000
    })
    showEditServiceDialog.value = false
    resetServiceForm()
  } catch (error) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: t('message.services.updateError'),
      life: 3000
    })
  }
}

function resetServiceForm() {
  serviceForm.id = null
  serviceForm.name = ''
  serviceForm.type = 'storage'
  serviceForm.endpoint = ''
  serviceForm.pricing = 0
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

onMounted(async () => {
  // Only fetch local services if not viewing peer-specific services
  if (!isPeerView.value) {
    await servicesStore.fetchServices()
  }
  // TODO: Fetch peer-specific services when backend API is ready
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.services {
  min-height: 100vh;
  padding: vars.$spacing-lg;
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

.loading {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: vars.$spacing-xl;
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

.service-form {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-lg;

  .field {
    display: flex;
    flex-direction: column;
    gap: vars.$spacing-sm;

    label {
      font-weight: 500;
      color: vars.$color-text;
    }
  }
}

.w-full {
  width: 100%;
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
