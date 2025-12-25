# Docker Dependencies Guide

## Overview

The Remote Network node includes a Docker dependency management system that automatically checks for and helps install Docker-related dependencies required for running DOCKER services.

## How It Works

### Automatic Dependency Checking

The node **automatically checks Docker dependencies on every startup**:

**First Startup:**
```bash
$ ./remote-network start

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🔍 Checking Docker dependencies...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
⚠️  Missing dependencies: Docker, Docker Compose

Docker services require these dependencies to function.

Would you like to install missing dependencies now? (yes/no): yes

[Installation proceeds...]
✅ Docker dependencies installed and running successfully!
```

If you answer **"no"**, the node continues to start but **Docker services will be disabled in the UI**.

**Subsequent Startups:**
```bash
$ ./remote-network start

✅ All Docker dependencies are installed
✅ Docker services are running
```

The node automatically starts Docker/Colima if not already running.

### Dependencies Checked

The system checks for:

- **Docker CLI** (`docker` command)
- **Docker Compose** (`docker compose` subcommand)
- **Docker Buildx** (`docker buildx` subcommand)
- **Colima** (macOS only - Docker runtime for macOS)

### What Happens If Dependencies Are Missing

If dependencies are missing and you choose **not to install** them:

```bash
⚠️  Missing dependencies: Docker, Docker Compose

Would you like to install missing dependencies now? (yes/no): no

⚠️  Skipping dependency installation.
Docker services will be disabled.
```

**The node continues to start successfully**, but:
- DOCKER services will be **disabled** in the UI
- Only DATA and STANDALONE service types will be available
- The node operates normally for non-Docker services

You can install dependencies later by running:
```bash
./remote-network start --install-dependencies
```

## Manual Dependency Installation

### Force Installation Prompt

To force the dependency installation prompt (useful if you skipped it before):

```bash
./remote-network start --install-dependencies
# or shorthand
./remote-network start -i
```

This will:
1. Check for missing dependencies
2. Prompt you to install them (even if you said "no" before)
3. Guide you through OS-specific installation
4. Start Docker/Colima if needed

**Note:** Dependencies are checked on **every startup**, but the installation prompt only appears:
- On first startup (if dependencies missing)
- When using the `--install-dependencies` flag

### Installation Per Operating System

#### Linux (Ubuntu/Debian)

```bash
sudo apt update
sudo apt install -y docker.io docker-compose-plugin docker-buildx-plugin
sudo systemctl start docker
sudo usermod -aG docker $USER
```

After installation, **log out and log back in** for group changes to take effect.

#### macOS

The system uses **Colima** (lightweight Docker alternative for macOS) instead of Docker Desktop:

```bash
# Install via Homebrew
brew install colima docker docker-compose docker-buildx

# Create CLI plugin symlinks
mkdir -p ~/.docker/cli-plugins
ln -sfn $(brew --prefix)/opt/docker-compose/bin/docker-compose ~/.docker/cli-plugins/docker-compose
ln -sfn $(brew --prefix)/opt/docker-buildx/bin/docker-buildx ~/.docker/cli-plugins/docker-buildx

# Start Colima
colima start
```

**Environment Variables:**
Add these to your shell profile (`~/.zshrc` or `~/.bashrc`):

```bash
export DOCKER_HOST="unix://$HOME/.colima/docker.sock"
export DOCKER_API_VERSION=1.42  # May vary
```

#### Windows

Download and install **Docker Desktop**:
- https://www.docker.com/products/docker-desktop/

**Important Requirements:**
1. **Enable Developer Mode** (recommended):
   ```powershell
   # Run in PowerShell as Administrator
   reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock" /t REG_DWORD /f /v "AllowDevelopmentWithoutDevLicense" /d "1"
   ```
   OR: Settings → Privacy & Security → For Developers → Enable Developer Mode

2. **Restart your computer** after installation

## UI Behavior

### Service Type Filtering

The Web UI automatically queries the node's capabilities and adjusts the available service types:

- **If Docker is available:** DATA, DOCKER, and STANDALONE service types are shown
- **If Docker is not available:** Only DATA and STANDALONE are shown

The UI fetches this information from the `/api/node/capabilities` endpoint on component load.

### Checking Docker Availability

You can check Docker availability via API:

```bash
curl -X GET http://localhost:30069/api/node/capabilities \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

Response:
```json
{
  "docker_available": true,
  "docker_version": "24.0.7",
  "colima_status": "running"  # macOS only
}
```

## Configuration

### Config Keys

The system uses two config keys (stored in `~/.config/remote-network/configs` on Linux):

- `dependencies_checked` (boolean) - Whether installation prompt has been shown
- `docker_dependencies_available` (boolean) - Current Docker availability status (updated on every startup)

### Forcing Installation Prompt Again

To force the installation prompt to appear again:

**Option 1: Use the flag**
```bash
./remote-network start --install-dependencies
```

**Option 2: Edit config manually**
1. Edit config file: `~/.config/remote-network/configs` (Linux) or `~/Library/Application Support/remote-network/configs` (macOS)
2. Find and set to `false`:
   ```ini
   dependencies_checked = false
   ```
3. Restart the node

## Troubleshooting

### "Docker is not responsive"

**Symptoms:**
- Dependencies are installed but Docker doesn't respond

**Solutions:**

**Linux:**
```bash
# Check Docker service status
sudo systemctl status docker

# Start Docker if not running
sudo systemctl start docker

# Enable Docker to start on boot
sudo systemctl enable docker
```

**macOS:**
```bash
# Check Colima status
colima status

# Start Colima if not running
colima start

