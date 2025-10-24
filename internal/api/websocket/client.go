package websocket

import (
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512 * 1024 // 512 KB
)

// Client represents a single WebSocket connection
type Client struct {
	// The WebSocket connection
	conn *websocket.Conn

	// Hub that manages this client
	hub *Hub

	// Buffered channel of outbound messages (structured)
	send chan *Message

	// Buffered channel of outbound messages (raw bytes for broadcasts)
	sendRaw chan []byte

	// Peer ID of the authenticated user
	peerID string

	// Logger
	logger *logrus.Logger
}

// NewClient creates a new Client instance
func NewClient(conn *websocket.Conn, hub *Hub, peerID string, logger *logrus.Logger) *Client {
	return &Client{
		conn:    conn,
		hub:     hub,
		send:    make(chan *Message, 256),
		sendRaw: make(chan []byte, 256),
		peerID:  peerID,
		logger:  logger,
	}
}

// readPump pumps messages from the WebSocket connection to the hub
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.WithError(err).WithField("peer_id", c.peerID).Warn("WebSocket read error")
			}
			break
		}

		// Parse incoming message
		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			c.logger.WithError(err).WithField("peer_id", c.peerID).Error("Failed to parse incoming message")
			continue
		}

		// Handle client messages (currently only ping)
		c.handleIncomingMessage(&msg)
	}
}

// writePump pumps messages from the hub to the WebSocket connection
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Marshal and send structured message
			messageBytes, err := json.Marshal(message)
			if err != nil {
				c.logger.WithError(err).Error("Failed to marshal message")
				continue
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, messageBytes); err != nil {
				c.logger.WithError(err).WithField("peer_id", c.peerID).Error("Failed to write message")
				return
			}

		case messageBytes, ok := <-c.sendRaw:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Send pre-marshaled message
			if err := c.conn.WriteMessage(websocket.TextMessage, messageBytes); err != nil {
				c.logger.WithError(err).WithField("peer_id", c.peerID).Error("Failed to write raw message")
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleIncomingMessage processes messages received from the client
func (c *Client) handleIncomingMessage(msg *Message) {
	switch msg.Type {
	case MessageTypePing:
		// Respond with pong
		pongMsg, err := NewMessage(MessageTypePong, nil)
		if err != nil {
			c.logger.WithError(err).Error("Failed to create pong message")
			return
		}
		c.send <- pongMsg

	default:
		c.logger.WithField("type", msg.Type).Debug("Received message from client")
		// Currently, we don't handle other message types from client
		// In the future, this could be extended for client->server requests
	}
}

// Start begins the read and write pumps for this client
func (c *Client) Start() {
	go c.writePump()
	go c.readPump()
}
