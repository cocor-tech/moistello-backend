package main

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestMigrationIdempotencyAndRollback(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database migration integration tests")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping test database: %v", err)
	}

	// 1. Run migrations up
	if err := runMigrationsUp(db); err != nil {
		t.Fatalf("initial runMigrationsUp failed: %v", err)
	}

	// 2. Re-run migrations up to test idempotency
	if err := runMigrationsUp(db); err != nil {
		t.Fatalf("idempotent re-run of runMigrationsUp failed: %v", err)
	}

	// 3. Test rollback (down)
	if err := runMigrationsDown(db); err != nil {
		t.Fatalf("runMigrationsDown failed: %v", err)
	}

	// 4. Test concurrent locking
	t.Run("ConcurrentMigrationLock", func(t *testing.T) {
		done := make(chan error, 2)
		go func() {
			db1, err := sql.Open("postgres", dbURL)
			if err != nil {
				done <- err
				return
			}
			defer db1.Close()
			done <- runMigrationsUp(db1)
		}()
		go func() {
			db2, err := sql.Open("postgres", dbURL)
			if err != nil {
				done <- err
				return
			}
			defer db2.Close()
			done <- runMigrationsUp(db2)
		}()

		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Errorf("concurrent migration execution failed: %v", err)
			}
		}
	})
}
