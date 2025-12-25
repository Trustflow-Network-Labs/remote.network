# Network State Monitoring System

## Overview

This document describes the comprehensive network state monitoring system that detects changes in IP addresses, NAT type, and network topology, and triggers appropriate metadata republishing based on node type.

## Components

### 1. NetworkStateMonitor (`internal/p2p/network_state_monitor.go`)

The main monitoring component that:
- Periodically checks for network state changes (default: every 5 minutes)
- Detects changes in:
  - Internal (local) IP address
  - External (public) IP address
  - NAT type
  - Local subnet/topology
- Triggers appropriate actions based on detected changes
- Provides stats for debugging and monitoring

### 2. Configuration

Located in `internal/utils/configs/configs`:

```ini
[nat]
# Network State Monitoring
# How often to check for IP/NAT changes (in minutes)
network_monitor_interval_minutes = 5
```

### 3. Integration Points

#### PeerManager (`internal/core/peer.go`)
- Initializes NetworkStateMonitor with dependencies
- Starts monitoring after initial NAT/topology detection
- Stops monitoring on shutdown
- Exposes monitoring stats via GetStats()

#### RelayManager (`internal/p2p/relay_manager.go`)
New methods added:
- `DisconnectCurrentRelay()` - Disconnects from current relay
- `ReconnectRelay()` - Full reconnection flow with fresh relay selection

#### RelaySelector (`internal/p2p/relay_selector.go`)
New method added:
- `ClearCandidates()` - Clears all relay candidates for fresh discovery

#### MetadataPublisher (`internal/p2p/metadata_publisher.go`)
Existing method utilized:
- `NotifyIPChange(newPublicIP, newPrivateIP)` - Updates and republishes metadata

## Change Detection Flow

### 1. Periodic Monitoring Loop

```
Every N minutes (configurable):
├── Capture current network state
│   ├── Get local IP
│   ├── Get external IP
│   ├── Get NAT type from detector
│   └── Get local subnet from topology manager
├── Compare with previous state
├── Detect changes
│   ├── Local IP changed?
│   ├── External IP changed?
│   ├── NAT type changed?
│   └── Local subnet changed?
└── Handle changes (if any detected)
```

### 2. Change Detection Logic

```go
// Changes trigger actions
if (localIPChanged || externalIPChanged) {
    // Re-detect NAT type with new IP configuration
    natResult = natDetector.DetectNATType()

    // Re-detect topology with new NAT result
    topology = topologyMgr.DetectLocalTopology(natResult)
}

// Handle based on node type
if (isNATNode) {
    handleNATNodeChanges()
} else {
    handlePublicNodeChanges()
}
```

## Metadata Republishing Strategies

### For Public/Relay Nodes

When IP changes are detected:

```
1. Log the IP change (old -> new)
2. Call MetadataPublisher.NotifyIPChange(newPublicIP, newPrivateIP)
   ├── Update metadata with new IP addresses
   ├── Increment sequence number
   ├── Publish updated metadata to DHT
   └── Verify metadata retrieval from DHT
```

**Result**: Metadata updated and published to DHT with new IP information.

### For NAT Nodes

When IP or NAT type changes are detected:

```
1. Log the network configuration change
2. Disconnect from current relay
   ├── Send relay disconnect message
   ├── Close QUIC connection
   ├── Call MetadataPublisher.NotifyRelayDisconnected()
   └── Clear session state
3. Wait briefly for cleanup (2 seconds)
4. Clear relay candidates for fresh discovery
5. Rediscover relay candidates with new network conditions
6. Select and connect to best relay
   ├── Measure latency to candidates
   ├── Score candidates (latency, reputation, pricing)
   ├── Connect to best relay
   ├── Establish relay session
   ├── Start receive stream for relayed messages
   └── Call MetadataPublisher.NotifyRelayConnected()
       ├── Update metadata with new relay info
       ├── Update IP addresses in metadata
       ├── Increment sequence number
       ├── Publish complete metadata to DHT
       └── Verify metadata retrieval
7. Start keepalives to new relay
```

**Result**: NAT node reconnects to optimal relay for new network conditions and publishes complete updated metadata to DHT.

## Critical Importance of Metadata Republishing

### Why Metadata Republishing is Essential

1. **Network Reachability**: Other peers need current IP addresses to connect
2. **Relay Discovery**: NAT peers need to know which relay to use to reach a peer
3. **DHT Accuracy**: DHT stores must be kept current for peer discovery
4. **Connection Establishment**: Stale metadata leads to connection failures

### Public Node Scenario

