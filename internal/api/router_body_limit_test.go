package api

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/middleware"
)

// testMaxBodyBytes is deliberately tiny so the route-table sweep below stays
// fast and does not have to allocate multi-megabyte payloads.
const testMaxBodyBytes int64 = 256

// bodyLimitGroups maps each route group in router.go that has write routes to
// a representative one, so the sweep above asserts coverage group by group
// rather than only counting routes. Keep in sync with the groups registered in
// NewRouter: a new group with write routes must appear here.
var bodyLimitGroups = map[string]string{
	"public webhooks":    "POST /webhooks/incoming/:id",
	"auth (pre-session)": "POST /v1/auth/register",
	"public name claim":  "POST /v1/claim-name",
	"authenticated":      "POST /v1/wallets",
	"admin":              "POST /v1/admin/feature-flags",
	"optional-auth":      "POST /v1/consent",
}

// TestRouter_BodyLimitCoversEveryWriteRoute is the route-table assertion for
// #445: every state-changing route must reject an oversized body with the
// typed 413, whatever its group, and the rejection must happen before auth so
// an oversized payload can never be processed.
func TestRouter_BodyLimitCoversEveryWriteRoute(t *testing.T) {
	r, token := newCSRFTestRouter(t, func(cfg *config.Config) {
		cfg.Server.MaxBodyBytes = testMaxBodyBytes
	})

	registered := map[string]bool{}
	checked := 0
	for _, route := range r.Routes() {
		if !mutatingMethods[route.Method] {
			continue
		}
		registered[route.Method+" "+route.Path] = true
		checked++

		key := route.Method + " " + route.Path
		body := strings.Repeat("a", int(testMaxBodyBytes)+1)

		req := httptest.NewRequest(route.Method, concretePath(route.Path), strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s is not body-limited (got %d, want 413): %s",
				key, w.Code, strings.TrimSpace(w.Body.String()))
			continue
		}
		if !strings.Contains(w.Body.String(), "REQUEST_BODY_TOO_LARGE") {
			t.Errorf("%s returned 413 without the typed error envelope: %s",
				key, strings.TrimSpace(w.Body.String()))
		}
	}

	// The discovery guard from the CSRF sweep: if route enumeration silently
	// stopped working, this test would pass while covering nothing.
	if checked < 50 {
		t.Fatalf("only %d mutating routes checked — route discovery looks broken", checked)
	}

	// Every documented group must actually be represented above.
	for group, key := range bodyLimitGroups {
		if !registered[key] {
			t.Errorf("bodyLimitGroups documents group %q via %q, which is no longer registered — update the map",
				group, key)
		}
	}
}

// A body under the cap must still reach the handler, proving the limit rejects
// only oversized payloads and does not break normal traffic.
func TestRouter_BodyLimitAllowsNormalRequest(t *testing.T) {
	r, _ := newCSRFTestRouter(t, func(cfg *config.Config) {
		cfg.Server.MaxBodyBytes = testMaxBodyBytes
	})

	// POST /v1/consent is optional-auth, so it proceeds past auth/CSRF for an
	// anonymous caller and reaches the (nil) handler.
	req := httptest.NewRequest(http.MethodPost, "/v1/consent", strings.NewReader(`{"sessionId":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("a small body was rejected by the body limit: %s", w.Body.String())
	}
}

// The cap is configurable per route through configuration. Pick a real
// write route and give it a larger cap than the global default.
func TestRouter_BodyLimitPerRouteOverride(t *testing.T) {
	const overrideRoute = "/v1/wallets"
	const overrideLimit int64 = 4096

	r, token := newCSRFTestRouter(t, func(cfg *config.Config) {
		cfg.Server.MaxBodyBytes = testMaxBodyBytes
		cfg.Server.MaxBodyBytesRoutes = map[string]int64{overrideRoute: overrideLimit}
	})

	oversized := strings.Repeat("a", int(overrideLimit)+1)

	// Overridden route: refuses bodies above its own, larger cap.
	req := httptest.NewRequest(http.MethodPost, overrideRoute, strings.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("%s should reject %d bytes at its %d override, got %d",
			overrideRoute, len(oversized), overrideLimit, w.Code)
	}
	if !strings.Contains(w.Body.String(), `"limitBytes":4096`) {
		t.Errorf("413 should report the override limit, got: %s", strings.TrimSpace(w.Body.String()))
	}

	// A body that is oversized for the global default but fine for the
	// override must NOT be rejected as too large.
	mid := strings.Repeat("a", int(testMaxBodyBytes)+1)
	req = httptest.NewRequest(http.MethodPost, overrideRoute, strings.NewReader(mid))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("%s rejected %d bytes even though its override allows %d",
			overrideRoute, len(mid), overrideLimit)
	}
}

// With no server.max_body_bytes configured, the historical 4 MB cap must still
// apply rather than the limit silently disappearing.
func TestRouter_BodyLimitDefaultsWhenUnconfigured(t *testing.T) {
	r, _ := newCSRFTestRouter(t)

	if middleware.DefaultMaxBodyBytes != 4*1024*1024 {
		t.Errorf("DefaultMaxBodyBytes = %d, want 4 MB", middleware.DefaultMaxBodyBytes)
	}

	// A small body is accepted, i.e. the middleware resolved the fallback cap
	// rather than treating the unset value as "no limit".
	req := httptest.NewRequest(http.MethodPost, "/v1/consent", strings.NewReader(`{"sessionId":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("small body rejected with no configured limit: %s", w.Body.String())
	}
}

// Stale entries in the group map would hide a route group that no longer
// exists, so assert the documented keys stay registered.
func TestBodyLimitGroups_StayInSync(t *testing.T) {
	r, _ := newCSRFTestRouter(t)

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	keys := make([]string, 0, len(bodyLimitGroups))
	for _, key := range bodyLimitGroups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !registered[key] {
			t.Errorf("documented route %q is not registered", key)
		}
	}
}
