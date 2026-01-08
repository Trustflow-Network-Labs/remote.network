<template>
  <AppLayout>
    <div class="wallets">
      <!-- Header Actions -->
      <div class="header-actions">
        <h2>{{ $t('message.wallets.title', 'Wallets') }}</h2>
        <div class="actions">
          <Button
            label="Create Wallet"
            icon="pi pi-plus"
            @click="showCreateDialog = true"
            severity="success"
          />
          <Button
            label="Import Wallet"
            icon="pi pi-upload"
            @click="showImportDialog = true"
            severity="info"
          />
          <Button
            label="Refresh"
            icon="pi pi-refresh"
            @click="refreshWallets"
            :loading="walletsStore.loading"
          />
        </div>
      </div>

      <div v-if="walletsStore.loading && !wallets.length" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <div v-else class="wallets-grid">
        <Card v-for="wallet in wallets" :key="wallet.wallet_id" class="wallet-card">
          <template #header>
            <div class="wallet-header">
              <div class="header-badges">
                <div class="network-badge" :class="`network-${getNetworkName(wallet.network)}`">
                  {{ getNetworkName(wallet.network).toUpperCase() }}
                </div>
                <div v-if="wallet.is_default" class="default-badge">
                  DEFAULT
                </div>
              </div>
              <Select
                :options="getWalletActions(wallet)"
                optionLabel="label"
                placeholder="Actions"
                @change="handleWalletAction($event, wallet)"
              >
                <template #value>
                  <i class="pi pi-ellipsis-v"></i>
                </template>
              </Select>
            </div>
          </template>
          <template #title>
            <div class="wallet-address">
              {{ shortenAddress(wallet.address) }}
              <i class="pi pi-copy copy-icon" @click="copyAddress(wallet.address)"></i>
            </div>
          </template>
          <template #content>
            <div class="wallet-info">
              <div class="balance-section">
                <div class="balance-header">
                  <div class="balance-label">Balance</div>
                  <i
                    class="pi pi-refresh reload-icon"
                    @click="loadBalance(wallet.wallet_id)"
                    :class="{ 'pi-spin': walletsStore.balanceLoading.get(wallet.wallet_id) }"
                  ></i>
                </div>
                <div class="balance-value">
                  <template v-if="wallet.balance">
                    {{ wallet.balance.balance.toFixed(6) }} {{ wallet.balance.currency }}
                  </template>
                  <template v-else>
                    <ProgressSpinner style="width:20px;height:20px" strokeWidth="4" />
                  </template>
                </div>
              </div>
              <div class="wallet-meta">
                <div><strong>Network:</strong> {{ wallet.network }}</div>
                <div><strong>Created:</strong> {{ formatDate(wallet.created_at) }}</div>
              </div>
            </div>
          </template>
        </Card>

        <div v-if="!wallets.length" class="empty-state">
          <i class="pi pi-wallet"></i>
          <p>No wallets found. Create or import a wallet to get started.</p>
        </div>
      </div>

      <!-- Create Wallet Dialog -->
      <Dialog v-model:visible="showCreateDialog" header="Create New Wallet" :style="{width: '500px'}" modal>
        <div class="dialog-content">
          <table class="form-table">
            <tbody>
              <tr>
                <td class="label-cell">Network</td>
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
                <td class="label-cell">Passphrase</td>
                <td class="input-cell">
                  <Password
                    v-model="createForm.passphrase"
                    placeholder="Enter passphrase"
                    toggleMask
                    :feedback="false"
                    class="full-width"
                  />
                  <small class="field-remark">
                    <i class="pi pi-lock"></i>
                    Used to encrypt the wallet. Will be stored securely in your keystore for autonomous payments (workflows, jobs).
                  </small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showCreateDialog = false" text />
          <Button
            label="Create"
            icon="pi pi-check"
            @click="createWallet"
            :loading="creating"
            :disabled="!createForm.network || !createForm.passphrase"
          />
        </template>
      </Dialog>

      <!-- Import Wallet Dialog -->
      <Dialog v-model:visible="showImportDialog" header="Import Wallet" :style="{width: '500px'}" modal>
        <div class="dialog-content">
          <table class="form-table">
            <tbody>
              <tr>
                <td class="label-cell">Private Key</td>
                <td class="input-cell">
                  <Textarea
                    v-model="importForm.privateKey"
                    placeholder="Enter private key (hex)"
                    rows="3"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Network</td>
                <td class="input-cell">
                  <Select
                    v-model="importForm.network"
                    :options="networkOptions"
                    optionLabel="label"
                    optionValue="value"
                    placeholder="Select Network"
                    class="full-width"
                  />
                </td>
              </tr>
              <tr>
                <td class="label-cell">Passphrase</td>
                <td class="input-cell">
                  <Password
                    v-model="importForm.passphrase"
                    placeholder="Enter passphrase"
                    toggleMask
                    :feedback="false"
                    class="full-width"
                  />
                  <small class="field-remark">
                    <i class="pi pi-lock"></i>
                    Will be stored securely in your keystore for autonomous payments.
                  </small>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showImportDialog = false" text />
          <Button
            label="Import"
            icon="pi pi-upload"
            @click="importWallet"
            :loading="importing"
            :disabled="!importForm.privateKey || !importForm.network || !importForm.passphrase"
          />
        </template>
      </Dialog>

      <!-- Delete Wallet Dialog -->
      <Dialog v-model:visible="showDeleteDialog" header="Delete Wallet" :style="{width: '450px'}" modal>
        <div class="dialog-content">
          <Message severity="warn">
            This action cannot be undone. Make sure you have backed up your private key!
          </Message>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showDeleteDialog = false" text />
          <Button
            label="Delete"
            icon="pi pi-trash"
            @click="deleteWallet"
            :loading="deleting"
            severity="danger"
          />
        </template>
      </Dialog>

      <!-- Export Wallet Dialog -->
      <Dialog v-model:visible="showExportDialog" header="Export Private Key" :style="{width: '500px'}" modal>
        <div class="dialog-content">
          <Message severity="warn" style="margin-bottom: 1rem;">
            Never share your private key! Anyone with access to it can control your funds.
          </Message>
          <table class="form-table" v-if="!exportedKey">
            <tbody>
              <tr>
                <td class="label-cell">Passphrase *</td>
                <td class="input-cell">
                  <Password
                    v-model="exportForm.passphrase"
                    placeholder="Enter passphrase to confirm"
                    toggleMask
                    :feedback="false"
                    class="full-width"
                  />
                  <small class="field-remark">
                    <i class="pi pi-shield"></i>
                    Required for security when exporting private keys.
                  </small>
                </td>
              </tr>
            </tbody>
          </table>
          <table class="form-table" v-if="exportedKey">
            <tbody>
              <tr>
                <td class="label-cell">Private Key</td>
                <td class="input-cell">
                  <div class="key-display">
                    <code>{{ exportedKey }}</code>
                    <i class="pi pi-copy copy-icon" @click="copyText(exportedKey)"></i>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <template #footer>
          <Button label="Close" icon="pi pi-times" @click="closeExportDialog" text />
          <Button
            v-if="!exportedKey"
            label="Export"
            icon="pi pi-key"
            @click="exportWallet"
            :loading="exporting"
            :disabled="!exportForm.passphrase"
          />
        </template>
      </Dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useWalletsStore } from '../../stores/wallets'
