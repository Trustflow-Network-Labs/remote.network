# Remote Network Architecture

**Status**: Production (January 2025)
**Version**: 1.0 - DHT-Only Architecture

---

## Table of Contents

1. [Overview](#overview)
2. [Core Concepts](#core-concepts)
3. [System Architecture](#system-architecture)
4. [Data Structures](#data-structures)
5. [Protocol Flow](#protocol-flow)
6. [DHT Integration](#dht-integration)
7. [NAT Traversal](#nat-traversal)
8. [Security](#security)
9. [Performance](#performance)

---

## Overview

Remote Network is a decentralized P2P networking system built on:

- **BitTorrent Mainline DHT** (BEP_5) for peer discovery
- **BEP_44 Mutable Data** for signed metadata storage
- **QUIC** for secure, multiplexed connections
- **Ed25519** for cryptographic identity
- **Relay-based NAT traversal** with hole punching support

### Key Design Principles

1. **DHT as Single Source of Truth** - All peer metadata lives in DHT, queried on-demand
2. **Lightweight Local Storage** - Only identity info stored locally (peer_id, public_key, is_relay)
3. **Cryptographic Identity** - Ed25519 keys for tamper-proof metadata
4. **NAT-Friendly** - Relay connections + hole punching for NAT traversal
5. **Efficient Discovery** - Identity exchange during QUIC handshake propagates peer knowledge

---

## Core Concepts

### 1. Two-Layer Identity System

**DHT Node ID (Layer 1 - Routing)**
- Used for DHT routing table (Kademlia)
- Public nodes: `SHA1(IP || r)` where r ∈ [0, 7] (BEP_42 security)
- NAT nodes: Random ID (bypass BEP_42 validation for non-public nodes)
- Changes if IP changes
- Purpose: DHT routing only

**Peer ID (Layer 2 - Application)**
- Used for application-level peer identification
- Derivation: `peer_id = SHA1(public_key)`
- Stable across IP changes and sessions
- Purpose: Persistent peer identity

**DHT Storage Key**
- Key: `SHA1(public_key)` (same as Peer ID)
- Used to store/retrieve metadata in DHT via BEP_44
- Enables peers to find metadata using public key

### 2. BEP_44 Mutable Data Storage

**Structure:**
```python
{
  "v": <bencoded metadata>,      # Peer metadata (IP, relay info, etc.)
  "k": <32-byte public key>,     # Ed25519 public key
  "sig": <64-byte signature>,    # Ed25519 signature of (seq + v)
  "seq": <integer>,              # Sequence number (monotonically increasing)
  "salt": null                   # Not used in our implementation
}
```

**Properties:**
- **Tamper-proof**: Ed25519 signature prevents modification
- **Versioned**: Sequence numbers ensure latest wins
- **Republished**: Every 30 minutes to keep data alive in DHT
- **Authoritative**: DHT is the single source of truth

### 3. Node Types

**Public Nodes**
- Have publicly routable IP address
- Can receive inbound connections directly
- Publish metadata to DHT immediately at startup
- Example: Servers with public IPs

**Relay Nodes** (Special Public Nodes)
- Public nodes that offer relay services
- Accept relay client registrations
- Forward traffic between NAT peers
- Publish metadata with `is_relay=true`
- Must be public (validated at startup)

**NAT Nodes** (Private)
- Behind NAT/firewall, no public IP
- Cannot receive inbound connections directly
- Must connect to relay before publishing metadata
- Publish metadata only after relay connection established
- Example: Home routers, mobile devices

### 4. Identity Exchange Protocol

Performed during QUIC handshake immediately after connection:

**Phase 1: Identity Exchange**
```go
// Both peers exchange:
{
  "peer_id":    "abc123...",      // SHA1(public_key)
  "dht_node_id": "def456...",     // DHT routing node ID
  "public_key": [32 bytes],       // Ed25519 public key
  "node_type":  "public"|"private",
  "is_relay":   true|false
}
```

**Phase 2: Known Peers Exchange**
```go
// Both peers exchange lists of known peers:
{
  "peers": [
    {
      "peer_id":    "...",
      "dht_node_id": "...",
      "public_key": [...],
      "is_relay":   true|false
    },
    // ... up to 50 peers
  ]
}
```

**Storage:**
- Store remote peer's identity in `known_peers` table
- Store all received known peers (INSERT OR IGNORE)
- Only update `last_seen` on conflict, preserve other fields

**Discovery Cascade:**
- After exchange, attempt to connect to newly discovered peers
- Each successful connection triggers another identity exchange
- Creates exponential discovery propagation through network

---

## System Architecture

### Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                       PeerManager (Core)                     │
│                                                               │
│  Coordinates all components, manages lifecycle               │
└───────┬──────────────────────────────┬──────────────────────┘
        │                              │
        ├──────────────────────────────┼──────────────────────┐
        │                              │                      │
┌───────▼────────┐          ┌──────────▼────────┐   ┌────────▼────────┐
│   DHT Peer     │          │   QUIC Peer       │   │  Relay Manager  │
│                │          │                   │   │                 │
│ - Discovery    │◄────────►│ - Identity Exch   │◄─►│ - NAT Sessions  │
│ - BEP_44 Store │          │ - Hole Punching   │   │ - Forwarding    │
│ - Republish    │          │ - Connections     │   │ - Keepalive     │
└───────┬────────┘          └──────────┬────────┘   └────────┬────────┘
        │                              │                      │
        │                   ┌──────────▼────────────────┐     │
        │                   │  Identity Exchanger       │     │
        │                   │                           │     │
        │                   │  - Identity Exchange      │     │
        │                   │  - Known Peers Exchange   │     │
        │                   │  - Recursive Discovery    │     │
        │                   └────────────┬──────────────┘     │
        │                                │                    │
        └────────────────────────────────┼────────────────────┘
                                         │
                              ┌──────────▼──────────┐
                              │  SQLite Database    │
                              │                     │
                              │  - known_peers      │
                              │    (lightweight)    │
                              └─────────────────────┘
```

### Key Components

**DHTPeer** (`internal/p2p/dht.go`)
- BitTorrent mainline DHT implementation
- BEP_44 mutable data PUT/GET operations
- Peer discovery via DHT queries
- Periodic republishing (30 min)

**QUICPeer** (`internal/p2p/quic.go`)
- QUIC server/client for encrypted connections
- Stream multiplexing
- Connection management
- Hole punching coordination

**IdentityExchanger** (`internal/p2p/identity_exchange.go`)
- Phase 3 QUIC handshake protocol
- Identity + known peers exchange
- Peer validation (verify peer_id matches public_key)
- Recursive discovery triggers

**RelayManager** (`internal/p2p/relay_manager.go`)
- Relay server for forwarding traffic
- Client registration and session management
- Keepalive monitoring
- Relay selection for NAT peers

**MetadataPublisher** (`internal/p2p/metadata_publisher.go`)
- Publishes peer metadata to DHT
- Automatic republishing every 30 minutes
- Immediate republish on metadata changes (relay connect/disconnect, IP change)

**Database** (`internal/database/`)
- **known_peers**: Minimal peer storage (peer_id, public_key, dht_node_id, is_relay, last_seen)
- No full metadata storage (metadata lives in DHT only)

---

## Data Structures

### KnownPeer (Database)

```go
type KnownPeer struct {
    PeerID     string    // SHA1(public_key)
    DHTNodeID  string    // DHT routing node ID
    PublicKey  []byte    // Ed25519 public key (32 bytes)
    IsRelay    bool      // Is this peer a relay?
    LastSeen   time.Time // Last contact timestamp
    Topic      string    // DHT topic ("remote-network-mesh")
    FirstSeen  time.Time // When first discovered
    Source     string    // "identity_exchange" or "peer_exchange"
}
```

### PeerMetadata (DHT Only)

```go
type PeerMetadata struct {
    // Network Info
    PublicIP    string
    PrivateIP   string
    PublicPort  int
    PrivatePort int

    // Classification
    NodeType    string   // "public" or "private"
    IsRelay     bool     // Offers relay services?

    // Relay Info (for NAT peers)
    UsingRelay      bool
    ConnectedRelay  string   // Relay peer_id
    RelaySessionID  string
    RelayAddress    string   // IP:port

    // Capabilities
    Protocols    []string
    Capabilities []string

    // Versioning
    Timestamp int64   // Unix timestamp
    Sequence  int64   // BEP_44 sequence number
    Version   int     // Metadata schema version
}
```

### Identity Exchange Messages

```go
// Step 1: Identity Exchange
type IdentityExchangeData struct {
    PeerID    string   `json:"peer_id"`
    DHTNodeID string   `json:"dht_node_id"`
    PublicKey []byte   `json:"public_key"`
    NodeType  string   `json:"node_type"`    // "public" or "private"
    IsRelay   bool     `json:"is_relay"`
    Topic     string   `json:"topic"`
}

// Step 2: Known Peers Exchange
type KnownPeersResponseData struct {
    Topic  string             `json:"topic"`
    Peers  []*KnownPeerEntry  `json:"peers"`
    Count  int                `json:"count"`
}

type KnownPeerEntry struct {
    PeerID    string   `json:"peer_id"`
    DHTNodeID string   `json:"dht_node_id"`
    PublicKey []byte   `json:"public_key"`
    IsRelay   bool     `json:"is_relay"`
}
```

---

## Protocol Flow

### Startup Flow

#### Public/Relay Nodes

```
1. Load/Generate Ed25519 keypair
   ├─ peer_id = SHA1(public_key)
   └─ dht_node_id = SHA1(IP || r)  # BEP_42 for public nodes

2. Detect Network
   ├─ Determine: public or private
   ├─ Get public IP
   └─ Get private IP

3. Build Initial Metadata
   ├─ network_info (IPs, ports)
   ├─ node_type = "public"
   ├─ is_relay = true|false
   ├─ using_relay = false (public nodes don't need relay)
   └─ sequence = 0

4. ✅ PUBLISH TO DHT IMMEDIATELY
   ├─ Sign with Ed25519 private key
   ├─ PUT to DHT (key = SHA1(public_key))
   └─ Start periodic republish (30 min)

5. Bootstrap Discovery
   ├─ Connect to bootstrap nodes
   ├─ Identity exchange (get peer_id, public_key)
   ├─ Known peers exchange (get list of 50 peers)
   └─ Store in known_peers table

6. Fully Operational
   ├─ Metadata in DHT
   ├─ Connected to bootstrap
   └─ Ready for inbound/outbound connections
```

#### NAT (Private) Nodes

```
1. Load/Generate Ed25519 keypair
   ├─ peer_id = SHA1(public_key)
   └─ dht_node_id = random  # No BEP_42 for NAT nodes

2. Detect Network
   ├─ Determine: private (behind NAT)
   ├─ Get public IP (observed from STUN)
   └─ Get private IP

3. ❌ DO NOT PUBLISH YET
   (Wait for relay connection)

4. Bootstrap Discovery
   ├─ Connect to bootstrap nodes
   ├─ Identity exchange
   ├─ Known peers exchange (get list of 50 peers)
   └─ Store in known_peers table

5. Find and Connect to Relay
   ├─ Filter known_peers for is_relay=true
   ├─ Select best relay (lowest RTT)
   ├─ Connect to relay
   ├─ Register as relay client
   └─ Receive session_id

6. ✅ NOW PUBLISH TO DHT
   ├─ Build metadata with relay info
   ├─ Sign with Ed25519 private key
   ├─ PUT to DHT (key = SHA1(public_key))
   └─ Start periodic republish (30 min)

7. Fully Operational
   ├─ Metadata in DHT (with relay info)
   ├─ Connected to relay
   └─ Reachable via relay forwarding
```

### Connection Flow

```
Peer A wants to connect to Peer B:

1. Check: Already connected?
   └─ Yes → DONE

2. Get Peer B's identity from known_peers
   ├─ peer_id
   ├─ public_key
   └─ is_relay

3. Query DHT for Peer B's metadata
   ├─ key = SHA1(public_key)
   ├─ GET from DHT (BEP_44)
   ├─ Verify signature with public_key
   └─ Extract metadata

4. Determine Connection Strategy
   ├─ Public peer? → Direct dial
   ├─ NAT peer with relay? → Connect via relay
   └─ NAT peer without relay? → SKIP (not reachable yet)

5. Establish QUIC Connection
   └─ Open QUIC stream

6. Identity Exchange
   ├─ Exchange identities (peer_id, public_key, node_type, is_relay)
   ├─ Store remote peer in known_peers
   ├─ Exchange known peers lists (up to 50 peers each)
   └─ Store newly discovered peers

7. Recursive Discovery
   └─ For each newly discovered peer:
      ├─ Check if already connected
      ├─ If not, attempt connection (back to step 3)
      └─ Cascading discovery propagates through network

8. Connection Complete
   └─ Ready for data exchange
```

---

## DHT Integration

### BEP_5: Peer Discovery

**Purpose**: Find peers interested in a topic

```
topic = "remote-network-mesh"
info_hash = SHA1(topic)

1. Announce to DHT
   DHT.Announce(info_hash, our_port)

2. Query DHT for peers
   peers = DHT.GetPeers(info_hash)

3. Connect to discovered peers
   For each peer: establish QUIC connection
```

### BEP_44: Metadata Storage

**Purpose**: Store signed, versioned peer metadata

```
1. Generate Metadata
   metadata = {
     public_ip: "...",
     node_type: "public",
     is_relay: true,
     ...
   }

2. Sign Metadata
   value = bencode(metadata)
   sig = ed25519_sign(private_key, seq + value)

3. Publish to DHT
   DHT.PutMutable(
     key = SHA1(public_key),
     value = value,
     sig = sig,
     seq = sequence_number,
     k = public_key
   )

4. Query Metadata
   result = DHT.GetMutable(SHA1(peer_public_key))
   verify_signature(result.value, result.sig, result.seq, peer_public_key)
```

### Republishing Strategy

- **Interval**: Every 30 minutes
- **Reason**: DHT nodes expire data after ~60 minutes
- **Logic**: Same sequence number (keep alive), increment seq only on metadata changes
- **Triggers for immediate republish**:
  - Relay connection/disconnection
  - IP address change
  - Node type change

---

## NAT Traversal

### Relay-Based Connections

**Registration Flow:**
```
NAT Peer                  Relay Node
    │                         │
    ├────── Connect ──────────►
    │                         │
    ├── register_relay_client ►
    │   {peer_id, public_key} │
    │                         │
    │◄──── Registration ────────┤
    │     {session_id,         │
    │      relay_address}      │
    │                         │
    ├──── Keepalive (5s) ─────►
    │◄──── Keepalive ACK ──────┤
```

**Forwarding Flow:**
```
Peer A              Relay               Peer B (NAT)
   │                  │                     │
   ├─ relay_forward ──►                     │
   │  {target: B's    │                     │
   │   session_id}    │                     │
   │                  ├─ forward_data ──────►
   │                  │  {from: A's peer_id}│
   │                  │                     │
   │                  │◄──── response ──────┤
   │◄─── response ────┤                     │
```

### Hole Punching

**When applicable:**
- Both peers are NAT
- Both connected to same relay
- Relay coordinates simultaneous connection attempts

**Protocol:**
1. Peer A requests hole punch via relay
2. Relay sends CONNECT to both peers
3. Both peers send CONNECT back (measure RTT)
4. Relay sends SYNC with measured RTT
5. Both peers wait RTT/2, then dial simultaneously
6. Success: Direct NAT-to-NAT connection
7. Failure: Fall back to relay forwarding

See [docs/hole-punching-protocol.md](hole-punching-protocol.md) for details.

---

## Security

### Cryptographic Identity

**Ed25519 Key Pair:**
- Generated once per node, stored in `./keys/`
- Private key: 32 bytes, never shared
- Public key: 32 bytes, shared during identity exchange
- Peer ID derived from public key (tamper-proof binding)

**Signature Verification:**
```go
// Verify metadata signature
derivedPeerID := SHA1(publicKey)
if derivedPeerID != claimedPeerID {
    return ErrPeerIDMismatch
}

isValid := ed25519.Verify(publicKey, signature, seq + value)
if !isValid {
    return ErrInvalidSignature
}
```

### Attack Mitigation

**Peer ID Spoofing:**
- Prevented: peer_id must equal SHA1(public_key)
- Verified during identity exchange
- Invalid peers rejected immediately

**Metadata Tampering:**
- Prevented: Ed25519 signature required for DHT storage
- Signature verification on every GET
- Unsigned/invalid metadata rejected

**Replay Attacks:**
- Prevented: Sequence numbers in BEP_44
- Lower sequence numbers ignored by DHT
- Stale metadata cannot overwrite fresh

**Sybil Attacks:**
- Mitigated: DHT uses BEP_42 for public nodes (IP-based node IDs)
- Each public IP can only control a limited number of DHT nodes
- NAT nodes use random IDs but limited influence on DHT routing

**Eclipse Attacks:**
- Mitigated: Multiple bootstrap nodes
- Peer exchange provides diverse peer sources
- DHT queries go to multiple nodes

---

## Performance

### Storage Efficiency

**Before (Old System):**
- Full metadata for all peers in database
- ~500 bytes per peer
- 1000 peers = ~500 KB database

**After (DHT-Only):**
- Minimal identity info only
- ~100 bytes per peer
- 1000 peers = ~100 KB database
- **80% reduction**

### Query Overhead

**DHT Queries:**
- Only when connection needed
- Typical latency: 500ms - 2s
- No caching implemented (query on every connection)

**Identity Exchange:**
- Happens during QUIC handshake
- ~5-10 ms overhead
- Discovers 50+ peers per connection

### Network Convergence

**Small Network (10-50 peers):**
- Full discovery: 2-5 minutes
- Method: Bootstrap + recursive discovery

**Medium Network (50-200 peers):**
- Full discovery: 5-10 minutes
- Exponential propagation via peer exchange

**Large Network (200+ peers):**
- Full discovery: 10-15 minutes
- Eventually consistent via periodic connections

### Resource Usage

**Memory:**
- Minimal: Only identity info cached in RAM
- ~100 bytes per known peer
- 1000 peers = ~100 KB

**CPU:**
- Ed25519 signing: ~0.1ms per signature
- Ed25519 verification: ~0.2ms per verification
- Bencoding: ~0.05ms per metadata

**Bandwidth:**
- DHT queries: ~1-2 KB per query
- Identity exchange: ~5 KB per handshake
- Known peers exchange: ~50 peers × 100 bytes = ~5 KB

---

## Configuration

Key settings in `internal/utils/configs/configs`:

```ini
[dht]
metadata_republish_interval = 1800  # 30 minutes

[peer_discovery]
discovery_interval = 30  # 30 seconds
max_known_peers_to_share = 50

[relay]
relay_mode = false  # Set to true for relay nodes
relay_keepalive_interval = 5  # 5 seconds

[quic]
quic_port = 30906
quic_idle_timeout = 300  # 5 minutes
```

---

## Future Enhancements

**Planned:**
- Metadata caching with TTL to reduce DHT queries
- NAT type detection and symmetric NAT port prediction
- Connection quality metrics and adaptive relay selection
- Service discovery protocol for application-level capabilities
- Geographic relay selection based on IP geolocation

---

## Event-Driven Architecture

### Overview

Remote Network uses an event-driven architecture to provide real-time updates to clients and coordinate between system components. The EventEmitter acts as a central message bus for all runtime events.

**Files**: `internal/api/server.go`, `internal/api/websocket/hub.go`

### EventEmitter System

**Implementation:**
```go
EventEmitter connects:
├─ JobManager → WorkflowManager
│   ├─ Job state changes
│   ├─ Execution progress
│   └─ Error events
│
├─ WorkflowManager → WebSocket Hub
│   ├─ Workflow execution events
│   ├─ Job completion notifications
│   └─ Error notifications
│
└─ DockerService → WebSocket Hub
    ├─ Image pull progress
    ├─ Build progress
    └─ Execution logs
```

**Event Types:**
- `job.status.changed` - Job state transitions
- `job.output.ready` - Job produced output
- `workflow.started` - Workflow execution began
- `workflow.completed` - Workflow finished
- `workflow.failed` - Workflow encountered error
- `docker.pull.progress` - Image download progress
- `docker.build.progress` - Image build progress
- `docker.execution.log` - Container log output

### WebSocket Hub

**Purpose**: Real-time bidirectional communication between server and web UI

**File**: `internal/api/websocket/hub.go`

**Architecture:**
```
WebSocket Hub
├─ Manages client connections
├─ Routes events from EventEmitter to subscribed clients
├─ Handles client subscriptions and filters
└─ Runs in dedicated goroutine (started at server.go:279)

Client Types:
├─ UI Clients: Dashboard, workflow designer
├─ Service Search: Real-time service discovery
└─ File Upload: Chunked file transfer
```

**Message Flow:**
```
System Event → EventEmitter → WebSocket Hub → Subscribed Clients
                     ↑                              ↓
                     └────── Client Actions ────────┘
```

**Connection Management:**
- Automatic connection cleanup on client disconnect
- Heartbeat/ping-pong for connection health
- Message queuing for slow clients
- Broadcast and targeted message delivery

### Real-Time Features

**1. Workflow Execution Monitoring**
```
User starts workflow in UI
  ↓
API: POST /api/workflows/:id/execute
  ↓
WorkflowManager executes jobs
  ↓
EventEmitter broadcasts:
  - workflow.started
  - job.status.changed (for each job)
  - workflow.completed
  ↓
WebSocket Hub pushes to UI
  ↓
UI updates in real-time (no polling)
```

**2. Docker Operations**
```
User creates Docker service
  ↓
DockerService pulls image
  ↓
EventEmitter broadcasts:
  - docker.pull.progress (%, layers, etc.)
  ↓
WebSocket pushes to UI
  ↓
UI shows progress bar in real-time
```

**3. Service Discovery**
```
User searches for services
  ↓
WebSocket: service_search message
  ↓
ServiceSearchHandler queries DHT/peers
  ↓
As peers respond:
  - WebSocket pushes each result
  ↓
UI updates service list incrementally
```

**Integration Points:**
- **Server Initialization** (server.go:114-125): EventEmitter connected to all managers
- **WebSocket Hub Startup** (server.go:279): Hub runs in background goroutine
- **API Handlers**: Trigger events for client notifications
- **Background Workers**: Data transfer events, peer discovery events

---

## Connection Lifecycle Management

### Overview

Connection lifecycle management ensures proper cleanup of stale connections, detection of NAT collisions, and efficient resource usage.

**Files**: `internal/p2p/quic.go`, `internal/core/peer.go`

### Active Streams Tracking

**Implementation:**
```go
ConnectionInfo struct:
├─ ActiveStreams (atomic int32)
│   ├─ Incremented when stream created
│   ├─ Decremented when stream closed
│   └─ Used for stale detection
│
├─ LastActivity (time.Time)
│   └─ Updated on any stream activity
│
└─ Connection (quic.Connection)
    └─ Underlying QUIC connection
```

### Stale Connection Cleanup

**Process** (quic.go):
```
cleanupStaleConnections() - Periodic background task (1 min interval)
├─ Enumerate all QUIC connections
├─ For each connection:
│   ├─ Check ActiveStreams == 0?
│   ├─ Check idle time > threshold (default: 5 min)?
│   ├─ If both true:
│   │   ├─ Log: "Cleaning up stale connection"
│   │   ├─ CloseWithError(0x100, "idle timeout")
│   │   └─ Remove from connection cache
│   └─ Else: Keep connection
└─ Notify PeerManager of cleanup events
```

**Benefits:**
- Prevents resource leaks
- Frees memory and file descriptors
- Maintains connection cache hygiene
- Enables connection pool optimization

### NAT Collision Detection

**Problem**: NAT can occasionally map different peers to same external IP:port

**Solution** (service_search_handler.go:commit 5875c98):
```
After QUIC connection established:
├─ Extract peer ID from connection
├─ Compare with expected peer ID
├─ If mismatch:
│   ├─ Log error
│   ├─ Close connection immediately
│   └─ Prevent data exchange with wrong peer
└─ If match: Proceed normally
```

**Security Impact:**
- Prevents accidental data leakage to wrong peer
- Essential for correctness in distributed workflows
- Protects against NAT-induced peer confusion

### Connection Health Monitoring

**Integration with PeerManager** (peer.go):
- 160 lines of connection lifecycle tracking
- Coordinated cleanup across relay and direct connections
- WebSocket notifications for UI updates
- Automatic re-discovery on connection loss

**Connection States:**
- `CONNECTING` - Connection in progress
- `CONNECTED` - Active connection with streams
- `IDLE` - Connected but no active streams
- `STALE` - Idle beyond threshold, marked for cleanup
- `CLOSED` - Connection terminated

---

**Under Consideration:**
- DHT query batching for bulk metadata retrieval
- Bloom filters to avoid redundant peer exchange
- mDNS for LAN peer discovery
- UPnP/NAT-PMP for port mapping

---

## References

- [BEP_5: DHT Protocol](http://www.bittorrent.org/beps/bep_0005.html)
- [BEP_42: DHT Security Extension](http://www.bittorrent.org/beps/bep_0042.html)
- [BEP_44: Storing Arbitrary Data in DHT](http://www.bittorrent.org/beps/bep_0044.html)
- [QUIC Protocol](https://www.rfc-editor.org/rfc/rfc9000.html)
- [Ed25519 Signature Scheme](https://ed25519.cr.yp.to/)
- [Hole Punching Protocol](hole-punching-protocol.md)

---

**Document Version**: 1.0
**Last Updated**: January 2025
**Status**: Production
