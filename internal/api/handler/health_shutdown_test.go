package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
)

func TestHealthHandler_ReadinessFailsFastDuringShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewHealthHandler(nil, nil, "", "")
	r := gin.New()
	r.GET("/health/ready", h.Readiness)
	r.GET("/health/live", h.Liveness)

	assert.False(t, h.ShuttingDown())
	h.BeginShutdown()
	assert.True(t, h.ShuttingDown())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/health/ready", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"shutting down"`)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/health/live", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "liveness stays green while draining")
}
