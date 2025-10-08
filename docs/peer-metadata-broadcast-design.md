# Peer Metadata Broadcast Design

## Problem Statement

When a peer's metadata changes (e.g., relay switch, NAT type change, new capabilities), other peers in the network need to be notified to update their local peer database. Currently:

❌ **Missing:** No mechanism to broadcast metadata changes to known peers
❌ **Missing:** NAT-to-NAT peers cannot communicate metadata updates directly
❌ **Result:** Stale metadata in peer databases, failed connections

## Design Goals

1. **Automatic Broadcast**: Detect metadata changes and broadcast to all known peers
2. **NAT Traversal**: Utilize hole punching for NAT-to-NAT communication
3. **Efficient**: Batch updates, avoid broadcast storms
4. **Reliable**: Retry failed deliveries, track delivery status
5. **Scalable**: Handle large peer counts without overwhelming network

## Use Cases

### 1. Relay Switch
**Scenario:** NAT peer switches from Relay A to Relay B

**Current State:**
- Peer connects to new relay
- Updates local metadata
- ❌ Other peers still have old relay info
- ❌ Cannot reach peer via old relay

**Desired State:**
- Peer broadcasts metadata update
- All known peers receive update
- Peers can reach peer via new relay

### 2. NAT Type Change
**Scenario:** Peer's NAT type changes (e.g., network change, router reboot)

**Impact:**
- Hole punching strategy may need adjustment
- Direct connection attempts may fail with old info

### 3. New Capability
**Scenario:** Peer adds new service or protocol

**Impact:**
- Other peers should discover new capability
- Enable new communication patterns

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Metadata Change Flow                       │
└─────────────────────────────────────────────────────────────────┘

1. Metadata Change Detected
   │
   ├──> Update local database
   │
   ├──> Trigger MetadataBroadcaster
   │
   └──> Get list of known peers (from database)
        │
        ├──> For each peer:
        │    │
        │    ├──> Check connection type:
        │    │    │
        │    │    ├──> Direct connection exists?
        │    │    │    └──> Send update directly
        │    │    │
        │    │    ├──> Public peer?
        │    │    │    └──> Direct dial + send
        │    │    │
        │    │    ├──> LAN peer?
        │    │    │    └──> LAN dial + send
        │    │    │
        │    │    ├──> NAT peer with relay?
        │    │    │    └──> Send via relay
        │    │    │
        │    │    └──> NAT peer without relay?
        │    │         └──> Attempt hole punch + send
        │    │
        │    └──> Track delivery status
        │
        └──> Retry failed deliveries (with backoff)
```

## Protocol Design

### Message Type

```go
MessageTypePeerMetadataUpdate MessageType = "peer_metadata_update"
```

### Message Data Structure

```go
type PeerMetadataUpdateData struct {
    NodeID      string                `json:"node_id"`      // Who is sending
    Topic       string                `json:"topic"`        // Topic context
    Version     int                   `json:"version"`      // Metadata version
    Timestamp   time.Time             `json:"timestamp"`    // When change occurred

    // What changed
    ChangeType  string                `json:"change_type"`  // "relay", "network", "capability", "full"

    // Full metadata (for new peers or "full" change type)
    Metadata    *PeerMetadata         `json:"metadata,omitempty"`

    // Partial updates (more efficient for small changes)
    RelayUpdate *RelayUpdateInfo      `json:"relay_update,omitempty"`
    NetworkUpdate *NetworkUpdateInfo  `json:"network_update,omitempty"`

    // Broadcast metadata
    Sequence    int64                 `json:"sequence"`     // For deduplication
    TTL         int                   `json:"ttl"`          // Prevent infinite loops
}

type RelayUpdateInfo struct {
    UsingRelay     bool   `json:"using_relay"`
    ConnectedRelay string `json:"connected_relay"`   // New relay NodeID
    SessionID      string `json:"session_id"`        // New session ID
    RelayAddress   string `json:"relay_address"`     // How to reach via relay
}

type NetworkUpdateInfo struct {
    PublicIP    string `json:"public_ip,omitempty"`
    PublicPort  int    `json:"public_port,omitempty"`
    PrivateIP   string `json:"private_ip,omitempty"`
    PrivatePort int    `json:"private_port,omitempty"`
    NATType     string `json:"nat_type,omitempty"`
}
```

### Delivery Strategies

#### 1. Direct Connection (Fastest)
```
Peer A ──[existing QUIC conn]──> Peer B
```
- Use existing connection
- Immediate delivery
- No overhead

#### 2. Public Peer (Fast)
```
NAT Peer ──[direct dial]──> Public Peer
```
- Direct dial to public address
- Fallback if connection doesn't exist
- Low latency

#### 3. LAN Peer (Fastest, Local)
```
Peer A ──[LAN direct dial]──> Peer B
```
- Same subnet detection
- Direct dial to private IP
- Sub-millisecond latency

#### 4. Relay Forward (Reliable)
```
NAT Peer A ──[relay]──> NAT Peer B
            (via shared relay)
