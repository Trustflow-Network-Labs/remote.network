package p2p

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/quic-go/quic-go"
)

// IdentityExchanger handles Phase 3 identity and known peers exchange
type IdentityExchanger struct {
	keyPair          *crypto.KeyPair
	dhtNodeID        string // DHT routing node ID
	dbManager        *database.SQLiteManager
	logger           *utils.LogsManager
	config           *utils.ConfigManager
	metadataFetcher  *MetadataFetcher // For fetching service counts from DHT
	ourNodeType      string           // "public" or "private"
	isRelay          bool             // Are we offering relay services?
	isStore          bool             // Has BEP_44 storage enabled?
}

// NewIdentityExchanger creates a new identity exchanger
func NewIdentityExchanger(
	keyPair *crypto.KeyPair,
	dhtNodeID string,
	dbManager *database.SQLiteManager,
	logger *utils.LogsManager,
	config *utils.ConfigManager,
) *IdentityExchanger {
	// Determine our node type
	nodeTypeManager := utils.NewNodeTypeManager()
	isPublic, _ := nodeTypeManager.IsPublicNode()

	nodeType := "private"
	if isPublic {
		nodeType = "public"
	}

	// Check if we're offering relay services
	isRelay := config.GetConfigBool("relay_mode", false)

	// Check if we have BEP_44 storage enabled (default: true)
	// Use same config key as dht.go to ensure consistency
	isStore := config.GetConfigBool("enable_bep44_store", true)

	return &IdentityExchanger{
		keyPair:     keyPair,
		dhtNodeID:   dhtNodeID,
		dbManager:   dbManager,
		logger:      logger,
		config:      config,
		ourNodeType: nodeType,
		isRelay:     isRelay,
		isStore:     isStore,
	}
}

// SetMetadataFetcher sets the metadata fetcher (called after initialization)
func (ie *IdentityExchanger) SetMetadataFetcher(fetcher *MetadataFetcher) {
	ie.metadataFetcher = fetcher
}

// PerformHandshake performs the complete identity + known peers exchange
// This is called immediately after QUIC connection is established
//
func (ie *IdentityExchanger) PerformHandshake(stream *quic.Stream, topic string, remoteAddr string) (*database.KnownPeer, error) {
	ie.logger.Debug("Starting Phase 3 handshake (identity + peers exchange)", "identity-exchange")

	// Step 1: Exchange identities
	remotePeer, err := ie.exchangeIdentities(stream, topic, remoteAddr) //nolint:copylocks
	if err != nil {
		return nil, fmt.Errorf("identity exchange failed: %v", err)
	}

	ie.logger.Info(fmt.Sprintf("Identity exchanged with peer %s (type: %s)",
		remotePeer.PeerID[:8], remotePeer.Source), "identity-exchange")

	// Step 2: Exchange known peers lists
	if err := ie.exchangeKnownPeers(stream, topic, remotePeer.PeerID); err != nil { //nolint:copylocks
		ie.logger.Warn(fmt.Sprintf("Known peers exchange failed: %v", err), "identity-exchange")
		// Don't fail handshake if peer exchange fails
	}

	return remotePeer, nil
}

