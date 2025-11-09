<template>
  <AppLayout>
    <div class="workflow-editor">
      <div
        ref="grid"
        class="workflow-grid"
        :style="gridStyles"
        @dragover="onDragOver"
        @drop="onDrop"
      >
        <!-- Service Cards -->
        <ServiceCard
          v-for="card in serviceCards"
          :key="card.id"
          :ref="(el) => setCardRef(el, card.id)"
          :job="card.job"
          :service-card-id="card.id"
          @close-service-card="removeServiceCard"
          @configure-service-card="configureServiceCard"
        />
      </div>

      <!-- Workflow Tools Panel -->
      <WorkflowTools
        ref="workflowToolsRef"
        @save="saveWorkflow"
        @execute="executeWorkflow"
        @delete="confirmDeleteWorkflow"
        @snap-to-grid="updateSnapToGrid"
      />

      <!-- Loading Overlay -->
      <div v-if="loading" class="loading-overlay">
        <ProgressSpinner />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import PlainDraggable from 'plain-draggable'
import ProgressSpinner from 'primevue/progressspinner'

import AppLayout from '../layout/AppLayout.vue'
import ServiceCard from './ServiceCard.vue'
import WorkflowTools from './WorkflowTools.vue'
import { useWorkflowsStore } from '../../stores/workflows'
import type { WorkflowJob } from '../../stores/workflows'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()
const workflowsStore = useWorkflowsStore() as any // TODO: Fix Pinia typing

// Grid configuration
const gridBoxXSize = 160
const gridBoxYSize = 100
const snapGravity = 80

// State
const loading = ref(false)
const grid = ref<HTMLElement | null>(null)
const workflowToolsRef = ref<any>(null)
const snapToGrid = ref(false)
const serviceCards = ref<Array<{
  id: number
  job: WorkflowJob
  x: number
  y: number
}>>([])

// Draggable instances
const draggableInstances: Map<number, any> = new Map()
const cardRefs: Map<number, any> = new Map()

// Grid styling
const gridStyles = computed(() => ({
  '--grid-box-x-size': `${gridBoxXSize}px`,
  '--grid-box-y-size': `${gridBoxYSize}px`,
}))

// Snap targets for PlainDraggable
const snapTargets = ref<Array<{ x: number; y: number }>>([])

function initializeSnapTargets() {
  const gridWidth = window.innerWidth
  const gridHeight = window.innerHeight
  const xPoints = Math.ceil(gridWidth / gridBoxXSize)
  const yPoints = Math.ceil(gridHeight / gridBoxYSize)

  snapTargets.value = []
  for (let i = 0; i < xPoints; i++) {
    for (let j = 0; j < yPoints; j++) {
      snapTargets.value.push({ x: i * gridBoxXSize, y: j * gridBoxYSize })
    }
  }
}

function setCardRef(el: any, cardId: number) {
  if (el) {
    cardRefs.set(cardId, el)
  }
}

function onDragOver(event: DragEvent) {
  event.preventDefault()
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'copy'
  }
}

async function onDrop(event: DragEvent) {
  event.preventDefault()

  const pickedService = workflowsStore.pickedService
  if (!pickedService) return

  let x = event.clientX
  let y = event.clientY

  // Adjust for grid offset
  if (grid.value) {
    const rect = grid.value.getBoundingClientRect()
    x -= rect.left
    y -= rect.top
  }

  // Snap to grid if enabled
  if (snapToGrid.value) {
    x = Math.round(x / gridBoxXSize) * gridBoxXSize
    y = Math.round(y / gridBoxYSize) * gridBoxYSize
  }

  await addServiceToWorkflow(pickedService, x, y)
  workflowsStore.setPickedService(null)
}

async function addServiceToWorkflow(service: any, x: number, y: number) {
  loading.value = true

  try {
    // Create workflow if it doesn't exist
    if (!workflowsStore.currentWorkflow) {
      const name = workflowToolsRef.value?.workflowName || 'Untitled Workflow'
      const description = workflowToolsRef.value?.workflowDescription || ''

      const workflow = await workflowsStore.createWorkflow({ name, description })

      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: t('message.workflows.created'),
        life: 3000
      })

      // Verify workflow was created
      if (!workflow || !workflow.id) {
        throw new Error('Failed to create workflow')
      }
    }

    // Verify currentWorkflow exists before proceeding
    if (!workflowsStore.currentWorkflow || !workflowsStore.currentWorkflow.id) {
      throw new Error('No active workflow')
    }

    // Add job to workflow
    const job = await workflowsStore.addWorkflowJob(workflowsStore.currentWorkflow.id, {
      workflow_id: workflowsStore.currentWorkflow.id,
      service_id: service.id,
      peer_id: service.peer_id, // App Peer ID (not DHT node_id)
      service_name: service.name,
      service_type: service.service_type,
      order: serviceCards.value.length,
      gui_x: x,
      gui_y: y,
      pricing_amount: service.pricing_amount,
      pricing_type: service.pricing_type,
      pricing_interval: service.pricing_interval,
      pricing_unit: service.pricing_unit
    })

    // Add card to canvas using the database ID from the job
    const cardId = job.id // Use actual database ID instead of array length
    serviceCards.value.push({
      id: cardId,
      job: job,
      x,
      y
    })

    // Initialize draggable for the new card
    await nextTick()
    initializeDraggableCard(cardId, x, y)

    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.workflows.jobAdded'),
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.workflows.jobAddError'),
      life: 5000
    })
  } finally {
    loading.value = false
  }
}

