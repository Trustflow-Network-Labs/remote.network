<template>
  <AppLayout>
    <div ref="workflowEditor" class="workflow-editor">
      <div
        ref="grid"
        class="workflow-grid"
        :style="gridStyles"
        @dragover="onDragOver"
        @drop="onDrop"
        @click="onGridClick"
      >
        <!-- Self Peer Card (Local Peer) -->
        <SelfPeerCard
          ref="selfPeerCardRef"
          :style="{ left: '50px', top: '50px' }"
        />

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
        :can-execute="canExecuteWorkflow"
        @save="saveWorkflow"
        @execute="executeWorkflow"
        @delete="confirmDeleteWorkflow"
        @snap-to-grid="updateSnapToGrid"
      />

      <!-- Loading Overlay -->
      <div v-if="loading" class="loading-overlay">
        <ProgressSpinner />
      </div>

      <!-- Interface Selector Dialog -->
      <InterfaceSelector
        v-model:visible="interfaceSelectorVisible"
        :from-card-name="pendingConnection.fromCardName"
        :to-card-name="pendingConnection.toCardName"
        :from-interfaces="pendingConnection.fromInterfaces"
        :to-interfaces="pendingConnection.toInterfaces"
        @confirm="onInterfacesSelected"
        @cancel="onInterfaceSelectorCancel"
      />

      <!-- Connection Details Dialog -->
      <ConnectionDetailsDialog
        v-model:visible="connectionDetailsVisible"
        :from-card-name="selectedConnection.fromCardName"
        :to-card-name="selectedConnection.toCardName"
        :interfaces="selectedConnection.interfaces"
        @delete="onDeleteConnection"
        @cancel="onConnectionDetailsCancel"
      />
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
// @ts-ignore
import LeaderLine from 'leader-line-new'

import AppLayout from '../layout/AppLayout.vue'
import ServiceCard from './ServiceCard.vue'
import SelfPeerCard from './SelfPeerCard.vue'
import WorkflowTools from './WorkflowTools.vue'
import InterfaceSelector from './InterfaceSelector.vue'
import ConnectionDetailsDialog from './ConnectionDetailsDialog.vue'
import { useWorkflowsStore } from '../../stores/workflows'
import type { WorkflowJob, ServiceInterface } from '../../stores/workflows'

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
const workflowEditor = ref<HTMLElement | null>(null)
const grid = ref<HTMLElement | null>(null)
const workflowToolsRef = ref<any>(null)
const selfPeerCardRef = ref<any>(null)
const snapToGrid = ref(false)
const serviceCards = ref<Array<{
  id: number
  job: WorkflowJob
  x: number
  y: number
}>>([])

// Draggable instances
const draggableInstances: Map<number | string, any> = new Map()
const cardRefs: Map<number, any> = new Map()

// Interface selector dialog state
const interfaceSelectorVisible = ref(false)
const pendingConnection = ref<{
  fromCardId: string | number
  toCardId: string | number
  fromCardName: string
  toCardName: string
  fromInterfaces: ServiceInterface[]
  toInterfaces: ServiceInterface[]
}>({
  fromCardId: '',
  toCardId: '',
  fromCardName: '',
  toCardName: '',
  fromInterfaces: [],
  toInterfaces: []
})

// Watch for dialog close to reset state (handles X button, click outside, etc.)
watch(interfaceSelectorVisible, (isVisible) => {
  if (!isVisible) {
    // Reset pending connection when dialog closes
    pendingConnection.value = {
      fromCardId: '',
      toCardId: '',
      fromCardName: '',
      toCardName: '',
      fromInterfaces: [],
      toInterfaces: []
    }
  }
})

// Connection details dialog state
const connectionDetailsVisible = ref(false)
const selectedConnection = ref<{
  connectionId: string
  fromCardId: string | number
  toCardId: string | number
  fromCardName: string
  toCardName: string
  interfaces: Array<{
    id: number
    from_interface_type: string
    to_interface_type: string
  }>
}>({
  connectionId: '',
  fromCardId: '',
  toCardId: '',
  fromCardName: '',
  toCardName: '',
  interfaces: []
})

