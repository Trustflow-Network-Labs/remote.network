<template>
  <AppLayout>
    <div class="invoices">
      <!-- Header Actions -->
      <div class="header-actions">
        <h2>{{ $t('message.invoices.title', 'Payment Invoices') }}</h2>
        <div class="actions">
          <Button
            label="Create Invoice"
            icon="pi pi-plus"
            @click="showCreateDialog = true"
            severity="success"
          />
          <Button
            label="Refresh"
            icon="pi pi-refresh"
            @click="refreshInvoices"
            :loading="invoicesStore.loading"
          />
        </div>
      </div>

      <!-- Filter Tabs -->
      <div class="filter-section">
        <Tabs :value="activeTab" @update:value="activeTab = Number($event)">
          <TabList>
            <Tab :value="0">All ({{ invoiceCount.total }})</Tab>
            <Tab :value="1">Pending ({{ invoiceCount.pending }})</Tab>
            <Tab :value="2">Settled ({{ invoiceCount.settled }})</Tab>
            <Tab :value="3">Failed ({{ invoiceCount.failed }})</Tab>
            <Tab :value="4">Expired ({{ invoiceCount.expired }})</Tab>
          </TabList>
        </Tabs>
      </div>

      <div v-if="invoicesStore.loading && !filteredInvoices.length" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <div v-else class="invoices-table-section">
        <DataTable
          :value="filteredInvoices"
          :paginator="filteredInvoices.length > 10"
          :rows="10"
          class="invoices-table"
          :rowsPerPageOptions="[10, 20, 50]"
          responsiveLayout="scroll"
          sortField="created_at"
          :sortOrder="-1"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-inbox"></i>
              <p>No invoices found.</p>
            </div>
          </template>

          <Column field="invoice_id" header="Invoice ID" :sortable="true">
            <template #body="slotProps">
              <div class="id-with-copy">
                <span>{{ shortenId(slotProps.data.invoice_id) }}</span>
                <i class="pi pi-copy copy-icon" @click="copyText(slotProps.data.invoice_id)"></i>
              </div>
            </template>
          </Column>

          <Column field="direction" header="Direction" :sortable="true">
            <template #body="slotProps">
              <Tag
                :value="getDirection(slotProps.data)"
                :severity="getDirection(slotProps.data) === 'Sent' ? 'info' : 'success'"
              />
            </template>
          </Column>

          <Column field="from_peer_id" header="From" :sortable="true">
            <template #body="slotProps">
              {{ shortenId(slotProps.data.from_peer_id) }}
            </template>
          </Column>

          <Column field="to_peer_id" header="To" :sortable="true">
            <template #body="slotProps">
              {{ shortenId(slotProps.data.to_peer_id) }}
            </template>
          </Column>

          <Column field="amount" header="Amount" :sortable="true">
            <template #body="slotProps">
              <strong>{{ formatAmount(slotProps.data.amount) }}</strong> {{ slotProps.data.currency }}
            </template>
          </Column>

          <Column field="network" header="Network" :sortable="true">
            <template #body="slotProps">
              <Tag :value="getNetworkDisplay(slotProps.data.network)" severity="secondary" />
            </template>
          </Column>

          <Column field="status" header="Status" :sortable="true">
            <template #body="slotProps">
              <Tag
                :value="slotProps.data.status.toUpperCase()"
                :severity="getStatusSeverity(slotProps.data.status)"
              />
            </template>
          </Column>

          <Column field="created_at" header="Created" :sortable="true">
            <template #body="slotProps">
              {{ formatDate(slotProps.data.created_at) }}
            </template>
          </Column>

          <Column header="Actions">
            <template #body="slotProps">
              <div class="action-buttons">
                <Button
                  v-if="slotProps.data.status === 'pending' && getDirection(slotProps.data) === 'Received'"
                  label="Accept"
                  icon="pi pi-check"
                  size="small"
                  severity="success"
                  @click="openAcceptDialog(slotProps.data)"
                />
                <Button
                  v-if="slotProps.data.status === 'pending' && getDirection(slotProps.data) === 'Received'"
                  label="Reject"
                  icon="pi pi-times"
                  size="small"
                  severity="danger"
                  @click="openRejectDialog(slotProps.data)"
                />
                <Button
                  v-if="slotProps.data.status === 'failed' && getDirection(slotProps.data) === 'Sent'"
                  label="Resend"
                  icon="pi pi-refresh"
                  size="small"
                  severity="warning"
                  @click="openResendDialog(slotProps.data)"
                  :loading="resending"
                />
                <Button
                  v-if="(slotProps.data.status === 'failed' || slotProps.data.status === 'expired' || slotProps.data.status === 'rejected') && getDirection(slotProps.data) === 'Sent'"
                  label="Delete"
                  icon="pi pi-trash"
                  size="small"
                  severity="danger"
                  @click="openDeleteDialog(slotProps.data)"
                  :loading="deleting"
                  outlined
                />
                <Button
                  icon="pi pi-info-circle"
                  size="small"
                  severity="info"
                  @click="viewDetails(slotProps.data)"
                  text
                />
              </div>
            </template>
          </Column>
        </DataTable>
      </div>

      <!-- Create Invoice Dialog -->
      <Dialog v-model:visible="showCreateDialog" header="Create Payment Invoice" :style="{width: '600px'}" modal>
        <div class="dialog-content">
          <table class="form-table">
            <tbody>
              <tr>
                <td class="label-cell">Recipient Peer ID *</td>
                <td class="input-cell">
                  <InputText
                    v-model="createForm.toPeerID"
                    placeholder="Enter recipient peer ID"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Network *</td>
                <td class="input-cell">
                  <Select
                    v-model="createForm.network"
                    :options="networkOptions"
                    optionLabel="label"
                    optionValue="value"
                    placeholder="Select Network"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Your Wallet *</td>
                <td class="input-cell">
                  <Select
                    v-model="createForm.fromWalletId"
                    :options="createWalletOptions"
                    optionLabel="label"
                    optionValue="value"
                    placeholder="Select wallet to receive payment"
                    class="full-width"
                    :disabled="!createForm.network"
                  />
                  <small v-if="!createForm.network" class="field-remark">Select a network first</small>
                  <small v-else-if="createWalletOptions.length === 0" class="field-remark text-warning">
                    No wallets found for {{ createForm.network }}. Create one first.
                  </small>
                </td>
              </tr>
              <tr>
                <td class="label-cell">Amount *</td>
                <td class="input-cell">
                  <InputNumber
                    v-model="createForm.amount"
                    mode="decimal"
                    :minFractionDigits="2"
                    :maxFractionDigits="6"
                    placeholder="0.000000"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Currency *</td>
                <td class="input-cell">
                  <InputText
                    v-model="createForm.currency"
                    placeholder="USDC (ERC-3009 required)"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Description</td>
                <td class="input-cell">
                  <Textarea
                    v-model="createForm.description"
                    rows="3"
                    placeholder="Optional description"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Expires In (hours)</td>
                <td class="input-cell">
                  <InputNumber
                    v-model="createForm.expiresInHours"
                    :min="1"
                    :max="168"
                    class="full-width"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showCreateDialog = false" text />
          <Button
            label="Create Invoice"
            icon="pi pi-check"
            @click="createInvoice"
            :loading="creating"
            :disabled="!isCreateFormValid"
          />
        </template>
      </Dialog>

      <!-- Accept Invoice Dialog -->
      <Dialog v-model:visible="showAcceptDialog" header="Accept Payment Invoice" :style="{width: '500px'}" modal>
        <div class="dialog-content" v-if="selectedInvoice">
          <Message severity="info" style="margin-bottom: 1rem;">
            You are about to accept this invoice and make a payment.
          </Message>
          <div class="invoice-details">
            <div><strong>Amount:</strong> {{ selectedInvoice.amount }} {{ selectedInvoice.currency }}</div>
            <div><strong>Network:</strong> {{ selectedInvoice.network }}</div>
            <div><strong>Description:</strong> {{ selectedInvoice.description || 'N/A' }}</div>
          </div>
          <table class="form-table">
            <tbody>
              <tr>
                <td class="label-cell">Select Wallet *</td>
                <td class="input-cell">
                  <Select
                    v-model="acceptForm.walletId"
                    :options="compatibleWallets"
                    optionLabel="label"
                    optionValue="value"
                    placeholder="Choose wallet"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Passphrase</td>
                <td class="input-cell">
                  <Password
                    v-model="acceptForm.passphrase"
                    placeholder="Leave empty to use stored passphrase"
                    toggleMask
                    :feedback="false"
                    class="full-width"
                  />
                  <small class="field-remark">
                    <i class="pi pi-info-circle"></i>
                    If left empty, will use the passphrase stored in your keystore when the wallet was created.
                  </small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showAcceptDialog = false" text />
          <Button
            label="Accept & Pay"
            icon="pi pi-check"
            severity="success"
            @click="acceptInvoice"
            :loading="accepting"
            :disabled="!acceptForm.walletId"
          />
        </template>
      </Dialog>

      <!-- Reject Invoice Dialog -->
      <Dialog v-model:visible="showRejectDialog" header="Reject Invoice" :style="{width: '500px'}" modal>
        <div class="dialog-content">
          <table class="form-table">
            <tbody>
              <tr>
                <td class="label-cell">Reason</td>
                <td class="input-cell">
                  <Textarea
                    v-model="rejectForm.reason"
                    rows="3"
                    placeholder="Why are you rejecting this invoice? (optional)"
                    class="full-width"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showRejectDialog = false" text />
          <Button
            label="Reject"
            icon="pi pi-times"
            severity="danger"
            @click="rejectInvoice"
            :loading="rejecting"
          />
        </template>
      </Dialog>

      <!-- Delete Invoice Dialog -->
      <Dialog v-model:visible="showDeleteDialog" header="Delete Invoice" :style="{width: '400px'}" modal>
        <div class="dialog-content">
          <Message severity="warn" :closable="false">
            Are you sure you want to delete this invoice? This action cannot be undone.
          </Message>
          <div v-if="selectedInvoice" class="invoice-summary">
            <p><strong>Invoice ID:</strong> {{ shortenId(selectedInvoice.invoice_id) }}</p>
            <p><strong>Amount:</strong> {{ selectedInvoice.amount }} {{ selectedInvoice.currency }}</p>
            <p><strong>Status:</strong> {{ selectedInvoice.status.toUpperCase() }}</p>
          </div>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showDeleteDialog = false" text />
          <Button
            label="Delete"
            icon="pi pi-trash"
            severity="danger"
            @click="deleteInvoice"
            :loading="deleting"
          />
        </template>
      </Dialog>

      <!-- Resend Invoice Dialog -->
      <Dialog v-model:visible="showResendDialog" header="Resend Invoice" :style="{width: '400px'}" modal>
        <div class="dialog-content">
          <Message severity="info" :closable="false">
            This will delete the failed invoice and create a new one with the same details.
          </Message>
          <div v-if="selectedInvoice" class="invoice-summary">
            <p><strong>Recipient:</strong> {{ shortenId(selectedInvoice.to_peer_id) }}</p>
            <p><strong>Amount:</strong> {{ selectedInvoice.amount }} {{ selectedInvoice.currency }}</p>
            <p><strong>Network:</strong> {{ getNetworkDisplay(selectedInvoice.network) }}</p>
            <p><strong>New Expiration:</strong> 24 hours from now</p>
          </div>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showResendDialog = false" text />
          <Button
            label="Resend"
            icon="pi pi-refresh"
            severity="warning"
            @click="resendInvoice"
            :loading="resending"
          />
        </template>
      </Dialog>

      <!-- Invoice Details Dialog -->
      <Dialog v-model:visible="showDetailsDialog" header="Invoice Details" :style="{width: '500px'}" modal>
        <div class="invoice-details-full" v-if="selectedInvoice">
          <div class="detail-row">
            <strong>Invoice ID:</strong>
            <span>{{ selectedInvoice.invoice_id }}</span>
          </div>
          <div class="detail-row">
            <strong>From:</strong>
            <span>{{ selectedInvoice.from_peer_id }}</span>
          </div>
          <div class="detail-row">
            <strong>To:</strong>
            <span>{{ selectedInvoice.to_peer_id }}</span>
          </div>
          <div class="detail-row">
            <strong>Amount:</strong>
            <span>{{ selectedInvoice.amount }} {{ selectedInvoice.currency }}</span>
          </div>
          <div class="detail-row">
            <strong>Network:</strong>
            <span>{{ selectedInvoice.network }}</span>
          </div>
          <div class="detail-row">
            <strong>Status:</strong>
            <Tag :value="selectedInvoice.status.toUpperCase()" :severity="getStatusSeverity(selectedInvoice.status)" />
          </div>
          <div class="detail-row">
            <strong>Description:</strong>
            <span>{{ selectedInvoice.description || 'N/A' }}</span>
          </div>
          <div class="detail-row">
            <strong>Created:</strong>
            <span>{{ formatDateFull(selectedInvoice.created_at) }}</span>
          </div>
          <div class="detail-row" v-if="selectedInvoice.expires_at">
            <strong>Expires:</strong>
            <span>{{ formatDateFull(selectedInvoice.expires_at) }}</span>
          </div>
          <div class="detail-row" v-if="selectedInvoice.accepted_at">
            <strong>Accepted:</strong>
            <span>{{ formatDateFull(selectedInvoice.accepted_at) }}</span>
          </div>
          <div class="detail-row" v-if="selectedInvoice.settled_at">
            <strong>Settled:</strong>
            <span>{{ formatDateFull(selectedInvoice.settled_at) }}</span>
          </div>
        </div>
        <template #footer>
          <Button label="Close" icon="pi pi-times" @click="showDetailsDialog = false" />
        </template>
      </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useInvoicesStore } from '../../stores/invoices'
