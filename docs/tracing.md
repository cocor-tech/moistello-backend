# Tracing

OpenTelemetry traces cover the request pipeline: HTTP handlers, database work,
and Stellar RPC calls, correlated to the request ID that the logs already carry.

## Configuration

`tracing.*` in config controls export:

| Key | Meaning |
| --- | --- |
| `tracing.enabled` | Master switch. When false, `tracing.Init` is a no-op and every helper returns a non-recording span. |
| `tracing.service_name` | `service.name` on the exported resource. |
| `tracing.collector_endpoint` | OTLP/gRPC collector address. |
| `tracing.sample_rate` | `TraceIDRatioBased` ratio, 0.0–1.0. |

Spans are exported over OTLP gRPC. Sampling is a ratio applied by the tracer
provider, so a trace is either fully sampled or not sampled at all, which keeps
a partial trace from being half-visible.

## How a request is correlated

`middleware.RequestID` resolves the incoming `X-Request-ID` or mints a UUID, sets
it on the response, puts it in the context for the log fields, and records it on
the active span as `request.id`. That single attribute is what joins a trace to
its log lines, and to a downstream service that propagates the W3C trace context
that `otelgin` maintains.

## What is instrumented

| Span | Emitted by |
| --- | --- |
| `http.<service>` (root, per request) | `middleware.TracingMiddleware` via `otelgin` |
| `db.transaction` | `postgres.WithTransaction`, `WithTransactionLevel`, `Transactor.WithTransaction` |
| `db.<operation>` | `tracing.WithDBSpan` / `tracing.StartDBSpan` at repository call sites |
| `stellar.<operation>` | Soroban RPC clients (`getEvents`, `getLedgerEntries`) |
| `redis.<operation>` | Redis call sites via `tracing.StartRedisSpan` |
| `http.<operation>` (outbound) | external HTTP calls via `tracing.StartHTTPSpan` |

Every transactional write passes through `postgres.withTransaction`, which is
the single implementation behind the transaction helpers, so a DB span exists
for transactional work without every repository being traced by hand.

Tracing is **disabled-safe**: when no tracer provider is configured,
`tracing.StartSpan` returns a non-recording span, so call sites can be written
unconditionally and `middleware.TracingMiddleware` degrades to a pass-through.

## PII

Span attributes are exported to a collector that is not subject to the
application's access controls, so nothing identifying a person or a credential
is recorded.

Rules applied at the call sites:

- **Outbound URLs go through `tracing.SafeURL`**, which keeps scheme, host and
  path and drops userinfo, query and fragment. That is where API keys, bearer
  tokens, signed parameters and email addresses actually appear.
- **Filters are described, not copied.** A query span records *which* filters
  are active (`db.filter.contract_version = true`), never their values, so a
  caller-supplied contract ID or tx hash cannot ride out in a trace.
- **RPC spans record the method and shape** of a request — the method name, the
  number of contracts, the starting ledger — never the ledger keys or filter
  contents.
- **No request or response bodies, tokens, wallet secrets, or email addresses**
  are attached to spans.

`pkg/tracing/tracing_test.go` guards this. `TestSpanAttributes_CarryNoPII`
enumerates the attributes the instrumented paths actually set and asserts none
of them is PII-shaped by name or leaks a secret by value. **Adding a new
instrumented call site means adding its attributes to that test**, so a careless
attribute is caught at review rather than in production.

## Limitations

- Span attributes are static per call site. Per-row detail (a specific user ID,
  a full SQL statement with bound values) is deliberately not recorded, so a
  trace shows which queries ran and how slow they were, not what they returned.
- The RPC URL is not recorded at all on Stellar spans, so a trace shows the
  operation but not which node served it. Add it through `tracing.SafeURL` if
  that becomes necessary.
- `WithTransaction` cannot attribute a span to a logical operation, only to the
  transaction. Callers wanting finer granularity should add `tracing.StartSpan`
  around the specific work.