function initializeDraggableCard(cardId: number, x: number, y: number) {
  const card = serviceCards.value.find(c => c.id === cardId)
  if (!card) {
    console.warn('Card not found:', cardId)
    return
  }

  const cardEl = cardRefs.get(cardId)
  if (!cardEl) {
    console.warn('Card ref not found:', cardId)
    return
  }

  // Get the actual DOM element - in Vue 3, component refs might be the component instance or the element
  let domElement = cardEl
  if (cardEl.$el) {
    domElement = cardEl.$el
  }

  if (!domElement || !(domElement instanceof HTMLElement)) {
    console.warn('Invalid DOM element for card:', cardId, domElement)
    return
  }

  // Clean up existing draggable instance
  if (draggableInstances.has(cardId)) {
    const instance = draggableInstances.get(cardId)
    if (instance && instance.remove) {
      instance.remove()
    }
    draggableInstances.delete(cardId)
  }

  // Create new draggable instance
  const draggable = new PlainDraggable(domElement, {
    autoScroll: true
  })

  draggable.left = x
  draggable.top = y

  if (snapToGrid.value) {
    draggable.snap = {
      targets: snapTargets.value,
      gravity: snapGravity
    }
  }

  draggable.onDragEnd = async (pos: { left: number; top: number }) => {
    card.x = pos.left
    card.y = pos.top

    // Update job GUI props in database
    if (workflowsStore.currentWorkflow) {
      try {
        await workflowsStore.updateJobGUIProps(
          workflowsStore.currentWorkflow.id,
          card.job.id,
          pos.left,
          pos.top
        )
      } catch (error) {
        console.error('Failed to update job position:', error)
      }
    }
  }

  draggableInstances.set(cardId, draggable)
}

async function removeServiceCard(cardId: number) {
  const card = serviceCards.value.find(c => c.id === cardId)
  if (!card) return

  // Clean up draggable instance first
  if (draggableInstances.has(cardId)) {
    const instance = draggableInstances.get(cardId)
    if (instance && instance.remove) {
      instance.remove()
    }
    draggableInstances.delete(cardId)
  }

  // Remove card from array
  serviceCards.value = serviceCards.value.filter(c => c.id !== cardId)

  // Remove from database if workflow exists and job has an ID
  if (workflowsStore.currentWorkflow && card.job.id) {
    loading.value = true
    try {
      await workflowsStore.removeWorkflowJob(workflowsStore.currentWorkflow.id, card.job.id)

      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: 'Service removed from workflow',
        life: 3000
      })
    } catch (error: any) {
      console.error('Failed to remove job from database:', error)
      // UI already updated, don't confuse user with error
    } finally {
      loading.value = false
    }
  }
}

function configureServiceCard(_cardId: number) {
  // TODO: Implement service card configuration dialog
  toast.add({
    severity: 'info',
    summary: t('message.common.info'),
    detail: 'Configuration coming soon',
    life: 3000
  })
}

async function saveWorkflow(data: { name: string; description: string }) {
  loading.value = true

  try {
    if (workflowsStore.currentWorkflow?.id) {
      await workflowsStore.updateWorkflow(workflowsStore.currentWorkflow.id, data)
      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: t('message.workflows.updated'),
        life: 3000
      })
    } else {
      await workflowsStore.createWorkflow(data)
      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: t('message.workflows.created'),
        life: 3000
      })
    }
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.workflows.saveError'),
      life: 5000
    })
  } finally {
    loading.value = false
  }
}

