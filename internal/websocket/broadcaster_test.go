package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/chat"
)

func TestBroadcaster_ChatMessageDelivered_DeliversToRecipientOnly(t *testing.T) {
	hub := NewHub()
	recipient := &Client{ID: "c1", UserID: "user-b", Send: make(chan []byte, 256), Hub: hub}
	other := &Client{ID: "c2", UserID: "user-c", Send: make(chan []byte, 256), Hub: hub}
	hub.Register(recipient)
	hub.Register(other)

	b := NewBroadcaster(hub, nil)
	msg := &chat.Message{ID: "msg-1", ConversationID: "conv-1", SenderID: "user-a", Ciphertext: "ct", Nonce: "n"}
	b.ChatMessageDelivered(t.Context(), "user-b", msg)

	select {
	case data := <-recipient.Send:
		var envelope Message
		require.NoError(t, json.Unmarshal(data, &envelope))
		assert.Equal(t, "chat.message", envelope.Type)
	default:
		t.Fatal("expected recipient to receive a message")
	}

	select {
	case <-other.Send:
		t.Fatal("a different user must not receive another user's chat message")
	default:
	}
}

func TestBroadcaster_ChatMessageDelivered_NoOpWhenRecipientOffline(t *testing.T) {
	hub := NewHub()
	b := NewBroadcaster(hub, nil)

	assert.NotPanics(t, func() {
		b.ChatMessageDelivered(t.Context(), "no-such-user", &chat.Message{ID: "msg-1"})
	})
}

// broadcasterJoinedClient registers a client for userID and subscribes it to room.
func broadcasterJoinedClient(t *testing.T, hub *Hub, id, userID, room string) *Client {
	t.Helper()
	c := &Client{ID: id, UserID: userID, Send: make(chan []byte, 16), Hub: hub}
	hub.Register(c)
	require.True(t, hub.JoinRoom(room, c.ID))
	return c
}

// broadcasterNextMessage returns the next message queued for c, failing if there is none.
func broadcasterNextMessage(t *testing.T, c *Client) Message {
	t.Helper()
	select {
	case data := <-c.Send:
		var msg Message
		require.NoError(t, json.Unmarshal(data, &msg))
		return msg
	default:
		t.Fatal("expected a message to be queued for the client")
		return Message{}
	}
}

func assertNoBroadcasterMessage(t *testing.T, c *Client, why string) {
	t.Helper()
	select {
	case <-c.Send:
		t.Fatal(why)
	default:
	}
}

func assertPayload(t *testing.T, msg Message, want map[string]any) {
	t.Helper()
	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "payload should be a JSON object")
	for key, value := range want {
		assert.Equal(t, value, payload[key], key)
	}
	ts, ok := payload["timestamp"].(string)
	require.True(t, ok, "payload should carry a timestamp")
	_, err := time.Parse(time.RFC3339Nano, ts)
	assert.NoError(t, err)
}

func TestBroadcaster_RoomEvents_DeliverTypedPayloadToRoomSubscribersOnly(t *testing.T) {
	tests := []struct {
		name     string
		emit     func(b *Broadcaster, ctx context.Context)
		wantType string
		want     map[string]any
	}{
		{
			name:     "CircleCreated",
			emit:     func(b *Broadcaster, ctx context.Context) { b.CircleCreated(ctx, "room-1", "organizer-1") },
			wantType: "circle.created",
			want:     map[string]any{"circleId": "room-1", "organizerId": "organizer-1"},
		},
		{
			name:     "CircleStatusChanged",
			emit:     func(b *Broadcaster, ctx context.Context) { b.CircleStatusChanged(ctx, "room-1", "active") },
			wantType: "circle.status_changed",
			want:     map[string]any{"circleId": "room-1", "status": "active"},
		},
		{
			name:     "MemberJoined",
			emit:     func(b *Broadcaster, ctx context.Context) { b.MemberJoined(ctx, "room-1", "user-9") },
			wantType: "member.joined",
			want:     map[string]any{"circleId": "room-1", "userId": "user-9"},
		},
		{
			name:     "MemberLeft",
			emit:     func(b *Broadcaster, ctx context.Context) { b.MemberLeft(ctx, "room-1", "user-9") },
			wantType: "member.left",
			want:     map[string]any{"circleId": "room-1", "userId": "user-9"},
		},
		{
			name:     "ContributionRecorded",
			emit:     func(b *Broadcaster, ctx context.Context) { b.ContributionRecorded(ctx, "room-1", "user-9", 3, 25.5) },
			wantType: "contribution.recorded",
			want:     map[string]any{"circleId": "room-1", "userId": "user-9", "roundNumber": float64(3), "amount": 25.5},
		},
		{
			name:     "PayoutExecuted",
			emit:     func(b *Broadcaster, ctx context.Context) { b.PayoutExecuted(ctx, "room-1", "user-8", 3, 100) },
			wantType: "payout.executed",
			want:     map[string]any{"circleId": "room-1", "recipientId": "user-8", "roundNumber": float64(3), "amount": float64(100)},
		},
		{
			name:     "MemberPenalized",
			emit:     func(b *Broadcaster, ctx context.Context) { b.MemberPenalized(ctx, "room-1", "user-9", 3, 2.5) },
			wantType: "member.penalized",
			want:     map[string]any{"circleId": "room-1", "userId": "user-9", "roundNumber": float64(3), "penaltyAmount": 2.5},
		},
		{
			name:     "CommunityJoined",
			emit:     func(b *Broadcaster, ctx context.Context) { b.CommunityJoined(ctx, "room-1", "user-9") },
			wantType: "community.joined",
			want:     map[string]any{"communityId": "room-1", "userId": "user-9"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewHub()
			subscriber := broadcasterJoinedClient(t, hub, "c1", "user-1", "room-1")
			elsewhere := broadcasterJoinedClient(t, hub, "c2", "user-2", "room-2")

			tt.emit(NewBroadcaster(hub, nil), t.Context())

			msg := broadcasterNextMessage(t, subscriber)
			assert.Equal(t, tt.wantType, msg.Type)
			assertPayload(t, msg, tt.want)
			assertNoBroadcasterMessage(t, elsewhere, "a client in another room must not receive the event")
		})
	}
}