// Leader-line connections (must be reactive for computed properties to update)
const connections = ref<Array<{
  line: any
  from: string
  to: string
  fromCardId: string | number
  toCardId: string | number
  fromCardName: string
  toCardName: string
  dbConnections: Array<{
    id: number
    from_interface_type: string
    to_interface_type: string
  }> // All database connections for this visual line
  svgElement?: SVGElement // Store reference to SVG element for click handling
  interfaces?: string[] // Store which interfaces are connected (legacy, to be removed)
}>>([])
const selectedConnector = ref<{ cardId: string | number; type: 'input' | 'output'; element: HTMLElement } | null>(null)
const previewLine = ref<any>(null)
let justCompletedConnection = false

// Grid styling
const gridStyles = computed(() => ({
  '--grid-box-x-size': `${gridBoxXSize}px`,
  '--grid-box-y-size': `${gridBoxYSize}px`,
}))

// Validate that all interfaces are connected
const canExecuteWorkflow = computed(() => {
  // No jobs means cannot execute
  if (serviceCards.value.length === 0) {
    return false
  }

  // Check all output interfaces are connected (except local peer)
  for (const card of serviceCards.value) {
    if (!card.job.interfaces || card.job.interfaces.length === 0) {
      continue
    }

    const hasOutputInterface = card.job.interfaces.some(
      iface => iface.interface_type === 'STDOUT' ||
               (iface.interface_type === 'MOUNT' && card.job.service_type === 'DATA')
    )

    if (hasOutputInterface) {
      // This card has output, check if it's connected (use loose equality for string/number comparison)
      const hasConnection = connections.value.some(conn => conn.fromCardId == card.id)
      if (!hasConnection) {
        return false // Output not connected
      }
    }
  }

  // Check all input interfaces are connected (except local peer)
  for (const card of serviceCards.value) {
    if (!card.job.interfaces || card.job.interfaces.length === 0) {
      continue
    }

    const hasInputInterface = card.job.interfaces.some(
      iface => iface.interface_type === 'STDIN' ||
               (iface.interface_type === 'MOUNT' && card.job.service_type !== 'DATA')
    )

    if (hasInputInterface) {
      // This card has input, check if it's connected (use loose equality for string/number comparison)
      const hasConnection = connections.value.some(conn => conn.toCardId == card.id)
      if (!hasConnection) {
        return false // Input not connected
      }
    }
  }

  // Local peer input must be connected (at least one connection to self-peer)
  const hasSelfPeerConnection = connections.value.some(conn => conn.toCardId == 'self-peer')
  if (!hasSelfPeerConnection) {
    return false
  }

  return true
})

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

