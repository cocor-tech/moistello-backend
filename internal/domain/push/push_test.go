package push_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/push"
)

func TestPushService_DevModeFallbackWhenNoServerKey(t *testing.T) {
	svc := push.NewService(push.Config{})

	err := svc.Send(context.Background(), "device-token-123", "title", "body")
	assert.NoError(t, err, "empty server key should gracefully fallback in dev mode without error")
}

func TestPushService_SendSuccess(t *testing.T) {
	var receivedBody map[string]any
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":1}`))
	}))
	defer server.Close()

	svc := push.NewService(push.Config{
		ServerKey: "test-fcm-key",
		BaseURL:   server.URL,
	})

	err := svc.Send(context.Background(), "device-token-abc", "New activity", "Someone contributed")
	require.NoError(t, err)

	assert.Equal(t, "key=test-fcm-key", receivedAuth)
	assert.Equal(t, "device-token-abc", receivedBody["to"])
	notif := receivedBody["notification"].(map[string]any)
	assert.Equal(t, "New activity", notif["title"])
	assert.Equal(t, "Someone contributed", notif["body"])
}

func TestPushService_RetryOnTransientServerError(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&attempts, 1)
		if current < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":1}`))
	}))
	defer server.Close()

	svc := push.NewService(push.Config{
		ServerKey:  "test-key",
		BaseURL:    server.URL,
		MaxRetries: 3,
	})

	err := svc.Send(context.Background(), "device-token", "title", "body")
	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestPushService_PermanentFailureWhenRetriesExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := push.NewService(push.Config{
		ServerKey:  "test-key",
		BaseURL:    server.URL,
		MaxRetries: 2,
	})

	err := svc.Send(context.Background(), "device-token", "title", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attempts to send push via fcm failed")
}

func TestPushService_MissingTokenErrors(t *testing.T) {
	svc := push.NewService(push.Config{ServerKey: "test-key"})

	err := svc.Send(context.Background(), "", "title", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no registered device token")
}
