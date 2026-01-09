package p2p

import (
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/krpc"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// BEP44Manager handles BEP_44 mutable data operations
type BEP44Manager struct {
	dhtPeer *DHTPeer
	logger  *utils.LogsManager
	config  *utils.ConfigManager
}

// MutableData represents BEP_44 mutable data structure
type MutableData struct {
	Value     []byte   // Bencoded value
	PublicKey [32]byte // Ed25519 public key
	Signature [64]byte // Ed25519 signature
	Sequence  int64    // Sequence number (monotonically increasing)
	Salt      []byte   // Optional salt (we don't use this)
}

// NewBEP44Manager creates a new BEP_44 manager
func NewBEP44Manager(dhtPeer *DHTPeer, logger *utils.LogsManager, config *utils.ConfigManager) *BEP44Manager {
	return &BEP44Manager{
		dhtPeer: dhtPeer,
		logger:  logger,
		config:  config,
	}
}

// PutMutableWithDiscovery publishes mutable data to the DHT with store peer discovery
// Multi-tier PUT strategy:
// 1. Store in local store (if enabled)
// 2. Discover and PUT to known store peers (public peers with -s=true)
// 3. PUT to bootstrap nodes
// 4. PUT to public DHT
func (b *BEP44Manager) PutMutableWithDiscovery(keyPair *crypto.KeyPair, value interface{}, sequence int64, knownPeersManager *KnownPeersManager, topic string) error {
	// Storage key is SHA1(public_key)
	storageKey := keyPair.StorageKey()

	b.logger.Info(fmt.Sprintf("BEP_44 PUT with discovery starting: key=%x, seq=%d, topic=%s",
		storageKey, sequence, topic), "bep44")

	// Convert PeerMetadata to bencode-safe format if needed
	if metadata, ok := value.(*database.PeerMetadata); ok {
		b.logger.Debug("Converting PeerMetadata to bencode-safe format", "bep44")
		value = metadata.ToBencodeSafe()
	}

	// Prepare public key
	if len(keyPair.PublicKey) != 32 {
		return fmt.Errorf("invalid public key length: expected 32, got %d", len(keyPair.PublicKey))
	}

	var publicKey [32]byte
	copy(publicKey[:], keyPair.PublicKey)

	// Tier 1: Store in local store (if we're a store peer)
	localStore := b.dhtPeer.GetStore()
	if localStore != nil {
		err := b.putToLocalStore(storageKey, value, publicKey, sequence, keyPair)
		if err != nil {
			b.logger.Warn(fmt.Sprintf("Failed to store in local cache: %v", err), "bep44")
		} else {
			b.logger.Debug("Stored metadata in local store", "bep44")
		}
	}

	// Tier 2: Discover store peers and PUT to them
	storePeers := b.discoverStorePeers(knownPeersManager, topic, 10)
	storePeerSuccesses := 0

	for _, storePeer := range storePeers {
		b.logger.Debug(fmt.Sprintf("Attempting PUT to store peer %s at %s", storePeer.DHTNodeID[:8], storePeer.Address), "bep44")

		// Parse address
		host, portStr, err := net.SplitHostPort(storePeer.Address)
		if err != nil {
			b.logger.Debug(fmt.Sprintf("Invalid store peer address %s: %v", storePeer.Address, err), "bep44")
			continue
		}

		port, err := net.LookupPort("udp", portStr)
		if err != nil {
			b.logger.Debug(fmt.Sprintf("Invalid port in address %s: %v", storePeer.Address, err), "bep44")
			continue
		}

		ip := net.ParseIP(host)
		if ip == nil {
			b.logger.Debug(fmt.Sprintf("Invalid IP in address %s", storePeer.Address), "bep44")
			continue
		}

		addr := dht.NewAddr(&net.UDPAddr{
			IP:   ip,
			Port: port,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = b.sendPutQuery(ctx, addr, storageKey, value, publicKey, sequence, keyPair)
		cancel()

		if err != nil {
			b.logger.Debug(fmt.Sprintf("Failed to PUT to store peer %s: %v", storePeer.Address, err), "bep44")
			continue
		}

		storePeerSuccesses++
		b.logger.Debug(fmt.Sprintf("Successfully PUT to store peer %s", storePeer.Address), "bep44")
	}

	b.logger.Info(fmt.Sprintf("PUT to store peers: %d/%d succeeded", storePeerSuccesses, len(storePeers)), "bep44")

	// Tier 3+4: PUT to bootstrap nodes and public DHT
	err := b.putToClosestNodes(storageKey, value, publicKey, sequence, keyPair)
	if err != nil {
		// If store peer PUTs succeeded but bootstrap failed, don't fail entirely
		if storePeerSuccesses > 0 {
			b.logger.Warn(fmt.Sprintf("Bootstrap PUT failed but %d store peers succeeded: %v", storePeerSuccesses, err), "bep44")
			return nil
		}
		return fmt.Errorf("PUT failed: %v", err)
	}

	b.logger.Info(fmt.Sprintf("BEP_44 PUT with discovery completed: key=%x, seq=%d", storageKey, sequence), "bep44")
	return nil
}

// PutMutable publishes mutable data to the DHT using BEP_44
// The data is signed with the private key and stored at SHA1(publicKey)
// IMPORTANT: If value is *database.PeerMetadata, it will be converted to bencode-safe format
func (b *BEP44Manager) PutMutable(keyPair *crypto.KeyPair, value interface{}, sequence int64) error {
	// Storage key is SHA1(public_key)
	storageKey := keyPair.StorageKey()

	b.logger.Info(fmt.Sprintf("BEP_44 PUT starting: key=%x, seq=%d, peerID=%s",
		storageKey, sequence, keyPair.PeerID()), "bep44")

	// Convert PeerMetadata to bencode-safe format if needed
	// This prevents issues with time.Time fields that bencode can't handle properly
	if metadata, ok := value.(*database.PeerMetadata); ok {
		b.logger.Debug("Converting PeerMetadata to bencode-safe format", "bep44")
		value = metadata.ToBencodeSafe()
	}

	// Prepare public key (Ed25519 public keys are 32 bytes)
	if len(keyPair.PublicKey) != 32 {
		b.logger.Error(fmt.Sprintf("BEP_44 PUT failed: invalid public key length: expected 32, got %d", len(keyPair.PublicKey)), "bep44")
		return fmt.Errorf("invalid public key length: expected 32, got %d", len(keyPair.PublicKey))
	}

	var publicKey [32]byte
	copy(publicKey[:], keyPair.PublicKey)

	b.logger.Debug(fmt.Sprintf("BEP_44 PUT: creating signed Put item (seq=%d)", sequence), "bep44")

	// Multi-tier PUT strategy:
	// Tier 1: Store in local store (if we're a store peer)
	// Tier 2: PUT to discovered store peers
	// Tier 3: PUT to bootstrap nodes

	// Tier 1: Store in local store
	localStore := b.dhtPeer.GetStore()
	if localStore != nil {
		err := b.putToLocalStore(storageKey, value, publicKey, sequence, keyPair)
		if err != nil {
			b.logger.Warn(fmt.Sprintf("Failed to store in local cache: %v", err), "bep44")
			// Don't fail the entire PUT if local storage fails
		} else {
			b.logger.Debug("Stored metadata in local store", "bep44")
		}
	}

	// Tier 2+3: Send "put" queries to store peers and bootstrap nodes
	// We need to find nodes close to the target key and send them the data
	err := b.putToClosestNodes(storageKey, value, publicKey, sequence, keyPair)
	if err != nil {
		b.logger.Error(fmt.Sprintf("BEP_44 PUT failed: %v", err), "bep44")
		return err
	}

	b.logger.Info(fmt.Sprintf("BEP_44 PUT completed successfully: key=%x, seq=%d", storageKey, sequence), "bep44")
	return nil
}

// putToClosestNodes sends the mutable data to nodes closest to the target key
func (b *BEP44Manager) putToClosestNodes(targetKey [20]byte, value interface{}, publicKey [32]byte, sequence int64, keyPair *crypto.KeyPair) error {
	// Get nodes from routing table
	nodes := b.dhtPeer.server.Nodes()
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes in DHT routing table")
	}

	b.logger.Debug(fmt.Sprintf("Found %d nodes in routing table for put operation", len(nodes)), "bep44")

	// Find closest nodes to target key
	// For simplicity, we'll send to multiple bootstrap nodes and well-known nodes
	bootstrapAddrs, err := getBootstrapAddrs(b.config)
	if err != nil {
		return fmt.Errorf("failed to get bootstrap addresses: %v", err)
	}

	successCount := 0
	targetSuccesses := 5 // Store on at least 5 nodes for redundancy

	for _, addr := range bootstrapAddrs {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := b.sendPutQuery(ctx, addr, targetKey, value, publicKey, sequence, keyPair)
		cancel()

		if err != nil {
			b.logger.Debug(fmt.Sprintf("Failed to put to node %s: %v", addr.String(), err), "bep44")
			continue
		}

		successCount++
		b.logger.Debug(fmt.Sprintf("Successfully stored mutable data on node %s", addr.String()), "bep44")

		if successCount >= targetSuccesses {
			break // Stored on enough nodes
		}
	}

	if successCount == 0 {
		return fmt.Errorf("failed to store mutable data on any DHT node")
	}

	b.logger.Info(fmt.Sprintf("Successfully stored mutable data on %d/%d nodes", successCount, len(bootstrapAddrs)), "bep44")
	return nil
}

// sendPutQuery sends a "put" query to a specific DHT node using Server.Put() API
func (b *BEP44Manager) sendPutQuery(ctx context.Context, addr dht.Addr, targetKey [20]byte, value interface{}, publicKey [32]byte, sequence int64, keyPair *crypto.KeyPair) error {
	b.logger.Debug(fmt.Sprintf("BEP_44 PUT query starting to node %s (target: %x)", addr.String(), targetKey), "bep44")

	// First, get a write token from the target node
	var target krpc.ID
	copy(target[:], targetKey[:])

	getInput := dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			ID:     b.dhtPeer.nodeID,
			Target: target,
		},
	}

	b.logger.Debug(fmt.Sprintf("BEP_44 PUT: Getting write token from %s", addr.String()), "bep44")

	getResult := b.dhtPeer.server.Query(ctx, addr, "get", getInput)
	if getResult.Err != nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 PUT: get token failed from %s: %v", addr.String(), getResult.Err), "bep44")
		return fmt.Errorf("get token failed: %v", getResult.Err)
	}

	// Extract token from response
	if getResult.Reply.R == nil || getResult.Reply.R.Token == nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 PUT: no token received from %s", addr.String()), "bep44")
		return fmt.Errorf("no token received from get query")
	}

	token := string(*getResult.Reply.R.Token)
	b.logger.Debug(fmt.Sprintf("BEP_44 PUT: Received write token from %s (len=%d)", addr.String(), len(token)), "bep44")

	// Extract current sequence from DHT (if exists)
	// This ensures we always increment from the actual DHT state, even after restart
	var dhtSequence int64 = -1
	if getResult.Reply.R != nil && getResult.Reply.R.Seq != nil {
		dhtSequence = *getResult.Reply.R.Seq
		b.logger.Debug(fmt.Sprintf("BEP_44 PUT: DHT has existing data with seq=%d from %s", dhtSequence, addr.String()), "bep44")
	}

	// Calculate final sequence: use max of our sequence and DHT sequence + 1
	// This prevents BEP44 rejection when sequence resets on node restart
	finalSequence := sequence
	if dhtSequence >= 0 {
		if sequence <= dhtSequence {
			finalSequence = dhtSequence + 1
			b.logger.Info(fmt.Sprintf("BEP_44 PUT: Adjusting sequence from %d to %d (DHT has seq=%d) for %s",
				sequence, finalSequence, dhtSequence, addr.String()), "bep44")
		}
	}

	// Create bep44.Put struct with mutable data
	// IMPORTANT: Pass raw value (not pre-bencoded), the library handles bencoding
	put := bep44.Put{
		V:   value,        // Raw value (will be bencoded by the library)
		K:   &publicKey,   // Ed25519 public key (32 bytes)
		Seq: finalSequence, // Use adjusted sequence number
		// Salt and CAS are optional, we don't use them
	}

	// Use Put.Sign() method to create signature with private key
	// This is the correct way - the library handles the signature format internally
	b.logger.Debug(fmt.Sprintf("BEP_44 PUT: Signing Put item with private key (seq=%d)", finalSequence), "bep44")
	put.Sign(keyPair.PrivateKey)

	b.logger.Debug(fmt.Sprintf("BEP_44 PUT: Sending put query to %s (seq=%d, sig=%x...)",
		addr.String(), finalSequence, put.Sig[:8]), "bep44")

	// Use Server.Put() API to send the put query
	// Pass dht.QueryRateLimiting{} for rate limiting (empty struct = no rate limiting)
	putResult := b.dhtPeer.server.Put(ctx, addr, put, token, dht.QueryRateLimiting{})
	if putResult.Err != nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 PUT: put query to %s failed: %v", addr.String(), putResult.Err), "bep44")
		return fmt.Errorf("put query failed: %v", putResult.Err)
	}

	b.logger.Debug(fmt.Sprintf("BEP_44 PUT query to %s completed successfully", addr.String()), "bep44")

	return nil
}

