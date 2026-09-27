package websocket

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seqMessage is the payload used by the ordering tests. Each publisher owns a
// disjoint Seq sequence so a reader can tell a reordering apart from a loss.
type seqMessage struct {
	Type    string `json:"type"`
	Payload struct {
		Publisher int `json:"publisher"`
		Seq       int `json:"seq"`
	} `json:"payload"`
}

// clientIDForUser finds the registered client id for a user, so tests can put
// the connection into a room without reaching into dialTestClient.
func clientIDForUser(t *testing.T, hub *Hub, userID string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		for id, c := range hub.clients {
			if c.UserID == userID {
				hub.mu.RUnlock()
				return id
			}
		}
		hub.mu.RUnlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no client registered for user %q", userID)
	return ""
}

// readSeqMessages reads until want messages have arrived or the deadline
// passes, returning what was received.
func readSeqMessages(t *testing.T, conn *websocket.Conn, want int) []seqMessage {
	t.Helper()
	out := make([]seqMessage, 0, want)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for len(out) < want {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read failed after %d/%d messages: %v", len(out), want, err)
		}
		var msg seqMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal %q: %v", data, err)
		}
		out = append(out, msg)
	}
	return out
}

// TestHub_PerConnectionFIFOUnderConcurrentPublishes is the core ordering
// guarantee for #447: messages a single connection accepts are delivered in
// the order they were accepted, even when many publishers race to enqueue
// them.
//
// The assertion is deliberately per-publisher FIFO rather than a global total
// order. Go does not order sends from concurrent goroutines, so no
// implementation can promise that publisher A's message 1 precedes publisher
// B's message 1. What must hold, and what this test pins down, is that each
// publisher's own stream is never reordered, and that nothing is lost or
// duplicated while the consumer keeps up.
func TestHub_PerConnectionFIFOUnderConcurrentPublishes(t *testing.T) {
	const (
		publishers     = 6
		messagesEach   = 20
		totalToDeliver = publishers * messagesEach
		joinCircle     = "circle-ordering"
		subscriberUser = "user-subscriber"
	)

	hub := NewHub()
	conn, cleanup := dialTestClient(t, hub, subscriberUser)
	defer cleanup()

	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "subscriber did not register")
	require.True(t, hub.JoinRoom(joinCircle, clientIDForUser(t, hub, subscriberUser)),
		"subscriber should be able to join the room")

	// Release every publisher at once so their sends genuinely contend.
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(publishers)

	for p := 0; p < publishers; p++ {
		go func(publisher int) {
			defer done.Done()
			start.Wait()
			for seq := 1; seq <= messagesEach; seq++ {
				hub.Broadcast(joinCircle, Message{
					Type: "state_update",
					Payload: map[string]int{
						"publisher": publisher,
						"seq":       seq,
					},
				})
			}
		}(p)
	}

	start.Done()
	done.Wait()

	received := readSeqMessages(t, conn, totalToDeliver)

	// Per-publisher FIFO: seq must arrive strictly increasing, complete and
	// duplicate-free for every publisher.
	perPublisher := make(map[int][]int, publishers)
	for _, msg := range received {
		perPublisher[msg.Payload.Publisher] = append(perPublisher[msg.Payload.Publisher], msg.Payload.Seq)
	}

	require.Len(t, perPublisher, publishers, "every publisher must be represented")
	for publisher, seqs := range perPublisher {
		require.Len(t, seqs, messagesEach,
			"publisher %d: expected %d messages, got %d (loss or duplication)",
			publisher, messagesEach, len(seqs))
		for i, seq := range seqs {
			assert.Equal(t, i+1, seq,
				"publisher %d: message %d arrived out of order (FIFO violated)", publisher, i)
		}
	}

	// A consumer that keeps up must not be evicted, and must not have had
	// anything dropped.
	assert.Equal(t, 1, hub.ClientCount(), "a consumer that keeps up must not be disconnected")
}

// Ordering must hold for the user-targeted path too, not just room fan-out.
func TestHub_PerConnectionFIFOForUserBroadcasts(t *testing.T) {
	const (
		publishers   = 4
		messagesEach = 15
		userID       = "user-fanout"
	)

	hub := NewHub()
	conn, cleanup := dialTestClient(t, hub, userID)
	defer cleanup()
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "client did not register")

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(publishers)
	for p := 0; p < publishers; p++ {
		go func(publisher int) {
			defer done.Done()
			start.Wait()
			for seq := 1; seq <= messagesEach; seq++ {
				hub.BroadcastToUser(userID, Message{
					Type:    "state_update",
					Payload: map[string]int{"publisher": publisher, "seq": seq},
				})
			}
		}(p)
	}
	start.Done()
	done.Wait()

	received := readSeqMessages(t, conn, publishers*messagesEach)
	perPublisher := make(map[int][]int, publishers)
	for _, msg := range received {
		perPublisher[msg.Payload.Publisher] = append(perPublisher[msg.Payload.Publisher], msg.Payload.Seq)
	}
	require.Len(t, perPublisher, publishers)
	for publisher, seqs := range perPublisher {
		require.Len(t, seqs, messagesEach, "publisher %d lost or duplicated messages", publisher)
		for i, seq := range seqs {
			assert.Equal(t, i+1, seq, "publisher %d: FIFO violated at position %d", publisher, i)
		}
	}
}

