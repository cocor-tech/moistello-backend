package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	db            *sql.DB
	redis         *redis.Client
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

	overallStatus := "ok"
	httpStatusCode := http.StatusOK

	if !allHealthy {
		if hasFailure {
			overallStatus = "degraded"
		} else {
			overallStatus = "degraded"
		}
	}

	resp := HealthResponse{
		Status:       overallStatus,
		Dependencies: deps,
	}

	c.JSON(httpStatusCode, resp)
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

	if resp.StatusCode >= 400 {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("received status code %d", resp.StatusCode),
		}
	}

	var body struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	msg := "reachable"
	if body.Result.Status != "" {
		msg = fmt.Sprintf("status: %s", body.Result.Status)
	}

	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", latency),
		Message: msg,
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

	if resp.StatusCode >= 400 {
		return DependencyStatus{
			Status:  "unhealthy",
			Latency: fmt.Sprintf("%v", latency),
			Message: fmt.Sprintf("received status code %d", resp.StatusCode),
		}
	}

	return DependencyStatus{
		Status:  "healthy",
		Latency: fmt.Sprintf("%v", latency),
		Message: "reachable",
	}
}

// @Summary Readiness check
// @Description Readiness probe — checks database and Redis connectivity.
// @Tags Health
// @Produce json
// @Success 200 {object} object{status=string}
// @Failure 503 {object} object{status=string,error=string}
// @Router /ready [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx := c.Request.Context()
	if h.db != nil {
		if err := h.db.PingContext(ctx); err != nil {
			c.JSON(503, gin.H{"status": "not ready", "error": "database unreachable"})
			return
		}
	}
	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			c.JSON(503, gin.H{"status": "not ready", "error": "redis unreachable"})
			return
		}
	}
	c.JSON(200, gin.H{"status": "ready"})
}
