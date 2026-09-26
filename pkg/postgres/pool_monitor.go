package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/pkg/metrics"
)

// Pool alert kinds passed to the PoolMonitor alert hook.
const (
	AlertPoolSaturated     = "pool_saturated"
	AlertIdleInTransaction = "idle_in_transaction"
)

// PoolAlert describes a suspected connection leak or pool exhaustion.
type PoolAlert struct {
	Kind   string
	Detail string
}

// PoolMonitorOptions tunes the monitor; zero values select the defaults.
type PoolMonitorOptions struct {
	// Interval between samples (default 15s).
	Interval time.Duration
	// IdleInTxThreshold is how long a connection may sit "idle in transaction"
	// before it is reported as leaked (default 60s).
	IdleInTxThreshold time.Duration
	// SaturationThreshold is the in-use / max-open ratio that triggers a
	// saturation alert (default 0.9). Ignored when the pool is unbounded.
	SaturationThreshold float64
	// QueryTimeout bounds the pg_stat_activity check, which needs a pool
	// connection and so cannot run while the pool is exhausted (default 5s).
	QueryTimeout time.Duration
}

// PoolMonitor samples database/sql pool stats and pg_stat_activity to expose
// pool saturation, connection acquire wait time and connections stuck idle in
// a transaction (the usual signature of a leak on an error path), and calls an
// alert hook when either problem is detected.
type PoolMonitor struct {
	db    *sqlx.DB
	opts  PoolMonitorOptions
	alert func(PoolAlert)

	lastWaitCount    int64
	lastWaitDuration time.Duration
}

// NewPoolMonitor builds a monitor. alert may be nil, in which case alerts are
// only logged and counted.
func NewPoolMonitor(db *sqlx.DB, opts PoolMonitorOptions, alert func(PoolAlert)) *PoolMonitor {
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Second
	}
	if opts.IdleInTxThreshold <= 0 {
		opts.IdleInTxThreshold = 60 * time.Second
	}
	if opts.SaturationThreshold <= 0 {
		opts.SaturationThreshold = 0.9
	}
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = 5 * time.Second
	}
	return &PoolMonitor{db: db, opts: opts, alert: alert}
}

// Run samples the pool until ctx is cancelled.
func (m *PoolMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.Check(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// Check takes one sample: it updates the pool metrics and fires the alert
// hook for saturation and for connections idle in transaction.
func (m *PoolMonitor) Check(ctx context.Context) {
	stats := m.db.Stats()

	metrics.DBPoolUtilization.WithLabelValues("open").Set(float64(stats.OpenConnections))
	metrics.DBPoolUtilization.WithLabelValues("in_use").Set(float64(stats.InUse))
	metrics.DBPoolUtilization.WithLabelValues("idle").Set(float64(stats.Idle))
	metrics.DBPoolUtilization.WithLabelValues("max_open").Set(float64(stats.MaxOpenConnections))

	// Acquire wait: how many callers had to wait for a free connection, and
	// for how long, since the previous sample.
	waits := stats.WaitCount - m.lastWaitCount
	waited := stats.WaitDuration - m.lastWaitDuration
	m.lastWaitCount, m.lastWaitDuration = stats.WaitCount, stats.WaitDuration
	if waits > 0 {
		metrics.DBPoolWaitTotal.Add(float64(waits))
		metrics.DBPoolWaitSecondsTotal.Add(waited.Seconds())
	}

	if stats.MaxOpenConnections > 0 {
		saturation := float64(stats.InUse) / float64(stats.MaxOpenConnections)
		metrics.DBPoolUtilization.WithLabelValues("saturation").Set(saturation)
		if saturation >= m.opts.SaturationThreshold {
			m.raise(PoolAlert{
				Kind: AlertPoolSaturated,
				Detail: fmt.Sprintf("%d/%d connections in use, %d acquire waits (%s) since last check",
					stats.InUse, stats.MaxOpenConnections, waits, waited),
			})
		}
	}

	m.checkIdleInTransaction(ctx)
}

func (m *PoolMonitor) checkIdleInTransaction(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, m.opts.QueryTimeout)
	defer cancel()

	var count int
	var oldest float64
	err := m.db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(EXTRACT(EPOCH FROM max(now() - state_change)), 0)
		FROM pg_stat_activity
		WHERE datname = current_database()
		  AND usename = current_user
		  AND pid <> pg_backend_pid()
		  AND state IN ('idle in transaction', 'idle in transaction (aborted)')
		  AND now() - state_change > make_interval(secs => $1)`,
		m.opts.IdleInTxThreshold.Seconds(),
	).Scan(&count, &oldest)
	if err != nil {
		log.Warn().Err(err).Msg("db pool monitor: idle-in-transaction check failed")
		return
	}

	metrics.DBIdleInTransaction.Set(float64(count))
	if count > 0 {
		m.raise(PoolAlert{
			Kind: AlertIdleInTransaction,
			Detail: fmt.Sprintf("%d connections idle in transaction for over %s (oldest %.0fs)",
				count, m.opts.IdleInTxThreshold, oldest),
		})
	}
}

func (m *PoolMonitor) raise(a PoolAlert) {
	metrics.DBPoolAlertsTotal.WithLabelValues(a.Kind).Inc()
	if m.alert != nil {
		m.alert(a)
	}
}
