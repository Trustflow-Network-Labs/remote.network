# DHT-Based Metadata Architecture

## Table of Contents
1. [Overview](#overview)
2. [Motivation](#motivation)
3. [Architecture Comparison](#architecture-comparison)
4. [Core Concepts](#core-concepts)
5. [Detailed Startup Flow](#detailed-startup-flow)
6. [Peer Exchange Protocol](#peer-exchange-protocol)
7. [Critical Filtering Logic](#critical-filtering-logic)
8. [Data Structures](#data-structures)
9. [NAT Peer Lifecycle](#nat-peer-lifecycle)
10. [Implementation Plan](#implementation-plan)
11. [Migration Strategy](#migration-strategy)
12. [Testing Strategy](#testing-strategy)

---

## Overview

This document describes the architectural redesign of the peer metadata exchange system, moving from a **broadcast/gossip model** to a **DHT-as-source-of-truth model** using BEP_44 mutable data storage.

### Key Changes
- **DHT becomes single source of truth** for peer metadata (using BEP_44)
- **Ed25519 public key cryptography** for peer identity and metadata signing
- **On-demand metadata queries** with 5-minute caching (vs constant broadcasts)
- **Known peers exchange** on every successful connection (vs dedicated PEX protocol)
- **Minimal local storage**: Only `(peer_id, public_key, last_seen, topic)`
- **No metadata broadcasts** or dedicated peer exchange messages

---

## Motivation

### Problems with Current Architecture

1. **Timing Issues**
   - Metadata broadcasts can arrive out-of-order
   - NAT peers may miss relay info updates
   - Race conditions between broadcast and peer exchange

2. **Stale Data**
   - Peer exchange can propagate stale metadata
   - No authoritative source leads to inconsistencies
   - Different peers have different views of network state

3. **Bandwidth Overhead**
   - Continuous broadcasts to all known peers
   - Peer exchange adds 2KB to every metadata message
   - Redundant information sharing

4. **Complexity**
   - Two separate systems (broadcasts + PEX) for same goal
   - Complex interaction between broadcast priority and PEX updates
   - Difficult to reason about metadata freshness

### Benefits of DHT-Based Approach

1. **Single Source of Truth**
   - All metadata stored in DHT with BEP_44
   - Signed by peer's private key (tamper-proof)
   - Versioned with sequence numbers (latest always wins)

2. **On-Demand Queries**
   - Query only when connection needed
   - 5-minute caching reduces query overhead
   - No continuous broadcasts

3. **Simplified Architecture**
   - Remove MetadataBroadcaster
   - Remove PeerExchange protocol
   - Single mechanism for metadata distribution

4. **Better Scalability**
   - DHT naturally distributes load across network
   - No O(n) broadcast messages to all peers
   - Query overhead proportional to actual connections

5. **Self-Healing**
   - Hourly republishing keeps data fresh
   - Peers always get latest metadata from DHT
   - No accumulation of stale data

---

## Architecture Comparison

### Current Architecture

```
┌─────────────┐
│  Peer Node  │
└──────┬──────┘
       │
       ├──► DHT (announce/get_peers for discovery)
       │
       ├──► Metadata Broadcaster (broadcast changes to all peers)
       │
       ├──► Peer Exchange (share 20 peers in every metadata msg)
       │
       └──► Local Database (full PeerMetadata for all peers)
```

**Data Flow:**
1. Peer updates local state (e.g., connects to relay)
2. Broadcasts metadata to ALL known peers
3. Receives peer exchange lists in metadata messages
4. Stores full metadata locally for all discovered peers
5. Periodic broadcasts keep everyone updated

### Proposed Architecture

```
┌─────────────┐
│  Peer Node  │
└──────┬──────┘
       │
       ├──► DHT (BEP_44: publish/query signed metadata)
       │     ├─ Publish: Every hour + on significant changes
       │     └─ Query: On-demand when connection needed
       │
       ├──► Known Peers Exchange (on every successful connection)
       │     └─ Exchange: (peer_id, public_key) pairs only
       │
       └──► Local Database
             ├─ Persistent: (peer_id, public_key, last_seen, topic)
             └─ Cache: Full metadata (5-minute TTL)
```

**Data Flow:**
1. Peer updates local state (e.g., connects to relay)
2. Publishes updated metadata to DHT (BEP_44 mutable data)
3. On connection with any peer: exchange known peers lists
4. When connection needed: query DHT for peer's metadata
5. Cache metadata for 5 minutes to reduce DHT queries

---

## Core Concepts

### 1. BEP_44: DHT Mutable Data

**What is BEP_44?**
- Extension to BitTorrent DHT for storing signed, versioned data
- Data is keyed by SHA1(public_key)
- Includes Ed25519 signature to prevent tampering
- Sequence number ensures latest version wins

**Structure:**
```python
{
  "v": <bencoded metadata>,      # Value: our peer metadata
  "k": <32-byte public key>,     # Ed25519 public key
  "sig": <64-byte signature>,    # Ed25519 signature of (seq + v)
  "seq": <integer>,              # Sequence number (monotonically increasing)
  "salt": <optional bytes>       # Salt (we won't use this)
}
```

**Operations:**
- **PUT**: Publish metadata to DHT (signed with private key)
- **GET**: Query metadata from DHT (verify signature with public key)
- **Republish**: Every ~60 minutes to keep data alive in DHT

**Reference Implementation:**
- [Pkarr](https://github.com/Nuhvi/pkarr) - Uses DHT for DNS records, similar architecture

### 2. Ed25519 Public Key Cryptography

**Why Ed25519?**
- Fast signing and verification
- 32-byte public keys (compact)
- 64-byte signatures (reasonable overhead)
- Required by BEP_44

**Key Generation:**
```go
// On first startup, generate keypair
publicKey, privateKey, err := ed25519.GenerateKey(nil)

// Store in config/keys directory
saveKeys(publicKey, privateKey)
```

**Usage:**
- **Signing**: Sign metadata before publishing to DHT
- **Verification**: Verify metadata signature after querying DHT
- **Identity**: Derive stable peer_id from public key

### 3. Two-Layer Identity System

**Layer 1: DHT Node ID (Routing)**
- Used for DHT routing table (BEP_5)
- **Public nodes**: `SHA1(IP || r)` where r ∈ [0, 7] (BEP_42 security)
- **NAT nodes**: Random ID (bypass BEP_42 validation)
- **Purpose**: DHT routing, not peer identity

**Layer 2: Peer ID (Application)**
- Used for application-level peer identification
- **Derivation**: `SHA1(public_key)`
- **Stable**: Never changes (tied to public key)
- **Purpose**: Persistent peer identity across sessions

**DHT Storage Key**
- **Key**: `SHA1(public_key)` (same as Peer ID)
- **Purpose**: Where we store/retrieve metadata in DHT

**Why Two Layers?**
- DHT security (BEP_42) requires node ID derived from IP
- But IP can change (DHCP, mobile, VPN)
- Peer ID must be stable for application logic
- Solution: Separate routing identity (DHT Node ID) from application identity (Peer ID)

### 4. Metadata Caching (5-Minute TTL)

**Why Cache?**
- DHT queries have latency (~500ms to 2s)
- Connections to same peer are frequent (keepalive, data exchange)
- Balance between freshness and query overhead

**Cache Structure:**
```go
type MetadataCache struct {
    entries map[string]*CacheEntry  // peer_id -> entry
    mutex   sync.RWMutex
}

type CacheEntry struct {
    Metadata  *PeerMetadata
    CachedAt  time.Time
    TTL       time.Duration  // 5 minutes
}
```

**Cache Logic:**
```go
func (c *MetadataCache) Get(peerID string) (*PeerMetadata, bool) {
    entry, exists := c.entries[peerID]
    if !exists {
        return nil, false  // Cache miss
    }

    if time.Since(entry.CachedAt) > entry.TTL {
        delete(c.entries, peerID)  // Expired
        return nil, false
    }

    return entry.Metadata, true  // Cache hit
}
```

**Cache Invalidation:**
- **Time-based**: Automatic after 5 minutes
- **Explicit**: On connection failure (peer may have changed)
- **Periodic cleanup**: Remove expired entries every minute

### 5. Known Peers Exchange

**What is Exchanged?**
- NOT full metadata (that's in DHT)
- ONLY: `(peer_id, public_key)` pairs

**When?**
- On **EVERY** successful connection (not just bootstrap)
- Bidirectional: Both sides exchange their known peers

**Purpose:**
- Distributed peer discovery (gossip-style)
- Bootstrap provides initial peers
- Each connection expands network knowledge
- Eventually, all peers know about all peers (or most)

**Example:**
```
Peer A connects to Peer B:

A → B: "Here are 50 peers I know: [(id1, key1), (id2, key2), ...]"
B → A: "Here are 50 peers I know: [(id3, key3), (id4, key4), ...]"

Both store new (peer_id, public_key) pairs in database.
When A needs to connect to peer id3, A queries DHT using key3.
```

---

## Detailed Startup Flow

### Phase 1: Initial Metadata Publishing (All Peers)

**Steps 1-3: Publish BEFORE Bootstrap**

```
ALL PEERS (Public & NAT):

┌─────────────────────────────────────────────────────────────┐
│ Step 1: Generate Metadata                                   │
│                                                              │
│  - Detect public/private IP addresses                       │
│  - Detect NAT type (UPnP, STUN, etc.)                      │
│  - Determine node type: "public" or "private"              │
│  - For NAT peers: using_relay = false (no relay yet)       │
│                                                              │
│  metadata = {                                               │
│    public_ip: "95.180.109.240",                            │
│    private_ip: "192.168.2.30",                             │
│    node_type: "private",                                    │
│    using_relay: false,         ← NAT peers start false     │
│    protocols: ["quic"],                                     │
│    timestamp: 1234567890,                                   │
│    seq: 0                       ← First publish            │
│  }                                                           │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 2: Publish to DHT (BEP_44)                             │
│                                                              │
│  key = SHA1(our_public_key)                                 │
│  value = bencode(metadata)                                  │
│  sig = ed25519_sign(privateKey, seq + value)               │
│                                                              │
│  DHT.Put(key, value, sig, seq, publicKey)                  │
│                                                              │
│  → Metadata now available in DHT at SHA1(our_public_key)   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 3: Announce to DHT (Standard)                          │
│                                                              │
│  topic = "remote-network-mesh"                              │
│  infohash = SHA1(topic)                                     │
│                                                              │
│  DHT.Announce(infohash, our_port)                          │
│                                                              │
│  → We're now discoverable for this topic                   │
└─────────────────────────────────────────────────────────────┘
```

### Phase 2: Bootstrap & Discovery (All Peers)

**Steps 4-7: Connect and Exchange**

```
ALL PEERS:

┌─────────────────────────────────────────────────────────────┐
│ Step 4: Connect to Bootstrap Nodes                          │
│                                                              │
│  Bootstrap nodes (from config):                             │
│    - 159.65.253.245:30906 (our custom bootstrap 1)         │
│    - 167.86.116.185:30906 (our custom bootstrap 2)         │
│                                                              │
│  Connect via QUIC to bootstrap nodes                        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 5: QUIC Handshake - Exchange Identity                  │
│                                                              │
│  Peer → Bootstrap:                                          │
│    {                                                         │
│      peer_id: SHA1(our_public_key),                        │
│      public_key: <32-byte Ed25519 key>                     │
│    }                                                         │
│                                                              │
│  Bootstrap → Peer:                                          │
│    {                                                         │
│      peer_id: SHA1(bootstrap_public_key),                  │
│      public_key: <32-byte Ed25519 key>                     │
│    }                                                         │
│                                                              │
│  Both sides store: (peer_id, public_key, last_seen, topic) │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 6: Exchange Known Peers Lists                          │
│                                                              │
│  Peer → Bootstrap: "Send me your known peers"              │
│                                                              │
│  Bootstrap → Peer:                                          │
│    [                                                         │
│      (peer_id_1, public_key_1),                            │
│      (peer_id_2, public_key_2),                            │
│      (peer_id_3, public_key_3),                            │
│      ... (up to 50 peers)                                   │
│    ]                                                         │
│                                                              │
│  Peer → Bootstrap: (also sends known peers, if any)        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 7: Store Known Peers Locally                          │
│                                                              │
│  For each (peer_id, public_key) received:                  │
│    - Check if already in database                          │
│    - If new: INSERT INTO known_peers                       │
│    - If exists: UPDATE last_seen = now()                   │
│                                                              │
│  Database now contains:                                     │
│    - Bootstrap nodes                                        │
│    - All peers known by bootstrap                          │
└─────────────────────────────────────────────────────────────┘
```

### Phase 3: NAT Peers Find Relay (NAT Peers Only)

**Steps 8-12: Relay Connection and Update**

```
NAT PEERS ONLY:

┌─────────────────────────────────────────────────────────────┐
│ Step 8: Query DHT for Known Peers                          │
│                                                              │
│  For each known peer from database:                        │
│    key = SHA1(peer.public_key)                             │
│    metadata = DHT.Get(key)                                 │
│    verify_signature(metadata, peer.public_key)             │
│                                                              │
│  Look for: is_relay = true                                 │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 9: Find Best Relay                                     │
│                                                              │
│  relay_nodes = filter(peers, is_relay=true)                │
│                                                              │
│  Sort by:                                                    │
│    1. RTT (ping latency)                                    │
│    2. Geographic proximity (IP subnet)                      │
│    3. Load (number of connected clients, if available)     │
│                                                              │
│  best_relay = relay_nodes[0]                               │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 10: Connect to Relay (Direct Connection)               │
│                                                              │
│  // Relay has public IP, so NAT peer can connect directly  │
│                                                              │
│  conn = QUIC.Connect(best_relay.public_ip)                 │
│                                                              │
│  Send registration message:                                 │
│    {                                                         │
│      type: "register_relay_client",                        │
│      peer_id: our_peer_id,                                 │
│      public_key: our_public_key                            │
│    }                                                         │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 11: Relay Assigns Session                              │
│                                                              │
│  Relay → NAT Peer:                                          │
│    {                                                         │
│      status: "registered",                                  │
│      session_id: "abc123def456",                           │
│      relay_address: "159.65.253.245:30906"                 │
│    }                                                         │
│                                                              │
│  NAT peer now has:                                          │
│    - relay_node_id                                          │
│    - relay_session_id                                       │
│    - relay_address                                          │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Step 12: Update DHT with Relay Info                        │
│                                                              │
│  metadata.using_relay = true                                │
│  metadata.connected_relay = relay_node_id                   │
│  metadata.relay_session_id = "abc123def456"                │
│  metadata.relay_address = "159.65.253.245:30906"           │
│  metadata.seq = 1  // Increment sequence number            │
│                                                              │
│  key = SHA1(our_public_key)                                 │
│  value = bencode(metadata)                                  │
│  sig = ed25519_sign(privateKey, seq + value)               │
│                                                              │
│  DHT.Put(key, value, sig, seq, publicKey)                  │
│                                                              │
│  → Updated metadata now in DHT!                            │
│  → Other peers can now reach us via relay                  │
└─────────────────────────────────────────────────────────────┘
```

---

## Peer Exchange Protocol

### Exchange on Every Successful Connection

**Not just bootstrap nodes** - exchange happens with ALL peers:

```
┌─────────────────────────────────────────────────────────────┐
│ Connection Event: Peer A ←→ Peer B                         │
└─────────────────────────────────────────────────────────────┘

Step 1: Mutual Identity Exchange
─────────────────────────────────

A → B: { peer_id: A_id, public_key: A_pubkey }
B → A: { peer_id: B_id, public_key: B_pubkey }

Both sides:
  - Verify public_key matches peer_id (SHA1(pubkey) == peer_id)
  - Store: INSERT OR UPDATE known_peers (peer_id, public_key, last_seen)


Step 2: Known Peers List Exchange
──────────────────────────────────

A → B: Known peers list
  [
    (peer_1_id, peer_1_pubkey),
    (peer_2_id, peer_2_pubkey),
    ...
    (peer_50_id, peer_50_pubkey)
  ]

B → A: Known peers list
  [
    (peer_51_id, peer_51_pubkey),
    (peer_52_id, peer_52_pubkey),
    ...
    (peer_100_id, peer_100_pubkey)
  ]


Step 3: Store New Peers
────────────────────────

For each received (peer_id, public_key):
  - Validate: SHA1(public_key) == peer_id
  - Check if already in database
  - If new: INSERT INTO known_peers
  - If exists: UPDATE last_seen = now()


Step 4: Avoid Duplicates
─────────────────────────

// Don't send peer back to itself
exclude_peer_ids = {B_id}

// Don't send peers that B likely knows
// (optional optimization)
sent_peers = SELECT * FROM known_peers
             WHERE peer_id NOT IN exclude_peer_ids
             ORDER BY last_seen DESC
             LIMIT 50
```

### Why Exchange on Every Connection?

1. **Distributed Discovery**
   - No reliance on single bootstrap node
   - Network knowledge spreads organically
   - Self-healing: gaps fill themselves over time

2. **Fast Convergence**
   - New peer connects to bootstrap: learns 50 peers
   - Connects to those 50: learns 50 more from each
   - Exponential growth in network knowledge

3. **Resilience**
   - If bootstrap goes down, peers still discover each other
   - Multiple paths to discover any peer
   - No single point of failure

4. **Freshness**
   - Every connection updates last_seen for exchanged peers
   - Inactive peers naturally age out
   - Active peers constantly reinforced

---

## Critical Filtering Logic

### The Problem: NAT Peers Without Relay

```
Scenario:
  - Peer A (public) wants to connect to Peer B (NAT)
  - Peer A knows Peer B's (peer_id, public_key) from exchange
  - Peer A queries DHT for Peer B's metadata

Query Result:
  {
    public_ip: "95.180.109.240",      // Shared with other NAT peers
    private_ip: "192.168.2.30",       // Not routable from outside
    node_type: "private",
    using_relay: false,                // ← NO RELAY INFO!
    protocols: ["quic"]
  }

Question: Can Peer A connect to Peer B?
Answer: NO! Peer B has no relay, and is behind NAT.
```

### Solution: Connectability Filter

**Before attempting connection, check if peer is reachable:**

```go
func (p *Peer) isConnectable(metadata *PeerMetadata) bool {
    // Case 1: Public peer - always connectable
    if metadata.NodeType == "public" {
        return true
    }

    // Case 2: NAT peer - only connectable with relay
    if metadata.NodeType == "private" {
        if !metadata.UsingRelay {
            return false  // ← SKIP! No relay = not reachable
        }

        // Validate relay info is complete
        if metadata.ConnectedRelay == "" ||
           metadata.RelaySessionID == "" ||
           metadata.RelayAddress == "" {
            return false  // ← SKIP! Incomplete relay info
        }

        return true  // ✓ NAT peer with valid relay
    }

    return false  // Unknown node type
}
```

### Peer Discovery with Filter

```go
func (p *Peer) discoverNetworkPeers() error {
    // Get all known peers from database
    knownPeers := p.db.GetAllKnownPeers()

    for _, peer := range knownPeers {
        // Check cache first (5-minute TTL)
        metadata, cached := p.metadataCache.Get(peer.PeerID)

        if !cached {
            // Cache miss - query DHT
            key := sha1.Sum(peer.PublicKey)
            metadata, err = p.dht.GetMetadata(key)
            if err != nil {
                p.logger.Debug(fmt.Sprintf("Failed to get metadata for %s: %v",
                    peer.PeerID, err))
                continue
            }

            // Verify signature
            if !verifySignature(metadata, peer.PublicKey) {
                p.logger.Warn(fmt.Sprintf("Invalid signature for %s", peer.PeerID))
                continue
            }

            // Cache for 5 minutes
            p.metadataCache.Set(peer.PeerID, metadata, 5*time.Minute)
        }

        // CRITICAL FILTER: Check if connectable
        if !p.isConnectable(metadata) {
            p.logger.Debug(fmt.Sprintf("Skipping %s: not connectable", peer.PeerID))
            continue  // ← SKIP! Save connection attempt
        }

        // Peer is connectable - proceed with connection
        if metadata.UsingRelay {
            p.connectViaRelay(peer, metadata)
        } else {
            p.connectDirect(peer, metadata)
        }
    }

    return nil
}
```

### Benefits of Filtering

#### 1. No Wasted Connection Attempts

**Without Filter:**
```
[2025-10-10 14:32:15] INFO  Attempting to connect to peer abc123...
[2025-10-10 14:32:45] ERROR Connection timeout to abc123 (30s)
[2025-10-10 14:32:46] WARN  Retrying connection to abc123 (attempt 2/3)
[2025-10-10 14:33:16] ERROR Connection timeout to abc123 (30s)
[2025-10-10 14:33:17] WARN  Retrying connection to abc123 (attempt 3/3)
[2025-10-10 14:33:47] ERROR Connection timeout to abc123 (30s)
[2025-10-10 14:33:48] ERROR Giving up on peer abc123

Total time wasted: 90 seconds
Total log spam: 6 messages
```

**With Filter:**
```
[2025-10-10 14:32:15] DEBUG Skipping peer abc123: NAT without relay info

Total time wasted: 0 seconds
Total log spam: 1 message (debug level)
```

#### 2. Automatic Retry (Self-Healing)

```go
// Peer discovery runs periodically
func (p *Peer) periodicDiscovery() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            p.discoverNetworkPeers()
            // Will automatically discover NAT peers once they have relay
        case <-p.ctx.Done():
            return
        }
    }
}
```

**Timeline:**
```
T+0s:  NAT peer boots, publishes metadata (using_relay=false)
T+5s:  Remote peer discovers NAT peer, skips (no relay)
T+10s: NAT peer connects to relay
T+11s: NAT peer updates DHT (using_relay=true)
T+30s: Remote peer's periodic discovery runs
T+31s: Remote peer queries DHT again, sees relay info
T+32s: Remote peer connects successfully via relay!
```

**Result:** No permanent failures, just temporary delay (max 30s)

---

## Data Structures

### PeerMetadata (Stored in DHT)

```go
type PeerMetadata struct {
    // Network Connectivity
    PublicIP    string   `bencode:"public_ip"`
    PrivateIP   string   `bencode:"private_ip"`
    PublicPort  int      `bencode:"public_port"`
    PrivatePort int      `bencode:"private_port"`

    // Node Classification
    NodeType    string   `bencode:"node_type"`     // "public" or "private"
    IsRelay     bool     `bencode:"is_relay"`

    // Relay Information (for NAT peers)
    UsingRelay      bool   `bencode:"using_relay"`
    ConnectedRelay  string `bencode:"connected_relay"`   // peer_id of relay
    RelaySessionID  string `bencode:"relay_session_id"`
    RelayAddress    string `bencode:"relay_address"`     // IP:port of relay

    // Capabilities
    Protocols    []string `bencode:"protocols"`
    Capabilities []string `bencode:"capabilities"`

    // Versioning
    Timestamp int64 `bencode:"timestamp"`  // Unix timestamp
    Sequence  int64 `bencode:"seq"`        // BEP_44 sequence number
}
```

### BEP_44 Structure

```go
type MutableData struct {
    Value     []byte    // Bencoded PeerMetadata
    PublicKey [32]byte  // Ed25519 public key
    Signature [64]byte  // Ed25519 signature
    Sequence  int64     // Monotonically increasing
    Salt      []byte    // Optional (we don't use)
}
```

### Local Database Schema

#### Table: `known_peers`

```sql
CREATE TABLE known_peers (
    peer_id     TEXT PRIMARY KEY,      -- SHA1(public_key)
    public_key  BLOB NOT NULL,         -- 32-byte Ed25519 key
    last_seen   INTEGER NOT NULL,      -- Unix timestamp
    topic       TEXT NOT NULL,         -- "remote-network-mesh"

    INDEX idx_last_seen (last_seen),
    INDEX idx_topic (topic)
);
```

#### Table: `metadata_cache`

```sql
CREATE TABLE metadata_cache (
    peer_id      TEXT PRIMARY KEY,
    metadata     TEXT NOT NULL,        -- JSON-encoded PeerMetadata
    cached_at    INTEGER NOT NULL,     -- Unix timestamp
    ttl_seconds  INTEGER DEFAULT 300,  -- 5 minutes

    INDEX idx_cached_at (cached_at)
);
```

### QUIC Handshake Messages

#### Identity Exchange

```go
type IdentityExchangeMsg struct {
    MessageType string   `json:"type"`        // "identity_exchange"
    PeerID      string   `json:"peer_id"`     // SHA1(public_key)
    PublicKey   [32]byte `json:"public_key"`  // Ed25519 public key
}
```

#### Known Peers Exchange

```go
type KnownPeersMsg struct {
    MessageType string      `json:"type"`    // "known_peers_exchange"
    Peers       []KnownPeer `json:"peers"`   // Up to 50 peers
}

type KnownPeer struct {
    PeerID    string   `json:"peer_id"`     // SHA1(public_key)
    PublicKey [32]byte `json:"public_key"`  // Ed25519 public key
}
```

---

## NAT Peer Lifecycle

### Complete Timeline

```
┌────────────────────────────────────────────────────────────────┐
│ T+0s: NAT Peer Startup                                         │
├────────────────────────────────────────────────────────────────┤
│ - Load/generate Ed25519 keypair                                │
│ - Derive peer_id = SHA1(public_key)                           │
│ - Detect network: NodeType = "private"                        │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+1s: Generate Initial Metadata                                │
├────────────────────────────────────────────────────────────────┤
│ metadata = {                                                    │
│   public_ip: "95.180.109.240",                                │
│   private_ip: "192.168.2.30",                                 │
│   node_type: "private",                                        │
│   using_relay: false,           ← No relay yet                │
│   protocols: ["quic"],                                         │
│   timestamp: 1728567600,                                       │
│   seq: 0                                                        │
│ }                                                               │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+2s: Publish to DHT (BEP_44)                                  │
├────────────────────────────────────────────────────────────────┤
│ key = SHA1(public_key)                                         │
│ sig = ed25519_sign(privateKey, seq + bencode(metadata))       │
│ DHT.Put(key, bencode(metadata), sig, seq=0, publicKey)        │
│                                                                 │
│ Status: Metadata in DHT, but shows no relay                   │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+3s: Announce to DHT                                          │
├────────────────────────────────────────────────────────────────┤
│ topic = "remote-network-mesh"                                  │
│ infohash = SHA1(topic)                                         │
│ DHT.Announce(infohash, port=30906)                            │
│                                                                 │
│ Status: Discoverable for topic                                │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+4s: Connect to Bootstrap Node                                │
├────────────────────────────────────────────────────────────────┤
│ bootstrap_addr = "159.65.253.245:30906"                        │
│ conn = QUIC.Connect(bootstrap_addr)                            │
│                                                                 │
│ Exchange identities:                                            │
│   → Send: (our_peer_id, our_public_key)                       │
│   ← Receive: (bootstrap_peer_id, bootstrap_public_key)        │
│                                                                 │
│ Exchange known peers:                                           │
│   → Send: [] (we're new, no known peers yet)                  │
│   ← Receive: [(p1_id, p1_key), (p2_id, p2_key), ...]         │
│                                                                 │
│ Store 50 peers from bootstrap in database                     │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+5s: Query DHT for Known Peers                                │
├────────────────────────────────────────────────────────────────┤
│ For each known peer:                                           │
│   key = SHA1(peer.public_key)                                 │
│   metadata = DHT.Get(key)                                      │
│   verify_signature(metadata, peer.public_key)                 │
│                                                                 │
│ Filter for relay nodes:                                        │
│   relay_peers = filter(peers, is_relay=true)                  │
│                                                                 │
│ Found 3 relay nodes!                                           │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+6s: Select Best Relay                                        │
├────────────────────────────────────────────────────────────────┤
│ Ping all relay nodes:                                          │
│   - Relay 1 (159.65.253.245): RTT = 45ms                      │
│   - Relay 2 (167.86.116.185): RTT = 120ms                     │
│   - Relay 3 (198.51.100.10): RTT = 200ms                      │
│                                                                 │
│ best_relay = Relay 1 (lowest RTT)                             │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+7s: Connect to Relay                                         │
├────────────────────────────────────────────────────────────────┤
│ conn = QUIC.Connect("159.65.253.245:30906")                   │
│                                                                 │
│ Send registration:                                              │
│   {                                                             │
│     type: "register_relay_client",                            │
│     peer_id: our_peer_id,                                      │
│     public_key: our_public_key                                │
│   }                                                             │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+8s: Relay Assigns Session                                    │
├────────────────────────────────────────────────────────────────┤
│ Relay → NAT Peer:                                              │
│   {                                                             │
│     status: "registered",                                      │
│     session_id: "abc123def456",                               │
│     relay_address: "159.65.253.245:30906",                    │
│     keepalive_interval: 5000  // 5 seconds                    │
│   }                                                             │
│                                                                 │
│ Start keepalive timer (ping relay every 5s)                   │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+9s: Update Metadata in DHT                                   │
├────────────────────────────────────────────────────────────────┤
│ metadata.using_relay = true                                     │
│ metadata.connected_relay = relay_peer_id                       │
│ metadata.relay_session_id = "abc123def456"                    │
│ metadata.relay_address = "159.65.253.245:30906"               │
│ metadata.timestamp = now()                                     │
│ metadata.seq = 1  // Increment sequence                       │
│                                                                 │
│ key = SHA1(public_key)                                         │
│ sig = ed25519_sign(privateKey, seq + bencode(metadata))       │
│ DHT.Put(key, bencode(metadata), sig, seq=1, publicKey)        │
│                                                                 │
│ Status: DHT updated with relay info!                          │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ T+10s: Fully Connected & Discoverable                          │
├────────────────────────────────────────────────────────────────┤
│ ✓ Metadata in DHT with relay info                             │
│ ✓ Connected to relay                                           │
│ ✓ Known by bootstrap and its peers                            │
│ ✓ Ready to receive connections via relay                      │
│                                                                 │
│ Remote peers can now:                                          │
│   1. Discover us via DHT/peer exchange                        │
│   2. Query DHT for our metadata                               │
│   3. See we have relay info                                    │
│   4. Connect to us via relay forwarding                       │
└────────────────────────────────────────────────────────────────┘
```

### Remote Peer's Perspective

```
Remote Peer wants to connect to NAT Peer:

┌────────────────────────────────────────────────────────────────┐
│ Scenario A: Query Before Relay Connection (T+0s to T+8s)      │
├────────────────────────────────────────────────────────────────┤
│ 1. Remote knows (peer_id, public_key) from peer exchange      │
│ 2. Remote queries DHT: metadata = DHT.Get(SHA1(public_key))   │
│ 3. Metadata shows: using_relay = false                        │
│ 4. Remote calls isConnectable(metadata)                       │
│ 5. Returns: false (NAT without relay)                         │
│ 6. Remote skips connection attempt                            │
│                                                                 │
│ Result: Clean skip, no failed connection                      │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ Scenario B: Query After Relay Connection (T+9s onwards)       │
├────────────────────────────────────────────────────────────────┤
│ 1. Remote queries DHT again (periodic discovery at T+30s)     │
│ 2. Metadata shows:                                             │
│      using_relay = true                                        │
│      connected_relay = "relay_peer_id"                        │
│      relay_session_id = "abc123def456"                        │
│      relay_address = "159.65.253.245:30906"                   │
│ 3. Remote calls isConnectable(metadata)                       │
│ 4. Returns: true (NAT with valid relay)                       │
│ 5. Remote connects via relay forwarding                       │
│                                                                 │
│ Result: Successful connection!                                │
└────────────────────────────────────────────────────────────────┘
```

---

## Implementation Plan

### Phase 1: BEP_44 Foundation (1-2 weeks)

#### Task 1.1: Ed25519 Key Management
```
File: internal/crypto/keys.go (new)

Functions:
  - GenerateKeypair() (publicKey, privateKey, error)
  - SaveKeys(publicKey, privateKey, filepath) error
  - LoadKeys(filepath) (publicKey, privateKey, error)
  - DerivePeerID(publicKey) string  // SHA1(pubkey)

Storage:
  - config/keys/ed25519_public.key
  - config/keys/ed25519_private.key
  - Permissions: 0600 for private key
```

#### Task 1.2: BEP_44 Implementation
```
File: internal/p2p/dht_bep44.go (new)

Functions:
  - PutMutable(key, value, signature, seq, publicKey) error
  - GetMutable(key) (value, signature, seq, publicKey, error)
  - VerifyMutableSignature(value, signature, seq, publicKey) bool

Protocol:
  - DHT query type: "put" and "get"
  - BEP_44 message format
  - Signature verification
  - Sequence number validation
```

#### Task 1.3: Metadata Publishing
```
File: internal/p2p/metadata_publisher.go (new)

Functions:
  - PublishMetadata(metadata *PeerMetadata) error
    → Bencode metadata
    → Sign with private key
    → Put to DHT via BEP_44

  - UpdateMetadata(metadata *PeerMetadata) error
    → Increment sequence number
    → Republish to DHT

  - StartPeriodicRepublish()
    → Republish every 60 minutes
    → Ensure DHT keeps our data
```

### Phase 2: Database Schema Migration (3-5 days)

#### Task 2.1: Create New Schema
```sql
-- Migration script: migrations/003_minimal_peer_storage.sql

CREATE TABLE known_peers_v2 (
    peer_id     TEXT PRIMARY KEY,
    public_key  BLOB NOT NULL,
    last_seen   INTEGER NOT NULL,
    topic       TEXT NOT NULL,

    INDEX idx_last_seen (last_seen),
    INDEX idx_topic (topic)
);

CREATE TABLE metadata_cache (
    peer_id      TEXT PRIMARY KEY,
    metadata     TEXT NOT NULL,
    cached_at    INTEGER NOT NULL,
    ttl_seconds  INTEGER DEFAULT 300,

    INDEX idx_cached_at (cached_at)
);

-- Migration: Copy essential data from old schema
INSERT INTO known_peers_v2 (peer_id, public_key, last_seen, topic)
SELECT node_id, '', last_seen, 'remote-network-mesh'
FROM peer_metadata;

-- Keep old table temporarily for rollback
ALTER TABLE peer_metadata RENAME TO peer_metadata_backup;
```

#### Task 2.2: Update Database Manager
```
File: internal/database/known_peers.go (new)

Functions:
  - StoreKnownPeer(peerID, publicKey, topic) error
  - GetKnownPeer(peerID) (publicKey, lastSeen, error)
  - GetAllKnownPeers() []KnownPeer
  - UpdateLastSeen(peerID) error
  - CleanupStalePeers(maxAge time.Duration) error
```

#### Task 2.3: Metadata Cache Implementation
```
File: internal/database/metadata_cache.go (new)

Functions:
  - CacheMetadata(peerID, metadata, ttl) error
  - GetCachedMetadata(peerID) (metadata, exists, error)
  - InvalidateCache(peerID) error
  - CleanupExpired() error  // Run every minute
```

### Phase 3: QUIC Handshake Modification (1 week)

#### Task 3.1: Identity Exchange Protocol
```
File: internal/p2p/messages.go

Add new message types:
  - MessageTypeIdentityExchange = "identity_exchange"
  - MessageTypeKnownPeersExchange = "known_peers_exchange"

Structures:
  - IdentityExchangeMsg
  - KnownPeersMsg
```

#### Task 3.2: Handshake Handler
```
File: internal/p2p/quic.go

Modify handleConnection():
  1. After QUIC connection established
  2. Send identity_exchange message
  3. Receive peer's identity_exchange
  4. Validate: SHA1(public_key) == peer_id
  5. Store in known_peers table
  6. Trigger known peers exchange
```

#### Task 3.3: Known Peers Exchange
```
File: internal/p2p/peer_exchange_v2.go (new)

Functions:
  - ExchangeKnownPeers(conn) error
    1. Get local known peers (limit 50)
    2. Send to remote peer
    3. Receive remote's known peers
    4. Store new peers in database
    5. Update last_seen for existing peers

  - SelectPeersToShare(excludePeerID) []KnownPeer
    → Recent peers (sorted by last_seen DESC)
    → Exclude peer we're talking to
    → Limit to 50 peers
```

### Phase 4: On-Demand Metadata Fetching (1 week)

#### Task 4.1: Metadata Query Service
```
File: internal/p2p/metadata_query.go (new)

Functions:
  - QueryMetadata(peerID, publicKey) (*PeerMetadata, error)
    1. Check cache first (5-min TTL)
    2. If cache miss: query DHT
    3. Verify signature with public key
    4. Cache result
    5. Return metadata

  - BatchQueryMetadata(peers []KnownPeer) map[string]*PeerMetadata
    → Query multiple peers in parallel
    → Return map of results
```

#### Task 4.2: Connectability Filter
```
File: internal/p2p/connectability.go (new)

Functions:
  - IsConnectable(metadata *PeerMetadata) bool
    → Check node type
    → Validate relay info for NAT peers
    → Return true/false

  - GetConnectionMethod(metadata *PeerMetadata) ConnectionMethod
    → Direct (public peer)
    → Relay (NAT with relay)
    → None (unreachable)
```

#### Task 4.3: Peer Discovery with Filter
```
File: internal/p2p/discovery.go (new)

Functions:
  - DiscoverNetworkPeers() error
    1. Get all known peers from database
    2. For each peer:
       a. Query metadata (cache-first)
       b. Check isConnectable()
       c. Skip if not connectable
       d. Connect if connectable
    3. Log statistics

  - StartPeriodicDiscovery()
    → Run discovery every 30 seconds
    → Automatic retry for NAT peers once they get relay
```

### Phase 5: Remove Legacy Systems (3-5 days)

#### Task 5.1: Remove Metadata Broadcaster
```
Files to delete:
  - internal/p2p/metadata_broadcaster.go

Files to modify:
  - internal/p2p/quic.go
    → Remove MetadataBroadcaster initialization
    → Remove broadcast calls on metadata changes
```

#### Task 5.2: Remove Old Peer Exchange
```
Files to delete:
  - internal/p2p/peer_exchange.go

Files to modify:
  - internal/p2p/messages.go
    → Remove KnownPeers field from metadata messages
  - internal/p2p/quic.go
    → Remove PEX selection logic
    → Remove storePeersFromExchange()
```

#### Task 5.3: Cleanup Message Types
```
File: internal/p2p/messages.go

Remove:
  - MessageTypePeerMetadataUpdate (no more broadcasts)
  - KnownPeers field from MetadataRequestData
  - KnownPeers field from MetadataResponseData

Keep:
  - MessageTypeMetadataRequest (for manual refresh)
  - MessageTypeMetadataResponse
```

### Phase 6: Integration & Testing (1-2 weeks)

#### Task 6.1: Unit Tests
```
Tests to write:
  - crypto/keys_test.go (key generation, save/load)
  - p2p/dht_bep44_test.go (put/get, signature verification)
  - p2p/metadata_publisher_test.go (publish, update, republish)
  - database/known_peers_test.go (CRUD operations)
  - database/metadata_cache_test.go (caching, expiration)
  - p2p/connectability_test.go (filter logic)
```

#### Task 6.2: Integration Tests
```
Scenarios:
  1. Public peer connects to public peer
  2. Public peer connects to NAT peer (via relay)
  3. NAT peer connects to NAT peer (via relay)
  4. NAT peer without relay is skipped
  5. NAT peer updates relay info, becomes connectable
  6. Metadata cache hit/miss scenarios
  7. Known peers exchange propagation
```

#### Task 6.3: Load Testing
```
Tests:
  - 100 peers: measure DHT query overhead
  - 1000 peers: measure cache hit rate
  - Network convergence time (how long until all peers know each other)
  - DHT republishing load
  - Relay failover scenarios
```

### Phase 7: Documentation & Deployment (3-5 days)

#### Task 7.1: Update Documentation
```
Files to update:
  - README.md (architecture overview)
  - docs/architecture.md (detailed design)
  - docs/peer-exchange-protocol.md (mark as deprecated)
  - docs/dht-metadata-architecture.md (this file!)
```

#### Task 7.2: Configuration Updates
```
File: internal/utils/configs/configs

New settings:
  [dht]
  metadata_republish_interval = 3600  # 1 hour

  [metadata_cache]
  ttl_seconds = 300                    # 5 minutes
  cleanup_interval = 60                # 1 minute

  [peer_discovery]
  discovery_interval = 30              # 30 seconds
  max_known_peers_to_share = 50       # Peer exchange limit
```

#### Task 7.3: Deployment Plan
```
1. Deploy to test network (3 nodes)
   - 1 relay (public)
   - 1 public peer
   - 1 NAT peer

2. Monitor for 48 hours:
   - DHT query rate
   - Cache hit rate
   - Connection success rate
   - Metadata freshness

3. Deploy to staging (10 nodes)
   - Mixed node types
   - Monitor convergence time

4. Gradual production rollout
   - 25% of nodes
   - 50% of nodes
   - 100% of nodes
```

---

## Migration Strategy

### Parallel Operation Period

**Option A: Hard Cutover (Recommended)**
```
1. Deploy new version to all nodes simultaneously
2. Old architecture disabled immediately
3. Database migration runs on startup
4. All nodes use DHT-based architecture

Pros:
  - Clean break, no confusion
  - No maintenance of two systems

Cons:
  - Requires coordinated deployment
  - Higher risk if issues found
```

**Option B: Gradual Migration**
```
1. Deploy new version with feature flag
2. Old architecture still active
3. New architecture runs in parallel
4. Compare results, ensure correctness
5. Flip feature flag to use new architecture
6. Remove old code in next release

Pros:
  - Lower risk
  - Can rollback easily

Cons:
  - Maintain both systems temporarily
  - More complex testing
```

### Database Migration

```go
// Migration script
func MigrateToMinimalPeerStorage(db *sql.DB) error {
    // Step 1: Create new tables
    _, err := db.Exec(`
        CREATE TABLE known_peers_v2 (
            peer_id TEXT PRIMARY KEY,
            public_key BLOB NOT NULL,
            last_seen INTEGER NOT NULL,
            topic TEXT NOT NULL
        )
    `)
    if err != nil {
        return err
    }

    // Step 2: Copy essential data
    // Note: public_key will be empty initially, filled during handshake
    _, err = db.Exec(`
        INSERT INTO known_peers_v2 (peer_id, public_key, last_seen, topic)
        SELECT node_id, '', last_seen, topic
        FROM peer_metadata
    `)
    if err != nil {
        return err
    }

    // Step 3: Backup old table
    _, err = db.Exec(`
        ALTER TABLE peer_metadata
        RENAME TO peer_metadata_backup
    `)
    if err != nil {
        return err
    }

    // Step 4: Rename new table
    _, err = db.Exec(`
        ALTER TABLE known_peers_v2
        RENAME TO known_peers
    `)

    return err
}
```

### Rollback Plan

```
If critical issues found:

1. Stop all nodes
2. Restore database backup:
   - DROP TABLE known_peers
   - ALTER TABLE peer_metadata_backup RENAME TO peer_metadata
3. Deploy previous version
4. Restart nodes

Keep peer_metadata_backup for 30 days after migration.
```

---

## Testing Strategy

### Unit Tests

```
✓ Ed25519 key generation
✓ Key save/load from disk
✓ Peer ID derivation (SHA1(pubkey))
✓ BEP_44 signature verification
✓ Metadata bencoding/parsing
✓ Cache hit/miss logic
✓ Cache expiration
✓ Connectability filter (all cases)
✓ Known peers deduplication
```

### Integration Tests

```
✓ Peer startup flow (all phases)
✓ Bootstrap connection & peer exchange
✓ DHT metadata publish/query
✓ Relay connection for NAT peer
✓ Metadata update after relay connection
✓ Remote peer discovers NAT peer
✓ Periodic discovery finds new peers
✓ Cache reduces DHT queries
✓ Relay failover scenario
✓ Metadata republishing
```

### System Tests

```
Scenario 1: 3-Node Network
  - 1 relay (public)
  - 1 public peer
  - 1 NAT peer

Verify:
  - NAT peer connects to relay
  - Public peer discovers NAT peer via DHT
  - Public peer connects to NAT peer via relay
  - All peers know about each other

Scenario 2: 10-Node Network
  - 2 relays
  - 3 public peers
  - 5 NAT peers

Verify:
  - All NAT peers find relays
  - Network converges (all peers know about all peers)
  - Convergence time < 2 minutes
  - Cache hit rate > 80% after convergence

Scenario 3: Relay Failure
  - NAT peer connected to relay
  - Kill relay process
  - NAT peer detects failure
  - NAT peer connects to backup relay
  - NAT peer updates DHT
  - Remote peers discover new relay info

Verify:
  - Failover time < 15 seconds
  - No data loss
  - Connections re-established
```

### Performance Benchmarks

```
Metrics to measure:

1. DHT Query Latency
   - p50: < 500ms
   - p95: < 2s
   - p99: < 5s

2. Cache Hit Rate
   - After 5 minutes: > 80%
   - After 10 minutes: > 90%

3. Network Convergence
   - 10 peers: < 1 minute
   - 100 peers: < 5 minutes
   - 1000 peers: < 15 minutes

4. Bandwidth Usage
   - DHT queries: < 10 KB/s per peer
   - Metadata publishing: < 1 KB/hour per peer
   - Known peers exchange: < 5 KB per connection

5. Storage
   - Known peers: ~100 bytes per peer
   - Metadata cache: ~500 bytes per peer
   - Total: < 1 MB for 1000 peers
```

---

## Conclusion

This architecture redesign provides:

1. **Simplified system** - Single source of truth (DHT), no broadcasts
2. **Better scalability** - On-demand queries, distributed load
3. **Improved reliability** - Signed metadata, versioning, self-healing
4. **Lower bandwidth** - Cache reduces queries by 80-90%
5. **Cleaner codebase** - Remove broadcaster and PEX, ~2000 lines deleted

**Next Steps:**
1. Review and approve this design document
2. Create feature branch: `feature/dht-metadata-storage`
3. Implement Phase 1 (BEP_44 foundation)
4. Iterate through remaining phases
5. Test thoroughly before production deployment

**Timeline Estimate:**
- Phase 1: 1-2 weeks
- Phase 2: 3-5 days
- Phase 3: 1 week
- Phase 4: 1 week
- Phase 5: 3-5 days
- Phase 6: 1-2 weeks
- Phase 7: 3-5 days

**Total: 6-8 weeks** for complete implementation and testing.
