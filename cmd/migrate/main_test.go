package main

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func appliedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	return n
}

func isApplied(t *testing.T, db *sql.DB, version string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = $1", version).Scan(&n); err != nil {
		t.Fatalf("querying schema_migrations: %v", err)
	}
	return n > 0
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database migration integration tests")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Logf("failed to close test database: %v", cerr)
		}
	})
	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping test database: %v", err)
	}
	return db
}

func TestMigrationIdempotencyAndRollback(t *testing.T) {
	db := openTestDB(t)

	// 1. Run migrations up
	if err := Run(db, Options{Direction: DirectionUp}); err != nil {
		t.Fatalf("initial up run failed: %v", err)
	}

	// 2. Re-run migrations up to test idempotency
	if err := Run(db, Options{Direction: DirectionUp}); err != nil {
		t.Fatalf("idempotent re-run of up failed: %v", err)
	}

	// 3. Test rollback (down)
	if err := Run(db, Options{Direction: DirectionDown}); err != nil {
		t.Fatalf("down run failed: %v", err)
	}

	// 4. Restore full state for other tests / environments
	if err := Run(db, Options{Direction: DirectionUp}); err != nil {
		t.Fatalf("restore up run failed: %v", err)
	}

	// 5. Test concurrent locking
	t.Run("ConcurrentMigrationLock", func(t *testing.T) {
		done := make(chan error, 2)
		go func() {
			db1, err := sql.Open("postgres", os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				done <- err
				return
			}
			var runErr error
			defer func() {
				if cerr := db1.Close(); cerr != nil {
					t.Logf("failed to close db1: %v", cerr)
				}
			}()
			done <- Run(db1, Options{Direction: DirectionUp})
		}()
		go func() {
			db2, err := sql.Open("postgres", os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				done <- err
				return
			}
			defer func() {
				if cerr := db2.Close(); cerr != nil {
					t.Logf("failed to close db2: %v", cerr)
				}
			}()
			done <- Run(db2, Options{Direction: DirectionUp})
		}()

		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Errorf("concurrent migration execution failed: %v", err)
			}
		}
	})
}

func TestMigrationTargeting(t *testing.T) {
	db := openTestDB(t)

	upFiles, err := listMigrationFiles(DirectionUp)
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if len(upFiles) < 5 {
		t.Fatalf("expected at least 5 migrations, got %d", len(upFiles))
	}

	// Start from a fully migrated state.
	if err := Run(db, Options{Direction: DirectionUp}); err != nil {
		t.Fatalf("up all failed: %v", err)
	}
	full := appliedCount(t, db)

	oldest := versionFromPath(upFiles[0])
	newest := versionFromPath(upFiles[len(upFiles)-1])

	// --count down reverts exactly N.
	if err := Run(db, Options{Direction: DirectionDown, Count: 2}); err != nil {
		t.Fatalf("down count=2 failed: %v", err)
	}
	if got := appliedCount(t, db); got != full-2 {
		t.Fatalf("after down count=2 want %d applied, got %d", full-2, got)
	}
	if !isApplied(t, db, oldest) || isApplied(t, db, newest) {
		t.Errorf("expected newest reverted and oldest still applied")
	}

	// --count up applies exactly N.
	if err := Run(db, Options{Direction: DirectionUp, Count: 1}); err != nil {
		t.Fatalf("up count=1 failed: %v", err)
	}
	if got := appliedCount(t, db); got != full-1 {
		t.Fatalf("after up count=1 want %d applied, got %d", full-1, got)
	}

	// --to up restores everything through the newest migration (inclusive).
	if err := Run(db, Options{Direction: DirectionUp, To: newest}); err != nil {
		t.Fatalf("up to=%s failed: %v", newest, err)
	}
	if got := appliedCount(t, db); got != full {
		t.Fatalf("after up to=newest want %d applied, got %d", full, got)
	}

	// --to down reverts strictly above the target, keeping it applied.
	target := versionFromPath(upFiles[len(upFiles)/2])
	if err := Run(db, Options{Direction: DirectionDown, To: target}); err != nil {
		t.Fatalf("down to=%s failed: %v", target, err)
	}
	if !isApplied(t, db, target) {
		t.Errorf("target %s should remain applied after down --to", target)
	}
	if isApplied(t, db, newest) && newest != target {
		t.Errorf("newest %s should have been reverted by down --to %s", newest, target)
	}

	// Legacy bare-prefix targeting resolves when unambiguous.
	if err := Run(db, Options{Direction: DirectionUp, To: "001"}); err != nil {
		t.Fatalf("up to legacy prefix 001 failed: %v", err)
	}

	// Ambiguous prefixes must be rejected outright.
	if err := Run(db, Options{Direction: DirectionUp, To: "035"}); err == nil {
		t.Error("ambiguous prefix 035 should be rejected")
	}
	if err := Run(db, Options{Direction: DirectionDown, To: "016"}); err == nil {
		t.Error("ambiguous prefix 016 should be rejected")
	}

	// Leave the database fully migrated.
	if err := Run(db, Options{Direction: DirectionUp}); err != nil {
		t.Fatalf("final up all failed: %v", err)
	}
	if got := appliedCount(t, db); got != full {
		t.Fatalf("final state want %d applied, got %d", full, got)
	}
}
