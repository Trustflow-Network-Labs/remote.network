package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "decrypt-service":
		RunDecryptService(args)
	case "query-dht":
		RunQueryDHT(args)
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: go run scripts/main.go <command> [args...]")
	fmt.Println("")
	fmt.Println("Available commands:")
	fmt.Println("  decrypt-service <service_id> <output_dir>")
	fmt.Println("    Decrypt and decompress a data service from the database")
	fmt.Println("    Example: go run scripts/main.go decrypt-service 1 ./decrypted_output")
	fmt.Println("")
	fmt.Println("  query-dht <peer_id>")
	fmt.Println("    Query DHT for a peer's metadata using priority store nodes")
	fmt.Println("    Example: go run scripts/main.go query-dht 0c225d04")
}
