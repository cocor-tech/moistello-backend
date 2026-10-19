package rabbitmq

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCloser struct {
	err    error
	closed bool
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return f.err
}

func TestClient_Close_NilResources(t *testing.T) {
	c := &Client{}
	assert.NoError(t, c.Close())
}

func TestCloseAll_AllSucceed(t *testing.T) {
	ch, conn := &fakeCloser{}, &fakeCloser{}

	err := closeAll(namedCloser{"channel", ch}, namedCloser{"connection", conn})

	require.NoError(t, err)
	assert.True(t, ch.closed)
	assert.True(t, conn.closed)
}

func TestCloseAll_ContinuesAfterFailure(t *testing.T) {
	chErr := errors.New("channel boom")
	ch, conn := &fakeCloser{err: chErr}, &fakeCloser{}

	err := closeAll(namedCloser{"channel", ch}, namedCloser{"connection", conn})

	require.Error(t, err)
	assert.ErrorIs(t, err, chErr)
	assert.Contains(t, err.Error(), "closing rabbitmq channel")
	assert.True(t, conn.closed, "connection must still be closed when channel close fails")
}

func TestCloseAll_JoinsAllErrors(t *testing.T) {
	chErr, connErr := errors.New("channel boom"), errors.New("conn boom")

	err := closeAll(
		namedCloser{"channel", &fakeCloser{err: chErr}},
		namedCloser{"connection", &fakeCloser{err: connErr}},
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, chErr)
	assert.ErrorIs(t, err, connErr)
	assert.Contains(t, err.Error(), "closing rabbitmq connection")
}
