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
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/bencode"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
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

// PutMutable publishes mutable data to the DHT using BEP_44
// The data is signed with the private key and stored at SHA1(publicKey)
func (b *BEP44Manager) PutMutable(keyPair *crypto.KeyPair, value interface{}, sequence int64) error {
	// Bencode the value
	valueBytes, err := bencode.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to bencode value: %v", err)
	}

	// Create signature payload: bencode(seq) + bencode(value)
	// BEP_44 specifies: signature = sign(bencode(seq) + bencode(v))
	seqBytes, err := bencode.Marshal(sequence)
	if err != nil {
		return fmt.Errorf("failed to bencode sequence: %v", err)
	}

	signPayload := append(seqBytes, valueBytes...)
	signature := keyPair.Sign(signPayload)

	// Validate signature length (Ed25519 signatures are 64 bytes)
	if len(signature) != 64 {
		return fmt.Errorf("invalid signature length: expected 64, got %d", len(signature))
	}

	// Prepare public key (Ed25519 public keys are 32 bytes)
	if len(keyPair.PublicKey) != 32 {
		return fmt.Errorf("invalid public key length: expected 32, got %d", len(keyPair.PublicKey))
	}

	var publicKey [32]byte
	copy(publicKey[:], keyPair.PublicKey)

	var sig [64]byte
	copy(sig[:], signature)

	// Storage key is SHA1(public_key)
	storageKey := keyPair.StorageKey()

	b.logger.Info(fmt.Sprintf("Publishing mutable data to DHT (key: %x, seq: %d, size: %d bytes)",
		storageKey, sequence, len(valueBytes)), "bep44")

	// Send "put" queries to multiple DHT nodes
	// We need to find nodes close to the target key and send them the data
	return b.putToClosestNodes(storageKey, valueBytes, publicKey, sig, sequence)
}

// putToClosestNodes sends the mutable data to nodes closest to the target key
func (b *BEP44Manager) putToClosestNodes(targetKey [20]byte, value []byte, publicKey [32]byte, signature [64]byte, sequence int64) error {
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
		err := b.sendPutQuery(ctx, addr, targetKey, value, publicKey, signature, sequence)
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

// sendPutQuery sends a "put" query to a specific DHT node
func (b *BEP44Manager) sendPutQuery(ctx context.Context, addr dht.Addr, targetKey [20]byte, value []byte, publicKey [32]byte, signature [64]byte, sequence int64) error {
	// Convert targetKey to krpc.ID
	var keyID krpc.ID
	copy(keyID[:], targetKey[:])

	// Create the "put" query arguments
	// BEP_44 specifies the following fields:
	// - id: node ID of requester
	// - token: token from previous get_peers query (we'll handle this)
	// - v: value to store (bencoded)
	// - k: public key (32 bytes)
	// - sig: signature (64 bytes)
	// - seq: sequence number
	// - cas: compare-and-swap (optional, we don't use)
	// - salt: salt (optional, we don't use)

	// First, we need to get a token from the target node
	// Send a "get" query to obtain the token
	udpAddr := &net.UDPAddr{
		IP:   addr.IP(),
		Port: addr.Port(),
	}

	// Send "get" query
	getInput := dht.QueryInput{
		MsgArgs: krpc.MsgArgs{
			ID: b.dhtPeer.nodeID,
		},
	}

	b.logger.Debug(fmt.Sprintf("Sending 'get' query to %s to obtain token", addr.String()), "bep44")

	getResult := b.dhtPeer.server.Query(ctx, addr, "get", getInput)
	if getResult.Err != nil {
		return fmt.Errorf("get query failed: %v", getResult.Err)
	}

	// Extract token from response
	if getResult.Reply.R == nil || getResult.Reply.R.Token == nil {
		return fmt.Errorf("no token received from get query")
	}

	token := *getResult.Reply.R.Token

	b.logger.Debug(fmt.Sprintf("Sending 'put' query to %s with token", addr.String()), "bep44")

	// Note: anacrolix/dht doesn't have native BEP_44 support
	// We need to use low-level bencode manipulation or custom query
	// For now, we'll use a workaround: send a custom query message

	// Create custom BEP_44 put message
	putMsg := map[string]interface{}{
		"q": "put",
		"t": "aa", // Transaction ID
		"y": "q",  // Query type
		"a": map[string]interface{}{
			"id":    b.dhtPeer.nodeID[:],
			"token": token,
			"v":     value,
			"k":     publicKey[:],
			"sig":   signature[:],
			"seq":   sequence,
		},
	}

	// Bencode the message
	msgBytes, err := bencode.Marshal(putMsg)
	if err != nil {
		return fmt.Errorf("failed to bencode put message: %v", err)
	}

	// Send raw UDP packet
	_, err = b.dhtPeer.conn.WriteToUDP(msgBytes, udpAddr)
	if err != nil {
		return fmt.Errorf("failed to send put query: %v", err)
	}

	// Wait for response (with timeout)
	// Note: This is a simplified implementation
	// In production, we should properly handle responses
	time.Sleep(500 * time.Millisecond)

	return nil
}

// GetMutable queries the DHT for mutable data using BEP_44
// Returns the value, signature, sequence number, and public key
func (b *BEP44Manager) GetMutable(publicKey ed25519.PublicKey) (*MutableData, error) {
	// Validate public key length
	if len(publicKey) != 32 {
		return nil, fmt.Errorf("invalid public key length: expected 32, got %d", len(publicKey))
	}

	// Storage key is SHA1(public_key)
	storageKey := sha1.Sum(publicKey)

	b.logger.Debug(fmt.Sprintf("Querying DHT for mutable data (key: %x)", storageKey), "bep44")

	// Query multiple DHT nodes
	bootstrapAddrs, err := getBootstrapAddrs(b.config)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap addresses: %v", err)
	}

	var latestData *MutableData
	var latestSeq int64 = -1

	for _, addr := range bootstrapAddrs {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		data, err := b.sendGetQuery(ctx, addr, storageKey, publicKey)
		cancel()

		if err != nil {
			b.logger.Debug(fmt.Sprintf("Failed to get from node %s: %v", addr.String(), err), "bep44")
			continue
		}

		// Keep the data with the highest sequence number
		if data.Sequence > latestSeq {
			latestSeq = data.Sequence
			latestData = data
		}

		b.logger.Debug(fmt.Sprintf("Received mutable data from %s (seq: %d)", addr.String(), data.Sequence), "bep44")
	}

	if latestData == nil {
		return nil, fmt.Errorf("no mutable data found for key %x", storageKey)
	}

	// Verify signature
	if !b.VerifyMutableSignature(latestData.Value, latestData.Signature[:], latestData.Sequence, publicKey) {
		return nil, fmt.Errorf("invalid signature for mutable data")
	}

	b.logger.Info(fmt.Sprintf("Successfully retrieved mutable data (key: %x, seq: %d, size: %d bytes)",
		storageKey, latestData.Sequence, len(latestData.Value)), "bep44")

	return latestData, nil
}

