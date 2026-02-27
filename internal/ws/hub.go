package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 8192
)

// Client represents a single WebSocket connection.
type Client struct {
	Hub    *Hub
	Conn   *websocket.Conn
	Send   chan []byte
	RoomID string
	UserID uint
}

// Hub maintains active rooms and broadcasts messages.
type Hub struct {
	Rooms      map[string]map[*Client]bool // roomID -> set of clients
	Register   chan *Client
	Unregister chan *Client
	Broadcast  chan *RoomMessage
	mu         sync.RWMutex
}

// RoomMessage carries a message destined for a specific room.
type RoomMessage struct {
	RoomID  string
	Message []byte
	Sender  *Client // nil for system messages
}

// NewHub creates and returns a new Hub instance.
func NewHub() *Hub {
	return &Hub{
		Rooms:      make(map[string]map[*Client]bool),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan *RoomMessage),
	}
}

// Run handles register, unregister, and broadcast events. It should be
// launched as a goroutine.
func (h *Hub) Run() {
	log := logger.Get()

	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			if h.Rooms[client.RoomID] == nil {
				h.Rooms[client.RoomID] = make(map[*Client]bool)
			}
			h.Rooms[client.RoomID][client] = true
			h.mu.Unlock()

			log.Info("client registered",
				zap.String("room_id", client.RoomID),
				zap.Uint("user_id", client.UserID),
			)

		case client := <-h.Unregister:
			h.mu.Lock()
			if clients, ok := h.Rooms[client.RoomID]; ok {
				if _, exists := clients[client]; exists {
					delete(clients, client)
					close(client.Send)
					if len(clients) == 0 {
						delete(h.Rooms, client.RoomID)
					}
				}
			}
			h.mu.Unlock()

			log.Info("client unregistered",
				zap.String("room_id", client.RoomID),
				zap.Uint("user_id", client.UserID),
			)

		case msg := <-h.Broadcast:
			h.mu.RLock()
			clients := h.Rooms[msg.RoomID]
			for client := range clients {
				// Skip sender if present
				if msg.Sender != nil && client == msg.Sender {
					continue
				}
				select {
				case client.Send <- msg.Message:
				default:
					// Client's send buffer is full; disconnect it.
					h.mu.RUnlock()
					h.mu.Lock()
					delete(h.Rooms[msg.RoomID], client)
					close(client.Send)
					if len(h.Rooms[msg.RoomID]) == 0 {
						delete(h.Rooms, msg.RoomID)
					}
					h.mu.Unlock()
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()
		}
	}
}

// ReadPump reads messages from the WebSocket connection. It is intended to be
// run in a per-client goroutine. The provided handler is called for each
// incoming message.
func (c *Client) ReadPump(handler func(*Client, []byte)) {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				logger.Get().Warn("unexpected websocket close",
					zap.String("room_id", c.RoomID),
					zap.Uint("user_id", c.UserID),
					zap.Error(err),
				)
			}
			break
		}
		handler(c, message)
	}
}

// WritePump sends messages from the Send channel to the WebSocket connection.
// It also sends periodic pings to keep the connection alive. It is intended to
// be run in a per-client goroutine.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel.
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
