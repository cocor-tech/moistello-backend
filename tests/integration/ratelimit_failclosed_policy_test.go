package integration_test

// Continues the #406 coverage in ratelimit_failclosed_test.go: the two
// tests here prove the rest of the documented outage matrix (the "safe
// GETs follow their documented policy" action item, and the auth/OTP
// by-construction guarantee) rather than just the direct auth/OTP 503 case.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSafeGetRoute_FollowsDocumentedGlobalPolicy_WhenRedisDown proves the
// second half of #406's action item: "safe GETs follow their documented
// policy." Per docs/rate-limiting.md, fail-closed is the default for every
// route class unless a route explicitly opts into WithFailOpen() — and no
// route in router.go currently does. So the documented policy for /health
// today is the same fail-closed 503, not a silent pass-through. If a future
// change adds a WithFailOpen() override for a public route, this test's
// intent is to be updated alongside it, not treated as proof that GETs are
// always exempt.
func TestSafeGetRoute_FollowsDocumentedGlobalPolicy_WhenRedisDown(t *testing.T) {
	rdb := unreachableRedis()
	defer rdb.Close()

	r := newAuthGroupRouter(rdb, testRateLimitConfig())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "service temporarily unavailable")
}

// TestAuthRoutes_StayFailClosed_EvenIfGlobalConfigFlipsOpen guards the
// specific guarantee router.go's AuthRateLimitMiddleware wiring depends on:
// auth/OTP is fail-closed BY CONSTRUCTION, regardless of the global
// fail_closed config value — an operator flipping the global default to
// fail-open (e.g. to keep public GETs alive during a Redis blip) must not
// silently also stop enforcing auth/OTP limits.
func TestAuthRoutes_StayFailClosed_EvenIfGlobalConfigFlipsOpen(t *testing.T) {
	rdb := unreachableRedis()
	defer rdb.Close()

	cfg := testRateLimitConfig()
	cfg.FailClosed = false // operator flips the global default to fail-open

	r := newAuthGroupRouter(rdb, cfg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/auth/nonce", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "auth/OTP must stay fail-closed even when the global config flips to fail-open")
}
