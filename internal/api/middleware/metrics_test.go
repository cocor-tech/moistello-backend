package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/pkg/metrics"
)

func TestPrometheusMiddleware_RecordsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Initialize vector metrics with sample observations/values so prometheus gatherer includes them
	metrics.WSActiveConnections.Set(1)
	metrics.DBPoolUtilization.WithLabelValues("open").Set(5)
	metrics.RPCLatencySeconds.WithLabelValues("stellar", "GetAccount").Observe(0.12)

	r := gin.New()
	r.Use(middleware.PrometheusMiddleware())

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/api/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/api/error", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
	})

	// Perform test requests
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/api/test", nil)
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/error", nil)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)

	// Fetch /metrics endpoint
	wm := httptest.NewRecorder()
	reqm, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(wm, reqm)

	assert.Equal(t, http.StatusOK, wm.Code)
	body := wm.Body.String()

	assert.Contains(t, body, "moistello_http_requests_total")
	assert.Contains(t, body, "moistello_http_duration_seconds")
	assert.Contains(t, body, "moistello_http_errors_total")
	assert.Contains(t, body, "moistello_websocket_active_connections")
	assert.Contains(t, body, "moistello_db_pool_utilization")
	assert.Contains(t, body, "moistello_rpc_latency_seconds")
}