```
- Use relay to forward message
- Works when hole punching not available
- Higher latency

#### 5. Hole Punch + Send (NAT Traversal)
```
NAT Peer A ──[hole punch]──> NAT Peer B
         (coordinate via relay, then direct)
```
- Attempt hole punch first
- Send update over direct connection
- Best for frequent updates
- Fallback to relay if fails

## Implementation Components

### 1. MetadataBroadcaster (New)
**File:** `internal/p2p/metadata_broadcaster.go`

```go
type MetadataBroadcaster struct {
    config      *utils.ConfigManager
    logger      *utils.LogsManager
    dbManager   *database.SQLiteManager
    quicPeer    *QUICPeer
    dhtPeer     *DHTPeer
    holePuncher *HolePuncher

    // Broadcast tracking
    sequenceNum   int64
    sequenceMutex sync.Mutex

    // Deduplication (prevent broadcast loops)
    recentBroadcasts map[string]int64  // key: "nodeID:sequence"
    dedupeWindow     time.Duration
    dedupeMutex      sync.RWMutex

    // Delivery tracking
    pendingDeliveries map[string]*DeliveryTask
    deliveryMutex     sync.RWMutex

    // Context
    ctx    context.Context
    cancel context.CancelFunc
}

type DeliveryTask struct {
    TargetPeerID string
    Message      *QUICMessage
    Attempts     int
    LastAttempt  time.Time
    Status       string // "pending", "delivered", "failed"
}
```

**Key Methods:**
```go
func NewMetadataBroadcaster(...) *MetadataBroadcaster
func (mb *MetadataBroadcaster) Start() error
func (mb *MetadataBroadcaster) Stop() error

// Main broadcast function
func (mb *MetadataBroadcaster) BroadcastMetadataChange(
    changeType string,
    metadata *PeerMetadata,
) error

// Delivery strategies
func (mb *MetadataBroadcaster) sendToDirectConnection(peerID string, msg *QUICMessage) error
func (mb *MetadataBroadcaster) sendToPublicPeer(peerID string, msg *QUICMessage) error
func (mb *MetadataBroadcaster) sendToLANPeer(peerID string, msg *QUICMessage) error
func (mb *MetadataBroadcaster) sendViaRelay(peerID string, msg *QUICMessage) error
func (mb *MetadataBroadcaster) sendViaHolePunch(peerID string, msg *QUICMessage) error

// Deduplication
func (mb *MetadataBroadcaster) shouldProcessUpdate(nodeID string, sequence int64) bool
func (mb *MetadataBroadcaster) recordBroadcast(nodeID string, sequence int64)

// Retry logic
func (mb *MetadataBroadcaster) retryFailedDeliveries()
```

### 2. Integration Points

#### RelayManager.updateOurMetadataWithRelay() (Update Existing)
**File:** `internal/p2p/relay_manager.go`

```go
func (rm *RelayManager) updateOurMetadataWithRelay(relay *RelayCandidate, sessionID string) error {
    // 1. Update database with new relay info
    // 2. Generate our updated metadata
    // 3. Call MetadataBroadcaster.BroadcastMetadataChange()
    return rm.broadcaster.BroadcastMetadataChange("relay", updatedMetadata)
}
```

#### QUICPeer.handleMetadataUpdate() (New)
**File:** `internal/p2p/quic.go`

```go
func (q *QUICPeer) handleMetadataUpdate(msg *QUICMessage, remoteAddr string) *QUICMessage {
    // 1. Parse update message
    // 2. Check deduplication (sequence number)
    // 3. Update local database
    // 4. Return acknowledgment
}
```

#### PeerManager Integration
**File:** `internal/core/peer.go`

```go
// Add to PeerManager struct
type PeerManager struct {
    // ... existing fields
    metadataBroadcaster *p2p.MetadataBroadcaster
}

// Initialize in NewPeerManager()
metadataBroadcaster := p2p.NewMetadataBroadcaster(config, logger, dbManager, quic, dht, holePuncher)
pm.metadataBroadcaster = metadataBroadcaster

// Start/Stop in lifecycle
func (pm *PeerManager) Start() error {
    // ... existing code
    if pm.metadataBroadcaster != nil {
        pm.metadataBroadcaster.Start()
    }
}
```

## Configuration

Add to `internal/utils/configs/configs`:

```toml
[metadata_broadcast]
# Enable automatic metadata broadcast
metadata_broadcast_enabled = true

