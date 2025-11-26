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
            <template #body="slotProps">
              <div class="description-cell">{{ slotProps.data.description }}</div>
            </template>
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
                <button
                  class="action-btn executions-btn"
                  @click="toggleExecutions(slotProps.data)"
                  :class="{ active: activeWorkflowId === slotProps.data.id }"
                >
                  <i class="pi pi-history"></i> {{ $t('message.workflows.executions') }}
                </button>
                <button class="action-btn delete-btn" @click="confirmDeleteWorkflow(slotProps.data)">
                  <i class="pi pi-trash"></i> {{ $t('message.common.delete') }}
                </button>
              </div>
            </template>
          </Column>
        </DataTable>
      </div>

      <!-- Workflow Executions Section -->
      <div v-if="activeWorkflowId" class="executions-section">
        <div class="executions-header">
          <div class="executions-title">
            <i class="pi pi-history"></i> {{ $t('message.workflows.executionHistory') }}
            <span class="workflow-name">{{ activeWorkflowName }}</span>
          </div>
          <button class="close-btn" @click="closeExecutions">
            <i class="pi pi-times"></i>
          </button>
        </div>

        <div v-if="loadingExecutions" class="loading">
          <ProgressSpinner style="width:40px;height:40px" strokeWidth="4" />
        </div>

        <div v-else-if="workflowExecutions.length === 0" class="empty-executions">
          <i class="pi pi-inbox"></i>
          <p>{{ $t('message.workflows.noExecutions') }}</p>
        </div>

        <div v-else class="executions-list">
          <div
            v-for="execution in workflowExecutions"
            :key="execution.id"
            class="execution-card"
            :class="`status-${execution.status.toLowerCase()}`"
            @click="viewExecutionDetails(execution)"
          >
            <div class="execution-header">
              <div class="execution-id">#{{ execution.id }}</div>
              <div class="execution-status">
                <span :class="['status-badge', `status-${execution.status.toLowerCase()}`]">
                  <i :class="getStatusIcon(execution.status)"></i>
                  {{ execution.status }}
                </span>
              </div>
            </div>
            <div class="execution-details">
              <div class="execution-detail">
                <span class="label">{{ $t('message.workflows.started') }}:</span>
                <span class="value">{{ formatDateTime(execution.started_at) }}</span>
              </div>
              <div class="execution-detail" v-if="execution.completed_at">
                <span class="label">{{ $t('message.workflows.completed') }}:</span>
                <span class="value">{{ formatDateTime(execution.completed_at) }}</span>
              </div>
              <div class="execution-detail" v-if="execution.completed_at">
                <span class="label">{{ $t('message.workflows.duration') }}:</span>
                <span class="value">{{ calculateDuration(execution.started_at, execution.completed_at) }}</span>
              </div>
              <div class="execution-detail" v-else-if="execution.started_at">
                <span class="label">{{ $t('message.workflows.running') }}:</span>
                <span class="value">{{ getRunningTime(execution.started_at) }}</span>
              </div>
            </div>
            <div v-if="execution.error && execution.status === 'failed'" class="execution-error">
              <i class="pi pi-exclamation-triangle"></i>
              {{ execution.error }}
            </div>

            <div class="execution-actions">
              <button class="view-details-btn">
                <i class="pi pi-eye"></i>
                {{ $t('message.workflows.viewDetails') }}
              </button>
            </div>
          </div>
        </div>
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

// Workflow Executions State
const activeWorkflowId = ref<number | null>(null)
const activeWorkflowName = ref<string>('')
const workflowExecutions = ref<any[]>([])
const loadingExecutions = ref(false)

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

// Workflow Executions Methods
async function loadExecutions(workflowId: number) {
  if (!workflowId) return

  loadingExecutions.value = true
  try {
    const executions = await workflowsStore.fetchWorkflowExecutions(workflowId)
    workflowExecutions.value = executions
  } catch (error) {
    console.error('Error loading executions:', error)
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: t('message.workflows.executionsLoadError'),
      life: 3000
    })
  } finally {
    loadingExecutions.value = false
  }
}

function toggleExecutions(workflow: Workflow) {
  if (activeWorkflowId.value === workflow.id) {
    closeExecutions()
  } else {
    activeWorkflowId.value = workflow.id
    activeWorkflowName.value = workflow.name
    loadExecutions(workflow.id)

    // WebSocket updates are handled by the workflows store
    // No polling needed - updates come in real-time via WebSocket
  }
}

function closeExecutions() {
  activeWorkflowId.value = null
  activeWorkflowName.value = ''
  workflowExecutions.value = []
}

