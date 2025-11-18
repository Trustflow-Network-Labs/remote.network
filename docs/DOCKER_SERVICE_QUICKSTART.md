# Docker Service Quick Start Guide

## 🚀 Get Started in 5 Minutes

### Prerequisites
- Docker installed and running
- Remote network node running
- JWT authentication token

---

## 1️⃣ Create Your First Docker Service

### Option A: Pull from Docker Hub

```bash
curl -X POST https://localhost:30069/api/services/docker/from-registry \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "hello-world",
    "image_name": "hello-world",
    "image_tag": "latest",
    "description": "Hello World Container"
  }'
```

**Response**:
```json
{
  "success": true,
  "service": {
    "id": 1,
    "name": "hello-world",
    "service_type": "DOCKER",
    ...
  },
  "suggested_interfaces": [
    {
      "interface_type": "STDOUT",
      "description": "Container outputs to STDOUT"
    }
  ]
}
```

### Option B: Build from Git Repository

```bash
curl -X POST https://localhost:30069/api/services/docker/from-git \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "my-app",
    "repo_url": "https://github.com/username/repo",
    "branch": "main",
    "description": "My Application"
  }'
```

For private repos, add authentication:
```json
{
  "service_name": "my-private-app",
  "repo_url": "https://github.com/username/private-repo",
  "branch": "main",
  "password": "github_pat_YOUR_PERSONAL_ACCESS_TOKEN",
  "description": "Private Application"
}
```

### Option C: Build from Local Directory

```bash
curl -X POST https://localhost:30069/api/services/docker/from-local \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "local-app",
    "local_path": "/absolute/path/to/your/project",
    "description": "Local Development App"
  }'
```

---

## 2️⃣ List Your Docker Services

```bash
curl -X GET https://localhost:30069/api/services \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

Filter for Docker services only:
```bash
curl -X GET https://localhost:30069/api/services \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  | jq '.services[] | select(.service_type == "DOCKER")'
```

---

## 3️⃣ View Service Details

```bash
curl -X GET https://localhost:30069/api/services/docker/1/details \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

**Response**:
```json
{
  "details": {
    "id": 1,
    "service_id": 1,
    "image_name": "hello-world",
    "image_tag": "latest",
    "source": "registry",
    "created_at": "2025-01-17T10:00:00Z"
  }
}
```

---

## 4️⃣ Execute Service in Workflow

Docker services are executed through workflows. Here's how:

### Create a Workflow with Docker Service

```bash
curl -X POST https://localhost:30069/api/workflows \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Docker Workflow",
    "description": "Run Docker service",
    "jobs": [
      {
        "name": "docker-job",
        "service_id": 1,
        "dependencies": []
      }
    ]
  }'
```

### Execute the Workflow

```bash
curl -X POST https://localhost:30069/api/workflows/1/execute \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

### Monitor Job Status

```bash
curl -X GET https://localhost:30069/api/workflows/1/executions/1 \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

---

## 5️⃣ Advanced: Custom Interfaces

Customize interfaces during service creation:

```bash
curl -X POST https://localhost:30069/api/services/docker/from-registry \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "nginx-custom",
    "image_name": "nginx",
    "image_tag": "latest",
    "description": "NGINX with custom mounts",
    "custom_interfaces": [
      {
        "interface_type": "STDOUT",
        "path": ""
      },
      {
        "interface_type": "MOUNT",
        "path": "/usr/share/nginx/html"
      },
      {
        "interface_type": "MOUNT",
        "path": "/etc/nginx/conf.d"
      }
    ]
  }'
```

---

## 🐛 Troubleshooting

### Check Docker is Running

```bash
docker ps
```

### View Node Logs

```bash
tail -f ~/.remote-network/logs/app.log
```

### Check Docker Service Status

```bash
curl -X GET https://localhost:30069/api/services/1 \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  | jq '.service.status'
```

### Common Issues

**Issue**: `Docker dependencies not met`
- **Solution**: Run `docker info` to verify Docker is running
- Check dependency manager initialized correctly

