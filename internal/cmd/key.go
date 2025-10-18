package cmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/spf13/cobra"
)

var (
	exportFormat string
	outputPath   string
	forceExport  bool
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage node cryptographic keys",
	Long: `Manage the node's Ed25519 cryptographic keys.

The node uses Ed25519 keys for:
- DHT mutable data storage (BEP_44)
- Authentication with the web UI
- Peer identity verification`,
}

var keyExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the node's private key",
	Long: `Export the node's Ed25519 private key for authentication purposes.

The exported key can be used to authenticate with the web UI or other services
that require proof of node ownership.

SECURITY WARNING: The private key grants full control over your node's identity.
Never share this key with anyone or upload it to untrusted services.

Supported formats:
  - binary: Raw binary format (64 bytes) - default
  - hex: Hexadecimal string format (128 characters)`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize configuration if not already done
		if config == nil {
			config = utils.NewConfigManager(configPath)
		}

		// Get keys directory using centralized path management
		paths := utils.GetAppPaths("")
		keysDir := filepath.Join(paths.DataDir, "keys")

		// Load the node's keypair
		keyPair, err := crypto.LoadKeys(keysDir)
		if err != nil {
			fmt.Printf("Error: Failed to load node keys: %v\n", err)
			fmt.Printf("Keys directory: %s\n", keysDir)
			fmt.Println("\nMake sure the node has been started at least once to generate keys.")
			os.Exit(1)
		}

		// Display security warning
		fmt.Println("⚠️  SECURITY WARNING ⚠️")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("You are about to export your node's private key.")
		fmt.Println("")
		fmt.Println("This key grants FULL CONTROL over your node's identity.")
		fmt.Println("Anyone with this key can:")
		fmt.Println("  • Authenticate as your node")
		fmt.Println("  • Access the node's web UI")
		fmt.Println("  • Sign messages on behalf of your node")
		fmt.Println("")
		fmt.Println("NEVER share this key with anyone you don't trust completely.")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// Require explicit confirmation unless --force is used
		if !forceExport {
			fmt.Print("Do you want to continue? (yes/no): ")
			var response string
			fmt.Scanln(&response)
			if response != "yes" && response != "y" && response != "YES" && response != "Y" {
				fmt.Println("Export cancelled.")
				os.Exit(0)
			}
		}

		// Get the private key bytes
		privateKeyBytes := keyPair.PrivateKeyBytes()

		// Determine output destination
		var outputFile string
		if outputPath != "" {
			outputFile = outputPath
		} else {
			// Default output file based on format
			if exportFormat == "hex" {
				outputFile = filepath.Join(paths.DataDir, "exported_private_key.txt")
			} else {
				outputFile = filepath.Join(paths.DataDir, "exported_private_key.bin")
			}
		}

		// Export based on format
		var dataToWrite []byte
		switch exportFormat {
		case "binary":
			dataToWrite = privateKeyBytes
		case "hex":
			hexStr := hex.EncodeToString(privateKeyBytes)
			dataToWrite = []byte(hexStr)
		default:
			fmt.Printf("Error: Unsupported format '%s'. Use 'binary' or 'hex'.\n", exportFormat)
			os.Exit(1)
		}

		// Write to file with secure permissions (only owner can read)
		if err := os.WriteFile(outputFile, dataToWrite, 0600); err != nil {
			fmt.Printf("Error: Failed to write key to file: %v\n", err)
			os.Exit(1)
		}

		// Success message
		fmt.Println()
		fmt.Println("✓ Private key exported successfully")
		fmt.Printf("  Format: %s\n", exportFormat)
		fmt.Printf("  Location: %s\n", outputFile)
		fmt.Printf("  Size: %d bytes\n", len(dataToWrite))
		fmt.Println()
		fmt.Println("Peer ID:", keyPair.PeerID())
		fmt.Println()
		fmt.Println("Remember to keep this file secure and delete it when no longer needed.")
	},
}

var keyInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Display node key information",
	Long: `Display information about the node's Ed25519 keys without revealing the private key.

This command shows:
  - Peer ID (derived from public key)
  - Public key (hex format)
  - Keys directory location
  - Key file status`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize configuration if not already done
		if config == nil {
			config = utils.NewConfigManager(configPath)
		}

		// Get keys directory using centralized path management
		paths := utils.GetAppPaths("")
		keysDir := filepath.Join(paths.DataDir, "keys")

		// Load the node's keypair
		keyPair, err := crypto.LoadKeys(keysDir)
		if err != nil {
			fmt.Printf("Error: Failed to load node keys: %v\n", err)
			fmt.Printf("Keys directory: %s\n", keysDir)
			fmt.Println("\nMake sure the node has been started at least once to generate keys.")
			os.Exit(1)
		}

		// Display key information
		fmt.Println("Node Key Information")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Printf("Peer ID:        %s\n", keyPair.PeerID())
		fmt.Printf("Public Key:     %s\n", hex.EncodeToString(keyPair.PublicKeyBytes()))
		fmt.Printf("Keys Directory: %s\n", keysDir)
		fmt.Println()

		// Check file status
		publicKeyPath := filepath.Join(keysDir, "ed25519_public.key")
		privateKeyPath := filepath.Join(keysDir, "ed25519_private.key")

		if info, err := os.Stat(publicKeyPath); err == nil {
			fmt.Printf("Public Key File:  %s (%d bytes)\n", publicKeyPath, info.Size())
		}
		if info, err := os.Stat(privateKeyPath); err == nil {
			fmt.Printf("Private Key File: %s (%d bytes)\n", privateKeyPath, info.Size())
		}
		fmt.Println()
	},
}

func init() {
	// Register key command
	rootCmd.AddCommand(keyCmd)

	// Register subcommands
	keyCmd.AddCommand(keyExportCmd)
	keyCmd.AddCommand(keyInfoCmd)

	// Flags for export command
	keyExportCmd.Flags().StringVarP(&exportFormat, "format", "f", "binary", "export format: binary or hex")
	keyExportCmd.Flags().StringVarP(&outputPath, "output", "o", "", "output file path (default: auto-generated in data directory)")
	keyExportCmd.Flags().BoolVar(&forceExport, "force", false, "skip confirmation prompt (use with caution)")
}
