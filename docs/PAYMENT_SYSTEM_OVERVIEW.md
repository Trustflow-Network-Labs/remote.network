# Payment System Overview

## Introduction

Remote Network features a comprehensive **decentralized payment system** for compensating peer-to-peer services and managing cryptocurrency wallets. The payment infrastructure enables trustless value exchange between nodes without centralized intermediaries.

---

## Quick Navigation

### Core Documentation

| Document | Purpose | Audience |
|----------|---------|----------|
| **[WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md)** | Complete wallet management guide (CLI + Web UI) | All users |
| **[P2P_PAYMENTS.md](./P2P_PAYMENTS.md)** | Peer-to-peer invoice payment system | Users sending/receiving payments |
| **[X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md)** | Technical protocol specification | Developers & integrators |

### Related Documentation

| Document | Relation |
|----------|----------|
| [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md) | Node identity encryption (different from wallet passphrases) |
| [VERIFIABLE_COMPUTE_DESIGN.md](./VERIFIABLE_COMPUTE_DESIGN.md) | Job execution payment integration |
| [API_REFERENCE.md](./API_REFERENCE.md) | HTTP/WebSocket payment endpoints |
| [WEBSOCKET_API.md](./WEBSOCKET_API.md) | Real-time payment notifications |

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Remote Network Payment System                 │
└─────────────────────────────────────────────────────────────────┘

┌──────────────────┐        ┌──────────────────┐
│  Wallet Manager  │        │ Invoice Manager  │
│                  │        │                  │
│ • Create/Import  │        │ • Create Invoice │
│ • Encrypt/Decrypt│        │ • Accept/Reject  │
│ • Sign Payments  │        │ • P2P Payments   │
│ • Query Balances │        │ • Expiration     │
└────────┬─────────┘        └────────┬─────────┘
         │                           │
         │         ┌─────────────────┘
         │         │
         ▼         ▼
    ┌────────────────────┐
    │  Escrow Manager    │
    │                    │
    │ • Create Escrow    │
    │ • Verify Signature │
    │ • Settle/Refund    │
    │ • x402 Protocol    │
    └──────────┬─────────┘
               │
               ▼
    ┌──────────────────────┐
    │  Payment Database    │
    │                      │
    │ • job_payments       │
    │ • payment_invoices   │
    │ • payment_signatures │
    └──────────┬───────────┘
               │
               ▼
    ┌──────────────────────────┐
    │  Blockchain Integration  │
    │                          │
    │ • EVM Chains (Base, ETH) │
    │ • Solana                 │
    │ • Balance Queries        │
    │ • Future: On-chain Escrow│
    └──────────────────────────┘
```

---

## Key Concepts

### 1. Wallets

**Cryptocurrency wallets for payment management**

- **Encryption**: AES-256-GCM with passphrase protection
- **Multi-chain**: Supports EVM (Base, Ethereum) and Solana
- **Management**: CLI and Web UI interfaces
- **Operations**: Create, import, export, delete, balance queries

**Example:**
```bash
# Create wallet on Base Sepolia testnet
remote-network wallet create --network eip155:84532

# Query balance
remote-network wallet balance --wallet-id wallet_abc123
```

**See:** [WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md)

---

### 2. Payment Signatures

**Cryptographic proof of payment authorization**

```
Payment Signature = Sign(
    Hash(amount + currency + network + recipient + timestamp + nonce),
    WalletPrivateKey
)
```

**Properties:**
- **Non-repudiation**: Cryptographically signed by payer
- **Integrity**: Hash prevents tampering
- **Freshness**: Timestamp + nonce prevent replay attacks
- **Verifiable**: Anyone can verify signature authenticity

**Signature Types:**
- **EVM**: ECDSA with secp256k1, Keccak256 hash
- **Solana**: Ed25519 signatures, SHA-256 hash

**See:** [X402_PAYMENT_PROTOCOL.md - Payment Signatures](./X402_PAYMENT_PROTOCOL.md#payment-signatures)

---

### 3. x402 Protocol

**Decentralized payment protocol for service compensation**

Named after HTTP 402 Payment Required status code, extended to P2P context.

**Core Flow:**
```
1. Requester creates payment signature
2. Provider verifies signature
3. Escrow created (locks payment)
4. Service executed
5. Escrow settled (releases payment)
```

**Features:**
- Blockchain-backed escrow
- Automatic settlement
- Multi-chain support
- Job execution integration

**See:** [X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md)

---

### 4. Escrow System

**Secure payment holding mechanism**

**States:**
```
pending → settled    (successful payment)
        → refunded   (failed service)
