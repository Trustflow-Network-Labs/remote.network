package p2p

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// ConnectionInfo stores metadata about a QUIC connection
type ConnectionInfo struct {
	Conn       *quic.Conn
	PeerID     string // Authenticated peer ID from identity exchange
	RemoteAddr string // Remote address (IP:Port)
}

type QUICPeer struct {
	config          *utils.ConfigManager
	logger          *utils.LogsManager
	ctx             context.Context
	cancel          context.CancelFunc
	listener        *quic.Listener
	connections     map[string]*ConnectionInfo // remote_addr → connection info
	peerConnections map[string]string          // peer_id → remote_addr
	connMutex       sync.RWMutex
	tlsConfig   *tls.Config
	port         int
	// Database manager for peer metadata
	dbManager   *database.SQLiteManager
	// DHT peer reference to get node ID
	dhtPeer     *DHTPeer
	// Relay peer reference for relay service info
	relayPeer   *RelayPeer
	// Hole puncher for NAT traversal
	holePuncher *HolePuncher
	// Identity exchanger for Phase 3 identity + known peers exchange
	identityExchanger *IdentityExchanger
	// Service query handler for service discovery
	serviceQueryHandler *ServiceQueryHandler
	// Callback for relay peer discovery
	onRelayDiscovered func(*database.PeerMetadata)
	// Callback for connection failures
	onConnectionFailure func(string)
	// Callback for when connection is ready (after identity exchange)
	onConnectionReady func(peerID string, remoteAddr string)
}

func NewQUICPeer(config *utils.ConfigManager, logger *utils.LogsManager, paths *utils.AppPaths, keyPair *crypto.KeyPair) (*QUICPeer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	port := config.GetConfigInt("quic_port", 30906, 1024, 65535)

	// Load or generate Ed25519-based TLS certificate
	tlsConfig, err := loadOrGenerateTLSConfig(paths, keyPair, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load/generate TLS config: %v", err)
	}

	q := &QUICPeer{
		config:          config,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		connections:     make(map[string]*ConnectionInfo),
		peerConnections: make(map[string]string),
		tlsConfig:       tlsConfig,
		port:            port,
	}

	// Configure mutual TLS (mTLS) authentication
	// Both server and client will verify each other's certificates
	q.tlsConfig.ClientAuth = tls.RequireAnyClientCert
	q.tlsConfig.VerifyPeerCertificate = q.verifyPeerCertificate

	return q, nil
}

// SetDependencies sets the DHT peer, database manager, and relay peer references
func (q *QUICPeer) SetDependencies(dhtPeer *DHTPeer, dbManager *database.SQLiteManager, relayPeer *RelayPeer) {
	q.dhtPeer = dhtPeer
	q.dbManager = dbManager
	q.relayPeer = relayPeer
}

// SetHolePuncher sets the hole puncher for NAT traversal
func (q *QUICPeer) SetHolePuncher(holePuncher *HolePuncher) {
	q.holePuncher = holePuncher
}

// SetRelayDiscoveryCallback sets a callback for when relay peers are discovered
func (q *QUICPeer) SetRelayDiscoveryCallback(callback func(*database.PeerMetadata)) {
	q.onRelayDiscovered = callback
}

// SetConnectionFailureCallback sets a callback for when connections to peers fail
func (q *QUICPeer) SetConnectionFailureCallback(callback func(string)) {
	q.onConnectionFailure = callback
}

// SetConnectionReadyCallback sets a callback for when connection is ready (after identity exchange)
func (q *QUICPeer) SetConnectionReadyCallback(callback func(peerID string, remoteAddr string)) {
	q.onConnectionReady = callback
}

// generateEd25519Certificate creates a self-signed X.509 certificate using an Ed25519 keypair
// The certificate embeds the node's Peer ID in the Subject CommonName for easy identification
func generateEd25519Certificate(keyPair *crypto.KeyPair, peerID string) ([]byte, error) {
	// Certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Organization: []string{"Remote Network Node"},
			CommonName:   peerID, // Embed Peer ID in certificate
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		// Allow any IP/DNS for flexibility in P2P networking
		IPAddresses: []net.IP{net.IPv4(0, 0, 0, 0), net.IPv6zero},
		DNSNames:    []string{"*"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, keyPair.PublicKey, keyPair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %v", err)
	}

	return certDER, nil
}

// saveCertificateToPEM saves a certificate and private key to PEM files
func saveCertificateToPEM(certDER []byte, keyPair *crypto.KeyPair, certPath, keyPath string) error {
	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	if certPEM == nil {
		return fmt.Errorf("failed to encode certificate to PEM")
	}

	// Save certificate file (readable by all)
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate file: %v", err)
	}

	// Encode private key to PEM (PKCS#8 format for Ed25519)
	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(keyPair.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %v", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})
	if keyPEM == nil {
		return fmt.Errorf("failed to encode private key to PEM")
	}

	// Save private key file (readable only by owner)
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key file: %v", err)
	}

	return nil
}

