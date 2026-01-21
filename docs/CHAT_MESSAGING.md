# Peer-to-Peer Chat Messaging

## Overview

Remote Network provides **end-to-end encrypted peer-to-peer chat** for secure communication between nodes. The system supports both **1-on-1 conversations** and **group chats**, with messages encrypted using industry-standard cryptographic protocols.

**Key Features:**
- End-to-end encryption using Double Ratchet (1-on-1) and Sender Keys (groups)
- Forward secrecy for all messages
- Offline message delivery via relay servers
- Read receipts and delivery confirmations
- Real-time updates via WebSocket
- Message persistence with configurable TTL

---

## Table of Contents

- [Supported Chat Types](#supported-chat-types)
- [Encryption Architecture](#encryption-architecture)
- [1-on-1 Conversations](#1-on-1-conversations)
- [Group Chats](#group-chats)
- [Message Flow](#message-flow)
- [Message Status Lifecycle](#message-status-lifecycle)
- [WebSocket Real-Time Updates](#websocket-real-time-updates)
- [API Reference](#api-reference)
- [Data Structures](#data-structures)
- [Security Considerations](#security-considerations)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)

---

## Supported Chat Types

### 1-on-1 Conversations

Direct messaging between two peers with:
- **Double Ratchet encryption** (Signal Protocol)
- Automatic key exchange on first message
- Per-message forward secrecy
- Out-of-order message handling

### Group Chats

Multi-party messaging with:
- **Sender Keys encryption** (Signal Protocol)
- Creator becomes admin
- Minimum 3 participants (creator + 2 members)
- Key distribution to new members
- Member invite/leave functionality

---

## Encryption Architecture

### 1-on-1: Double Ratchet Algorithm

The Double Ratchet algorithm provides forward secrecy and break-in recovery for 1-on-1 chats:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Double Ratchet Protocol                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐                      ┌──────────────┐         │
│  │    Alice     │                      │     Bob      │         │
│  └──────┬───────┘                      └──────┬───────┘         │
│         │                                      │                 │
│         │ 1. Generate Ephemeral DH Key Pair    │                 │
│         │                                      │                 │
│         │ 2. Key Exchange Request ────────────▶│                 │
│         │    (ephemeral_pub_key + signature)   │                 │
│         │                                      │                 │
│         │                    3. Generate DH Key│                 │
│         │                       Compute Shared │                 │
│         │                       Secret         │                 │
│         │                                      │                 │
│         │◀─────────── Key Exchange ACK ────────│                 │
│         │    (ephemeral_pub_key + signature)   │                 │
│         │                                      │                 │
│         │ 4. Compute Shared Secret             │                 │
│         │    Initialize Ratchet                │                 │
│         │                                      │                 │
│         │ 5. Encrypted Message ───────────────▶│                 │
│         │    (ciphertext + nonce + DH_pub_key) │                 │
│         │                                      │                 │
│         │                    6. DH Ratchet Step│                 │
│         │                       Decrypt Message│                 │
│         │                                      │                 │
└─────────────────────────────────────────────────────────────────┘
```

**Components:**

| Component | Purpose |
|-----------|---------|
| Root Key | KDF chain root, updated with each DH ratchet |
| Send Chain Key | Derives message keys for outgoing messages |
| Recv Chain Key | Derives message keys for incoming messages |
| DH Ratchet Key Pair | X25519 key pair, rotated periodically |
| Skipped Keys | Cache for out-of-order message decryption |

### Group: Sender Keys Protocol

Sender Keys provide efficient group encryption where each member maintains their own sending key:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Sender Keys Protocol                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Group Creator (Admin)                                           │
│  ┌─────────────┐                                                 │
│  │  Generate   │                                                 │
│  │ Sender Key  │                                                 │
│  └──────┬──────┘                                                 │
│         │                                                        │
│         ├──────────── Distribute Key ──────────▶ Member A       │
│         │                                                        │
│         ├──────────── Distribute Key ──────────▶ Member B       │
│         │                                                        │
│         └──────────── Distribute Key ──────────▶ Member C       │
│                                                                  │
│  Each member:                                                    │
│  - Generates their own sender key                                │
│  - Distributes to all other members                              │
│  - Stores received keys for decryption                           │
│                                                                  │
│  Message Flow:                                                   │
│  ┌──────────┐                                                    │
│  │ Member A │ ──── Encrypt with A's sender key ────▶ All Members│
│  └──────────┘      (recipients use stored A's key to decrypt)   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Sender Key Structure:**

| Field | Description |
|-------|-------------|
| Chain Key | 32-byte symmetric key for deriving message keys |
| Message Number | Counter for message ordering |
| Signing Key | Ed25519 key for message authentication |

---

## 1-on-1 Conversations

### Creating a Conversation

Conversations are created automatically when sending the first message to a peer, or explicitly via API:

**Web UI:**
1. Navigate to **Chat** page
2. Click **"New Conversation"**
3. Enter peer ID
4. Click **"Create"**

**REST API:**
```http
POST /api/chat/conversations
Content-Type: application/json

{
  "peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5"
}
```

**Response:**
```json
{
  "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
  "conversation_type": "1on1",
  "peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5",
  "created_at": 1735401234,
  "updated_at": 1735401234,
  "unread_count": 0
}
```

### Key Exchange Process

Key exchange is initiated automatically when sending the first message:

1. **Initiator (Alice):**
   - Generates ephemeral X25519 key pair
   - Signs ephemeral public key with Ed25519 identity key
   - Sends key exchange request to peer

2. **Receiver (Bob):**
   - Verifies signature using Alice's Ed25519 public key
   - Generates own ephemeral X25519 key pair
   - Computes shared secret via ECDH
   - Sends key exchange ACK with ephemeral public key + signature

3. **Completion:**
   - Alice verifies Bob's signature
   - Alice computes shared secret
   - Both initialize Double Ratchet with shared secret
   - Messages can now be exchanged

**Key Exchange Timeout:** 10 seconds (peer may be offline)

### Sending Messages

**REST API:**
```http
POST /api/chat/conversations/{conversation_id}/messages
Content-Type: application/json

{
  "content": "Hello, this is an encrypted message!"
}
```

**Response:**
```json
{
  "message_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "message": {
    "message_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
    "sender_peer_id": "abc123...",
    "timestamp": 1735401234,
    "status": "sent",
    "content": "Hello, this is an encrypted message!"
  }
}
```

### Receiving Messages

Incoming messages are:
1. Received via QUIC connection (direct or relay)
2. Decrypted using Double Ratchet
3. Stored in local database
4. Broadcast via WebSocket to UI

---

## Group Chats

### Creating a Group

Groups require at least 2 other members besides the creator.

**Web UI:**
1. Navigate to **Chat** page
2. Click **"New Group"**
3. Enter group name
4. Add member peer IDs
5. Click **"Create Group"**

**REST API:**
```http
POST /api/chat/groups
Content-Type: application/json

{
  "group_name": "Project Team",
  "members": [
    "peer_id_1",
    "peer_id_2",
    "peer_id_3"
  ]
}
```

**Response:**
```json
{
  "conversation_id": "660e8400-e29b-41d4-a716-446655440001",
  "conversation_type": "group",
  "group_name": "Project Team",
  "created_at": 1735401234,
  "updated_at": 1735401234,
  "unread_count": 0
}
```

### Group Creation Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Group Creation Flow                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Creator                                                         │
│  ┌─────────────┐                                                 │
│  │ 1. Create   │                                                 │
│  │    Group    │                                                 │
│  └──────┬──────┘                                                 │
│         │                                                        │
│         │ 2. Generate Sender Key                                 │
│         │                                                        │
│         │ 3. Save to Database                                    │
│         │                                                        │
│         ├──────────── 4. Send Group Create ────────▶ Member A   │
│         │                 (group_id, name, members)              │
│         │                                                        │
│         ├──────────── 4. Send Group Create ────────▶ Member B   │
│         │                                                        │
│         │                                                        │
│         ├──────────── 5. Distribute Sender Key ───▶ Member A    │
│         │                 (chain_key, message_num)               │
│         │                                                        │
│         └──────────── 5. Distribute Sender Key ───▶ Member B    │
│                                                                  │
│  Members:                                                        │
│  - Receive group creation notification                           │
│  - Store creator's sender key                                    │
│  - Generate own sender key                                       │
│  - Distribute to all members                                     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Sending Group Messages

**REST API:**
```http
POST /api/chat/groups/{group_id}/messages
Content-Type: application/json

{
  "content": "Hello team!"
}
```

**Response:**
```json
{
  "message_id": "8d9e6679-7425-40de-944b-e07fc1f90ae8"
}
```

Group messages are:
1. Encrypted using sender's key
2. Signed with Ed25519 identity key
3. Sent to all group members
4. Each recipient decrypts using stored sender key

### Managing Group Members

**Invite a Peer:**
```http
POST /api/chat/groups/{group_id}/invite
Content-Type: application/json

{
  "peer_id": "new_member_peer_id"
}
```

**Leave Group:**
```http
POST /api/chat/groups/{group_id}/leave
```

**Get Members:**
```http
GET /api/chat/groups/{group_id}/members
```

**Response:**
```json
{
  "members": [
    {
      "peer_id": "creator_peer_id",
      "joined_at": 1735401234,
      "is_admin": true
    },
    {
      "peer_id": "member_peer_id",
      "joined_at": 1735401235,
      "is_admin": false
    }
  ]
}
```

---

## Message Flow

### Direct Connection Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Direct Message Flow                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Sender                                           Receiver       │
│  ┌───────────┐                                   ┌───────────┐  │
│  │           │                                   │           │  │
│  │ 1. Encrypt│                                   │           │  │
│  │    Message│                                   │           │  │
│  │           │                                   │           │  │
│  │ 2. Store  │                                   │           │  │
│  │   (created)│                                  │           │  │
│  │           │                                   │           │  │
│  │ 3. Send   │─────────── QUIC ─────────────────▶│ 4. Receive│  │
│  │    via    │                                   │           │  │
│  │    QUIC   │                                   │ 5. Decrypt│  │
│  │           │                                   │           │  │
│  │           │◀─────── Delivery Confirm ─────────│ 6. Store  │  │
│  │ 7. Update │                                   │           │  │
│  │ (delivered)│                                  │ 7. Notify │  │
│  │           │                                   │   via WS  │  │
│  └───────────┘                                   └───────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Relay Connection Flow

For NAT-ed peers, messages are routed through relay servers:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Relay Message Flow                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Sender                  Relay Server              Receiver      │
│  ┌───────────┐          ┌───────────┐          ┌───────────┐   │
│  │           │          │           │          │           │   │
│  │ 1. Query  │──DHT────▶│           │          │           │   │
│  │    Peer   │          │           │          │           │   │
│  │   Metadata│          │           │          │           │   │
│  │           │          │           │          │           │   │
│  │ 2. Get    │          │           │          │           │   │
│  │   Relay   │          │           │          │           │   │
│  │   Address │          │           │          │           │   │
│  │           │          │           │          │           │   │
│  │ 3. Send   │─────────▶│ 4. Forward│─────────▶│ 5. Receive│   │
│  │    via    │          │    to     │          │           │   │
│  │   Relay   │          │   Target  │          │ 6. Decrypt│   │
│  │           │          │           │          │           │   │
│  │           │◀─success─│           │          │           │   │
│  │ 7. Status │          │           │          │           │   │
│  │   (sent)  │          │           │          │           │   │
│  │           │          │           │          │           │   │
│  └───────────┘          └───────────┘          └───────────┘   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Store-and-Forward for Offline Peers

If the recipient is offline (public peer), messages are queued locally:

```
┌─────────────────────────────────────────────────────────────────┐
│                Store-and-Forward Flow                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Sender                                           Receiver       │
│  ┌───────────┐                                   ┌───────────┐  │
│  │           │                                   │  OFFLINE  │  │
│  │ 1. Attempt│                                   │           │  │
│  │    Send   │─────────── FAILED ────────────────│           │  │
│  │           │                                   │           │  │
│  │ 2. Queue  │                                   │           │  │
│  │   Locally │                                   │           │  │
│  │   (sent)  │                                   │           │  │
│  │           │                                   │           │  │
│  │    ...    │       (time passes)               │           │  │
│  │           │                                   │           │  │
│  │           │                                   │  ONLINE   │  │
│  │           │◀─────────── Connect ──────────────│           │  │
│  │           │                                   │           │  │
│  │ 3. Deliver│───────────── QUIC ───────────────▶│ 4. Receive│  │
│  │   Queued  │                                   │           │  │
│  │  Messages │                                   │           │  │
│  │           │                                   │           │  │
│  └───────────┘                                   └───────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Message Status Lifecycle

### Status States

```
created ────▶ sent ────▶ delivered ────▶ read
   │
   └─────────────────────────▶ failed
```

| Status | Description | When Set |
|--------|-------------|----------|
| **created** | Message encrypted and stored locally | After encryption |
| **sent** | Message delivered to relay or queued | Relay confirms / store-forward |
| **delivered** | Message received by recipient | Direct delivery / delivery receipt |
| **read** | Message read by recipient | Read receipt received |
| **failed** | Delivery failed after all retries | Max retries exceeded |

### Delivery Confirmations

Delivery confirmations are sent automatically when a message is received:

```go
// Receiver sends delivery confirmation
SendDeliveryConfirmation(toPeerID, messageID, conversationID)
```

### Read Receipts

Read receipts are sent when a user marks a message or conversation as read:

**Mark Single Message:**
```http
POST /api/chat/messages/{message_id}/read
```

**Mark Conversation:**
```http
POST /api/chat/conversations/{conversation_id}/read
```

---

## WebSocket Real-Time Updates

### Event Types

Subscribe to chat events for real-time updates:

| Event | Description |
|-------|-------------|
| `chat.conversation.created` | New conversation created |
| `chat.conversation.updated` | Conversation metadata changed |
| `chat.conversations.updated` | Conversation list refreshed |
| `chat.message.created` | New message created (local) |
| `chat.message.received` | New message received (remote) |
| `chat.message.sent` | Message sent successfully |
| `chat.message.delivered` | Message delivered to recipient |
| `chat.message.read` | Message read by recipient |
| `chat.message.failed` | Message delivery failed |
| `chat.group.invite` | Group invite received |
| `chat.key_exchange.complete` | Key exchange completed |

### Subscription Example

```javascript
import { getWebSocketService, MessageType } from '../services/websocket'

const ws = getWebSocketService()

// Subscribe to new messages
ws.subscribe(MessageType.CHAT_MESSAGE_RECEIVED, (payload) => {
  console.log('New message:', payload)
  // payload contains: message_id, conversation_id, sender_peer_id, content, timestamp
})

// Subscribe to delivery confirmations
ws.subscribe(MessageType.CHAT_MESSAGE_DELIVERED, (payload) => {
  console.log('Message delivered:', payload.message_id)
})

// Subscribe to key exchange completion
ws.subscribe(MessageType.CHAT_KEY_EXCHANGE_COMPLETE, (payload) => {
  console.log('Key exchange complete with peer:', payload.peer_id)
})
```

### Event Payloads

**chat.message.received:**
```json
{
  "type": "chat.message.received",
  "payload": {
    "message_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
    "sender_peer_id": "c540bee38ab8a72d...",
    "content": "Hello!",
    "timestamp": 1735401234,
    "status": "delivered"
  }
}
```

**chat.message.delivered:**
```json
{
  "type": "chat.message.delivered",
  "payload": {
    "message_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "timestamp": 1735401235
  }
}
```

---

## API Reference

### Conversations

```
GET    /api/chat/conversations                  List conversations
POST   /api/chat/conversations                  Create conversation
GET    /api/chat/conversations/{id}             Get conversation
DELETE /api/chat/conversations/{id}             Delete conversation
POST   /api/chat/conversations/{id}/read        Mark as read
GET    /api/chat/conversations/{id}/messages    List messages
POST   /api/chat/conversations/{id}/messages    Send message
```

### Messages

```
POST   /api/chat/messages/{id}/read             Mark message as read
```

### Groups

```
POST   /api/chat/groups                         Create group
POST   /api/chat/groups/{id}/messages           Send group message
POST   /api/chat/groups/{id}/invite             Invite member
POST   /api/chat/groups/{id}/leave              Leave group
GET    /api/chat/groups/{id}/members            Get members
```

### Key Exchange

```
POST   /api/chat/key-exchange                   Initiate key exchange
```

### Unread Count

```
GET    /api/chat/unread                         Get total unread count
```

---

## Data Structures

### ChatConversation

```json
{
  "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
  "conversation_type": "1on1",
  "peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5",
  "group_name": null,
  "created_at": 1735401234,
  "updated_at": 1735401234,
  "last_message_at": 1735401500,
  "unread_count": 3,
  "last_message": { ... }
}
```

### ChatMessage

```json
{
  "message_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
  "sender_peer_id": "c540bee38ab8a72d354839127bd3fda6b46f2ab5",
  "content": "Hello, world!",
  "timestamp": 1735401234,
  "status": "delivered",
  "sent_at": 1735401234,
  "delivered_at": 1735401235,
  "read_at": 0
}
```

### ChatGroupMember

```json
{
  "conversation_id": "660e8400-e29b-41d4-a716-446655440001",
  "peer_id": "abc123...",
  "joined_at": 1735401234,
  "is_admin": true,
  "left_at": 0
}
```

---

## Security Considerations

### End-to-End Encryption

All messages are encrypted before leaving the sender's device:

1. **1-on-1 Messages:**
   - Encrypted with NaCl secretbox (XSalsa20-Poly1305)
   - Per-message keys derived via HKDF
   - Forward secrecy via DH ratchet

2. **Group Messages:**
   - Encrypted with NaCl secretbox
   - Per-message keys derived from sender's chain key
   - Signed with Ed25519 for authentication

### Key Storage

Cryptographic keys are stored encrypted at rest:

| Key Type | Storage | Encryption |
|----------|---------|------------|
| Ratchet State | SQLite | NaCl secretbox with master key |
| Sender Keys | SQLite | NaCl secretbox with master key |
| Skipped Keys | SQLite | NaCl secretbox with master key |
| Master Key | Memory | Derived from node's Ed25519 private key |

### Message Retention

Messages are stored with configurable TTL:
- Default: 30 days
- Automatic cleanup via background routine
- Skipped keys cleaned up after 7 days

### Identity Verification

Key exchanges are authenticated using Ed25519 signatures:
- Ephemeral public keys are signed with identity keys
- Signatures verified before accepting key exchange
- Prevents MITM attacks

---

## Configuration

### Chat Settings

Configure via the config file or environment variables:

| Setting | Default | Description |
|---------|---------|-------------|
| `chat_message_ttl_days` | 30 | Message retention period |
| `invoice_max_retries` | 3 | Message send retry attempts |
| `invoice_retry_backoff_ms` | 2000 | Initial retry backoff |

### Cleanup Routine

The cleanup routine runs automatically:
- **Interval:** Every 24 hours
- **Tasks:**
  - Delete expired messages (older than TTL)
  - Clean up old skipped keys (older than 7 days)

---

## Troubleshooting

### Key Exchange Timeout

**Problem:** "Key exchange timed out after 10 seconds"

**Possible Causes:**
1. Peer is offline
2. Network connectivity issues
3. Peer is behind NAT without relay

**Solutions:**
- Verify peer is online in peers list
- Check network connectivity
- Ensure peer has published relay metadata

### Message Delivery Failed

**Problem:** Message status shows "failed"

**Possible Causes:**
1. Peer unreachable after all retries
2. Relay server unavailable
3. Key exchange not completed

**Solutions:**
```bash
# Check peer connectivity
curl http://localhost:30069/api/peers

# Check relay status
curl http://localhost:30069/api/relay/status

# Re-initiate key exchange
curl -X POST http://localhost:30069/api/chat/key-exchange \
  -H "Content-Type: application/json" \
  -d '{"peer_id": "..."}'
```

### Decryption Failed

**Problem:** "Decryption failed" error

**Possible Causes:**
1. Corrupted ratchet state
2. Out-of-sync message counters
3. Missing skipped keys

**Solutions:**
- Delete conversation and re-create
- Clear ratchet state for conversation
- Re-initiate key exchange

### Group Message Not Received

**Problem:** Group messages not appearing for some members

**Possible Causes:**
1. Member doesn't have sender key
2. Member offline during key distribution
3. Network issues to specific members

**Solutions:**
- Re-distribute sender key to affected member
- Have member rejoin the group
- Check member's network connectivity

---

## Database Schema

### Tables

```sql
-- Conversations
CREATE TABLE chat_conversations (
    conversation_id TEXT PRIMARY KEY,
    conversation_type TEXT NOT NULL CHECK(conversation_type IN ('1on1', 'group')),
    peer_id TEXT,
    group_name TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_message_at INTEGER DEFAULT 0,
    unread_count INTEGER DEFAULT 0
);

-- Messages
CREATE TABLE chat_messages (
    message_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sender_peer_id TEXT NOT NULL,
    encrypted_content BLOB NOT NULL,
    nonce BLOB NOT NULL,
    message_number INTEGER NOT NULL,
    timestamp INTEGER NOT NULL,
    status TEXT DEFAULT 'created',
    sent_at INTEGER DEFAULT 0,
    delivered_at INTEGER DEFAULT 0,
    read_at INTEGER DEFAULT 0,
    decrypted_content TEXT DEFAULT '',
    FOREIGN KEY (conversation_id) REFERENCES chat_conversations(conversation_id) ON DELETE CASCADE
);

-- Group Members
CREATE TABLE chat_group_members (
    conversation_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    joined_at INTEGER NOT NULL,
    is_admin INTEGER DEFAULT 0,
    left_at INTEGER DEFAULT 0,
    UNIQUE(conversation_id, peer_id)
);

-- Sender Keys (Group Encryption)
CREATE TABLE chat_sender_keys (
    conversation_id TEXT NOT NULL,
    sender_peer_id TEXT NOT NULL,
    encrypted_chain_key BLOB NOT NULL,
    nonce BLOB NOT NULL,
    signing_key BLOB,
    message_number INTEGER DEFAULT 0,
    updated_at INTEGER NOT NULL,
    UNIQUE(conversation_id, sender_peer_id)
);

-- Ratchet State (1-on-1 Encryption)
CREATE TABLE chat_ratchet_state (
    conversation_id TEXT PRIMARY KEY,
    encrypted_state BLOB NOT NULL,
    nonce BLOB NOT NULL,
    send_message_num INTEGER DEFAULT 0,
    recv_message_num INTEGER DEFAULT 0,
    remote_dh_pub_key BLOB,
    key_exchange_complete INTEGER DEFAULT 0
);

-- Skipped Keys (Out-of-Order Messages)
CREATE TABLE chat_skipped_keys (
    conversation_id TEXT NOT NULL,
    message_number INTEGER NOT NULL,
    encrypted_key BLOB NOT NULL,
    nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(conversation_id, message_number)
);
```

---

## Support

For chat issues:

1. Check [Troubleshooting](#troubleshooting) section
2. Review logs: `~/.cache/remote-network/logs/`
3. Report issues: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated:** 2025-01-21
