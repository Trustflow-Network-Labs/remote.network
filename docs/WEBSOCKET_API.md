# WebSocket API Documentation

## Overview

The Remote Network Node WebSocket API provides real-time, bidirectional communication for monitoring node status, managing services, executing workflows, and handling file uploads. The WebSocket connection enables live updates for Docker operations, workflow executions, and network events.

## Connection Setup

### WebSocket Endpoint

```
ws://localhost:8080/api/ws?token=<JWT_TOKEN>
```

For production environments:
```
wss://<host>:<port>/api/ws?token=<JWT_TOKEN>
```

### Authentication

WebSocket connections require JWT authentication via query parameter:

1. **Obtain JWT Token**: First authenticate via REST API `/api/auth/login` to receive a JWT token
2. **Connect with Token**: Append the token as a query parameter: `?token=<JWT_TOKEN>`
3. **Connection Handshake**: On successful authentication, the server sends a `connected` message

**Example Connection (JavaScript)**:
```javascript
const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...";
const ws = new WebSocket(`ws://localhost:8080/api/ws?token=${token}`);

ws.onopen = () => {
  console.log('WebSocket connected');
};

ws.onmessage = (event) => {
  const message = JSON.parse(event.data);
  console.log('Received:', message);
};
```

### Connection Lifecycle

**Connection Confirmation**: Upon successful connection, the server sends:
```json
{
  "type": "connected",
  "payload": {
    "message": "Connected to WebSocket server",
    "peer_id": "12D3KooWABC..."
  },
  "timestamp": 1703520000
}
```

**Keep-Alive**: The server sends ping messages every 54 seconds. Clients should respond with pong or rely on browser's automatic handling.

**Timeouts**:
- Write timeout: 10 seconds
- Pong timeout: 60 seconds
- Max message size: 10 MB

## Message Structure

All WebSocket messages follow a consistent JSON structure:

```json
{
  "type": "<message_type>",
  "payload": { ... },
  "timestamp": <unix_timestamp>
}
```

### Fields

- `type` (string): Message type identifier (see Message Types below)
- `payload` (object): Message-specific data payload
- `timestamp` (integer): Unix timestamp when the message was created

## Message Types

### Control Messages

#### 1. Connected (Server → Client)
Sent immediately after successful WebSocket connection.

**Message Type**: `connected`

**Direction**: Server → Client

**Payload**:
```json
{
  "message": "Connected to WebSocket server",
  "peer_id": "12D3KooWABC..."
}
```

**Example**:
```json
{
  "type": "connected",
  "payload": {
    "message": "Connected to WebSocket server",
    "peer_id": "12D3KooWABCDEF123456"
  },
  "timestamp": 1703520000
}
```

---

#### 2. Ping/Pong (Bidirectional)
Used for connection keep-alive.

**Message Type**: `ping`, `pong`

**Direction**: Bidirectional

**Client Request**:
```json
{
  "type": "ping",
  "payload": null,
  "timestamp": 1703520000
}
```

**Server Response**:
```json
{
  "type": "pong",
  "payload": null,
  "timestamp": 1703520001
}
```

---

#### 3. Error (Server → Client)
General error message.

**Message Type**: `error`

**Direction**: Server → Client

**Payload**:
```json
{
  "error": "Error description",
  "code": "ERROR_CODE"
}
```

---

### Node Status Messages

#### 4. Node Status (Server → Client)
Real-time node status updates broadcast every 5 seconds.

**Message Type**: `node.status`

**Direction**: Server → Client

**Payload**:
```json
{
  "peer_id": "12D3KooWABC...",
  "dht_node_id": "0x1234...",
  "uptime_seconds": 86400,
  "stats": {
    "peer_id": "12D3KooWABC...",
    "dht_node_id": "0x1234...",
    "uptime_seconds": 86400,
    "quic_connections": 5,
    "relay_sessions": 2,
    "services_count": 10
  },
  "known_peers": 42
}
```

**Broadcast Interval**: Every 5 seconds

---

### Peer Management Messages

#### 5. Peers Updated (Server → Client)
List of known peers broadcast periodically.

**Message Type**: `peers.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "peers": [
    {
      "id": "12D3KooWXYZ...",
      "addresses": ["192.168.1.100:8080", "/ip4/192.168.1.100/udp/8080/quic"],
      "last_seen": 1703520000,
      "is_relay": false,
      "can_be_relay": true,
      "connection_quality": 95,
      "metadata": {}
    }
  ]
}
```

**Broadcast Interval**: Every 10 seconds

---

#### 6. Blacklist Updated (Server → Client)
Blacklisted peers list broadcast periodically.

**Message Type**: `blacklist.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "blacklist": [
    {
      "peer_id": "12D3KooWBAD...",
      "reason": "Malicious behavior detected",
      "added_at": 1703520000,
      "expires_at": 0
    }
  ]
}
```

**Broadcast Interval**: Every 30 seconds

---

### Relay Messages

#### 7. Relay Sessions (Server → Client)
Active relay sessions when node is acting as a relay.

**Message Type**: `relay.sessions`

**Direction**: Server → Client

**Payload**:
```json
{
  "sessions": [
    {
      "session_id": "session-uuid-1234",
      "remote_peer_id": "12D3KooWXYZ...",
      "start_time": 1703520000,
      "duration_seconds": 3600,
      "bytes_sent": 1048576,
      "bytes_recv": 524288,
      "earnings": 0.0042
    }
  ]
}
```

**Broadcast Interval**: Every 5 seconds (when relay mode active)

---

#### 8. Relay Candidates (Server → Client)
Available relay candidates when node is behind NAT.

**Message Type**: `relay.candidates`

**Direction**: Server → Client

**Payload**:
```json
{
  "candidates": [
    {
      "node_id": "0x1234...",
      "peer_id": "12D3KooWRELAY...",
      "endpoint": "relay.example.com:8080",
      "latency": "50ms",
      "latency_ms": 50,
      "reputation_score": 0.95,
      "pricing_per_gb": 0.001,
      "capacity": 1000,
      "last_seen": 1703520000,
      "is_connected": true,
      "is_preferred": false
    }
  ]
}
```

**Broadcast Interval**: Every 5 seconds (when NAT mode active)

---

### Service Management Messages

#### 9. Services Updated (Server → Client)
List of local services broadcast periodically.

**Message Type**: `services.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "services": [
    {
      "id": "1",
      "name": "image-processor",
      "description": "Image processing service",
      "type": "DOCKER",
      "config": {
        "image": "myorg/image-processor:latest",
        "capabilities": ["resize", "crop", "filter"]
      },
      "status": "ACTIVE",
      "created_at": 1703520000,
      "updated_at": 1703520100
    }
  ]
}
```

**Broadcast Interval**: Every 15 seconds

---

#### 10. Service Search Request (Client → Server)
Search for services across the network.

**Message Type**: `service.search.request`

**Direction**: Client → Server

**Request Payload**:
```json
{
  "query": "image processing",
  "service_type": "DOCKER,DATA",
  "peer_ids": ["12D3KooWXYZ...", "12D3KooWABC..."],
  "active_only": true
}
```

**Fields**:
- `query` (string): Search text (searches in name and description)
- `service_type` (string): Comma-separated service types (DOCKER, DATA, STANDALONE). Empty = all types
- `peer_ids` (array): Specific peer IDs to query. Empty = all peers
- `active_only` (boolean): Only return active services

---

#### 11. Service Search Response (Server → Client)
Results from service search (streamed as they arrive).

**Message Type**: `service.search.response`

**Direction**: Server → Client

**Response Payload**:
```json
{
  "services": [
    {
      "id": 1,
      "name": "image-processor",
      "description": "Advanced image processing",
      "service_type": "DOCKER",
      "type": "COMPUTE",
      "status": "ACTIVE",
      "pricing_amount": 0.001,
      "pricing_type": "PER_EXECUTION",
      "pricing_interval": 0,
      "pricing_unit": "EXECUTION",
      "capabilities": {
        "gpu": true,
        "max_resolution": "4K"
      },
      "peer_id": "12D3KooWXYZ...",
      "peer_name": "node-1",
      "interfaces": [
        {
          "interface_type": "STDIN",
          "path": "",
          "mount_function": ""
        },
        {
          "interface_type": "MOUNT",
          "path": "/input",
          "mount_function": "INPUT"
        }
      ]
    }
  ],
  "error": "",
  "complete": false
}
```

**Fields**:
- `services` (array): Array of matching services
- `error` (string): Error message if search failed
- `complete` (boolean): `true` when search is complete, `false` for partial results

**Notes**:
- Server streams partial results as they arrive from peers
- Multiple messages are sent with `complete: false`, followed by final message with `complete: true`
- Enables responsive UI updates as results arrive

---

#### 12. Peer Capabilities Updated (Server → Client)
Updates when peer capabilities change.

**Message Type**: `peer.capabilities.updated`

**Direction**: Server → Client

---

### Workflow Management Messages

#### 13. Workflows Updated (Server → Client)
List of workflows broadcast periodically.

**Message Type**: `workflows.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "workflows": [
    {
      "id": "1",
      "name": "data-pipeline",
      "description": "ETL data processing pipeline",
      "status": "running",
      "job_count": 5,
      "config": {
        "jobs": [...],
        "dependencies": {...}
      },
      "created_at": 1703520000,
      "updated_at": 1703520100
    }
  ]
}
```

**Broadcast Interval**: Every 15 seconds

---

#### 14. Execution Updated (Server → Client)
Workflow execution status updates.

**Message Type**: `execution.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "execution_id": 123,
  "workflow_id": 1,
  "status": "running",
  "error": "",
  "started_at": "2024-01-01T12:00:00Z",
  "completed_at": "2024-01-01T12:30:00Z"
}
```

**Status Values**: `pending`, `running`, `completed`, `failed`, `cancelled`

---

#### 15. Job Status Updated (Server → Client)
Individual job status updates within workflow execution.

**Message Type**: `job.status.updated`

**Direction**: Server → Client

**Payload**:
```json
{
  "job_execution_id": 456,
  "workflow_job_id": 10,
  "execution_id": 123,
  "job_name": "data-transform",
  "status": "running",
  "error": ""
}
```

**Status Values**: `pending`, `running`, `completed`, `failed`, `skipped`

---

### Docker Operations Messages

#### 16. Docker Pull Progress (Server → Client)
Real-time Docker image pull progress.

**Message Type**: `docker.pull.progress`

**Direction**: Server → Client

**Payload**:
```json
{
  "service_name": "image-processor",
  "image_name": "myorg/image-processor:latest",
  "status": "Downloading",
  "progress": "[==============>      ] 70%",
  "progress_detail": {
    "current": 73400320,
    "total": 104857600
  }
}
```

---

#### 17. Docker Build Output (Server → Client)
Real-time Docker image build output.

**Message Type**: `docker.build.output`

**Direction**: Server → Client

**Payload**:
```json
{
  "service_name": "custom-service",
  "image_name": "custom-service:latest",
  "stream": "Step 3/5 : RUN apt-get update\n",
  "error": "",
  "error_detail": null
}
```

---

#### 18. Docker Operation Start (Server → Client)
Notification that a Docker operation has started.

**Message Type**: `docker.operation.start`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-1234",
  "operation_type": "build",
  "service_name": "my-service",
  "image_name": "my-service:latest",
  "message": "Starting Docker image build..."
}
```

