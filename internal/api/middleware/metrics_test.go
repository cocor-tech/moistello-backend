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

func TestMetricsEndpoint_RequiresAdminAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	adminKey := "test-admin-api-key"

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware(adminKey))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Request without API key should be rejected
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusUnauthorized, w1.Code)

	// Request with wrong API key should be rejected
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/metrics", nil)
	req2.Header.Set("X-Admin-API-Key", "wrong-key")
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)

	// Request with correct API key should succeed
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/metrics", nil)
	req3.Header.Set("X-Admin-API-Key", adminKey)
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)
	assert.Contains(t, w3.Body.String(), "moistello_http_requests_total")
}

func TestMetricsEndpoint_DualAdminAPIKeys_ZeroDowntimeRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	primaryKey := "primary-admin-key-12345"
	secondaryKey := "secondary-admin-key-67890"

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware(primaryKey, secondaryKey))
	r.GET("/metrics", func(c *gin.Context) {
		identity, _ := c.Get("adminKeyIdentity")
		c.JSON(http.StatusOK, gin.H{"status": "ok", "identity": identity})
	})

	// 1. Primary key succeeds with identity=primary
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/metrics", nil)
	req1.Header.Set("X-Admin-API-Key", primaryKey)
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, w1.Body.String(), `"identity":"primary"`)

	// 2. Secondary key succeeds with identity=secondary
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/metrics", nil)
	req2.Header.Set("X-Admin-API-Key", secondaryKey)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), `"identity":"secondary"`)

	// 3. Unknown key is rejected with 401
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/metrics", nil)
	req3.Header.Set("X-Admin-API-Key", "unknown-key")
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusUnauthorized, w3.Code)

	// 4. Missing key is rejected with 401
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusUnauthorized, w4.Code)
}