// GetMutable queries the DHT for mutable data using BEP_44 with local cache priority
// Priority order:
// 1. Local store (if we're a store peer)
// 2. Bootstrap nodes and public DHT
// Returns the value, signature, sequence number, and public key
func (b *BEP44Manager) GetMutable(publicKey ed25519.PublicKey) (*MutableData, error) {
	// Validate public key length
	if len(publicKey) != 32 {
		b.logger.Error(fmt.Sprintf("BEP_44 GET failed: invalid public key length: expected 32, got %d", len(publicKey)), "bep44")
		return nil, fmt.Errorf("invalid public key length: expected 32, got %d", len(publicKey))
	}

	// Storage key is SHA1(public_key)
	storageKey := sha1.Sum(publicKey)

	// Tier 1: Check local store first
	item := b.getFromLocalStore(storageKey)
	if item != nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET: Found in local cache (target=%x)", storageKey[:8]), "bep44")
		mutableData := convertItemToMutableData(item)
		if mutableData != nil {
			b.logger.Info(fmt.Sprintf("BEP_44 GET completed from local cache: target=%x, seq=%d", storageKey[:8], mutableData.Sequence), "bep44")
			return mutableData, nil
		}
	}

	// Tier 2: Query multiple DHT nodes
	b.logger.Debug("BEP_44 GET: Not in local cache, querying DHT", "bep44")
	bootstrapAddrs, err := getBootstrapAddrs(b.config)
	if err != nil {
		b.logger.Error(fmt.Sprintf("BEP_44 GET failed: cannot get bootstrap addresses: %v", err), "bep44")
		return nil, fmt.Errorf("failed to get bootstrap addresses: %v", err)
	}

	b.logger.Info(fmt.Sprintf("BEP_44 GET starting: target=%x, querying %d nodes", storageKey, len(bootstrapAddrs)), "bep44")

	var latestData *MutableData
	var latestSeq int64 = -1
	var successCount, failureCount int

	for i, addr := range bootstrapAddrs {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET attempt %d/%d: querying node %s", i+1, len(bootstrapAddrs), addr.String()), "bep44")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		data, err := b.sendGetQuery(ctx, addr, storageKey, publicKey)
		cancel()

		if err != nil {
			failureCount++
			b.logger.Debug(fmt.Sprintf("BEP_44 GET failed from node %s (%d/%d): %v", addr.String(), i+1, len(bootstrapAddrs), err), "bep44")
			continue
		}

		successCount++
		// Keep the data with the highest sequence number
		if data.Sequence > latestSeq {
			latestSeq = data.Sequence
			latestData = data
		}

		b.logger.Debug(fmt.Sprintf("BEP_44 GET success from %s (%d/%d): seq=%d, size=%d bytes",
			addr.String(), i+1, len(bootstrapAddrs), data.Sequence, len(data.Value)), "bep44")
	}

	b.logger.Info(fmt.Sprintf("BEP_44 GET query phase complete: %d successful, %d failed out of %d nodes",
		successCount, failureCount, len(bootstrapAddrs)), "bep44")

	if latestData == nil {
		b.logger.Error(fmt.Sprintf("BEP_44 GET failed: no mutable data found for key %x after querying %d nodes",
			storageKey, len(bootstrapAddrs)), "bep44")
		return nil, fmt.Errorf("no mutable data found for key %x", storageKey)
	}

	// Verify signature
	if !b.VerifyMutableSignature(latestData.Value, latestData.Signature[:], latestData.Sequence, publicKey) {
		b.logger.Error(fmt.Sprintf("BEP_44 GET failed: signature verification failed for key %x", storageKey), "bep44")
		return nil, fmt.Errorf("invalid signature for mutable data")
	}

	b.logger.Info(fmt.Sprintf("BEP_44 GET completed successfully: key=%x, seq=%d, size=%d bytes",
		storageKey, latestData.Sequence, len(latestData.Value)), "bep44")

	return latestData, nil
}

