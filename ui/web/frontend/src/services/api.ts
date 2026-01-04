import axios, { AxiosInstance } from 'axios'

/**
 * API Client for communicating with Remote Network Node
 * Supports both local and remote node connections
 */
class APIClient {
  private client: AxiosInstance
  private baseURL: string

  constructor(baseURL: string = 'http://localhost:30069') {
    this.baseURL = baseURL
    this.client = axios.create({
      baseURL: this.baseURL,
      timeout: 3600000, // 1 hour to accommodate long-running operations like Docker builds
      headers: {
        'Content-Type': 'application/json',
      },
    })

    // Add request interceptor to include JWT token
    this.client.interceptors.request.use(
      (config) => {
        const token = localStorage.getItem('auth_token')
        if (token) {
          config.headers.Authorization = `Bearer ${token}`
        }
        return config
      },
      (error) => Promise.reject(error)
    )

    // Add response interceptor to handle auth errors
    this.client.interceptors.response.use(
      (response) => response,
      (error) => {
        if (error.response?.status === 401) {
          // Token expired or invalid, clear auth state
          localStorage.removeItem('auth_token')
          localStorage.removeItem('peer_id')
          localStorage.removeItem('wallet_address')
          localStorage.removeItem('auth_provider')
          window.location.href = '/login'
        }
        return Promise.reject(error)
      }
    )
  }

  /**
   * Update the base URL for connecting to a different node
   */
  setBaseURL(baseURL: string) {
    this.baseURL = baseURL
    this.client.defaults.baseURL = baseURL
  }

  /**
   * Get current base URL
   */
  getBaseURL(): string {
    return this.baseURL
  }

  /**
   * Health check endpoint
   */
  async health() {
    const response = await this.client.get('/api/health')
    return response.data
  }

  /**
   * Request a new authentication challenge
   */
  async getChallenge(): Promise<{ challenge: string; expires_in: number }> {
    const response = await this.client.get('/api/auth/challenge')
    return response.data
  }

  /**
   * Authenticate with Ed25519 signature
   */
  async authenticateEd25519(challenge: string, signature: string, publicKey?: string) {
    const response = await this.client.post('/api/auth/ed25519', {
      challenge,
      signature,
      public_key: publicKey,
    })
    return response.data
  }

  /**
   * Get node status
   */
  async getNodeStatus() {
    const response = await this.client.get('/api/node/status')
    return response.data
  }

  /**
   * Restart the node
   */
  async restartNode() {
    const response = await this.client.post('/api/node/restart')
    return response.data
  }

  /**
   * Get node capabilities (Docker availability, system capabilities, etc.)
   */
  async getNodeCapabilities(): Promise<NodeCapabilitiesResponse> {
    const response = await this.client.get('/api/node/capabilities')
    return response.data
  }

  /**
   * Get list of known peers
   */
  async getPeers() {
    const response = await this.client.get('/api/peers')
    return response.data
  }

  /**
   * Get peer capabilities (GPU, CPU, Memory, etc.) from DHT
   */
  async getPeerCapabilities(peerId: string): Promise<PeerCapabilitiesResponse> {
    const response = await this.client.get(`/api/peers/${peerId}/capabilities`)
    return response.data
  }

  // ===== Services API =====

  /**
   * Get all services
   */
  async getServices() {
    const response = await this.client.get('/api/services')
    return response.data
  }

  /**
   * Get service by ID
   */
  async getService(id: number) {
    const response = await this.client.get(`/api/services/${id}`)
    return response.data
  }

  /**
   * Add a new service
   */
  async addService(service: any) {
    const response = await this.client.post('/api/services', service)
    return response.data
  }

  /**
   * Update a service
   */
  async updateService(id: number, service: any) {
    const response = await this.client.put(`/api/services/${id}`, service)
    return response.data
  }

  /**
   * Delete a service
   */
  async deleteService(id: number) {
    const response = await this.client.delete(`/api/services/${id}`)
    return response.data
  }

  /**
   * Update service status
   */
  async updateServiceStatus(id: number, status: string) {
    const response = await this.client.put(`/api/services/${id}/status`, { status })
    return response.data
  }

