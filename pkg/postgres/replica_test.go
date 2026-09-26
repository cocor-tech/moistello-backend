package postgres

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

func newMockDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock")
}

func TestReader_UsesReplicaOnlyForWhitelistedQueries(t *testing.T) {
	primary, replica := newMockDB(t), newMockDB(t)
	r := NewReader(primary, replica)

	assert.Same(t, replica, r.For(QueryAdminMetrics))
	assert.Same(t, replica, r.For(QueryAdminDailyVolume))
	assert.Same(t, primary, r.For(ReadQuery("users.find_by_id")), "non-whitelisted queries stay on the primary")
}

func TestReader_NoReplicaConfigured_UsesPrimary(t *testing.T) {
	primary := newMockDB(t)
	r := NewReader(primary, nil)

	assert.Same(t, primary, r.For(QueryAdminMetrics))
	assert.Same(t, primary, r.For(QueryAdminDailyVolume))
}

func TestNewReplica_EmptyURL_ReturnsNil(t *testing.T) {
	assert.Nil(t, NewReplica(config.DatabaseConfig{}))
}
