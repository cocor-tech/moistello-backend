package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unknownLengthReader forces ContentLength to -1, mimicking a chunked upload
// where the size is not declared up front.
type unknownLengthReader struct{ r io.Reader }

func (u unknownLengthReader) Read(p []byte) (int, error) { return u.r.Read(p) }

// drainHandler reads the whole body and stays silent, which is the case
// BodyLimit has to catch and turn into a 413.
func drainHandler(c *gin.Context) {
	_, _ = io.Copy(io.Discard, c.Request.Body)
}

func newBodyLimitEngine(t *testing.T, defaultLimit int64, overrides map[string]int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(defaultLimit, overrides))
	return r
}

func postJSON(r *gin.Engine, path string, body string, declaredLength bool) *httptest.ResponseRecorder {
	var reader io.Reader = strings.NewReader(body)
	if !declaredLength {
		reader = unknownLengthReader{strings.NewReader(body)}
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertTooLarge(t *testing.T, w *httptest.ResponseRecorder, limit int64) {
	t.Helper()
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "REQUEST_BODY_TOO_LARGE", "response is not the typed 413 envelope")
	assert.Contains(t, body, `"limitBytes":`+strconv.FormatInt(limit, 10),
		"413 should report the cap that was violated")
	// RFC 9457 problem details, consistent with every other error envelope.
	assert.Contains(t, body, `"type":"https://moistello.com/probs/request-body-too-large"`)
	assert.Contains(t, body, `"title":"Request Entity Too Large"`)
}

func TestBodyLimit_AllowsBodyUnderLimit(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, nil)
	r.POST("/v1/things", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, string(body))
	})

	w := postJSON(r, "/v1/things", strings.Repeat("a", 1000), true)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, w.Body.String(), 1000)
}

// A declared Content-Length over the cap is refused before the handler runs,
// so the handler never has to notice the truncation.
func TestBodyLimit_RejectsDeclaredOversizedBody(t *testing.T) {
	var handlerRan bool
	r := newBodyLimitEngine(t, 1024, nil)
	r.POST("/v1/things", func(c *gin.Context) {
		handlerRan = true
		c.Status(http.StatusOK)
	})

	w := postJSON(r, "/v1/things", strings.Repeat("a", 2048), true)
	assertTooLarge(t, w, 1024)
	assert.False(t, handlerRan, "handler must not run for an oversized body")
}

// A chunked upload has no declared length, so the cap is enforced while the
// handler reads. A handler that reports nothing itself must still get a 413.
func TestBodyLimit_RejectsUndeclaredOversizedBody(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, nil)
	r.POST("/v1/things", drainHandler)

	w := postJSON(r, "/v1/things", strings.Repeat("a", 4096), false)
	assertTooLarge(t, w, 1024)
}

// When the handler does answer, its own status wins: a deliberate 400 must not
// be rewritten into a 413.
func TestBodyLimit_PreservesHandlerErrorResponse(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, nil)
	r.POST("/v1/things", func(c *gin.Context) {
		_, _ = io.Copy(io.Discard, c.Request.Body)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "BAD_REQUEST"})
	})

	w := postJSON(r, "/v1/things", strings.Repeat("a", 4096), false)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "BAD_REQUEST")
}

func TestBodyLimit_ConfigOverrideRaisesLimitForRoute(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, map[string]int64{"/v1/uploads": 8192})
	r.POST("/v1/uploads", drainHandler)
	r.POST("/v1/things", drainHandler)

	// The overridden route accepts more than the global default.
	w := postJSON(r, "/v1/uploads", strings.Repeat("a", 4096), true)
	assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code, "override should allow 4 KB")

	// The default route is still capped.
	assertTooLarge(t, postJSON(r, "/v1/things", strings.Repeat("a", 4096), true), 1024)
}

func TestBodyLimit_ConfigOverrideCanLowerLimit(t *testing.T) {
	r := newBodyLimitEngine(t, 8192, map[string]int64{"/v1/auth/nonce": 64})
	r.POST("/v1/auth/nonce", drainHandler)

	assertTooLarge(t, postJSON(r, "/v1/auth/nonce", strings.Repeat("a", 512), true), 64)
}