```
Initial State:
├── Public IP: 192.0.2.1
├── Metadata in DHT contains: 192.0.2.1
└── Other peers can connect to: 192.0.2.1

Network Change (ISP assigned new IP):
├── New Public IP: 192.0.2.100
├── Old metadata in DHT still says: 192.0.2.1
└── Other peers try to connect to: 192.0.2.1 ❌ FAILS

After Monitoring Detects Change:
├── NetworkStateMonitor detects IP change
├── Calls NotifyIPChange(192.0.2.100)
├── Metadata updated and published to DHT
├── New metadata in DHT contains: 192.0.2.100
└── Other peers can now connect to: 192.0.2.100 ✅ SUCCESS
```

### NAT Node Scenario

```
Initial State:
├── NAT Node behind: ISP-NAT-1
├── Connected to: Relay-A (optimal for ISP-NAT-1)
├── Metadata in DHT contains:
│   ├── using_relay: true
│   ├── connected_relay: Relay-A
│   └── relay_address: relay-a.example.com:30906
└── Other peers reach this node via: Relay-A

Network Change (mobile device switches from WiFi to 4G):
├── New network: Mobile-NAT-2 (different ISP, different NAT type)
├── Old relay connection: BROKEN (network path changed)
├── Old metadata in DHT still says: Use Relay-A
└── Other peers try to reach via: Relay-A ❌ FAILS (session invalid)

After Monitoring Detects Change:
├── NetworkStateMonitor detects IP/NAT change
├── Disconnects from Relay-A
├── Clears relay candidates (Relay-A was optimal for old network)
├── Rediscovers relays with new network conditions
├── Selects Relay-B (optimal for Mobile-NAT-2)
├── Connects to Relay-B and establishes new session
├── Calls NotifyRelayConnected(Relay-B)
├── Metadata updated and published to DHT:
│   ├── using_relay: true
│   ├── connected_relay: Relay-B (updated)
│   ├── relay_address: relay-b.example.com:30906 (updated)
│   ├── public_ip: new-mobile-ip (updated)
│   └── private_ip: new-mobile-local-ip (updated)
└── Other peers can now reach via: Relay-B ✅ SUCCESS
```

## Monitoring and Debugging

### Stats Exposed via API

```json
{
  "network_monitor": {
    "running": true,
    "consecutive_failures": 0,
    "current_state": {
      "local_ip": "192.168.1.100",
      "external_ip": "203.0.113.50",
      "nat_type": "Port Restricted Cone NAT",
      "local_subnet": "192.168.1.0/24",
      "detection_time": "2025-01-15T10:30:00Z"
    },
    "previous_state": {
      "local_ip": "192.168.1.99",
      "external_ip": "203.0.113.45",
      "nat_type": "Full Cone NAT",
      "local_subnet": "192.168.1.0/24",
      "detection_time": "2025-01-15T10:25:00Z"
    }
  }
}
```

### Log Messages

The system provides comprehensive logging at each stage:

```
[network-monitor] Starting network state monitor...
[network-monitor] Initial network state captured: LocalIP=192.168.1.100, ExternalIP=203.0.113.50, NATType=Port Restricted Cone NAT
[network-monitor] Network state monitoring interval: 5m0s
[network-monitor] Network state monitor started successfully

... (5 minutes later) ...

[network-monitor] Network state check triggered
[network-monitor] === NETWORK STATE CHANGES DETECTED ===
[network-monitor] Local IP changed: 192.168.1.100 -> 192.168.1.101
[network-monitor] External IP changed: 203.0.113.50 -> 203.0.113.51
[network-monitor] ======================================
[network-monitor] Re-detecting NAT type due to IP changes...
[nat-detector] Starting NAT type detection...
[nat-detector] Detected Port Restricted Cone NAT
[network-monitor] NAT re-detection complete: Port Restricted Cone NAT (Difficulty: moderate, Requires Relay: false)
[network-monitor] Re-detecting network topology...
[nat-topology] Detecting local network topology...
[nat-topology] Local topology detected: Public IP=203.0.113.51, Subnet=192.168.1.0/24
[network-monitor] Topology re-detection complete: Public IP=203.0.113.51, Subnet=192.168.1.0/24
[network-monitor] Handling network changes for NAT node
[network-monitor] NAT node network configuration changed - initiating relay reconnection...
[network-monitor] Step 1: Disconnecting from current relay...
[relay-manager] Disconnecting from current relay (requested by network state monitor)
[metadata-publisher] Updating metadata: relay disconnected
[network-monitor] Disconnected from current relay
[network-monitor] Step 2: Initiating new relay selection and connection...
[relay-manager] Reconnecting to relay with new network configuration...
[relay-manager] Clearing relay candidates for fresh discovery...
[relay-selector] Cleared 3 relay candidates for fresh discovery
[relay-manager] Rediscovering relay candidates with new network configuration...
[relay-manager] Discovered 5 relay candidates
[relay-manager] Selecting and connecting to best relay for new network configuration...
[relay-selector] Selected best relay: relay-b.example.com (latency: 45ms)
[relay-manager] Connected to relay: relay-b.example.com
[metadata-publisher] Updating metadata with relay info (relay_peer_id: abc123, session: session-xyz)
[metadata-publisher] Successfully updated metadata with relay info (seq: 5)
[relay-manager] Successfully reconnected to relay with new network configuration
[network-monitor] Successfully reconnected to relay and published updated metadata
```

