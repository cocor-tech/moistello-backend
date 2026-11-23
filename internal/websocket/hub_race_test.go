package websocket

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHub_ConcurrentOperations is a stress test that exercises all Hub
// operations concurrently to detect data races via `go test -race`.
func TestHub_ConcurrentOperations(t *testing.T) {
	hub := NewHub()
	const numGoroutines = 50
	const numOps = 100

	var wg sync.WaitGroup

	// Spawn goroutines that register/unregister clients
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clientID := fmt.Sprintf("client-%d", id)
			client := &Client{ID: clientID, Send: make(chan []byte, 256), Hub: hub}

			for j := 0; j < numOps; j++ {
				hub.Register(client)
				hub.JoinRoom("circle-shared", clientID)
				hub.JoinRoom(fmt.Sprintf("circle-%d", id), clientID)
				hub.Broadcast("circle-shared", Message{Type: "test", Payload: j})
				hub.BroadcastToUser(clientID, Message{Type: "user.update", Payload: j})
				hub.LeaveRoom(fmt.Sprintf("circle-%d", id), clientID)

				// Drain the Send channel to prevent blocking
				for len(client.Send) > 0 {
					<-client.Send
				}
			}

			hub.LeaveRoom("circle-shared", clientID)
			hub.Unregister(client)
		}(i)
	}

	wg.Wait()

	// After all goroutines finish, stats should be consistent
	clients, rooms := hub.Stats()
	assert.Equal(t, 0, clients, "all clients should be unregistered")
	t.Logf("final state: %d clients, %d rooms", clients, rooms)
}

// TestHub_ConcurrentRegisterUnregister focuses on rapid register/unregister
// cycles to catch races in client map access.
func TestHub_ConcurrentRegisterUnregister(t *testing.T) {
	hub := NewHub()
	const n = 100

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := &Client{
				ID:   fmt.Sprintf("c-%d", id),
				Send: make(chan []byte, 256),
				Hub:  hub,
			}
			hub.Register(client)
			// Immediately unregister from a different goroutine
			go hub.Unregister(client)
		}(i)
	}

	wg.Wait()
}

// TestHub_ConcurrentBroadcastAndLeave exercises the specific race where
// Broadcast iterates a room while LeaveRoom modifies it.
func TestHub_ConcurrentBroadcastAndLeave(t *testing.T) {
	hub := NewHub()
	const n = 20

	clients := make([]*Client, n)
	for i := 0; i < n; i++ {
		clients[i] = &Client{
			ID:   fmt.Sprintf("c-%d", i),
			Send: make(chan []byte, 256),
			Hub:  hub,
		}
		hub.Register(clients[i])
		hub.JoinRoom("race-circle", clients[i].ID)
	}

	var wg sync.WaitGroup

	// Broadcast concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()
			hub.Broadcast("race-circle", Message{Type: "event", Payload: iter})
		}(i)
	}

	// Leave concurrently
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hub.LeaveRoom("race-circle", clients[idx].ID)
		}(i)
	}

	wg.Wait()

	// Drain all client channels
	for _, c := range clients {
		for len(c.Send) > 0 {
			<-c.Send
		}
	}
}

// TestHub_ConcurrentJoinRoomAndBroadcast exercises the race where JoinRoom
// adds clients while Broadcast reads the room.
func TestHub_ConcurrentJoinRoomAndBroadcast(t *testing.T) {
	hub := NewHub()
	const n = 30

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := &Client{
				ID:   fmt.Sprintf("c-%d", id),
				Send: make(chan []byte, 256),
				Hub:  hub,
			}
			hub.Register(client)
			hub.JoinRoom("dynamic-circle", client.ID)
		}(i)
	}

	// Simultaneously broadcast to the same room
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hub.Broadcast("dynamic-circle", Message{Type: "concurrent", Payload: "data"})
		}()
	}

	wg.Wait()
}

// TestHub_ConcurrentStatsReads verifies Stats() is safe under contention.
func TestHub_ConcurrentStatsReads(t *testing.T) {
	hub := NewHub()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := &Client{
				ID:   fmt.Sprintf("c-%d", id),
				Send: make(chan []byte, 256),
				Hub:  hub,
			}
			hub.Register(client)
			hub.Stats()
			hub.ClientCount()
			hub.RoomCount()
			hub.Unregister(client)
		}(i)
	}
	wg.Wait()

	c, r := hub.Stats()
	assert.Equal(t, 0, c)
	assert.GreaterOrEqual(t, r, 0)
}

// TestClient_ConcurrentStateAccess verifies that Client state operations
// (missedPings, sendError, Close, Disconnect, Kick) are race-free under -race.
func TestClient_ConcurrentStateAccess(t *testing.T) {
	hub := NewHub()
	client := NewClient(hub, nil, "user-1")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client.missedPings.Add(1)
			_ = client.MissedPings()
			if id%2 == 0 {
				client.ResetMissedPings()
			}
			client.sendError("test")
			switch id % 3 {
			case 0:
				client.Close()
			case 1:
				client.Disconnect()
			default:
				client.Kick(1000, "done")
			}
		}(i)
	}
	wg.Wait()

	client.ResetMissedPings()
	assert.Equal(t, int32(0), client.MissedPings())
}
