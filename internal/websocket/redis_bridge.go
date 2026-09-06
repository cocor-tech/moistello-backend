package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// RedisBridge subscribes to a Redis channel for cross-instance WebSocket
// events and relays them to the local Hub. One RedisBridge should be started
// per API server instance.
type RedisBridge struct {
	hub         *Hub
	rdb         *redis.Client
	pubsub      *redis.PubSub
	stop        chan struct{}
	cancel      context.CancelFunc
	once        sync.Once
	done        chan struct{}
	queue       chan []byte
	rateLimiter *BridgeRateLimiter
}

// BridgeRateLimiter provides per-client rate limiting for websocket bridge relays.
type BridgeRateLimiter struct {
	mu      sync.Mutex
	limits  map[string][]time.Time
	maxRate int           // e.g. max messages per window
	window  time.Duration // e.g. 1 second
}

func NewBridgeRateLimiter(maxRate int, window time.Duration) *BridgeRateLimiter {
	return &BridgeRateLimiter{
		limits:  make(map[string][]time.Time),
		maxRate: maxRate,
		window:  window,
	}
}

func (rl *BridgeRateLimiter) Allow(key string) bool {
	if key == "" || rl.maxRate <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	var recent []time.Time
	for _, t := range rl.limits[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rl.maxRate {
		rl.limits[key] = recent
		return false
	}

	recent = append(recent, now)
	rl.limits[key] = recent
	return true
}

// NewRedisBridge creates a RedisBridge and starts consuming events from the
// Redis channel in a background goroutine. Call Close to stop consumption.
func NewRedisBridge(hub *Hub, rdb *redis.Client) *RedisBridge {
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := rdb.Subscribe(ctx, redisChannel)
	b := &RedisBridge{
		hub:         hub,
		rdb:         rdb,
		pubsub:      pubsub,
		stop:        make(chan struct{}),
		cancel:      cancel,
		done:        make(chan struct{}),
		queue:       make(chan []byte, 1024),                // Bounded relay queue with backpressure
		rateLimiter: NewBridgeRateLimiter(100, time.Second), // Per-client rate limiter default
	}
	go b.consume(ctx)
	go b.processQueue(ctx)
	return b
}

// Close stops the background goroutine that consumes Redis events.
func (b *RedisBridge) Close() {
	b.once.Do(func() {
		close(b.stop)
		if b.cancel != nil {
			b.cancel()
		}
		if b.pubsub != nil {
			_ = b.pubsub.Close()
		}
	})
	<-b.done
}

func (b *RedisBridge) consume(ctx context.Context) {
	defer close(b.done)
	defer func() {
		if b.pubsub != nil {
			_ = b.pubsub.Close()
		}
	}()

	ch := b.pubsub.Channel(redis.WithChannelSize(256))

	for {
		select {
		case <-b.stop:
			return
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Apply backpressure via non-blocking channel send or drop policy if full
			select {
			case b.queue <- []byte(msg.Payload):
			default:
				log.Warn().Msg("redis bridge queue full: dropping incoming pub/sub message (backpressure policy)")
			}
		}
	}
}

func (b *RedisBridge) processQueue(ctx context.Context) {
	for {
		select {
		case <-b.stop:
			return
		case <-ctx.Done():
			return
		case data := <-b.queue:
			b.handleMessage(data)
		}
	}
}

func (b *RedisBridge) handleMessage(data []byte) {
	var env struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		log.Debug().Err(err).Msg("redis bridge unmarshal")
		return
	}

	var pay struct {
		CircleID    string  `json:"circleId"`
		CommunityID string  `json:"communityId"`
		UserID      string  `json:"userId"`
		Status      string  `json:"status"`
		RoundNumber int     `json:"roundNumber"`
		Amount      float64 `json:"amount"`
		Timestamp   string  `json:"timestamp"`
	}
	if err := json.Unmarshal(env.Payload, &pay); err != nil {
		log.Debug().Err(err).Msg("redis bridge payload unmarshal")
		return
	}

	// Apply per-client rate limiting based on recipient (UserID or Room/Circle/Community)
	limiterKey := pay.UserID
	if limiterKey == "" {
		limiterKey = pay.CircleID
	}
	if limiterKey == "" {
		limiterKey = pay.CommunityID
	}

	if !b.rateLimiter.Allow(limiterKey) {
		log.Debug().Str("key", limiterKey).Msg("redis bridge rate limit exceeded for target, dropping message")
		return
	}

	// Reconstruct and relay to local Hub
	var msg Message
	msg.Type = env.Type
	msg.Payload = pay

	// Relay to circle room or user
	switch env.Type {
	case "user.updated", "notification.new":
		if pay.UserID != "" {
			b.hub.BroadcastToUser(pay.UserID, msg)
		}
	default:
		room := pay.CircleID
		if room == "" {
			room = pay.CommunityID
		}
		if room != "" {
			b.hub.Broadcast(room, msg)
		}
	}
}

// Ensure RedisBridge implements a keep-alive / stats method.
func (b *RedisBridge) Stats() (int, int) { return b.hub.Stats() }
