package postgres

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
)

// ValidatePoolSettings checks database connection pool settings and emits
// startup warnings when configuration appears inconsistent with production
// topology requirements (e.g., large direct connections vs connection poolers).
func ValidatePoolSettings(cfg config.DatabaseConfig) {
	if cfg.MaxOpenConns <= 0 {
		log.Warn().Int("max_open_conns", cfg.MaxOpenConns).
			Msg("database pool: max_open_conns <= 0 (unbounded connection pool); risky in production")
	}
	if cfg.MaxIdleConns > cfg.MaxOpenConns && cfg.MaxOpenConns > 0 {
		log.Warn().Int("max_idle_conns", cfg.MaxIdleConns).Int("max_open_conns", cfg.MaxOpenConns).
			Msg("database pool: max_idle_conns exceeds max_open_conns; database/sql will clamp max_idle_conns to max_open_conns")
	}
	if cfg.MaxOpenConns >= 50 {
		log.Warn().Int("max_open_conns", cfg.MaxOpenConns).
			Msg("database pool: max_open_conns is set to 50 or higher; in multi-replica production deployments without PgBouncer, total backend connections = replicas * max_open_conns, which may exhaust PostgreSQL max_connections. Consider using PgBouncer or lowering max_open_conns per replica (see docs/deploy/database-pool-sizing.md)")
	}
}

func New(cfg config.DatabaseConfig) (*sqlx.DB, error) {
	ValidatePoolSettings(cfg)

	db, err := sqlx.Connect("postgres", cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	log.Info().
		Int("max_open_conns", cfg.MaxOpenConns).
		Int("max_idle_conns", cfg.MaxIdleConns).
		Dur("conn_max_lifetime", cfg.ConnMaxLifetime).
		Msg("connected to PostgreSQL")
	return db, nil
}
