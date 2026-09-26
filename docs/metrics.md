# Business Metrics Documentation

Moistello exports Prometheus business metrics for monitoring contributions, payouts, user registrations, and active users.

## Metrics Catalog

- `moistello_contributions_total{status, currency, method}`: Counter of contribution transactions.
- `moistello_contribution_volume_total{currency}`: Counter of total contribution volume.
- `moistello_payouts_total{status, currency, method}`: Counter of payout transactions.
- `moistello_payout_volume_total{currency}`: Counter of total payout volume.
- `moistello_user_registrations_total{method}`: Counter of user sign-ups.
- `moistello_active_users_current`: Gauge representing active platform users.

## Database Pool Metrics

- `moistello_db_pool_utilization{type}`: Gauge of pool state (`open`, `in_use`, `idle`, `max_open`, `saturation`).
- `moistello_db_pool_wait_total`: Counter of callers that had to wait for a free connection.
- `moistello_db_pool_wait_seconds_total`: Counter of time spent waiting to acquire a connection.
- `moistello_db_idle_in_transaction_connections`: Gauge of connections idle in a transaction for over 60s (leak indicator).
- `moistello_db_pool_alerts_total{kind}`: Counter of alerts raised (`pool_saturated`, `idle_in_transaction`).

Alerts are also logged at warn level with the offending pool numbers.

## Useful Prometheus Queries

- Contribution Rate (per sec):
  `rate(moistello_contributions_total[5m])`
- Payout Volume (sum by currency):
  `sum(rate(moistello_payout_volume_total[1h])) by (currency)`
