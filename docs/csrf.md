# CSRF boundary

This document records which routes enforce CSRF validation, which are exempt, and why (#399).

## How CSRF validation works here

`middleware.CSRFTokenValidator` (`internal/api/middleware/csrf.go`) is **bound to the bearer session**:

- Safe methods (`GET`, `HEAD`, `OPTIONS`) pass, **except WebSocket upgrades**, which are always checked.
- Every other request must send `X-CSRF-Token` **and** `Authorization: Bearer <token>`.
- The expected value is read from Redis at `csrf:<sha256(bearer token)>`, and the two are compared in constant time.
- If the key is missing (e.g. the session was revoked), the request is rejected with 403. A Redis error returns 503 (fail closed).

Because the token is tied to a session, CSRF can only be enforced on **authenticated** requests. An anonymous request has no session to validate against.

## Where it is enforced

| Route group (`internal/api/router.go`) | Enforcement |
|---|---|
| `wsRoute` (`GET /ws` upgrade) | Always: `CSRFTokenValidator` |
| `authenticated` (`/v1/...`), including nested `admin` (`/v1/admin/...`) | Always: `CSRFTokenValidator` |
| `optional` (`/v1/circles`, `/v1/consent`) | **When an `Authorization` header is present**: `CSRFTokenValidatorIfAuthenticated`. A signed-in user's `POST /v1/consent` is checked. Anonymous, pre-login consent is not, since there's no session. |

## Exempt state-changing routes

| Route | Why it is exempt |
|---|---|
| `POST /webhooks/incoming/:id` | Server-to-server webhook, verified by the handler |
| `POST /webhooks/yellowcard` | Server-to-server provider webhook, signature-verified by the handler |
| `POST /v1/auth/register` | Creates the session, so no CSRF token can exist yet |
| `POST /v1/auth/register/verify` | Creates the session |
| `POST /v1/auth/refresh` | Exchanges a refresh token for a new session |
| `POST /v1/auth/nonce` | Pre-auth wallet challenge |
| `POST /v1/auth/verify` | Pre-auth wallet signature login; creates the session |
| `POST /v1/claim-name` | Anonymous and not user-bound (allocates the next generated name) |

## Keeping it enforced

`TestRouter_CSRFCoverage` (`internal/api/router_csrf_test.go`) builds the real router and sends **every** registered `POST`/`PUT`/`PATCH`/`DELETE` route an authenticated request **without** `X-CSRF-Token`. Each one must answer `403 missing CSRF token` unless it is listed in `csrfExemptRoutes`. It also fails if an exemption names a route that no longer exists.

When you add a route:

- **State-changing and acting on a user's data:** register it on `authenticated`, or on `optional` if anonymous access is also valid. Nothing else is needed.
- **Genuinely session-less** (webhook, pre-auth step): add it to `csrfExemptRoutes` in the test with a justification, and add it to the table above. Treat this as a security review.
