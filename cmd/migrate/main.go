package main

import (
	"database/sql"
	"flag"

	_ "github.com/lib/pq"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/logger"
	"github.com/rs/zerolog/log"
)

func main() {
	direction := flag.String("direction", "up", "Migration direction: up or down")
	to := flag.String("to", "", "Target migration version, e.g. 042_create_swap_offers (or legacy prefix 042 when unambiguous).\n"+
		"With -direction up: apply pending migrations through the target.\n"+
		"With -direction down: revert applied migrations above the target (target stays applied).")
	count := flag.Int("count", 0, "Limit how many migrations to apply (up) or revert (down).\n"+
		"Defaults: apply all pending on up, revert all on down.")
	flag.Parse()

	opts := Options{Direction: *direction, To: *to, Count: *count}
	if err := run(opts, "."); err != nil {
		// Entrypoint handles startup failure gracefully
		logger.Init("info", "json")
		log.Fatal().Err(err).Msg("migration execution failed")
	}
}

func run(opts Options, configPath string) error {
	if err := opts.Validate(); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)

	db, err := sql.Open("postgres", cfg.Database.URL)
	if err != nil {
		return err
	}
	defer logClose(db, "database connection")

	if err := Run(db, opts); err != nil {
		return err
	}
	return nil
}