// A slow consumer must be evicted, and — the part that used to be broken —
// its connection must actually be closed. Unregistering alone left the socket
// and both pumps running with nobody left to serve them.
func TestHub_SlowConsumerIsEvictedAndDisconnected(t *testing.T) {
	const (
		joinCircle    = "circle-slow"
		slowUser      = "user-slow"
		messageSize   = 64 * 1024
		maxBroadcasts = 4000
	)

	hub := NewHub()
	conn, cleanup := dialTestClient(t, hub, slowUser)
	defer cleanup()
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "client did not register")
	require.True(t, hub.JoinRoom(joinCircle, clientIDForUser(t, hub, slowUser)))

	// Deliberately never read from conn: the server's send buffer and the
	// kernel socket buffers fill, and the hub must evict us.
	payload := strings.Repeat("a", messageSize)
	deadline := time.Now().Add(20 * time.Second)
	evicted := false
	for i := 0; i < maxBroadcasts && time.Now().Before(deadline); i++ {
		hub.Broadcast(joinCircle, Message{Type: "state_update", Payload: payload})
		if hub.ClientCount() == 0 {
			evicted = true
			break
		}
	}
	require.True(t, evicted, "a consumer that never reads must be evicted for backpressure")

	// The connection must be torn down, not merely unregistered.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var readErr error
	for readErr == nil {
		if _, _, err := conn.ReadMessage(); err != nil {
			readErr = err
		}
	}
	assert.Error(t, readErr, "an evicted slow consumer must observe its connection closing")

	// And it must stay gone: later broadcasts have nobody to reach.
	hub.Broadcast(joinCircle, Message{Type: "state_update", Payload: "after"})
	assert.Equal(t, 0, hub.ClientCount())
}

// Eviction must be safe for clients that have no live socket, which is how the
// hub is unit-tested elsewhere.
func TestHub_SlowConsumerEvictionHandlesClientsWithoutConn(t *testing.T) {
	hub := NewHub()
	client := &Client{ID: "no-conn", Send: make(chan []byte, 1), Hub: hub}
	hub.Register(client)
	hub.JoinRoom("circle", client.ID)

	client.Send <- []byte("already full")
	require.NotPanics(t, func() {
		hub.Broadcast("circle", Message{Type: "drop"})
	})
	assert.Equal(t, 0, hub.ClientCount(), "overflowing client must be unregistered")
}

// roomMemberCount reports how many connections can currently receive a
// broadcast for a circle.
func roomMemberCount(hub *Hub, circleID string) int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.rooms[circleID])
}

// Reconnect behaviour is part of the documented contract: a reconnecting
// client gets a brand new connection with no room subscriptions, so it must
// re-subscribe before it receives anything again. Messages published while it
// was away are not replayed.
func TestHub_ReconnectRequiresResubscribe(t *testing.T) {
	const joinCircle = "circle-reconnect"

	hub := NewHub()
	conn, cleanup := dialTestClient(t, hub, "user-reconnect")
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "client did not register")
	require.True(t, hub.JoinRoom(joinCircle, clientIDForUser(t, hub, "user-reconnect")))

	hub.Broadcast(joinCircle, Message{Type: "state_update", Payload: map[string]int{"seq": 1}})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err, "subscribed client should receive the broadcast")
	require.Contains(t, string(data), `"seq":1`)

	// Drop the connection and reconnect, exactly as a client would.
	require.NoError(t, conn.Close())
	waitFor(t, func() bool { return hub.ClientCount() == 0 }, "old connection should be unregistered")
	require.Equal(t, 0, roomMemberCount(hub, joinCircle),
		"an emptied room must not linger, or RoomCount overstates live rooms")

	conn2, cleanup2 := dialTestClient(t, hub, "user-reconnect")
	defer cleanup2()
	waitFor(t, func() bool { return hub.ClientCount() == 1 }, "reconnected client did not register")

	// Subscriptions are per-connection: the new socket is not in the room, so
	// this broadcast has no target and is dropped rather than replayed later.
	require.Equal(t, 0, roomMemberCount(hub, joinCircle),
		"a reconnected client must not inherit the previous connection's subscriptions")
	hub.Broadcast(joinCircle, Message{Type: "state_update", Payload: map[string]int{"seq": 2}})

	// Re-subscribing restores delivery. The first message the client sees must
	// be seq 3: if the missed seq 2 had been buffered or replayed, it would
	// arrive first.
	subscribe := fmt.Sprintf(`{"type":"subscribe","circleId":%q}`, joinCircle)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, []byte(subscribe)))
	waitFor(t, func() bool { return roomMemberCount(hub, joinCircle) == 1 },
		"client did not re-subscribe to the room")

	hub.Broadcast(joinCircle, Message{Type: "state_update", Payload: map[string]int{"seq": 3}})
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err = conn2.ReadMessage()
	require.NoError(t, err, "re-subscribed client should receive broadcasts again")
	assert.Contains(t, string(data), `"seq":3`, "messages missed while disconnected must not be replayed")

	cleanup()
}
