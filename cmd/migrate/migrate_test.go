package main

import (
	"errors"
	"testing"
)

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"up all", Options{Direction: DirectionUp}, false},
		{"down one", Options{Direction: DirectionDown}, false},
		{"bad direction", Options{Direction: "sideways"}, true},
		{"to with up", Options{Direction: DirectionUp, To: "042_create_swap_offers"}, false},
		{"count with down", Options{Direction: DirectionDown, Count: 3}, false},
		{"to and count", Options{Direction: DirectionUp, To: "001_create_users", Count: 2}, true},
		{"negative count", Options{Direction: DirectionUp, Count: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestVersionFromPath(t *testing.T) {
	cases := map[string]string{
		"internal/database/migrations/001_create_users.up.sql":      "001_create_users",
		"internal/database/migrations/035_create_governance.up.sql": "035_create_governance",
		"internal/database/migrations/026_session_ttl.down.sql":     "026_session_ttl",
		"weird-no-extension": "weird-no-extension",
	}
	for path, want := range cases {
		if got := versionFromPath(path); got != want {
			t.Errorf("versionFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestListMigrationFilesEmbedded(t *testing.T) {
	up, err := listMigrationFiles(DirectionUp)
	if err != nil {
		t.Fatalf("listMigrationFiles(up): %v", err)
	}
	down, err := listMigrationFiles(DirectionDown)
	if err != nil {
		t.Fatalf("listMigrationFiles(down): %v", err)
	}

	if len(up) == 0 || len(down) == 0 {
		t.Fatalf("expected embedded migrations, got up=%d down=%d", len(up), len(down))
	}

	// Sorted order is execution order.
	for i := 1; i < len(up); i++ {
		if up[i-1] >= up[i] {
			t.Fatalf("up files not sorted at index %d: %s >= %s", i, up[i-1], up[i])
		}
	}

	// Duplicate numeric prefixes must remain distinct versions.
	stems := make(map[string]bool)
	prefixCount := map[string]int{}
	for _, f := range up {
		v := versionFromPath(f)
		if stems[v] {
			t.Errorf("duplicate migration version %q", v)
		}
		stems[v] = true
		prefixCount[prefixOf(v)]++
	}
	for prefix, n := range prefixCount {
		if n > 1 && barePrefixRe.MatchString(prefix) {
			t.Logf("note: numeric prefix %s shared by %d migrations (must stay distinct)", prefix, n)
		}
	}
	if prefixCount["016"] < 2 || prefixCount["031"] < 2 {
		t.Fatalf("expected duplicate prefixes 016/031 in test fixtures, got %#v", prefixCount)
	}
}

func TestResolveTargetIndex(t *testing.T) {
	files := []string{
		"migrations/001_a.up.sql",
		"migrations/002_b.up.sql",
		"migrations/016_c.up.sql",
		"migrations/016_d.up.sql",
		"migrations/035_e.up.sql",
		"migrations/035_f.up.sql",
	}

	if idx, err := resolveTargetIndex("016_c", files); err != nil || idx != 2 {
		t.Fatalf("exact stem lookup: idx=%d err=%v", idx, err)
	}
	if idx, err := resolveTargetIndex("002", files); err != nil || idx != 1 {
		t.Fatalf("unique bare prefix lookup: idx=%d err=%v", idx, err)
	}
	if _, err := resolveTargetIndex("016", files); err == nil {
		t.Fatal("ambiguous bare prefix should fail")
	}
	if _, err := resolveTargetIndex("035", files); err == nil {
		t.Fatal("ambiguous bare prefix should fail")
	}
	if _, err := resolveTargetIndex("999_nothing", files); err == nil {
		t.Fatal("unknown version should fail")
	}
}

func TestExpandLegacyRows(t *testing.T) {
	files := []string{
		"migrations/001_a.up.sql",
		"migrations/002_b.up.sql",
		"migrations/016_c.up.sql",
		"migrations/016_d.up.sql",
	}
	raw := map[string]bool{
		"001_a": true, // modern row passes through untouched
		"001":   true, // legacy row for a unique-prefix file
		"016":   true, // legacy row covering two same-prefix files
	}
	got := expandLegacyRows(raw, files)

	for _, v := range []string{"001_a", "001", "016", "016_c", "016_d"} {
		if !got[v] {
			t.Errorf("expandLegacyRows missing %q", v)
		}
	}
	if got["002_b"] {
		t.Error("expandLegacyRows marked unapplied version as applied")
	}
}

func TestSelectUpFiles(t *testing.T) {
	files := []string{
		"migrations/001_a.up.sql",
		"migrations/002_b.up.sql",
		"migrations/003_c.up.sql",
		"migrations/004_d.up.sql",
	}
	allApplied := map[string]bool{"001_a": true, "002_b": true, "003_c": true, "004_d": true}
	noneApplied := map[string]bool{}

	t.Run("all pending when nothing applied", func(t *testing.T) {
		got, err := selectUpFiles(files, noneApplied, Options{Direction: DirectionUp})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 {
			t.Fatalf("want all 4 pending, got %v", got)
		}
	})

	t.Run("idempotent when everything applied", func(t *testing.T) {
		got, err := selectUpFiles(files, allApplied, Options{Direction: DirectionUp})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want no pending, got %v", got)
		}
	})

	t.Run("skips already applied", func(t *testing.T) {
		applied := map[string]bool{"001_a": true, "002_b": true}
		got, err := selectUpFiles(files, applied, Options{Direction: DirectionUp})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"migrations/003_c.up.sql", "migrations/004_d.up.sql"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("--to is inclusive", func(t *testing.T) {
		got, err := selectUpFiles(files, noneApplied, Options{Direction: DirectionUp, To: "002_b"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("--to should apply through target, got %v", got)
		}
	})

	t.Run("--to on applied target is a no-op", func(t *testing.T) {
		applied := map[string]bool{"001_a": true, "002_b": true}
		got, err := selectUpFiles(files, applied, Options{Direction: DirectionUp, To: "002_b"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("target already applied, want no-op, got %v", got)
		}
	})

	t.Run("--count limits applications", func(t *testing.T) {
		got, err := selectUpFiles(files, noneApplied, Options{Direction: DirectionUp, Count: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[1] != "migrations/002_b.up.sql" {
			t.Fatalf("count=2 should stop after second pending, got %v", got)
		}
	})
}

func TestSelectDownFiles(t *testing.T) {
	files := []string{
		"migrations/001_a.down.sql",
		"migrations/002_b.down.sql",
		"migrations/003_c.down.sql",
		"migrations/004_d.down.sql",
	}
	order := []string{
		"migrations/001_a.up.sql",
		"migrations/002_b.up.sql",
		"migrations/003_c.up.sql",
		"migrations/004_d.up.sql",
	}
	allApplied := map[string]bool{"001_a": true, "002_b": true, "003_c": true, "004_d": true}

	t.Run("default reverts all applied, newest first", func(t *testing.T) {
		got, err := selectDownFiles(files, order, allApplied, Options{Direction: DirectionDown})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 || got[0] != "migrations/004_d.down.sql" {
			t.Fatalf("default down should revert all applied, got %v", got)
		}
	})

	t.Run("--count N reverts N steps", func(t *testing.T) {
		got, err := selectDownFiles(files, order, allApplied, Options{Direction: DirectionDown, Count: 3})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"migrations/004_d.down.sql", "migrations/003_c.down.sql", "migrations/002_b.down.sql"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("--to keeps the target applied", func(t *testing.T) {
		got, err := selectDownFiles(files, order, allApplied, Options{Direction: DirectionDown, To: "002_b"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"migrations/004_d.down.sql", "migrations/003_c.down.sql"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("--to top target is a no-op", func(t *testing.T) {
		got, err := selectDownFiles(files, order, allApplied, Options{Direction: DirectionDown, To: "004_d"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("down --to newest should be no-op, got %v", got)
		}
	})

	t.Run("only applied migrations are reverted", func(t *testing.T) {
		partial := map[string]bool{"001_a": true, "002_b": true}
		got, err := selectDownFiles(files, order, partial, Options{Direction: DirectionDown, Count: 5})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"migrations/002_b.down.sql", "migrations/001_a.down.sql"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})
}

type fakeCloser struct {
	err    error
	closed int
}

func (f *fakeCloser) Close() error {
	f.closed++
	return f.err
}

func TestCloseInto_SuccessLeavesErrUnchanged(t *testing.T) {
	c := &fakeCloser{}
	var err error
	closeInto(c, "database", &err)
	if c.closed != 1 {
		t.Fatalf("Close called %d times, want 1", c.closed)
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestCloseInto_SuccessKeepsExistingErr(t *testing.T) {
	orig := errors.New("query failed")
	err := orig
	closeInto(&fakeCloser{}, "database", &err)
	if err != orig {
		t.Fatalf("err = %v, want original error unchanged", err)
	}
}

func TestCloseInto_CloseFailureIsReported(t *testing.T) {
	closeErr := errors.New("connection reset")
	var err error
	closeInto(&fakeCloser{err: closeErr}, "database", &err)
	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want it to wrap the close error", err)
	}
	if got, want := err.Error(), "closing database: connection reset"; got != want {
		t.Fatalf("err = %q, want %q", got, want)
	}
}

func TestCloseInto_CloseFailureJoinsExistingErr(t *testing.T) {
	orig := errors.New("query failed")
	closeErr := errors.New("connection reset")
	err := orig
	closeInto(&fakeCloser{err: closeErr}, "database", &err)
	if !errors.Is(err, orig) {
		t.Fatalf("err = %v, want it to keep the original error", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want it to include the close error", err)
	}
}

func TestLogClose_ClosesOnSuccessAndFailure(t *testing.T) {
	ok := &fakeCloser{}
	logClose(ok, "redis")
	if ok.closed != 1 {
		t.Fatalf("Close called %d times, want 1", ok.closed)
	}

	failing := &fakeCloser{err: errors.New("boom")}
	logClose(failing, "redis")
	if failing.closed != 1 {
		t.Fatalf("Close called %d times, want 1", failing.closed)
	}
}