```

**Database:**
```sql
CREATE TABLE job_payments (
    id INTEGER PRIMARY KEY,
    job_execution_id INTEGER,  -- 0 for P2P payments
    payment_signature TEXT,
    amount REAL,
    currency TEXT,
    network TEXT,
    escrow_status TEXT,        -- 'pending', 'settled', 'refunded'
    ...
);
```

**Current:** Database-tracked escrow
**Future:** On-chain smart contract escrow

**See:** [X402_PAYMENT_PROTOCOL.md - Escrow Management](./X402_PAYMENT_PROTOCOL.md#escrow-management)

---

### 5. P2P Invoice Payments

**Direct payment requests between peers**

**Flow:**
```
1. Requester creates invoice
2. Recipient receives notification (WebSocket)
3. Recipient reviews & accepts/rejects
4. If accepted: Creates payment signature
5. Escrow created & auto-settled
6. Both peers notified of completion
```

**Use Cases:**
- Service fees outside job executions
- Cost splitting between peers
- Arbitrary payment requests
- Microtransactions

**See:** [P2P_PAYMENTS.md](./P2P_PAYMENTS.md)

---

## Payment Types

### Job Execution Payments

**Automatic payments for service execution**

| Aspect | Details |
|--------|---------|
| **Trigger** | Job execution request |
| **Payer** | Job requester |
| **Payee** | Service provider |
| **Escrow** | Created during job request |
| **Settlement** | After job completion |
| **Job ID** | Real job_execution_id |

**Example:**
```
User A requests job execution from User B
1. A creates payment signature (0.05 ETH)
2. B receives job request + payment
3. B creates escrow, executes job
4. Job completes successfully
5. B settles escrow, receives payment
```

**See:** [X402_PAYMENT_PROTOCOL.md - Payment Flow](./X402_PAYMENT_PROTOCOL.md#payment-flow)

---

### P2P Invoice Payments

**Manual payment requests between peers**

| Aspect | Details |
|--------|---------|
| **Trigger** | Invoice creation |
| **Payer** | Invoice recipient |
| **Payee** | Invoice creator |
| **Escrow** | Created on acceptance |
| **Settlement** | Auto after acceptance |
| **Job ID** | 0 (no job) |

**Example:**
```
User A requests payment from User B
1. A creates invoice (0.05 ETH for consulting)
2. B receives invoice notification
3. B reviews and accepts invoice
4. B's payment signature created
5. Escrow created & auto-settled
6. A receives payment
```

**See:** [P2P_PAYMENTS.md](./P2P_PAYMENTS.md)

---

## Supported Blockchains

### Ethereum Virtual Machine (EVM)

**Base (Recommended)**
- **Mainnet**: `eip155:8453` - Low fees, fast confirmations
- **Sepolia Testnet**: `eip155:84532` - For development/testing

**Other EVM Chains** (Compatible)
- Ethereum: `eip155:1`
- Polygon: `eip155:137`
- Arbitrum: `eip155:42161`
- Optimism: `eip155:10`

**Features:**
- Native currency: ETH
- Signature: ECDSA (secp256k1)
- Hash: Keccak256
- Future: ERC-20 token support

---

### Solana

**Networks**
- **Mainnet Beta**: `solana:mainnet-beta` - Production
- **Devnet**: `solana:devnet` - Development/testing

**Features:**
- Native currency: SOL
- Signature: Ed25519
- Hash: SHA-256
- Future: SPL token support

---

## Getting Started

### 1. Create a Wallet

**CLI:**
```bash
# Create wallet on Base Sepolia (testnet)
remote-network wallet create --network eip155:84532

# Set as default
remote-network config set x402_default_wallet_id <wallet-id>
```

**Web UI:**
1. Navigate to **Wallets** page
2. Click **"Create Wallet"**
3. Select network: Base Sepolia
4. Enter passphrase (twice)
5. Click **"Create"**

**Output:**
```
✓ Wallet created successfully
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Wallet ID:  wallet_abc123def456
Network:    eip155:84532
Address:    0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
```

---

### 2. Fund Your Wallet

**Base Sepolia Testnet:**
1. Copy wallet address
2. Visit [Base Sepolia Faucet](https://faucet.quicknode.com/base/sepolia)
3. Paste address and request testnet ETH
4. Wait 1-2 minutes for confirmation

**Verify balance:**
```bash
remote-network wallet balance --wallet-id wallet_abc123def456
```

---

### 3. Send a P2P Payment

**Web UI:**
1. Navigate to **Invoices** page
2. Click **"Create Invoice"**
3. Fill in form:
   - Recipient Peer ID: `c540bee38ab8a72d...`
   - Amount: `0.05`
   - Currency: `ETH`
   - Network: `eip155:84532`
   - Description: `Test payment`
4. Click **"Create Invoice"**

**Recipient receives notification:**
```
New Payment Request
0.05 ETH from abc123...
"Test payment"