**Operation Types**: `build`, `pull`, `create`

---

#### 19. Docker Operation Progress (Server → Client)
Progress updates for Docker operations.

**Message Type**: `docker.operation.progress`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-1234",
  "message": "Building Docker image...",
  "percentage": 45.5,
  "stream": "Step 5/10 : COPY . /app\n"
}
```

---

#### 20. Docker Operation Complete (Server → Client)
Notification that a Docker operation completed successfully.

**Message Type**: `docker.operation.complete`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-1234",
  "service_id": 42,
  "service_name": "my-service",
  "image_name": "my-service:latest",
  "suggested_interfaces": [
    {
      "interface_type": "STDIN",
      "path": "",
      "mount_function": ""
    }
  ],
  "service": {
    "id": 42,
    "name": "my-service",
    "status": "ACTIVE"
  }
}
```

---

#### 21. Docker Operation Error (Server → Client)
Notification that a Docker operation failed.

**Message Type**: `docker.operation.error`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-1234",
  "error": "Failed to build image: base image not found",
  "code": "IMAGE_BUILD_FAILED"
}
```

---

### Standalone Service Operations Messages

#### 22. Standalone Operation Start (Server → Client)
Notification that a standalone service operation started.

**Message Type**: `standalone.operation.start`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-5678",
  "operation_type": "git-clone",
  "service_name": "rust-calculator",
  "repo_url": "https://github.com/example/calculator.git",
  "message": "Cloning Git repository..."
}
```

