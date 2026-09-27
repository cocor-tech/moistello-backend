# Admin API Key Zero-Downtime Rotation Runbook

## Overview
The `/metrics` endpoint and administrative operational interfaces are protected by an `X-Admin-API-Key` header check (`middleware.AdminAPIKeyMiddleware`). 

To eliminate coordinated downtime when rotating admin credentials, Moistello supports **dual admin keys** (`auth.admin_api_key` and `auth.admin_api_key_secondary`). Both keys are independently valid, and every authenticated request emits an audit log and metrics label indicating which key identity (`primary` or `secondary`) was presented.

---

## Metric & Audit Observability
- Metric: `moistello_admin_key_requests_total{identity="primary|secondary"}`
- Request Context: `adminKeyIdentity` (`primary` or `secondary`)
- Debug Log: `authenticated with [primary|secondary] admin API key`

---

## Zero-Downtime Rotation Procedure

### Phase 1: Generate & Stage Secondary Key
1. Generate a new high-entropy 32-byte hex secret:
   ```bash
   openssl rand -hex 32
   ```
2. Configure the new secret as `MOISTELLO_AUTH_ADMIN_API_KEY_SECONDARY` (or `ADMIN_API_KEY_SECONDARY`).
3. Deploy or reload the API server.
4. Verify the server is running and both keys are accepted.

### Phase 2: Migrate Consumers to Secondary Key
1. Update monitoring scrapers (e.g., Prometheus scraper jobs, Datadog agents, health probes) to use the new secondary key in the `X-Admin-API-Key` header.
2. Monitor metrics in Grafana / Prometheus:
   ```promql
   sum by (identity) (rate(moistello_admin_key_requests_total[5m]))
   ```
3. Confirm that traffic on `identity="primary"` drops to zero and traffic on `identity="secondary"` matches total scraper volume.

### Phase 3: Promote Secondary Key to Primary
1. Set `MOISTELLO_AUTH_ADMIN_API_KEY` to the value of the new secret.
2. Unset or clear `MOISTELLO_AUTH_ADMIN_API_KEY_SECONDARY`.
3. Deploy or reload the API server.
4. Verify that requests continue succeeding under `identity="primary"`.

### Phase 4: Revoke Old Key
1. Discard the old primary key from password managers / secret stores.
2. Ensure no legacy scrapers or cron scripts are still attempting to use the deprecated key.
