package webhook

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/jobqueue"
)

func TestSignWebhookPayload(t *testing.T) {
	payload := []byte(`{"event":"test"}`)
	secret := "my-secret"

	sig1 := SignWebhookPayload(payload, secret)
	sig2 := SignWebhookPayload(payload, secret)

	assert.Equal(t, sig1, sig2, "same payload and secret should produce identical signatures")
	assert.NotEmpty(t, sig1)
	assert.Len(t, sig1, 64, "SHA-256 hex digest should be 64 chars")
}

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	payload := []byte(`{"event":"payment.completed"}`)
	secret := "super-secret-key"
	signature := SignWebhookPayload(payload, secret)

	assert.True(t, VerifyWebhookSignature(payload, signature, secret))
}

func TestVerifyWebhookSignature_Tampered(t *testing.T) {
	payload := []byte(`{"event":"payment.completed"}`)
	secret := "super-secret-key"
	signature := SignWebhookPayload(payload, secret)

	tampered := "deadbeef" + signature[8:]
	assert.False(t, VerifyWebhookSignature(payload, tampered, secret))
}

func TestVerifyWebhookSignature_WrongSecret(t *testing.T) {
	payload := []byte(`{"event":"payment.completed"}`)
	signature := SignWebhookPayload(payload, "correct-secret")

	assert.False(t, VerifyWebhookSignature(payload, signature, "wrong-secret"))
}

func TestVerifyWebhookSignature_MissingSignature(t *testing.T) {
	payload := []byte(`{"event":"payment.completed"}`)
	secret := "super-secret-key"

	assert.False(t, VerifyWebhookSignature(payload, "", secret))
	assert.False(t, VerifyWebhookSignature(payload, "   ", secret))
}

func TestConstantTimeCompare(t *testing.T) {
	a := []byte("hello")
	b := []byte("hello")
	c := []byte("world")

	assert.True(t, constantTimeCompare(a, b))
	assert.False(t, constantTimeCompare(a, c))
	assert.False(t, constantTimeCompare(a, []byte("hi")))
}

func TestVerifySignature(t *testing.T) {
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	assert.True(t, VerifySignature(hash, hash))
	assert.False(t, VerifySignature(hash, "00000000000000000000000000000000000000000000000000000000000000"))
	assert.False(t, VerifySignature("not-hex", hash))
	assert.False(t, VerifySignature(hash, "not-hex"))
}

type fakeWebhookRepo struct {
	webhooks map[string]*WebhookRegistration
}

func (f *fakeWebhookRepo) Register(ctx context.Context, wh *WebhookRegistration) error {
	f.webhooks[wh.ID] = wh
	return nil
}

func (f *fakeWebhookRepo) GetByUserID(ctx context.Context, userID string) ([]WebhookRegistration, error) {
	return nil, nil
}

func (f *fakeWebhookRepo) GetActiveWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	var list []WebhookRegistration
	for _, wh := range f.webhooks {
		list = append(list, *wh)
	}
	return list, nil
}

func (f *fakeWebhookRepo) GetByID(ctx context.Context, id string) (*WebhookRegistration, error) {
	if wh, ok := f.webhooks[id]; ok {
		return wh, nil
	}
	return nil, nil
}
func (f *fakeWebhookRepo) Delete(ctx context.Context, id string) error {
	delete(f.webhooks, id)
	return nil
}
func (f *fakeWebhookRepo) ListDeliveries(ctx context.Context, webhookID string, page, limit int) ([]DeliveryLog, int, error) {
	return nil, 0, nil
}

func TestDispatchPayload_DetachedBackgroundContext(t *testing.T) {
	received := make(chan []byte, 1)
	receivedSig := make(chan string, 1)
	receivedReqID := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond) // slight delay
		body, _ := io.ReadAll(r.Body)
		received <- body
		receivedSig <- r.Header.Get("X-Moistello-Signature")
		receivedReqID <- r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := &fakeWebhookRepo{
		webhooks: map[string]*WebhookRegistration{
			"wh-1": {
				ID:         "wh-1",
				UserID:     "user-1",
				TargetURL:  server.URL,
				Secret:     "secret-123",
				SecretHash: webhookSHA256("secret-123"),
			},
		},
	}

	d := NewDispatcher(repo, WithTimeout(2*time.Second))

	// Create a context and cancel it IMMEDIATELY
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "requestID", "req-test-999"))
	cancel() // cancelled before / as dispatch occurs

	err := d.DispatchPayload(ctx, map[string]string{"event": "user.created"}, 1)
	require.NoError(t, err)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	err = d.Shutdown(shutdownCtx)
	require.NoError(t, err)

	select {
	case body := <-received:
		assert.Contains(t, string(body), "user.created")
		sig := <-receivedSig
		assert.NotEmpty(t, sig)
		assert.True(t, VerifyWebhookSignature(body, sig, webhookSHA256("secret-123")))
		reqID := <-receivedReqID
		assert.Equal(t, "req-test-999", reqID)
	case <-time.After(1 * time.Second):
		t.Fatal("webhook delivery failed or was cancelled by request context")
	}
}

func webhookSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestDispatchPayload_BoundedConcurrency(t *testing.T) {
	var activeReqs atomic.Int32
	var maxObserved atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := activeReqs.Add(1)
		for {
			old := maxObserved.Load()
			if current <= old || maxObserved.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		activeReqs.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := &fakeWebhookRepo{
		webhooks: make(map[string]*WebhookRegistration),
	}
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("wh-%d", i)
		repo.webhooks[id] = &WebhookRegistration{
			ID:        id,
			UserID:    "user-1",
			TargetURL: server.URL,
			Secret:    "sec",
		}
	}

	maxConcurrency := 2
	d := NewDispatcher(repo, WithMaxConcurrency(maxConcurrency), WithTimeout(2*time.Second))

	err := d.DispatchPayload(context.Background(), map[string]string{"event": "test"}, 1)
	require.NoError(t, err)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	err = d.Shutdown(shutdownCtx)
	require.NoError(t, err)

	assert.LessOrEqual(t, maxObserved.Load(), int32(maxConcurrency), "peak concurrent deliveries must not exceed bounded limit")
}

func TestDispatchPayload_PersistentRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := &fakeWebhookRepo{
		webhooks: map[string]*WebhookRegistration{
			"wh-1": {
				ID:        "wh-1",
				UserID:    "user-1",
				TargetURL: server.URL,
				Secret:    "secret-retry",
			},
		},
	}

	jq := jobqueue.NewJobQueue(nil) // in-memory job queue
	d := NewDispatcher(repo, WithJobQueue(jq), WithTimeout(1*time.Second))

	err := d.DispatchPayload(context.Background(), map[string]string{"event": "retry.test"}, 3)
	require.NoError(t, err)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	err = d.Shutdown(shutdownCtx)
	require.NoError(t, err)

	// Ensure job was enqueued in persistent job queue
	job, err := jq.Dequeue(context.Background(), WebhookQueueName)
	require.NoError(t, err)
	require.NotNil(t, job, "failed delivery must be persisted in job queue for retries")
	assert.Equal(t, 3, job.MaxRetries)

	// Test processing retry job
	err = d.ProcessRetryJob(context.Background(), job)
	assert.Error(t, err, "server returns 500, retry should return error")
}

func TestDispatchPayload_ClosedDispatcher(t *testing.T) {
	repo := &fakeWebhookRepo{webhooks: map[string]*WebhookRegistration{}}
	d := NewDispatcher(repo)

	err := d.Shutdown(context.Background())
	require.NoError(t, err)

	err = d.DispatchPayload(context.Background(), map[string]string{"event": "test"}, 1)
	assert.Equal(t, ErrDispatcherClosed, err)
}

func TestConstantTimeCompareTiming(t *testing.T) {
	a := []byte("same-length-string")
	b := []byte("same-length-string")
	c := []byte("different-str")

	iterations := 1000
	var sameTime, diffTime time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		constantTimeCompare(a, b)
		sameTime += time.Since(start)
	}

	for i := 0; i < iterations; i++ {
		start := time.Now()
		constantTimeCompare(a, c)
		diffTime += time.Since(start)
	}

	avgSame := sameTime / time.Duration(iterations)
	avgDiff := diffTime / time.Duration(iterations)

	ratio := float64(avgDiff) / float64(avgSame)
	assert.Greater(t, ratio, 0.1, "different-length comparison should not be significantly faster")
	assert.Less(t, ratio, 5.0, "different-length comparison should not be significantly slower")
	_ = subtle.ConstantTimeCompare(a, b)
}

func ExampleSignWebhookPayload() {
	payload := []byte(`{"event":"payment.completed","amount":100}`)
	secret := "whsec_1234567890"
	signature := SignWebhookPayload(payload, secret)
	println(len(signature))
}

func ExampleVerifyWebhookSignature() {
	payload := []byte(`{"event":"payment.completed","amount":100}`)
	secret := "whsec_1234567890"
	signature := SignWebhookPayload(payload, secret)
	println(VerifyWebhookSignature(payload, signature, secret))
}