**Operation Types**: `git-clone`, `git-build`, `upload`

---

#### 23. Standalone Operation Progress (Server → Client)
Progress updates for standalone service operations.

**Message Type**: `standalone.operation.progress`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-5678",
  "message": "Compiling Rust binary...",
  "percentage": 60.0,
  "step": "building"
}
```

**Steps**: `cloning`, `building`, `validating`

---

#### 24. Standalone Operation Complete (Server → Client)
Notification that a standalone operation completed successfully.

**Message Type**: `standalone.operation.complete`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-5678",
  "service_id": 43,
  "service_name": "rust-calculator",
  "executable_path": "/path/to/calculator",
  "commit_hash": "abc123def456",
  "suggested_interfaces": [
    {
      "interface_type": "STDIN",
      "path": "",
      "mount_function": ""
    }
  ],
  "service": {
    "id": 43,
    "name": "rust-calculator",
    "status": "ACTIVE"
  }
}
```

---

#### 25. Standalone Operation Error (Server → Client)
Notification that a standalone operation failed.

**Message Type**: `standalone.operation.error`

**Direction**: Server → Client

**Payload**:
```json
{
  "operation_id": "op-uuid-5678",
  "error": "Build failed: cargo build returned non-zero exit code",
  "code": "BUILD_FAILED"
}
```

