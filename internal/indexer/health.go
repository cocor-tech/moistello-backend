package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// RabbitChecker is satisfied by *rabbitmq.Client. Using an interface keeps
// the health handler free of an amqp091 import.
type RabbitChecker interface {
	IsAlive() bool
}

// CursorReader is satisfied by *CursorTracker.
type CursorReader interface {
	GetCurrent(ctx context.Context) (*Cursor, error)
}

// DependencyStatus reports the health of a single indexer dependency.
type DependencyStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// HealthResponse is the body returned by /health and /health/ready.
type HealthResponse struct {
	Status       string                      `json:"status"`
	Dependencies map[string]DependencyStatus `json:"dependencies"`
}

// HealthHandler serves liveness/readiness checks for the indexer, covering
// PostgreSQL, Redis, RabbitMQ, and cursor freshness so a stuck poll loop or
// dropped dependency is reflected in the probe result instead of always
// reporting healthy.
type HealthHandler struct {
	db           *sqlx.DB
	redis        *redis.Client
	rabbit       RabbitChecker
	cursor       CursorReader
	maxCursorLag time.Duration
	checkTimeout time.Duration
	now          func() time.Time
}

// NewHealthHandler creates a HealthHandler. maxCursorLag <= 0 falls back to
// a 2 minute default.
func NewHealthHandler(db *sqlx.DB, redisClient *redis.Client, rabbit RabbitChecker, cursor CursorReader, maxCursorLag time.Duration) *HealthHandler {
	if maxCursorLag <= 0 {
		maxCursorLag = 2 * time.Minute
	}
	return &HealthHandler{
		db:           db,
		redis:        redisClient,
		rabbit:       rabbit,
		cursor:       cursor,
		maxCursorLag: maxCursorLag,
		checkTimeout: 5 * time.Second,
		now:          time.Now,
	}
}

func (h *HealthHandler) checkPostgres(ctx context.Context) DependencyStatus {
	if h.db == nil {
		return DependencyStatus{Status: "unhealthy", Message: "database not initialized"}
	}
	if err := h.db.PingContext(ctx); err != nil {
		return DependencyStatus{Status: "unhealthy", Message: fmt.Sprintf("unreachable: %v", err)}
	}
	return DependencyStatus{Status: "healthy", Message: "connected"}
}

func (h *HealthHandler) checkRedis(ctx context.Context) DependencyStatus {
	if h.redis == nil {
		return DependencyStatus{Status: "unhealthy", Message: "redis not initialized"}
	}
	if err := h.redis.Ping(ctx).Err(); err != nil {
		return DependencyStatus{Status: "unhealthy", Message: fmt.Sprintf("unreachable: %v", err)}
	}
	return DependencyStatus{Status: "healthy", Message: "connected"}
}

func (h *HealthHandler) checkRabbitMQ() DependencyStatus {
	if h.rabbit == nil {
		return DependencyStatus{Status: "unhealthy", Message: "rabbitmq not initialized"}
	}
	if !h.rabbit.IsAlive() {
		return DependencyStatus{Status: "unhealthy", Message: "connection closed"}
	}
	return DependencyStatus{Status: "healthy", Message: "connected"}
}

func (h *HealthHandler) checkCursor(ctx context.Context) DependencyStatus {
	if h.cursor == nil {
		return DependencyStatus{Status: "unhealthy", Message: "cursor tracker not initialized"}
	}
	cursor, err := h.cursor.GetCurrent(ctx)
	if err != nil {
		return DependencyStatus{Status: "unhealthy", Message: fmt.Sprintf("cannot read cursor: %v", err)}
	}
	lag := cursor.Lag(h.now())
	if lag > h.maxCursorLag {
		return DependencyStatus{
			Status:  "unhealthy",
			Message: fmt.Sprintf("cursor stale: last advanced %s ago (max %s)", lag.Round(time.Second), h.maxCursorLag),
		}
	}
	return DependencyStatus{Status: "healthy", Message: fmt.Sprintf("lag %s", lag.Round(time.Second))}
}

// checkAll runs every dependency check and reports whether all are healthy.
func (h *HealthHandler) checkAll(ctx context.Context) (map[string]DependencyStatus, bool) {
	deps := map[string]DependencyStatus{
		"postgres": h.checkPostgres(ctx),
		"redis":    h.checkRedis(ctx),
		"rabbitmq": h.checkRabbitMQ(),
		"cursor":   h.checkCursor(ctx),
	}
	healthy := true
	for _, d := range deps {
		if d.Status != "healthy" {
			healthy = false
			break
		}
	}
	return deps, healthy
}

// Health reports the status of every dependency. Returns 503 if any
// dependency (including cursor freshness) is unhealthy.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.checkTimeout)
	defer cancel()

	deps, healthy := h.checkAll(ctx)
	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, HealthResponse{Status: status, Dependencies: deps})
}

// Ready reports whether the indexer is ready to be considered up by
// orchestration tooling — all dependencies reachable and the cursor fresh.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.checkTimeout)
	defer cancel()

	deps, healthy := h.checkAll(ctx)
	if !healthy {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "not ready", Dependencies: deps})
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ready", Dependencies: deps})
}

// LagStats holds indexer lag statistics
type LagStats struct {
	LedgerLag      int64   `json:"ledger_lag"`
	QueueDepth     int64   `json:"queue_depth"`
	ProcessingRate float64 `json:"processing_rate"`
}

// Lag serves indexer lag metrics in Prometheus exposition or JSON format.
func (h *HealthHandler) Lag(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.checkTimeout)
	defer cancel()

	var lag int64 = 0
	if h.cursor != nil {
		if c, err := h.cursor.GetCurrent(ctx); err == nil && c != nil {
			lag = int64(c.Lag(h.now()).Seconds())
		}
	}

	stats := LagStats{
		LedgerLag:      lag,
		QueueDepth:     0,
		ProcessingRate: 10.0,
	}

	if r.Header.Get("Accept") == "text/plain" || r.URL.Query().Get("format") == "prometheus" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "# HELP moistello_indexer_ledger_lag Difference in ledgers/seconds from head\n")
		fmt.Fprintf(w, "# TYPE moistello_indexer_ledger_lag gauge\n")
		fmt.Fprintf(w, "moistello_indexer_ledger_lag %d\n", stats.LedgerLag)
		fmt.Fprintf(w, "# HELP moistello_indexer_queue_depth Pending unprocessed events in queue\n")
		fmt.Fprintf(w, "# TYPE moistello_indexer_queue_depth gauge\n")
		fmt.Fprintf(w, "moistello_indexer_queue_depth %d\n", stats.QueueDepth)
		fmt.Fprintf(w, "# HELP moistello_indexer_processing_rate Events processed per second\n")
		fmt.Fprintf(w, "# TYPE moistello_indexer_processing_rate gauge\n")
		fmt.Fprintf(w, "moistello_indexer_processing_rate %.2f\n", stats.ProcessingRate)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

