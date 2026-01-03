# x402 Payment Protocol

## Overview

The **x402 Payment Protocol** is Remote Network's decentralized payment system for compensating peer-to-peer service execution. It extends the HTTP 402 Payment Required concept to a peer-to-peer context, enabling trustless payments for computational services.

**Key features:**
- **Blockchain-backed escrow** for payment security
- **Multi-chain support** (Ethereum, Solana, Base, etc.)
- **Payment signatures** for cryptographic proof
- **Facilitator verification** for escrow management
- **Automatic settlement** upon service completion
- **Refund mechanisms** for failed executions

---

## Table of Contents

- [Architecture](#architecture)
- [Payment Flow](#payment-flow)
- [Payment Signatures](#payment-signatures)
- [Escrow Management](#escrow-management)
- [Settlement Process](#settlement-process)
- [Supported Blockchains](#supported-blockchains)
- [Database Schema](#database-schema)
- [Configuration](#configuration)
- [Security Model](#security-model)
- [Error Handling](#error-handling)

---

## Architecture

### System Components

```
┌──────────────────────────────────────────────────────────────┐
│                    x402 Payment Protocol                      │
└──────────────────────────────────────────────────────────────┘

┌─────────────┐                                   ┌─────────────┐
│   Requester │                                   │   Provider  │
│   (Payer)   │                                   │  (Payee)    │
└──────┬──────┘                                   └──────┬──────┘
       │                                                 │
       │ 1. Job Request + Payment Signature              │
       ├────────────────────────────────────────────────▶│
       │                                                 │
       │                                                 │ 2. Create Escrow
       │                                                 │    ↓
       │                                          ┌──────▼──────┐
       │                                          │   x402      │
       │                                          │ Facilitator │
       │                                          │  (Escrow)   │
       │                                          └──────┬──────┘
       │                                                 │
       │                                                 │ 3. Verify Signature
       │                                                 │ 4. Lock Funds
       │                                                 │
       │                                                 │
       │                        5. Execute Service       │
       │ ◀───────────────────────────────────────────────┤
       │    (Docker containers, workflows, etc.)         │
       │                                                 │
       │                                                 │
       │                        6. Service Complete      │
       │                                                 │
       │                                          ┌──────▼──────┐
       │                                          │ Settlement  │
       │                                          │   Verify    │
       │                                          │  Complete   │
       │                                          └──────┬──────┘
       │                                                 │
       │                        7. Release Funds         │
       │ ◀───────────────────────────────────────────────┤
       │                                                 │
       │ ✓ Payment Released          ✓ Payment Received │
       └─────────────────────────────────────────────────┘
```

### Core Modules

**1. WalletManager** (`internal/payment/wallet_manager.go`)
- Wallet CRUD operations
- Private key encryption/decryption
- Payment signature creation
- Balance queries

**2. EscrowManager** (`internal/payment/escrow_manager.go`)
- Escrow creation and lifecycle
- Payment verification
- Settlement and refund logic
- Facilitator integration

**3. InvoiceManager** (`internal/payment/invoice_manager.go`)
- P2P invoice management
- Invoice-to-payment mapping
- Automatic expiration cleanup

**4. PaymentDatabase** (`internal/database/job_payments.go`)
- Payment persistence
- Status tracking
- Transaction records

---

## Payment Flow

### Job Execution Payment

**Complete flow for paying for a remote job execution:**

```
Step 1: Job Request with Payment
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Requester creates payment signature:
  - Amount: 0.05 ETH
  - Currency: ETH
  - Network: eip155:84532
  - Recipient: Provider peer ID
  - Wallet: Requester's wallet
  - Passphrase: Decryption key

Signature includes:
  - Payment data hash
  - Wallet address
  - Blockchain signature
  - Timestamp + nonce

QUIC message sent:
  MessageType: job_request
  Payload: {
    job_spec: {...},
    payment_signature: {...}
  }

Step 2: Provider Receives & Creates Escrow
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Provider's EscrowManager:
  1. Validates payment signature
  2. Checks wallet address on blockchain
  3. Verifies sufficient balance
  4. Creates escrow record in database:
     - job_execution_id: 12345
     - amount: 0.05
     - currency: ETH
     - network: eip155:84532
     - status: 'pending'

Escrow ID returned: 67890

Step 3: Job Execution
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Provider executes job:
  - Docker container launched
  - Service runs
  - Results generated
  - Execution completes

Execution status: 'completed'

Step 4: Settlement
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Provider calls SettleEscrow(67890):
  1. Verifies job execution completed
  2. Updates escrow status: 'settled'
  3. Records settlement timestamp
  4. Triggers blockchain transaction (off-chain for now)

WebSocket notifications sent:
  - Requester: "Payment settled"
  - Provider: "Payment received"

Result: Payment complete ✓
```

### P2P Invoice Payment

**Complete flow for peer-to-peer invoice payment:**

```
Step 1: Invoice Creation
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Requester (payee) creates invoice:
  - to_peer_id: abc123...
  - amount: 0.05
  - currency: ETH
  - network: eip155:84532
  - description: "Consulting fee"
  - expires_in: 24 hours

Invoice stored:
  - invoice_id: inv_xyz789...
  - status: 'pending'
  - created_at: <timestamp>
  - expires_at: <timestamp + 24h>

QUIC message sent:
  MessageType: invoice_request
  Payload: {invoice_data}

Step 2: Recipient Reviews
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Recipient receives WebSocket notification:
  type: invoice.received
  payload: {invoice_data}

Recipient reviews:
  - Amount: 0.05 ETH ✓
  - Network: eip155:84532 ✓
  - From: Trusted peer ✓
  - Description: Valid ✓

Decision: Accept

Step 3: Payment Creation (Acceptance)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Recipient accepts via Web UI:
  1. Selects compatible wallet
  2. Enters passphrase
  3. Creates payment signature
  4. Calls AcceptInvoice(invoice_id, wallet_id, passphrase)

InvoiceManager:
  1. Creates PaymentData:
     - amount: 0.05
     - currency: ETH
     - network: eip155:84532
     - recipient: Requester peer ID
  2. Signs payment with wallet
  3. Creates escrow (job_execution_id = 0)
  4. Updates invoice:
     - status: 'accepted'
     - payment_id: 67890

Step 4: Automatic Settlement
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
InvoiceManager auto-settles:
  - No job execution required (P2P payment)
  - Immediately calls SettleEscrow(67890)
  - Updates invoice status: 'settled'

WebSocket notifications:
  - Requester: "Invoice settled, payment received"
  - Recipient: "Payment sent successfully"

Result: P2P payment complete ✓
```

---

## Payment Signatures

### Signature Structure

Payment signatures provide cryptographic proof of payment authorization:

```go
type PaymentSignature struct {
    PaymentHash      string    // SHA-256 hash of payment data
    WalletAddress    string    // Payer's blockchain address
    Signature        []byte    // Blockchain signature
    Timestamp        int64     // Unix timestamp
    Nonce            string    // Unique identifier (prevents replay)
    SignatureType    string    // "EVM" or "Solana"
}
```

### Creating Signatures (EVM)

**Process:**
1. Create payment data structure
2. Hash payment data (Keccak256)
3. Sign hash with private key (ECDSA)
4. Encode signature

**Code example:**
```go
// Create payment data
paymentData := &PaymentData{
    Amount:      0.05,
    Currency:    "ETH",
    Network:     "eip155:84532",
    Recipient:   "provider_peer_id",
    Description: "Job execution payment",
}

// Sign payment
signature, err := walletManager.SignPayment(
    walletID,
    paymentData,
    passphrase,
)
```

**Signature components:**
```
Payment Hash:
  Input: {amount, currency, network, recipient, timestamp, nonce}
  Hash: Keccak256(JSON-encoded input)

Signature:
  Input: Payment hash
  Method: ECDSA with secp256k1 curve
  Output: 65-byte signature (r, s, v)

Wallet Address:
  Derived from public key
  Format: 0x-prefixed hex string
```

### Creating Signatures (Solana)

**Process:**
1. Create payment data structure
2. Hash payment data (SHA-256)
3. Sign hash with Ed25519
4. Encode signature

**Signature components:**
```
Payment Hash:
  Input: {amount, currency, network, recipient, timestamp, nonce}
  Hash: SHA256(JSON-encoded input)

Signature:
  Input: Payment hash
  Method: Ed25519
  Output: 64-byte signature

Wallet Address:
  Base58-encoded public key
  Example: 8YvKSRvGkKhZFz9KJ2xQWQvN5hFz9K...
```

### Signature Verification

**Verification process:**

```go
func VerifyPaymentSignature(sig *PaymentSignature, paymentData *PaymentData) bool {
    // 1. Recreate payment hash
    expectedHash := HashPaymentData(paymentData)
    if expectedHash != sig.PaymentHash {
        return false
    }

    // 2. Verify timestamp (prevent replay)
    if time.Now().Unix() - sig.Timestamp > 300 { // 5 min window
        return false
    }

    // 3. Verify nonce uniqueness
    if NonceUsed(sig.Nonce) {
        return false
    }

    // 4. Recover public key from signature
    pubKey := RecoverPublicKey(sig.PaymentHash, sig.Signature)

    // 5. Derive address from public key
    address := PublicKeyToAddress(pubKey)

    // 6. Compare with claimed address
    return address == sig.WalletAddress
}
```

---

## Escrow Management

### Escrow Lifecycle

```
┌────────────────────────────────────────────────────┐
│               Escrow State Machine                 │
└────────────────────────────────────────────────────┘

  CREATE
    │
    ▼
┌─────────┐
│ pending │──────┐
└─────────┘      │
    │            │ TIMEOUT
    │ SETTLE     │ (future)
    ▼            ▼
┌─────────┐  ┌──────────┐
│ settled │  │ refunded │
└─────────┘  └──────────┘
```

### Database Schema

```sql
CREATE TABLE job_payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_execution_id INTEGER,           -- 0 for P2P payments
    payment_signature TEXT NOT NULL,    -- JSON-encoded signature
    amount REAL NOT NULL,
    currency TEXT NOT NULL,
    network TEXT NOT NULL,
    escrow_status TEXT NOT NULL,        -- 'pending', 'settled', 'refunded'
    created_at INTEGER NOT NULL,
    settled_at INTEGER,
    refunded_at INTEGER,
    metadata TEXT,
    FOREIGN KEY (job_execution_id) REFERENCES job_executions(id)
);
```

### Creating Escrow

**Function:** `EscrowManager.CreateEscrow(paymentSig, jobExecutionID)`

```go
func (em *EscrowManager) CreateEscrow(
    paymentSig *PaymentSignature,
    jobExecutionID int64,
) (int64, error) {
    // 1. Verify payment signature
    if !VerifyPaymentSignature(paymentSig) {
        return 0, ErrInvalidSignature
    }

    // 2. Create payment record
    payment := &Payment{
        JobExecutionID:   jobExecutionID,  // 0 for P2P
        PaymentSignature: paymentSig,
        Amount:           paymentSig.Amount,
        Currency:         paymentSig.Currency,
        Network:          paymentSig.Network,
        EscrowStatus:     "pending",
        CreatedAt:        time.Now(),
    }

    // 3. Store in database
    paymentID, err := em.db.CreatePayment(payment)
    if err != nil {
        return 0, err
    }

    // 4. Log escrow creation
    em.logger.Info(fmt.Sprintf(
        "Escrow created: payment_id=%d, amount=%.6f %s",
        paymentID, payment.Amount, payment.Currency,
    ))

    return paymentID, nil
}
```

### Settling Escrow

**Function:** `EscrowManager.SettleEscrow(paymentID)`

```go
func (em *EscrowManager) SettleEscrow(paymentID int64) error {
    // 1. Get payment record
    payment, err := em.db.GetPayment(paymentID)
    if err != nil {
        return err
    }

    // 2. Verify payment is pending
    if payment.EscrowStatus != "pending" {
        return ErrInvalidEscrowStatus
    }

    // 3. For job payments, verify job completion
    if payment.JobExecutionID != 0 {
        jobExec, err := em.db.GetJobExecution(payment.JobExecutionID)
        if err != nil {
            return err
        }
        if jobExec.Status != "completed" {
            return ErrJobNotCompleted
        }
    }

    // 4. Update escrow status
    payment.EscrowStatus = "settled"
    payment.SettledAt = time.Now()

    if err := em.db.UpdatePayment(payment); err != nil {
        return err
    }

    // 5. Trigger blockchain settlement (future)
    // em.blockchain.ReleaseEscrow(payment)

    // 6. Log settlement
    em.logger.Info(fmt.Sprintf(
        "Escrow settled: payment_id=%d, amount=%.6f %s",
        paymentID, payment.Amount, payment.Currency,
    ))

    return nil
}
```

### Refunding Escrow (Future)

**Function:** `EscrowManager.RefundEscrow(paymentID, reason)`

```go
func (em *EscrowManager) RefundEscrow(
    paymentID int64,
    reason string,
) error {
    // 1. Get payment record
    payment, err := em.db.GetPayment(paymentID)
    if err != nil {
        return err
    }

    // 2. Verify payment is pending
    if payment.EscrowStatus != "pending" {
        return ErrInvalidEscrowStatus
    }

    // 3. Verify refund conditions
    if payment.JobExecutionID != 0 {
        jobExec, err := em.db.GetJobExecution(payment.JobExecutionID)
        if err != nil {
            return err
        }
        // Only refund if job failed or timed out
        if jobExec.Status != "failed" && jobExec.Status != "timeout" {
            return ErrInvalidRefundCondition
        }
    }

    // 4. Update escrow status
    payment.EscrowStatus = "refunded"
    payment.RefundedAt = time.Now()
    payment.Metadata["refund_reason"] = reason

    if err := em.db.UpdatePayment(payment); err != nil {
        return err
    }

    // 5. Trigger blockchain refund
    // em.blockchain.RefundEscrow(payment)

    // 6. Log refund
    em.logger.Info(fmt.Sprintf(
        "Escrow refunded: payment_id=%d, reason=%s",
        paymentID, reason,
    ))

    return nil
}
```

---

## Settlement Process

### Automatic Settlement

**Triggers:**

1. **Job Execution Completed**
   ```go
   // After job completes successfully
   if execution.Status == "completed" {
       paymentID := execution.PaymentID
       escrowManager.SettleEscrow(paymentID)
   }
   ```

2. **P2P Invoice Accepted**
   ```go
   // After invoice acceptance
   invoiceManager.AcceptInvoice(invoiceID, walletID, passphrase)
   // Automatically triggers:
   go invoiceManager.SettleInvoice(invoiceID)
   ```

### Settlement Verification

**Checklist before settlement:**

- [ ] Payment signature verified
- [ ] Escrow status is 'pending'
- [ ] Job execution completed (if job payment)
- [ ] No fraud detected
- [ ] Blockchain account has funds
- [ ] Settlement not already processed

### Blockchain Integration (Future)

**Current state:**
- Payments recorded in database
- Settlement marks payment as complete
- **No on-chain transactions yet**

**Future implementation:**
```go
// Blockchain settlement
func (bm *BlockchainManager) ReleaseEscrow(payment *Payment) error {
    switch payment.Network {
    case "eip155:8453", "eip155:84532":
        return bm.ReleaseEVMEscrow(payment)
    case "solana:mainnet-beta", "solana:devnet":
        return bm.ReleaseSolanaEscrow(payment)
    }
}

func (bm *BlockchainManager) ReleaseEVMEscrow(payment *Payment) error {
    // 1. Connect to RPC
    client, _ := ethclient.Dial(bm.getRPCEndpoint(payment.Network))

    // 2. Load escrow contract
    contract := bm.loadEscrowContract(payment.Network)

    // 3. Create release transaction
    tx := contract.ReleaseEscrow(
        paymentID,
        recipientAddress,
        amount,
    )

    // 4. Sign and send
    signedTx, _ := wallet.SignTransaction(tx)
    txHash, _ := client.SendTransaction(signedTx)

    // 5. Wait for confirmation
    receipt, _ := client.WaitForReceipt(txHash)

    return nil
}
```

---

## Supported Blockchains

### Ethereum Virtual Machine (EVM)

**Networks:**
- Base Mainnet (Chain ID: 8453)
- Base Sepolia (Chain ID: 84532)
- Ethereum Mainnet (Chain ID: 1)
- Polygon (Chain ID: 137)
- Arbitrum (Chain ID: 42161)
- Optimism (Chain ID: 10)

**Features:**
- ECDSA signatures (secp256k1)
- Keccak256 hashing
- ERC-20 token support (future)
- Smart contract escrow (future)

**RPC Methods used:**
- `eth_getBalance` - Query wallet balance
- `eth_sendTransaction` - Send payments (future)
- `eth_call` - Query contract state (future)

### Solana

**Networks:**
- Solana Mainnet Beta
- Solana Devnet
- Solana Testnet

**Features:**
- Ed25519 signatures
- SHA-256 hashing
- SPL token support (future)
- Program escrow (future)

**RPC Methods used:**
- `getBalance` - Query wallet balance
- `sendTransaction` - Send payments (future)
- `getAccountInfo` - Query account state (future)

### Adding New Chains

**To support a new blockchain:**

1. **Implement signature creation:**
   ```go
   func (wm *WalletManager) SignPaymentNewChain(
       walletID string,
       paymentData *PaymentData,
       passphrase string,
   ) (*PaymentSignature, error)
   ```

2. **Implement balance query:**
   ```go
   func (wm *WalletManager) GetBalanceNewChain(
       walletID string,
       wallet *EncryptedWallet,
   ) (*WalletBalance, error)
   ```

3. **Add RPC endpoint configuration:**
   ```bash
   remote-network config set rpc_endpoint_newchain_mainnet https://rpc.newchain.com
   ```

4. **Update network validation:**
   ```go
   func validateNetwork(network string) bool {
       supportedNetworks := []string{
           "eip155:8453",
           "solana:mainnet-beta",
           "newchain:mainnet",  // Add new chain
       }
       return contains(supportedNetworks, network)
   }
   ```

---

## Database Schema

### job_payments Table

```sql
CREATE TABLE job_payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_execution_id INTEGER NOT NULL,
    payment_signature TEXT NOT NULL,
    amount REAL NOT NULL,
    currency TEXT NOT NULL,
    network TEXT NOT NULL,
    escrow_status TEXT NOT NULL DEFAULT 'pending',
    escrow_address TEXT,
    transaction_hash TEXT,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    settled_at INTEGER,
    refunded_at INTEGER,
    metadata TEXT,
    FOREIGN KEY (job_execution_id) REFERENCES job_executions(id) ON DELETE CASCADE
);

CREATE INDEX idx_payments_job_execution ON job_payments(job_execution_id);
CREATE INDEX idx_payments_status ON job_payments(escrow_status);
CREATE INDEX idx_payments_network ON job_payments(network);
```

### payment_invoices Table

```sql
CREATE TABLE payment_invoices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    invoice_id TEXT NOT NULL UNIQUE,
    from_peer_id TEXT NOT NULL,
    to_peer_id TEXT NOT NULL,
    amount REAL NOT NULL,
    currency TEXT NOT NULL,
    network TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    payment_signature TEXT,
    transaction_id TEXT,
    payment_id INTEGER,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    expires_at INTEGER,
    accepted_at INTEGER,
    rejected_at INTEGER,
    settled_at INTEGER,
    metadata TEXT,
    FOREIGN KEY (payment_id) REFERENCES job_payments(id) ON DELETE SET NULL
);

CREATE INDEX idx_invoices_from_peer ON payment_invoices(from_peer_id);
CREATE INDEX idx_invoices_to_peer ON payment_invoices(to_peer_id);
CREATE INDEX idx_invoices_status ON payment_invoices(status);
```

### Relationships

```
job_executions (1) ──▶ (1) job_payments
                            │
                            │
payment_invoices (1) ──▶ (0..1) job_payments
                        (payment_id)
```

**Key:**
- Job payments: `job_execution_id` references actual job
- P2P payments: `job_execution_id = 0`
- Invoices link via `payment_id` to `job_payments`

---

## Configuration

### Payment Settings

```bash
# Default wallet for payments
remote-network config set x402_default_wallet_id wallet_abc123

# Invoice expiration (hours)
remote-network config set invoice_default_expiration_hours 24

# Invoice cleanup interval (hours)
remote-network config set invoice_cleanup_interval_hours 6

# Wallet balance refresh interval (seconds)
remote-network config set wallet_balance_refresh_interval 60
```

### RPC Endpoints

```bash
# Base Mainnet
remote-network config set rpc_endpoint_eip155_8453 https://mainnet.base.org

# Base Sepolia
remote-network config set rpc_endpoint_eip155_84532 https://sepolia.base.org

# Solana Mainnet
remote-network config set rpc_endpoint_solana_mainnet-beta https://api.mainnet-beta.solana.com

# Solana Devnet
remote-network config set rpc_endpoint_solana_devnet https://api.devnet.solana.com
```

### Payment Verification

```bash
# Signature validity window (seconds)
remote-network config set payment_signature_validity 300  # 5 minutes

# Minimum payment amount (prevents dust attacks)
remote-network config set min_payment_amount_eth 0.0001
remote-network config set min_payment_amount_sol 0.001
```

---

## Security Model

### Threat Model

**Protected against:**
- ✅ Payment replay attacks (nonce + timestamp)
- ✅ Signature forgery (cryptographic verification)
- ✅ Double-spending (escrow locks funds)
- ✅ Man-in-the-middle (QUIC encryption)
- ✅ Unauthorized settlements (status checks)

**Future protections:**
- 🔄 On-chain escrow (smart contracts)
- 🔄 Multi-sig settlements
- 🔄 Dispute resolution
- 🔄 Payment channels (Lightning, state channels)

### Attack Scenarios

**1. Replay Attack**
```
Attacker intercepts payment signature and tries to reuse it.

Protection:
  - Nonce checked for uniqueness
  - Timestamp validated (5-minute window)
  - Database tracks used nonces
```

**2. Insufficient Funds**
```
Payer creates payment signature but wallet has no funds.

Current: Payment accepted (off-chain)
Future: On-chain verification before acceptance
```

**3. Settlement Race Condition**
```
Multiple settlement attempts for same payment.

Protection:
  - Database transaction locks
  - Status check before settlement
  - Idempotent settlement operation
```

**4. Malicious Provider**
```
Provider claims job completed but didn't execute.

Current: Trust-based (peer reputation)
Future: Verifiable compute (see VERIFIABLE_COMPUTE_DESIGN.md)
```

---

## Error Handling

### Common Errors

**ErrInvalidSignature:**
```
Error: Payment signature verification failed
Cause: Invalid signature, wrong wallet, or corrupted data
Solution: Recreate payment signature with correct wallet
```

**ErrInsufficientBalance:**
```
Error: Wallet balance too low for payment
Cause: Wallet doesn't have enough funds
Solution: Fund wallet on blockchain before retrying
```

**ErrNetworkMismatch:**
```
Error: Wallet network doesn't match payment network
Cause: Using ETH wallet for SOL payment
Solution: Use wallet on correct network
```

**ErrInvalidEscrowStatus:**
```
Error: Cannot settle escrow in current status
Cause: Escrow already settled or refunded
Solution: Check escrow status before operation
```

**ErrJobNotCompleted:**
```
Error: Cannot settle payment for incomplete job
Cause: Job execution status is not 'completed'
Solution: Wait for job completion or handle failure
```

### Error Response Format

```json
{
  "error": "invalid_payment_signature",
  "message": "Payment signature verification failed",
  "details": {
    "wallet_address": "0x742d35...",
    "expected_hash": "0xabc123...",
    "received_hash": "0xdef456..."
  }
}
```

---

## Future Enhancements

### On-Chain Escrow

**Smart contract escrow (EVM):**
```solidity
contract X402Escrow {
    mapping(bytes32 => Escrow) public escrows;

    struct Escrow {
        address payer;
        address payee;
        uint256 amount;
        EscrowStatus status;
        uint256 createdAt;
    }

    function createEscrow(
        bytes32 paymentId,
        address payee,
        uint256 amount
    ) external payable {
        require(msg.value == amount, "Incorrect amount");
        escrows[paymentId] = Escrow({
            payer: msg.sender,
            payee: payee,
            amount: amount,
            status: EscrowStatus.Pending,
            createdAt: block.timestamp
        });
    }

    function settleEscrow(bytes32 paymentId) external {
        Escrow storage escrow = escrows[paymentId];
        require(escrow.status == EscrowStatus.Pending);

        escrow.status = EscrowStatus.Settled;
        payable(escrow.payee).transfer(escrow.amount);
    }

    function refundEscrow(bytes32 paymentId) external {
        Escrow storage escrow = escrows[paymentId];
        require(escrow.status == EscrowStatus.Pending);
        require(block.timestamp > escrow.createdAt + 7 days);

        escrow.status = EscrowStatus.Refunded;
        payable(escrow.payer).transfer(escrow.amount);
    }
}
```

### Payment Channels

**Lightning-style payment channels:**
- Reduce on-chain transactions
- Enable micropayments
- Lower transaction fees
- Faster settlement

### Multi-Currency Support

**Support for multiple tokens:**
- ERC-20 tokens (USDC, DAI, etc.)
- SPL tokens (Solana)
- Cross-chain swaps
- Stablecoin payments

---

## Related Documentation

- [WALLET_MANAGEMENT.md](./WALLET_MANAGEMENT.md) - Wallet setup and management
- [P2P_PAYMENTS.md](./P2P_PAYMENTS.md) - Peer-to-peer invoice payments
- [VERIFIABLE_COMPUTE_DESIGN.md](./VERIFIABLE_COMPUTE_DESIGN.md) - Verifiable job execution
- [API_REFERENCE.md](./API_REFERENCE.md) - Complete API documentation

---

**Last Updated**: 2025-12-28
