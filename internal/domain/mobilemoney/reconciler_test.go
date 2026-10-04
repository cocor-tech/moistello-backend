package mobilemoney

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

	moistelloredis "github.com/moistello/backend/pkg/redis"
)

type mockReconcileService struct {
	reconcileFn func(ctx context.Context) (int, error)
	callCount   atomic.Int64
}

func (m *mockReconcileService) InitiateOnramp(ctx context.Context, userID string, req OnrampRequest, idempotencyKey string) (*Transaction, error) {
	return nil, nil
}
func (m *mockReconcileService) InitiateOfframp(ctx context.Context, userID string, req OfframpRequest, idempotencyKey string) (*Transaction, error) {
	return nil, nil
}
func (m *mockReconcileService) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	return nil, nil
}
func (m *mockReconcileService) Reconcile(ctx context.Context) (int, error) {
	m.callCount.Add(1)
	if m.reconcileFn != nil {
		return m.reconcileFn(ctx)
	}
	return 1, nil
}
func (m *mockReconcileService) ListProviders(ctx context.Context) []ProviderInfo {
	return nil
}
func (m *mockReconcileService) GetSupportedCurrencies(ctx context.Context) []string {
	return nil
}

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestReconcilerSingleRunSuccess(t *testing.T) {
	_, rdb := newTestRedis(t)
	svc := &mockReconcileService{
		reconcileFn: func(ctx context.Context) (int, error) {
			return 3, nil
		},
	}

	reconciler := NewReconciler(rdb, svc, time.Minute, 0)
	count, held, err := reconciler.RunOnce(context.Background())

	require.NoError(t, err)
	assert.True(t, held)
	assert.Equal(t, 3, count)
	assert.Equal(t, int64(1), svc.callCount.Load())

	// Verify lock was released
	exists, err := rdb.Exists(context.Background(), reconcileLockKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "lock should be released after run")
}

func TestReconcilerConcurrentReplicasProduceSingleEffectivePass(t *testing.T) {
	const replicas = 8
	_, rdb := newTestRedis(t)

	svc := &mockReconcileService{
		reconcileFn: func(ctx context.Context) (int, error) {
			time.Sleep(50 * time.Millisecond)
			return 5, nil
		},
	}

	startTrigger := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]bool, replicas)
	counts := make([]int, replicas)

	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := NewReconciler(rdb, svc, time.Minute, 0)
			<-startTrigger
			c, held, err := r.RunOnce(context.Background())
			assert.NoError(t, err)
			results[idx] = held
			counts[idx] = c
		}(i)
	}

	close(startTrigger)
	wg.Wait()

	winners := 0
	for i, held := range results {
		if held {
			winners++
			assert.Equal(t, 5, counts[i])
		} else {
			assert.Equal(t, 0, counts[i])
		}
	}

	assert.Equal(t, 1, winners, "exactly one replica should acquire the lock and reconcile")
	assert.Equal(t, int64(1), svc.callCount.Load(), "effective reconcile pass must be exactly 1")
}

func TestReconcilerHeartbeatRenewsLock(t *testing.T) {
	_, rdb := newTestRedis(t)

	svc := &mockReconcileService{
		reconcileFn: func(ctx context.Context) (int, error) {
			time.Sleep(150 * time.Millisecond)
			return 2, nil
		},
	}

	reconciler := NewReconciler(rdb, svc, time.Minute, 0)
	// Very short TTL to force the shared lock's heartbeat renewal to run
	// during the pass. #416 moved the lock into pkg/redis, so the TTL now
	// lives on the shared lock rather than on the reconciler.
	reconciler.lock = moistelloredis.NewLock(rdb, reconcileLockKey, 100*time.Millisecond)

	count, held, err := reconciler.RunOnce(context.Background())
	require.NoError(t, err)
	assert.True(t, held)
	assert.Equal(t, 2, count)
}

func TestReconcilerStartStop(t *testing.T) {
	_, rdb := newTestRedis(t)
	svc := &mockReconcileService{}

	reconciler := NewReconciler(rdb, svc, 10*time.Millisecond, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler.Start(ctx)
	reconciler.Start(ctx) // Duplicate start should be safe

	time.Sleep(50 * time.Millisecond)
	reconciler.Stop()
	reconciler.Stop() // Duplicate stop should be safe

	assert.GreaterOrEqual(t, svc.callCount.Load(), int64(1))
}
