<template>
  <Dialog
    v-model:visible="isVisible"
    :header="$t('message.workflows.selectInterfaces')"
    :modal="true"
    :style="{ width: '550px' }"
    @update:visible="onVisibilityChange"
  >
    <div class="interface-selector">
      <div class="section">
        <h4>{{ $t('message.workflows.sourceOutputs') }}</h4>
        <p class="source-name">{{ fromCardName }}</p>

        <div v-if="availableOutputInterfaces.length === 0" class="no-interfaces">
          {{ $t('message.workflows.noOutputInterfaces') }}
        </div>

        <div v-for="iface in availableOutputInterfaces" :key="iface.interface_type" class="interface-item">
          <div class="interface-row">
            <Checkbox
              v-model="selectedSourceOutputs"
              :inputId="`source-${iface.interface_type}`"
              :value="iface.interface_type"
              @change="onSourceOutputChange(iface.interface_type)"
            />
            <label :for="`source-${iface.interface_type}`" class="interface-label">
              <span class="interface-type">{{ iface.interface_type }}</span>
              <span v-if="iface.mount_function" class="mount-function">
                ({{ iface.mount_function }})
              </span>
              <span class="interface-path">{{ iface.path }}</span>
            </label>
          </div>
          <div v-if="selectedSourceOutputs.includes(iface.interface_type)" class="rename-row">
            <label class="rename-label">
              <i class="pi pi-pencil"></i>
              {{ $t('message.workflows.renameAs') }}:
            </label>
            <InputText
              v-model="renameMap[iface.interface_type]"
              :placeholder="$t('message.workflows.optionalNewName')"
              class="rename-input"
              size="small"
            />
          </div>
        </div>
      </div>

      <div class="section">
        <h4>{{ $t('message.workflows.destinationInputs') }}</h4>
        <p class="destination-name">{{ toCardName }}</p>

        <div v-if="availableInputInterfaces.length === 0" class="no-interfaces">
          {{ $t('message.workflows.noInputInterfaces') }}
        </div>

        <div v-for="iface in availableInputInterfaces" :key="iface.interface_type" class="interface-item">
          <div class="interface-row">
            <Checkbox
              v-model="selectedDestinationInputs"
              :inputId="`dest-${iface.interface_type}`"
              :value="iface.interface_type"
            />
            <label :for="`dest-${iface.interface_type}`" class="interface-label">
              <span class="interface-type">{{ iface.interface_type }}</span>
              <span v-if="iface.mount_function" class="mount-function">
                ({{ iface.mount_function }})
              </span>
              <span class="interface-path">{{ iface.path }}</span>
            </label>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <Button
        :label="$t('message.common.cancel')"
        icon="pi pi-times"
        @click="cancel"
        severity="secondary"
      />
      <Button
        :label="$t('message.common.create')"
        icon="pi pi-check"
        @click="confirm"
        :disabled="selectedSourceOutputs.length === 0 || selectedDestinationInputs.length === 0"
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch, reactive } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import InputText from 'primevue/inputtext'
import type { ServiceInterface } from '../../stores/workflows'

interface Props {
  visible: boolean
  fromCardName: string
  toCardName: string
  fromInterfaces: ServiceInterface[]
  toInterfaces: ServiceInterface[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  'confirm': [data: { sourceOutputs: string[], destinationInputs: string[], renameMap: Record<string, string> }]
  'cancel': []
}>()

const isVisible = ref(props.visible)
const selectedSourceOutputs = ref<string[]>([])
const selectedDestinationInputs = ref<string[]>([])
const renameMap = reactive<Record<string, string>>({})

// Watch for prop changes
watch(() => props.visible, (newVal) => {
  isVisible.value = newVal
  if (newVal) {
    // Select all available source outputs by default
    selectedSourceOutputs.value = availableOutputInterfaces.value.map(iface => iface.interface_type)

    // Select the first destination input by default (user can change)
    if (availableInputInterfaces.value.length > 0) {
      selectedDestinationInputs.value = [availableInputInterfaces.value[0].interface_type]
    }

    // Reset rename map
    Object.keys(renameMap).forEach(key => delete renameMap[key])
  }
})

function onSourceOutputChange(interfaceType: string) {
  // Clear rename when unchecked
  if (!selectedSourceOutputs.value.includes(interfaceType)) {
    delete renameMap[interfaceType]
  }
}

// Get available output interfaces (STDOUT, STDERR, LOGS, MOUNT with OUTPUT/BOTH)
const availableOutputInterfaces = computed(() => {
  return props.fromInterfaces.filter(iface => {
    if (iface.interface_type === 'MOUNT') {
      // If mount_function is not set, treat as BOTH (backwards compatibility)
      const mountFunc = iface.mount_function || 'BOTH'
      return mountFunc === 'OUTPUT' || mountFunc === 'BOTH'
    }
    return iface.interface_type === 'STDOUT' ||
           iface.interface_type === 'STDERR' ||
           iface.interface_type === 'LOGS'
  })
})

// Get available input interfaces (STDIN, MOUNT with INPUT/BOTH)
const availableInputInterfaces = computed(() => {
  return props.toInterfaces.filter(iface => {
    if (iface.interface_type === 'MOUNT') {
      // If mount_function is not set, treat as BOTH (backwards compatibility)
      const mountFunc = iface.mount_function || 'BOTH'
      return mountFunc === 'INPUT' || mountFunc === 'BOTH'
    }
    return iface.interface_type === 'STDIN'
  })
})

function onVisibilityChange(value: boolean) {
  emit('update:visible', value)
}

function confirm() {
  emit('confirm', {
    sourceOutputs: selectedSourceOutputs.value,
    destinationInputs: selectedDestinationInputs.value,
    renameMap: { ...renameMap }
  })
  isVisible.value = false
}

function cancel() {
  emit('cancel')
  isVisible.value = false
}
</script>

<style scoped>
.interface-selector {
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

.section h4 {
  margin: 0 0 0.5rem 0;
  color: var(--text-color);
  font-size: 1rem;
  font-weight: 600;
}

.source-name,
.destination-name {
  margin: 0 0 1rem 0;
  padding: 0.5rem;
  background: var(--surface-50);
  border-radius: 4px;
  font-weight: 500;
  color: var(--primary-color);
}

.no-interfaces {
  padding: 1rem;
  text-align: center;
  color: var(--text-color-secondary);
  font-style: italic;
}

.interface-item {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.75rem;
  border: 1px solid var(--surface-border);
  border-radius: 4px;
  margin-bottom: 0.5rem;
  transition: all 0.2s;
}

.interface-item:hover {
  background: var(--surface-50);
}

.interface-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.interface-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex: 1;
  cursor: pointer;
}

.rename-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding-top: 0.5rem;
  padding-left: 1.75rem;
  border-top: 1px dashed var(--surface-border);
}

.rename-label {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-size: 0.85rem;
  color: var(--text-color-secondary);
  white-space: nowrap;
}

.rename-input {
  flex: 1;
  font-size: 0.85rem;
}

.interface-type {
  font-weight: 600;
  color: var(--primary-color);
  min-width: 80px;
}

.mount-function {
  font-size: 0.875rem;
  color: var(--text-color-secondary);
}

.interface-path {
  color: var(--text-color-secondary);
  font-family: monospace;
  font-size: 0.875rem;
}
</style>
