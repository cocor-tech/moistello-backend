# API Error Envelope Specification

All error responses returned by the Moistello Backend API adhere to a single unified contract for frontend consumption:

```json
{
  "success": false,
  "code": "ERROR_CODE",
  "message": "Human-readable description of the error",
  "details": null,
  "requestId": "550e8400-e29b-41d4-a716-446655440000"
}
```

## Standard Error Codes & Status Mappings
- `BAD_REQUEST` (`400 Bad Request`)
- `VALIDATION_ERROR` (`400 Bad Request` or `422 Unprocessable Entity`)
- `UNAUTHORIZED` (`401 Unauthorized`)
- `FORBIDDEN` (`403 Forbidden`)
- `NOT_FOUND` (`404 Not Found`)
- `CONFLICT` (`409 Conflict`)
- `RATE_LIMIT_EXCEEDED` (`429 Too Many Requests`)
- `INTERNAL_ERROR` (`500 Internal Server Error`)

## Request ID Correlation
The `requestId` field is populated from incoming `X-Request-ID` headers or generated uniquely by the logging middleware to facilitate end-to-end trace correlation.
