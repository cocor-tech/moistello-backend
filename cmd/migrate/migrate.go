package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/moistello/backend/internal/database"
	"github.com/rs/zerolog/log"
)

// Direction values for Options.Direction.
const (
	DirectionUp   = "up"
	DirectionDown = "down"
)

// migrationLockKey is the arbitrary fixed key for the advisory lock that
// serializes migration runs.
const migrationLockKey = 718239102

// Options controls a migration run.
type Options struct {
	// Direction is DirectionUp to apply migrations or DirectionDown to revert them.
	Direction string

	// To targets a specific migration version, inclusively. For "up", pending
	// migrations are applied until the target is reached. For "down", applied
	// migrations strictly above the target are reverted; the target itself
	// remains applied. Mutually exclusive with Count.
	To string

	// Count limits how many migrations are applied ("up") or reverted
	// ("down"). When zero: "up" applies every pending migration and "down"
	// reverts every applied migration. Mutually exclusive with To.
	Count int
}

// Validate rejects impossible option combinations.
func (o Options) Validate() error {
	if o.Direction != DirectionUp && o.Direction != DirectionDown {
		return fmt.Errorf("unknown direction %q: use 'up' or 'down'", o.Direction)
	}
	if o.To != "" && o.Count != 0 {
		return fmt.Errorf("--to and --count are mutually exclusive")
	}
	if o.Count < 0 {
		return fmt.Errorf("--count must be positive, got %d", o.Count)
	}
	return nil
}

// barePrefixRe matches legacy schema_migrations rows recorded before
// versions became full file stems (e.g. "035").
var barePrefixRe = regexp.MustCompile(`^\d+$`)

// versionFromPath extracts the migration version from an embedded path,
// e.g. "internal/database/migrations/035_create_governance.up.sql" ->
// "035_create_governance".
//
// The full filename stem is used as the identity of a migration so that
// migrations sharing a numeric prefix (016_email_verifications vs
// 016_widen_wallet_address, 035_create_governance vs 042_create_swap_offers)
// remain distinct, trackable versions instead of colliding inside
// schema_migrations.
func versionFromPath(path string) string {
	base := filepath.Base(path)
	if i := strings.Index(base, "."); i >= 0 {
		return base[:i]
	}
	return base
}

// prefixOf returns the numeric portion of a version, e.g.
// "016_email_verifications" -> "016".
func prefixOf(version string) string {
	if i := strings.Index(version, "_"); i >= 0 {
		return version[:i]
	}
	return version
}

// listMigrationFiles returns the embedded .sql files for the given direction,
// sorted lexicographically which is the execution order for "up".
func listMigrationFiles(direction string) ([]string, error) {
	suffix := fmt.Sprintf(".%s.sql", direction)
	var files []string

	err := fs.WalkDir(database.MigrationsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(filepath.Base(path), suffix) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking migrations: %w", err)
	}

	sort.Strings(files)
	return files, nil
}

// resolveTargetIndex resolves opts.To against the ordered file list and
// returns the index of the targeted migration. Exact version (full stem) and
// bare numeric prefix forms are accepted; ambiguous prefixes are rejected.
func resolveTargetIndex(to string, files []string) (int, error) {
	for i, f := range files {
		if versionFromPath(f) == to {
			return i, nil
		}
	}

	if barePrefixRe.MatchString(to) {
		first, found := -1, false
		for i, f := range files {
			if prefixOf(versionFromPath(f)) == to {
				if !found {
					first, found = i, true
				} else {
					return -1, fmt.Errorf(
						"ambiguous migration version %q: matches %s and %s; use the full version name",
						to, versionFromPath(files[first]), versionFromPath(f),
					)
				}
			}
		}
		if found {
			return first, nil
		}
	}

	return -1, fmt.Errorf("unknown migration version %q", to)
}

// loadAppliedVersions reads the raw version column of schema_migrations.
func loadAppliedVersions(ctx context.Context, conn *sql.Conn) (applied map[string]bool, err error) {
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close schema_migrations rows")
		}
	}()

	applied = make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

