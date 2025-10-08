package p2p

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/google/uuid"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
)

type QUICPeer struct {
	config      *utils.ConfigManager
	logger      *utils.LogsManager
	ctx         context.Context
	cancel      context.CancelFunc
	listener    *quic.Listener
	connections map[string]*quic.Conn
	connMutex   sync.RWMutex
	tlsConfig   *tls.Config
	port        int
	// Database manager for peer metadata
	dbManager   *database.SQLiteManager
	// DHT peer reference to get node ID
	dhtPeer     *DHTPeer
	// Relay peer reference for relay service info
	relayPeer   *RelayPeer
	// Hole puncher for NAT traversal
	holePuncher *HolePuncher
	// Callback for relay peer discovery
	onRelayDiscovered func(*database.PeerMetadata)
	// Callback for connection failures
	onConnectionFailure func(string)
}

func NewQUICPeer(config *utils.ConfigManager, logger *utils.LogsManager) (*QUICPeer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	port := config.GetConfigInt("quic_port", 30906, 1024, 65535)

	// Generate self-signed certificate for QUIC
	tlsConfig, err := generateTLSConfig()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to generate TLS config: %v", err)
	}

	return &QUICPeer{
		config:      config,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		connections: make(map[string]*quic.Conn),
		tlsConfig:   tlsConfig,
		port:        port,
	}, nil
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

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Remote Network Node"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{certDER},
				PrivateKey:  key,
			},
		},
		NextProtos: []string{"remote-network-p2p"},
	}, nil
}

func (q *QUICPeer) Start() error {
	listenAddr := q.config.GetConfigWithDefault("quic_listen_addr", "0.0.0.0")
	addr := fmt.Sprintf("%s:%d", listenAddr, q.port)

	idleTimeout := q.config.GetConfigDuration("quic_connection_timeout", 5*time.Minute)
	keepAlivePeriod := q.config.GetConfigDuration("quic_keepalive_period", 15*time.Second)

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
			q.connections[remoteAddr] = conn
			q.connMutex.Unlock()

			// Handle connection in goroutine
			go q.handleConnection(conn, remoteAddr)
		}
	}
}

func (q *QUICPeer) handleConnection(conn *quic.Conn, remoteAddr string) {
	defer func() {
		q.connMutex.Lock()
		delete(q.connections, remoteAddr)
		q.connMutex.Unlock()

		q.logger.Debug(fmt.Sprintf("Connection %s closed", remoteAddr), "quic")
	}()

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
					return
				}
				q.logger.Error(fmt.Sprintf("Failed to accept stream from %s: %v", remoteAddr, err), "quic")
				return
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
	if err != nil {
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
		response = q.handleMetadataRequest(msg, remoteAddr)
	case MessageTypePeerAnnounce:
		q.handlePeerAnnounce(msg, remoteAddr)
		// No response needed for announcements
	case MessageTypePing:
		response = q.handlePing(msg, remoteAddr)
	case MessageTypeEcho:
		response = q.handleEcho(msg, remoteAddr)
	case MessageTypeRelayRegister:
		response = q.handleRelayRegister(msg, remoteAddr)
	case MessageTypeRelayData:
		q.handleRelayData(msg, remoteAddr)
		// Relay data is forwarded, no response to sender
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
			return
		}

		_, err = stream.Write(responseData)
		if err != nil {
			q.logger.Error(fmt.Sprintf("Failed to write response to %s: %v", remoteAddr, err), "quic")
		} else {
			q.logger.Debug(fmt.Sprintf("Sent %s response to %s", response.Type, remoteAddr), "quic")
		}
	}
}

