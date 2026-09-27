package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubVersionResolver records how many times it was consulted and returns a
// canned outcome, so caching can be observed rather than inferred.
type stubVersionResolver struct {
	mu      sync.Mutex
	calls   int
	version string
	err     error
}

func (s *stubVersionResolver) ResolveContractVersion(_ context.Context, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.version, s.err
}

func (s *stubVersionResolver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// fixedClock returns a clock the test advances by hand, so TTL expiry is tested
// without sleeping.
func fixedClock(start time.Time) func() time.Time {
	now := start
	return func() time.Time { return now }
}

func TestStaticContractVersions(t *testing.T) {
	versions := StaticContractVersions{"cc1": "hash1"}

	version, err := versions.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)
	assert.Equal(t, "hash1", version)

	_, err = versions.ResolveContractVersion(context.Background(), "cc2")
	assert.Error(t, err, "an unconfigured contract must not resolve to an empty version")

	// A configured-but-empty version is treated as unconfigured, so it cannot be
	// recorded as if it were a real version.
	_, err = StaticContractVersions{"cc1": ""}.ResolveContractVersion(context.Background(), "cc1")
	assert.Error(t, err)
}

// TestCachingResolver_ReusesSuccessWithinTTL is the reason the resolver is
// cached: a busy contract must not cost a ledger read per event.
func TestCachingResolver_ReusesSuccessWithinTTL(t *testing.T) {
	inner := &stubVersionResolver{version: "hash1"}
	c := NewCachingContractVersionResolver(inner, time.Minute)
	c.now = fixedClock(time.Unix(0, 0))

	for i := 0; i < 5; i++ {
		version, err := c.ResolveContractVersion(context.Background(), "cc1")
		require.NoError(t, err)
		assert.Equal(t, "hash1", version)
	}
	assert.Equal(t, 1, inner.callCount(), "a successful lookup must be reused for the whole TTL")
}

// TestCachingResolver_RefreshesAfterTTL checks the cache follows a contract
// upgrade once the window passes.
func TestCachingResolver_RefreshesAfterTTL(t *testing.T) {
	clock := fixedClock(time.Unix(0, 0))
	inner := &stubVersionResolver{version: "hash1"}
	c := NewCachingContractVersionResolver(inner, time.Minute)
	c.now = clock

	_, err := c.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)

	// Still inside the window: the old version is served without a new lookup.
	version, err := c.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)
	assert.Equal(t, "hash1", version)
	assert.Equal(t, 1, inner.callCount())

	// The contract is upgraded and the window has passed.
	inner.mu.Lock()
	inner.version = "hash2"
	inner.mu.Unlock()
	now := time.Unix(0, 0).Add(2 * time.Minute)
	c.now = func() time.Time { return now }

	version, err = c.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)
	assert.Equal(t, "hash2", version)
	assert.Equal(t, 2, inner.callCount())
}

// TestCachingResolver_CachesPerContract checks two contracts do not share an
// entry, which is what keeps one contract's version from being attributed to
// another.
func TestCachingResolver_CachesPerContract(t *testing.T) {
	inner := &stubVersionResolver{version: "hash1"}
	c := NewCachingContractVersionResolver(inner, time.Minute)
	c.now = fixedClock(time.Unix(0, 0))

	_, err := c.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)
	_, err = c.ResolveContractVersion(context.Background(), "cc2")
	require.NoError(t, err)

	assert.Equal(t, 2, inner.callCount())

	_, _ = c.ResolveContractVersion(context.Background(), "cc1")
	_, _ = c.ResolveContractVersion(context.Background(), "cc2")
	assert.Equal(t, 2, inner.callCount())
}

// TestCachingResolver_CachesFailureBriefly checks a failure is remembered so a
// broken contract cannot cause a read per event, but for a much shorter window
// than a success so a blip clears quickly.
func TestCachingResolver_CachesFailureBriefly(t *testing.T) {
	start := time.Unix(0, 0)
	now := start
	inner := &stubVersionResolver{err: errors.New("rpc down")}
	c := NewCachingContractVersionResolver(inner, time.Minute)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		_, err := c.ResolveContractVersion(context.Background(), "cc1")
		assert.Error(t, err)
	}
	assert.Equal(t, 1, inner.callCount(), "a failure must be retried only after the shorter window")

	// Just inside the failure window the failure is still served from cache.
	now = start.Add(c.failureTTL - time.Second)
	_, err := c.ResolveContractVersion(context.Background(), "cc1")
	assert.Error(t, err)
	assert.Equal(t, 1, inner.callCount())

	// Past the failure window the lookup is retried.
	now = start.Add(c.failureTTL + time.Second)
	_, err = c.ResolveContractVersion(context.Background(), "cc1")
	assert.Error(t, err)
	assert.Equal(t, 2, inner.callCount())

	// Once the underlying problem clears, the next window serves the real
	// version: a transient error does not turn into a long run of unknown.
	inner.mu.Lock()
	inner.err = nil
	inner.version = "hash1"
	inner.mu.Unlock()

	now = start.Add(2*c.failureTTL + 2*time.Second)
	version, err := c.ResolveContractVersion(context.Background(), "cc1")
	require.NoError(t, err)
	assert.Equal(t, "hash1", version)
	assert.Equal(t, 3, inner.callCount())
}