// loadCertificateFromPEM loads a certificate and private key from PEM files
func loadCertificateFromPEM(certPath, keyPath string) (tls.Certificate, error) {
	// Load certificate
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to read certificate file: %v", err)
	}

	// Load private key
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to read private key file: %v", err)
	}

	// Parse PEM blocks
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return tls.Certificate{}, fmt.Errorf("failed to decode certificate PEM")
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		return tls.Certificate{}, fmt.Errorf("failed to decode private key PEM")
	}

	// Parse certificate
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to parse certificate: %v", err)
	}

	// Check if certificate is expired or expiring soon (< 30 days)
	now := time.Now()
	if now.After(cert.NotAfter) {
		return tls.Certificate{}, fmt.Errorf("certificate expired on %v", cert.NotAfter)
	}
	if now.Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return tls.Certificate{}, fmt.Errorf("certificate expiring soon (expires %v)", cert.NotAfter)
	}

	// Parse private key
	privateKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to parse private key: %v", err)
	}

	// Verify it's an Ed25519 key
	ed25519Key, ok := privateKey.(ed25519.PrivateKey)
	if !ok {
		return tls.Certificate{}, fmt.Errorf("private key is not Ed25519")
	}

	// Construct tls.Certificate
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certBlock.Bytes},
		PrivateKey:  ed25519Key,
	}

	return tlsCert, nil
}

// loadOrGenerateTLSConfig loads existing TLS certificate or generates a new one
// Uses the node's Ed25519 keypair for both TLS and peer identity
func loadOrGenerateTLSConfig(paths *utils.AppPaths, keyPair *crypto.KeyPair, logger *utils.LogsManager) (*tls.Config, error) {
	certPath := paths.GetDataPath("tls-cert.pem")
	keyPath := paths.GetDataPath("tls-key.pem")
	peerID := keyPair.PeerID()

	// Try to load existing certificate
	tlsCert, err := loadCertificateFromPEM(certPath, keyPath)
	if err == nil {
		logger.Info(fmt.Sprintf("Loaded existing TLS certificate from %s", certPath), "quic")
		return &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{"remote-network-p2p"},
			MinVersion:   tls.VersionTLS13,
		}, nil
	}

	// Certificate doesn't exist or is expired, generate new one
	logger.Info(fmt.Sprintf("Generating new Ed25519 TLS certificate (reason: %v)", err), "quic")

	certDER, err := generateEd25519Certificate(keyPair, peerID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate certificate: %v", err)
	}

	// Save certificate to disk
	if err := saveCertificateToPEM(certDER, keyPair, certPath, keyPath); err != nil {
		return nil, fmt.Errorf("failed to save certificate: %v", err)
	}

	logger.Info(fmt.Sprintf("TLS certificate saved to %s", certPath), "quic")

	// Load the newly created certificate
	tlsCert, err = loadCertificateFromPEM(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load newly created certificate: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"remote-network-p2p"},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// isBlacklisted checks if a peer is blacklisted
// Returns true if blacklisted, false otherwise
// Logs and allows connection if database check fails (fail-open for availability)
func (q *QUICPeer) isBlacklisted(peerID string) bool {
	if q.dbManager == nil {
		// Database not initialized yet, allow connection
		return false
	}

	blacklisted, err := q.dbManager.IsBlacklisted(peerID)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to check blacklist for peer %s: %v", peerID, err), "quic")
		// Fail-open: allow connection if database check fails
		return false
	}

	return blacklisted
}

// verifyPeerCertificate verifies the peer's TLS certificate
// Extracts the Ed25519 public key, computes Peer ID, and checks blacklist
func (q *QUICPeer) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no certificate presented by peer")
	}

	// Parse the peer's certificate
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("failed to parse peer certificate: %v", err)
	}

	// Extract Ed25519 public key from certificate
	pubKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("certificate does not contain Ed25519 public key")
	}

	// Validate public key size
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Ed25519 public key size: %d", len(pubKey))
	}

	// Compute Peer ID from public key (same as identity system)
	peerID := crypto.DerivePeerID(pubKey)

	// Check if peer is blacklisted
	if q.isBlacklisted(peerID) {
		q.logger.Info(fmt.Sprintf("Rejected connection from blacklisted peer %s", peerID), "quic")
		return fmt.Errorf("peer %s is blacklisted", peerID)
	}

	// Certificate and peer ID are valid, allow connection
	return nil
}

