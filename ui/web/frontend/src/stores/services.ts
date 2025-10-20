import { defineStore } from 'pinia'
import { api } from '../services/api'

export type ServiceType = 'storage' | 'docker' | 'standalone' | 'relay'
export type ServiceStatus = 'available' | 'busy' | 'offline'

export interface Service {
  id?: number
  type: ServiceType
  endpoint: string
  capabilities: Record<string, any>
  status: ServiceStatus
  name?: string
  description?: string
  pricing?: number
  created_at?: string
  updated_at?: string
}

export interface ServicesState {
  services: Service[]
  loading: boolean
  error: string | null
}

export const useServicesStore = defineStore('services', {
  state: (): ServicesState => ({
    services: [],
    loading: false,
    error: null
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
    }
  }
})