  /**
   * Update service pricing and payment networks
   */
  async updateServicePricing(id: number, pricingData: {
    pricing_amount: number
    pricing_type: string
    pricing_interval: number
    pricing_unit: string
    accepted_payment_networks: string[]
  }) {
    const response = await this.client.put(`/api/services/${id}/pricing`, pricingData)
    return response.data
  }

  /**
   * Get service interfaces
   */
  async getServiceInterfaces(serviceId: number) {
    const response = await this.client.get(`/api/services/${serviceId}/interfaces`)
    return response.data
  }

  /**
   * Get service passphrase
   */
  async getServicePassphrase(id: number) {
    const response = await this.client.get(`/api/services/${id}/passphrase`)
    return response.data
  }

  /**
   * Create Docker service from registry
   */
  async createDockerFromRegistry(data: {
    service_name: string
    image_name: string
    image_tag?: string
    description?: string
    username?: string
    password?: string
  }) {
    const response = await this.client.post('/api/services/docker/from-registry', data)
    return response.data
  }

  /**
   * Create Docker service from Git repository
   */
  async createDockerFromGit(data: {
    service_name: string
    repo_url: string
    branch?: string
    username?: string
    password?: string
    description?: string
  }) {
    const response = await this.client.post('/api/services/docker/from-git', data)
    return response.data
  }

  /**
   * Create Docker service from local directory
   */
  async createDockerFromLocal(data: {
    service_name: string
    local_path: string
    description?: string
  }) {
    const response = await this.client.post('/api/services/docker/from-local', data)
    return response.data
  }

  /**
   * Update service interfaces for a Docker service
   */
  async updateDockerServiceInterfaces(serviceId: number, interfaces: any[]) {
    const response = await this.client.put(`/api/services/docker/${serviceId}/interfaces`, {
      interfaces
    })
    return response.data
  }

  /**
   * Update Docker service configuration (entrypoint and cmd)
   */
  async updateDockerServiceConfig(serviceId: number, config: { entrypoint: string, cmd: string }) {
    const response = await this.client.put(`/api/services/docker/${serviceId}/config`, config)
    return response.data
  }

  /**
   * Get Docker service details
   */
  async getDockerServiceDetails(serviceId: number) {
    const response = await this.client.get(`/api/services/docker/${serviceId}/details`)
    return response.data
  }

  // ===== Standalone Services API =====

  /**
   * Create Standalone service from local executable
   */
  async createStandaloneFromLocal(data: {
    service_name: string
    executable_path: string
    arguments?: string[]
    working_directory?: string
    environment_variables?: Record<string, string>
    timeout_seconds?: number
    run_as_user?: string
    description?: string
    capabilities?: Record<string, any>
    custom_interfaces?: any[]
  }) {
    const response = await this.client.post('/api/services/standalone/from-local', data)
    return response.data
  }

  /**
   * Create Standalone service from Git repository
   */
  async createStandaloneFromGit(data: {
    service_name: string
    repo_url: string
    branch?: string
    executable_path: string
    build_command?: string
    arguments?: string[]
    working_directory?: string
    environment_variables?: Record<string, string>
    timeout_seconds?: number
    run_as_user?: string
    username?: string
    password?: string
    description?: string
    capabilities?: Record<string, any>
    custom_interfaces?: any[]
  }) {
    const response = await this.client.post('/api/services/standalone/from-git', data)
    return response.data
  }

  /**
   * Get Standalone service details
   */
  async getStandaloneServiceDetails(serviceId: number) {
    const response = await this.client.get(`/api/services/standalone/${serviceId}/details`)
    return response.data
  }

  /**
   * Finalize uploaded Standalone service with configuration
   */
  async finalizeStandaloneUpload(serviceId: number, data: {
    executable_path: string
    arguments?: string[]
    working_directory?: string
    environment_variables?: Record<string, string>
    timeout_seconds?: number
    run_as_user?: string
    capabilities?: Record<string, any>
    custom_interfaces?: any[]
  }) {
    const response = await this.client.post(`/api/services/standalone/${serviceId}/finalize`, data)
    return response.data
  }

  /**
   * Delete Standalone service
   */
  async deleteStandaloneService(serviceId: number) {
    const response = await this.client.delete(`/api/services/standalone/${serviceId}`)
    return response.data
  }

  // ===== Blacklist API =====