import { useWalletsStore } from '../../stores/wallets'
import { useAuthStore } from '../../stores/auth'
import { useToast } from 'primevue/usetoast'
import { api } from '../../services/api'
import AppLayout from '../layout/AppLayout.vue'
import Button from 'primevue/button'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Dialog from 'primevue/dialog'
import Select from 'primevue/select'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Password from 'primevue/password'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import ProgressSpinner from 'primevue/progressspinner'

const invoicesStore = useInvoicesStore()
const walletsStore = useWalletsStore()
const authStore = useAuthStore()
const toast = useToast()

// State
const activeTab = ref(0)
const showCreateDialog = ref(false)
const showAcceptDialog = ref(false)
const showRejectDialog = ref(false)
const showDetailsDialog = ref(false)
const showDeleteDialog = ref(false)
const showResendDialog = ref(false)
const creating = ref(false)
const accepting = ref(false)
const rejecting = ref(false)
const deleting = ref(false)
const resending = ref(false)
const selectedInvoice = ref<any>(null)

// Forms
const createForm = ref({
  toPeerID: '',
  fromWalletId: '',
  amount: 0,
  currency: 'USDC',
  network: '',
  description: '',
  expiresInHours: 24
})

const acceptForm = ref({ walletId: '', passphrase: '' })
const rejectForm = ref({ reason: '' })