function onGridClick(event: MouseEvent) {
  // If clicking on the grid itself (not a connector), cancel the preview
  const target = event.target as HTMLElement
  if (selectedConnector.value && !target.classList.contains('card-rip-connector')) {
    clearSelection()
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
      interfaces: service.interfaces || [], // Include service interfaces from remote service search
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

    // Setup connector handlers for the new card
    await nextTick()
    setupConnectorHandlers()

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
    return
  }

  const cardEl = cardRefs.get(cardId)
  if (!cardEl) {
    return
  }

  // Get the actual DOM element - in Vue 3, component refs might be the component instance or the element
  let domElement = cardEl
  if (cardEl.$el) {
    domElement = cardEl.$el
  }

  if (!domElement || !(domElement instanceof HTMLElement)) {
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

  // Create new draggable instance (cast to any to avoid TypeScript errors with onMove)
  const draggable: any = new PlainDraggable(domElement, {
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

  // Update connections while dragging
  draggable.onMove = () => {
    updateConnectionsForCard(cardId)
  }

  draggable.onDragEnd = async (pos: { left: number; top: number }) => {
    // Round to integers since backend expects int values
    const x = Math.round(pos.left)
    const y = Math.round(pos.top)

    card.x = x
    card.y = y

    // Update job GUI props in database
    if (workflowsStore.currentWorkflow) {
      try {
        await workflowsStore.updateJobGUIProps(
          workflowsStore.currentWorkflow.id,
          card.job.id,
          x,
          y
        )
      } catch (error) {
        // Silently fail - position update is not critical
      }
    }
  }

  draggableInstances.set(cardId, draggable)
}

async function removeServiceCard(cardId: number) {
  const card = serviceCards.value.find(c => c.id === cardId)
  if (!card) return

  // Remove all connections associated with this card
  const connectionsToRemove = connections.value.filter(c =>
    c.from.includes(cardId.toString()) || c.to.includes(cardId.toString())
  )

  // Delete connections from database
  if (workflowsStore.currentWorkflow?.id) {
    for (const conn of connectionsToRemove) {
      // Delete all database connections for this visual line
      for (const dbConn of conn.dbConnections) {
        try {
          await workflowsStore.deleteWorkflowConnection(workflowsStore.currentWorkflow.id, dbConn.id)
        } catch (error) {
          // Silently fail - connection will be cascade deleted
        }
      }
    }
  }

  // Remove leader lines from UI
  connectionsToRemove.forEach(conn => {
    if (conn.line && conn.line.remove) {
      conn.line.remove()
    }
  })

  // Remove from connections array
  connections.value.splice(0, connections.value.length,
    ...connections.value.filter(c => !connectionsToRemove.includes(c))
  )

  // Clean up draggable instance
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

    // Clean up leader lines first (DB connections will be cascade deleted)
    connections.value.forEach(c => {
      if (c.line && c.line.remove) {
        c.line.remove()
      }
    })
    connections.value.length = 0

    // Delete workflow from database (cascade deletes nodes and connections)
    await workflowsStore.deleteWorkflow(workflowId)

    // Clean up UI state
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
  if (workflowsStore.currentWorkflow?.id && workflowsStore.currentUIState) {
    // Merge with existing state to avoid overwriting other fields
    workflowsStore.updateUIState(workflowsStore.currentWorkflow.id, {
      ...workflowsStore.currentUIState,
      snap_to_grid: enabled
    })
  }
}

async function loadWorkflow() {
  const workflowId = route.params.id

  // Clean up existing UI connections and cards before loading (without deleting from database)
  clearConnectionsUI()
  serviceCards.value = []

  // Handle new workflow or invalid ID
  if (!workflowId || workflowId === 'design' || workflowId === 'new' || Array.isArray(workflowId)) {
    workflowsStore.clearCurrentWorkflow()
    // Still initialize self-peer for new workflows
    await initializeSelfPeerDraggable()
    await nextTick()
    setupConnectorHandlers()
    return
  }

  // Validate numeric ID
  const numericId = Number(workflowId)
  if (isNaN(numericId) || numericId <= 0) {
    workflowsStore.clearCurrentWorkflow()
    // Still initialize self-peer for invalid IDs
    await initializeSelfPeerDraggable()
    await nextTick()
    setupConnectorHandlers()
    return
  }

  loading.value = true

  try {
    const workflow = await workflowsStore.fetchWorkflow(numericId)

    // Load service cards from workflow jobs (nodes)
    if (workflow.jobs && workflow.jobs.length > 0) {
      serviceCards.value = workflow.jobs.map((job: WorkflowJob, index: number) => ({
        id: job.id, // Use database ID, not array index
        job: job,
        x: job.gui_x || 100 + (index * 50),
        y: job.gui_y || 100 + (index * 50)
      }))

      // Wait for cards to render
      await nextTick()
      await nextTick() // Extra tick to ensure refs are populated

      // Initialize draggables
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

    // Initialize self-peer draggable after UI state is loaded
    await initializeSelfPeerDraggable()

    // Set up connector handlers for all cards including self-peer
    await nextTick()
    await nextTick() // Extra tick to ensure connectors are in DOM
    setupConnectorHandlers()

    // Load connections after all cards and connectors are fully initialized
    if (workflow.jobs && workflow.jobs.length > 0) {
      await nextTick()
      await nextTick() // Extra tick to ensure everything is ready
      await loadConnections()
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

// Leader-line connection management
function handleConnectorClick(event: Event, cardId: string | number, connectorType: 'input' | 'output') {
  event.stopPropagation()

  // Prevent connector clicks while interface selector dialog is open
  if (interfaceSelectorVisible.value) {
    return
  }

  // Prevent starting a new connection immediately after completing one
  if (justCompletedConnection) {
    justCompletedConnection = false
    return
  }

  const element = event.target as HTMLElement

  if (!selectedConnector.value) {
    // First click - select this connector and create preview line
    selectedConnector.value = { cardId, type: connectorType, element }
    element.classList.add('selected')

    // Create preview line from this connector to a temporary anchor point
    createPreviewLine(element)

    // Add mouse move listener to update preview line
    grid.value?.addEventListener('mousemove', updatePreviewLine)
  } else {
    // Second click - create connection
    const { cardId: fromCardId, type: fromType } = selectedConnector.value

    // Validate connection: output -> input
    if (fromType === 'output' && connectorType === 'input') {
      createConnection(fromCardId, cardId)
    } else if (fromType === 'input' && connectorType === 'output') {
      createConnection(cardId, fromCardId)
    } else {
      toast.add({
        severity: 'warn',
        summary: t('message.common.warning'),
        detail: 'Connections must go from output to input',
        life: 3000
      })
    }

    // Set flag to prevent immediate re-selection
    justCompletedConnection = true

    // Clear selection and preview
    clearSelection()

    // Reset flag after a short delay
    setTimeout(() => {
      justCompletedConnection = false
    }, 100)
  }
}

function createPreviewLine(startElement: HTMLElement) {
  // Create a temporary div to act as the end point that will follow the mouse
  const tempEnd = document.createElement('div')
  tempEnd.id = 'preview-line-end'
  tempEnd.style.position = 'absolute'
  tempEnd.style.width = '1px'
  tempEnd.style.height = '1px'
  tempEnd.style.left = '0px'
  tempEnd.style.top = '0px'
  tempEnd.style.pointerEvents = 'none'
  grid.value?.appendChild(tempEnd)

  previewLine.value = new LeaderLine(
    startElement,
    tempEnd,
    {
      color: 'rgba(205, 81, 36, 0.5)',
      size: 2,
      path: 'fluid',
      startPlug: 'disc',
      endPlug: 'arrow2',
      startPlugColor: 'rgba(205, 81, 36, 0.5)',
      endPlugColor: 'rgba(205, 81, 36, 0.5)',
      dash: { animation: true }
    }
  )
}

function updatePreviewLine(event: MouseEvent) {
  if (!previewLine.value || !grid.value) return

  const tempEnd = document.getElementById('preview-line-end')
  if (tempEnd) {
    // Get grid's position and scroll offset
    const gridRect = grid.value.getBoundingClientRect()
    const scrollLeft = grid.value.scrollLeft
    const scrollTop = grid.value.scrollTop

    // Calculate position relative to grid
    const x = event.clientX - gridRect.left + scrollLeft
    const y = event.clientY - gridRect.top + scrollTop

    tempEnd.style.left = `${x}px`
    tempEnd.style.top = `${y}px`
    previewLine.value.position()
  }
}

function clearSelection() {
  // Remove preview line
  if (previewLine.value) {
    previewLine.value.remove()
    previewLine.value = null
  }

  // Remove temp end element
  const tempEnd = document.getElementById('preview-line-end')
  if (tempEnd) {
    tempEnd.remove()
  }

  // Remove mouse move listener
  grid.value?.removeEventListener('mousemove', updatePreviewLine)

  // Clear selection styling
  document.querySelectorAll('.card-rip-connector.selected').forEach(el => {
    el.classList.remove('selected')
  })

  selectedConnector.value = null
}

function attachConnectionClickHandler(connectionId: string, line: any): Promise<SVGElement | null> {
  return new Promise((resolve) => {
    if (!line) {
      resolve(null)
      return
    }

    // Wait for SVG to be rendered
    setTimeout(() => {
      // Find all leader-line SVG elements
      const allLines = document.querySelectorAll('body > svg.leader-line')
      const targetSvg = allLines[allLines.length - 1] as SVGElement

      if (targetSvg) {
        // Store the connection ID
        targetSvg.setAttribute('data-connection-id', connectionId)

        // Enable pointer events on the SVG
        targetSvg.style.pointerEvents = 'auto'
        targetSvg.style.cursor = 'pointer'

        // Make ALL child elements clickable
        const allElements = targetSvg.querySelectorAll('*')
        allElements.forEach(el => {
          ;(el as SVGElement).style.pointerEvents = 'auto'
          ;(el as SVGElement).style.cursor = 'pointer'
        })

        resolve(targetSvg)
      } else {
        resolve(null)
      }
    }, 150)
  })
}

async function createConnection(fromCardId: string | number, toCardId: string | number) {
  // Find connector elements
  const fromElement = document.querySelector(`[data-card-id="${fromCardId}"][data-connector="output"]`)
  const toElement = document.querySelector(`[data-card-id="${toCardId}"][data-connector="input"]`)

  if (!fromElement || !toElement) {
    return
  }

  // Check if connection already exists
  const connectionId = `${fromCardId}->${toCardId}`
  if (connections.value.find(c => c.from === connectionId)) {
    toast.add({
      severity: 'warn',
      summary: t('message.common.warning'),
      detail: 'Connection already exists',
      life: 3000
    })
    return
  }

  // Get card data and interfaces
  const fromCard = fromCardId === 'self-peer' ?
    { job: { service_name: 'Local Peer', interfaces: [] } } :
    serviceCards.value.find(c => c.id == fromCardId)

  const toCard = toCardId === 'self-peer' ?
    { job: { service_name: 'Local Peer', interfaces: [{ interface_type: 'STDIN', path: 'input/' }] as ServiceInterface[] } } :
    serviceCards.value.find(c => c.id == toCardId)

  if (!fromCard || !toCard) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: 'Card not found',
      life: 3000
    })
    return
  }

  // Set pending connection data
  pendingConnection.value = {
    fromCardId,
    toCardId,
    fromCardName: fromCard.job.service_name || 'Unknown',
    toCardName: toCard.job.service_name || 'Unknown',
    fromInterfaces: fromCard.job.interfaces || [],
    toInterfaces: toCard.job.interfaces || []
  }

  // Show interface selector dialog
  interfaceSelectorVisible.value = true
}

async function onInterfacesSelected(data: { sourceOutputs: string[], destinationInputs: string[] }) {
  const { fromCardId, toCardId, fromCardName, toCardName } = pendingConnection.value

  // Save to database - check if workflow exists first
  if (!workflowsStore.currentWorkflow?.id) {
    toast.add({
      severity: 'warn',
      summary: t('message.common.warning'),
      detail: 'Please save the workflow before creating connections',
      life: 5000
    })
    return
  }

  try {
    // Get actual database node IDs from cards
    let fromNodeId: number | null = null
    if (fromCardId !== 'self-peer') {
      const fromCard = serviceCards.value.find(c => c.id == fromCardId)
      fromNodeId = fromCard?.job.id || null
    }

    let toNodeId: number | null = null
    if (toCardId !== 'self-peer') {
      const toCard = serviceCards.value.find(c => c.id == toCardId)
      toNodeId = toCard?.job.id || null
    }

    // Create connections for each combination of source output and destination input
    const createdConnections: Array<{
      id: number
      from_interface_type: string
      to_interface_type: string
    }> = []

    // Create connection for each source output to each destination input
    for (const sourceOutput of data.sourceOutputs) {
      for (const destInput of data.destinationInputs) {
        const connection = {
          from_node_id: fromNodeId,
          from_interface_type: sourceOutput,  // STDOUT, STDERR, LOGS, or MOUNT
          to_node_id: toNodeId,
          to_interface_type: destInput  // STDIN or MOUNT
        }

        const result = await workflowsStore.addWorkflowConnection(
          workflowsStore.currentWorkflow.id,
          connection
        )

        if (result && result.connection) {
          createdConnections.push({
            id: result.connection.id,
            from_interface_type: sourceOutput,
            to_interface_type: destInput
          })
        }
      }
    }

    // Create visual leader line (one line for all interfaces)
    const fromElement = document.querySelector(`[data-card-id="${fromCardId}"][data-connector="output"]`)
    const toElement = document.querySelector(`[data-card-id="${toCardId}"][data-connector="input"]`)

    if (!fromElement || !toElement) {
      toast.add({
        severity: 'error',
        summary: t('message.common.error'),
        detail: 'Failed to find connector elements',
        life: 3000
      })
      return
    }

    const line = new LeaderLine(
      fromElement,
      toElement,
      {
        color: 'rgb(205, 81, 36)',
        size: 3,
        path: 'fluid',
        startPlug: 'disc',
        endPlug: 'arrow2',
        startPlugColor: 'rgb(205, 81, 36)',
        endPlugColor: 'rgb(205, 81, 36)',
        dash: false
      }
    )

    line.show('draw', {duration: 500, timing: [0.58, 0, 0.42, 1]})

    const connectionId = `${fromCardId}->${toCardId}`
    ;(line as any)._connectionId = connectionId

    // Add click handler and store
    const svgElement = await attachConnectionClickHandler(connectionId, line)

    connections.value.push({
      line,
      from: connectionId,
      to: `${toCardId}`,
      fromCardId,
      toCardId,
      fromCardName,
      toCardName,
      dbConnections: createdConnections,
      svgElement: svgElement || undefined,
      interfaces: data.sourceOutputs  // Store which source interfaces are connected
    })

    const totalConnections = createdConnections.length
    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: `Connection created (${totalConnections} interface${totalConnections > 1 ? 's' : ''})`,
      life: 3000
    })

    // Close dialog (watcher will reset state automatically)
    interfaceSelectorVisible.value = false

  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || 'Failed to create connection',
      life: 5000
    })

    // Close dialog (watcher will reset state automatically)
    interfaceSelectorVisible.value = false
  }
}

