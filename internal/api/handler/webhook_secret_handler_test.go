package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/webhook"
)

// fakeWebhookRepo is an in-memory WebhookRepository for handler tests.
type fakeWebhookRepo struct {
	registered []webhook.WebhookRegistration
}

func (r *fakeWebhookRepo) Register(_ context.Context, wh *webhook.WebhookRegistration) error {
	r.registered = append(r.registered, *wh)
	return nil
}
func (r *fakeWebhookRepo) GetByUserID(_ context.Context, userID string) ([]webhook.WebhookRegistration, error) {
	var out []webhook.WebhookRegistration
	for _, w := range r.registered {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out, nil
}
func (r *fakeWebhookRepo) GetActiveWebhooks(_ context.Context) ([]webhook.WebhookRegistration, error) {
	return r.registered, nil
}
func (r *fakeWebhookRepo) GetByID(_ context.Context, id string) (*webhook.WebhookRegistration, error) {
	for i := range r.registered {
		if r.registered[i].ID == id {
			return &r.registered[i], nil
		}
	}
	return nil, nil
}
func (r *fakeWebhookRepo) Delete(_ context.Context, id string) error {
	for i, w := range r.registered {
		if w.ID == id {
			r.registered = append(r.registered[:i], r.registered[i+1:]...)
			return nil
		}
	}
	return nil
}
func (r *fakeWebhookRepo) ListDeliveries(_ context.Context, _ string, _, _ int) ([]webhook.DeliveryLog, int, error) {
	return nil, 0, nil
}

func setupWebhookSecretRouter(repo webhook.WebhookRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewWebhookHandler(repo)
	ih := handler.NewIncomingWebhookHandler(repo)
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-sec-1")
		c.Next()
	})
	r.POST("/webhooks", h.RegisterWebhook)
	r.GET("/webhooks", h.ListWebhooks)
	r.POST("/webhooks/incoming/:id", ih.ReceiveWebhook)
	return r
}

// TestWebhookHandler_SecretReturnedOnceAtRegistration verifies that:
//   - The plaintext secret is included in the registration response exactly once.
//   - The secret_hash (64-char hex) is the only persistent representation.
//   - The secret field is not present in subsequent list responses.
func TestWebhookHandler_SecretReturnedOnceAtRegistration(t *testing.T) {
	repo := &fakeWebhookRepo{}
	r := setupWebhookSecretRouter(repo)

	body, _ := json.Marshal(map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"contribution.confirmed"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/webhooks", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "response must have a data envelope")

	// Plaintext secret must be in the one-time registration response.
	secret, hasSecret := data["secret"].(string)
	assert.True(t, hasSecret, "plaintext secret must be returned at registration time")
	assert.NotEmpty(t, secret, "secret must not be empty")
	assert.Len(t, secret, 64, "default secret is 32 random bytes encoded as 64 hex chars")

	// The webhook record must NOT expose the secret through JSON.
	webhookObj, ok := data["webhook"].(map[string]any)
	require.True(t, ok, "response must contain webhook object")
	_, secretInWebhook := webhookObj["secret"]
	assert.False(t, secretInWebhook, "webhook object must not expose plaintext secret")

	// secret_hash must be a 64-char lowercase hex digest (SHA-256).
	secretHash, ok := webhookObj["secret_hash"].(string)
	assert.True(t, ok, "secret_hash must be present in the webhook object")
	assert.Regexp(t, `^[0-9a-f]{64}$`, secretHash, "secret_hash must be a 64-char hex SHA-256 digest")

	// The hash must be the SHA-256 of the secret.
	assert.NotEqual(t, secret, secretHash, "secret and secret_hash must differ")

	// The persisted record must store the hash, not the secret.
	require.Len(t, repo.registered, 1)
	assert.Equal(t, secretHash, repo.registered[0].SecretHash)
	assert.Empty(t, repo.registered[0].Secret, "Secret field must not be persisted")
}

// TestWebhookHandler_ListDoesNotExposeSecret verifies list responses never
// include the plaintext secret.
func TestWebhookHandler_ListDoesNotExposeSecret(t *testing.T) {
	repo := &fakeWebhookRepo{}
	r := setupWebhookSecretRouter(repo)

	// Register first.
	body, _ := json.Marshal(map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"payout.executed"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/webhooks", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// List.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/webhooks", nil)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var listResp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &listResp))
	listData := listResp["data"].(map[string]any)
	webhooks := listData["webhooks"].([]any)
	require.Len(t, webhooks, 1)

	wh := webhooks[0].(map[string]any)
	_, hasSecret := wh["secret"]
	assert.False(t, hasSecret, "list response must not include plaintext secret")
	assert.Regexp(t, `^[0-9a-f]{64}$`, wh["secret_hash"], "list must include secret_hash")
}

// TestWebhookIncoming_SignatureVerifiedWithHash verifies that the incoming
// webhook endpoint accepts a signature computed with the stored secret_hash.
func TestWebhookIncoming_SignatureVerifiedWithHash(t *testing.T) {
	repo := &fakeWebhookRepo{}
	r := setupWebhookSecretRouter(repo)

	// Register a webhook to get an ID and the one-time secret.
	body, _ := json.Marshal(map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"contribution.confirmed"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/webhooks", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var regResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &regResp))
	data := regResp["data"].(map[string]any)
	webhookObj := data["webhook"].(map[string]any)
	webhookID := webhookObj["id"].(string)
	secretHash := webhookObj["secret_hash"].(string)

	payload := []byte(`{"event":"contribution.confirmed","amount":100}`)
	sig := webhook.SignWebhookPayload(payload, secretHash)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/webhooks/incoming/"+webhookID, bytes.NewBuffer(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Moistello-Signature", sig)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code, "valid signature should be accepted")
}

// TestWebhookIncoming_WrongSignatureRejected verifies tampered signatures fail.
func TestWebhookIncoming_WrongSignatureRejected(t *testing.T) {
	repo := &fakeWebhookRepo{}
	r := setupWebhookSecretRouter(repo)

	body, _ := json.Marshal(map[string]any{
		"url":    "https://example.com/hook",
		"events": []string{"contribution.confirmed"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/webhooks", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var regResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &regResp))
	data := regResp["data"].(map[string]any)
	webhookObj := data["webhook"].(map[string]any)
	webhookID := webhookObj["id"].(string)

	payload := []byte(`{"event":"contribution.confirmed"}`)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/webhooks/incoming/"+webhookID, bytes.NewBuffer(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Moistello-Signature", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusUnauthorized, w2.Code, "wrong signature must be rejected")
}