  /**
   * Get blacklist
   */
  async getBlacklist() {
    const response = await this.client.get('/api/blacklist')
    return response.data
  }

  /**
   * Add peer to blacklist
   */
  async addToBlacklist(peerId: string) {
    const response = await this.client.post('/api/blacklist', { peer_id: peerId })
    return response.data
  }

  /**
   * Remove peer from blacklist
   */
  async removeFromBlacklist(peerId: string) {
    const response = await this.client.delete(`/api/blacklist/${peerId}`)
    return response.data
  }

  // ===== Workflows API =====

  /**
   * Get all workflows
   */
  async getWorkflows() {
    const response = await this.client.get('/api/workflows')
    return response.data
  }

  /**
   * Get workflow by ID with jobs
   */
  async getWorkflow(id: number) {
    const response = await this.client.get(`/api/workflows/${id}`)
    return response.data
  }

  /**
   * Create a new workflow
   */
  async createWorkflow(workflow: { name: string; description: string }) {
    const response = await this.client.post('/api/workflows', workflow)
    return response.data
  }

  /**
   * Update workflow metadata
   */
  async updateWorkflow(id: number, updates: any) {
    const response = await this.client.put(`/api/workflows/${id}`, updates)
    return response.data
  }

  /**
   * Delete workflow
   */
  async deleteWorkflow(id: number) {
    const response = await this.client.delete(`/api/workflows/${id}`)
    return response.data
  }

  /**
   * Add workflow node (service card)
   */
  async addWorkflowNode(workflowId: number, node: any) {
    const response = await this.client.post(`/api/workflows/${workflowId}/nodes`, node)
    return response.data
  }

  /**
   * Get all nodes for a workflow
   */
  async getWorkflowNodes(workflowId: number) {
    const response = await this.client.get(`/api/workflows/${workflowId}/nodes`)
    return response.data
  }

  /**
   * Get workflow connections
   */
  async getWorkflowConnections(workflowId: number) {
    const response = await this.client.get(`/api/workflows/${workflowId}/connections`)
    return response.data
  }

  /**
   * Add workflow connection
   */
  async addWorkflowConnection(workflowId: number, connection: any) {
    const response = await this.client.post(`/api/workflows/${workflowId}/connections`, connection)
    return response.data
  }

  /**
   * Delete workflow connection
   */
  async deleteWorkflowConnection(workflowId: number, connectionId: number) {
    const response = await this.client.delete(`/api/workflows/${workflowId}/connections/${connectionId}`)
    return response.data
  }

  /**
   * Update workflow connection (e.g., destination_file_name)
   */
  async updateWorkflowConnection(workflowId: number, connectionId: number, data: { destination_file_name?: string | null }) {
    const response = await this.client.put(`/api/workflows/${workflowId}/connections/${connectionId}`, data)
    return response.data
  }

  /**
   * Remove workflow node
   */
  async removeWorkflowNode(workflowId: number, nodeId: number) {
    const response = await this.client.delete(`/api/workflows/${workflowId}/nodes/${nodeId}`)
    return response.data
  }

  /**
   * Update node GUI properties (position)
   */
  async updateNodeGUIProps(workflowId: number, nodeId: number, props: { x: number; y: number }) {
    const response = await this.client.put(`/api/workflows/${workflowId}/nodes/${nodeId}/gui-props`, props)
    return response.data
  }

  /**
   * Update node configuration (entrypoint and cmd)
   */
  async updateNodeConfig(workflowId: number, nodeId: number, config: { entrypoint: string[]; cmd: string[] }) {
    const response = await this.client.put(`/api/workflows/${workflowId}/nodes/${nodeId}/config`, config)
    return response.data
  }

  /**
   * Get workflow UI state
   */
  async getWorkflowUIState(workflowId: number) {
    const response = await this.client.get(`/api/workflows/${workflowId}/ui-state`)
    return response.data
  }

  /**
   * Update workflow UI state
   */
  async updateWorkflowUIState(workflowId: number, state: any) {
    const response = await this.client.put(`/api/workflows/${workflowId}/ui-state`, state)
    return response.data
  }

  // Legacy job methods (for backward compatibility)
  async addWorkflowJob(workflowId: number, job: any) {
    return this.addWorkflowNode(workflowId, job)
  }