function onInterfaceSelectorCancel() {
  // Just close dialog, watcher will reset state automatically
  interfaceSelectorVisible.value = false
}

async function onDeleteConnection() {
  if (!selectedConnection.value.connectionId) return

  const connectionId = selectedConnection.value.connectionId
  const index = connections.value.findIndex(c => c.from === connectionId)
  if (index === -1) return

  const connection = connections.value[index]

  // Delete all database connections for this visual line
  if (workflowsStore.currentWorkflow?.id) {
    try {
      for (const dbConn of connection.dbConnections) {
        await workflowsStore.deleteWorkflowConnection(workflowsStore.currentWorkflow.id, dbConn.id)
      }
    } catch (error) {
      toast.add({
        severity: 'error',
        summary: t('message.common.error'),
        detail: 'Failed to delete connection',
        life: 3000
      })
      connectionDetailsVisible.value = false
      return
    }
  }

  // Remove leader line from UI
  if (connection.line && connection.line.remove) {
    connection.line.remove()
  }

  // Remove from connections array
  connections.value.splice(index, 1)

  connectionDetailsVisible.value = false

  toast.add({
    severity: 'success',
    summary: t('message.common.success'),
    detail: 'Connection removed',
    life: 3000
  })
}

function onConnectionDetailsCancel() {
  connectionDetailsVisible.value = false
  selectedConnection.value = {
    connectionId: '',
    fromCardId: '',
    toCardId: '',
    fromCardName: '',
    toCardName: '',
    interfaces: []
  }
}

