package websocket

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 4096

	// Send channel buffer size.
	sendBufferSize = 256

	// Max missed pings before closing stale connection.
	maxMissedPings = 3
)

// Client represents a single WebSocket connection. It is created when a
// client connects and destroyed when the connection is closed.
type Client struct {
	ID          string
	UserID      string
	Hub         *Hub
	Conn        *websocket.Conn
	Send        chan []byte
	missedPings atomic.Int32
	mu          sync.Mutex
}

// NewClient creates a new Client bound to the given Hub and WebSocket
// connection. The userID identifies the authenticated user.
func NewClient(hub *Hub, conn *websocket.Conn, userID string) *Client {
	return &Client{
		ID:     uuid.New().String(),
		UserID: userID,
		Hub:    hub,
		Conn:   conn,
		Send:   make(chan []byte, sendBufferSize),
	}
}

// ReadPump pumps messages from the WebSocket connection to the hub.
//
// The application runs ReadPump in a per-connection goroutine. It ensures
// there is at most one reader on a connection by executing all reads from
// this goroutine. When the connection is closed, the client is unregistered.
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.missedPings.Store(0)
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, msgBytes, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Warn().Err(err).Str("clientID", c.ID).Msg("websocket read error")
			}
			break
		}
		c.missedPings.Store(0)
		c.handleMessage(msgBytes)
	}
}

// WritePump pumps messages from the hub to the WebSocket connection.
//
// A goroutine running WritePump is started for each connection. It ensures
// there is at most one writer by executing all writes from this goroutine.
// Ping messages are sent periodically to keep the connection alive.
// If 3 consecutive pings pass without a pong/heartbeat response, the connection
// is closed to clean up stale connections.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the Send channel — close the connection
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Warn().Err(err).Str("clientID", c.ID).Msg("websocket write error")
				return
			}
		case <-ticker.C:
			if c.missedPings.Load() >= maxMissedPings {
				log.Warn().Str("clientID", c.ID).Int32("missedPings", c.missedPings.Load()).
					Msg("closing stale websocket connection due to missed pings")
				return
			}
			c.missedPings.Add(1)

			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// MissedPings returns the current count of missed heartbeat pings.
func (c *Client) MissedPings() int32 {
	return c.missedPings.Load()
}

// ResetMissedPings resets the missed heartbeat ping count to 0.
func (c *Client) ResetMissedPings() {
	c.missedPings.Store(0)
}

// handleMessage processes a single incoming WebSocket message.
// Supported message types:
//   - "ping": responds with a pong
//   - "subscribe": joins a circle room (requires "circleId" field)
//   - "unsubscribe": leaves a circle room (requires "circleId" field)
func (c *Client) handleMessage(data []byte) {
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Debug().Err(err).Str("clientID", c.ID).Msg("invalid websocket message")
		return
	}

	msgType, _ := msg["type"].(string)
	switch msgType {
	case "ping":
		select {
		case c.Send <- []byte(`{"type":"pong"}`):
		default:
		}
	case "subscribe":
		circleID, _ := msg["circleId"].(string)
		if circleID != "" {
			c.Hub.JoinRoom(circleID, c.ID)
		}
	case "unsubscribe":
		circleID, _ := msg["circleId"].(string)
		if circleID != "" {
			c.Hub.LeaveRoom(circleID, c.ID)
		}
	default:
		log.Debug().Str("type", msgType).Str("clientID", c.ID).Msg("unknown message type")
	}
}
