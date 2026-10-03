package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Single-flight lock (#416).
//
// Scheduled background jobs run on every API server replica. Without a
// cross-replica guard, two replicas waking on the same tick would both process
// the same work — for the swap sweeper that means two escrow release attempts
// against the same offer. This is the guard, extracted from the mobile-money
// reconciler (which had it inline) so every scheduled job shares one
// implementation rather than each re-deriving the same Lua/token/heartbeat
// dance.
//
// The lock is a token-owned Redis key: acquire with SETNX, renew with a
// compare-and-expire script, release with a compare-and-delete script. The
// token is what makes release safe — a replica whose lock already expired and
// was taken by someone else must not delete *their* lock on the way out.

// renewScript extends the TTL only while the caller still owns the key.
var renewScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

// releaseScript deletes the key only while the caller still owns it.
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// DefaultLockTTL bounds how long a crashed holder can block a job. Long passes
// stay alive through heartbeat renewal rather than by setting a huge TTL, so a
// crash is recovered from quickly.
const DefaultLockTTL = 5 * time.Minute

// Lock is a single-flight guard over one named job.
type Lock struct {
	client *redis.Client
	key    string
	ttl    time.Duration

	// token identifies this acquisition. Empty until Acquire succeeds.
	token string
}

// NewLock returns a Lock for the given key. A non-positive ttl falls back to
// DefaultLockTTL.
func NewLock(client *redis.Client, key string, ttl time.Duration) *Lock {
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &Lock{client: client, key: key, ttl: ttl}
}

// Key returns the Redis key this lock contends on.
func (l *Lock) Key() string { return l.key }

// Acquire attempts to take the lock without blocking. It reports whether the
// lock was taken; a false return with a nil error means another replica holds
// it, which is the expected outcome for all but one replica on any given tick.
func (l *Lock) Acquire(ctx context.Context) (bool, error) {
	if l.client == nil {
		// No Redis configured (standalone/test): treat the lock as held so the
		// job still runs. Single-replica deployments have nothing to contend
		// with, and refusing to run would silently disable the job.
		l.token = "no-redis"
		return true, nil
	}

	token, err := newToken()
	if err != nil {
		return false, err
	}

	acquired, err := l.client.SetNX(ctx, l.key, token, l.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("acquiring lock %s: %w", l.key, err)
	}
	if !acquired {
		return false, nil
	}
	l.token = token
	return true, nil
}

// With runs fn while holding the lock, renewing the TTL in the background for
// as long as fn runs. It reports whether the lock was held: when it is false,
// fn was not run at all and the caller should treat the tick as skipped.
//
// The heartbeat is what keeps a long pass from having its lock expire
// mid-flight, which would let a second replica start a concurrent pass over
// the same work.
func (l *Lock) With(ctx context.Context, fn func(context.Context) error) (bool, error) {
	acquired, err := l.Acquire(ctx)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	defer l.Release()

	if l.client != nil {
		heartbeatDone := make(chan struct{})
		defer close(heartbeatDone)
		go l.heartbeat(heartbeatDone)
	}

	if err := fn(ctx); err != nil {
		return true, err
	}
	return true, nil
}

// Release drops the lock if this holder still owns it. It is safe to call when
// the lock was never acquired, and safe to call more than once.
func (l *Lock) Release() {
	if l.client == nil || l.token == "" {
		l.token = ""
		return
	}
	// A detached context: releasing must still be attempted even if the
	// triggering context has been cancelled, or the lock would linger until
	// its TTL and needlessly block every other replica.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := releaseScript.Run(ctx, l.client, []string{l.key}, l.token).Result(); err != nil {
		log.Warn().Err(err).Str("key", l.key).Msg("releasing single-flight lock")
	}
	l.token = ""
}

func (l *Lock) heartbeat(done chan struct{}) {
	renewInterval := l.ttl / 3
	if renewInterval <= 0 {
		renewInterval = time.Minute
	}
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := renewScript.Run(ctx, l.client, []string{l.key}, l.token, l.ttl.Milliseconds()).Result()
			cancel()
			if err != nil {
				log.Warn().Err(err).Str("key", l.key).Msg("failed to renew single-flight lock heartbeat")
			}
		}
	}
}

// newToken mints the random value that proves ownership of the lock key.
func newToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating lock token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
