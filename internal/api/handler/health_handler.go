package handler

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	db    *sql.DB
	redis *redis.Client
}

func NewHealthHandler(db *sql.DB, rds *redis.Client) *HealthHandler {
	return &HealthHandler{db: db, redis: rds}
}

// @Summary Health check
// @Description Liveness probe — returns 200 if service is running.
// @Tags Health
// @Produce json
// @Success 200 {object} object{status=string}
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
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
	if err := h.db.PingContext(ctx); err != nil {
		c.JSON(503, gin.H{"status": "not ready", "error": "database unreachable"})
		return
	}
	if err := h.redis.Ping(ctx).Err(); err != nil {
		c.JSON(503, gin.H{"status": "not ready", "error": "redis unreachable"})
		return
	}
	c.JSON(200, gin.H{"status": "ready"})
}
