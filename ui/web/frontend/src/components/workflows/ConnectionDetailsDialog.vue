<template>
  <Dialog
    :visible="visible"
    :modal="true"
    :closable="true"
    :draggable="false"
    class="connection-details-dialog"
    @update:visible="$emit('update:visible', $event)"
  >
    <template #header>
      <div class="dialog-header">
        <i class="pi pi-link"></i>
        <span>{{ $t('message.workflows.connectionDetails') }}</span>
      </div>
    </template>

    <div class="connection-info">
      <div class="node-info">
        <div class="from-node">
          <i class="pi pi-arrow-right"></i>
          <span class="node-label">{{ $t('message.workflows.from') }}:</span>
          <span class="node-name">{{ fromCardName }}</span>
        </div>
        <div class="to-node">
          <i class="pi pi-arrow-right"></i>
          <span class="node-label">{{ $t('message.workflows.to') }}:</span>
          <span class="node-name">{{ toCardName }}</span>
        </div>
      </div>

      <div class="interfaces-section">
        <h4>{{ $t('message.workflows.connectedInterfaces') }}</h4>
        <div v-if="localInterfaces.length > 0" class="interfaces-list">
          <div
            v-for="(iface, index) in localInterfaces"
            :key="`${iface.from_interface_type}-${iface.to_interface_type}-${iface.id}`"
            class="interface-item"
          >
            <div class="interface-row">
              <span class="interface-type output">{{ iface.from_interface_type }}</span>
              <i class="pi pi-arrow-right arrow-icon"></i>
              <span class="interface-type input">{{ iface.to_interface_type }}</span>
            </div>
            <div class="rename-row">
              <label :for="`rename-${index}`" class="rename-label">
                <i class="pi pi-pencil"></i>
                {{ $t('message.workflows.renameAs') }}:
              </label>
              <InputText
                :id="`rename-${index}`"
                v-model="localInterfaces[index].destination_file_name"
                :placeholder="$t('message.workflows.optionalNewName')"
                class="rename-input"
                size="small"
              />
            </div>
          </div>
        </div>
        <div v-else class="no-interfaces">
          {{ $t('message.workflows.noInterfacesConnected') }}
        </div>
      </div>
    </div>

    <template #footer>
      <div class="dialog-footer">
        <Button
          :label="$t('message.common.cancel')"
          icon="pi pi-times"
          @click="onCancel"
          class="p-button-text"
        />
        <Button
          v-if="hasChanges"
          :label="$t('message.common.save')"
          icon="pi pi-save"
          @click="onSave"
          class="p-button-success"
        />
        <Button
          :label="$t('message.workflows.deleteConnection')"
          icon="pi pi-trash"
          @click="onDelete"
          class="p-button-danger"
        />
      </div>
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'

interface ConnectionInterface {
  id: number
  from_interface_type: string
  to_interface_type: string
  destination_file_name?: string | null
}

const props = defineProps<{
  visible: boolean
  fromCardName: string
  toCardName: string
  interfaces: ConnectionInterface[]
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'delete'): void
  (e: 'cancel'): void
  (e: 'save', interfaces: ConnectionInterface[]): void
}>()

// Local copy of interfaces for editing
const localInterfaces = ref<ConnectionInterface[]>([])

// Watch for prop changes and create local copy
watch(() => props.interfaces, (newInterfaces) => {
  localInterfaces.value = newInterfaces.map(iface => ({
    ...iface,
    destination_file_name: iface.destination_file_name || ''
  }))
}, { immediate: true, deep: true })

// Watch for visibility changes to reset local state
watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    localInterfaces.value = props.interfaces.map(iface => ({
      ...iface,
      destination_file_name: iface.destination_file_name || ''
    }))
  }
})

// Check if there are any changes
const hasChanges = computed(() => {
  return localInterfaces.value.some((local, index) => {
    const original = props.interfaces[index]
    const originalName = original?.destination_file_name || ''
    const localName = local.destination_file_name || ''
    return localName !== originalName
  })
})

function onDelete() {
  emit('delete')
}

function onCancel() {
  emit('cancel')
  emit('update:visible', false)
}

function onSave() {
  // Only include interfaces that have a destination_file_name set
  const updatedInterfaces = localInterfaces.value.map(iface => ({
    ...iface,
    destination_file_name: iface.destination_file_name?.trim() || null
  }))
  emit('save', updatedInterfaces)
  emit('update:visible', false)
}
</script>

<style scoped>
.connection-details-dialog {
  width: 500px;
}

.dialog-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 1.1rem;
  font-weight: 600;
}

.connection-info {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
  padding: 1rem 0;
}

.node-info {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1rem;
  background: var(--surface-50);
  border-radius: 6px;
}

.from-node,
.to-node {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.node-label {
  font-weight: 600;
  color: var(--text-color-secondary);
  min-width: 50px;
}

.node-name {
  font-weight: 500;
  color: var(--text-color);
}

.interfaces-section h4 {
  margin: 0 0 1rem 0;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--text-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.interfaces-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.interface-item {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  background: var(--surface-0);
  border: 1px solid var(--surface-border);
  border-radius: 6px;
  transition: all 0.2s;
}

.interface-item:hover {
  background: var(--surface-50);
  border-color: var(--primary-color);
}

.interface-row {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.rename-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding-top: 0.5rem;
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
  padding: 0.4rem 0.8rem;
  border-radius: 4px;
  font-weight: 600;
  font-size: 0.85rem;
  font-family: monospace;
  flex: 1;
  text-align: center;
}

.interface-type.output {
  background: rgba(205, 81, 36, 0.1);
  color: rgb(205, 81, 36);
  border: 1px solid rgba(205, 81, 36, 0.3);
}

.interface-type.input {
  background: rgba(36, 153, 205, 0.1);
  color: rgb(36, 100, 205);
  border: 1px solid rgba(36, 153, 205, 0.3);
}

.arrow-icon {
  color: var(--text-color-secondary);
  font-size: 1rem;
}

.no-interfaces {
  padding: 2rem;
  text-align: center;
  color: var(--text-color-secondary);
  font-style: italic;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
