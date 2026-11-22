package response_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/response"
)

func init() { gin.SetMode(gin.TestMode) }

// newGin returns a minimal gin engine + recorder pair for a single route.
func newGin(method, path string, h gin.HandlerFunc) (*gin.Engine, *httptest.ResponseRecorder) {
	r := gin.New()
	switch method {
	case http.MethodGet:
		r.GET(path, h)
	case http.MethodPost:
		r.POST(path, h)
	}
	return r, httptest.NewRecorder()
}

func decode(t *testing.T, w *httptest.ResponseRecorder) response.Envelope {
	t.Helper()
	var env response.Envelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	return env
}

// ---------------------------------------------------------------------------
// NewPaginationMeta
// ---------------------------------------------------------------------------

func TestNewPaginationMeta_NormalCase(t *testing.T) {
	m := response.NewPaginationMeta(2, 10, 35)
	assert.Equal(t, 2, m.Page)
	assert.Equal(t, 10, m.Limit)
	assert.Equal(t, 35, m.TotalItems)
	assert.Equal(t, 35, m.Total)
	assert.Equal(t, 4, m.TotalPages)   // ceil(35/10)
	assert.True(t, m.HasMore)         // page*limit=20 < 35
}

func TestNewPaginationMeta_LastPage(t *testing.T) {
	// page 4, limit 10, total 35 – 4*10=40 >= 35 → no more pages
	m := response.NewPaginationMeta(4, 10, 35)
	assert.False(t, m.HasMore)
}

func TestNewPaginationMeta_ZeroLimit_NoPanic(t *testing.T) {
	m := response.NewPaginationMeta(1, 0, 100)
	assert.Equal(t, 0, m.TotalPages, "zero limit → 0 total pages (no division)")
	assert.False(t, m.HasMore, "HasMore requires limit > 0")
}

func TestNewPaginationMeta_ExactlyOnePage(t *testing.T) {
	m := response.NewPaginationMeta(1, 10, 10)
	assert.Equal(t, 1, m.TotalPages)
	assert.False(t, m.HasMore) // 1*10 >= 10
}

func TestNewPaginationMeta_EmptyResultSet(t *testing.T) {
	m := response.NewPaginationMeta(1, 10, 0)
	assert.Equal(t, 0, m.TotalPages)
	assert.False(t, m.HasMore)
}

// ---------------------------------------------------------------------------
// OK and OKWithMeta
// ---------------------------------------------------------------------------

func TestOK_SetsSuccessTrue(t *testing.T) {
	r, _ := newGin(http.MethodGet, "/ok", func(c *gin.Context) { response.OK(c, gin.H{"id": 1}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))

	env := decode(t, w)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, env.Success)
}

func TestOKWithMeta_IncludesMeta(t *testing.T) {
	r := gin.New()
	r.GET("/list", func(c *gin.Context) {
		response.OKWithMeta(c, []string{"a", "b"}, response.NewPaginationMeta(1, 2, 2))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/list", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.NotNil(t, raw["meta"])
}

// ---------------------------------------------------------------------------
// Created
// ---------------------------------------------------------------------------

func TestCreated_Returns201(t *testing.T) {
	r := gin.New()
	r.POST("/items", func(c *gin.Context) { response.Created(c, gin.H{"id": 42}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/items", nil))

	env := decode(t, w)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, env.Success)
}

// ---------------------------------------------------------------------------
// Error helper shortcuts
// ---------------------------------------------------------------------------

func TestBadRequest_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.BadRequest(c, "bad") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "BAD_REQUEST", env.Code)
}

func TestUnauthorized_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.Unauthorized(c, "nope") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", env.Code)
}

func TestForbidden_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.Forbidden(c, "forbidden") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "FORBIDDEN", env.Code)
}

func TestNotFound_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.NotFound(c, "not found") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "NOT_FOUND", env.Code)
}

func TestConflict_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.Conflict(c, "conflict") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "CONFLICT", env.Code)
}

func TestInternalError_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.InternalError(c, "oops") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "INTERNAL_ERROR", env.Code)
}

func TestTokenExpired_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.TokenExpired(c, "expired") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "TOKEN_EXPIRED", env.Code)
}

func TestRateLimitExceeded_Code(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.RateLimitExceeded(c, "slow down") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "RATE_LIMIT_EXCEEDED", env.Code)
}