---

### File Upload Messages

The WebSocket API supports chunked file uploads with pause/resume capability.

#### 26. File Upload Start (Client → Server)
Initiate a new file upload session.

**Message Type**: `file.upload.start`

**Direction**: Client → Server

**Request Payload**:
```json
{
  "service_id": 1,
  "upload_group_id": "upload-group-uuid",
  "filename": "data.csv",
  "file_path": "datasets/data.csv",
  "file_index": 0,
  "total_files": 3,
  "total_size": 10485760,
  "total_chunks": 100,
  "chunk_size": 104857
}
```

**Fields**:
- `service_id`: ID of the service receiving the file
- `upload_group_id`: Unique ID for the upload group (supports multi-file uploads)
- `filename`: Name of the file being uploaded
- `file_path`: Relative path within the service context
- `file_index`: Index of this file in the upload group (0-based)
- `total_files`: Total number of files in the upload group
- `total_size`: Total size of this file in bytes
- `total_chunks`: Number of chunks for this file
- `chunk_size`: Size of each chunk in bytes

**Server Response**: `file.upload.progress` with session_id

---

#### 27. File Upload Chunk (Client → Server)
Send a chunk of file data.

**Message Type**: `file.upload.chunk`

**Direction**: Client → Server

**Request Payload**:
```json
{
  "session_id": "session-uuid-1234",
  "chunk_index": 42,
  "data": "base64_encoded_chunk_data..."
}
```

**Fields**:
- `session_id`: Upload session ID (received from upload start response)
- `chunk_index`: Index of this chunk (0-based)
- `data`: Base64-encoded chunk data

**Server Response**: `file.upload.progress` after each chunk

---

#### 28. File Upload Progress (Server → Client)
Progress update for file upload.

**Message Type**: `file.upload.progress`

**Direction**: Server → Client

**Payload**:
```json
{
  "session_id": "session-uuid-1234",
  "chunks_received": 42,
  "bytes_uploaded": 4404096,
  "percentage": 42.0
}
```

---

#### 29. File Upload Pause (Client → Server)
Pause an active upload session.

**Message Type**: `file.upload.pause`

**Direction**: Client → Server

**Request Payload**:
```json
{
  "session_id": "session-uuid-1234"
}
```

---

#### 30. File Upload Resume (Client → Server)
Resume a paused upload session.

**Message Type**: `file.upload.resume`

**Direction**: Client → Server

**Request Payload**:
```json
{
  "session_id": "session-uuid-1234"
}
```

**Server Response**: `file.upload.progress` with current progress

---

#### 31. File Upload Complete (Server → Client)
Notification that file upload completed successfully.

