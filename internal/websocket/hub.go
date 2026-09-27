package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/pkg/metrics"
)

// Message is a structured WebSocket message sent to clients.
type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// SubscriptionAuthorizer decides whether a client may subscribe to a given
// circle room. Implementations should query the membership table.
type SubscriptionAuthorizer interface {
	CanSubscribe(ctx context.Context, circleID, userID string) (bool, error)
}

// Hub maintains the set of active WebSocket clients and manages circle-based
// rooms for targeted broadcasts.
type Hub struct {
	mu          sync.RWMutex
	clients     map[string]*Client            // clientID -> Client
	userClients map[string]map[string]*Client // userID -> clientID -> Client
	rooms       map[string]map[string]*Client // circleID -> clientID -> Client
	auth        SubscriptionAuthorizer
	closed      bool // set by Shutdown; Register rejects new clients afterwards
}

// shutdownPollInterval is how often Shutdown re-checks the client count.
const shutdownPollInterval = 10 * time.Millisecond

// NewHub creates a new Hub with empty client and room registries.
func NewHub() *Hub {
	return &Hub{
		clients:     make(map[string]*Client),
		userClients: make(map[string]map[string]*Client),
		rooms:       make(map[string]map[string]*Client),
	}
}

// Register adds a client to the hub so it can receive broadcasts and updates metrics.
func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		log.Debug().Str("clientID", client.ID).Msg("hub is shutting down; refusing new client")
		client.Close()
		return
	}
	if _, ok := h.clients[client.ID]; !ok {
		h.clients[client.ID] = client
		if h.userClients[client.UserID] == nil {
			h.userClients[client.UserID] = make(map[string]*Client)
		}
		h.userClients[client.UserID][client.ID] = client
		metrics.WSActiveConnections.Inc()
	}
	h.mu.Unlock()
	log.Debug().Str("clientID", client.ID).Str("userID", client.UserID).Msg("client registered")
}

// Unregister removes a client from the hub and all rooms it has joined.
// It is safe to call from any goroutine.
func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	if _, ok := h.clients[client.ID]; ok {
		delete(h.clients, client.ID)
		metrics.WSActiveConnections.Dec()
	}
	if userMap, ok := h.userClients[client.UserID]; ok {
		delete(userMap, client.ID)
		if len(userMap) == 0 {
			delete(h.userClients, client.UserID)
		}
	}
	// Drop the client from every room, and forget rooms it emptied. Leaving
	// empty room entries behind would inflate RoomCount()/Stats() and grow the
	// map for the lifetime of the process.
	for circleID, room := range h.rooms {
		delete(room, client.ID)
		if len(room) == 0 {
			delete(h.rooms, circleID)
		}
	}
	h.mu.Unlock()
	log.Debug().Str("clientID", client.ID).Msg("client unregistered")
}

const (
	// CloseCircleMembershipRevoked is the distinct WebSocket close code
	// sent when a user's circle membership is revoked mid-session.
	CloseCircleMembershipRevoked  = 4403
	ReasonCircleMembershipRevoked = "circle membership revoked"
)

// SetSubscriptionAuthorizer sets the authorizer used to check circle membership
// before allowing a client to join a room.
func (h *Hub) SetSubscriptionAuthorizer(auth SubscriptionAuthorizer) {
	h.auth = auth
}

// JoinRoom subscribes a client to a circle's broadcast room. It returns true
// if the client is allowed to join (membership verified) and false otherwise.
func (h *Hub) JoinRoom(circleID, clientID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.auth != nil {
		client, ok := h.clients[clientID]
		if !ok {
			return false
		}
		allowed, err := h.auth.CanSubscribe(context.Background(), circleID, client.UserID)
		if err != nil || !allowed {
			return false
		}
	}

	if _, ok := h.rooms[circleID]; !ok {
		h.rooms[circleID] = make(map[string]*Client)
	}
	if client, ok := h.clients[clientID]; ok {
		h.rooms[circleID][clientID] = client
	}
	log.Debug().Str("circleID", circleID).Str("clientID", clientID).Msg("client joined room")
	return true
}

// LeaveRoom unsubscribes a client from a circle's broadcast room. The room
// entry is dropped once its last subscriber leaves, so RoomCount reflects
// rooms that can actually receive a broadcast.
func (h *Hub) LeaveRoom(circleID, clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[circleID]; ok {
		delete(room, clientID)
		if len(room) == 0 {
			delete(h.rooms, circleID)
		}
	}
	log.Debug().Str("circleID", circleID).Str("clientID", clientID).Msg("client left room")
}

