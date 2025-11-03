package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	_ "modernc.org/sqlite"
)

func RunQueryDHT(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: go run main.go query-dht <peer_id>")
		fmt.Println("Example: go run main.go query-dht 0c225d04")
		fmt.Println("")
		fmt.Println("This tool queries DHT for a peer's metadata using priority store nodes.")
		fmt.Println("It will show the latest sequence version and decode the metadata.")
		os.Exit(1)
	}

	peerIDShort := args[0]

	// Get paths
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(homeDir, "Library/Application Support/remote-network/remote-network.db")
	configPath := filepath.Join(homeDir, "Library/Application Support/remote-network/configs")

	// Connect to database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get peer public key and details from known_peers
	var publicKeyHex, dhtNodeID, peerID string
	var isStore int
	err = db.QueryRow(`
		SELECT hex(public_key), dht_node_id, peer_id, is_store
		FROM known_peers
		WHERE dht_node_id LIKE ? OR peer_id LIKE ?
		LIMIT 1
	`, peerIDShort+"%", peerIDShort+"%").Scan(&publicKeyHex, &dhtNodeID, &peerID, &isStore)
	if err != nil {
		fmt.Printf("Failed to find peer %s in known_peers: %v\n", peerIDShort, err)
		fmt.Println("\nTip: Make sure the peer ID is in your known_peers database")
		os.Exit(1)
	}

	publicKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		fmt.Printf("Failed to decode public key: %v\n", err)
		os.Exit(1)
	}

	publicKey := ed25519.PublicKey(publicKeyBytes)

	fmt.Printf("=== DHT Metadata Query ===\n")
	fmt.Printf("DHT Node ID: %s\n", dhtNodeID)
	fmt.Printf("Peer ID: %s\n", peerID)
	fmt.Printf("Public Key: %s\n", publicKeyHex)
	fmt.Printf("Is Store Peer: %v\n", isStore == 1)
	fmt.Printf("\n")

	// Initialize config
	config := utils.NewConfigManager(configPath)

	// Initialize logger
	logger := utils.NewLogsManager(config)

	// Initialize DHT
	dhtPeer, err := p2p.NewDHTPeer(config, logger)
	if err != nil {
		fmt.Printf("Failed to create DHT peer: %v\n", err)
		os.Exit(1)
	}

	// Start DHT
	if err := dhtPeer.Start(); err != nil {
		fmt.Printf("Failed to start DHT: %v\n", err)
		os.Exit(1)
	}
	defer dhtPeer.Stop()

	// Wait for DHT to bootstrap
	fmt.Println("Bootstrapping DHT (waiting 5 seconds)...")
	time.Sleep(5 * time.Second)

	// Create BEP44 manager
	bep44Manager := p2p.NewBEP44Manager(dhtPeer, logger, config)

	// Get store nodes for priority queries
	storeNodes, err := getStoreNodes(db)
	if err != nil {
		fmt.Printf("Warning: Failed to get store nodes: %v\n", err)
		storeNodes = []string{}
	}

	fmt.Printf("Querying DHT with %d priority store nodes...\n\n", len(storeNodes))

	// Query with priority routing (like the actual code does)
	var mutableData *p2p.MutableData
	if len(storeNodes) > 0 {
		mutableData, err = bep44Manager.GetMutableWithPriority(publicKey, storeNodes)
	} else {
		mutableData, err = bep44Manager.GetMutable(publicKey)
	}

	if err != nil {
		fmt.Printf("❌ DHT query failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Found metadata: seq=%d, size=%d bytes\n\n", mutableData.Sequence, len(mutableData.Value))

	// Decode metadata
	metadata, err := decodeMetadata(mutableData.Value)
	if err != nil {
		fmt.Printf("❌ Failed to decode metadata: %v\n", err)
		fmt.Printf("\nRaw data (first 200 bytes):\n%s\n", string(mutableData.Value[:min(200, len(mutableData.Value))]))
		os.Exit(1)
	}

	// Display decoded metadata
	displayMetadata(metadata, mutableData.Sequence)

	// Check for issues
	fmt.Printf("\n=== Validation ===\n")
	if metadata.NetworkInfo.NodeType == "private" && !metadata.NetworkInfo.UsingRelay {
		fmt.Printf("⚠️  WARNING: NAT peer without relay info!\n")
		fmt.Printf("   This peer cannot be reached by other peers.\n")
	} else if metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.RelayAddress == "" {
		fmt.Printf("❌ ERROR: UsingRelay=true but RelayAddress is empty!\n")
		fmt.Printf("   This is a critical bug - metadata is corrupted.\n")
	} else if metadata.NetworkInfo.UsingRelay {
		fmt.Printf("✅ NAT peer with relay: %s\n", metadata.NetworkInfo.RelayAddress)
	} else if metadata.NetworkInfo.IsRelay {
		fmt.Printf("✅ Relay node: %s\n", metadata.NetworkInfo.RelayEndpoint)
	} else {
		fmt.Printf("✅ Public peer: %s:%d\n", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
	}
}

func getStoreNodes(db *sql.DB) ([]string, error) {
	// Query database directly for store peer addresses
	// Note: known_peers doesn't have private_ip column, but we can derive from peer_id network
	// For now, we'll use the dht_node_id and assume they're reachable
	// In production, you'd need a separate table with IP addresses

	// Since we don't have IPs in known_peers, let's just return empty list
	// The BEP44 manager will query the DHT network directly
	return []string{}, nil
}

func decodeMetadata(data []byte) (*database.PeerMetadata, error) {
	// DHT metadata is stored in bencode format, not JSON
	return database.DecodeBencodedMetadata(data)
}

func displayMetadata(metadata *database.PeerMetadata, seq int64) {
	fmt.Printf("=== Decoded Metadata (seq=%d) ===\n", seq)
	fmt.Printf("Node ID: %s\n", metadata.NodeID)
	fmt.Printf("Peer ID: %s\n", metadata.PeerID)
	fmt.Printf("Topic: %s\n", metadata.Topic)
	fmt.Printf("Version: %d\n", metadata.Version)
	fmt.Printf("Timestamp: %s\n", metadata.Timestamp.Format(time.RFC3339))
	fmt.Printf("\nNetwork Info:\n")
	fmt.Printf("  Public IP: %s:%d\n", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)
	fmt.Printf("  Private IP: %s:%d\n", metadata.NetworkInfo.PrivateIP, metadata.NetworkInfo.PrivatePort)
	fmt.Printf("  Node Type: %s\n", metadata.NetworkInfo.NodeType)
	fmt.Printf("  Is Relay: %v\n", metadata.NetworkInfo.IsRelay)

	if metadata.NetworkInfo.IsRelay {
		fmt.Printf("  Relay Endpoint: %s\n", metadata.NetworkInfo.RelayEndpoint)
		fmt.Printf("  Relay Pricing: %d micro-units/GB\n", metadata.NetworkInfo.RelayPricing)
		fmt.Printf("  Relay Capacity: %d sessions\n", metadata.NetworkInfo.RelayCapacity)
		fmt.Printf("  Reputation Score: %d basis points\n", metadata.NetworkInfo.ReputationScore)
	}

	fmt.Printf("  Using Relay: %v\n", metadata.NetworkInfo.UsingRelay)
	if metadata.NetworkInfo.UsingRelay {
		fmt.Printf("  Connected Relay: %s\n", metadata.NetworkInfo.ConnectedRelay)
		fmt.Printf("  Relay Session ID: %s\n", metadata.NetworkInfo.RelaySessionID)
		fmt.Printf("  Relay Address: %s\n", metadata.NetworkInfo.RelayAddress)
		if metadata.NetworkInfo.RelayAddress == "" {
			fmt.Printf("  ❌ ERROR: RelayAddress is EMPTY!\n")
		}
	}

	fmt.Printf("\nServices:\n")
	fmt.Printf("  Files (DATA): %d\n", metadata.FilesCount)
	fmt.Printf("  Apps (DOCKER+STANDALONE): %d\n", metadata.AppsCount)

	if len(metadata.Capabilities) > 0 {
		fmt.Printf("\nCapabilities: %v\n", metadata.Capabilities)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
