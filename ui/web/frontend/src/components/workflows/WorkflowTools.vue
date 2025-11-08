<template>
  <div class="workflow-tools">
    <div class="workflow-tools-container">
      <!-- Workflow Details Section -->
      <div class="workflow-details">
        <div class="workflow-details-header">
          <div class="workflow-details-header-title">
            <i class="pi pi-receipt"></i> {{ $t('message.workflows.details') }}
          </div>
          <div class="workflow-details-header-window-controls">
            <i
              :class="['pi', workflowDetailsExpanded ? 'pi-window-minimize' : 'pi-window-maximize']"
              @click="toggleWorkflowDetails"
            ></i>
          </div>
        </div>
        <div v-show="!workflowDetailsExpanded" class="workflow-details-body">
          <div class="workflow-details-body-section">
            <FloatLabel variant="on">
              <InputText id="workflowName" v-model="workflowName" />
              <label for="workflowName">{{ $t('message.workflows.name') }}</label>
            </FloatLabel>
          </div>
          <div class="workflow-details-body-section">
            <FloatLabel variant="on">
              <Textarea id="workflowDescription" v-model="workflowDescription" rows="5" style="resize: none" />
              <label for="workflowDescription">{{ $t('message.workflows.description') }}</label>
            </FloatLabel>
          </div>
          <div class="workflow-details-body-section in-line-stretch">
            <div class="workflow-details-body-section-left">
              <div>{{ $t('message.workflows.snapToGrid') }}</div>
              <div>
                <ToggleButton
                  v-model="snapToGrid"
                  onLabel="On"
                  offLabel="Off"
                  size="small"
                />
              </div>
            </div>
            <div class="workflow-details-body-section-right">
              <div class="input-box">
                <button class="btn light" @click="deleteWorkflow">
                  <i class="pi pi-times-circle"></i> {{ $t('message.common.delete') }}
                </button>
              </div>
              <div class="input-box">
                <button class="btn" @click="saveWorkflow">
                  <i class="pi pi-save"></i> {{ $t('message.common.save') }}
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Service Search Section -->
      <div class="search-services">
        <div class="search-services-header">
          <div class="search-services-header-title">
            <i class="pi pi-search"></i> {{ $t('message.workflows.searchServices') }}
          </div>
          <div class="search-services-header-window-controls">
            <i
              :class="['pi', serviceSearchExpanded ? 'pi-window-minimize' : 'pi-window-maximize']"
              @click="toggleServiceSearch"
            ></i>
          </div>
        </div>
        <div v-show="!serviceSearchExpanded" class="search-services-body">
          <div class="search-services-body-section">
            <InputGroup>
              <InputText
                v-model="searchQuery"
                :placeholder="$t('message.workflows.searchPlaceholder')"
              />
              <InputGroupAddon>
                <Button
                  icon="pi pi-search"
                  severity="secondary"
                  variant="text"
                  @click="toggleSearchServiceTypes"
                />
              </InputGroupAddon>
            </InputGroup>
            <Menu ref="menu" :model="searchServicesTypes" popup class="!min-w-fit" />
          </div>

          <div class="separator">{{ $t('message.workflows.servicesFound') }}:</div>

          <div v-if="servicesStore.loading" class="loading">
            <ProgressSpinner style="width:30px;height:30px" strokeWidth="4" />
          </div>

          <div v-else class="service-offers">
            <div
              v-for="service in filteredServices"
              :key="service.id"
              class="service-item"
              draggable="true"
              @dragstart="onDragStart($event, service)"
              @dragend="onDragEnd"
            >
              <div class="service-item-icon">
                <i :class="getServiceIcon(service.service_type)"></i>
              </div>
              <div class="service-item-details">
                <div class="service-item-name">{{ service.name }}</div>
                <div class="service-item-type">{{ service.service_type }}</div>
                <div class="service-item-node">{{ service.node_id?.substring(0, 12) }}...</div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import InputGroup from 'primevue/inputgroup'
import InputGroupAddon from 'primevue/inputgroupaddon'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import FloatLabel from 'primevue/floatlabel'
import ToggleButton from 'primevue/togglebutton'
import ProgressSpinner from 'primevue/progressspinner'
import Menu from 'primevue/menu'
import { useServicesStore } from '../../stores/services'
import { useWorkflowsStore } from '../../stores/workflows'
import { useTextUtils } from '../../composables/useTextUtils'

// @ts-ignore - t is used in template
const { t } = useI18n()
const servicesStore = useServicesStore() as any // TODO: Fix Pinia typing
const workflowsStore = useWorkflowsStore() as any // TODO: Fix Pinia typing
const { generateRandomName } = useTextUtils()

const emit = defineEmits<{
  save: [data: { name: string; description: string }]
  execute: []
  delete: []
  snapToGrid: [enabled: boolean]
}>()

// Workflow Details
const workflowDetailsExpanded = ref(false)
const workflowName = ref('')
const workflowDescription = ref('')
const snapToGrid = ref(false)

