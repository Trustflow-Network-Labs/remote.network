package p2p

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// Errors
var (
	ErrHolePunchActive       = errors.New("hole punch already active for this peer")
	ErrNoPublicAddress       = errors.New("peer has no public address")
	ErrNotRelayConnection    = errors.New("hole punch stream must come from relay connection")
	ErrHolePunchTimeout      = errors.New("hole punch timed out")
	ErrNoDirectConnection    = errors.New("no direct connection established")
	ErrInvalidStreamDirection = errors.New("invalid stream direction for hole punch")
)

const (
	// Protocol identifier for hole punch streams
	HolePunchProtocol = "/holepunch/1.0.0"
)

// HolePuncher manages hole punching for direct peer-to-peer connections
type HolePuncher struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	quicPeer        *QUICPeer
	dhtPeer         *DHTPeer
	dbManager       *database.SQLiteManager
	metadataFetcher *MetadataFetcher

	// NAT detection info
	natDetector *NATDetector

	// Active hole punch attempts (for deduplication)
	activeMx sync.Mutex
	active   map[string]struct{} // key: peer node ID

	// Context
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics
	metricsMx            sync.RWMutex
	totalAttempts        int64
	successfulPunches    int64
	failedPunches        int64
	lanDetections        int64
	directDials          int64
	relayFallbacks       int64
	totalRTT             time.Duration
	totalPunchTime       time.Duration
	successByNATType     map[string]int64
}

// NewHolePuncher creates a new hole puncher
func NewHolePuncher(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	quicPeer *QUICPeer,
	dhtPeer *DHTPeer,
	dbManager *database.SQLiteManager,
	metadataFetcher *MetadataFetcher,
	natDetector *NATDetector,
) *HolePuncher {
	ctx, cancel := context.WithCancel(context.Background())

	hp := &HolePuncher{
		config:          config,
		logger:          logger,
		quicPeer:        quicPeer,
		dhtPeer:         dhtPeer,
		dbManager:       dbManager,
		metadataFetcher: metadataFetcher,
		natDetector:     natDetector,
		active:          make(map[string]struct{}),
		ctx:             ctx,
		cancel:          cancel,
		successByNATType: make(map[string]int64),
	}

	return hp
}

// Start starts the hole puncher
func (hp *HolePuncher) Start() error {
	hp.logger.Info("Starting hole puncher...", "hole-puncher")

	// Register stream handler for incoming hole punch requests
	// This will be set up by QUICPeer when it receives a stream with HolePunchProtocol

	return nil
}

// Stop stops the hole puncher
func (hp *HolePuncher) Stop() error {
	hp.logger.Info("Stopping hole puncher...", "hole-puncher")
	hp.cancel()
	return nil
}

// beginDirectConnect checks if we can start a hole punch attempt (deduplication)
func (hp *HolePuncher) beginDirectConnect(peerID string) error {
	hp.activeMx.Lock()
	defer hp.activeMx.Unlock()

	if _, active := hp.active[peerID]; active {
		return ErrHolePunchActive
	}

	hp.active[peerID] = struct{}{}
	return nil
}

// endDirectConnect cleans up after a hole punch attempt
func (hp *HolePuncher) endDirectConnect(peerID string) {
	hp.activeMx.Lock()
	defer hp.activeMx.Unlock()
	delete(hp.active, peerID)
}

