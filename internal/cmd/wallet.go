package cmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/payment"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	walletNetwork    string
	walletPrivateKey string
	walletID         string
	forceWallet      bool
)

var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Manage x402 payment wallets",
	Long: `Manage x402 payment wallets for job execution and relay payments.

The node uses wallets for:
- Paying for job executions on remote peers
- Paying for relay services (NAT traversal)
- Receiving payments for services provided

Wallets are encrypted and stored locally using AES-256-GCM encryption.`,
}

var walletCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new payment wallet",
	Long: `Create a new payment wallet for a specific blockchain network.

The wallet will be encrypted with a passphrase and stored locally.

Supported networks:
  - eip155:84532  (Base Sepolia testnet - default)
  - eip155:8453   (Base mainnet)
  - solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp (Solana mainnet)
  - solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z (Solana testnet)

Example:
  remote-network wallet create --network eip155:84532`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Default to Base Sepolia testnet
		if walletNetwork == "" {
			walletNetwork = "eip155:84532"
		}

		// Prompt for passphrase
		fmt.Println("Creating new wallet...")
		fmt.Printf("Network: %s\n", walletNetwork)
		fmt.Println()

		passphrase, err := promptPassphrase("Enter passphrase to encrypt wallet: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		confirmedPassphrase, err := promptPassphrase("Confirm passphrase: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if passphrase != confirmedPassphrase {
			fmt.Println("Error: Passphrases do not match")
			os.Exit(1)
		}

		// Create wallet
		wallet, err := walletManager.CreateWallet(walletNetwork, passphrase)
		if err != nil {
			fmt.Printf("Error: Failed to create wallet: %v\n", err)
			os.Exit(1)
		}

		// Success message
		fmt.Println()
		fmt.Println("✓ Wallet created successfully")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("Wallet ID:  %s\n", wallet.ID)
		fmt.Printf("Network:    %s\n", wallet.Network)
		fmt.Printf("Address:    %s\n", wallet.Address)
		fmt.Println()
		fmt.Println("To set this as your default wallet:")
		fmt.Printf("  remote-network config set x402_default_wallet_id %s\n", wallet.ID)
		fmt.Println()
		fmt.Println("Remember your passphrase - it cannot be recovered if lost!")
		fmt.Println()
	},
}

var walletImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import an existing wallet from private key",
	Long: `Import an existing wallet using a private key.

The private key should be provided in hexadecimal format (with or without 0x prefix).

SECURITY WARNING: Never share your private key with anyone or enter it on
untrusted systems. The private key grants full control over the wallet.

Example:
  remote-network wallet import --private-key 0x1234... --network eip155:84532`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Validate inputs
		if walletPrivateKey == "" {
			fmt.Println("Error: --private-key is required")
			os.Exit(1)
		}

		if walletNetwork == "" {
			walletNetwork = "eip155:84532"
		}

		// Display security warning
		fmt.Println("⚠️  SECURITY WARNING ⚠️")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("You are about to import a wallet using a private key.")
		fmt.Println("")
		fmt.Println("Make sure you are on a TRUSTED system and the private key is from")
		fmt.Println("a wallet you control. Never import keys from untrusted sources.")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// Require confirmation unless --force is used
		if !forceWallet {
			fmt.Print("Do you want to continue? (yes/no): ")
			var response string
			fmt.Scanln(&response)
			if response != "yes" && response != "y" && response != "YES" && response != "Y" {
				fmt.Println("Import cancelled.")
				os.Exit(0)
			}
		}

		// Prompt for passphrase
		fmt.Println()
		passphrase, err := promptPassphrase("Enter passphrase to encrypt wallet: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		confirmedPassphrase, err := promptPassphrase("Confirm passphrase: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if passphrase != confirmedPassphrase {
			fmt.Println("Error: Passphrases do not match")
			os.Exit(1)
		}

		// Import wallet
		wallet, err := walletManager.ImportWallet(walletPrivateKey, walletNetwork, passphrase)
		if err != nil {
			fmt.Printf("Error: Failed to import wallet: %v\n", err)
			os.Exit(1)
		}

		// Success message
		fmt.Println()
		fmt.Println("✓ Wallet imported successfully")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("Wallet ID:  %s\n", wallet.ID)
		fmt.Printf("Network:    %s\n", wallet.Network)
		fmt.Printf("Address:    %s\n", wallet.Address)
		fmt.Println()
		fmt.Println("To set this as your default wallet:")
		fmt.Printf("  remote-network config set x402_default_wallet_id %s\n", wallet.ID)
		fmt.Println()
	},
}

var walletListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all payment wallets",
	Long:  `Display all payment wallets stored on this node.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Get config to check default wallet
		if config == nil {
			config = utils.NewConfigManager(configPath)
		}
		defaultWalletID := config.GetConfigWithDefault("x402_default_wallet_id", "")

		// List wallets
		wallets := walletManager.ListWallets()

		if len(wallets) == 0 {
			fmt.Println("No wallets found.")
			fmt.Println()
			fmt.Println("Create a new wallet:")
			fmt.Println("  remote-network wallet create --network eip155:84532")
			return
		}

		fmt.Println("Payment Wallets")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		for _, wallet := range wallets {
			defaultMarker := ""
			if wallet.ID == defaultWalletID {
				defaultMarker = " (default)"
			}

			fmt.Printf("Wallet ID:  %s%s\n", wallet.ID, defaultMarker)
			fmt.Printf("Network:    %s\n", wallet.Network)
			fmt.Printf("Address:    %s\n", wallet.Address)
			fmt.Println()
		}

		if defaultWalletID == "" {
			fmt.Println("No default wallet set. Set one using:")
			fmt.Println("  remote-network config set x402_default_wallet_id <wallet-id>")
			fmt.Println()
		}
	},
}

var walletInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Display detailed information about a wallet",
	Long: `Display detailed information about a specific wallet.

Example:
  remote-network wallet info --wallet-id <id>`,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate inputs
		if walletID == "" {
			fmt.Println("Error: --wallet-id is required")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  remote-network wallet info --wallet-id <id>")
			fmt.Println()
			fmt.Println("List all wallets:")
			fmt.Println("  remote-network wallet list")
			os.Exit(1)
		}

		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Prompt for passphrase
		passphrase, err := promptPassphrase("Enter wallet passphrase: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// Get wallet
		wallet, err := walletManager.GetWallet(walletID, passphrase)
		if err != nil {
			fmt.Printf("Error: Failed to load wallet: %v\n", err)
			os.Exit(1)
		}

		// Get config to check if default
		if config == nil {
			config = utils.NewConfigManager(configPath)
		}
		defaultWalletID := config.GetConfigWithDefault("x402_default_wallet_id", "")
		isDefault := wallet.ID == defaultWalletID

		// Display wallet information
		fmt.Println()
		fmt.Println("Wallet Information")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Printf("Wallet ID:       %s\n", wallet.ID)
		fmt.Printf("Network:         %s\n", wallet.Network)
		fmt.Printf("Address:         %s\n", wallet.Address)
		fmt.Printf("Default Wallet:  %v\n", isDefault)
		fmt.Println()

		// Display network info
		networkName := getNetworkName(wallet.Network)
		fmt.Printf("Network Name:    %s\n", networkName)
		fmt.Println()

		// Show public key (truncated)
		if len(wallet.Address) > 20 {
			fmt.Printf("Address (short): %s...%s\n", wallet.Address[:10], wallet.Address[len(wallet.Address)-8:])
		}
		fmt.Println()
	},
}

var walletBalanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Query wallet balance from blockchain",
	Long: `Query the current balance of a wallet from the blockchain.

This command queries the blockchain directly to get the real-time balance.