async function loadConnections() {
  if (!workflowsStore.currentWorkflow?.id) return

  try {
    const response = await workflowsStore.getWorkflowConnections(workflowsStore.currentWorkflow.id)
    const dbConnections = response.connections || []

    // Group database connections by node pairs (from_node_id -> to_node_id)
    const connectionGroups = new Map<string, Array<any>>()

    for (const dbConn of dbConnections) {
      // Map database node IDs to card IDs
      let fromCardId: string | number = 'self-peer'
      let toCardId: string | number = 'self-peer'
      let fromCardName = 'Self Peer'
      let toCardName = 'Self Peer'

      // Find the card with matching job.id for from_node_id
      if (dbConn.from_node_id !== null) {
        const fromCard = serviceCards.value.find(card => card.job.id === dbConn.from_node_id)
        if (fromCard) {
          fromCardId = fromCard.id
          fromCardName = fromCard.job.service_name || 'Unknown'
        } else {
          continue
        }
      }

      // Find the card with matching job.id for to_node_id
      if (dbConn.to_node_id !== null) {
        const toCard = serviceCards.value.find(card => card.job.id === dbConn.to_node_id)
        if (toCard) {
          toCardId = toCard.id
          toCardName = toCard.job.service_name || 'Unknown'
        } else {
          continue
        }
      }

      // Group by connection ID (node pair)
      const connectionId = `${fromCardId}->${toCardId}`
      if (!connectionGroups.has(connectionId)) {
        connectionGroups.set(connectionId, [])
      }
      connectionGroups.get(connectionId)!.push({
        ...dbConn,
        fromCardId,
        toCardId,
        fromCardName,
        toCardName
      })
    }

    // Create one visual line per node pair
    for (const [connectionId, dbConnGroup] of connectionGroups) {
      const firstConn = dbConnGroup[0]
      const fromCardId = firstConn.fromCardId
      const toCardId = firstConn.toCardId
      const fromCardName = firstConn.fromCardName
      const toCardName = firstConn.toCardName

      // Find connector DOM elements
      const fromElement = document.querySelector(`[data-card-id="${fromCardId}"][data-connector="output"]`)
      const toElement = document.querySelector(`[data-card-id="${toCardId}"][data-connector="input"]`)

      if (!fromElement || !toElement) {
        continue
      }

      // Create the leader line
      const line = new LeaderLine(
        fromElement,
        toElement,
        {
          color: 'rgb(205, 81, 36)',
          size: 3,
          path: 'fluid',
          startPlug: 'disc',
          endPlug: 'arrow2',
          startPlugColor: 'rgb(205, 81, 36)',
          endPlugColor: 'rgb(205, 81, 36)'
        }
      )

      // Add click handler to delete connection and store SVG reference
      await nextTick()
      const svgElement = await attachConnectionClickHandler(connectionId, line)

      // Store all database connections for this visual line
      connections.value.push({
        line,
        from: connectionId,
        to: `${toCardId}`,
        fromCardId,
        toCardId,
        fromCardName,
        toCardName,
        dbConnections: dbConnGroup.map(conn => ({
          id: conn.id,
          from_interface_type: conn.from_interface_type,
          to_interface_type: conn.to_interface_type
        })),
        svgElement: svgElement || undefined
      })
    }
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || 'Failed to load connections',
      life: 5000
    })
  }
}

