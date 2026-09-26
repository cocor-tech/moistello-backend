package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRabbit struct {
	alive bool
}

func (m *mockRabbit) IsAlive() bool {
	return m.alive
}

func setupTestHealthHandler(t *testing.T) (*HealthHandler, sqlmock.Sqlmock, func()) {
	gin.SetMode(gin.TestMode)
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)

	rds := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	// Stub Stellar RPC + Horizon so the health check sees them as reachable
	// without requiring external services on fixed localhost ports.
	stellarRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"status":"healthy"}}`)
	}))
	horizon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	h := NewHealthHandler(mockDB, rds, stellarRPC.URL, horizon.URL)
	h.WithRabbitMQ(&mockRabbit{alive: true})

	cleanup := func() {
		stellarRPC.Close()
		horizon.Close()
		mockDB.Close()
		rds.Close()
		mr.Close()
	}

	return h, mock, cleanup
}

func TestHealthHandler_Health_AllHealthy(t *testing.T) {
	h, mock, cleanup := setupTestHealthHandler(t)
	defer cleanup()

	mock.ExpectPing()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}

func TestHealthHandler_Health_PostgresDown(t *testing.T) {
	h, mock, cleanup := setupTestHealthHandler(t)
	defer cleanup()

	mock.ExpectPing().WillReturnError(sql.ErrConnDone)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "degraded")
	assert.Contains(t, w.Body.String(), "unhealthy")
}

func TestHealthHandler_Readiness_Unhealthy(t *testing.T) {
	h, mock, cleanup := setupTestHealthHandler(t)
	defer cleanup()

	h.WithRabbitMQ(&mockRabbit{alive: false})
	mock.ExpectPing()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Readiness(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "not ready")
}

func TestHealthHandler_Readiness_ReportsPerDependencyStatus(t *testing.T) {
	h, mock, cleanup := setupTestHealthHandler(t)
	defer cleanup()

	mock.ExpectPing().WillReturnError(sql.ErrConnDone)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Readiness(c)

	var resp HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "not ready", resp.Status)
	assert.Equal(t, "unhealthy", resp.Dependencies["postgres"].Status)
	assert.Equal(t, "healthy", resp.Dependencies["redis"].Status)
	assert.Equal(t, "healthy", resp.Dependencies["rabbitmq"].Status)
}

func TestHealthHandler_Readiness_RedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()
	mock.ExpectPing()

	// Nothing listens on this address, so the Redis check must fail.
	rds := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer rds.Close()

	h := NewHealthHandler(mockDB, rds, "", "")
	h.WithRabbitMQ(&mockRabbit{alive: true})
	h.SetTimeout(200 * time.Millisecond)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Readiness(c)

	var resp HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "unhealthy", resp.Dependencies["redis"].Status)
	assert.Equal(t, "healthy", resp.Dependencies["postgres"].Status)
}

func TestHealthHandler_Liveness(t *testing.T) {
	h, _, cleanup := setupTestHealthHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/live", nil)

	h.Liveness(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}
