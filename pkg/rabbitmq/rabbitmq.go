package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"io"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
)

type Client struct {
	conn *amqp091.Connection
	ch   *amqp091.Channel
}

func New(cfg config.RabbitMQConfig) (*Client, error) {
	conn, err := amqp091.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("connecting to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening channel: %w", err)
	}

	err = ch.ExchangeDeclare(cfg.Exchange, "topic", true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("declaring exchange: %w", err)
	}

	log.Info().Msg("connected to RabbitMQ")
	return &Client{conn: conn, ch: ch}, nil
}

func (c *Client) Channel() *amqp091.Channel { return c.ch }

// IsAlive reports whether the underlying AMQP connection is still open.
// It is used by health-check probes without exposing the raw connection.
func (c *Client) IsAlive() bool {
	return c.conn != nil && !c.conn.IsClosed()
}

// Close closes the channel and then the connection. Both are always
// attempted; any failures are joined into the returned error so callers can
// see every cleanup problem rather than only the first.
func (c *Client) Close() error {
	var closers []namedCloser
	if c.ch != nil {
		closers = append(closers, namedCloser{"channel", c.ch})
	}
	if c.conn != nil {
		closers = append(closers, namedCloser{"connection", c.conn})
	}
	return closeAll(closers...)
}

type namedCloser struct {
	name string
	c    io.Closer
}

// closeAll closes every closer in order, continuing past failures, and
// returns the joined errors (nil if all succeeded).
func closeAll(closers ...namedCloser) error {
	var errs []error
	for _, nc := range closers {
		if err := nc.c.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing rabbitmq %s: %w", nc.name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Client) Publish(exchange, routingKey string, body []byte) error {
	return c.ch.Publish(exchange, routingKey, false, false, amqp091.Publishing{
		ContentType:  "application/json",
		Body:         body,
		DeliveryMode: amqp091.Persistent,
	})
}

// EnsureQueue declares a durable queue on the client's channel and binds it
// to the exchange with the given routing key.
func (c *Client) EnsureQueue(name, exchange, routingKey string) error {
	return EnsureQueue(c.ch, name, exchange, routingKey)
}

// Handler processes a single consumed message. Returning a nil error acks the
// delivery; a non-nil error nacks it without requeueing (poison messages are
// dropped instead of looping forever).
type Handler func(ctx context.Context, body []byte) error

// Consume starts consuming a durable queue and blocks until ctx is cancelled
// or the AMQP channel closes. Messages are dispatched to handler one at a time.
func (c *Client) Consume(ctx context.Context, queue string, handler Handler) error {
	if err := c.ch.Qos(10, 0, false); err != nil {
		return fmt.Errorf("setting qos: %w", err)
	}

	msgs, err := c.ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consuming queue %s: %w", queue, err)
	}

	log.Info().Str("queue", queue).Msg("rabbitmq consumer started")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				log.Warn().Str("queue", queue).Msg("rabbitmq consumer channel closed")
				return fmt.Errorf("channel closed while consuming %q", queue)
			}
			if err := handler(ctx, msg.Body); err != nil {
				log.Warn().Err(err).Str("queue", queue).Msg("message handler failed; nacking")
				if nackErr := msg.Nack(false, false); nackErr != nil {
					log.Error().Err(nackErr).Str("queue", queue).Msg("failed to nack message")
				}
				continue
			}
			if ackErr := msg.Ack(false); ackErr != nil {
				log.Error().Err(ackErr).Str("queue", queue).Msg("failed to ack message")
			}
		}
	}
}

func EnsureQueue(ch *amqp091.Channel, name, exchange, routingKey string) error {
	q, err := ch.QueueDeclare(name, true, false, false, false, nil)
	if err != nil {
		return err
	}
	return ch.QueueBind(q.Name, routingKey, exchange, false, nil)
}