// GetMetrics returns current hole punch metrics
func (hp *HolePuncher) GetMetrics() map[string]interface{} {
	hp.metricsMx.RLock()
	defer hp.metricsMx.RUnlock()

	avgRTT := time.Duration(0)
	avgPunchTime := time.Duration(0)
	successRate := float64(0)

	if hp.totalAttempts > 0 {
		successRate = float64(hp.successfulPunches) / float64(hp.totalAttempts) * 100
	}

	if hp.successfulPunches > 0 {
		avgRTT = hp.totalRTT / time.Duration(hp.successfulPunches)
		avgPunchTime = hp.totalPunchTime / time.Duration(hp.successfulPunches)
	}

	return map[string]interface{}{
		"total_attempts":      hp.totalAttempts,
		"successful_punches":  hp.successfulPunches,
		"failed_punches":      hp.failedPunches,
		"lan_detections":      hp.lanDetections,
		"direct_dials":        hp.directDials,
		"relay_fallbacks":     hp.relayFallbacks,
		"success_rate":        fmt.Sprintf("%.1f%%", successRate),
		"average_rtt":         avgRTT.String(),
		"average_punch_time":  avgPunchTime.String(),
		"success_by_nat_type": hp.successByNATType,
	}
}

// recordAttempt records a hole punch attempt
func (hp *HolePuncher) recordAttempt() {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.totalAttempts++
}

// recordSuccess records a successful hole punch
func (hp *HolePuncher) recordSuccess(natPair string, rtt, punchTime time.Duration) {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.successfulPunches++
	hp.totalRTT += rtt
	hp.totalPunchTime += punchTime
	hp.successByNATType[natPair]++
}

// recordFailure records a failed hole punch
func (hp *HolePuncher) recordFailure() {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.failedPunches++
}

// recordLANDetection records a LAN peer detection
func (hp *HolePuncher) recordLANDetection() {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.lanDetections++
}

// recordDirectDial records a successful direct dial
func (hp *HolePuncher) recordDirectDial() {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.directDials++
}

// recordRelayFallback records a fallback to relay
func (hp *HolePuncher) recordRelayFallback() {
	hp.metricsMx.Lock()
	defer hp.metricsMx.Unlock()
	hp.relayFallbacks++
}

// DirectConnect attempts to establish a direct connection to a peer
// This is the main entry point for hole punching
func (hp *HolePuncher) DirectConnect(peerID string) error {
	hp.logger.Debug(fmt.Sprintf("DirectConnect initiated for peer %s", peerID), "hole-puncher")

	// Deduplication check
	if err := hp.beginDirectConnect(peerID); err != nil {
		return err
	}
	defer hp.endDirectConnect(peerID)

	// Record attempt
	hp.recordAttempt()

	// Get peer metadata
	metadata, err := hp.getPeerMetadata(peerID)
	if err != nil {
		return fmt.Errorf("failed to get peer metadata: %v", err)
	}

	// Step 1: Check if already have direct connection
	if hp.hasDirectConnection(peerID) {
		hp.logger.Debug(fmt.Sprintf("Already have direct connection to peer %s", peerID), "hole-puncher")
		return nil
	}

	// Step 2: Check if LAN peer (same public IP + subnet)
	if hp.config.GetConfigBool("hole_punch_lan_detection_enabled", true) {
		if hp.IsLANPeer(metadata) {
			hp.logger.Info(fmt.Sprintf("Detected LAN peer %s, attempting direct LAN dial", peerID), "hole-puncher")
			hp.recordLANDetection()

			if err := hp.directLANDial(metadata); err == nil {
				hp.logger.Info(fmt.Sprintf("Successfully connected to LAN peer %s", peerID), "hole-puncher")
				return nil
			}
			hp.logger.Warn(fmt.Sprintf("LAN dial failed for peer %s: %v, continuing to next strategy", peerID, err), "hole-puncher")
		}
	}

	// Step 3: Try direct dial if peer has public address
	if hp.hasPublicAddress(metadata) {
		hp.logger.Debug(fmt.Sprintf("Peer %s has public address, attempting direct dial", peerID), "hole-puncher")

		if err := hp.directDialPublic(metadata); err == nil {
			hp.logger.Info(fmt.Sprintf("Successfully connected directly to public peer %s", peerID), "hole-puncher")
			hp.recordDirectDial()
			return nil
		}
		hp.logger.Warn(fmt.Sprintf("Direct dial failed for peer %s: %v, attempting hole punch", peerID, err), "hole-puncher")
	}

	// Step 4: Attempt hole punch via relay
	hp.logger.Info(fmt.Sprintf("Attempting hole punch for peer %s", peerID), "hole-puncher")
	if err := hp.initiateHolePunch(peerID, metadata); err == nil {
		hp.logger.Info(fmt.Sprintf("Successfully hole punched connection to peer %s", peerID), "hole-puncher")
		return nil
	} else {
		hp.logger.Warn(fmt.Sprintf("Hole punch failed for peer %s: %v", peerID, err), "hole-puncher")
		hp.recordFailure()
	}

	// Step 5: Fallback - relay connection should already exist
	hp.logger.Info(fmt.Sprintf("All direct connection attempts failed for peer %s, falling back to relay", peerID), "hole-puncher")
	hp.recordRelayFallback()

	return fmt.Errorf("direct connection not possible, relay fallback active")
}

