package system

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// SystemCapabilities represents the hardware and software capabilities of the current system
type SystemCapabilities struct {
	Platform          string            `json:"platform" bencode:"platform"`
	Architecture      string            `json:"architecture" bencode:"architecture"`
	KernelVersion     string            `json:"kernel_version" bencode:"kernel_version"`
	CPUModel          string            `json:"cpu_model" bencode:"cpu_model"`
	CPUCores          int               `json:"cpu_cores" bencode:"cpu_cores"`
	CPUThreads        int               `json:"cpu_threads" bencode:"cpu_threads"`
	TotalMemoryMB     int64             `json:"total_memory_mb" bencode:"total_memory_mb"`
	AvailableMemoryMB int64             `json:"available_memory_mb" bencode:"available_memory_mb"`
	TotalDiskMB       int64             `json:"total_disk_mb" bencode:"total_disk_mb"`
	AvailableDiskMB   int64             `json:"available_disk_mb" bencode:"available_disk_mb"`
	GPUs              []GPUInfo         `json:"gpus,omitempty" bencode:"gpus,omitempty"`
	HasDocker         bool              `json:"has_docker" bencode:"has_docker"`
	DockerVersion     string            `json:"docker_version,omitempty" bencode:"docker_version,omitempty"`
	HasPython         bool              `json:"has_python" bencode:"has_python"`
	PythonVersion     string            `json:"python_version,omitempty" bencode:"python_version,omitempty"`
	Wallets           map[string]string `json:"wallets,omitempty" bencode:"wallets,omitempty"` // Map of payment network -> wallet address (e.g., {"eip155:84532": "0x123...", "solana:devnet": "ABC..."})
}

// GPUInfo represents information about a GPU
type GPUInfo struct {
	Index         int    `json:"index" bencode:"index"`
	Name          string `json:"name" bencode:"name"`
	Vendor        string `json:"vendor" bencode:"vendor"`
	MemoryMB      int64  `json:"memory_mb" bencode:"memory_mb"`
	UUID          string `json:"uuid,omitempty" bencode:"uuid,omitempty"`
	DriverVersion string `json:"driver_version,omitempty" bencode:"driver_version,omitempty"`
}

// CapabilitySummary is a compact representation of SystemCapabilities for DHT storage.
// BEP44 limits DHT values to 1000 bytes, so we use short bencode keys and GB instead of MB.
// Full capabilities can be fetched via QUIC using the capabilities request message.
type CapabilitySummary struct {
	Platform    string `json:"platform" bencode:"p"`       // "linux", "darwin", "windows"
	Arch        string `json:"arch" bencode:"a"`           // "amd64", "arm64"
	CPUCores    int    `json:"cpu_cores" bencode:"c"`      // CPU core count
	MemoryGB    int    `json:"memory_gb" bencode:"m"`      // Total RAM in GB
	DiskGB      int    `json:"disk_gb" bencode:"d"`        // Available disk in GB
	GPUCount    int    `json:"gpu_count" bencode:"g"`      // Number of GPUs
	GPUMemoryGB int    `json:"gpu_memory_gb" bencode:"gm"` // Total GPU memory in GB
	GPUVendor   string `json:"gpu_vendor" bencode:"gv"`    // "nvidia", "amd", "intel", "mixed", ""
	HasDocker   bool   `json:"has_docker" bencode:"dk"`    // Docker available
	HasPython   bool   `json:"has_python" bencode:"py"`    // Python available
}