function updateConnectionsForCard(cardId: string | number) {
  // Update all leader-lines that are connected to this card
  // Use loose equality (==) to handle string vs number comparison
  connections.value.forEach(conn => {
    if (conn.fromCardId == cardId || conn.toCardId == cardId) {
      if (conn.line && conn.line.position) {
        conn.line.position()
      }
    }
  })
}

// Clear connection UI elements without deleting from database
function clearConnectionsUI() {
  // Remove all leader lines from UI
  connections.value.forEach(c => {
    if (c.line && c.line.remove) {
      c.line.remove()
    }
  })

  // Clear connections array
  connections.value.length = 0
}

function setupConnectorHandlers() {
  // Remove existing handlers by cloning and replacing (cleanest way to remove all listeners)
  document.querySelectorAll('.card-rip-connector.allowed').forEach(connector => {
    const clone = connector.cloneNode(true)
    connector.parentNode?.replaceChild(clone, connector)
  })

  // Set up fresh click handlers for all connectors
  document.querySelectorAll('.card-rip-connector.allowed').forEach(connector => {
    const cardId = connector.getAttribute('data-card-id')
    const connectorType = connector.getAttribute('data-connector') as 'input' | 'output'

    if (cardId && connectorType) {
      connector.addEventListener('click', (e) => handleConnectorClick(e, cardId, connectorType))
    }
  })
}

