package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/p2p"
)

// ServiceSearchHandler handles service search requests via WebSocket
type ServiceSearchHandler struct {
	logger        *logrus.Logger
	dbManager     *database.SQLiteManager
	quicPeer      *p2p.QUICPeer
	metadataQuery *p2p.MetadataQueryService
	dhtPeer       *p2p.DHTPeer
	relayPeer     *p2p.RelayPeer
	ourPeerID     string // Our persistent Ed25519-based peer ID
}

// NewServiceSearchHandler creates a new service search handler
func NewServiceSearchHandler(
	logger *logrus.Logger,
	dbManager *database.SQLiteManager,
	quicPeer *p2p.QUICPeer,
	metadataQuery *p2p.MetadataQueryService,
	dhtPeer *p2p.DHTPeer,
	relayPeer *p2p.RelayPeer,
	ourPeerID string,
) *ServiceSearchHandler {
	return &ServiceSearchHandler{
		logger:        logger,
		dbManager:     dbManager,
		quicPeer:      quicPeer,
		metadataQuery: metadataQuery,
		dhtPeer:       dhtPeer,
		relayPeer:     relayPeer,
		ourPeerID:     ourPeerID,
	}
}

// HandleServiceSearchRequest handles a service search request from the client
func (ssh *ServiceSearchHandler) HandleServiceSearchRequest(client *Client, payload json.RawMessage) error {
	// Parse the request payload
	var request ServiceSearchRequestPayload
	if err := json.Unmarshal(payload, &request); err != nil {
		ssh.logger.WithError(err).Error("Failed to parse service search request")
		return ssh.sendErrorResponse(client, "Invalid request payload")
	}

	ssh.logger.WithFields(logrus.Fields{
		"query":        request.Query,
		"service_type": request.ServiceType,
		"peer_ids":     request.PeerIDs,
		"activeOnly":   request.ActiveOnly,
	}).Info("Processing service search request")

	// Get currently connected QUIC peers
	connectedPeers := ssh.quicPeer.GetConnections()
	if len(connectedPeers) == 0 {
		ssh.logger.Info("No currently connected peers - will discover and connect to peers from database")
		// Don't return early - continue to peer discovery phase below
	}

	// Build peer ID filter map if specific peer IDs were requested
	var peerIDFilter map[string]bool
	if len(request.PeerIDs) > 0 {
		peerIDFilter = make(map[string]bool)
		for _, pid := range request.PeerIDs {
			peerIDFilter[pid] = true
		}
		ssh.logger.WithFields(logrus.Fields{
			"requested_peers": len(request.PeerIDs),
		}).Info("Filtering search to specific peer IDs")
	}

	// Build integrated query list: direct connections + relay-accessible NAT peers
	type queryTarget struct {
		peerAddr      string // For direct queries
		peerID        string // For relay queries
		useRelay      bool
		useLocalRelay bool   // True if NAT peer is connected to our own relay
		relayAddr     string // Relay address if using relay
		sessionID     string // Relay session ID if using relay
	}

	queryTargets := make([]queryTarget, 0)

	// Build query targets from connected peers
	// Track peer IDs to avoid duplicates
	peerIDsSeen := make(map[string]bool)
	var peersToCheckForRelay []string

	connections := ssh.quicPeer.GetConnections()
	ssh.logger.WithFields(logrus.Fields{
		"connection_count": len(connections),
	}).Debug("Building service search query target list from connections")

	for _, addr := range connections {
		// Try to get peer ID from connection metadata
		peerID := ssh.getPeerIDFromConnection(nil, addr)

		if peerID == "" {
			// No peer ID available - if we have a filter, skip this connection
			if peerIDFilter != nil {
				ssh.logger.WithFields(logrus.Fields{
					"address": addr,
				}).Debug("Skipping connection without peer ID (filter active)")
				continue
			}
			// No filter active, treat as direct connection
			queryTargets = append(queryTargets, queryTarget{
				peerAddr: addr,
				useRelay: false,
			})
			ssh.logger.WithFields(logrus.Fields{
				"address": addr,
			}).Debug("Adding connection as direct query target (no peer ID)")
			continue
		}

		// Skip if we've already processed this peer
		if peerIDsSeen[peerID] {
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
				"address": addr,
			}).Debug("Skipping duplicate peer ID")
			continue
		}

		// If filter is active, check if this peer ID matches
		if peerIDFilter != nil && !peerIDFilter[peerID] {
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
			}).Debug("Skipping peer - not in requested peer IDs")
			continue
		}

		peerIDsSeen[peerID] = true

		// Check if this is an inbound-only connection (NAT peer)
		if ssh.isInboundOnlyConnection(addr) {
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
				"address": addr,
			}).Debug("Detected NAT peer with inbound-only connection, will query via relay")

			// Add to relay query list
			peersToCheckForRelay = append(peersToCheckForRelay, peerID)
		} else {
			// Direct connection available
			queryTargets = append(queryTargets, queryTarget{
				peerAddr: addr,
				peerID:   peerID,
				useRelay: false,
			})
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
				"address": addr,
			}).Debug("Adding peer as direct query target")
		}
	}

	// Add NAT peers accessible via relay
	// If specific peer IDs requested, only query those
	// If no specific peers requested, query ALL known NAT peers (via relay)

	if len(request.PeerIDs) > 0 {
		// User specified peer IDs - merge with any already detected NAT peers
		for _, pid := range request.PeerIDs {
			// Skip if already in list
			found := false
			for _, existing := range peersToCheckForRelay {
				if existing == pid {
					found = true
					break
				}
			}
			if !found {
				peersToCheckForRelay = append(peersToCheckForRelay, pid)
			}
		}
	} else {
		// General search - get all known peers from database
		// Get configured topic(s) for peer discovery
		topic := "remote-network-mesh" // Default topic
		knownPeers, err := ssh.dbManager.KnownPeers.GetKnownPeersByTopic(topic)
		if err != nil {
			ssh.logger.WithError(err).Warn("Failed to get known peers from database for relay query")
		} else {
			// Limit number of relay peers to query for performance
			// (prevents overwhelming network with too many concurrent queries)
			maxRelayQueries := 50
			addedCount := 0

			// Add known peers to check list (up to max limit)
			for _, kp := range knownPeers {
				if addedCount >= maxRelayQueries {
					ssh.logger.WithFields(logrus.Fields{
						"limit":   maxRelayQueries,
						"total":   len(knownPeers),
						"skipped": len(knownPeers) - maxRelayQueries,
					}).Debug("Reached max relay query limit, skipping remaining peers")
					break
				}

				// Skip if already in list (avoid duplicates)
				alreadyInList := false
				for _, existing := range peersToCheckForRelay {
					if existing == kp.PeerID {
						alreadyInList = true
						break
					}
				}
				if !alreadyInList {
					peersToCheckForRelay = append(peersToCheckForRelay, kp.PeerID)
					addedCount++
				}
			}
			ssh.logger.WithField("known_peers_count", len(peersToCheckForRelay)).Debug("Checking known NAT peers for relay accessibility")
		}
	}

	// Process peers to check for relay accessibility or direct public connectivity
	for _, peerID := range peersToCheckForRelay {
		// Check if this peer is already in the query target list
		existingTargetIndex := -1
		for i, target := range queryTargets {
			// Compare peer IDs directly (both are hex strings)
			if target.peerID == peerID {
				existingTargetIndex = i
				break
			}
		}

		// If peer already in queryTargets, we may need to update it to use relay
		// This handles cases where NAT peer was initially misidentified as direct connection
		if existingTargetIndex >= 0 {
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
			}).Debug("Peer already in query targets, checking if relay info needs updating")
		}

		// Always check DHT metadata to determine correct connectivity (even if already in list)
		{
			// Get peer from database to query metadata
			peer, err := ssh.dbManager.KnownPeers.GetKnownPeer(peerID, "remote-network-mesh")
			if err != nil || peer == nil || len(peer.PublicKey) == 0 {
				ssh.logger.WithError(err).WithField("peer_id", peerID).Debug("Could not get peer from database, skipping")
				continue
			}

			// Query DHT for peer metadata to determine connectivity method
			metadata, err := ssh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
			if err != nil {
				ssh.logger.WithError(err).WithField("peer_id", peerID).Debug("Could not query peer metadata from DHT, skipping")
				continue
			}

			// Determine how to connect to this peer based on its metadata
			if metadata.NetworkInfo.UsingRelay {
				// NAT peer - needs relay forwarding
				if metadata.NetworkInfo.RelayAddress == "" || metadata.NetworkInfo.RelaySessionID == "" {
					ssh.logger.WithField("peer_id", peerID).Debug("NAT peer missing relay connection details, skipping")
					continue
				}

				// Check if this NAT peer is connected to our own relay
				// Compare the relay peer ID (ConnectedRelay) with our own peer ID
				if metadata.NetworkInfo.ConnectedRelay == ssh.ourPeerID {
					ssh.logger.WithFields(logrus.Fields{
						"peer_id":       peerID,
						"relay_peer_id": metadata.NetworkInfo.ConnectedRelay,
						"our_peer_id":   ssh.ourPeerID,
					}).Debug("NAT peer connected to our own relay - will query via local relay session")

					newTarget := queryTarget{
						peerID:        peerID,
						useRelay:      true,
						useLocalRelay: true, // Flag for local relay forwarding
						sessionID:     metadata.NetworkInfo.RelaySessionID,
					}

					if existingTargetIndex >= 0 {
						// Update existing entry
						ssh.logger.WithFields(logrus.Fields{
							"peer_id": peerID[:8],
						}).Debug("Updating existing query target to use local relay")
						queryTargets[existingTargetIndex] = newTarget
					} else {
						// Add new entry
						queryTargets = append(queryTargets, newTarget)
					}
					continue
				}

				ssh.logger.WithFields(logrus.Fields{
					"peer_id":    peerID,
					"relay_addr": metadata.NetworkInfo.RelayAddress,
				}).Debug("Adding relay-accessible NAT peer to query list")

				newTarget := queryTarget{
					peerID:        peerID,
					useRelay:      true,
					useLocalRelay: false,
					relayAddr:     metadata.NetworkInfo.RelayAddress,
					sessionID:     metadata.NetworkInfo.RelaySessionID,
				}

				if existingTargetIndex >= 0 {
					// Update existing entry
					ssh.logger.WithFields(logrus.Fields{
						"peer_id": peerID[:8],
					}).Debug("Updating existing query target to use remote relay")
					queryTargets[existingTargetIndex] = newTarget
				} else {
					// Add new entry
					queryTargets = append(queryTargets, newTarget)
				}
			} else if metadata.NetworkInfo.PublicIP != "" {
				// Public peer - use direct connection
				peerAddr := fmt.Sprintf("%s:%d", metadata.NetworkInfo.PublicIP, metadata.NetworkInfo.PublicPort)

				ssh.logger.WithFields(logrus.Fields{
					"peer_id":   peerID,
					"peer_addr": peerAddr,
				}).Debug("Adding direct-accessible public peer to query list")

				newTarget := queryTarget{
					peerAddr: peerAddr,
					peerID:   peerID,
					useRelay: false,
				}

				if existingTargetIndex >= 0 {
					// Update existing entry
					ssh.logger.WithFields(logrus.Fields{
						"peer_id": peerID[:8],
					}).Debug("Updating existing query target to use direct connection")
					queryTargets[existingTargetIndex] = newTarget
				} else {
					// Add new entry
					queryTargets = append(queryTargets, newTarget)
				}
			} else {
				ssh.logger.WithField("peer_id", peerID).Debug("Peer has no relay info and no public IP, skipping")
			}
		}
	}

	// Calculate actual direct vs relay counts from query targets
	directCount := 0
	relayCount := 0
	for _, target := range queryTargets {
		if target.useRelay {
			relayCount++
		} else {
			directCount++
		}
	}

	ssh.logger.WithFields(logrus.Fields{
		"total_targets": len(queryTargets),
		"direct":        directCount,
		"relay":         relayCount,
	}).Info("Querying peers for services (direct + relay)")

	// Query all targets in parallel using goroutines for better performance
	type queryResult struct {
		services     []RemoteServiceInfo
		target       queryTarget
		err          error
		usedFallback bool
	}

	results := make(chan queryResult, len(queryTargets))
	queryCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Launch all queries concurrently
	for _, target := range queryTargets {
		go func(t queryTarget) {
			// Add panic recovery to prevent goroutine crashes
			defer func() {
				if r := recover(); r != nil {
					ssh.logger.WithFields(logrus.Fields{
						"panic":     r,
						"peer_addr": t.peerAddr,
						"peer_id":   t.peerID,
					}).Error("Panic recovered in service query goroutine")

					// Send error result
					results <- queryResult{
						services:     nil,
						target:       t,
						err:          fmt.Errorf("query panicked: %v", r),
						usedFallback: false,
					}
				}
			}()

			var services []RemoteServiceInfo
			var err error
			var usedFallback bool

			if t.useRelay {
				if t.useLocalRelay {
					// Query NAT peer connected to our own relay via local session
					services, err = ssh.queryPeerViaLocalRelay(
						queryCtx,
						t.peerID,
						request.Query,
						request.ServiceType,
						request.ActiveOnly,
					)
				} else {
					// Query via remote relay forwarding
					services, err = ssh.queryPeerViaRelay(
						queryCtx,
						t.peerID,
						t.relayAddr,
						t.sessionID,
						request.Query,
						request.ServiceType,
						request.ActiveOnly,
					)
				}
			} else {
				// Try direct QUIC query first
				services, err = ssh.queryPeerServices(
					queryCtx,
					t.peerAddr,
					t.peerID, // Pass peer ID for connection lookup
					request.Query,
					request.ServiceType,
					request.ActiveOnly,
				)

				// Connection fallback: if direct query fails, try relay
				if err != nil {
					ssh.logger.WithFields(logrus.Fields{
						"peer_addr": t.peerAddr,
						"peer_id":   t.peerID,
					}).Debug("Direct query failed, attempting relay fallback")

					// Use peer ID for relay lookup if available, otherwise fallback to address
					lookupKey := t.peerID
					if lookupKey == "" {
						lookupKey = t.peerAddr
					}

					relayAddr, sessionID, relayErr := ssh.getPeerRelayInfo(lookupKey, "remote-network-mesh")
					if relayErr == nil {
						// Retry via relay - use peer ID if available
						targetID := t.peerID
						if targetID == "" {
							targetID = t.peerAddr
						}

						services, err = ssh.queryPeerViaRelay(
							queryCtx,
							targetID,
							relayAddr,
							sessionID,
							request.Query,
							request.ServiceType,
							request.ActiveOnly,
						)

						if err == nil {
							usedFallback = true
							ssh.logger.WithField("peer_addr", t.peerAddr).Info("Relay fallback successful")
						}
					}
				}
			}

			results <- queryResult{
				services:     services,
				target:       t,
				err:          err,
				usedFallback: usedFallback,
			}
		}(target)
	}

	// Stream results as they arrive for better responsiveness
	// Send partial results immediately instead of waiting for all peers
	allServices := make([]RemoteServiceInfo, 0)
	successCount := 0
	timeoutCount := 0
	unreachableCount := 0
	fallbackCount := 0

	for i := 0; i < len(queryTargets); i++ {
		select {
		case result := <-results:
			ssh.logger.WithFields(logrus.Fields{
				"has_error":      result.err != nil,
				"error":          result.err,
				"services_count": len(result.services),
				"peer_address":   result.target.peerAddr,
				"used_fallback":  result.usedFallback,
			}).Debug("Received service query result from peer")

			if result.err != nil {
				// Classify and log error
				errType := classifyError(result.err)

				logFields := logrus.Fields{
					"error_type": errType,
				}

				if result.target.useRelay {
					logFields["peer_id"] = result.target.peerID
					logFields["relay_addr"] = result.target.relayAddr
				} else {
					logFields["peer_addr"] = result.target.peerAddr
				}

				switch errType {
				case "timeout":
					timeoutCount++
					ssh.logger.WithFields(logFields).Warn("Query timeout - peer may be slow or overloaded")
				case "unreachable":
					unreachableCount++
					ssh.logger.WithFields(logFields).Warn("Peer unreachable - may be offline or network issue")
				default:
					ssh.logger.WithError(result.err).WithFields(logFields).Warn("Service query failed")
				}
			} else {
				successCount++
				if result.usedFallback {
					fallbackCount++
				}

				// Send partial results immediately if we got any services
				if len(result.services) > 0 {
					ssh.sendPartialSearchResponse(client, result.services, false)
					allServices = append(allServices, result.services...)
				}
			}

		case <-queryCtx.Done():
			ssh.logger.Warn("Service search timeout - some peers did not respond in time")
			// Break out and send final results
			goto done
		}
	}

