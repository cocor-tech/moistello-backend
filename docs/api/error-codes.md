# API Error Code Catalog (RFC 9457 Problem Details)

## Overview
Moistello standardizes all HTTP 4xx and 5xx client and server error responses following **RFC 9457 (Problem Details for HTTP APIs)** while maintaining complete backward compatibility with the legacy response envelope.

Client applications (frontend web, mobile wallets, and SDKs) should branch on the machine-readable `code` field or RFC 9457 `type` URI rather than matching on localized or reworded `message` strings.

---

## Response Structure

```json
{
  "success": false,
  "code": "NONCE_EXPIRED",
  "message": "authentication nonce has expired",
  "details": null,
  "requestId": "req-9c87f12a-3b4",
  "type": "https://moistello.com/probs/nonce-expired",
  "title": "Unauthorized",
  "status": 401,
  "detail": "authentication nonce has expired",
  "instance": "/v1/auth/verify"
}
```

---

## Error Code Reference Catalog

### 1. Authentication & Session Errors (401 / 403)

| Code | HTTP Status | Title | Description | Client Action |
|---|---|---|---|---|
| `UNAUTHORIZED` | 401 | Unauthorized | Missing or malformed authorization header. | Prompt user to connect wallet or sign in. |
| `INVALID_CREDENTIALS` | 401 | Unauthorized | Signature verification failed or credentials incorrect. | Request user re-sign the challenge payload. |
| `TOKEN_EXPIRED` | 401 | Unauthorized | Access JWT has expired. | Call `/v1/auth/refresh` with refresh token. |
| `TOKEN_INVALID` | 401 | Unauthorized | Access JWT signature is invalid or claims are malformed. | Force full wallet re-authentication. |
| `NONCE_EXPIRED` | 401 | Unauthorized | Challenge nonce expired (> 5m). | Request fresh nonce via `/v1/auth/nonce`. |
| `FORBIDDEN` | 403 | Forbidden | Caller lacks required role or permission. | Surface permission-denied banner. |
| `ADMIN_ACCESS_REQUIRED` | 403 | Forbidden | Admin role required. | Restrict UI view to admin operators. |

### 2. Validation & Input Errors (400 / 422)

| Code | HTTP Status | Title | Description | Client Action |
|---|---|---|---|---|
| `BAD_REQUEST` | 400 | Bad Request | Request payload is malformed or missing parameters. | Check request syntax and payload types. |
| `VALIDATION_ERROR` | 400 / 422 | Bad Request / Unprocessable | Field-level validation failed (see `details`). | Highlight invalid form fields in UI. |
| `MALFORMED_JSON` | 400 | Bad Request | JSON payload failed to parse. | Fix JSON formatting. |
| `INVALID_INPUT` | 400 | Bad Request | Parameter value out of bounds or invalid format. | Correct parameter values. |

### 3. Resource & Domain Errors (404 / 409)

| Code | HTTP Status | Title | Description | Client Action |
|---|---|---|---|---|
| `NOT_FOUND` | 404 | Not Found | Requested entity (circle, user, wallet, invite) does not exist. | Display 404 state / route away. |
| `CONFLICT` | 409 | Conflict | Resource already exists or concurrent modification. | Refresh state or choose alternate identifier. |
| `CIRCLE_FULL` | 400 / 409 | Conflict | Circle has reached maximum member capacity. | Display circle full indicator. |
| `CIRCLE_NOT_ACTIVE` | 400 | Bad Request | Operation requires active circle state. | Notify user circle is pending or completed. |
| `NOT_A_MEMBER` | 403 | Forbidden | User is not a member of the circle. | Display join circle CTA. |
| `ALREADY_MEMBER` | 409 | Conflict | User has already joined the target circle. | Navigate to circle dashboard. |
| `INVALID_INVITE` | 400 | Bad Request | Invite code is expired or revoked. | Prompt user for valid invite code. |

### 4. Rate Limiting & Protections (429)

| Code | HTTP Status | Title | Description | Client Action |
|---|---|---|---|---|
| `RATE_LIMIT_EXCEEDED` | 429 | Too Many Requests | Request quota exceeded for IP, user, or resource. | Retry after `Retry-After` header interval. |
| `CSRF_TOKEN_INVALID` | 403 | Forbidden | Missing or invalid CSRF token on state-changing request. | Refresh CSRF token session. |
| `IDEMPOTENCY_CONFLICT` | 409 | Conflict | Concurrent request with same idempotency key in progress. | Await original request response. |
