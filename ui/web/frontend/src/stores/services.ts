import { defineStore } from 'pinia'
import { api } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export type ServiceType = 'storage' | 'docker' | 'standalone' | 'relay'
export type ServiceStatus = 'available' | 'busy' | 'offline' | 'ACTIVE' | 'INACTIVE'
export type PricingType = 'ONE_TIME' | 'RECURRING'
export type PricingUnit = 'SECONDS' | 'MINUTES' | 'HOURS' | 'DAYS' | 'WEEKS' | 'MONTHS' | 'YEARS'

export interface PricingModel {
  amount: number
  type: PricingType
  interval?: number
  unit?: PricingUnit
}

export interface Service {
  id?: number
  service_type?: string  // 'DATA', 'DOCKER', 'STANDALONE'
  type: ServiceType
  endpoint: string
  capabilities: Record<string, any>
  status: ServiceStatus
  name?: string
  description?: string
  pricing?: number  // Legacy field
  pricing_amount?: number
  pricing_type?: PricingType
  pricing_interval?: number
  pricing_unit?: PricingUnit
  created_at?: string
  updated_at?: string
}

export interface RemoteService extends Service {
  peer_id: string
  peer_name?: string
}

export interface ServicesState {
  services: Service[]
  remoteServices: RemoteService[]
  loading: boolean
  remoteLoading: boolean
  error: string | null
  _wsUnsubscribe: (() => void) | null
}

export const useServicesStore = defineStore('services', {
  state: (): ServicesState => ({
    services: [],
    remoteServices: [],
    loading: false,
    remoteLoading: false,
    error: null,
    // Track unsubscribe function
    _wsUnsubscribe: null as (() => void) | null
  }),

  getters: {
    totalServices: (state) => state.services.length,
    servicesByType: (state) => (type: ServiceType) =>
      state.services.filter(s => s.type === type),
    availableServices: (state) =>
      state.services.filter(s => s.status === 'available'),
    serviceById: (state) => (id: number) =>
      state.services.find(s => s.id === id)
  },

  actions: {
    async fetchServices() {
      this.loading = true
      this.error = null

      try {
        const response = await api.getServices()
        this.services = response.services || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to fetch services:', error)
      } finally {
        this.loading = false
      }
    },

    async addService(service: Omit<Service, 'id' | 'created_at' | 'updated_at'>) {
      try {
        const response = await api.addService(service)
        this.services.push(response.service)
        return response.service
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to add service:', error)
        throw error
      }
    },

    async updateService(id: number, service: Partial<Service>) {
      try {
        const response = await api.updateService(id, service)
        const index = this.services.findIndex(s => s.id === id)
        if (index !== -1) {
          this.services[index] = response.service
        }
        return response.service
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to update service:', error)
        throw error
      }
    },

    async deleteService(id: number) {
      try {
        await api.deleteService(id)
        this.services = this.services.filter(s => s.id !== id)
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to delete service:', error)
        throw error
      }
    },

    updateServiceStatus(id: number, status: ServiceStatus) {
      const service = this.services.find(s => s.id === id)
      if (service) {
        service.status = status
      }
    },

    async changeServiceStatus(id: number, status: 'ACTIVE' | 'INACTIVE') {
      try {
        await api.updateServiceStatus(id, status)
        const index = this.services.findIndex(s => s.id === id)
        if (index !== -1) {
          this.services[index].status = status
        }
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to update service status:', error)
        throw error
      }
    },

    async getServicePassphrase(id: number): Promise<string> {
      try {
        const response = await api.getServicePassphrase(id)
        return response.passphrase
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        console.error('Failed to get service passphrase:', error)
        throw error
      }
    },

    async searchRemoteServices(query: string, serviceTypes: string[] = [], peerIds: string[] = []) {
      this.remoteLoading = true
      this.error = null
      this.remoteServices = [] // Clear previous results

      try {
        const wsService = getWebSocketService()
        if (!wsService) {
          throw new Error('WebSocket service not available')
        }

        // Subscribe to streaming search responses
        const responsePromise = new Promise<RemoteService[]>((resolve, reject) => {
          const timeout = setTimeout(() => {
            unsubscribe()
            reject(new Error('Service search timeout'))
          }, 60000) // 60 second timeout (increased for streaming)

          let accumulatedServices: RemoteService[] = []

          const unsubscribe = wsService.subscribe(
            MessageType.SERVICE_SEARCH_RESPONSE,
            (payload: any) => {
              // Check for errors
              if (payload.error) {
                clearTimeout(timeout)
                unsubscribe()
                reject(new Error(payload.error))
                return
              }

              // Accumulate incoming services
              if (payload.services && payload.services.length > 0) {
                accumulatedServices = [...accumulatedServices, ...payload.services]
                // Update UI progressively with partial results
                this.remoteServices = accumulatedServices
              }

              // Check if this is the final message
              if (payload.complete) {
                clearTimeout(timeout)
                unsubscribe()
                resolve(accumulatedServices)
              }
            }
          )
        })

        // Convert service types array to comma-separated string
        const serviceTypeStr = serviceTypes.length > 0 ? serviceTypes.join(',') : ''

        // Send search request
        wsService.send({
          type: MessageType.SERVICE_SEARCH_REQUEST,
          payload: {
            query: query,
            service_type: serviceTypeStr,
            peer_ids: peerIds,
            active_only: true
          }
        })

        // Wait for response
        const services = await responsePromise
        this.remoteServices = services
      } catch (error: any) {
        this.error = error.message || 'Failed to search remote services'
        console.error('Failed to search remote services:', error)
        this.remoteServices = []
      } finally {
        this.remoteLoading = false
      }
    },

    // Initialize WebSocket subscription
    initializeWebSocket() {
      const wsService = getWebSocketService()
      if (!wsService) {
        console.warn('[ServicesStore] WebSocket service not available')
        return
      }

      // Subscribe to services updates
      const unsubscribe = wsService.subscribe(
        MessageType.SERVICES_UPDATED,
        this.handleServicesUpdate.bind(this)
      )

      // Store unsubscribe function
      ;(this as any)._wsUnsubscribe = unsubscribe

      console.log('[ServicesStore] WebSocket subscription initialized')
    },

    // Cleanup WebSocket subscription
    cleanupWebSocket() {
      const unsubscribe = (this as any)._wsUnsubscribe
      if (unsubscribe) {
        unsubscribe()
        ;(this as any)._wsUnsubscribe = null
      }
    },

    // Handle WebSocket services update
    handleServicesUpdate(payload: any) {
      if (payload && payload.services) {
        this.services = payload.services.map((svc: any) => ({
          id: parseInt(svc.id),
          type: svc.type,
          endpoint: svc.endpoint || '',
          capabilities: svc.config || {},
          status: svc.status,
          name: svc.name,
          description: svc.description,
          pricing: svc.pricing || 0,
          created_at: new Date(svc.created_at * 1000).toISOString(),
          updated_at: new Date(svc.updated_at * 1000).toISOString()
        }))
        this.loading = false
        this.error = null
      }
    }
  }
})