// TestCachingResolver_RejectsEmptySuccess guards a resolver that returns neither
// a version nor an error: caching that as a success would write a blank version
// to the audit log as though it were real.
func TestCachingResolver_RejectsEmptySuccess(t *testing.T) {
	inner := &stubVersionResolver{version: ""}
	c := NewCachingContractVersionResolver(inner, time.Minute)
	c.now = fixedClock(time.Unix(0, 0))

	version, err := c.ResolveContractVersion(context.Background(), "cc1")
	assert.Error(t, err)
	assert.Empty(t, version)
}

// TestCachingResolver_DefaultsTTL checks a non-positive ttl falls back to the
// documented default instead of caching forever or never.
func TestCachingResolver_DefaultsTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		c := NewCachingContractVersionResolver(&stubVersionResolver{version: "hash1"}, ttl)
		assert.Equal(t, DefaultContractVersionTTL, c.ttl)
		assert.Greater(t, c.failureTTL, time.Duration(0))
		assert.Less(t, c.failureTTL, c.ttl, "failures must be retried sooner than successes")
	}
}

// TestCachingResolver_ConcurrentUse is a race-detector guard: the indexer
// consults the resolver from its processing loop, and a data race here would
// corrupt the cache map.
func TestCachingResolver_ConcurrentUse(t *testing.T) {
	c := NewCachingContractVersionResolver(&stubVersionResolver{version: "hash1"}, time.Minute)
	c.now = fixedClock(time.Unix(0, 0))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := c.ResolveContractVersion(context.Background(), "cc1")
			assert.NoError(t, err)
			assert.Equal(t, "hash1", version)
		}()
	}
	wg.Wait()
}

func TestSorobanContractVersions_RequiresLedgerClient(t *testing.T) {
	_, err := SorobanContractVersions{}.ResolveContractVersion(context.Background(), "cc1")
	assert.Error(t, err, "a nil ledger client must fail rather than panic")
}

// TestPersistContractEvent_RecordsVersion checks the resolved version reaches
// the audit log, which is what makes an event attributable to a contract
// implementation.
func TestPersistContractEvent_RecordsVersion(t *testing.T) {
	db, dbm, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	ev := contractEvent(EventCircleCreated, "cc1", nil)
	dbm.ExpectExec(`INSERT INTO contract_events \(tx_hash, ledger, contract_id, event_type, contract_version, payload, processed_at\)`).
		WithArgs(ev.TxHash, ev.Ledger, ev.ContractID, ev.EventType, "hash1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	p := &EventProcessor{
		db:       sqlx.NewDb(db, "sqlmock"),
		versions: StaticContractVersions{"cc1": "hash1"},
	}
	require.NoError(t, p.persistContractEvent(context.Background(), ev))
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestPersistContractEvent_UnknownVersion covers the two ways a version can be
// unknown. The audit row is still written either way, because losing the row
// loses more than the version does.
func TestPersistContractEvent_UnknownVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		versions  ContractVersionResolver
		wantCalls float64
	}{
		{"no resolver configured", nil, 0},
		{"resolution fails", &stubVersionResolver{err: errors.New("rpc down")}, 1},
		{"resolver returns nothing", &stubVersionResolver{version: ""}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, dbm, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			registry := prometheus.NewRegistry()
			counter := prometheus.NewCounter(prometheus.CounterOpts{
				Name: "test_contract_version_unknown_total",
				Help: "test counter",
			})
			registry.MustRegister(counter)

			ev := contractEvent(EventCircleCreated, "cc1", nil)
			dbm.ExpectExec(`INSERT INTO contract_events`).
				WithArgs(ev.TxHash, ev.Ledger, ev.ContractID, ev.EventType,
					ContractVersionUnknown, sqlmock.AnyArg(), sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))

			p := &EventProcessor{
				db:             sqlx.NewDb(db, "sqlmock"),
				versions:       tc.versions,
				versionUnknown: counter,
			}
			require.NoError(t, p.persistContractEvent(context.Background(), ev))
			assert.NoError(t, dbm.ExpectationsWereMet())
			assert.Equal(t, tc.wantCalls, testutil.ToFloat64(counter),
				"an unresolvable version must be counted so the failure is visible")
		})
	}
}
