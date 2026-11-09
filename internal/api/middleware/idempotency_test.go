package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/middleware"
)

func newTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestIdempotencyMiddleware_ConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)
	ctx := context.Background()

	key := "test-concurrent-key-" + time.Now().Format(time.RFC3339Nano)
	rdb.Del(ctx, "idempotency:"+key)
	defer rdb.Del(ctx, "idempotency:"+key)

	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		// Simulate processing work
		time.Sleep(50 * time.Millisecond)
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	const numRequests = 10
	var wg sync.WaitGroup
	statusCodes := make(chan int, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/submit", nil)
			req.Header.Set("X-Idempotency-Key", key)
			r.ServeHTTP(w, req)
			statusCodes <- w.Code
		}()
	}

	wg.Wait()
	close(statusCodes)

	okCount := 0
	conflictCount := 0
	for code := range statusCodes {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		}
	}

	assert.Equal(t, 1, okCount, "Exactly one concurrent request with the same idempotency key must succeed")
	assert.Equal(t, numRequests-1, conflictCount, "All other concurrent requests must receive 409 Conflict")
}

func TestIdempotencyMiddleware_NoHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIdempotencyMiddleware_DifferentKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)
	ctx := context.Background()

	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	key1 := "key-a-" + time.Now().Format(time.RFC3339Nano)
	key2 := "key-b-" + time.Now().Format(time.RFC3339Nano)
	defer rdb.Del(ctx, "idempotency:"+key1, "idempotency:"+key2)

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/submit", nil)
	req1.Header.Set("Idempotency-Key", key1)
	r.ServeHTTP(w1, req1)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/submit", nil)
	req2.Header.Set("Idempotency-Key", key2)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestIdempotencyMiddleware_ReplaysStoredResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)
	ctx := context.Background()

	key := "test-replay-key-" + time.Now().Format(time.RFC3339Nano)
	rdb.Del(ctx, "idempotency:user-1:"+key)
	defer rdb.Del(ctx, "idempotency:user-1:"+key)

	callCount := 0
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		callCount++
		c.JSON(http.StatusCreated, gin.H{"success": true, "callCount": callCount})
	})

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/submit", nil)
	req1.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(w1, req1)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/submit", nil)
	req2.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusCreated, w1.Code)
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "replayed response body must match the original")
	assert.Equal(t, "true", w2.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, callCount, "the handler must only run once; the second request is a replay")
}

func TestIdempotencyMiddleware_ScopedPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)
	ctx := context.Background()

	key := "test-scope-key-" + time.Now().Format(time.RFC3339Nano)
	defer rdb.Del(ctx, "idempotency:user-a:"+key, "idempotency:user-b:"+key)

	var currentUser string
	callCount := 0
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", currentUser); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		callCount++
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	currentUser = "user-a"
	wA := httptest.NewRecorder()
	reqA, _ := http.NewRequest("POST", "/submit", nil)
	reqA.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(wA, reqA)

	currentUser = "user-b"
	wB := httptest.NewRecorder()
	reqB, _ := http.NewRequest("POST", "/submit", nil)
	reqB.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(wB, reqB)

	assert.Equal(t, http.StatusOK, wA.Code)
	assert.Equal(t, http.StatusOK, wB.Code, "a different user reusing the same key must not be blocked or served the other user's cached response")
	assert.Equal(t, 2, callCount, "each user's request must run the handler independently")
}

func TestIdempotencyMiddleware_FailsClosedWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:19999", DB: 0})
	defer func() { _ = rdb.Close() }()

	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", nil)
	req.Header.Set("X-Idempotency-Key", "any-key")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, false, resp["success"])
}

func TestIdempotencyMiddleware_SkipsNonMutatingMethods(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	calls := 0
	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.GET("/items", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"calls": calls})
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/items", nil)
		req.Header.Set("Idempotency-Key", "get-key")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("Idempotency-Replayed"))
	}
	assert.Equal(t, 2, calls, "non-mutating GET requests must not be cached by IdempotencyMiddleware")
}

func TestIdempotencyMiddleware_RejectsPayloadMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	calls := 0
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/transfer", func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest(http.MethodPost, "/transfer", strings.NewReader(`{"amount":100}`))
	req1.Header.Set("Idempotency-Key", "transfer-key-1")
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Reusing the same key with a different request payload must be rejected with 422.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/transfer", strings.NewReader(`{"amount":500}`))
	req2.Header.Set("Idempotency-Key", "transfer-key-1")
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnprocessableEntity, w2.Code)
	assert.Equal(t, 1, calls, "handler must not execute when payload mismatches")
}

func TestIdempotencyMiddleware_ReleasesKeyOn5xxSoClientCanRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	calls := 0
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/flaky", func(c *gin.Context) {
		calls++
		if calls == 1 {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "temporary db failure"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "calls": calls})
	})

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest(http.MethodPost, "/flaky", strings.NewReader(`{"op":"run"}`))
	req1.Header.Set("Idempotency-Key", "retry-key")
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusInternalServerError, w1.Code)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/flaky", strings.NewReader(`{"op":"run"}`))
	req2.Header.Set("Idempotency-Key", "retry-key")
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, 2, calls, "5xx response must release key so retry executes the handler")
}

func TestIdempotencyMiddleware_RejectsOversizedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	r := gin.New()
	r.Use(middleware.IdempotencyMiddleware(rdb))
	r.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/submit", nil)
	req.Header.Set("Idempotency-Key", strings.Repeat("k", 256))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestIdempotencyMiddleware_SupportsPutPatchDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedisClient(t)

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		calls := 0
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
		r.Use(middleware.IdempotencyMiddleware(rdb))
		r.Handle(method, "/resource", func(c *gin.Context) {
			calls++
			c.JSON(http.StatusOK, gin.H{"method": method, "calls": calls})
		})

		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(method, "/resource", strings.NewReader(`{"v":1}`))
			req.Header.Set("Idempotency-Key", "key-"+method)
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			if i == 1 {
				assert.Equal(t, "true", w.Header().Get("Idempotency-Replayed"))
			}
		}
		assert.Equal(t, 1, calls, "handler for %s must only run once", method)
	}
}