[Accept] [Reject]
```

---

### 4. Receive a P2P Payment

**When invoice received:**
1. Check **Invoices** page → **Received** tab
2. Review invoice details
3. Click **"Accept"**
4. Select compatible wallet
5. Enter wallet passphrase
6. Click **"Accept & Pay"**

**Payment processes:**
```
✓ Invoice accepted successfully
✓ Payment initiated
✓ Escrow created
✓ Payment settled
```

---

## Security Considerations

### Invoice Encryption

**All payment invoice messages are end-to-end encrypted** to protect sensitive payment information:

**Encryption Method:**
- One-shot ECDH with ephemeral X25519 keys
- NaCl Secretbox authenticated encryption (XSalsa20-Poly1305)
- Ed25519 signature authentication
- Forward secrecy protection

**Protected Data:**
- Invoice amounts and currency
- Payment descriptions
- Recipient information
- Network details

**See:** [ENCRYPTION_ARCHITECTURE.md](./ENCRYPTION_ARCHITECTURE.md) for complete technical details.

---

### Wallet Security

**✅ Best Practices:**
- Use strong passphrases (16+ characters)
- Store passphrases in password manager
- Export and backup private keys securely
- Never share private keys
- Use testnet wallets for development

**❌ Avoid:**
- Weak or reused passphrases
- Storing passphrases in plaintext
- Committing wallet files to git
- Importing untrusted private keys

**See:** [WALLET_MANAGEMENT.md - Best Practices](./WALLET_MANAGEMENT.md#best-practices)

---

### Payment Security

**Protected Against:**
- ✅ Replay attacks (nonce + timestamp)
- ✅ Signature forgery (cryptographic verification)
- ✅ Double-spending (escrow locks funds)
- ✅ Man-in-the-middle (QUIC encryption)

**Future Protections:**
- 🔄 On-chain escrow (smart contracts)
- 🔄 Multi-sig settlements
- 🔄 Dispute resolution
- 🔄 Payment channels

**See:** [X402_PAYMENT_PROTOCOL.md - Security Model](./X402_PAYMENT_PROTOCOL.md#security-model)

---

### Invoice Security

**Before Accepting Invoices:**
1. Verify sender identity (known peer)
2. Confirm amount matches agreement
3. Check network compatibility
4. Ensure sufficient wallet balance
5. Review expiration time

**See:** [P2P_PAYMENTS.md - Security Considerations](./P2P_PAYMENTS.md#security-considerations)

---

## Configuration

### Payment Settings

```bash
# Set default wallet
remote-network config set x402_default_wallet_id wallet_abc123

# Invoice expiration (hours)
remote-network config set invoice_default_expiration_hours 24

# Invoice cleanup interval (hours)
remote-network config set invoice_cleanup_interval_hours 6

# Balance refresh interval (seconds)
remote-network config set wallet_balance_refresh_interval 60
```

---

### RPC Endpoints

**Default endpoints:**
```bash
# Base Mainnet
rpc_endpoint_eip155_8453=https://mainnet.base.org

# Base Sepolia
rpc_endpoint_eip155_84532=https://sepolia.base.org

# Solana Mainnet
rpc_endpoint_solana_mainnet-beta=https://api.mainnet-beta.solana.com

# Solana Devnet
rpc_endpoint_solana_devnet=https://api.devnet.solana.com
```

**Custom endpoints:**
```bash
# Override Base Mainnet RPC
remote-network config set rpc_endpoint_eip155_8453 https://your-rpc-url.com
```

---

## API Reference

### Wallet Endpoints

```
GET    /api/wallets                    List all wallets
POST   /api/wallets                    Create new wallet
POST   /api/wallets/import             Import wallet from private key
GET    /api/wallets/{id}/balance       Query wallet balance
POST   /api/wallets/{id}/export        Export private key
DELETE /api/wallets/{id}               Delete wallet
```

---

### Invoice Endpoints

```
POST   /api/invoices                   Create invoice
GET    /api/invoices                   List invoices
GET    /api/invoices/{id}              Get invoice details
POST   /api/invoices/{id}/accept       Accept invoice
POST   /api/invoices/{id}/reject       Reject invoice
```

---

### WebSocket Events

**Wallet Events:**
```
wallet.created           New wallet created
wallet.deleted           Wallet removed
wallet.balance.update    Balance changed
wallets.updated          Wallet list refreshed
```

**Invoice Events:**
```
invoice.created          New invoice created (sent)
invoice.received         New invoice received
invoice.accepted         Invoice accepted by payer
invoice.rejected         Invoice rejected by payer
invoice.settled          Payment completed
invoice.expired          Invoice expired
invoices.updated         Invoice list refreshed
```

**See:** [API_REFERENCE.md](./API_REFERENCE.md) for complete API documentation

---

## CLI Commands

### Wallet Management

```bash
# List wallets
remote-network wallet list

