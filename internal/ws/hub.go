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

	// Maximum number of slow handlers (cooking QA, voice processing) that may
	// run concurrently per client. Cheap handlers (ping, step_change,
	// ephemeral edits) are not bounded by this.
	maxConcurrentHandlers = 2
)

// Client represents a single WebSocket connection.
type Client struct {
	Hub    *Hub
	Conn   *websocket.Conn
	Send   chan []byte
	RoomID string
	UserID uint

	// done is closed exactly once (via closeOnce) when the client is
	// unregistered or evicted by the hub. The Send channel itself is NEVER
	// closed, so concurrent handler goroutines can never panic with
	// send-on-closed-channel; they observe done instead.
	done      chan struct{}
	closeOnce sync.Once

	// handlerSem bounds concurrent long-running message handlers per client.
	handlerSem chan struct{}

	// currentStep tracks which recipe step the user is viewing, guarded by
	// stepMu. hasStep distinguishes "step 0" from "never reported".
	stepMu      sync.RWMutex
	currentStep int
	hasStep     bool
}

// NewClient creates a Client with its channels initialized.
func NewClient(hub *Hub, conn *websocket.Conn, roomID string, userID uint) *Client {
	return &Client{
		Hub:        hub,
		Conn:       conn,
		Send:       make(chan []byte, 256),
		RoomID:     roomID,
		UserID:     userID,
		done:       make(chan struct{}),
		handlerSem: make(chan struct{}, maxConcurrentHandlers),
	}
}

// markClosed signals that the client is dead. Safe to call multiple times
// (e.g. eviction by the hub followed by the read pump unregistering).
func (c *Client) markClosed() {
	c.closeOnce.Do(func() {
		close(c.done)
	})
}

// TrySend delivers a message to the client's send buffer. It returns false if
// the client has been closed. Because the Send channel is never closed, this
// can never panic; a send blocked on a full buffer is released when the hub
// closes the client.
func (c *Client) TrySend(message []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.Send <- message:
		return true
	case <-c.done:
		return false
	}
}

// tryAcquireHandlerSlot reserves a slot for a long-running handler. It
// returns false without blocking if the client already has the maximum number
// of handlers in flight.
func (c *Client) tryAcquireHandlerSlot() bool {
	select {
	case c.handlerSem <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseHandlerSlot frees a slot reserved by tryAcquireHandlerSlot.
func (c *Client) releaseHandlerSlot() {
	<-c.handlerSem
}

// SetCurrentStep records which recipe step the user is currently viewing.
func (c *Client) SetCurrentStep(step int) {
	c.stepMu.Lock()
	c.currentStep = step
	c.hasStep = true
	c.stepMu.Unlock()
}

// CurrentStep returns the user's current recipe step. The bool is false if
// the client has never reported a step.
func (c *Client) CurrentStep() (int, bool) {
	c.stepMu.RLock()
	defer c.stepMu.RUnlock()
	return c.currentStep, c.hasStep
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
					if len(clients) == 0 {
						delete(h.Rooms, client.RoomID)
					}
				}
			}
			h.mu.Unlock()

			// Never close client.Send: handler goroutines may still be
			// sending. Signal death via the done channel instead.
			client.markClosed()

			log.Info("client unregistered",
				zap.String("room_id", client.RoomID),
				zap.Uint("user_id", client.UserID),
			)

		case msg := <-h.Broadcast:
			// Collect clients that can't keep up during a read-locked
			// iteration, then evict them afterwards under a write lock.
			// Mutating the map while ranging over it under a dropped read
			// lock is racy.
			var doomed []*Client
			h.mu.RLock()
			for client := range h.Rooms[msg.RoomID] {
				// Skip sender if present
				if msg.Sender != nil && client == msg.Sender {
					continue
				}
				select {
				case client.Send <- msg.Message:
				default:
					// Client's send buffer is full; evict it below.
					doomed = append(doomed, client)
				}
			}
			h.mu.RUnlock()

			if len(doomed) > 0 {
				h.mu.Lock()
				for _, client := range doomed {
					if clients, ok := h.Rooms[msg.RoomID]; ok {
						if _, exists := clients[client]; exists {
							delete(clients, client)
							if len(clients) == 0 {
								delete(h.Rooms, msg.RoomID)
							}
						}
					}
				}
				h.mu.Unlock()

				for _, client := range doomed {
					client.markClosed()
					log.Warn("evicted slow websocket client",
						zap.String("room_id", client.RoomID),
						zap.Uint("user_id", client.UserID),
					)
				}
			}
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
		case message := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}

		case <-c.done:
			// The hub closed this client.
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
			return

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
