# Rate Limiting: Outage Policy

## The single policy: fail closed

When Redis is unreachable, the rate limiter **fails closed** by default: a
request that cannot be rate-checked is refused with `503 Service
Unavailable` rather than silently let through.

This is the decision from issue #161. The repository previously had two rate
limiters with opposite security postures for the same intent:

- **Legacy JS** (`middleware/rateLimiter.js`) failed **closed** — a Redis
  outage returned `503` with "Security protection active during system
  maintenance".
- **Go** (`internal/api/middleware/ratelimit.go`) failed **open** — a Redis
  outage fell back to an in-process in-memory limiter.

Two postures for one mechanism is a policy accident, not a decision. The
unified policy is **fail closed**, for two reasons:

1. **A limiter that cannot read its counters has no idea what it is
   enforcing.** Falling open during an outage means every caller is
   unthrottled exactly when the service is degraded — the moment abuse
   becomes most likely and most damaging.
2. **It matches the security-critical use.** The auth/OTP routes depend on
   the limiter to slow credential stuffing. A rate limiter that stops
   limiting during an outage is worse than no rate limiter at all.

Fail-closed is therefore the default for every route class, including auth.

## Configuration

```yaml
rate_limit:
  global: 100
  authenticated: 300
  auth: 10
  fail_closed: true   # default; the single policy
```

`rate_limit.fail_closed` is the global default (`true`). It is applied
consistently by all three middleware constructors:
`RateLimitMiddleware`, `AuthRateLimitMiddleware` (global + authenticated
limits), and `PerResourceRateLimitMiddleware`.

## Per-route overrides

The middleware constructors accept options that override the global default
for one route only. This is the escape hatch for the rare route where a 503
during a Redis outage is worse than an unenforced limit (typically a public,
read-only, non-security path).

```go
// This route fails open: in-memory fallback during a Redis outage.
r.Use(middleware.RateLimitMiddleware(rdb, cfg, middleware.WithFailOpen()))

// This route always fails closed, even if the global config is flipped.
r.Use(middleware.RateLimitMiddleware(rdb, cfg, middleware.WithFailClosed()))
```

`AuthRateLimitMiddleware` is fail-closed **by construction** — it forces the
fail-closed policy unless the route explicitly passes `WithFailOpen()`. The
auth/OTP surface is the case the policy exists for; it must not silently
stop limiting because of a global config change.

## Behaviour matrix

| Redis state | Policy | Result |
|---|---|---|
| Up | either | Sliding-window check; `429` when the limit is exceeded, with `X-RateLimit-*` headers on every response. |
| Down | fail closed (default) | `503 Service Unavailable` with an explicit "rate limiter unavailable" body; `X-RateLimit-*` headers still emitted. |
| Down | fail open (`WithFailOpen()`) | In-memory limiter takes over (per-process, best effort); request proceeds unless the in-memory limit is hit. |

## Why the legacy JS middleware is not changed

`middleware/rateLimiter.js` already implements fail-closed and remains the
reference for the intended posture. The Go middleware — the one the running
server uses — was updated to match it. The JS middleware is legacy and is
kept as-is except for this documentation.
