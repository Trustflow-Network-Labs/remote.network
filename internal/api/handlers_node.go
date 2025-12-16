package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/system"
)

// NodeStatusResponse represents node status information
type NodeStatusResponse struct {
	PeerID     string                 `json:"peer_id"`
	DHTNodeID  string                 `json:"dht_node_id"`
	Uptime     int64                  `json:"uptime_seconds"`
	Stats      map[string]interface{} `json:"stats"`
	KnownPeers int                    `json:"known_peers"`
}

// NodeCapabilitiesResponse represents node capabilities (Docker, etc.)
type NodeCapabilitiesResponse struct {
	DockerAvailable bool                        `json:"docker_available"`
	DockerVersion   string                      `json:"docker_version,omitempty"`
	ColimaStatus    string                      `json:"colima_status,omitempty"` // macOS only
	System          *system.SystemCapabilities  `json:"system,omitempty"`        // Full system capabilities
}

// handleNodeStatus returns the current node status
func (s *APIServer) handleNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Add JWT authentication middleware

	// Get node stats from peer manager
	stats := s.peerManager.GetStats()

	// Get known peers count
	peersCount, err := s.peerManager.GetKnownPeers().GetKnownPeersCount()
	if err != nil {
		s.logger.Warn("Failed to get known peers count", "api")
		peersCount = 0
	}

	// Safely extract peer_id and dht_node_id from stats
	peerID := ""
	if val, ok := stats["peer_id"]; ok && val != nil {
		if str, ok := val.(string); ok {
			peerID = str
		}
	}

	dhtNodeID := ""
	if val, ok := stats["dht_node_id"]; ok && val != nil {
		if str, ok := val.(string); ok {
			dhtNodeID = str
		}
	}

	response := NodeStatusResponse{
		PeerID:     peerID,
		DHTNodeID:  dhtNodeID,
		Uptime:     int64(time.Since(s.startTime).Seconds()),
		Stats:      stats,
		KnownPeers: peersCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleNodeCapabilities returns the node's capabilities (Docker availability, etc.)
func (s *APIServer) handleNodeCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get Docker availability from config
	dockerAvailable := s.config.GetConfigBool("docker_dependencies_available", false)

	response := NodeCapabilitiesResponse{
		DockerAvailable: dockerAvailable,
	}

	// Optionally get Docker version if available
	if dockerAvailable {
		if output, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
			response.DockerVersion = string(output)
		}

		// On macOS, check Colima status
		if output, err := exec.Command("colima", "status").CombinedOutput(); err == nil {
			status := string(output)
			if containsString(status, "running") {
				response.ColimaStatus = "running"
			} else {
				response.ColimaStatus = "stopped"
			}
		}
	}

	// Gather full system capabilities (GPU, CPU, Memory, etc.)
	// Use home directory for disk info, or current directory as fallback
	dataDir, err := os.UserHomeDir()
	if err != nil {
		dataDir = "."
	}
	sysCaps, err := system.GatherSystemCapabilities(dataDir)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("Failed to gather system capabilities: %v", err), "api")
	} else {
		response.System = sysCaps
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// containsString checks if a string contains a substring (case-insensitive)
func containsString(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// RestartResponse represents the response from restart endpoint
type RestartResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// handleNodeRestart restarts the running node
func (s *APIServer) handleNodeRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Add JWT authentication middleware

	s.logger.Info("Restart request received via API", "api")

	// Send response immediately before restarting
	response := RestartResponse{
		Success: true,
		Message: "Node restart initiated successfully",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	// Flush the response to ensure client receives it
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Perform restart asynchronously using the restart command
	go func() {
		// Wait a moment to ensure response is sent
		time.Sleep(500 * time.Millisecond)

		s.logger.Info("Starting node restart via restart command", "api")

		// Get the executable path
		exePath, err := os.Executable()
		if err != nil {
			msg := fmt.Sprintf("Failed to get executable path: %v", err)
			s.logger.Error(msg, "api")
			return
		}

		// Build the restart command with same flags
		restartArgs := []string{"restart"}

		// Get config path from config manager if set
		if configPath := s.config.GetConfigWithDefault("config_path", ""); configPath != "" {
			restartArgs = append(restartArgs, "--config", configPath)
		}

		// Add passphrase file flag if it exists in config (auto-created or user-provided)
		if autoPassphraseFile := s.config.GetConfigWithDefault("auto_passphrase_file", ""); autoPassphraseFile != "" {
			restartArgs = append(restartArgs, "--passphrase-file", autoPassphraseFile)
		}

		// Check if relay mode is enabled
		if s.config.GetConfigBool("relay_mode", false) {
			restartArgs = append(restartArgs, "--relay")
		}

		// Preserve DHT store setting
		if s.config.GetConfigBool("enable_bep44_store", true) {
			restartArgs = append(restartArgs, "--store")
		} else {
			restartArgs = append(restartArgs, "--store=false")
		}

		// Execute the restart command in a detached process
		restartCmd := exec.Command(exePath, restartArgs...)

		// Set up process attributes for proper detachment (platform-specific)
		restartCmd.SysProcAttr = getDetachedProcessAttr()

		// Detach standard streams
		restartCmd.Stdout = nil
		restartCmd.Stderr = nil
		restartCmd.Stdin = nil

		s.logger.Info(fmt.Sprintf("Executing restart command: %s %v", exePath, restartArgs), "api")

		err = restartCmd.Start()
		if err != nil {
			msg := fmt.Sprintf("Failed to execute restart command: %v", err)
			s.logger.Error(msg, "api")
			return
		}

		s.logger.Info(fmt.Sprintf("Restart command process started with PID: %d", restartCmd.Process.Pid), "api")

		// Release the process
		err = restartCmd.Process.Release()
		if err != nil {
			s.logger.Warn(fmt.Sprintf("Failed to release restart process: %v", err), "api")
		}

		s.logger.Info("Restart command initiated successfully", "api")
	}()
}
