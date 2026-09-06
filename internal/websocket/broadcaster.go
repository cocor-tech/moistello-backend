package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/domain/chat"
)

const redisChannel = "moistello:ws:events"

// Broadcaster relays real-time events to WebSocket clients and to other
// API server instances via Redis Pub/Sub.
type Broadcaster struct {
	hub *Hub
	rdb *redis.Client
}

// NewBroadcaster creates a Broadcaster backed by the given Hub and Redis client.
// The Redis client is used to publish events so that other API server instances
// can relay them to their own WebSocket clients.
func NewBroadcaster(hub *Hub, rdb *redis.Client) *Broadcaster {
	return &Broadcaster{hub: hub, rdb: rdb}
}

func (b *Broadcaster) publish(room string, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Warn().Err(err).Str("type", msg.Type).Msg("broadcaster marshal")
		return
	}

	// In-process broadcast to local Hub
	if room != "" {
		b.hub.Broadcast(room, msg)
	}

	// Cross-instance broadcast via Redis
	if b.rdb != nil {
		if err := b.rdb.Publish(context.Background(), redisChannel, data).Err(); err != nil {
			log.Warn().Err(err).Str("type", msg.Type).Msg("broadcaster redis publish")
		}
	}
}

func (b *Broadcaster) publishToUser(userID string, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Warn().Err(err).Str("type", msg.Type).Msg("broadcaster marshal")
		return
	}

	b.hub.BroadcastToUser(userID, msg)

	if b.rdb != nil {
		b.rdb.Publish(context.Background(), redisChannel, data)
	}
}

// ── Circle events ──

func (b *Broadcaster) CircleCreated(ctx context.Context, circleID, organizerID string) {
	b.publish(circleID, Message{Type: "circle.created", Payload: map[string]any{
		"circleId": circleID, "organizerId": organizerID, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) CircleStatusChanged(ctx context.Context, circleID, status string) {
	b.publish(circleID, Message{Type: "circle.status_changed", Payload: map[string]any{
		"circleId": circleID, "status": status, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) MemberJoined(ctx context.Context, circleID, userID string) {
	b.publish(circleID, Message{Type: "member.joined", Payload: map[string]any{
		"circleId": circleID, "userId": userID, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) MemberLeft(ctx context.Context, circleID, userID string) {
	b.publish(circleID, Message{Type: "member.left", Payload: map[string]any{
		"circleId": circleID, "userId": userID, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) ContributionRecorded(ctx context.Context, circleID, userID string, roundNumber int, amount float64) {
	b.publish(circleID, Message{Type: "contribution.recorded", Payload: map[string]any{
		"circleId": circleID, "userId": userID, "roundNumber": roundNumber,
		"amount": amount, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) PayoutExecuted(ctx context.Context, circleID, recipientID string, roundNumber int, amount float64) {
	b.publish(circleID, Message{Type: "payout.executed", Payload: map[string]any{
		"circleId": circleID, "recipientId": recipientID, "roundNumber": roundNumber,
		"amount": amount, "timestamp": time.Now().UTC(),
	}})
}

// MemberPenalized broadcasts a penalty imposed on a circle member for a missed
// contribution. Satisfies circle.Broadcaster.
func (b *Broadcaster) MemberPenalized(ctx context.Context, circleID, userID string, roundNumber int, penaltyAmount float64) {
	b.publish(circleID, Message{Type: "member.penalized", Payload: map[string]any{
		"circleId": circleID, "userId": userID, "roundNumber": roundNumber,
		"penaltyAmount": penaltyAmount, "timestamp": time.Now().UTC(),
	}})
}

// ── Community events ──

func (b *Broadcaster) CommunityJoined(ctx context.Context, communityID, userID string) {
	b.publish(communityID, Message{Type: "community.joined", Payload: map[string]any{
		"communityId": communityID, "userId": userID, "timestamp": time.Now().UTC(),
	}})
}

// ── User events ──

func (b *Broadcaster) UserUpdated(ctx context.Context, userID string) {
	b.publishToUser(userID, Message{Type: "user.updated", Payload: map[string]any{
		"userId": userID, "timestamp": time.Now().UTC(),
	}})
}

func (b *Broadcaster) NotificationCreated(ctx context.Context, userID, notificationID string) {
	b.publishToUser(userID, Message{Type: "notification.new", Payload: map[string]any{
		"userId": userID, "notificationId": notificationID, "timestamp": time.Now().UTC(),
	}})
}

// ── Chat events ──

// ChatMessageDelivered pushes a newly-sent E2EE chat message to the
// recipient's live WebSocket connection(s), if any are open. Satisfies
// chat.Broadcaster.
func (b *Broadcaster) ChatMessageDelivered(ctx context.Context, recipientID string, msg *chat.Message) {
	b.publishToUser(recipientID, Message{Type: "chat.message", Payload: msg})
}
