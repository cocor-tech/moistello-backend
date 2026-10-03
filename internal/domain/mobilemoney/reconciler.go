package mobilemoney

import (
	"context"
	"math/big"
	"crypto/rand"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	moistelloredis "github.com/moistello/backend/pkg/redis"
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
	lock     *moistelloredis.Lock

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
		// #416: the single-flight lock is now the shared helper in pkg/redis,
		// so the swap sweeper guards its pass with the same implementation
		// rather than a second copy of the token/heartbeat logic.
		lock:     moistelloredis.NewLock(rdb, reconcileLockKey, DefaultReconcileLockTTL),
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

	var count int
	held, err := r.lock.With(ctx, func(ctx context.Context) error {
		reconciled, reconcileErr := r.service.Reconcile(ctx)
		count = reconciled
		return reconcileErr
	})
	if err != nil {
		return count, held, err
	}
	return count, held, nil
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