# Check Docker connection
docker ps
```

**Windows:**
- Ensure Docker Desktop is running (check system tray)
- Restart Docker Desktop from the system tray menu

### "User is not in docker group" (Linux)

**Solution:**
```bash
# Add user to docker group
sudo usermod -aG docker $USER

# Apply changes (choose one):
# Option 1: Log out and log back in
# Option 2: Run new shell with group
newgrp docker
```

### "Symlinks not supported" (Windows)

**Solution:**
Enable Developer Mode (see Windows installation section above)

### Docker Version Mismatch (macOS)

**Symptoms:**
```
Error: Maximum supported API version is X.XX
```

**Solution:**
Add to shell profile:
```bash
export DOCKER_API_VERSION=1.42  # Use version from error message
```

### Colima Not Starting (macOS)

**Solutions:**
```bash
# Check Colima logs
colima logs

# Reset Colima
colima delete
colima start

# Specify resources
colima start --cpu 4 --memory 8
```

## Best Practices

### Development Environment

1. **Always use `--check-dependencies` flag** when setting up a new development machine
2. **Verify installation** before trying to add DOCKER services:
   ```bash
   docker ps
   docker compose version
   docker buildx version
   ```

### Production/Server Environment

1. **Install dependencies before first node start**
2. **Use automated installation** via package managers
3. **Set up systemd service** (Linux) to auto-start Docker:
   ```bash
   sudo systemctl enable docker
   ```

### Continuous Integration

For automated testing/deployment:

```bash
# Install dependencies non-interactively
./scripts/install-docker-deps.sh  # Create this script

# Start node (will skip prompts if dependencies installed)
./remote-network start
```

## Architecture Notes

### DependencyManager Implementation

**File:** `internal/dependencies/dependencies.go`

The dependency management system is implemented through the `DependencyManager` struct:

```go
DependencyManager
├─ Platform Detection
│   ├─ Detects OS (Linux, macOS, Windows)
│   ├─ Identifies distribution (Debian/Ubuntu, Fedora/RHEL, etc.)
│   └─ Determines package manager (apt, dnf, brew, choco)
│
├─ Dependency Checking
│   ├─ Docker CLI existence (`docker version`)
│   ├─ Docker Compose subcommand (`docker compose version`)
│   ├─ Docker Buildx subcommand (`docker buildx version`)
│   ├─ Colima (macOS) - (`colima version`)
│   └─ Service running status (Docker daemon/Colima)
│
├─ Installation Management
│   ├─ Platform-specific installation commands
│   ├─ User prompts and confirmation
│   ├─ Execution and progress tracking
│   └─ Post-install verification
│
└─ Service Startup
    ├─ Docker daemon start (Linux)
    ├─ Colima start (macOS)
    └─ Docker Desktop detection (all platforms)
```

**Initialization** (server.go:138):
```go
depManager := dependencies.NewDependencyManager(config, logger)
```

**Key Methods:**
- `CheckDependencies()` - Verify all required dependencies
- `InstallMissingDependencies()` - Platform-specific installation
- `StartDockerServices()` - Start Docker/Colima if not running
- `dockerSubcommandExists(cmd)` - Check for Docker subcommands
- Platform-specific installers: `installLinux()`, `installDarwin()`, `installWindows()`

**Platform-Specific Files:**
- `dependencies/linux.go` - apt/dnf package installation
- `dependencies/darwin.go` - Homebrew/Colima installation
- `dependencies/windows.go` - Chocolatey/scoop installation

**Configuration Storage:**
```ini
[dependencies]
dependencies_checked = true
docker_dependencies_available = true
```

### Dependency Flow

**First Startup:**
1. **Node Startup** → Check `dependencies_checked` config (false)
2. **Run Full Check** → Prompt user to install if missing
3. **Save result** → Store `dependencies_checked = true` and `docker_dependencies_available`
4. **Start Services** → Initialize Docker/Colima if available

**Subsequent Startups:**
1. **Node Startup** → Quick dependency check
2. **If available** → Start Docker/Colima services automatically
3. **Update status** → Update `docker_dependencies_available` in config
4. **Print status** → Display availability to user

**API & UI:**
1. **API Endpoint** → `/api/node/capabilities` returns current availability
2. **UI** → Fetches capabilities, filters service types dynamically

### Non-Blocking Design

The dependency system is **non-blocking**:
- Missing dependencies do not prevent node startup
- Node operates normally with DATA/STANDALONE services
- DOCKER services can be enabled later by installing dependencies

## FAQ

**Q: Do I need Docker to use the Remote Network node?**
A: No, Docker is only required for DOCKER service types. DATA and STANDALONE services work without Docker.

**Q: Will the node prompt me every time if Docker is missing?**
A: No. The node checks dependencies on every startup but only prompts for installation:
   - On first startup
   - When using `--install-dependencies` flag

   If you skip installation, the node continues without Docker services enabled.

**Q: What happens if I say "no" to installing dependencies?**
A: The node continues to start normally, but DOCKER services will be disabled in the UI. Only DATA and STANDALONE service types will be available.

**Q: Can I use Docker Desktop on macOS instead of Colima?**
A: Yes, but Colima is recommended for better performance and lower resource usage.

**Q: How do I enable DOCKER services after installing Docker?**
A: Simply restart the node. It will detect the newly installed dependencies and start Docker services automatically.

**Q: Does the node automatically start Docker/Colima on every startup?**
A: Yes, if dependencies are installed, the node automatically starts Docker/Colima services if they're not already running.

**Q: Does this work in Docker containers?**
A: For Docker-in-Docker scenarios, mount the Docker socket or use Docker Desktop with WSL2 (Windows).

---

**Last Updated:** 2025-11-17