// GetMutableWithPriority queries the DHT for mutable data with prioritized node routing
// Queries priority nodes first (bootstrap nodes, relay nodes), then falls back to general DHT
// Returns the value with the highest sequence number found
func (b *BEP44Manager) GetMutableWithPriority(publicKey ed25519.PublicKey, priorityNodes []string) (*MutableData, error) {
	// Validate public key length
	if len(publicKey) != 32 {
		return nil, fmt.Errorf("invalid public key length: expected 32, got %d", len(publicKey))
	}

	// Storage key is SHA1(public_key)
	storageKey := sha1.Sum(publicKey)

	b.logger.Debug(fmt.Sprintf("Querying DHT for mutable data with priority routing (key: %x, priority_nodes: %d)",
		storageKey, len(priorityNodes)), "bep44")

	var latestData *MutableData
	var latestSeq int64 = -1

	// Phase 1: Query priority nodes first (bootstrap nodes, relay nodes)
	if len(priorityNodes) > 0 {
		b.logger.Debug(fmt.Sprintf("Phase 1: Querying %d priority nodes", len(priorityNodes)), "bep44")

		for _, nodeAddr := range priorityNodes {
			// Parse address (format: "IP:Port")
			udpAddr, err := net.ResolveUDPAddr("udp", nodeAddr)
			if err != nil {
				b.logger.Debug(fmt.Sprintf("Invalid priority node address %s: %v", nodeAddr, err), "bep44")
				continue
			}

			addr := dht.NewAddr(udpAddr)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			data, err := b.sendGetQuery(ctx, addr, storageKey, publicKey)
			cancel()

			if err != nil {
				b.logger.Debug(fmt.Sprintf("Failed to get from priority node %s: %v", nodeAddr, err), "bep44")
				continue
			}

			// Keep the data with the highest sequence number
			if data.Sequence > latestSeq {
				latestSeq = data.Sequence
				latestData = data
				b.logger.Debug(fmt.Sprintf("Found newer data from priority node %s (seq: %d)", nodeAddr, data.Sequence), "bep44")
			}
		}

		// If we found data from priority nodes, return it immediately
		if latestData != nil {
			b.logger.Info(fmt.Sprintf("Found mutable data from priority nodes (seq: %d)", latestSeq), "bep44")

			// Verify signature
			if !b.VerifyMutableSignature(latestData.Value, latestData.Signature[:], latestData.Sequence, publicKey) {
				return nil, fmt.Errorf("invalid signature for mutable data from priority nodes")
			}

			return latestData, nil
		}
	}

	// Phase 2: Fall back to general DHT bootstrap nodes with retry logic
	b.logger.Debug("Phase 2: No data from priority nodes, querying general DHT bootstrap nodes with retry", "bep44")

	bootstrapAddrs, err := getBootstrapAddrs(b.config)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap addresses: %v", err)
	}

	// Retry logic for bootstrap queries (useful during initial DHT join)
	const maxRetries = 2
	const retryDelay = 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			b.logger.Debug(fmt.Sprintf("Bootstrap query retry attempt %d/%d after %v delay", attempt, maxRetries, retryDelay), "bep44")
			time.Sleep(retryDelay)
		}

		for _, addr := range bootstrapAddrs {
			// Longer timeout for bootstrap nodes during initial join (15s instead of 10s)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			data, err := b.sendGetQuery(ctx, addr, storageKey, publicKey)
			cancel()

			if err != nil {
				b.logger.Debug(fmt.Sprintf("Failed to get from bootstrap node %s (attempt %d/%d): %v", addr.String(), attempt+1, maxRetries+1, err), "bep44")
				continue
			}

			// Keep the data with the highest sequence number
			if data.Sequence > latestSeq {
				latestSeq = data.Sequence
				latestData = data
			}

			b.logger.Debug(fmt.Sprintf("Received mutable data from %s (seq: %d, attempt: %d)", addr.String(), data.Sequence, attempt+1), "bep44")
		}

		// If we found data, break retry loop
		if latestData != nil {
			break
		}
	}

	if latestData == nil {
		return nil, fmt.Errorf("no mutable data found for key %x after %d attempts", storageKey, maxRetries+1)
	}

	// Verify signature
	if !b.VerifyMutableSignature(latestData.Value, latestData.Signature[:], latestData.Sequence, publicKey) {
		return nil, fmt.Errorf("invalid signature for mutable data")
	}

	b.logger.Info(fmt.Sprintf("Successfully retrieved mutable data from general DHT (key: %x, seq: %d, size: %d bytes)",
		storageKey, latestData.Sequence, len(latestData.Value)), "bep44")

	return latestData, nil
}