Example:
  remote-network wallet balance --wallet-id <id>`,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate inputs
		if walletID == "" {
			fmt.Println("Error: --wallet-id is required")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  remote-network wallet balance --wallet-id <id>")
			fmt.Println()
			fmt.Println("List all wallets:")
			fmt.Println("  remote-network wallet list")
			os.Exit(1)
		}

		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Querying balance for wallet %s...\n", walletID)
		fmt.Println()

		// Get balance
		balance, err := walletManager.GetBalance(walletID)
		if err != nil {
			fmt.Printf("Error: Failed to query balance: %v\n", err)
			os.Exit(1)
		}

		// Display balance
		fmt.Println("✓ Balance retrieved")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Printf("Wallet ID:  %s\n", balance.WalletID)
		fmt.Printf("Network:    %s\n", balance.Network)
		fmt.Printf("Address:    %s\n", balance.Address)
		fmt.Printf("Balance:    %.6f %s\n", balance.Balance, balance.Currency)
		fmt.Println()
	},
}

var walletDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a wallet",
	Long: `Delete a wallet and remove it from local storage.

SECURITY WARNING: This action is irreversible. Make sure you have backed up
your private key before deleting the wallet. Once deleted, the wallet cannot
be recovered unless you have the private key.

Example:
  remote-network wallet delete --wallet-id <id>`,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate inputs
		if walletID == "" {
			fmt.Println("Error: --wallet-id is required")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  remote-network wallet delete --wallet-id <id>")
			fmt.Println()
			fmt.Println("List all wallets:")
			fmt.Println("  remote-network wallet list")
			os.Exit(1)
		}

		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Display security warning
		fmt.Println("⚠️  WARNING ⚠️")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("You are about to DELETE a wallet.")
		fmt.Println("")
		fmt.Println("This action is IRREVERSIBLE. The wallet file will be permanently")
		fmt.Println("removed from your system. Make sure you have:")
		fmt.Println("  • Exported and backed up the private key")
		fmt.Println("  • Transferred any funds to another wallet")
		fmt.Println("")
		fmt.Printf("Wallet ID: %s\n", walletID)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// Require confirmation unless --force is used
		if !forceWallet {
			fmt.Print("Do you want to continue? (yes/no): ")
			var response string
			fmt.Scanln(&response)
			if response != "yes" && response != "y" && response != "YES" && response != "Y" {
				fmt.Println("Delete cancelled.")
				os.Exit(0)
			}
		}

		// Prompt for passphrase to verify ownership
		fmt.Println()
		passphrase, err := promptPassphrase("Enter wallet passphrase to confirm: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// Delete wallet
		if err := walletManager.DeleteWallet(walletID, passphrase); err != nil {
			fmt.Printf("Error: Failed to delete wallet: %v\n", err)
			os.Exit(1)
		}

		// Success message
		fmt.Println()
		fmt.Println("✓ Wallet deleted successfully")
		fmt.Println()
	},
}

var walletExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export wallet private key",
	Long: `Export the private key of a wallet for backup or transfer purposes.

SECURITY WARNING: The private key grants full control over the wallet.
Never share this key with anyone or upload it to untrusted services.