done:
	// Log comprehensive statistics
	ssh.logger.WithFields(logrus.Fields{
		"total_services":     len(allServices),
		"queried_peers":      len(queryTargets),
		"successful":         successCount,
		"failed_timeout":     timeoutCount,
		"failed_unreachable": unreachableCount,
		"relay_fallbacks":    fallbackCount,
		"direct_queries":     directCount,
		"relay_queries":      relayCount,
	}).Info("Service search completed")

	// Send final response indicating search is complete
	return ssh.sendPartialSearchResponse(client, []RemoteServiceInfo{}, true)
}

// queryPeerServices queries a specific peer address for services matching the criteria
func (ssh *ServiceSearchHandler) queryPeerServices(ctx context.Context, peerAddr string, peerID string, query string, serviceType string, activeOnly bool) ([]RemoteServiceInfo, error) {
	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
		"peer_id":      peerID,
		"query":        query,
		"service_type": serviceType,
		"active_only":  activeOnly,
	}).Debug("Starting service query to peer")

	// Create service search request message
	// If serviceType is empty, query all service types
	if serviceType == "" {
		serviceType = "DATA,DOCKER,STANDALONE"
	}
	searchMsg := p2p.CreateServiceSearchRequest(query, serviceType, activeOnly)
	msgBytes, err := searchMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %v", err)
	}

	// Check context before attempting connection
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled before connection: %v", ctx.Err())
	default:
	}

	// Try to get existing connection by peer ID first (for NAT peers with ephemeral ports)
	type connResult struct {
		conn *interface{}
		err  error
	}
	connChan := make(chan connResult, 1)

	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
		"peer_id":      peerID,
	}).Debug("Attempting QUIC connection for service query")

	go func() {
		var conn *quic.Conn
		var err error

		// If we have a peer ID, try to get existing connection by ID first
		// This handles NAT peers where peerAddr contains ephemeral source port
		if peerID != "" {
			conn, err = ssh.quicPeer.GetConnectionByPeerID(peerID)
			if err == nil && conn != nil {
				ssh.logger.WithFields(logrus.Fields{
					"peer_id": peerID[:8],
				}).Debug("Reusing existing connection by peer ID")
				var connInterface interface{} = conn
				connChan <- connResult{conn: &connInterface, err: nil}
				return
			}
			// Log but don't fail - will try ConnectToPeer as fallback
			ssh.logger.WithFields(logrus.Fields{
				"peer_id": peerID[:8],
				"error":   err,
			}).Debug("No existing connection found by peer ID, trying address-based connection")
		}

		// Fall back to address-based connection (works for public peers)
		conn, err = ssh.quicPeer.ConnectToPeer(peerAddr)
		if err != nil {
			// Send error immediately without trying to wrap nil conn
			connChan <- connResult{conn: nil, err: err}
			return
		}
		// Only wrap connection if it's not nil
		var connInterface interface{} = conn
		connChan <- connResult{conn: &connInterface, err: nil}
	}()

	var conn interface{}
	select {
	case result := <-connChan:
		if result.err != nil {
			return nil, fmt.Errorf("failed to connect to peer: %v", result.err)
		}
		// Defensive check: ensure conn pointer is not nil
		if result.conn == nil {
			return nil, fmt.Errorf("connection is nil")
		}
		conn = *result.conn
	case <-ctx.Done():
		return nil, fmt.Errorf("connection timeout: %v", ctx.Err())
	}

	// Defensive check: ensure conn is not nil before type assertion
	if conn == nil {
		return nil, fmt.Errorf("connection is nil after result processing")
	}

	// Type assert directly to *quic.Conn (the actual type returned by ConnectToPeer)
	quicConn, ok := conn.(*quic.Conn)
	if !ok {
		ssh.logger.WithFields(logrus.Fields{
			"peer_address": peerAddr,
			"actual_type":  fmt.Sprintf("%T", conn),
		}).Warn("Connection type assertion failed - expected *quic.Conn")
		return nil, fmt.Errorf("invalid connection type: expected *quic.Conn, got %T", conn)
	}

	// Open stream for service search with the parent context
	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
	}).Debug("Opening stream for service query")

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		ssh.logger.WithFields(logrus.Fields{
			"peer_address": peerAddr,
			"error":        err.Error(),
		}).Warn("Failed to open stream for service query")
		return nil, fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
	}).Debug("Stream opened successfully, sending service query message")

	// Send search request
	if _, err := stream.Write(msgBytes); err != nil {
		return nil, fmt.Errorf("failed to send search request: %v", err)
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
		"message_size": len(msgBytes),
	}).Debug("Service query message sent successfully")

	// Read response with deadline
	// Increased timeout for relay-mediated connections
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	buffer := make([]byte, 1024*1024) // 1MB buffer for response
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		// Real error (not just stream closure)
		return nil, fmt.Errorf("failed to read search response: %v", err)
	}
	if n == 0 {
		// No data received (stream closed before response sent)
		return nil, fmt.Errorf("failed to read search response: no data received (EOF)")
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_address": peerAddr,
		"bytes_read":   n,
	}).Debug("Received service query response")

	// Parse response
	responseMsg, err := p2p.UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Extract service search response
	var searchResponse p2p.ServiceSearchResponse
	if err := responseMsg.GetDataAs(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %v", err)
	}

	if searchResponse.Error != "" {
		return nil, fmt.Errorf("peer returned error: %s", searchResponse.Error)
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_address":   peerAddr,
		"services_count": len(searchResponse.Services),
	}).Debug("Service query response parsed successfully")

	// Convert to RemoteServiceInfo
	services := make([]RemoteServiceInfo, 0, len(searchResponse.Services))
	for _, svc := range searchResponse.Services {
		// Use peerID if available, otherwise fallback to peerAddr
		servicePeerID := peerID
		if servicePeerID == "" {
			servicePeerID = peerAddr
		}
		services = append(services, RemoteServiceInfo{
			ID:              svc.ID,
			Name:            svc.Name,
			Description:     svc.Description,
			ServiceType:     svc.ServiceType,
			Type:            svc.Type,
			Status:          svc.Status,
			PricingAmount:   svc.PricingAmount,
			PricingType:     svc.PricingType,
			PricingInterval: svc.PricingInterval,
			PricingUnit:     svc.PricingUnit,
			Capabilities:    svc.Capabilities,
			Hash:            svc.Hash,
			SizeBytes:       svc.SizeBytes,
			PeerID:          servicePeerID,
		})
	}

	return services, nil
}

