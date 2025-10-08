# System Architecture Diagrams

This directory contains UML diagrams documenting the peer management and NAT traversal system.

## Diagrams

### 1. Peer Aging Sequence Diagram
**File:** `1-peer-aging-sequence.puml`

Shows the complete flow of the 1-day peer aging mechanism with reachability verification:
- Periodic check every 5 minutes
- 24-hour staleness criteria
- Direct ping for public peers
- Relay + session verification for NAT peers
- LastSeen timestamp updates
- Peer removal logic

### 2. NAT Peer Session Verification
**File:** `2-nat-session-verification.puml`

Details the NAT peer verification process through relay:
- Relay reachability ping
- Session query message flow
- Database + memory session lookup
- Keepalive validation
- Active session determination

### 3. Connection Failure Cleanup
**File:** `3-connection-failure-cleanup.puml`

Shows event-driven cleanup on connection failures:
- Connection attempt failure
- Callback trigger mechanism
- Address-to-peer lookup
- Reachability verification
- Automatic peer removal

### 4. System Architecture
**File:** `4-system-architecture.puml`

Component diagram showing:
- System layers (Core, P2P, Database, Utils)
- Component relationships
- Callback mechanisms
- Data flow between components

### 5. DHT/QUIC Communication Data Flow
**File:** `5-dht-quic-dataflow.puml`

Comprehensive data flow diagram showing:
- DHT peer discovery
- QUIC metadata exchange
- Bidirectional metadata sync
- Relay peer detection
- Periodic reachability checks
- NAT peer session verification
- Connection failure handling

### 6. Class Diagram
**File:** `6-class-diagram.puml`

Object-oriented view of the system:
- Key classes and their relationships
- Data structures (PeerMetadata, NetworkInfo, RelaySession)
- Message types and data transfer objects
- Database interfaces
- Component dependencies

### 7. Peer Lifecycle State Machine
**File:** `7-peer-lifecycle-state.puml`

State diagram showing peer lifecycle:
- Discovery through DHT
- Fresh vs Stale states (24h threshold)
- Verification states
- Public peer vs NAT peer verification paths
- Connection failure handling
- Relay peer special handling
- State transitions and triggers

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
plantuml docs/diagrams/1-peer-aging-sequence.puml

# Render all diagrams
plantuml docs/diagrams/*.puml

# Output will be .png files in the same directory
```

### Generate SVG (scalable)

```bash
plantuml -tsvg docs/diagrams/*.puml
```

## Key Concepts

### Peer Aging
- **Check Interval**: 5 minutes (`peer_reachability_check_interval`)
- **Max Age**: 24 hours (`peer_metadata_max_age`)
- **Action**: Peers older than 24h are verified via ping
- **Result**: If unreachable → removed, if reachable → LastSeen updated

### NAT Peer Verification
1. Ping the relay peer
2. If relay reachable → query for NAT peer's session
3. Relay checks database AND memory for active session
4. Session is "active" only if keepalive is recent
5. NAT peer removed if relay unreachable OR no active session

### Connection Failure Cleanup
1. Any connection attempt failure triggers callback
2. Failed address is looked up in peer database
3. Same reachability verification as periodic aging
4. Peer removed if unreachable

### Message Types Added
- `MessageTypeRelaySessionQuery` - Query relay for session status
- `MessageTypeRelaySessionStatus` - Response with session details

## Database Schema

### Peer Metadata
- `node_id` - Unique peer identifier
- `topic` - DHT topic
- `last_seen` - Timestamp of last contact (updated on reachability check)
- `network_info` - JSON with endpoints, NAT type, relay info

### Relay Sessions
- `session_id` - Unique session identifier
- `client_node_id` - NAT peer using relay
- `relay_node_id` - Relay peer providing service
- `status` - active/inactive/terminated
- `last_keepalive` - Last keepalive timestamp

## Configuration

```toml
# Peer metadata aging
peer_metadata_max_age = 24h
peer_reachability_check_interval = 5m

# Relay configuration
relay_mode = false
relay_keepalive_interval = 5s
```