func TestRequestEntityTooLarge_CodeAndDetails(t *testing.T) {
	r := gin.New()
	r.POST("/upload", func(c *gin.Context) { response.RequestEntityTooLarge(c, 4096) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/upload", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, "REQUEST_BODY_TOO_LARGE", env.Code)
	assert.Contains(t, env.Message, "4096")
}

func TestValidationError_CodeAndDetails(t *testing.T) {
	r := gin.New()
	r.POST("/items", func(c *gin.Context) {
		response.ValidationError(c, "validation failed", gin.H{"field": "name"})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/items", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", env.Code)
}

func TestValidationErrors_422(t *testing.T) {
	r := gin.New()
	r.POST("/form", func(c *gin.Context) { response.ValidationErrors(c, "field required") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/form", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", env.Code)
}

func TestErrorWithCode_CustomStatusAndCode(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		response.ErrorWithCode(c, http.StatusServiceUnavailable, "MAINTENANCE", "down for maintenance")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "MAINTENANCE", env.Code)
}

func TestSuccess_AliasForOK(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.Success(c, "data") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, env.Success)
}

// ---------------------------------------------------------------------------
// RFC 9457 field accuracy
// ---------------------------------------------------------------------------

func TestError_RFC9457Fields(t *testing.T) {
	r := gin.New()
	r.GET("/conflict", func(c *gin.Context) {
		response.Conflict(c, "already exists")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/conflict", nil)
	req.Header.Set("X-Request-ID", "req-abc")
	r.ServeHTTP(w, req)

	env := decode(t, w)
	assert.Equal(t, 409, env.Status)
	assert.Equal(t, "Conflict", env.Title)
	assert.Equal(t, "already exists", env.Detail)
	assert.Equal(t, "https://moistello.com/probs/conflict", env.Type)
	assert.Equal(t, "/conflict", env.Instance)
	assert.Equal(t, "req-abc", env.RequestId)
}

func TestError_EmptyCodeFallsBackToErrorCode(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		response.Error(c, http.StatusBadRequest, "", "missing code", nil)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	env := decode(t, w)
	assert.Equal(t, "ERROR", env.Code, "empty code must default to ERROR")
	assert.Equal(t, "about:blank", env.Type, "empty code → about:blank type URI")
}

// ---------------------------------------------------------------------------
// ETag / conditional-request on non-GET methods
// ---------------------------------------------------------------------------

func TestOK_NonGetDoesNotSetETag(t *testing.T) {
	r := gin.New()
	r.POST("/items", func(c *gin.Context) { response.OK(c, "created") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/items", nil))
	assert.Empty(t, w.Header().Get("ETag"), "ETag must not be set for POST responses")
}

func TestOK_GetSetsETagAndCacheControl(t *testing.T) {
	r := gin.New()
	r.GET("/data", func(c *gin.Context) { response.OK(c, gin.H{"v": 1}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/data", nil))
	assert.NotEmpty(t, w.Header().Get("ETag"))
	assert.Equal(t, "private, must-revalidate", w.Header().Get("Cache-Control"))
}

func TestOK_WildcardIfNoneMatchReturns304(t *testing.T) {
	r := gin.New()
	r.GET("/v", func(c *gin.Context) { response.OK(c, gin.H{"n": 1}) })

	req := httptest.NewRequest(http.MethodGet, "/v", nil)
	req.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
}

func TestOK_WeakETagMatchReturns304(t *testing.T) {
	r := gin.New()
	r.GET("/v", func(c *gin.Context) { response.OK(c, gin.H{"n": 1}) })

	// First – get the strong ETag.
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/v", nil))
	etag := w1.Header().Get("ETag")
	require.NotEmpty(t, etag)

	// Second – send it as a weak tag (W/"…") which the server must also honour.
	req := httptest.NewRequest(http.MethodGet, "/v", nil)
	req.Header.Set("If-None-Match", fmt.Sprintf("W/%s", etag))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	assert.Equal(t, http.StatusNotModified, w2.Code)
}

// ---------------------------------------------------------------------------
// Request-ID header resolution priority
// ---------------------------------------------------------------------------

func TestOK_RequestIDFromXRequestIDHeader(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.OK(c, "ok") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "upper-rid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	env := decode(t, w)
	assert.Equal(t, "upper-rid", env.RequestId)
}

func TestError_RequestIDFromXRequestIdHeader(t *testing.T) {
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { response.BadRequest(c, "bad") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "lower-rid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	env := decode(t, w)
	assert.Equal(t, "lower-rid", env.RequestId)
}