// Route keys come from configuration, so a stray slash must not silently
// disable an override.
func TestBodyLimit_OverrideKeyNormalisation(t *testing.T) {
	for _, key := range []string{"/v1/uploads", "v1/uploads", "/v1/uploads/"} {
		r := newBodyLimitEngine(t, 1024, map[string]int64{key: 8192})
		r.POST("/v1/uploads", drainHandler)

		w := postJSON(r, "/v1/uploads", strings.Repeat("a", 4096), true)
		assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code, "override key %q was not matched", key)
	}
}

// An override must be able to raise the cap well above the global default,
// which is the case config-driven overrides exist for.
func TestBodyLimit_ConfigOverrideRaisesLimitWellAboveDefault(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, map[string]int64{"/v1/uploads": 64 * 1024})
	r.POST("/v1/uploads", drainHandler)
	r.POST("/v1/things", drainHandler)

	w := postJSON(r, "/v1/uploads", strings.Repeat("a", 32*1024), true)
	assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code, "override should allow 32 KB")

	// The default route is unaffected by its sibling's override.
	assertTooLarge(t, postJSON(r, "/v1/things", strings.Repeat("a", 32*1024), true), 1024)
}

// Parametrised routes resolve to their Gin pattern, so overrides are keyed by
// "/v1/circles/:id/contribute" rather than a concrete id.
func TestBodyLimit_OverrideMatchesParametrisedRoute(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, map[string]int64{"/v1/circles/:id/contribute": 8192})
	r.POST("/v1/circles/:id/contribute", drainHandler)

	w := postJSON(r, "/v1/circles/abc/contribute", strings.Repeat("a", 4096), true)
	assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code, "parametrised override should apply")
}

func TestBodyLimit_IgnoresNonPositiveOverride(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, map[string]int64{"/v1/things": 0, "/v1/other": -5})
	r.POST("/v1/things", drainHandler)
	r.POST("/v1/other", drainHandler)

	assertTooLarge(t, postJSON(r, "/v1/things", strings.Repeat("a", 2048), true), 1024)
	assertTooLarge(t, postJSON(r, "/v1/other", strings.Repeat("a", 2048), true), 1024)
}

// An unset server.max_body_bytes must keep the historical 4 MB cap rather
// than disabling the limit.
func TestBodyLimit_FallsBackToDefaultWhenUnset(t *testing.T) {
	r := newBodyLimitEngine(t, 0, nil)

	var limit int64
	r.POST("/v1/things", func(c *gin.Context) {
		limit = EffectiveBodyLimit(c)
		c.Status(http.StatusOK)
	})

	postJSON(r, "/v1/things", "{}", true)
	assert.Equal(t, DefaultMaxBodyBytes, limit)
}

// Reads are not capped: the limit exists to stop oversized writes from
// exhausting memory.
func TestBodyLimit_DoesNotLimitReadMethods(t *testing.T) {
	r := newBodyLimitEngine(t, 16, nil)
	r.GET("/v1/things", func(c *gin.Context) {
		c.String(http.StatusOK, strings.Repeat("a", 4096))
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/things", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, w.Body.String(), 4096)
}

func TestBodyLimit_PUTAndPATCHAndDELETEAreCapped(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := newBodyLimitEngine(t, 128, nil)
		r.Handle(method, "/v1/things/:id", drainHandler)

		req := httptest.NewRequest(method, "/v1/things/abc", strings.NewReader(strings.Repeat("a", 512)))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "%s should be capped", method)
	}
}

func TestEffectiveBodyLimit_ZeroWithoutMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var limit int64
	r.GET("/v1/things", func(c *gin.Context) {
		limit = EffectiveBodyLimit(c)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/things", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Zero(t, limit)
}

// A 413 must not leave the connection unusable for the next request.
func TestBodyLimit_RequestAfterRejectionSucceeds(t *testing.T) {
	r := newBodyLimitEngine(t, 1024, nil)
	r.POST("/v1/things", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.String(http.StatusOK, string(body))
	})

	assertTooLarge(t, postJSON(r, "/v1/things", strings.Repeat("a", 4096), true), 1024)

	w := postJSON(r, "/v1/things", "small", true)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "small", w.Body.String())
}
