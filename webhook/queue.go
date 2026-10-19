package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/moistello/backend/pkg/rabbitmq"
	"github.com/rs/zerolog/log"
)

// WebhookRoutingKey is the routing key queued webhook messages are published
// with on the events exchange.
const WebhookRoutingKey = "webhook.dispatch"

// Publisher abstracts the RabbitMQ producer. *rabbitmq.Client satisfies it.
type Publisher interface {
	Publish(exchange, routingKey string, body []byte) error
}

// Message is the durable envelope for a single webhook delivery attempt set.
// It carries everything the queue consumer needs to POST the payload.
// The signing secret is intentionally excluded from the JSON envelope and
// looked up from the database by the consumer using RegistrationID, so that
// raw secrets never travel through the message queue (gosec G117).
type Message struct {
	RegistrationID string          `json:"registrationId"`
	UserID         string          `json:"userId,omitempty"`
	TargetURL      string          `json:"targetUrl"`
	Payload        json.RawMessage `json:"payload"`
	RequestID      string          `json:"requestId,omitempty"`
	MaxRetries     int             `json:"maxRetries"`
	// Secret is populated in-memory by the consumer after a database lookup.
	// It is deliberately excluded from JSON to prevent secrets leaking into
	// the queue broker or its logs.
	Secret string `json:"-"`
}

// Deliver POSTs the message payload to the registered target with an
// HMAC-SHA256 signature header, retrying with exponential backoff. It returns
// an error only after maxRetries exhausted attempts, which lets the queue
// consumer nack the message.
func (m Message) Deliver(ctx context.Context, httpClient *http.Client) error {
	backoff := 100 * time.Millisecond

	for attempt := 1; attempt <= m.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TargetURL, bytes.NewBuffer(m.Payload))
		if err != nil {
			return fmt.Errorf("building webhook request for %s: %w", m.TargetURL, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Signature", SignWebhookPayload(m.Payload, m.Secret))
		if m.RequestID != "" {
			req.Header.Set("X-Request-ID", m.RequestID)
		}

		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if cerr := resp.Body.Close(); cerr != nil {
				log.Warn().Err(cerr).Str("targetUrl", m.TargetURL).Msg("failed to close response body")
			}
			return nil
		}
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				log.Warn().Err(cerr).Str("targetUrl", m.TargetURL).Msg("failed to close response body")
			}
		}

		if attempt < m.MaxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	return fmt.Errorf("webhook delivery to %s failed after %d attempts", m.TargetURL, m.MaxRetries)
}

// QueuedDispatcher replaces direct in-process HTTP fan-out: instead of firing
// goroutines that die with the process, it publishes one durable envelope per
// active registration so deliveries survive restarts and can be retried by a
// dedicated consumer.
type QueuedDispatcher struct {
	repo      WebhookRepository
	publisher Publisher
	exchange  string
}

func NewQueuedDispatcher(repo WebhookRepository, publisher Publisher, exchange string) *QueuedDispatcher {
	return &QueuedDispatcher{repo: repo, publisher: publisher, exchange: exchange}
}

// DispatchPayload publishes the payload once per active webhook registration.
// maxRetries is stored in the envelope and honoured by the consumer.
func (d *QueuedDispatcher) DispatchPayload(ctx context.Context, payload interface{}, requestID string, maxRetries int) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling webhook payload: %w", err)
	}

	webhooks, err := d.repo.GetActiveWebhooks(ctx)
	if err != nil {
		return fmt.Errorf("loading webhooks for dispatch: %w", err)
	}

	for _, wh := range webhooks {
		msg := Message{
			RegistrationID: wh.ID,
			UserID:         wh.UserID,
			TargetURL:      wh.TargetURL,
			Payload:        body,
			RequestID:      requestID,
			MaxRetries:     maxRetries,
			// Secret is intentionally omitted; the consumer fetches it from
			// the database by RegistrationID at delivery time.
		}
		envelope, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshalling webhook envelope %s: %w", wh.ID, err)
		}
		if err := d.publisher.Publish(d.exchange, WebhookRoutingKey, envelope); err != nil {
			return fmt.Errorf("publishing webhook envelope %s: %w", wh.ID, err)
		}
	}
	return nil
}

// StartQueueConsumer ensures the durable webhooks queue exists and consumes
// envelopes, delivering each over HTTP. Failures after MaxRetries attempts are
// nacked without requeueing so poison messages do not block the queue.
//
// repo is used to look up the signing secret by RegistrationID at delivery
// time; the secret is never stored in the queue envelope (gosec G117).
func StartQueueConsumer(ctx context.Context, client *rabbitmq.Client, exchange, queue string, repo WebhookRepository) error {
	if err := client.EnsureQueue(queue, exchange, WebhookRoutingKey); err != nil {
		return fmt.Errorf("ensuring webhooks queue: %w", err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	return client.Consume(ctx, queue, func(ctx context.Context, body []byte) error {
		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			return fmt.Errorf("decoding webhook envelope: %w", err)
		}
		if msg.MaxRetries <= 0 {
			msg.MaxRetries = 3
		}

		// Fetch the signing secret from the database so it is never stored
		// in the queue broker or its logs.
		wh, err := repo.GetByID(ctx, msg.RegistrationID)
		if err != nil {
			return fmt.Errorf("fetching webhook secret for %s: %w", msg.RegistrationID, err)
		}
		msg.Secret = wh.Secret
		if msg.Secret == "" {
			msg.Secret = wh.SecretHash
		}

		return msg.Deliver(ctx, httpClient)
	})
}