**Message Type**: `file.upload.complete`

**Direction**: Server → Client

**Payload**:
```json
{
  "session_id": "session-uuid-1234",
  "file_hash": "sha256:abc123def456..."
}
```

---

#### 32. File Upload Error (Server → Client)
Notification that file upload failed.

**Message Type**: `file.upload.error`

**Direction**: Server → Client

**Payload**:
```json
{
  "session_id": "session-uuid-1234",
  "error": "Failed to write chunk to disk",
  "code": "WRITE_FAILED"
}
```

**Error Codes**:
- `SESSION_NOT_FOUND`: Upload session not found
- `SESSION_RESTORE_FAILED`: Failed to restore paused session
- `INVALID_ENCODING`: Chunk data encoding is invalid
- `WRITE_FAILED`: Failed to write chunk to disk
- `UPLOAD_START_FAILED`: Failed to start upload session

---

## Event Flow Examples

### Example 1: Service Search Flow

```
Client → Server:
{
  "type": "service.search.request",
  "payload": {
    "query": "image",
    "service_type": "DOCKER",
    "peer_ids": [],
    "active_only": true
  },
  "timestamp": 1703520000
}

Server → Client (Partial Result 1):
{
  "type": "service.search.response",
  "payload": {
    "services": [
      { "id": 1, "name": "image-processor", "peer_id": "12D3KooWXYZ..." }
    ],
    "error": "",
    "complete": false
  },
  "timestamp": 1703520001
}

Server → Client (Partial Result 2):
{
  "type": "service.search.response",
  "payload": {
    "services": [
      { "id": 5, "name": "image-resizer", "peer_id": "12D3KooWABC..." }
    ],
    "error": "",
    "complete": false
  },
  "timestamp": 1703520002
}

Server → Client (Final Result):
{
  "type": "service.search.response",
  "payload": {
    "services": [],
    "error": "",
    "complete": true
  },
  "timestamp": 1703520003
}
```

---

### Example 2: Docker Build Flow

```
Server → Client (Start):
{
  "type": "docker.operation.start",
  "payload": {
    "operation_id": "op-123",
    "operation_type": "build",
    "service_name": "my-app",
    "message": "Starting Docker image build..."
  },
  "timestamp": 1703520000
}

Server → Client (Progress 1):
{
  "type": "docker.operation.progress",
  "payload": {
    "operation_id": "op-123",
    "message": "Cloning Git repository...",
    "percentage": 10
  },
  "timestamp": 1703520005
}

Server → Client (Build Output):
{
  "type": "docker.build.output",
  "payload": {
    "service_name": "my-app",
    "image_name": "my-app:latest",
    "stream": "Step 1/5 : FROM node:18\n"
  },
  "timestamp": 1703520010
}

Server → Client (Progress 2):
{
  "type": "docker.operation.progress",
  "payload": {
    "operation_id": "op-123",
    "message": "Building Docker image...",
    "percentage": 60
  },
  "timestamp": 1703520020
}

Server → Client (Complete):
{
  "type": "docker.operation.complete",
  "payload": {
    "operation_id": "op-123",
    "service_id": 42,
    "service_name": "my-app",
    "image_name": "my-app:latest"
  },
  "timestamp": 1703520030
}
```

---

### Example 3: Workflow Execution Flow

```
Server → Client (Execution Started):
{
  "type": "execution.updated",
  "payload": {
    "execution_id": 123,
    "workflow_id": 1,
    "status": "running",
    "started_at": "2024-01-01T12:00:00Z"
  },
  "timestamp": 1703520000
}

Server → Client (Job 1 Update):
{
  "type": "job.status.updated",
  "payload": {
    "job_execution_id": 456,
    "workflow_job_id": 10,
    "execution_id": 123,
    "job_name": "data-fetch",
    "status": "completed"
  },
  "timestamp": 1703520010
}

Server → Client (Job 2 Update):
{
  "type": "job.status.updated",
  "payload": {
    "job_execution_id": 457,
    "workflow_job_id": 11,
    "execution_id": 123,
    "job_name": "data-transform",
    "status": "running"
  },
  "timestamp": 1703520011
}

Server → Client (Execution Complete):
{
  "type": "execution.updated",
  "payload": {
    "execution_id": 123,
    "workflow_id": 1,
    "status": "completed",
    "started_at": "2024-01-01T12:00:00Z",
    "completed_at": "2024-01-01T12:30:00Z"
  },
  "timestamp": 1703521800
}
```

