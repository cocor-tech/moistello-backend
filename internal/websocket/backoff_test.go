package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconnectSpread_ScalesAndClamps(t *testing.T) {
	assert.Equal(t, reconnectMinDelay, reconnectSpread(0))
	assert.Equal(t, 10*time.Second, reconnectSpread(5000))
	assert.Equal(t, reconnectMaxSpread, reconnectSpread(10_000_000))
}

func TestReconnectHints_SmoothArrivalRate(t *testing.T) {
	const clients = 5000
	spread := reconnectSpread(clients)

	buckets := make(map[int]int)
	for i := 0; i < clients; i++ {
		d := reconnectDelay(spread)
		require.GreaterOrEqual(t, d, reconnectMinDelay)
		require.Less(t, d, reconnectMinDelay+spread)
		buckets[int((d-reconnectMinDelay)/time.Second)]++
	}

	// Without jitter every client would arrive in the same instant. With it,
	// no one-second bucket may hold much more than the target rate.
	for sec, n := range buckets {
		assert.LessOrEqual(t, n, reconnectTargetRate*2, "arrival rate spiked at second %d", sec)
	}
	assert.GreaterOrEqual(t, len(buckets), int(spread/time.Second)-1)
}

func TestShutdownCloseReason_RoundTrip(t *testing.T) {
	d, ok := parseReconnectAfter(shutdownCloseReason(2500 * time.Millisecond))
	require.True(t, ok)
	assert.Equal(t, 2500*time.Millisecond, d)

	_, ok = parseReconnectAfter("server shutting down")
	assert.False(t, ok)
}

func TestHub_Shutdown_HintsAreJitteredAndReconnectsFitOneCycle(t *testing.T) {
	hub := NewHub()
	const n = 12
	var conns []*websocket.Conn
	for i := 0; i < n; i++ {
		conn, cleanup := dialTestClient(t, hub, "user")
		defer cleanup()
		conns = append(conns, conn)
	}
	waitFor(t, func() bool { return hub.ClientCount() == n }, "clients did not register")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, hub.Shutdown(ctx))

	spread := reconnectSpread(n)
	distinct := make(map[time.Duration]struct{})
	for _, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := conn.ReadMessage()
		var closeErr *websocket.CloseError
		require.ErrorAs(t, err, &closeErr)
		d, ok := parseReconnectAfter(closeErr.Text)
		require.True(t, ok)
		assert.GreaterOrEqual(t, d, reconnectMinDelay)
		assert.Less(t, d, reconnectMinDelay+spread, "every client must be able to reconnect within one backoff window")
		distinct[d] = struct{}{}
	}
	assert.Greater(t, len(distinct), 1, "hints must be jittered per client")
}

func TestTokenBucket_LimitsAndRefills(t *testing.T) {
	var b tokenBucket
	now := time.Now()
	for i := 0; i < 3; i++ {
		assert.True(t, b.allow(now, 3, 1))
	}
	assert.False(t, b.allow(now, 3, 1), "burst exhausted")
	assert.True(t, b.allow(now.Add(1100*time.Millisecond), 3, 1), "token refilled after a second")
}

func TestClient_SubscribeIsRateLimited(t *testing.T) {
	hub := NewHub()
	c := &Client{ID: "c1", UserID: "u1", Send: make(chan []byte, 2*subscribeBurst), Hub: hub}
	hub.Register(c)

	for i := 0; i < subscribeBurst; i++ {
		c.handleMessage([]byte(`{"type":"subscribe","circleId":"circle-1"}`))
	}
	assert.Empty(t, c.Send, "subscribes within the burst succeed silently")

	c.handleMessage([]byte(`{"type":"subscribe","circleId":"circle-1"}`))
	select {
	case msg := <-c.Send:
		assert.Contains(t, string(msg), "rate limited")
	default:
		t.Fatal("expected a rate limit error")
	}
}
