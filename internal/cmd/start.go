package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the remote network node",
	Long: `Start the remote network node to join the P2P network.

This will:
- Initialize the DHT for peer discovery
- Start QUIC listener for peer connections
- Subscribe to configured topics
- Begin announcing presence to the network`,
	Run: func(cmd *cobra.Command, args []string) {
		logger.Info("Starting Remote Network Node...", "cli")

		// Ensure the executable path is absolute for Windows compatibility
		exePath, err := filepath.Abs(os.Args[0])
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get absolute path: %v", err), "cli")
			fmt.Printf("Error getting absolute path: %v\n", err)
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Starting node from: %s", exePath), "cli")

		// Initialize PID manager and write current PID
		pidManager, err := utils.NewPIDManager(config)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to create PID manager: %v", err), "cli")
			os.Exit(1)
		}

		// Initialize monitoring server
		monitoringServer := utils.NewMonitoringServer(config, logger)
		if err := monitoringServer.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start monitoring server: %v", err), "cli")
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Monitoring server started on port %s", monitoringServer.GetPort()), "cli")

		// Initialize API server for web UI (will start after peer manager)
		var apiServer *api.APIServer

		// Check if another instance is already running
		if existingPID, err := pidManager.ReadPID(); err == nil {
			if pidManager.IsProcessRunning(existingPID) {
				logger.Error(fmt.Sprintf("Another instance is already running with PID: %d", existingPID), "cli")
				fmt.Printf("Another instance is already running with PID: %d\n", existingPID)
				fmt.Println("Use 'remote-network stop' to stop the existing instance first")
				os.Exit(1)
			} else {
				// Clean up stale PID file
				pidManager.RemovePIDFile()
			}
		}

		// Write current PID to file
		currentPID := os.Getpid()
		if err := pidManager.WritePID(currentPID); err != nil {
			logger.Error(fmt.Sprintf("Failed to write PID file: %v", err), "cli")
			os.Exit(1)
		}

		// Ensure PID file is cleaned up on exit
		defer func() {
			if err := pidManager.RemovePIDFile(); err != nil {
				logger.Warn(fmt.Sprintf("Failed to remove PID file: %v", err), "cli")
			}
		}()

		logger.Info(fmt.Sprintf("Node started with PID: %d", currentPID), "cli")

		// Start the peer manager
		if err := peerManager.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start peer manager: %v", err), "cli")
			os.Exit(1)
		}

		// Print startup information
		stats := peerManager.GetStats()
		logger.Info(fmt.Sprintf("Node started with DHT Node ID: %s", stats["dht_node_id"]), "cli")

		// Show relay mode status
		if relayMode, ok := stats["relay_mode"].(bool); ok && relayMode {
			logger.Info("Node is running in RELAY MODE", "cli")
			fmt.Println("✓ Relay mode enabled - accepting relay connections from NAT peers")
		} else {
			// Check NAT type
			if natType, ok := stats["nat_type"].(string); ok {
				logger.Info(fmt.Sprintf("NAT Type: %s", natType), "cli")
				if requiresRelay, ok := stats["requires_relay"].(bool); ok && requiresRelay {
					fmt.Println("ℹ NAT type requires relay - will connect via relay peers")
				}
			}
		}

		topics := peerManager.GetTopics()
		if len(topics) > 0 {
			logger.Info(fmt.Sprintf("Subscribed to topics: %v", topics), "cli")
		}

		// Start API server for web UI
		apiServer = api.NewAPIServer(config, logger, peerManager, peerManager.GetDBManager())
		if err := apiServer.Start(); err != nil {
			logger.Warn(fmt.Sprintf("Failed to start API server: %v", err), "cli")
			fmt.Println("⚠ Web UI unavailable - API server failed to start")
		} else {
			fmt.Printf("✓ Web UI available at http://localhost:%s\n", apiServer.GetPort())
			logger.Info(fmt.Sprintf("API server started on port %s", apiServer.GetPort()), "cli")
		}

		fmt.Println("Remote Network Node is running. Press Ctrl+C to stop.")

		// Setup signal handling for graceful shutdown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Create cleanup function that we'll call from multiple places
		cleanup := func() {
			logger.Info("Shutdown signal received, stopping node...", "cli")

			// Stop peer manager
			if err := peerManager.Stop(); err != nil {
				logger.Error(fmt.Sprintf("Error stopping peer manager: %v", err), "cli")
			}

			// Stop monitoring server
			if err := monitoringServer.Stop(); err != nil {
				logger.Error(fmt.Sprintf("Error stopping monitoring server: %v", err), "cli")
			}

			// Stop API server
			if apiServer != nil {
				if err := apiServer.Stop(); err != nil {
					logger.Error(fmt.Sprintf("Error stopping API server: %v", err), "cli")
				}
			}

			// Clean up PID file
			if err := pidManager.RemovePIDFile(); err != nil {
				logger.Warn(fmt.Sprintf("Failed to remove PID file: %v", err), "cli")
			}

			logger.Info("Remote Network Node stopped successfully", "cli")
		}

		// Set up signal handling in a goroutine
		go func() {
			<-sigChan
			cleanup()
			os.Exit(0)
		}()

		// Use a blocking channel to keep the main function alive
		done := make(chan bool)
		<-done
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