func (q *QUICPeer) Start() error {
	listenAddr := q.config.GetConfigWithDefault("quic_listen_addr", "0.0.0.0")
	addr := fmt.Sprintf("%s:%d", listenAddr, q.port)

	idleTimeout := q.config.GetConfigDuration("quic_connection_timeout", 30*time.Minute)
	keepAlivePeriod := q.config.GetConfigDuration("quic_keepalive_period", 10*time.Second)

	q.logger.Info(fmt.Sprintf("Starting QUIC peer on %s (MaxIdleTimeout=%v, KeepAlivePeriod=%v)", addr, idleTimeout, keepAlivePeriod), "quic")

	listener, err := quic.ListenAddr(addr, q.tlsConfig, &quic.Config{
		MaxIdleTimeout:  idleTimeout,
		KeepAlivePeriod: keepAlivePeriod,
	})

	if err != nil {
		return fmt.Errorf("failed to start QUIC listener: %v", err)
	}

	q.listener = listener

	// Accept incoming connections
	go q.acceptConnections()

	return nil
}

// SetIdentityExchanger sets the identity exchanger for this QUIC peer
func (q *QUICPeer) SetIdentityExchanger(exchanger *IdentityExchanger) {
	q.identityExchanger = exchanger
}

// SetServiceQueryHandler sets the service query handler for this QUIC peer
func (q *QUICPeer) SetServiceQueryHandler(handler *ServiceQueryHandler) {
	q.serviceQueryHandler = handler
}

func (q *QUICPeer) acceptConnections() {
	for {
		select {
		case <-q.ctx.Done():
			return
		default:
			conn, err := q.listener.Accept(q.ctx)
			if err != nil {
				if q.ctx.Err() != nil {
					return // Context cancelled, shutting down
				}
				q.logger.Error(fmt.Sprintf("Failed to accept QUIC connection: %v", err), "quic")
				continue
			}

			remoteAddr := conn.RemoteAddr().String()
			q.logger.Info(fmt.Sprintf("Accepted QUIC connection from %s", remoteAddr), "quic")

			q.connMutex.Lock()
			// Store connection temporarily without peer ID (will be updated after identity exchange)
			q.connections[remoteAddr] = &ConnectionInfo{
				Conn:       conn,
				PeerID:     "", // Will be set during identity exchange
				RemoteAddr: remoteAddr,
			}
			q.connMutex.Unlock()

			// Handle connection in goroutine
			go q.handleConnection(conn, remoteAddr)
		}
	}
}

func (q *QUICPeer) handleConnection(conn *quic.Conn, remoteAddr string) {
	defer func() {
		q.connMutex.Lock()
		// Remove peer connection mapping if it exists
		if connInfo, exists := q.connections[remoteAddr]; exists && connInfo.PeerID != "" {
			delete(q.peerConnections, connInfo.PeerID)
		}
		delete(q.connections, remoteAddr)
		q.connMutex.Unlock()

		q.logger.Debug(fmt.Sprintf("Connection %s closed", remoteAddr), "quic")
	}()

	// ===== PHASE 3: IDENTITY EXCHANGE (FIRST STREAM - SERVER SIDE) =====
	// Wait for first stream for identity exchange
	if q.identityExchanger != nil {
		streamPtr, err := conn.AcceptStream(q.ctx)
		if err != nil {
			q.logger.Error(fmt.Sprintf("Failed to accept identity exchange stream from %s: %v", remoteAddr, err), "quic")
			return
		}

		// Perform identity handshake
		// Note: We don't know the topic yet, will use default or extract from identity exchange
		// Pass pointer as quic.Stream interface (pointer implements interface)
		remotePeer, err := q.identityExchanger.PerformHandshake(streamPtr, "remote-network-mesh", remoteAddr)
		streamPtr.Close()

		if err != nil {
			q.logger.Error(fmt.Sprintf("Identity exchange failed with %s: %v", remoteAddr, err), "quic")
			conn.CloseWithError(1, "identity exchange failed")
			return
		}

		q.logger.Info(fmt.Sprintf("Server-side identity exchange complete with %s (peer_id: %s, public_key stored)",
			remoteAddr, remotePeer.PeerID[:8]), "quic")

		// Update connection info with peer ID
		q.connMutex.Lock()
		if connInfo, exists := q.connections[remoteAddr]; exists {
			connInfo.PeerID = remotePeer.PeerID
			q.peerConnections[remotePeer.PeerID] = remoteAddr
		}
		q.connMutex.Unlock()

		// Notify that connection is ready (server-side)
		if q.onConnectionReady != nil {
			go q.onConnectionReady(remotePeer.PeerID, remoteAddr)
		}
	} else {
		q.logger.Warn(fmt.Sprintf("Identity exchanger not set, skipping identity exchange for %s", remoteAddr), "quic")
	}

	// ===== HANDLE SUBSEQUENT STREAMS (METADATA, ETC.) =====
	// Handle incoming streams
	for {
		select {
		case <-q.ctx.Done():
			return
		case <-conn.Context().Done():
			return
		default:
			stream, err := conn.AcceptStream(q.ctx)
			if err != nil {
				if q.ctx.Err() != nil {
					// Context cancelled, shutting down gracefully
					return
				}
				// Check if connection is closed
				select {
				case <-conn.Context().Done():
					// Connection closed, exit handler
					q.logger.Debug(fmt.Sprintf("Connection %s closed, stopping stream handler", remoteAddr), "quic")
					return
				default:
					// Temporary error, log and continue accepting streams
					q.logger.Warn(fmt.Sprintf("Temporary error accepting stream from %s: %v (continuing)", remoteAddr, err), "quic")
					continue
				}
			}

			go q.handleStream(stream, remoteAddr)
		}
	}
}

