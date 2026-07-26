package main

import (
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/lib/pq"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/logger"
	"github.com/rs/zerolog/log"
)

//go:embed ../../internal/database/migrations/*.sql
var migrationsFS embed.FS

func main() {
	direction := flag.String("direction", "up", "Migration direction: up or down")
	flag.Parse()

	cfg, err := config.Load(".")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)

	db, err := sql.Open("postgres", cfg.Database.URL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database connection")
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	switch *direction {
	case "up":
		if err := runMigrationsUp(db); err != nil {
			log.Fatal().Err(err).Msg("migration up failed")
		}
	case "down":
		if err := runMigrationsDown(db); err != nil {
			log.Fatal().Err(err).Msg("migration down failed")
		}
	default:
		log.Fatal().Msgf("unknown direction %q: use 'up' or 'down'", *direction)
	}
}

// ensureMigrationsTable creates the schema_migrations tracking table if it does not exist.
func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

// appliedVersions returns the set of migration versions that have already been applied.
func appliedVersions(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func runMigrationsUp(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("ensuring migrations table: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return fmt.Errorf("reading applied versions: %w", err)
	}

	files, err := listMigrationFiles("up")
	if err != nil {
		return err
	}

	applied_count := 0
	for _, f := range files {
		version := migrationVersion(f)
		if applied[version] {
			log.Debug().Str("version", version).Msg("already applied, skipping")
			continue
		}

		content, err := fs.ReadFile(migrationsFS, f)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", f, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for %s: %w", f, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("executing migration %s: %w", f, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recording migration %s: %w", f, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %s: %w", f, err)
		}

		log.Info().Str("version", version).Msg("applied migration")
		applied_count++
	}

	if applied_count == 0 {
		log.Info().Msg("no new migrations to apply")
	} else {
		log.Info().Int("count", applied_count).Msg("migrations applied successfully")
	}
	return nil
}

func runMigrationsDown(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("ensuring migrations table: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return fmt.Errorf("reading applied versions: %w", err)
	}

	files, err := listMigrationFiles("down")
	if err != nil {
		return err
	}

	// Apply down migrations in reverse order.
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}

	reverted_count := 0
	for _, f := range files {
		version := migrationVersion(f)
		if !applied[version] {
			log.Debug().Str("version", version).Msg("not applied, skipping rollback")
			continue
		}

		content, err := fs.ReadFile(migrationsFS, f)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", f, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for %s: %w", f, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("executing down migration %s: %w", f, err)
		}

		if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("removing migration record %s: %w", f, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing rollback %s: %w", f, err)
		}

		log.Info().Str("version", version).Msg("reverted migration")
		reverted_count++

		// Roll back only one migration at a time (safe default).
		break
	}

	if reverted_count == 0 {
		log.Info().Msg("no migrations to revert")
	}
	return nil
}

// listMigrationFiles returns sorted .sql files for the given direction ("up" or "down").
func listMigrationFiles(direction string) ([]string, error) {
	suffix := fmt.Sprintf(".%s.sql", direction)
	var files []string

	err := fs.WalkDir(migrationsFS, ".", func(path string, d fs.DirEntry, err error) error {
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

// migrationVersion extracts the numeric prefix from a migration filename,
// e.g. "internal/database/migrations/001_create_users.up.sql" → "001".
func migrationVersion(filePath string) string {
	base := filepath.Base(filePath)
	parts := strings.SplitN(base, "_", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return base
}
