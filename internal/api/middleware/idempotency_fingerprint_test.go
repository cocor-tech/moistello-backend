package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/middleware"
)

func newIdempotencyRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-a"); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	return r
}

func sendWithKey(r *gin.Engine, method, path, key, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	r.ServeHTTP(w, req)
	return w
}

// #462: replaying the first response for a different payload would report an
// operation that never ran as successful.
func TestIdempotencyMiddleware_RejectsKeyReuseWithDifferentBody(t *testing.T) {
	r := newIdempotencyRouter(t)
	calls := 0
	r.POST("/withdraw", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	first := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{"amount":"10"}`)
	second := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{"amount":"500"}`)

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusUnprocessableEntity, second.Code)
	assert.Empty(t, second.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, calls, "the conflicting request must not run")
}

func TestIdempotencyMiddleware_RejectsKeyReuseOnDifferentResource(t *testing.T) {
	r := newIdempotencyRouter(t)
	calls := 0
	r.POST("/circles/:id/contribute", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"circle": c.Param("id")})
	})

	first := sendWithKey(r, http.MethodPost, "/circles/a/contribute", "k-1", `{}`)
	second := sendWithKey(r, http.MethodPost, "/circles/b/contribute", "k-1", `{}`)

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusUnprocessableEntity, second.Code, "same route pattern, different circle: not the same request")
	assert.Equal(t, 1, calls)
}

func TestIdempotencyMiddleware_IdenticalRetryReplaysAndHandlerSeesBody(t *testing.T) {
	r := newIdempotencyRouter(t)
	calls := 0
	r.POST("/withdraw", func(c *gin.Context) {
		calls++
		var req struct {
			Amount string `json:"amount"`
		}
		require.NoError(t, c.ShouldBindJSON(&req), "the body must still be readable after fingerprinting")
		c.JSON(http.StatusCreated, gin.H{"amount": req.Amount})
	})

	first := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{"amount":"10"}`)
	retry := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{"amount":"10"}`)

	assert.Equal(t, http.StatusCreated, first.Code)
	assert.Contains(t, first.Body.String(), `"amount":"10"`)
	assert.Equal(t, http.StatusCreated, retry.Code)
	assert.Equal(t, first.Body.String(), retry.Body.String())
	assert.Equal(t, "true", retry.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, calls)
}

func TestIdempotencyMiddleware_IgnoresSafeMethods(t *testing.T) {
	r := newIdempotencyRouter(t)
	calls := 0
	r.GET("/wallets/balance", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"calls": calls})
	})

	first := sendWithKey(r, http.MethodGet, "/wallets/balance", "k-1", "")
	second := sendWithKey(r, http.MethodGet, "/wallets/balance", "k-1", "")

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Empty(t, second.Header().Get("Idempotency-Replayed"), "a read must never be answered from the idempotency cache")
	assert.Equal(t, 2, calls)
}

func TestIdempotencyMiddleware_RejectsOverlongKey(t *testing.T) {
	r := newIdempotencyRouter(t)
	called := false
	r.POST("/withdraw", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	w := sendWithKey(r, http.MethodPost, "/withdraw", strings.Repeat("k", middleware.MaxIdempotencyKeyLength+1), `{}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, called)
}

// A 429 from a per-route limiter happens before the handler runs; caching it
// would turn every retry within the TTL into the same rejection.
func TestIdempotencyMiddleware_RetryRunsAfterRateLimitRejection(t *testing.T) {
	r := newIdempotencyRouter(t)
	limited := true
	calls := 0
	r.POST("/withdraw",
		func(c *gin.Context) {
			if limited {
				c.AbortWithStatus(http.StatusTooManyRequests)
				return
			}
			c.Next()
		},
		func(c *gin.Context) {
			calls++
			c.JSON(http.StatusOK, gin.H{"success": true})
		},
	)

	first := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{}`)
	limited = false
	retry := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{}`)

	assert.Equal(t, http.StatusTooManyRequests, first.Code)
	assert.Equal(t, http.StatusOK, retry.Code)
	assert.Empty(t, retry.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, calls)
}

// A 500 may have happened after side effects (funds moved), so it stays cached:
// re-running the handler on retry could execute the operation twice.
func TestIdempotencyMiddleware_ServerErrorIsReplayedNotReExecuted(t *testing.T) {
	r := newIdempotencyRouter(t)
	calls := 0
	r.POST("/withdraw", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusInternalServerError, gin.H{"success": false})
	})

	first := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{}`)
	retry := sendWithKey(r, http.MethodPost, "/withdraw", "k-1", `{}`)

	assert.Equal(t, http.StatusInternalServerError, first.Code)
	assert.Equal(t, http.StatusInternalServerError, retry.Code)
	assert.Equal(t, "true", retry.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, calls)
}