// getPeerRelayInfo queries DHT for peer metadata to get relay information
func (ssh *ServiceSearchHandler) getPeerRelayInfo(peerID string, topic string) (relayAddr string, sessionID string, err error) {
	ssh.logger.WithFields(logrus.Fields{
		"peer_id": peerID[:8],
		"topic":   topic,
	}).Debug("Looking up relay info for peer")

	// Use "network" as default topic if none provided
	if topic == "" {
		topic = "network"
	}

	// Get peer from database
	peer, err := ssh.dbManager.KnownPeers.GetKnownPeer(peerID, topic)
	if err != nil {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
			"error":   err.Error(),
		}).Warn("Failed to get peer from database for relay lookup")
		return "", "", fmt.Errorf("peer not found in database: %v", err)
	}

	if peer == nil {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
		}).Warn("Peer not found in database (nil result)")
		return "", "", fmt.Errorf("peer not found in database")
	}

	if len(peer.PublicKey) == 0 {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
		}).Warn("Peer has no public key, cannot query DHT")
		return "", "", fmt.Errorf("peer has no public key")
	}

	// Query DHT for peer metadata (now with store node priority!)
	ssh.logger.WithFields(logrus.Fields{
		"peer_id": peerID[:8],
	}).Debug("Querying DHT for peer relay metadata")

	metadata, err := ssh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
			"error":   err.Error(),
		}).Warn("Failed to query peer metadata from DHT for relay info")
		return "", "", fmt.Errorf("failed to query metadata: %v", err)
	}

	// Check if peer is using a relay
	if !metadata.NetworkInfo.UsingRelay {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
		}).Debug("Peer is not using a relay")
		return "", "", fmt.Errorf("peer is not using a relay")
	}

	if metadata.NetworkInfo.RelayAddress == "" || metadata.NetworkInfo.RelaySessionID == "" {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id":       peerID[:8],
			"relay_address": metadata.NetworkInfo.RelayAddress,
			"session_id":    metadata.NetworkInfo.RelaySessionID,
		}).Warn("Peer metadata missing relay connection details")
		return "", "", fmt.Errorf("peer metadata missing relay info")
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_id":       peerID[:8],
		"relay_address": metadata.NetworkInfo.RelayAddress,
		"session_id":    metadata.NetworkInfo.RelaySessionID[:8],
	}).Debug("Successfully retrieved relay info for peer")

	return metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID, nil
}

