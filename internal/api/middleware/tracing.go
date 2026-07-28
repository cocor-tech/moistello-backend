package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/pkg/tracing"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// TracingMiddleware returns a Gin middleware that enables OpenTelemetry distributed tracing.
// It propagates trace context from incoming requests and creates spans for each HTTP request.
func TracingMiddleware(serviceName string) gin.HandlerFunc {
	if tracing.GetTracerProvider() == nil {
		// If tracing is not initialized, return a no-op middleware
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return otelgin.Middleware(serviceName)
}
