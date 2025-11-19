<template>
  <Dialog
    :visible="visible"
    @update:visible="(value: boolean) => emit('update:visible', value)"
    modal
    :header="$t('message.services.reviewInterfaces')"
    :style="{ width: '800px' }"
    :closable="false"
  >
    <div class="interface-review-container">
      <!-- Service Information -->
      <div class="service-info">
        <h3>{{ serviceName }}</h3>
        <p class="image-name">{{ imageName }}</p>
      </div>

      <!-- Docker Configuration (Entrypoint & CMD) -->
      <Card class="config-section">
        <template #title>
          <i class="pi pi-cog"></i> {{ $t('message.services.dockerConfiguration') }}
        </template>
        <template #content>
          <div class="config-grid">
            <div class="config-item">
              <label>{{ $t('message.services.entrypoint') }}</label>
              <div class="config-value">
                <code v-if="entrypoint && entrypoint.length > 0">{{ entrypoint.join(' ') }}</code>
                <span v-else class="empty-value">{{ $t('message.common.notSet') }}</span>
              </div>
            </div>
            <div class="config-item">
              <label>{{ $t('message.services.cmd') }}</label>
              <div class="config-value">
                <code v-if="cmd && cmd.length > 0">{{ cmd.join(' ') }}</code>
                <span v-else class="empty-value">{{ $t('message.common.notSet') }}</span>
              </div>
            </div>
          </div>
        </template>
      </Card>

      <!-- Interface Management -->
      <Card class="interfaces-section">
        <template #title>
          <div class="title-with-action">
            <span><i class="pi pi-link"></i> {{ $t('message.services.interfaces') }}</span>
            <Button
              :label="$t('message.services.addInterface')"
              icon="pi pi-plus"
              size="small"
              @click="addInterface"
              outlined
            />
          </div>
        </template>
        <template #content>
          <div v-if="interfaces.length === 0" class="empty-state">
            <i class="pi pi-info-circle"></i>
            <p>{{ $t('message.services.noInterfacesDetected') }}</p>
          </div>
          <div v-else class="interfaces-list">
            <div
              v-for="(iface, index) in interfaces"
              :key="index"
              class="interface-item"
              :class="{ 'has-mount-function': iface.interface_type === 'MOUNT' }"
            >
              <div class="interface-type">
                <Dropdown
                  v-model="iface.interface_type"
                  :options="interfaceTypes"
                  optionLabel="label"
                  optionValue="value"
                  :placeholder="$t('message.services.selectInterfaceType')"
                />
              </div>
              <div class="interface-path">
                <InputText
                  v-model="iface.path"
                  :placeholder="iface.interface_type === 'MOUNT' ? $t('message.services.mountPath') : $t('message.services.optional')"
                  :disabled="iface.interface_type !== 'MOUNT'"
                />
              </div>
              <div v-if="iface.interface_type === 'MOUNT'" class="interface-mount-function">
                <Dropdown
                  v-model="iface.mount_function"
                  :options="mountFunctionTypes"
                  optionLabel="label"
                  optionValue="value"
                  placeholder="Select usage"
                />
              </div>
              <div class="interface-description">
                <InputText
                  v-model="iface.description"
                  :placeholder="$t('message.common.description')"
                />
              </div>
              <div class="interface-actions">
                <Button
                  icon="pi pi-trash"
                  severity="danger"
                  text
                  rounded
                  @click="removeInterface(index)"
                  :title="$t('message.common.delete')"
                />
              </div>
            </div>
          </div>
        </template>
      </Card>

      <!-- Info Message -->
      <Message severity="info" :closable="false">
        {{ $t('message.services.interfaceReviewInfo') }}
      </Message>
    </div>

    <template #footer>
      <Button
        :label="$t('message.common.cancel')"
        icon="pi pi-times"
        @click="handleCancel"
        text
      />
      <Button
        :label="$t('message.services.saveInterfaces')"
        icon="pi pi-check"
        @click="handleConfirm"
        autofocus
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import Dialog from 'primevue/dialog'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Dropdown from 'primevue/dropdown'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'

interface ServiceInterface {
  interface_type: string
  path: string
  description: string
  mount_function?: string  // For MOUNT interfaces: INPUT, OUTPUT, or BOTH
}

interface Props {
  visible: boolean
  serviceName: string
  imageName: string
  suggestedInterfaces: any[]
  entrypoint?: string[] | null
  cmd?: string[] | null
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [interfaces: ServiceInterface[]]
  cancel: []
}>()

// Local state
const interfaces = ref<ServiceInterface[]>([])

