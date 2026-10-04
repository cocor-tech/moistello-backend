package indexer

import (
	"context"
	"encoding/json"
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

// fakeDeadLetters records what the engine and reconciler dead-letter, so the
// behaviour can be asserted without a database.
type fakeDeadLetters struct {
	mu        sync.Mutex
	recorded  []*DeadLetterEntry
	recordErr error
}

func (f *fakeDeadLetters) Record(_ context.Context, entry *DeadLetterEntry) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordErr != nil {
		return "", f.recordErr
	}
	f.recorded = append(f.recorded, entry)
	return "entry-1", nil
}

func (f *fakeDeadLetters) List(context.Context, int) ([]DeadLetterEntry, error) {
	return nil, nil
}

func (f *fakeDeadLetters) Count(context.Context) (int, error) { return len(f.recorded), nil }

func (f *fakeDeadLetters) Resolve(context.Context, string) error { return nil }

func (f *fakeDeadLetters) entries() []*DeadLetterEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*DeadLetterEntry(nil), f.recorded...)
}

func sampleTxn() *Transaction {
	return &Transaction{
		Hash:      "abc123",
		Ledger:    4242,
		CreatedAt: time.Now(),
		Operations: []Operation{
			{ID: 1, Type: "invoke_host_function", SourceAccount: "GMASTER"},
		},
	}
}

// ---------------------------------------------------------------------------
// The gap this closes
// ---------------------------------------------------------------------------

// A failed event must leave a durable record. Previously the poll loop logged
// the error, advanced the cursor and moved on, so the event was lost with no
// trace that it had ever been seen.
func TestEngineDeadLetter_RecordsFailedEvent(t *testing.T) {
	store := &fakeDeadLetters{}
	metrics := newUnregisteredMetrics()
	engine := &Engine{deadLetters: store, metrics: metrics}

	engine.deadLetter(context.Background(), sampleTxn(), errors.New("contract decode failed"))

	recorded := store.entries()
	require.Len(t, recorded, 1)
	assert.Equal(t, "abc123", recorded[0].TxHash)
	assert.Equal(t, int64(4242), recorded[0].Ledger)
	assert.Equal(t, "stellar", recorded[0].Chain)
	assert.Equal(t, "contract decode failed", recorded[0].Error)
	assert.Equal(t, "dead_letter", recorded[0].Status)
	assert.Equal(t, 1, recorded[0].Attempts)

	// The raw transaction is kept so the event can be replayed without
	// re-fetching the ledger from Horizon.
	require.NotEmpty(t, recorded[0].Payload)
	assert.Contains(t, string(recorded[0].Payload), "abc123")

	assert.Equal(t, float64(1), counterValue(metrics.DeadLettered))
}

func TestEngineDeadLetter_NoopWhenStoreNotWired(t *testing.T) {
	// An engine without a store must not panic; the feature is opt-in.
	engine := &Engine{metrics: newUnregisteredMetrics()}
	assert.NotPanics(t, func() {
		engine.deadLetter(context.Background(), sampleTxn(), errors.New("boom"))
	})
}

// Bookkeeping must not escalate a recoverable per-event failure into a failed
// poll cycle, so a failing dead-letter write is swallowed after logging.
func TestEngineDeadLetter_SwallowsStoreFailure(t *testing.T) {
	store := &fakeDeadLetters{recordErr: errors.New("db down")}
	metrics := newUnregisteredMetrics()
	engine := &Engine{deadLetters: store, metrics: metrics}

	assert.NotPanics(t, func() {
		engine.deadLetter(context.Background(), sampleTxn(), errors.New("processing failed"))
	})
	assert.Empty(t, store.entries())
	assert.Equal(t, float64(0), counterValue(metrics.DeadLettered),
		"a failure to record must not be counted as a dead-lettered event")
}

// The reconciler advances the cursor past events it could not process, so it
// must record them too or they vanish just as silently.
func TestReconcilerRecordDeadLetter_RecordsFailedEvent(t *testing.T) {
	store := &fakeDeadLetters{}
	r := &Reconciler{deadLetters: store}

	r.recordDeadLetter(context.Background(), sampleTxn(), 4242, errors.New("constraint violation"))

	recorded := store.entries()
	require.Len(t, recorded, 1)
	assert.Equal(t, "abc123", recorded[0].TxHash)
	assert.Equal(t, int64(4242), recorded[0].Ledger)
	assert.Equal(t, "constraint violation", recorded[0].Error)
}

