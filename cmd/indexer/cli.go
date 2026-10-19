package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
)

func openDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return db, nil
}

// closeInto closes c and joins any close error into *errp, so a failed close
// surfaces from the command instead of being silently dropped.
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
