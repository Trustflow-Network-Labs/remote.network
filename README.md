
# Remote Network Node

> **Decentralized P2P networking with NAT-friendly metadata exchange.**

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

---

**Remote Network Node** is a P2P networking implementation that uses BitTorrent DHT with BEP_44 mutable data for decentralized metadata storage and QUIC-based connections for NAT-friendly peer-to-peer communication.

---

## Features

- 🔍 **DHT-Based Metadata Architecture** - BEP_44 mutable data for tamper-proof, signed peer metadata
- 🔐 **Ed25519 Cryptography** - Secure peer identity with public key signatures
- ⚡ **On-Demand Queries** - Cache-first metadata retrieval with 5-minute TTL
- 🗄️ **Minimal Peer Storage** - Lightweight SQLite database (peer_id, public_key, topic only)
- 🌐 **NAT-Friendly Architecture** - Relay-based connections for NAT peers, hole punching support
- 📊 **Peer Discovery Service** - Smart filtering of connectable peers (public/relay/NAT)
- 🔄 **Automatic Republishing** - Hourly DHT republishing keeps metadata fresh

---

## Architecture

The node uses a modern DHT-based metadata architecture:

1. **Crypto Layer** - Ed25519 keys for peer identity and metadata signing
2. **DHT Layer** - BEP_44 mutable data storage for metadata (DHT is source of truth)
3. **Cache Layer** - 5-minute metadata cache to reduce DHT query overhead
4. **QUIC Layer** - Encrypted connections with identity exchange during handshake
5. **Discovery Layer** - On-demand peer discovery with connectability filtering

### Metadata Flow

```
Ed25519 Keys → Sign Metadata → Publish to DHT (BEP_44)
                                      ↓
                              Cache (5 min TTL)
                                      ↓
                     Query on-demand → QUIC Connection
```

### Key Changes from Old System

- ❌ **Removed:** Metadata broadcasts (replaced by DHT publishing)
- ❌ **Removed:** Peer exchange protocol (replaced by identity exchange)
- ✅ **Added:** BEP_44 signed mutable data
- ✅ **Added:** Metadata caching with TTL
- ✅ **Added:** Connectability filtering for smart peer selection

For detailed architecture, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

---

## Getting Started

### Prerequisites

- Go 1.24+
- SQLite support (CGO enabled for modernc.org/sqlite)

### Installation

```bash
git clone https://github.com/Trustflow-Network-Labs/remote-network-node.git
cd remote-network-node

# Build
go build -o remote-network ./cmd/main.go
```

### Running

```bash
# Start the node
./remote-network start

# Enable memory monitoring (separate terminal)
go tool pprof http://localhost:6060/debug/pprof/heap
```

### Linux Server Configuration

For optimal QUIC performance on Linux servers, configure UDP buffer sizes:

```bash
# Create sysctl configuration file
sudo nano /etc/sysctl.d/90-quic-buffers.conf
```

Add the following content:

```
# QUIC UDP buffer sizes
net.core.rmem_max=7500000
net.core.rmem_default=2500000
net.core.wmem_max=7500000
net.core.wmem_default=2500000
```

Apply the configuration:

```bash
# Apply the new settings
sudo sysctl -p /etc/sysctl.d/90-quic-buffers.conf

# Or reload all sysctl configs
sudo sysctl --system

# Verify settings
sysctl net.core.rmem_max net.core.wmem_max
```

---

## Project Structure

| Directory               | Purpose                                           |
|--------------------------|---------------------------------------------------|
| `cmd/`                   | Application entrypoint and CLI                   |
| `internal/core/`         | Core peer management and coordination            |
| `internal/p2p/`          | DHT and QUIC protocol implementations            |
| `internal/database/`     | SQLite peer metadata storage                     |
| `internal/utils/`        | Network utilities and node type detection       |
| `monitoring/`            | Memory leak and performance monitoring scripts   |

---

## Contributing

Contributions are welcome and appreciated!

Steps to contribute:

1. Fork this repo
2. Create your feature branch: `git checkout -b my-new-feature`
3. Commit your changes: `git commit -am 'Add some feature'`
4. Push to the branch: `git push origin my-new-feature`
5. Submit a pull request 🚀

---

## License

This project is licensed under the MIT License.  
See the [LICENSE](LICENSE) file for details.

---

## Links

- [Issue Tracker](https://github.com/Trustflow-Network-Labs/trustflow-node/issues)
- [Trustflow Network Labs GitHub](https://github.com/Trustflow-Network-Labs)

---

## Protocol Details

### DHT BEP_44 Implementation

- Uses BEP_44 mutable data for tamper-proof metadata storage
- Ed25519 signatures ensure metadata authenticity
- Storage key: `SHA1(public_key)`
- Sequence numbers prevent replay attacks
- Hourly republishing keeps data fresh in DHT

### Identity Exchange

- Public keys exchanged during QUIC handshake
- Known peers shared (minimal: peer_id + public_key)
- Peer ID derived from public key: `peer_id = SHA1(public_key)`
- Enables on-demand metadata queries from DHT

### Database Schema

**Minimal Storage:**
- `known_peers`: peer_id, public_key, topic, last_seen (no metadata!)
- `metadata_cache`: cached metadata with 5-minute TTL

**Full metadata queried on-demand from DHT:**
- Node identification, network info, capabilities
- Signed with Ed25519, versioned with sequence numbers
- Always fresh (DHT is source of truth)

---

## Development Roadmap

### Completed (January 2025)
- ✅ **Phase 1-6:** DHT-based metadata architecture
- ✅ BEP_44 mutable data with Ed25519 signatures
- ✅ Metadata caching with TTL (5 minutes)
- ✅ On-demand peer discovery with filtering
- ✅ Identity exchange during QUIC handshake
- ✅ Comprehensive test suite (59 tests, 3000+ lines)
- ✅ Legacy system removal (broadcasts, peer exchange)
- ✅ NAT detection and relay-based connections
- ✅ Hole punching for NAT-to-NAT communication

### Planned
- 📋 Phase 7 deployment monitoring and optimization
- 📋 Service discovery protocol enhancement
- 📋 Connection quality metrics and peer scoring
- 📋 DHT query optimization based on network patterns
