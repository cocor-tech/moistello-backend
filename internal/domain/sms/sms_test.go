package sms_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/sms"
)

func TestSMSService_DevModeFallbackWhenNoCredentials(t *testing.T) {
	svc := sms.NewService(sms.Config{})

	err := svc.Send(context.Background(), "+15551234567", "hello")
	assert.NoError(t, err, "empty credentials should gracefully fallback in dev mode without error")
}

func TestSMSService_SendSuccess(t *testing.T) {
	var receivedForm url.Values
	var receivedAuth string
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		_ = r.ParseForm()
		receivedForm = r.Form
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer server.Close()

	svc := sms.NewService(sms.Config{
		AccountSID: "AC-test-sid",
		AuthToken:  "test-token",
		FromNumber: "+15550001111",
		BaseURL:    server.URL,
	})

	err := svc.Send(context.Background(), "+15559998888", "your OTP is 123456")
	require.NoError(t, err)

	assert.Contains(t, receivedPath, "AC-test-sid")
	assert.NotEmpty(t, receivedAuth)
	assert.Equal(t, "+15559998888", receivedForm.Get("To"))
	assert.Equal(t, "+15550001111", receivedForm.Get("From"))
	assert.Equal(t, "your OTP is 123456", receivedForm.Get("Body"))
}

func TestSMSService_RetryOnTransientServerError(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&attempts, 1)
		if current < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM123"}`))
	}))
	defer server.Close()

	svc := sms.NewService(sms.Config{
		AccountSID: "AC-test",
		AuthToken:  "token",
		FromNumber: "+15550001111",
		BaseURL:    server.URL,
		MaxRetries: 3,
	})

	err := svc.Send(context.Background(), "+15559998888", "retry test")
	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestSMSService_PermanentFailureWhenRetriesExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := sms.NewService(sms.Config{
		AccountSID: "AC-test",
		AuthToken:  "token",
		FromNumber: "+15550001111",
		BaseURL:    server.URL,
		MaxRetries: 2,
	})

	err := svc.Send(context.Background(), "+15559998888", "failure test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attempts to send sms via twilio failed")
}

func TestSMSService_MissingFromNumberErrors(t *testing.T) {
	svc := sms.NewService(sms.Config{
		AccountSID: "AC-test",
		AuthToken:  "token",
	})

	err := svc.Send(context.Background(), "+15559998888", "no from number")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "FromNumber")
}