// handleMetadataRequest processes metadata requests and returns peer metadata
// generateOurMetadata creates metadata for our own node
func (q *QUICPeer) generateOurMetadata(nodeID, topic string) (*database.PeerMetadata, error) {
	// Get our network information
	nodeTypeManager := utils.NewNodeTypeManager()
	externalIP, _ := nodeTypeManager.GetExternalIP()
	privateIP, _ := nodeTypeManager.GetLocalIP()
	isPublic, _ := nodeTypeManager.IsPublicNode()

	// Determine node type
	nodeType := "private"
	if isPublic {
		nodeType = "public"
	}

	// Create network info
	networkInfo := database.NetworkInfo{
		PublicIP:    externalIP,
		PublicPort:  q.port,
		PrivateIP:   privateIP,
		PrivatePort: q.port,
		NodeType:    nodeType,
		Protocols: []database.Protocol{
			{Name: "quic", Port: q.port},
		},
	}

	// Add relay service information if this node is in relay mode
	if q.relayPeer != nil && q.relayPeer.IsRelayMode() {
		q.logger.Debug(fmt.Sprintf("Setting IsRelay=true in metadata for topic %s", topic), "quic")
		networkInfo.IsRelay = true
		networkInfo.RelayEndpoint = fmt.Sprintf("%s:%d", externalIP, q.port)

		// Get relay peer stats to populate pricing and capacity
		relayStats := q.relayPeer.GetStats()
		if pricing, ok := relayStats["pricing_per_gb"].(float64); ok {
			networkInfo.RelayPricing = pricing
		}
		if capacity, ok := relayStats["max_connections"].(int); ok {
			networkInfo.RelayCapacity = capacity
		}

		// Get reputation score from database if available
		if q.dbManager != nil && q.dbManager.Relay != nil {
			stats := q.dbManager.Relay.GetStats()
			// Simple reputation calculation: starts at 0.5, increases with successful sessions
			if activeSessions, ok := stats["active_sessions"].(int); ok {
				networkInfo.ReputationScore = 0.5 + float64(activeSessions)*0.01
				if networkInfo.ReputationScore > 1.0 {
					networkInfo.ReputationScore = 1.0
				}
			}
		}
	}

	// Create metadata for our node
	metadata := &database.PeerMetadata{
		NodeID:      nodeID,
		Topic:       topic,
		Version:     1,
		Timestamp:   time.Now(),
		NetworkInfo: networkInfo,
		Capabilities: []string{"metadata_exchange", "ping_pong"},
		Services:    make(map[string]database.Service),
		Extensions:  make(map[string]interface{}),
		LastSeen:    time.Now(),
		Source:      "quic",
	}

	return metadata, nil
}

func (q *QUICPeer) handleMetadataRequest(msg *QUICMessage, remoteAddr string) *QUICMessage {
	var requestData MetadataRequestData
	if err := msg.GetDataAs(&requestData); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse metadata request from %s: %v", remoteAddr, err), "quic")
		return CreateMetadataResponse(msg.RequestID, nil, fmt.Errorf("invalid request data"))
	}

	q.logger.Info(fmt.Sprintf("Bidirectional metadata request for topic '%s' from %s", requestData.Topic, remoteAddr), "quic")

	// Check if we have dependencies set
	if q.dhtPeer == nil || q.dbManager == nil {
		q.logger.Error("DHT peer or database manager not set", "quic")
		return CreateMetadataResponse(msg.RequestID, nil, fmt.Errorf("service not ready"))
	}

	// Process requester's metadata if provided (bidirectional exchange)
	if requestData.MyMetadata != nil {
		// Check if this is our own node ID to prevent self-storage
		ourNodeID := q.dhtPeer.NodeID()
		if requestData.MyMetadata.NodeID == ourNodeID {
			q.logger.Debug(fmt.Sprintf("Skipping storage of our own metadata from %s (Node ID: %s)", remoteAddr, requestData.MyMetadata.NodeID), "quic")
		} else {
			requestData.MyMetadata.LastSeen = time.Now()
			requestData.MyMetadata.Source = "quic"
			if err := q.dbManager.PeerMetadata.StorePeerMetadata(requestData.MyMetadata); err != nil {
				q.logger.Error(fmt.Sprintf("Failed to store requester metadata from %s: %v", remoteAddr, err), "quic")
			} else {
				q.logger.Info(fmt.Sprintf("Successfully stored requester metadata from %s (Node ID: %s, Topic: %s)",
					remoteAddr, requestData.MyMetadata.NodeID, requestData.MyMetadata.Topic), "quic")

				// Debug: log IsRelay status
				q.logger.Debug(fmt.Sprintf("Metadata from %s: IsRelay=%v, callback=%v",
					requestData.MyMetadata.NodeID, requestData.MyMetadata.NetworkInfo.IsRelay, q.onRelayDiscovered != nil), "quic")

				// Notify relay discovery callback if this is a relay peer
				if requestData.MyMetadata.NetworkInfo.IsRelay && q.onRelayDiscovered != nil {
					q.logger.Debug(fmt.Sprintf("Discovered relay peer: %s", requestData.MyMetadata.NodeID), "quic")
					q.onRelayDiscovered(requestData.MyMetadata)
				}
			}
		}
	}

	// Generate our own metadata for the response
	nodeID := q.dhtPeer.NodeID()
	metadata, err := q.generateOurMetadata(nodeID, requestData.Topic)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to generate our metadata: %v", err), "quic")
		return CreateMetadataResponse(msg.RequestID, nil, fmt.Errorf("failed to generate metadata"))
	}

	return CreateMetadataResponse(msg.RequestID, metadata, nil)
}

