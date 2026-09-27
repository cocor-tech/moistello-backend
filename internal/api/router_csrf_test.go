package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/redis/go-redis/v9"
)

// csrfExemptRoutes lists every state-changing route that intentionally runs
// WITHOUT CSRF enforcement, with the reason (#399). CSRF tokens are bound to
// the bearer session, so only routes with no user session to protect belong
// here. Adding a route to this list is a security decision — justify it.
var csrfExemptRoutes = map[string]string{
	"POST /webhooks/incoming/:id":   "server-to-server webhook, verified by the handler",
	"POST /webhooks/yellowcard":     "server-to-server provider webhook, signature-verified by the handler",
	"POST /v1/auth/register":        "creates the session; no CSRF token can exist yet",
	"POST /v1/auth/register/verify": "creates the session; no CSRF token can exist yet",
	"POST /v1/auth/refresh":         "exchanges a refresh token for a new session",
	"POST /v1/auth/nonce":           "pre-auth wallet challenge",
	"POST /v1/auth/verify":          "pre-auth wallet signature login; creates the session",
	"POST /v1/claim-name":           "anonymous, not user-bound (allocates the next generated name)",
}

var mutatingMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// newCSRFTestRouter builds the real router with nil handlers (only method
// values are taken at registration), a fresh ECDSA JWT key and miniredis, and
// returns it with a valid bearer token for an authenticated caller.
//
// Optional mutators adjust the config before the router is built, so other
// route-table tests can reuse this harness (e.g. #445 sets a small body cap).
func newCSRFTestRouter(t *testing.T, mutate ...func(*config.Config)) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshalling public key: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	token, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub":  "11111111-1111-1111-1111-111111111111",
		"role": "admin", // so admin routes get past AdminMiddleware to CSRF-or-not
		"exp":  time.Now().Add(time.Hour).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cfg := &config.Config{}
	cfg.RateLimit.Global = 1_000_000
	cfg.RateLimit.Authenticated = 1_000_000
	cfg.RateLimit.Auth = 1_000_000
	cfg.CORS.AllowedOrigins = []string{"https://app.example"}
	for _, m := range mutate {
		m(cfg)
	}

	// Zero-value handlers: routes only take method values at registration, and
	// the requests under test are stopped by middleware before any handler runs.
	r := NewRouter(cfg, rdb,
		&handler.AuthHandler{}, &handler.UserHandler{}, &handler.CircleHandler{},
		&handler.ContributionHandler{}, &handler.PayoutHandler{}, &handler.InviteHandler{},
		&handler.NotificationHandler{}, &handler.AdminHandler{}, &handler.WebhookHandler{},
		&handler.HealthHandler{}, &handler.PasskeyCredentialHandler{}, &handler.WalletHandler{},
		&handler.DepositHandler{}, &handler.MobileMoneyHandler{}, &handler.ChatHandler{},
		&handler.CommunityHandler{}, &handler.WebSocketHandler{}, &handler.SavingsGoalHandler{},
		&handler.TokenHandler{}, &handler.SwapHandler{}, &handler.GovernanceHandler{},
		&handler.ReputationHandler{}, &handler.ReferralHandler{}, &handler.ConsentHandler{},
		&handler.AdminJobQueueHandler{}, nil, &handler.YellowCardWebhookHandler{},
		pubPEM,
	)
	return r, token
}

func concretePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") || strings.HasPrefix(p, "*") {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

// TestRouter_CSRFCoverage is the route-table assertion for #399: every
// state-changing route must reject an authenticated request that lacks an
// X-CSRF-Token, unless it is explicitly listed in csrfExemptRoutes.
func TestRouter_CSRFCoverage(t *testing.T) {
	r, token := newCSRFTestRouter(t)

	registered := map[string]bool{}
	checked := 0
	for _, route := range r.Routes() {
		if !mutatingMethods[route.Method] {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if _, exempt := csrfExemptRoutes[key]; exempt {
			continue
		}
		checked++

		req := httptest.NewRequest(route.Method, concretePath(route.Path), strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "missing CSRF token") {
			t.Errorf("%s is state-changing but not CSRF-enforced (got %d %s); "+
				"put it behind CSRFTokenValidator or justify it in csrfExemptRoutes",
				key, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}

	if checked < 50 {
		t.Fatalf("only %d mutating routes checked — route discovery looks broken", checked)
	}
	// Stale exemptions would silently widen the boundary if a path is reused.
	for key := range csrfExemptRoutes {
		if !registered[key] {
			t.Errorf("csrfExemptRoutes lists %q, which is no longer registered — remove it", key)
		}
	}
}

// Optional-auth routes must still let anonymous callers through CSRF (they
// have no session to bind a token to), e.g. pre-login cookie consent.
func TestRouter_OptionalAuthAnonymousSkipsCSRF(t *testing.T) {
	r, _ := newCSRFTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/consent", strings.NewReader(`{"sessionId":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "CSRF") {
		t.Fatalf("anonymous POST /v1/consent was CSRF-rejected: %d %s", w.Code, w.Body.String())
	}
}

// The WebSocket upgrade is a GET but still CSRF-checked by the validator.
func TestRouter_WebSocketUpgradeRequiresCSRF(t *testing.T) {
	r, token := newCSRFTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "missing CSRF token") {
		t.Fatalf("websocket upgrade without CSRF token: got %d %s", w.Code, w.Body.String())
	}
}
