import { defineStore } from 'pinia'
import { api } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface WorkflowJob {
  id: number
  workflow_id: number
  service_id: number
  node_id: string
  service_name: string
  service_type: string
  order: number
  gui_x?: number
  gui_y?: number
  input_mapping?: Record<string, any>
  output_mapping?: Record<string, any>
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

export interface WorkflowsState {
  workflows: Workflow[]
  currentWorkflow: Workflow | null
  loading: boolean
  error: string | null
  pickedService: any | null
}

export const useWorkflowsStore = defineStore('workflows', {
  state: (): WorkflowsState => ({
    workflows: [],
    currentWorkflow: null,
    loading: false,
    error: null,
    pickedService: null
  }),

  // Track unsubscribe function
  _wsUnsubscribe: null as (() => void) | null,

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
        console.error('Failed to fetch workflows:', error)
      } finally {
        this.loading = false
      }
    },

    async fetchWorkflow(id: number) {
      this.loading = true
      this.error = null

      try {
        const response = await api.getWorkflow(id)
        this.currentWorkflow = response.workflow
        return response.workflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to fetch workflow:', error)
        throw error
      } finally {
        this.loading = false
      }
    },

    async createWorkflow(workflow: { name: string; description: string }) {
      try {
        const response = await api.createWorkflow(workflow)
        this.workflows.push(response.workflow)
        this.currentWorkflow = response.workflow
        return response.workflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to create workflow:', error)
        throw error
      }
    },

    async updateWorkflow(id: number, updates: Partial<Workflow>) {
      try {
        const response = await api.updateWorkflow(id, updates)
        const index = this.workflows.findIndex(w => w.id === id)
        if (index !== -1) {
          this.workflows[index] = response.workflow
        }
        if (this.currentWorkflow?.id === id) {
          this.currentWorkflow = response.workflow
        }
        return response.workflow
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to update workflow:', error)
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
        console.error('Failed to delete workflow:', error)
        throw error
      }
    },

    async addWorkflowJob(workflowId: number, job: Partial<WorkflowJob>) {
      try {
        const response = await api.addWorkflowJob(workflowId, job)
        if (this.currentWorkflow?.id === workflowId) {
          this.currentWorkflow.jobs.push(response.job)
        }
        return response.job
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to add workflow job:', error)
        throw error
      }
    },

    async removeWorkflowJob(workflowId: number, jobId: number) {
      try {
        await api.removeWorkflowJob(workflowId, jobId)
        if (this.currentWorkflow?.id === workflowId) {
          this.currentWorkflow.jobs = this.currentWorkflow.jobs.filter(j => j.id !== jobId)
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to remove workflow job:', error)
        throw error
      }
    },

    async updateJobGUIProps(workflowId: number, jobId: number, x: number, y: number) {
      try {
        await api.updateJobGUIProps(workflowId, jobId, { x, y })
        if (this.currentWorkflow?.id === workflowId) {
          const job = this.currentWorkflow.jobs.find(j => j.id === jobId)
          if (job) {
            job.gui_x = x
            job.gui_y = y
          }
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to update job GUI props:', error)
        throw error
      }
    },

    async executeWorkflow(id: number) {
      try {
        const response = await api.executeWorkflow(id)
        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to execute workflow:', error)
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
      this.pickedService = null
    },

    // Initialize WebSocket subscription
    initializeWebSocket() {
      const wsService = getWebSocketService()
      if (!wsService) {
        console.warn('[WorkflowsStore] WebSocket service not available')
        return
      }

      // Subscribe to workflows updates
      const unsubscribe = wsService.subscribe(
        MessageType.WORKFLOWS_UPDATED,
        this.handleWorkflowsUpdate.bind(this)
      )

      // Store unsubscribe function
      ;(this as any)._wsUnsubscribe = unsubscribe

      console.log('[WorkflowsStore] WebSocket subscription initialized')
    },

    // Cleanup WebSocket subscription
    cleanupWebSocket() {
      const unsubscribe = (this as any)._wsUnsubscribe
      if (unsubscribe) {
        unsubscribe()
        ;(this as any)._wsUnsubscribe = null
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