// handlePeerAnnounce processes peer announcements and stores them in database
func (q *QUICPeer) handlePeerAnnounce(msg *QUICMessage, remoteAddr string) {
	var announceData PeerAnnounceData
	if err := msg.GetDataAs(&announceData); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse peer announce from %s: %v", remoteAddr, err), "quic")
		return
	}

	q.logger.Info(fmt.Sprintf("Peer announce from %s: action=%s, topic=%s", remoteAddr, announceData.Action, announceData.Topic), "quic")

	if q.dbManager == nil {
		q.logger.Error("Database manager not set", "quic")
		return
	}

	// Update metadata with current timestamp
	if announceData.Metadata != nil {
		announceData.Metadata.LastSeen = time.Now()

		// Store peer metadata
		if err := q.dbManager.PeerMetadata.StorePeerMetadata(announceData.Metadata); err != nil {
			q.logger.Error(fmt.Sprintf("Failed to store peer metadata from %s: %v", remoteAddr, err), "quic")
		} else {
			q.logger.Debug(fmt.Sprintf("Stored metadata for peer %s in topic %s", announceData.NodeID, announceData.Topic), "quic")
		}
	}
}

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

// RequestPeerMetadata requests metadata from a peer via QUIC
func (q *QUICPeer) RequestPeerMetadata(peerAddr string, topic string) error {
	q.logger.Info(fmt.Sprintf("Requesting metadata from peer %s for topic '%s'", peerAddr, topic), "quic")

	// Check if we have dependencies set
	if q.dhtPeer == nil || q.dbManager == nil {
		q.logger.Error("DHT peer or database manager not set", "quic")
		return fmt.Errorf("service not ready")
	}

	// Create bidirectional metadata request with our own metadata
	nodeID := q.dhtPeer.NodeID()
	requestID := uuid.New().String()

	// Generate our metadata to send along with the request
	myMetadata, err := q.generateOurMetadata(nodeID, topic)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to generate our metadata for %s: %v", peerAddr, err), "quic")
		return err
	}

	request := CreateBidirectionalMetadataRequest(nodeID, topic, []string{"network", "capabilities"}, myMetadata)
	request.RequestID = requestID

	// Marshal request
	requestData, err := request.Marshal()
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to marshal metadata request: %v", err), "quic")
		return err
	}

	// Connect to peer and send request
	conn, err := q.ConnectToPeer(peerAddr)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to connect to peer %s: %v", peerAddr, err), "quic")
		return err
	}

	stream, err := q.openStreamWithTimeout(conn)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to open stream to %s: %v", peerAddr, err), "quic")
		return err
	}
	defer stream.Close()

	// Send request
	_, err = stream.Write(requestData)
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to send metadata request to %s: %v", peerAddr, err), "quic")
		return err
	}

	q.logger.Debug(fmt.Sprintf("Sent metadata request to %s for topic '%s'", peerAddr, topic), "quic")

	// Read response with timeout
	buffer := make([]byte, q.config.GetConfigInt("buffer_size", 65536, 1024, 1048576))
	n, err := stream.Read(buffer)
	if err != nil {
		// EOF is expected when server closes stream after sending response
		if err != io.EOF {
			q.logger.Error(fmt.Sprintf("Failed to read metadata response from %s: %v", peerAddr, err), "quic")
			return err
		}
		// For EOF, we should have received data before the stream closed
		if n == 0 {
			q.logger.Error(fmt.Sprintf("No data received from %s before stream closed", peerAddr), "quic")
			return fmt.Errorf("no response data received")
		}
	}

	// Parse response
	response, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse metadata response from %s: %v", peerAddr, err), "quic")
		return err
	}

	// Validate response
	if response.Type != MessageTypeMetadataResponse || response.RequestID != requestID {
		q.logger.Error(fmt.Sprintf("Invalid metadata response from %s", peerAddr), "quic")
		return fmt.Errorf("invalid response")
	}

	// Process response
	var responseData MetadataResponseData
	if err := response.GetDataAs(&responseData); err != nil {
		q.logger.Error(fmt.Sprintf("Failed to parse metadata response data from %s: %v", peerAddr, err), "quic")
		return err
	}

	if responseData.Error != "" {
		q.logger.Error(fmt.Sprintf("Peer %s returned error: %s", peerAddr, responseData.Error), "quic")
		return fmt.Errorf("peer error: %s", responseData.Error)
	}

	// Store peer metadata if received
	if responseData.Metadata != nil {
		// Check if this is our own node ID to prevent self-storage
		ourNodeID := q.dhtPeer.NodeID()
		if responseData.Metadata.NodeID == ourNodeID {
			q.logger.Debug(fmt.Sprintf("Skipping storage of our own metadata from %s (Node ID: %s)", peerAddr, responseData.Metadata.NodeID), "quic")
			return nil
		}

		responseData.Metadata.LastSeen = time.Now()
		responseData.Metadata.Source = "quic"

		if err := q.dbManager.PeerMetadata.StorePeerMetadata(responseData.Metadata); err != nil {
			q.logger.Error(fmt.Sprintf("Failed to store metadata from %s: %v", peerAddr, err), "quic")
			return err
		}

		q.logger.Info(fmt.Sprintf("Successfully stored metadata from peer %s (Node ID: %s, Topic: %s)",
			peerAddr, responseData.Metadata.NodeID, responseData.Metadata.Topic), "quic")

		// Debug: log IsRelay status
		q.logger.Debug(fmt.Sprintf("Metadata from %s: IsRelay=%v, callback=%v",
			responseData.Metadata.NodeID, responseData.Metadata.NetworkInfo.IsRelay, q.onRelayDiscovered != nil), "quic")

		// Notify relay discovery callback if this is a relay peer
		if responseData.Metadata.NetworkInfo.IsRelay && q.onRelayDiscovered != nil {
			q.logger.Debug(fmt.Sprintf("Discovered relay peer: %s", responseData.Metadata.NodeID), "quic")
			q.onRelayDiscovered(responseData.Metadata)
		}
	}

	return nil
}

