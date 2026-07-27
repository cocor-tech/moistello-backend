package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/pkg/response"
	"github.com/rs/zerolog/log"
)

func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Error().Interface("panic", err).Str("path", c.Request.URL.Path).Msg("panic recovered")
				c.Abort()
				response.InternalError(c, "internal server error")
			}
		}()
		c.Next()
	}
}