// getPeerMetadata retrieves peer metadata from DHT
func (hp *HolePuncher) getPeerMetadata(peerID string) (*database.PeerMetadata, error) {
	// Get topic from config (assume we're on the same topic)
	topics := hp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	if len(topics) == 0 {
		return nil, fmt.Errorf("no topics configured")
	}

	topic := topics[0] // Use first topic for now

	// Get peer's public key from known_peers database
	knownPeer, err := hp.dbManager.KnownPeers.GetKnownPeer(peerID, topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer from known_peers: %v", err)
	}

	if len(knownPeer.PublicKey) == 0 {
		return nil, fmt.Errorf("peer %s has no public key in known_peers", peerID[:8])
	}

	// Fetch metadata from DHT using MetadataFetcher
	metadata, err := hp.metadataFetcher.GetPeerMetadata(knownPeer.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata from DHT: %v", err)
	}

	return metadata, nil
}

// hasDirectConnection checks if we already have a direct connection to the peer
func (hp *HolePuncher) hasDirectConnection(peerID string) bool {
	// Get peer metadata to find its address
	metadata, err := hp.getPeerMetadata(peerID)
	if err != nil {
		return false
	}

	// Build possible addresses for the peer
	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	possibleAddrs := []string{}

	// Add public address
	if metadata.NetworkInfo.PublicIP != "" {
		possibleAddrs = append(possibleAddrs, fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, quicPort))
	}

	// Add private address (for LAN)
	if metadata.NetworkInfo.PrivateIP != "" {
		possibleAddrs = append(possibleAddrs, fmt.Sprintf("%s:%d", metadata.NetworkInfo.PrivateIP, quicPort))
	}

	// Check if we have an active connection to any of these addresses
	connections := hp.quicPeer.GetConnections()
	for _, conn := range connections {
		for _, addr := range possibleAddrs {
			if conn == addr {
				hp.logger.Debug(fmt.Sprintf("Found existing direct connection to peer %s at %s", peerID, addr), "hole-puncher")
				return true
			}
		}
	}

	return false
}

// IsLANPeer checks if a peer is on the same local network (exported for use in peer connection logic)
func (hp *HolePuncher) IsLANPeer(metadata *database.PeerMetadata) bool {
	if hp.natDetector == nil {
		return false
	}

	result := hp.natDetector.GetLastResult()
	if result == nil {
		return false
	}

	ourPublicIP := result.PublicIP
	ourPrivateIP := result.PrivateIP

	// Check 1: Same public IP (behind same NAT gateway)
	if metadata.NetworkInfo.PublicIP != ourPublicIP {
		return false
	}

	// Check 2: Private IPs in same subnet
	subnetMask := hp.config.GetConfigInt("hole_punch_lan_subnet_mask", 24, 8, 32)
	ourSubnet := getSubnet(ourPrivateIP, subnetMask)
	peerSubnet := getSubnet(metadata.NetworkInfo.PrivateIP, subnetMask)

	if ourSubnet == "" || peerSubnet == "" {
		return false
	}

	return ourSubnet == peerSubnet
}