// sendGetQuery sends a "get" query for mutable data to a specific DHT node
// FIXED: Now uses DHT server's Query API instead of raw UDP operations to avoid socket interference
func (b *BEP44Manager) sendGetQuery(ctx context.Context, addr dht.Addr, targetKey [20]byte, publicKey ed25519.PublicKey) (*MutableData, error) {
	// Use DHT server's Query API instead of raw UDP operations
	// This properly handles responses without interfering with the serve loop

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Convert targetKey to krpc.ID for the query
	var target krpc.ID
	copy(target[:], targetKey[:])

	queryInput := dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			Target: target, // BEP_44 uses "target" field
		},
	}

	b.logger.Debug(fmt.Sprintf("BEP_44 GET query to %s: target=%x", addr.String(), targetKey), "bep44")

	result := b.dhtPeer.server.Query(queryCtx, addr, "get", queryInput)
	if result.Err != nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET query to %s failed: %v", addr.String(), result.Err), "bep44")
		return nil, fmt.Errorf("get query failed: %v", result.Err)
	}

	b.logger.Debug(fmt.Sprintf("BEP_44 GET raw response from %s: Reply.R=%+v", addr.String(), result.Reply.R != nil), "bep44")

	// Extract mutable data from response (BEP44Return fields are embedded in Reply.R)
	if result.Reply.R == nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET response from %s: Reply.R is nil", addr.String()), "bep44")
		return nil, fmt.Errorf("invalid response: Reply.R is nil")
	}

	// Log detailed response field status
	b.logger.Debug(fmt.Sprintf("BEP_44 GET response fields from %s: V=%v (len=%d), K=%v, Sig=%v, Seq=%v",
		addr.String(),
		result.Reply.R.V != nil, len(result.Reply.R.V),
		result.Reply.R.K != [32]byte{},
		result.Reply.R.Sig != [64]byte{},
		result.Reply.R.Seq != nil), "bep44")

	if result.Reply.R.Bep44Return.Seq == nil {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET response from %s: missing BEP_44 data (Seq is nil)", addr.String()), "bep44")
		return nil, fmt.Errorf("invalid response: missing BEP_44 data")
	}

	// BEP_44 response fields from Bep44Return struct
	value := result.Reply.R.V      // bencode.Bytes ([]byte)
	k := result.Reply.R.K          // [32]byte
	sig := result.Reply.R.Sig      // [64]byte
	seq := *result.Reply.R.Seq     // *int64 -> int64

	b.logger.Debug(fmt.Sprintf("BEP_44 GET extracted fields from %s: seq=%d, value_len=%d, k=%x..., sig=%x...",
		addr.String(), seq, len(value), k[:8], sig[:8]), "bep44")

	if len(value) == 0 {
		b.logger.Debug(fmt.Sprintf("BEP_44 GET response from %s: incomplete data (value is empty)", addr.String()), "bep44")
		return nil, fmt.Errorf("incomplete mutable data in response: missing value")
	}

	// Verify signature using BEP_44 format: "3:seqi" + seq + "e1:v" + bencoded_value
	// This matches the signature format created by put.Sign() in sendPutQuery
	signPayload := []byte(fmt.Sprintf("3:seqi%de1:v", seq))
	signPayload = append(signPayload, value...)

	b.logger.Debug(fmt.Sprintf("BEP_44 GET verifying signature from %s: signPayload_len=%d", addr.String(), len(signPayload)), "bep44")

	// Convert [32]byte and [64]byte to slices for ed25519.Verify
	if !ed25519.Verify(k[:], signPayload, sig[:]) {
		b.logger.Warn(fmt.Sprintf("BEP_44 GET signature verification FAILED from %s (target: %x)", addr.String(), targetKey), "bep44")
		return nil, fmt.Errorf("signature verification failed")
	}

	// k and sig are already [32]byte and [64]byte, no need to copy
	pubKey := k
	signature := sig

	b.logger.Debug(fmt.Sprintf("BEP_44 GET successfully fetched and verified from %s: seq=%d, size=%d bytes", addr.String(), seq, len(value)), "bep44")

	return &MutableData{
		Value:     value,
		PublicKey: pubKey,
		Signature: signature,
		Sequence:  seq,
		Salt:      nil,
	}, nil
}

