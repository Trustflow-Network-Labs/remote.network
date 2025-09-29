package cmd

import (
	"fmt"
	"os"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:     "stop",
	Aliases: []string{"stop-node", "kill"},
	Short:   "Stop the running remote-network node",
	Long:    "Stop the running remote-network node by sending a graceful termination signal",
	Args:    cobra.ExactArgs(0),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Override PersistentPreRun for stop command - we don't need peer manager
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
			logger.Error(msg, "stop")
			os.Exit(1)
		}

		// Read the PID from file
		pid, err := pidManager.ReadPID()
		if err != nil {
			msg := fmt.Sprintf("Failed to read PID: %v", err)
			fmt.Println(msg)
			logger.Error(msg, "stop")
			os.Exit(1)
		}

		fmt.Printf("Found running node with PID: %d\n", pid)

		// Check if process is actually running
		if !pidManager.IsProcessRunning(pid) {
			msg := fmt.Sprintf("Process with PID %d is not running", pid)
			fmt.Println(msg)
			logger.Warn(msg, "stop")

			// Clean up stale PID file
			if err := pidManager.RemovePIDFile(); err != nil {
				fmt.Printf("Warning: Failed to remove stale PID file: %v\n", err)
			} else {
				fmt.Println("Removed stale PID file")
			}
			os.Exit(0)
		}

		// Stop the process
		fmt.Printf("Stopping remote-network node (PID: %d)...\n", pid)
		err = pidManager.StopProcess(pid)
		if err != nil {
			msg := fmt.Sprintf("Failed to stop process: %v", err)
			fmt.Println(msg)
			logger.Error(msg, "stop")
			os.Exit(1)
		}

		// Clean up PID file
		if err := pidManager.RemovePIDFile(); err != nil {
			fmt.Printf("Warning: Failed to remove PID file: %v\n", err)
		}

		msg := "Remote-network node stopped successfully"
		fmt.Println(msg)
		logger.Info(msg, "stop")
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Cleanup logger
		if logger != nil {
			logger.Close()
		}
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}