import { useToast } from 'primevue/usetoast'
import { api } from '../../services/api'
import AppLayout from '../layout/AppLayout.vue'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Dialog from 'primevue/dialog'
import Select from 'primevue/select'
import Password from 'primevue/password'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'

const walletsStore = useWalletsStore()
const toast = useToast()

// State
const showCreateDialog = ref(false)
const showImportDialog = ref(false)
const showDeleteDialog = ref(false)
const showExportDialog = ref(false)
const creating = ref(false)
const importing = ref(false)
const deleting = ref(false)
const exporting = ref(false)
const exportedKey = ref('')
const selectedWallet = ref<any>(null)

// Forms
const createForm = ref({ network: '', passphrase: '' })
const importForm = ref({ privateKey: '', network: '', passphrase: '' })
const deleteForm = ref({})
const exportForm = ref({ passphrase: '' })

// Auto-refresh interval
let balanceRefreshInterval: ReturnType<typeof setInterval> | null = null

// Network options - fetched from API (intersection of facilitator and app supported networks)
const networkOptions = ref<Array<{ label: string; value: string }>>([])
const loadingNetworks = ref(false)

// Computed
const wallets = computed(() => walletsStore.wallets)

// Get wallet actions based on whether it's default or not
const getWalletActions = (wallet: any) => {
  const actions = []

  // Show "Set as Default" only if not already default
  if (!wallet.is_default) {
    actions.push({ label: 'Set as Default', value: 'set_default', icon: 'pi pi-star' })
  }

  actions.push({ label: 'Export Private Key', value: 'export', icon: 'pi pi-key' })
  actions.push({ label: 'Delete Wallet', value: 'delete', icon: 'pi pi-trash' })

  return actions
}

