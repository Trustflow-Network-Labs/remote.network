# Hole Punching Protocol

## Overview

The hole punching protocol enables direct peer-to-peer connections between NAT peers, avoiding relay overhead when possible. The protocol is inspired by libp2p's DCUtR (Direct Connection Upgrade through Relay) but optimized for QUIC transport.

## Diagrams

Visual representations of the protocol are available in PlantUML format:

- **[hp-1-successful-flow.puml](diagrams/hp-1-successful-flow.puml)** - Standard successful hole punch
- **[hp-2-lan-detection.puml](diagrams/hp-2-lan-detection.puml)** - LAN peer optimization
- **[hp-3-direct-dial.puml](diagrams/hp-3-direct-dial.puml)** - Direct dial to public peer
- **[hp-4-failure-fallback.puml](diagrams/hp-4-failure-fallback.puml)** - Fallback to relay
- **[hp-5-architecture.puml](diagrams/hp-5-architecture.puml)** - System architecture
- **[hp-6-decision-tree.puml](diagrams/hp-6-decision-tree.puml)** - Connection decision logic
- **[hp-7-rtt-timing.puml](diagrams/hp-7-rtt-timing.puml)** - RTT measurement details

## Connection Decision Tree

```
┌─────────────────────────────────────┐
│ Want to connect to Peer B           │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 1. Already have direct connection?  │
└──────────────┬──────────────────────┘
          Yes  │  No
       ┌───────┘
       │       │
       ▼       ▼
     DONE  ┌─────────────────────────────────────┐
           │ 2. LAN Peer Detection                │
           │    Same public IP + same subnet?     │
           └──────────────┬──────────────────────┘
                     Yes  │  No
                  ┌───────┘
                  │       │
                  ▼       ▼
         ┌─────────────────┐  ┌─────────────────────────────┐
         │ Direct LAN dial  │  │ 3. Peer has public address? │
         │ (private IP)     │  │    Try direct dial first    │
         └────────┬─────────┘  └──────────────┬──────────────┘
             Yes  │  No            Yes    │  No
            ┌─────┘               ┌───────┘
            │     │               │       │
            ▼     ▼               ▼       ▼
          DONE  ┌─────────┐  ┌─────────────┐  ┌──────────────┐
                │ Continue│  │ Direct dial │  │ 4. Hole punch│
                │ to step3│  └──────┬──────┘  │    via relay │
                └────┬────┘      Yes│No        └──────┬───────┘
                     │          ┌────┘            Yes │ No
                     │          │    │            ┌───┘
                     │          ▼    ▼            │   │
                     │        DONE ┌──────────┐   ▼   ▼
                     └─────────────►Continue  │ DONE ┌─────────────┐
                                   │to step 4 │      │ 5. Fallback │
                                   └──────────┘      │ to relay    │
                                                     │ forwarding  │
                                                     └─────────────┘
```

## Protocol Messages

### 1. HolePunchConnect

Exchanged by both peers to share addressing information and measure RTT.

```json
{
  "type": "hole_punch_connect",
  "version": 1,
  "timestamp": "2025-10-08T12:00:00Z",
  "data": {
    "node_id": "abc123...",
    "observed_addrs": [
      "203.0.113.1:30906",
      "203.0.113.1:30907"
    ],
    "private_addrs": [
      "192.168.1.10:30906"
    ],
    "public_ip": "203.0.113.1",
    "private_ip": "192.168.1.10",
    "nat_type": "symmetric",
    "is_relay": false,
    "send_time": 1728388800000
  }
}
```

**Fields:**
- `node_id`: Sender's DHT node ID
- `observed_addrs`: Public addresses where sender can be reached
- `private_addrs`: Private addresses for LAN detection
- `public_ip`: Public IP address
- `private_ip`: Private/local IP address
- `nat_type`: NAT classification (cone, symmetric, etc.)
- `is_relay`: Whether this peer is a relay
- `send_time`: Unix timestamp in milliseconds for RTT calculation

### 2. HolePunchSync

Sent by initiator to signal that both peers should start hole punching.

```json
{
  "type": "hole_punch_sync",
  "version": 1,
  "timestamp": "2025-10-08T12:00:01Z",
  "data": {
    "rtt": 45
  }
}
```

**Fields:**
- `rtt`: Measured round-trip time in milliseconds

