<template>
  <Dialog
    v-model:visible="dialogVisible"
    :header="$t('message.workflows.configureJob')"
    :modal="true"
    :closable="true"
    :style="{ width: '500px' }"
    @hide="onCancel"
  >
    <div class="job-config-dialog">
      <!-- Job Info -->
      <div class="job-info">
        <div class="job-name">{{ job?.service_name || 'Unknown Service' }}</div>
        <div class="job-type" :class="jobTypeClass">{{ job?.service_type }}</div>
      </div>

      <!-- Docker Configuration (only for DOCKER type) -->
      <div v-if="isDocker" class="docker-config">
        <h4>{{ $t('message.services.dockerConfiguration') }}</h4>

        <!-- Entrypoint -->
        <div class="form-field">
          <label>{{ $t('message.services.entrypoint') }}</label>
          <InputText
            v-model="editableEntrypoint"
            :placeholder="$t('message.services.entrypointPlaceholder')"
          />
          <small class="help-text">{{ $t('message.services.entrypointHelp') }}</small>
        </div>

        <!-- Commands -->
        <div class="form-field">
          <label>{{ $t('message.services.cmd') }}</label>
          <InputText
            v-model="editableCommands"
            :placeholder="$t('message.services.cmdPlaceholder')"
          />
          <small class="help-text">{{ $t('message.services.cmdHelp') }}</small>
        </div>

        <div class="defaults-note">
          <i class="pi pi-info-circle"></i>
          <span>{{ $t('message.workflows.dockerDefaultsNote') }}</span>
        </div>
      </div>

      <!-- Non-Docker info -->
      <div v-else class="non-docker-info">
        <p>{{ $t('message.workflows.noConfigForType') }}</p>
      </div>
    </div>

    <template #footer>
      <Button
        :label="$t('message.common.cancel')"
        severity="secondary"
        @click="onCancel"
      />
      <Button
        v-if="isDocker"
        :label="$t('message.common.save')"
        @click="onSave"
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import type { WorkflowJob } from '../../stores/workflows'

interface Props {
  visible: boolean
  job: WorkflowJob | null
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  'save': [data: { entrypoint: string[], cmd: string[] }]
  'cancel': []
}>()

const editableEntrypoint = ref('')
const editableCommands = ref('')

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value)
})

const isDocker = computed(() => props.job?.service_type === 'DOCKER')

const jobTypeClass = computed(() => ({
  'data': props.job?.service_type === 'DATA',
  'docker': props.job?.service_type === 'DOCKER',
  'standalone': props.job?.service_type === 'STANDALONE'
}))

// Convert array of args to shell-like string (quote args with spaces)
function argsToShellString(args: string[] | undefined): string {
  if (!args || args.length === 0) return ''
  return args.map(arg => {
    // Quote arguments that contain spaces, quotes, or special chars
    if (/[\s"'\\]/.test(arg)) {
      // Escape any existing double quotes and wrap in double quotes
      return `"${arg.replace(/"/g, '\\"')}"`
    }
    return arg
  }).join(' ')
}

// Parse shell-like string to array of args (respects quotes)
function shellStringToArgs(input: string): string[] {
  const trimmed = input.trim()
  if (!trimmed) return []

  const args: string[] = []
  let current = ''
  let inQuote = false
  let quoteChar = ''
  let escaped = false

  for (let i = 0; i < trimmed.length; i++) {
    const char = trimmed[i]

    if (escaped) {
      current += char
      escaped = false
      continue
    }

    if (char === '\\') {
      escaped = true
      continue
    }

    if ((char === '"' || char === "'") && !inQuote) {
      inQuote = true
      quoteChar = char
      continue
    }

    if (char === quoteChar && inQuote) {
      inQuote = false
      quoteChar = ''
      continue
    }

    if (char === ' ' && !inQuote) {
      if (current) {
        args.push(current)
        current = ''
      }
      continue
    }

    current += char
  }

  if (current) {
    args.push(current)
  }

  return args
}

// Initialize form when job changes
watch(() => props.job, (newJob) => {
  if (newJob) {
    // Convert arrays to shell-like strings for editing (quote args with spaces)
    editableEntrypoint.value = argsToShellString(newJob.entrypoint)
    editableCommands.value = argsToShellString(newJob.cmd)
  }
}, { immediate: true })

function onSave() {
  // Parse shell-like strings back to arrays (respects quotes)
  const entrypoint = shellStringToArgs(editableEntrypoint.value)
  const cmd = shellStringToArgs(editableCommands.value)

  emit('save', { entrypoint, cmd })
  dialogVisible.value = false
}

function onCancel() {
  emit('cancel')
  dialogVisible.value = false
}
</script>

<style scoped lang="scss">
.job-config-dialog {
  .job-info {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 20px;
    padding-bottom: 15px;
    border-bottom: 1px solid #e0e0e0;

    .job-name {
      font-size: 1.1rem;
      font-weight: 600;
    }

    .job-type {
      padding: 4px 10px;
      border-radius: 4px;
      font-size: 0.75rem;
      font-weight: 600;
      color: white;

      &.data {
        background-color: rgb(205, 81, 36);
      }
      &.docker {
        background-color: #4060c3;
      }
      &.standalone {
        background-color: #4060c3;
      }
    }
  }

  .docker-config {
    h4 {
      margin: 0 0 15px 0;
      color: #333;
    }

    .form-field {
      margin-bottom: 15px;

      label {
        display: block;
        margin-bottom: 5px;
        font-weight: 500;
        color: #555;
      }

      input {
        width: 100%;
      }

      .help-text {
        display: block;
        margin-top: 4px;
        color: #888;
        font-size: 0.85rem;
      }
    }

    .defaults-note {
      display: flex;
      align-items: flex-start;
      gap: 8px;
      margin-top: 20px;
      padding: 10px;
      background-color: #f0f7ff;
      border-radius: 4px;
      color: #1e88e5;
      font-size: 0.85rem;

      i {
        margin-top: 2px;
      }
    }
  }

  .non-docker-info {
    padding: 20px;
    text-align: center;
    color: #666;
  }
}
</style>
