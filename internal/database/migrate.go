package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
)

// migrationsDir is the directory inside MigrationsFS holding the .sql files.
const migrationsDir = "migrations"

// migrationLockID is a fixed key for the Postgres session-level advisory lock
// held while migrations run. Multiple API replicas starting at once therefore
// serialise: one applies the pending migrations while the others wait and then
// observe there is nothing left to do.
const migrationLockID int64 = 4823570119

// migrationFilePattern matches the required filename convention,
// e.g. "007_create_invites.up.sql".
var migrationFilePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.(up|down)\.sql$`)

// Migration is a single versioned schema change: a paired up/down SQL file.
type Migration struct {
	Version  string
	Name     string
	UpPath   string
	DownPath string
}

// Status reports whether a migration has been applied to a given database.
type Status struct {
	Migration
	Applied   bool
	AppliedAt time.Time
}

// LoadMigrations returns every embedded migration ordered by version.
//
// It rejects a migration set that cannot be applied deterministically —
// filenames that do not follow the convention, two migrations sharing a version,
// or an up file without its matching down file. Catching these at load time
// means a broken set fails loudly instead of being silently skipped.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(MigrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	byVersion := make(map[string]*Migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		match := migrationFilePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration %q does not match the required <version>_<name>.<up|down>.sql convention", name)
		}
		version, title, direction := match[1], match[2], match[3]

		m, ok := byVersion[version]
		if !ok {
			m = &Migration{Version: version, Name: title}
			byVersion[version] = m
		}
		if m.Name != title {
			return nil, fmt.Errorf("migration version %s is used by both %q and %q: versions must be unique", version, m.Name, title)
		}

		path := migrationsDir + "/" + name
		switch direction {
		case "up":
			if m.UpPath != "" {
				return nil, fmt.Errorf("migration version %s has more than one up file", version)
			}
			m.UpPath = path
		case "down":
			if m.DownPath != "" {
				return nil, fmt.Errorf("migration version %s has more than one down file", version)
			}
			m.DownPath = path
		}
	}

	migrations := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.UpPath == "" {
			return nil, fmt.Errorf("migration %s_%s has a down file but no up file", m.Version, m.Name)
		}
		if m.DownPath == "" {
			return nil, fmt.Errorf("migration %s_%s has no down file: every migration must be reversible", m.Version, m.Name)
		}
		migrations = append(migrations, *m)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// EnsureSchemaMigrationsTable creates the version-tracking table if absent.
func EnsureSchemaMigrationsTable(ctx context.Context, db execer) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("ensuring schema_migrations table: %w", err)
	}
	return nil
}

// AppliedVersions returns the versions recorded in schema_migrations, keyed by
// version and valued by the time they were applied.
func AppliedVersions(ctx context.Context, db querier) (map[string]time.Time, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, applied_at FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]time.Time)
	for rows.Next() {
		var (
			version   string
			appliedAt time.Time
		)
		if err := rows.Scan(&version, &appliedAt); err != nil {
			return nil, fmt.Errorf("scanning applied migration: %w", err)
		}
		applied[version] = appliedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating applied migrations: %w", err)
	}
	return applied, nil
}

// Statuses reports every known migration alongside whether it has been applied.
func Statuses(ctx context.Context, db *sql.DB) ([]Status, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	if err := EnsureSchemaMigrationsTable(ctx, db); err != nil {
		return nil, err
	}
	applied, err := AppliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	statuses := make([]Status, 0, len(migrations))
	for _, m := range migrations {
		appliedAt, ok := applied[m.Version]
		statuses = append(statuses, Status{Migration: m, Applied: ok, AppliedAt: appliedAt})
	}
	return statuses, nil
}

// PendingVersions returns the versions that exist on disk but have not been
// applied to the database. Callers that do not want to migrate automatically can
// use it to warn that the schema is behind the code.
func PendingVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	statuses, err := Statuses(ctx, db)
	if err != nil {
		return nil, err
	}

	var pending []string
	for _, s := range statuses {
		if !s.Applied {
			pending = append(pending, s.Version+"_"+s.Name)
		}
	}
	return pending, nil
}

// Up applies every migration that has not yet been recorded, oldest first, and
// returns how many it applied. Each migration and its schema_migrations record
// share a transaction, so a failure leaves neither behind.
func Up(ctx context.Context, db *sql.DB) (int, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return 0, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Close()

	release, err := lockMigrations(ctx, conn)
	if err != nil {
		return 0, err
	}
	defer release()

	if err := EnsureSchemaMigrationsTable(ctx, conn); err != nil {
		return 0, err
	}
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			log.Debug().Str("version", m.Version).Msg("migration already applied, skipping")
			continue
		}

		record := "INSERT INTO schema_migrations (version) VALUES ($1)"
		if err := runInTx(ctx, conn, m, m.UpPath, record); err != nil {
			return count, err
		}

		log.Info().Str("version", m.Version).Str("name", m.Name).Msg("applied migration")
		count++
	}
	return count, nil
}

// Down rolls back the most recently applied migrations, newest first, and
// returns how many it reverted. steps of 1 reverts a single migration; a value
// below 1 is treated as 1 so an accidental zero cannot unwind the schema.
func Down(ctx context.Context, db *sql.DB, steps int) (int, error) {
	if steps < 1 {
		steps = 1
	}

	migrations, err := LoadMigrations()
	if err != nil {
		return 0, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Close()

	release, err := lockMigrations(ctx, conn)
	if err != nil {
		return 0, err
	}
	defer release()

	if err := EnsureSchemaMigrationsTable(ctx, conn); err != nil {
		return 0, err
	}
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return 0, err
	}

	count := 0
	for i := len(migrations) - 1; i >= 0 && count < steps; i-- {
		m := migrations[i]
		if _, ok := applied[m.Version]; !ok {
			continue
		}

		record := "DELETE FROM schema_migrations WHERE version = $1"
		if err := runInTx(ctx, conn, m, m.DownPath, record); err != nil {
			return count, err
		}

		log.Info().Str("version", m.Version).Str("name", m.Name).Msg("reverted migration")
		count++
	}
	return count, nil
}

// runInTx executes one migration file and its schema_migrations bookkeeping in a
// single transaction, so the recorded state can never drift from the schema.
func runInTx(ctx context.Context, conn *sql.Conn, m Migration, sqlPath, recordStmt string) error {
	content, err := fs.ReadFile(MigrationsFS, sqlPath)
	if err != nil {
		return fmt.Errorf("reading migration %s: %w", sqlPath, err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction for %s: %w", sqlPath, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("executing %s: %w", sqlPath, err)
	}
	if _, err := tx.ExecContext(ctx, recordStmt, m.Version); err != nil {
		return fmt.Errorf("recording migration %s: %w", m.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s: %w", sqlPath, err)
	}
	return nil
}

// lockMigrations takes the session-level advisory lock and returns a function
// that releases it. It blocks until the lock is free.
func lockMigrations(ctx context.Context, conn *sql.Conn) (func(), error) {
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, fmt.Errorf("acquiring migration advisory lock: %w", err)
	}
	return func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			log.Warn().Err(err).Msg("failed to release migration advisory lock")
		}
	}, nil
}

// execer and querier let the helpers above run against a *sql.DB, a *sql.Conn or
// a *sql.Tx interchangeably.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
