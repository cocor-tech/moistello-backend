package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	defaultIdempotencyTTL = 24 * time.Hour
	idempotencyProcessing = "processing"

	// MaxIdempotencyKeyLength bounds client-supplied keys, which are embedded
	// in the Redis key.
	MaxIdempotencyKeyLength = 255
)

// idempotencyRecord is what gets stored in Redis once the original request
// finishes, so a replayed request can be answered without re-running the
// handler.
type idempotencyRecord struct {
	StatusCode  int    `json:"statusCode"`
	Body        []byte `json:"body"`
	ContentType string `json:"contentType"`
	// Fingerprint identifies the request the key was first used for, so reusing
	// the key for a different request is refused instead of replayed.
	Fingerprint string `json:"fingerprint,omitempty"`
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
// for Idempotency-Key or X-Idempotency-Key request headers on mutating requests
// (POST, PUT, PATCH, DELETE). It uses an atomic Redis SET NX operation to claim
// the key so only one concurrent request with a given key actually runs the
// handler — every other concurrent request with the same key gets 409 Conflict
// while the original is still in flight.
//
// Once the original request completes, its response is cached in Redis. A
// later request that reuses the same key gets that cached response replayed
// verbatim instead of re-running the handler — provided it is the same
// request. Reusing a key with a different target or body gets 422 (#462):
// replaying the first response would tell the client an operation it never
// ran had succeeded.
//
// Responses that mean the request was turned away before it ran (429, 503) are
// not cached, so the client's retry with the same key is executed.
//
// Keys are scoped per authenticated user (via GetUserID) so two different
// users can never collide on the same idempotency key. Must run after
// AuthMiddleware so the user ID is available on the context.
func IdempotencyMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := IdempotencyKeyHeader(c)

		// Reads are idempotent by definition; caching them would serve stale data.
		if key == "" || !methodCarriesBody(c.Request.Method) {
			c.Next()
			return
		}
		if len(key) > MaxIdempotencyKeyLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   fmt.Sprintf("Idempotency-Key must be at most %d characters", MaxIdempotencyKeyLength),
			})
			return
		}

		fingerprint, err := requestFingerprint(c)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				// BodyLimit writes its typed 413 once this middleware returns.
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "could not read request body",
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
			replayOrReject(c, ctx, redisClient, redisKey, fingerprint)
			return
		}

		capture := &bodyCapture{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = capture

		c.Next()

		if status := c.Writer.Status(); status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
			redisClient.Del(ctx, redisKey)
			return
		}

		record := idempotencyRecord{
			StatusCode:  c.Writer.Status(),
			Body:        capture.body.Bytes(),
			ContentType: c.Writer.Header().Get("Content-Type"),
			Fingerprint: fingerprint,
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

// requestFingerprint hashes the concrete request target and body, then puts the
// body back for the handler. The target matters because the Redis key carries
// only the route pattern: one key sent to two different circles is two
// different requests.
func requestFingerprint(c *gin.Context) (string, error) {
	var body []byte
	if c.Request.Body != nil {
		b, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return "", err
		}
		body = b
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
	}
	target := append([]byte(c.Request.URL.RequestURI()), 0)
	sum := sha256.Sum256(append(target, body...))
	return hex.EncodeToString(sum[:]), nil
}

// replayOrReject answers a request whose key is already claimed: 409 while the
// original is still in flight (or its record is unreadable), 422 when the key
// was first used for a different request, otherwise the original response
// replayed verbatim.
func replayOrReject(c *gin.Context, ctx context.Context, redisClient *redis.Client, redisKey, fingerprint string) {
	var record idempotencyRecord
	cached, err := redisClient.Get(ctx, redisKey).Result()
	if err != nil || cached == idempotencyProcessing || json.Unmarshal([]byte(cached), &record) != nil {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"success": false,
			"error":   "idempotency key already used or request in progress",
		})
		return
	}

	// Records written before fingerprinting carry none; replay them as before.
	if record.Fingerprint != "" && record.Fingerprint != fingerprint {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error":   "Idempotency-Key was already used for a different request",
		})
		return
	}

	c.Header("Idempotency-Replayed", "true")
	contentType := record.ContentType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(record.StatusCode, contentType, record.Body)
	c.Abort()
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