// Network options - fetched from API (intersection of facilitator and app supported networks)
const networkOptions = ref<Array<{ label: string; value: string }>>([])
const loadingNetworks = ref(false)

// Computed
const filteredInvoices = computed(() => {
  const allInvoices = invoicesStore.invoices
  const tabIndex = activeTab.value

  if (tabIndex === 0) return allInvoices
  if (tabIndex === 1) return allInvoices.filter(i => i.status === 'pending')
  if (tabIndex === 2) return allInvoices.filter(i => i.status === 'settled')
  if (tabIndex === 3) return allInvoices.filter(i => i.status === 'failed')
  if (tabIndex === 4) return allInvoices.filter(i => i.status === 'expired')

  return allInvoices
})

const isCreateFormValid = computed(() => {
  return createForm.value.toPeerID &&
         createForm.value.fromWalletId &&
         createForm.value.amount > 0 &&
         createForm.value.currency &&
         createForm.value.network
})

const invoiceCount = computed(() => {
  const invoices = invoicesStore.invoices
  return {
    total: invoices.length,
    pending: invoices.filter(i => i.status === 'pending').length,
    settled: invoices.filter(i => i.status === 'settled').length,
    failed: invoices.filter(i => i.status === 'failed').length,
    expired: invoices.filter(i => i.status === 'expired').length
  }
})