## Connection Cleanup Integration

### Overview

Network state changes trigger comprehensive connection cleanup to ensure stale connections don't persist after network topology changes.

**Files:** `internal/p2p/quic.go`, `internal/core/peer.go`

### Connection Cleanup on Network Changes

When network state changes are detected, the following cleanup processes are triggered:

```
Network State Change Detected
├─ NAT Nodes:
│   ├─ Disconnect from current relay (explicit)
│   ├─ Close all relay-associated QUIC connections
│   ├─ Trigger stale connection cleanup
│   └─ Clear relay candidate cache
│
└─ Public Nodes:
    ├─ Close connections bound to old IP
    ├─ Trigger stale connection cleanup
    └─ Re-establish listening on new IP
```

### Stale Connection Detection (Post-Network Change)

**Enhanced Cleanup** (Commit a6ba0ce):

After network changes, the system actively cleans up stale connections:

```go
cleanupStaleConnections() triggered by network change:
├─ Enumerate all QUIC connections
├─ Check each connection:
│   ├─ ActiveStreams == 0?
│   ├─ Connection bound to old IP?
│   ├─ Idle time > threshold?
│   └─ Peer ID verification passed?
├─ For stale connections:
│   ├─ Log: "Cleaning up stale connection after network change"
│   ├─ CloseWithError(0x100, "network topology changed")
│   ├─ Remove from connection cache
│   └─ Notify PeerManager
└─ Update connection statistics
```

**Benefits:**
- Prevents zombie connections on old IP addresses
- Frees resources (file descriptors, memory)
- Ensures fresh connections use new network configuration
- Maintains accurate connection state in PeerManager

### Integration with PeerManager

The PeerManager coordinates between network monitoring and connection lifecycle:

```
Network Monitor → PeerManager → Connection Cleanup
       ↓                ↓              ↓
  IP Change     Update peer      Clean stale
  Detected       state map        connections
       ↓                ↓              ↓
  NAT Change    Disconnect       Update UI via
  Detected      old relays        WebSocket
       ↓                ↓              ↓
  Topology      Trigger          Emit connection
  Change        cleanup           state events
```

**PeerManager Responsibilities:**
- Track connection state per peer
- Coordinate cleanup across relay and direct connections
- Emit WebSocket events for UI updates
- Maintain peer reachability information
- Handle automatic peer re-discovery

### WebSocket Notifications

When network changes trigger connection cleanup, the UI is notified:

```json
{
  "type": "connection_state_changed",
  "payload": {
    "peer_id": "abc123",
    "old_state": "CONNECTED",
    "new_state": "DISCONNECTED",
    "reason": "network_topology_changed",
    "timestamp": "2025-01-15T10:35:00Z"
  }
}
```

### Example: Complete Network Change Flow

```
1. WiFi → 4G Network Switch on Mobile Device
   ├─ Local IP: 192.168.1.100 → 10.0.0.50
   ├─ External IP: 203.0.113.45 → 198.51.100.20
   └─ NAT Type: Cone NAT → Symmetric NAT

2. NetworkStateMonitor Detects Change
   └─ Triggers handleNATNodeChanges()

3. Connection Cleanup Phase
   ├─ Disconnect from current relay (explicit)
   ├─ Close all QUIC connections on old IP
   ├─ cleanupStaleConnections() removes orphaned connections
   ├─ Clear relay candidate cache
   └─ Notify PeerManager of cleanup

4. Reconnection Phase
   ├─ Rediscover relay candidates (new network)
   ├─ Select optimal relay for Symmetric NAT + 4G
   ├─ Establish new QUIC connection on new IP
   ├─ Create relay session
   └─ Verify connection with peer ID check

5. Metadata Update Phase
   ├─ Update metadata with new relay info
   ├─ Update metadata with new IP addresses
   ├─ Increment sequence number
   ├─ Publish to DHT
   └─ Verify DHT retrieval

6. Verification Phase
   ├─ Confirm no stale connections remain
   ├─ Verify ActiveStreams counters accurate
   ├─ Check peer reachability
   └─ Emit WebSocket event: connection_restored
```

### Logs During Cleanup

