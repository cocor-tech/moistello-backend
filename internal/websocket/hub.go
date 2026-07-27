package websocket

import (
	"encoding/json"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/pkg/metrics"
)

// Message is a structured WebSocket message sent to clients.
type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Hub maintains the set of active WebSocket clients and manages circle-based
// rooms for targeted broadcasts.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client            // clientID -> Client
	rooms   map[string]map[string]*Client // circleID -> clientID -> Client
}

// NewHub creates a new Hub with empty client and room registries.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
		rooms:   make(map[string]map[string]*Client),
	}
}

// Register adds a client to the hub so it can receive broadcasts and updates metrics.
func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	if _, ok := h.clients[client.ID]; !ok {
		h.clients[client.ID] = client
		metrics.WSActiveConnections.Inc()
	}
	h.mu.Unlock()
	log.Debug().Str("clientID", client.ID).Msg("client registered")
}

// Unregister removes a client from the hub and all rooms it has joined.
// It is safe to call from any goroutine.
func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	if _, ok := h.clients[client.ID]; ok {
		delete(h.clients, client.ID)
		metrics.WSActiveConnections.Dec()
	}
	for _, room := range h.rooms {
		delete(room, client.ID)
	}
	h.mu.Unlock()
	log.Debug().Str("clientID", client.ID).Msg("client unregistered")
}

// JoinRoom subscribes a client to a circle's broadcast room.
func (h *Hub) JoinRoom(circleID, clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[circleID]; !ok {
		h.rooms[circleID] = make(map[string]*Client)
	}
	if client, ok := h.clients[clientID]; ok {
		h.rooms[circleID][clientID] = client
	}
	log.Debug().Str("circleID", circleID).Str("clientID", clientID).Msg("client joined room")
}

// LeaveRoom unsubscribes a client from a circle's broadcast room.
func (h *Hub) LeaveRoom(circleID, clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[circleID]; ok {
		delete(room, clientID)
	}
	log.Debug().Str("circleID", circleID).Str("clientID", clientID).Msg("client left room")
}

// Broadcast sends a message to all clients currently subscribed to a circle
// room. If the circle has no subscribers the message is silently dropped.
func (h *Hub) Broadcast(circleID string, msg Message) {
	h.mu.RLock()
	room, ok := h.rooms[circleID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Warn().Err(err).Str("type", msg.Type).Msg("marshaling broadcast message")
		return
	}

	for _, client := range room {
		select {
		case client.Send <- data:
		default:
			// Client's send buffer is full — assume disconnected
			go h.Unregister(client)
		}
	}
}

// BroadcastToUser sends a message to a specific user identified by userID.
// The userID maps to a registered Client; if no client is found the message
// is silently dropped.
func (h *Hub) BroadcastToUser(userID string, msg Message) {
	h.mu.RLock()
	client, ok := h.clients[userID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Warn().Err(err).Str("type", msg.Type).Str("userID", userID).Msg("marshaling user message")
		return
	}

	select {
	case client.Send <- data:
	default:
		go h.Unregister(client)
	}
}

// Stats returns the current number of connected clients and active rooms.
func (h *Hub) Stats() (clients int, rooms int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients), len(h.rooms)
}

// ClientCount returns the total number of registered clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RoomCount returns the total number of active rooms.
func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}
