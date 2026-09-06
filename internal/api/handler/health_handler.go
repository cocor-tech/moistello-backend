package handler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// rabbitChecker is satisfied by *rabbitmq.Client.
// Using an interface keeps the health handler free of an amqp091 import.
type rabbitChecker interface {
	IsAlive() bool
}

type HealthHandler struct {
	db            *sql.DB
	redis         *redis.Client
	rabbit        rabbitChecker
	sorobanRPCURL string
	horizonURL    string
	checkTimeout  time.Duration
	httpClient    *http.Client
}

type DependencyStatus struct {
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Message string `json:"message,omitempty"`
}

type HealthResponse struct {
	Status       string                      `json:"status"`
	Dependencies map[string]DependencyStatus `json:"dependencies"`
}

func NewHealthHandler(db *sql.DB, rds *redis.Client, sorobanRPCURL, horizonURL string) *HealthHandler {
	return &HealthHandler{
		db:            db,
		redis:         rds,
		sorobanRPCURL: sorobanRPCURL,
		horizonURL:    horizonURL,
		checkTimeout:  5 * time.Second,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

// WithRabbitMQ attaches a RabbitMQ liveness checker to the health handler.
// Call this after NewHealthHandler when the RabbitMQ client is available.
func (h *HealthHandler) WithRabbitMQ(r rabbitChecker) *HealthHandler {
	h.rabbit = r
	return h
}

func (h *HealthHandler) SetTimeout(d time.Duration) {
	if d > 0 {
		h.checkTimeout = d
		h.httpClient.Timeout = d
	}
}

// @Summary Health check with dependency status
// @Description Reports status of PostgreSQL, Redis, Stellar RPC, and Horizon dependencies.
// @Tags Health
// @Produce json
// @Param timeout query string false "Timeout per check in seconds or duration (default 5s)"
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	timeout := h.checkTimeout
	if reqTimeout := c.Query("timeout"); reqTimeout != "" {
		if d, err := time.ParseDuration(reqTimeout); err == nil && d > 0 {
			timeout = d
		} else if sec, err := time.ParseDuration(reqTimeout + "s"); err == nil && sec > 0 {
			timeout = sec
		}
	}

	deps := make(map[string]DependencyStatus)
	allHealthy := true
	hasFailure := false

	// 1. PostgreSQL check
	postgresStatus := h.checkPostgres(timeout)
	deps["postgres"] = postgresStatus
	if postgresStatus.Status != "healthy" {
		allHealthy = false
		hasFailure = true
	}

	// 2. Redis check
	redisStatus := h.checkRedis(timeout)
	deps["redis"] = redisStatus
	if redisStatus.Status != "healthy" {
		allHealthy = false
		hasFailure = true
	}

	// 3. Stellar RPC check
	stellarRPCStatus := h.checkStellarRPC(timeout)
	deps["stellar_rpc"] = stellarRPCStatus
	if stellarRPCStatus.Status != "healthy" {
		allHealthy = false
	}

	// 4. Horizon check
	horizonStatus := h.checkHorizon(timeout)
	deps["horizon"] = horizonStatus
	if horizonStatus.Status != "healthy" {
		allHealthy = false
	}

	// 5. RabbitMQ check
	rabbitmqStatus := h.checkRabbitMQ()
	deps["rabbitmq"] = rabbitmqStatus
	if rabbitmqStatus.Status != "healthy" {
		allHealthy = false
		hasFailure = true
	}

	overallStatus := "ok"
	httpStatusCode := http.StatusOK

	if !allHealthy {
		overallStatus = "degraded"
		if hasFailure {
			httpStatusCode = http.StatusServiceUnavailable
		}
	}

	resp := HealthResponse{
		Status:       overallStatus,
		Dependencies: deps,
	}

	c.JSON(httpStatusCode, resp)
}

// @Summary Liveness check
// @Description Reports liveness status of the API service.
// @Tags Health
// @Produce json
// @Success 200 {object} HealthResponse
// @Router /health/live [get]
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{
		Status: "ok",
	})
}

