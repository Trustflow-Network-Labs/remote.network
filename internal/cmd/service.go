package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/spf13/cobra"
)

var (
	serviceID       int64
	paymentNetworks []string
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage services and payment network settings",
	Long: `Manage services offered by this node and configure payment network preferences.

Services can specify which blockchain networks they accept for payments.
By default, services accept all networks supported by your configured wallets.`,
}

var serviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all services",
	Long: `List all services offered by this node with their payment network settings.

Shows service ID, name, type, pricing, and accepted payment networks.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize database
		dbManager, err := database.NewSQLiteManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize database: %v\n", err)
			os.Exit(1)
		}
		defer dbManager.Close()

		// Get all services
		services, err := dbManager.GetAllServices()
		if err != nil {
			fmt.Printf("Error: Failed to get services: %v\n", err)
			os.Exit(1)
		}

		if len(services) == 0 {
			fmt.Println("No services configured")
			return
		}

		// Display services
		fmt.Println("Services:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		for _, service := range services {
			fmt.Printf("\nID:          %d\n", service.ID)
			fmt.Printf("Name:        %s\n", service.Name)
			fmt.Printf("Type:        %s\n", service.ServiceType)
			fmt.Printf("Pricing:     %.6f USDC\n", service.PricingAmount)
			fmt.Printf("Status:      %s\n", service.Status)

			if len(service.AcceptedPaymentNetworks) > 0 {
				fmt.Printf("Networks:    %s (explicit)\n", strings.Join(service.AcceptedPaymentNetworks, ", "))
			} else {
				fmt.Printf("Networks:    All supported networks (auto-detected)\n")
			}
		}
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	},
}

var servicePaymentNetworksCmd = &cobra.Command{
	Use:   "payment-networks",
	Short: "Manage payment network settings for services",
	Long: `View and configure which blockchain networks a service accepts for payment.

By default, services accept all networks supported by your configured wallets.
You can restrict a service to specific networks using the 'set' subcommand.`,
}

var serviceGetNetworksCmd = &cobra.Command{
	Use:   "get <service-id>",
	Short: "Get accepted payment networks for a service",
	Long: `Display which blockchain networks a service accepts for payment.

Example:
  remote-network service payment-networks get 1`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serviceID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Error: Invalid service ID: %v\n", err)
			os.Exit(1)
		}

		// Initialize database
		dbManager, err := database.NewSQLiteManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize database: %v\n", err)
			os.Exit(1)
		}
		defer dbManager.Close()

		// Get service
		service, err := dbManager.GetService(serviceID)
		if err != nil {
			fmt.Printf("Error: Failed to get service: %v\n", err)
			os.Exit(1)
		}

		if service == nil {
			fmt.Printf("Error: Service %d not found\n", serviceID)
			os.Exit(1)
		}

		// Display payment networks
		fmt.Printf("Service: %s (ID: %d)\n", service.Name, service.ID)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		if len(service.AcceptedPaymentNetworks) > 0 {
			fmt.Println("Accepted payment networks (explicit):")
			for _, network := range service.AcceptedPaymentNetworks {
				fmt.Printf("  - %s\n", network)
			}
		} else {
			fmt.Println("Accepted payment networks: All supported networks (auto-detected)")
			fmt.Println()
			fmt.Println("Tip: Use 'remote-network wallet list' to see your configured wallets")
		}
	},
}

var serviceSetNetworksCmd = &cobra.Command{
	Use:   "set <service-id> <network1> [network2...]",
	Short: "Set accepted payment networks for a service",
	Long: `Configure which blockchain networks a service accepts for payment.

Supported network formats (CAIP-2):
  - eip155:84532     (Base Sepolia testnet)
  - eip155:8453      (Base mainnet)
  - eip155:*         (Any EVM chain)
  - solana:devnet    (Solana devnet)
  - solana:mainnet-beta (Solana mainnet)
  - solana:*         (Any Solana network)