func (q *QUICPeer) handleStream(stream *quic.Stream, remoteAddr string) {
	q.logger.Debug(fmt.Sprintf("Handling stream from %s", remoteAddr), "quic")

	// Read message with buffer - don't wait for EOF
	buffer := make([]byte, q.config.GetConfigInt("buffer_size", 65536, 1024, 1048576))
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		q.logger.Error(fmt.Sprintf("Failed to read from stream %s: %v", remoteAddr, err), "quic")
		return
	}
	data := buffer[:n]

	// Parse QUIC message
	msg, err := UnmarshalQUICMessage(data)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse message from %s: %v", remoteAddr, err), "quic")
		return
	}

	// Validate message
	if err := msg.Validate(); err != nil {
		q.logger.Error(fmt.Sprintf("Invalid message from %s: %v", remoteAddr, err), "quic")
		return
	}

	q.logger.Debug(fmt.Sprintf("Received %s message from %s", msg.Type, remoteAddr), "quic")

	// Handle different message types
	var response *QUICMessage
	switch msg.Type {
	case MessageTypeMetadataRequest:
		// Metadata exchange removed in DHT-only architecture - metadata comes from DHT
		q.logger.Warn(fmt.Sprintf("Received legacy metadata_request from %s (no longer supported)", remoteAddr), "quic")
		response = nil
	case MessageTypePeerAnnounce:
		// Peer announce removed in DHT-only architecture
		q.logger.Warn(fmt.Sprintf("Received legacy peer_announce from %s (no longer supported)", remoteAddr), "quic")
		// No response needed
	case MessageTypeServiceRequest:
		// Service discovery query
		if q.serviceQueryHandler != nil {
			response = q.serviceQueryHandler.HandleServiceSearchRequest(msg, remoteAddr)
		} else {
			q.logger.Warn(fmt.Sprintf("Received service_request from %s but service query handler not initialized", remoteAddr), "quic")
			response = CreateServiceSearchResponse(nil, "service discovery not available")
		}
	case MessageTypePing:
		response = q.handlePing(msg, remoteAddr)
	case MessageTypeEcho:
		response = q.handleEcho(msg, remoteAddr)
	case MessageTypeRelayRegister:
		response = q.handleRelayRegister(msg, remoteAddr)
	case MessageTypeRelayReceiveStreamReady:
		// Special handling: NAT peer is setting up receive stream for relay forwarding
		// Keep stream open so relay can forward messages to this peer
		if q.relayPeer != nil {
			if err := q.relayPeer.HandleRelayReceiveStreamReady(msg, stream); err != nil {
				q.logger.Error(fmt.Sprintf("Failed to handle relay receive stream setup: %v", err), "quic")
			}
		} else {
			q.logger.Warn("Received relay_receive_stream_ready but relay mode is not enabled", "quic")
		}
		return // Don't close stream - it will be used for forwarding messages
	case MessageTypeRelayData:
		q.handleRelayData(msg, remoteAddr, stream)
		// Relay data is forwarded, stream managed by relay handler
		return
	case MessageTypeRelayForward:
		q.handleRelayData(msg, remoteAddr, stream)
		// Relay forward keeps stream open for response routing
		return
	case MessageTypeRelaySessionQuery:
		response = q.handleRelaySessionQuery(msg, remoteAddr)
	case MessageTypeHolePunchConnect, MessageTypeHolePunchSync:
		// Hole punch messages are handled by the HolePuncher
		// The HolePuncher reads the stream directly, so we pass control to it
		if q.holePuncher != nil {
			if err := q.holePuncher.HandleHolePunchStream(stream); err != nil {
				q.logger.Error(fmt.Sprintf("Hole punch stream handling failed: %v", err), "quic")
			}
		} else {
			q.logger.Warn("Received hole punch message but hole puncher is not initialized", "quic")
		}
		return
	default:
		q.logger.Warn(fmt.Sprintf("Unknown message type %s from %s", msg.Type, remoteAddr), "quic")
		return
	}

	// Send response if any
	if response != nil {
		responseData, err := response.Marshal()
		if err != nil {
			q.logger.Error(fmt.Sprintf("Failed to marshal response to %s: %v", remoteAddr, err), "quic")
			stream.Close()
			return
		}

		_, err = stream.Write(responseData)
		if err != nil {
			q.logger.Error(fmt.Sprintf("Failed to write response to %s: %v", remoteAddr, err), "quic")
			stream.Close()
			return
		}

		q.logger.Debug(fmt.Sprintf("Sent %s response to %s", response.Type, remoteAddr), "quic")

		// Close stream to flush write buffer and signal response complete (FIN bit)
		// This is required by quic-go to ensure data is actually transmitted
		if err := stream.Close(); err != nil {
			q.logger.Warn(fmt.Sprintf("Failed to close stream to %s: %v", remoteAddr, err), "quic")
		}
	} else {
		// No response to send, close stream anyway to free resources
		stream.Close()
	}
}