// queryPeerViaRelay queries a peer's services through a relay
func (ssh *ServiceSearchHandler) queryPeerViaRelay(ctx context.Context, targetPeerID string, relayAddr string, sessionID string, query string, serviceType string, activeOnly bool) ([]RemoteServiceInfo, error) {
	ssh.logger.WithFields(logrus.Fields{
		"target_peer_id": targetPeerID[:8],
		"relay_address":  relayAddr,
		"session_id":     sessionID,
		"query":          query,
		"service_type":   serviceType,
		"active_only":    activeOnly,
	}).Debug("Starting service query via relay")

	// Create service search request
	if serviceType == "" {
		serviceType = "DATA,DOCKER,STANDALONE"
	}
	searchMsg := p2p.CreateServiceSearchRequest(query, serviceType, activeOnly)
	searchMsgBytes, err := searchMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %v", err)
	}

	// Create relay forward message wrapping the service search
	// Use persistent peer IDs for relay forwarding (relay indexes by peer ID, not node ID)
	forwardMsg := p2p.NewQUICMessage(p2p.MessageTypeRelayForward, &p2p.RelayForwardData{
		SessionID:    sessionID,
		SourcePeerID: ssh.ourPeerID, // Our persistent peer ID
		TargetPeerID: targetPeerID,  // Target's persistent peer ID
		MessageType:  "service_search",
		Payload:      searchMsgBytes,
		PayloadSize:  int64(len(searchMsgBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Check context before attempting connection
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled before connection: %v", ctx.Err())
	default:
	}

	// Connect to relay with timeout wrapper (ConnectToPeer doesn't accept context)
	type connResult struct {
		conn *interface{}
		err  error
	}
	connChan := make(chan connResult, 1)

	go func() {
		conn, err := ssh.quicPeer.ConnectToPeer(relayAddr)
		if err != nil {
			// Send error immediately without trying to wrap nil conn
			connChan <- connResult{conn: nil, err: err}
			return
		}
		// Only wrap connection if it's not nil
		var connInterface interface{} = conn
		connChan <- connResult{conn: &connInterface, err: nil}
	}()

	var conn interface{}
	select {
	case result := <-connChan:
		if result.err != nil {
			return nil, fmt.Errorf("failed to connect to relay: %v", result.err)
		}
		// Defensive check: ensure conn pointer is not nil
		if result.conn == nil {
			return nil, fmt.Errorf("relay connection is nil")
		}
		conn = *result.conn
	case <-ctx.Done():
		return nil, fmt.Errorf("relay connection timeout: %v", ctx.Err())
	}

	// Defensive check: ensure conn is not nil before type assertion
	if conn == nil {
		return nil, fmt.Errorf("relay connection is nil after result processing")
	}

	// Type assert to *quic.Conn directly
	quicConn, ok := conn.(*quic.Conn)
	if !ok {
		// Log actual type for debugging
		ssh.logger.WithFields(logrus.Fields{
			"actual_type": fmt.Sprintf("%T", conn),
			"relay_addr":  relayAddr,
		}).Warn("Connection type assertion failed")
		return nil, fmt.Errorf("invalid connection type: expected *quic.Conn, got %T", conn)
	}

	// Open stream with the parent context
	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send relay forward request
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return nil, fmt.Errorf("failed to send relay forward: %v", err)
	}

	// Read response with deadline
	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	buffer := make([]byte, 1024*1024) // 1MB buffer
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		// Real error (not just stream closure)
		return nil, fmt.Errorf("failed to read relay response: %v", err)
	}
	if n == 0 {
		// No data received (stream closed before response sent)
		return nil, fmt.Errorf("failed to read relay response: no data received (EOF)")
	}

	// Parse relay response - might be wrapped in RELAY_FORWARD or direct SERVICE_SEARCH_RESPONSE
	responseMsg, err := p2p.UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Check if response is wrapped in RELAY_FORWARD (relay forwarded the response back)
	if responseMsg.Type == p2p.MessageTypeRelayForward {
		var forwardData p2p.RelayForwardData
		if err := responseMsg.GetDataAs(&forwardData); err != nil {
			return nil, fmt.Errorf("failed to parse relay forward wrapper: %v", err)
		}
		// Unwrap the actual service search response from the payload
		responseMsg, err = p2p.UnmarshalQUICMessage(forwardData.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal wrapped response: %v", err)
		}
	}

	// Extract service search response
	var searchResponse p2p.ServiceSearchResponse
	if err := responseMsg.GetDataAs(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %v", err)
	}

	if searchResponse.Error != "" {
		return nil, fmt.Errorf("peer returned error: %s", searchResponse.Error)
	}

	// Convert to RemoteServiceInfo
	services := make([]RemoteServiceInfo, 0, len(searchResponse.Services))
	for _, svc := range searchResponse.Services {
		services = append(services, RemoteServiceInfo{
			ID:              svc.ID,
			Name:            svc.Name,
			Description:     svc.Description,
			ServiceType:     svc.ServiceType,
			Type:            svc.Type,
			Status:          svc.Status,
			PricingAmount:   svc.PricingAmount,
			PricingType:     svc.PricingType,
			PricingInterval: svc.PricingInterval,
			PricingUnit:     svc.PricingUnit,
			Capabilities:    svc.Capabilities,
			Hash:            svc.Hash,
			SizeBytes:       svc.SizeBytes,
			PeerID:          targetPeerID,
		})
	}

	return services, nil
}