## Protocol Flow

### Initiator (Peer A) Flow

```
1. Check if already connected directly → DONE
2. Check if LAN peer (same public IP + subnet) → Direct LAN dial
3. Check if peer has public address → Try direct dial
4. Open hole punch stream via relay to Peer B
5. Send CONNECT, start RTT timer
6. Receive CONNECT from B, calculate RTT
7. Send SYNC with measured RTT
8. Wait RTT/2 milliseconds
9. Simultaneously dial all of B's addresses
10. Success → DONE | Failure → Use relay forwarding
```

### Receiver (Peer B) Flow

```
1. Receive hole punch stream (must be from inbound relay connection)
2. Receive CONNECT from A, extract addresses
3. Send CONNECT back with own addresses
4. Receive SYNC from A with RTT
5. Wait RTT/2 milliseconds
6. Simultaneously dial all of A's addresses
7. Success → DONE | Failure → Already have relay connection
```

## LAN Peer Detection

Before attempting hole punching, peers check if they're on the same local network:

```go
IsLANPeer(peerB) = (
    peerA.PublicIP == peerB.PublicIP &&
    getSubnet(peerA.PrivateIP, /24) == getSubnet(peerB.PrivateIP, /24)
)
```

**Example:**
- Peer A: Public=203.0.113.1, Private=192.168.1.10
- Peer B: Public=203.0.113.1, Private=192.168.1.20
- Result: LAN peers → direct connection via 192.168.1.20:30906

## RTT-Based Synchronization

The protocol uses measured RTT to synchronize simultaneous connection attempts:

1. **RTT Measurement:**
   - Initiator sends CONNECT at time T1
   - Receiver responds with CONNECT at time T2
   - Initiator receives CONNECT at time T3
   - RTT = T3 - T1

2. **Synchronization:**
   - Both peers wait RTT/2 before dialing
   - This accounts for network propagation time
   - Ensures both peers send packets nearly simultaneously

**Why Simultaneous?**
- NAT creates a mapping when an outbound packet is sent
- If both peers send simultaneously, both NATs create mappings
- Incoming packets then traverse the newly-created mappings

## NAT Type Compatibility

| Peer A NAT  | Peer B NAT  | Success Rate | Strategy              |
|-------------|-------------|--------------|------------------------|
| Full Cone   | Full Cone   | ~100%        | Simple simultaneous    |
| Full Cone   | Restricted  | ~100%        | Simple simultaneous    |
| Full Cone   | Symmetric   | ~80%         | Simultaneous w/ timing |
| Restricted  | Restricted  | ~95%         | Simultaneous w/ timing |
| Restricted  | Symmetric   | ~70%         | Simultaneous w/ timing |
| Symmetric   | Symmetric   | ~40-60%      | Timing critical        |

**Note:** Port prediction for symmetric NATs may be added in future iterations.

## Timing Constraints

```toml
[hole_punch]
hole_punch_enabled = true
hole_punch_timeout = 10s           # Max time for entire process
hole_punch_max_attempts = 3        # Retry attempts
hole_punch_stream_timeout = 60s    # Stream deadline
hole_punch_direct_dial_timeout = 5s # Direct dial attempt timeout
```

## Security Considerations

1. **Stream Direction Validation:**
   - Hole punch streams must come from inbound relay connections only
   - Prevents unsolicited hole punch attempts

2. **Deduplication:**
   - Track active hole punch attempts per peer
   - Prevent multiple simultaneous attempts

3. **Address Validation:**
   - Validate observed addresses are public (not private ranges)
   - Validate addresses match expected formats

4. **Rate Limiting:**
   - Limit hole punch attempts per peer per time window
   - Prevent resource exhaustion attacks

## Metrics

The protocol tracks:
- Success/failure rates by NAT type combination
- Average hole punch duration
- RTT measurements
- Fallback to relay frequency
- LAN peer detection rate

## References

- [libp2p DCUtR Specification](https://github.com/libp2p/specs/blob/master/relay/DCUtR.md)
- [RFC 5389 - STUN](https://datatracker.ietf.org/doc/html/rfc5389)
- [RFC 5766 - TURN](https://datatracker.ietf.org/doc/html/rfc5766)
- [RFC 8445 - ICE](https://datatracker.ietf.org/doc/html/rfc8445)
