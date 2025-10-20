package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

var (
	configPath     string
	relayMode      bool
	enableDHTStore bool
	config         *utils.ConfigManager
	logger         *utils.LogsManager
	peerManager    *core.PeerManager
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

		// Override relay_mode from command line flag if provided
		if relayMode {
			config.SetConfig("relay_mode", true)
		}

		// Override enable_bep44_store from command line flag if provided
		// Note: The flag defaults to true, but we only set config if explicitly provided
		if cmd.Flags().Changed("store") {
			config.SetConfig("enable_bep44_store", enableDHTStore)
		}

		// Initialize logging
		logger = utils.NewLogsManager(config)

		// Skip peer manager initialization for stop, restart, and key commands
		cmdName := cmd.Name()
		if cmdName == "stop" || cmdName == "stop-node" || cmdName == "kill" || cmdName == "restart" || cmdName == "restart-node" || cmdName == "key" || cmdName == "export" || cmdName == "info" {
			return
		}

		// Validate relay mode requirements
		if config.GetConfigBool("relay_mode", false) {
			nodeTypeManager := utils.NewNodeTypeManager()
			isPublic, err := nodeTypeManager.IsPublicNode()
			if err != nil || !isPublic {
				logger.Warn("Relay mode requested but node does not have a public IP address", "cli")
				logger.Warn("Starting as NAT peer instead. Relay nodes require public IP addresses.", "cli")
				fmt.Println("WARNING: Relay mode requires a public IP address")
				fmt.Println("Starting node as NAT peer instead of relay")
				config.SetConfig("relay_mode", false)
			} else {
				logger.Info("Relay mode enabled - node will act as a relay for NAT peers", "cli")
			}
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
	rootCmd.PersistentFlags().BoolVarP(&relayMode, "relay", "r", false, "enable relay mode (requires public IP)")
	rootCmd.PersistentFlags().BoolVarP(&enableDHTStore, "store", "s", true, "enable BEP_44 DHT storage for serving mutable data to other peers")
}