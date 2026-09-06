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

	t.Run("Error response with requestId", func(t *testing.T) {
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
	})
}
