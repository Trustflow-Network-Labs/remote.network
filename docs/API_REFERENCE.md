# Remote Network Node API Reference

Complete REST API documentation for the Remote Network Node.

## Table of Contents

1. [Authentication](#authentication)
2. [Node Management](#node-management)
3. [Peers](#peers)
4. [Services (Generic)](#services-generic)
5. [Docker Services](#docker-services)
6. [Standalone Services](#standalone-services)
7. [Workflows](#workflows)
8. [Blacklist](#blacklist)
9. [Relay Management](#relay-management)
10. [WebSocket](#websocket)
11. [Error Handling](#error-handling)

---

## Base URL

- **HTTPS (default):** `https://<node-ip>:30069`
- **HTTP (localhost only):** `http://127.0.0.1:8081`

All endpoints use JSON for request and response bodies unless otherwise specified.

---

## Authentication

The API uses JWT (JSON Web Token) authentication. Most endpoints require a valid JWT token in the `Authorization` header.

### Get Authentication Challenge

Generate a challenge for Ed25519 signature-based authentication.

**Endpoint:** `GET /api/auth/challenge`

**Authentication:** None required

**Response:**
```json
{
  "challenge": "random-challenge-string",
  "expires_in": 300
}
```

**Error Codes:**
- `500` - Failed to generate challenge

---

### Authenticate with Ed25519

Authenticate using Ed25519 signature and receive a JWT token.

**Endpoint:** `POST /api/auth/ed25519`

**Authentication:** None required

**Request Body:**
```json
{
  "challenge": "random-challenge-string",
  "signature": "hex-encoded-signature",
  "public_key": "hex-encoded-public-key"
}
```

**Response:**
```json
{
  "success": true,
  "token": "jwt-token-string",
  "peer_id": "peer-id-string"
}
```

**Error Response:**
```json
{
  "success": false,
  "error": "Invalid or expired challenge"
}
```

**Error Codes:**
- `400` - Invalid request body
- `401` - Invalid or expired challenge / Authentication failed
- `503` - Ed25519 authentication not available

**Notes:**
- Challenge is valid for 5 minutes
- JWT token is valid for 24 hours
- The signature must be created by signing the challenge with the Ed25519 private key

---

## Node Management

Endpoints for managing and monitoring the node.

### Get Node Status

Returns current node status, uptime, and statistics.

**Endpoint:** `GET /api/node/status`

**Authentication:** JWT required

**Response:**
```json
{
  "peer_id": "12D3KooW...",
  "dht_node_id": "abc123...",
  "uptime_seconds": 3600,
  "known_peers": 42,
  "stats": {
    "peer_id": "12D3KooW...",
    "dht_node_id": "abc123...",
    "relay_mode": false,
    "nat_mode": true,
    "connected_relay": "12D3KooW...",
    "active_connections": 5
  }
}
```

**Error Codes:**
- `401` - Unauthorized (invalid/missing JWT)
- `405` - Method not allowed
- `500` - Internal server error

---

### Get Node Capabilities

Returns the node's system capabilities including Docker, GPU, CPU, and memory information.

**Endpoint:** `GET /api/node/capabilities`

**Authentication:** JWT required

**Response:**
```json
{
  "docker_available": true,
  "docker_version": "24.0.7",
  "colima_status": "running",
  "system": {
    "platform": "darwin",
    "architecture": "arm64",
    "kernel_version": "23.1.0",
    "cpu_model": "Apple M1",
    "cpu_cores": 8,
    "cpu_threads": 8,
    "total_memory_mb": 16384,
    "available_memory_mb": 8192,
    "total_disk_mb": 512000,
    "available_disk_mb": 256000,
    "has_docker": true,
    "docker_version": "24.0.7",
    "has_python": true,
    "python_version": "3.11.5",
    "gpus": [
      {
        "index": 0,
        "name": "Apple M1",
        "vendor": "Apple",
        "memory_mb": 8192,
        "uuid": "",
        "driver_version": ""
      }
    ]
  }
}
```

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed

**Notes:**
- `colima_status` is only present on macOS systems
- GPU information may be empty on systems without dedicated GPUs

---

### Restart Node

Initiates a node restart.

**Endpoint:** `POST /api/node/restart`

**Authentication:** JWT required

**Response:**
```json
{
  "success": true,
  "message": "Node restart initiated successfully"
}
```

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to initiate restart

**Notes:**
- The response is sent immediately before the restart begins
- The node will restart with the same configuration flags
- Existing connections will be dropped

---

## Peers

Endpoints for discovering and managing network peers.

### Get All Peers

Returns a list of all known peers in the network.

**Endpoint:** `GET /api/peers`

**Authentication:** JWT required

**Response:**
```json
{
  "peers": [
    {
      "peer_id": "12D3KooW...",
      "dht_node_id": "abc123...",
      "is_relay": true,
      "is_store": true,
      "files_count": 5,
      "apps_count": 3,
      "last_seen": "2024-12-25T10:30:00Z",
      "topic": "remote-network-mesh",
      "source": "dht",
      "is_behind_nat": false,
      "nat_type": "",
      "using_relay": false,
      "connected_relay_id": ""
    }
  ],
  "total": 1
}
```

**Fields:**
- `files_count` - Count of ACTIVE DATA services
- `apps_count` - Count of ACTIVE DOCKER + STANDALONE services
- `is_relay` - Whether this peer acts as a relay
- `is_store` - Whether this peer provides DHT storage
- `is_behind_nat` - Whether this peer is behind NAT
- `nat_type` - NAT type (e.g., "full_cone", "restricted")
- `using_relay` - Whether this peer is currently using a relay
- `connected_relay_id` - PeerID of the connected relay (if using_relay is true)

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to fetch peers

---

### Get Peer Capabilities

Returns detailed system capabilities for a specific peer.

**Endpoint:** `GET /api/peers/{peer_id}/capabilities`

**Authentication:** JWT required

**Path Parameters:**
- `peer_id` - The peer ID to query

**Response:**
```json
{
  "peer_id": "12D3KooW...",
  "capabilities": {
    "platform": "linux",
    "architecture": "amd64",
    "kernel_version": "5.15.0",
    "cpu_model": "Intel Xeon",
    "cpu_cores": 16,
    "cpu_threads": 32,
    "total_memory_mb": 65536,
    "available_memory_mb": 32768,
    "total_disk_mb": 1024000,
    "available_disk_mb": 512000,
    "has_docker": true,
    "docker_version": "24.0.7",
    "has_python": true,
    "python_version": "3.10.8",
    "gpus": [
      {
        "index": 0,
        "name": "NVIDIA RTX 4090",
        "vendor": "NVIDIA",
        "memory_mb": 24576,
        "uuid": "GPU-xxx",
        "driver_version": "535.129.03"
      }
    ]
  }
}
```

**Error Response:**
```json
{
  "peer_id": "12D3KooW...",
  "error": "No system capabilities found in peer metadata"
}
```

**Error Codes:**
- `400` - Invalid path or missing peer_id
- `401` - Unauthorized
- `405` - Method not allowed

**Notes:**
- First returns DHT summary data for fast response
- Full capabilities are fetched in background via QUIC
- WebSocket event `PEER_CAPABILITIES_UPDATED` is sent when full data is available
- Some fields may be partial when using DHT summary (no GPU names/UUIDs, no kernel version)

---

## Services (Generic)

Generic service management endpoints that work across all service types (DATA, DOCKER, STANDALONE).

### Get All Services

Returns all services offered by this node.

**Endpoint:** `GET /api/services`

**Authentication:** JWT required

**Response:**
```json
{
  "services": [
    {
      "id": 1,
      "name": "my-service",
      "description": "A test service",
      "service_type": "DOCKER",
      "type": "docker",
      "status": "ACTIVE",
      "endpoint": "",
      "pricing_amount": 0.0,
      "pricing_type": "FREE",
      "pricing_interval": "",
      "pricing_unit": "",
      "capabilities": {
        "image_name": "my-image",
        "image_tag": "latest"
      },
      "created_at": "2024-12-25T10:00:00Z",
      "updated_at": "2024-12-25T10:00:00Z",
      "interfaces": [
        {
          "id": 1,
          "service_id": 1,
          "interface_type": "STDIN",
          "path": "",
          "mount_function": "",
          "description": "Standard input"
        }
      ],
      "docker_details": {
        "id": 1,
        "service_id": 1,
        "image_name": "my-image",
        "image_tag": "latest",
        "source": "registry",
        "compose_path": "",
        "entrypoint": "",
        "cmd": ""
      }
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve services

**Notes:**
- `docker_details` is only included for DOCKER services
- `interfaces` are always included for all services

---

### Get Service by ID

Returns a specific service by ID.

**Endpoint:** `GET /api/services/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "service": {
    "id": 1,
    "name": "my-service",
    "description": "A test service",
    "service_type": "DOCKER",
    "type": "docker",
    "status": "ACTIVE",
    "capabilities": {},
    "created_at": "2024-12-25T10:00:00Z",
    "updated_at": "2024-12-25T10:00:00Z"
  }
}
```

**Error Codes:**
- `400` - Invalid service ID
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Internal server error

---

### Create Service

Creates a new service (generic endpoint, prefer type-specific endpoints).

**Endpoint:** `POST /api/services`

**Authentication:** JWT required

**Request Body:**
```json
{
  "type": "docker",
  "name": "my-service",
  "description": "A test service",
  "endpoint": "",
  "capabilities": {}
}
```

**Required Fields:**
- `type` - Service type: "storage", "docker", "standalone", "relay"
- `name` - Service name

**Optional Fields:**
- `description` - Service description
- `endpoint` - Service endpoint (required for non-DATA services)
- `status` - Service status (default: "available")
- `capabilities` - Service capabilities object

**Response:**
```json
{
  "service": {
    "id": 1,
    "name": "my-service",
    "type": "docker",
    "service_type": "DOCKER",
    "status": "ACTIVE"
  },
  "message": "Service added successfully"
}
```

**Error Codes:**
- `400` - Invalid request body or missing required fields
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to add service

---

### Update Service

Updates an existing service.

**Endpoint:** `PUT /api/services/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Request Body:**
```json
{
  "name": "updated-service-name",
  "description": "Updated description",
  "status": "ACTIVE"
}
```

**Response:**
```json
{
  "service": {
    "id": 1,
    "name": "updated-service-name",
    "description": "Updated description"
  },
  "message": "Service updated successfully"
}
```

**Error Codes:**
- `400` - Invalid service ID or request body
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to update service

---

### Delete Service

Deletes a service and its associated resources.

**Endpoint:** `DELETE /api/services/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "message": "Service deleted successfully"
}
```

**Error Codes:**
- `400` - Invalid service ID
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to delete service

**Notes:**
- For DOCKER services, also removes the Docker image
- Cascade deletes all interfaces and service-specific details

---

### Update Service Status

Updates the status of a service (ACTIVE/INACTIVE).

**Endpoint:** `PUT /api/services/{id}/status`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Request Body:**
```json
{
  "status": "ACTIVE"
}
```

**Valid Status Values:**
- `ACTIVE` - Service is active and available
- `INACTIVE` - Service is inactive

**Response:**
```json
{
  "message": "Service status updated successfully",
  "status": "ACTIVE"
}
```

**Error Codes:**
- `400` - Invalid service ID, request body, or status value
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to update status

---

### Get Service Passphrase

Retrieves the encryption passphrase for a DATA service.

**Endpoint:** `GET /api/services/{id}/passphrase`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "passphrase": "encryption-passphrase-here",
  "data_details": {
    "id": 1,
    "service_id": 1,
    "file_path": "/path/to/file.dat",
    "file_size": 1024000,
    "file_hash": "sha256-hash",
    "encryption_method": "AES-256-GCM"
  }
}
```

**Error Codes:**
- `400` - Invalid service ID or not a DATA service
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to retrieve passphrase or data details

**Notes:**
- Only available for DATA services
- The passphrase is used to decrypt the encrypted data file

---

### Get Service Interfaces

Returns all interfaces for a specific service.

**Endpoint:** `GET /api/services/{id}/interfaces`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "interfaces": [
    {
      "id": 1,
      "service_id": 1,
      "interface_type": "STDIN",
      "path": "",
      "mount_function": "",
      "description": "Standard input stream"
    },
    {
      "id": 2,
      "service_id": 1,
      "interface_type": "STDOUT",
      "path": "",
      "mount_function": "",
      "description": "Standard output stream"
    }
  ],
  "total": 2
}
```

**Interface Types:**
- `STDIN` - Standard input stream
- `STDOUT` - Standard output stream
- `STDERR` - Standard error stream
- `LOGS` - Combined log output
- `MOUNT` - File/directory mount point
- `ENV` - Environment variable
- `ARG` - Command-line argument

**Error Codes:**
- `400` - Invalid service ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve interfaces

---

## Docker Services

Specialized endpoints for creating and managing Docker-based services.

### Create Docker Service from Registry

Creates a Docker service by pulling an image from a registry (Docker Hub, GHCR, etc.).

**Endpoint:** `POST /api/services/docker/from-registry`

**Authentication:** JWT required

**Request Body:**
```json
{
  "service_name": "my-docker-service",
  "image_name": "nginx",
  "image_tag": "alpine",
  "description": "Nginx web server",
  "username": "docker-username",
  "password": "docker-password",
  "custom_interfaces": [
    {
      "interface_type": "ENV",
      "path": "PORT",
      "description": "HTTP port"
    }
  ]
}
```

**Required Fields:**
- `service_name` - Name for the service
- `image_name` - Docker image name (e.g., "nginx", "ghcr.io/owner/repo")

**Optional Fields:**
- `image_tag` - Image tag (default: "latest")
- `description` - Service description
- `username` - Registry username (for private registries)
- `password` - Registry password (for private registries)
- `custom_interfaces` - Array of custom interface definitions

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "my-docker-service",
    "service_type": "DOCKER",
    "status": "ACTIVE",
    "capabilities": {
      "image_name": "nginx",
      "image_tag": "alpine"
    }
  },
  "suggested_interfaces": [
    {
      "interface_type": "ENV",
      "path": "PORT",
      "description": "Detected environment variable"
    },
    {
      "interface_type": "MOUNT",
      "path": "/usr/share/nginx/html",
      "mount_function": "INPUT",
      "description": "Detected volume mount"
    }
  ]
}
```

**Error Codes:**
- `400` - Missing required fields or invalid request
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to create service (image pull failed, etc.)

**Notes:**
- The image is pulled immediately during service creation
- Suggested interfaces are auto-detected from image metadata
- Authentication credentials are only used during image pull (not stored)

---

### Create Docker Service from Git Repository

Creates a Docker service by cloning a Git repository and building from Dockerfile or docker-compose.yml.

**Endpoint:** `POST /api/services/docker/from-git`

**Authentication:** JWT required

**Request Body:**
```json
{
  "service_name": "my-git-service",
  "repo_url": "https://github.com/user/repo.git",
  "branch": "main",
  "username": "git-username",
  "password": "git-token",
  "description": "Service built from Git",
  "custom_interfaces": []
}
```

**Required Fields:**
- `service_name` - Name for the service
- `repo_url` - Git repository URL

**Optional Fields:**
- `branch` - Git branch name (default: "main" or "master")
- `username` - Git username (for private repos)
- `password` - Git password or personal access token (for private repos)
- `description` - Service description
- `custom_interfaces` - Array of custom interface definitions

**Response:**
```json
{
  "operation_id": "docker-git-1735128000000000000",
  "message": "Docker service creation started"
}
```

**Error Codes:**
- `400` - Missing required fields or invalid request
- `401` - Unauthorized
- `405` - Method not allowed

**Notes:**
- This is an **asynchronous operation**
- Returns immediately with an `operation_id`
- Progress updates are sent via WebSocket:
  - `DOCKER_OPERATION_START` - Build started
  - `DOCKER_OPERATION_PROGRESS` - Build progress updates
  - `DOCKER_OPERATION_COMPLETE` - Build completed successfully
  - `DOCKER_OPERATION_ERROR` - Build failed
- The repository is cloned and built using either Dockerfile or docker-compose.yml
- If both exist, docker-compose.yml takes precedence

---

### Create Docker Service from Local Directory

Creates a Docker service from a local directory containing a Dockerfile or docker-compose.yml.

**Endpoint:** `POST /api/services/docker/from-local`

**Authentication:** JWT required

**Request Body:**
```json
{
  "service_name": "my-local-service",
  "local_path": "/path/to/directory",
  "description": "Service built from local directory",
  "custom_interfaces": []
}
```

**Required Fields:**
- `service_name` - Name for the service
- `local_path` - Absolute path to directory containing Dockerfile or docker-compose.yml

**Optional Fields:**
- `description` - Service description
- `custom_interfaces` - Array of custom interface definitions

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "my-local-service",
    "service_type": "DOCKER",
    "status": "ACTIVE"
  },
  "suggested_interfaces": [
    {
      "interface_type": "STDIN",
      "description": "Standard input"
    }
  ]
}
```

**Error Codes:**
- `400` - Missing required fields or invalid path
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to create service (build failed, no Dockerfile found, etc.)

**Notes:**
- The directory must contain either Dockerfile or docker-compose.yml
- If both exist, docker-compose.yml takes precedence
- The image is built immediately during service creation

---

### Validate Git Repository

Validates that a Git repository is accessible before creating a service.

**Endpoint:** `POST /api/services/docker/validate-git`

**Authentication:** JWT required

**Request Body:**
```json
{
  "repo_url": "https://github.com/user/repo.git",
  "username": "git-username",
  "password": "git-token"
}
```

**Required Fields:**
- `repo_url` - Git repository URL

**Optional Fields:**
- `username` - Git username (for private repos)
- `password` - Git password or token (for private repos)

**Response (Success):**
```json
{
  "valid": true
}
```

**Response (Failure):**
```json
{
  "valid": false,
  "error": "authentication failed: invalid credentials"
}
```

**Error Codes:**
- `400` - Missing repo_url
- `401` - Unauthorized
- `405` - Method not allowed

**Notes:**
- Always returns 200 OK with a `valid` boolean
- Use this endpoint to validate credentials before creating a service

---

### Get Docker Service Details

Returns detailed Docker-specific information for a service.

**Endpoint:** `GET /api/services/docker/{id}/details`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "details": {
    "id": 1,
    "service_id": 1,
    "image_name": "my-image",
    "image_tag": "latest",
    "source": "registry",
    "compose_path": "",
    "entrypoint": "[\"nginx\"]",
    "cmd": "[\"-g\", \"daemon off;\"]"
  }
}
```

**Fields:**
- `source` - How the service was created: "registry", "git", or "local"
- `compose_path` - Path to docker-compose.yml (if applicable)
- `entrypoint` - JSON array of entrypoint command
- `cmd` - JSON array of command arguments

**Error Codes:**
- `400` - Invalid service ID
- `401` - Unauthorized
- `404` - Docker service not found
- `405` - Method not allowed
- `500` - Failed to retrieve details

---

### Get Suggested Interfaces for Docker Service

Re-detects and returns suggested interfaces based on Docker image metadata.

**Endpoint:** `GET /api/services/docker/{id}/interfaces/suggested`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "suggested_interfaces": [
    {
      "interface_type": "ENV",
      "path": "DATABASE_URL",
      "description": "Database connection string"
    },
    {
      "interface_type": "MOUNT",
      "path": "/app/data",
      "mount_function": "INPUT",
      "description": "Data directory"
    },
    {
      "interface_type": "ARG",
      "path": "--config",
      "description": "Configuration file path"
    }
  ]
}
```

**Error Codes:**
- `400` - Invalid service ID or unknown source
- `401` - Unauthorized
- `404` - Docker service not found
- `405` - Method not allowed
- `500` - Failed to detect interfaces

**Notes:**
- Interfaces are detected from:
  - ENV declarations in Dockerfile
  - VOLUME declarations in Dockerfile
  - EXPOSE declarations (converted to ENV for ports)
  - docker-compose.yml environment and volumes
- This endpoint re-scans the image/compose file for the latest interface suggestions

---

### Update Docker Service Interfaces

Replaces all interfaces for a Docker service with a new set.

**Endpoint:** `PUT /api/services/docker/{id}/interfaces`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Request Body:**
```json
{
  "interfaces": [
    {
      "interface_type": "STDIN",
      "path": "",
      "description": "Standard input"
    },
    {
      "interface_type": "MOUNT",
      "path": "/data",
      "mount_function": "INPUT",
      "description": "Input data directory"
    }
  ]
}
```

**Response:**
```json
{
  "success": true,
  "message": "Interfaces updated successfully"
}
```

**Error Codes:**
- `400` - Invalid service ID or request body
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to update interfaces

**Notes:**
- This is a **replace** operation, not an append
- All existing interfaces are deleted first, then new ones are added
- A WebSocket `SERVICE_UPDATED` event is broadcast after update

---

### Update Docker Service Configuration

Updates the entrypoint and/or command for a Docker service.

**Endpoint:** `PUT /api/services/docker/{id}/config`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Request Body:**
```json
{
  "entrypoint": "python app.py",
  "cmd": "--verbose --port 8080"
}
```

**Alternative (JSON array format):**
```json
{
  "entrypoint": "[\"python\", \"app.py\"]",
  "cmd": "[\"--verbose\", \"--port\", \"8080\"]"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Configuration updated successfully"
}
```

**Error Codes:**
- `400` - Invalid service ID, request body, or command format
- `401` - Unauthorized
- `404` - Docker service not found
- `405` - Method not allowed
- `500` - Failed to update configuration

**Notes:**
- Supports both shell-style strings and JSON arrays
- Shell-style strings are parsed using `shlex` and converted to JSON arrays
- Empty strings clear the entrypoint/cmd
- Examples:
  - Shell: `"python app.py --verbose"`
  - JSON: `"[\"python\", \"app.py\", \"--verbose\"]"`
  - Both produce the same result

---

## Standalone Services

Endpoints for creating and managing standalone (non-Docker) executable services.

### Create Standalone Service from Local Executable

Creates a standalone service from an executable already on the filesystem.

**Endpoint:** `POST /api/services/standalone/from-local`

**Authentication:** JWT required

**Request Body:**
```json
{
  "service_name": "python-processor",
  "executable_path": "/usr/bin/python3",
  "arguments": ["-u", "script.py", "--verbose"],
  "working_directory": "/opt/myapp",
  "environment_variables": {
    "PYTHONUNBUFFERED": "1",
    "API_KEY": "secret"
  },
  "timeout_seconds": 600,
  "run_as_user": "appuser:appgroup",
  "description": "Python data processor",
  "capabilities": {
    "platform": "linux",
    "architectures": ["amd64"],
    "min_memory_mb": 512,
    "min_cpu_cores": 1
  },
  "custom_interfaces": [
    {
      "interface_type": "MOUNT",
      "path": "/data/input",
      "mount_function": "INPUT",
      "description": "Input data"
    }
  ]
}
```

**Required Fields:**
- `service_name` - Name for the service
- `executable_path` - Absolute path to executable

**Optional Fields:**
- `arguments` - Array of command-line arguments
- `working_directory` - Working directory for execution
- `environment_variables` - Object of environment variables
- `timeout_seconds` - Execution timeout (default: 300)
- `run_as_user` - User to run as (format: "user:group")
- `description` - Service description
- `capabilities` - Service capability requirements
- `custom_interfaces` - Array of interface definitions

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "python-processor",
    "service_type": "STANDALONE",
    "status": "ACTIVE"
  },
  "suggested_interfaces": [
    {
      "interface_type": "STDIN",
      "description": "Standard input stream"
    },
    {
      "interface_type": "STDOUT",
      "description": "Standard output stream"
    }
  ]
}
```

**Error Codes:**
- `400` - Missing required fields or invalid request
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to create service

---

### Create Standalone Service from Git Repository

Creates a standalone service by cloning a Git repository, optionally building, and configuring an executable.

**Endpoint:** `POST /api/services/standalone/from-git`

**Authentication:** JWT required

**Request Body:**
```json
{
  "service_name": "rust-app",
  "repo_url": "https://github.com/user/rust-app.git",
  "branch": "main",
  "executable_path": "target/release/app",
  "build_command": "cargo build --release",
  "arguments": ["--config", "config.toml"],
  "working_directory": "",
  "environment_variables": {},
  "timeout_seconds": 300,
  "run_as_user": "",
  "username": "",
  "password": "",
  "description": "Rust application",
  "capabilities": {},
  "custom_interfaces": []
}
```

**Required Fields:**
- `service_name` - Name for the service
- `repo_url` - Git repository URL
- `executable_path` - Path to executable within the repository (after build)

**Optional Fields:**
- `branch` - Git branch (default: "main")
- `build_command` - Shell command to build the project
- `arguments` - Array of command-line arguments
- `working_directory` - Working directory (relative to repo root)
- `environment_variables` - Object of environment variables
- `timeout_seconds` - Execution timeout (default: 300)
- `run_as_user` - User to run as
- `username` - Git username (for private repos)
- `password` - Git password/token (for private repos)
- `description` - Service description
- `capabilities` - Service capabilities
- `custom_interfaces` - Array of interface definitions

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "rust-app",
    "service_type": "STANDALONE",
    "status": "ACTIVE"
  },
  "suggested_interfaces": []
}
```

**Error Codes:**
- `400` - Missing required fields
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to create service (clone/build failed)

**Notes:**
- Repository is cloned to a temporary directory
- If `build_command` is provided, it's executed before service creation
- The executable path is relative to the repository root

---

### Get Standalone Service Details

Returns detailed information for a standalone service.

**Endpoint:** `GET /api/services/standalone/{id}/details`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "python-processor",
    "service_type": "STANDALONE",
    "status": "ACTIVE",
    "interfaces": []
  },
  "details": {
    "id": 1,
    "service_id": 1,
    "executable_path": "/usr/bin/python3",
    "arguments": ["-u", "script.py"],
    "working_directory": "/opt/myapp",
    "environment_variables": {
      "PYTHONUNBUFFERED": "1"
    },
    "timeout_seconds": 600,
    "run_as_user": "appuser:appgroup",
    "source": "local"
  }
}
```

**Error Codes:**
- `400` - Invalid service ID or not a standalone service
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to retrieve details

---

### Finalize Standalone Service Upload

Finalizes a standalone service after WebSocket chunked upload completes.

**Endpoint:** `POST /api/services/standalone/{id}/finalize`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Request Body:**
```json
{
  "executable_path": "app/run.sh",
  "arguments": ["--mode", "production"],
  "working_directory": "app",
  "environment_variables": {
    "ENV": "production"
  },
  "timeout_seconds": 300,
  "run_as_user": "",
  "capabilities": {},
  "custom_interfaces": []
}
```

**Required Fields:**
- `executable_path` - Path to executable within uploaded directory

**Optional Fields:**
- (Same as create standalone from local)

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "uploaded-service",
    "service_type": "STANDALONE",
    "status": "ACTIVE",
    "interfaces": []
  },
  "suggested_interfaces": []
}
```

**Error Codes:**
- `400` - Invalid service ID, not a standalone service, or invalid request
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to finalize service

**Notes:**
- This endpoint is called after WebSocket file upload completes
- The service must already exist (created during upload)
- See WebSocket documentation for file upload protocol

---

### Delete Standalone Service

Deletes a standalone service.

**Endpoint:** `DELETE /api/services/standalone/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Service ID (integer)

**Response:**
```json
{
  "success": true,
  "message": "Service deleted successfully"
}
```

**Error Codes:**
- `400` - Invalid service ID or not a standalone service
- `401` - Unauthorized
- `404` - Service not found
- `405` - Method not allowed
- `500` - Failed to delete service

**Notes:**
- Cascade deletes service details and interfaces
- Files on disk are NOT automatically deleted

---

## Workflows

Endpoints for creating, managing, and executing workflows.

### Get All Workflows

Returns all workflows defined on this node.

**Endpoint:** `GET /api/workflows`

**Authentication:** JWT required

**Response:**
```json
{
  "workflows": [
    {
      "id": 1,
      "name": "Data Processing Pipeline",
      "description": "Multi-stage data processing",
      "definition": {},
      "created_at": "2024-12-25T10:00:00Z",
      "updated_at": "2024-12-25T10:00:00Z"
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve workflows

---

### Get Workflow by ID

Returns a specific workflow.

**Endpoint:** `GET /api/workflows/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "id": 1,
  "name": "Data Processing Pipeline",
  "description": "Multi-stage data processing",
  "definition": {
    "nodes": [],
    "edges": []
  },
  "created_at": "2024-12-25T10:00:00Z",
  "updated_at": "2024-12-25T10:00:00Z"
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `404` - Workflow not found
- `405` - Method not allowed
- `500` - Internal server error

---

### Create Workflow

Creates a new workflow.

**Endpoint:** `POST /api/workflows`

**Authentication:** JWT required

**Request Body:**
```json
{
  "name": "My Workflow",
  "description": "Workflow description",
  "definition": {}
}
```

**Required Fields:**
- `name` - Workflow name

**Optional Fields:**
- `description` - Workflow description
- `definition` - Workflow definition object (default: {})

**Response:**
```json
{
  "id": 1,
  "name": "My Workflow",
  "description": "Workflow description",
  "definition": {},
  "created_at": "2024-12-25T10:00:00Z",
  "updated_at": "2024-12-25T10:00:00Z"
}
```

**Error Codes:**
- `400` - Invalid request body or missing name
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to create workflow

---

### Update Workflow

Updates an existing workflow.

**Endpoint:** `PUT /api/workflows/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Request Body:**
```json
{
  "name": "Updated Workflow Name",
  "description": "Updated description",
  "definition": {}
}
```

**Required Fields:**
- `name` - Workflow name

**Response:**
```json
{
  "id": 1,
  "name": "Updated Workflow Name",
  "description": "Updated description",
  "definition": {},
  "created_at": "2024-12-25T10:00:00Z",
  "updated_at": "2024-12-25T11:00:00Z"
}
```

**Error Codes:**
- `400` - Invalid workflow ID or request body
- `401` - Unauthorized
- `404` - Workflow not found
- `405` - Method not allowed
- `500` - Failed to update workflow

---

### Delete Workflow

Deletes a workflow.

**Endpoint:** `DELETE /api/workflows/{id}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "message": "Workflow deleted successfully"
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `404` - Workflow not found
- `405` - Method not allowed
- `500` - Failed to delete workflow

---

### Execute Workflow

Executes a workflow asynchronously.

**Endpoint:** `POST /api/workflows/{id}/execute`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "message": "Workflow execution started - validation and execution in progress",
  "workflow_id": 1,
  "note": "Check workflow status via GET /api/workflows/:id for execution progress"
}
```

**Error Codes:**
- `400` - Invalid workflow ID or failed to build workflow definition
- `401` - Unauthorized
- `404` - Workflow not found
- `405` - Method not allowed
- `503` - Workflow manager not initialized

**Notes:**
- This is an **asynchronous operation**
- Returns `202 Accepted` immediately
- Workflow is validated and executed in the background
- Monitor progress via WebSocket events or by polling workflow status
- The workflow definition is built from nodes and connections before execution

---

### Get Workflow Jobs (Legacy)

Returns all jobs associated with a workflow.

**Endpoint:** `GET /api/workflows/{id}/jobs`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "jobs": [
    {
      "id": 1,
      "workflow_id": 1,
      "status": "COMPLETED",
      "created_at": "2024-12-25T10:00:00Z"
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve jobs

**Notes:**
- This is a legacy endpoint
- Prefer using `/api/workflows/{id}/execution-instances` for new workflows

---

### Get Workflow Execution Instances

Returns all execution instances for a workflow.

**Endpoint:** `GET /api/workflows/{id}/execution-instances`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "executions": [
    {
      "id": 1,
      "workflow_id": 1,
      "status": "COMPLETED",
      "started_at": "2024-12-25T10:00:00Z",
      "completed_at": "2024-12-25T10:05:00Z",
      "error": null
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve execution instances

---

### Get Workflow Executions (Legacy)

Returns job execution details for a workflow.

**Endpoint:** `GET /api/workflows/{id}/executions`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "executions": [],
  "total": 0
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve executions

**Notes:**
- This is a legacy endpoint for job_executions table
- Prefer using execution-instances for new workflows

---

### Get Workflow Nodes

Returns all nodes in a workflow.

**Endpoint:** `GET /api/workflows/{id}/nodes`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "nodes": [
    {
      "id": 1,
      "workflow_id": 1,
      "node_type": "SERVICE",
      "peer_id": "12D3KooW...",
      "service_id": 5,
      "config": {},
      "gui_props": {
        "position": {"x": 100, "y": 100}
      }
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve nodes

---

### Add Workflow Node

Adds a node to a workflow.

**Endpoint:** `POST /api/workflows/{id}/nodes`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Request Body:**
```json
{
  "node_type": "SERVICE",
  "peer_id": "12D3KooW...",
  "service_id": 5,
  "config": {
    "timeout": 300
  },
  "gui_props": {
    "position": {"x": 100, "y": 100}
  }
}
```

**Response:**
```json
{
  "id": 1,
  "workflow_id": 1,
  "node_type": "SERVICE",
  "peer_id": "12D3KooW...",
  "service_id": 5,
  "config": {},
  "gui_props": {}
}
```

**Error Codes:**
- `400` - Invalid workflow ID or request body
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to add node

---

### Delete Workflow Node

Deletes a node from a workflow.

**Endpoint:** `DELETE /api/workflows/{id}/nodes/{nodeId}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)
- `nodeId` - Node ID (integer)

**Response:**
```json
{
  "message": "Node deleted successfully"
}
```

**Error Codes:**
- `400` - Invalid workflow ID or node ID
- `401` - Unauthorized
- `404` - Node not found
- `405` - Method not allowed
- `500` - Failed to delete node

---

### Update Workflow Node GUI Properties

Updates the GUI properties (position, etc.) for a workflow node.

**Endpoint:** `PUT /api/workflows/{id}/nodes/{nodeId}/gui-props`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)
- `nodeId` - Node ID (integer)

**Request Body:**
```json
{
  "gui_props": {
    "position": {"x": 200, "y": 150},
    "width": 200,
    "height": 100
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "GUI properties updated"
}
```

**Error Codes:**
- `400` - Invalid IDs or request body
- `401` - Unauthorized
- `404` - Node not found
- `405` - Method not allowed
- `500` - Failed to update properties

---

### Update Workflow Node Configuration

Updates the configuration for a workflow node.

**Endpoint:** `PUT /api/workflows/{id}/nodes/{nodeId}/config`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)
- `nodeId` - Node ID (integer)

**Request Body:**
```json
{
  "config": {
    "timeout": 600,
    "retry_count": 3
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "Node configuration updated"
}
```

**Error Codes:**
- `400` - Invalid IDs or request body
- `401` - Unauthorized
- `404` - Node not found
- `405` - Method not allowed
- `500` - Failed to update configuration

---

### Get Workflow Connections

Returns all connections (edges) in a workflow.

**Endpoint:** `GET /api/workflows/{id}/connections`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "connections": [
    {
      "id": 1,
      "workflow_id": 1,
      "source_node_id": 1,
      "source_interface_id": 1,
      "target_node_id": 2,
      "target_interface_id": 2
    }
  ],
  "total": 1
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve connections

---

### Add Workflow Connection

Adds a connection between two nodes in a workflow.

**Endpoint:** `POST /api/workflows/{id}/connections`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Request Body:**
```json
{
  "source_node_id": 1,
  "source_interface_id": 1,
  "target_node_id": 2,
  "target_interface_id": 2
}
```

**Response:**
```json
{
  "id": 1,
  "workflow_id": 1,
  "source_node_id": 1,
  "source_interface_id": 1,
  "target_node_id": 2,
  "target_interface_id": 2
}
```

**Error Codes:**
- `400` - Invalid workflow ID or request body
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to add connection

---

### Update Workflow Connection

Updates an existing workflow connection.

**Endpoint:** `PUT /api/workflows/{id}/connections/{connectionId}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)
- `connectionId` - Connection ID (integer)

**Request Body:**
```json
{
  "source_node_id": 1,
  "source_interface_id": 1,
  "target_node_id": 3,
  "target_interface_id": 3
}
```

**Response:**
```json
{
  "success": true,
  "message": "Connection updated"
}
```

**Error Codes:**
- `400` - Invalid IDs or request body
- `401` - Unauthorized
- `404` - Connection not found
- `405` - Method not allowed
- `500` - Failed to update connection

---

### Delete Workflow Connection

Deletes a connection from a workflow.

**Endpoint:** `DELETE /api/workflows/{id}/connections/{connectionId}`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)
- `connectionId` - Connection ID (integer)

**Response:**
```json
{
  "message": "Connection deleted successfully"
}
```

**Error Codes:**
- `400` - Invalid workflow ID or connection ID
- `401` - Unauthorized
- `404` - Connection not found
- `405` - Method not allowed
- `500` - Failed to delete connection

---

### Get Workflow UI State

Returns the UI state (canvas position, zoom, etc.) for a workflow.

**Endpoint:** `GET /api/workflows/{id}/ui-state`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Response:**
```json
{
  "ui_state": {
    "zoom": 1.0,
    "pan_x": 0,
    "pan_y": 0
  }
}
```

**Error Codes:**
- `400` - Invalid workflow ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve UI state

---

### Update Workflow UI State

Updates the UI state for a workflow.

**Endpoint:** `PUT /api/workflows/{id}/ui-state`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow ID (integer)

**Request Body:**
```json
{
  "ui_state": {
    "zoom": 1.5,
    "pan_x": 100,
    "pan_y": 50
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "UI state updated"
}
```

**Error Codes:**
- `400` - Invalid workflow ID or request body
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to update UI state

---

### Get Job Execution Interfaces

Returns the interfaces for a specific job execution.

**Endpoint:** `GET /api/job-executions/{id}/interfaces`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Job execution ID (integer)

**Response:**
```json
{
  "interfaces": [
    {
      "id": 1,
      "job_execution_id": 1,
      "interface_type": "STDOUT",
      "value": "Output data..."
    }
  ]
}
```

**Error Codes:**
- `400` - Invalid job execution ID
- `401` - Unauthorized
- `404` - Job execution not found
- `405` - Method not allowed
- `500` - Failed to retrieve interfaces

---

### Get Workflow Execution Jobs

Returns all jobs for a specific workflow execution instance.

**Endpoint:** `GET /api/workflow-executions/{id}/jobs`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow execution ID (integer)

**Response:**
```json
{
  "jobs": [
    {
      "id": 1,
      "workflow_execution_id": 1,
      "node_id": 1,
      "status": "COMPLETED",
      "started_at": "2024-12-25T10:00:00Z",
      "completed_at": "2024-12-25T10:01:00Z"
    }
  ]
}
```

**Error Codes:**
- `400` - Invalid workflow execution ID
- `401` - Unauthorized
- `404` - Workflow execution not found
- `405` - Method not allowed
- `500` - Failed to retrieve jobs

---

### Get Workflow Execution Status

Returns the current status of a workflow execution.

**Endpoint:** `GET /api/workflow-executions/{id}/status`

**Authentication:** JWT required

**Path Parameters:**
- `id` - Workflow execution ID (integer)

**Response:**
```json
{
  "id": 1,
  "workflow_id": 1,
  "status": "RUNNING",
  "started_at": "2024-12-25T10:00:00Z",
  "completed_at": null,
  "error": null,
  "progress": 50
}
```

**Error Codes:**
- `400` - Invalid workflow execution ID
- `401` - Unauthorized
- `404` - Workflow execution not found
- `405` - Method not allowed
- `500` - Failed to retrieve status

---

## Blacklist

Endpoints for managing the peer blacklist.

### Get Blacklist

Returns all blacklisted peers.

**Endpoint:** `GET /api/blacklist`

**Authentication:** JWT required

**Response:**
```json
{
  "blacklist": [
    {
      "peer_id": "12D3KooW...",
      "reason": "Manual blacklist",
      "blacklisted_at": "2024-12-25T10:00:00Z"
    }
  ],
  "peer_ids": ["12D3KooW..."],
  "total": 1
}
```

**Error Codes:**
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to retrieve blacklist

---

### Add to Blacklist

Adds a peer to the blacklist.

**Endpoint:** `POST /api/blacklist`

**Authentication:** JWT required

**Request Body:**
```json
{
  "peer_id": "12D3KooW...",
  "reason": "Malicious behavior"
}
```

**Required Fields:**
- `peer_id` - Peer ID to blacklist

**Optional Fields:**
- `reason` - Reason for blacklisting (default: "Manually blacklisted")

**Response:**
```json
{
  "peer_id": "12D3KooW...",
  "reason": "Malicious behavior",
  "message": "Peer added to blacklist successfully"
}
```

**Error Codes:**
- `400` - Invalid request body or missing peer_id
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to blacklist peer

---

### Remove from Blacklist

Removes a peer from the blacklist.

**Endpoint:** `DELETE /api/blacklist/{peer_id}`

**Authentication:** JWT required

**Path Parameters:**
- `peer_id` - Peer ID to remove from blacklist

**Response:**
```json
{
  "message": "Peer removed from blacklist successfully"
}
```

**Error Codes:**
- `400` - Missing peer ID
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to remove peer from blacklist

---

### Check Blacklist Status

Checks if a specific peer is blacklisted.

**Endpoint:** `GET /api/blacklist/check?peer_id={peer_id}`

**Authentication:** JWT required

**Query Parameters:**
- `peer_id` - Peer ID to check

**Response:**
```json
{
  "peer_id": "12D3KooW...",
  "is_blacklisted": true
}
```

**Error Codes:**
- `400` - Missing peer_id query parameter
- `401` - Unauthorized
- `405` - Method not allowed
- `500` - Failed to check blacklist status

---

## Relay Management

Endpoints for managing relay connections (for nodes behind NAT) and relay sessions (for relay nodes).

### Get Relay Sessions (Relay Mode)

Returns all active relay sessions. Only available when node is in relay mode.

**Endpoint:** `GET /api/relay/sessions`

**Authentication:** JWT required

**Response:**
```json
[
  {
    "session_id": "session-abc123",
    "client_node_id": "12D3KooW...",
    "client_peer_id": "peer-123",
    "session_type": "QUIC",
    "start_time": 1703508000,
    "duration_seconds": 3600,
    "ingress_bytes": 1048576,
    "egress_bytes": 2097152,
    "total_bytes": 3145728,
    "earnings": 0.05,
    "last_keepalive": 1703511600
  }
]
```

**Error Codes:**
- `401` - Unauthorized
- `403` - Node is not in relay mode
- `405` - Method not allowed
- `500` - Relay peer not initialized

**Notes:**
- Only available when node is running with `--relay` flag
- Session earnings are calculated based on bandwidth usage and relay pricing

---

### Disconnect Relay Session

Disconnects a specific relay session.

**Endpoint:** `POST /api/relay/sessions/{sessionID}/disconnect`

**Authentication:** JWT required

**Path Parameters:**
- `sessionID` - Session ID to disconnect

**Response:**
```json
{
  "success": true,
  "message": "Session disconnected successfully"
}
```

**Error Codes:**
- `400` - Invalid URL format
- `401` - Unauthorized
- `404` - Session not found
- `405` - Method not allowed
- `500` - Relay peer not initialized

---

### Blacklist and Disconnect Relay Session

Blacklists a client peer and disconnects their session.

**Endpoint:** `POST /api/relay/sessions/{sessionID}/blacklist`

**Authentication:** JWT required

**Path Parameters:**
- `sessionID` - Session ID to blacklist

**Response:**
```json
{
  "success": true,
  "message": "Peer blacklisted and disconnected successfully"
}
```

**Error Codes:**
- `400` - Invalid URL format
- `401` - Unauthorized
- `404` - Session not found
- `405` - Method not allowed
- `500` - Relay peer not initialized or failed to blacklist peer

**Notes:**
- The client's node ID is extracted from the session
- The peer is added to the blacklist with reason "Manual blacklist from relay session"
- The session is disconnected after blacklisting

---

### Get Relay Candidates (NAT Mode)

Returns available relay candidates. Only available when node is behind NAT.

**Endpoint:** `GET /api/relay/candidates`

**Authentication:** JWT required

**Response:**
```json
[
  {
    "node_id": "12D3KooW...",
    "peer_id": "peer-123",
    "endpoint": "203.0.113.5:30000",
    "latency": "25ms",
    "latency_ms": 25,
    "reputation_score": 0.95,
    "pricing_per_gb": 0.01,
    "capacity": 100,
    "last_seen": 1703508000,
    "is_connected": true,
    "is_preferred": false,
    "is_available": true,
    "unavailable_msg": ""
  }
]
```

**Fields:**
- `node_id` - DHT node ID (changes on restart)
- `peer_id` - Persistent Ed25519-based peer ID
- `is_connected` - Currently connected to this relay
- `is_preferred` - This relay is marked as preferred
- `is_available` - Relay responded to ping/pong check
- `unavailable_msg` - Reason if relay is unavailable

**Error Codes:**
- `401` - Unauthorized
- `403` - Node is not in NAT mode
- `405` - Method not allowed

**Notes:**
- Only available when node is behind NAT
- Candidates are automatically discovered and ranked
- Latency and availability are continuously monitored

---

### Connect to Relay

Initiates connection to a specific relay.

**Endpoint:** `POST /api/relay/connect`

**Authentication:** JWT required

**Request Body:**
```json
{
  "peer_id": "peer-123"
}
```

**Required Fields:**
- `peer_id` - Persistent peer ID of the relay to connect to

**Response:**
```json
{
  "success": true,
  "message": "Relay connection initiated"
}
```

**Error Codes:**
- `400` - Invalid request body or missing peer_id
- `401` - Unauthorized
- `403` - Node is not in NAT mode
- `405` - Method not allowed

**Notes:**
- This is an **asynchronous operation**
- Returns `202 Accepted` immediately
- Connection status is updated via WebSocket `RELAY_CANDIDATES` event
- Use persistent `peer_id`, not the temporary `node_id`

---

### Disconnect from Relay

Disconnects from the current relay.

**Endpoint:** `POST /api/relay/disconnect`

**Authentication:** JWT required

**Response:**
```json
{
  "success": true,
  "message": "Disconnected from relay successfully"
}
```

**Error Codes:**
- `401` - Unauthorized
- `403` - Node is not in NAT mode
- `405` - Method not allowed

**Notes:**
- Disconnects from currently connected relay
- Node will automatically attempt to reconnect to a new relay
- WebSocket `RELAY_CANDIDATES` event is triggered after disconnect

---

### Set Preferred Relay

Marks a relay as preferred for future connections.

**Endpoint:** `POST /api/relay/prefer`

**Authentication:** JWT required

**Request Body:**
```json
{
  "peer_id": "peer-123"
}
```

**Required Fields:**
- `peer_id` - Persistent peer ID of the relay to prefer

**Response:**
```json
{
  "success": true,
  "message": "Preferred relay set successfully"
}
```

**Error Codes:**
- `400` - Invalid request body or missing peer_id
- `401` - Unauthorized
- `403` - Node is not in NAT mode
- `405` - Method not allowed
- `500` - Failed to set preferred relay

**Notes:**
- The preferred relay will be prioritized during automatic relay selection
- Preference is persistent across node restarts
- Use persistent `peer_id`, not the temporary `node_id`

---

## WebSocket

Real-time bidirectional communication endpoint.

### WebSocket Connection

Establishes a WebSocket connection for real-time updates.

**Endpoint:** `GET /api/ws`

**Protocol:** WebSocket

**Authentication:** None (connection is open, but consider implementing token-based auth)

**Message Format:**
```json
{
  "type": "MESSAGE_TYPE",
  "payload": {}
}
```

**Outbound Message Types (Server to Client):**
- `SERVICE_UPDATED` - Service list changed
- `PEER_UPDATED` - Peer list changed
- `WORKFLOW_UPDATED` - Workflow changed
- `BLACKLIST_UPDATED` - Blacklist changed
- `RELAY_CANDIDATES` - Relay candidates updated
- `PEER_CAPABILITIES_UPDATED` - Full peer capabilities available
- `DOCKER_OPERATION_START` - Docker build started
- `DOCKER_OPERATION_PROGRESS` - Docker build progress
- `DOCKER_OPERATION_COMPLETE` - Docker build completed
- `DOCKER_OPERATION_ERROR` - Docker build failed

**Inbound Message Types (Client to Server):**
- `FILE_UPLOAD_START` - Start chunked file upload
- `FILE_UPLOAD_CHUNK` - Upload file chunk
- `FILE_UPLOAD_END` - Complete file upload
- `SERVICE_SEARCH` - Search for remote services

**Example - Service Update:**
```json
{
  "type": "SERVICE_UPDATED",
  "payload": {}
}
```

**Example - Docker Build Progress:**
```json
{
  "type": "DOCKER_OPERATION_PROGRESS",
  "payload": {
    "operation_id": "docker-git-1735128000000000000",
    "message": "Building Docker image...",
    "percentage": 50
  }
}
```

**Example - File Upload Start:**
```json
{
  "type": "FILE_UPLOAD_START",
  "payload": {
    "session_id": "upload-123",
    "service_id": 1,
    "file_name": "app.zip",
    "file_size": 1048576,
    "chunk_size": 262144
  }
}
```

**Notes:**
- WebSocket connections remain open for real-time updates
- The server broadcasts events to all connected clients
- File uploads use chunked transfer for large files
- Service search queries remote peers for available services

---

## Error Handling

All API endpoints follow consistent error handling patterns.

### HTTP Status Codes

- `200` - Success
- `201` - Created (for POST requests that create resources)
- `202` - Accepted (for asynchronous operations)
- `204` - No Content (for successful DELETE requests)
- `400` - Bad Request (invalid input, missing required fields)
- `401` - Unauthorized (missing or invalid JWT)
- `403` - Forbidden (operation not allowed for this node type)
- `404` - Not Found (resource doesn't exist)
- `405` - Method Not Allowed (wrong HTTP method)
- `500` - Internal Server Error (server-side error)
- `503` - Service Unavailable (service not initialized)

### Error Response Format

All error responses follow this format:

```json
{
  "error": "Error message describing what went wrong"
}
```

Or for authentication endpoints:

```json
{
  "success": false,
  "error": "Error message"
}
```

### Common Error Scenarios

**Missing Authentication:**
```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{
  "error": "Unauthorized"
}
```

**Invalid Request Body:**
```http
HTTP/1.1 400 Bad Request
Content-Type: application/json

{
  "error": "Invalid request body"
}
```

**Resource Not Found:**
```http
HTTP/1.1 404 Not Found
Content-Type: application/json

{
  "error": "Service not found"
}
```

**Internal Server Error:**
```http
HTTP/1.1 500 Internal Server Error
Content-Type: application/json

{
  "error": "Failed to retrieve services"
}
```

---

## Notes

- **HTTPS by Default:** The API server uses HTTPS with ECDSA P-256 certificates by default on port 30069
- **Localhost HTTP:** An additional HTTP server is available on localhost:8081 for local development
- **JWT Expiration:** JWT tokens are valid for 24 hours after issuance
- **Challenge Expiration:** Authentication challenges expire after 5 minutes
- **Asynchronous Operations:** Some operations (Docker Git builds, workflow execution, relay connections) are asynchronous and return immediately with a tracking ID
- **WebSocket Events:** Most state changes trigger WebSocket events for real-time UI updates
- **Timeouts:** The server has generous timeouts (1 hour write timeout) to accommodate long-running operations like Docker builds
- **CORS:** CORS is enabled for all origins (consider restricting in production)