func TestBroadcaster_UserEvents_DeliverToThatUserOnly(t *testing.T) {
	tests := []struct {
		name     string
		emit     func(b *Broadcaster, ctx context.Context)
		wantType string
		want     map[string]any
	}{
		{
			name:     "UserUpdated",
			emit:     func(b *Broadcaster, ctx context.Context) { b.UserUpdated(ctx, "user-1") },
			wantType: "user.updated",
			want:     map[string]any{"userId": "user-1"},
		},
		{
			name:     "NotificationCreated",
			emit:     func(b *Broadcaster, ctx context.Context) { b.NotificationCreated(ctx, "user-1", "notif-7") },
			wantType: "notification.new",
			want:     map[string]any{"userId": "user-1", "notificationId": "notif-7"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewHub()
			target := &Client{ID: "c1", UserID: "user-1", Send: make(chan []byte, 16), Hub: hub}
			other := &Client{ID: "c2", UserID: "user-2", Send: make(chan []byte, 16), Hub: hub}
			hub.Register(target)
			hub.Register(other)

			tt.emit(NewBroadcaster(hub, nil), t.Context())

			msg := broadcasterNextMessage(t, target)
			assert.Equal(t, tt.wantType, msg.Type)
			assertPayload(t, msg, tt.want)
			assertNoBroadcasterMessage(t, other, "a different user must not receive the event")
		})
	}
}

func TestBroadcaster_Publish_SkipsLocalBroadcastForEmptyRoom(t *testing.T) {
	hub := NewHub()
	// JoinRoom("") succeeds without an authorizer, so the empty-room guard in
	// publish is the only thing keeping this client from the message.
	c := broadcasterJoinedClient(t, hub, "c1", "user-1", "")

	NewBroadcaster(hub, nil).publish("", Message{Type: "ping"})

	assertNoBroadcasterMessage(t, c, "publish with no room must not broadcast locally")
}

func TestBroadcaster_Publish_DropsMessageThatCannotBeMarshaled(t *testing.T) {
	hub := NewHub()
	member := broadcasterJoinedClient(t, hub, "c1", "user-1", "room-1")
	b := NewBroadcaster(hub, nil)
	unmarshalable := Message{Type: "bad", Payload: make(chan int)}

	assert.NotPanics(t, func() {
		b.publish("room-1", unmarshalable)
		b.publishToUser("user-1", unmarshalable)
	})

	assertNoBroadcasterMessage(t, member, "an unmarshalable message must not be delivered")
}

func TestBroadcaster_Publish_RelaysToRedisAndLocalSubscribers(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer func() { _ = rdb.Close() }()

	ctx := t.Context()
	pubsub := rdb.Subscribe(ctx, redisChannel)
	defer func() { _ = pubsub.Close() }()
	_, err = pubsub.Receive(ctx) // wait for the subscription to be confirmed
	require.NoError(t, err)

	hub := NewHub()
	member := broadcasterJoinedClient(t, hub, "c1", "user-1", "room-1")

	NewBroadcaster(hub, rdb).MemberJoined(ctx, "room-1", "user-9")

	select {
	case relayed := <-pubsub.Channel():
		var msg Message
		require.NoError(t, json.Unmarshal([]byte(relayed.Payload), &msg))
		assert.Equal(t, "member.joined", msg.Type)
		assertPayload(t, msg, map[string]any{"circleId": "room-1", "userId": "user-9"})
	case <-time.After(2 * time.Second):
		t.Fatal("expected the event to be published to Redis for other instances")
	}

	assert.Equal(t, "member.joined", broadcasterNextMessage(t, member).Type)
}

func TestBroadcaster_Publish_StillDeliversLocallyWhenRedisIsDown(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer func() { _ = rdb.Close() }()
	s.Close() // every publish from here on fails

	hub := NewHub()
	member := broadcasterJoinedClient(t, hub, "c1", "user-1", "room-1")

	assert.NotPanics(t, func() {
		NewBroadcaster(hub, rdb).CircleStatusChanged(t.Context(), "room-1", "completed")
	})

	assert.Equal(t, "circle.status_changed", broadcasterNextMessage(t, member).Type)
}