// Service Search
const serviceSearchExpanded = ref(false)
const searchQuery = ref('')
const selectedServiceTypes = ref<string[]>(['DATA', 'DOCKER', 'STANDALONE'])
const menu = ref()

// Menu items for service type filter
const searchServicesTypes = computed(() => [
  {
    label: 'DATA',
    icon: 'pi pi-file',
    command: () => toggleServiceType('DATA')
  },
  {
    label: 'DOCKER',
    icon: 'pi pi-box',
    command: () => toggleServiceType('DOCKER')
  },
  {
    label: 'STANDALONE',
    icon: 'pi pi-code',
    command: () => toggleServiceType('STANDALONE')
  }
])

const filteredServices = computed(() => {
  let services = servicesStore.services || []

  // Filter by service type
  if (selectedServiceTypes.value.length > 0) {
    services = services.filter((s: any) =>
      selectedServiceTypes.value.includes(s.service_type)
    )
  }

  // Filter by search query
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    services = services.filter((s: any) =>
      s.name.toLowerCase().includes(query) ||
      s.description?.toLowerCase().includes(query) ||
      s.node_id?.toLowerCase().includes(query)
    )
  }

  return services
})

function toggleWorkflowDetails() {
  workflowDetailsExpanded.value = !workflowDetailsExpanded.value
}

function toggleServiceSearch() {
  serviceSearchExpanded.value = !serviceSearchExpanded.value
}

function toggleSearchServiceTypes(event: any) {
  menu.value.toggle(event)
}

function toggleServiceType(type: string) {
  const index = selectedServiceTypes.value.indexOf(type)
  if (index > -1) {
    selectedServiceTypes.value.splice(index, 1)
  } else {
    selectedServiceTypes.value.push(type)
  }
}

function saveWorkflow() {
  emit('save', {
    name: workflowName.value,
    description: workflowDescription.value
  })
}

function executeWorkflow() {
  emit('execute')
}

function deleteWorkflow() {
  emit('delete')
}

async function searchServices() {
  await servicesStore.fetchServices()
}

function getServiceIcon(serviceType: string): string {
  switch (serviceType) {
    case 'DATA':
      return 'pi pi-file'
    case 'DOCKER':
      return 'pi pi-box'
    case 'STANDALONE':
      return 'pi pi-code'
    default:
      return 'pi pi-question-circle'
  }
}

function onDragStart(event: DragEvent, service: any) {
  workflowsStore.setPickedService(service)
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'copy'
  }
}

function onDragEnd() {
  // Cleanup if needed
}

// Watch snap to grid changes
watch(snapToGrid, (newValue) => {
  emit('snapToGrid', newValue)
})

// Load current workflow data
watch(() => workflowsStore.currentWorkflow, (workflow) => {
  if (workflow) {
    workflowName.value = workflow.name
    workflowDescription.value = workflow.description
    snapToGrid.value = workflow.snap_to_grid
  }
}, { immediate: true })

// Initialize on mount
onMounted(() => {
  // Generate random workflow name if not set
  if (!workflowName.value) {
    workflowName.value = generateRandomName()
  }

  // Load services
  searchServices()
})

