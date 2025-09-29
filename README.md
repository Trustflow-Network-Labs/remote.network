
# Remote Network Node

> **Decentralized P2P networking with NAT-friendly metadata exchange.**

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

---

**Remote Network Node** is a P2P networking implementation that combines BitTorrent DHT peer discovery with QUIC-based metadata exchange for NAT-friendly peer-to-peer communication.

---

## Features

- 🔍 **BitTorrent DHT Peer Discovery** - Uses proven DHT protocol for initial peer discovery
- 🔐 **QUIC Metadata Exchange** - Secure, bidirectional metadata exchange over QUIC streams
- 🗄️ **SQLite Peer Database** - Persistent storage of peer metadata with connection pooling
- 🌐 **NAT-Friendly Architecture** - Designed for peers behind NATs with private IP discovery
- 📊 **Memory Leak Monitoring** - Built-in pprof endpoints for performance monitoring
- 🎯 **Node Type Detection** - Automatic detection of public vs private node configuration

---

## Architecture

The node operates in three layers:

1. **DHT Layer** - BitTorrent DHT for peer discovery using `add_peer`/`get_peers` operations
2. **QUIC Layer** - Encrypted metadata exchange with bidirectional stream support
3. **Database Layer** - SQLite storage for peer metadata, network topology, and capabilities

### Peer Discovery Flow

```
DHT Peer Discovery → QUIC Connection → Bidirectional Metadata Exchange → SQLite Storage
```

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

### DHT Implementation

- Uses standard BitTorrent DHT with `add_peer`/`get_peers` operations
- Custom topic-based peer discovery for network segmentation
- Automatic bootstrap node connectivity for network joining

### QUIC Metadata Exchange

- TLS-encrypted streams for secure peer communication
- Bidirectional metadata exchange on single streams (NAT-friendly)
- Structured message protocol with request/response correlation
- Support for future service discovery and hole punching

### Database Schema

Peer metadata includes:
- Node identification (ID, topic, endpoints)
- Network information (public/private IPs, NAT detection)
- Capabilities and service offerings
- Connection quality metrics

---

## Development Roadmap

### Completed
- ✅ DHT peer discovery integration
- ✅ QUIC bidirectional metadata exchange
- ✅ SQLite peer database with connection pooling
- ✅ Node type detection (public/private)
- ✅ Memory leak monitoring infrastructure

### In Progress
- 🔄 NAT detection and classification

### Planned
- 📋 UDP hole punching for NAT-to-NAT communication
- 📋 Service discovery protocol
- 📋 Connection quality metrics and peer scoring
- 📋 Distributed service orchestration