Examples:
  # Accept only Base Sepolia
  remote-network service payment-networks set 1 eip155:84532

  # Accept both EVM and Solana
  remote-network service payment-networks set 1 eip155:* solana:*

  # Accept specific networks
  remote-network service payment-networks set 1 eip155:84532 solana:devnet`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		serviceID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Error: Invalid service ID: %v\n", err)
			os.Exit(1)
		}

		networks := args[1:]

		// Validate networks
		for _, network := range networks {
			if !isValidPaymentNetworkCLI(network) {
				fmt.Printf("Error: Invalid network format: %s\n", network)
				fmt.Println()
				fmt.Println("Valid formats (CAIP-2):")
				fmt.Println("  - eip155:<chainID>  (e.g., eip155:84532)")
				fmt.Println("  - eip155:*          (any EVM chain)")
				fmt.Println("  - solana:<cluster>  (e.g., solana:devnet)")
				fmt.Println("  - solana:*          (any Solana network)")
				os.Exit(1)
			}
		}

		// Initialize database
		dbManager, err := database.NewSQLiteManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize database: %v\n", err)
			os.Exit(1)
		}
		defer dbManager.Close()

		// Get service
		service, err := dbManager.GetService(serviceID)
		if err != nil {
			fmt.Printf("Error: Failed to get service: %v\n", err)
			os.Exit(1)
		}

		if service == nil {
			fmt.Printf("Error: Service %d not found\n", serviceID)
			os.Exit(1)
		}

		// Update service
		service.AcceptedPaymentNetworks = networks
		if err := dbManager.UpdateService(service); err != nil {
			fmt.Printf("Error: Failed to update service: %v\n", err)
			os.Exit(1)
		}

		// Success
		fmt.Println("✓ Payment networks updated successfully")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("Service:  %s (ID: %d)\n", service.Name, service.ID)
		fmt.Println("Networks:")
		for _, network := range networks {
			fmt.Printf("  - %s\n", network)
		}
	},
}

var serviceClearNetworksCmd = &cobra.Command{
	Use:   "clear <service-id>",
	Short: "Clear payment network restrictions (accept all)",
	Long: `Remove payment network restrictions from a service.

After clearing, the service will accept all networks supported by your wallets.

Example:
  remote-network service payment-networks clear 1`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serviceID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Printf("Error: Invalid service ID: %v\n", err)
			os.Exit(1)
		}

		// Initialize database
		dbManager, err := database.NewSQLiteManager(config)
		if err != nil {
			fmt.Printf("Error: Failed to initialize database: %v\n", err)
			os.Exit(1)
		}
		defer dbManager.Close()

		// Get service
		service, err := dbManager.GetService(serviceID)
		if err != nil {
			fmt.Printf("Error: Failed to get service: %v\n", err)
			os.Exit(1)
		}

		if service == nil {
			fmt.Printf("Error: Service %d not found\n", serviceID)
			os.Exit(1)
		}

		// Clear restrictions
		service.AcceptedPaymentNetworks = nil
		if err := dbManager.UpdateService(service); err != nil {
			fmt.Printf("Error: Failed to update service: %v\n", err)
			os.Exit(1)
		}

		// Success
		fmt.Println("✓ Payment network restrictions cleared")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("Service: %s (ID: %d)\n", service.Name, service.ID)
		fmt.Println("Networks: All supported networks (auto-detected)")
		fmt.Println()
		fmt.Println("Tip: Use 'remote-network wallet list' to see your configured wallets")
	},
}

// isValidPaymentNetworkCLI validates a payment network identifier
func isValidPaymentNetworkCLI(network string) bool {
	// Must be in format "namespace:reference" (CAIP-2)
	parts := strings.Split(network, ":")
	if len(parts) != 2 {
		return false
	}

	namespace := parts[0]
	reference := parts[1]

	// Namespace and reference must be non-empty
	if namespace == "" || reference == "" {
		return false
	}

	// Valid namespaces
	if namespace != "eip155" && namespace != "solana" {
		return false
	}

	// Reference can be wildcard or specific chain/cluster
	if reference == "*" {
		return true
	}

	// For EVM (eip155), reference should be numeric chain ID
	if namespace == "eip155" {
		_, err := strconv.ParseInt(reference, 10, 64)
		return err == nil
	}

	// For Solana, any non-empty string is valid
	if namespace == "solana" {
		return true
	}

	return true
}

func init() {
	// Register service command
	rootCmd.AddCommand(serviceCmd)

	// Add subcommands
	serviceCmd.AddCommand(serviceListCmd)
	serviceCmd.AddCommand(servicePaymentNetworksCmd)

	// Add payment-networks subcommands
	servicePaymentNetworksCmd.AddCommand(serviceGetNetworksCmd)
	servicePaymentNetworksCmd.AddCommand(serviceSetNetworksCmd)
	servicePaymentNetworksCmd.AddCommand(serviceClearNetworksCmd)
}
