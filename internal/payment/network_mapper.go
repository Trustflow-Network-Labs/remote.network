package payment

import "fmt"

// NetworkMapper converts between CAIP-2 network identifiers and facilitator-specific network names
type NetworkMapper struct {
	caip2ToPayAI map[string]string
	payAIToCaip2 map[string]string
}

// NewNetworkMapper creates a new network mapper
func NewNetworkMapper() *NetworkMapper {
	// Mapping from CAIP-2 format to PayAI network names
	caip2ToPayAI := map[string]string{
		// Base networks
		"eip155:8453":  "base",
		"eip155:84532": "base-sepolia",

		// Ethereum networks
		"eip155:1":     "ethereum",
		"eip155:11155111": "ethereum-sepolia",

		// Polygon networks
		"eip155:137":   "polygon",
		"eip155:80002": "polygon-amoy",

		// Avalanche networks
		"eip155:43114": "avalanche",
		"eip155:43113": "avalanche-fuji",

		// Solana networks (full CAIP-2 format)
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": "solana",
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": "solana-devnet",
		"solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z":  "solana-testnet",

		// Solana networks (aliases for backward compatibility)
		"solana:mainnet-beta": "solana",
		"solana:mainnet":      "solana",
		"solana:devnet":       "solana-devnet",
		"solana:testnet":      "solana-testnet",
	}

	// Create reverse mapping (use canonical CAIP-2 format only, not aliases)
	payAIToCaip2 := map[string]string{
		// Base networks
		"base":         "eip155:8453",
		"base-sepolia": "eip155:84532",

		// Ethereum networks
		"ethereum":         "eip155:1",
		"ethereum-sepolia": "eip155:11155111",

		// Polygon networks
		"polygon":       "eip155:137",
		"polygon-amoy":  "eip155:80002",

		// Avalanche networks
		"avalanche":      "eip155:43114",
		"avalanche-fuji": "eip155:43113",

		// Solana networks (canonical CAIP-2 format only)
		"solana":         "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
		"solana-devnet":  "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1",
		"solana-testnet": "solana:4uhcVJyU9pJkvQyS88uRDiswHXSCkY3z",
	}

	return &NetworkMapper{
		caip2ToPayAI: caip2ToPayAI,
		payAIToCaip2: payAIToCaip2,
	}
}

// ToPayAI converts CAIP-2 network identifier to PayAI network name
func (nm *NetworkMapper) ToPayAI(caip2Network string) (string, error) {
	if payaiNetwork, ok := nm.caip2ToPayAI[caip2Network]; ok {
		return payaiNetwork, nil
	}
	return "", fmt.Errorf("unsupported network for PayAI: %s", caip2Network)
}

// ToCaip2 converts PayAI network name to CAIP-2 network identifier
func (nm *NetworkMapper) ToCaip2(payaiNetwork string) (string, error) {
	if caip2Network, ok := nm.payAIToCaip2[payaiNetwork]; ok {
		return caip2Network, nil
	}
	return "", fmt.Errorf("unknown PayAI network: %s", payaiNetwork)
}

// IsSupported checks if a CAIP-2 network is supported by PayAI
func (nm *NetworkMapper) IsSupported(caip2Network string) bool {
	_, ok := nm.caip2ToPayAI[caip2Network]
	return ok
}

// GetSupportedNetworks returns all supported CAIP-2 networks
func (nm *NetworkMapper) GetSupportedNetworks() []string {
	networks := make([]string, 0, len(nm.caip2ToPayAI))
	for caip2 := range nm.caip2ToPayAI {
		networks = append(networks, caip2)
	}
	return networks
}
