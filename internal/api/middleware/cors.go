package middleware

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/config"
)

// CORSMiddleware builds the CORS handler for the given environment's policy.
// The allowed origins are resolved per environment in config/cors.go (#348);
// MaxAge comes from configuration and previously was hardcoded to 12h, which
// silently ignored cors.max_age.
func CORSMiddleware(cfg config.CORSConfig) gin.HandlerFunc {
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = 12 * time.Hour
	}
	return cors.New(cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     cfg.AllowedMethods,
		AllowHeaders:     cfg.AllowedHeaders,
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID", "X-API-Version"},
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           maxAge,
	})
}
