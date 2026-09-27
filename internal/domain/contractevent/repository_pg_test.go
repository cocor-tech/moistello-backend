package contractevent

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wasmHashV1 = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
const wasmHashV2 = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"

func newMockRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, dbm, err := sqlmock.New()
	require.NoError(t, err)
	return NewRepository(sqlx.NewDb(sqlDB, "postgres")), dbm, func() { _ = sqlDB.Close() }
}

func eventRows(versions ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{
		"id", "tx_hash", "ledger", "contract_id", "event_type", "contract_version", "payload", "processed_at",
	})
	for _, version := range versions {
		rows.AddRow(
			uuid.New(), "tx1", int64(100), "cc1", "CircleCreated", version,
			[]byte(`{"circle_id":"abc"}`), time.Now().UTC(),
		)
	}
	return rows
}

// TestList_NoFilterQueriesEverything checks the unfiltered path still works and
// still paginates.
func TestList_NoFilterQueriesEverything(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(9))
	dbm.ExpectQuery(`SELECT id, tx_hash, ledger, contract_id, event_type, contract_version, payload, processed_at FROM contract_events ORDER BY ledger DESC, id DESC LIMIT \$1 OFFSET \$2`).
		WithArgs(20, 0).
		WillReturnRows(eventRows(wasmHashV2, wasmHashV1))

	events, total, err := repo.List(context.Background(), ListFilter{}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 9, total)
	require.Len(t, events, 2)
	assert.Equal(t, wasmHashV2, events[0].ContractVersion)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_FilterByContractVersion is the point of the feature: a contract ID is
// reused across upgrades, so this filter is what separates the events produced
// by one implementation from those of another.
func TestList_FilterByContractVersion(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events WHERE contract_version = \$1`).
		WithArgs(wasmHashV2).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	dbm.ExpectQuery(`FROM contract_events WHERE contract_version = \$1 ORDER BY`).
		WithArgs(wasmHashV2, 20, 0).
		WillReturnRows(eventRows(wasmHashV2))

	events, total, err := repo.List(context.Background(), ListFilter{ContractVersion: wasmHashV2}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, events, 1)
	assert.Equal(t, wasmHashV2, events[0].ContractVersion)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_FilterByUnknownVersion checks "unknown" selects exactly the events
// whose version could not be resolved, rather than meaning "do not filter".
func TestList_FilterByUnknownVersion(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events WHERE contract_version = \$1`).
		WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(11))
	dbm.ExpectQuery(`FROM contract_events WHERE contract_version = \$1 ORDER BY`).
		WithArgs("unknown", 20, 0).
		WillReturnRows(eventRows("unknown"))

	events, total, err := repo.List(context.Background(), ListFilter{ContractVersion: "unknown"}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 11, total)
	require.Len(t, events, 1)
	assert.Equal(t, "unknown", events[0].ContractVersion)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_FilterByContractAndEventType pins the remaining equality filters to