// expandLegacyRows maps legacy bare-prefix rows (e.g. "016") onto the full
// versions of every migration carrying that prefix. Databases migrated with
// older runners cannot distinguish same-prefix migrations, so they are all
// treated as applied rather than re-running DDL that may already exist.
func expandLegacyRows(raw map[string]bool, files []string) map[string]bool {
	applied := make(map[string]bool, len(raw))
	for v := range raw {
		applied[v] = true
	}

	stems := make([]string, 0, 4)
	for v := range raw {
		if !barePrefixRe.MatchString(v) {
			continue
		}
		stems = stems[:0]
		for _, f := range files {
			sv := versionFromPath(f)
			if prefixOf(sv) == v {
				stems = append(stems, sv)
			}
		}
		for _, sv := range stems {
			applied[sv] = true
		}
		if len(stems) > 1 {
			log.Warn().Strs("versions", stems).Msgf("legacy migration record %q covers multiple files; treating all as applied", v)
		}
	}
	return applied
}

// selectUpFiles returns the ordered pending migrations to apply.
func selectUpFiles(files []string, applied map[string]bool, opts Options) ([]string, error) {
	targetIdx := -1
	if opts.To != "" {
		idx, err := resolveTargetIndex(opts.To, files)
		if err != nil {
			return nil, err
		}
		targetIdx = idx
	}

	var selected []string
	for i, f := range files {
		version := versionFromPath(f)
		if applied[version] {
			continue
		}
		if targetIdx >= 0 && i > targetIdx {
			break
		}
		selected = append(selected, f)
		if opts.Count != 0 && len(selected) == opts.Count {
			break
		}
	}
	return selected, nil
}

// selectDownFiles returns the ordered migrations to revert, newest first.
// orderedFiles is the canonical "up" order used for --to resolution; the
// returned files are drawn from downFiles (a few migrations have no .down
// counterpart and are simply never selected).
func selectDownFiles(downFiles, orderedFiles []string, applied map[string]bool, opts Options) ([]string, error) {
	targetIdx := -1
	if opts.To != "" {
		idx, err := resolveTargetIndex(opts.To, orderedFiles)
		if err != nil {
			return nil, err
		}
		targetIdx = idx
	}

	limit := opts.Count

	order := make(map[string]int, len(orderedFiles))
	for i, f := range orderedFiles {
		order[versionFromPath(f)] = i
	}

	var selected []string
	for i := len(downFiles) - 1; i >= 0; i-- {
		f := downFiles[i]
		version := versionFromPath(f)
		if targetIdx >= 0 && order[version] <= targetIdx {
			break // the target itself stays applied
		}
		if !applied[version] {
			continue
		}
		selected = append(selected, f)
		if limit != 0 && len(selected) == limit {
			break
		}
	}
	return selected, nil
}

// ensureMigrationsTable creates the schema_migrations tracking table if it
// does not exist and acquires the session-level advisory lock on conn. The
// whole run must use this single connection so the lock can be released
// deterministically (a pool would hand out different sessions).
func ensureMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey)
	return err
}

// releaseAdvisoryLock releases the session-level migration lock. It uses an
// independent deadline so it still runs when the caller's context failed.
func releaseAdvisoryLock(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
		log.Warn().Err(err).Msg("releasing migration advisory lock")
	}
}

func recordApplied(tx *sql.Tx, version string) error {
	prefix := prefixOf(version)
	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING", version); err != nil {
		return err
	}
	if prefix == version {
		return nil
	}
	// Clear any legacy bare-prefix row covering this migration.
	_, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", prefix)
	return err
}

func removeApplied(tx *sql.Tx, version string) error {
	if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", version); err != nil {
		return err
	}
	prefix := prefixOf(version)
	if prefix == version {
		return nil
	}
	_, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", prefix)
	return err
}