---

### Example 4: File Upload Flow

```
Client → Server (Start Upload):
{
  "type": "file.upload.start",
  "payload": {
    "service_id": 1,
    "upload_group_id": "upload-123",
    "filename": "data.csv",
    "file_path": "datasets/data.csv",
    "file_index": 0,
    "total_files": 1,
    "total_size": 1048576,
    "total_chunks": 10,
    "chunk_size": 104857
  },
  "timestamp": 1703520000
}

Server → Client (Initial Progress):
{
  "type": "file.upload.progress",
  "payload": {
    "session_id": "session-abc",
    "chunks_received": 0,
    "bytes_uploaded": 0,
    "percentage": 0
  },
  "timestamp": 1703520001
}

Client → Server (Chunk 1):
{
  "type": "file.upload.chunk",
  "payload": {
    "session_id": "session-abc",
    "chunk_index": 0,
    "data": "YmFzZTY0X2VuY29kZWRfZGF0YQ=="
  },
  "timestamp": 1703520002
}

Server → Client (Progress Update):
{
  "type": "file.upload.progress",
  "payload": {
    "session_id": "session-abc",
    "chunks_received": 1,
    "bytes_uploaded": 104857,
    "percentage": 10.0
  },
  "timestamp": 1703520003
}

... (more chunks) ...

Server → Client (Complete):
{
  "type": "file.upload.complete",
  "payload": {
    "session_id": "session-abc",
    "file_hash": "sha256:abc123..."
  },
  "timestamp": 1703520030
}
```

---

## Error Handling

### Connection Errors

**Authentication Failed**:
```json
HTTP 401 Unauthorized
{
  "error": "Invalid authentication token"
}
```

**Missing Token**:
```json
HTTP 401 Unauthorized
{
  "error": "Missing authentication token"
}
```

### Message Errors

**Invalid Message Format**:
The server will log the error and continue processing other messages.

**Upload Session Not Found**:
```json
{
  "type": "file.upload.error",
  "payload": {
    "session_id": "session-abc",
    "error": "Upload session not found",
    "code": "SESSION_NOT_FOUND"
  },
  "timestamp": 1703520000
}
```

### Disconnection Handling

Clients should implement reconnection logic with exponential backoff:

```javascript
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
const baseDelay = 1000;

function connect() {
  const ws = new WebSocket(`ws://localhost:8080/api/ws?token=${token}`);

  ws.onclose = () => {
    if (reconnectAttempts < maxReconnectAttempts) {
      const delay = baseDelay * Math.pow(2, reconnectAttempts);
      setTimeout(connect, delay);
      reconnectAttempts++;
    }
  };

  ws.onopen = () => {
    reconnectAttempts = 0;
  };
}
```

---

## Best Practices

### Client Implementation

1. **Handle Partial Results**: Service search returns streaming results. Process each partial response as it arrives
2. **Implement Reconnection**: Network interruptions happen. Implement exponential backoff reconnection
3. **Validate Messages**: Always validate message structure before processing
4. **Buffer Management**: For file uploads, implement proper chunking and buffering
5. **Memory Management**: Large broadcasts (services, workflows) can be memory-intensive. Clean up old data

### Performance Considerations

1. **Broadcast Intervals**: Adjust UI update frequency to match broadcast intervals
2. **File Upload Chunk Size**: Default 100KB chunks work well for most scenarios. Adjust based on network conditions
3. **Connection Pooling**: Reuse WebSocket connections. Don't create multiple connections per page
4. **Message Queuing**: If processing is slow, queue messages to avoid blocking the WebSocket thread

### Security Considerations

1. **JWT Token Management**:
   - Store tokens securely
   - Refresh tokens before expiration
   - Never log or expose tokens
2. **Input Validation**: Validate all client-to-server messages
3. **Rate Limiting**: The server may rate-limit connections or messages
4. **TLS/SSL**: Always use WSS (WebSocket Secure) in production

---

## Client Libraries

### JavaScript/TypeScript Example

```typescript
interface WebSocketMessage {
  type: string;
  payload: any;
  timestamp: number;
}

