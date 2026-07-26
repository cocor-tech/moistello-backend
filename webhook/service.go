package webhook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type WebhookRegistration struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TargetURL string    `json:"target_url"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}

type WebhookRepository interface {
	Register(ctx context.Context, wh *WebhookRegistration) error
	GetByUserID(ctx context.Context, userID string) ([]WebhookRegistration, error)
	GetActiveWebhooks(ctx context.Context) ([]WebhookRegistration, error)
}

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Register(ctx context.Context, wh *WebhookRegistration) error {
	query := `
		INSERT INTO webhooks (id, user_id, target_url, secret, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query, wh.ID, wh.UserID, wh.TargetURL, wh.Secret, time.Now())
	return err
}

func (r *PostgresRepository) GetActiveWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	query := `SELECT id, user_id, target_url, secret, created_at FROM webhooks`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WebhookRegistration
	for rows.Next() {
		var wh WebhookRegistration
		if err := rows.Scan(&wh.ID, &wh.UserID, &wh.TargetURL, &wh.Secret, &wh.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, wh)
	}
	return list, nil
}

type Dispatcher struct {
	repo       WebhookRepository
	httpClient *http.Client
}

func NewDispatcher(repo WebhookRepository) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// DispatchPayload delivers webhook payloads to active registrations with exponential backoff retries.
func (d *Dispatcher) DispatchPayload(ctx context.Context, payload interface{}, maxRetries int) error {
	webhooks, err := d.repo.GetActiveWebhooks(ctx)
	if err != nil {
		return fmt.Errorf("failed to load webhooks for dispatch: %w", err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	for _, wh := range webhooks {
		go d.deliverWithRetry(wh.TargetURL, body, maxRetries)
	}

	return nil
}

func (d *Dispatcher) deliverWithRetry(targetURL string, body []byte, maxRetries int) {
	backoff := 100 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.httpClient.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return // Delivery succeeded
		}

		if resp != nil {
			resp.Body.Close()
		}

		if attempt < maxRetries {
			time.Sleep(backoff)
			backoff *= 2 // Exponential backoff delay
		}
	}
}