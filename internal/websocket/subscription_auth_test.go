package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type stubAuthorizer struct {
	allowed bool
	err     error
}

func (s stubAuthorizer) CanSubscribe(ctx context.Context, circleID, userID string) (bool, error) {
	return s.allowed, s.err
}

func TestHub_JoinRoomDeniedForNonMember(t *testing.T) {
	hub := NewHub()
	client := &Client{ID: "c1", UserID: "u1", Send: make(chan []byte, 10), Hub: hub}
	hub.Register(client)
	hub.SetSubscriptionAuthorizer(stubAuthorizer{allowed: false})

	assert.False(t, hub.JoinRoom("circle-123", "c1"))
	_, rooms := hub.Stats()
	assert.Equal(t, 0, rooms)
}

func TestHub_JoinRoomAllowedForMember(t *testing.T) {
	hub := NewHub()
	client := &Client{ID: "c1", UserID: "u1", Send: make(chan []byte, 10), Hub: hub}
	hub.Register(client)
	hub.SetSubscriptionAuthorizer(stubAuthorizer{allowed: true})

	assert.True(t, hub.JoinRoom("circle-123", "c1"))
	_, rooms := hub.Stats()
	assert.Equal(t, 1, rooms)
}

func TestClient_HandleSubscribeDeniedForNonMember(t *testing.T) {
	hub := NewHub()
	client := &Client{ID: "c1", UserID: "u1", Send: make(chan []byte, 10), Hub: hub}
	hub.Register(client)
	hub.SetSubscriptionAuthorizer(stubAuthorizer{allowed: false})

	client.handleMessage([]byte(`{"type":"subscribe","circleId":"circle-123"}`))
	_, rooms := hub.Stats()
	assert.Equal(t, 0, rooms)

	select {
	case msg := <-client.Send:
		assert.Contains(t, string(msg), "error")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected error message")
	}
}

type dynamicAuthorizer struct {
	memberships map[string]bool
}

func (d *dynamicAuthorizer) CanSubscribe(ctx context.Context, circleID, userID string) (bool, error) {
	return d.memberships[circleID+":"+userID], nil
}

func TestHub_AuditRoomMemberships_RemovesRevokedMemberAndStopsDelivery(t *testing.T) {
	hub := NewHub()
	auth := &dynamicAuthorizer{
		memberships: map[string]bool{
			"circle-1:u1": true,
			"circle-1:u2": true,
		},
	}
	hub.SetSubscriptionAuthorizer(auth)

	c1 := &Client{ID: "c1", UserID: "u1", Send: make(chan []byte, 10), Hub: hub}
	c2 := &Client{ID: "c2", UserID: "u2", Send: make(chan []byte, 10), Hub: hub}
	hub.Register(c1)
	hub.Register(c2)

	assert.True(t, hub.JoinRoom("circle-1", "c1"))
	assert.True(t, hub.JoinRoom("circle-1", "c2"))

	// Revoke u1's membership
	auth.memberships["circle-1:u1"] = false

	// Run audit
	hub.AuditRoomMemberships(context.Background())

	// Broadcast an event
	hub.Broadcast("circle-1", Message{Type: "round.started", Payload: map[string]string{"circleId": "circle-1"}})

	// c2 should receive the broadcast
	select {
	case msg := <-c2.Send:
		assert.Contains(t, string(msg), "round.started")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected broadcast message on c2")
	}

	// c1 should have received error/removal notification and NOT the broadcast
	var c1Messages []string
	drain:
	for {
		select {
		case msg := <-c1.Send:
			c1Messages = append(c1Messages, string(msg))
		default:
			break drain
		}
	}

	for _, m := range c1Messages {
		assert.NotContains(t, m, "round.started", "revoked client must not receive room broadcasts")
	}
}
