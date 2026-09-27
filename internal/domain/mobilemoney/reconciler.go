package mobilemoney

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

const (
	// reconcileLockKey is the Redis single-flight lock key every replica contends for.
	// Exactly one instance runs a reconciliation pass at a time.
	reconcileLockKey = "lock:mobilemoney-reconcile"

	// DefaultReconcileLockTTL bounds how long a crashed holder can block the pass.
	// Long passes are kept alive via heartbeat renewal.
	DefaultReconcileLockTTL = 5 * time.Minute

	// DefaultReconcileInterval is used when no interval is configured.
	DefaultReconcileInterval = 5 * time.Minute

	// DefaultReconcileJitter is the upper bound of the random delay before each pass.
	DefaultReconcileJitter = 30 * time.Second
)

// Reconciler coordinates scheduled mobile-money transaction reconciliation across replicas.
// Every API server replica runs this job, but each pass is guarded by a single-flight Redis lock:
// replicas wake up at jittered intervals and the first to acquire the lock performs the pass
// while the rest skip the tick.
type Reconciler struct {
	redis    *redis.Client
	service  Service
	interval time.Duration
	jitter   time.Duration
	lockTTL  time.Duration

	jitterFn func(time.Duration) time.Duration

	mu       sync.Mutex
	started  bool
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewReconciler constructs a Reconciler instance.
func NewReconciler(rdb *redis.Client, svc Service, interval, jitter time.Duration) *Reconciler {
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	if jitter < 0 {
		jitter = 0
	}
	return &Reconciler{
		redis:    rdb,
		service:  svc,
		interval: interval,
		jitter:   jitter,
		lockTTL:  DefaultReconcileLockTTL,
		jitterFn: randomJitter,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the reconciliation loop in the background.
func (r *Reconciler) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()

	go r.run(ctx)
}

// Stop terminates the reconciliation loop gracefully after any in-flight pass finishes.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })

	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		return
	}

	select {
	case <-r.doneCh:
	case <-time.After(r.interval):
		log.Warn().Msg("mobile money reconciler worker did not stop within one interval")
	}
}

func (r *Reconciler) run(ctx context.Context) {
	defer close(r.doneCh)
	log.Info().
		Dur("interval", r.interval).
		Dur("jitter", r.jitter).
		Msg("mobile money reconciler worker started")

	for {
		if delay := r.jitterFn(r.jitter); delay > 0 {
			if !r.sleep(ctx, delay) {
				return
			}
		}

		count, held, err := r.RunOnce(ctx)
		switch {
		case err != nil:
			log.Error().Err(err).Msg("mobile money reconciliation pass failed")
		case !held:
			log.Debug().Msg("mobile money reconciliation skipped: another replica holds the lock")
		default:
			if count > 0 {
				log.Info().Int("count", count).Msg("mobile money reconciliation updated pending transactions")
			} else {
				log.Debug().Msg("mobile money reconciliation pass completed: no pending transactions updated")
			}
		}

		if !r.sleep(ctx, r.interval) {
			return
		}
	}
}

// RunOnce performs a single guarded reconciliation pass.
// Returns (reconciledCount, lockHeld, error). If another replica holds the lock,
// lockHeld is false, count is 0, and err is nil.
func (r *Reconciler) RunOnce(ctx context.Context) (int, bool, error) {
	if r.redis == nil {
		// Fallback for standalone/test environments without Redis
		count, err := r.service.Reconcile(ctx)
		return count, true, err
	}

	token, err := newLockToken()
	if err != nil {
		return 0, false, err
	}

	acquired, err := r.redis.SetNX(ctx, reconcileLockKey, token, r.lockTTL).Result()
	if err != nil {
		return 0, false, fmt.Errorf("acquiring mobile money reconcile lock: %w", err)
	}
	if !acquired {
		return 0, false, nil
	}
	defer r.release(token)

	// Heartbeat renewal loop for long passes
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go r.heartbeat(heartbeatDone, token)

	count, err := r.service.Reconcile(ctx)
	if err != nil {
		return count, true, err
	}
	return count, true, nil
}

var renewReconcileLock = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

func (r *Reconciler) heartbeat(done chan struct{}, token string) {
	renewInterval := r.lockTTL / 3
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
			ttlMs := r.lockTTL.Milliseconds()
			_, err := renewReconcileLock.Run(ctx, r.redis, []string{reconcileLockKey}, token, ttlMs).Result()
			cancel()
			if err != nil {
				log.Warn().Err(err).Msg("failed to renew mobile money reconcile lock heartbeat")
			}
		}
	}
}

var releaseReconcileLock = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

func (r *Reconciler) release(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := releaseReconcileLock.Run(ctx, r.redis, []string{reconcileLockKey}, token).Result(); err != nil {
		log.Warn().Err(err).Msg("releasing mobile money reconcile lock")
	}
}

func (r *Reconciler) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		case <-r.stopCh:
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	case <-r.stopCh:
		return false
	}
}

func newLockToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating reconcile lock token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return max / 2
	}
	return time.Duration(n.Int64())
}