```
[network-monitor] Network state change detected
[peer-manager] Initiating connection cleanup for network change
[quic] Cleaning up stale connections (trigger: network_change)
[quic] Stale connection detected: connection_id=conn-123, idle=0s, streams=0
[quic] Closing connection: reason=network_topology_changed
[peer-manager] Connection closed: peer_id=abc123, reason=network_change
[websocket] Broadcasting connection_state_changed event
[network-monitor] Connection cleanup complete, 3 connections closed
[network-monitor] Proceeding with relay reconnection...
```

### Configuration

Connection cleanup behavior is configurable:

```ini
[connection_cleanup]
# Idle timeout before connection cleanup (seconds)
idle_connection_timeout = 300  # 5 minutes

# Cleanup check interval (seconds)
cleanup_interval = 60  # 1 minute

# Force cleanup on network change
force_cleanup_on_network_change = true
```

---

## Error Handling

### Consecutive Failure Detection

```go
if captureStateFails {
    consecutiveFailures++
    if consecutiveFailures >= maxFailuresBeforeAlert {
        logger.Error("Network state monitoring failing consistently")
    }
}

if captureStateSucceeds {
    consecutiveFailures = 0  // Reset on success
    logger.Info("Network state monitoring recovered")
}
```

### Relay Reconnection Failure

If relay reconnection fails after network change:
- Error is logged
- System continues operating
- Next monitoring cycle will retry
- Peer remains unreachable until relay connection is restored

## Performance Considerations

### Monitoring Interval

- **Default**: 5 minutes
- **Minimum**: 1 minute (configurable)
- **Maximum**: 60 minutes (configurable)
- **Recommendation**: 5 minutes provides good balance between responsiveness and overhead

### Network Overhead

- Local IP detection: Local syscall (negligible)
- External IP detection: 1 HTTP request to IP detection service (~1KB)
- NAT detection: 3-5 STUN requests (~500 bytes each) only when IP changes
- Metadata publishing: 1 DHT PUT request (~2KB) only when changes detected

**Total overhead per check**: ~1KB (external IP check)
**Total overhead on change**: ~10KB (IP check + STUN + DHT publish)

### CPU Impact

- Monitoring goroutine: Sleeps between checks (minimal CPU)
- State capture: <1ms per check
- Change detection: Simple comparison (microseconds)
- NAT re-detection: ~1-2 seconds (only on IP change)
- Relay reconnection: ~5-10 seconds (only on NAT node IP/NAT change)

## Testing Scenarios

### Scenario 1: Public Node IP Change

```bash
# Simulate IP change (requires root)
sudo ip addr add 192.0.2.100/24 dev eth0
sudo ip addr del 192.0.2.1/24 dev eth0

# Expected behavior within 5 minutes:
# 1. Monitor detects external IP change
# 2. NAT re-detection runs
# 3. Metadata updated with new IP
# 4. Metadata published to DHT
# 5. Other peers can connect to new IP
```

### Scenario 2: NAT Node Network Switch

```bash
# Simulate WiFi to 4G switch
# On mobile device: Disable WiFi, enable 4G

# Expected behavior within 5 minutes:
# 1. Monitor detects IP and NAT type change
# 2. Disconnects from current relay
# 3. Clears relay candidates
# 4. Rediscovers relays
# 5. Selects best relay for new network
# 6. Connects to new relay
# 7. Publishes updated metadata with new relay
# 8. Other peers can reach via new relay
```

### Scenario 3: Public Node Behind Firewall

```bash
# Node moves from direct internet to behind NAT
# (e.g., laptop at office -> laptop at home)

# Expected behavior:
# 1. Monitor detects NAT type change (None -> Cone/Symmetric)
# 2. NAT re-detection identifies NAT type
# 3. Logs warning about NAT introduction
# 4. Metadata updated with new network info
# Note: Node may need reconfiguration if relay manager not initialized
```

## Future Enhancements

### Possible Improvements

1. **Fast Change Detection**
   - Listen to OS network interface change events
   - Immediate check on network change instead of waiting for interval
   - Requires platform-specific code (Linux: netlink, macOS: SystemConfiguration)

2. **Smart Monitoring Intervals**
   - Shorter intervals when on mobile networks (frequent changes)
   - Longer intervals when on stable connections
   - Adaptive based on change history

3. **Predictive Relay Selection**
   - Remember relay performance per network
   - Pre-select relay when returning to known network
   - Faster reconnection times

4. **Network Quality Monitoring**
   - Detect network degradation (packet loss, latency increase)
   - Proactive relay switching before connection breaks
   - Quality-based metadata updates

## Conclusion

The network state monitoring system provides automated detection and handling of network changes, ensuring that:
- **Public nodes** always advertise current IP addresses
- **NAT nodes** maintain optimal relay connections
- **Metadata** in DHT stays current and accurate
- **Peer connectivity** is maintained across network changes
- **System resilience** is improved through automatic recovery

This is critical for mobile peers, dynamic IP environments, and long-running nodes that may experience network configuration changes over time.
