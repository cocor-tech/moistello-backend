package middleware_test

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/redis/go-redis/v9"
)

// A bearer token long enough for the validator's format check (>= 32 chars).
const csrfTestBearer = "bearer-token-0123456789abcdef0123456789"

func newIfAuthenticatedRouter(t *testing.T) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	r := gin.New()
	r.Use(middleware.CSRFTokenValidatorIfAuthenticated(rdb))
	r.POST("/consent", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/consent", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, mr
}

func doCSRF(r *gin.Engine, method, auth, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/consent", strings.NewReader("{}"))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// #399: optional-auth routes enforce CSRF for authenticated callers only.
func TestCSRFTokenValidatorIfAuthenticated(t *testing.T) {
	t.Run("anonymous mutating request passes through", func(t *testing.T) {
		r, _ := newIfAuthenticatedRouter(t)
		if w := doCSRF(r, http.MethodPost, "", ""); w.Code != http.StatusNoContent {
			t.Fatalf("got %d, want 204", w.Code)
		}
	})

	t.Run("authenticated request without a CSRF token is rejected", func(t *testing.T) {
		r, _ := newIfAuthenticatedRouter(t)
		w := doCSRF(r, http.MethodPost, "Bearer "+csrfTestBearer, "")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "missing CSRF token") {
			t.Fatalf("got %d %s, want 403 missing CSRF token", w.Code, w.Body.String())
		}
	})

	t.Run("authenticated request with a wrong CSRF token is rejected", func(t *testing.T) {
		r, mr := newIfAuthenticatedRouter(t)
		mr.Set(fmt.Sprintf("csrf:%x", sha256.Sum256([]byte(csrfTestBearer))), "expected-token")
		w := doCSRF(r, http.MethodPost, "Bearer "+csrfTestBearer, "wrong-token")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invalid CSRF token") {
			t.Fatalf("got %d %s, want 403 invalid CSRF token", w.Code, w.Body.String())
		}
	})

	t.Run("authenticated request with the session's CSRF token passes", func(t *testing.T) {
		r, mr := newIfAuthenticatedRouter(t)
		mr.Set(fmt.Sprintf("csrf:%x", sha256.Sum256([]byte(csrfTestBearer))), "expected-token")
		if w := doCSRF(r, http.MethodPost, "Bearer "+csrfTestBearer, "expected-token"); w.Code != http.StatusNoContent {
			t.Fatalf("got %d %s, want 204", w.Code, w.Body.String())
		}
	})

	t.Run("safe methods are never CSRF-checked", func(t *testing.T) {
		r, _ := newIfAuthenticatedRouter(t)
		if w := doCSRF(r, http.MethodGet, "Bearer "+csrfTestBearer, ""); w.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", w.Code)
		}
	})
}
