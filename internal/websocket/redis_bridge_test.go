package websocket_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/websocket"
)

func TestBridgeRateLimiter(t *testing.T) {
	rl := websocket.NewBridgeRateLimiter(2, time.Second)

	assert.True(t, rl.Allow("user-1"))
	assert.True(t, rl.Allow("user-1"))
	assert.False(t, rl.Allow("user-1"), "should be rate limited on 3rd request")

	// Different user should pass
	assert.True(t, rl.Allow("user-2"))
}

func TestRedisBridge_PublishesToHub(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()

	hub := websocket.NewHub()

	bridge := websocket.NewRedisBridge(hub, rdb)
	defer bridge.Close()

	payload, _ := json.Marshal(map[string]any{
		"circleId": "circle-123",
		"userId":   "user-abc",
		"status":   "active",
	})
	env, _ := json.Marshal(map[string]any{
		"type":    "user.updated",
		"payload": json.RawMessage(payload),
	})

	err = rdb.Publish(context.Background(), "moistello_ws_events", env).Err()
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
}