func TestReconcilerRecordDeadLetter_NoopWhenStoreNotWired(t *testing.T) {
	r := &Reconciler{}
	assert.NotPanics(t, func() {
		r.recordDeadLetter(context.Background(), sampleTxn(), 1, errors.New("boom"))
	})
}

func TestReconciler_WithDeadLettersWiresStore(t *testing.T) {
	store := &fakeDeadLetters{}
	r := NewReconciler(nil, nil, nil, nil).WithDeadLetters(store)
	require.NotNil(t, r.deadLetters)
	assert.Same(t, store, r.deadLetters)
}

// counterValue reads a counter for assertions, tolerating a nil collector so
// tests can build a partial IndexerMetrics.
func counterValue(c prometheus.Counter) float64 {
	if c == nil {
		return 0
	}
	return testutil.ToFloat64(c)
}

func TestMarshalPayload_NilTransaction(t *testing.T) {
	payload, err := MarshalPayload(nil)
	require.NoError(t, err)
	assert.Nil(t, payload)
}

func TestMarshalPayload_RoundTripsTheEvent(t *testing.T) {
	txn := sampleTxn()
	payload, err := MarshalPayload(txn)
	require.NoError(t, err)

	var decoded Transaction
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, txn.Hash, decoded.Hash)
	assert.Equal(t, txn.Ledger, decoded.Ledger)
	require.Len(t, decoded.Operations, 1)
	assert.Equal(t, "invoke_host_function", decoded.Operations[0].Type)
}

// ---------------------------------------------------------------------------
// PGDeadLetterStore
// ---------------------------------------------------------------------------

func newTestDeadLetterStore(t *testing.T) (DeadLetterStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	return NewDeadLetterStore(db), mock, func() { _ = db.Close() }
}

// A repeated failure must update the single row for that hash rather than
// inserting a new one each poll, otherwise a poison event grows the table
// without bound.
func TestPGDeadLetter_Record_UpsertsOnTxHashConflict(t *testing.T) {
	store, mock, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	mock.ExpectQuery("INSERT INTO indexer_dead_letter").
		WithArgs("stellar", "abc123", int64(4242), "boom", 1, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("11111111-1111-1111-1111-111111111111"))

	id, err := store.Record(context.Background(), &DeadLetterEntry{
		TxHash: "abc123", Ledger: 4242, Error: "boom",
	})
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDeadLetter_Record_RejectsMissingHash(t *testing.T) {
	store, _, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	_, err := store.Record(context.Background(), &DeadLetterEntry{Ledger: 1, Error: "boom"})
	require.Error(t, err)
}

func TestPGDeadLetter_Record_DefaultsChainAndAttempts(t *testing.T) {
	store, mock, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	entry := &DeadLetterEntry{TxHash: "xyz", Ledger: 9, Error: "boom"}
	mock.ExpectQuery("INSERT INTO indexer_dead_letter").
		WithArgs("stellar", "xyz", int64(9), "boom", 1, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("id-1"))

	_, err := store.Record(context.Background(), entry)
	require.NoError(t, err)
	assert.Equal(t, "stellar", entry.Chain, "chain must default to stellar")
	assert.Equal(t, 1, entry.Attempts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDeadLetter_List_OnlyUnresolvedNewestFirst(t *testing.T) {
	store, mock, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	mock.ExpectQuery("FROM indexer_dead_letter.*WHERE status = 'dead_letter'.*ORDER BY created_at DESC").
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "tx_hash", "ledger", "error", "attempts",
			"payload", "status", "created_at", "updated_at", "resolved_at",
		}).AddRow(
			"11111111-1111-1111-1111-111111111111", "stellar", "abc", int64(7),
			"boom", 3, nil, "dead_letter", time.Now(), time.Now(), nil,
		))

	entries, err := store.List(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "abc", entries[0].TxHash)
	assert.Equal(t, 3, entries[0].Attempts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDeadLetter_Resolve_UnknownEntryReportsNotFound(t *testing.T) {
	store, mock, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	mock.ExpectExec("UPDATE indexer_dead_letter").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Resolve(context.Background(), "missing")
	require.ErrorIs(t, err, ErrDeadLetterNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDeadLetter_Resolve_MarksResolved(t *testing.T) {
	store, mock, cleanup := newTestDeadLetterStore(t)
	defer cleanup()

	mock.ExpectExec("UPDATE indexer_dead_letter").
		WithArgs("id-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Resolve(context.Background(), "id-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}
