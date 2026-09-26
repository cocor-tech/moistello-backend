package websocket

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// shutdownReason prefixes the close-frame reason sent on server shutdown.
	shutdownReason = "server shutting down"

	// reconnectMinDelay is the earliest a client is told to come back.
	reconnectMinDelay = time.Second

	// reconnectTargetRate is the number of reconnects per second the server
	// aims to absorb after a restart. The jitter window grows with the number
	// of connected clients so arrivals are spread at roughly this rate.
	reconnectTargetRate = 500

	// reconnectMaxSpread caps the jitter window.
	reconnectMaxSpread = 60 * time.Second

	// subscribeBurst and subscribeRefill bound how fast a single connection
	// may send subscribe messages: a burst of subscribeBurst, refilled at
	// subscribeRefill tokens per second.
	subscribeBurst  = 20
	subscribeRefill = 10.0
)

// reconnectSpread returns the width of the jitter window for a hub holding
// the given number of clients.
func reconnectSpread(clients int) time.Duration {
	spread := time.Duration(clients) * time.Second / reconnectTargetRate
	if spread < reconnectMinDelay {
		return reconnectMinDelay
	}
	if spread > reconnectMaxSpread {
		return reconnectMaxSpread
	}
	return spread
}

// reconnectDelay picks a random delay in [reconnectMinDelay, reconnectMinDelay+spread).
func reconnectDelay(spread time.Duration) time.Duration {
	return reconnectMinDelay + time.Duration(rand.Int63n(int64(spread)))
}

// shutdownCloseReason builds the close-frame reason carrying the backoff hint.
func shutdownCloseReason(delay time.Duration) string {
	return fmt.Sprintf("%s; reconnect_after_ms=%d", shutdownReason, delay.Milliseconds())
}

// parseReconnectAfter extracts the backoff hint from a close-frame reason.
func parseReconnectAfter(reason string) (time.Duration, bool) {
	const key = "reconnect_after_ms="
	i := strings.Index(reason, key)
	if i < 0 {
		return 0, false
	}
	ms, err := strconv.ParseInt(reason[i+len(key):], 10, 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

// tokenBucket is a small token-bucket limiter. The zero value starts full.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// allow consumes one token and reports whether one was available.
func (b *tokenBucket) allow(now time.Time, burst int, refill float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.IsZero() {
		b.tokens = float64(burst)
	} else {
		b.tokens += now.Sub(b.last).Seconds() * refill
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
