package postgres

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
)

// ReadQuery names a read-heavy, latency-tolerant query that may be served
// from a read replica. Only queries listed in replicaQueries are routed
// there; everything else stays on the primary.
type ReadQuery string

const (
	QueryAdminMetrics     ReadQuery = "admin.metrics"
	QueryAdminDailyVolume ReadQuery = "admin.daily_volume"
)

var replicaQueries = map[ReadQuery]struct{}{
	QueryAdminMetrics:     {},
	QueryAdminDailyVolume: {},
}

// Reader picks the database handle for a read query.
type Reader struct {
	primary *sqlx.DB
	replica *sqlx.DB
}

// NewReader returns a Reader over the primary and an optional replica. A nil
// replica sends every query to the primary.
func NewReader(primary, replica *sqlx.DB) *Reader {
	return &Reader{primary: primary, replica: replica}
}

// For returns the replica for whitelisted queries when one is configured,
// and the primary otherwise.
func (r *Reader) For(q ReadQuery) *sqlx.DB {
	if r.replica != nil {
		if _, ok := replicaQueries[q]; ok {
			return r.replica
		}
	}
	return r.primary
}

// NewReplica connects to the read replica configured in cfg.ReplicaURL. It
// returns nil when no replica is configured or it cannot be reached, so
// callers fall back to the primary.
func NewReplica(cfg config.DatabaseConfig) *sqlx.DB {
	if cfg.ReplicaURL == "" {
		return nil
	}
	db, err := sqlx.Connect("postgres", cfg.ReplicaURL)
	if err != nil {
		log.Warn().Err(fmt.Errorf("connecting to postgres replica: %w", err)).Msg("read replica unavailable — analytics queries will use the primary")
		return nil
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	log.Info().Msg("connected to PostgreSQL read replica")
	return db
}