// @Summary Readiness check
// @Description Reports readiness status of core dependencies (PostgreSQL, Redis, RabbitMQ, Stellar RPC, Horizon).
// @Tags Health
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse
// @Router /health/ready [get]
func (h *HealthHandler) Readiness(c *gin.Context) {
	timeout := h.checkTimeout
	deps := make(map[string]DependencyStatus)
	allHealthy := true

	postgresStatus := h.checkPostgres(timeout)
	deps["postgres"] = postgresStatus
	if postgresStatus.Status != "healthy" {
		allHealthy = false
	}

	redisStatus := h.checkRedis(timeout)
	deps["redis"] = redisStatus
	if redisStatus.Status != "healthy" {
		allHealthy = false
	}

	rabbitmqStatus := h.checkRabbitMQ()
	deps["rabbitmq"] = rabbitmqStatus
	if rabbitmqStatus.Status != "healthy" {
		allHealthy = false
	}

	stellarRPCStatus := h.checkStellarRPC(timeout)
	deps["stellar_rpc"] = stellarRPCStatus
	if stellarRPCStatus.Status != "healthy" {
		allHealthy = false
	}

	horizonStatus := h.checkHorizon(timeout)
	deps["horizon"] = horizonStatus
	if horizonStatus.Status != "healthy" {
		allHealthy = false
	}

	overallStatus := "ready"
	httpStatusCode := http.StatusOK
	if !allHealthy {
		overallStatus = "not ready"
		httpStatusCode = http.StatusServiceUnavailable
	}

	c.JSON(httpStatusCode, HealthResponse{
		Status:       overallStatus,
		Dependencies: deps,
	})
}

func (h *HealthHandler) checkPostgres(timeout time.Duration) DependencyStatus {
	if h.db == nil {
		return DependencyStatus{Status: "unhealthy", Message: "database instance not initialized"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	if err := h.db.PingContext(ctx); err != nil {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", time.Since(start).Round(time.Microsecond)),
			Message: fmt.Sprintf("unreachable: %v", err),
		}
	}
	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", time.Since(start).Round(time.Microsecond)),
		Message: "connected",
	}
}

func (h *HealthHandler) checkRedis(timeout time.Duration) DependencyStatus {
	if h.redis == nil {
		return DependencyStatus{Status: "unhealthy", Message: "redis instance not initialized"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	if err := h.redis.Ping(ctx).Err(); err != nil {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", time.Since(start).Round(time.Microsecond)),
			Message: fmt.Sprintf("unreachable: %v", err),
		}
	}
	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", time.Since(start).Round(time.Microsecond)),
		Message: "connected",
	}
}

func (h *HealthHandler) checkRabbitMQ() DependencyStatus {
	if h.rabbit == nil {
		return DependencyStatus{Status: "unhealthy", Message: "rabbitmq client not initialized"}
	}
	if !h.rabbit.IsAlive() {
		return DependencyStatus{Status: "unhealthy", Message: "connection closed"}
	}
	return DependencyStatus{Status: "healthy", Message: "connected"}
}

func (h *HealthHandler) checkStellarRPC(timeout time.Duration) DependencyStatus {
	if h.sorobanRPCURL == "" {
		return DependencyStatus{Status: "unhealthy", Message: "soroban_rpc_url not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"getHealth"}`)
	req, err := http.NewRequestWithContext(ctx, "POST", h.sorobanRPCURL, bytes.NewBuffer(payload))
	if err != nil {
		return DependencyStatus{Status: "unhealthy", Message: fmt.Sprintf("failed to construct request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := h.httpClient.Do(req)
	latency := time.Since(start).Round(time.Microsecond)
	if err != nil {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("bad status code: %d", resp.StatusCode),
		}
	}

	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", latency),
		Message: "reachable",
	}
}

func (h *HealthHandler) checkHorizon(timeout time.Duration) DependencyStatus {
	if h.horizonURL == "" {
		return DependencyStatus{Status: "unhealthy", Message: "horizon_url not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", h.horizonURL, nil)
	if err != nil {
		return DependencyStatus{Status: "unhealthy", Message: fmt.Sprintf("failed to construct request: %v", err)}
	}

	start := time.Now()
	resp, err := h.httpClient.Do(req)
	latency := time.Since(start).Round(time.Microsecond)
	if err != nil {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("bad status code: %d", resp.StatusCode),
		}
	}

	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", latency),
		Message: "reachable",
	}
}
