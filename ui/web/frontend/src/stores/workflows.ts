import { defineStore } from 'pinia'
import { api } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface ServiceInterface {
  id: number
  service_id: number
  interface_type: 'STDIN' | 'STDOUT' | 'STDERR' | 'LOGS' | 'MOUNT'
  path: string
  mount_function?: 'INPUT' | 'OUTPUT' | 'BOTH'  // For MOUNT interfaces
  created_at: string
}

export interface WorkflowJob {
  id: number
  workflow_id: number
  service_id: number
  peer_id: string  // App Peer ID (not DHT node_id)
  service_name: string
  service_type: string
  order: number
  gui_x?: number
  gui_y?: number
  input_mapping?: Record<string, any>
  output_mapping?: Record<string, any>
  pricing_amount?: number
  pricing_type?: string
  pricing_interval?: number
  pricing_unit?: string
  interfaces?: ServiceInterface[]  // Service interfaces defining allowed inputs/outputs
  created_at: string
}

export interface Workflow {
  id: number
  name: string
  description: string
  jobs: WorkflowJob[]
  snap_to_grid: boolean
  status?: 'draft' | 'active' | 'executing' | 'completed' | 'failed'
  created_at: string
  updated_at: string
}

export interface WorkflowUIState {
  snap_to_grid: boolean
  zoom_level?: number
  pan_x?: number
  pan_y?: number
  self_peer_x?: number
  self_peer_y?: number
}

export interface WorkflowsState {
  workflows: Workflow[]
  currentWorkflow: Workflow | null
  currentUIState: WorkflowUIState | null
  loading: boolean
  error: string | null
  pickedService: any | null
  _wsUnsubscribe: (() => void) | null
}

