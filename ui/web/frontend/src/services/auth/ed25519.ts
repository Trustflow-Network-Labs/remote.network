import { api } from '../api'
import { ed25519 } from '@noble/curves/ed25519.js'

/**
 * Ed25519 Authentication Service
 * Handles authentication using Ed25519 private key signatures
 * Supports both PEM-encoded and raw binary key formats
 */

export interface Ed25519AuthResult {
  success: boolean
  token?: string
  peer_id?: string
  error?: string
}

/**
 * Convert hex string to Uint8Array
 */
function hexToBytes(hex: string): Uint8Array {
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substring(i, i + 2), 16)
  }
  return bytes
}

/**
 * Convert Uint8Array to hex string
 */
function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

/**
 * Sign a message with Ed25519 private key using @noble/curves
 * @param privateKeyHex - Full 64-byte private key as hex (or 32-byte seed)
 * @param message - Message to sign
 * @returns Signature as hex string
 */
function signMessage(privateKeyHex: string, message: Uint8Array): string {
  try {
    const privateKeyBytes = hexToBytes(privateKeyHex)

    // If we have a 32-byte seed, derive the full keypair
    let privateKeyToUse: Uint8Array

    if (privateKeyBytes.length === 32) {
      // This is just the seed - @noble/curves can work with it directly
      privateKeyToUse = privateKeyBytes
    } else if (privateKeyBytes.length === 64) {
      // Full private key (seed + public key)
      // Extract just the seed (first 32 bytes) for @noble/curves
      privateKeyToUse = privateKeyBytes.slice(0, 32)
    } else {
      throw new Error(`Invalid private key length: ${privateKeyBytes.length} (expected 32 or 64 bytes)`)
    }

    // Sign using @noble/curves/ed25519
    const signature = ed25519.sign(message, privateKeyToUse)
    return bytesToHex(signature)
  } catch (error: any) {
    throw new Error(`Failed to sign message: ${error.message || error}`)
  }
}

/**
 * Authenticate using Ed25519 private key
 * @param privateKeyHex - Ed25519 private key as hex string (64 or 128 chars / 32 or 64 bytes)
 * @param publicKeyHex - Optional Ed25519 public key as hex string (64 chars / 32 bytes)
 */
export async function authenticateEd25519(
  privateKeyHex: string,
  publicKeyHex?: string
): Promise<Ed25519AuthResult> {
  try {
    // Validate private key format (accept both 32-byte seed and 64-byte full key)
    if (!/^[0-9a-fA-F]{64}$/.test(privateKeyHex) && !/^[0-9a-fA-F]{128}$/.test(privateKeyHex)) {
      throw new Error('Invalid private key format. Expected 64 or 128 hex characters (32 or 64 bytes)')
    }

    // Validate public key format if provided
    if (publicKeyHex && !/^[0-9a-fA-F]{64}$/.test(publicKeyHex)) {
      throw new Error('Invalid public key format. Expected 64 hex characters (32 bytes)')
    }

    // Step 1: Get challenge from server
    const challengeResponse = await api.getChallenge()
    const challenge = challengeResponse.challenge

    // Step 2: Sign the challenge with private key
    const challengeBytes = hexToBytes(challenge)
    const signature = signMessage(privateKeyHex, challengeBytes)

    // Step 3: Send signature to server for verification
    const authResponse = await api.authenticateEd25519(challenge, signature, publicKeyHex)

    if (authResponse.success && authResponse.token) {
      // Store auth token and peer_id
      localStorage.setItem('auth_token', authResponse.token)
      localStorage.setItem('peer_id', authResponse.peer_id)
      localStorage.setItem('auth_provider', 'ed25519')

      return {
        success: true,
        token: authResponse.token,
        peer_id: authResponse.peer_id,
      }
    }

    return {
      success: false,
      error: authResponse.error || 'Authentication failed',
    }
  } catch (error: any) {
    return {
      success: false,
      error: error.message || 'Ed25519 authentication failed',
    }
  }
}

/**
 * Decode PEM-encoded private key to raw bytes
 */
function decodePEM(pemContent: string): Uint8Array {
  // Remove PEM headers and whitespace
  const base64 = pemContent
    .replace(/-----BEGIN PRIVATE KEY-----/g, '')
    .replace(/-----END PRIVATE KEY-----/g, '')
    .replace(/\s/g, '')

  // Decode base64
  const binaryString = atob(base64)
  const bytes = new Uint8Array(binaryString.length)
  for (let i = 0; i < binaryString.length; i++) {
    bytes[i] = binaryString.charCodeAt(i)
  }

  // Extract the actual Ed25519 private key from PKCS#8 format
  // PKCS#8 Ed25519 structure: [16 bytes header] + [32 bytes private key seed]
  // Total is 48 bytes, but we only need the last 32 bytes (the seed)
  // For Ed25519, the full private key is 64 bytes: [32 bytes seed] + [32 bytes public key]

  // Find the private key seed (last 32 bytes of PKCS#8 payload)
  if (bytes.length >= 48) {
    // PKCS#8 format - extract seed from offset 16
    const seed = bytes.slice(16, 48) // 32 bytes seed

    // For signing, we need the full 64-byte private key
    // Ed25519 full key = seed + public key
    // We'll derive the public key from the seed (this requires crypto library)
    // For now, return just the seed and handle it differently
    return seed
  }

  return bytes
}

/**
 * Load Ed25519 private key from file
 * Supports both raw binary (64 bytes) and PEM-encoded formats
 */
export async function loadPrivateKeyFromFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()

    reader.onload = (e) => {
      try {
        const result = e.target?.result

        if (result instanceof ArrayBuffer) {
          const bytes = new Uint8Array(result)

          // Try binary format first (32 or 64 bytes)
          if (bytes.length === 32 || bytes.length === 64) {
            // Valid binary key size - use it directly
            resolve(bytesToHex(bytes))
            return
          }

          // Not a valid binary size - try to decode as PEM text
          try {
            // Decode ArrayBuffer as UTF-8 text
            const decoder = new TextDecoder('utf-8')
            const text = decoder.decode(bytes)

            if (text.includes('BEGIN PRIVATE KEY')) {
              const keyBytes = decodePEM(text)

              // PEM gives us a 32-byte seed - this is perfect for @noble/curves
              if (keyBytes.length === 32) {
                // Return 32-byte seed as hex (64 hex chars)
                resolve(bytesToHex(keyBytes))
                return
              } else if (keyBytes.length === 64) {
                // Already have full key
                resolve(bytesToHex(keyBytes))
                return
              } else {
                reject(new Error(`Unexpected PEM key size: ${keyBytes.length} bytes`))
                return
              }
            } else {
              reject(
                new Error(
                  `Invalid key file: expected 32 or 64 bytes for binary format, or PEM format with BEGIN PRIVATE KEY header. Got ${bytes.length} bytes.`
                )
              )
              return
            }
          } catch (pemError: any) {
            reject(
              new Error(
                `Failed to parse key file: not a valid binary key (${bytes.length} bytes) or PEM format`
              )
            )
            return
          }
        } else {
          reject(new Error('Unexpected file content type'))
        }
      } catch (error: any) {
        reject(new Error(`Failed to parse key file: ${error.message}`))
      }
    }

    reader.onerror = () => reject(new Error('Failed to read file'))

    // Read as ArrayBuffer (works for both binary and text files)
    reader.readAsArrayBuffer(file)
  })
}