const compatibleWallets = computed(() => {
  if (!selectedInvoice.value) return []
  return walletsStore.wallets
    .filter(w => w.network === selectedInvoice.value.network)
    .map(w => ({
      label: `${w.address.substring(0, 8)}... (${w.network})`,
      value: w.wallet_id
    }))
})

const createWalletOptions = computed(() => {
  if (!createForm.value.network) return []
  return walletsStore.wallets
    .filter(w => w.network === createForm.value.network)
    .map(w => ({
      label: `${w.address.substring(0, 8)}... (${w.network})`,
      value: w.wallet_id
    }))
})

// Methods
const refreshInvoices = async () => {
  await invoicesStore.fetchInvoices()
}

const fetchAllowedNetworks = async () => {
  loadingNetworks.value = true
  try {
    const data = await api.getAllowedNetworks()
    if (data.success && data.networks) {
      // Map API response to dropdown format
      networkOptions.value = data.networks.map((net: { display_name: string; caip2: string }) => ({
        label: net.display_name,
        value: net.caip2
      }))
    }
  } catch (error) {
    console.error('Error fetching allowed networks:', error)
    toast.add({
      severity: 'warn',
      summary: 'Network Options',
      detail: 'Could not load available networks. Using defaults.',
      life: 3000
    })
    // Fallback to default networks if API call fails
    networkOptions.value = [
      { label: 'Base Mainnet', value: 'eip155:8453' },
      { label: 'Base Sepolia', value: 'eip155:84532' },
      { label: 'Solana Mainnet', value: 'solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp' },
      { label: 'Solana Devnet', value: 'solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1' }
    ]
  } finally {
    loadingNetworks.value = false
  }
}

