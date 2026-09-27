package admin

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	metricsCalls atomic.Int32
	dailyCalls   atomic.Int32
	metrics      *Metrics
	daily        []DailyVolumePoint
	metricsErr   error
	dailyErr     error
}

func (f *fakeRepo) Metrics(_ context.Context, days int) (*Metrics, error) {
	f.metricsCalls.Add(1)
	return f.metrics, f.metricsErr
}

func (f *fakeRepo) DailyVolume(_ context.Context, days int) ([]DailyVolumePoint, error) {
	f.dailyCalls.Add(1)
	return f.daily, f.dailyErr
}

func TestService_GetMetricsCachesAggregate(t *testing.T) {
	repo := &fakeRepo{metrics: &Metrics{TotalUsers: 42, TotalVolumeUSD: 100.5}}
	svc := mustNewService(t, repo, time.Minute)

	m1, err := svc.GetMetrics(context.Background(), 30)
	require.NoError(t, err)
	m2, err := svc.GetMetrics(context.Background(), 30)
	require.NoError(t, err)

	assert.Equal(t, 42, m1.TotalUsers)
	assert.Equal(t, m1, m2)
	// Second call must be served from cache: the repo is hit only once.
	assert.Equal(t, int32(1), repo.metricsCalls.Load())
}

func TestService_CacheExpires(t *testing.T) {
	repo := &fakeRepo{metrics: &Metrics{TotalUsers: 1}}
	svc := mustNewService(t, repo, 20*time.Millisecond)

	_, err := svc.GetMetrics(context.Background(), 30)
	require.NoError(t, err)

	time.Sleep(40 * time.Millisecond)
	_, err = svc.GetMetrics(context.Background(), 30)
	require.NoError(t, err)

	assert.Equal(t, int32(2), repo.metricsCalls.Load(), "expired cache entries must be recomputed")
}

func TestService_DifferentWindowsAreCachedSeparately(t *testing.T) {
	repo := &fakeRepo{metrics: &Metrics{TotalUsers: 1}}
	svc := mustNewService(t, repo, time.Minute)

	_, err := svc.GetMetrics(context.Background(), 7)
	require.NoError(t, err)
	_, err = svc.GetMetrics(context.Background(), 30)
	require.NoError(t, err)

	assert.Equal(t, int32(2), repo.metricsCalls.Load())
}

func TestService_GetDailyVolumeCaches(t *testing.T) {
	repo := &fakeRepo{daily: []DailyVolumePoint{{ContributionVolume: 5}}}
	svc := mustNewService(t, repo, time.Minute)

	d1, err := svc.GetDailyVolume(context.Background(), 30)
	require.NoError(t, err)
	d2, err := svc.GetDailyVolume(context.Background(), 30)
	require.NoError(t, err)

	assert.Equal(t, 5.0, d1[0].ContributionVolume)
	assert.Equal(t, d1, d2)
	assert.Equal(t, int32(1), repo.dailyCalls.Load())
}

func TestService_PropagatesRepositoryErrors(t *testing.T) {
	repo := &fakeRepo{metricsErr: assert.AnError}
	svc := mustNewService(t, repo, time.Minute)

	_, err := svc.GetMetrics(context.Background(), 30)
	require.ErrorIs(t, err, assert.AnError)

	// Errors must not be cached.
	_, err = svc.GetMetrics(context.Background(), 30)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, int32(2), repo.metricsCalls.Load())
}

func TestNewService_DefaultTTL(t *testing.T) {
	svc := mustNewService(t, &fakeRepo{}, 0)
	assert.Equal(t, DefaultCacheTTL, svc.ttl)
}

func mustNewService(t *testing.T, repo Repository, ttl time.Duration) *Service {
	t.Helper()
	svc, err := NewService(repo, ttl)
	require.NoError(t, err)
	return svc
}

// Regression guard (#396): a nil repository must be rejected at construction,
// never reach GetMetrics and panic per request.
func TestNewService_NilRepositoryReturnsError(t *testing.T) {
	svc, err := NewService(nil, 0)
	require.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, svc)
}

func TestNewService_TypedNilRepositoryReturnsError(t *testing.T) {
	var repo *fakeRepo // typed nil wrapped in the Repository interface
	svc, err := NewService(repo, 0)
	require.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, svc)
}
