package swap

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/user"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

// sweepHarness builds a service over a shared, in-memory offer set whose claims
// are genuinely atomic, so concurrency is exercised end to end rather than
// stubbed out.
type sweepHarness struct {
	offers   []SwapOffer
	mu       sync.Mutex
	claimed  map[string]bool
	released atomic.Int64
	cancels  atomic.Int64
}

func newSweepHarness(offerIDs ...string) *sweepHarness {
	h := &sweepHarness{claimed: make(map[string]bool)}
	for _, id := range offerIDs {
		h.offers = append(h.offers, SwapOffer{
			ID:            id,
			OfferorUserID: "u-" + id,
			Status:        SwapOfferStatusCreated,
			ExpiresAt:     time.Now().Add(-time.Hour), // already expired
		})
	}
	return h
}

func (h *sweepHarness) repo() Repository {
	return &concurrentSweepRepo{h: h}
}

func (h *sweepHarness) users() UserService {
	return &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
}

func (h *sweepHarness) escrow() EscrowClient {
	return &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		h.cancels.Add(1)
		// Widen the window in which a second replica could interleave, which is
		// exactly the race this test exists to catch.
		time.Sleep(20 * time.Millisecond)
		return "tx-" + swapID, nil
	}}
}

// concurrentSweepRepo models the database semantics that matter: the claim is a
// compare-and-set that exactly one caller can win.
type concurrentSweepRepo struct {
	h *sweepHarness
	noopRepo
}

func (r *concurrentSweepRepo) ListExpiredCreatedOffers(ctx context.Context, now time.Time) ([]SwapOffer, error) {
	return r.h.offers, nil
}

func (r *concurrentSweepRepo) ClaimOfferForSweep(ctx context.Context, id string, now time.Time) (bool, error) {
	r.h.mu.Lock()

// ---------------------------------------------------------------------------
// Concurrent sweepers over the same expired-offer set (#416)
// ---------------------------------------------------------------------------

// Two replicas sweeping the same expired offers must release escrow exactly
// once per offer. This is the double-release the issue describes.
func TestConcurrentSweepers_ReleaseEscrowExactlyOncePerOffer(t *testing.T) {
	ctx := context.Background()
	h := newSweepHarness("offer-1", "offer-2", "offer-3")

	const replicas = 6
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, replicas)

	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each replica gets its own service over the same shared store,
			// mirroring separate processes hitting one database.
			svc := NewService(h.repo(), nil, h.users(), h.escrow())
			<-start
			swept, err := svc.SweepExpiredOffers(ctx)
			assert.NoError(t, err)
			results[idx] = swept
		}(i)
	}

	close(start)
	wg.Wait()

	// Exactly one release per offer, no matter how many replicas raced.
	assert.Equal(t, int64(3), h.cancels.Load(),
		"each expired offer must have its escrow released exactly once")

	// And exactly one replica reported doing the work for each offer.
	total := 0
	for _, r := range results {
		total += r
	}
	assert.Equal(t, 3, total, "the swept count must total one per offer")
	assert.Equal(t, int64(0), h.released.Load(), "no claim should need returning on the happy path")
}

// The single-flight lock is the outer guard: concurrent replicas should skip
// the tick entirely rather than all reaching the claim step.
func TestSweepOnce_OnlyOneReplicaHoldsTheLock(t *testing.T) {
	ctx := context.Background()
	_, rdb := newTestRedis(t)
	h := newSweepHarness("offer-1", "offer-2")

	svc := NewService(h.repo(), nil, h.users(), h.escrow())

	const replicas = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var heldCount atomic.Int64
	var sweepTotal atomic.Int64

	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One sweeper per replica, all contending on the same lock key.
			s := NewSweeper(svc, rdb, time.Minute)
			<-start
			swept, held, err := s.SweepOnce(ctx)
			assert.NoError(t, err)
			if held {
				heldCount.Add(1)
				sweepTotal.Add(int64(swept))
			}
		}()
	}

	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), heldCount.Load(), "exactly one replica may hold the sweep lock")
	assert.Equal(t, int64(2), sweepTotal.Load(), "the lock holder sweeps both offers")
	assert.Equal(t, int64(2), h.cancels.Load(), "escrow released once per offer despite 8 replicas")
}