// VerifyMutableSignature verifies the Ed25519 signature of mutable data
// According to BEP_44 specification:
// Format: "3:seqi" + seq + "e1:v" + bencoded_value
func (b *BEP44Manager) VerifyMutableSignature(value []byte, signature []byte, sequence int64, publicKey ed25519.PublicKey) bool {
	// Recreate the signature payload using BEP_44 format
	signPayload := []byte(fmt.Sprintf("3:seqi%de1:v", sequence))
	signPayload = append(signPayload, value...)

	// Verify signature
	return crypto.VerifyWithPublicKey(publicKey, signPayload, signature)
}

// CreateSignature creates a BEP_44 signature for given value and sequence
func CreateSignature(keyPair *crypto.KeyPair, value []byte, sequence int64) ([]byte, error) {
	// Create signature payload using BEP_44 format
	// Format: "3:seqi" + seq + "e1:v" + value (value should be bencoded)
	signPayload := []byte(fmt.Sprintf("3:seqi%de1:v", sequence))
	signPayload = append(signPayload, value...)

	signature := keyPair.Sign(signPayload)

	return signature, nil
}

// EncodeSequence encodes a sequence number as bytes (big-endian int64)
func EncodeSequence(seq int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(seq))
	return buf
}

// DecodeSequence decodes a sequence number from bytes
func DecodeSequence(data []byte) int64 {
	if len(data) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(data[:8]))
}