  async removeWorkflowJob(workflowId: number, jobId: number) {
    return this.removeWorkflowNode(workflowId, jobId)
  }

  async updateJobGUIProps(workflowId: number, jobId: number, props: { x: number; y: number }) {
    return this.updateNodeGUIProps(workflowId, jobId, props)
  }

  /**
   * Execute workflow
   */
  async executeWorkflow(id: number) {
    const response = await this.client.post(`/api/workflows/${id}/execute`)
    return response.data
  }

  /**
   * Get workflow execution instances
   */
  async getWorkflowExecutionInstances(workflowId: number) {
    const response = await this.client.get(`/api/workflows/${workflowId}/execution-instances`)
    return response.data
  }

  /**
   * Get jobs for a workflow execution
   */
  async getWorkflowExecutionJobs(executionId: number) {
    const response = await this.client.get(`/api/workflow-executions/${executionId}/jobs`)
    return response.data
  }

  /**
   * Get workflow execution status (execution + jobs)
   */
  async getWorkflowExecutionStatus(executionId: number) {
    const response = await this.client.get(`/api/workflow-executions/${executionId}/status`)
    return response.data
  }

  // ===== Relay API =====

  /**
   * Get all active relay sessions (relay mode only)
   */
  async getRelaySessions() {
    const response = await this.client.get('/api/relay/sessions')
    return response.data
  }

  /**
   * Disconnect a specific relay session
   */
  async disconnectRelaySession(sessionId: string) {
    const response = await this.client.post(`/api/relay/sessions/${sessionId}/disconnect`)
    return response.data
  }

  /**
   * Blacklist and disconnect a peer from relay session
   */
  async blacklistRelaySession(sessionId: string) {
    const response = await this.client.post(`/api/relay/sessions/${sessionId}/blacklist`)
    return response.data
  }

  /**
   * Get available relay candidates (NAT mode only)
   */
  async getRelayCandidates() {
    const response = await this.client.get('/api/relay/candidates')
    return response.data
  }

  /**
   * Connect to a specific relay
   * Uses extended timeout as relay connection can take time (NAT traversal, handshakes, etc.)
   */
  async connectToRelay(peerId: string) {
    const response = await this.client.post('/api/relay/connect', { peer_id: peerId }, {
      timeout: 60000 // 60 seconds for relay connection
    })
    return response.data
  }

  /**
   * Disconnect from current relay
   */
  async disconnectFromRelay() {
    const response = await this.client.post('/api/relay/disconnect')
    return response.data
  }

  /**
   * Set preferred relay
   */
  async setPreferredRelay(peerId: string) {
    const response = await this.client.post('/api/relay/prefer', { peer_id: peerId })
    return response.data
  }

  // ===== Wallet Management =====

  /**
   * List all wallets
   */
  async listWallets(includeBalance: boolean = false): Promise<{ wallets: Wallet[]; count: number }> {
    const response = await this.client.get('/api/wallets', {
      params: { balance: includeBalance }
    })
    return response.data
  }

  /**
   * Create new wallet
   */
  async createWallet(network: string, passphrase: string): Promise<{ success: boolean; wallet_id: string; address: string; network: string }> {
    const response = await this.client.post('/api/wallets', { network, passphrase })
    return response.data
  }

  /**
   * Import wallet from private key
   */
  async importWallet(privateKey: string, network: string, passphrase: string): Promise<{ success: boolean; wallet_id: string; address: string; network: string }> {
    const response = await this.client.post('/api/wallets/import', {
      private_key: privateKey,
      network,
      passphrase
    })
    return response.data
  }

  /**
   * Delete wallet
   */
  async deleteWallet(walletId: string, passphrase: string): Promise<{ success: boolean; message: string }> {
    const response = await this.client.delete(`/api/wallets/${walletId}`, {
      data: { passphrase }
    })
    return response.data
  }

  /**
   * Get wallet balance
   */
  async getWalletBalance(walletId: string): Promise<WalletBalance> {
    const response = await this.client.get(`/api/wallets/${walletId}/balance`)
    return response.data
  }

  /**
   * Export wallet private key
   */
  async exportWallet(walletId: string, passphrase: string): Promise<{ success: boolean; wallet_id: string; private_key: string; address: string; network: string }> {
    const response = await this.client.post(`/api/wallets/${walletId}/export`, { passphrase })
    return response.data
  }

