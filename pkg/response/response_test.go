package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/response"
)

func TestResponse_EnvelopeContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test-ok", func(c *gin.Context) {
		response.OK(c, gin.H{"foo": "bar"})
	})
	r.GET("/test-err", func(c *gin.Context) {
		response.BadRequest(c, "invalid input")
	})
	r.GET("/test-auth-err", func(c *gin.Context) {
		response.InvalidCredentials(c, "invalid email or signature")
	})
	r.GET("/test-nonce-err", func(c *gin.Context) {
		response.NonceExpired(c, "nonce expired")
	})

	t.Run("OK response", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-ok", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var env response.Envelope
		err := json.Unmarshal(w.Body.Bytes(), &env)
		require.NoError(t, err)
		assert.True(t, env.Success)
		assert.NotNil(t, env.Data)
	})

	t.Run("Error response with RFC 9457 problem details and requestId", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-err", nil)
		req.Header.Add("X-Request-Id", "req-123")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var env response.Envelope
		err := json.Unmarshal(w.Body.Bytes(), &env)
		require.NoError(t, err)
		assert.False(t, env.Success)
		assert.Equal(t, "BAD_REQUEST", env.Code)
		assert.Equal(t, "invalid input", env.Message)
		assert.Equal(t, "req-123", env.RequestId)

		// RFC 9457 assertions
		assert.Equal(t, 400, env.Status)
		assert.Equal(t, "Bad Request", env.Title)
		assert.Equal(t, "invalid input", env.Detail)
		assert.Equal(t, "https://moistello.com/probs/bad-request", env.Type)
		assert.Equal(t, "/test-err", env.Instance)
	})

	t.Run("Auth error response with stable code and problem details", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-auth-err", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		var env response.Envelope
		err := json.Unmarshal(w.Body.Bytes(), &env)
		require.NoError(t, err)
		assert.False(t, env.Success)
		assert.Equal(t, "INVALID_CREDENTIALS", env.Code)
		assert.Equal(t, 401, env.Status)
		assert.Equal(t, "Unauthorized", env.Title)
		assert.Equal(t, "invalid email or signature", env.Detail)
		assert.Equal(t, "https://moistello.com/probs/invalid-credentials", env.Type)
	})

	t.Run("Nonce expired error response with stable code and problem details", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test-nonce-err", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		var env response.Envelope
		err := json.Unmarshal(w.Body.Bytes(), &env)
		require.NoError(t, err)
		assert.False(t, env.Success)
		assert.Equal(t, "NONCE_EXPIRED", env.Code)
		assert.Equal(t, "https://moistello.com/probs/nonce-expired", env.Type)
	})
}
