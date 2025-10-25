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
              <p>{{ $t('message.common.noData') }}</p>
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

import AppLayout from '../layout/AppLayout.vue'
import { usePeersStore } from '../../stores/peers'
import { useClipboard } from '../../composables/useClipboard'
import { useTextUtils } from '../../composables/useTextUtils'

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
  // Placeholder - backend will provide this data
  return 0
}

function getAppServicesCount(peer: any): number {
  // Placeholder - backend will provide this data
  return 0
}

function viewPeerServices(peer: any) {
  router.push(`/services/peer/${peer.peer_id}`)
}

function blacklistPeer(peer: any) {
  confirm.require({
    message: t('message.configuration.blacklistConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await peersStore.addToBlacklist(peer.peer_id, 'Blacklisted from peers view')
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

  .peers-table {
    // Table styling handled by PrimeVue
  }
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
</style>
