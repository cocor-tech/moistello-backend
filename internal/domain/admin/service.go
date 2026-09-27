package admin

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// ErrNilRepository is returned by NewService when constructed without a
// repository. Every metrics call would otherwise nil-panic on the first
// request instead of failing once, loudly, at startup.
var ErrNilRepository = errors.New("admin: NewService requires a non-nil Repository")

// DefaultCacheTTL bounds how long expensive aggregate results are served from
// the in-memory cache before being recomputed.
const DefaultCacheTTL = 60 * time.Second

type cacheEntry struct {
	value   any
	expires time.Time
}

// Service exposes platform metrics, caching expensive aggregates in memory
// for CacheTTL so the admin dashboard does not hammer the database.
type Service struct {
	repo Repository
	ttl  time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewService builds the admin metrics service. It returns ErrNilRepository
// for a nil repo (including a typed nil pointer wrapped in the interface) so
// callers fail fast at construction rather than panicking per request.
func NewService(repo Repository, ttl time.Duration) (*Service, error) {
	if isNilRepository(repo) {
		return nil, ErrNilRepository
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Service{
		repo:  repo,
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
	}, nil
}

func isNilRepository(repo Repository) bool {
	if repo == nil {
		return true
	}
	v := reflect.ValueOf(repo)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// GetMetrics returns the platform aggregate snapshot, cached for the service
// TTL.
func (s *Service) GetMetrics(ctx context.Context, days int) (*Metrics, error) {
	if days < 1 {
		days = 30
	}
	key := "metrics:" + strconv.Itoa(days)

	if v, ok := s.get(key); ok {
		return v.(*Metrics), nil
	}

	m, err := s.repo.Metrics(ctx, days)
	if err != nil {
		return nil, err
	}
	s.set(key, m)
	return m, nil
}

// GetDailyVolume returns the time-bucketed volume series, cached for the
// service TTL.
func (s *Service) GetDailyVolume(ctx context.Context, days int) ([]DailyVolumePoint, error) {
	if days < 1 {
		days = 30
	}
	key := "dailyVolume:" + strconv.Itoa(days)

	if v, ok := s.get(key); ok {
		return v.([]DailyVolumePoint), nil
	}

	points, err := s.repo.DailyVolume(ctx, days)
	if err != nil {
		return nil, err
	}
	s.set(key, points)
	return points, nil
}

func (s *Service) get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok || time.Now().After(entry.expires) {
		delete(s.cache, key)
		return nil, false
	}
	return entry.value, true
}

func (s *Service) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = cacheEntry{value: value, expires: time.Now().Add(s.ttl)}
}
