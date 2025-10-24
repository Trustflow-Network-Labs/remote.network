package websocket

import (
	"encoding/json"
	"sync"

	"github.com/sirupsen/logrus"
)

// Hub maintains the set of active clients and broadcasts messages to them
type Hub struct {
	// Registered clients
	clients map[*Client]bool

	// Inbound messages from clients
	broadcast chan *Message

	// Register requests from clients
	register chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Mutex to protect clients map
	mu sync.RWMutex

	// Logger
	logger *logrus.Logger
}

// NewHub creates a new Hub instance
func NewHub(logger *logrus.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan *Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		logger:     logger,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.logger.WithField("peer_id", client.peerID).Info("WebSocket client connected")
			h.logger.WithField("count", len(h.clients)).Debug("Active WebSocket clients")

			// Send connection confirmation message
			connectedMsg, err := NewMessage(MessageTypeConnected, ConnectedPayload{
				Message: "Connected to WebSocket server",
				PeerID:  client.peerID,
			})
			if err == nil {
				client.send <- connectedMsg
			}

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.logger.WithField("peer_id", client.peerID).Info("WebSocket client disconnected")
				h.logger.WithField("count", len(h.clients)).Debug("Active WebSocket clients")
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			// Marshal message to JSON once
			messageBytes, err := json.Marshal(message)
			if err != nil {
				h.logger.WithError(err).Error("Failed to marshal broadcast message")
				continue
			}

			h.mu.RLock()
			clientCount := len(h.clients)
			h.mu.RUnlock()

			if clientCount == 0 {
				// No clients connected, skip broadcast
				continue
			}

			h.logger.WithFields(logrus.Fields{
				"type":         message.Type,
				"client_count": clientCount,
			}).Debug("Broadcasting message to clients")

			// Send to all connected clients
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.sendRaw <- messageBytes:
					// Message sent successfully
				default:
					// Client's send channel is full, close the connection
					go func(c *Client) {
						h.logger.WithField("peer_id", c.peerID).Warn("Client send buffer full, closing connection")
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients
func (h *Hub) Broadcast(message *Message) {
	h.broadcast <- message
}

// BroadcastPayload creates a message with the given type and payload, then broadcasts it
func (h *Hub) BroadcastPayload(msgType MessageType, payload interface{}) error {
	message, err := NewMessage(msgType, payload)
	if err != nil {
		return err
	}
	h.Broadcast(message)
	return nil
}

// ClientCount returns the number of connected clients
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RegisterClient sends a client to the register channel
func (h *Hub) RegisterClient(client *Client) {
	h.register <- client
}

// UnregisterClient sends a client to the unregister channel
func (h *Hub) UnregisterClient(client *Client) {
	h.unregister <- client
}
