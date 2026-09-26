package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

// newUnregisteredMetrics builds an IndexerMetrics using bare prometheus
// constructors (not promauto), so it is never registered against the
// default registry. That lets each test build its own instance without
// tripping "duplicate metrics collector registration" panics.
func newUnregisteredMetrics() *IndexerMetrics {
	return &IndexerMetrics{
		EventsProcessed:       prometheus.NewCounter(prometheus.CounterOpts{Name: "test_events_processed"}),
		PollErrors:            prometheus.NewCounter(prometheus.CounterOpts{Name: "test_poll_errors"}),
		ProcessErrors:         prometheus.NewCounter(prometheus.CounterOpts{Name: "test_process_errors"}),
		LastLedger:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_last_ledger"}),
		ReconcilerRuns:        prometheus.NewCounter(prometheus.CounterOpts{Name: "test_reconciler_runs"}),
		DedupSize:             prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_dedup_size"}),
		CursorLagSeconds:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_cursor_lag_seconds"}),
		UnknownContractEvents: prometheus.NewCounter(prometheus.CounterOpts{Name: "test_unknown_contract_events"}),
	}
}

func TestEngine_Poll_SetsCursorLagMetric(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	tracker := NewCursorTracker(db)

	lastProcessed := time.Now().Add(-42 * time.Second)
	rows := sqlmock.NewRows([]string{"chain", "last_ledger", "last_processed_at"}).
		AddRow("stellar", int64(500), lastProcessed)
	mock.ExpectQuery("SELECT chain, last_ledger, last_processed_at FROM indexer_cursor").WillReturnRows(rows)

	// No new ledgers — poll() should still record the cursor lag before
	// returning early.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(LedgerResponse{})
	}))
	defer server.Close()

	poller := NewPoller(server.URL, nil)
	metrics := newUnregisteredMetrics()

	engine := &Engine{
		cfg:     config.IndexerConfig{BatchSize: 10},
		cursor:  tracker,
		poller:  poller,
		metrics: metrics,
	}

	err = engine.poll(context.Background())

	require.NoError(t, err)
	assert.InDelta(t, 42, testutil.ToFloat64(metrics.CursorLagSeconds), 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEngine_Poll_CursorLagMetric_UpdatesOnStaleCursor(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	tracker := NewCursorTracker(db)

	staleSince := time.Now().Add(-10 * time.Minute)
	rows := sqlmock.NewRows([]string{"chain", "last_ledger", "last_processed_at"}).
		AddRow("stellar", int64(500), staleSince)
	mock.ExpectQuery("SELECT chain, last_ledger, last_processed_at FROM indexer_cursor").WillReturnRows(rows)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(LedgerResponse{})
	}))
	defer server.Close()

	poller := NewPoller(server.URL, nil)
	metrics := newUnregisteredMetrics()

	engine := &Engine{
		cfg:     config.IndexerConfig{BatchSize: 10},
		cursor:  tracker,
		poller:  poller,
		metrics: metrics,
	}

	err = engine.poll(context.Background())

	require.NoError(t, err)
	assert.InDelta(t, 600, testutil.ToFloat64(metrics.CursorLagSeconds), 2)
}