# Broadcast behavior
metadata_broadcast_batch_interval = 5s
metadata_broadcast_max_batch_size = 50
metadata_broadcast_ttl = 3

# Delivery
metadata_broadcast_timeout = 10s
metadata_broadcast_max_retries = 3
metadata_broadcast_retry_interval = 30s

# Deduplication
metadata_broadcast_dedupe_window = 5m

# Hole punching for broadcasts
metadata_broadcast_hole_punch_enabled = true
metadata_broadcast_hole_punch_timeout = 10s
```

## Change Detection Triggers

### 1. Relay Switch
**Location:** `RelayManager.ConnectToRelay()`
**Trigger:** After successful relay connection
**ChangeType:** "relay"
**Data:** RelayUpdateInfo

### 2. Relay Disconnect
**Location:** `RelayManager.DisconnectRelay()`
**Trigger:** After disconnecting from relay
**ChangeType:** "relay"
**Data:** RelayUpdateInfo (empty relay)

### 3. NAT Detection Complete/Change
**Location:** `NATDetector.DetectNATType()`
**Trigger:** After NAT type detection
**ChangeType:** "network"
**Data:** NetworkUpdateInfo

### 4. Service Registration
**Location:** Service registration APIs (future)
**Trigger:** When new service added
**ChangeType:** "capability"
**Data:** Full metadata

## Deduplication Strategy

### Sequence Numbers
- Each node maintains a monotonically increasing sequence number
- Broadcast messages include: `nodeID:sequence`
- Receivers track recent broadcasts in a time-windowed cache

### TTL (Time To Live)
- Prevents infinite broadcast loops
- Decremented at each hop
- Dropped when TTL reaches 0

### Example:
```
Node A broadcasts update (seq=100, TTL=3)
  └──> Node B receives (TTL=3, records A:100)
       └──> Node B could rebroadcast (TTL=2, checks: already saw A:100? Yes, skip)
```

## Delivery Guarantees

### Best-Effort with Retries
- **Initial attempt:** Immediate send
- **Retry 1:** After 30s if no ACK
- **Retry 2:** After 1min if no ACK
- **Retry 3:** After 2min if no ACK
- **Give up:** After 3 failures

### Acknowledgments
```go
MessageTypePeerMetadataUpdateAck MessageType = "peer_metadata_update_ack"

type PeerMetadataUpdateAckData struct {
    NodeID   string `json:"node_id"`
    Sequence int64  `json:"sequence"`
    Status   string `json:"status"` // "received", "applied", "error"
    Error    string `json:"error,omitempty"`
}
```

## Performance Considerations

### Batch Updates
- Collect metadata changes for 5s
- Send single batch instead of many small updates
- Reduces network overhead

### Connection Reuse
- Prefer existing connections over new ones
- Cache hole-punched connections for future use

### Priority Queue
- High priority: Relay changes (affects reachability)
- Medium priority: Network changes
- Low priority: Capability changes

### Metrics
Track:
- Total broadcasts sent
- Delivery success rate by strategy
- Average delivery time
- Failed deliveries by reason

## Testing Scenarios

### 1. Direct Connection Broadcast
- Peer A and B have direct connection
- A changes relay
- B receives update immediately

### 2. LAN Peer Broadcast
- Peer A and B on same LAN
- A changes metadata
- B receives via LAN direct dial

### 3. NAT-to-NAT via Hole Punch
- Peer A and B both behind NAT
- A changes relay
- A hole punches to B
- B receives update over direct connection

### 4. NAT-to-NAT via Relay
- Peer A and B both behind NAT
- Hole punch fails
- A sends via relay to B
- B receives forwarded update

### 5. Broadcast Storm Prevention
- 10 peers all switch relays simultaneously
- Deduplication prevents message amplification
- TTL prevents loops

### 6. Partial Failure Recovery
- Broadcast to 50 peers
- 5 fail initially
- Retry mechanism delivers after 30s

## Migration Path

### Phase 1: Add Message Types & Broadcaster (This PR)
- Add new message types
- Implement MetadataBroadcaster
- No automatic triggers yet

### Phase 2: Integrate with RelayManager
- Call broadcaster on relay changes
- Test NAT-to-NAT updates

### Phase 3: Add Other Triggers
- NAT detection changes
- Network changes
- Service updates

### Phase 4: Optimize & Scale
- Batch updates
- Priority queues
- Advanced metrics

## Success Metrics

- ✅ Metadata updates delivered within 10s for 95% of peers
- ✅ <1% duplicate deliveries (deduplication working)
- ✅ >90% delivery success rate for NAT-to-NAT via hole punch
- ✅ 100% delivery via relay fallback
- ✅ Zero broadcast storms in stress tests

---

**Next Steps:** Implement MetadataBroadcaster and integrate with RelayManager