// Note: handleMetadataRequest and generateOurMetadata removed - DHT-only architecture
// Note: storePeersFromExchange removed in Phase 5 - peer exchange now handled by identity_exchange.go

// handlePing processes ping messages and returns pong responses
func (q *QUICPeer) handlePing(msg *QUICMessage, remoteAddr string) *QUICMessage {
	var pingData PingData
	if err := msg.GetDataAs(&pingData); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse ping from %s: %v", remoteAddr, err), "quic")
		return nil
	}

	q.logger.Debug(fmt.Sprintf("Ping from %s: id=%s, message=%s", remoteAddr, pingData.ID, pingData.Message), "quic")

	// Check if this is a relay session keepalive
	if pingData.Message == "keepalive" && q.relayPeer != nil {
		// ID should be the session ID
		q.relayPeer.UpdateSessionKeepalive(pingData.ID)
	}

	// Calculate RTT
	rtt := time.Since(pingData.Timestamp).Milliseconds()

	return CreatePong(pingData.ID, rtt)
}

// handleEcho processes echo messages (legacy support)
func (q *QUICPeer) handleEcho(msg *QUICMessage, remoteAddr string) *QUICMessage {
	var echoData EchoData
	if err := msg.GetDataAs(&echoData); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse echo from %s: %v", remoteAddr, err), "quic")
		return nil
	}

	q.logger.Debug(fmt.Sprintf("Echo from %s: %s", remoteAddr, echoData.Message), "quic")

	// Return echo response
	return CreateEcho(fmt.Sprintf("Echo: %s", echoData.Message))
}

func (q *QUICPeer) ConnectToPeer(addr string) (*quic.Conn, error) {
	q.logger.Info(fmt.Sprintf("Connecting to peer %s", addr), "quic")

	// Check if already connected
	q.connMutex.RLock()
	if connInfo, exists := q.connections[addr]; exists {
		q.connMutex.RUnlock()
		return connInfo.Conn, nil
	}
	q.connMutex.RUnlock()

	// Create TLS config for client with custom peer verification
	// InsecureSkipVerify skips hostname/IP verification (not applicable to P2P),
	// but VerifyPeerCertificate still validates Ed25519 public key and blacklist
	tlsConfig := &tls.Config{
		Certificates:          q.tlsConfig.Certificates, // Present client certificate for mutual TLS
		InsecureSkipVerify:    true,                     // Skip hostname/IP verification, use custom peer verification
		VerifyPeerCertificate: q.verifyPeerCertificate,
		NextProtos:            []string{"remote-network-p2p"},
		MinVersion:            tls.VersionTLS13,
	}

	idleTimeout := q.config.GetConfigDuration("quic_connection_timeout", 30*time.Minute)
	keepAlivePeriod := q.config.GetConfigDuration("quic_keepalive_period", 10*time.Second)

	// Get dial timeout for connection establishment (prevents hanging on unreachable peers)
	dialTimeout := q.config.GetConfigDuration("quic_dial_timeout", 5*time.Second)

	q.logger.Debug(fmt.Sprintf("Dialing %s with QUIC config: DialTimeout=%v, MaxIdleTimeout=%v, KeepAlivePeriod=%v", addr, dialTimeout, idleTimeout, keepAlivePeriod), "quic")

	// Create context with timeout for connection establishment
	// This prevents blocking for 45+ seconds on unreachable peers
	dialCtx, dialCancel := context.WithTimeout(q.ctx, dialTimeout)
	defer dialCancel()

	conn, err := quic.DialAddr(dialCtx, addr, tlsConfig, &quic.Config{
		MaxIdleTimeout:  idleTimeout,
		KeepAlivePeriod: keepAlivePeriod,
	})

	if err != nil {
		// Check if error was due to timeout
		if dialCtx.Err() == context.DeadlineExceeded {
			q.logger.Debug(fmt.Sprintf("Connection to %s timed out after %v (peer likely offline/unreachable)", addr, dialTimeout), "quic")
		}

		// Notify about connection failure if callback is set
		if q.onConnectionFailure != nil {
			go q.onConnectionFailure(addr)
		}
		return nil, fmt.Errorf("failed to connect to peer %s: %v", addr, err)
	}

	q.logger.Info(fmt.Sprintf("Successfully connected to peer %s (MaxIdleTimeout=%v, KeepAlivePeriod=%v)", addr, idleTimeout, keepAlivePeriod), "quic")

	// ===== PHASE 3: IDENTITY EXCHANGE (IMMEDIATELY AFTER CONNECTION) =====
	// Perform identity exchange on first stream to authenticate the connection
	// This ensures all subsequent operations on this connection are authenticated
	if q.identityExchanger != nil {
		identityStream, err := q.openStreamWithTimeout(conn)
		if err != nil {
			conn.CloseWithError(1, "failed to open identity stream")
			return nil, fmt.Errorf("failed to open identity exchange stream: %v", err)
		}

		// Use default topic for identity exchange (connection-level authentication)
		remotePeer, err := q.identityExchanger.PerformHandshake(identityStream, "remote-network-mesh", addr)
		identityStream.Close()

		if err != nil {
			conn.CloseWithError(1, "identity exchange failed")
			return nil, fmt.Errorf("identity exchange failed: %v", err)
		}

		q.logger.Info(fmt.Sprintf("Connection to %s authenticated (peer_id: %s, public_key stored)",
			addr, remotePeer.PeerID[:8]), "quic")

		// Store authenticated connection with peer ID
		q.connMutex.Lock()
		q.connections[addr] = &ConnectionInfo{
			Conn:       conn,
			PeerID:     remotePeer.PeerID,
			RemoteAddr: addr,
		}
		q.peerConnections[remotePeer.PeerID] = addr
		q.connMutex.Unlock()

		// Notify that connection is ready (client-side)
		if q.onConnectionReady != nil {
			go q.onConnectionReady(remotePeer.PeerID, addr)
		}
	} else {
		q.logger.Warn(fmt.Sprintf("Identity exchanger not set, connection to %s not authenticated", addr), "quic")

		// Store connection without peer ID (not authenticated)
		q.connMutex.Lock()
		q.connections[addr] = &ConnectionInfo{
			Conn:       conn,
			PeerID:     "",
			RemoteAddr: addr,
		}
		q.connMutex.Unlock()
	}

	// Monitor connection
	go q.monitorConnection(conn, addr)

	// Handle incoming streams from server (bidirectional support)
	// This allows the server to initiate streams back to the client
	// Essential for relay-to-NAT-peer communication
	go q.handleClientStreams(conn, addr)

	return conn, nil
}