// queryPeerViaLocalRelay queries a NAT peer connected to our own relay via the existing relay session
// This avoids the relay trying to connect to itself
func (ssh *ServiceSearchHandler) queryPeerViaLocalRelay(ctx context.Context, targetPeerID string, query string, serviceType string, activeOnly bool) ([]RemoteServiceInfo, error) {
	ssh.logger.WithFields(logrus.Fields{
		"target_peer_id": targetPeerID[:8],
		"query":          query,
		"service_type":   serviceType,
		"active_only":    activeOnly,
	}).Debug("Starting local relay query to connected NAT peer")

	// Check if relay is available
	if ssh.relayPeer == nil {
		return nil, fmt.Errorf("relay peer not available")
	}

	// Get the relay session connection for this NAT peer
	conn, err := ssh.relayPeer.GetClientConnection(targetPeerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get relay session for NAT peer %s: %v", targetPeerID[:8], err)
	}

	ssh.logger.WithFields(logrus.Fields{
		"target_peer_id": targetPeerID[:8],
	}).Debug("Querying NAT peer via relay session connection")

	// Use the existing relay session connection to send the query
	return ssh.queryPeerViaConnection(ctx, conn, targetPeerID, query, serviceType, activeOnly)
}

// queryPeerViaConnection queries a peer using an existing QUIC connection
// This is used when the relay wants to query a NAT peer connected to it via relay session
func (ssh *ServiceSearchHandler) queryPeerViaConnection(ctx context.Context, quicConn *quic.Conn, peerID string, query string, serviceType string, activeOnly bool) ([]RemoteServiceInfo, error) {
	ssh.logger.WithFields(logrus.Fields{
		"peer_id":      peerID[:8],
		"query":        query,
		"service_type": serviceType,
		"active_only":  activeOnly,
	}).Debug("Starting service query via existing connection")

	// Create service search request message
	if serviceType == "" {
		serviceType = "DATA,DOCKER,STANDALONE"
	}
	searchMsg := p2p.CreateServiceSearchRequest(query, serviceType, activeOnly)
	msgBytes, err := searchMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %v", err)
	}

	// Check context before attempting stream open
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled before opening stream: %v", ctx.Err())
	default:
	}

	// Open a new bidirectional stream on the existing connection
	ssh.logger.WithFields(logrus.Fields{
		"peer_id": peerID[:8],
	}).Debug("Opening stream on relay session connection")

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		ssh.logger.WithFields(logrus.Fields{
			"peer_id": peerID[:8],
			"error":   err.Error(),
		}).Warn("Failed to open stream on relay session connection")
		return nil, fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	ssh.logger.WithFields(logrus.Fields{
		"peer_id": peerID[:8],
	}).Debug("Stream opened successfully, sending service query message")

	// Send search request
	if _, err := stream.Write(msgBytes); err != nil {
		return nil, fmt.Errorf("failed to send search request: %v", err)
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_id":      peerID[:8],
		"message_size": len(msgBytes),
	}).Debug("Service query message sent successfully via relay session")

	// Read response with deadline
	stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	buffer := make([]byte, 1024*1024) // 1MB buffer for response
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read search response: %v", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("failed to read search response: no data received (EOF)")
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_id":    peerID[:8],
		"bytes_read": n,
	}).Debug("Received service query response via relay session")

	// Parse response
	responseMsg, err := p2p.UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Extract service search response
	var searchResponse p2p.ServiceSearchResponse
	if err := responseMsg.GetDataAs(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %v", err)
	}

	if searchResponse.Error != "" {
		return nil, fmt.Errorf("peer returned error: %s", searchResponse.Error)
	}

	ssh.logger.WithFields(logrus.Fields{
		"peer_id":        peerID[:8],
		"services_count": len(searchResponse.Services),
	}).Debug("Service query response parsed successfully from relay session")

	// Convert to RemoteServiceInfo
	services := make([]RemoteServiceInfo, 0, len(searchResponse.Services))
	for _, svc := range searchResponse.Services {
		services = append(services, RemoteServiceInfo{
			ID:              svc.ID,
			Name:            svc.Name,
			Description:     svc.Description,
			ServiceType:     svc.ServiceType,
			Type:            svc.Type,
			Status:          svc.Status,
			PricingAmount:   svc.PricingAmount,
			PricingType:     svc.PricingType,
			PricingInterval: svc.PricingInterval,
			PricingUnit:     svc.PricingUnit,
			Capabilities:    svc.Capabilities,
			Hash:            svc.Hash,
			SizeBytes:       svc.SizeBytes,
			PeerID:          peerID, // Use peer ID for relay session queries
		})
	}

	return services, nil
}