// ============================================================================
// Local Store Helper Methods
// ============================================================================

// storeInLocalStore stores a mutable item in the local BEP_44 store
// This is used for caching metadata retrieved from other peers
func (b *BEP44Manager) storeInLocalStore(item bep44.Item) error {
	store := b.dhtPeer.GetStore()
	if store == nil {
		return fmt.Errorf("local store is disabled")
	}

	target := item.Target()
	b.logger.Debug(fmt.Sprintf("Storing in local store: target=%x, K=%x, seq=%d, is_mutable=%v",
		target[:], item.K[:8], item.Seq, item.K != [32]byte{}), "dht-bep44")

	err := store.Put(&item)
	if err != nil {
		return fmt.Errorf("failed to store in local cache: %v", err)
	}

	b.logger.Debug(fmt.Sprintf("Successfully stored in local store: target=%x", target[:8]), "dht-bep44")
	return nil
}

// getFromLocalStore retrieves a mutable item from the local BEP_44 store
// Returns nil if not found or if store is disabled
func (b *BEP44Manager) getFromLocalStore(target bep44.Target) *bep44.Item {
	store := b.dhtPeer.GetStore()
	if store == nil {
		return nil
	}

	item, err := store.Get(target)
	if err != nil {
		return nil
	}

	b.logger.Debug(fmt.Sprintf("Retrieved metadata from local store for target %x", target[:8]), "dht-bep44")
	return item
}