const createInvoice = async () => {
  creating.value = true
  try {
    const response = await invoicesStore.createInvoice(
      createForm.value.toPeerID,
      createForm.value.fromWalletId,
      createForm.value.amount,
      createForm.value.currency,
      createForm.value.network,
      createForm.value.description,
      createForm.value.expiresInHours
    )

    // Check if delivery failed but invoice was created
    if (!response.success && response.invoice_id) {
      toast.add({
        severity: 'warn',
        summary: 'Invoice Created (Delivery Failed)',
        detail: 'Invoice saved but could not be delivered to recipient. You can resend it from the Failed tab.',
        life: 6000
      })
    } else {
      toast.add({ severity: 'success', summary: 'Invoice Created', detail: 'Invoice delivered successfully', life: 3000 })
    }

    showCreateDialog.value = false
    createForm.value = {
      toPeerID: '',
      fromWalletId: '',
      amount: 0,
      currency: 'USDC',
      network: '',
      description: '',
      expiresInHours: 24
    }
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to create invoice', detail: error.message, life: 5000 })
  } finally {
    creating.value = false
  }
}

const openAcceptDialog = (invoice: any) => {
  selectedInvoice.value = invoice
  acceptForm.value = { walletId: '', passphrase: '' }
  showAcceptDialog.value = true
}

const acceptInvoice = async () => {
  if (!selectedInvoice.value) return
  accepting.value = true
  try {
    await invoicesStore.acceptInvoice(
      selectedInvoice.value.invoice_id,
      acceptForm.value.walletId,
      acceptForm.value.passphrase
    )
    toast.add({ severity: 'success', summary: 'Invoice Accepted', detail: 'Payment is being processed', life: 3000 })
    showAcceptDialog.value = false
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to accept invoice', detail: error.message, life: 5000 })
  } finally {
    accepting.value = false
  }
}