# Create wallet
remote-network wallet create --network eip155:84532

# Import wallet
remote-network wallet import --private-key 0x... --network eip155:84532

# Get wallet info
remote-network wallet info --wallet-id <id>

# Query balance
remote-network wallet balance --wallet-id <id>

# Export private key
remote-network wallet export --wallet-id <id>

# Delete wallet
remote-network wallet delete --wallet-id <id>
```

---

### Invoice Management (Future)

```bash
# Create invoice
remote-network invoice create \
  --to-peer <peer-id> \
  --amount 0.05 \
  --currency ETH \
  --network eip155:84532 \
  --description "Payment for services"

# List invoices
remote-network invoice list --status pending

# Accept invoice
remote-network invoice accept <invoice-id> --wallet-id <id>

# Reject invoice
remote-network invoice reject <invoice-id> --reason "Insufficient funds"
```

---

## Troubleshooting

### Common Issues

**"Invalid passphrase" error**
- Verify passphrase spelling and case
- Check for extra spaces
- If forgotten, wallet cannot be recovered

**Balance query fails**
- Check RPC endpoint status
- Verify network connectivity
- Configure custom RPC endpoint
- Check rate limiting on public RPC

**"Network mismatch" error**
- Invoice requires different network than wallet
- Create/use wallet on matching network

**Invoice not received**
- Verify recipient peer is online
- Check QUIC connection status
- Verify peer ID is correct

**See:**
- [WALLET_MANAGEMENT.md - Troubleshooting](./WALLET_MANAGEMENT.md#troubleshooting)
- [P2P_PAYMENTS.md - Troubleshooting](./P2P_PAYMENTS.md#troubleshooting)

---

## Future Roadmap

### Short-term (Q1 2025)

- [ ] CLI invoice management commands
- [ ] Browser notifications for invoices
- [ ] Invoice templates
- [ ] Payment history export (CSV)
- [ ] Multi-wallet selection UI improvements

---

### Medium-term (Q2-Q3 2025)

- [ ] On-chain escrow smart contracts
- [ ] ERC-20 token support (USDC, DAI)
- [ ] SPL token support (Solana)
- [ ] Payment channels (Lightning-style)
- [ ] Multi-sig wallet support
- [ ] Hardware wallet integration (Ledger, Trezor)

---

### Long-term (Q4 2025+)

- [ ] Cross-chain atomic swaps
- [ ] Decentralized dispute resolution
- [ ] Reputation-based credit lines
- [ ] Automated payment routing
- [ ] Privacy features (zk-proofs, stealth addresses)
- [ ] Integration with existing payment networks

---

## Related Documentation

### Core Payment Docs
- [WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md) - Wallet setup and management
- [P2P_PAYMENTS.md](./P2P_PAYMENTS.md) - Peer-to-peer invoice payments
- [X402_PAYMENT_PROTOCOL.md](./X402_PAYMENT_PROTOCOL.md) - Protocol specification

### Security & Encryption
- [ENCRYPTION_ARCHITECTURE.md](./ENCRYPTION_ARCHITECTURE.md) - Invoice encryption details
- [KEYSTORE_SETUP.md](./KEYSTORE_SETUP.md) - Node identity encryption

### Related Systems
- [VERIFIABLE_COMPUTE_DESIGN.md](./VERIFIABLE_COMPUTE_DESIGN.md) - Job execution integration
- [API_REFERENCE.md](./API_REFERENCE.md) - HTTP/WebSocket APIs
- [WEBSOCKET_API.md](./WEBSOCKET_API.md) - Real-time notifications

---

## Support

For payment system issues:

1. Check relevant troubleshooting sections
2. Review logs: `~/.cache/remote-network/logs/` (Linux) or `~/Library/Logs/remote-network/` (macOS)
3. Report issues: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated**: 2026-01-26
