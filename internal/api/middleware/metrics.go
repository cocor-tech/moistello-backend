package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/pkg/metrics"
)

// PrometheusMiddleware collects Prometheus metrics for HTTP requests,
// including request count, latency histogram, and error rate.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		metrics.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		metrics.HTTPLatencySeconds.WithLabelValues(method, path).Observe(duration)

		if c.Writer.Status() >= 400 {
			metrics.HTTPErrorsTotal.WithLabelValues(method, path, status).Inc()
		}
	}
}