// directLANDial attempts direct connection using private IP
func (hp *HolePuncher) directLANDial(metadata *database.PeerMetadata) error {
	// Use peer's private endpoint directly
	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	privateEndpoint := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PrivateIP, quicPort)

	hp.logger.Debug(fmt.Sprintf("Attempting LAN dial to %s via %s", metadata.NodeID, privateEndpoint), "hole-puncher")

	// Set timeout for LAN dial
	timeout := hp.config.GetConfigDuration("hole_punch_direct_dial_timeout", 5*time.Second)
	ctx, cancel := context.WithTimeout(hp.ctx, timeout)
	defer cancel()

	// Attempt connection
	conn, err := hp.quicPeer.ConnectToPeer(privateEndpoint)
	if err != nil {
		return fmt.Errorf("LAN dial failed: %v", err)
	}

	// Verify connection is working by sending a ping
	if err := hp.verifyConnection(ctx, conn); err != nil {
		conn.CloseWithError(0, "verification failed")
		return fmt.Errorf("LAN connection verification failed: %v", err)
	}

	hp.logger.Info(fmt.Sprintf("LAN connection established to %s via %s", metadata.NodeID, privateEndpoint), "hole-puncher")
	return nil
}

// hasPublicAddress checks if peer has a public address
func (hp *HolePuncher) hasPublicAddress(metadata *database.PeerMetadata) bool {
	// Check if peer is not using relay and has public IP
	// Public peers don't need relays
	return !metadata.NetworkInfo.UsingRelay && metadata.NetworkInfo.PublicIP != ""
}

// directDialPublic attempts direct connection to public peer
func (hp *HolePuncher) directDialPublic(metadata *database.PeerMetadata) error {
	// Use peer's public endpoint
	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	publicEndpoint := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, quicPort)

	hp.logger.Debug(fmt.Sprintf("Attempting direct dial to %s via %s", metadata.NodeID, publicEndpoint), "hole-puncher")

	// Set timeout for direct dial
	timeout := hp.config.GetConfigDuration("hole_punch_direct_dial_timeout", 5*time.Second)
	ctx, cancel := context.WithTimeout(hp.ctx, timeout)
	defer cancel()

	// Attempt connection
	conn, err := hp.quicPeer.ConnectToPeer(publicEndpoint)
	if err != nil {
		return fmt.Errorf("direct dial failed: %v", err)
	}

	// Verify connection is working
	if err := hp.verifyConnection(ctx, conn); err != nil {
		conn.CloseWithError(0, "verification failed")
		return fmt.Errorf("direct connection verification failed: %v", err)
	}

	hp.logger.Info(fmt.Sprintf("Direct connection established to %s via %s", metadata.NodeID, publicEndpoint), "hole-puncher")
	return nil
}

// verifyConnection sends a ping to verify the connection works
func (hp *HolePuncher) verifyConnection(ctx context.Context, conn *quic.Conn) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Send ping
	pingMsg := CreatePing("hole-punch-verify", "connection-verification")
	msgBytes, err := pingMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal ping: %v", err)
	}

	if _, err := stream.Write(msgBytes); err != nil {
		return fmt.Errorf("failed to send ping: %v", err)
	}

	// Wait for pong
	buffer := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return fmt.Errorf("failed to read pong: %v", err)
	}

	return nil
}

