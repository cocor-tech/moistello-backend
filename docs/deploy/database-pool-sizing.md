# PostgreSQL Connection Pool Sizing & Metrics Guide

## Overview
Proper connection pool sizing prevents connection exhaustion under peak traffic while avoiding wasted PostgreSQL backend resources during idle periods. Moistello exposes full pool stats to Prometheus and checks pool configuration during startup.

---

## Production Topologies & Sizing Formulas

### 1. Direct PostgreSQL (Single Instance / Replica)
In direct connection mode, each API server replica connects directly to the PostgreSQL instance without an intermediate pooler.

- **Formula**:
  $$\text{MaxOpenConns} = \frac{\text{PostgreSQL max\_connections} - \text{superuser\_reserved\_connections}}{\text{App Replicas}}$$
- **Example**:
  - PostgreSQL `max_connections = 100`, `superuser_reserved = 5`.
  - App Replicas = `3`.
  - Recommended `max_open_conns` per replica = `(100 - 5) / 3 \approx 25-30`.
  - Recommended `max_idle_conns` = `5-10`.
  - Recommended `conn_max_lifetime` = `30m`.

### 2. Connection Pooler (PgBouncer in Transaction Mode)
When using PgBouncer in transaction pooling mode:
- The app instances can configure higher `max_open_conns` (e.g. 50–100) because connections are only held for the duration of a transaction/query.
- PgBouncer maintains a compact server pool to the real database (e.g., `default_pool_size = 20-40`).
- Best for high-concurrency stateless microservices and auto-scaling workloads.

### 3. Primary with Read Replica (`MOISTELLO_DATABASE_REPLICA_URL`)
Moistello routes analytics and reporting queries (`admin.metrics`, `admin.daily_volume`) to a read replica via `postgres.NewReader`.
- **Primary Pool**: Dedicated to OLTP transactions (circles, contributions, payouts, wallets). Sized based on write concurrency.
- **Replica Pool**: Dedicated to read-heavy analytics. Prevents long-running analytical queries from starving transaction pools.

---

## Pool Metrics on `/metrics`

The following Prometheus metrics are exported by `pkg/postgres/pool_monitor.go`:

| Metric Name | Type | Description |
|---|---|---|
| `moistello_db_pool_utilization{type="open"}` | Gauge | Current open connections (in use + idle). |
| `moistello_db_pool_utilization{type="in_use"}` | Gauge | Connections currently active in queries/transactions. |
| `moistello_db_pool_utilization{type="idle"}` | Gauge | Idle connections waiting in pool. |
| `moistello_db_pool_utilization{type="max_open"}` | Gauge | Maximum allowed open connections. |
| `moistello_db_pool_utilization{type="saturation"}` | Gauge | Pool saturation ratio (`in_use / max_open`). |
| `moistello_db_pool_wait_total` | Counter | Cumulative number of times a goroutine had to wait for a connection. |
| `moistello_db_pool_wait_seconds_total` | Counter | Total wait time in seconds spent acquiring connections. |
| `moistello_db_idle_in_transaction_connections` | Gauge | Connections stuck idle in a transaction beyond threshold (leaks). |
| `moistello_db_pool_alerts_total{kind="..."}` | Counter | Total pool alerts (`pool_saturated`, `idle_in_transaction`). |

---

## Startup Invariant Warnings
During server startup, `postgres.ValidatePoolSettings` verifies:
- Unbounded pools (`max_open_conns <= 0`).
- `max_idle_conns > max_open_conns` (which database/sql clamps automatically).
- `max_open_conns >= 50` in direct multi-replica environments to avoid PostgreSQL backend exhaustion.