// sendPartialSearchResponse sends partial or final search results
func (ssh *ServiceSearchHandler) sendPartialSearchResponse(client *Client, services []RemoteServiceInfo, complete bool) error {
	responseMsg, err := NewMessage(MessageTypeServiceSearchResponse, ServiceSearchResponsePayload{
		Services: services,
		Complete: complete,
	})
	if err != nil {
		ssh.logger.WithError(err).Error("Failed to create partial search response message")
		return err
	}

	client.Send(responseMsg)
	return nil
}

// sendErrorResponse sends an error response to the client
func (ssh *ServiceSearchHandler) sendErrorResponse(client *Client, errorMsg string) error {
	responseMsg, err := NewMessage(MessageTypeServiceSearchResponse, ServiceSearchResponsePayload{
		Services: []RemoteServiceInfo{},
		Error:    errorMsg,
		Complete: true, // Error is final
	})
	if err != nil {
		ssh.logger.WithError(err).Error("Failed to create error response message")
		return err
	}

	client.Send(responseMsg)
	return nil
}

// classifyError classifies errors into specific types for better logging
func classifyError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// Check for timeout/deadline errors
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		return "timeout"
	}

	// Check for connection errors
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "no route to host") ||
		strings.Contains(errMsg, "network is unreachable") {
		return "unreachable"
	}

	// Check for context cancellation
	if strings.Contains(errMsg, "context canceled") {
		return "canceled"
	}

	return "unknown"
}

// getPeerIDFromConnection extracts peer ID from connection
func (ssh *ServiceSearchHandler) getPeerIDFromConnection(conn interface{}, addr string) string {
	// Get peer ID from QUIC connection metadata (stored during identity exchange)
	peerID, exists := ssh.quicPeer.GetPeerIDByAddress(addr)
	if !exists || peerID == "" {
		// No peer ID found, connection may not have completed identity exchange yet
		return ""
	}
	return peerID
}

// isInboundOnlyConnection checks if connection is from NAT peer
func (ssh *ServiceSearchHandler) isInboundOnlyConnection(addr string) bool {
	// Parse address to extract port
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return false
	}

	port := parts[1]
	portNum := 0
	fmt.Sscanf(port, "%d", &portNum)

	// Ephemeral ports are typically 49152-65535
	// These indicate the connection was initiated by the remote peer (inbound to us)
	// which suggests the remote peer is behind NAT
	if portNum >= 49152 {
		ssh.logger.WithFields(logrus.Fields{
			"address": addr,
			"port":    portNum,
		}).Debug("Detected ephemeral port - likely inbound NAT connection")
		return true
	}

	return false
}