func (ie *IdentityExchanger) exchangeIdentities(stream *quic.Stream, topic string, remoteAddr string) (*database.KnownPeer, error) {
	// Send our identity
	ourIdentity := CreateIdentityExchange(
		ie.keyPair.PeerID(),
		ie.dhtNodeID,
		ie.keyPair.PublicKeyBytes(),
		ie.ourNodeType,
		ie.isRelay,
		ie.isStore,
		topic,
	)

	if err := ie.sendMessage(stream, ourIdentity); err != nil { //nolint:copylocks
		return nil, fmt.Errorf("failed to send our identity: %v", err)
	}

	ie.logger.Debug(fmt.Sprintf("Sent our identity (peer_id: %s, type: %s, relay: %v, store: %v)",
		ie.keyPair.PeerID()[:8], ie.ourNodeType, ie.isRelay, ie.isStore), "identity-exchange")

	// Receive remote identity
	remoteMsg, err := ie.receiveMessage(stream, 5*time.Second) //nolint:copylocks
	if err != nil {
		return nil, fmt.Errorf("failed to receive remote identity: %v", err)
	}

	if remoteMsg.Type != MessageTypeIdentityExchange {
		return nil, fmt.Errorf("unexpected message type: %s (expected identity_exchange)", remoteMsg.Type)
	}

	// Parse identity data
	var remoteIdentity IdentityExchangeData
	dataBytes, err := json.Marshal(remoteMsg.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal identity data: %v", err)
	}

	if err := json.Unmarshal(dataBytes, &remoteIdentity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal identity data: %v", err)
	}

	// Validate identity
	if err := ie.validateIdentity(&remoteIdentity); err != nil {
		return nil, fmt.Errorf("identity validation failed: %v", err)
	}

	ie.logger.Debug(fmt.Sprintf("Received valid identity from peer %s (type: %s, relay: %v, store: %v)",
		remoteIdentity.PeerID[:8], remoteIdentity.NodeType, remoteIdentity.IsRelay, remoteIdentity.IsStore), "identity-exchange")

	// Check if this is ourselves (shouldn't happen, but guard against it)
	if remoteIdentity.PeerID == ie.keyPair.PeerID() {
		ie.logger.Warn("Received our own identity from connection, skipping storage", "identity-exchange")
		return nil, fmt.Errorf("received our own identity")
	}

	// Store peer identity in known_peers database
	// Note: Service counts are set to 0 initially and will be updated separately if DHT fetch succeeds
	knownPeer := &database.KnownPeer{
		PeerID:     remoteIdentity.PeerID,
		DHTNodeID:  remoteIdentity.DHTNodeID,
		PublicKey:  remoteIdentity.PublicKey,
		IsRelay:    remoteIdentity.IsRelay,
		IsStore:    remoteIdentity.IsStore,
		FilesCount: 0, // Default to 0, will be updated if DHT fetch succeeds
		AppsCount:  0, // Default to 0, will be updated if DHT fetch succeeds
		Topic:      topic,
		Source:     "identity_exchange",
	}

	if err := ie.dbManager.KnownPeers.StoreKnownPeer(knownPeer); err != nil {
		return nil, fmt.Errorf("failed to store known peer: %v", err)
	}

	ie.logger.Info(fmt.Sprintf("Stored peer %s in known_peers database (relay: %v, store: %v)",
		remoteIdentity.PeerID[:8], remoteIdentity.IsRelay, remoteIdentity.IsStore), "identity-exchange")

	// Try to fetch and update service counts from DHT metadata (best effort)
	// This is done after storing the peer identity to avoid blocking identity exchange
	if ie.metadataFetcher != nil {
		metadata, err := ie.metadataFetcher.GetPeerMetadata(remoteIdentity.PublicKey)
		if err == nil && metadata != nil {
			// Successfully fetched metadata, update service counts
			if err := ie.dbManager.KnownPeers.UpdatePeerServiceCounts(
				remoteIdentity.PeerID,
				topic,
				metadata.FilesCount,
				metadata.AppsCount,
			); err != nil {
				ie.logger.Warn(fmt.Sprintf("Failed to update service counts for peer %s: %v",
					remoteIdentity.PeerID[:8], err), "identity-exchange")
			} else {
				ie.logger.Debug(fmt.Sprintf("Updated service counts from DHT for peer %s (files: %d, apps: %d)",
					remoteIdentity.PeerID[:8], metadata.FilesCount, metadata.AppsCount), "identity-exchange")
				// Update the knownPeer struct for return value
				knownPeer.FilesCount = metadata.FilesCount
				knownPeer.AppsCount = metadata.AppsCount
			}
		} else {
			ie.logger.Debug(fmt.Sprintf("Could not fetch service counts from DHT for peer %s: %v (will be updated later by PeerValidator)",
				remoteIdentity.PeerID[:8], err), "identity-exchange")
		}
	}

	// Register store node with MetadataFetcher for priority DHT queries
	if remoteIdentity.IsStore && ie.metadataFetcher != nil {
		// Extract IP from remoteAddr and construct DHT address
		// remoteAddr format is "IP:PORT", we need "IP:DHT_PORT"
		host := remoteAddr
		if lastColon := len(remoteAddr) - 1; lastColon > 0 {
			for i := len(remoteAddr) - 1; i >= 0; i-- {
				if remoteAddr[i] == ':' {
					host = remoteAddr[:i]
					break
				}
			}
		}

		// Get DHT port from config
		dhtPort := ie.config.GetConfigInt("dht_port", 30609, 1024, 65535)
		peerDHTAddr := fmt.Sprintf("%s:%d", host, dhtPort)

		ie.logger.Info(fmt.Sprintf("Registering DHT store node for priority queries (peer_id: %s, dht_addr: %s)",
			remoteIdentity.PeerID[:8], peerDHTAddr), "identity-exchange")

		ie.metadataFetcher.AddStoreNode(peerDHTAddr)
	}

	return knownPeer, nil
}

