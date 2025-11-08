<template>
  <AppLayout>
    <main class="workflows">
      <!-- Top Controls -->
      <div class="workflows-controls">
        <div class="workflows-controls-buttons">
          <div class="input-box">
            <button
              :class="['btn', 'light']"
              :disabled="!hasSelection"
              @click="confirmDeleteSelected"
            >
              <i class="pi pi-times-circle"></i> {{ $t('message.common.delete') }}
            </button>
          </div>
          <div class="input-box">
            <button class="btn" @click="createWorkflow">
              <i class="pi pi-plus-circle"></i> {{ $t('message.workflows.new') }}
            </button>
          </div>
        </div>
      </div>

      <!-- Filter Boxes -->
      <div class="workflows-filters">
        <div class="workflows-filters-title">
          <i class="pi pi-filter"></i> {{ $t('message.workflows.filterWorkflowsByStatus') }}
        </div>
        <div class="workflows-filters-list">
          <div class="workflows-filters-box" @click="filterByStatus('draft')">
            <div class="workflows-filters-box-content">
              <div class="workflows-filters-box-content-icon">
                <OverlayBadge :value="draftCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-objects-column" size="large"
                    style="background-color: #4060c3; color: #fff" />
                </OverlayBadge>
              </div>
              <div class="workflows-filters-box-content-details">
                {{ $t('message.workflows.inDesign') }}
              </div>
            </div>
          </div>

          <div class="workflows-filters-box" @click="filterByStatus('executing')">
            <div class="workflows-filters-box-content">
              <div class="workflows-filters-box-content-icon">
                <OverlayBadge :value="runningCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-play" size="large"
                    style="background-color: rgba(205, 81, 36, 1); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="workflows-filters-box-content-details">
                {{ $t('message.workflows.running') }}
              </div>
            </div>
          </div>

          <div class="workflows-filters-box" @click="filterByStatus('completed')">
            <div class="workflows-filters-box-content">
              <div class="workflows-filters-box-content-icon">
                <OverlayBadge :value="completedCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-verified" size="large"
                    style="background-color: rgba(86, 164, 82, 1); color: #fff" />
                </OverlayBadge>
              </div>
              <div class="workflows-filters-box-content-details">
                {{ $t('message.workflows.completed') }}
              </div>
            </div>
          </div>

          <div class="workflows-filters-box" @click="filterByStatus('failed')">
            <div class="workflows-filters-box-content">
              <div class="workflows-filters-box-content-icon">
                <OverlayBadge :value="erroredCount" severity="contrast" size="small">
                  <Avatar icon="pi pi-exclamation-circle" size="large"
                    style="background-color: #d32f2f; color: #fff" />
                </OverlayBadge>
              </div>
              <div class="workflows-filters-box-content-details">
                {{ $t('message.workflows.errored') }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Workflows List Section -->
      <div class="workflows-list-section">
        <div class="workflows-list-title">
          <i class="pi pi-list"></i> {{ $t('message.workflows.workflowsList') }}
        </div>

        <div v-if="workflowsStore.loading" class="loading">
          <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
        </div>

        <DataTable
          v-else
          v-model:selection="selectedWorkflows"
          :value="workflowsStore.workflows || []"
          :paginator="(workflowsStore.workflows || []).length > 10"
          :rows="10"
          class="workflows-table"
          :rowsPerPageOptions="[10, 20, 50, 100]"
          responsiveLayout="scroll"
          dataKey="id"
          selectionMode="multiple"
          filterDisplay="row"
          v-model:filters="workflowFilters"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-inbox"></i>
              <p>{{ $t('message.common.noData') }}</p>
            </div>
          </template>

          <Column field="name" :header="$t('message.workflows.name')" :sortable="true" filterMatchMode="contains" :showFilterMenu="false">
            <template #filter="{ filterModel, filterCallback }">
              <InputText
                v-model="filterModel.value"
                type="text"
                @input="filterCallback()"
                :placeholder="$t('message.workflows.searchByName')"
                class="p-column-filter"
              />
            </template>
          </Column>
          <Column field="description" :header="$t('message.workflows.description')" filterMatchMode="contains" :showFilterMenu="false">
            <template #filter="{ filterModel, filterCallback }">
              <InputText
                v-model="filterModel.value"
                type="text"
                @input="filterCallback()"
                :placeholder="$t('message.workflows.searchByDescription')"
                class="p-column-filter"
              />
            </template>
          </Column>
          <Column field="status" :header="$t('message.workflows.status')" :sortable="true">
            <template #body="slotProps">
              <span :class="['status-badge', `status-${slotProps.data.status || 'draft'}`]">
                {{ getStatusLabel(slotProps.data.status || 'draft') }}
              </span>
            </template>
          </Column>
          <Column :header="$t('message.common.actions')" :exportable="false" style="min-width:10rem">
            <template #body="slotProps">
              <div class="action-buttons">
                <button class="action-btn view-btn" @click="viewWorkflow(slotProps.data)">
                  <i class="pi pi-eye"></i> {{ $t('message.common.show') }}
                </button>
                <button class="action-btn delete-btn" @click="confirmDeleteWorkflow(slotProps.data)">
                  <i class="pi pi-trash"></i> {{ $t('message.common.delete') }}
                </button>
              </div>
            </template>
          </Column>
        </DataTable>
      </div>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'

import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import InputText from 'primevue/inputtext'
import Avatar from 'primevue/avatar'
import OverlayBadge from 'primevue/overlaybadge'
import { FilterMatchMode } from '@primevue/core/api'

import AppLayout from '../layout/AppLayout.vue'
import { useWorkflowsStore } from '../../stores/workflows'
import type { Workflow } from '../../stores/workflows'

const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

const workflowsStore = useWorkflowsStore() as any // TODO: Fix Pinia typing

// State
const selectedWorkflows = ref<Workflow[]>([])
const statusFilter = ref<string | null>(null)
const workflowFilters = ref({
  name: { value: null, matchMode: FilterMatchMode.CONTAINS },
  description: { value: null, matchMode: FilterMatchMode.CONTAINS }
})

// Computed
const draftCount = computed(() =>
  (workflowsStore.workflows || []).filter((w: Workflow) => w?.status === 'draft').length
)

const runningCount = computed(() =>
  (workflowsStore.workflows || []).filter((w: Workflow) => w?.status === 'executing').length
)

const completedCount = computed(() =>
  (workflowsStore.workflows || []).filter((w: Workflow) => w?.status === 'completed').length
)

const erroredCount = computed(() =>
  (workflowsStore.workflows || []).filter((w: Workflow) => w?.status === 'failed').length
)

const hasSelection = computed(() => selectedWorkflows.value.length > 0)

// Methods
function createWorkflow() {
  router.push({ name: 'WorkflowEditorNew' })
}

function viewWorkflow(workflow: any) {
  router.push({ name: 'WorkflowEditor', params: { id: workflow.id } })
}

function filterByStatus(status: string) {
  if (statusFilter.value === status) {
    statusFilter.value = null // Toggle off if clicking same filter
  } else {
    statusFilter.value = status
  }
}

function getStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    draft: t('message.workflows.statusDraft'),
    active: t('message.workflows.statusActive'),
    executing: t('message.workflows.statusExecuting'),
    completed: t('message.workflows.statusCompleted'),
    failed: t('message.workflows.statusFailed')
  }
  return labels[status] || status
}

