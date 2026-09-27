package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/middleware"
)

// #393: RequireIdempotencyKey makes the key mandatory on money-moving routes.
func TestRequireIdempotencyKey_RejectsMissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	r := gin.New()
	r.POST("/withdraw", middleware.RequireIdempotencyKey(), func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/withdraw", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, called, "handler must not run without an idempotency key")
}

func TestRequireIdempotencyKey_AcceptsEitherHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/withdraw", middleware.RequireIdempotencyKey(), func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, h := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/withdraw", nil)
		req.Header.Set(h, "k-1")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, h)
	}
}

// #393: the same user + key on two different routes must not cross-replay.
func TestIdempotencyMiddleware_ScopedPerRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	calls := map[string]int{}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-a"); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	for _, p := range []string{"/deposit", "/withdraw"} {
		path := p
		r.POST(path, func(c *gin.Context) {
			calls[path]++
			c.JSON(http.StatusOK, gin.H{"route": path})
		})
	}

	for _, p := range []string{"/deposit", "/withdraw"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", p, nil)
		req.Header.Set("Idempotency-Key", "shared-key")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, p)
		assert.Contains(t, w.Body.String(), p, "must not replay the other route's response")
	}
	assert.Equal(t, 1, calls["/deposit"])
	assert.Equal(t, 1, calls["/withdraw"])
}