// the columns they claim.
func TestList_FilterByContractAndEventType(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events WHERE contract_id = \$1 AND event_type = \$2`).
		WithArgs("cc1", "CircleCreated").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	dbm.ExpectQuery(`FROM contract_events WHERE contract_id = \$1 AND event_type = \$2 ORDER BY`).
		WithArgs("cc1", "CircleCreated", 20, 0).
		WillReturnRows(eventRows(wasmHashV1))

	_, total, err := repo.List(context.Background(), ListFilter{
		ContractID: "cc1",
		EventType:  "CircleCreated",
	}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_FilterByLedgerRange checks the bounds are inclusive and land on the
// ledger sequence.
func TestList_FilterByLedgerRange(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()
	from, to := int64(10), int64(20)

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events WHERE ledger >= \$1 AND ledger <= \$2`).
		WithArgs(int64(10), int64(20)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	dbm.ExpectQuery(`FROM contract_events WHERE ledger >= \$1 AND ledger <= \$2 ORDER BY`).
		WithArgs(int64(10), int64(20), 20, 0).
		WillReturnRows(eventRows(wasmHashV1))

	_, total, err := repo.List(context.Background(), ListFilter{FromLedger: &from, ToLedger: &to}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_AllFiltersCombine checks every clause keeps its own placeholder, so
// the full filter stays bound rather than interpolated.
func TestList_AllFiltersCombine(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()
	from := int64(5)

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events WHERE tx_hash = \$1 AND contract_id = \$2 AND event_type = \$3 AND contract_version = \$4 AND ledger >= \$5`).
		WithArgs("tx1", "cc1", "MemberJoined", wasmHashV1, int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	dbm.ExpectQuery(`FROM contract_events WHERE tx_hash = \$1 AND contract_id = \$2 AND event_type = \$3 AND contract_version = \$4 AND ledger >= \$5 ORDER BY ledger DESC, id DESC LIMIT \$6 OFFSET \$7`).
		WithArgs("tx1", "cc1", "MemberJoined", wasmHashV1, int64(5), 20, 0).
		WillReturnRows(eventRows(wasmHashV1))

	_, total, err := repo.List(context.Background(), ListFilter{
		TxHash:          "tx1",
		ContractID:      "cc1",
		EventType:       "MemberJoined",
		ContractVersion: wasmHashV1,
		FromLedger:      &from,
	}, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_PaginatesBeyondFirstPage checks the offset follows the page and that
// placeholders shift with the filter args.
func TestList_PaginatesBeyondFirstPage(t *testing.T) {
	repo, dbm, closeFn := newMockRepo(t)
	defer closeFn()

	dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(45))
	dbm.ExpectQuery(`FROM contract_events ORDER BY ledger DESC, id DESC LIMIT \$1 OFFSET \$2`).
		WithArgs(20, 20).
		WillReturnRows(eventRows(wasmHashV1))

	_, total, err := repo.List(context.Background(), ListFilter{}, 2, 20)
	require.NoError(t, err)
	assert.Equal(t, 45, total)
	assert.NoError(t, dbm.ExpectationsWereMet())
}

// TestList_ClampsPaging guards the pagination inputs rather than letting a
// caller request an unbounded scan of an append-only table.
func TestList_ClampsPaging(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		page, limit, wantLimit, wantOffset int
	}{
		{"page below one", 0, 20, 20, 0},
		{"negative page", -3, 20, 20, 0},
		{"limit below one", 1, 0, 20, 0},
		{"limit above maximum", 1, 5000, 20, 0},
		{"deep page", 3, 5, 5, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, dbm, closeFn := newMockRepo(t)
			defer closeFn()

			dbm.ExpectQuery(`SELECT COUNT\(\*\) FROM contract_events`).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			dbm.ExpectQuery(`FROM contract_events ORDER BY ledger DESC, id DESC LIMIT \$1 OFFSET \$2`).
				WithArgs(tc.wantLimit, tc.wantOffset).
				WillReturnRows(eventRows())

			_, _, err := repo.List(context.Background(), ListFilter{}, tc.page, tc.limit)
			require.NoError(t, err)
			assert.NoError(t, dbm.ExpectationsWereMet())
		})
	}
}

func TestListFilter_Predicates(t *testing.T) {
	lo, hi := int64(5), int64(10)

	assert.True(t, ListFilter{}.IsZero())
	assert.False(t, ListFilter{ContractVersion: wasmHashV1}.IsZero())
	assert.False(t, ListFilter{FromLedger: &lo}.IsZero())

	assert.False(t, ListFilter{FromLedger: &lo, ToLedger: &hi}.Inverted())
	assert.True(t, ListFilter{FromLedger: &hi, ToLedger: &lo}.Inverted())
	assert.False(t, ListFilter{FromLedger: &lo}.Inverted())
	assert.False(t, ListFilter{}.Inverted())
}