defineExpose({
  workflowName,
  workflowDescription,
  snapToGrid
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

:global(body) {
  --p-floatlabel-on-active-background: #fff;
  --p-inputtext-focus-border-color: rgb(27, 38, 54);
  --p-floatlabel-focus-color: rgb(27, 38, 54);
  --p-textarea-background: #fff;
  --p-textarea-color: rgb(27, 38, 54);
  --p-textarea-focus-border-color: rgba(205, 81, 36, .5);
  --p-togglebutton-background: #fff;
  --p-togglebutton-border-color: none;
  --p-togglebutton-hover-background: rgb(246, 114, 66);
  --p-togglebutton-hover-color: #fff;
  --p-togglebutton-checked-background: rgb(246, 114, 66);
  --p-togglebutton-checked-border-color: none;
  --p-togglebutton-content-checked-background: rgb(246, 114, 66);
  --p-inputtext-background: rgba(240, 240, 240, 1);
  --p-inputtext-color: rgb(27, 38, 54);
  --p-inputtext-border-color: rgba(205, 81, 36, .5);
  --p-inputtext-focus-border-color: rgba(205, 81, 36, .5);
  --p-inputgroup-addon-border-color: rgba(205, 81, 36, .5);
  --p-inputgroup-addon-background: rgba(205, 81, 36, 1);
  --p-inputgroup-addon-color: rgba(240, 240, 240, 1);
  --p-button-text-secondary-color: rgba(240, 240, 240, 1);
  --p-button-text-secondary-hover-background: rgb(246, 114, 66);
}

.workflow-tools {
  position: fixed;
  right: 0;
  top: 0;
  width: 320px;
  max-height: 100vh;
  background-color: transparent;
  overflow: visible;
  z-index: 100;

  input,
  textarea {
    width: 100%;
  }

  .workflow-tools-container {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    max-height: 100vh;

    .search-services {
      flex: 1 1 auto;
      position: relative;
      display: flex;
      flex-direction: column;
      width: 100%;
      min-height: 0; // Important for flex overflow
      max-height: calc(100vh - 60px); // Leave room for workflow details header at minimum
    }

    .workflow-details {
      flex-shrink: 0;
    }

    .workflow-details,
    .search-services {
      position: relative;
      margin: 0;
      padding: 0;
      box-sizing: border-box;
      border: 1px dotted rgba(205, 81, 36, .5);
      background-color: rgb(27, 38, 54);

      .workflow-details-header,
      .search-services-header {
        position: relative;
        text-align: left;
        height: 40px;
        line-height: 40px;
        background-color: rgba(205, 81, 36, 1);
        padding: 10px;
        display: flex;
        flex-direction: row;
        flex-wrap: nowrap;
        justify-content: space-between;
        align-content: center;
        align-items: center;

        .workflow-details-header-title,
        .search-services-header-title {
          color: #fff;
          font-weight: 600;
        }

        .workflow-details-header-window-controls,
        .search-services-header-window-controls {
          cursor: pointer;
          margin-left: 8px;
          color: #fff;
        }
      }

      .workflow-details-body,
      .search-services-body {
        position: relative;
        text-align: left;
        background-color: rgb(38, 49, 65);
        padding: 10px 5px;
      }

      .search-services-body {
        flex: 1;
        position: relative;
        display: flex;
        flex-direction: column;
        width: 100%;
        min-height: 0; // Important for flex overflow
      }

      .workflow-details-body-section,
      .search-services-body-section {
        margin: .5rem 0;

        textarea {
          background-color: var(--p-textarea-background) !important;
          color:  var(--p-textarea-color) !important;
          &:focus {
            border-color: var(--p-textarea-focus-border-color) !important;
          }
        }

        input {
          background-color: var(--p-inputtext-background) !important;
          color:  var(--p-inputtext-color) !important;
          border-color: var(--p-inputtext-border-color) !important;
          &:focus {
            border-color: var(--p-inputtext-focus-border-color) !important;
          }
        }

        &:first-child {
          margin-top: 0;
        }

        &:last-child {
          margin-bottom: 0;
        }

        &.in-line-stretch {
          display: flex;
          flex-direction: row;
          flex-wrap: nowrap;
          justify-content: space-between;
          align-content: center;
          align-items: center;

          .workflow-details-body-section-left {
            display: flex;
            flex-direction: row;
            flex-wrap: nowrap;
            justify-content: flex-start;
            align-content: center;
            align-items: center;

            div {
              font-size: .75rem;
              color: #fff;

              &:first-child {
                margin-right: .5rem;
              }
            }
          }

          .workflow-details-body-section-right {
            flex: 1;
            display: flex;
            flex-direction: row;
            flex-wrap: nowrap;
            justify-content: flex-end;
            align-content: center;
            align-items: center;
          }
        }
      }

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
          }

          &:hover {
            background-color: rgb(246, 114, 66);
          }
        }
      }

      .separator {
        flex-shrink: 0;
        display: flex;
        align-items: center;
        text-align: center;
        font-size: 10px;
        margin: 16px 0;
        color: #fff;

        &::before,
        &::after {
          content: '';
          flex: 1;
          border-bottom: 1px solid rgba(205, 81, 36, 1);
        }

        &:not(:empty)::before {
          margin-right: .25em;
        }

        &:not(:empty)::after {
          margin-left: .25em;
        }
      }

      .service-offers {
        flex: 1;
        overflow: auto;
      }
    }
  }
}

.loading {
  display: flex;
  justify-content: center;
  padding: vars.$spacing-lg;
}

.service-item {
  display: flex;
  gap: vars.$spacing-sm;
  padding: vars.$spacing-sm;
  background: rgba(205, 81, 36, 0.1);
  border: 1px solid rgba(205, 81, 36, .5);
  border-radius: 6px;
  cursor: grab;
  transition: all 0.2s ease;
  margin-bottom: 8px;

  &:hover {
    background: rgba(205, 81, 36, 0.2);
    transform: translateX(-4px);
  }

  &:active {
    cursor: grabbing;
  }
}

.service-item-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  background: rgba(205, 81, 36, 1);
  border-radius: 6px;
  color: white;

  i {
    font-size: 1.2rem;
  }
}

.service-item-details {
  flex: 1;
  min-width: 0;

  .service-item-name {
    font-weight: 600;
    font-size: vars.$font-size-sm;
    color: #fff;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .service-item-type {
    font-size: vars.$font-size-xs;
    color: rgb(246, 114, 66);
    font-weight: 500;
  }

  .service-item-node {
    font-size: vars.$font-size-xs;
    color: rgba(255, 255, 255, 0.7);
    font-family: monospace;
  }
}
</style>
