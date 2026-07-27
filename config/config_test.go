package config_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

func TestLoad_FailsWithoutMasterSecretKey(t *testing.T) {
	os.Unsetenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY")
	os.Unsetenv("STELLAR_MASTER_SECRET_KEY")
	os.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	defer os.Unsetenv("MOISTELLO_DATABASE_URL")

	cfg, err := config.Load(".")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "stellar master secret key must be set")
}

func TestLoad_SucceedsWithEnvSecretKey(t *testing.T) {
	testSecret := "SDJFKSDJFKSDJFKSDJFKSDJFKSDJFKSDJFKSDJFKSDJFKSDJFKSD"
	os.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", testSecret)
	os.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	defer os.Unsetenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY")
	defer os.Unsetenv("MOISTELLO_DATABASE_URL")

	cfg, err := config.Load(".")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, testSecret, cfg.Stellar.MasterSecretKey)
}