class RemoteNetworkWebSocket {
  private ws: WebSocket | null = null;
  private token: string;
  private handlers: Map<string, (payload: any) => void> = new Map();

  constructor(token: string) {
    this.token = token;
  }

  connect(url: string = 'ws://localhost:8080/api/ws') {
    this.ws = new WebSocket(`${url}?token=${this.token}`);

    this.ws.onmessage = (event) => {
      const message: WebSocketMessage = JSON.parse(event.data);
      const handler = this.handlers.get(message.type);
      if (handler) {
        handler(message.payload);
      }
    };
  }

  on(messageType: string, handler: (payload: any) => void) {
    this.handlers.set(messageType, handler);
  }

  send(type: string, payload: any) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      const message: WebSocketMessage = {
        type,
        payload,
        timestamp: Math.floor(Date.now() / 1000)
      };
      this.ws.send(JSON.stringify(message));
    }
  }

  // Service search
  searchServices(query: string, serviceType: string = '', activeOnly: boolean = true) {
    this.send('service.search.request', {
      query,
      service_type: serviceType,
      peer_ids: [],
      active_only: activeOnly
    });
  }

  disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}

// Usage
const wsClient = new RemoteNetworkWebSocket('your-jwt-token');
wsClient.connect();

wsClient.on('connected', (payload) => {
  console.log('Connected:', payload.peer_id);
});

wsClient.on('node.status', (payload) => {
  console.log('Node status:', payload);
});

wsClient.on('service.search.response', (payload) => {
  if (!payload.complete) {
    console.log('Found services:', payload.services);
  } else {
    console.log('Search complete');
  }
});

// Search for services
wsClient.searchServices('image', 'DOCKER');
```

---

## Appendix

### Message Type Quick Reference

| Message Type | Direction | Purpose |
|-------------|-----------|---------|
| `connected` | S→C | Connection confirmation |
| `ping` | Bidirectional | Keep-alive ping |
| `pong` | Bidirectional | Keep-alive pong |
| `error` | S→C | General error |
| `node.status` | S→C | Node status update (5s) |
| `peers.updated` | S→C | Peer list update (10s) |
| `blacklist.updated` | S→C | Blacklist update (30s) |
| `relay.sessions` | S→C | Relay sessions (5s) |
| `relay.candidates` | S→C | Relay candidates (5s) |
| `services.updated` | S→C | Service list update (15s) |
| `service.search.request` | C→S | Search for services |
| `service.search.response` | S→C | Service search results |
| `peer.capabilities.updated` | S→C | Peer capabilities update |
| `workflows.updated` | S→C | Workflow list update (15s) |
| `execution.updated` | S→C | Workflow execution update |
| `job.status.updated` | S→C | Job status update |
| `docker.pull.progress` | S→C | Docker pull progress |
| `docker.build.output` | S→C | Docker build output |
| `docker.operation.start` | S→C | Docker operation started |
| `docker.operation.progress` | S→C | Docker operation progress |
| `docker.operation.complete` | S→C | Docker operation complete |
| `docker.operation.error` | S→C | Docker operation failed |
| `standalone.operation.start` | S→C | Standalone op started |
| `standalone.operation.progress` | S→C | Standalone op progress |
| `standalone.operation.complete` | S→C | Standalone op complete |
| `standalone.operation.error` | S→C | Standalone op failed |
| `file.upload.start` | C→S | Start file upload |
| `file.upload.chunk` | C→S | Upload file chunk |
| `file.upload.pause` | C→S | Pause file upload |
| `file.upload.resume` | C→S | Resume file upload |
| `file.upload.progress` | S→C | Upload progress update |
| `file.upload.complete` | S→C | Upload complete |
| `file.upload.error` | S→C | Upload failed |

**Direction Legend**:
- S→C: Server to Client
- C→S: Client to Server
- Bidirectional: Both directions

**Broadcast Intervals**: Numbers in parentheses indicate automatic broadcast interval

---

## Version History

- **v1.0** (2024-12-25): Initial documentation
  - Connection setup and authentication
  - All message types documented
  - Event flow examples
  - Client library examples
  - Best practices and security considerations