function confirmDeleteWorkflow(workflow: any) {
  confirm.require({
    message: t('message.workflows.deleteConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await workflowsStore.deleteWorkflow(workflow.id)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.workflows.deleteSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.workflows.deleteError'),
          life: 3000
        })
      }
    }
  })
}

function confirmDeleteSelected() {
  if (!hasSelection.value) return

  confirm.require({
    message: t('message.workflows.deleteSelectedConfirm', { count: selectedWorkflows.value.length }),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await Promise.all(
          selectedWorkflows.value.map(w => workflowsStore.deleteWorkflow(w.id))
        )
        selectedWorkflows.value = []
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.workflows.deleteSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.workflows.deleteError'),
          life: 3000
        })
      }
    }
  })
}

onMounted(async () => {
  await workflowsStore.fetchWorkflows()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.workflows {
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
}

.workflows-controls {
  display: flex;
  flex-direction: row;
  flex-wrap: nowrap;
  justify-content: flex-end;
  align-content: center;
  align-items: center;
  width: 100%;
  padding-bottom: 1.5rem;
  border-bottom: 2px solid rgb(49, 64, 92);

  .workflows-controls-buttons {
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

          &:disabled {
            background-color: #666;
            color: #999;
            cursor: not-allowed;
          }
        }

        &:hover {
          background-color: rgb(246, 114, 66);
        }

        &:disabled {
          background-color: #333333;
          cursor: not-allowed;
        }
      }
    }
  }
}

