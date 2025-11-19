<template>
  <Dialog
    :visible="visible"
    @update:visible="(value: boolean) => emit('update:visible', value)"
    modal
    :header="title"
    :style="{ width: '700px' }"
    :closable="false"
    :draggable="false"
  >
    <div class="docker-progress-container">
      <!-- Progress Info -->
      <div class="progress-info">
        <div class="info-item">
          <label>Service:</label>
          <span>{{ serviceName }}</span>
        </div>
        <div class="info-item">
          <label>Image:</label>
          <span>{{ imageName }}</span>
        </div>
        <div class="info-item">
          <label>Status:</label>
          <Tag :value="status" :severity="statusSeverity" />
        </div>
      </div>

      <!-- Console Output -->
      <Card class="console-card">
        <template #title>
          <div class="console-header">
            <i class="pi pi-terminal"></i>
            <span>Console Output</span>
            <Button
              icon="pi pi-trash"
              text
              rounded
              size="small"
              @click="clearOutput"
              :title="$t('message.common.clear')"
            />
          </div>
        </template>
        <template #content>
          <div ref="consoleOutput" class="console-output">
            <div
              v-for="(line, index) in outputLines"
              :key="index"
              class="console-line"
              :class="{ 'error-line': line.isError }"
            >
              <span class="line-prefix">$</span>
              <span class="line-content">{{ line.text }}</span>
            </div>
            <div v-if="outputLines.length === 0" class="empty-console">
              Waiting for output...
            </div>
          </div>
        </template>
      </Card>
    </div>

    <template #footer>
      <div class="footer-actions">
        <div class="footer-left">
          <span v-if="status === 'running'" class="status-text">
            <i class="pi pi-spin pi-spinner"></i>
            {{ currentOperation }}
          </span>
        </div>
        <div class="footer-right">
          <Button
            v-if="status === 'completed'"
            :label="$t('message.common.close')"
            icon="pi pi-check"
            @click="handleClose"
            autofocus
          />
          <Button
            v-else-if="status === 'error'"
            :label="$t('message.common.close')"
            icon="pi pi-times"
            severity="danger"
            @click="handleClose"
          />
        </div>
      </div>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch, nextTick, computed } from 'vue'
import Dialog from 'primevue/dialog'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Tag from 'primevue/tag'

interface Props {
  visible: boolean
  serviceName: string
  imageName: string
}

interface OutputLine {
  text: string
  isError: boolean
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  close: []
}>()

// State
const outputLines = ref<OutputLine[]>([])
const consoleOutput = ref<HTMLElement | null>(null)
const status = ref<'running' | 'completed' | 'error'>('running')
const currentOperation = ref('Initializing...')

// Computed
const title = computed(() => {
  if (status.value === 'completed') return 'Docker Service Created Successfully'
  if (status.value === 'error') return 'Docker Service Creation Failed'
  return 'Creating Docker Service...'
})

const statusSeverity = computed(() => {
  if (status.value === 'completed') return 'success'
  if (status.value === 'error') return 'danger'
  return 'info'
})

// Watch for visibility changes to reset state
watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    outputLines.value = []
    status.value = 'running'
    currentOperation.value = 'Initializing...'
  }
})

// Methods
const addOutputLine = (text: string, isError = false) => {
  outputLines.value.push({ text, isError })

  // Auto-scroll to bottom
  nextTick(() => {
    if (consoleOutput.value) {
      consoleOutput.value.scrollTop = consoleOutput.value.scrollHeight
    }
  })
}

const clearOutput = () => {
  outputLines.value = []
}

const setStatus = (newStatus: 'running' | 'completed' | 'error') => {
  status.value = newStatus
}

const setCurrentOperation = (operation: string) => {
  currentOperation.value = operation
}

const handleClose = () => {
  emit('close')
  emit('update:visible', false)
}

// Expose methods for parent component
defineExpose({
  addOutputLine,
  setStatus,
  setCurrentOperation
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.docker-progress-container {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-lg;
}

.progress-info {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: vars.$spacing-md;
  padding: vars.$spacing-md;
  background: vars.$color-surface;
  border-radius: vars.$border-radius-sm;

  .info-item {
    display: flex;
    flex-direction: column;
    gap: vars.$spacing-xs;

    label {
      font-size: vars.$font-size-sm;
      font-weight: 600;
      color: vars.$color-text-secondary;
      text-transform: uppercase;
    }

    span {
      color: vars.$color-text;
      font-size: vars.$font-size-md;
    }
  }
}

.console-card {
  :deep(.p-card-title) {
    padding: 0;
    margin: 0;
  }

  :deep(.p-card-content) {
    padding: 0;
  }

  .console-header {
    display: flex;
    align-items: center;
    gap: vars.$spacing-sm;
    color: vars.$color-text;
    font-size: vars.$font-size-md;

    i.pi-terminal {
      color: vars.$color-primary;
    }

    span {
      flex: 1;
    }
  }

  .console-output {
    background: #1e1e1e;
    color: #d4d4d4;
    font-family: 'Courier New', monospace;
    font-size: 13px;
    padding: vars.$spacing-md;
    border-radius: vars.$border-radius-sm;
    max-height: 400px;
    overflow-y: auto;
    line-height: 1.5;

    .console-line {
      display: flex;
      gap: vars.$spacing-sm;
      margin-bottom: 4px;

      .line-prefix {
        color: #4ec9b0;
        user-select: none;
      }

      .line-content {
        flex: 1;
        word-break: break-all;
      }

      &.error-line {
        .line-content {
          color: #f48771;
        }
      }
    }

    .empty-console {
      color: #858585;
      font-style: italic;
      text-align: center;
      padding: vars.$spacing-xl;
    }

    /* Scrollbar styling */
    &::-webkit-scrollbar {
      width: 8px;
    }

    &::-webkit-scrollbar-track {
      background: #2d2d2d;
      border-radius: 4px;
    }

    &::-webkit-scrollbar-thumb {
      background: #555;
      border-radius: 4px;

      &:hover {
        background: #666;
      }
    }
  }
}

.footer-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;

  .footer-left {
    .status-text {
      display: flex;
      align-items: center;
      gap: vars.$spacing-sm;
      color: vars.$color-text-secondary;
      font-size: vars.$font-size-sm;
    }
  }

  .footer-right {
    display: flex;
    gap: vars.$spacing-sm;
  }
}
</style>
