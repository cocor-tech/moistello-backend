package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialLimitedClient(t *testing.T, hub *Hub, limit int64) *websocket.Conn {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := NewClient(hub, conn, "user-limit")
		client.SetMaxMessageSize(limit)
		hub.Register(client)
		go client.WritePump()
		go client.ReadPump()
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestClient_OversizedMessageClosesConnectionWith1009(t *testing.T) {
	hub := NewHub()
	conn := dialLimitedClient(t, hub, 64)
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "client did not register")

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 65))))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr, "server must close the connection with a close frame")
	assert.Equal(t, websocket.CloseMessageTooBig, closeErr.Code)

	waitFor(t, func() bool { return hub.ClientCount() == 0 }, "oversized sender must be unregistered from the hub")
}

func TestClient_MessageAtLimitIsAccepted(t *testing.T) {
	hub := NewHub()
	conn := dialLimitedClient(t, hub, 64)
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "client did not register")

	ping := `{"type":"ping","pad":"` + strings.Repeat("x", 40) + `"}`
	require.Len(t, ping, 64)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(ping)))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, reply, err := conn.ReadMessage()
	require.NoError(t, err, "a frame within the limit must not close the connection")
	assert.JSONEq(t, `{"type":"pong"}`, string(reply))
	assert.Equal(t, 1, hub.ClientCount())
}

func TestClient_SetMaxMessageSizeIgnoresNonPositive(t *testing.T) {
	c := NewClient(NewHub(), nil, "u")
	c.SetMaxMessageSize(0)
	c.SetMaxMessageSize(-1)
	assert.Equal(t, int64(DefaultMaxMessageSize), c.maxMessageSize)

	c.SetMaxMessageSize(1 << 20)
	assert.Equal(t, int64(1<<20), c.maxMessageSize)
}