// handleClientStreams handles incoming streams on client-side (outbound) connections
// This enables bidirectional communication where the server can initiate streams back to the client
// Critical for relay servers to query NAT peers that connect to them
func (q *QUICPeer) handleClientStreams(conn *quic.Conn, remoteAddr string) {
	q.logger.Debug(fmt.Sprintf("Starting stream handler for client connection to %s", remoteAddr), "quic")

	for {
		select {
		case <-q.ctx.Done():
			return
		case <-conn.Context().Done():
			q.logger.Debug(fmt.Sprintf("Client connection to %s closed, stopping stream handler", remoteAddr), "quic")
			return
		default:
			stream, err := conn.AcceptStream(q.ctx)
			if err != nil {
				if q.ctx.Err() != nil {
					// Context cancelled, shutting down gracefully
					return
				}
				// Check if connection is closed
				select {
				case <-conn.Context().Done():
					// Connection closed, exit handler
					q.logger.Debug(fmt.Sprintf("Client connection to %s closed, stopping stream handler", remoteAddr), "quic")
					return
				default:
					// Temporary error, log and continue accepting streams
					q.logger.Warn(fmt.Sprintf("Temporary error accepting stream from %s: %v (continuing)", remoteAddr, err), "quic")
					continue
				}
			}

			// Handle stream in goroutine (same handler as server-side streams)
			go q.handleStream(stream, remoteAddr)
		}
	}
}

func (q *QUICPeer) monitorConnection(conn *quic.Conn, addr string) {
	<-conn.Context().Done()

	q.connMutex.Lock()
	// Remove peer connection mapping if it exists
	if connInfo, exists := q.connections[addr]; exists && connInfo.PeerID != "" {
		delete(q.peerConnections, connInfo.PeerID)
	}
	delete(q.connections, addr)
	q.connMutex.Unlock()

	q.logger.Debug(fmt.Sprintf("Connection to %s closed", addr), "quic")
}

func (q *QUICPeer) SendMessage(addr string, message []byte) error {
	conn, err := q.ConnectToPeer(addr)
	if err != nil {
		return err
	}

	stream, err := q.openStreamWithTimeout(conn)
	if err != nil {
		return fmt.Errorf("failed to open stream to %s: %v", addr, err)
	}
	defer stream.Close()

	_, err = stream.Write(message)
	if err != nil {
		return fmt.Errorf("failed to send message to %s: %v", addr, err)
	}

	q.logger.Debug(fmt.Sprintf("Sent message to %s: %s", addr, string(message)), "quic")

	// Read response (optional)
	buffer := make([]byte, q.config.GetConfigInt("buffer_size", 65536, 1024, 1048576))
	n, err := stream.Read(buffer)
	if err == nil {
		response := string(buffer[:n])
		q.logger.Debug(fmt.Sprintf("Received response from %s: %s", addr, response), "quic")
	}

	return nil
}