// Methods
const refreshWallets = async () => {
  await walletsStore.fetchWallets()
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

const loadBalance = async (walletId: string) => {
  try {
    await walletsStore.refreshBalance(walletId)
    toast.add({ severity: 'success', summary: 'Balance Updated', life: 3000 })
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to load balance', detail: error.message, life: 5000 })
  }
}

const createWallet = async () => {
  creating.value = true
  try {
    await walletsStore.createWallet(createForm.value.network, createForm.value.passphrase)
    toast.add({ severity: 'success', summary: 'Wallet Created', life: 3000 })
    showCreateDialog.value = false
    createForm.value = { network: '', passphrase: '' }
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to create wallet', detail: error.message, life: 5000 })
  } finally {
    creating.value = false
  }
}

const importWallet = async () => {
  importing.value = true
  try {
    await walletsStore.importWallet(
      importForm.value.privateKey,
      importForm.value.network,
      importForm.value.passphrase
    )
    toast.add({ severity: 'success', summary: 'Wallet Imported', life: 3000 })
    showImportDialog.value = false
    importForm.value = { privateKey: '', network: '', passphrase: '' }
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Failed to import wallet', detail: error.message, life: 5000 })
  } finally {
    importing.value = false
  }
}

const handleWalletAction = async (event: any, wallet: any) => {
  selectedWallet.value = wallet
  if (event.value.value === 'export') {
    showExportDialog.value = true
  } else if (event.value.value === 'delete') {
    showDeleteDialog.value = true
  } else if (event.value.value === 'set_default') {
    await setAsDefault(wallet.wallet_id)
  }
}

const setAsDefault = async (walletId: string) => {
  try {
    await walletsStore.setDefaultWallet(walletId)
    toast.add({
      severity: 'success',
      summary: 'Default Wallet Updated',
      detail: 'Wallet has been set as default',
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Failed to set default wallet',
      detail: error.message,
      life: 5000
    })
  }
}

const exportWallet = async () => {
  if (!selectedWallet.value) return
  exporting.value = true
  try {
    const result = await walletsStore.exportWallet(
      selectedWallet.value.wallet_id,
      exportForm.value.passphrase
    )
    exportedKey.value = result.private_key
    toast.add({ severity: 'success', summary: 'Private Key Exported', life: 3000 })
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Export failed', detail: error.message, life: 5000 })
  } finally {
    exporting.value = false
  }
}

const closeExportDialog = () => {
  showExportDialog.value = false
  exportForm.value = { passphrase: '' }
  exportedKey.value = ''
}

const deleteWallet = async () => {
  if (!selectedWallet.value) return
  deleting.value = true
  try {
    await walletsStore.deleteWallet(selectedWallet.value.wallet_id, '')
    toast.add({ severity: 'success', summary: 'Wallet Deleted', life: 3000 })
    showDeleteDialog.value = false
    deleteForm.value = {}
    selectedWallet.value = null
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Delete failed', detail: error.message, life: 5000 })
  } finally {
    deleting.value = false
  }
}

const loadAllBalances = async () => {
  const walletIds = wallets.value.map((w: any) => w.wallet_id)
  for (const walletId of walletIds) {
    try {
      await walletsStore.refreshBalance(walletId)
    } catch (error) {
      console.error(`Failed to load balance for wallet ${walletId}:`, error)
    }
  }
}

const startBalanceAutoRefresh = () => {
  // Initial load
  loadAllBalances()

  // Set up 10-second interval
  balanceRefreshInterval = setInterval(() => {
    loadAllBalances()
  }, 10000)
}

const stopBalanceAutoRefresh = () => {
  if (balanceRefreshInterval) {
    clearInterval(balanceRefreshInterval)
    balanceRefreshInterval = null
  }
}

const shortenAddress = (address: string) => {
  if (!address) return ''
  return `${address.substring(0, 6)}...${address.substring(address.length - 4)}`
}

const copyAddress = (address: string) => {
  navigator.clipboard.writeText(address)
  toast.add({ severity: 'info', summary: 'Copied to clipboard', life: 2000 })
}

const copyText = (text: string) => {
  navigator.clipboard.writeText(text)
  toast.add({ severity: 'info', summary: 'Copied to clipboard', life: 2000 })
}

const getNetworkName = (network: string) => {
  if (network.includes('base')) return 'base'
  if (network.includes('solana')) return 'solana'
  if (network.includes('8453')) return 'base'
  if (network.includes('84532')) return 'base-sepolia'
  return 'unknown'
}

const formatDate = (timestamp: number) => {
  return new Date(timestamp * 1000).toLocaleDateString()
}

// Lifecycle
onMounted(async () => {
  await Promise.all([
    refreshWallets(),
    fetchAllowedNetworks()
  ])
  walletsStore.subscribeToUpdates()

  // Start auto-refresh for balances
  startBalanceAutoRefresh()
})

onUnmounted(() => {
  walletsStore.unsubscribeFromUpdates()
  stopBalanceAutoRefresh()
})
</script>

<style scoped lang="scss">
.wallets {
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

  .loading {
    display: flex;
    justify-content: center;
    padding: 4rem;
  }

  .wallets-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(350px, 1fr));
    gap: 1.5rem;
  }

  .wallet-card {
    .wallet-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 1rem;
      background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);

      .header-badges {
        display: flex;
        gap: 0.5rem;
        align-items: center;
      }

      .network-badge {
        padding: 0.25rem 0.75rem;
        border-radius: 1rem;
        font-size: 0.75rem;
        font-weight: 600;
        color: white;

        &.network-base {
          background: #0052FF;
        }

        &.network-base-sepolia {
          background: #FF6B35;
        }

        &.network-solana {
          background: #14F195;
          color: #000;
        }
      }

      .default-badge {
        padding: 0.25rem 0.75rem;
        border-radius: 1rem;
        font-size: 0.65rem;
        font-weight: 700;
        background: #FFD700;
        color: #000;
        letter-spacing: 0.5px;
        box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
      }
    }

    .wallet-address {
      font-family: monospace;
      font-size: 1.1rem;
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

    .wallet-info {
      .balance-section {
        margin-bottom: 1.5rem;
        padding: 1rem;
        background: #f8f9fa;
        border-radius: 0.5rem;

        .balance-header {
          display: flex;
          justify-content: space-between;
          align-items: center;
          margin-bottom: 0.5rem;

          .balance-label {
            font-size: 0.875rem;
            color: #6c757d;
          }

          .reload-icon {
            cursor: pointer;
            color: #6c757d;
            font-size: 1rem;
            padding: 0.25rem;
            transition: all 0.2s;

            &:hover {
              color: #2c3e50;
              transform: scale(1.1);
            }

            &.pi-spin {
              animation: spin 1s linear infinite;
            }
          }
        }

        .balance-value {
          font-size: 1.5rem;
          font-weight: 600;
          color: #2c3e50;
        }
      }

      .wallet-meta {
        display: flex;
        flex-direction: column;
        gap: 0.5rem;
        font-size: 0.875rem;
        color: #6c757d;
      }
    }
  }

  .empty-state {
    grid-column: 1 / -1;
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
        min-width: 140px;
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
      }
    }

    .form-field {
      margin-bottom: 1.5rem;

      label {
        display: block;
        margin-bottom: 0.5rem;
        font-weight: 600;
      }

      small {
        display: block;
        margin-top: 0.25rem;
        color: #6c757d;
      }

      .full-width {
        width: 100%;
      }
    }

    .key-display {
      display: flex;
      align-items: center;
      gap: 0.5rem;
      padding: 1rem;
      background: #f8f9fa;
      border-radius: 0.5rem;
      word-break: break-all;

      code {
        flex: 1;
        font-family: monospace;
      }

      .copy-icon {
        cursor: pointer;
        opacity: 0.6;

        &:hover {
          opacity: 1;
        }
      }
    }
  }
}

@keyframes spin {
  0% {
    transform: rotate(0deg);
  }
  100% {
    transform: rotate(360deg);
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
        min-width: 140px;
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
      }
    }
  }
}
</style>