.workflows-filters {
  padding: 1rem 0;

  .workflows-filters-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
    }
  }

  .workflows-filters-list {
    display: flex;
    flex-direction: row;
    flex-wrap: wrap;
    justify-content: flex-start;
    align-content: flex-start;
    align-items: flex-start;
    width: 100%;
    max-width: 100%;

    .workflows-filters-box {
      width: 10rem;
      min-width: 10rem;
      height: 5rem;
      background-color: rgb(38, 49, 65);
      border-radius: 4px;
      cursor: pointer;
      margin: 1rem 1rem 1rem 0;
      padding: .5rem;
      transition: background-color 0.2s ease;

      &:hover {
        background-color: rgb(49, 64, 92);
      }

      display: flex;
      flex-direction: column;
      flex-wrap: nowrap;
      justify-content: center;
      align-content: center;
      align-items: center;

      .workflows-filters-box-content {
        width: 100%;
        display: flex;
        flex-direction: row;
        flex-wrap: nowrap;
        justify-content: flex-start;
        align-content: center;
        align-items: center;

        .workflows-filters-box-content-icon {
          padding: 0 .25rem;
        }

        .workflows-filters-box-content-details {
          padding-left: 1rem;
          font-size: .85rem;
        }
      }
    }
  }
}

.workflows-list-section {
  padding: 1rem 0;

  .workflows-list-title {
    text-align: left;
    font-size: 1.5rem;
    padding-top: .5rem;
    margin-bottom: 1rem;

    i {
      vertical-align: center;
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
}

.status-badge {
  padding: 0.25rem 0.75rem;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;

  &.status-draft {
    background-color: #4060c3;
    color: #fff;
  }

  &.status-active {
    background-color: rgba(205, 81, 36, 1);
    color: #fff;
  }

  &.status-executing {
    background-color: rgba(205, 81, 36, 1);
    color: #fff;
  }

  &.status-completed {
    background-color: rgba(86, 164, 82, 1);
    color: #fff;
  }

  &.status-failed {
    background-color: #d32f2f;
    color: #fff;
  }
}

:deep(.workflows-table) {
  .p-datatable-thead > tr > th {
    background-color: rgb(38, 49, 65);
    color: vars.$color-text;
    border: none;
  }

  .p-datatable-tbody > tr {
    background-color: rgb(27, 38, 54);
    color: vars.$color-text;

    &:hover {
      background-color: rgb(38, 49, 65);
    }

    > td {
      border: none;
    }
  }
}

.action-buttons {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-start;

  .action-btn {
    min-width: 60px;
    height: 28px;
    line-height: 28px;
    border-radius: 3px;
    border: none;
    padding: 0 8px;
    cursor: pointer;
    font-size: .85rem;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    transition: background-color 0.2s ease;

    i {
      font-size: .85rem;
    }

    &.view-btn {
      background-color: rgb(205, 81, 36);
      color: #fff;

      &:hover {
        background-color: rgb(246, 114, 66);
      }
    }

    &.delete-btn {
      background-color: #fff;
      color: rgb(27, 38, 54);

      &:hover {
        background-color: #fff;
        color: rgb(205, 81, 36);
      }
    }
  }
}
</style>
