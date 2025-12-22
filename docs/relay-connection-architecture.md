# Relay Connection Architecture

This document provides a detailed technical guide to the connection, reconnection, and relay selection mechanisms in the Remote Network system. It covers data flows from both NAT peer and relay perspectives, with specific file and function references for debugging.

## Table of Contents
- [Overview](#overview)
- [Connection Establishment](#connection-establishment)
  - [NAT Peer Perspective](#nat-peer-perspective)
  - [Relay Perspective](#relay-perspective)
- [Reconnection Logic](#reconnection-logic)
  - [Stale Connection Detection](#stale-connection-detection)
  - [Reconnection Strategy](#reconnection-strategy)
- [Relay Selection](#relay-selection)
  - [Selection Algorithm](#selection-algorithm)
  - [Latency Measurement](#latency-measurement)
- [Stream Management](#stream-management)
- [Key Data Structures](#key-data-structures)
- [Debugging Guide](#debugging-guide)

---

## Overview

The system uses a relay-based architecture to enable communication between NAT peers. Key components:

- **RelayManager** - Manages relay connections for NAT peers
- **RelayPeer** - Handles relay server functionality
- **RelaySelector** - Selects optimal relay based on latency/reputation/pricing
- **QUICPeer** - QUIC transport layer with Ed25519-based mTLS
- **HolePuncher** - NAT traversal via UDP hole punching
- **NATTopologyManager** - Network topology detection

### Connection Strategy Hierarchy
1. **Direct LAN** - Same subnet connection
2. **Direct Public** - Public IP connection (for peers without NAT)
3. **UDP Hole Punching** - NAT traversal for compatible NAT types
4. **Relay Forwarding** - Fallback for symmetric NAT or failed hole punching

---

## Connection Establishment

### NAT Peer Perspective

#### Phase 1: NAT Detection & Topology Discovery
**Files:** `internal/p2p/nat_detector.go`, `internal/p2p/nat_topology.go`, `internal/p2p/hole_puncher.go`

```
NATDetector.DetectNATType()
├─ Determines NAT type: Full Cone, Restricted, Port Restricted, Symmetric, None
├─ File: nat_detector.go:50
└─ Used to determine if relay is needed

NATTopologyManager.DetectLocalTopology()
├─ Detects: LocalSubnet, PublicIP, PeersOnSameNAT
├─ File: nat_topology.go:42
└─ Stores network topology information

HolePuncher.DirectConnect(peerID)
├─ Implements connection strategy (try-fail-continue pattern)
├─ File: hole_puncher.go:211
├─ Step 1: Check if already connected
├─ Step 2: Try LAN direct dial (if same subnet)
├─ Step 3: Try public direct dial (if peer has public IP)
├─ Step 4: Try UDP hole punching
└─ Step 5: Fallback to relay
```

**Logs to check:**
- "NAT type detected: X"
- "Local topology detected: subnet=X, public_ip=Y"
- "DirectConnect initiated for peer X"
- "Detected LAN peer X, attempting direct LAN dial"
- "Successfully connected to LAN peer X" / "LAN dial failed"
- "Attempting hole punch for peer X"
- "All direct connection attempts failed, falling back to relay"

---

#### Phase 2: Relay Discovery
**Files:** `internal/core/peer.go`, `internal/p2p/relay_manager.go`

```
Peer.connectToBootstrapPeers()
├─ Connect to custom_bootstrap_nodes via QUIC
├─ File: internal/core/peer.go:150
├─ Exchange Ed25519 public keys + known_peers lists
└─ Discover initial relay candidates

RelayManager.Start()
├─ Subscribe to relay discoveries
├─ File: internal/p2p/relay_manager.go:100
├─ Channel: discoveryChan (receives RelayDiscoveredEvent)
└─ Adds discovered relays to RelaySelector

Flow:
1. Bootstrap connection established
2. Peer metadata fetched from DHT (BEP44)
3. Filter peers where NetworkInfo.IsRelay == true
4. RelaySelector.AddCandidate() called
   └─ File: internal/p2p/relay_selector.go:80
```

**Logs to check:**
- "Connecting to bootstrap peer: X"
- "Discovered relay candidate: peer_id=X, endpoint=Y"
- "Added relay candidate: X (total: Y candidates)"

**Potential Issues:**
- **No relay candidates found** - Check bootstrap nodes connectivity
- **Duplicate relay additions** - Check deduplication logic in relay_selector.go:80

---

#### Phase 3: Relay Selection
**Files:** `internal/p2p/relay_selector.go`, `internal/p2p/relay_manager.go`

```
RelayManager.EvaluateAndSelectRelay()
├─ File: relay_manager.go:250
├─ Measures all candidates in batches
└─ Selects best relay

  ├─> RelaySelector.MeasureAllCandidates()
  │   ├─ File: relay_selector.go:200
  │   ├─ Batch size: 3 relays at a time
  │   ├─ Pre-establish QUIC connections
  │   ├─> RelaySelector.MeasureLatency(candidate)
  │   │   ├─ File: relay_selector.go:150
  │   │   ├─ Send MessageTypePing
  │   │   └─ Wait for MessageTypePong
  │   └─ Record latency in RelayCandidate.Latency
  │
  └─> RelaySelector.SelectBestRelay()
      ├─ File: relay_selector.go:300
      ├─ Priority #1: Preferred Relay (from database)
      ├─ Priority #2: Score-based selection
      │   ├─ Filter: maxLatency, minReputation, maxPricing
      │   ├─ Calculate score per candidate:
      │   │   └─ (latency * 0.5) + (reputation * 0.3) + (pricing * 0.2)
      │   └─ Select highest scoring candidate
      └─ Return selected RelayCandidate
```

**Configuration (relay_selector.go:40):**
- `maxLatency`: 500ms (relay_max_latency)
- `minReputation`: 0.3 (relay_min_reputation)
- `maxPricing`: 0.01 (relay_max_pricing)
- `switchThreshold`: 20% improvement required

**Logs to check:**
- "Measuring latency for relay: X"
- "Relay X latency: Yms, reputation: Z"
- "Selected relay: peer_id=X, score=Y, latency=Zms"
- "Using preferred relay: X"

**Potential Issues:**
- **All candidates filtered out** - Check latency/reputation/pricing thresholds
- **Latency measurement timeout** - Check QUIC connectivity to relays
- **Wrong relay selected** - Verify score calculation in relay_selector.go:320

---

#### Phase 4: Relay Connection Establishment
**Files:** `internal/p2p/relay_manager.go`, `internal/p2p/quic.go`

```
RelayManager.ConnectToRelay(relayCandidate)
├─ File: relay_manager.go:400
├─ Step 1: Establish QUIC connection
│   ├─> QUICPeer.ConnectToPeer(relay.Endpoint)
│   │   ├─ File: quic.go:200
│   │   ├─ mTLS with Ed25519 certificates
│   │   └─ Returns quic.Connection
│
├─ Step 2: Open registration stream
│   ├─> QUICPeer.OpenStreamSync()
│   │   ├─ File: quic.go:350
│   │   └─ Returns quic.Stream
│
├─ Step 3: Send relay registration
│   ├─ Message: MessageTypeRelayRegister
│   │   ├─ PeerID (Ed25519 public key)
│   │   ├─ NodeID (DHT node ID)
│   │   ├─ NATType
│   │   ├─ PublicEndpoint
│   │   └─ PrivateEndpoint
│   ├─ File: messages.go:150
│   └─ Write to registration stream
│
├─ Step 4: Wait for relay acceptance
│   ├─ Read from registration stream
│   ├─ Expect: MessageTypeRelayAccept
│   │   └─ Contains: SessionID
│   └─ Timeout: 10 seconds
│
├─ Step 5: Store session info
│   ├─ currentRelay = relayCandidate
│   ├─ currentRelayConn = connection
│   └─ sessionID = received session ID
│
└─ Step 6: Open receive stream
    ├─> QUICPeer.OpenStreamSync()
    ├─ Send: MessageTypeRelayReceiveStreamReady
    │   └─ Contains: SessionID
    ├─ Relay stores this stream in RelaySession.ReceiveStream
    └─> Start listenForRelayedMessages() goroutine
        ├─ File: relay_manager.go:600
        └─ Blocks reading from receive stream
```

**Logs to check:**
- "Connecting to relay: X"
- "QUIC connection established to relay: X"
- "Sent relay registration: session_id=X"
- "Relay accepted registration: session_id=X"
- "Receive stream ready: session_id=X"
- "Started listening for relayed messages"

**Potential Issues:**
- **Registration timeout** - Relay not responding (check relay logs)
- **Session ID mismatch** - Check relay_manager.go:450 session storage
- **Receive stream errors** - Check relay_manager.go:600 error handling
- **Connection established but no stream** - QUIC stream limit reached

---

#### Phase 5: Keepalive Loop
**Files:** `internal/p2p/relay_manager.go`

```
RelayManager.sendKeepalives()
├─ File: relay_manager.go:700
├─ Goroutine: Started after successful connection
├─ Interval: 5 seconds (relay_keepalive_interval)
├─ Timeout: 120 seconds (4x keepalive interval)
│
└─ Loop:
    ├─ Open ephemeral stream
    ├─ Send MessageTypePing with sessionID
    ├─ Wait for MessageTypePong (timeout: 5s)
    ├─ Success: Continue
    └─ Failure: handleRelayConnectionLoss()
        └─ File: relay_manager.go:800
```

**Logs to check:**
- "Sending keepalive ping: session_id=X"
- "Received keepalive pong: session_id=X"
- "Keepalive timeout: session_id=X" (indicates problem)

**Potential Issues:**
- **Keepalive timeout** - Network issues or relay overload
- **Stream creation failures** - QUIC connection degraded
- **Pong never received** - Relay session expired (check relay logs)

---

### Relay Perspective

#### Phase 1: Accept Registration
**Files:** `internal/p2p/relay.go`

```
RelayPeer.HandleRelayRegister(stream, message)
├─ File: relay.go:300
├─ Triggered when: NAT peer sends MessageTypeRelayRegister
│
├─ Step 1: Extract registration info
│   ├─ ClientPeerID (Ed25519 ID)
│   ├─ ClientNodeID (DHT ID)
│   ├─ NATType
│   ├─ PublicEndpoint
│   └─ PrivateEndpoint
│
├─ Step 2: Generate session
│   ├─ SessionID = UUID
│   └─ File: relay.go:320
│
├─ Step 3: Create RelaySession
│   ├─ Store in: sessions map (keyed by SessionID)
│   ├─ Store in: registeredClients map (keyed by ClientPeerID)
│   └─ Data structure (see below):
│       ├─ SessionID
│       ├─ ClientPeerID
│       ├─ ClientNodeID
│       ├─ Connection (quic.Connection)
│       ├─ ReceiveStream (*quic.Stream) [nil initially]
│       ├─ StartTime
│       ├─ LastKeepalive
│       ├─ IngressBytes
│       └─ EgressBytes
│
├─ Step 4: Send acceptance
│   ├─ Message: MessageTypeRelayAccept
│   │   └─ SessionID
│   └─ Write to registration stream
│
└─ Step 5: Start session monitor
    └─> monitorSession(sessionID)
        ├─ File: relay.go:500
        ├─ Goroutine: Per session
        └─ Monitors keepalive timeout
```

**Logs to check:**
- "Relay registration received: peer_id=X, nat_type=Y"
- "Created relay session: session_id=X, peer_id=Y"
- "Sent relay acceptance: session_id=X"

**Potential Issues:**
- **Duplicate registrations** - Check registeredClients deduplication
- **Session ID collision** - Verify UUID generation
- **Memory leak** - Ensure sessions are cleaned up in relay.go:900

---

#### Phase 2: Receive Stream Setup
**Files:** `internal/p2p/relay.go`

```
RelayPeer.HandleRelayReceiveStreamReady(stream, message)
├─ File: relay.go:400
├─ Triggered when: NAT peer sends MessageTypeRelayReceiveStreamReady
│
├─ Step 1: Lookup session
│   ├─ SessionID from message
│   └─ Lookup in sessions map
│
├─ Step 2: Store receive stream
│   ├─ session.ReceiveStream = stream
│   ├─ This stream is now used for ALL forwarded messages
│   └─ Stream remains open for entire session lifetime
│
└─ Step 3: Mark stream ready
    └─ session.ReceiveStreamReady = true
```

**Logs to check:**
- "Receive stream ready: session_id=X"
- "Stored receive stream for session: X"

**Potential Issues:**
- **Session not found** - Registration might have failed
- **Receive stream already set** - Duplicate setup (check relay.go:410)
- **Stream closed prematurely** - NAT peer disconnected

---

#### Phase 3: Message Forwarding
**Files:** `internal/p2p/relay.go`

```
RelayPeer.HandleRelayForward(stream, message)
├─ File: relay.go:600
├─ Triggered when: NAT peer A sends message to NAT peer B via relay
│
├─ Step 1: Validate source session
│   ├─ SessionID from message
│   ├─ Lookup source session
│   └─ Verify session is active
│
├─ Step 2: Extract forwarding info
│   ├─ SourcePeerID (NAT peer A)
│   ├─ TargetPeerID (NAT peer B)
│   ├─ MessageType (e.g., "service_search", "job_request")
│   └─ Payload (actual message bytes)
│
├─ Step 3: Lookup target session
│   ├─ Lookup in registeredClients map
│   │   └─ Key: TargetPeerID
│   └─ Get target's RelaySession
│
├─ Step 4: Store correlation ID (for responses)
│   ├─ Generate correlation ID
│   ├─ Store: pendingRequests[correlationID] = stream
│   │   └─ Used to route response back to source
│   └─ File: relay.go:650
│
└─ Step 5: Forward to target
    └─> forwardDataToTarget(targetSession, payload)
        ├─ File: relay.go:750
        ├─ Inject SourcePeerID into payload
        ├─ Write length prefix (4 bytes)
        ├─ Write payload to targetSession.ReceiveStream
        └─ Update targetSession.EgressBytes
```

**Flow diagram:**
```
NAT Peer A                    Relay                    NAT Peer B
    |                           |                           |
    |--relay_forward----------->|                           |
    |  (target=B, payload)      |                           |
    |                           |                           |
    |                           |--length prefix (4 bytes)->|
    |                           |--payload------------------>|
    |                           |  (via B's ReceiveStream)  |
    |                           |                           |
    |                           |<-response-----------------|
    |                           |  (via new stream)         |
    |<-response-----------------|                           |
    |  (via correlation ID)     |                           |
```

**Logs to check:**
- "Forwarding message: source=X, target=Y, type=Z"
- "Message forwarded: correlation_id=X, bytes=Y"
- "Target peer not found: X" (error case)
- "Receive stream not ready for target: X" (error case)

**Potential Issues:**
- **Target not registered** - Peer B never connected to relay
- **Receive stream nil** - Peer B didn't send MessageTypeRelayReceiveStreamReady
- **Write errors** - Peer B's connection closed (check relay.go:760)
- **Correlation ID leak** - Responses never received (check cleanup in relay.go:680)

---

#### Phase 4: Session Monitoring
**Files:** `internal/p2p/relay.go`

```
RelayPeer.monitorSession(sessionID)
├─ File: relay.go:500
├─ Goroutine: Per session
├─ Interval: session.KeepaliveInterval (5s default)
├─ Timeout: 4x interval (120s default)
│
└─ Loop:
    ├─ Check: time.Since(session.LastKeepalive)
    ├─ If > timeout:
    │   └─> terminateSession(sessionID)
    │       ├─ File: relay.go:900
    │       ├─ Remove from sessions map
    │       ├─ Remove from registeredClients map
    │       ├─ Record final IngressBytes/EgressBytes
    │       └─ Close ReceiveStream
    │
    └─ If < timeout:
        └─ Continue monitoring
```

**Logs to check:**
- "Session keepalive timeout: session_id=X, last_keepalive=Y"
- "Terminating session: session_id=X, duration=Y, ingress=Z, egress=W"
- "Session terminated: X"

**Potential Issues:**
- **Premature termination** - Keepalive timeout too aggressive
- **Session not removed** - Memory leak (check relay.go:920)
- **Stale sessions** - Monitoring goroutine crashed (check panic recovery)

---

## Reconnection Logic

### Stale Connection Detection

#### NAT Peer Side
**Files:** `internal/p2p/relay_manager.go`, `internal/p2p/network_state_monitor.go`

**Method 1: Keepalive Timeout**
```
RelayManager.sendKeepalives()
├─ File: relay_manager.go:700
├─ Detection: Failed to send ping OR no pong received
├─ Timeout: 5 seconds (per ping)
├─ Trigger: handleRelayConnectionLoss()
│
└─ Conditions triggering detection:
    ├─ Stream creation failed
    ├─ Write error on stream
    ├─ Read timeout (5s) waiting for pong
    └─ Invalid response (not MessageTypePong)
```

**Method 2: Receive Stream Errors**
```
RelayManager.listenForRelayedMessages()
├─ File: relay_manager.go:600
├─ Detection: Read errors on receive stream
├─ Trigger: handleRelayConnectionLoss()
│
└─ Conditions:
    ├─ Stream closed by relay
    ├─ Read timeout
    ├─ Invalid message length (<4 bytes)
    └─ Connection closed
```

**Method 3: Network State Changes**
```
NetworkStateMonitor.checkForChanges()
├─ File: network_state_monitor.go:100
├─ Detection: IP or NAT type changed
├─ Interval: 5 minutes (network_monitor_interval_minutes)
├─ Trigger: ReconnectRelay()
│
└─ Monitored changes:
    ├─ Local IP changed
    ├─ External IP changed
    ├─ NAT type changed
    └─ Local subnet changed
```

**Logs to check:**
- "Keepalive failed: session_id=X, error=Y"
- "Receive stream error: error=X"
- "Network state changed: old_ip=X, new_ip=Y"
- "NAT type changed: old=X, new=Y"
- "Relay connection loss detected"

---

#### Relay Side
**Files:** `internal/p2p/relay.go`

**Method 1: Keepalive Timeout**
```
RelayPeer.monitorSession(sessionID)
├─ File: relay.go:500
├─ Timeout: 4x keepalive interval (120s default)
├─ Check: time.Since(session.LastKeepalive)
├─ Action: terminateSession()
│
└─ LastKeepalive updated by:
    ├─ HandleRelayRegister() - on registration
    ├─ HandlePing() - on each ping received
    └─ File: relay.go:850
```

**Method 2: Stream Write Errors**
```
RelayPeer.forwardDataToTarget()
├─ File: relay.go:750
├─ Detection: Write error to ReceiveStream
├─ Action: Clear ReceiveStream, continue serving
│   └─ Graceful degradation (don't terminate session)
│
└─ Conditions:
    ├─ Stream closed
    ├─ Connection closed
    └─ Write timeout
```

**Logs to check:**
- "Session keepalive timeout: session_id=X"
- "Write error on receive stream: session_id=X, error=Y"
- "Session terminated: session_id=X"

---

### Reconnection Strategy

**Files:** `internal/p2p/relay_manager.go`

#### 4-Phase Reconnection Process

```
RelayManager.handleRelayConnectionLoss()
├─ File: relay_manager.go:800
├─ Triggered by: Keepalive timeout, stream errors, or network changes
│
├─ PHASE 1: Immediate Retry (Same Relay)
│   ├─ Attempts: 3
│   ├─ Delay: 2 seconds between attempts
│   ├─ Total duration: ~6 seconds
│   │
│   └─ Loop:
│       ├─> DisconnectRelay()
│       │   ├─ File: relay_manager.go:1000
│       │   ├─ Send relay_disconnect message
│       │   ├─ Report final traffic stats
│       │   └─ Close QUIC connection
│       │
│       ├─ Wait 2 seconds
│       │
│       └─> ConnectToRelay(failedRelay)
│           ├─ Success: Return (reconnected)
│           └─ Failure: Continue to next attempt
│
├─ PHASE 2: Mark Problematic
│   ├─ failedRelay.FailureCount++
│   ├─ failedRelay.LastFailure = time.Now()
│   ├─ If FailureCount >= 3:
│   │   └─> RelaySelector.RemoveCandidate(failedRelay)
│   │       └─ File: relay_selector.go:450
│   └─ File: relay_manager.go:850
│
├─ PHASE 3: Fast Failover (Alternative Relay)
│   ├─ Use existing latency measurements (no re-measurement)
│   ├─ Duration: <1 second
│   │
│   └─> SelectAndConnectRelay()
│       ├─ File: relay_manager.go:200
│       ├─> RelaySelector.SelectBestRelay()
│       │   └─ Uses cached latency measurements
│       │
│       └─> ConnectToRelay(selectedRelay)
│           ├─ Success: Return (failover complete)
│           └─ Failure: Continue to Phase 4
│
└─ PHASE 4: Rediscovery (No Candidates)
    ├─ All relay candidates exhausted
    ├─ Duration: 10-30 seconds
    │
    ├─> rediscoverRelayCandidates()
    │   ├─ File: relay_manager.go:1100
    │   ├─ Query known_peers for is_relay == true
    │   ├─ Fetch metadata from DHT (BEP44)
    │   ├─> RelaySelector.AddCandidate() for each
    │   └─ Returns new candidate list
    │
    ├─> EvaluateAndSelectRelay()
    │   ├─ File: relay_manager.go:250
    │   ├─ Measure all new candidates
    │   └─ Select best relay
    │
    └─> If still fails:
        └─> ensureDHTDisconnected()
            ├─ Update DHT metadata: relay_disconnected = true
            ├─ Allows peers to know this peer is unreachable
            └─ File: relay_manager.go:1200
```

**Recovery Timeline:**
```
Time    Phase          Action                          Duration
----------------------------------------------------------------------
0s      PHASE 1        Retry same relay (attempt 1)    ~2s
2s      PHASE 1        Retry same relay (attempt 2)    ~2s
4s      PHASE 1        Retry same relay (attempt 3)    ~2s
6s      PHASE 2        Mark relay problematic           <1s
7s      PHASE 3        Fast failover to alternative     <1s
8s      Success        OR continue to Phase 4           -
8s      PHASE 4        Rediscover + measure relays      10-30s
```

**Logs to check:**
- "Relay connection loss detected: session_id=X"
- "Attempting immediate retry (1/3): relay=X"
- "Immediate retry failed: attempt=X, error=Y"
- "Marking relay as problematic: relay=X, failures=Y"
- "Attempting fast failover: relay=X"
- "Fast failover successful: relay=X"
- "All relay candidates exhausted, rediscovering..."
- "Rediscovered X relay candidates"
- "Relay reconnection failed completely"

**Potential Issues:**
- **Infinite retry loop** - Check failure count increment in relay_manager.go:850
- **Failed to failover** - No alternative relays (check candidate pool)
- **Rediscovery timeout** - DHT query failures (check DHT logs)
- **Premature DHT disconnect** - Check ensureDHTDisconnected() trigger
- **Stale connection reused** - DisconnectRelay() not cleaning up properly

---

## Relay Selection

### Selection Algorithm

**Files:** `internal/p2p/relay_selector.go`

```
RelaySelector.SelectBestRelay()
├─ File: relay_selector.go:300
│
├─ PRIORITY 1: Preferred Relay
│   ├─ Check database for preferred relay
│   ├─ File: relay_selector.go:310
│   ├─ Verify meets minimum criteria:
│   │   ├─ IsAvailable == true
│   │   ├─ Latency > 0 && < maxLatency (500ms)
│   │   ├─ ReputationScore >= minReputation (0.3)
│   │   └─ PricingPerGB <= maxPricing (0.01)
│   └─ If meets criteria: Always select (skip score calculation)
│
└─ PRIORITY 2: Score-Based Selection
    ├─ Filter candidates:
    │   ├─ IsAvailable == true (ping/pong succeeded)
    │   ├─ Latency measured && < maxLatency
    │   ├─ ReputationScore >= minReputation
    │   └─ PricingPerGB <= maxPricing
    │
    ├─ Calculate score for each candidate:
    │   ├─ latencyScore = 1.0 - (latency_ms / maxLatency_ms)
    │   │   └─ Example: 50ms with 500ms max = 1.0 - 0.1 = 0.9
    │   │
    │   ├─ reputationScore = ReputationScore (0.0-1.0)
    │   │   └─ From DHT metadata or default 0.5
    │   │
    │   ├─ pricingScore = 1.0 - (pricing / maxPricing)
    │   │   └─ Example: $0.005/GB with $0.01 max = 1.0 - 0.5 = 0.5
    │   │
    │   └─ totalScore = (latencyScore * 0.5) + (reputationScore * 0.3) + (pricingScore * 0.2)
    │       └─ Weights: Latency 50%, Reputation 30%, Pricing 20%
    │
    └─ Select candidate with highest totalScore
```

**Switch Decision:**
```
RelaySelector.ShouldSwitchRelay(currentRelay, candidateRelay)
├─ File: relay_selector.go:400
│
├─ Always switch if:
│   └─ Candidate is preferred relay
│
└─ Otherwise, calculate improvement:
    ├─ currentScore = (current.latency * 0.7) + (current.reputation * 0.3)
    ├─ candidateScore = (candidate.latency * 0.7) + (candidate.reputation * 0.3)
    ├─ improvement = (currentScore - candidateScore) / currentScore
    ├─ threshold = 0.2 (20% improvement required)
    └─ Switch if: improvement > threshold
```

**Example calculation:**
```
Current Relay:  latency=100ms, reputation=0.5, pricing=$0.008/GB
Candidate:      latency=50ms,  reputation=0.7, pricing=$0.005/GB

Scores:
- latencyScore:    1.0 - (50/500) = 0.90
- reputationScore: 0.7
- pricingScore:    1.0 - (0.005/0.01) = 0.50
- totalScore:      (0.90 * 0.5) + (0.7 * 0.3) + (0.50 * 0.2)
                 = 0.45 + 0.21 + 0.10
                 = 0.76

Improvement check:
- currentScore:   (100 * 0.7) + (0.5 * 0.3) = 70.15
- candidateScore: (50 * 0.7) + (0.7 * 0.3) = 35.21
- improvement:    (70.15 - 35.21) / 70.15 = 49.8%
- Result: SWITCH (exceeds 20% threshold)
```

**Logs to check:**
- "Using preferred relay: peer_id=X"
- "Candidate filtered out: relay=X, reason=Y"
- "Relay scores: relay=X, latency=Y, reputation=Z, pricing=W, total=T"
- "Selected best relay: relay=X, score=Y"
- "Should switch relay: current=X, candidate=Y, improvement=Z%"

**Potential Issues:**
- **No candidates pass filters** - Check threshold configuration
- **Same relay always selected** - Check score calculation weights
- **Frequent relay switching** - Lower switchThreshold (currently 20%)
- **Preferred relay ignored** - Not meeting minimum criteria

---

### Latency Measurement

**Files:** `internal/p2p/relay_selector.go`

```
RelaySelector.MeasureAllCandidates()
├─ File: relay_selector.go:200
├─ Batch size: 3 relays at a time
├─ Timeout: 5 seconds per measurement
│
└─ Process:
    ├─ For each batch of 3:
    │   │
    │   ├─ Step 1: Pre-establish QUIC connections
    │   │   └─> QUICPeer.ConnectToPeer(candidate.Endpoint)
    │   │       ├─ Parallel connection attempts
    │   │       └─ Reuse existing if already connected
    │   │
    │   ├─ Step 2: Measure latency over existing connection
    │   │   └─> MeasureLatency(candidate)
    │   │       ├─ File: relay_selector.go:150
    │   │       ├─ Open ephemeral stream
    │   │       ├─ Send MessageTypePing
    │   │       ├─ Wait for MessageTypePong
    │   │       ├─ Calculate RTT
    │   │       └─ candidate.Latency = RTT
    │   │
    │   ├─ Step 3: Mark availability
    │   │   ├─ Success: candidate.IsAvailable = true
    │   │   └─ Failure: candidate.IsAvailable = false, store error
    │   │
    │   └─ Step 4: Cleanup non-selected connections
    │       ├─ Close connections to relays not selected
    │       └─ Preserve connection to selected relay
    │
    └─ Return all measured candidates
```

**Optimization: Connection Reuse**
```
Before measurement:
├─ Check if QUIC connection already exists
├─ If exists: Reuse for latency measurement
└─ If not: Establish new connection

After measurement:
├─ If relay selected: Keep connection open
│   └─ Used for registration in ConnectToRelay()
└─ If relay not selected: Close connection
    └─ Free resources
```

**Logs to check:**
- "Measuring latency for batch: candidates=X"
- "Pre-establishing connection to relay: X"
- "Measuring latency: relay=X"
- "Latency measured: relay=X, latency=Yms"
- "Latency measurement failed: relay=X, error=Y"
- "Relay marked unavailable: relay=X, reason=Y"
- "Cleaning up connection to relay: X"

**Potential Issues:**
- **Measurement timeout** - Increase timeout in relay_selector.go:160
- **All measurements fail** - Network connectivity issues
- **Inaccurate latency** - QUIC handshake overhead (should reuse connections)
- **Connection leak** - Verify cleanup in relay_selector.go:280

---

## Stream Management

### Stream Types and Lifecycles

**Files:** `internal/p2p/relay_manager.go`, `internal/p2p/relay.go`

#### 1. Registration Stream (Ephemeral)
```
Purpose:  Send relay_register, receive relay_accept
Lifetime: <1 second (opened, message exchanged, closed)
Direction: NAT peer → Relay

Flow:
1. OpenStreamSync()                    [relay_manager.go:420]
2. Write MessageTypeRelayRegister      [relay_manager.go:430]
3. Read MessageTypeRelayAccept         [relay_manager.go:440]
4. Stream automatically closes         [QUIC stream lifecycle]
```

**Potential Issues:**
- **Stream not closed** - QUIC stream limit reached (check quic.go:360)
- **Reused incorrectly** - Should be new stream per registration

---

#### 2. Receive Stream (Long-lived)
```
Purpose:  Relay forwards messages to NAT peer
Lifetime: Entire relay session (minutes to hours)
Direction: Relay → NAT peer

Setup (NAT peer side):
1. OpenStreamSync()                        [relay_manager.go:480]
2. Send MessageTypeRelayReceiveStreamReady [relay_manager.go:490]
3. Start listenForRelayedMessages()        [relay_manager.go:600]
   └─ Goroutine blocks reading from stream

Protocol:
├─ Message format: [4-byte length prefix][payload]
├─ Read loop:
│   ├─ Read 4 bytes (length)
│   ├─ Read N bytes (payload)
│   ├─> HandleRelayedMessage(payload)
│   │   └─ File: quic.go:800
│   └─ Repeat
│
└─ Error handling:
    ├─ Read error → handleRelayConnectionLoss()
    ├─ Invalid length → log warning, continue
    └─ Stream closed → handleRelayConnectionLoss()

Storage (Relay side):
└─ session.ReceiveStream = stream  [relay.go:420]
```

**Logs to check:**
- "Receive stream opened: session_id=X"
- "Listening for relayed messages: session_id=X"
- "Received relayed message: length=X bytes"
- "Receive stream error: error=X"
- "Receive stream closed: session_id=X"

**Potential Issues:**
- **Stream closed prematurely** - NAT peer disconnected or reconnected
- **Message corruption** - Length prefix mismatch (check relay.go:760)
- **Goroutine leak** - listenForRelayedMessages() not exiting (check relay_manager.go:650)
- **Buffer overflow** - Large message (>1MB, check limits in quic.go:810)

---

#### 3. Keepalive Stream (Ephemeral)
```
Purpose:  Periodic ping/pong for connection health
Lifetime: <1 second per ping
Interval: 5 seconds
Timeout:  5 seconds (per ping), 120 seconds (session timeout)
Direction: NAT peer → Relay → NAT peer

Flow:
1. Every 5 seconds:
   ├─ OpenStreamSync()               [relay_manager.go:710]
   ├─ Write MessageTypePing          [relay_manager.go:720]
   │   └─ Includes: SessionID
   ├─ Read MessageTypePong           [relay_manager.go:730]
   │   └─ Timeout: 5 seconds
   └─ Close stream                   [automatic]

2. Relay side (HandlePing):
   ├─ Update session.LastKeepalive   [relay.go:860]
   ├─ Write MessageTypePong          [relay.go:870]
   └─ Return
```

**Configuration:**
- `relay_keepalive_interval`: 5 seconds
- `relay_keepalive_timeout`: 5 seconds (per ping)
- Session timeout: 4x interval = 120 seconds (relay side)

**Logs to check:**
- "Sending keepalive: session_id=X"
- "Keepalive pong received: session_id=X"
- "Keepalive timeout: session_id=X"
- "Keepalive stream error: error=X"

**Potential Issues:**
- **Frequent timeouts** - Network latency >5s (increase timeout)
- **No pongs received** - Relay not responding (check relay logs)
- **Stream creation failures** - QUIC connection degraded

---

#### 4. Forward Stream (Ephemeral)
```
Purpose:  NAT peer sends relay_forward messages
Lifetime: <1 second (opened, message sent, response received, closed)
Direction: NAT peer → Relay → Target peer

Flow (Source peer):
1. OpenStreamSync()                    [quic.go:400]
2. Write MessageTypeRelayForward       [quic.go:410]
   ├─ SessionID
   ├─ SourcePeerID
   ├─ TargetPeerID
   ├─ MessageType ("service_search", "job_request", etc.)
   └─ Payload (actual message bytes)
3. Wait for response (timeout: 30s)    [quic.go:420]
4. Read response                       [quic.go:430]
5. Close stream                        [automatic]

Flow (Relay):
1. Receive relay_forward               [relay.go:600]
2. Lookup target session               [relay.go:620]
3. Generate correlation ID             [relay.go:650]
4. Store: pendingRequests[corrID] = stream [relay.go:660]
5. Forward to target via ReceiveStream [relay.go:750]
6. Target sends response (new stream)  [relay.go:1000]
7. Lookup correlation ID               [relay.go:1010]
8. Write response to original stream   [relay.go:1020]
9. Cleanup: delete pendingRequests     [relay.go:1030]

Flow (Target peer):
1. Receive via ReceiveStream           [relay_manager.go:610]
2. Process message                     [quic.go:820]
3. OpenStreamSync() to relay           [quic.go:850]
4. Write response                      [quic.go:860]
5. Close stream                        [automatic]
```

**Correlation ID Management:**
```
Relay maintains:
├─ pendingRequests map[string]quic.Stream
│   └─ Key: correlation ID (UUID)
│   └─ Value: original forward stream
│
├─ Lifecycle:
│   ├─ Created: When relay_forward received
│   ├─ Used: When response received from target
│   └─ Deleted: After response written to source
│
└─ Timeout: 30 seconds (cleanup stale entries)
    └─ File: relay.go:680
```

**Logs to check:**
- "Forwarding message: source=X, target=Y, type=Z"
- "Correlation ID stored: id=X"
- "Received response: correlation_id=X"
- "Response forwarded: correlation_id=X"
- "Correlation ID not found: id=X" (timeout or error)

**Potential Issues:**
- **Correlation ID leak** - Responses never received (check cleanup in relay.go:1040)
- **Response timeout** - Target peer not responding (>30s)
- **Wrong stream reused** - Should be new stream per forward
- **Stream not closed** - Memory leak (check QUIC stream limits)

---

## Key Data Structures

### RelaySession (relay.go:50)
```go
type RelaySession struct {
    SessionID         string         // UUID generated at registration
    ClientPeerID      string         // Ed25519 public key (persistent ID)
    ClientNodeID      string         // DHT node ID (ephemeral, changes per run)
    Connection        *quic.Conn     // QUIC connection to NAT peer
    ReceiveStream     *quic.Stream   // Pre-opened stream for forwarding messages
    StartTime         time.Time      // Session creation timestamp
    LastKeepalive     time.Time      // Updated on each ping
    IngressBytes      int64          // Bytes received from NAT peer
    EgressBytes       int64          // Bytes sent to NAT peer
    KeepaliveInterval time.Duration  // Default: 5 seconds
}

// Usage:
// - Stored in relay.sessions map (key: SessionID)
// - Stored in relay.registeredClients map (key: ClientPeerID)
// - Allows lookup by session ID or peer ID
```

**Critical Fields for Debugging:**
- `LastKeepalive` - Check if stale (>120s = dead session)
- `ReceiveStream` - Should be non-nil after setup
- `IngressBytes/EgressBytes` - Monitor for traffic anomalies

**Logs to correlate:**
- "Session created: session_id=X, peer_id=Y"
- "Last keepalive: session_id=X, time=Y"
- "Session traffic: session_id=X, ingress=Y, egress=Z"

---

### RelayCandidate (relay_selector.go:30)
```go
type RelayCandidate struct {
    PeerID          string         // Ed25519 public key (persistent)
    NodeID          string         // DHT node ID (ephemeral)
    Endpoint        string         // "IP:port" for QUIC connection
    Latency         time.Duration  // Round-trip time from MeasureLatency()
    ReputationScore float64        // 0.0-1.0 (from DHT metadata or default 0.5)
    PricingPerGB    float64        // USD per GB (from DHT metadata)
    Capacity        int            // Max concurrent sessions (from DHT metadata)
    LastSeen        time.Time      // Last discovery or measurement
    FailureCount    int            // Consecutive connection failures
    LastFailure     time.Time      // Timestamp of most recent failure
    IsAvailable     bool           // Ping/pong succeeded
    UnavailableMsg  string         // Reason if IsAvailable == false
}

// Selection criteria:
// - IsAvailable == true
// - Latency > 0 && < 500ms (configurable)
// - ReputationScore >= 0.3 (configurable)
// - PricingPerGB <= 0.01 (configurable)
// - FailureCount < 3 (removed if >= 3)
```

**Critical Fields for Debugging:**
- `FailureCount` - Check if relay being removed prematurely
- `IsAvailable` - Should be true for selected relay
- `Latency` - Should be measured (>0) before selection
- `UnavailableMsg` - Explains why relay not selected

**Logs to correlate:**
- "Relay candidate added: peer_id=X, endpoint=Y"
- "Latency measured: relay=X, latency=Yms"
- "Relay marked unavailable: relay=X, reason=Y"
- "Relay removed: relay=X, failures=Y"

---

### NetworkState (network_state_monitor.go:30)
```go
type NetworkState struct {
    LocalIP       string   // Local network IP (e.g., 192.168.1.100)
    ExternalIP    string   // Public IP from STUN (e.g., 1.2.3.4)
    NATType       NATType  // FullCone, Restricted, PortRestricted, Symmetric, None
    LocalSubnet   string   // CIDR notation (e.g., 192.168.1.0/24)
    DetectionTime time.Time
}

// Monitored changes:
// - LocalIP changed → Reconnect relay (new private endpoint)
// - ExternalIP changed → Reconnect relay (new public endpoint)
// - NATType changed → Reconnect relay (strategy may change)
// - LocalSubnet changed → Update topology
```

**Change Detection (network_state_monitor.go:120):**
```go
func (n *NetworkStateMonitor) hasChanged(old, new NetworkState) bool {
    return old.LocalIP != new.LocalIP ||
           old.ExternalIP != new.ExternalIP ||
           old.NATType != new.NATType ||
           old.LocalSubnet != new.LocalSubnet
}
```

**Logs to correlate:**
- "Network state changed: old_ip=X, new_ip=Y"
- "NAT type changed: old=X, new=Y"
- "External IP changed: old=X, new=Y"
- "Triggering relay reconnection due to network change"

---

## Debugging Guide

### Common Issues and Log Patterns

#### Issue 1: Stale Connections Not Detected
**Symptoms:**
- Peers appear connected but messages not delivered
- No keepalive timeout logs
- Receive stream still open

**Investigation:**
1. Check NAT peer keepalive logs:
   ```
   grep "Sending keepalive" logs/peer.log
   grep "Keepalive timeout" logs/peer.log
   ```
   - Should see ping every 5 seconds
   - Should see timeout if relay unreachable

2. Check relay session monitor logs:
   ```
   grep "Session keepalive timeout" logs/relay.log
   grep "Last keepalive" logs/relay.log
   ```
   - Should see timeout after 120 seconds of no pings

3. Verify keepalive is actually running:
   - Check `relay_manager.go:700` - `sendKeepalives()` goroutine started
   - Check `relay.go:500` - `monitorSession()` goroutine started

**Common Causes:**
- Keepalive goroutine crashed (check panic logs)
- Timeout too long (increase sensitivity)
- Stream errors silently ignored (check error handling)

**Fix Locations:**
- `relay_manager.go:750` - Increase timeout detection sensitivity
- `relay.go:520` - Decrease session timeout (currently 4x interval)

---

#### Issue 2: Wrong Stream Reused in Established Connections
**Symptoms:**
- Messages sent to wrong peer
- Correlation ID not found errors
- Response routing failures

**Investigation:**
1. Check stream lifecycle logs:
   ```
   grep "Receive stream opened" logs/peer.log
   grep "Receive stream closed" logs/peer.log
   ```
   - Each session should have exactly ONE receive stream
   - Stream should persist for entire session

2. Check correlation ID management:
   ```
   grep "Correlation ID stored" logs/relay.log
   grep "Correlation ID not found" logs/relay.log
   ```
   - Each forward should create new correlation ID
   - Correlation ID should be deleted after response

3. Verify stream uniqueness:
   - `relay.go:420` - Ensure `session.ReceiveStream` not overwritten
   - `relay_manager.go:480` - Ensure new stream opened per session

**Common Causes:**
- Receive stream overwritten during reconnection
- Correlation ID collision (UUID not unique)
- Stream handle reused after close

**Fix Locations:**
- `relay.go:410` - Check if receive stream already set
- `relay.go:650` - Add collision detection for correlation IDs
- `relay_manager.go:600` - Close old receive stream before opening new

---

#### Issue 3: Relay Selection Always Picks Same Relay
**Symptoms:**
- Other relays never selected despite better latency
- Preferred relay used even if slow
- No relay switching logs

**Investigation:**
1. Check relay candidate pool:
   ```
   grep "Relay candidate added" logs/peer.log
   grep "Relay removed" logs/peer.log
   ```
   - Should have multiple candidates (at least 3-5)
   - Check if candidates being removed prematurely

2. Check latency measurements:
   ```
   grep "Latency measured" logs/peer.log
   grep "Relay scores" logs/peer.log
   ```
   - All candidates should have latency measured
   - Verify score calculation is correct

3. Check switch decision:
   ```
   grep "Should switch relay" logs/peer.log
   ```
   - Should show improvement percentage
   - Verify threshold (20%) is appropriate

**Common Causes:**
- Preferred relay always meets criteria (skips score calculation)
- Switch threshold too high (20% improvement hard to achieve)
- Latency measurements outdated

**Fix Locations:**
- `relay_selector.go:310` - Add logging for preferred relay bypass
- `relay_selector.go:400` - Lower switchThreshold from 0.2 to 0.1
- `relay_selector.go:200` - Add periodic re-measurement

---

#### Issue 4: Frequent Reconnections (Flapping)
**Symptoms:**
- Relay connection repeatedly drops and reconnects
- Keepalive timeout every few minutes
- Network state changed logs frequent

**Investigation:**
1. Check network stability:
   ```
   grep "Network state changed" logs/peer.log
   grep "NAT type changed" logs/peer.log
   ```
   - Frequent changes indicate unstable network
   - Check if IP actually changing or false positive

2. Check keepalive patterns:
   ```
   grep "Keepalive timeout" logs/peer.log
   grep "Keepalive failed" logs/peer.log
   ```
   - Timeouts at regular intervals = network issue
   - Random timeouts = relay overload

3. Check relay health:
   ```
   grep "Session terminated" logs/relay.log
   grep "Write error on receive stream" logs/relay.log
   ```
   - Many session terminations = relay issue
   - Write errors = NAT peer connectivity issue

**Common Causes:**
- Network monitoring too sensitive (5min interval may catch IP rotation)
- Keepalive timeout too aggressive (5s may be too short)
- Relay overloaded (check relay capacity)

**Fix Locations:**
- `network_state_monitor.go:100` - Increase monitoring interval to 10 minutes
- `relay_manager.go:710` - Increase keepalive timeout to 10 seconds
- `relay.go:520` - Increase session timeout to 300 seconds (5 minutes)

---

#### Issue 5: Relay Reconnection Stuck in Phase 1
**Symptoms:**
- Reconnection attempts same relay 3 times
- Never proceeds to failover
- "Attempting immediate retry" logs repeated

**Investigation:**
1. Check reconnection phase logs:
   ```
   grep "Attempting immediate retry" logs/peer.log
   grep "Attempting fast failover" logs/peer.log
   grep "All relay candidates exhausted" logs/peer.log
   ```
   - Should progress through phases if Phase 1 fails
   - Check if Phase 2+ ever reached

2. Check relay candidate state:
   ```
   grep "Marking relay as problematic" logs/peer.log
   grep "Relay removed" logs/peer.log
   ```
   - Failed relay should be marked
   - Should be removed after 3 failures

3. Verify phase progression logic:
   - `relay_manager.go:830` - Check if loop exits correctly
   - `relay_manager.go:850` - Verify Phase 2 is reached

**Common Causes:**
- Infinite retry loop (return statement in wrong place)
- Relay not marked as problematic (failure count not incremented)
- Alternative candidates not available

**Fix Locations:**
- `relay_manager.go:840` - Ensure break after max attempts
- `relay_manager.go:850` - Verify FailureCount incremented
- `relay_manager.go:870` - Add logging for phase transitions

---

### Log Correlation Examples

#### Successful Connection Flow
```
NAT Peer logs:
[INFO] NAT type detected: Symmetric
[INFO] Discovered relay candidate: peer_id=abc123, endpoint=1.2.3.4:5678
[INFO] Measuring latency for relay: abc123
[INFO] Latency measured: relay=abc123, latency=50ms
[INFO] Selected relay: peer_id=abc123, score=0.76
[INFO] Connecting to relay: abc123
[INFO] QUIC connection established to relay: abc123
[INFO] Sent relay registration: session_id=xyz789
[INFO] Relay accepted registration: session_id=xyz789
[INFO] Receive stream ready: session_id=xyz789
[INFO] Started listening for relayed messages
[INFO] Sending keepalive: session_id=xyz789
[INFO] Keepalive pong received: session_id=xyz789

Relay logs:
[INFO] Relay registration received: peer_id=abc123, nat_type=Symmetric
[INFO] Created relay session: session_id=xyz789, peer_id=abc123
[INFO] Sent relay acceptance: session_id=xyz789
[INFO] Receive stream ready: session_id=xyz789
[INFO] Stored receive stream for session: xyz789
[INFO] Ping received: session_id=xyz789
[INFO] Updated last keepalive: session_id=xyz789
```

**Timeline:** ~1-2 seconds from discovery to active session

---

#### Successful Reconnection Flow (Phase 3 Failover)
```
NAT Peer logs:
[WARN] Keepalive timeout: session_id=xyz789
[INFO] Relay connection loss detected: session_id=xyz789
[INFO] Attempting immediate retry (1/3): relay=abc123
[ERROR] Immediate retry failed: attempt=1, error=connection refused
[INFO] Attempting immediate retry (2/3): relay=abc123
[ERROR] Immediate retry failed: attempt=2, error=connection refused
[INFO] Attempting immediate retry (3/3): relay=abc123
[ERROR] Immediate retry failed: attempt=3, error=connection refused
[WARN] Marking relay as problematic: relay=abc123, failures=1
[INFO] Attempting fast failover: relay=def456
[INFO] Fast failover successful: relay=def456
[INFO] Connected to relay: session_id=uvw012
[INFO] Receive stream ready: session_id=uvw012
```

**Timeline:** ~8 seconds (6s Phase 1 + 2s Phase 3)

---

#### Failed Reconnection (Full Rediscovery)
```
NAT Peer logs:
[WARN] Keepalive timeout: session_id=xyz789
[INFO] Relay connection loss detected: session_id=xyz789
[INFO] Attempting immediate retry (1/3): relay=abc123
[ERROR] Immediate retry failed: attempt=1, error=connection refused
[INFO] Attempting immediate retry (2/3): relay=abc123
[ERROR] Immediate retry failed: attempt=2, error=connection refused
[INFO] Attempting immediate retry (3/3): relay=abc123
[ERROR] Immediate retry failed: attempt=3, error=connection refused
[WARN] Marking relay as problematic: relay=abc123, failures=3
[WARN] Relay removed: relay=abc123, failures=3
[INFO] Attempting fast failover: relay=def456
[ERROR] Fast failover failed: error=no available candidates
[WARN] All relay candidates exhausted, rediscovering...
[INFO] Querying DHT for relay peers
[INFO] Rediscovered 5 relay candidates
[INFO] Measuring latency for batch: candidates=5
[INFO] Latency measured: relay=ghi789, latency=80ms
[INFO] Selected relay: peer_id=ghi789, score=0.68
[INFO] Connected to relay: session_id=rst345
```

**Timeline:** ~30 seconds (6s Phase 1 + 1s Phase 2/3 + 20s Phase 4)

---

### Key File Reference

**Connection Management:**
- `internal/p2p/relay_manager.go` - NAT peer relay connection management
- `internal/p2p/relay.go` - Relay server session management
- `internal/p2p/relay_selector.go` - Relay selection algorithm
- `internal/p2p/quic.go` - QUIC transport layer

**Network Monitoring:**
- `internal/p2p/network_state_monitor.go` - IP/NAT change detection
- `internal/p2p/nat_detector.go` - NAT type detection
- `internal/p2p/nat_topology.go` - Topology and strategy detection

**NAT Traversal:**
- `internal/p2p/hole_puncher.go` - UDP hole punching
- `internal/p2p/messages.go` - Protocol messages

**Orchestration:**
- `internal/core/peer.go` - Component initialization

---

### Performance Tuning Knobs

**Connection Stability (relay_manager.go):**
- `relay_keepalive_interval`: 5s → Increase to reduce overhead
- `relay_keepalive_timeout`: 5s → Increase for unstable networks
- `relay_session_timeout`: 120s → Increase to reduce false positives

**Reconnection Aggressiveness (relay_manager.go):**
- `immediate_retry_attempts`: 3 → Decrease to failover faster
- `immediate_retry_delay`: 2s → Decrease to reconnect faster
- `failure_threshold`: 3 → Increase to be more forgiving

**Relay Selection (relay_selector.go):**
- `relay_max_latency`: 500ms → Decrease for better QoS
- `relay_min_reputation`: 0.3 → Increase for more reliable relays
- `relay_switch_threshold`: 0.2 → Decrease to switch more frequently
- `relay_batch_size`: 3 → Increase to measure faster

**Network Monitoring (network_state_monitor.go):**
- `network_monitor_interval_minutes`: 5 → Increase to reduce reconnections

---

## Conclusion

This document provides a comprehensive guide to the relay connection architecture. Use the log patterns and file references to:

1. **Trace connection flows** - Follow logs through each phase
2. **Identify bottlenecks** - Compare actual vs expected timelines
3. **Debug stream issues** - Verify stream lifecycle and reuse
4. **Tune performance** - Adjust configuration knobs based on observations
5. **Root cause failures** - Correlate NAT peer and relay logs

For additional debugging, enable verbose logging in:
- `internal/p2p/relay_manager.go` - Add debug logs in critical functions
- `internal/p2p/relay.go` - Add session state logging
- `internal/p2p/relay_selector.go` - Add candidate evaluation details