// convertItemToMutableData converts a bep44.Item to MutableData structure
func convertItemToMutableData(item *bep44.Item) *MutableData {
	if item == nil {
		return nil
	}

	// Extract value as bytes (assuming it's already bencode encoded)
	var valueBytes []byte
	if v, ok := item.V.([]byte); ok {
		valueBytes = v
	} else {
		// If it's not []byte, we need to handle it differently
		// For now, return nil as we expect bencoded bytes
		return nil
	}

	md := &MutableData{
		Value:    valueBytes,
		Sequence: item.Seq,
		Salt:     item.Salt,
	}

	// Copy public key
	copy(md.PublicKey[:], item.K[:])

	// Copy signature
	copy(md.Signature[:], item.Sig[:])

	return md
}

// ============================================================================
// Store Peer Discovery
// ============================================================================

// StorePeerInfo contains information about a store peer
type StorePeerInfo struct {
	DHTNodeID string // DHT node ID (40 hex chars)
	PublicKey []byte // Ed25519 public key
	Address   string // IP:Port address for direct PUT
	IsRelay   bool   // Is this peer also a relay?
}

// discoverStorePeers discovers which known peers have BEP_44 storage enabled
// and whose metadata can be retrieved from DHT. This is used during PUT operations
// to find additional targets beyond bootstrap nodes.
//
// The function:
// 1. Queries known_peers database for peers with is_store=true
// 2. For public peers: uses PublicIP:30609
// 3. Excludes NAT peers (even if is_store=true) as they can't receive direct PUTs
// 4. Returns best-effort list (skips unreachable peers)
func (b *BEP44Manager) discoverStorePeers(knownPeersManager *KnownPeersManager, topic string, maxPeers int) []*StorePeerInfo {
	if maxPeers <= 0 {
		maxPeers = 10 // Default
	}

	// Query database for store peers
	knownPeers, err := knownPeersManager.GetAllKnownPeers()
	if err != nil {
		b.logger.Warn(fmt.Sprintf("Failed to query known_peers: %v", err), "dht-bep44")
		return nil
	}

	var storePeers []*StorePeerInfo
	for _, peer := range knownPeers {
		// Filter by topic
		if peer.Topic != topic {
			continue
		}

		// Skip if not a store peer
		if !peer.IsStore {
			continue
		}

		// For store peers, we need to get their metadata to find their PublicIP
		// and verify they are public (not NAT) nodes
		// NAT peers cannot receive direct PUTs even with -s=true
		metadata := b.getMetadataForPeer(peer.PublicKey)
		if metadata == nil {
			b.logger.Debug(fmt.Sprintf("Could not retrieve metadata for store peer %s", peer.PeerID[:8]), "dht-bep44")
			continue
		}

		// Only accept public nodes (node_type == "public")
		// NAT peers cannot receive direct connections
		if metadata.NetworkInfo.NodeType != "public" {
			b.logger.Debug(fmt.Sprintf("Skipping NAT store peer %s (type: %s)", peer.PeerID[:8], metadata.NetworkInfo.NodeType), "dht-bep44")
			continue
		}

		// Verify we have a public IP
		if metadata.NetworkInfo.PublicIP == "" {
			b.logger.Debug(fmt.Sprintf("Store peer %s has no PublicIP", peer.PeerID[:8]), "dht-bep44")
			continue
		}

		// Use PublicIP:30609 for public peers
		address := fmt.Sprintf("%s:30609", metadata.NetworkInfo.PublicIP)

		storePeers = append(storePeers, &StorePeerInfo{
			DHTNodeID: peer.DHTNodeID,
			PublicKey: peer.PublicKey,
			Address:   address,
			IsRelay:   peer.IsRelay,
		})

		if len(storePeers) >= maxPeers {
			break
		}
	}

	b.logger.Info(fmt.Sprintf("Discovered %d store peers for topic %s", len(storePeers), topic), "dht-bep44")
	return storePeers
}

