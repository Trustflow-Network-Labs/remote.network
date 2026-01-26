# Encryption Architecture

## Overview

Remote Network implements **end-to-end encryption** for sensitive peer-to-peer communications and data transfers. This document details the cryptographic mechanisms used to protect invoicing messages and data service file transfers.

All encryption mechanisms prioritize:
- **Forward Secrecy**: Past communications remain secure even if long-term keys are compromised
- **Authentication**: Verify sender identity using digital signatures
- **Integrity**: Detect message tampering through authenticated encryption
- **Replay Protection**: Prevent replay attacks using timestamps and nonces

---

## Table of Contents

1. [Invoice Encryption](#invoice-encryption)
2. [DATA Service Encryption](#data-service-encryption)
3. [Security Properties](#security-properties)
4. [Key Derivation](#key-derivation)
5. [Implementation Details](#implementation-details)
6. [Comparison with Chat Encryption](#comparison-with-chat-encryption)
7. [Best Practices](#best-practices)

---

## Invoice Encryption

### Overview

Payment invoice messages use **one-shot ECDH encryption** with ephemeral keys for forward secrecy. Unlike chat messages (which use Double Ratchet for ongoing conversations), invoices are transactional and don't require persistent ratchet state.

**Key Features:**
- One-shot ECDH key exchange
- Ephemeral X25519 keys (generated per invoice)
- NaCl Secretbox encryption (XSalsa20 + Poly1305)
- Ed25519 signature authentication
- HKDF-SHA256 key derivation

---

### Encryption Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                  Invoice Encryption Protocol                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐                      ┌──────────────┐         │
│  │   Sender     │                      │  Recipient   │         │
│  │  (Invoice    │                      │  (Invoice    │         │
│  │   Creator)   │                      │  Receiver)   │         │
│  └──────┬───────┘                      └──────┬───────┘         │
│         │                                      │                 │
│         │ 1. Generate ephemeral X25519 keypair │                 │
│         │    (forward secrecy)                 │                 │
│         │                                      │                 │
│         │ 2. Convert recipient Ed25519 pub     │                 │
│         │    key to X25519                     │                 │
│         │                                      │                 │
│         │ 3. ECDH(ephemeral_priv,             │                 │
│         │         recipient_x25519_pub)        │                 │
│         │    → shared_secret                   │                 │
│         │                                      │                 │
│         │ 4. HKDF-SHA256(shared_secret)       │                 │
│         │    → encryption_key                  │                 │
│         │                                      │                 │
│         │ 5. Generate random nonce (24 bytes)  │                 │
│         │                                      │                 │
│         │ 6. NaCl Secretbox:                   │                 │
│         │    Encrypt(plaintext, key, nonce)    │                 │
│         │    → ciphertext                      │                 │
│         │                                      │                 │
│         │ 7. Ed25519 Sign:                     │                 │
│         │    Sign(ephemeral_pub || ciphertext  │                 │
│         │         || nonce, sender_priv_key)   │                 │
│         │    → signature                       │                 │
│         │                                      │                 │
│         │ 8. Send Invoice Message ────────────▶│                 │
│         │    {                                 │                 │
│         │      ciphertext,                     │                 │
│         │      nonce,                          │                 │
│         │      ephemeral_pub_key,              │                 │
│         │      signature                       │                 │
│         │    }                                 │                 │
│         │                                      │                 │
│         │                    9. Verify signature│                 │
│         │                       (authenticate)  │                 │
│         │                                      │                 │
│         │                   10. Convert own     │                 │
│         │                       Ed25519 priv to │                 │
│         │                       X25519          │                 │
│         │                                      │                 │
│         │                   11. ECDH(our_priv,  │                 │
│         │                       sender_eph_pub) │                 │
│         │                       → shared_secret │                 │
│         │                                      │                 │
│         │                   12. HKDF-SHA256     │                 │
│         │                       → decryption_key│                 │
│         │                                      │                 │
│         │                   13. Decrypt with    │                 │
│         │                       NaCl Secretbox  │                 │
│         │                       → plaintext     │                 │
│         │                                      │                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### Cryptographic Primitives

| Component | Algorithm | Purpose |
|-----------|-----------|---------|
| **Key Exchange** | X25519 (ECDH) | Derive shared secret |
| **Symmetric Encryption** | XSalsa20 | Confidentiality |
| **Message Authentication** | Poly1305 | Integrity |
| **Combined Encryption** | NaCl Secretbox | Authenticated encryption |
| **Digital Signature** | Ed25519 | Sender authentication |
| **Key Derivation** | HKDF-SHA256 | Derive encryption keys |
| **Identity Keys** | Ed25519 | Node identity (converted to X25519 for DH) |

---

### Message Format

**Encrypted Invoice Message:**
```go
type EncryptedInvoiceMessage struct {
    Ciphertext      []byte    // Encrypted payload
    Nonce           [24]byte  // Random nonce for Secretbox
    EphemeralPubKey [32]byte  // Sender's ephemeral X25519 public key
    Signature       []byte    // Ed25519 signature (64 bytes)
    SenderPeerID    string    // Sender's identity for key lookup
}
```

**Signature Payload:**
```
signature = Ed25519.Sign(
    ephemeral_pub_key || ciphertext || nonce,
    sender_ed25519_private_key
)
```

---

### Code Example

**Encryption (Sender):**
```go
import "github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"

// Initialize invoice crypto manager with node's Ed25519 keypair
invoiceCrypto := crypto.NewInvoiceCryptoManager(nodeKeyPair)

// Encrypt invoice data for recipient
ciphertext, nonce, ephemeralPubKey, signature, err := invoiceCrypto.EncryptInvoice(
    plaintextJSON,
    recipientEd25519PublicKey,
)
if err != nil {
    return fmt.Errorf("encryption failed: %v", err)
}

// Send encrypted message via QUIC
sendMessage(ciphertext, nonce, ephemeralPubKey, signature)
```

**Decryption (Recipient):**
```go
// Initialize invoice crypto manager
invoiceCrypto := crypto.NewInvoiceCryptoManager(nodeKeyPair)

// Decrypt received invoice
plaintext, err := invoiceCrypto.DecryptInvoice(
    ciphertext,
    nonce,
    senderEphemeralPubKey,
    signature,
    senderEd25519PublicKey,
)
if err != nil {
    return fmt.Errorf("decryption failed: %v", err)
}

// Parse decrypted invoice
var invoice InvoiceData
json.Unmarshal(plaintext, &invoice)
```

---

## DATA Service Encryption

### Overview

DATA service file transfers use **ECDH key exchange** to establish a symmetric encryption key. Unlike invoices (which encrypt inline), data transfers derive a shared key that is then used separately for streaming file encryption/decryption with AES-256-GCM.

**Key Features:**
- ECDH key exchange (separate from data transfer)
- Ephemeral X25519 keys (per transfer session)
- AES-256-GCM for file encryption
- Ed25519 signature authentication
- Timestamp-based replay protection (5-minute window)
- HKDF-SHA256 key derivation

---

### Encryption Flow

```
┌─────────────────────────────────────────────────────────────────┐
│            DATA Service Encryption Protocol                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐                      ┌──────────────┐         │
│  │   Sender     │                      │  Recipient   │         │
│  │ (Data Source)│                      │ (Data Worker)│         │
│  └──────┬───────┘                      └──────┬───────┘         │
│         │                                      │                 │
│         │ === KEY EXCHANGE PHASE ===           │                 │
│         │                                      │                 │
│         │ 1. Generate ephemeral X25519 keypair │                 │
│         │                                      │                 │
│         │ 2. Convert recipient Ed25519 pub     │                 │
│         │    key to X25519                     │                 │
│         │                                      │                 │
│         │ 3. ECDH(ephemeral_priv,             │                 │
│         │         recipient_x25519_pub)        │                 │
│         │    → shared_secret                   │                 │
│         │                                      │                 │
│         │ 4. HKDF-SHA256(shared_secret,       │                 │
│         │                "DATA_TRANSFER_KEY")  │                 │
│         │    → aes_256_key                     │                 │
│         │                                      │                 │
│         │ 5. timestamp = now()                 │                 │
│         │                                      │                 │
│         │ 6. Ed25519 Sign:                     │                 │
│         │    Sign(ephemeral_pub || timestamp,  │                 │
│         │         sender_priv_key)             │                 │
│         │    → signature                       │                 │
│         │                                      │                 │
│         │ 7. Send Key Exchange ───────────────▶│                 │
│         │    {                                 │                 │
│         │      ephemeral_pub_key,              │                 │
│         │      signature,                      │                 │
│         │      timestamp                       │                 │
│         │    }                                 │                 │
│         │                                      │                 │
│         │                     8. Verify timestamp│                 │
│         │                        (5-min window)│                 │
│         │                                      │                 │
│         │                     9. Verify signature│                 │
│         │                        (authenticate)│                 │
│         │                                      │                 │
│         │                    10. Convert own   │                 │
│         │                        Ed25519 priv  │                 │
│         │                        to X25519     │                 │
│         │                                      │                 │
│         │                    11. ECDH(our_priv,│                 │
│         │                        sender_eph_pub)│                 │
│         │                        → shared_secret│                 │
│         │                                      │                 │
│         │                    12. HKDF-SHA256   │                 │
│         │                        → aes_256_key │                 │
│         │                                      │                 │
│         │ === DATA TRANSFER PHASE ===          │                 │
│         │                                      │                 │
│         │ 13. Stream encrypted file chunks ───▶│                 │
│         │     (AES-256-GCM with derived key)   │                 │
│         │                                      │                 │
│         │                    14. Decrypt chunks│                 │
│         │                        with derived  │                 │
│         │                        AES-256 key   │                 │
│         │                                      │                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### Cryptographic Primitives

| Component | Algorithm | Purpose |
|-----------|-----------|---------|
| **Key Exchange** | X25519 (ECDH) | Derive shared secret |
| **Symmetric Encryption** | AES-256-GCM | File confidentiality |
| **Message Authentication** | GCM mode | Integrity & authenticity |
| **Digital Signature** | Ed25519 | Sender authentication |
| **Key Derivation** | HKDF-SHA256 | Derive AES-256 keys |
| **Replay Protection** | Timestamp validation | Prevent replay attacks |
| **Identity Keys** | Ed25519 | Node identity (converted to X25519 for DH) |

---

### Key Exchange Format

**ECDH Key Exchange Parameters:**
```go
type DataTransferKeyExchange struct {
    EphemeralPubKey [32]byte  // Sender's ephemeral X25519 public key
    Signature       []byte    // Ed25519 signature (64 bytes)
    Timestamp       int64     // Unix timestamp for replay protection
    SenderPeerID    string    // Sender's identity for key lookup
}
```

**Signature Payload:**
```
signature = Ed25519.Sign(
    ephemeral_pub_key || timestamp,
    sender_ed25519_private_key
)
```

**Timestamp Validation:**
- Accept if: `current_time - 300 < timestamp < current_time + 60`
- Reject if timestamp is > 5 minutes old (replay protection)
- Reject if timestamp is > 1 minute in future (clock skew tolerance)

---

### Code Example

**Key Exchange (Sender):**
```go
import "github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"

// Initialize data transfer crypto manager
dataCrypto := crypto.NewDataTransferCryptoManager(nodeKeyPair)

// Generate ECDH key exchange parameters
ephemeralPubKey, signature, timestamp, derivedKey, err := dataCrypto.GenerateKeyExchange(
    recipientEd25519PublicKey,
)
if err != nil {
    return fmt.Errorf("key exchange generation failed: %v", err)
}

// Send key exchange to recipient
sendKeyExchange(ephemeralPubKey, signature, timestamp)

// Use derivedKey for file encryption (AES-256-GCM)
encryptedFile := encryptFile(fileData, derivedKey)
sendFileChunks(encryptedFile)
```

**Key Derivation (Recipient):**
```go
// Initialize data transfer crypto manager
dataCrypto := crypto.NewDataTransferCryptoManager(nodeKeyPair)

// Derive decryption key from received parameters
derivedKey, err := dataCrypto.DeriveDecryptionKey(
    ephemeralPubKey,
    signature,
    timestamp,
    senderEd25519PublicKey,
)
if err != nil {
    return fmt.Errorf("key derivation failed: %v", err)
}

// Use derivedKey for file decryption
decryptedFile := decryptFile(receivedChunks, derivedKey)
```

---

## Security Properties

### Forward Secrecy

Both invoice and DATA service encryption provide **perfect forward secrecy**:

**How it works:**
1. Each transaction generates fresh ephemeral X25519 key pairs
2. Ephemeral private keys are immediately zeroed from memory after use
3. Shared secrets are derived via ECDH and then cleared
4. Even if long-term identity keys are compromised, past messages remain secure

**Example:**
```go
// Ephemeral key is cleared after key derivation
defer func() {
    for i := range ephemeralPrivKey {
        ephemeralPrivKey[i] = 0
    }
}()
```

---

### Authentication

**Sender Authentication** via Ed25519 signatures:

| Encryption Type | Signature Covers | Prevents |
|----------------|------------------|----------|
| **Invoice** | `ephemeral_pub_key \|\| ciphertext \|\| nonce` | Impersonation, tampering |
| **DATA Transfer** | `ephemeral_pub_key \|\| timestamp` | Impersonation, replay |

**Verification Process:**
1. Retrieve sender's Ed25519 public key using `sender_peer_id`
2. Verify signature over appropriate payload
3. Reject message if signature invalid
4. Proceed with decryption only after authentication

---

### Replay Protection

| Encryption Type | Mechanism | Protection Window |
|----------------|-----------|-------------------|
| **Invoice** | Random nonce (24 bytes) | N/A (one-time) |
| **DATA Transfer** | Timestamp validation | 5 minutes |

**DATA Transfer Replay Protection:**
```go
// Reject if timestamp is outside 5-minute window
now := time.Now().Unix()
if now - timestamp > 300 || timestamp > now + 60 {
    return errors.New("timestamp outside valid window (replay protection)")
}
```

---

### Integrity Protection

**Authenticated Encryption** ensures message integrity:

| Encryption Type | AEAD Scheme | Tag Size |
|----------------|-------------|----------|
| **Invoice** | NaCl Secretbox (XSalsa20-Poly1305) | 16 bytes |
| **DATA Transfer** | AES-256-GCM | 16 bytes |

**Protection guarantees:**
- Detects any tampering with ciphertext
- Detects any tampering with associated data
- Decryption fails immediately if integrity check fails
- No partial plaintext revealed on tampering

---

## Key Derivation

### HKDF-SHA256 Key Derivation

Both encryption mechanisms use **HKDF-SHA256** to derive encryption keys from ECDH shared secrets.

**HKDF Parameters:**
```go
// HKDF-SHA256 configuration
hkdfReader := hkdf.New(
    sha256.New,           // Hash function
    sharedSecret,         // Input key material (IKM)
    nil,                  // Salt (none - shared secret is already random)
    []byte(info),         // Info (domain separation)
)

// Extract 32 bytes for AES-256 or Secretbox key
io.ReadFull(hkdfReader, key[:32])
```

**Domain Separation (Info Parameter):**
```go
// Defined in internal/crypto/key_derivation.go
const (
    InfoInvoiceKey      = "INVOICE_ENCRYPTION_KEY_V1"
    InfoDataTransferKey = "DATA_TRANSFER_KEY_V1"
)
```

**Why domain separation?**
- Ensures keys for different purposes are cryptographically independent
- Prevents key reuse across different contexts
- Enables protocol versioning (V1, V2, etc.)

---

### Ed25519 ↔ X25519 Key Conversion

Both mechanisms use **Ed25519** identity keys but need **X25519** for ECDH.

**Public Key Conversion (Ed25519 → X25519):**
```go
func ed25519PublicKeyToCurve25519(ed25519PubKey ed25519.PublicKey) (*[32]byte, error) {
    // Convert Edwards curve point to Montgomery curve point
    var x25519PubKey [32]byte
    extra25519.PublicKeyToCurve25519(&x25519PubKey, (*[32]byte)(ed25519PubKey))
    return &x25519PubKey, nil
}
```

**Private Key Conversion (Ed25519 → X25519):**
```go
func ed25519PrivateKeyToCurve25519(ed25519PrivKey ed25519.PrivateKey) (*[32]byte, error) {
    // Extract seed and convert
    var x25519PrivKey [32]byte
    h := sha512.Sum512(ed25519PrivKey[:32])
    copy(x25519PrivKey[:], h[:32])

    // Clamp for X25519
    x25519PrivKey[0] &= 248
    x25519PrivKey[31] &= 127
    x25519PrivKey[31] |= 64

    return &x25519PrivKey, nil
}
```

---

## Implementation Details

### Invoice Encryption Implementation

**File:** `internal/crypto/invoice_crypto.go`

**Key Components:**
```go
type InvoiceCryptoManager struct {
    keyPair *KeyPair  // Node's Ed25519 identity keys
}

// Encrypt invoice for recipient
func (icm *InvoiceCryptoManager) EncryptInvoice(
    plaintext []byte,
    recipientEd25519PubKey ed25519.PublicKey,
) (ciphertext []byte, nonce [24]byte, ephemeralPubKey [32]byte,
    signature []byte, err error)

// Decrypt invoice from sender
func (icm *InvoiceCryptoManager) DecryptInvoice(
    ciphertext []byte,
    nonce [24]byte,
    senderEphemeralPubKey [32]byte,
    signature []byte,
    senderEd25519PubKey ed25519.PublicKey,
) (plaintext []byte, err error)
```

**Usage in Invoice Handler:**
- `internal/p2p/invoice_message_handler.go` - Sending encrypted invoices
- `internal/p2p/invoice_handler.go` - Receiving encrypted invoices

---

### DATA Service Encryption Implementation

**File:** `internal/crypto/data_crypto.go`

**Key Components:**
```go
type DataTransferCryptoManager struct {
    keyPair *KeyPair  // Node's Ed25519 identity keys
}

// Generate ECDH key exchange (sender side)
func (dcm *DataTransferCryptoManager) GenerateKeyExchange(
    recipientEd25519PubKey ed25519.PublicKey,
) (ephemeralPubKey [32]byte, signature []byte, timestamp int64,
    derivedKey [32]byte, err error)

// Derive decryption key (recipient side)
func (dcm *DataTransferCryptoManager) DeriveDecryptionKey(
    ephemeralPubKey [32]byte,
    signature []byte,
    timestamp int64,
    senderEd25519PubKey ed25519.PublicKey,
) (derivedKey [32]byte, err error)
```

**Usage in Data Workers:**
- `internal/workers/data_worker.go` - Key exchange and file encryption
- `internal/services/file_processor.go` - File chunk encryption/decryption
- `internal/core/job_manager.go` - Orchestrating encrypted transfers

---

### Memory Safety

Both implementations include **secure memory handling**:

**Zeroing Sensitive Keys:**
```go
// Clear ephemeral private key from memory
for i := range ephemeralPrivKey {
    ephemeralPrivKey[i] = 0
}

// Clear derived keys after use
defer ZeroKey(&encryptionKey)
defer ZeroKey(ourX25519PrivKey)
```

**ZeroKey Helper:**
```go
func ZeroKey(key interface{}) {
    switch k := key.(type) {
    case *[32]byte:
        for i := range k {
            k[i] = 0
        }
    // ... other key types
    }
}
```

---

## Comparison with Chat Encryption

Remote Network uses **different encryption mechanisms** for different use cases:

| Feature | **Invoice Encryption** | **DATA Service Encryption** | **Chat Encryption** |
|---------|----------------------|--------------------------|-------------------|
| **Use Case** | Payment invoices | File transfers | Messaging |
| **Protocol** | One-shot ECDH | ECDH key exchange | Double Ratchet |
| **Key Lifetime** | Per-invoice | Per-transfer session | Continuous ratcheting |
| **Forward Secrecy** | ✅ Ephemeral keys | ✅ Ephemeral keys | ✅ Ratcheting keys |
| **Break-in Recovery** | N/A (one-shot) | N/A (per-session) | ✅ DH ratchet |
| **Symmetric Cipher** | XSalsa20-Poly1305 | AES-256-GCM | AES-256-GCM |
| **State Management** | Stateless | Per-session only | Persistent ratchet |
| **Replay Protection** | Nonce | Timestamp | Message counter |
| **Implementation** | `invoice_crypto.go` | `data_crypto.go` | `chat_crypto.go` |

**Why different approaches?**

1. **Invoices**: Transactional, one-time messages → Simple one-shot ECDH
2. **Data Transfers**: Session-based, large files → ECDH key exchange + streaming
3. **Chat**: Ongoing conversations → Double Ratchet for continuous security

---

## Best Practices

### For Developers

**✅ DO:**
- Always verify signatures before decryption
- Clear ephemeral keys from memory immediately after use
- Use HKDF with proper domain separation
- Validate timestamps for replay protection
- Log encryption failures for debugging (without leaking keys)
- Use constant-time comparison for authentication tags

**❌ DON'T:**
- Reuse ephemeral keys across multiple messages
- Skip signature verification
- Ignore timestamp validation
- Log plaintext or encryption keys
- Use weak random number generators
- Share Ed25519 private keys across different nodes

---

### For System Administrators

**Security Monitoring:**
```bash
# Monitor encryption failures
tail -f ~/.cache/remote-network/logs/api.log | grep "encryption\|decryption"

# Check for replay attacks
grep "timestamp outside valid window" ~/.cache/remote-network/logs/api.log

# Monitor signature verification failures
grep "signature verification failed" ~/.cache/remote-network/logs/api.log
```

**Key Management:**
- Keep Ed25519 identity keys secure (see [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md))
- Use hardware security modules (HSM) for production deployments
- Regularly rotate node identity keys (with proper migration)
- Backup keystore with strong encryption

---

## Testing

### Unit Tests

**Invoice Encryption Tests:**
```bash
# Run invoice crypto tests
go test ./internal/crypto -run TestInvoiceCrypto -v

# Test encryption/decryption round-trip
go test ./internal/crypto -run TestInvoiceEncryptDecrypt -v

# Test signature verification
go test ./internal/crypto -run TestInvoiceSignatureVerification -v
```

**DATA Transfer Encryption Tests:**
```bash
# Run data transfer crypto tests
go test ./internal/crypto -run TestDataTransferCrypto -v

# Test key exchange
go test ./internal/crypto -run TestDataTransferKeyExchange -v

# Test timestamp validation
go test ./internal/crypto -run TestDataTransferReplayProtection -v
```

---

### Integration Tests

**End-to-End Invoice Encryption:**
```bash
# Test encrypted invoice creation and acceptance
go test ./internal/p2p -run TestEncryptedInvoiceFlow -v
```

**End-to-End DATA Transfer Encryption:**
```bash
# Test encrypted file transfer
go test ./internal/workers -run TestEncryptedDataTransfer -v
```

---

## Future Enhancements

### Short-term

- [ ] Add encryption performance metrics
- [ ] Implement key rotation mechanisms
- [ ] Add encryption audit logging
- [ ] Support hardware security modules (HSM)

### Medium-term

- [ ] Quantum-resistant key exchange (post-quantum cryptography)
- [ ] Certificate pinning for identity verification
- [ ] Multi-party encryption for group scenarios
- [ ] Encrypted metadata support

### Long-term

- [ ] Zero-knowledge proof integration
- [ ] Homomorphic encryption for computation on encrypted data
- [ ] Secure multi-party computation (MPC) support
- [ ] Threshold cryptography for distributed trust

---

## Related Documentation

### Encryption & Security
- [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md) - Node identity key management
- [CHAT_MESSAGING.md](./CHAT_MESSAGING.md) - Chat encryption (Double Ratchet)

### Invoice Payments
- [P2P_PAYMENTS.md](./P2P_PAYMENTS.md) - Peer-to-peer invoice payments
- [PAYMENT_SYSTEM_OVERVIEW.md](./PAYMENT_SYSTEM_OVERVIEW.md) - Payment system architecture
- [X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md) - Payment protocol specification

### Data Services
- [STANDALONE_SERVICES_API.md](./STANDALONE_SERVICES_API.md) - Standalone services overview
- [workflow-creation-and-execution.md](./workflow-creation-and-execution.md) - Workflow execution

### Architecture
- [ARCHITECTURE.md](./ARCHITECTURE.md) - System architecture overview
- [VERIFIABLE_COMPUTE_DESIGN.md](./VERIFIABLE_COMPUTE_DESIGN.md) - Verifiable computation

---

## References

### Cryptographic Standards

- **X25519**: [RFC 7748](https://www.rfc-editor.org/rfc/rfc7748) - Elliptic Curves for Security
- **Ed25519**: [RFC 8032](https://www.rfc-editor.org/rfc/rfc8032) - Edwards-Curve Digital Signature Algorithm
- **HKDF**: [RFC 5869](https://www.rfc-editor.org/rfc/rfc5869) - HMAC-based Extract-and-Expand Key Derivation Function
- **ChaCha20-Poly1305**: [RFC 8439](https://www.rfc-editor.org/rfc/rfc8439) - ChaCha20 and Poly1305 for IETF Protocols
- **AES-GCM**: [NIST SP 800-38D](https://nvlpubs.nist.gov/nistpubs/Legacy/SP/nistspecialpublication800-38d.pdf) - Galois/Counter Mode

### Libraries Used

- **golang.org/x/crypto/curve25519** - X25519 ECDH implementation
- **golang.org/x/crypto/nacl/secretbox** - NaCl authenticated encryption
- **golang.org/x/crypto/hkdf** - HKDF key derivation
- **crypto/ed25519** - Ed25519 signatures (Go standard library)

---

## Support

For encryption-related issues:

1. Check implementation files:
   - `internal/crypto/invoice_crypto.go`
   - `internal/crypto/data_crypto.go`
   - `internal/crypto/key_derivation.go`

2. Review test files:
   - `internal/crypto/invoice_crypto_test.go`
   - `internal/crypto/data_crypto_test.go`

3. Report security issues: security@remote.network (private disclosure)
4. Report bugs: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated**: 2026-01-26
**Version**: 1.0
