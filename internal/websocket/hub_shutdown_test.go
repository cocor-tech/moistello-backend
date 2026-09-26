package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dialTestClient upgrades a real connection against hub and starts both
// pumps, mirroring what the HTTP handler does in production.
func dialTestClient(t *testing.T, hub *Hub, userID string) (*websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := NewClient(hub, conn, userID)
		hub.Register(client)
		go client.WritePump()
		go client.ReadPump()
	}))
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	return conn, func() { conn.Close(); srv.Close() }
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestHub_Shutdown_SendsCloseFrameAndDrains(t *testing.T) {
	hub := NewHub()
	conn1, cleanup1 := dialTestClient(t, hub, "user-1")
	defer cleanup1()
	conn2, cleanup2 := dialTestClient(t, hub, "user-2")
	defer cleanup2()
	waitFor(t, func() bool { return hub.ClientCount() == 2 }, "clients did not register")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, hub.Shutdown(ctx))
	assert.Equal(t, 0, hub.ClientCount(), "every client must be unregistered before Shutdown returns")

	for _, conn := range []*websocket.Conn{conn1, conn2} {
		conn.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := conn.ReadMessage()
		var closeErr *websocket.CloseError
		require.ErrorAs(t, err, &closeErr, "peer must receive a close frame")
		assert.Equal(t, websocket.CloseGoingAway, closeErr.Code)
		assert.Equal(t, "server shutting down", closeErr.Text)
	}

	// Idempotent.
	require.NoError(t, hub.Shutdown(context.Background()))
}

func TestHub_Shutdown_RefusesNewClients(t *testing.T) {
	hub := NewHub()
	require.NoError(t, hub.Shutdown(context.Background()))

	conn, cleanup := dialTestClient(t, hub, "late-user")
	defer cleanup()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr, "a client arriving during shutdown is closed immediately")
	assert.Equal(t, websocket.CloseGoingAway, closeErr.Code)
	assert.Equal(t, 0, hub.ClientCount())
}

func TestHub_Shutdown_ForcesStuckClientsAtDeadline(t *testing.T) {
	hub := NewHub()
	// A client with no pumps never processes the close signal on its own.
	stuck := &Client{ID: "stuck", UserID: "u", Hub: hub, Send: make(chan []byte, 1)}
	hub.Register(stuck)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := hub.Shutdown(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 0, hub.ClientCount(), "stuck clients are unregistered forcibly")
}