func executeMigration(ctx context.Context, conn *sql.Conn, filePath, content, direction string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction for %s: %w", filePath, err)
	}

	if _, err := tx.Exec(content); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("executing %s migration %s: %w", direction, filePath, err)
	}

	version := versionFromPath(filePath)
	bookkeep := recordApplied
	if direction == DirectionDown {
		bookkeep = removeApplied
	}
	if err := bookkeep(tx, version); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("updating schema_migrations for %s: %w", filePath, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %s: %w", filePath, err)
	}
	return nil
}

func readMigrations(ctx context.Context, conn *sql.Conn, selected []string, direction string) error {
	for _, f := range selected {
		content, err := fs.ReadFile(database.MigrationsFS, f)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", f, err)
		}
		if err := executeMigration(ctx, conn, f, string(content), direction); err != nil {
			return err
		}
		action := "applied migration"
		if direction == DirectionDown {
			action = "reverted migration"
		}
		log.Info().Str("version", versionFromPath(f)).Msg(action)
	}
	return nil
}

func runMigrationsUp(ctx context.Context, conn *sql.Conn, opts Options) error {
	files, err := listMigrationFiles(DirectionUp)
	if err != nil {
		return err
	}

	raw, err := loadAppliedVersions(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading applied versions: %w", err)
	}
	applied := expandLegacyRows(raw, files)

	selected, err := selectUpFiles(files, applied, opts)
	if err != nil {
		return err
	}

	if opts.To != "" && len(selected) == 0 {
		log.Info().Str("target", opts.To).Msg("target version already applied, nothing to do")
	}

	if err := readMigrations(ctx, conn, selected, DirectionUp); err != nil {
		return err
	}

	if len(selected) == 0 {
		log.Info().Msg("no new migrations to apply")
	} else {
		log.Info().Int("count", len(selected)).Msg("migrations applied successfully")
	}
	return nil
}

func runMigrationsDown(ctx context.Context, conn *sql.Conn, opts Options) error {
	downFiles, err := listMigrationFiles(DirectionDown)
	if err != nil {
		return err
	}
	// Version ordering comes from the up file set; stems match between
	// directions.
	upFiles, err := listMigrationFiles(DirectionUp)
	if err != nil {
		return err
	}

	raw, err := loadAppliedVersions(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading applied versions: %w", err)
	}
	applied := expandLegacyRows(raw, upFiles)

	selected, err := selectDownFiles(downFiles, upFiles, applied, opts)
	if err != nil {
		return err
	}

	if err := readMigrations(ctx, conn, selected, DirectionDown); err != nil {
		return err
	}

	if len(selected) == 0 {
		log.Info().Msg("no migrations to revert")
	}
	return nil
}

// Run executes a migration run described by opts. All work happens on a single
// pinned connection so the advisory lock is held and released by the same
// database session.
func Run(db *sql.DB, opts Options) (err error) {
	if err := opts.Validate(); err != nil {
		return err
	}

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring dedicated migration connection: %w", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close dedicated migration connection")
		}
	}()

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return fmt.Errorf("ensuring migrations table: %w", err)
	}
	defer releaseAdvisoryLock(conn)

	switch opts.Direction {
	case DirectionUp:
		return runMigrationsUp(ctx, conn, opts)
	case DirectionDown:
		return runMigrationsDown(ctx, conn, opts)
	default:
		return fmt.Errorf("unknown direction %q: use 'up' or 'down'", opts.Direction)
	}
}

// closeInto closes c and joins any close error into *errp, so a failed close
// surfaces from the caller instead of being silently dropped.
func closeInto(c io.Closer, what string, errp *error) {
	if cerr := c.Close(); cerr != nil {
		*errp = errors.Join(*errp, fmt.Errorf("closing %s: %w", what, cerr))
	}
}

// logClose closes c and logs any close error. It is for deferred cleanup in
// main, where there is no caller to return the error to.
func logClose(c io.Closer, what string) {
	if err := c.Close(); err != nil {
		log.Error().Err(err).Str("resource", what).Msg("failed to close")
	}
}
