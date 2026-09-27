# WebSocket delivery guarantees

What a client can rely on when consuming real-time updates over
`GET /ws`, and what it must not assume. The behaviours described here are
pinned down by tests in `internal/websocket/ordering_test.go`.

## Ordering

**Guaranteed: per-connection FIFO.**

Each connection has exactly one writer goroutine (`Client.WritePump`) reading
from a single buffered channel (`Client.Send`, 256 slots). Go channels are
FIFO, so once a message is accepted into a connection's send buffer it is
written to that socket in the order it was accepted, and messages already
queued are never reordered behind later ones.

**Not guaranteed: a total order across concurrent publishers.**

Go does not order sends from different goroutines. If two publishers race,
which message enters a connection's buffer first is not defined, and the
server does not try to impose one. Concretely, if publisher A emits `a1, a2`
and publisher B emits `b1, b2` at the same time, a client may observe
`a1, b1, b2, a2`. What *is* guaranteed is that A's own messages stay ordered
relative to each other (`a1` before `a2`), and likewise for B.

If your UI needs a single total order, put a monotonically increasing
sequence number in the event payload and have the client discard anything it
has already seen. Do not infer ordering from arrival order across different
event types.

Covered by `TestHub_PerConnectionFIFOUnderConcurrentPublishes` and
`TestHub_PerConnectionFIFOForUserBroadcasts`.

## Backpressure and slow consumers

Broadcasts use a non-blocking send. If a connection's send buffer is full, the
message is **dropped** and that connection is evicted: it is unregistered from
the hub and its socket is closed, so a client that cannot keep up can never
stall publishers or other subscribers.

The socket is closed without a graceful close frame, because a slow consumer
may have its write pump blocked inside a write; attempting a close frame would
block the publisher for up to the 10s write timeout. The client therefore
observes an **abnormal closure (1006)**, not a specific close code. Treat 1006
as "reconnect", the same as any other unexpected disconnect.

Eviction counters:

| Metric | Meaning |
| --- | --- |
| `moistello_websocket_dropped_messages_total` | Messages discarded because a send buffer was full |
| `moistello_websocket_slow_clients_disconnected_total` | Connections evicted for backpressure |
| `moistello_websocket_stale_connections_closed_total` | Connections closed after missed heartbeats |

A client that reconnects faster than it can consume will be evicted again, so
the fix is to read from the socket promptly (or to resume from REST) rather
than to retry broadcasts blindly.

Covered by `TestHub_SlowConsumerIsEvictedAndDisconnected`.

## Reconnect behaviour

**Subscriptions are per-connection, not per-user.** A reconnecting client
starts with an empty subscription set and receives nothing until it sends
`{"type":"subscribe","circleId":"..."}` again. Membership is re-checked against
the database on every subscribe, so a client whose membership was revoked
while it was away is refused.

**Nothing is replayed.** Events published while a client was disconnected are
lost; the server keeps no per-client backlog. On resubscribing, a client
receives only events published from then on. Any state it needs in the gap
must be re-fetched over REST.

**Reconnect after server shutdown carries a backoff hint.** The server closes
connections with code `1001` (going away) and a reason of the form:

```
server shutting down; reconnect_after_ms=1234
```

Clients should honour `reconnect_after_ms`. The delay is jittered per client
and widens with the number of connections the instance held, so a fleet
reconnecting after a restart does not stampede the returning instance.

**Heartbeats.** The server pings every 54s and closes a connection after two
consecutive unanswered pings. A client must answer pings (browsers and
`gorilla/websocket` do this automatically) or it will be disconnected and
should reconnect.

Covered by `TestHub_ReconnectRequiresResubscribe` and
`TestHub_Shutdown_SendsCloseFrameAndDrains`.

## Multiple API instances

Events are also published to Redis (`moistello:ws:events`) so every instance
delivers to its own local clients. This path is at-most-once: if the bridge
queue is full the incoming Pub/Sub message is dropped, and Redis Pub/Sub
ordering guarantees apply only within a single channel. The per-connection
FIFO guarantee above is unaffected, because each instance still serialises
writes through one `WritePump` per connection.