//nolint:copylocks // quic.Stream is an interface, safe to pass by value
func (ie *IdentityExchanger) exchangeKnownPeers(stream *quic.Stream, topic, remotePeerID string) error {
	// Get our known peers to share (excluding the peer we're talking to)
	exclude := []string{remotePeerID, ie.keyPair.PeerID()} // Exclude them and ourselves

	ourPeers, err := ie.selectPeersToShare(topic, 50, exclude)
	if err != nil {
		return fmt.Errorf("failed to select peers to share: %v", err)
	}

	ie.logger.Debug(fmt.Sprintf("Selected %d peers to share with %s", len(ourPeers), remotePeerID[:8]), "identity-exchange")

	// Send our known peers (bidirectional exchange)
	// We send a response message directly with our peers
	response := CreateKnownPeersResponse(topic, ourPeers, len(ourPeers))
	if err := ie.sendMessage(stream, response); err != nil { //nolint:copylocks
		return fmt.Errorf("failed to send our known peers: %v", err)
	}

	ie.logger.Debug(fmt.Sprintf("Sent %d known peers to %s", len(ourPeers), remotePeerID[:8]), "identity-exchange")

	// Receive remote known peers
	remoteMsg, err := ie.receiveMessage(stream, 10*time.Second) //nolint:copylocks
	if err != nil {
		return fmt.Errorf("failed to receive remote known peers: %v", err)
	}

	if remoteMsg.Type != MessageTypeKnownPeersResponse {
		return fmt.Errorf("unexpected message type: %s (expected known_peers_response)", remoteMsg.Type)
	}

	// Parse known peers response
	var peersResponse KnownPeersResponseData
	dataBytes, err := json.Marshal(remoteMsg.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal peers data: %v", err)
	}

	if err := json.Unmarshal(dataBytes, &peersResponse); err != nil {
		return fmt.Errorf("failed to unmarshal peers data: %v", err)
	}

	ie.logger.Debug(fmt.Sprintf("Received %d known peers from %s (total: %d)",
		len(peersResponse.Peers), remotePeerID[:8], peersResponse.Count), "identity-exchange")

	// Store received peers
	stored := ie.storeReceivedPeers(peersResponse.Peers, topic)

	ie.logger.Info(fmt.Sprintf("Stored %d new peers from exchange with %s", stored, remotePeerID[:8]), "identity-exchange")

	return nil
}

// selectPeersToShare selects known peers to share (recent, exclude list)
func (ie *IdentityExchanger) selectPeersToShare(topic string, limit int, exclude []string) ([]*KnownPeerEntry, error) {
	// Get recent known peers from database
	knownPeers, err := ie.dbManager.KnownPeers.GetRecentKnownPeers(limit+len(exclude), topic)
	if err != nil {
		return nil, err
	}

	// Create exclusion map
	excludeMap := make(map[string]bool)
	for _, peerID := range exclude {
		excludeMap[peerID] = true
	}

	// Filter and convert to KnownPeerEntry
	var entries []*KnownPeerEntry
	for _, peer := range knownPeers {
		if excludeMap[peer.PeerID] {
			continue // Skip excluded peers
		}

		if len(peer.PublicKey) == 0 {
			continue // Skip peers without public key (migrated data)
		}

		entries = append(entries, &KnownPeerEntry{
			PeerID:    peer.PeerID,
			DHTNodeID: peer.DHTNodeID,
			PublicKey: peer.PublicKey,
			IsRelay:   peer.IsRelay,
			IsStore:   peer.IsStore,
		})

		if len(entries) >= limit {
			break
		}
	}

	return entries, nil
}