func (q *QUICPeer) BroadcastMessage(peers []net.Addr, message []byte) error {
	var errors []error

	for _, peer := range peers {
		if err := q.SendMessage(peer.String(), message); err != nil {
			q.logger.Error(fmt.Sprintf("Failed to send message to %s: %v", peer.String(), err), "quic")
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to send to %d peers", len(errors))
	}

	return nil
}

func (q *QUICPeer) GetConnections() []string {
	q.connMutex.RLock()
	defer q.connMutex.RUnlock()

	var addrs []string
	for addr := range q.connections {
		addrs = append(addrs, addr)
	}

	return addrs
}

func (q *QUICPeer) GetConnectionCount() int {
	q.connMutex.RLock()
	defer q.connMutex.RUnlock()

	return len(q.connections)
}

// GetPeerIDByAddress returns the peer ID for a connection address
func (q *QUICPeer) GetPeerIDByAddress(addr string) (string, bool) {
	q.connMutex.RLock()
	defer q.connMutex.RUnlock()

	if connInfo, exists := q.connections[addr]; exists {
		return connInfo.PeerID, connInfo.PeerID != ""
	}
	return "", false
}

// GetAddressByPeerID returns the connection address for a peer ID
func (q *QUICPeer) GetAddressByPeerID(peerID string) (string, bool) {
	q.connMutex.RLock()
	defer q.connMutex.RUnlock()

	addr, exists := q.peerConnections[peerID]
	return addr, exists
}

// GetConnectionByPeerID returns the connection for a peer ID
func (q *QUICPeer) GetConnectionByPeerID(peerID string) (*quic.Conn, error) {
	q.connMutex.RLock()
	defer q.connMutex.RUnlock()

	addr, exists := q.peerConnections[peerID]
	if !exists {
		return nil, fmt.Errorf("no connection found for peer ID %s", peerID[:8])
	}

	connInfo, exists := q.connections[addr]
	if !exists {
		return nil, fmt.Errorf("connection metadata missing for peer ID %s", peerID[:8])
	}

	return connInfo.Conn, nil
}

// Ping sends a ping message to a peer and waits for pong response
func (q *QUICPeer) Ping(addr string) error {
	conn, err := q.ConnectToPeer(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %v", addr, err)
	}

	stream, err := q.openStreamWithTimeout(conn)
	if err != nil {
		return fmt.Errorf("failed to open stream to %s: %v", addr, err)
	}
	defer stream.Close()

	// Create ping message
	pingMsg := CreatePing("relay-latency-check", "ping")
	msgBytes, err := pingMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal ping message: %v", err)
	}

	// Send ping
	_, err = stream.Write(msgBytes)
	if err != nil {
		return fmt.Errorf("failed to send ping to %s: %v", addr, err)
	}

	// Wait for pong response with timeout
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return fmt.Errorf("failed to read pong from %s: %v", addr, err)
	}

	// Parse response
	response, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return fmt.Errorf("failed to parse pong from %s: %v", addr, err)
	}

	if response.Type != MessageTypePong {
		return fmt.Errorf("expected pong but got %s from %s", response.Type, addr)
	}

	q.logger.Debug(fmt.Sprintf("Successfully pinged %s", addr), "quic")
	return nil
}

// QueryRelayForSession queries a relay to check if a NAT peer has an active session
func (q *QUICPeer) QueryRelayForSession(relayAddr string, clientNodeID string, queryNodeID string) (bool, error) {
	conn, err := q.ConnectToPeer(relayAddr)
	if err != nil {
		return false, fmt.Errorf("failed to connect to relay %s: %v", relayAddr, err)
	}

	stream, err := q.openStreamWithTimeout(conn)
	if err != nil {
		return false, fmt.Errorf("failed to open stream to relay %s: %v", relayAddr, err)
	}
	defer stream.Close()

	// Create session query message
	queryMsg := CreateRelaySessionQuery(clientNodeID, queryNodeID)
	msgBytes, err := queryMsg.Marshal()
	if err != nil {
		return false, fmt.Errorf("failed to marshal session query: %v", err)
	}

	// Send query
	_, err = stream.Write(msgBytes)
	if err != nil {
		return false, fmt.Errorf("failed to send session query to %s: %v", relayAddr, err)
	}

	// Wait for status response with timeout
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil && (err != io.EOF || n == 0) {
		// Only fail if we got an error other than EOF, or EOF with no data
		return false, fmt.Errorf("failed to read session status from %s: %v", relayAddr, err)
	}

	// Parse response
	response, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return false, fmt.Errorf("failed to parse session status from %s: %v", relayAddr, err)
	}

	if response.Type != MessageTypeRelaySessionStatus {
		return false, fmt.Errorf("expected relay_session_status but got %s from %s", response.Type, relayAddr)
	}

	// Extract status data
	var statusData RelaySessionStatusData
	if err := response.GetDataAs(&statusData); err != nil {
		return false, fmt.Errorf("failed to parse session status data: %v", err)
	}

	q.logger.Debug(fmt.Sprintf("Relay session query for %s: hasSession=%v, sessionActive=%v",
		clientNodeID, statusData.HasSession, statusData.SessionActive), "quic")

	return statusData.HasSession && statusData.SessionActive, nil
}

