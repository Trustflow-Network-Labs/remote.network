<template>
  <Dialog
    v-model:visible="isVisible"
    :header="$t('message.workflows.selectInterfaces')"
    :modal="true"
    :style="{ width: '500px' }"
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
          <Checkbox
            v-model="selectedInterfaces"
            :inputId="`interface-${iface.interface_type}`"
            :value="iface.interface_type"
            :disabled="!canSelectInterface(iface)"
          />
          <label :for="`interface-${iface.interface_type}`" class="interface-label">
            <span class="interface-type">{{ iface.interface_type }}</span>
            <span v-if="iface.mount_function" class="mount-function">
              ({{ iface.mount_function }})
            </span>
            <span class="interface-path">{{ iface.path }}</span>
          </label>
        </div>
      </div>

      <div class="section">
        <h4>{{ $t('message.workflows.destinationInputs') }}</h4>
        <p class="destination-name">{{ toCardName }}</p>

        <div v-if="availableInputInterfaces.length === 0" class="no-interfaces">
          {{ $t('message.workflows.noInputInterfaces') }}
        </div>

        <div v-for="iface in availableInputInterfaces" :key="iface.interface_type" class="interface-item readonly">
          <i class="pi pi-arrow-down"></i>
          <span class="interface-label">
            <span class="interface-type">{{ iface.interface_type }}</span>
            <span v-if="iface.mount_function" class="mount-function">
              ({{ iface.mount_function }})
            </span>
            <span class="interface-path">{{ iface.path }}</span>
          </span>
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
        :disabled="selectedInterfaces.length === 0"
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
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
  'confirm': [selectedInterfaces: string[]]
  'cancel': []
}>()

const isVisible = ref(props.visible)
const selectedInterfaces = ref<string[]>([])

// Watch for prop changes
watch(() => props.visible, (newVal) => {
  isVisible.value = newVal
  if (newVal) {
    // Select all available output interfaces by default
    selectedInterfaces.value = availableOutputInterfaces.value.map(iface => iface.interface_type)
  }
})

// Get available output interfaces (STDOUT, STDERR, LOGS, MOUNT with OUTPUT/BOTH)
const availableOutputInterfaces = computed(() => {
  return props.fromInterfaces.filter(iface => {
    if (iface.interface_type === 'MOUNT') {
      return iface.mount_function === 'OUTPUT' || iface.mount_function === 'BOTH'
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
      return iface.mount_function === 'INPUT' || iface.mount_function === 'BOTH'
    }
    return iface.interface_type === 'STDIN'
  })
})

// Check if an interface can be selected
function canSelectInterface(iface: ServiceInterface): boolean {
  // Always allow selection of output interfaces
  return true
}

function onVisibilityChange(value: boolean) {
  emit('update:visible', value)
}

function confirm() {
  emit('confirm', selectedInterfaces.value)
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
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem;
  border: 1px solid var(--surface-border);
  border-radius: 4px;
  margin-bottom: 0.5rem;
  transition: all 0.2s;
}

.interface-item:hover {
  background: var(--surface-50);
}

.interface-item.readonly {
  background: var(--surface-100);
  cursor: default;
}

.interface-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex: 1;
  cursor: pointer;
}

.interface-item.readonly .interface-label {
  cursor: default;
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
