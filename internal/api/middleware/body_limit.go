package middleware

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/response"
)

const (
	// BodyLimitStateKey is the gin context key holding the *bodyLimitState for
	// the matched route: the effective cap in bytes and whether a handler read
	// past it.
	BodyLimitStateKey = "requestBodyLimitState"

	// DefaultMaxBodyBytes is the fallback cap (4 MB) used when
	// server.max_body_bytes is unset. It matches the value that used to be
	// hardcoded in the HTTP server wrapper (#445), so deployments that never
	// set the new option keep identical behaviour.
	DefaultMaxBodyBytes int64 = 4 * 1024 * 1024
)

// bodyLimitState tracks the cap resolved for a single request.
type bodyLimitState struct {
	limit    int64
	exceeded bool
}

// limitedBody delegates to http.MaxBytesReader and records the overflow.
// Handlers that surface the read error themselves keep their own response;
// BodyLimit only substitutes a typed 413 when the handler stayed silent.
type limitedBody struct {
	rc io.ReadCloser
	st *bodyLimitState
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		b.st.exceeded = true
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// bodyless requests are left alone on purpose: the cap exists to keep
// oversized writes from exhausting memory, and wrapping reads would only cost
// work without protecting anything.
func methodCarriesBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// normaliseRoute canonicalises a route key so that configuration and
// c.FullPath() agree regardless of a stray leading or trailing slash.
func normaliseRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return ""
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if len(route) > 1 {
		route = strings.TrimRight(route, "/")
	}
	return route
}

// BodyLimit caps request bodies on every route that carries one, and lets
// individual routes opt into a different cap.
//
// The effective limit is resolved from the matched route before any handler
// runs, in this order:
//
//  1. overrides[route], keyed by the Gin route pattern
//     ("/v1/circles/:id/contribute"), when that value is positive.
//  2. defaultLimit, or DefaultMaxBodyBytes when defaultLimit is unset.
//
// Overrides are deliberately configuration-driven rather than a separate
// per-group middleware: group middleware only runs *after* the global
// middleware, so a group could not raise the cap in time to stop the
// fail-fast size check below from rejecting the request first.
//
// Exceeding the cap yields a typed 413 REQUEST_BODY_TOO_LARGE problem-details
// response, so clients get the same envelope as every other API error instead
// of a truncated body or a bare 400.
func BodyLimit(defaultLimit int64, overrides map[string]int64) gin.HandlerFunc {
	if defaultLimit <= 0 {
		defaultLimit = DefaultMaxBodyBytes
	}

	// Canonicalise once at construction rather than on every request.
	routeOverrides := make(map[string]int64, len(overrides))
	for route, limit := range overrides {
		if limit > 0 {
			routeOverrides[normaliseRoute(route)] = limit
		}
	}

	return func(c *gin.Context) {
		if !methodCarriesBody(c.Request.Method) || c.Request.Body == nil {
			c.Next()
			return
		}

		limit := defaultLimit
		if override, ok := routeOverrides[normaliseRoute(c.FullPath())]; ok {
			limit = override
		}

		// Reject on the declared length so the caller gets a typed 413
		// straight away and the handler is never entered. Abort is required:
		// returning without it lets Gin's own Next loop run the remaining
		// handlers anyway.
		if c.Request.ContentLength > limit {
			c.Abort()
			response.RequestEntityTooLarge(c, limit)
			return
		}

		st := &bodyLimitState{limit: limit}
		c.Set(BodyLimitStateKey, st)
		c.Request.Body = &limitedBody{
			rc: http.MaxBytesReader(c.Writer, c.Request.Body, limit),
			st: st,
		}

		c.Next()

		// The body was longer than the cap, so the handler saw a read error.
		// Prefer its own response when it produced one — a deliberate 400
		// stays a 400 — but never let an oversized payload pass as a success.
		if st.exceeded && !c.Writer.Written() {
			response.RequestEntityTooLarge(c, limit)
		}
	}
}

// EffectiveBodyLimit reports the byte cap resolved for the current route, or 0
// when no cap was applied. Handlers and tests can read it to advertise or
// assert the limit they are running under.
func EffectiveBodyLimit(c *gin.Context) int64 {
	if v, ok := c.Get(BodyLimitStateKey); ok {
		if st, ok := v.(*bodyLimitState); ok {
			return st.limit
		}
	}
	return 0
}

// ErrBodyTooLarge is the sentinel behind the 413, exposed for handlers that
// inspect the body themselves and would rather propagate it than write the
// response directly.
var ErrBodyTooLarge = apperrors.ErrRequestEntityTooLarge