async function initializeSelfPeerDraggable() {
  await nextTick()
  if (selfPeerCardRef.value) {
    const domElement = selfPeerCardRef.value.$el || selfPeerCardRef.value
    if (domElement && domElement instanceof HTMLElement) {
      // Remove existing instance if present
      const existingInstance = draggableInstances.get('self-peer')
      if (existingInstance && existingInstance.remove) {
        existingInstance.remove()
      }

      // Cast to any to avoid TypeScript errors with onMove
      const draggable: any = new PlainDraggable(domElement, {
        autoScroll: true
      })

      // Load saved position from database or use default
      const selfPeerX = workflowsStore.currentUIState?.self_peer_x || 50
      const selfPeerY = workflowsStore.currentUIState?.self_peer_y || 50
      draggable.left = selfPeerX
      draggable.top = selfPeerY

      // Update connections while dragging
      draggable.onMove = () => {
        updateConnectionsForCard('self-peer')
      }

      // Save position to database when dragging ends
      draggable.onDragEnd = (pos: { left: number; top: number }) => {
        if (workflowsStore.currentWorkflow?.id && workflowsStore.currentUIState) {
          // Round to integers since backend expects int values
          const x = Math.round(pos.left)
          const y = Math.round(pos.top)

          // Merge with existing state to avoid overwriting other fields
          workflowsStore.updateUIState(workflowsStore.currentWorkflow.id, {
            ...workflowsStore.currentUIState,
            self_peer_x: x,
            self_peer_y: y
          })
        }
      }

      draggableInstances.set('self-peer', draggable)
    }
  }
}