// ToSummary creates a compact CapabilitySummary from full SystemCapabilities.
// This is used for DHT storage to stay under the BEP44 1000-byte limit.
func (sc *SystemCapabilities) ToSummary() *CapabilitySummary {
	summary := &CapabilitySummary{
		Platform:  sc.Platform,
		Arch:      sc.Architecture,
		CPUCores:  sc.CPUCores,
		MemoryGB:  int(sc.TotalMemoryMB / 1024),   // Convert MB to GB
		DiskGB:    int(sc.AvailableDiskMB / 1024), // Convert MB to GB
		GPUCount:  len(sc.GPUs),
		HasDocker: sc.HasDocker,
		HasPython: sc.HasPython,
	}

	// Calculate total GPU memory and determine vendor
	if len(sc.GPUs) > 0 {
		var totalGPUMemoryMB int64
		vendors := make(map[string]bool)

		for _, gpu := range sc.GPUs {
			totalGPUMemoryMB += gpu.MemoryMB
			if gpu.Vendor != "" {
				vendors[gpu.Vendor] = true
			}
		}

		summary.GPUMemoryGB = int(totalGPUMemoryMB / 1024) // Convert MB to GB

		// Determine GPU vendor string
		if len(vendors) == 1 {
			for vendor := range vendors {
				summary.GPUVendor = vendor
			}
		} else if len(vendors) > 1 {
			summary.GPUVendor = "mixed"
		}
	}

	return summary
}

// ServiceCapabilities represents the requirements of a service
type ServiceCapabilities struct {
	Platform         string   `json:"platform"`           // "linux", "windows", "darwin", "any"
	Architectures    []string `json:"architectures"`      // ["amd64", "arm64"]
	MinMemoryMB      int64    `json:"min_memory_mb"`
	MinCPUCores      int      `json:"min_cpu_cores"`
	MinDiskMB        int64    `json:"min_disk_mb"`
	RequiresGPU      bool     `json:"requires_gpu"`
	GPUVendor        string   `json:"gpu_vendor"`         // "nvidia", "amd", "intel", "any"
	MinGPUMemoryMB   int64    `json:"min_gpu_memory_mb"`
	ExecutablePath   string   `json:"executable_path"`
	ExecutableHash   string   `json:"executable_hash"`
	Source           string   `json:"source"`             // "local", "upload", "git"
}

// GatherSystemCapabilities collects current system information
func GatherSystemCapabilities(dataDir string) (*SystemCapabilities, error) {
	caps := &SystemCapabilities{
		Platform:     runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	// Gather kernel version
	caps.KernelVersion = getKernelVersion()

	// Gather CPU information
	caps.CPUCores = runtime.NumCPU()
	caps.CPUThreads = runtime.NumCPU() // Simplified - may not be accurate for hyperthreading
	caps.CPUModel = getCPUModel()

	// Gather memory information
	caps.TotalMemoryMB, caps.AvailableMemoryMB = getMemoryInfo()

	// Gather disk information
	caps.TotalDiskMB, caps.AvailableDiskMB = getDiskInfo(dataDir)

	// Attempt GPU detection (best effort)
	caps.GPUs = detectGPUs()

	// Check for Docker
	caps.HasDocker, caps.DockerVersion = checkDockerAvailable()

	// Check for Python
	caps.HasPython, caps.PythonVersion = checkPythonAvailable()

	return caps, nil
}

// getKernelVersion returns the kernel version
func getKernelVersion() string {
	switch runtime.GOOS {
	case "linux", "darwin":
		out, err := exec.Command("uname", "-r").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "windows":
		out, err := exec.Command("cmd", "/c", "ver").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return "unknown"
}

// getCPUModel returns the CPU model name
func getCPUModel() string {
	switch runtime.GOOS {
	case "linux":
		// Try to read from /proc/cpuinfo
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "model name") {
					parts := strings.Split(line, ":")
					if len(parts) >= 2 {
						return strings.TrimSpace(parts[1])
					}
				}
			}
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "windows":
		out, err := exec.Command("wmic", "cpu", "get", "name").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 1 {
				return strings.TrimSpace(lines[1])
			}
		}
	}
	return "unknown"
}