Example:
  remote-network wallet export --wallet-id <id>`,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate inputs
		if walletID == "" {
			fmt.Println("Error: --wallet-id is required")
			os.Exit(1)
		}

		// Initialize config manager for wallet operations
		config := utils.NewConfigManager("configs")

		// Initialize wallet manager
		walletManager, err := payment.NewWalletManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize wallet manager: %v\n", err)
			os.Exit(1)
		}

		// Display security warning
		fmt.Println("⚠️  SECURITY WARNING ⚠️")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("You are about to export your wallet's private key.")
		fmt.Println("")
		fmt.Println("This key grants FULL CONTROL over your wallet and funds.")
		fmt.Println("Anyone with this key can:")
		fmt.Println("  • Transfer all funds from the wallet")
		fmt.Println("  • Sign transactions on your behalf")
		fmt.Println("  • Impersonate your identity")
		fmt.Println("")
		fmt.Println("NEVER share this key with anyone you don't trust completely.")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// Require confirmation unless --force is used
		if !forceWallet {
			fmt.Print("Do you want to continue? (yes/no): ")
			var response string
			fmt.Scanln(&response)
			if response != "yes" && response != "y" && response != "YES" && response != "Y" {
				fmt.Println("Export cancelled.")
				os.Exit(0)
			}
		}

		// Prompt for passphrase
		fmt.Println()
		passphrase, err := promptPassphrase("Enter wallet passphrase: ")
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		// Get wallet
		wallet, err := walletManager.GetWallet(walletID, passphrase)
		if err != nil {
			fmt.Printf("Error: Failed to load wallet: %v\n", err)
			os.Exit(1)
		}

		// Export private key
		privateKeyHex := hex.EncodeToString(wallet.PrivateKey)

		// Display private key
		fmt.Println()
		fmt.Println("✓ Private key exported")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Println("Private Key (Hexadecimal):")
		fmt.Printf("0x%s\n", privateKeyHex)
		fmt.Println()
		fmt.Println("Keep this key secure and delete it when no longer needed.")
		fmt.Println()
	},
}

// Helper function to prompt for passphrase securely
func promptPassphrase(prompt string) (string, error) {
	fmt.Print(prompt)
	passphraseBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println() // New line after password input
	if err != nil {
		return "", fmt.Errorf("failed to read passphrase: %v", err)
	}
	return string(passphraseBytes), nil
}

// Helper function to get human-readable network name
func getNetworkName(network string) string {
	switch network {
	case "eip155:84532":
		return "Base Sepolia (Testnet)"
	case "eip155:8453":
		return "Base (Mainnet)"
	case "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp":
		return "Solana Mainnet"
	case "solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z":
		return "Solana Testnet"
	default:
		if strings.HasPrefix(network, "eip155:") {
			return "EVM Chain (ID: " + strings.TrimPrefix(network, "eip155:") + ")"
		} else if strings.HasPrefix(network, "solana:") {
			return "Solana Chain"
		}
		return "Unknown Network"
	}
}

func init() {
	// Register wallet command
	rootCmd.AddCommand(walletCmd)

	// Register subcommands
	walletCmd.AddCommand(walletCreateCmd)
	walletCmd.AddCommand(walletImportCmd)
	walletCmd.AddCommand(walletListCmd)
	walletCmd.AddCommand(walletInfoCmd)
	walletCmd.AddCommand(walletBalanceCmd)
	walletCmd.AddCommand(walletDeleteCmd)
	walletCmd.AddCommand(walletExportCmd)

	// Flags for create command
	walletCreateCmd.Flags().StringVarP(&walletNetwork, "network", "n", "", "blockchain network (e.g., eip155:84532)")

	// Flags for import command
	walletImportCmd.Flags().StringVarP(&walletPrivateKey, "private-key", "k", "", "private key in hexadecimal format (required)")
	walletImportCmd.Flags().StringVarP(&walletNetwork, "network", "n", "", "blockchain network (e.g., eip155:84532)")
	walletImportCmd.Flags().BoolVar(&forceWallet, "force", false, "skip confirmation prompt (use with caution)")

	// Flags for info command
	walletInfoCmd.Flags().StringVarP(&walletID, "wallet-id", "w", "", "wallet ID (required)")

	// Flags for balance command
	walletBalanceCmd.Flags().StringVarP(&walletID, "wallet-id", "w", "", "wallet ID (required)")

	// Flags for delete command
	walletDeleteCmd.Flags().StringVarP(&walletID, "wallet-id", "w", "", "wallet ID (required)")
	walletDeleteCmd.Flags().BoolVar(&forceWallet, "force", false, "skip confirmation prompt (use with caution)")

	// Flags for export command
	walletExportCmd.Flags().StringVarP(&walletID, "wallet-id", "w", "", "wallet ID (required)")
	walletExportCmd.Flags().BoolVar(&forceWallet, "force", false, "skip confirmation prompt (use with caution)")
}
