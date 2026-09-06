package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/moistello/backend/pkg/jobqueue"
)

type WebhookRegistration struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	TargetURL string `json:"target_url"`
	// Secret is kept in-memory only and not returned in API responses or persisted.
	Secret string `json:"-"`
	// SecretHash stores the SHA256 hex of the secret and is persisted.
	SecretHash string    `json:"secret_hash"`
	Events     []string  `json:"events"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
}

type DeliveryLog struct {
	ID         string    `json:"id"`
	WebhookID  string    `json:"webhook_id"`
	StatusCode int       `json:"status_code"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

type WebhookRepository interface {
	Register(ctx context.Context, wh *WebhookRegistration) error
	GetByUserID(ctx context.Context, userID string) ([]WebhookRegistration, error)
	GetActiveWebhooks(ctx context.Context) ([]WebhookRegistration, error)
	GetByID(ctx context.Context, id string) (*WebhookRegistration, error)
	Delete(ctx context.Context, id string) error
	ListDeliveries(ctx context.Context, webhookID string, page, limit int) ([]DeliveryLog, int, error)
}

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Register(ctx context.Context, wh *WebhookRegistration) error {
	query := `
		INSERT INTO webhooks (id, user_id, target_url, secret_hash, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query, wh.ID, wh.UserID, wh.TargetURL, wh.SecretHash, time.Now())
	return err
}

func (r *PostgresRepository) GetActiveWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	query := `SELECT id, user_id, target_url, secret_hash, created_at FROM webhooks`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WebhookRegistration
	for rows.Next() {
		var wh WebhookRegistration
		if err := rows.Scan(&wh.ID, &wh.UserID, &wh.TargetURL, &wh.SecretHash, &wh.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, wh)
	}
	return list, nil
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*WebhookRegistration, error) {
	query := `SELECT id, user_id, target_url, secret_hash, created_at FROM webhooks WHERE id = $1`
	var wh WebhookRegistration
	if err := r.db.QueryRowContext(ctx, query, id).Scan(&wh.ID, &wh.UserID, &wh.TargetURL, &wh.SecretHash, &wh.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &wh, nil
}

func (r *PostgresRepository) GetByUserID(ctx context.Context, userID string) ([]WebhookRegistration, error) {
	query := `SELECT id, user_id, target_url, secret_hash, created_at FROM webhooks WHERE user_id = $1`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WebhookRegistration
	for rows.Next() {
		var wh WebhookRegistration
		if err := rows.Scan(&wh.ID, &wh.UserID, &wh.TargetURL, &wh.SecretHash, &wh.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, wh)
	}
	return list, nil
}

func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	return err
}

func (r *PostgresRepository) ListDeliveries(ctx context.Context, webhookID string, page, limit int) ([]DeliveryLog, int, error) {
	var total int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = $1`, webhookID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * limit
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, webhook_id, status_code, success, error, duration_ms, created_at FROM webhook_deliveries WHERE webhook_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		webhookID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []DeliveryLog
	for rows.Next() {
		var d DeliveryLog
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.StatusCode, &d.Success, &d.Error, &d.DurationMs, &d.CreatedAt); err != nil {
			return nil, 0, err
		}
		list = append(list, d)
	}
	return list, total, nil
}

// JobEnqueuer defines the contract for persisting background retry jobs.
type JobEnqueuer interface {
	Enqueue(ctx context.Context, queueName string, payload any, maxRetries int) (*jobqueue.Job, error)
}

// WebhookRetryPayload is the serializable payload stored in the persistent job queue for retries.
type WebhookRetryPayload struct {
	WebhookID  string          `json:"webhook_id"`
	TargetURL  string          `json:"target_url"`
	SecretHash string          `json:"secret_hash"`
	Payload    json.RawMessage `json:"payload"`
	RequestID  string          `json:"request_id,omitempty"`
}

type Dispatcher struct {
	repo       WebhookRepository
	httpClient *http.Client
	jobQueue   JobEnqueuer
	sem        chan struct{}
	wg         sync.WaitGroup
	timeout    time.Duration
	stop       chan struct{}
	mu         sync.RWMutex
	closed     bool
}

type DispatcherOption func(*Dispatcher)

func WithMaxConcurrency(n int) DispatcherOption {
	return func(d *Dispatcher) {
		if n > 0 {
			d.sem = make(chan struct{}, n)
		}
	}
}

func WithHTTPClient(client *http.Client) DispatcherOption {
	return func(d *Dispatcher) {
		if client != nil {
			d.httpClient = client
		}
	}
}

func WithJobQueue(jq JobEnqueuer) DispatcherOption {
	return func(d *Dispatcher) {
		d.jobQueue = jq
	}
}

func WithTimeout(t time.Duration) DispatcherOption {
	return func(d *Dispatcher) {
		if t > 0 {
			d.timeout = t
		}
	}
}

const (
	DefaultMaxConcurrency = 25
	DefaultTimeout        = 5 * time.Second
	WebhookQueueName      = "webhook_delivery"
)

var ErrDispatcherClosed = errors.New("dispatcher is closed")