onMounted(async () => {
  initializeSnapTargets()

  // Add global click handler for leader-line connections
  document.addEventListener('click', (e: MouseEvent) => {
    const target = e.target as Element
    // Check if click is on a leader-line SVG or its child
    let svgElement = target.closest('svg.leader-line') as SVGElement

    if (svgElement) {
      const connectionId = svgElement.getAttribute('data-connection-id')
      if (connectionId) {
        e.stopPropagation()

        // Find the connection in our connections array
        const connection = connections.value.find(c => c.from === connectionId)
        if (connection) {
          // Show connection details dialog
          selectedConnection.value = {
            connectionId,
            fromCardId: connection.fromCardId,
            toCardId: connection.toCardId,
            fromCardName: connection.fromCardName,
            toCardName: connection.toCardName,
            interfaces: connection.dbConnections
          }
          connectionDetailsVisible.value = true
        }
      }
    }
  })

  // Add global hover handler for visual feedback
  document.addEventListener('mouseover', (e: MouseEvent) => {
    const target = e.target as Element
    const svgElement = target.closest('svg.leader-line') as SVGElement
    if (svgElement && svgElement.hasAttribute('data-connection-id')) {
      svgElement.style.opacity = '0.7'
    }
  })

  document.addEventListener('mouseout', (e: MouseEvent) => {
    const target = e.target as Element
    const svgElement = target.closest('svg.leader-line') as SVGElement
    if (svgElement && svgElement.hasAttribute('data-connection-id')) {
      svgElement.style.opacity = '1'
    }
  })

  // Add scroll handler for workflow-editor to update connection line positions
  if (workflowEditor.value) {
    workflowEditor.value.addEventListener('scroll', () => {
      // Update all connection line positions when scrolling
      connections.value.forEach(conn => {
        if (conn.line && conn.line.position) {
          conn.line.position()
        }
      })
    })
  }

  // Load workflow (this will also initialize self-peer and set up connectors)
  await loadWorkflow()
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

  // Clean up all leader-line UI connections (without deleting from database)
  clearConnectionsUI()
})

// Watch for route changes to reload workflow
watch(() => route.params.id, async () => {
  await loadWorkflow()
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

// Connector selected state
:deep(.card-rip-connector.selected) {
  background-color: rgba(255, 215, 0, 0.5) !important;
  border-width: 6px !important;
  box-shadow: 0 0 10px rgba(255, 215, 0, 0.8);
  cursor: pointer;
}

:deep(.card-rip-connector.allowed) {
  cursor: pointer;
  transition: all 0.2s ease;

  &:hover {
    transform: scale(1.2);
    box-shadow: 0 0 8px rgba(205, 81, 36, 0.6);
  }
}
</style>