  // ===== Invoice Management =====

  /**
   * Create payment invoice
   */
  async createInvoice(
    toPeerID: string,
    fromWalletId: string,
    amount: number,
    currency: string,
    network: string,
    description: string,
    expiresInHours: number = 24
  ): Promise<{ success: boolean; invoice_id: string; message?: string; delivery_error?: string }> {
    const response = await this.client.post('/api/invoices', {
      to_peer_id: toPeerID,
      from_wallet_id: fromWalletId,
      amount,
      currency,
      network,
      description,
      expires_in_hours: expiresInHours
    })
    return response.data
  }

  /**
   * List invoices
   */
  async listInvoices(status?: string, limit: number = 50, offset: number = 0): Promise<{ invoices: Invoice[]; count: number }> {
    const response = await this.client.get('/api/invoices', {
      params: { status, limit, offset }
    })
    return response.data
  }

  /**
   * Get invoice details
   */
  async getInvoice(invoiceId: string): Promise<Invoice> {
    const response = await this.client.get(`/api/invoices/${invoiceId}`)
    return response.data
  }

  /**
   * Accept invoice and make payment
   */
  async acceptInvoice(invoiceId: string, walletId: string, passphrase: string): Promise<{ success: boolean; message: string }> {
    const response = await this.client.post(`/api/invoices/${invoiceId}/accept`, {
      wallet_id: walletId,
      passphrase
    })
    return response.data
  }

  /**
   * Reject invoice
   */
  async rejectInvoice(invoiceId: string, reason?: string): Promise<{ success: boolean; message: string }> {
    const response = await this.client.post(`/api/invoices/${invoiceId}/reject`, {
      reason: reason || ''
    })
    return response.data
  }

  /**
   * Delete invoice (only for failed/expired/rejected invoices)
   */
  async deleteInvoice(invoiceId: string): Promise<{ success: boolean; message: string }> {
    const response = await this.client.delete(`/api/invoices/${invoiceId}/delete`)
    return response.data
  }

  /**
   * Resend invoice (only for failed invoices)
   */
  async resendInvoice(invoiceId: string): Promise<{ success: boolean; message: string; new_invoice_id: string }> {
    const response = await this.client.post(`/api/invoices/${invoiceId}/resend`)
    return response.data
  }
}

// Create default instance for localhost
export const api = new APIClient()

// Export class for creating custom instances
export default APIClient

// ===== Type Definitions =====

/**
 * Wallet information
 */
export interface Wallet {
  wallet_id: string
  network: string
  address: string
  created_at: number
  balance?: WalletBalance
}

/**
 * Wallet balance information
 */
export interface WalletBalance {
  wallet_id: string
  network: string
  address: string
  balance: number
  currency: string
  updated_at: number
}

/**
 * Payment invoice
 */
export interface Invoice {
  invoice_id: string
  from_peer_id: string
  to_peer_id: string
  amount: number
  currency: string
  network: string
  description: string
  status: string // 'pending', 'accepted', 'rejected', 'settled', 'expired'
  created_at: number
  expires_at?: number
  accepted_at?: number
  rejected_at?: number
  settled_at?: number
}

/**
 * GPU information
 */
export interface GPUInfo {
  index: number
  name: string
  vendor: string
  memory_mb: number
  uuid?: string
  driver_version?: string
}

/**
 * System capabilities of a peer
 */
export interface SystemCapabilities {
  platform: string
  architecture: string
  kernel_version: string
  cpu_model: string
  cpu_cores: number
  cpu_threads: number
  total_memory_mb: number
  available_memory_mb: number
  total_disk_mb: number
  available_disk_mb: number
  gpus?: GPUInfo[]
  has_docker: boolean
  docker_version?: string
  has_python: boolean
  python_version?: string
}

/**
 * Response from peer capabilities endpoint
 */
export interface PeerCapabilitiesResponse {
  peer_id: string
  capabilities?: SystemCapabilities
  error?: string
}

/**
 * Response from node capabilities endpoint
 */
export interface NodeCapabilitiesResponse {
  docker_available: boolean
  docker_version?: string
  colima_status?: string
  system?: SystemCapabilities
}
