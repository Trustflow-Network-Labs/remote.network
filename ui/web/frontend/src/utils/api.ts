/**
 * API utility functions
 */

/**
 * Get the full API URL for a given path
 * Uses the node endpoint from localStorage or defaults to localhost
 */
export function getApiUrl(path: string): string {
  const nodeEndpoint = localStorage.getItem('node_endpoint') || 'http://localhost:30069'

  // Ensure path starts with /
  const normalizedPath = path.startsWith('/') ? path : `/${path}`

  // Remove trailing slash from endpoint if present
  const normalizedEndpoint = nodeEndpoint.endsWith('/')
    ? nodeEndpoint.slice(0, -1)
    : nodeEndpoint

  return `${normalizedEndpoint}${normalizedPath}`
}
