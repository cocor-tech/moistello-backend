package database

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadMigrations_EmbeddedSetIsValid guards the real migration set: every file
// follows the naming convention, versions are unique, and each up file has a
// matching down file. A violation here would otherwise surface as a migration
// that is silently skipped at deploy time.
func TestLoadMigrations_EmbeddedSetIsValid(t *testing.T) {
	migrations, err := LoadMigrations()
	require.NoError(t, err)
	require.NotEmpty(t, migrations)

	seen := make(map[string]string, len(migrations))
	for _, m := range migrations {
		assert.NotEmpty(t, m.UpPath, "migration %s has no up file", m.Version)
		assert.NotEmpty(t, m.DownPath, "migration %s has no down file", m.Version)

		if previous, duplicate := seen[m.Version]; duplicate {
			t.Errorf("version %s is shared by %q and %q", m.Version, previous, m.Name)
		}
		seen[m.Version] = m.Name
	}
}

func TestLoadMigrations_ReturnsVersionsInOrder(t *testing.T) {
	migrations, err := LoadMigrations()
	require.NoError(t, err)

	for i := 1; i < len(migrations); i++ {
		previous, err := strconv.Atoi(migrations[i-1].Version)
		require.NoError(t, err)
		current, err := strconv.Atoi(migrations[i].Version)
		require.NoError(t, err)

		assert.Less(t, previous, current, "migrations must be ordered by ascending version")
	}
}

// TestLoadMigrations_ContentIsReadable proves each discovered path resolves to a
// non-empty file in the embedded filesystem, so a rename that breaks the embed
// pattern fails the build's tests rather than the deploy.
func TestLoadMigrations_ContentIsReadable(t *testing.T) {
	migrations, err := LoadMigrations()
	require.NoError(t, err)

	for _, m := range migrations {
		for _, path := range []string{m.UpPath, m.DownPath} {
			content, err := MigrationsFS.ReadFile(path)
			require.NoError(t, err, "reading %s", path)
			assert.NotEmpty(t, content, "%s is empty", path)
		}
	}
}

func TestMigrationFilePattern(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		valid bool
	}{
		{"up file", "001_create_users.up.sql", true},
		{"down file", "030_create_job_queue.down.sql", true},
		{"missing version", "create_users.up.sql", false},
		{"missing direction", "001_create_users.sql", false},
		{"unknown direction", "001_create_users.sideways.sql", false},
		{"uppercase name", "001_Create_Users.up.sql", false},
		{"not sql", "001_create_users.up.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, migrationFilePattern.MatchString(tt.file))
		})
	}
}