// initiateHolePunch initiates the hole punching protocol as the initiator
func (hp *HolePuncher) initiateHolePunch(peerID string, metadata *database.PeerMetadata) error {
	startTime := time.Now()

	hp.logger.Debug(fmt.Sprintf("Starting hole punch to peer %s", peerID), "hole-puncher")

	// Step 1: Get relay address and connection
	relayNodeID := metadata.NetworkInfo.ConnectedRelay
	if relayNodeID == "" || !metadata.NetworkInfo.UsingRelay {
		return fmt.Errorf("peer is not using a relay")
	}

	// Get relay metadata to find relay address
	topics := hp.config.GetTopics("subscribe_topics", []string{"remote-network-mesh"})
	if len(topics) == 0 {
		return fmt.Errorf("no topics configured")
	}
	topic := topics[0]

	// Get relay peer's public key from known_peers
	relayKnownPeer, err := hp.dbManager.KnownPeers.GetKnownPeer(relayNodeID, topic)
	if err != nil {
		return fmt.Errorf("failed to get relay peer from known_peers: %v", err)
	}

	if len(relayKnownPeer.PublicKey) == 0 {
		return fmt.Errorf("relay peer %s has no public key in known_peers", relayNodeID[:8])
	}

	// Fetch relay metadata from DHT
	relayMetadata, err := hp.metadataFetcher.GetPeerMetadata(relayKnownPeer.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to get relay metadata from DHT: %v", err)
	}

	// Connect to relay (ConnectToPeer will reuse existing connection if available)
	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	relayAddr := fmt.Sprintf("%s:%d", relayMetadata.NetworkInfo.PublicIP, quicPort)

	relayConn, err := hp.quicPeer.ConnectToPeer(relayAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Step 2: Open hole punch stream via relay
	timeout := hp.config.GetConfigDuration("hole_punch_stream_timeout", 60*time.Second)
	ctx, cancel := context.WithTimeout(hp.ctx, timeout)
	defer cancel()

	stream, err := relayConn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open hole punch stream: %v", err)
	}
	defer stream.Close()

	// Step 3: Prepare our addresses
	result := hp.natDetector.GetLastResult()
	if result == nil {
		return fmt.Errorf("NAT detection not completed")
	}

	observedAddrs := hp.buildObservedAddrs(result)
	privateAddrs := hp.buildPrivateAddrs(result)
	natType := result.NATType.String()

	// Step 4: Send CONNECT with our addresses and start RTT timer
	t1 := time.Now()
	connectMsg := CreateHolePunchConnect(
		hp.dhtPeer.NodeID(),
		observedAddrs,
		privateAddrs,
		result.PublicIP,
		result.PrivateIP,
		natType,
		false, // We are not a relay
	)

	connectBytes, err := connectMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal CONNECT: %v", err)
	}

	if _, err := stream.Write(connectBytes); err != nil {
		return fmt.Errorf("failed to send CONNECT: %v", err)
	}

	hp.logger.Debug(fmt.Sprintf("Sent CONNECT to peer %s", peerID), "hole-puncher")

	// Step 5: Receive CONNECT from peer
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	buffer := make([]byte, 8192)
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return fmt.Errorf("failed to receive CONNECT: %v", err)
	}
	t3 := time.Now()

	// Step 6: Parse peer's CONNECT
	peerConnectMsg, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return fmt.Errorf("failed to unmarshal peer CONNECT: %v", err)
	}

	if peerConnectMsg.Type != MessageTypeHolePunchConnect {
		return fmt.Errorf("unexpected message type: %s", peerConnectMsg.Type)
	}

	var peerConnectData HolePunchConnectData
	if err := peerConnectMsg.GetDataAs(&peerConnectData); err != nil {
		return fmt.Errorf("invalid CONNECT data: %v", err)
	}

	// Step 7: Calculate RTT
	rtt := t3.Sub(t1)
	hp.logger.Debug(fmt.Sprintf("Measured RTT to peer %s: %v", peerID, rtt), "hole-puncher")

	// Step 8: Send SYNC with RTT
	syncMsg := CreateHolePunchSync(rtt.Milliseconds())
	syncBytes, err := syncMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal SYNC: %v", err)
	}

	if _, err := stream.Write(syncBytes); err != nil {
		return fmt.Errorf("failed to send SYNC: %v", err)
	}

	hp.logger.Debug(fmt.Sprintf("Sent SYNC to peer %s", peerID), "hole-puncher")

	// Step 9: Wait RTT/2, then simultaneously dial all peer addresses
	peerAddrs := append(peerConnectData.ObservedAddrs, peerConnectData.PrivateAddrs...)
	conn, err := hp.simultaneousDial(peerAddrs, rtt)
	if err != nil {
		return fmt.Errorf("simultaneous dial failed: %v", err)
	}

	// Success! Record metrics
	punchTime := time.Since(startTime)
	natPair := fmt.Sprintf("%s-%s", natType, peerConnectData.NATType)
	hp.recordSuccess(natPair, rtt, punchTime)

	hp.logger.Info(fmt.Sprintf("Hole punch successful to peer %s (RTT: %v, Total: %v)", peerID, rtt, punchTime), "hole-puncher")

	// Store the direct connection (QUICPeer will handle it internally via ConnectToPeer)
	// The connection is already tracked when simultaneousDial succeeds
	_ = conn

	return nil
}

