package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/metrics"
)

func newMonitorDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { mockDB.Close() })
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

// A connection stuck idle in a transaction (a leak on an error path) must
// raise an alert and be reflected in the gauge.
func TestPoolMonitor_DetectsIdleInTransaction(t *testing.T) {
	db, mock := newMonitorDB(t)
	mock.ExpectQuery("pg_stat_activity").
		WithArgs(60.0).
		WillReturnRows(sqlmock.NewRows([]string{"count", "oldest"}).AddRow(2, 120.0))

	var alerts []PoolAlert
	m := NewPoolMonitor(db, PoolMonitorOptions{}, func(a PoolAlert) { alerts = append(alerts, a) })
	m.Check(context.Background())

	require.Len(t, alerts, 1)
	require.Equal(t, AlertIdleInTransaction, alerts[0].Kind)
	require.Equal(t, 2.0, testutil.ToFloat64(metrics.DBIdleInTransaction))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Fault injection: a connection that is never released exhausts a pool of one,
// which must be reported as saturation even though the follow-up check cannot
// get a connection.
func TestPoolMonitor_DetectsPoolSaturation(t *testing.T) {
	db, _ := newMonitorDB(t)
	db.SetMaxOpenConns(1)

	leaked, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer leaked.Close()

	var alerts []PoolAlert
	m := NewPoolMonitor(db, PoolMonitorOptions{QueryTimeout: 50 * time.Millisecond}, func(a PoolAlert) { alerts = append(alerts, a) })
	m.Check(context.Background())

	require.Len(t, alerts, 1)
	require.Equal(t, AlertPoolSaturated, alerts[0].Kind)
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.DBPoolUtilization.WithLabelValues("saturation")))
}

func TestPoolMonitor_HealthyPoolRaisesNoAlert(t *testing.T) {
	db, mock := newMonitorDB(t)
	mock.ExpectQuery("pg_stat_activity").
		WillReturnRows(sqlmock.NewRows([]string{"count", "oldest"}).AddRow(0, 0.0))

	called := false
	m := NewPoolMonitor(db, PoolMonitorOptions{}, func(PoolAlert) { called = true })
	m.Check(context.Background())

	require.False(t, called)
}
