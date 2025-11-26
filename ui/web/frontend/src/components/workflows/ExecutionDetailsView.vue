<template>
  <AppLayout>
    <main class="execution-details">
      <!-- Header -->
      <div class="execution-header">
        <button class="back-btn" @click="goBack">
          <i class="pi pi-arrow-left"></i> {{ $t('message.common.back') }}
        </button>
        <h1 class="execution-title">
          <i class="pi pi-history"></i>
          {{ $t('message.workflows.executionDetails') }} #{{ execution?.id }}
        </h1>
      </div>

      <!-- Loading State -->
      <div v-if="loading" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <!-- Error State -->
      <div v-else-if="error" class="error-state">
        <i class="pi pi-exclamation-triangle"></i>
        <p>{{ error }}</p>
      </div>

      <!-- Execution Content -->
      <div v-else-if="execution" class="execution-content">
        <!-- Execution Status Card -->
        <div class="status-card">
          <div class="status-card-header">
            <h2>{{ $t('message.workflows.executionStatus') }}</h2>
          </div>
          <div class="status-card-body">
            <div class="status-item">
              <span class="status-label">{{ $t('message.workflows.status') }}:</span>
              <span :class="['status-badge', `status-${execution.status.toLowerCase()}`]">
                <i :class="getStatusIcon(execution.status)"></i>
                {{ execution.status }}
              </span>
            </div>
            <div class="status-item">
              <span class="status-label">{{ $t('message.workflows.workflowId') }}:</span>
              <span class="status-value">{{ execution.workflow_id }}</span>
            </div>
            <div class="status-item">
              <span class="status-label">{{ $t('message.workflows.started') }}:</span>
              <span class="status-value">{{ formatDateTime(execution.started_at) }}</span>
            </div>
            <div v-if="execution.completed_at" class="status-item">
              <span class="status-label">{{ $t('message.workflows.completed') }}:</span>
              <span class="status-value">{{ formatDateTime(execution.completed_at) }}</span>
            </div>
            <div v-if="execution.completed_at" class="status-item">
              <span class="status-label">{{ $t('message.workflows.duration') }}:</span>
              <span class="status-value">{{ calculateDuration(execution.started_at, execution.completed_at) }}</span>
            </div>
            <div v-else-if="execution.started_at" class="status-item">
              <span class="status-label">{{ $t('message.workflows.running') }}:</span>
              <span class="status-value">{{ getRunningTime(execution.started_at) }}</span>
            </div>
            <div v-if="execution.error" class="status-item error-item">
              <span class="status-label">{{ $t('message.common.error') }}:</span>
              <span class="status-value error-text">{{ execution.error }}</span>
            </div>
          </div>
        </div>

        <!-- Jobs List -->
        <div class="jobs-section">
          <div class="jobs-header">
            <h2><i class="pi pi-sitemap"></i> {{ $t('message.workflows.jobs') }}</h2>
            <span class="jobs-count">{{ jobs.length }} {{ $t('message.workflows.jobsCount') }}</span>
          </div>

          <div v-if="jobs.length === 0" class="empty-jobs">
            <i class="pi pi-inbox"></i>
            <p>{{ $t('message.workflows.noJobs') }}</p>
          </div>

          <div v-else class="jobs-list">
            <div
              v-for="job in jobs"
              :key="job.id"
              class="job-card"
              :class="`status-${job.status.toLowerCase()}`"
            >
              <div class="job-header">
                <div class="job-title">
                  <h3>{{ job.job_name }}</h3>
                  <span class="job-id">#{{ job.id }}</span>
                </div>
                <span :class="['status-badge', `status-${job.status.toLowerCase()}`]">
                  <i :class="getJobStatusIcon(job.status)"></i>
                  {{ job.status }}
                </span>
              </div>

              <div class="job-details">
                <div class="job-detail">
                  <span class="label">{{ $t('message.workflows.nodeId') }}:</span>
                  <span class="value">{{ job.node_id }}</span>
                </div>
                <div class="job-detail">
                  <span class="label">{{ $t('message.workflows.serviceId') }}:</span>
                  <span class="value">{{ job.service_id }}</span>
                </div>
                <div class="job-detail">
                  <span class="label">{{ $t('message.workflows.executorPeer') }}:</span>
                  <div class="value-with-copy">
                    <span class="value">{{ formatPeerId(job.executor_peer_id) }}</span>
                    <i
                      class="pi pi-copy copy-icon"
                      @click="copyToClipboard(job.executor_peer_id)"
                      :title="$t('message.common.copy')"
                    ></i>
                  </div>
                </div>
                <div v-if="job.remote_job_execution_id" class="job-detail">
                  <span class="label">{{ $t('message.workflows.remoteExecutionId') }}:</span>
                  <span class="value">{{ job.remote_job_execution_id }}</span>
                </div>
              </div>

              <div v-if="job.error" class="job-error">
                <i class="pi pi-exclamation-triangle"></i>
                {{ job.error }}
              </div>

              <div v-if="job.result && Object.keys(job.result).length > 0" class="job-result">
                <div class="result-header" @click="toggleResult(job.id)">
                  <i :class="expandedResults[job.id] ? 'pi pi-chevron-up' : 'pi pi-chevron-down'"></i>
                  {{ $t('message.workflows.result') }}
                </div>
                <pre v-if="expandedResults[job.id]" class="result-content">{{ JSON.stringify(job.result, null, 2) }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import ProgressSpinner from 'primevue/progressspinner'

import AppLayout from '../layout/AppLayout.vue'
import { useWorkflowsStore } from '../../stores/workflows'
import { useClipboard } from '../../composables/useClipboard'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const { copyToClipboard: copyText } = useClipboard()
const workflowsStore = useWorkflowsStore()

// State
const loading = ref(true)
const error = ref<string | null>(null)
const expandedResults = ref<Record<number, boolean>>({})

// Use reactive data from store (will auto-update via WebSocket)
const execution = computed(() => workflowsStore.selectedExecution)
const jobs = computed(() => workflowsStore.executionJobs)

// Methods
async function loadExecutionStatus() {
  try {
    loading.value = true
    error.value = null

    const executionId = parseInt(route.params.executionId as string)
    const status = await workflowsStore.fetchExecutionStatus(executionId)

    // Store data in the store so WebSocket updates can modify it
    workflowsStore.selectedExecution = status.execution
    workflowsStore.executionJobs = status.jobs || []
  } catch (err: any) {
    error.value = err.message || t('message.workflows.executionLoadError')
    console.error('Error loading execution status:', err)
  } finally {
    loading.value = false
  }
}

function goBack() {
  router.back()
}

function formatPeerId(peerId: string): string {
  if (!peerId) return '-'
  return peerId.length > 16 ? `${peerId.substring(0, 8)}...${peerId.substring(peerId.length - 8)}` : peerId
}

function formatDateTime(dateStr: string): string {
  if (!dateStr || dateStr === '0001-01-01T00:00:00Z') return '-'

  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return '-'

  return date.toLocaleString()
}

function calculateDuration(startStr: string, endStr: string): string {
  if (!startStr || !endStr) return '-'

  const start = new Date(startStr)
  const end = new Date(endStr)

  if (isNaN(start.getTime()) || isNaN(end.getTime())) return '-'

  const diffMs = end.getTime() - start.getTime()
  if (diffMs < 0) return '-'

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
  if (!startStr || startStr === '0001-01-01T00:00:00Z') return '-'

  const start = new Date(startStr)
  if (isNaN(start.getTime())) return '-'

  const now = new Date()
  const diffMs = now.getTime() - start.getTime()
  if (diffMs < 0) return '-'

  const diffSecs = Math.floor(diffMs / 1000)
  if (diffSecs < 60) return `${diffSecs}s`

  const diffMins = Math.floor(diffSecs / 60)
  const secs = diffSecs % 60
  return `${diffMins}m ${secs}s`
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

function getJobStatusIcon(status: string): string {
  const icons: Record<string, string> = {
    pending: 'pi pi-clock',
    running: 'pi pi-spin pi-spinner',
    completed: 'pi pi-check-circle',
    failed: 'pi pi-times-circle'
  }
  return icons[status.toLowerCase()] || 'pi pi-circle'
}

async function copyToClipboard(text: string) {
  const success = await copyText(text)
  if (success) {
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.common.copiedToClipboard'),
      life: 2000
    })
  }
}

