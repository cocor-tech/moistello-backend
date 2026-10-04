package swap

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	moistelloredis "github.com/moistello/backend/pkg/redis"
)

// SweepLockKey is the single-flight lock key every replica contends for (#416).
// Exactly one instance sweeps expired offers at a time.
const SweepLockKey = "lock:swap-sweep"

// DefaultSweepInterval is used when no interval is configured.
const DefaultSweepInterval = time.Minute

// Sweeper periodically runs SweepExpiredOffers, releasing escrow on-chain for
// created swap offers past their expires_at and marking them expired (#243).
// Without it, stale offers and the escrowed funds behind them are never
// cleaned up.
//
// Every replica runs this worker, so each pass is guarded by the shared Redis
// single-flight lock (#416): the first replica to acquire it sweeps, the rest
// skip the tick. The lock is an optimisation that avoids duplicated work — the
// guarantee against a double escrow release is the per-offer atomic claim in
// SweepExpiredOffers, which holds even if the lock is lost or expires.
type Sweeper struct {
	service  *Service
	interval time.Duration
	lock     *moistelloredis.Lock

	stopCh   chan struct{}
	stopOnce sync.Once
	doneCh   chan struct{}

	mu      sync.Mutex
	started bool
}

// NewSweeper constructs a Sweeper. rdb may be nil, in which case the lock
// always succeeds (single-replica/standalone) and the per-offer claim remains
// the real safety net.
func NewSweeper(service *Service, rdb *redis.Client, interval time.Duration) *Sweeper {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Sweeper{
		service:  service,
		interval: interval,
		lock:     moistelloredis.NewLock(rdb, SweepLockKey, moistelloredis.DefaultLockTTL),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the sweep loop in the background. It returns immediately and
// is safe to call more than once: a second call is a no-op rather than a second
// worker racing the first one for the same doneCh.
func (s *Sweeper) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	go s.run(ctx)
}

// Stop ends the sweep loop after the in-flight sweep finishes. Safe to call
// more than once.
func (s *Sweeper) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-time.After(s.interval):
		log.Warn().Msg("swap sweep worker did not stop within one interval")
	}
}

func (s *Sweeper) run(ctx context.Context) {
	defer close(s.doneCh)
	log.Info().Dur("interval", s.interval).Msg("swap sweep worker started")

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.SweepOnce(ctx)
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		}
	}
}

// SweepOnce performs a single guarded sweep pass. It returns the number of
// offers swept and whether this replica held the lock. When the lock is not
// held, the pass is skipped entirely and the count is zero — that is the
// expected outcome for all but one replica on any given tick.
func (s *Sweeper) SweepOnce(ctx context.Context) (int, bool, error) {
	var swept int
	held, err := s.lock.With(ctx, func(ctx context.Context) error {
		count, sweepErr := s.service.SweepExpiredOffers(ctx)
		swept = count
		return sweepErr
	})
	if err != nil {
		return 0, held, err
	}
	if !held {
		log.Debug().Msg("swap sweep skipped: another replica holds the lock")
		return 0, false, nil
	}
	if swept > 0 {
		log.Info().Int("swept", swept).Msg("swap sweep released expired offers")
	}
	return swept, true, nil
}
