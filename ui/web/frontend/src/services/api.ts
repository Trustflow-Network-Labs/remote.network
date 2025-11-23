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
   * Get node capabilities (Docker availability, etc.)
   */
  async getNodeCapabilities() {
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
}

// Create default instance for localhost
export const api = new APIClient()

// Export class for creating custom instances
export default APIClient