export const useWorkflowsStore = defineStore('workflows', {
  state: (): WorkflowsState => ({
    workflows: [],
    currentWorkflow: null,
    currentUIState: null,
    loading: false,
    error: null,
    pickedService: null,
    _wsUnsubscribe: null
  }),

  getters: {
    totalWorkflows: (state) => state.workflows.length,
    workflowById: (state) => (id: number) =>
      state.workflows.find(w => w.id === id),
    activeWorkflows: (state) =>
      state.workflows.filter(w => w.status === 'active'),
    isEditingWorkflow: (state) => state.currentWorkflow !== null
  },

  actions: {
    async fetchWorkflows() {
      this.loading = true
      this.error = null

      try {
        const response = await api.getWorkflows()
        this.workflows = response.workflows || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async fetchWorkflow(id: number) {
      this.loading = true
      this.error = null

      try {
        const response = await api.getWorkflow(id)
        // Backend returns workflow directly for single workflow
        const workflow = response.workflow || response
        this.currentWorkflow = workflow

        // Fetch nodes for the workflow
        const nodesResponse = await api.getWorkflowNodes(id)
        if (nodesResponse.nodes && this.currentWorkflow) {
          // Interfaces are already included in the workflow definition from remote service search
          // No need to query local database for each service - just use what's in the definition
          this.currentWorkflow.jobs = nodesResponse.nodes.map((node: WorkflowJob) => {
            // Ensure interfaces array exists (fallback to empty array)
            if (!node.interfaces) {
              node.interfaces = []
            }
            return node
          })
        }

        // Fetch UI state
        try {
          const uiState = await api.getWorkflowUIState(id)
          this.currentUIState = uiState
        } catch (uiError) {
          // UI state might not exist yet, create default
          this.currentUIState = {
            snap_to_grid: false,
            zoom_level: 1.0,
            pan_x: 0,
            pan_y: 0,
            self_peer_x: 50,
            self_peer_y: 50
          }
        }
        return this.currentWorkflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async createWorkflow(workflow: { name: string; description: string }) {
      try {
        const response = await api.createWorkflow(workflow)
        // Backend returns workflow directly, not wrapped
        const newWorkflow = response.workflow || response
        // Ensure jobs array is initialized
        if (!newWorkflow.jobs) {
          newWorkflow.jobs = []
        }
        this.workflows.push(newWorkflow)
        this.currentWorkflow = newWorkflow
        return newWorkflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async updateWorkflow(id: number, updates: Partial<Workflow>) {
      try {
        const response = await api.updateWorkflow(id, updates)
        // Backend returns workflow directly
        const updatedWorkflow = response.workflow || response
        const index = this.workflows.findIndex(w => w.id === id)
        if (index !== -1) {
          this.workflows[index] = updatedWorkflow
        }
        if (this.currentWorkflow?.id === id) {
          this.currentWorkflow = updatedWorkflow
        }
        return updatedWorkflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async deleteWorkflow(id: number) {
      try {
        await api.deleteWorkflow(id)
        this.workflows = this.workflows.filter(w => w.id !== id)
        if (this.currentWorkflow?.id === id) {
          this.currentWorkflow = null
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async addWorkflowJob(workflowId: number, job: Partial<WorkflowJob>) {
      try {
        const response = await api.addWorkflowNode(workflowId, job)
        const node = response.node || response

        // Interfaces should already be included in the job from service search
        // If not present, initialize empty array
        if (!node.interfaces) {
          node.interfaces = []
        }

        if (this.currentWorkflow?.id === workflowId) {
          // Ensure jobs array exists
          if (!this.currentWorkflow.jobs) {
            this.currentWorkflow.jobs = []
          }
          this.currentWorkflow.jobs.push(node)
        }
        return node
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async removeWorkflowJob(workflowId: number, jobId: number) {
      try {
        await api.removeWorkflowNode(workflowId, jobId)
        if (this.currentWorkflow?.id === workflowId && this.currentWorkflow.jobs) {
          this.currentWorkflow.jobs = this.currentWorkflow.jobs.filter(j => j.id !== jobId)
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async updateJobGUIProps(workflowId: number, jobId: number, x: number, y: number) {
      try {
        await api.updateNodeGUIProps(workflowId, jobId, { x, y })
        if (this.currentWorkflow?.id === workflowId && this.currentWorkflow.jobs) {
          const job = this.currentWorkflow.jobs.find(j => j.id === jobId)
          if (job) {
            job.gui_x = x
            job.gui_y = y
          }
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async updateUIState(workflowId: number, state: Partial<WorkflowUIState>) {
      try {
        await api.updateWorkflowUIState(workflowId, state)
        if (this.currentWorkflow?.id === workflowId && this.currentUIState) {
          Object.assign(this.currentUIState, state)
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async executeWorkflow(id: number) {
      try {
        const response = await api.executeWorkflow(id)
        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async addWorkflowConnection(workflowId: number, connection: any) {
      try {
        const response = await api.addWorkflowConnection(workflowId, connection)
        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async getWorkflowConnections(workflowId: number) {
      try {
        const response = await api.getWorkflowConnections(workflowId)
        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    async deleteWorkflowConnection(workflowId: number, connectionId: number) {
      try {
        await api.deleteWorkflowConnection(workflowId, connectionId)
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    setCurrentWorkflow(workflow: Workflow | null) {
      this.currentWorkflow = workflow
    },

    setPickedService(service: any | null) {
      this.pickedService = service
    },

    clearCurrentWorkflow() {
      this.currentWorkflow = null
      this.currentUIState = null
      this.pickedService = null
    },

    // Initialize WebSocket subscription
    initializeWebSocket() {
      const wsService = getWebSocketService()
      if (!wsService) {
        return
      }

      // Subscribe to workflows updates
      const self = this as any
      const unsubscribe = wsService.subscribe(
        MessageType.WORKFLOWS_UPDATED,
        (payload: any) => self.handleWorkflowsUpdate(payload)
      )

      // Store unsubscribe function
      self._wsUnsubscribe = unsubscribe
    },

    // Cleanup WebSocket subscription
    cleanupWebSocket() {
      if (this._wsUnsubscribe) {
        this._wsUnsubscribe()
        this._wsUnsubscribe = null
      }
    },

    // Handle WebSocket workflows update
    handleWorkflowsUpdate(payload: any) {
      if (payload && payload.workflows) {
        this.workflows = payload.workflows.map((wf: any) => ({
          id: parseInt(wf.id),
          name: wf.name,
          description: wf.description,
          jobs: wf.jobs || [],
          snap_to_grid: false,
          status: wf.status || 'draft',
          created_at: new Date(wf.created_at * 1000).toISOString(),
          updated_at: new Date(wf.updated_at * 1000).toISOString()
        }))
        this.loading = false
        this.error = null
      }
    }
  }
})
