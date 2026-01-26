# Peer-to-Peer Payment Invoices

## Overview

Remote Network supports **peer-to-peer payment invoices** for direct cryptocurrency payments between nodes. Unlike job execution payments, P2P invoices enable arbitrary payment requests between peers without requiring a service execution.

**Use cases:**
- Requesting payment for services rendered off-network
- Splitting costs between peers
- Peer-to-peer service fees
- Custom payment agreements
- Microtransactions between trusted peers

---

## Table of Contents

- [Supported Currencies](#supported-currencies)
- [How It Works](#how-it-works)
- [Invoice Lifecycle](#invoice-lifecycle)
- [Creating Invoices](#creating-invoices)
- [Receiving Invoices](#receiving-invoices)
- [Managing Invoices](#managing-invoices)
- [WebSocket Real-Time Updates](#websocket-real-time-updates)
- [Payment Flow vs Job Payments](#payment-flow-vs-job-payments)
- [Security Considerations](#security-considerations)
- [API Reference](#api-reference)

---

## Supported Currencies

### ⚠️ IMPORTANT: ERC-3009 / SPL Token Requirement

**All x402 facilitators require EIP-3009 compliant tokens on EVM or SPL/Token-2022 tokens on Solana.**
**Native blockchain currencies (ETH, SOL) are NOT supported by x402.org, PayAI, or x402-rs facilitators.**

### Supported Tokens

#### EVM Networks (Base, Ethereum, Polygon, etc.)
- ✅ **USDC** - Recommended, default currency
- ✅ **USDT** - Tether USD
- ✅ **Any ERC-3009 compliant token**
- ❌ **ETH** - NOT supported (not ERC-3009 compliant)
- ❌ **WETH** - NOT supported (lacks ERC-3009)

**Token Contract Addresses:**
| Network | USDC Contract Address |
|---------|----------------------|
| Base Sepolia | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` |
| Base Mainnet | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` |
| Ethereum Sepolia | `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238` |
| Ethereum Mainnet | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |

#### Solana Networks
- ✅ **USDC (SPL)** - Recommended
- ✅ **Any SPL or Token-2022 token**
- ❌ **SOL** - NOT supported with current x402 facilitators

**Token Mint Addresses:**
| Network | USDC Mint Address |
|---------|------------------|
| Solana Mainnet | `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v` |
| Solana Devnet | `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU` |

### Why ERC-3009 / SPL Tokens?

**ERC-3009** enables gasless token transfers using cryptographic signatures:
- Payer signs an authorization off-chain
- Facilitator executes the transfer on-chain
- Facilitator pays the gas fees
- Perfect for micropayments and API payments

**SPL Tokens** work similarly on Solana:
- Payer signs a transaction off-chain
- Transaction includes SPL token transfer instruction
- Facilitator submits and pays transaction fees
- Extremely low cost (fractions of a cent)

### Getting Testnet USDC

**Base Sepolia:**
1. Visit [Circle's Testnet Faucet](https://faucet.circle.com/)
2. Select Base Sepolia
3. Enter your wallet address
4. Receive testnet USDC

**Solana Devnet:**
1. Use Solana CLI: `spl-token airdrop <amount> <mint-address>`
2. Or use web-based faucets for testnet SPL USDC

---

## How It Works

### Invoice-Based Flow

P2P payments use an **invoice-request-accept** flow:

```
┌─────────────┐                                    ┌─────────────┐
│   Requester │                                    │    Payer    │
│  (creates   │                                    │ (receives   │
│   invoice)  │                                    │  invoice)   │
└──────┬──────┘                                    └──────┬──────┘
       │                                                  │
       │ 1. Create Invoice                                │
       │    - Amount, currency, network                   │
       │    - Description, expiration                     │
       │                                                  │
       │ 2. Send via QUIC ────────────────────────────▶  │
       │    (invoice_request message)                     │
       │                                                  │
       │                          3. Receive Notification │
       │                             (WebSocket)          │
       │                                                  │
       │                          4. Review Invoice       │
       │                             - Check amount       │
       │                             - Verify network     │
       │                             - Select wallet      │
       │                                                  │
       │                          5a. ACCEPT or 5b. REJECT│
       │                                                  │
       │  ◀──────────────────────────── Accept           │
       │    (payment signature + escrow)                  │
       │                                                  │
       │ 6. x402 Facilitator Verification                 │
       │    - Verify payment signature                    │
       │    - Create blockchain escrow                    │
       │    - Auto-settle (no job completion required)    │
       │                                                  │
       │ 7. Settlement ◀──────────────▶ Settlement       │
       │    (WebSocket notification)   (WebSocket)        │
       │                                                  │
       │ ✓ Payment Complete            ✓ Payment Complete│
       │   Status: settled             Status: settled    │
       └──────────────────────────────────────────────────┘
```

### Key Components

1. **Invoice Creator** (Requester)
   - Creates payment request
   - Specifies amount, currency, network
   - Receives payment when accepted

2. **Invoice Recipient** (Payer)
   - Receives invoice notification
   - Reviews and accepts/rejects
   - Provides wallet for payment

3. **x402 Facilitator**
   - Verifies payment signatures
   - Creates blockchain escrow
   - Auto-settles upon acceptance

4. **WebSocket Notifications**
   - Real-time status updates
   - Invoice received alerts
   - Payment settlement notifications

---

## Invoice Lifecycle

### Invoice States

```
pending ────▶ accepted ────▶ settled
   │
   ├─────────▶ rejected
   │
   └─────────▶ expired
```

| Status | Description | Can Transition To |
|--------|-------------|-------------------|
| **pending** | Created, awaiting response | accepted, rejected, expired |
| **accepted** | Accepted by payer, payment processing | settled |
| **rejected** | Declined by payer | (terminal state) |
| **settled** | Payment completed successfully | (terminal state) |
| **expired** | Timed out without response | (terminal state) |

### Status Transitions

**pending → accepted:**
- Payer accepts invoice
- Payment signature created
- Escrow initiated

**pending → rejected:**
- Payer declines invoice
- Optional reason provided
- No payment created

**pending → expired:**
- Expiration time reached
- No response from payer
- Automatic cleanup

**accepted → settled:**
- x402 facilitator verification complete
- Blockchain transaction confirmed
- Funds transferred to requester

---

## Creating Invoices

### Web UI

1. Navigate to **Invoices** page
2. Click **"Create Invoice"** button
3. Fill in the form:
   - **Recipient Peer ID**: Target peer to receive invoice
   - **Amount**: Payment amount (e.g., 0.05)
   - **Currency**: Token symbol (ETH, SOL)
   - **Network**: Blockchain network (must match wallet)
   - **Description**: Payment purpose (max 200 chars)
   - **Expires In**: Hours until expiration (default: 24)
4. Click **"Create Invoice"**

**Example form:**
```
Recipient Peer ID: c540bee38ab8a72d354839127bd3fda6b46f2ab5
Amount: 0.05
Currency: ETH
Network: eip155:84532 (Base Sepolia)
Description: Payment for consulting services
Expires In: 24 hours
```

**Success notification:**
```
✓ Invoice created successfully
  Invoice ID: inv_a1b2c3d4e5f6...
  Sent to: c540bee3...f2ab5
```

### CLI (Future)

```bash
# Create P2P invoice
remote-network invoice create \
  --to-peer c540bee38ab8a72d354839127bd3fda6b46f2ab5 \
  --amount 0.05 \
  --currency ETH \
  --network eip155:84532 \
  --description "Payment for consulting services" \
  --expires 24h
```

### REST API

```bash
POST /api/invoices

{
  "to_peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5",
  "amount": 0.05,
  "currency": "ETH",
  "network": "eip155:84532",
  "description": "Payment for consulting services",
  "expires_in_hours": 24
}
```

**Response:**
```json
{
  "success": true,
  "invoice_id": "inv_a1b2c3d4e5f6..."
}
```

---

## Receiving Invoices

### Real-Time Notification

When an invoice is sent to you:

1. **WebSocket notification** received
   ```json
   {
     "type": "invoice.received",
     "payload": {
       "invoice_id": "inv_...",
       "from_peer_id": "abc123...",
       "amount": 0.05,
       "currency": "ETH",
       "network": "eip155:84532",
       "description": "Payment for consulting services",
       "expires_at": 1735401234
     }
   }
   ```

2. **Browser notification** (if enabled)
   ```
   New Payment Request
   0.05 ETH from abc123...
   "Payment for consulting services"
   ```

3. **Invoice appears** in Invoices page
   - **Received** tab
   - **Pending** status
   - Action buttons available

### Reviewing an Invoice

**Invoice card displays:**
```
┌───────────────────────────────────────────────┐
│ Invoice ID: inv_a1b2c3d4e5f6...               │
│                                                │
│ Direction: ◀ Received                          │
│ From: abc123...def456                          │
│ Amount: 0.05 ETH                               │
│ Network: eip155:84532 (Base Sepolia)          │
│ Status: Pending                                │
│                                                │
│ Description:                                   │
│ "Payment for consulting services"              │
│                                                │
│ Expires: in 23 hours                           │
│                                                │
│ [Accept] [Reject] [Details]                    │
└───────────────────────────────────────────────┘
```

### Accepting an Invoice

**Web UI:**
1. Click **"Accept"** button on invoice card
2. **Accept Invoice dialog** appears:
   - Shows invoice details
   - Displays your compatible wallets
3. Select wallet (network must match invoice)
4. Enter wallet passphrase
5. Click **"Accept & Pay"**

**Accept dialog:**
```
┌─────────────────────────────────────────┐
│ Accept Invoice                          │
│                                         │
│ Invoice: inv_a1b2c3d4e5f6...            │
│ Amount: 0.05 ETH                        │
│ Network: eip155:84532                   │
│                                         │
│ Select Wallet:                          │
│ ○ wallet_abc123 (Base Sepolia)          │
│   Balance: 1.25 ETH                     │
│                                         │
│ ○ wallet_xyz789 (Solana Devnet)         │
│   ⚠️ Network mismatch - not compatible  │
│                                         │
│ Wallet Passphrase:                      │
│ [__________________]                    │
│                                         │
│        [Cancel]  [Accept & Pay]         │
└─────────────────────────────────────────┘
```

**What happens:**
1. Wallet decrypted with passphrase
2. Payment signature created
3. Escrow initiated via x402 facilitator
4. Invoice status → `accepted`
5. Auto-settlement begins

**Success notification:**
```
✓ Invoice accepted successfully
  Payment initiated
  Status: Processing
```

### Rejecting an Invoice

**Web UI:**
1. Click **"Reject"** button
2. **Reject Invoice dialog** appears
3. (Optional) Enter rejection reason
4. Click **"Reject"**

**Reject dialog:**
```
┌─────────────────────────────────────────┐
│ Reject Invoice                          │
│                                         │
│ Invoice: inv_a1b2c3d4e5f6...            │
│ Amount: 0.05 ETH                        │
│                                         │
│ Reason (optional):                      │
│ [_________________________________]      │
│                                         │
│        [Cancel]  [Reject]               │
└─────────────────────────────────────────┘
```

**Rejection reasons (examples):**
- "Insufficient funds"
- "Service not rendered"
- "Amount incorrect"
- "Invoice expired"

---

## Managing Invoices

### Listing Invoices

**Web UI - Tabs:**
- **All**: All invoices (sent + received)
- **Pending**: Awaiting action
- **Settled**: Completed payments
- **Expired**: Timed out invoices

**Filtering:**
```
Status: [All ▼] [Pending ▼] [Settled ▼] [Expired ▼]
Direction: [All ▼] [Sent ▼] [Received ▼]
Search: [_________________] 🔍
```

**DataTable columns:**
| Invoice ID | Direction | Peer | Amount | Network | Status | Created | Actions |
|------------|-----------|------|--------|---------|--------|---------|---------|
| inv_abc... | Sent ▶ | def456 | 0.05 ETH | Base | Settled | 2h ago | [Details] |
| inv_xyz... | Received ◀ | abc123 | 0.1 ETH | Solana | Pending | 5m ago | [Accept] [Reject] |

### Invoice Details

Click **"Details"** button to view full invoice information:

```
┌────────────────────────────────────────────────┐
│ Invoice Details                                │
│                                                │
│ Invoice ID:    inv_a1b2c3d4e5f6...             │
│ Status:        Settled ✓                       │
│                                                │
│ Amount:        0.05 ETH                        │
│ Currency:      ETH                             │
│ Network:       eip155:84532 (Base Sepolia)     │
│                                                │
│ From Peer:     abc123...def456                 │
│ To Peer:       xyz789...012345                 │
│                                                │
│ Description:                                   │
│ "Payment for consulting services"              │
│                                                │
│ Timeline:                                      │
│ Created:   2025-12-28 10:00:00 UTC             │
│ Accepted:  2025-12-28 10:15:23 UTC             │
│ Settled:   2025-12-28 10:16:45 UTC             │
│                                                │
│ Payment Details:                               │
│ Payment ID:    12345                           │
│ Escrow:        Created                         │
│ Settlement:    Completed                       │
│                                                │
│               [Close]                          │
└────────────────────────────────────────────────┘
```

### Automatic Expiration Cleanup

**Background process:**
- Runs every 6 hours
- Marks expired pending invoices
- Updates status to `expired`

**Configuration:**
```bash
remote-network config set invoice_cleanup_interval_hours 6
remote-network config set invoice_default_expiration_hours 24
```

---

## WebSocket Real-Time Updates

### Subscribe to Invoice Events

All invoice state changes are broadcast via WebSocket:

```javascript
// Subscribe to invoice events
const ws = getWebSocketService()

ws.subscribe('invoice.created', (data) => {
  console.log('Invoice created:', data.payload)
})

ws.subscribe('invoice.received', (data) => {
  console.log('Invoice received:', data.payload)
  showNotification(`Payment request: ${data.payload.amount} ${data.payload.currency}`)
})

ws.subscribe('invoice.accepted', (data) => {
  console.log('Invoice accepted:', data.payload)
})

ws.subscribe('invoice.settled', (data) => {
  console.log('Invoice settled:', data.payload)
  showNotification(`Payment received: ${data.payload.amount} ${data.payload.currency}`)
})

ws.subscribe('invoice.rejected', (data) => {
  console.log('Invoice rejected:', data.payload)
})

ws.subscribe('invoice.expired', (data) => {
  console.log('Invoice expired:', data.payload)
})
```

### Event Payloads

**invoice.created:**
```json
{
  "type": "invoice.created",
  "payload": {
    "invoice_id": "inv_...",
    "from_peer_id": "abc123...",
    "to_peer_id": "def456...",
    "amount": 0.05,
    "currency": "ETH",
    "network": "eip155:84532",
    "description": "Payment for consulting services",
    "status": "pending",
    "created_at": 1735401234,
    "expires_at": 1735487634
  }
}
```

**invoice.settled:**
```json
{
  "type": "invoice.settled",
  "payload": {
    "invoice_id": "inv_...",
    "status": "settled",
    "settled_at": 1735401345
  }
}
```

---

## Payment Flow vs Job Payments

### Comparison

| Aspect | Job Payments | P2P Invoice Payments |
|--------|--------------|---------------------|
| **Trigger** | Job execution request | Invoice creation |
| **Initiator** | Job requester | Invoice creator (payee) |
| **Payer** | Job requester | Invoice recipient |
| **Payee** | Service provider | Invoice creator |
| **Escrow Creation** | During job request | On invoice acceptance |
| **Settlement** | After job completion | Auto after acceptance |
| **Job ID** | Real job_execution_id | 0 (no job) |
| **Refund** | On job failure | On rejection/timeout |
| **Database** | `job_payments` table | `job_payments` + `payment_invoices` |
| **Purpose** | Compensate service execution | Arbitrary peer payments |

### Database Design

**P2P invoices reuse the x402 payment infrastructure:**

```sql
-- Invoices table (metadata)
CREATE TABLE payment_invoices (
    id INTEGER PRIMARY KEY,
    invoice_id TEXT UNIQUE,
    from_peer_id TEXT,
    to_peer_id TEXT,
    amount REAL,
    currency TEXT,
    network TEXT,
    description TEXT,
    status TEXT,
    payment_id INTEGER,  -- Links to job_payments
    created_at INTEGER,
    expires_at INTEGER,
    FOREIGN KEY (payment_id) REFERENCES job_payments(id)
);

-- Payments table (reused)
CREATE TABLE job_payments (
    id INTEGER PRIMARY KEY,
    job_execution_id INTEGER,  -- 0 for P2P payments
    amount REAL,
    currency TEXT,
    network TEXT,
    escrow_status TEXT,
    ...
);
```

**Key design:**
- P2P invoices use `job_execution_id = 0`
- Link via `payment_invoices.payment_id`
- Leverage existing x402 escrow logic
- Automatic settlement (no job completion check)

---

## Security Considerations

### End-to-End Encryption

**All invoice messages are end-to-end encrypted** using one-shot ECDH with ephemeral keys:

**Encryption Features:**
- ✅ **Forward Secrecy**: Past invoices remain secure even if long-term keys compromised
- ✅ **Authentication**: Ed25519 signatures verify sender identity
- ✅ **Integrity**: NaCl Secretbox (XSalsa20-Poly1305) prevents tampering
- ✅ **Confidentiality**: Only sender and recipient can read invoice content

**What's Protected:**
- Invoice amounts and currency
- Payment descriptions
- Recipient peer IDs
- Network information
- All invoice metadata

**Technical Details:**
```
Invoice Encryption Flow:
1. Generate ephemeral X25519 key pair (forward secrecy)
2. ECDH key exchange with recipient's public key
3. Derive encryption key using HKDF-SHA256
4. Encrypt with NaCl Secretbox (XSalsa20 + Poly1305)
5. Sign with Ed25519 for authentication
6. Send encrypted invoice via QUIC
```

**Learn more:** See [ENCRYPTION_ARCHITECTURE.md](./ENCRYPTION_ARCHITECTURE.md) for complete cryptographic details.

---

### Invoice Validation

**Before accepting an invoice, verify:**

1. **Sender Identity**
   - Known peer ID
   - Trusted peer
   - Expected payment request

2. **Amount Reasonability**
   - Matches agreed amount
   - Within expected range
   - Not suspiciously large

3. **Network Compatibility**
   - You have wallet on that network
   - Wallet has sufficient balance
   - RPC endpoint accessible

4. **Expiration**
   - Invoice not expired
   - Reasonable expiration time
   - Enough time to review

### Passphrase Protection

**Wallet passphrase security:**
- Never shared across invoices
- Required for each acceptance
- Not stored by application
- Cleared from memory after use

### Escrow Safety

**x402 facilitator protection:**
- Payment signature verified
- Blockchain escrow created
- Atomic settlement
- No double-spend risk

### Rejection Reasons

**Always provide reason when rejecting:**
- Helps requester understand
- Prevents disputes
- Maintains peer relationships
- Audit trail for disputes

---

## API Reference

### HTTP Endpoints

```
POST   /api/invoices                 Create invoice
GET    /api/invoices                 List invoices
GET    /api/invoices/{id}            Get invoice details
POST   /api/invoices/{id}/accept     Accept invoice
POST   /api/invoices/{id}/reject     Reject invoice
```

### Create Invoice

**Request:**
```http
POST /api/invoices
Content-Type: application/json

{
  "to_peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5",
  "amount": 0.05,
  "currency": "ETH",
  "network": "eip155:84532",
  "description": "Payment for consulting services",
  "expires_in_hours": 24
}
```

**Response:**
```json
{
  "success": true,
  "invoice_id": "inv_a1b2c3d4e5f6..."
}
```

### List Invoices

**Request:**
```http
GET /api/invoices?status=pending&limit=50&offset=0
```

**Query parameters:**
- `status` (optional): Filter by status (pending, settled, expired, rejected)
- `limit` (optional): Max results (default: 50)
- `offset` (optional): Pagination offset (default: 0)

**Response:**
```json
{
  "invoices": [
    {
      "invoice_id": "inv_...",
      "from_peer_id": "abc123...",
      "to_peer_id": "def456...",
      "amount": 0.05,
      "currency": "ETH",
      "network": "eip155:84532",
      "description": "Payment for consulting services",
      "status": "pending",
      "created_at": 1735401234,
      "expires_at": 1735487634
    }
  ],
  "count": 1
}
```

### Accept Invoice

**Request:**
```http
POST /api/invoices/inv_a1b2c3d4e5f6.../accept
Content-Type: application/json

{
  "wallet_id": "wallet_abc123",
  "passphrase": "MyWalletPassphrase123!"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Invoice accepted and payment initiated"
}
```

### Reject Invoice

**Request:**
```http
POST /api/invoices/inv_a1b2c3d4e5f6.../reject
Content-Type: application/json

{
  "reason": "Insufficient funds"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Invoice rejected"
}
```

### WebSocket Events

```javascript
// Event types
invoice.created          // New invoice created (sent)
invoice.received         // New invoice received
invoice.accepted         // Invoice accepted by payer
invoice.rejected         // Invoice rejected by payer
invoice.settled          // Payment completed
invoice.expired          // Invoice expired
invoices.updated         // Invoice list refreshed
```

---

## Best Practices

### For Invoice Creators

✅ **DO:**
- Provide clear, descriptive payment reasons
- Set reasonable expiration times (12-48 hours)
- Verify recipient peer ID before sending
- Monitor invoice status via WebSocket
- Follow up on expired invoices
- Keep records of invoice IDs

❌ **DON'T:**
- Send invoices to unknown peers
- Use vague descriptions
- Set very short expiration times (< 1 hour)
- Create duplicate invoices
- Spam peers with requests

### For Invoice Recipients

✅ **DO:**
- Review all invoice details before accepting
- Verify sender identity
- Check amount matches agreement
- Ensure sufficient wallet balance
- Provide rejection reasons
- Monitor settlement status

❌ **DON'T:**
- Accept invoices from unknown peers
- Pay without verifying amount
- Use wrong network wallet
- Ignore suspicious invoices
- Leave invoices pending indefinitely

---

## Troubleshooting

### Invoice Not Received

**Problem**: Created invoice doesn't appear for recipient

**Possible causes:**
1. Recipient peer offline
2. QUIC connection issue
3. Peer ID incorrect

**Solutions:**
```bash
# Verify recipient peer is online
remote-network peers list | grep c540bee3

# Check QUIC connections
remote-network status

# Verify peer ID spelling
remote-network peers info c540bee38ab8a72d354839127bd3fda6b46f2ab5
```

### Network Mismatch Error

**Problem**: "Network mismatch" when accepting invoice

**Cause**: Your wallet network doesn't match invoice network

**Solution:**
1. Check invoice network: `eip155:84532`
2. Create wallet on matching network:
   ```bash
   remote-network wallet create --network eip155:84532
   ```
3. Or use existing wallet on that network

### Acceptance Fails - Insufficient Balance

**Problem**: "Insufficient balance" error when accepting

**Solution:**
```bash
# Check wallet balance
remote-network wallet balance --wallet-id wallet_abc123

# Fund wallet on blockchain
# Transfer ETH/SOL to wallet address

# Retry acceptance
```

### Invoice Expired Before Review

**Problem**: Invoice expired before you could review

**Solution:**
- Contact invoice creator
- Request new invoice with longer expiration
- Monitor invoices more frequently
- Enable browser notifications

### Settlement Stuck

**Problem**: Invoice accepted but not settling

**Possible causes:**
1. x402 facilitator issue
2. Blockchain congestion
3. Payment signature invalid

**Solutions:**
```bash
# Check invoice status
curl http://localhost:8765/api/invoices/inv_...

# View payment details
curl http://localhost:8765/api/invoices/inv_.../payment

# Check logs
tail -f ~/.cache/remote-network/logs/api.log
```

---

## Related Documentation

### Payment System
- [PAYMENT_SYSTEM_OVERVIEW.md](./PAYMENT_SYSTEM_OVERVIEW.md) - Payment system architecture
- [WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md) - Wallet management guide
- [X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md) - Payment protocol specification

### Security & Encryption
- [ENCRYPTION_ARCHITECTURE.md](./ENCRYPTION_ARCHITECTURE.md) - Invoice encryption details
- [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md) - Node identity key management

---

## Support

For P2P payment issues:

1. Check [Troubleshooting](#troubleshooting) section
2. Review [WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md)
3. Review [X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md)
4. Report issues: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated**: 2026-01-26
