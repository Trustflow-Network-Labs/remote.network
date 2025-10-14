# System Diagrams

This directory contains PlantUML diagrams documenting the hole punching NAT traversal system.

## Current Diagrams

### Hole Punching Protocol

All diagrams relate to the NAT traversal hole punching protocol:

**hp-1-successful-flow.puml** - Standard successful hole punch sequence
- Shows the complete flow of a successful hole punch between two NAT peers
- Includes relay coordination and simultaneous connection attempts

**hp-2-lan-detection.puml** - LAN peer detection and direct connection
- Demonstrates how peers detect they're on the same local network
- Shows direct connection optimization for LAN peers

**hp-3-direct-dial.puml** - Direct dial to public peer
- Shows connection flow when target peer is publicly accessible
- Demonstrates that hole punching is not needed for public peers

**hp-4-failure-fallback.puml** - Hole punch failure and relay fallback
- Shows what happens when hole punching fails
- Demonstrates automatic fallback to relay forwarding

**hp-5-architecture.puml** - System architecture for hole punching
- Component diagram showing hole punching system components
- Shows integration with QUIC, relay manager, and DHT

**hp-6-decision-tree.puml** - Connection decision logic
- Flow chart for determining connection strategy
- Shows how system chooses between LAN, direct, hole punch, or relay

**hp-7-rtt-timing.puml** - RTT measurement and synchronization
- Details RTT measurement during hole punch coordination
- Shows timing synchronization for simultaneous connection attempts

## Viewing the Diagrams

### Online Viewers

1. **PlantUML Online Server**
   - Go to http://www.plantuml.com/plantuml/uml/
   - Copy and paste the content of any `.puml` file
   - Click "Submit" to render

2. **VS Code Extension**
   - Install "PlantUML" extension
   - Open any `.puml` file
   - Press `Alt+D` to preview

3. **IntelliJ IDEA**
   - Install "PlantUML integration" plugin
   - Right-click on `.puml` file → "Show PlantUML Diagram"

### Local Rendering

```bash
# Install PlantUML (requires Java)
# macOS
brew install plantuml

# Ubuntu/Debian
apt-get install plantuml

# Render to PNG
plantuml docs/diagrams/hp-1-successful-flow.puml

# Render all diagrams
plantuml docs/diagrams/*.puml

# Output will be .png files in the same directory
```

### Generate SVG (scalable)

```bash
plantuml -tsvg docs/diagrams/*.puml
```

## Key Concepts

### Hole Punching Overview

The hole punching protocol enables direct NAT-to-NAT connections by:

1. **Relay Coordination** - Relay peer coordinates the connection attempt
2. **RTT Measurement** - Both peers measure round-trip time
3. **Synchronized Dial** - Both peers dial simultaneously after RTT/2 delay
4. **NAT Mapping Creation** - Simultaneous packets create NAT mappings
5. **Direct Connection** - Packets traverse the newly-created NAT mappings

### Connection Strategies (in priority order)

1. **Existing Connection** - Reuse if already connected
2. **LAN Direct** - Direct connection if same local network
3. **Public Direct** - Direct dial if target is public
4. **Hole Punch** - NAT-to-NAT via coordinated simultaneous dial
5. **Relay Forward** - Fallback if hole punch fails

### Message Types

- `MessageTypeHolePunchConnect` - Exchange addressing info and measure RTT
- `MessageTypeHolePunchSync` - Signal to start simultaneous dial

## Configuration

```toml
[hole_punch]
hole_punch_enabled = true
hole_punch_timeout = 10s           # Max time for entire process
hole_punch_max_attempts = 3        # Retry attempts
hole_punch_stream_timeout = 60s    # Stream deadline
hole_punch_direct_dial_timeout = 5s # Direct dial timeout
```

## Related Documentation

See [docs/hole-punching-protocol.md](../hole-punching-protocol.md) for detailed protocol specification.

See [docs/ARCHITECTURE.md](../ARCHITECTURE.md) for overall system architecture including DHT and identity exchange.

---

**Note**: Previous diagrams (peer aging, metadata exchange flows, etc.) have been removed as they referenced the old broadcast-based architecture. The current DHT-only architecture uses:
- BEP_44 mutable data for metadata storage (no peer aging mechanism)
- Identity exchange during QUIC handshake (no separate metadata request/response)
- On-demand DHT queries (no periodic reachability checks)