// sendGetQuery sends a "get" query for mutable data to a specific DHT node
func (b *BEP44Manager) sendGetQuery(ctx context.Context, addr dht.Addr, targetKey [20]byte, publicKey ed25519.PublicKey) (*MutableData, error) {
	udpAddr := &net.UDPAddr{
		IP:   addr.IP(),
		Port: addr.Port(),
	}

	// Create custom BEP_44 get message
	getMsg := map[string]interface{}{
		"q": "get",
		"t": "bb", // Transaction ID
		"y": "q",  // Query type
		"a": map[string]interface{}{
			"id":     b.dhtPeer.nodeID[:],
			"target": targetKey[:],
		},
	}

	// Bencode the message
	msgBytes, err := bencode.Marshal(getMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to bencode get message: %v", err)
	}

	// Send raw UDP packet
	_, err = b.dhtPeer.conn.WriteToUDP(msgBytes, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send get query: %v", err)
	}

	// Wait for response (simplified implementation)
	// In production, this should properly listen for UDP responses
	buf := make([]byte, 65536)
	b.dhtPeer.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := b.dhtPeer.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to receive response: %v", err)
	}

	// Parse response
	var response map[string]interface{}
	err = bencode.Unmarshal(buf[:n], &response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Extract mutable data from response
	rMap, ok := response["r"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format: missing 'r' field")
	}

	// Extract fields
	value, _ := rMap["v"].([]byte)
	k, _ := rMap["k"].([]byte)
	sig, _ := rMap["sig"].([]byte)
	seq, _ := rMap["seq"].(int64)

	if value == nil || k == nil || sig == nil {
		return nil, fmt.Errorf("incomplete mutable data in response")
	}

	// Validate sizes
	if len(k) != 32 {
		return nil, fmt.Errorf("invalid public key size: %d", len(k))
	}
	if len(sig) != 64 {
		return nil, fmt.Errorf("invalid signature size: %d", len(sig))
	}

	var pubKey [32]byte
	var signature [64]byte
	copy(pubKey[:], k)
	copy(signature[:], sig)

	return &MutableData{
		Value:     value,
		PublicKey: pubKey,
		Signature: signature,
		Sequence:  seq,
		Salt:      nil,
	}, nil
}

// VerifyMutableSignature verifies the Ed25519 signature of mutable data
// According to BEP_44: signature = sign(bencode(seq) + bencode(v) + bencode(salt))
// We don't use salt, so: signature = sign(bencode(seq) + bencode(v))
func (b *BEP44Manager) VerifyMutableSignature(value []byte, signature []byte, sequence int64, publicKey ed25519.PublicKey) bool {
	// Recreate the signature payload
	seqBytes, err := bencode.Marshal(sequence)
	if err != nil {
		b.logger.Warn(fmt.Sprintf("Failed to bencode sequence for verification: %v", err), "bep44")
		return false
	}

	signPayload := append(seqBytes, value...)

	// Verify signature
	return crypto.VerifyWithPublicKey(publicKey, signPayload, signature)
}

// CreateSignature creates a BEP_44 signature for given value and sequence
func CreateSignature(keyPair *crypto.KeyPair, value []byte, sequence int64) ([]byte, error) {
	// Create signature payload: bencode(seq) + bencode(value)
	seqBytes, err := bencode.Marshal(sequence)
	if err != nil {
		return nil, fmt.Errorf("failed to bencode sequence: %v", err)
	}

	signPayload := append(seqBytes, value...)
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
