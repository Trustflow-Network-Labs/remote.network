package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:     "restart",
	Aliases: []string{"restart-node"},
	Short:   "Restart the running remote-network node",
	Long:    "Restart the running remote-network node by stopping it gracefully and starting it again",
	Args:    cobra.ExactArgs(0),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Override PersistentPreRun for restart command - we don't need peer manager yet
		// Initialize minimal configuration only
		config = utils.NewConfigManager(configPath)
		logger = utils.NewLogsManager(config)
	},
	Run: func(cmd *cobra.Command, args []string) {
		// Create PID Manager instance
		pidManager, err := utils.NewPIDManager(config)
		if err != nil {
			msg := fmt.Sprintf("Failed to create PID manager: %v", err)
			fmt.Println(msg)
			logger.Error(msg, "restart")
			os.Exit(1)
		}

		// Check if node is running
		pid, err := pidManager.ReadPID()
		isRunning := err == nil && pidManager.IsProcessRunning(pid)

		if isRunning {
			fmt.Printf("Found running node with PID: %d\n", pid)
			fmt.Println("Stopping node...")

			// Stop the process
			err = pidManager.StopProcess(pid)
			if err != nil {
				msg := fmt.Sprintf("Failed to stop process: %v", err)
				fmt.Println(msg)
				logger.Error(msg, "restart")
				os.Exit(1)
			}

			// Clean up PID file
			if err := pidManager.RemovePIDFile(); err != nil {
				fmt.Printf("Warning: Failed to remove PID file: %v\n", err)
			}

			fmt.Println("Node stopped successfully")
			logger.Info("Node stopped successfully", "restart")

			// Wait a moment for cleanup
			time.Sleep(2 * time.Second)
		} else {
			fmt.Println("No running node found, starting fresh...")
			logger.Info("No running node found, starting fresh", "restart")

			// Clean up stale PID file if exists
			if err == nil {
				if err := pidManager.RemovePIDFile(); err != nil {
					fmt.Printf("Warning: Failed to remove stale PID file: %v\n", err)
				}
			}
		}

		// Start the node again
		fmt.Println("Starting node...")
		logger.Info("Starting node", "restart")

		// Get the executable path
		exePath, err := os.Executable()
		if err != nil {
			msg := fmt.Sprintf("Failed to get executable path: %v", err)
			fmt.Println(msg)
			logger.Error(msg, "restart")
			os.Exit(1)
		}

		// Build the start command with same flags
		startArgs := []string{"start"}

		// Add config flag if provided
		if configPath != "" {
			startArgs = append(startArgs, "--config", configPath)
		}

		// Add relay flag if provided
		if relayMode {
			startArgs = append(startArgs, "--relay")
		}

		// Add store flag if explicitly set
		if cmd.Flags().Changed("store") {
			if enableDHTStore {
				startArgs = append(startArgs, "--store")
			} else {
				startArgs = append(startArgs, "--store=false")
			}
		}

		// Start the process in the background
		startCmd := exec.Command(exePath, startArgs...)
		startCmd.Stdout = nil
		startCmd.Stderr = nil
		startCmd.Stdin = nil

		err = startCmd.Start()
		if err != nil {
			msg := fmt.Sprintf("Failed to start node: %v", err)
			fmt.Println(msg)
			logger.Error(msg, "restart")
			os.Exit(1)
		}

		// Detach the process
		err = startCmd.Process.Release()
		if err != nil {
			msg := fmt.Sprintf("Warning: Failed to detach process: %v", err)
			fmt.Println(msg)
			logger.Warn(msg, "restart")
		}

		msg := "Remote-network node restarted successfully (new PID will be written by start process)"
		fmt.Println(msg)
		logger.Info(msg, "restart")
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Cleanup logger
		if logger != nil {
			logger.Close()
		}
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}
