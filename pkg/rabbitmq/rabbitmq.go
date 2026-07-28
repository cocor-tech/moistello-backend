package rabbitmq

import (
	"fmt"

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

func (c *Client) Close() {
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) Publish(exchange, routingKey string, body []byte) error {
	return c.ch.Publish(exchange, routingKey, false, false, amqp091.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
}

func EnsureQueue(ch *amqp091.Channel, name, exchange, routingKey string) error {
	q, err := ch.QueueDeclare(name, true, false, false, false, nil)
	if err != nil {
		return err
	}
	return ch.QueueBind(q.Name, routingKey, exchange, false, nil)
}
