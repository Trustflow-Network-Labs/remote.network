<template>
  <AppLayout>
    <div class="peers">
      <div class="peers-header">
        <h1>{{ $t('message.peers.title') }}</h1>
      </div>

      <div v-if="peersStore.loading" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <DataTable
        v-else
        :value="peersStore.peers"
        :paginator="true"
        :rows="25"
        class="peers-table"
        :rowsPerPageOptions="[10, 25, 50]"
        responsiveLayout="scroll"
        filterDisplay="row"
        v-model:filters="filters"
      >
        <template #empty>
          <div class="empty-state">
            <i class="pi pi-inbox"></i>
            <p>{{ $t('message.common.noData') }}</p>
          </div>
        </template>

        <Column field="peer_id" :header="$t('message.peers.peerId')" :sortable="true" filterMatchMode="contains" :showFilterMenu="false">
          <template #body="slotProps">
            <code class="peer-id">{{ slotProps.data.peer_id }}</code>
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

        <Column field="is_relay" :header="$t('message.peers.isRelay')" :sortable="true" style="width:10%">
          <template #body="slotProps">
            <Tag
              :value="slotProps.data.is_relay ? 'Yes' : 'No'"
              :severity="slotProps.data.is_relay ? 'success' : 'secondary'"
            />
          </template>
        </Column>

        <Column field="is_store" :header="$t('message.peers.isStore')" :sortable="true" style="width:10%">
          <template #body="slotProps">
            <Tag
              :value="slotProps.data.is_store ? 'Yes' : 'No'"
              :severity="slotProps.data.is_store ? 'success' : 'secondary'"
            />
          </template>
        </Column>

        <Column field="last_seen" :header="$t('message.peers.lastSeen')" :sortable="true" style="width:15%">
          <template #body="slotProps">
            {{ formatDate(slotProps.data.last_seen) }}
          </template>
        </Column>

        <Column field="source" :header="$t('message.peers.source')" :sortable="true" style="width:12%">
          <template #body="slotProps">
            <Tag :value="slotProps.data.source" severity="info" />
          </template>
        </Column>

        <Column :exportable="false" style="min-width:8rem">
          <template #body="slotProps">
            <Button
              v-if="!peersStore.isBlacklisted(slotProps.data.peer_id)"
              icon="pi pi-ban"
              text
              rounded
              severity="danger"
              @click="blacklistPeer(slotProps.data)"
            />
            <Tag v-else value="Blacklisted" severity="danger" />
          </template>
        </Column>
      </DataTable>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import { FilterMatchMode } from '@primevue/core/api'

import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import InputText from 'primevue/inputtext'

import AppLayout from '../layout/AppLayout.vue'
import { usePeersStore } from '../../stores/peers'

const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()
const peersStore = usePeersStore()

const filters = ref({
  peer_id: { value: null, matchMode: FilterMatchMode.CONTAINS }
})

function formatDate(dateString: string): string {
  if (!dateString) return 'N/A'
  const date = new Date(dateString)
  return date.toLocaleString()
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
}

.peers-header {
  margin-bottom: vars.$spacing-xl;

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

.peer-id {
  font-family: 'Courier New', monospace;
  font-size: vars.$font-size-sm;
  background: rgba(vars.$color-primary, 0.1);
  padding: vars.$spacing-xs;
  border-radius: vars.$border-radius-sm;
}

.peers-table {
  // Background handled by PrimeVue
}
</style>