// Interface types
const interfaceTypes = [
  { label: 'STDIN (Standard Input)', value: 'STDIN' },
  { label: 'STDOUT (Standard Output)', value: 'STDOUT' },
  { label: 'STDERR (Standard Error)', value: 'STDERR' },
  { label: 'LOGS (Container Logs)', value: 'LOGS' },
  { label: 'MOUNT (Volume Mount)', value: 'MOUNT' }
]

// Mount function types (for MOUNT interfaces)
const mountFunctionTypes = [
  { label: 'INPUT (Provides data to container)', value: 'INPUT' },
  { label: 'OUTPUT (Receives data from container)', value: 'OUTPUT' },
  { label: 'BOTH (Bidirectional)', value: 'BOTH' }
]

// Watch for visibility changes to initialize interfaces
watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    // Initialize interfaces from suggested interfaces
    interfaces.value = props.suggestedInterfaces.map(iface => ({
      interface_type: iface.interface_type || iface.InterfaceType || '',
      path: iface.path || iface.Path || '',
      description: iface.description || iface.Description || '',
      mount_function: iface.mount_function || iface.MountFunction || undefined
    }))
  }
}, { immediate: true })

const addInterface = () => {
  interfaces.value.push({
    interface_type: '',
    path: '',
    description: '',
    mount_function: undefined
  })
}

const removeInterface = (index: number) => {
  interfaces.value.splice(index, 1)
}

const handleConfirm = () => {
  // Validate interfaces
  const validInterfaces = interfaces.value.filter(iface => {
    if (!iface.interface_type) return false
    // MOUNT interfaces require a path and mount_function
    if (iface.interface_type === 'MOUNT') {
      if (!iface.path || !iface.mount_function) return false
    }
    return true
  })

  emit('confirm', validInterfaces)
  emit('update:visible', false)
}

const handleCancel = () => {
  emit('cancel')
  emit('update:visible', false)
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.interface-review-container {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-lg;
}

.service-info {
  text-align: center;
  padding: vars.$spacing-md;
  background: vars.$color-surface;
  border-radius: vars.$border-radius-md;

  h3 {
    margin: 0 0 vars.$spacing-xs 0;
    color: vars.$color-text;
    font-size: vars.$font-size-xl;
  }

  .image-name {
    margin: 0;
    color: vars.$color-text-secondary;
    font-family: 'Courier New', monospace;
    font-size: vars.$font-size-sm;
  }
}

.config-section,
.interfaces-section {
  :deep(.p-card-title) {
    display: flex;
    align-items: center;
    gap: vars.$spacing-sm;
    color: vars.$color-text;
    font-size: vars.$font-size-lg;
  }

  .title-with-action {
    display: flex;
    justify-content: space-between;
    align-items: center;
    width: 100%;
  }
}

.config-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: vars.$spacing-lg;

  .config-item {
    label {
      display: block;
      font-weight: 600;
      color: vars.$color-text-secondary;
      font-size: vars.$font-size-sm;
      text-transform: uppercase;
      margin-bottom: vars.$spacing-xs;
    }

    .config-value {
      code {
        display: block;
        background: rgba(vars.$color-primary, 0.1);
        padding: vars.$spacing-sm;
        border-radius: vars.$border-radius-sm;
        color: vars.$color-text;
        font-family: 'Courier New', monospace;
        font-size: vars.$font-size-sm;
        word-break: break-all;
      }

      .empty-value {
        display: block;
        padding: vars.$spacing-sm;
        color: vars.$color-text-secondary;
        font-style: italic;
      }
    }
  }
}

.empty-state {
  text-align: center;
  padding: vars.$spacing-xl;
  color: vars.$color-text-secondary;

  i {
    font-size: 2rem;
    margin-bottom: vars.$spacing-md;
    display: block;
  }

  p {
    margin: 0;
  }
}

.interfaces-list {
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-md;

  .interface-item {
    display: grid;
    grid-template-columns: 200px 1fr 1fr auto;
    gap: vars.$spacing-sm;
    align-items: center;
    padding: vars.$spacing-sm;
    background: vars.$color-surface;
    border-radius: vars.$border-radius-sm;
    border: 1px solid rgba(vars.$color-primary, 0.2);

    &.has-mount-function {
      grid-template-columns: 200px 1fr 180px 1fr auto;
    }

    .interface-type {
      :deep(.p-dropdown) {
        width: 100%;
      }
    }

    .interface-path,
    .interface-mount-function,
    .interface-description {
      :deep(.p-inputtext),
      :deep(.p-dropdown) {
        width: 100%;
      }
    }

    .interface-actions {
      display: flex;
      gap: vars.$spacing-xs;
    }
  }
}

:deep(.p-message) {
  .p-message-wrapper {
    padding: vars.$spacing-md;
  }
}
</style>