func NewDispatcher(repo WebhookRepository, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		repo: repo,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		sem:     make(chan struct{}, DefaultMaxConcurrency),
		timeout: DefaultTimeout,
		stop:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DispatchPayload delivers webhook payloads to active registrations with bounded concurrency
// and background execution. It detaches execution from the incoming request context to prevent
// mid-delivery cancellation when the request context finishes, while keeping request metadata and per-attempt timeouts.
func (d *Dispatcher) DispatchPayload(ctx context.Context, payload interface{}, maxRetries int) error {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return ErrDispatcherClosed
	}
	d.mu.RUnlock()

	// Detach execution from caller's request context while preserving metadata
	bgCtx := context.WithoutCancel(ctx)
	webhooks, err := d.repo.GetActiveWebhooks(bgCtx)
	if err != nil {
		return fmt.Errorf("failed to load webhooks for dispatch: %w", err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	reqID, _ := ctx.Value("requestID").(string)

	for _, wh := range webhooks {
		d.wg.Add(1)
		go func(w WebhookRegistration) {
			defer d.wg.Done()
			select {
			case d.sem <- struct{}{}:
				defer func() { <-d.sem }()
			case <-d.stop:
				return
			}

			d.deliverWithRetry(bgCtx, w, body, maxRetries, reqID)
		}(wh)
	}

	return nil
}

func (d *Dispatcher) deliverWithRetry(ctx context.Context, wh WebhookRegistration, body []byte, maxRetries int, reqID string) {
	// Always sign with SecretHash. The raw secret is never persisted; the hash
	// is the canonical HMAC key for all outbound deliveries.
	err := d.sendHTTP(ctx, wh.TargetURL, wh.SecretHash, body, reqID)
	if err == nil {
		return
	}

	// If persistent job queue is configured and retries are requested, persist retry job to DB
	if d.jobQueue != nil && maxRetries > 1 {
		retryPayload := WebhookRetryPayload{
			WebhookID:  wh.ID,
			TargetURL:  wh.TargetURL,
			SecretHash: wh.SecretHash,
			Payload:    body,
			RequestID:  reqID,
		}
		_, enqueueErr := d.jobQueue.Enqueue(ctx, WebhookQueueName, retryPayload, maxRetries)
		if enqueueErr == nil {
			return
		}
		// If queue enqueueing fails, fallback to in-memory retry
	}

	// In-memory exponential backoff retry fallback
	d.inMemoryRetry(ctx, wh, body, maxRetries, reqID)
}

func (d *Dispatcher) inMemoryRetry(ctx context.Context, wh WebhookRegistration, body []byte, maxRetries int, reqID string) {
	backoff := 100 * time.Millisecond
	for attempt := 2; attempt <= maxRetries; attempt++ {
		select {
		case <-d.stop:
			return
		case <-time.After(backoff):
		}

		err := d.sendHTTP(ctx, wh.TargetURL, wh.SecretHash, body, reqID)
		if err == nil {
			return
		}
		backoff *= 2
	}
}

func (d *Dispatcher) sendHTTP(ctx context.Context, targetURL, secret string, body []byte, reqID string) error {
	attemptCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, "POST", targetURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Moistello-Signature", SignWebhookPayload(body, secret))
	}
	if reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

// ProcessRetryJob executes a queued retry job from the persistent job queue.
func (d *Dispatcher) ProcessRetryJob(ctx context.Context, job *jobqueue.Job) error {
	var payload WebhookRetryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshaling retry payload: %w", err)
	}
	bgCtx := context.WithoutCancel(ctx)
	// payload.SecretHash contains the hex-encoded secret hash used as signing key
	return d.sendHTTP(bgCtx, payload.TargetURL, payload.SecretHash, payload.Payload, payload.RequestID)
}

// Shutdown gracefully waits for all active in-flight delivery goroutines to complete.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		close(d.stop)
		return ctx.Err()
	}
}

// SignWebhookPayload computes an HMAC-SHA256 signature for the given payload
// using the webhook secret. The signature is returned as a hex string.
func SignWebhookPayload(payload []byte, secret string) string {
	// If secret looks like a hex-encoded SHA256 (64 chars), decode it and use raw bytes as key.
	var key []byte
	if len(secret) == 64 {
		if kb, err := hex.DecodeString(secret); err == nil {
			key = kb
		} else {
			key = []byte(secret)
		}
	} else {
		key = []byte(secret)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature verifies the HMAC-SHA256 signature of a webhook payload
// using constant-time comparison to prevent timing attacks.
func VerifyWebhookSignature(payload []byte, signature, secret string) bool {
	if len(signature) == 0 {
		return false
	}
	expected := SignWebhookPayload(payload, secret)
	return constantTimeCompare([]byte(expected), []byte(signature))
}

// constantTimeCompare reports whether a and b are equal in constant time.
func constantTimeCompare(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// VerifySignature reports whether two hex-encoded signatures are equal, in
// constant time. Non-hex or length-mismatched inputs never match.
func VerifySignature(expected, signature string) bool {
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return constantTimeCompare(expectedBytes, sigBytes)
}