// handleRelayRegister processes relay registration requests from NAT peers
func (q *QUICPeer) handleRelayRegister(msg *QUICMessage, remoteAddr string) *QUICMessage {
	// Only handle if we have a relay peer (we're in relay mode)
	if q.relayPeer == nil {
		q.logger.Warn(fmt.Sprintf("Received relay_register from %s but relay mode is not enabled", remoteAddr), "quic")
		return CreateRelayReject("", "", "relay mode not enabled")
	}

	// Get connection for this remote address
	q.connMutex.RLock()
	connInfo, exists := q.connections[remoteAddr]
	q.connMutex.RUnlock()

	if !exists {
		q.logger.Error(fmt.Sprintf("No connection found for %s during relay registration", remoteAddr), "quic")
		return CreateRelayReject("", "", "no active connection")
	}

	// Delegate to relay peer for registration
	return q.relayPeer.HandleRelayRegister(msg, connInfo.Conn, remoteAddr)
}

// handleRelayData processes relay data forwarding
func (q *QUICPeer) handleRelayData(msg *QUICMessage, remoteAddr string, stream *quic.Stream) {
	// Only handle if we have a relay peer (we're in relay mode)
	if q.relayPeer == nil {
		q.logger.Warn(fmt.Sprintf("Received relay_data from %s but relay mode is not enabled", remoteAddr), "quic")
		return
	}

	// Delegate to relay peer - HandleRelayForward handles both relay_forward and relay_data
	if err := q.relayPeer.HandleRelayForward(msg, remoteAddr, stream); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to handle relay data from %s: %v", remoteAddr, err), "quic")
	}
}

// handleRelaySessionQuery processes relay session query requests
func (q *QUICPeer) handleRelaySessionQuery(msg *QUICMessage, remoteAddr string) *QUICMessage {
	// Only handle if we have a relay peer (we're in relay mode)
	if q.relayPeer == nil {
		q.logger.Warn(fmt.Sprintf("Received relay_session_query from %s but relay mode is not enabled", remoteAddr), "quic")
		// Return negative response
		return CreateRelaySessionStatus("", false, "", false, 0)
	}

	// Delegate to relay peer
	return q.relayPeer.HandleRelaySessionQuery(msg, remoteAddr)
}

// HandleRelayedMessage processes messages received via relay forwarding
func (q *QUICPeer) HandleRelayedMessage(msg *QUICMessage) *QUICMessage {
	q.logger.Debug(fmt.Sprintf("Handling relayed message: type=%s", msg.Type), "quic")

	// Route based on message type
	switch msg.Type {
	case MessageTypeServiceRequest:
		// Service search request forwarded via relay
		if q.serviceQueryHandler != nil {
			return q.serviceQueryHandler.HandleServiceSearchRequest(msg, "relayed")
		}
		q.logger.Warn("Received service_request via relay but handler not initialized", "quic")
		return CreateServiceSearchResponse(nil, "service discovery not available")

	case MessageTypeRelayHolePunch:
		// Hole punch coordination via relay
		// TODO: Route to hole puncher
		q.logger.Warn("Hole punch via relay not yet implemented", "quic")
		return nil

	default:
		q.logger.Warn(fmt.Sprintf("Unhandled relayed message type: %s", msg.Type), "quic")
		return nil
	}
}

// openStreamWithTimeout opens a QUIC stream with configurable timeout to prevent blocking
func (q *QUICPeer) openStreamWithTimeout(conn *quic.Conn) (*quic.Stream, error) {
	streamOpenTimeout := q.config.GetConfigDuration("quic_stream_open_timeout", 5*time.Second)
	streamCtx, cancel := context.WithTimeout(q.ctx, streamOpenTimeout)
	defer cancel()

	return conn.OpenStreamSync(streamCtx)
}

func (q *QUICPeer) Stop() error {
	q.logger.Info("Stopping QUIC peer...", "quic")
	q.cancel()

	// Close all connections
	q.connMutex.Lock()
	for addr, connInfo := range q.connections {
		q.logger.Debug(fmt.Sprintf("Closing connection to %s", addr), "quic")
		connInfo.Conn.CloseWithError(0, "shutting down")
	}
	q.connections = make(map[string]*ConnectionInfo)
	q.peerConnections = make(map[string]string)
	q.connMutex.Unlock()

	// Close listener
	if q.listener != nil {
		return q.listener.Close()
	}

	return nil
}