func (q *QUICPeer) ConnectToPeer(addr string) (*quic.Conn, error) {
	q.logger.Info(fmt.Sprintf("Connecting to peer %s", addr), "quic")

	// Check if already connected
	q.connMutex.RLock()
	if conn, exists := q.connections[addr]; exists {
		q.connMutex.RUnlock()
		return conn, nil
	}
	q.connMutex.RUnlock()

	// Create TLS config for client (insecure for now)
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"remote-network-p2p"},
	}

	idleTimeout := q.config.GetConfigDuration("quic_connection_timeout", 5*time.Minute)
	keepAlivePeriod := q.config.GetConfigDuration("quic_keepalive_period", 15*time.Second)

	q.logger.Debug(fmt.Sprintf("Dialing %s with QUIC config: MaxIdleTimeout=%v, KeepAlivePeriod=%v", addr, idleTimeout, keepAlivePeriod), "quic")

	conn, err := quic.DialAddr(q.ctx, addr, tlsConfig, &quic.Config{
		MaxIdleTimeout:  idleTimeout,
		KeepAlivePeriod: keepAlivePeriod,
	})

	if err != nil {
		// Notify about connection failure if callback is set
		if q.onConnectionFailure != nil {
			go q.onConnectionFailure(addr)
		}
		return nil, fmt.Errorf("failed to connect to peer %s: %v", addr, err)
	}

	q.logger.Info(fmt.Sprintf("Successfully connected to peer %s (MaxIdleTimeout=%v, KeepAlivePeriod=%v)", addr, idleTimeout, keepAlivePeriod), "quic")

	// Store connection
	q.connMutex.Lock()
	q.connections[addr] = conn
	q.connMutex.Unlock()

	// Monitor connection
	go q.monitorConnection(conn, addr)

	return conn, nil
}

func (q *QUICPeer) monitorConnection(conn *quic.Conn, addr string) {
	<-conn.Context().Done()

	q.connMutex.Lock()
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
	if err != nil {
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
	if err != nil {
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
	conn, exists := q.connections[remoteAddr]
	q.connMutex.RUnlock()

	if !exists {
		q.logger.Error(fmt.Sprintf("No connection found for %s during relay registration", remoteAddr), "quic")
		return CreateRelayReject("", "", "no active connection")
	}

	// Delegate to relay peer for registration
	return q.relayPeer.HandleRelayRegister(msg, conn, remoteAddr)
}

// handleRelayData processes relay data forwarding
func (q *QUICPeer) handleRelayData(msg *QUICMessage, remoteAddr string) {
	// Only handle if we have a relay peer (we're in relay mode)
	if q.relayPeer == nil {
		q.logger.Warn(fmt.Sprintf("Received relay_data from %s but relay mode is not enabled", remoteAddr), "quic")
		return
	}

	// Delegate to relay peer - HandleRelayForward handles both relay_forward and relay_data
	if err := q.relayPeer.HandleRelayForward(msg, remoteAddr); err != nil {
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
	for addr, conn := range q.connections {
		q.logger.Debug(fmt.Sprintf("Closing connection to %s", addr), "quic")
		conn.CloseWithError(0, "shutting down")
	}
	q.connections = make(map[string]*quic.Conn)
	q.connMutex.Unlock()

	// Close listener
	if q.listener != nil {
		return q.listener.Close()
	}

	return nil
}