function viewExecutionDetails(execution: any) {
  router.push({
    name: 'ExecutionDetails',
    params: {
      workflowId: activeWorkflowId.value!,
      executionId: execution.id
    }
  })
}

function getStatusIcon(status: string): string {
  const icons: Record<string, string> = {
    pending: 'pi pi-clock',
    running: 'pi pi-spin pi-spinner',
    completed: 'pi pi-check-circle',
    failed: 'pi pi-times-circle',
    cancelled: 'pi pi-ban'
  }
  return icons[status.toLowerCase()] || 'pi pi-circle'
}

function formatDateTime(dateStr: string): string {
  if (!dateStr) return '-'

  // Go zero time - job hasn't started/completed yet
  if (dateStr === '0001-01-01T00:00:00Z') return '-'

  const date = new Date(dateStr)

  // Check if date is valid
  if (isNaN(date.getTime())) {
    console.warn('Invalid date string:', dateStr)
    return '-'
  }

  const now = new Date()
  const diffMs = now.getTime() - date.getTime()

  // If the date is in the future or way too far in the past, it's likely invalid
  if (diffMs < 0 || diffMs > 365 * 24 * 60 * 60 * 1000 * 100) {
    console.warn('Date out of reasonable range:', dateStr, 'Diff:', diffMs)
    return '-'
  }

  const diffMins = Math.floor(diffMs / 60000)

  if (diffMins < 1) return 'Just now'
  if (diffMins < 60) return `${diffMins} min${diffMins > 1 ? 's' : ''} ago`

  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours} hour${diffHours > 1 ? 's' : ''} ago`

  const diffDays = Math.floor(diffHours / 24)
  if (diffDays > 365) {
    const years = Math.floor(diffDays / 365)
    return `${years} year${years > 1 ? 's' : ''} ago`
  }
  return `${diffDays} day${diffDays > 1 ? 's' : ''} ago`
}

function calculateDuration(startStr: string, endStr: string): string {
  if (!startStr || !endStr) return '-'

  // Go zero time - job hasn't started/completed yet
  const goZeroTime = '0001-01-01T00:00:00Z'
  if (startStr === goZeroTime || endStr === goZeroTime) return '-'

  const start = new Date(startStr)
  const end = new Date(endStr)

  // Check if dates are valid
  if (isNaN(start.getTime()) || isNaN(end.getTime())) {
    console.warn('Invalid date strings:', startStr, endStr)
    return '-'
  }

  const diffMs = end.getTime() - start.getTime()

  // If duration is negative or unreasonably large, something is wrong
  if (diffMs < 0 || diffMs > 365 * 24 * 60 * 60 * 1000) {
    // Don't spam console, just return '-'
    return '-'
  }

  const diffSecs = Math.floor(diffMs / 1000)

  if (diffSecs < 60) return `${diffSecs}s`

  const diffMins = Math.floor(diffSecs / 60)
  const secs = diffSecs % 60

  if (diffMins < 60) return `${diffMins}m ${secs}s`

  const hours = Math.floor(diffMins / 60)
  const mins = diffMins % 60
  return `${hours}h ${mins}m`
}

function getRunningTime(startStr: string): string {
  if (!startStr) return '-'

  // Go zero time - job hasn't started yet
  if (startStr === '0001-01-01T00:00:00Z') return '-'

  const start = new Date(startStr)

  // Check if date is valid
  if (isNaN(start.getTime())) {
    console.warn('Invalid start date string:', startStr)
    return '-'
  }

  const now = new Date()
  const diffMs = now.getTime() - start.getTime()

  // If the difference is negative or unreasonably large, return '-'
  if (diffMs < 0 || diffMs > 365 * 24 * 60 * 60 * 1000) {
    // Don't spam console, just return '-'
    return '-'
  }

  const diffSecs = Math.floor(diffMs / 1000)

  if (diffSecs < 60) return `${diffSecs}s`

  const diffMins = Math.floor(diffSecs / 60)
  const secs = diffSecs % 60
  return `${diffMins}m ${secs}s`
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

.description-cell {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  overflow: hidden;
  text-overflow: ellipsis;
  line-clamp: 3;
  max-width: 400px;
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

    &.executions-btn {
      background-color: rgb(64, 96, 195);
      color: #fff;

      &:hover {
        background-color: rgb(84, 116, 215);
      }

      &.active {
        background-color: rgb(84, 116, 215);
        box-shadow: 0 0 0 2px rgba(84, 116, 215, 0.5);
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

// Job Executions Section
.executions-section {
  margin-top: 2rem;
  padding: 1.5rem;
  background-color: rgb(27, 38, 54);
  border-radius: 8px;
  border: 2px solid rgb(49, 64, 92);

  .executions-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1.5rem;
    padding-bottom: 1rem;
    border-bottom: 2px solid rgb(49, 64, 92);

    .executions-title {
      font-size: 1.5rem;
      font-weight: 600;
      display: flex;
      align-items: center;
      gap: 0.5rem;

      i {
        color: rgb(64, 96, 195);
      }

      .workflow-name {
        color: rgb(205, 81, 36);
        margin-left: 0.5rem;
        font-style: italic;
      }
    }

    .close-btn {
      background-color: transparent;
      border: none;
      color: #fff;
      font-size: 1.25rem;
      cursor: pointer;
      padding: 0.25rem 0.5rem;
      border-radius: 4px;
      transition: background-color 0.2s ease;

      &:hover {
        background-color: rgb(49, 64, 92);
      }
    }
  }

  .loading, .empty-executions {
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    padding: 2rem;
    color: vars.$color-text-secondary;

    i {
      font-size: 3rem;
      margin-bottom: 1rem;
    }

    p {
      font-size: 1rem;
      margin: 0;
    }
  }

  .executions-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .execution-card {
    background-color: rgb(38, 49, 65);
    border-radius: 6px;
    padding: 1rem;
    border-left: 4px solid rgb(64, 96, 195);
    cursor: pointer;
    transition: transform 0.2s ease, box-shadow 0.2s ease;

    &:hover {
      transform: translateX(4px);
      box-shadow: 0 4px 8px rgba(0, 0, 0, 0.2);
    }

    &.status-pending {
      border-left-color: #f59e0b;
    }

    &.status-running {
      border-left-color: rgb(64, 96, 195);
      animation: pulse 2s ease-in-out infinite;
    }

    &.status-completed {
      border-left-color: rgba(86, 164, 82, 1);
    }

    &.status-failed {
      border-left-color: #d32f2f;
    }

    &.status-cancelled {
      border-left-color: #ff9800;
    }

    .execution-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 0.75rem;

      .execution-id {
        font-size: 1.25rem;
        font-weight: 700;
        color: rgb(205, 81, 36);
      }

      .execution-status {
        .status-badge {
          display: inline-flex;
          align-items: center;
          gap: 0.375rem;
          padding: 0.375rem 0.75rem;
          border-radius: 12px;
          font-size: 0.75rem;
          font-weight: 600;
          text-transform: uppercase;

          i {
            font-size: 0.875rem;
          }

          &.status-pending {
            background-color: #f59e0b;
            color: #fff;
          }

          &.status-running {
            background-color: rgb(64, 96, 195);
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

          &.status-cancelled {
            background-color: #ff9800;
            color: #fff;
          }
        }
      }
    }

    .execution-details {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 0.5rem;
      margin-bottom: 0.5rem;

      .execution-detail {
        display: flex;
        gap: 0.5rem;
        font-size: 0.875rem;

        .label {
          color: rgba(255, 255, 255, 0.7);
          font-weight: 500;
        }

        .value {
          color: #fff;
          font-weight: 600;
          font-family: 'Courier New', monospace;
        }

        .value-with-copy {
          display: flex;
          align-items: center;
          gap: 0.5rem;

          .value {
            color: #fff;
            font-weight: 600;
            font-family: 'Courier New', monospace;
          }

          .copy-icon {
            color: rgba(255, 255, 255, 0.5);
            cursor: pointer;
            font-size: 0.875rem;
            transition: color 0.2s ease;

            &:hover {
              color: rgb(205, 81, 36);
            }
          }
        }
      }
    }

    .execution-error {
      margin-top: 0.75rem;
      padding: 0.75rem;
      background-color: rgba(211, 47, 47, 0.1);
      border-left: 3px solid #d32f2f;
      border-radius: 4px;
      color: #ff6b6b;
      font-size: 0.875rem;
      display: flex;
      align-items: flex-start;
      gap: 0.5rem;

      i {
        flex-shrink: 0;
        margin-top: 0.125rem;
      }
    }

    .execution-actions {
      margin-top: 0.75rem;
      display: flex;
      justify-content: flex-end;

      .view-details-btn {
        padding: 0.5rem 1rem;
        background-color: rgb(205, 81, 36);
        color: #fff;
        border: none;
        border-radius: 4px;
        cursor: pointer;
        font-size: 0.875rem;
        display: inline-flex;
        align-items: center;
        gap: 0.5rem;
        transition: all 0.2s ease;

        &:hover {
          background-color: rgb(246, 114, 66);
        }

        i {
          font-size: 0.875rem;
        }
      }
    }
  }
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}
</style>