**Issue**: `Image not found`
- **Solution**: Ensure image name is correct and accessible
- Check Docker Hub or registry credentials

**Issue**: `Git repository validation failed`
- **Solution**: Verify repo URL is correct
- For private repos, ensure token has correct permissions

**Issue**: `Build failed`
- **Solution**: Check Dockerfile syntax
- Verify build context has all required files

---

## 📊 Understanding Interfaces

### STDIN
- Allows sending input to container
- Useful for interactive applications
- Example: Configuration data, user input

### STDOUT
- Captures container output
- Automatically suggested for all containers
- Used for logs and results

### MOUNT
- Persistent storage
- Data exchange between host and container
- Example: `/data`, `/config`, `/output`

---

## 🎯 Common Use Cases

### 1. Data Processing Pipeline

```bash
# Create service from image
curl -X POST .../from-registry \
  -d '{
    "service_name": "data-processor",
    "image_name": "python:3.9",
    "custom_interfaces": [
      {"interface_type": "MOUNT", "path": "/data"},
      {"interface_type": "STDOUT"}
    ]
  }'
```

### 2. Web Application

```bash
# Create from Git repo
curl -X POST .../from-git \
  -d '{
    "service_name": "web-app",
    "repo_url": "https://github.com/user/webapp",
    "branch": "production"
  }'
```

### 3. Custom Script Execution

```bash
# Create from local directory
curl -X POST .../from-local \
  -d '{
    "service_name": "custom-script",
    "local_path": "/path/to/script-dir",
    "custom_interfaces": [
      {"interface_type": "STDIN"},
      {"interface_type": "STDOUT"},
      {"interface_type": "MOUNT", "path": "/output"}
    ]
  }'
```

---

## 🔐 Authentication Tips

### Get JWT Token

```bash
# 1. Get challenge
CHALLENGE=$(curl -X GET https://localhost:30069/api/auth/challenge | jq -r '.challenge')

# 2. Sign challenge (using your node's keypair)
# This would be done by your application

# 3. Authenticate
TOKEN=$(curl -X POST https://localhost:30069/api/auth/ed25519 \
  -d '{"challenge": "'$CHALLENGE'", "signature": "YOUR_SIGNATURE"}' \
  | jq -r '.token')

# 4. Use token
curl -H "Authorization: Bearer $TOKEN" https://localhost:30069/api/services
```

---

## 📝 Docker Compose Example

### Sample docker-compose.yml

```yaml
version: '3'
services:
  app:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - NODE_ENV=production
```

### Create Service from Compose

```bash
curl -X POST https://localhost:30069/api/services/docker/from-local \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "service_name": "compose-app",
    "local_path": "/path/to/compose-project",
    "description": "Multi-service application"
  }'
```

The system will:
1. Detect `docker-compose.yml`
2. Build all services defined
3. Treat as single service unit
4. Auto-detect interfaces from compose config

---

## 📚 Next Steps

1. **Explore the API**: Try all three creation methods
2. **Create Workflows**: Chain Docker services together
3. **Monitor Execution**: Watch container logs and job status
4. **Customize**: Define custom interfaces for your needs

For detailed documentation, see:
- `DOCKER_SERVICE_IMPLEMENTATION.md` - Full technical details
- `DOCKER_IMPLEMENTATION_COMPLETE.md` - Implementation summary

---

## 💡 Tips

1. **Image Tags**: Always specify tags for reproducibility (`nginx:1.21` vs `nginx:latest`)
2. **Git Branches**: Pin to specific branches or tags for stability
3. **Local Builds**: Use absolute paths to avoid confusion
4. **Interfaces**: Start with suggested, customize as needed
5. **Testing**: Test services individually before adding to workflows

---

**Need Help?**
- Check node logs: `~/.remote-network/logs/app.log`
- View Docker logs: Docker pull/build logs in `~/.remote-network/data/docker_logs/`
- API documentation: See implementation docs

**Happy Dockering! 🐳**
