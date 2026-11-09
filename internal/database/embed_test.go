package database

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationsFS_ReadDir(t *testing.T) {
	tests := []struct {
		name      string
		dir       string
		wantError bool
	}{
		{
			name:      "read valid migrations directory",
			dir:       "migrations",
			wantError: false,
		},
		{
			name:      "read nonexistent directory",
			dir:       "nonexistent",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := MigrationsFS.ReadDir(tt.dir)
			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, entries)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, entries)

			hasUpMigration := false
			hasDownMigration := false

			for _, entry := range entries {
				assert.False(t, entry.IsDir(), "migration entry should be a file, not a directory")
				assert.True(t, strings.HasSuffix(entry.Name(), ".sql"), "migration file should have .sql extension")

				if strings.HasSuffix(entry.Name(), ".up.sql") {
					hasUpMigration = true
				}
				if strings.HasSuffix(entry.Name(), ".down.sql") {
					hasDownMigration = true
				}
			}

			assert.True(t, hasUpMigration, "migrations FS should contain at least one .up.sql file")
			assert.True(t, hasDownMigration, "migrations FS should contain at least one .down.sql file")
		})
	}
}

func TestMigrationsFS_ReadFile(t *testing.T) {
	tests := []struct {
		name      string
		filePath  string
		wantError bool
	}{
		{
			name:      "read valid migration 001 up",
			filePath:  "migrations/001_create_users.up.sql",
			wantError: false,
		},
		{
			name:      "read valid migration 001 down",
			filePath:  "migrations/001_create_users.down.sql",
			wantError: false,
		},
		{
			name:      "read nonexistent migration file",
			filePath:  "migrations/999_nonexistent.sql",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := MigrationsFS.ReadFile(tt.filePath)
			if tt.wantError {
				assert.Error(t, err)
				assert.Empty(t, content)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, content)
		})
	}
}

func TestMigrationsFS_AllFilesReadableAndNonEmpty(t *testing.T) {
	entries, err := MigrationsFS.ReadDir("migrations")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			path := "migrations/" + entry.Name()
			content, err := MigrationsFS.ReadFile(path)
			require.NoError(t, err, "failed to read %s", path)
			assert.NotEmpty(t, content, "file %s should not be empty", path)
		})
	}
}

func TestMigrationsFS_NotificationAndAuditCoveringIndexes(t *testing.T) {
	upBytes, err := MigrationsFS.ReadFile("migrations/052_add_notification_and_audit_covering_indexes.up.sql")
	require.NoError(t, err)
	upSQL := string(upBytes)

	downBytes, err := MigrationsFS.ReadFile("migrations/052_add_notification_and_audit_covering_indexes.down.sql")
	require.NoError(t, err)
	downSQL := string(downBytes)

	expectedIndexes := []string{
		"idx_notifications_user_created_covering",
		"idx_notifications_user_unread_created_covering",
		"idx_notifications_user_search_covering",
		"idx_audit_log_created_id_covering",
		"idx_audit_log_actor_created_covering",
		"idx_audit_log_resource_type_created_covering",
		"idx_audit_log_action_created_covering",
	}

	for _, idx := range expectedIndexes {
		assert.Contains(t, upSQL, "CREATE INDEX IF NOT EXISTS "+idx, "up migration must create %s", idx)
		assert.Contains(t, downSQL, "DROP INDEX IF EXISTS "+idx, "down migration must drop %s", idx)
	}
	assert.Contains(t, upSQL, "INCLUDE (")
}