// getMemoryInfo returns total and available memory in MB
func getMemoryInfo() (int64, int64) {
	var total, available int64

	switch runtime.GOOS {
	case "linux":
		// Read from /proc/meminfo
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							total = val / 1024 // Convert KB to MB
						}
					}
				}
				if strings.HasPrefix(line, "MemAvailable:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							available = val / 1024 // Convert KB to MB
						}
					}
				}
			}
		}
	case "darwin":
		// Get total memory
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			if val, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
				total = val / (1024 * 1024) // Convert bytes to MB
			}
		}
		// Get available memory (approximate)
		out, err = exec.Command("vm_stat").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			var freePages, inactivePages int64
			for _, line := range lines {
				if strings.Contains(line, "Pages free:") {
					fields := strings.Fields(line)
					if len(fields) >= 3 {
						if val, err := strconv.ParseInt(strings.TrimSuffix(fields[2], "."), 10, 64); err == nil {
							freePages = val
						}
					}
				}
				if strings.Contains(line, "Pages inactive:") {
					fields := strings.Fields(line)
					if len(fields) >= 3 {
						if val, err := strconv.ParseInt(strings.TrimSuffix(fields[2], "."), 10, 64); err == nil {
							inactivePages = val
						}
					}
				}
			}
			// Approximate available memory (4KB pages on macOS)
			available = (freePages + inactivePages) * 4096 / (1024 * 1024)
		}
	case "windows":
		// Use wmic to get memory info
		out, err := exec.Command("wmic", "OS", "get", "TotalVisibleMemorySize").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 1 {
				if val, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
					total = val / 1024 // Convert KB to MB
				}
			}
		}
		out, err = exec.Command("wmic", "OS", "get", "FreePhysicalMemory").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 1 {
				if val, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
					available = val / 1024 // Convert KB to MB
				}
			}
		}
	}

	// Fallback if we couldn't get the values
	if total == 0 {
		total = 4096 // Default to 4GB
	}
	if available == 0 {
		available = total / 2 // Default to 50% available
	}

	return total, available
}

// getDiskInfo returns total and available disk space for the given path in MB
func getDiskInfo(path string) (int64, int64) {
	var total, available int64

	switch runtime.GOOS {
	case "linux", "darwin":
		out, err := exec.Command("df", "-k", path).Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 1 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 4 {
					if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						total = val / 1024 // Convert KB to MB
					}
					if val, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
						available = val / 1024 // Convert KB to MB
					}
				}
			}
		}
	case "windows":
		// Extract drive letter from path
		drive := "C:"
		if len(path) >= 2 && path[1] == ':' {
			drive = path[:2]
		}
		out, err := exec.Command("wmic", "logicaldisk", "where", fmt.Sprintf("DeviceID='%s'", drive), "get", "Size,FreeSpace").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 1 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 2 {
					if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						available = val / (1024 * 1024) // Convert bytes to MB
					}
					if val, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
						total = val / (1024 * 1024) // Convert bytes to MB
					}
				}
			}
		}
	}

	// Fallback if we couldn't get the values
	if total == 0 {
		total = 100000 // Default to ~100GB
	}
	if available == 0 {
		available = total / 2 // Default to 50% available
	}

	return total, available
}

// detectGPUs attempts to detect available GPUs
func detectGPUs() []GPUInfo {
	var gpus []GPUInfo

	// Try NVIDIA GPUs first
	if nvidiaGPUs := detectNVIDIAGPUs(); len(nvidiaGPUs) > 0 {
		gpus = append(gpus, nvidiaGPUs...)
	}

	// Could add AMD and Intel GPU detection here

	return gpus
}

// detectNVIDIAGPUs detects NVIDIA GPUs using nvidia-smi
func detectNVIDIAGPUs() []GPUInfo {
	var gpus []GPUInfo

	// Check if nvidia-smi is available
	out, err := exec.Command("nvidia-smi", "--query-gpu=index,name,memory.total,uuid,driver_version", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpus
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		fields := strings.Split(line, ", ")
		if len(fields) >= 5 {
			gpu := GPUInfo{
				Vendor: "nvidia",
			}

			if idx, err := strconv.Atoi(strings.TrimSpace(fields[0])); err == nil {
				gpu.Index = idx
			}
			gpu.Name = strings.TrimSpace(fields[1])
			if mem, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); err == nil {
				gpu.MemoryMB = mem
			}
			gpu.UUID = strings.TrimSpace(fields[3])
			gpu.DriverVersion = strings.TrimSpace(fields[4])

			gpus = append(gpus, gpu)
		}
	}

	return gpus
}

