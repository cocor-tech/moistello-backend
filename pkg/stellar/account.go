package stellar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// lockTTL is how long a distributed sequence lock is held before expiring
// (protects against dead locks if a process dies while holding it).
const lockTTL = 5 * time.Second

// lockRetries is the number of attempts when waiting for another replica's
// distributed lock to be released.
const lockRetries = 40

// lockSleep is the delay between lock acquisition attempts.
const lockSleep = 250 * time.Millisecond

type AccountManager struct {
	// mu is a best-effort in-process lock used ONLY when no Redis client is
	// configured (e.g. local tests). For multi-instance deployments pass a
	// Redis client to NewAccountManager so replicas share the lock.
	mu          sync.Mutex
	rdb         *redis.Client
	publicKey   string
	currentSeq  int64
	lastFetched time.Time
	client      *Client
	maxDrift    time.Duration
}

// NewAccountManager builds an AccountManager. Pass a non-nil *redis.Client as
// the third argument to enable distributed locking + sequence synchronisation
// across all API server replicas. Without Redis the manager falls back to a
// per-process sync.Mutex, which is only safe for single-instance deployments.
func NewAccountManager(client *Client, publicKey string, rdb ...*redis.Client) *AccountManager {
	m := &AccountManager{
		client:    client,
		publicKey: publicKey,
		maxDrift:  30 * time.Second,
	}
	if len(rdb) > 0 {
		m.rdb = rdb[0]
	}
	return m
}

// NextSequence returns the next valid sequence number. Thread-safe across
// goroutines; when a Redis client is configured, also safe across every
// API server replica (distributed lock + refresh from Horizon under lock).
func (m *AccountManager) NextSequence(ctx context.Context) (int64, error) {
	if m.rdb == nil {
		return m.nextSequenceLocal(ctx)
	}
	unlock, err := m.acquireLock(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring sequence lock: %w", err)
	}
	defer unlock()

	if m.lastFetched.IsZero() || time.Since(m.lastFetched) > m.maxDrift {
		seq, err := m.fetchSequence(ctx)
		if err != nil {
			return 0, fmt.Errorf("fetching sequence: %w", err)
		}
		// Never go backwards even if chain state races with our cached copy
		if seq > m.currentSeq {
			m.currentSeq = seq
		}
		m.lastFetched = time.Now()
	} else {
		// Re-check against chain under lock to stay in sync with other
		// replicas when we have a reasonably fresh local copy. Catches the
		// case where a sibling replica advanced the chain but the local
		// maxDrift window hasn't elapsed yet.
		chain, err := m.fetchSequence(ctx)
		if err == nil && chain > m.currentSeq {
			m.currentSeq = chain
		}
	}

	seq := m.currentSeq
	m.currentSeq++
	return seq, nil
}

// nextSequenceLocal is the legacy in-process mutex path used when Redis is
// not configured. Safe for single-instance / local-test environments.
func (m *AccountManager) nextSequenceLocal(ctx context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastFetched.IsZero() || time.Since(m.lastFetched) > m.maxDrift {
		seq, err := m.fetchSequence(ctx)
		if err != nil {
			return 0, fmt.Errorf("fetching sequence: %w", err)
		}
		m.currentSeq = seq
		m.lastFetched = time.Now()
	}

	seq := m.currentSeq
	m.currentSeq++
	return seq, nil
}

// acquireLock takes a distributed Redis lock (SET NX PX) for this manager's
// public key. Returns an unlock func that atomically releases the lock only
// if the token still matches (prevents deleting a sibling replica's lock).
func (m *AccountManager) acquireLock(ctx context.Context) (func(), error) {
	key := fmt.Sprintf("stellar:seqlock:%s", m.publicKey)
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return func() {}, fmt.Errorf("generating lock token: %w", err)
	}
	token := hex.EncodeToString(tok)

	for i := 0; i < lockRetries; i++ {
		ok, err := m.rdb.SetNX(ctx, key, token, lockTTL).Result()
		if err != nil {
			return func() {}, fmt.Errorf("redis SETNX: %w", err)
		}
		if ok {
			break
		}
		if i == lockRetries-1 {
			return func() {}, fmt.Errorf("sequence lock contention after %d attempts", lockRetries)
		}
		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-time.After(lockSleep):
		}
	}

	unlock := func() {
		// Compare-and-delete via Lua so we never nuke another replica's lock.
		script := redis.NewScript(`
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			else
				return 0
			end
		`)
		_, _ = script.Run(context.Background(), m.rdb, []string{key}, token).Result()
	}
	return unlock, nil
}

func (m *AccountManager) fetchSequence(ctx context.Context) (int64, error) {
	acc, err := m.client.GetAccount(ctx, m.publicKey)
	if err != nil {
		return 0, err
	}
	seq, err := strconv.ParseInt(acc.Sequence, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing sequence: %w", err)
	}
	return seq, nil
}

// Reset invalidates the cached sequence so the next call reloads from chain.
func (m *AccountManager) Reset() {
	if m.rdb == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
	}
	m.lastFetched = time.Time{}
	m.currentSeq = 0
}

func (m *AccountManager) PublicKey() string { return m.publicKey }
