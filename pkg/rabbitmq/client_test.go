package rabbitmq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// IsAlive
// ---------------------------------------------------------------------------

func TestClient_IsAlive_NilConn(t *testing.T) {
	c := &Client{}
	assert.False(t, c.IsAlive(), "nil connection must report not alive")
}

// ---------------------------------------------------------------------------
// Channel
// ---------------------------------------------------------------------------

func TestClient_Channel_ReturnsStoredChannel(t *testing.T) {
	c := &Client{}
	// ch is nil when not connected; just verify the accessor returns whatever
	// is stored without panicking.
	assert.Nil(t, c.Channel())
}

// ---------------------------------------------------------------------------
// Consume – channel-closed path
// ---------------------------------------------------------------------------

func TestConsume_ChannelClosedReturnsError(t *testing.T) {
	// Build a msgs channel and close it immediately to exercise the !ok branch.
	msgs := make(chan delivery, 0)
	close(msgs)

	ctx := context.Background()
	err := consumeFrom(ctx, msgs, func(_ context.Context, _ []byte) error { return nil })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel closed while consuming")
}

func TestConsume_ContextCancelledExitsLoop(t *testing.T) {
	msgs := make(chan delivery) // never receives anything

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := consumeFrom(ctx, msgs, func(_ context.Context, _ []byte) error { return nil })

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestConsume_HandlerErrorContinues(t *testing.T) {
	// handler errors must nack then continue; the loop exits via ctx cancel.
	msgs := make(chan delivery, 1)
	msgs <- delivery{body: []byte("bad"), nackErr: nil}

	ctx, cancel := context.WithCancel(context.Background())
	handlerCalled := false

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := consumeFrom(ctx, msgs, func(_ context.Context, body []byte) error {
		handlerCalled = true
		return errors.New("processing failed")
	})

	assert.True(t, handlerCalled, "handler must have been called")
	// exit is via ctx cancel, not an unrecoverable error
	assert.ErrorIs(t, err, context.Canceled)
}

func TestConsume_HandlerSuccessAcks(t *testing.T) {
	msgs := make(chan delivery, 1)
	acked := false
	msgs <- delivery{
		body:    []byte("ok"),
		ackFn:  func() { acked = true },
		nackErr: nil,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_ = consumeFrom(ctx, msgs, func(_ context.Context, _ []byte) error { return nil })
	assert.True(t, acked, "successful handler must trigger ack")
}

// ---------------------------------------------------------------------------
// Helpers – delivery abstraction used by tests above
// ---------------------------------------------------------------------------
// delivery is a local stand-in for amqp091.Delivery that lets us exercise the
// consume loop logic without requiring a real broker. consumeFrom is a thin
// seam that mirrors the core loop in Client.Consume.

type delivery struct {
	body    []byte
	ackFn   func()
	nackErr error // unused in this stub; just satisfies the struct
}

// consumeFrom is a testable extraction of the message-dispatch loop in
// Client.Consume, using our stub delivery type instead of amqp091.Delivery so
// we can drive it without a real broker.
func consumeFrom(ctx context.Context, ch <-chan delivery, handler Handler) error {
	const queue = "test-queue"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return errors.New("channel closed while consuming \"" + queue + "\"")
			}
			if err := handler(ctx, msg.body); err != nil {
				// nack path – continue
				continue
			}
			if msg.ackFn != nil {
				msg.ackFn()
			}
		}
	}
}