// checkDockerAvailable checks if Docker is installed and running
func checkDockerAvailable() (bool, string) {
	out, err := exec.Command("docker", "--version").Output()
	if err != nil {
		return false, ""
	}

	version := strings.TrimSpace(string(out))
	// Try to extract just the version number
	if strings.Contains(version, "Docker version") {
		parts := strings.Fields(version)
		if len(parts) >= 3 {
			version = strings.TrimSuffix(parts[2], ",")
		}
	}

	return true, version
}

// checkPythonAvailable checks if Python is installed
func checkPythonAvailable() (bool, string) {
	// Try python3 first
	out, err := exec.Command("python3", "--version").Output()
	if err == nil {
		version := strings.TrimSpace(string(out))
		version = strings.TrimPrefix(version, "Python ")
		return true, version
	}

	// Try python
	out, err = exec.Command("python", "--version").Output()
	if err == nil {
		version := strings.TrimSpace(string(out))
		version = strings.TrimPrefix(version, "Python ")
		return true, version
	}

	return false, ""
}

// CanExecuteService checks if the peer can execute a service based on capabilities
func CanExecuteService(peerCaps *SystemCapabilities, serviceCaps *ServiceCapabilities) (bool, string) {
	if peerCaps == nil || serviceCaps == nil {
		return false, "missing capabilities information"
	}

	// Check platform compatibility
	if serviceCaps.Platform != "any" && serviceCaps.Platform != peerCaps.Platform {
		return false, fmt.Sprintf("incompatible platform: service requires %s, peer has %s", serviceCaps.Platform, peerCaps.Platform)
	}

	// Check architecture compatibility
	if len(serviceCaps.Architectures) > 0 {
		archMatch := false
		for _, arch := range serviceCaps.Architectures {
			if arch == peerCaps.Architecture || arch == "any" {
				archMatch = true
				break
			}
		}
		if !archMatch {
			return false, fmt.Sprintf("incompatible architecture: service requires one of %v, peer has %s", serviceCaps.Architectures, peerCaps.Architecture)
		}
	}

	// Check memory requirements
	if serviceCaps.MinMemoryMB > 0 && serviceCaps.MinMemoryMB > peerCaps.AvailableMemoryMB {
		return false, fmt.Sprintf("insufficient memory: service requires %d MB, peer has %d MB available", serviceCaps.MinMemoryMB, peerCaps.AvailableMemoryMB)
	}

	// Check CPU requirements
	if serviceCaps.MinCPUCores > 0 && serviceCaps.MinCPUCores > peerCaps.CPUCores {
		return false, fmt.Sprintf("insufficient CPU cores: service requires %d cores, peer has %d cores", serviceCaps.MinCPUCores, peerCaps.CPUCores)
	}

	// Check disk requirements
	if serviceCaps.MinDiskMB > 0 && serviceCaps.MinDiskMB > peerCaps.AvailableDiskMB {
		return false, fmt.Sprintf("insufficient disk space: service requires %d MB, peer has %d MB available", serviceCaps.MinDiskMB, peerCaps.AvailableDiskMB)
	}

	// Check GPU requirements
	if serviceCaps.RequiresGPU {
		if len(peerCaps.GPUs) == 0 {
			return false, "service requires GPU, but peer has no GPUs"
		}

		// Check GPU vendor if specified
		if serviceCaps.GPUVendor != "" && serviceCaps.GPUVendor != "any" {
			vendorMatch := false
			for _, gpu := range peerCaps.GPUs {
				if gpu.Vendor == serviceCaps.GPUVendor {
					vendorMatch = true
					break
				}
			}
			if !vendorMatch {
				return false, fmt.Sprintf("incompatible GPU vendor: service requires %s, peer has GPUs from other vendors", serviceCaps.GPUVendor)
			}
		}

		// Check GPU memory if specified
		if serviceCaps.MinGPUMemoryMB > 0 {
			memoryMatch := false
			for _, gpu := range peerCaps.GPUs {
				if gpu.MemoryMB >= serviceCaps.MinGPUMemoryMB {
					memoryMatch = true
					break
				}
			}
			if !memoryMatch {
				return false, fmt.Sprintf("insufficient GPU memory: service requires %d MB, no GPU with sufficient memory found", serviceCaps.MinGPUMemoryMB)
			}
		}
	}

	return true, ""
}
