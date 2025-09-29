package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

var (
	configPath  string
	config      *utils.ConfigManager
	logger      *utils.LogsManager
	peerManager *core.PeerManager
)

var rootCmd = &cobra.Command{
	Use:   "remote-network",
	Short: "Remote Network P2P Node",
	Long: `A decentralized P2P network node using DHT for discovery and QUIC for communication.

Supports topic-based communication where peers can subscribe to topics and
exchange messages with other peers interested in the same topics.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize configuration
		config = utils.NewConfigManager(configPath)

		// Initialize logging
		logger = utils.NewLogsManager(config)

		// Skip peer manager initialization for stop command and its aliases
		cmdName := cmd.Name()
		if cmdName == "stop" || cmdName == "stop-node" || cmdName == "kill" {
			return
		}

		// Initialize peer manager for other commands
		var err error
		peerManager, err = core.NewPeerManager(config, logger)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize peer manager: %v", err), "cli")
			os.Exit(1)
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Cleanup
		if logger != nil {
			logger.Close()
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "config file path")
}