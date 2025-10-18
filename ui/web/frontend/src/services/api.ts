import axios, { AxiosInstance } from 'axios'

/**
 * API Client for communicating with Remote Network Node
 * Supports both local and remote node connections
 */
class APIClient {
  private client: AxiosInstance
  private baseURL: string

  constructor(baseURL: string = 'http://localhost:8080') {
    this.baseURL = baseURL
    this.client = axios.create({
      baseURL: this.baseURL,
      timeout: 15000,
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
   * Get list of known peers
   */
  async getPeers() {
    const response = await this.client.get('/api/peers')
    return response.data
  }
}

// Create default instance for localhost
export const api = new APIClient()

// Export class for creating custom instances
export default APIClient