// A replica that does not hold the lock must not sweep at all.
func TestSweepOnce_SkippedWhenLockHeldElsewhere(t *testing.T) {
	ctx := context.Background()
	_, rdb := newTestRedis(t)
	h := newSweepHarness("offer-1")

	svc := NewService(h.repo(), nil, h.users(), h.escrow())

	// Simulate another replica already holding the lock.
	blocker := NewSweeper(svc, rdb, time.Minute)
	acquired, err := blocker.lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	sweeper := NewSweeper(svc, rdb, time.Minute)
	swept, held, err := sweeper.SweepOnce(ctx)

	require.NoError(t, err)
	assert.False(t, held, "a replica that cannot take the lock must skip the tick")
	assert.Equal(t, 0, swept)
	assert.Equal(t, int64(0), h.cancels.Load(), "no escrow work may happen without the lock")
}

// The lock must be released after a pass so the next tick is not blocked.
func TestSweepOnce_ReleasesLockAfterPass(t *testing.T) {
	ctx := context.Background()
	_, rdb := newTestRedis(t)
	h := newSweepHarness("offer-1")

	svc := NewService(h.repo(), nil, h.users(), h.escrow())
	sweeper := NewSweeper(svc, rdb, time.Minute)

	_, held, err := sweeper.SweepOnce(ctx)
	require.NoError(t, err)
	require.True(t, held)

	exists, err := rdb.Exists(ctx, SweepLockKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "the lock must be released after the pass")
}

// With no Redis configured the sweep must still run — otherwise a
// single-replica deployment would silently stop expiring offers.
func TestSweepOnce_RunsWithoutRedis(t *testing.T) {
	ctx := context.Background()
	h := newSweepHarness("offer-1")

	svc := NewService(h.repo(), nil, h.users(), h.escrow())
	sweeper := NewSweeper(svc, nil, time.Minute)

	swept, held, err := sweeper.SweepOnce(ctx)
	require.NoError(t, err)
	assert.True(t, held)
	assert.Equal(t, 1, swept)
}

func TestSweeper_StartStopIsIdempotent(t *testing.T) {
	_, rdb := newTestRedis(t)
	h := newSweepHarness("offer-1")
	svc := NewService(h.repo(), nil, h.users(), h.escrow())

	sweeper := NewSweeper(svc, rdb, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweeper.Start(ctx)
	sweeper.Start(ctx) // duplicate start must be safe
	time.Sleep(30 * time.Millisecond)
	sweeper.Stop()
	sweeper.Stop() // duplicate stop must be safe
}

	defer r.h.mu.Unlock()
	if r.h.claimed[id] {
		return false, nil
	}
	r.h.claimed[id] = true
	return true, nil
}

func (r *concurrentSweepRepo) ReleaseSweepClaim(ctx context.Context, id string) error {
	r.h.released.Add(1)
	return nil
}

func (r *concurrentSweepRepo) FinalizeSweep(ctx context.Context, id string, transactionHash *string) error {
	return nil
}

// noopRepo supplies the rest of the Repository interface, which the sweep path
// never touches.
type noopRepo struct{}

func (noopRepo) CreateSwapOffer(context.Context, *SwapOffer) error { return nil }

func (noopRepo) GetSwapOfferByID(context.Context, string) (*SwapOffer, error) {
	return nil, nil
}

func (noopRepo) UpdateSwapOfferStatus(context.Context, string, SwapOfferStatus, *string) error {
	return nil
}

func (noopRepo) CompareAndSwapStatus(context.Context, string, SwapOfferStatus, SwapOfferStatus, *string) (bool, error) {
	return true, nil
}

func (noopRepo) ListUserSwapOffers(context.Context, string, SwapHistoryFilter) ([]SwapOffer, int, error) {
	return nil, 0, nil
}

func (noopRepo) ListCircleSwapOffers(context.Context, string, SwapHistoryFilter) ([]SwapOffer, int, error) {
	return nil, 0, nil
}
