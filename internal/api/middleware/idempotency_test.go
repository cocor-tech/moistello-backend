package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/middleware"
)

func TestIdempotencyMiddleware_ConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis is not available at localhost:6379, skipping integration test")
	}

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
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis unavailable")
	}

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
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis unavailable")
	}

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
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis is not available at localhost:6379, skipping integration test")
	}

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
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis is not available at localhost:6379, skipping integration test")
	}

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
