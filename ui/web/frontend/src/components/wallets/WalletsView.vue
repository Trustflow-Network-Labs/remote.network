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
              <div class="network-badge" :class="`network-${getNetworkName(wallet.network)}`">
                {{ getNetworkName(wallet.network).toUpperCase() }}
              </div>
              <Select
                :options="walletActions"
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
                <div class="balance-label">Balance</div>
                <div class="balance-value" v-if="wallet.balance">
                  {{ wallet.balance.balance.toFixed(6) }} {{ wallet.balance.currency }}
                </div>
                <Button
                  v-else
                  label="Load Balance"
                  icon="pi pi-sync"
                  size="small"
                  @click="loadBalance(wallet.wallet_id)"
                  :loading="walletsStore.balanceLoading.get(wallet.wallet_id)"
                />
              </div>
              <div class="wallet-meta">
                <div><strong>ID:</strong> {{ wallet.wallet_id.substring(0, 8) }}...</div>
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
      <Dialog v-model:visible="showCreateDialog" header="Create New Wallet" :style="{width: '450px'}" modal>
        <div class="dialog-content">
          <div class="form-field">
            <label>Network</label>
            <Select
              v-model="createForm.network"
              :options="networkOptions"
              optionLabel="label"
              optionValue="value"
              placeholder="Select Network"
              class="full-width"
            />
          </div>
          <div class="form-field">
            <label>Passphrase</label>
            <Password
              v-model="createForm.passphrase"
              placeholder="Enter passphrase"
              toggleMask
              :feedback="false"
              class="full-width"
            />
            <small>Used to encrypt the wallet. Keep it safe!</small>
          </div>
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
      <Dialog v-model:visible="showImportDialog" header="Import Wallet" :style="{width: '450px'}" modal>
        <div class="dialog-content">
          <div class="form-field">
            <label>Private Key</label>
            <Textarea
              v-model="importForm.privateKey"
              placeholder="Enter private key (hex)"
              rows="3"
              class="full-width"
            />
          </div>
          <div class="form-field">
            <label>Network</label>
            <Select
              v-model="importForm.network"
              :options="networkOptions"
              optionLabel="label"
              optionValue="value"
              placeholder="Select Network"
              class="full-width"
            />
          </div>
          <div class="form-field">
            <label>Passphrase</label>
            <Password
              v-model="importForm.passphrase"
              placeholder="Enter passphrase"
              toggleMask
              :feedback="false"
              class="full-width"
            />
          </div>
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
          <div class="form-field">
            <label>Passphrase</label>
            <Password
              v-model="deleteForm.passphrase"
              placeholder="Enter passphrase to confirm"
              toggleMask
              :feedback="false"
              class="full-width"
            />
          </div>
        </div>
        <template #footer>
          <Button label="Cancel" icon="pi pi-times" @click="showDeleteDialog = false" text />
          <Button
            label="Delete"
            icon="pi pi-trash"
            @click="deleteWallet"
            :loading="deleting"
            severity="danger"
            :disabled="!deleteForm.passphrase"
          />
        </template>
      </Dialog>

      <!-- Export Wallet Dialog -->
      <Dialog v-model:visible="showExportDialog" header="Export Private Key" :style="{width: '450px'}" modal>
        <div class="dialog-content">
          <Message severity="warn">
            Never share your private key! Anyone with access to it can control your funds.
          </Message>
          <div class="form-field" v-if="!exportedKey">
            <label>Passphrase</label>
            <Password
              v-model="exportForm.passphrase"
              placeholder="Enter passphrase"
              toggleMask
              :feedback="false"
              class="full-width"
            />
          </div>
          <div class="form-field" v-if="exportedKey">
            <label>Private Key</label>
            <div class="key-display">
              <code>{{ exportedKey }}</code>
              <i class="pi pi-copy copy-icon" @click="copyText(exportedKey)"></i>
            </div>
          </div>
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
import AppLayout from '../layout/AppLayout.vue'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Dialog from 'primevue/dialog'
import Select from 'primevue/select'
import InputText from 'primevue/inputtext'
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
const deleteForm = ref({ passphrase: '' })
const exportForm = ref({ passphrase: '' })

// Network options
const networkOptions = [
  { label: 'Base Mainnet', value: 'eip155:8453' },
  { label: 'Base Sepolia', value: 'eip155:84532' },
  { label: 'Solana Mainnet', value: 'solana:mainnet-beta' },
  { label: 'Solana Devnet', value: 'solana:devnet' }
]

// Wallet actions
const walletActions = [
  { label: 'Export Private Key', value: 'export', icon: 'pi pi-key' },
  { label: 'Delete Wallet', value: 'delete', icon: 'pi pi-trash' }
]

// Computed
const wallets = computed(() => walletsStore.wallets)

// Methods
const refreshWallets = async () => {
  await walletsStore.fetchWallets()
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

const handleWalletAction = (event: any, wallet: any) => {
  selectedWallet.value = wallet
  if (event.value.value === 'export') {
    showExportDialog.value = true
  } else if (event.value.value === 'delete') {
    showDeleteDialog.value = true
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
    await walletsStore.deleteWallet(selectedWallet.value.wallet_id, deleteForm.value.passphrase)
    toast.add({ severity: 'success', summary: 'Wallet Deleted', life: 3000 })
    showDeleteDialog.value = false
    deleteForm.value = { passphrase: '' }
    selectedWallet.value = null
  } catch (error: any) {
    toast.add({ severity: 'error', summary: 'Delete failed', detail: error.message, life: 5000 })
  } finally {
    deleting.value = false
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
  await refreshWallets()
  walletsStore.subscribeToUpdates()
})

onUnmounted(() => {
  walletsStore.unsubscribeFromUpdates()
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

        .balance-label {
          font-size: 0.875rem;
          color: #6c757d;
          margin-bottom: 0.25rem;
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
</style>