// buildObservedAddrs builds list of observed public addresses
func (hp *HolePuncher) buildObservedAddrs(result *NATDetectionResult) []string {
	if result.PublicIP == "" {
		return []string{}
	}

	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	return []string{
		fmt.Sprintf("%s:%d", result.PublicIP, quicPort),
	}
}

// buildPrivateAddrs builds list of private addresses
func (hp *HolePuncher) buildPrivateAddrs(result *NATDetectionResult) []string {
	if result.PrivateIP == "" {
		return []string{}
	}

	quicPort := hp.config.GetConfigInt("quic_port", 30906, 1024, 65535)
	return []string{
		fmt.Sprintf("%s:%d", result.PrivateIP, quicPort),
	}
}

// simultaneousDial attempts to dial multiple addresses simultaneously
func (hp *HolePuncher) simultaneousDial(peerAddrs []string, rtt time.Duration) (*quic.Conn, error) {
	if len(peerAddrs) == 0 {
		return nil, fmt.Errorf("no peer addresses to dial")
	}

	hp.logger.Debug(fmt.Sprintf("Simultaneous dial to %d addresses (RTT: %v)", len(peerAddrs), rtt), "hole-puncher")

	// Wait RTT/2 for synchronization
	time.Sleep(rtt / 2)

	// Create context for dial attempts
	timeout := hp.config.GetConfigDuration("hole_punch_direct_dial_timeout", 5*time.Second)
	ctx, cancel := context.WithTimeout(hp.ctx, timeout)
	defer cancel()

	// Channel to receive first successful connection
	connChan := make(chan *quic.Conn, len(peerAddrs))
	errChan := make(chan error, len(peerAddrs))

	// Try all addresses in parallel
	for _, addr := range peerAddrs {
		go func(address string) {
			hp.logger.Debug(fmt.Sprintf("Dialing %s", address), "hole-puncher")
			conn, err := hp.quicPeer.ConnectToPeer(address)
			if err != nil {
				errChan <- err
				return
			}
			connChan <- conn
		}(addr)
	}

	// Wait for first success or all failures
	var lastErr error
	for i := 0; i < len(peerAddrs); i++ {
		select {
		case conn := <-connChan:
			hp.logger.Debug("Simultaneous dial succeeded", "hole-puncher")
			return conn, nil
		case err := <-errChan:
			lastErr = err
		case <-ctx.Done():
			return nil, fmt.Errorf("dial timeout: %v", ctx.Err())
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all dials failed: %v", lastErr)
	}

	return nil, fmt.Errorf("all dials failed")
}

// HandleHolePunchStream handles incoming hole punch requests (receiver side)
func (hp *HolePuncher) HandleHolePunchStream(stream *quic.Stream) error {
	startTime := time.Now()

	hp.logger.Debug("Received hole punch stream", "hole-puncher")

	// Step 1: Receive CONNECT from initiator
	(*stream).SetReadDeadline(time.Now().Add(10 * time.Second))
	buffer := make([]byte, 8192)
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return fmt.Errorf("failed to receive CONNECT: %v", err)
	}

	// Step 2: Parse initiator's CONNECT
	initiatorConnectMsg, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return fmt.Errorf("failed to unmarshal CONNECT: %v", err)
	}

	if initiatorConnectMsg.Type != MessageTypeHolePunchConnect {
		return fmt.Errorf("unexpected message type: %s", initiatorConnectMsg.Type)
	}

	var initiatorConnectData HolePunchConnectData
	if err := initiatorConnectMsg.GetDataAs(&initiatorConnectData); err != nil {
		return fmt.Errorf("invalid CONNECT data: %v", err)
	}

	peerID := initiatorConnectData.NodeID
	hp.logger.Debug(fmt.Sprintf("Hole punch request from peer %s", peerID), "hole-puncher")

	// Step 3: Prepare our addresses
	result := hp.natDetector.GetLastResult()
	if result == nil {
		return fmt.Errorf("NAT detection not completed")
	}

	observedAddrs := hp.buildObservedAddrs(result)
	privateAddrs := hp.buildPrivateAddrs(result)
	natType := result.NATType.String()

	// Step 4: Send CONNECT back with our addresses
	connectMsg := CreateHolePunchConnect(
		hp.dhtPeer.NodeID(),
		observedAddrs,
		privateAddrs,
		result.PublicIP,
		result.PrivateIP,
		natType,
		false, // We are not a relay
	)

	connectBytes, err := connectMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal CONNECT: %v", err)
	}

	if _, err := stream.Write(connectBytes); err != nil {
		return fmt.Errorf("failed to send CONNECT: %v", err)
	}

	hp.logger.Debug(fmt.Sprintf("Sent CONNECT to peer %s", peerID), "hole-puncher")

	// Step 5: Receive SYNC from initiator
	(*stream).SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err = stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return fmt.Errorf("failed to receive SYNC: %v", err)
	}

	syncMsg, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return fmt.Errorf("failed to unmarshal SYNC: %v", err)
	}

	if syncMsg.Type != MessageTypeHolePunchSync {
		return fmt.Errorf("unexpected message type: %s", syncMsg.Type)
	}

	var syncData HolePunchSyncData
	if err := syncMsg.GetDataAs(&syncData); err != nil {
		return fmt.Errorf("invalid SYNC data: %v", err)
	}

	rtt := time.Duration(syncData.RTT) * time.Millisecond
	hp.logger.Debug(fmt.Sprintf("Received SYNC with RTT %v from peer %s", rtt, peerID), "hole-puncher")

	// Close the relay stream as we no longer need it
	(*stream).Close()

	// Step 6: Wait RTT/2, then simultaneously dial all peer addresses
	initiatorAddrs := append(initiatorConnectData.ObservedAddrs, initiatorConnectData.PrivateAddrs...)
	conn, err := hp.simultaneousDial(initiatorAddrs, rtt)
	if err != nil {
		return fmt.Errorf("simultaneous dial failed: %v", err)
	}

	// Success! Record metrics
	punchTime := time.Since(startTime)
	natPair := fmt.Sprintf("%s-%s", natType, initiatorConnectData.NATType)
	hp.recordSuccess(natPair, rtt, punchTime)

	hp.logger.Info(fmt.Sprintf("Hole punch successful from peer %s (RTT: %v, Total: %v)", peerID, rtt, punchTime), "hole-puncher")

	// Store the direct connection (QUICPeer will handle it internally via ConnectToPeer)
	// The connection is already tracked when simultaneousDial succeeds
	_ = conn

	return nil
}

// getSubnet returns the subnet for an IP address with the given mask
func getSubnet(ipStr string, maskBits int) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}

	// Create mask
	mask := net.CIDRMask(maskBits, 32)
	if ip.To4() == nil {
		// IPv6
		mask = net.CIDRMask(maskBits, 128)
	}

	// Apply mask to get network address
	network := ip.Mask(mask)
	return fmt.Sprintf("%s/%d", network.String(), maskBits)
}
