package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	defaultIdempotencyTTL   = 24 * time.Hour
	idempotencyProcessing   = "processing"
	maxIdempotencyKeyLength = 255
)

// idempotencyRecord is what gets stored in Redis once the original request
// finishes, so a replayed request can be answered without re-running the
// handler.
type idempotencyRecord struct {
	StatusCode  int    `json:"statusCode"`
	Body        []byte `json:"body"`
	ContentType string `json:"contentType"`
	RequestHash string `json:"requestHash,omitempty"`
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hashRequestBody(c *gin.Context) (string, error) {
	if c.Request == nil || c.Request.Body == nil {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), nil
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return "", err
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	sum := sha256.Sum256(bodyBytes)
	return hex.EncodeToString(sum[:]), nil
}

// bodyCapture tees everything written to the real gin.ResponseWriter into an
// in-memory buffer so the response can be cached after the handler returns.
type bodyCapture struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyCapture) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyCapture) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// IdempotencyMiddleware prevents race conditions and request replay by checking
// for Idempotency-Key or X-Idempotency-Key request headers. It uses an atomic
// Redis SET NX operation to claim the key so only one concurrent request with
// a given key actually runs the handler — every other concurrent request with
// the same key gets 409 Conflict while the original is still in flight.
//
// Once the original request completes, its response is cached in Redis. A
// later (non-concurrent) request that reuses the same key gets that cached
// response replayed verbatim instead of re-running the handler.
//
// Keys are scoped per authenticated user (via GetUserID) so two different
// users can never collide on the same idempotency key. Must run after
// AuthMiddleware so the user ID is available on the context.
func IdempotencyMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request != nil && !isMutatingMethod(c.Request.Method) {
			c.Next()
			return
		}

		key := IdempotencyKeyHeader(c)

		if key == "" {
			c.Next()
			return
		}

		if len(key) > maxIdempotencyKeyLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "idempotency key exceeds maximum length of 255 characters",
			})
			return
		}

		reqHash, err := hashRequestBody(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "failed to read request body",
			})
			return
		}

		scope := GetUserID(c)
		if scope == "" {
			scope = "anon"
		}
		// Scope per user (#198) *and* per route (#393): the same client key
		// reused on a different endpoint must not replay that endpoint's
		// cached response.
		redisKey := fmt.Sprintf("idempotency:%s:%s %s:%s", scope, c.Request.Method, c.FullPath(), key)
		ctx := c.Request.Context()

		// Atomic SET NX: only the first request with this key claims it.
		set, err := redisClient.SetNX(ctx, redisKey, idempotencyProcessing, defaultIdempotencyTTL).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "service unavailable: idempotency store error",
			})
			return
		}

		if !set {
			if !tryReplay(c, ctx, redisClient, redisKey, reqHash) {
				c.AbortWithStatusJSON(http.StatusConflict, gin.H{
					"success": false,
					"error":   "idempotency key already used or request in progress",
				})
			}
			return
		}

		capture := &bodyCapture{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = capture

		completed := false
		defer func() {
			if !completed {
				redisClient.Del(ctx, redisKey)
			}
		}()

		c.Next()
		completed = true

		statusCode := c.Writer.Status()
		if statusCode >= http.StatusInternalServerError {
			// Do not cache transient 5xx responses so the caller can retry
			// with the same Idempotency-Key.
			redisClient.Del(ctx, redisKey)
			return
		}

		record := idempotencyRecord{
			StatusCode:  statusCode,
			Body:        capture.body.Bytes(),
			ContentType: c.Writer.Header().Get("Content-Type"),
			RequestHash: reqHash,
		}
		payload, err := json.Marshal(record)
		if err != nil {
			// Can't cache the response — release the key rather than leave
			// callers stuck behind a "processing" marker for the full TTL.
			redisClient.Del(ctx, redisKey)
			return
		}
		redisClient.Set(ctx, redisKey, payload, defaultIdempotencyTTL)
	}
}

// tryReplay looks up an existing idempotency key. If it still holds the
// "processing" placeholder (or the lookup fails/races with expiry), the
// original request is still in flight or indeterminate and the caller
// should get 409. If it holds a cached response, that response is written
// back verbatim and true is returned. If the cached record has a different
// request body hash, 422 Unprocessable Entity is written and true is returned.
func tryReplay(c *gin.Context, ctx context.Context, redisClient *redis.Client, redisKey, reqHash string) bool {
	cached, err := redisClient.Get(ctx, redisKey).Result()
	if err != nil || cached == idempotencyProcessing {
		return false
	}

	var record idempotencyRecord
	if err := json.Unmarshal([]byte(cached), &record); err != nil {
		return false
	}

	if record.RequestHash != "" && reqHash != "" && record.RequestHash != reqHash {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error":   "idempotency key already used with different request payload",
		})
		return true
	}

	c.Header("Idempotency-Replayed", "true")
	contentType := record.ContentType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(record.StatusCode, contentType, record.Body)
	c.Abort()
	return true
}

// IdempotencyKeyHeader returns the request's idempotency key (Idempotency-Key,
// falling back to X-Idempotency-Key), trimmed; "" if neither is set.
func IdempotencyKeyHeader(c *gin.Context) string {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	return key
}

// RequireIdempotencyKey rejects a request that carries no idempotency key
// with 400 (#393). IdempotencyMiddleware on its own is opt-in — a request
// without a key passes straight through — so a client retry of a
// money-moving call (withdraw, deposit, contribute, swap, vote, bid…) with no
// key would execute twice. Mount this on those routes, after
// IdempotencyMiddleware, to make the key mandatory.
func RequireIdempotencyKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IdempotencyKeyHeader(c) == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "Idempotency-Key header is required for this request",
			})
			return
		}
		c.Next()
	}
}