// storeReceivedPeers stores peers received from exchange
func (ie *IdentityExchanger) storeReceivedPeers(peers []*KnownPeerEntry, topic string) int {
	stored := 0

	for _, entry := range peers {
		// Validate peer entry
		if entry.PeerID == "" || len(entry.PublicKey) == 0 {
			ie.logger.Debug("Skipping invalid peer entry (missing peer_id or public_key)", "identity-exchange")
			continue
		}

		// Skip ourselves
		if entry.PeerID == ie.keyPair.PeerID() {
			continue
		}

		// Verify peer_id matches public_key
		derivedPeerID := crypto.DerivePeerID(entry.PublicKey)
		if derivedPeerID != entry.PeerID {
			ie.logger.Warn(fmt.Sprintf("Peer ID mismatch: claimed %s, derived %s",
				entry.PeerID[:8], derivedPeerID[:8]), "identity-exchange")
			continue
		}

		// Store in database (with default 0 service counts)
		knownPeer := &database.KnownPeer{
			PeerID:     entry.PeerID,
			DHTNodeID:  entry.DHTNodeID,
			PublicKey:  entry.PublicKey,
			IsRelay:    entry.IsRelay,
			IsStore:    entry.IsStore,
			FilesCount: 0, // Default to 0, will be updated if DHT fetch succeeds
			AppsCount:  0, // Default to 0, will be updated if DHT fetch succeeds
			Topic:      topic,
			Source:     "peer_exchange",
		}

		if err := ie.dbManager.KnownPeers.StoreKnownPeer(knownPeer); err != nil {
			ie.logger.Debug(fmt.Sprintf("Failed to store peer %s: %v", entry.PeerID[:8], err), "identity-exchange")
			continue
		}

		// Try to fetch and update service counts from DHT metadata (best effort)
		if ie.metadataFetcher != nil {
			metadata, err := ie.metadataFetcher.GetPeerMetadata(entry.PublicKey)
			if err == nil && metadata != nil {
				// Successfully fetched metadata, update service counts
				if err := ie.dbManager.KnownPeers.UpdatePeerServiceCounts(
					entry.PeerID,
					topic,
					metadata.FilesCount,
					metadata.AppsCount,
				); err != nil {
					ie.logger.Debug(fmt.Sprintf("Failed to update service counts for peer %s from peer_exchange: %v",
						entry.PeerID[:8], err), "identity-exchange")
				} else {
					ie.logger.Debug(fmt.Sprintf("Updated service counts from DHT for peer %s via peer_exchange (files: %d, apps: %d)",
						entry.PeerID[:8], metadata.FilesCount, metadata.AppsCount), "identity-exchange")
				}
			}
		}

		stored++
	}

	return stored
}

// validateIdentity validates the received identity data
func (ie *IdentityExchanger) validateIdentity(identity *IdentityExchangeData) error {
	// Check peer_id length (SHA1 hex = 40 chars)
	if len(identity.PeerID) != 40 {
		return fmt.Errorf("invalid peer_id length: %d (expected 40)", len(identity.PeerID))
	}

	// Check public key length (Ed25519 = 32 bytes)
	if len(identity.PublicKey) != 32 {
		return fmt.Errorf("invalid public key length: %d (expected 32)", len(identity.PublicKey))
	}

	// Verify peer_id matches public_key
	derivedPeerID := crypto.DerivePeerID(identity.PublicKey)
	if derivedPeerID != identity.PeerID {
		return fmt.Errorf("peer_id mismatch: claimed %s, derived %s",
			identity.PeerID[:8], derivedPeerID[:8])
	}

	// Validate node type
	if identity.NodeType != "public" && identity.NodeType != "private" {
		return fmt.Errorf("invalid node type: %s (expected 'public' or 'private')", identity.NodeType)
	}

	return nil
}

//nolint:copylocks // quic.Stream is an interface, safe to pass by value
func (ie *IdentityExchanger) sendMessage(stream *quic.Stream, msg *QUICMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// Write length prefix (4 bytes)
	length := uint32(len(data))
	lengthBytes := make([]byte, 4)
	lengthBytes[0] = byte(length >> 24)
	lengthBytes[1] = byte(length >> 16)
	lengthBytes[2] = byte(length >> 8)
	lengthBytes[3] = byte(length)

	if _, err := stream.Write(lengthBytes); err != nil {
		return fmt.Errorf("failed to write length prefix: %v", err)
	}

	// Write message data
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("failed to write message data: %v", err)
	}

	return nil
}

//nolint:copylocks // quic.Stream is an interface, safe to pass by value
func (ie *IdentityExchanger) receiveMessage(stream *quic.Stream, timeout time.Duration) (*QUICMessage, error) {
	// Set read deadline
	stream.SetReadDeadline(time.Now().Add(timeout))
	defer stream.SetReadDeadline(time.Time{}) // Clear deadline

	// Read length prefix (4 bytes)
	lengthBytes := make([]byte, 4)
	if _, err := stream.Read(lengthBytes); err != nil {
		return nil, fmt.Errorf("failed to read length prefix: %v", err)
	}

	length := uint32(lengthBytes[0])<<24 | uint32(lengthBytes[1])<<16 | uint32(lengthBytes[2])<<8 | uint32(lengthBytes[3])

	// Sanity check
	if length > 10*1024*1024 { // 10 MB max
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	// Read message data
	data := make([]byte, length)
	if _, err := stream.Read(data); err != nil {
		return nil, fmt.Errorf("failed to read message data: %v", err)
	}

	// Unmarshal message
	var msg QUICMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %v", err)
	}

	return &msg, nil
}
