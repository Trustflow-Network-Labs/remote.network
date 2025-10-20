<template>
  <AppLayout>
    <div class="configuration">
      <div class="configuration-header">
        <h1>{{ $t('message.configuration.title') }}</h1>
      </div>

    <TabView>
      <!-- Services Tab -->
      <TabPanel :header="$t('message.configuration.services')">
        <div class="services-section">
          <div class="section-header">
            <h2>{{ $t('message.services.title') }}</h2>
            <Button
              :label="$t('message.services.add')"
              icon="pi pi-plus"
              @click="showAddServiceDialog = true"
              severity="success"
            />
          </div>

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
      </TabPanel>

      <!-- Blacklist Tab -->
      <TabPanel :header="$t('message.configuration.blacklist')">
        <div class="blacklist-section">
          <div class="section-header">
            <h2>{{ $t('message.configuration.blacklist') }}</h2>
            <Button
              :label="$t('message.configuration.addToBlacklist')"
              icon="pi pi-ban"
              @click="showAddBlacklistDialog = true"
              severity="danger"
            />
          </div>

          <div v-if="peersStore.loading" class="loading">
            <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
          </div>

          <DataTable
            v-else
            :value="peersStore.blacklist"
            :paginator="true"
            :rows="10"
            class="blacklist-table"
            :rowsPerPageOptions="[5, 10, 25]"
            responsiveLayout="scroll"
          >
            <template #empty>
              <div class="empty-state">
                <i class="pi pi-inbox"></i>
                <p>{{ $t('message.common.noData') }}</p>
              </div>
            </template>

            <Column field="peer_id" :header="$t('message.peers.peerId')" :sortable="true">
              <template #body="slotProps">
                <code class="peer-id">{{ slotProps.data.peer_id }}</code>
              </template>
            </Column>
            <Column field="reason" header="Reason" :sortable="true"></Column>
            <Column field="blacklisted_at" :header="$t('message.peers.lastSeen')" :sortable="true">
              <template #body="slotProps">
                {{ formatDate(slotProps.data.blacklisted_at) }}
              </template>
            </Column>
            <Column :exportable="false" style="min-width:8rem">
              <template #body="slotProps">
                <Button
                  icon="pi pi-check"
                  text
                  rounded
                  severity="success"
                  @click="confirmUnblacklistPeer(slotProps.data)"
                  :label="$t('message.configuration.removeFromBlacklist')"
                />
              </template>
            </Column>
          </DataTable>
        </div>
      </TabPanel>
    </TabView>

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

    <!-- Add to Blacklist Dialog -->
    <Dialog
      v-model:visible="showAddBlacklistDialog"
      :header="$t('message.configuration.addToBlacklist')"
      :modal="true"
      :style="{ width: '30vw' }"
      :breakpoints="{ '960px': '50vw', '640px': '90vw' }"
    >
      <div class="blacklist-form">
        <div class="field">
          <label for="peer-id">{{ $t('message.peers.peerId') }}</label>
          <InputText id="peer-id" v-model="blacklistForm.peer_id" class="w-full" />
        </div>
        <div class="field">
          <label for="blacklist-reason">Reason</label>
          <Textarea id="blacklist-reason" v-model="blacklistForm.reason" rows="3" class="w-full" />
        </div>
      </div>

      <template #footer>
        <Button :label="$t('message.common.cancel')" icon="pi pi-times" text @click="showAddBlacklistDialog = false" />
        <Button :label="$t('message.common.add')" icon="pi pi-ban" severity="danger" @click="addToBlacklist" />
      </template>
    </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'

import TabView from 'primevue/tabview'
import TabPanel from 'primevue/tabpanel'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Dropdown from 'primevue/dropdown'
import Textarea from 'primevue/textarea'

import AppLayout from '../layout/AppLayout.vue'
import { useServicesStore } from '../../stores/services'
import { usePeersStore } from '../../stores/peers'

const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

const servicesStore = useServicesStore()
const peersStore = usePeersStore()

const showAddServiceDialog = ref(false)
const showEditServiceDialog = ref(false)
const showAddBlacklistDialog = ref(false)

const serviceForm = reactive({
  id: null as number | null,
  name: '',
  type: 'storage',
  endpoint: '',
  pricing: 0
})

const blacklistForm = reactive({
  peer_id: '',
  reason: ''
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

function formatDate(dateString: string): string {
  const date = new Date(dateString)
  return date.toLocaleString()
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

async function addToBlacklist() {
  try {
    await peersStore.addToBlacklist(blacklistForm.peer_id, blacklistForm.reason)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.configuration.blacklistSuccess'),
      life: 3000
    })
    showAddBlacklistDialog.value = false
    resetBlacklistForm()
  } catch (error) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: t('message.configuration.blacklistError'),
      life: 3000
    })
  }
}

function confirmUnblacklistPeer(peer: any) {
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

function resetBlacklistForm() {
  blacklistForm.peer_id = ''
  blacklistForm.reason = ''
}

onMounted(async () => {
  await servicesStore.fetchServices()
  await peersStore.fetchBlacklist()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.configuration {
  min-height: 100vh;
  padding: vars.$spacing-lg;
}

.configuration-header {
  margin-bottom: vars.$spacing-xl;

  h1 {
    color: vars.$color-primary;
    font-size: vars.$font-size-xxl;
    margin: 0;
  }
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: vars.$spacing-lg;

  h2 {
    color: vars.$color-text;
    font-size: vars.$font-size-xl;
    margin: 0;
  }
}

.services-section,
.blacklist-section {
  // Background handled by PrimeVue TabView
  padding: vars.$spacing-lg;
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

.peer-id {
  font-family: 'Courier New', monospace;
  font-size: vars.$font-size-sm;
  background: rgba(vars.$color-primary, 0.1);
  padding: vars.$spacing-xs;
  border-radius: vars.$border-radius-sm;
}

.service-form,
.blacklist-form {
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
</style>
