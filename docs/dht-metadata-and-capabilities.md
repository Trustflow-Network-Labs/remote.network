# DHT Metadata Storage and Peer Capabilities Exchange

This document provides a detailed technical guide to DHT metadata storage/reading and peer capabilities exchange via QUIC in the Remote Network system. It covers data flows, protocols, and specific file/function references for debugging.

## Table of Contents
- [Overview](#overview)
- [DHT Metadata Storage (BEP44 Protocol)](#dht-metadata-storage-bep44-protocol)
  - [Metadata Structure](#metadata-structure)
  - [Storage Mechanism](#storage-mechanism)
  - [Update Triggers](#update-triggers)
  - [Retrieval Strategy](#retrieval-strategy)
  - [Signing and Verification](#signing-and-verification)
- [Peer Capabilities Exchange](#peer-capabilities-exchange)
  - [Capabilities Structure](#capabilities-structure)
  - [Gathering Process](#gathering-process)
  - [Exchange Protocols](#exchange-protocols)
  - [Storage Mechanisms](#storage-mechanisms)
  - [Usage in Peer Selection](#usage-in-peer-selection)
- [Complete Data Flows](#complete-data-flows)
- [Key Data Structures](#key-data-structures)
- [Debugging Guide](#debugging-guide)

---

## Overview

The Remote Network uses a hybrid approach for peer discovery and capability exchange:

### DHT (BEP44 Mutable Data)
- **Purpose:** Decentralized peer discovery and metadata storage
- **Protocol:** BitTorrent Mainline DHT with BEP44 extensions
- **Storage:** Mutable data signed with Ed25519
- **Size Limit:** ~1000 bytes (bencode overhead + signature)
- **Lifetime:** Requires periodic republish (default: 30 minutes)

### QUIC Capabilities Exchange
- **Purpose:** Detailed system capabilities on-demand
- **Protocol:** QUIC streams with custom messages
- **Security:** mTLS with Ed25519 certificates
- **Size:** No practical limit (full system information)

### Architecture Components
- **BEP44Manager** - DHT mutable data operations
- **MetadataPublisher** - Automated metadata publishing and updates
- **MetadataFetcher** - Prioritized metadata retrieval
- **CapabilitiesHandler** - QUIC-based capabilities request handler
- **SystemCapabilities** - Hardware/software capability gathering
- **QUICPeer** - QUIC connection manager with relay discovery callbacks

### Relay Discovery Callback Mechanism

**File:** `internal/p2p/quic.go:66`

The QUICPeer includes a relay discovery callback system that automatically notifies the relay manager when relay nodes are discovered:

```go
type QUICPeer struct {
    ...
    onRelayDiscovered func(*database.PeerMetadata) // Callback for relay discovery
    ...
}
```

**Purpose:**
- Automatically notify RelayManager when relay candidates are discovered
- Enable dynamic relay pool building
- Support network changes and relay failover

**Flow:**
```
Metadata Discovery → Check IsRelay=true → Trigger onRelayDiscovered()
                                               ↓
                                        RelayManager.AddCandidate()
                                               ↓
                                        Update relay pool for selection
```

**Integration Points:**
- **MetadataQueryService** - Triggers callback on relay peer discovery
- **PeerDiscovery** - Calls callback when discovering relay nodes via DHT
- **RelayManager** - Receives notifications and updates relay candidate pool

**Usage Example:**
```go
// QUICPeer initialization with relay discovery callback
quicPeer := NewQUICPeer(...)
quicPeer.onRelayDiscovered = func(metadata *database.PeerMetadata) {
    relayManager.AddRelayCandidate(metadata)
    logger.Info("Discovered relay: " + metadata.PeerID)
}
```

**Benefits:**
- Automatic relay pool maintenance
- Real-time relay discovery
- Decoupled architecture (QUICPeer doesn't need direct RelayManager reference)
- Supports asynchronous discovery patterns

---

## DHT Metadata Storage (BEP44 Protocol)

### Metadata Structure

#### Full PeerMetadata
**File:** `internal/database/peer_metadata_types.go:9-31`

```go
type PeerMetadata struct {
    // Core Identity
    PeerID    string    // SHA1(public_key) - persistent Ed25519-based ID
    NodeID    string    // DHT routing node ID (ephemeral, changes on restart)
    Topic     string    // Network topic (e.g., "remote-network-mesh")
    Version   int       // Metadata version number
    Timestamp time.Time // Last update timestamp

    // Network Connectivity
    NetworkInfo NetworkInfo

    // Capabilities & Services
    Capabilities []string               // List of supported capabilities
    Extensions   map[string]interface{} // Custom extensions
    FilesCount   int                    // Count of ACTIVE DATA services
    AppsCount    int                    // Count of ACTIVE DOCKER + STANDALONE services
}
```

#### NetworkInfo Structure
**File:** `internal/database/peer_metadata_types.go:34-64`

```go
type NetworkInfo struct {
    // Public connectivity
    PublicIP   string
    PublicPort int

    // Private connectivity (NAT)
    PrivateIP   string
    PrivatePort int

    // Node characteristics
    NodeType       string   // "public" or "private"
    NATType        string   // "full_cone", "restricted", "symmetric", etc.
    SupportsUPnP   bool
    RelayEndpoints []string

    // Relay service info (if node offers relay services)
    IsRelay         bool
    RelayEndpoint   string
    RelayPricing    int    // Price per GB * 1000000 (scaled integer)
    RelayCapacity   int    // Max concurrent connections
    ReputationScore int    // Relay reputation 0-10000 (scaled)

    // NAT peer relay connection info (if using relay)
    UsingRelay     bool
    ConnectedRelay string // Relay PeerID
    RelaySessionID string
    RelayAddress   string

    // Protocol support
    Protocols []Protocol
}
```

**Why These Fields?**
- `IsRelay` - Allows NAT peers to discover relay candidates
- `UsingRelay` + `ConnectedRelay` - Other peers know how to reach NAT peer
- `RelaySessionID` - Used for message routing through relay
- `FilesCount`/`AppsCount` - Quick filtering for service capacity

---

### Storage Mechanism

#### Storage Key Calculation
**File:** `internal/p2p/dht_bep44.go:54-56`

```go
// Storage key is deterministic from public key
storageKey := keyPair.StorageKey() // SHA1(public_key)
```

**Key Properties:**
- Deterministic: Same public key = same storage location
- 20 bytes (SHA1 hash)
- Cannot be changed (tied to identity)
- Anyone can query, only owner can update

---

#### Multi-Tier Storage Strategy
**File:** `internal/p2p/dht_bep44.go:46-143`

```
BEP44Manager.PutMutable(keyPair, value, sequence)
│
├─ TIER 1: Local BEP44 Store
│   ├─ File: dht_bep44.go:66-72
│   ├─ Condition: If node has BEP44 storage enabled
│   ├─ Purpose: Fast local cache, serve queries
│   └─ localStore.Put(storageKey, value, signature, sequence)
│
├─ TIER 2: Store Peers (Direct PUT)
│   ├─ File: dht_bep44.go:74-95
│   ├─ Discovery: Query known_peers for IsStore == true
│   ├─ Target: Public peers with -s=true flag
│   ├─ Method: Direct KRPC PUT to IP:30609
│   └─ Purpose: Guaranteed storage on reliable nodes
│       └─ For each store peer:
│           ├─ Get write token via GET query
│           ├─> SendPutRequest(storePeer.IP:30609, token, value, signature)
│           └─ File: dht_bep44.go:280-308
│
├─ TIER 3: Bootstrap Nodes
│   ├─ File: dht_bep44.go:97-115
│   ├─ Target: Configured custom_bootstrap_nodes
│   ├─ Method: Direct KRPC PUT
│   ├─ Purpose: Ensure metadata reaches well-connected nodes
│   └─ Retry: Up to 2 retries with 2s delay
│       └─ File: dht_bep44.go:466-499
│
└─ TIER 4: Closest DHT Nodes
    ├─ File: dht_bep44.go:117-143
    ├─ Target: 5 closest nodes to storageKey (XOR distance)
    ├─ Method: Standard DHT PUT operation
    ├─ Purpose: Distribute across DHT for redundancy
    └─> dht.Put(ctx, storageKey, signedValue)
```

**Why Multi-Tier?**
1. **Local Store** - Instant retrieval for future queries
2. **Store Peers** - Guaranteed availability from dedicated nodes
3. **Bootstrap Nodes** - High uptime, well-connected
4. **DHT Nodes** - Decentralized redundancy

**Logs to check:**
- "Stored metadata in local BEP44 store"
- "Sending PUT to store peer: X"
- "Sending PUT to bootstrap node: X"
- "DHT PUT completed: key=X"

**Potential Issues:**
- **Store peers unreachable** - Check if public nodes are running
- **Bootstrap PUT timeout** - Increase timeout in dht_bep44.go:466
- **DHT PUT fails** - Check DHT routing table health

---

#### Bencode Serialization
**File:** `internal/database/peer_metadata_bencode.go:71-115`

**Problem:** BEP44 signature verification fails if time.Time serialized directly

**Solution:** Convert to bencode-safe structure

```go
PeerMetadata.ToBencodeSafe()
├─ File: peer_metadata_bencode.go:71
├─ Create BencodePeerMetadata struct
│   ├─ Timestamp: time.Time → int64 (Unix timestamp)
│   ├─ NetworkInfo: Convert nested time.Time fields
│   └─ All other fields copied directly
│
└─ Return BencodePeerMetadata (ready for bencode)

BencodePeerMetadata.FromBencode()
├─ File: peer_metadata_bencode.go:118
├─ Convert back to PeerMetadata
│   ├─ Timestamp: int64 → time.Time (Unix())
│   ├─ Reconstruct time.Time fields
│   └─ Return original PeerMetadata
```

**Bencode Format Example:**
```
d
  8:peer_id40:abc123def456...
  7:node_id40:xyz789uvw012...
  5:topic21:remote-network-mesh
  7:versioni1e
  9:timestampi1703001234e
  12:network_infod
    9:public_ip12:203.0.113.1
    11:public_porti30906e
    10:private_ip15:192.168.1.100
    12:private_porti30906e
    9:node_type7:private
    8:nat_type9:symmetric
    11:using_relayi1e
    15:connected_relay40:relay_peer_id_here...
    15:relay_session_id36:session-uuid-here
    13:relay_address21:1.2.3.4:5678
  e
  11:files_counti5e
  10:apps_counti3e
e
```

**Size Calculation:**
- Typical metadata: 600-900 bytes (bencode)
- With signature: +64 bytes
- With public key: +32 bytes
- Total: ~700-1000 bytes (within BEP44 limit)

**Logs to check:**
- "Bencode metadata size: X bytes"
- "Bencode serialization failed: X" (if over limit)

---

#### BEP44 Signature Process
**File:** `internal/p2p/dht_bep44.go:280-308`

```
SendPutRequest(nodeAddr, token, value, signature)
│
├─ Step 1: Get write token
│   ├─> SendGetRequest(nodeAddr, targetKey)
│   │   ├─ Send: get query with target=targetKey
│   │   ├─ Receive: token (write authorization)
│   │   └─ Timeout: 10 seconds
│   └─ File: dht_bep44.go:331-357
│
├─ Step 2: Create BEP44 Put structure
│   ├─ V: Value (raw bytes, will be bencoded by library)
│   ├─ K: Ed25519 public key (32 bytes)
│   ├─ Seq: Sequence number (int64)
│   └─ Cas: Compare-and-swap (optional, for atomic updates)
│
├─ Step 3: Sign the data
│   ├─> CreateSignature(keyPair, value, sequence)
│   │   ├─ File: dht_bep44.go:619-628
│   │   ├─ Format: "3:seqi" + seq + "e1:v" + bencoded_value
│   │   ├─> keyPair.Sign(signPayload)
│   │   │   └─ Ed25519 signature (64 bytes)
│   │   └─ Return signature
│   │
│   └─ put.Signature = signature
│
└─ Step 4: Send PUT query
    ├─ Message: put query with token, V, K, Sig, Seq
    ├─> dht.SendQuery(nodeAddr, putMessage)
    └─ Node verifies signature before accepting
```

**Signature Payload Format (BEP44 Spec):**
```
"3:seqi" + sequence_number + "e1:v" + bencoded_value

Example:
"3:seqi1e1:v" + "d8:peer_id40:...e"
```

**Why This Format?**
- Ensures signature covers both sequence and value
- Prevents replay attacks (old sequence rejected)
- Standard BEP44 format (interoperable with other implementations)

**Logs to check:**
- "Creating signature: sequence=X"
- "PUT request sent: node=X, sequence=Y"
- "PUT request failed: error=X"

**Potential Issues:**
- **Signature mismatch** - Bencode format inconsistent (check serialization)
- **Write token expired** - GET and PUT must be close together
- **Sequence number too low** - Use higher sequence than existing

---

#### Sequence Number Management
**File:** `internal/p2p/metadata_publisher.go:24-26,68-69`

```go
type MetadataPublisher struct {
    currentSequence int64  // Monotonically increasing
    // ...
}

func (mp *MetadataPublisher) PublishMetadata(metadata *database.PeerMetadata) error {
    sequence := mp.currentSequence
    mp.currentSequence++  // Increment for next publish

    // Convert to bencode-safe format
    bencodeMetadata := metadata.ToBencodeSafe()
    value := bencode.Marshal(bencodeMetadata)

    // Sign and store
    bep44Manager.PutMutable(keyPair, value, sequence)
}
```

**Sequence Number Rules:**
1. **Initial publish:** Sequence = 0
2. **Each update:** Increment sequence
3. **Periodic republish:** Same sequence (no changes)
4. **DHT conflict resolution:** Higher sequence wins

**Timeline Example:**
```
Time    Event                           Sequence
-----------------------------------------------------
0s      Initial publish                 0
300s    Relay connection update         1
600s    Periodic republish             1 (no change)
900s    IP address change              2
1200s   Periodic republish             2 (no change)
1500s   Service count update           3
```

**Logs to check:**
- "Publishing metadata: sequence=X"
- "Incrementing sequence: X → Y"
- "Periodic republish: sequence=X (unchanged)"

**Potential Issues:**
- **Sequence not incrementing** - Updates won't propagate
- **Sequence jump too large** - Check for accidental resets
- **Sequence collision** - Multiple publishers using same key (should be impossible)

---

### Update Triggers

#### Initial Publish
**File:** `internal/core/peer.go:1323,1339`

```
Peer.Start()
├─ Gather system capabilities
├─ Detect network topology (public vs NAT)
├─ Join DHT
│
├─ PUBLIC/RELAY NODES:
│   ├─ Create PeerMetadata immediately
│   │   ├─ NetworkInfo: PublicIP, NodeType="public"
│   │   ├─ IsRelay: true (if relay mode)
│   │   ├─ FilesCount, AppsCount from database
│   │   └─ Capabilities summary
│   │
│   └─> metadataPublisher.PublishMetadata(metadata)
│       ├─ File: peer.go:1323
│       ├─ Sequence: 0 (initial)
│       └─ Multi-tier PUT to DHT
│
└─ NAT NODES:
    ├─ Create PeerMetadata but DON'T publish yet
    ├─ Wait for relay connection
    └─ File: peer.go:1339 (deferred)
```

**Why Defer NAT Node Publishing?**
- Avoids incomplete metadata (UsingRelay=false, ConnectedRelay="")
- Other peers wouldn't know how to reach NAT peer
- Better to publish once with complete relay information

**Logs to check:**
- "Node type detected: public - Publishing metadata immediately"
- "Node type detected: private - Deferring metadata publish until relay connection"
- "Initial metadata published: sequence=0"

---

#### Update Trigger 1: Relay Connection
**File:** `internal/p2p/metadata_publisher.go:268-321`

```
RelayManager.ConnectToRelay(relay)
├─ Establish QUIC connection
├─ Send relay registration
├─ Receive session ID and relay address
│
└─> metadataPublisher.NotifyRelayConnected(relayPeerID, sessionID, relayAddress)
    ├─ File: metadata_publisher.go:268
    │
    ├─ Update metadata:
    │   ├─ NetworkInfo.UsingRelay = true
    │   ├─ NetworkInfo.ConnectedRelay = relayPeerID
    │   ├─ NetworkInfo.RelaySessionID = sessionID
    │   ├─ NetworkInfo.RelayAddress = relayAddress
    │   └─ Timestamp = time.Now()
    │
    ├─ Increment sequence number
    │   └─ currentSequence++
    │
    └─> PublishMetadata(updatedMetadata)
        └─ Multi-tier PUT to DHT
```

**Timeline:**
```
0s    NAT node starts
5s    Relay selected
6s    Relay connection established
6s    NotifyRelayConnected() called
7s    Metadata published to DHT (sequence=0 or 1)
```

**Logs to check:**
- "Relay connected: peer_id=X, session_id=Y"
- "Updating metadata with relay connection info"
- "Publishing relay connection metadata: sequence=X"

**Potential Issues:**
- **Metadata never published** - NotifyRelayConnected() not called
- **Stale relay info** - Old session ID not cleared on reconnection
- **Sequence not incremented** - Updates won't propagate

---

#### Update Trigger 2: Relay Disconnection
**File:** `internal/p2p/metadata_publisher.go:323-357`

```
RelayManager.handleRelayConnectionLoss()
├─ Detect keepalive timeout or stream error
├─ Attempt reconnection (4 phases)
│
└─ If all reconnection attempts fail:
    └─> metadataPublisher.NotifyRelayDisconnected()
        ├─ File: metadata_publisher.go:323
        │
        ├─ Update metadata:
        │   ├─ NetworkInfo.UsingRelay = false
        │   ├─ NetworkInfo.ConnectedRelay = ""
        │   ├─ NetworkInfo.RelaySessionID = ""
        │   ├─ NetworkInfo.RelayAddress = ""
        │   └─ Timestamp = time.Now()
        │
        ├─ Increment sequence number
        │
        └─> PublishMetadata(updatedMetadata)
```

**Why Update on Disconnection?**
- Informs other peers this NAT node is temporarily unreachable
- Prevents wasted connection attempts
- Allows monitoring systems to detect offline nodes

**Logs to check:**
- "Relay disconnected: peer_id=X"
- "Clearing relay connection metadata"
- "Publishing relay disconnection: sequence=X"

**Potential Issues:**
- **False disconnection** - Triggered during reconnection (should wait for full failure)
- **Metadata not cleared** - Old relay info persists
- **Rapid publish/unpublish** - Reconnection flapping (check relay stability)

---

#### Update Trigger 3: IP Address Change
**File:** `internal/p2p/metadata_publisher.go:359-396`

```
NetworkStateMonitor.checkForChanges()
├─ File: network_state_monitor.go:120
├─ Interval: 5 minutes (configurable)
├─ Detect changes:
│   ├─ Local IP changed
│   ├─ External IP changed
│   ├─ NAT type changed
│   └─ Local subnet changed
│
├─ If public node:
│   └─> metadataPublisher.NotifyIPChange(newPublicIP, newPrivateIP)
│       ├─ File: metadata_publisher.go:359
│       │
│       ├─ Update metadata:
│       │   ├─ NetworkInfo.PublicIP = newPublicIP
│       │   ├─ NetworkInfo.PrivateIP = newPrivateIP
│       │   └─ Timestamp = time.Now()
│       │
│       ├─ Increment sequence number
│       │
│       └─> PublishMetadata(updatedMetadata)
│
└─ If NAT node:
    └─ Trigger relay reconnection (relay will update metadata)
```

**Network Change Detection:**
```
hasChanged(oldState, newState)
├─ Compare: oldState.LocalIP vs newState.LocalIP
├─ Compare: oldState.ExternalIP vs newState.ExternalIP
├─ Compare: oldState.NATType vs newState.NATType
└─ If any different: Return true
```

**Logs to check:**
- "Network state changed: old_ip=X, new_ip=Y"
- "Notifying metadata publisher of IP change"
- "Publishing IP change metadata: sequence=X"

**Potential Issues:**
- **Frequent IP changes** - DHCP lease renewals (filter out same IP)
- **External IP flapping** - STUN server inconsistencies
- **Metadata storm** - Too many publishes (rate limit)

---

#### Update Trigger 4: Service Count Change
**File:** `internal/p2p/metadata_publisher.go:398-438`

```
ServiceManager (hypothetical)
├─ Service added/removed/status changed
│
└─> metadataPublisher.UpdateServiceMetadata()
    ├─ File: metadata_publisher.go:398
    │
    ├─ Query database:
    │   ├─ filesCount = COUNT(*) FROM services WHERE type='DATA' AND status='ACTIVE'
    │   ├─ appsCount = COUNT(*) FROM services WHERE type IN ('DOCKER','STANDALONE') AND status='ACTIVE'
    │   └─ File: (database query)
    │
    ├─ Update metadata:
    │   ├─ FilesCount = filesCount
    │   ├─ AppsCount = appsCount
    │   └─ Timestamp = time.Now()
    │
    ├─ Increment sequence number
    │
    └─> PublishMetadata(updatedMetadata)
```

**Why Publish Service Counts?**
- Enables quick filtering: "Find peers with available capacity"
- Avoids querying peers with no services
- Lightweight metric (2 integers)

**Logs to check:**
- "Service count changed: files=X, apps=Y"
- "Updating service metadata"
- "Publishing service metadata: sequence=X"

**Potential Issues:**
- **Counts not updating** - Database triggers missing
- **Incorrect counts** - Check WHERE clauses (ACTIVE status)
- **Frequent updates** - Batch service changes before publishing

---

#### Update Trigger 5: Periodic Republish
**File:** `internal/p2p/metadata_publisher.go:142-167,169-214`

```
MetadataPublisher.Start()
├─ File: metadata_publisher.go:142
│
└─ Start background goroutine:
    └─ republishLoop()
        ├─ File: metadata_publisher.go:169
        ├─ Interval: 30 minutes (metadata_republish_interval)
        │
        └─ Loop:
            ├─ Sleep(republishInterval)
            │
            ├─ Get current metadata (no changes)
            │
            ├─ Use SAME sequence number
            │   └─ No increment (not an update)
            │
            └─> bep44Manager.PutMutable(keyPair, value, currentSequence)
                └─ Multi-tier PUT to refresh DHT entries
```

**Why Periodic Republish?**
- DHT entries expire after ~1-2 hours (implementation-dependent)
- Ensures metadata remains discoverable
- Compensates for DHT churn (nodes joining/leaving)

**Republish vs Update:**
| Event | Sequence | Metadata | Purpose |
|-------|----------|----------|---------|
| **Republish** | Same | Unchanged | Keep alive in DHT |
| **Update** | Increment | Changed | Propagate new information |

**Logs to check:**
- "Periodic metadata republish: sequence=X"
- "Republish interval: 30m"
- "Skipping republish: no metadata to publish"

**Potential Issues:**
- **Republish too frequent** - DHT overhead (increase interval)
- **Republish too infrequent** - Metadata expires (decrease interval)
- **Republish with wrong sequence** - Accidentally incremented

---

### Retrieval Strategy

#### Multi-Priority Query Strategy
**File:** `internal/p2p/metadata_fetcher.go:35-89`

```
MetadataFetcher.FetchMetadata(peerPublicKey)
├─ File: metadata_fetcher.go:35
│
├─ PRIORITY 1: Local BEP44 Store
│   ├─> bep44Manager.GetFromLocalStore(storageKey)
│   │   ├─ File: dht_bep44.go:325
│   │   ├─ Latency: ~1ms
│   │   └─ If found: Return immediately
│   │
│   └─ If not found: Continue to Priority 2
│
├─ PRIORITY 2: Store Peers
│   ├─> Query known_peers for IsStore == true
│   │   └─ File: metadata_fetcher.go:45
│   │
│   ├─ For each store peer:
│   │   └─> bep44Manager.GetMutable(storePeer.Endpoint, storageKey)
│   │       ├─ Direct KRPC GET to IP:30609
│   │       ├─ Latency: ~50ms (direct connection)
│   │       └─ File: dht_bep44.go:331-357
│   │
│   └─ If found: Return, else continue
│
├─ PRIORITY 3: Relay Nodes
│   ├─ If peer is using relay (from discovery)
│   ├─> Get relay peer metadata
│   └─ Ask relay for target peer metadata
│
├─ PRIORITY 4: Bootstrap Nodes
│   ├─> For each bootstrap node:
│   │   └─> bep44Manager.GetMutable(bootstrapNode, storageKey)
│   │       ├─ Latency: ~100ms
│   │       └─ Retry: Up to 2 retries with 2s delay
│   │
│   └─ If found: Return, else continue
│
└─ PRIORITY 5: Public DHT
    └─> dht.Get(storageKey)
        ├─ Standard DHT GET (query closest nodes)
        ├─ Latency: ~200-500ms
        └─ Return best result (highest sequence)
```

**Query Optimization: In-Flight Deduplication**
**File:** `internal/p2p/metadata_query.go:66-79`

```go
type MetadataQueryManager struct {
    inFlightQueries map[string]chan *database.PeerMetadata
}

func (m *MetadataQueryManager) QueryMetadata(peerID string) (*database.PeerMetadata, error) {
    // Check if query already in progress
    if existingChan, exists := m.inFlightQueries[peerID]; exists {
        // Wait for existing query to complete
        return <-existingChan, nil
    }

    // Start new query
    resultChan := make(chan *database.PeerMetadata, 1)
    m.inFlightQueries[peerID] = resultChan

    // Fetch metadata
    metadata := fetchFromDHT(peerID)

    // Broadcast result to all waiters
    resultChan <- metadata
    delete(m.inFlightQueries, peerID)

    return metadata, nil
}
```

**Logs to check:**
- "Fetching metadata: peer_id=X"
- "Metadata found in local store: peer_id=X"
- "Querying store peer: peer_id=X, store=Y"
- "Metadata fetched from bootstrap node: X"
- "Metadata query timeout: peer_id=X"
- "In-flight query deduplication: peer_id=X (waiters: Y)"

**Potential Issues:**
- **All sources fail** - Peer never published metadata
- **Stale metadata** - Local cache not invalidated (check sequence)
- **Query storm** - Too many concurrent queries (check deduplication)
- **Timeout too short** - DHT queries need time (increase timeout)

---

#### BEP44 GET Request
**File:** `internal/p2p/dht_bep44.go:331-357`

```
BEP44Manager.GetMutable(nodeAddr, targetKey)
│
├─ Step 1: Send GET query
│   ├─ Message: get query with target=targetKey
│   ├─> dht.SendQuery(nodeAddr, getMessage)
│   └─ Timeout: 10 seconds (or 15s during DHT join)
│
├─ Step 2: Receive response
│   ├─ Fields:
│   │   ├─ V: Value (bencoded PeerMetadata)
│   │   ├─ K: Ed25519 public key (32 bytes)
│   │   ├─ Sig: Ed25519 signature (64 bytes)
│   │   ├─ Seq: Sequence number (int64)
│   │   └─ Token: Write token (for future PUT)
│   │
│   └─ File: dht_bep44.go:370-385
│
├─ Step 3: Verify signature
│   ├─> VerifyMutableSignature(V, Sig, Seq, K)
│   │   ├─ File: dht_bep44.go:606-616
│   │   ├─ Recreate signature payload: "3:seqi" + Seq + "e1:v" + V
│   │   ├─> crypto.VerifyWithPublicKey(K, payload, Sig)
│   │   └─ Return true/false
│   │
│   └─ If invalid: Reject and continue to next source
│
├─ Step 4: Decode value
│   ├─> bencode.Unmarshal(V)
│   └─> BencodePeerMetadata.FromBencode()
│       └─ Convert to PeerMetadata
│
└─ Return PeerMetadata
```

**Signature Verification Process:**
```
Signature Payload = "3:seqi" + sequence + "e1:v" + bencoded_value

Example:
"3:seqi5e1:v" + "d8:peer_id40:abc...e"

Verification:
Ed25519.Verify(publicKey, signaturePayload, signature) → true/false
```

**Logs to check:**
- "Sending GET request: target=X, node=Y"
- "GET response received: sequence=X, size=Y bytes"
- "Signature verification: peer_id=X, result=valid"
- "Signature verification failed: peer_id=X"
- "GET request timeout: target=X"

**Potential Issues:**
- **Signature verification fails** - Bencode format mismatch or tampering
- **No response** - Node offline or doesn't have data
- **Wrong data returned** - Node returned data for different key (bug)

---

### Signing and Verification

#### Cryptographic Identity Foundation
**File:** `internal/crypto/keypair.go`

```
Ed25519 KeyPair
├─ Private Key: 64 bytes (secret)
├─ Public Key: 32 bytes (shared publicly)
│
├─ Derivations:
│   ├─ PeerID = SHA1(public_key) [20 bytes]
│   ├─ Storage Key = SHA1(public_key) [20 bytes]
│   └─ DHT NodeID = Random (rotates per session)
│
└─ Properties:
    ├─ Deterministic: Same keypair = same PeerID forever
    ├─ Unforgeable: Only private key holder can sign
    └─ Verifiable: Anyone can verify with public key
```

#### Signature Creation Flow
**File:** `internal/p2p/dht_bep44.go:619-628`

```
CreateSignature(keyPair, value, sequence)
├─ Step 1: Create signature payload (BEP44 format)
│   ├─ prefix = "3:seqi"
│   ├─ seqStr = fmt.Sprintf("%d", sequence)
│   ├─ midfix = "e1:v"
│   ├─ payload = prefix + seqStr + midfix + value
│   └─ Example: "3:seqi5e1:vd8:peer_id40:...e"
│
├─ Step 2: Sign with Ed25519
│   ├─> keyPair.Sign(payload)
│   │   ├─ Uses ed25519.Sign(privateKey, payload)
│   │   └─ Returns 64-byte signature
│   │
│   └─ File: crypto/keypair.go:50-55
│
└─ Return signature (64 bytes)
```

#### Signature Verification Flow
**File:** `internal/p2p/dht_bep44.go:606-616`

```
VerifyMutableSignature(value, signature, sequence, publicKey)
├─ Step 1: Recreate signature payload
│   ├─ Must match exactly what was signed
│   ├─ payload = "3:seqi" + fmt.Sprintf("%d", sequence) + "e1:v" + value
│   └─ Any mismatch = verification failure
│
├─ Step 2: Verify Ed25519 signature
│   ├─> crypto.VerifyWithPublicKey(publicKey, payload, signature)
│   │   ├─ Uses ed25519.Verify(publicKey, payload, signature)
│   │   └─ Returns true/false
│   │
│   └─ File: crypto/keypair.go:60-65
│
└─ Return verification result
```

#### Security Guarantees

**Authenticity:**
- Only peer with private key can create valid signature
- Prevents impersonation

**Integrity:**
- Any modification to value or sequence invalidates signature
- Prevents tampering

**Non-Repudiation:**
- Signature proves peer published this data
- Cannot deny authorship

**Replay Protection:**
- Sequence number must be higher than existing
- Prevents old metadata from overwriting new

#### Verification Points

```
1. DHT PUT (Storage)
   ├─ Node receives PUT request
   ├─> Verify signature before storing
   └─ Reject if invalid

2. DHT GET (Retrieval)
   ├─ Client receives GET response
   ├─> Verify signature before using
   └─ Reject if invalid

3. Local Store (Caching)
   ├─ Before caching fetched metadata
   ├─> Verify signature
   └─ Don't cache if invalid

4. Conflict Resolution
   ├─ Multiple responses with different sequences
   ├─> Verify all signatures
   ├─ Reject invalid ones
   └─ Use highest valid sequence
```

**Logs to check:**
- "Creating signature: sequence=X"
- "Signature created: length=64 bytes"
- "Verifying signature: peer_id=X, sequence=Y"
- "Signature valid: peer_id=X"
- "Signature invalid: peer_id=X, reason=X"

**Potential Issues:**
- **Signature always fails** - Bencode format inconsistent (time.Time issue?)
- **Wrong public key used** - Verify PeerID = SHA1(publicKey)
- **Sequence mismatch** - Payload doesn't include correct sequence
- **Bencode ordering** - Keys must be sorted (spec requirement)

---

## Peer Capabilities Exchange

### Capabilities Structure

#### Full SystemCapabilities
**File:** `internal/system/capabilities.go:13-29`

```go
type SystemCapabilities struct {
    // Platform Information
    Platform          string    // "linux", "darwin", "windows"
    Architecture      string    // "amd64", "arm64", "386", "arm"
    KernelVersion     string    // e.g., "5.15.0-76-generic"

    // CPU Information
    CPUModel          string    // e.g., "Intel(R) Core(TM) i7-9750H"
    CPUCores          int       // Physical cores
    CPUThreads        int       // Logical processors (hyperthreading)

    // Memory Information
    TotalMemoryMB     int64     // Total RAM in MB
    AvailableMemoryMB int64     // Available RAM in MB

    // Disk Information
    TotalDiskMB       int64     // Total disk space in MB (data directory)
    AvailableDiskMB   int64     // Available disk space in MB

    // GPU Information
    GPUs              []GPUInfo // List of detected GPUs

    // Software Capabilities
    HasDocker         bool
    DockerVersion     string    // e.g., "24.0.5"
    HasPython         bool
    PythonVersion     string    // e.g., "3.11.4"
}
```

#### GPU Information
**File:** `internal/system/capabilities.go:31-39`

```go
type GPUInfo struct {
    Index         int       // GPU index (0, 1, 2...)
    Name          string    // e.g., "NVIDIA GeForce RTX 2060"
    Vendor        string    // "nvidia", "amd", "intel"
    MemoryMB      int64     // GPU VRAM in MB
    UUID          string    // Unique GPU identifier
    DriverVersion string    // e.g., "525.105.17"
}
```

#### Compact Summary for DHT
**File:** `internal/system/capabilities.go:44-55`

```go
type CapabilitySummary struct {
    Platform    string // "linux", "darwin", "windows" (short bencode key: "p")
    Arch        string // "amd64", "arm64" (short key: "a")
    CPUCores    int    // Short key: "c"
    MemoryGB    int    // GB instead of MB (short key: "m")
    DiskGB      int    // GB instead of MB (short key: "d")
    GPUCount    int    // Number of GPUs (short key: "g")
    GPUMemoryGB int    // Total GPU memory in GB (short key: "gm")
    GPUVendor   string // "nvidia", "amd", "intel", "mixed" (short key: "gv")
    HasDocker   bool   // Short key: "dk"
    HasPython   bool   // Short key: "py"
}
```

**Why Compact Summary?**
- BEP44 limit: ~1000 bytes total (metadata + signature)
- Full capabilities: ~500-1000 bytes (too large)
- Compact summary: ~100-150 bytes (fits easily)
- Enough for basic filtering, full details via QUIC on-demand

**Conversion Logic:**
**File:** `internal/system/capabilities.go:59-96`

```go
func (sc *SystemCapabilities) ToSummary() *CapabilitySummary {
    summary := &CapabilitySummary{
        Platform:  sc.Platform,
        Arch:      sc.Architecture,
        CPUCores:  sc.CPUCores,
        MemoryGB:  int(sc.TotalMemoryMB / 1024),   // Convert MB → GB
        DiskGB:    int(sc.AvailableDiskMB / 1024), // Available, not total
        GPUCount:  len(sc.GPUs),
        HasDocker: sc.HasDocker,
        HasPython: sc.HasPython,
    }

    // Calculate total GPU memory
    var totalGPUMemoryMB int64
    vendors := make(map[string]bool)

    for _, gpu := range sc.GPUs {
        totalGPUMemoryMB += gpu.MemoryMB
        vendors[gpu.Vendor] = true
    }

    summary.GPUMemoryGB = int(totalGPUMemoryMB / 1024)

    // Determine GPU vendor
    if len(vendors) == 0 {
        summary.GPUVendor = ""
    } else if len(vendors) == 1 {
        for vendor := range vendors {
            summary.GPUVendor = vendor // "nvidia", "amd", "intel"
        }
    } else {
        summary.GPUVendor = "mixed" // Multiple vendors
    }

    return summary
}
```

---

### Gathering Process

#### Capability Detection at Startup
**File:** `internal/system/capabilities.go:113-144`

```
GatherSystemCapabilities(dataDir)
├─ File: capabilities.go:113
│
├─ STEP 1: Platform Detection
│   ├─> runtime.GOOS → Platform
│   ├─> runtime.GOARCH → Architecture
│   └─> getKernelVersion() → KernelVersion
│       ├─ Linux: uname -r
│       ├─ Darwin: uname -r
│       └─ Windows: ver command
│
├─ STEP 2: CPU Detection
│   ├─> getCPUInfo() → CPUModel, CPUCores, CPUThreads
│   │   ├─ Linux: /proc/cpuinfo
│   │   ├─ Darwin: sysctl -n machdep.cpu.brand_string
│   │   └─ Windows: WMIC cpu get name
│   │
│   └─ File: capabilities.go:200-250
│
├─ STEP 3: Memory Detection
│   ├─> getMemoryInfo() → TotalMemoryMB, AvailableMemoryMB
│   │   ├─ Linux: /proc/meminfo (MemTotal, MemAvailable)
│   │   ├─ Darwin: sysctl -n hw.memsize
│   │   └─ Windows: WMIC OS get TotalVisibleMemorySize
│   │
│   └─ File: capabilities.go:252-290
│
├─ STEP 4: Disk Detection
│   ├─> getDiskInfo(dataDir) → TotalDiskMB, AvailableDiskMB
│   │   ├─ Cross-platform: syscall.Statfs(dataDir)
│   │   ├─ Measures disk where data directory resides
│   │   └─ Returns total and available space
│   │
│   └─ File: capabilities.go:292-320
│
├─ STEP 5: GPU Detection
│   ├─> detectGPUs() → []GPUInfo
│   │   ├─ NVIDIA: nvidia-smi --query-gpu=... --format=csv
│   │   │   └─ Query: index, name, memory.total, uuid, driver_version
│   │   ├─ AMD: rocm-smi (future implementation)
│   │   ├─ Intel: intel_gpu_top (future implementation)
│   │   └─ Parse output into GPUInfo structs
│   │
│   └─ File: capabilities.go:322-400
│
├─ STEP 6: Docker Detection
│   ├─> detectDocker() → HasDocker, DockerVersion
│   │   ├─ Command: docker version --format '{{.Server.Version}}'
│   │   ├─ Success: HasDocker=true, parse version
│   │   └─ Failure: HasDocker=false
│   │
│   └─ File: capabilities.go:402-420
│
└─ STEP 7: Python Detection
    ├─> detectPython() → HasPython, PythonVersion
    │   ├─ Command: python3 --version
    │   ├─ Success: HasPython=true, parse version
    │   └─ Failure: HasPython=false
    │
    └─ File: capabilities.go:422-436
```

**NVIDIA GPU Detection Example:**
```bash
$ nvidia-smi --query-gpu=index,name,memory.total,uuid,driver_version --format=csv,noheader

0, NVIDIA GeForce RTX 2060, 6144 MiB, GPU-abc123, 525.105.17
1, NVIDIA GeForce GTX 1080, 8192 MiB, GPU-def456, 525.105.17
```

**Parsing Output:**
```go
for _, line := range strings.Split(output, "\n") {
    parts := strings.Split(line, ",")
    gpu := GPUInfo{
        Index:         parseInt(parts[0]),
        Name:          strings.TrimSpace(parts[1]),
        MemoryMB:      parseMemory(parts[2]), // "6144 MiB" → 6144
        UUID:          strings.TrimSpace(parts[3]),
        DriverVersion: strings.TrimSpace(parts[4]),
        Vendor:        "nvidia",
    }
    gpus = append(gpus, gpu)
}
```

**Logs to check:**
- "Gathering system capabilities"
- "Platform detected: linux/amd64"
- "CPU detected: Intel Core i7, cores=6, threads=12"
- "Memory detected: total=16384MB, available=8192MB"
- "Disk detected: total=512000MB, available=102400MB"
- "GPU detected: NVIDIA GeForce RTX 2060, memory=6144MB"
- "Docker detected: version=24.0.5"
- "Python detected: version=3.11.4"
- "Capabilities gathering complete"

**Potential Issues:**
- **GPU not detected** - nvidia-smi not in PATH or driver missing
- **Memory detection fails** - /proc/meminfo missing (non-Linux)
- **Docker version parse error** - Unexpected format (check regex)
- **Platform-specific commands fail** - Cross-platform edge cases

---

### Exchange Protocols

#### Phase 1: Identity Exchange (Handshake)
**File:** `internal/p2p/identity_exchange.go:74-96`

```
QUICPeer.ConnectToPeer(endpoint)
├─ Establish QUIC connection (mTLS with Ed25519)
├─> OpenStreamSync() for handshake
│
└─> PerformHandshake(stream, topic, remoteAddr)
    ├─ File: identity_exchange.go:74
    │
    ├─ Send: MessageTypeIdentity
    │   ├─ PeerID (SHA1 of public key)
    │   ├─ DHTNodeID (current DHT routing ID)
    │   ├─ PublicKey (Ed25519, 32 bytes)
    │   ├─ Topic ("remote-network-mesh")
    │   ├─ NodeType ("public" or "private")
    │   ├─ IsRelay (bool)
    │   └─ IsStore (bool)
    │
    ├─ Receive: MessageTypeIdentity (from remote)
    │
    ├─ Verify: PeerID == SHA1(PublicKey)
    │   └─ Prevents impersonation
    │
    └─ Store in known_peers (in-memory)
```

**Why NOT Include Capabilities in Handshake?**
- Handshake must be fast (<100ms)
- Full capabilities too large (500-1000 bytes)
- Not always needed (most connections don't check capabilities)
- On-demand fetching more efficient

**Logs to check:**
- "Performing identity exchange with: X"
- "Identity exchange successful: peer_id=X, is_relay=Y"
- "Identity verification failed: peer_id != SHA1(public_key)"

---

#### Phase 2: On-Demand Capabilities Request
**File:** `internal/p2p/capabilities_handler.go:33-46`

```
Peer A (Requester)                          Peer B (Responder)
    |                                             |
    |--QUIC connection established----------------|
    |                                             |
    |--MessageTypeCapabilitiesRequest------------>|
    |                                             |
    |                                             |--Load capabilities from cache
    |                                             |  (gathered at startup)
    |                                             |
    |<-MessageTypeCapabilitiesResponse------------|
    |  (Full SystemCapabilities)                  |
    |                                             |
    |--Parse and validate-------------------------|
    |                                             |

Handler on Peer B:
HandleCapabilitiesRequest(msg, remoteAddr)
├─ File: capabilities_handler.go:33
│
├─ Validate request (check rate limits, auth)
│
├─ Load capabilities from cache
│   └─ handler.capabilities (set at startup)
│
├─ Serialize to JSON
│   └─ json.Marshal(capabilities)
│
└─> Send MessageTypeCapabilitiesResponse
    ├─ Payload: JSON-encoded SystemCapabilities
    └─ Write to response stream
```

**Message Format:**
```
Request:
{
  "type": "capabilities_request",
  "timestamp": 1703001234
}

Response:
{
  "type": "capabilities_response",
  "capabilities": {
    "platform": "linux",
    "architecture": "amd64",
    "kernel_version": "5.15.0",
    "cpu_model": "Intel Core i7-9750H",
    "cpu_cores": 6,
    "cpu_threads": 12,
    "total_memory_mb": 16384,
    "available_memory_mb": 8192,
    "total_disk_mb": 512000,
    "available_disk_mb": 102400,
    "gpus": [{
      "index": 0,
      "name": "NVIDIA GeForce RTX 2060",
      "vendor": "nvidia",
      "memory_mb": 6144,
      "uuid": "GPU-abc123",
      "driver_version": "525.105.17"
    }],
    "has_docker": true,
    "docker_version": "24.0.5",
    "has_python": true,
    "python_version": "3.11.4"
  }
}
```

**Logs to check:**
- "Sending capabilities request to: peer_id=X"
- "Received capabilities request from: peer_id=X"
- "Sending capabilities response: size=X bytes"
- "Received capabilities response: peer_id=X"
- "Capabilities request timeout: peer_id=X"

**Potential Issues:**
- **Request timeout** - Peer offline or overloaded
- **Capabilities not cached** - GatherSystemCapabilities() not called
- **Parse error** - JSON format mismatch (version compatibility)
- **Rate limiting** - Too many requests (implement backoff)

---

### Storage Mechanisms

#### Storage Comparison

| Storage Location | Data Type | Size Limit | Latency | Lifetime | Purpose |
|------------------|-----------|------------|---------|----------|---------|
| **DHT (BEP44)** | CapabilitySummary | ~100-150 bytes | 50-500ms | Republish required (30m) | Discovery & filtering |
| **In-Memory Cache** | Full SystemCapabilities | ~500-1000 bytes | <1ms | Process lifetime | Fast QUIC responses |
| **Known Peers DB** | Minimal (PeerID, IsRelay) | ~100 bytes | <1ms | Session lifetime | Connection tracking |

#### In-Memory Capabilities Cache
**File:** `internal/p2p/capabilities_handler.go:20-25`

```go
type CapabilitiesHandler struct {
    capabilities *system.SystemCapabilities // Cached at startup
    peer         *QUICPeer                  // QUIC connection manager
}

func NewCapabilitiesHandler(peer *QUICPeer) *CapabilitiesHandler {
    return &CapabilitiesHandler{
        capabilities: system.GatherSystemCapabilities(dataDir),
        peer:         peer,
    }
}
```

**Update Mechanism:**
```go
func (h *CapabilitiesHandler) UpdateCapabilities(newCaps *system.SystemCapabilities) {
    h.mu.Lock()
    defer h.mu.Unlock()

    h.capabilities = newCaps
    // Optionally trigger metadata republish
}
```

**When to Update:**
- Manual trigger (admin command)
- Resource monitoring detects changes (future)
- Service status changes (GPU added/removed)

---

### Usage in Peer Selection

#### Service Matching Algorithm
**File:** `internal/system/capabilities.go:438-513`

```
CanExecuteService(peerCaps, serviceCaps) → (bool, string)
├─ File: capabilities.go:438
│
├─ CHECK 1: Platform Compatibility
│   ├─ If serviceCaps.Platform == "any": Allow all
│   ├─ Else: peerCaps.Platform == serviceCaps.Platform
│   └─ Failure: "incompatible platform: need X, have Y"
│
├─ CHECK 2: Architecture Compatibility
│   ├─ If serviceCaps.Architecture == "any": Allow all
│   ├─ Cross-architecture matrix:
│   │   ├─ amd64 container → amd64 host ✓
│   │   ├─ arm64 container → arm64 host ✓
│   │   ├─ amd64 container → arm64 host (if Docker emulation) ✓
│   │   └─ arm64 container → amd64 host (if Docker emulation) ✓
│   └─ Failure: "incompatible architecture: need X, have Y"
│
├─ CHECK 3: Memory Requirements
│   ├─ peerCaps.AvailableMemoryMB >= serviceCaps.MinMemoryMB
│   └─ Failure: "insufficient memory: need XMB, have YMB available"
│
├─ CHECK 4: CPU Requirements
│   ├─ peerCaps.CPUCores >= serviceCaps.MinCPUCores
│   └─ Failure: "insufficient CPU cores: need X, have Y"
│
├─ CHECK 5: Disk Requirements
│   ├─ peerCaps.AvailableDiskMB >= serviceCaps.MinDiskMB
│   └─ Failure: "insufficient disk: need XMB, have YMB available"
│
├─ CHECK 6: GPU Requirements
│   ├─ If serviceCaps.RequiresGPU:
│   │   ├─ len(peerCaps.GPUs) > 0
│   │   └─ Failure: "service requires GPU, peer has none"
│   │
│   ├─ If serviceCaps.GPUVendor specified:
│   │   ├─ Find GPU with matching vendor
│   │   └─ Failure: "service requires X GPU, peer has Y"
│   │
│   └─ If serviceCaps.MinGPUMemoryMB specified:
│       ├─ Find GPU with sufficient memory
│       └─ Failure: "insufficient GPU memory: need XMB, max available YMB"
│
├─ CHECK 7: Docker Requirements
│   ├─ If serviceCaps.RequiresDocker:
│   │   ├─ peerCaps.HasDocker == true
│   │   └─ Failure: "service requires Docker, peer doesn't have it"
│   │
│   └─ If serviceCaps.MinDockerVersion specified:
│       ├─ Compare versions (semantic versioning)
│       └─ Failure: "Docker version too old: need X, have Y"
│
└─ CHECK 8: Python Requirements
    ├─ If serviceCaps.RequiresPython:
    │   ├─ peerCaps.HasPython == true
    │   └─ Failure: "service requires Python, peer doesn't have it"
    │
    └─ If serviceCaps.MinPythonVersion specified:
        ├─ Compare versions
        └─ Failure: "Python version too old: need X, have Y"
```

#### Peer Selection Flow
```
JobScheduler.FindSuitablePeer(jobID)
│
├─ PHASE 1: Get job requirements
│   └─ serviceCaps = GetServiceCapabilities(jobID)
│
├─ PHASE 2: Query DHT for peers
│   ├─> DiscoverPeers() → List of peer metadata
│   └─ Filter: FilesCount > 0 OR AppsCount > 0 (has capacity)
│
├─ PHASE 3: Coarse filtering (DHT metadata)
│   ├─ For each peer metadata:
│   │   ├─ Check CapabilitySummary (if available in metadata)
│   │   ├─ Platform matches?
│   │   ├─ Architecture compatible?
│   │   ├─ Enough memory (rough estimate)?
│   │   └─ GPU required and GPUCount > 0?
│   │
│   └─ candidates = peers passing coarse filter
│
├─ PHASE 4: Establish QUIC connections
│   └─ For each candidate:
│       └─> ConnectToPeer(candidate.Endpoint)
│
├─ PHASE 5: Request full capabilities
│   └─ For each connected candidate:
│       └─> SendCapabilitiesRequest()
│           └─ Receive full SystemCapabilities
│
├─ PHASE 6: Fine filtering (full capabilities)
│   └─ For each candidate:
│       └─> CanExecuteService(candidateCaps, serviceCaps)
│           ├─ Returns: (bool, reason)
│           └─ If true: Add to suitable_peers
│
├─ PHASE 7: Ranking (if multiple suitable peers)
│   ├─ Score factors:
│   │   ├─ Available resources (memory, CPU, GPU)
│   │   ├─ Latency (from ping)
│   │   ├─ Reputation (from DHT metadata)
│   │   └─ Load (current running jobs)
│   │
│   └─ Select highest scoring peer
│
└─ Return selected peer
```

**Logs to check:**
- "Finding suitable peer for job: X"
- "DHT query returned Y peers"
- "Coarse filter: X/Y peers passed"
- "Requesting capabilities from: peer_id=X"
- "Peer filtered out: peer_id=X, reason=Y"
- "Suitable peers found: X"
- "Selected peer: peer_id=X, score=Y"

**Potential Issues:**
- **No suitable peers** - Requirements too strict or peers offline
- **Capabilities mismatch after selection** - Resources consumed between check and execution
- **Stale metadata** - Peer capabilities changed (republish delay)
- **Over-filtering** - Requirements stricter than necessary

---

## Complete Data Flows

### Flow 1: Public Node Startup → Metadata Publish

```
1. Node starts
   └─ File: cmd/main.go

2. Initialize components
   └─> peer.Start()
       └─ File: internal/core/peer.go:150

3. Gather capabilities
   └─> capabilities := system.GatherSystemCapabilities(dataDir)
       ├─ Detect platform, CPU, memory, disk, GPUs
       ├─ Check Docker, Python
       └─ File: internal/system/capabilities.go:113

4. Detect network topology
   └─> natType := natDetector.DetectNATType()
       ├─ STUN queries determine NAT type
       ├─ Result: "none" (public node)
       └─ File: internal/p2p/nat_detector.go:50

5. Join DHT
   └─> dht.Bootstrap(bootstrapNodes)
       └─ File: internal/p2p/dht.go:100

6. Create PeerMetadata
   ├─ PeerID: SHA1(publicKey)
   ├─ NodeID: dht.NodeID()
   ├─ NetworkInfo:
   │   ├─ PublicIP: detected external IP
   │   ├─ PublicPort: 30906
   │   ├─ NodeType: "public"
   │   ├─ IsRelay: true (if -r flag)
   │   └─ IsStore: true (if -s flag)
   ├─ FilesCount: query database
   ├─ AppsCount: query database
   └─ Capabilities: capabilities.ToSummary()

7. Convert to bencode-safe format
   └─> bencodeMetadata := metadata.ToBencodeSafe()
       ├─ time.Time → int64 (Unix timestamp)
       └─ File: internal/database/peer_metadata_bencode.go:71

8. Publish to DHT
   └─> metadataPublisher.PublishMetadata(metadata)
       ├─ File: internal/p2p/metadata_publisher.go:68
       ├─ Sequence: 0 (initial publish)
       │
       └─> bep44Manager.PutMutable(keyPair, value, 0)
           ├─ Tier 1: Local store
           ├─ Tier 2: Store peers (direct PUT)
           ├─ Tier 3: Bootstrap nodes
           └─ Tier 4: Closest DHT nodes
           └─ File: internal/p2p/dht_bep44.go:46

9. Start periodic republish
   └─> metadataPublisher.Start()
       ├─ Interval: 30 minutes
       └─ File: internal/p2p/metadata_publisher.go:142

Total time: ~2-3 seconds
```

---

### Flow 2: NAT Node → Relay Connection → Metadata Update

```
1. Node starts (NAT detected)
   └─ NodeType: "private"

2. Create metadata but DON'T publish
   ├─ UsingRelay: false
   ├─ ConnectedRelay: ""
   └─ Stored locally (not in DHT yet)

3. Discover relay candidates
   └─> Query DHT for IsRelay == true peers
       └─ File: internal/p2p/relay_manager.go:1100

4. Select best relay
   └─> relaySelector.SelectBestRelay()
       ├─ Measure latency
       ├─ Calculate scores
       └─ Return best relay
       └─ File: internal/p2p/relay_selector.go:300

5. Connect to relay
   └─> relayManager.ConnectToRelay(selectedRelay)
       ├─ Establish QUIC connection
       ├─ Send relay_register
       ├─ Receive relay_accept (sessionID, relayAddress)
       └─ File: internal/p2p/relay_manager.go:400

6. Update metadata with relay info
   └─> metadataPublisher.NotifyRelayConnected(relayPeerID, sessionID, relayAddress)
       ├─ File: internal/p2p/metadata_publisher.go:268
       ├─ Update fields:
       │   ├─ UsingRelay: true
       │   ├─ ConnectedRelay: relayPeerID
       │   ├─ RelaySessionID: sessionID
       │   └─ RelayAddress: relayAddress
       └─ Increment sequence: 0 → 1

7. First publish to DHT (NOW with complete info)
   └─> metadataPublisher.PublishMetadata(updatedMetadata)
       ├─ Sequence: 1 (or 0 if first publish)
       └─> Multi-tier PUT
           └─ File: internal/p2p/dht_bep44.go:46

8. Start periodic republish
   └─ Interval: 30 minutes

Total time: ~8-10 seconds (including relay selection + connection)
```

---

### Flow 3: Peer Discovery → Capability Validation → Job Assignment

```
1. Requester needs to execute job
   └─ Job requirements:
       ├─ Platform: linux
       ├─ MinMemoryMB: 4096
       ├─ MinCPUCores: 2
       ├─ RequiresGPU: true
       ├─ GPUVendor: nvidia
       └─ MinGPUMemoryMB: 4096

2. Query DHT for peers
   └─> Periodic discovery or explicit query
       ├─ Get list of known peer IDs
       └─ For each peer:
           └─> metadataFetcher.FetchMetadata(peerPublicKey)
               ├─ Multi-priority query (local → store → bootstrap → DHT)
               └─ Returns: PeerMetadata
               └─ File: internal/p2p/metadata_fetcher.go:35

3. Coarse filtering (DHT metadata)
   ├─ For each peer metadata:
   │   ├─ Check: FilesCount > 0 OR AppsCount > 0
   │   ├─ Check: CapabilitySummary (if available)
   │   │   ├─ Platform == "linux" ✓
   │   │   ├─ MemoryGB >= 4 ✓ (4096MB = 4GB)
   │   │   ├─ GPUCount > 0 ✓
   │   │   └─ GPUVendor == "nvidia" ✓
   │   └─ Add to candidates if passed
   │
   └─ Result: 5 candidates

4. Establish QUIC connections
   └─ For each candidate (in parallel):
       └─> quicPeer.ConnectToPeer(candidate.Endpoint)
           ├─ Identity exchange
           └─ File: internal/p2p/quic.go:200

5. Request full capabilities
   └─ For each connected candidate:
       └─> SendCapabilitiesRequest()
           ├─ File: internal/p2p/capabilities_handler.go:XX
           ├─ Receive: MessageTypeCapabilitiesResponse
           └─ Parse: Full SystemCapabilities

6. Fine filtering (full capabilities)
   └─ For each candidate:
       └─> CanExecuteService(candidateCaps, serviceCaps)
           ├─ File: internal/system/capabilities.go:438
           │
           ├─ Platform: linux == linux ✓
           ├─ Memory: 8192MB >= 4096MB ✓
           ├─ CPU: 6 cores >= 2 cores ✓
           ├─ GPU: 1 GPU available ✓
           ├─ GPU Vendor: nvidia == nvidia ✓
           ├─ GPU Memory: 6144MB >= 4096MB ✓
           └─ Result: (true, "")

7. Ranking suitable peers
   ├─ Candidate A: score = 0.85 (8GB RAM, 50ms latency)
   ├─ Candidate B: score = 0.92 (16GB RAM, 30ms latency) ← BEST
   └─ Candidate C: score = 0.78 (8GB RAM, 80ms latency)

8. Select best peer
   └─ Selected: Candidate B

9. Assign job to peer
   └─> SendJobRequest(candidateB, jobData)
       └─ File: internal/p2p/job_handler.go

Total time: ~500ms-2s (DHT query + QUIC connections + capability checks)
```

---

### Flow 4: Capability Update → Metadata Republish

```
1. System change detected
   └─ Example: GPU driver updated

2. Re-gather capabilities
   └─> capabilities := system.GatherSystemCapabilities(dataDir)
       ├─ Detects new driver version
       └─ File: internal/system/capabilities.go:113

3. Update in-memory cache
   └─> capabilitiesHandler.UpdateCapabilities(capabilities)
       └─ File: internal/p2p/capabilities_handler.go:48

4. (Optional) Update compact summary in metadata
   └─> metadata.Capabilities = capabilities.ToSummary()

5. Increment sequence and publish
   └─> metadataPublisher.UpdateServiceMetadata()
       ├─ Increment: sequence++
       └─> PublishMetadata(updatedMetadata)
           └─ Multi-tier PUT
           └─ File: internal/p2p/metadata_publisher.go:398

6. Future capability requests return new data
   └─ QUIC CapabilitiesRequest returns updated driver version

Total time: ~2-3 seconds (capability gathering + DHT publish)
```

---

## Key Data Structures

### PeerMetadata (Bencode-Safe)
**File:** `internal/database/peer_metadata_bencode.go:12-37`

```go
type BencodePeerMetadata struct {
    PeerID    string `bencode:"peer_id"`
    NodeID    string `bencode:"node_id"`
    Topic     string `bencode:"topic"`
    Version   int    `bencode:"version"`
    Timestamp int64  `bencode:"timestamp"` // Unix timestamp (not time.Time!)

    NetworkInfo struct {
        PublicIP       string `bencode:"public_ip"`
        PublicPort     int    `bencode:"public_port"`
        PrivateIP      string `bencode:"private_ip"`
        PrivatePort    int    `bencode:"private_port"`
        NodeType       string `bencode:"node_type"`
        NATType        string `bencode:"nat_type"`
        IsRelay        bool   `bencode:"is_relay"`
        RelayEndpoint  string `bencode:"relay_endpoint,omitempty"`
        UsingRelay     bool   `bencode:"using_relay"`
        ConnectedRelay string `bencode:"connected_relay,omitempty"`
        RelaySessionID string `bencode:"relay_session_id,omitempty"`
        RelayAddress   string `bencode:"relay_address,omitempty"`
    } `bencode:"network_info"`

    Capabilities []string `bencode:"capabilities,omitempty"`
    FilesCount   int      `bencode:"files_count"`
    AppsCount    int      `bencode:"apps_count"`
}
```

### SystemCapabilities (Full)
**File:** `internal/system/capabilities.go:13-29`

```go
type SystemCapabilities struct {
    Platform          string    `json:"platform"`
    Architecture      string    `json:"architecture"`
    KernelVersion     string    `json:"kernel_version"`
    CPUModel          string    `json:"cpu_model"`
    CPUCores          int       `json:"cpu_cores"`
    CPUThreads        int       `json:"cpu_threads"`
    TotalMemoryMB     int64     `json:"total_memory_mb"`
    AvailableMemoryMB int64     `json:"available_memory_mb"`
    TotalDiskMB       int64     `json:"total_disk_mb"`
    AvailableDiskMB   int64     `json:"available_disk_mb"`
    GPUs              []GPUInfo `json:"gpus"`
    HasDocker         bool      `json:"has_docker"`
    DockerVersion     string    `json:"docker_version"`
    HasPython         bool      `json:"has_python"`
    PythonVersion     string    `json:"python_version"`
}
```

### CapabilitySummary (Compact for DHT)
**File:** `internal/system/capabilities.go:44-55`

```go
type CapabilitySummary struct {
    Platform    string `bencode:"p"`  // Short keys for size
    Arch        string `bencode:"a"`
    CPUCores    int    `bencode:"c"`
    MemoryGB    int    `bencode:"m"`
    DiskGB      int    `bencode:"d"`
    GPUCount    int    `bencode:"g"`
    GPUMemoryGB int    `bencode:"gm"`
    GPUVendor   string `bencode:"gv"`
    HasDocker   bool   `bencode:"dk"`
    HasPython   bool   `bencode:"py"`
}
```

---

## Debugging Guide

### Common Issues and Solutions

#### Issue 1: Metadata Never Published (NAT Node)

**Symptoms:**
- NAT node starts but not discoverable
- DHT queries for peer return empty
- No "Publishing metadata" logs

**Investigation:**
```bash
# Check if relay connection established
grep "Relay accepted registration" logs/peer.log
grep "NotifyRelayConnected" logs/peer.log

# Check if metadata publish attempted
grep "Publishing metadata" logs/peer.log
grep "PutMutable" logs/peer.log
```

**Common Causes:**
1. **Relay connection never established** - Check relay selection
2. **NotifyRelayConnected() not called** - Check relay_manager.go:400-500
3. **Publish silently failed** - Check DHT connectivity

**Fix Locations:**
- `internal/p2p/relay_manager.go:480` - Verify NotifyRelayConnected() called after registration
- `internal/p2p/metadata_publisher.go:268` - Add logging for relay connection notification
- `internal/core/peer.go:1339` - Verify deferred publish triggered

---

#### Issue 2: Signature Verification Fails

**Symptoms:**
- "Signature verification failed" errors
- DHT GET succeeds but data rejected
- Other peers can't query your metadata

**Investigation:**
```bash
# Check signature creation
grep "Creating signature" logs/peer.log

# Check verification failures
grep "Signature verification failed" logs/peer.log
grep "invalid signature" logs/peer.log

# Check bencode format
grep "Bencode metadata size" logs/peer.log
```

**Common Causes:**
1. **time.Time not converted to Unix timestamp** - BencodePeerMetadata issue
2. **Bencode key ordering wrong** - Must be sorted alphabetically
3. **Sequence number mismatch** - Payload doesn't include seq in signature

**Fix Locations:**
- `internal/database/peer_metadata_bencode.go:71` - Ensure all time.Time converted
- `internal/p2p/dht_bep44.go:619` - Verify signature payload format
- `internal/p2p/dht_bep44.go:606` - Add detailed verification logging

**Verification Test:**
```go
// Manual signature test
func TestSignatureVerification(t *testing.T) {
    metadata := createTestMetadata()
    bencodeMetadata := metadata.ToBencodeSafe()
    value := bencode.Marshal(bencodeMetadata)

    signature := CreateSignature(keyPair, value, 0)
    valid := VerifyMutableSignature(value, signature, 0, keyPair.PublicKey())

    assert.True(t, valid, "Signature should be valid")
}
```

---

#### Issue 3: Metadata Size Exceeds BEP44 Limit

**Symptoms:**
- "Metadata too large" errors
- DHT PUT rejected
- Bencode size >1000 bytes

**Investigation:**
```bash
# Check metadata size
grep "Bencode metadata size" logs/peer.log

# Check field sizes
grep "peer_id length" logs/peer.log
grep "capabilities count" logs/peer.log
```

**Common Causes:**
1. **Too many capabilities** - Capabilities array too large
2. **Long relay endpoint strings** - IPv6 addresses are longer
3. **Extensions map too large** - Custom extensions bloat

**Fix Locations:**
- `internal/database/peer_metadata_bencode.go:71` - Add size calculation
- `internal/system/capabilities.go:59` - Use more compact summary
- `internal/p2p/metadata_publisher.go:68` - Add size validation before publish

**Size Optimization:**
```go
// Use short field names in bencode
type CompactMetadata struct {
    P  string `bencode:"p"`  // peer_id
    N  string `bencode:"n"`  // node_id
    T  int64  `bencode:"t"`  // timestamp
    NI struct {
        PI string `bencode:"pi"` // public_ip
        PP int    `bencode:"pp"` // public_port
        // ... other fields
    } `bencode:"ni"`
}
```

---

#### Issue 4: Stale Capabilities Returned

**Symptoms:**
- Peer selected but fails to execute job
- Capabilities mismatch between DHT and QUIC
- "Insufficient memory" errors despite claiming enough

**Investigation:**
```bash
# Check capability gathering
grep "Gathering system capabilities" logs/peer.log
grep "Capabilities gathering complete" logs/peer.log

# Check updates
grep "UpdateCapabilities" logs/peer.log
grep "UpdateServiceMetadata" logs/peer.log

# Compare timestamps
grep "metadata timestamp" logs/peer.log
```

**Common Causes:**
1. **Capabilities cached at startup, never updated** - No monitoring
2. **Resources consumed between check and execution** - Race condition
3. **Metadata not republished after update** - Sequence not incremented

**Fix Locations:**
- `internal/system/capabilities.go:113` - Add periodic re-gathering
- `internal/p2p/capabilities_handler.go:48` - Trigger metadata publish on update
- `internal/p2p/metadata_publisher.go:398` - Ensure UpdateServiceMetadata() called

**Resource Monitoring (Future):**
```go
// Periodically update available resources
func (c *CapabilitiesHandler) StartResourceMonitoring() {
    ticker := time.NewTicker(5 * time.Minute)
    for range ticker.C {
        newCaps := system.GatherSystemCapabilities(dataDir)
        if c.capabilitiesChanged(c.capabilities, newCaps) {
            c.UpdateCapabilities(newCaps)
            metadataPublisher.UpdateServiceMetadata()
        }
    }
}
```

---

#### Issue 5: No Suitable Peers Found

**Symptoms:**
- Job scheduler can't find peers
- "No suitable peers" errors
- All peers filtered out

**Investigation:**
```bash
# Check DHT query results
grep "DHT query returned" logs/peer.log

# Check filtering reasons
grep "Peer filtered out" logs/peer.log
grep "reason=" logs/peer.log

# Check capability comparisons
grep "CanExecuteService" logs/peer.log
```

**Common Causes:**
1. **Requirements too strict** - Asking for 32 cores when peers have 8
2. **Platform mismatch** - Requiring linux but only darwin peers available
3. **GPU requirements not met** - Asking for nvidia but peers have amd
4. **Peers offline** - DHT metadata stale, peers actually offline

**Fix Locations:**
- `internal/system/capabilities.go:438` - Add detailed logging for each check
- Job scheduler - Relax requirements or show which requirement failing
- `internal/p2p/metadata_fetcher.go:35` - Verify metadata freshness

**Debugging Query:**
```sql
-- Check what capabilities peers advertise
SELECT peer_id, platform, cpu_cores, memory_gb, gpu_count
FROM peer_metadata
WHERE is_active = true;

-- Compare with job requirements
SELECT * FROM job_requirements WHERE job_id = 'X';
```

---

### Log Correlation Examples

#### Successful Metadata Publish (Public Node)
```
[INFO] Starting peer node
[INFO] Gathering system capabilities
[INFO] Platform detected: linux/amd64
[INFO] CPU detected: Intel Core i7, cores=6, threads=12
[INFO] Memory detected: total=16384MB, available=8192MB
[INFO] GPU detected: NVIDIA GeForce RTX 2060, memory=6144MB
[INFO] Docker detected: version=24.0.5
[INFO] Capabilities gathering complete
[INFO] Detecting NAT type
[INFO] NAT type detected: none (public node)
[INFO] Joining DHT
[INFO] DHT bootstrap complete
[INFO] Creating peer metadata
[INFO] Node type: public - publishing metadata immediately
[INFO] Converting metadata to bencode-safe format
[INFO] Bencode metadata size: 742 bytes
[INFO] Creating signature: sequence=0
[INFO] Publishing metadata: sequence=0
[INFO] Stored in local BEP44 store
[INFO] Sending PUT to store peer: abc123
[INFO] Sending PUT to bootstrap node: def456
[INFO] DHT PUT completed: 5 nodes responded
[INFO] Metadata publish successful
[INFO] Starting periodic republish: interval=30m
```

**Timeline:** ~3 seconds

---

#### Successful Relay Connection + Metadata Update (NAT Node)
```
[INFO] Starting peer node
[INFO] Gathering system capabilities
[INFO] Capabilities gathering complete
[INFO] Detecting NAT type
[INFO] NAT type detected: symmetric
[INFO] Node type: private - deferring metadata publish
[INFO] Discovering relay candidates
[INFO] Discovered relay candidate: peer_id=relay1, endpoint=1.2.3.4:5678
[INFO] Measuring latency for relay: relay1
[INFO] Latency measured: relay1, latency=50ms
[INFO] Selected relay: peer_id=relay1, score=0.76
[INFO] Connecting to relay: relay1
[INFO] QUIC connection established
[INFO] Sent relay registration
[INFO] Relay accepted registration: session_id=xyz789
[INFO] NotifyRelayConnected: peer=relay1, session=xyz789
[INFO] Updating metadata with relay connection info
[INFO] Publishing metadata: sequence=0
[INFO] Bencode metadata size: 810 bytes
[INFO] Creating signature: sequence=0
[INFO] Metadata publish successful
[INFO] Starting periodic republish: interval=30m
```

**Timeline:** ~10 seconds

---

#### Successful Peer Discovery + Job Assignment
```
[INFO] Job received: job_id=job123, requires GPU
[INFO] Querying DHT for peers
[INFO] DHT query returned 15 peers
[INFO] Coarse filtering: platform, memory, GPU
[INFO] Coarse filter passed: 5/15 peers
[INFO] Establishing QUIC connections to candidates
[INFO] Connected to: peer1
[INFO] Connected to: peer2
[INFO] Connected to: peer3
[INFO] Connection failed: peer4 (timeout)
[INFO] Connected to: peer5
[INFO] Requesting capabilities from: peer1
[INFO] Requesting capabilities from: peer2
[INFO] Requesting capabilities from: peer3
[INFO] Requesting capabilities from: peer5
[INFO] Received capabilities: peer1
[INFO] Fine filtering: peer1
[INFO] Peer passed all checks: peer1
[INFO] Received capabilities: peer2
[INFO] Fine filtering: peer2
[INFO] Peer filtered out: peer2, reason=insufficient GPU memory
[INFO] Received capabilities: peer3
[INFO] Peer passed all checks: peer3
[INFO] Received capabilities: peer5
[INFO] Peer passed all checks: peer5
[INFO] Suitable peers found: 3
[INFO] Ranking peers
[INFO] Selected peer: peer3, score=0.92
[INFO] Assigning job to: peer3
```

**Timeline:** ~1.5 seconds

---

### Performance Tuning

**DHT Publish Optimization:**
- `metadata_republish_interval`: 30 minutes (decrease = fresher, increase = less overhead)
- Store peer count: Target 3-5 reliable store peers
- Bootstrap node count: 2-3 sufficient

**Capability Gathering:**
- Cache validity: Consider caching for 5-10 minutes
- GPU detection: nvidia-smi is slow (~500ms), cache results
- Concurrent gathering: Parallelize detection where possible

**Peer Selection:**
- Coarse filter first: Use DHT metadata to eliminate obviously unsuitable peers
- Batch capability requests: Request from multiple peers in parallel
- Connection reuse: Keep QUIC connections open for future queries

---

## Conclusion

This document provides comprehensive coverage of DHT metadata storage and peer capabilities exchange. Use it to:

1. **Understand data flows** - Follow metadata from gathering to DHT storage to retrieval
2. **Debug issues** - Use log patterns and file references to locate problems
3. **Optimize performance** - Tune republish intervals, cache duration, and query strategies
4. **Extend functionality** - Add new capabilities or metadata fields correctly
5. **Monitor health** - Track metadata freshness, signature validity, and peer discovery

For additional debugging, enable verbose logging in:
- `internal/p2p/dht_bep44.go` - DHT operations and signature verification
- `internal/p2p/metadata_publisher.go` - Publish triggers and sequencing
- `internal/system/capabilities.go` - Capability gathering and matching
