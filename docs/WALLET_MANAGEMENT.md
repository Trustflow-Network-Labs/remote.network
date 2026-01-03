# Wallet Management Guide

## Overview

Remote Network supports **cryptocurrency wallets** for managing payments within the x402 payment protocol. Wallets enable you to:
- Pay for job executions on remote peers
- Pay for relay services (NAT traversal)
- Receive payments for services you provide
- Send and receive peer-to-peer payment invoices

This guide covers wallet management through both the **CLI** and **Web UI**.

---

## Table of Contents

- [Supported Networks](#supported-networks)
- [Wallet Security](#wallet-security)
- [CLI Wallet Management](#cli-wallet-management)
- [Web UI Wallet Management](#web-ui-wallet-management)
- [Balance Queries](#balance-queries)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

---

## Supported Networks

Remote Network supports multiple blockchain networks using **CAIP-2** network identifiers:

### Ethereum Virtual Machine (EVM) Chains

| Network | CAIP-2 Identifier | Currency | Type |
|---------|-------------------|----------|------|
| Base Mainnet | `eip155:8453` | ETH | Production |
| Base Sepolia | `eip155:84532` | ETH | Testnet |

### Solana Chains

| Network | CAIP-2 Identifier | Currency | Type |
|---------|-------------------|----------|------|
| Solana Mainnet | `solana:mainnet-beta` | SOL | Production |
| Solana Devnet | `solana:devnet` | SOL | Testnet |

**Note**: Additional EVM chains can be supported by using their chain ID in the format `eip155:<chain-id>`.

---

## Wallet Security

### Encryption

All wallets are encrypted using **AES-256-GCM** encryption with the following security features:

- **Passphrase Protection**: Each wallet requires a passphrase for decryption
- **Secure Storage**: Encrypted wallet files are stored locally
- **No Plaintext Keys**: Private keys are never stored unencrypted
- **Salt + Nonce**: Each wallet uses unique salt and nonce values

### Wallet File Location

Encrypted wallet files are stored in:

- **macOS**: `~/Library/Application Support/remote-network/wallets/`
- **Linux**: `~/.local/share/remote-network/wallets/`
- **Windows**: `%APPDATA%\remote-network\wallets\`

### File Naming

Wallets are stored as `{wallet_id}.wallet` where `wallet_id` is a unique identifier.

---

## CLI Wallet Management

### List Wallets

Display all wallets stored on your node:

```bash
remote-network wallet list
```

**Output:**
```
Payment Wallets
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Wallet ID:  wallet_abc123def456 (default)
Network:    eip155:84532
Address:    0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb

Wallet ID:  wallet_789xyz012
Network:    solana:devnet
Address:    8YvKSRvGkKhZFz9KJ2xQWQvN5hFz9KJ2xQWQvN5hFz9K
```

### Create New Wallet

Create a new cryptocurrency wallet:

```bash
remote-network wallet create --network eip155:84532
```

**Interactive prompts:**
```
Creating new wallet...
Network: eip155:84532

Enter passphrase to encrypt wallet: ********
Confirm passphrase: ********

✓ Wallet created successfully
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Wallet ID:  wallet_abc123def456
Network:    eip155:84532
Address:    0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb

To set this as your default wallet:
  remote-network config set x402_default_wallet_id wallet_abc123def456

Remember your passphrase - it cannot be recovered if lost!
```

**Supported networks:**
```bash
# Base Sepolia (testnet) - default
remote-network wallet create --network eip155:84532

# Base Mainnet
remote-network wallet create --network eip155:8453

# Solana Mainnet
remote-network wallet create --network solana:mainnet-beta

# Solana Devnet
remote-network wallet create --network solana:devnet
```

### Import Existing Wallet

Import a wallet using an existing private key:

```bash
remote-network wallet import --private-key 0x1234... --network eip155:84532
```

**Security warning displayed:**
```
⚠️  SECURITY WARNING ⚠️
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
You are about to import a wallet using a private key.

Make sure you are on a TRUSTED system and the private key is from
a wallet you control. Never import keys from untrusted sources.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Do you want to continue? (yes/no): yes

Enter passphrase to encrypt wallet: ********
Confirm passphrase: ********

✓ Wallet imported successfully
```

**Skip confirmation (use with caution):**
```bash
remote-network wallet import --private-key 0x1234... --network eip155:84532 --force
```

### View Wallet Details

Get detailed information about a specific wallet:

```bash
remote-network wallet info --wallet-id wallet_abc123def456
```

**Output:**
```
Enter wallet passphrase: ********

Wallet Information
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Wallet ID:       wallet_abc123def456
Network:         eip155:84532
Address:         0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
Default Wallet:  true

Network Name:    Base Sepolia (Testnet)

Address (short): 0x742d35...5f0bEb
```

### Query Wallet Balance

Query real-time balance from the blockchain:

```bash
remote-network wallet balance --wallet-id wallet_abc123def456
```

**Output:**
```
Querying balance for wallet wallet_abc123def456...

✓ Balance retrieved
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Wallet ID:  wallet_abc123def456
Network:    eip155:84532
Address:    0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
Balance:    1.250000 ETH
```

**How it works:**
- Connects to blockchain RPC endpoint
- Queries native token balance
- For EVM chains: Uses `eth_getBalance`
- For Solana: Uses `getBalance` RPC method

### Export Private Key

Export a wallet's private key for backup or transfer:

```bash
remote-network wallet export --wallet-id wallet_abc123def456
```

**Security warning:**
```
⚠️  SECURITY WARNING ⚠️
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
You are about to export your wallet's private key.

This key grants FULL CONTROL over your wallet and funds.
Anyone with this key can:
  • Transfer all funds from the wallet
  • Sign transactions on your behalf
  • Impersonate your identity

NEVER share this key with anyone you don't trust completely.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Do you want to continue? (yes/no): yes

Enter wallet passphrase: ********

✓ Private key exported
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Private Key (Hexadecimal):
0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef

Keep this key secure and delete it when no longer needed.
```

**Skip confirmation:**
```bash
remote-network wallet export --wallet-id wallet_abc123def456 --force
```

### Delete Wallet

Permanently delete a wallet from local storage:

```bash
remote-network wallet delete --wallet-id wallet_abc123def456
```

**Warning displayed:**
```
⚠️  WARNING ⚠️
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
You are about to DELETE a wallet.

This action is IRREVERSIBLE. The wallet file will be permanently
removed from your system. Make sure you have:
  • Exported and backed up the private key
  • Transferred any funds to another wallet

Wallet ID: wallet_abc123def456
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Do you want to continue? (yes/no): yes

Enter wallet passphrase to confirm: ********

✓ Wallet deleted successfully
```

**Skip confirmation:**
```bash
remote-network wallet delete --wallet-id wallet_abc123def456 --force
```

### Set Default Wallet

Configure a wallet as the default for automatic payments:

```bash
remote-network config set x402_default_wallet_id wallet_abc123def456
```

The default wallet is automatically used for:
- Job execution payments
- Relay service payments
- Automated payment flows

---

## Web UI Wallet Management

### Accessing the Wallets Page

1. Navigate to the web UI: `http://localhost:8765`
2. Log in with your credentials
3. Click **"Wallets"** in the sidebar navigation

### Wallet Grid View

The Wallets page displays all wallets in a card grid layout:

**Each wallet card shows:**
- Network badge (color-coded)
- Wallet address (with copy button)
- Balance (if loaded)
- Action menu (⋮)

**Network color coding:**
- **Blue**: Base networks (EVM)
- **Green**: Solana networks
- **Gray**: Unknown networks

### Creating a Wallet (Web UI)

1. Click **"Create Wallet"** button
2. Fill in the form:
   - **Network**: Select from dropdown
     - Base Mainnet (eip155:8453)
     - Base Sepolia (eip155:84532)
     - Solana Mainnet (solana:mainnet-beta)
     - Solana Devnet (solana:devnet)
   - **Passphrase**: Enter encryption passphrase
   - **Confirm Passphrase**: Re-enter passphrase
3. Click **"Create"**

**Success notification:**
```
✓ Wallet created successfully
  Wallet ID: wallet_abc123def456
  Address: 0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
```

### Importing a Wallet (Web UI)

1. Click **"Import Wallet"** button
2. Fill in the form:
   - **Private Key**: Paste hexadecimal private key (with or without 0x prefix)
   - **Network**: Select blockchain network
   - **Passphrase**: Enter encryption passphrase
   - **Confirm Passphrase**: Re-enter passphrase
3. Review security warning
4. Click **"Import"**

**Security warning:**
```
⚠️ SECURITY WARNING
Importing a private key gives full control over the wallet.
Only import keys from wallets you own and trust the source.
```

### Loading Wallet Balance (Web UI)

**Method 1: Individual wallet**
1. Click on a wallet card
2. Click **"Load Balance"** button
3. Balance appears in the card (with loading spinner)

**Method 2: All wallets**
1. Click **"Refresh"** button in header
2. Select **"Include Balances"**
3. All wallet balances load simultaneously

**Balance display:**
```
Balance: 1.250000 ETH
Last updated: 2 minutes ago
```

### Exporting Private Key (Web UI)

1. Click the **action menu (⋮)** on a wallet card
2. Select **"Export Private Key"**
3. Review security warning
4. Enter wallet passphrase
5. Click **"Export"**
6. Private key is displayed (with copy button)

**Security features:**
- Password-protected dialog
- Copy-to-clipboard button
- Auto-hide after 30 seconds
- Clear security warnings

### Deleting a Wallet (Web UI)

1. Click the **action menu (⋮)** on a wallet card
2. Select **"Delete Wallet"**
3. Review deletion warning
4. Enter wallet passphrase to confirm
5. Click **"Delete"**

**Warning dialog:**
```
⚠️ Delete Wallet

This action is IRREVERSIBLE. Make sure you have:
• Exported and backed up the private key
• Transferred any funds to another wallet

Wallet ID: wallet_abc123def456
Address: 0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
```

### Real-Time Updates

The Web UI receives real-time wallet updates via WebSocket:

**WebSocket events:**
- `wallet.created` - New wallet created
- `wallet.deleted` - Wallet removed
- `wallet.balance.update` - Balance changed
- `wallets.updated` - Wallet list refreshed

**Automatic refresh triggers:**
- Wallet creation/deletion on any connected client
- Balance updates from blockchain polling
- External wallet operations via CLI

---

## Balance Queries

### RPC Endpoints

Remote Network queries blockchain balances using RPC endpoints:

**Default RPC endpoints:**

| Network | RPC URL |
|---------|---------|
| Base Mainnet | `https://mainnet.base.org` |
| Base Sepolia | `https://sepolia.base.org` |
| Solana Mainnet | `https://api.mainnet-beta.solana.com` |
| Solana Devnet | `https://api.devnet.solana.com` |

### Custom RPC Endpoints

Override default endpoints in your config:

```bash
# Base Mainnet custom RPC
remote-network config set rpc_endpoint_eip155_8453 https://your-rpc-url.com

# Solana Mainnet custom RPC
remote-network config set rpc_endpoint_solana_mainnet-beta https://your-solana-rpc.com
```

**Config format:**
```
rpc_endpoint_{network_with_underscores}
```

**Examples:**
```
rpc_endpoint_eip155_8453=https://mainnet.base.org
rpc_endpoint_eip155_84532=https://sepolia.base.org
rpc_endpoint_solana_mainnet-beta=https://api.mainnet-beta.solana.com
```

### Balance Refresh Interval

Configure automatic balance refresh (Web UI):

```bash
remote-network config set wallet_balance_refresh_interval 60
```

**Default**: 60 seconds

---

## Best Practices

### Security

✅ **DO:**
- Use strong, unique passphrases (16+ characters)
- Store passphrases in a password manager
- Export and backup private keys securely
- Transfer funds before deleting wallets
- Use testnet wallets for development
- Verify addresses before sending funds
- Keep wallet software updated

❌ **DON'T:**
- Share private keys with anyone
- Store passphrases in plaintext
- Commit wallet files to git repositories
- Use weak or reused passphrases
- Delete wallets with funds inside
- Import untrusted private keys
- Store private keys in browser localStorage

### Wallet Organization

**Use multiple wallets for different purposes:**

1. **Development Wallet**: Testnet (Base Sepolia, Solana Devnet)
   - For testing payment flows
   - Low-value funds only

2. **Production Wallet**: Mainnet (Base, Solana)
   - For real payments
   - Regularly monitored balance

3. **Service Wallet**: Dedicated per service type
   - Job execution payments
   - Relay service payments
   - P2P invoice payments

### Passphrase Management

**Good passphrase examples:**
```
MyRemoteN3tw0rk!Wallet2024
correct-horse-battery-staple-87!
P@ssw0rd_F0r_BaseW@llet
```

**Storage options:**
1. **Password Manager**: 1Password, Bitwarden, LastPass
2. **Hardware Security Key**: YubiKey, Ledger
3. **Encrypted File**: GPG-encrypted backup
4. **Paper Backup**: In secure physical location

### Backup Strategy

**Backup checklist:**
- [ ] Export all private keys
- [ ] Store passphrases separately
- [ ] Test wallet recovery process
- [ ] Document wallet IDs and purposes
- [ ] Keep multiple backup locations
- [ ] Encrypt backups with GPG/age
- [ ] Store backups offline (cold storage)

---

## Troubleshooting

### "Invalid passphrase" Error

**Problem**: Wallet passphrase is rejected

**Solutions:**
1. Verify passphrase spelling and case
2. Check for extra spaces or characters
3. Try re-entering carefully
4. If forgotten, wallet cannot be recovered (create new wallet)

### Balance Query Failures

**Problem**: Balance query returns error

**Possible causes:**
1. **RPC endpoint down**: Check endpoint status
2. **Network connectivity**: Verify internet connection
3. **Rate limiting**: Too many requests to public RPC
4. **Invalid wallet**: Wallet may be corrupted

**Solutions:**
```bash
# Test RPC endpoint manually
curl https://mainnet.base.org -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Configure custom RPC endpoint
remote-network config set rpc_endpoint_eip155_8453 https://your-rpc.com

# Check wallet file exists
ls -la ~/Library/Application\ Support/remote-network/wallets/
```

### Wallet File Corruption

**Problem**: Wallet file cannot be decrypted

**Symptoms:**
- "Failed to decrypt wallet" error
- Garbled wallet data
- Unexpected encryption errors

**Solutions:**
1. Restore from backup wallet file
2. Re-import using private key (if backed up)
3. Create new wallet (if no backup exists)

### Default Wallet Not Working

**Problem**: Payments fail with "no default wallet" error

**Check configuration:**
```bash
# View current default wallet
remote-network config get x402_default_wallet_id

# Set default wallet
remote-network config set x402_default_wallet_id wallet_abc123def456

# Verify wallet exists
remote-network wallet list
```

### Network Mismatch Errors

**Problem**: "Network mismatch" error when accepting invoice

**Cause**: Invoice requires different network than wallet provides

**Solution:**
1. Check invoice network requirement
2. Create/use wallet on matching network
3. Transfer funds to correct network wallet

---

## API Reference

### HTTP Endpoints

```
GET    /api/wallets                    List all wallets
POST   /api/wallets                    Create new wallet
POST   /api/wallets/import             Import wallet from private key
GET    /api/wallets/{id}/balance       Query wallet balance
POST   /api/wallets/{id}/export        Export private key
DELETE /api/wallets/{id}               Delete wallet
```

### WebSocket Events

```
wallet.created           New wallet created
wallet.deleted           Wallet removed
wallet.balance.update    Balance updated
wallets.updated          Wallet list changed
```

See [API_REFERENCE.md](./API_REFERENCE.md) for detailed API documentation.

---

## Support

For wallet-related issues:

1. Check [Troubleshooting](#troubleshooting) section
2. Review logs: `~/.cache/remote-network/logs/` (Linux) or `~/Library/Logs/remote-network/` (macOS)
3. Report issues: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated**: 2025-12-28