// AuditRoomMemberships re-checks circle membership for all clients currently
// subscribed to circle rooms. If a member has been removed or circle access
// lapsed, the client is unsubscribed from the room and sent an authorization
// error message.
func (h *Hub) AuditRoomMemberships(ctx context.Context) {
	if h.auth == nil {
		return
	}

	type roomSubscription struct {
		circleID string
		client   *Client
	}

	h.mu.RLock()
	var subs []roomSubscription
	for circleID, room := range h.rooms {
		for _, client := range room {
			subs = append(subs, roomSubscription{
				circleID: circleID,
				client:   client,
			})
		}
	}
	h.mu.RUnlock()

	for _, sub := range subs {
		allowed, err := h.auth.CanSubscribe(ctx, sub.circleID, sub.client.UserID)
		if err != nil || !allowed {
			log.Info().
				Str("circleID", sub.circleID).
				Str("clientID", sub.client.ID).
				Str("userID", sub.client.UserID).
				Msg("revoking circle room subscription due to lapsed membership")

			h.LeaveRoom(sub.circleID, sub.client.ID)
			sub.client.sendError("circle membership lapsed; unsubscribed from room")
		}
	}
}

// StartMembershipAuditor periodically audits circle room subscriptions to
// remove clients whose membership has been revoked mid-session.
func (h *Hub) StartMembershipAuditor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.AuditRoomMemberships(ctx)
		}
	}
}

// Broadcast sends a message to all clients currently subscribed to a circle
// room. If the circle has no subscribers the message is silently dropped.
func (h *Hub) Broadcast(circleID string, msg Message) {
	h.mu.RLock()
	room, ok := h.rooms[circleID]
	if !ok {
		h.mu.RUnlock()
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		h.mu.RUnlock()
		log.Warn().Err(err).Str("type", msg.Type).Msg("marshaling broadcast message")
		return
	}

	clients := make([]*Client, 0, len(room))
	for _, client := range room {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	var dropped []*Client
	for _, client := range clients {
		select {
		case client.Send <- data:
		default:
			// Client's send buffer is full — record metric and mark for deterministic unregister
			metrics.WSDroppedMessagesTotal.Inc()
			dropped = append(dropped, client)
		}
	}

	for _, client := range dropped {
		metrics.WSSlowClientsDisconnectedTotal.Inc()
		log.Warn().Str("clientID", client.ID).Str("userID", client.UserID).Msg("disconnecting slow websocket client due to backpressure overflow")
		h.Unregister(client)
		// Unregister alone only stops new deliveries; the socket and both
		// pumps would stay alive with nobody left to serve them. Close it so
		// the peer learns it was dropped and the connection is reclaimed.
		client.Disconnect()
	}
}

// BroadcastToUser sends a message to a specific user identified by userID.
// Delivers to all connections of the user. If no client is found the message
// is silently dropped.
func (h *Hub) BroadcastToUser(userID string, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Warn().Err(err).Str("type", msg.Type).Str("userID", userID).Msg("marshaling user message")
		return
	}

	h.mu.RLock()
	userConns := h.userClients[userID]
	var targets []*Client
	for _, client := range userConns {
		targets = append(targets, client)
	}
	h.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	for _, client := range targets {
		select {
		case client.Send <- data:
		default:
			metrics.WSDroppedMessagesTotal.Inc()
			metrics.WSSlowClientsDisconnectedTotal.Inc()
			log.Warn().Str("clientID", client.ID).Str("userID", client.UserID).Msg("disconnecting slow client on user broadcast backpressure")
			h.Unregister(client)
			client.Disconnect()
		}
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

// UserClientCount returns the number of registered clients for a given user.
func (h *Hub) UserClientCount(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.userClients[userID])
}

// RoomCount returns the total number of active rooms.
func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

// Shutdown stops accepting clients and asks every connected client to close.
// It waits until all clients have unregistered or ctx expires; connections
// still open at the deadline are closed forcibly so that the pools they
// depend on can be shut down afterwards. Calling Shutdown twice is safe.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	clients := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	log.Info().Int("clients", len(clients)).Msg("draining websocket clients")
	// Each client gets its own jittered reconnect hint so the fleet does not
	// stampede back the moment the server returns.
	spread := reconnectSpread(len(clients))
	for _, c := range clients {
		c.reconnectAfter.Store(int64(reconnectDelay(spread)))
		c.Close()
	}

	ticker := time.NewTicker(shutdownPollInterval)
	defer ticker.Stop()
	for h.ClientCount() > 0 {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			remaining := make([]*Client, 0, len(h.clients))
			for _, c := range h.clients {
				remaining = append(remaining, c)
			}
			h.mu.Unlock()
			for _, c := range remaining {
				if c.Conn != nil {
					_ = c.Conn.Close()
				}
				h.Unregister(c)
			}
			log.Warn().Int("forced", len(remaining)).Msg("websocket clients did not close in time; connections closed forcibly")
			return ctx.Err()
		case <-ticker.C:
		}
	}
	log.Info().Msg("all websocket clients drained")
	return nil
}
