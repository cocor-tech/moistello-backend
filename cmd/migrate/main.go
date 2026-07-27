// Command migrate applies, rolls back and reports on the SQL migrations
// embedded in internal/database/migrations.
//
//	go run ./cmd/migrate --direction up
//	go run ./cmd/migrate --direction down --steps 1
//	go run ./cmd/migrate --direction status
package main

import (
	"context"
	"database/sql"
	"flag"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/database"
	"github.com/moistello/backend/pkg/logger"
	"github.com/rs/zerolog/log"
)

// migrateTimeout bounds a migration run so a lock held by another process
// cannot block the command indefinitely.
const migrateTimeout = 5 * time.Minute

func main() {
	direction := flag.String("direction", "up", "Migration direction: up, down or status")
	steps := flag.Int("steps", 1, "Number of migrations to roll back when direction is down")
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

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}

	switch *direction {
	case "up":
		applied, err := database.Up(ctx, db)
		if err != nil {
			log.Fatal().Err(err).Msg("migration up failed")
		}
		if applied == 0 {
			log.Info().Msg("no new migrations to apply")
		} else {
			log.Info().Int("count", applied).Msg("migrations applied successfully")
		}
	case "down":
		reverted, err := database.Down(ctx, db, *steps)
		if err != nil {
			log.Fatal().Err(err).Msg("migration down failed")
		}
		if reverted == 0 {
			log.Info().Msg("no migrations to revert")
		} else {
			log.Info().Int("count", reverted).Msg("migrations reverted successfully")
		}
	case "status":
		if err := printStatus(ctx, db); err != nil {
			log.Fatal().Err(err).Msg("failed to read migration status")
		}
	default:
		log.Error().Str("direction", *direction).Msg("unknown direction: use 'up', 'down' or 'status'")
		os.Exit(1)
	}
}

func printStatus(ctx context.Context, db *sql.DB) error {
	statuses, err := database.Statuses(ctx, db)
	if err != nil {
		return err
	}

	pending := 0
	for _, s := range statuses {
		event := log.Info().Str("version", s.Version).Str("name", s.Name).Bool("applied", s.Applied)
		if s.Applied {
			event = event.Time("applied_at", s.AppliedAt)
		} else {
			pending++
		}
		event.Msg("migration")
	}
	log.Info().Int("total", len(statuses)).Int("pending", pending).Msg("migration status")
	return nil
}
