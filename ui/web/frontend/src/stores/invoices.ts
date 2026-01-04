import { defineStore } from 'pinia'
import { api, type Invoice } from '../services/api'
import { getWebSocketService, MessageType } from '../services/websocket'

export interface InvoicesState {
  invoices: Invoice[]
  loading: boolean
  error: string | null
}

// Track unsubscribe functions outside the store
let _wsUnsubscribe: (() => void) | null = null

export const useInvoicesStore = defineStore('invoices', {
  state: (): InvoicesState => ({
    invoices: [],
    loading: false,
    error: null
  }),

  getters: {
    totalInvoices: (state) => state.invoices.length,
    pendingInvoices: (state) => state.invoices.filter(i => i.status === 'pending'),
    settledInvoices: (state) => state.invoices.filter(i => i.status === 'settled'),
    expiredInvoices: (state) => state.invoices.filter(i => i.status === 'expired'),
    sentInvoices: (state) => (localPeerID: string) =>
      state.invoices.filter(i => i.from_peer_id === localPeerID),
    receivedInvoices: (state) => (localPeerID: string) =>
      state.invoices.filter(i => i.to_peer_id === localPeerID),
    getInvoice: (state) => (invoiceId: string) =>
      state.invoices.find(i => i.invoice_id === invoiceId),
    invoicesByStatus: (state) => (status: string) =>
      state.invoices.filter(i => i.status === status)
  },

  actions: {
    async fetchInvoices(status?: string, limit: number = 50, offset: number = 0) {
      this.loading = true
      this.error = null

      try {
        const response = await api.listInvoices(status, limit, offset)
        this.invoices = response.invoices || []
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
      } finally {
        this.loading = false
      }
    },

    async createInvoice(
      toPeerID: string,
      fromWalletId: string,
      amount: number,
      currency: string,
      network: string,
      description: string,
      expiresInHours: number = 24
    ) {
      this.loading = true
      this.error = null

      try {
        const response = await api.createInvoice(
          toPeerID,
          fromWalletId,
          amount,
          currency,
          network,
          description,
          expiresInHours
        )

        // Refresh invoices list (will show invoice as pending or failed)
        await this.fetchInvoices()

        // If response indicates delivery failure, set error but don't throw
        if (!response.success && response.invoice_id) {
          this.error = response.message || 'Invoice created but delivery failed'
        }

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async acceptInvoice(invoiceId: string, walletId: string, passphrase: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.acceptInvoice(invoiceId, walletId, passphrase)

        // Update invoice status locally
        const invoice = this.invoices.find(i => i.invoice_id === invoiceId)
        if (invoice) {
          invoice.status = 'accepted'
          invoice.accepted_at = Date.now() / 1000
        }

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async rejectInvoice(invoiceId: string, reason?: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.rejectInvoice(invoiceId, reason)

        // Update invoice status locally
        const invoice = this.invoices.find(i => i.invoice_id === invoiceId)
        if (invoice) {
          invoice.status = 'rejected'
          invoice.rejected_at = Date.now() / 1000
        }

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async deleteInvoice(invoiceId: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.deleteInvoice(invoiceId)

        // Remove invoice from local list
        const index = this.invoices.findIndex(i => i.invoice_id === invoiceId)
        if (index !== -1) {
          this.invoices.splice(index, 1)
        }

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async resendInvoice(invoiceId: string) {
      this.loading = true
      this.error = null

      try {
        const response = await api.resendInvoice(invoiceId)

        // Remove old invoice and refresh list to get new one
        const index = this.invoices.findIndex(i => i.invoice_id === invoiceId)
        if (index !== -1) {
          this.invoices.splice(index, 1)
        }

        // Refresh to get the new invoice
        await this.fetchInvoices()

        return response
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      } finally {
        this.loading = false
      }
    },

    async fetchInvoiceDetails(invoiceId: string) {
      this.error = null

      try {
        const invoice = await api.getInvoice(invoiceId)

        // Update or add invoice to list
        const index = this.invoices.findIndex(i => i.invoice_id === invoiceId)
        if (index !== -1) {
          this.invoices[index] = invoice
        } else {
          this.invoices.push(invoice)
        }

        return invoice
      } catch (error: any) {
        this.error = error.response?.data?.message || error.message
        throw error
      }
    },

    // WebSocket subscriptions
    subscribeToUpdates() {
      if (_wsUnsubscribe) {
        return // Already subscribed
      }

      const ws = getWebSocketService()
      if (!ws) {
        console.warn('WebSocket service not available')
        return
      }

      // Subscribe to invoice updates
      const unsubInvoices = ws.subscribe(MessageType.INVOICES_UPDATED, (_payload: any) => {
        console.log('Invoices updated via WebSocket')
        this.fetchInvoices()
      })

      const unsubCreated = ws.subscribe(MessageType.INVOICE_CREATED, (payload: any) => {
        console.log('Invoice created via WebSocket:', payload)
        this.fetchInvoices()
      })

      const unsubReceived = ws.subscribe(MessageType.INVOICE_RECEIVED, (payload: any) => {
        console.log('Invoice received via WebSocket:', payload)
        const invoice = payload as Invoice
        // Add to list if not already present
        const exists = this.invoices.some(i => i.invoice_id === invoice.invoice_id)
        if (!exists) {
          this.invoices.unshift(invoice)
        }
      })

      const unsubAccepted = ws.subscribe(MessageType.INVOICE_ACCEPTED, (payload: any) => {
        console.log('Invoice accepted via WebSocket:', payload)
        this.updateInvoiceStatus(payload.invoice_id, 'accepted')
      })

      const unsubRejected = ws.subscribe(MessageType.INVOICE_REJECTED, (payload: any) => {
        console.log('Invoice rejected via WebSocket:', payload)
        this.updateInvoiceStatus(payload.invoice_id, 'rejected')
      })

      const unsubSettled = ws.subscribe(MessageType.INVOICE_SETTLED, (payload: any) => {
        console.log('Invoice settled via WebSocket:', payload)
        this.updateInvoiceStatus(payload.invoice_id, 'settled')
      })

      const unsubExpired = ws.subscribe(MessageType.INVOICE_EXPIRED, (payload: any) => {
        console.log('Invoice expired via WebSocket:', payload)
        this.updateInvoiceStatus(payload.invoice_id, 'expired')
      })

      // Store combined unsubscribe function
      _wsUnsubscribe = () => {
        unsubInvoices()
        unsubCreated()
        unsubReceived()
        unsubAccepted()
        unsubRejected()
        unsubSettled()
        unsubExpired()
      }
    },

    unsubscribeFromUpdates() {
      if (_wsUnsubscribe) {
        _wsUnsubscribe()
        _wsUnsubscribe = null
      }
    },

    // Helper to update invoice status
    updateInvoiceStatus(invoiceId: string, status: string) {
      const invoice = this.invoices.find(i => i.invoice_id === invoiceId)
      if (invoice) {
        invoice.status = status
        const now = Date.now() / 1000

        if (status === 'accepted') invoice.accepted_at = now
        else if (status === 'rejected') invoice.rejected_at = now
        else if (status === 'settled') invoice.settled_at = now
      }
    }
  }
})
