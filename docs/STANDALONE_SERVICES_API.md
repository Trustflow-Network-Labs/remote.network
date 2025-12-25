# Standalone Services API Documentation

## Overview

Standalone Services allow you to execute native executables, scripts, and compiled programs on remote peers without requiring Docker containerization. This is perfect for:

- Running Python/Node.js/Ruby scripts
- Executing compiled binaries (Go, Rust, C++)
- Running shell scripts
- Platform-specific applications

## Table of Contents

1. [Creating Services](#creating-services)
2. [API Endpoints](#api-endpoints)
3. [Service Configuration](#service-configuration)
4. [System Capabilities](#system-capabilities)
5. [Examples](#examples)
6. [Testing](#testing)

---

## Creating Services

There are three ways to create a standalone service:

### 1. From Local Executable

Reference an executable already on the peer's filesystem.

**Endpoint:** `POST /api/services/standalone/from-local`

**Request Body:**
```json
{
  "service_name": "python-processor",
  "executable_path": "/usr/bin/python3",
  "arguments": ["-u", "script.py", "--verbose"],
  "working_directory": "/opt/myapp",
  "environment_variables": {
    "PYTHONUNBUFFERED": "1",
    "API_KEY": "your-secret-key"
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
      "description": "Input data directory"
    },
    {
      "interface_type": "MOUNT",
      "path": "/data/output",
      "mount_function": "OUTPUT",
      "description": "Output results directory"
    }
  ]
}
```

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 123,
    "service_type": "STANDALONE",
    "name": "python-processor",
    "description": "Python data processor",
    "status": "ACTIVE",
    "capabilities": { ... },
    "interfaces": [ ... ]
  },
  "suggested_interfaces": [
    {
      "interface_type": "STDIN",
      "description": "Standard input stream for the process"
    },
    {
      "interface_type": "STDOUT",
      "description": "Standard output stream from the process"
    },
    {
      "interface_type": "STDERR",
      "description": "Standard error stream from the process"
    },
    {
      "interface_type": "LOGS",
      "description": "Combined logs from the process execution"
    }
  ]
}
```

---

### 2. From Upload (via WebSocket)

Upload an executable file to the peer.

**Process:**

1. **Connect to WebSocket:** `ws://host:port/ws`

2. **Authenticate:** Send JWT token

3. **Initiate Upload:**
```json
{
  "type": "FILE_UPLOAD_START",
  "payload": {
    "service_id": 0,
    "upload_group_id": "unique-uuid",
    "filename": "myapp",
    "file_path": "myapp",
    "file_index": 0,
    "total_files": 1,
    "total_size": 1048576,
    "total_chunks": 10,
    "chunk_size": 104857
  }
}
```

4. **Send Chunks:**
```json
{
  "type": "FILE_UPLOAD_CHUNK",
  "payload": {
    "session_id": "session-uuid",
    "chunk_index": 0,
    "data": "base64-encoded-chunk-data"
  }
}
```

5. **Complete Upload:**
```json
{
  "type": "FILE_UPLOAD_END",
  "payload": {
    "session_id": "session-uuid"
  }
}
```

6. **Server Response:**
```json
{
  "type": "FILE_UPLOAD_COMPLETE",
  "payload": {
    "upload_group_id": "unique-uuid",
    "service_id": 123,
    "files": ["myapp"],
    "success": true
  }
}
```

The uploaded file is stored at: `{dataDir}/standalone_services/{service_name}/myapp`

**Implementation Details:**
- **Handler**: `internal/api/websocket/file_upload_handler.go`
- **Temp Directory**: OS-specific temp dir + `/remote-network-uploads`
- **Session Management**: Active sessions tracked with unique session IDs
- **Chunk Processing**: Chunks written sequentially to temp file
- **File Permissions**: Uploaded executables set to `0755` (executable)
- **Upload Completion**: Callback triggers service finalization
- **Error Handling**: Session cleanup on errors, partial upload recovery

**Configuration:**
- `upload_temp_dir`: Override default temp directory location (optional)
- Default chunk size: 1MB (1048576 bytes)
- Maximum file size: Configurable (no hardcoded limit)

---

### 3. From Git Repository

Clone and optionally build from a Git repository.

**Endpoint:** `POST /api/services/standalone/from-git`

**Request Body:**
```json
{
  "service_name": "rust-app",
  "repo_url": "https://github.com/user/rust-app.git",
  "branch": "main",
  "executable_path": "target/release/myapp",
  "build_command": "cargo build --release",
  "arguments": ["--config", "prod.toml"],
  "working_directory": "",
  "environment_variables": {
    "RUST_LOG": "info"
  },
  "timeout_seconds": 300,
  "username": "git-username",
  "password": "git-token",
  "description": "Rust application from Git",
  "capabilities": {
    "platform": "linux",
    "architectures": ["amd64", "arm64"]
  }
}
```

**Response:** Same as "From Local" response

---

## API Endpoints

### Get Service Details

**Endpoint:** `GET /api/services/standalone/:id/details`

**Response:**
```json
{
  "success": true,
  "service": {
    "id": 123,
    "service_type": "STANDALONE",
    "name": "python-processor",
    "description": "Python data processor",
    "status": "ACTIVE",
    "interfaces": [ ... ]
  },
  "details": {
    "id": 1,
    "service_id": 123,
    "executable_path": "/usr/bin/python3",
    "arguments": "[\"script.py\", \"--verbose\"]",
    "working_directory": "/opt/myapp",
    "environment_variables": "{\"API_KEY\":\"secret\"}",
    "timeout_seconds": 600,
    "run_as_user": "",
    "source": "local",
    "git_repo_url": "",
    "git_commit_hash": "",
    "upload_hash": ""
  }
}
```

---

### Delete Service

**Endpoint:** `DELETE /api/services/standalone/:id`

**Response:**
```json
{
  "success": true,
  "message": "Service deleted successfully"
}
```

---

## Service Configuration

### Required Fields

- `service_name`: Unique name for the service
- `executable_path`: For local/upload - absolute path; For git - relative path within repo

### Optional Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `arguments` | array | `[]` | Command-line arguments |
| `working_directory` | string | executable's directory | Working directory for execution |
| `environment_variables` | object | `{}` | Environment variables |
| `timeout_seconds` | int | `600` | Execution timeout (10 minutes) |
| `run_as_user` | string | current user | User:group for execution (Unix only) |
| `description` | string | `""` | Service description |
| `capabilities` | object | auto-detected | Platform requirements |

### Capabilities Schema

```json
{
  "platform": "linux",              // "linux", "darwin", "windows", "any"
  "architectures": ["amd64"],       // ["amd64", "arm64", "386", "arm"]
  "min_memory_mb": 512,             // Minimum RAM required
  "min_cpu_cores": 1,               // Minimum CPU cores
  "min_disk_mb": 100,               // Minimum disk space
  "requires_gpu": false,            // GPU requirement
  "gpu_vendor": "nvidia",           // "nvidia", "amd", "intel", "any"
  "min_gpu_memory_mb": 4096         // Minimum GPU memory
}
```

---

## System Capabilities

The platform automatically gathers and publishes system capabilities for each peer:

```json
{
  "platform": "linux",
  "architecture": "amd64",
  "kernel_version": "5.15.0",
  "cpu_model": "Intel Core i7",
  "cpu_cores": 8,
  "cpu_threads": 16,
  "total_memory_mb": 16384,
  "available_memory_mb": 8192,
  "total_disk_mb": 512000,
  "available_disk_mb": 256000,
  "gpus": [
    {
      "index": 0,
      "name": "NVIDIA GeForce RTX 3080",
      "vendor": "nvidia",
      "memory_mb": 10240,
      "uuid": "GPU-...",
      "driver_version": "525.60.11"
    }
  ],
  "has_docker": true,
  "docker_version": "24.0.7",
  "has_python": true,
  "python_version": "3.11.0"
}
```

Jobs are automatically scheduled only on compatible peers based on these capabilities.

---

## Examples

### Example 1: Simple Python Script

```bash
curl -X POST http://localhost:30069/api/services/standalone/from-local \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "hello-world",
    "executable_path": "/usr/bin/python3",
    "arguments": ["-c", "print(\"Hello from standalone service!\")"],
    "description": "Simple hello world Python script"
  }'
```

---

### Example 2: Data Processing with MOUNT Interfaces

```json
{
  "service_name": "csv-processor",
  "executable_path": "/usr/bin/python3",
  "arguments": ["process_csv.py"],
  "working_directory": "/opt/processor",
  "environment_variables": {
    "PYTHONUNBUFFERED": "1"
  },
  "custom_interfaces": [
    {
      "interface_type": "MOUNT",
      "path": "/data/input",
      "mount_function": "INPUT",
      "description": "CSV files to process"
    },
    {
      "interface_type": "MOUNT",
      "path": "/data/output",
      "mount_function": "OUTPUT",
      "description": "Processed results"
    }
  ]
}
```

---

### Example 3: Rust Binary from Git

```json
{
  "service_name": "image-optimizer",
  "repo_url": "https://github.com/user/image-optimizer.git",
  "branch": "main",
  "executable_path": "target/release/image-optimizer",
  "build_command": "cargo build --release",
  "arguments": ["--quality", "85"],
  "description": "High-performance image optimizer",
  "capabilities": {
    "platform": "linux",
    "architectures": ["amd64"],
    "min_memory_mb": 1024,
    "min_cpu_cores": 2
  }
}
```

---

### Example 4: GPU-Accelerated Processing

```json
{
  "service_name": "ml-inference",
  "executable_path": "/opt/ml/inference",
  "arguments": ["--model", "/models/resnet50.onnx"],
  "environment_variables": {
    "CUDA_VISIBLE_DEVICES": "0"
  },
  "capabilities": {
    "platform": "linux",
    "architectures": ["amd64"],
    "min_memory_mb": 8192,
    "min_cpu_cores": 4,
    "requires_gpu": true,
    "gpu_vendor": "nvidia",
    "min_gpu_memory_mb": 8192
  }
}
```

---

## Testing

### 1. Create a Test Script

Create `test_script.sh`:
```bash
#!/bin/bash
echo "Script started at $(date)"
echo "Arguments: $@"
echo "Working directory: $(pwd)"
echo "Environment: USER=$USER"
sleep 2
echo "Script completed successfully"
exit 0
```

Make it executable:
```bash
chmod +x test_script.sh
```

### 2. Create the Service

```bash
curl -X POST http://localhost:30069/api/services/standalone/from-local \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "test-script",
    "executable_path": "/path/to/test_script.sh",
    "arguments": ["arg1", "arg2"],
    "description": "Test standalone service"
  }'
```

### 3. Create a Workflow

Use the service in a workflow (via UI or API) and execute it. Check the outputs in:
```
{dataDir}/workflows/{orchestrator_peer}/{workflow_job_id}/jobs/{executor_peer}/{job_execution_id}/output/
```

Output files:
- `stdout.txt` - Standard output
- `stderr.txt` - Standard error
- `logs.txt` - Combined logs

---

## Security Considerations

### 1. File Permissions

- Uploaded executables are stored with `0755` permissions
- Working directories are created with `0755` permissions
- Output directories use `0755` permissions

### 2. User Switching

The `run_as_user` field allows specifying a user:group for execution:
```json
{
  "run_as_user": "appuser:appgroup"
}
```

**Note:** Currently requires root privileges and is logged as a warning. Full implementation pending.

### 3. Input Validation

- Arguments are sanitized to remove empty/whitespace-only entries
- Paths are validated for existence and accessibility
- Timeouts are enforced to prevent runaway processes

### 4. Isolation

Standalone services run with the same privileges as the node process. For better isolation:
- Consider running the node as a restricted user
- Use Docker services for untrusted code
- Implement additional sandboxing if needed

---

## Troubleshooting

### Service Creation Fails

**Error:** "invalid executable: permission denied"
- Ensure executable has execute permissions
- Check file ownership
- Verify path is absolute

**Error:** "failed to clone repository"
- Check Git credentials
- Verify repository URL
- Ensure network connectivity

### Execution Fails

**Error:** "Process exited with code 127"
- Executable not found
- Check `executable_path` is correct
- Verify dependencies are installed

**Error:** "Process execution timed out"
- Increase `timeout_seconds`
- Optimize the executable
- Check for infinite loops

### Platform Compatibility

**Error:** "incompatible platform"
- Check `capabilities.platform` matches target peer
- Verify `architectures` includes target architecture
- Ensure resource requirements are met

---

## Related Documentation

- [Workflow Creation Guide](WORKFLOWS.md)
- [Job Execution Flow](JOB_EXECUTION.md)
- [Docker Services](DOCKER_SERVICES.md)
- [WebSocket API](WEBSOCKET_API.md)

---

**Last Updated:** 2025-01-17
**Version:** 1.0