const openRejectDialog = (invoice: any) => {
  selectedInvoice.value = invoice
  rejectForm.value = { reason: '' }
  showRejectDialog.value = true
}

const rejectInvoice = async () => {
  if (!selectedInvoice.value) return
  rejecting.value = true
  try {
    await invoicesStore.rejectInvoice(selectedInvoice.value.invoice_id, rejectForm.value.reason)
    toast.add({ severity: 'info', summary: 'Invoice Rejected', life: 3000 })
    showRejectDialog.value = false
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to reject invoice', detail: error.message, life: 5000 })
  } finally {
    rejecting.value = false
  }
}

const openDeleteDialog = (invoice: any) => {
  selectedInvoice.value = invoice
  showDeleteDialog.value = true
}

const deleteInvoice = async () => {
  if (!selectedInvoice.value) return
  deleting.value = true
  try {
    await invoicesStore.deleteInvoice(selectedInvoice.value.invoice_id)
    toast.add({ severity: 'success', summary: 'Invoice Deleted', detail: 'Invoice removed successfully', life: 3000 })
    showDeleteDialog.value = false
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to delete invoice', detail: error.message, life: 5000 })
  } finally {
    deleting.value = false
  }
}

const openResendDialog = (invoice: any) => {
  selectedInvoice.value = invoice
  showResendDialog.value = true
}

const resendInvoice = async () => {
  if (!selectedInvoice.value) return
  resending.value = true
  try {
    const response = await invoicesStore.resendInvoice(selectedInvoice.value.invoice_id)
    toast.add({ severity: 'success', summary: 'Invoice Resent', detail: `New invoice ID: ${response.new_invoice_id}`, life: 5000 })
    showResendDialog.value = false
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to resend invoice', detail: error.message, life: 5000 })
  } finally {
    resending.value = false
  }
}

const viewDetails = (invoice: any) => {
  selectedInvoice.value = invoice
  showDetailsDialog.value = true
}

const getDirection = (invoice: any) => {
  return invoice.from_peer_id === authStore.peerId ? 'Sent' : 'Received'
}

const getStatusSeverity = (status: string) => {
  const map: any = {
    pending: 'warning',
    accepted: 'info',
    settled: 'success',
    rejected: 'danger',
    failed: 'danger',
    expired: 'secondary'
  }
  return map[status] || 'info'
}

const getNetworkDisplay = (network: string) => {
  if (network.includes('8453')) return 'Base'
  if (network.includes('84532')) return 'Base Sepolia'
  if (network.includes('mainnet')) return 'Solana'
  if (network.includes('devnet')) return 'Solana Devnet'
  return network
}

const shortenId = (id: string) => {
  if (!id) return ''
  return `${id.substring(0, 8)}...${id.substring(id.length - 6)}`
}

const copyText = (text: string) => {
  navigator.clipboard.writeText(text)
  toast.add({ severity: 'info', summary: 'Copied to clipboard', life: 2000 })
}

const formatDate = (timestamp: number) => {
  return new Date(timestamp * 1000).toLocaleDateString()
}

const formatDateFull = (timestamp: number) => {
  return new Date(timestamp * 1000).toLocaleString()
}

const formatAmount = (amount: any) => {
  const num = Number(amount)
  return isNaN(num) ? '0.000000' : num.toFixed(6)
}

// Lifecycle
onMounted(async () => {
  await Promise.all([
    refreshInvoices(),
    walletsStore.fetchWallets(),
    fetchAllowedNetworks()
  ])
  invoicesStore.subscribeToUpdates()
})

onUnmounted(() => {
  invoicesStore.unsubscribeFromUpdates()
})
</script>