// getMetadataForPeer retrieves metadata for a peer from DHT using multi-tier strategy
// Priority order:
// 1. Local store (if we have it cached)
// 2. Known store peers (query them directly)
// 3. Bootstrap nodes
// 4. Public DHT
// Returns nil if metadata cannot be retrieved
func (b *BEP44Manager) getMetadataForPeer(publicKey []byte) *database.PeerMetadata {
	if len(publicKey) != 32 {
		return nil
	}

	// Calculate storage target (SHA1 of public key)
	target := sha1.Sum(publicKey)

	// Tier 1: Try local store first
	item := b.getFromLocalStore(target)
	if item != nil {
		b.logger.Debug(fmt.Sprintf("Retrieved peer metadata from local cache (target: %x)", target[:8]), "dht-bep44")
		return b.decodeMetadataFromItem(item)
	}

	// Tier 2: Try known store peers (skipped here to avoid circular dependency during discovery)
	// This would require querying store peers, but we're calling this FROM discoverStorePeers
	// So we skip directly to DHT query

	// Tier 3+4: Perform DHT GET (will query bootstrap nodes and public DHT)
	b.logger.Debug(fmt.Sprintf("Metadata not in cache, performing DHT GET (target: %x)", target[:8]), "dht-bep44")

	// Use the existing GetMutable method which queries bootstrap nodes and DHT
	mutableData, err := b.GetMutable(publicKey)
	if err != nil {
		b.logger.Debug(fmt.Sprintf("DHT GET failed for peer metadata: %v", err), "dht-bep44")
		return nil
	}

	if mutableData != nil && mutableData.Value != nil {
		// Decode the metadata
		metadata, err := database.DecodeBencodedMetadata(mutableData.Value)
		if err != nil {
			b.logger.Debug(fmt.Sprintf("Failed to decode peer metadata: %v", err), "dht-bep44")
			return nil
		}

		// Cache in local store
		// Create bep44.Item for caching
		item := bep44.Item{
			V:   mutableData.Value,
			K:   mutableData.PublicKey,
			Sig: mutableData.Signature,
			Seq: mutableData.Sequence,
		}
		b.cacheMetadataInLocalStore(item)

		return metadata
	}

	return nil
}

// cacheMetadataInLocalStore caches a retrieved metadata item in local store
func (b *BEP44Manager) cacheMetadataInLocalStore(item bep44.Item) {
	err := b.storeInLocalStore(item)
	if err != nil {
		b.logger.Debug(fmt.Sprintf("Failed to cache metadata: %v", err), "dht-bep44")
	}
}

// putToLocalStore stores mutable data in the local BEP_44 store
func (b *BEP44Manager) putToLocalStore(targetKey [20]byte, value interface{}, publicKey [32]byte, sequence int64, keyPair *crypto.KeyPair) error {
	// Create Put object
	put := bep44.Put{
		V:   value,      // Raw value (will be bencoded by the library)
		K:   &publicKey, // Ed25519 public key
		Seq: sequence,   // Sequence number
	}

	// Sign it
	put.Sign(keyPair.PrivateKey)

	// Create bep44.Item
	item := bep44.Item{
		V:   put.V,
		K:   publicKey,
		Sig: put.Sig,
		Seq: sequence,
	}

	// Store in local store
	return b.storeInLocalStore(item)
}

// decodeMetadataFromItem decodes PeerMetadata from a bep44.Item
func (b *BEP44Manager) decodeMetadataFromItem(item *bep44.Item) *database.PeerMetadata {
	if item == nil {
		return nil
	}

	// Extract bencoded value
	var valueBytes []byte
	if v, ok := item.V.([]byte); ok {
		valueBytes = v
	} else {
		b.logger.Debug("Item value is not []byte", "dht-bep44")
		return nil
	}

	// Decode bencode to PeerMetadata
	metadata, err := database.DecodeBencodedMetadata(valueBytes)
	if err != nil {
		b.logger.Debug(fmt.Sprintf("Failed to decode metadata: %v", err), "dht-bep44")
		return nil
	}

	return metadata
}