async function executeWorkflow() {
  if (!workflowsStore.currentWorkflow?.id) {
    toast.add({
      severity: 'warn',
      summary: t('message.common.warning'),
      detail: t('message.workflows.noWorkflowToExecute'),
      life: 3000
    })
    return
  }

  loading.value = true

  try {
    await workflowsStore.executeWorkflow(workflowsStore.currentWorkflow.id)
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.workflows.executionStarted'),
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.workflows.executeError'),
      life: 5000
    })
  } finally {
    loading.value = false
  }
}

function confirmDeleteWorkflow() {
  if (!workflowsStore.currentWorkflow?.id) {
    toast.add({
      severity: 'warn',
      summary: t('message.common.warning'),
      detail: t('message.workflows.noWorkflowToDelete'),
      life: 3000
    })
    return
  }

  confirm.require({
    message: t('message.workflows.deleteConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      await deleteWorkflow()
    }
  })
}

async function deleteWorkflow() {
  if (!workflowsStore.currentWorkflow?.id) return

  loading.value = true

  try {
    const workflowId = workflowsStore.currentWorkflow.id
    await workflowsStore.deleteWorkflow(workflowId)

    // Clean up
    serviceCards.value = []
    draggableInstances.clear()
    cardRefs.clear()
    workflowsStore.clearCurrentWorkflow()

    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.workflows.deleteSuccess'),
      life: 3000
    })

    // Navigate back to workflows list
    router.push({ name: 'Workflows' })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.workflows.deleteError'),
      life: 5000
    })
  } finally {
    loading.value = false
  }
}

function updateSnapToGrid(enabled: boolean) {
  snapToGrid.value = enabled

  // Update all existing draggable instances
  draggableInstances.forEach((draggable) => {
    if (enabled) {
      draggable.snap = {
        targets: snapTargets.value,
        gravity: snapGravity
      }
    } else {
      draggable.snap = false
    }
  })

  // Save UI state to database only if workflow exists AND has an ID
  if (workflowsStore.currentWorkflow?.id) {
    workflowsStore.updateUIState(workflowsStore.currentWorkflow.id, {
      snap_to_grid: enabled
    })
  }
}

async function loadWorkflow() {
  const workflowId = route.params.id

  // Handle new workflow or invalid ID
  if (!workflowId || workflowId === 'design' || workflowId === 'new' || Array.isArray(workflowId)) {
    workflowsStore.clearCurrentWorkflow()
    return
  }

  // Validate numeric ID
  const numericId = Number(workflowId)
  if (isNaN(numericId) || numericId <= 0) {
    console.warn('Invalid workflow ID:', workflowId)
    workflowsStore.clearCurrentWorkflow()
    return
  }

  loading.value = true

  try {
    const workflow = await workflowsStore.fetchWorkflow(numericId)

    // Load service cards from workflow jobs (nodes)
    if (workflow.jobs && workflow.jobs.length > 0) {
      serviceCards.value = workflow.jobs.map((job: WorkflowJob, index: number) => ({
        id: index,
        job: job,
        x: job.gui_x || 100 + (index * 50),
        y: job.gui_y || 100 + (index * 50)
      }))

      // Initialize draggables
      await nextTick()
      serviceCards.value.forEach(card => {
        initializeDraggableCard(card.id, card.x, card.y)
      })
    }

    // Load UI state
    if (workflowsStore.currentUIState) {
      snapToGrid.value = workflowsStore.currentUIState.snap_to_grid

      // Apply snap-to-grid to existing draggable instances
      updateSnapToGrid(snapToGrid.value)
    }
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.workflows.loadError'),
      life: 5000
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  initializeSnapTargets()
  loadWorkflow()
})

onUnmounted(() => {
  // Clean up all draggable instances
  draggableInstances.forEach((instance) => {
    if (instance && instance.remove) {
      instance.remove()
    }
  })
  draggableInstances.clear()
  cardRefs.clear()
})

// Watch for route changes to reload workflow
watch(() => route.params.id, () => {
  loadWorkflow()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.workflow-editor {
  position: relative;
  width: 100%;
  height: 100vh;
  overflow: auto;
}

.workflow-grid {
  position: absolute;
  top: 0;
  left: 0;
  width: 1000%;
  height: 1000%;
  background-color: rgb(27, 38, 54);
  background-size: calc(5px * 2) calc(5px * 2), var(--grid-box-x-size) var(--grid-box-y-size);
  background-image:
    linear-gradient(to bottom, transparent 5px, rgb(27, 38, 54) 5px),
    linear-gradient(to right, rgba(205, 81, 36, .5) 1px, transparent 1px),
    linear-gradient(to right, transparent 5px, rgb(27, 38, 54) 5px),
    linear-gradient(to bottom, rgba(205, 81, 36, .5) 1px, transparent 1px);
  background-attachment: local;
}

.loading-overlay {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
</style>
