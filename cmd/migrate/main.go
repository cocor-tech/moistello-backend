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
	if err := opts.Validate(); err != nil {
		log.Fatal().Err(err).Msg("invalid migration options")
	}

	cfg, err := config.Load(".")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)

	db, err := sql.Open("postgres", cfg.Database.URL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database connection")
	}
	defer logClose(db, "database connection")

	if err := Run(db, opts); err != nil {
		log.Fatal().Err(err).Msgf("migration %s failed", opts.Direction)
	}
}
