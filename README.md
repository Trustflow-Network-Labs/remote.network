# Remote Network Node

> **Decentralized P2P compute network for trust-based data exchange and secure computation.**

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

---

**Remote Network Node** is an open-source, decentralized framework for orchestrating distributed workflows across a peer-to-peer network. It enables secure data exchange and computation using Docker containers, with cryptographic identity verification and NAT-friendly connectivity.

This project is the successor to [trustflow-node](https://github.com/Trustflow-Network-Labs/trustflow-node), rebuilt with a custom P2P stack using Mainline DHT and manual implementation of gossip protocol, hole punching, and relaying.

---

## Key Features

### P2P Networking
- **Mainline DHT** - BitTorrent DHT with BEP_44 mutable data for decentralized peer discovery and metadata storage
- **Ed25519 Cryptography** - Secure peer identity with public key signatures
- **NAT-Friendly Architecture** - Relay-based connections, hole punching, and QUIC transport
- **Custom Gossip Protocol** - Efficient peer-to-peer message propagation

### Workflow Orchestration
- **Visual Workflow Designer** - Web UI for creating and managing distributed workflows
- **Service Registry** - Publish and discover services across the network
- **Job Execution Engine** - Coordinate multi-step workflows across peers
- **Interface System** - Flexible I/O interfaces (STDIN, STDOUT, STDERR, LOGS, MOUNT)

### Container Runtime
- **Docker Integration** - Pull images from registries, build from Git repos or local Dockerfiles
- **Automatic Interface Detection** - Detect container interfaces from image metadata
- **Mount Support** - Bidirectional file exchange via container mount points
- **Execution Monitoring** - Real-time output streaming via WebSocket

### Security
- **End-to-End Encryption** - QUIC-based encrypted connections
- **Signed Metadata** - BEP_44 ensures tamper-proof peer metadata
- **Job Verification** - Cryptographic verification of job execution results

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Web UI (Vue.js)                         │
├─────────────────────────────────────────────────────────────────┤
│                         REST API + WebSocket                     │
├───────────────┬───────────────┬───────────────┬─────────────────┤
│   Workflow    │     Job       │    Service    │      Peer       │
│   Manager     │   Manager     │   Registry    │    Discovery    │
├───────────────┴───────────────┴───────────────┴─────────────────┤
│                       Data Worker (Transfers)                    │
├─────────────────────────────────────────────────────────────────┤
│   Docker Service   │   QUIC Transport   │   Relay Manager       │
├────────────────────┴───────────────────┴────────────────────────┤
│                     Mainline DHT (BEP_44)                        │
└─────────────────────────────────────────────────────────────────┘
```

### Workflow Execution Flow

```
1. Create Services     → Register Docker containers as network services
2. Design Workflow     → Connect services via visual workflow designer
3. Start Execution     → Orchestrator distributes jobs to executor peers
4. Data Transfer       → Outputs flow between jobs via secure channels
5. Collect Results     → Final outputs delivered to requesting peer
```

### Interface Types

| Interface | Direction | Description |
|-----------|-----------|-------------|
| STDIN     | Input     | Stream data to container's standard input |
| STDOUT    | Output    | Capture container's standard output |
| STDERR    | Output    | Capture container's error output |
| LOGS      | Output    | Combined container logs with timestamps |
| MOUNT     | Both      | File-based I/O via container mount points |

---

## Getting Started

### Prerequisites

- Go 1.24+
- Docker (automatically installed if missing)

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

# Access Web UI
open http://localhost:30069
```

### Key Management

```bash
# View key information
./remote-network key info

# Export private key for web UI authentication
./remote-network key export --format binary

# Export as hex format
./remote-network key export --format hex --output /path/to/key.txt
```

---

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/` | Application entrypoint and CLI |
| `internal/core/` | Workflow and job management |
| `internal/p2p/` | DHT, QUIC, relay implementations |
| `internal/services/` | Docker service integration |
| `internal/workers/` | Background workers (data transfer) |
| `internal/database/` | SQLite persistence layer |
| `internal/api/` | REST API and WebSocket handlers |
| `web/` | Vue.js frontend application |
| `docs/` | Architecture and protocol documentation |

---

## Documentation

- [Architecture Overview](docs/ARCHITECTURE.md)
- [Docker Service Quickstart](docs/DOCKER_SERVICE_QUICKSTART.md)
- [Docker Dependencies](docs/DOCKER_DEPENDENCIES.md)
- [Hole Punching Protocol](docs/hole-punching-protocol.md)
- [Keystore Setup](docs/KEYSTORE_SETUP.md)
- [Network State Monitoring](docs/NETWORK_STATE_MONITORING.md)

---

## Use Cases

### Distributed Data Processing
Process data across multiple peers, each contributing specialized services:
```
Data Source → Preprocessing → ML Inference → Results Aggregation
    (Peer A)     (Peer B)        (Peer C)         (Peer D)
```

### Decentralized File Conversion
Convert files using containerized tools without centralized servers:
```
PDF Document → OCR Service → Text Extraction → Summary Generation
```

### Privacy-Preserving Computation
Execute sensitive computations on remote peers without exposing raw data:
```
Encrypted Input → Secure Compute → Encrypted Output
```

---

## Development Roadmap

### Completed
- **P2P Layer**: Mainline DHT, BEP_44 metadata, QUIC transport
- **NAT Traversal**: Relay servers, hole punching
- **Service Registry**: Docker service creation and discovery
- **Workflow Engine**: Visual designer, job orchestration
- **Interface System**: STDIN, STDOUT, STDERR, LOGS, MOUNT
- **Data Transfer**: Local and remote job output routing
- **Web UI**: Service management, workflow designer, job monitoring

### In Progress
- Remote peer job execution and verification
- Encrypted data transfer between peers
- Service marketplace and discovery

### Planned
- WASM runtime support
- Reputation and trust scoring
- Payment integration for compute services
- Mobile node support

---

## Linux Server Configuration

For optimal QUIC performance on Linux servers:

```bash
# Create sysctl configuration
sudo nano /etc/sysctl.d/90-quic-buffers.conf

# Add content:
net.core.rmem_max=7500000
net.core.rmem_default=2500000
net.core.wmem_max=7500000
net.core.wmem_default=2500000

# Apply settings
sudo sysctl -p /etc/sysctl.d/90-quic-buffers.conf
```

---

## Contributing

Contributions are welcome!

1. Fork this repo
2. Create your feature branch: `git checkout -b my-new-feature`
3. Commit your changes: `git commit -am 'Add some feature'`
4. Push to the branch: `git push origin my-new-feature`
5. Submit a pull request

---

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

---

## Workflow Examples

- Decentralized data integration for pollinator species management in agroecosystems

![Remote Network Workflow](./remote_network_workflow_example_1.png)

- Satellite-based deforestation estimation

![Remote Network Workflow](./remote_network_workflow_example_2.png)

---

## Links

- [Issue Tracker](https://github.com/Trustflow-Network-Labs/remote-network-node/issues)
- [Trustflow Network Labs](https://github.com/Trustflow-Network-Labs)
- [Previous Project (trustflow-node)](https://github.com/Trustflow-Network-Labs/trustflow-node)