<style scoped lang="scss">
.invoices {
  padding: 2rem;

  .header-actions {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 2rem;

    h2 {
      margin: 0;
    }

    .actions {
      display: flex;
      gap: 0.5rem;
    }
  }

  .filter-section {
    margin-bottom: 1.5rem;
  }

  .loading {
    display: flex;
    justify-content: center;
    padding: 4rem;
  }

  .invoices-table-section {
    .action-buttons {
      display: flex;
      gap: 0.5rem;
    }

    .id-with-copy {
      display: flex;
      align-items: center;
      gap: 0.5rem;

      .copy-icon {
        cursor: pointer;
        opacity: 0.6;

        &:hover {
          opacity: 1;
        }
      }
    }
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 4rem;
    color: #6c757d;

    i {
      font-size: 4rem;
      margin-bottom: 1rem;
    }
  }

  .dialog-content {
    .form-table {
      width: 100%;
      border-collapse: separate;
      border-spacing: 0;

      tbody {
        tr {
          vertical-align: top;
        }

        tr:not(:last-child) {
          .input-cell {
            padding-bottom: 1rem;
          }
        }
      }

      .label-cell {
        vertical-align: top;
        padding-right: 1rem;
        padding-top: 0.6rem;
        font-weight: 600;
        white-space: nowrap;
        min-width: 160px;
      }

      .input-cell {
        vertical-align: top;
        width: 100%;

        .full-width {
          width: 100%;
        }

        // Force PrimeVue Password to display as flex block (maintains internal flex layout but forces new line)
        :deep(.p-password) {
          display: flex !important;
          width: 100% !important;
          position: relative;
        }

        // Force other PrimeVue components to take full width
        :deep(.p-select),
        :deep(.p-inputtext),
        :deep(.p-inputnumber),
        :deep(.p-textarea) {
          display: block !important;
          width: 100% !important;
        }

        small.field-remark {
          display: block !important;
          margin-top: 0.5rem;
          color: #6c757d;
          font-size: 0.875rem;
          line-height: 1.4;
          width: 100%;
          clear: both;

          i {
            font-size: 0.8rem;
            margin-right: 4px;
          }
        }

        .text-warning {
          color: #ffa500;
        }
      }
    }

    .form-field {
      margin-bottom: 1.5rem;

      label {
        display: block;
        margin-bottom: 0.5rem;
        font-weight: 600;
      }

      .full-width {
        width: 100%;
      }
    }

    .invoice-details {
      margin: 1.5rem 0;
      padding: 1rem;
      background: #f8f9fa;
      border-radius: 0.5rem;

      div {
        margin-bottom: 0.5rem;

        &:last-child {
          margin-bottom: 0;
        }
      }
    }
  }

  .invoice-details-full {
    .detail-row {
      display: flex;
      justify-content: space-between;
      padding: 0.75rem;
      border-bottom: 1px solid #e9ecef;

      &:last-child {
        border-bottom: none;
      }

      strong {
        color: #6c757d;
        min-width: 120px;
      }

      span {
        text-align: right;
        word-break: break-all;
      }
    }
  }
}
</style>

<style lang="scss">
// Global styles for dialogs (they render outside scoped component)
.p-dialog {
  .dialog-content {
    .form-table {
      width: 100%;
      border-collapse: separate;
      border-spacing: 0;

      tbody {
        tr {
          vertical-align: top;
        }

        tr:not(:last-child) {
          .input-cell {
            padding-bottom: 1rem;
          }
        }
      }

      .label-cell {
        vertical-align: top;
        padding-right: 1rem;
        padding-top: 0.6rem;
        font-weight: 600;
        white-space: nowrap;
        min-width: 160px;
      }

      .input-cell {
        vertical-align: top;
        width: 100%;

        .full-width {
          width: 100%;
        }

        // Force PrimeVue Password and Select to display as flex block (maintains internal layout)
        .p-password,
        .p-select {
          display: flex !important;
          width: 100% !important;
          position: relative;
        }

        // Force other PrimeVue components to take full width
        .p-inputtext,
        .p-inputnumber,
        .p-textarea {
          display: block !important;
          width: 100% !important;
        }

        small.field-remark {
          display: block !important;
          margin-top: 0.5rem;
          color: #6c757d;
          font-size: 0.875rem;
          line-height: 1.4;
          width: 100%;
          clear: both;

          i {
            font-size: 0.8rem;
            margin-right: 4px;
          }
        }

        .text-warning {
          color: #ffa500;
        }
      }
    }
  }
}
</style>