function toggleResult(jobId: number) {
  expandedResults.value[jobId] = !expandedResults.value[jobId]
}

onMounted(async () => {
  await loadExecutionStatus()

  // WebSocket updates are handled by the workflows store
  // No polling needed - updates come in real-time via WebSocket
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.execution-details {
  width: 100%;
  height: 100vh;
  overflow: auto;
  padding: 1.5rem;
}

.execution-header {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-bottom: 2rem;
  padding-bottom: 1rem;
  border-bottom: 2px solid rgb(49, 64, 92);

  .back-btn {
    padding: 0.5rem 1rem;
    background-color: rgb(38, 49, 65);
    color: #fff;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 0.9rem;
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    transition: background-color 0.2s ease;

    &:hover {
      background-color: rgb(49, 64, 92);
    }
  }

  .execution-title {
    font-size: 1.75rem;
    font-weight: 600;
    margin: 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;

    i {
      color: rgb(64, 96, 195);
    }
  }
}

.loading {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 4rem;
}

.error-state {
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: 4rem;
  color: #ff6b6b;

  i {
    font-size: 4rem;
    margin-bottom: 1rem;
  }

  p {
    font-size: 1.25rem;
    margin: 0;
  }
}

.execution-content {
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

.status-card {
  background-color: rgb(27, 38, 54);
  border-radius: 8px;
  border: 2px solid rgb(49, 64, 92);
  overflow: hidden;

  .status-card-header {
    padding: 1rem 1.5rem;
    background-color: rgb(38, 49, 65);
    border-bottom: 1px solid rgb(49, 64, 92);

    h2 {
      margin: 0;
      font-size: 1.25rem;
      font-weight: 600;
    }
  }

  .status-card-body {
    padding: 1.5rem;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 1rem;

    .status-item {
      display: flex;
      flex-direction: column;
      gap: 0.5rem;

      .status-label {
        font-size: 0.875rem;
        color: rgba(255, 255, 255, 0.7);
        font-weight: 500;
      }

      .status-value {
        font-size: 1rem;
        color: #fff;
        font-weight: 600;
      }

      &.error-item {
        grid-column: 1 / -1;

        .error-text {
          color: #ff6b6b;
          padding: 0.75rem;
          background-color: rgba(211, 47, 47, 0.1);
          border-left: 3px solid #d32f2f;
          border-radius: 4px;
        }
      }
    }
  }
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.75rem;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  width: fit-content;

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

.jobs-section {
  background-color: rgb(27, 38, 54);
  border-radius: 8px;
  border: 2px solid rgb(49, 64, 92);
  overflow: hidden;

  .jobs-header {
    padding: 1rem 1.5rem;
    background-color: rgb(38, 49, 65);
    border-bottom: 1px solid rgb(49, 64, 92);
    display: flex;
    justify-content: space-between;
    align-items: center;

    h2 {
      margin: 0;
      font-size: 1.25rem;
      font-weight: 600;
      display: flex;
      align-items: center;
      gap: 0.5rem;

      i {
        color: rgb(205, 81, 36);
      }
    }

    .jobs-count {
      font-size: 0.9rem;
      color: rgba(255, 255, 255, 0.7);
      font-weight: 500;
    }
  }

  .empty-jobs {
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    padding: 3rem;
    color: rgba(255, 255, 255, 0.5);

    i {
      font-size: 3rem;
      margin-bottom: 1rem;
    }

    p {
      font-size: 1rem;
      margin: 0;
    }
  }

  .jobs-list {
    padding: 1.5rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
}

.job-card {
  background-color: rgb(38, 49, 65);
  border-radius: 6px;
  padding: 1.25rem;
  border-left: 4px solid rgb(64, 96, 195);
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

  .job-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;

    .job-title {
      display: flex;
      align-items: center;
      gap: 0.75rem;

      h3 {
        margin: 0;
        font-size: 1.125rem;
        font-weight: 600;
        color: rgb(205, 81, 36);
      }

      .job-id {
        font-size: 0.875rem;
        color: rgba(255, 255, 255, 0.6);
        font-family: 'Courier New', monospace;
      }
    }
  }

  .job-details {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 0.75rem;
    margin-bottom: 0.75rem;

    .job-detail {
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

  .job-error {
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

  .job-result {
    margin-top: 0.75rem;
    background-color: rgb(27, 38, 54);
    border-radius: 4px;
    overflow: hidden;

    .result-header {
      padding: 0.75rem 1rem;
      background-color: rgb(49, 64, 92);
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 0.5rem;
      font-size: 0.875rem;
      font-weight: 600;
      transition: background-color 0.2s ease;

      &:hover {
        background-color: rgb(64, 96, 195);
      }

      i {
        font-size: 0.75rem;
      }
    }

    .result-content {
      padding: 1rem;
      margin: 0;
      font-size: 0.8rem;
      font-family: 'Courier New', monospace;
      overflow-x: auto;
      background-color: rgb(27, 38, 54);
      color: #fff;
      border: none;
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
