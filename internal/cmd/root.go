package cmd

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto/keystore"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

var (
	configPath           string
	relayMode            bool
	enableDHTStore       bool
	passphraseFile       string
	installDependencies  bool
	config               *utils.ConfigManager
	logger               *utils.LogsManager
	peerManager          *core.PeerManager
	keyPair              *crypto.KeyPair // Loaded from keystore, used by start command
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

		// Initialize encrypted keystore and load keys
		paths := utils.GetAppPaths("")
		keystoreData, err := keystore.InitOrLoadKeystore(paths.DataDir, passphraseFile, config)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize keystore: %v", err), "cli")
			fmt.Printf("\n❌ Keystore initialization failed: %v\n", err)
			os.Exit(1)
		}

		// Store JWT secret in config (in-memory only, never written to disk)
		config.SetConfig("jwt_secret", hex.EncodeToString(keystoreData.JWTSecret))

		// Convert keystore data to crypto.KeyPair and store in package variable
		// The start command will use this to create the peer manager
		keyPair, err = keystore.LoadKeysFromKeystore(keystoreData)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to load keys from keystore: %v", err), "cli")
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Keystore unlocked successfully (peer_id: %s)", keyPair.PeerID()), "cli")

		// Note: Peer manager is NOT created here to avoid resource leaks
		// It will be created in the start command when actually needed
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
	rootCmd.PersistentFlags().StringVarP(&passphraseFile, "passphrase-file", "p", "", "path to file containing keystore passphrase (for automated deployments)")
	rootCmd.PersistentFlags().BoolVarP(&installDependencies, "install-dependencies", "i", false, "install missing Docker dependencies (checks on every startup)")
}