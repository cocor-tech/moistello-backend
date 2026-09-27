# Indexer Extended-Outage Recovery Runbook

This runbook outlines the operational recovery procedure when the Moistello ledger indexer encounters an extended outage or severe lag.

---

## 1. Overview & Decision Flowchart

When the indexer stops or falls behind, pending ledger events accumulate on the Stellar network. When restarted, the indexer must safely catch up without missing contract events or overwhelming downstream message brokers.

```
                  +---------------------------+
                  |  Indexer Outage Detected  |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  |    Step 1: Assess Lag     |
                  |  (Ledgers & Time Lag)     |
                  +-------------+-------------+
                                |
             +------------------+------------------+
             |                                     |
             v                                     v
     [ Lag < 1,000 ledgers ]              [ Lag >= 1,000 ledgers ]
     (Short outage: < 1.5 hours)          (Extended outage: > 1.5 hours)
             |                                     |
             v                                     v
+--------------------------+          +--------------------------+
|  Strategy A: Live Catchup|          | Strategy B: Batched Replay|
|  - Start standard indexer|          | - Scale up batch size    |
|  - Monitor lag reduction |          | - Run catch-up profile   |
+------------+-------------+          +------------+-------------+
             |                                     |
             +------------------+------------------+
                                |
                                v
                  +---------------------------+
                  | Step 2: Cursor Continuity |
                  | Verify sequential replay  |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  | Step 3: Event Audit       |
                  | Validate deduplication    |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  | Step 4: All-Clear Criteria|
                  +---------------------------+
```

---

## 2. Step 1: Assess Lag & Health

### 2.1 Query the Indexer Lag Endpoint

Check the internal lag endpoint exposed by the API server:

```bash
# JSON format
curl -s http://localhost:1100/internal/indexer/lag | jq .

# Prometheus format
curl -s "http://localhost:1100/internal/indexer/lag?format=prometheus"
```

**Response Example:**
```json
{
  "ledgerLag": 2400,
  "queueDepth": 0,
  "processingRate": 10.0
}
```

### 2.2 Inspect Redis Cursor & Head Height

Compare the stored indexer cursor with the latest ledger on Stellar Horizon:

```bash
# 1. Fetch current indexer cursor from Redis
redis-cli GET indexer:cursor

# 2. Fetch current ledger lag in seconds
redis-cli GET indexer:lag_seconds

# 3. Query Stellar Horizon for latest network ledger sequence
curl -s "https://horizon-testnet.stellar.org/ledgers?order=desc&limit=1" | jq '.embedded.records[0].sequence'
```

### 2.3 Calculate Total Ledger Gap

$$\text{Ledger Gap} = \text{Horizon Latest Sequence} - \text{Stored Last Ledger}$$

- **Estimated Time to Recover:**
  $$\text{Recovery Time (s)} = \frac{\text{Ledger Gap}}{\text{Processing Rate (ledgers/s)}}$$

---

## 3. Step 2: Choose Recovery Strategy

### Strategy A: Live Sequential Catchup (Lag < 1,000 ledgers)
For outages under 1.5 hours, the indexer's built-in `Reconciler` automatically detects gaps and catches up without special tuning.

1. Ensure RabbitMQ and Postgres connections are healthy:
   ```bash
   curl -s http://localhost:1100/health | jq .dependencies
   ```
2. Start or restart the indexer service:
   ```bash
   systemctl start moistello-indexer
   # or via docker:
   docker-compose up -d indexer
   ```
3. Watch recovery logs:
   ```bash
   journalctl -u moistello-indexer -f --output=cat
   ```

---

### Strategy B: High-Throughput Batched Backfill (Lag >= 1,000 ledgers)
For extended outages, adjust indexer batching to process ledgers faster while preventing memory spikes:

1. Update configuration environment variables temporarily:
   ```bash
   export MOISTELLO_INDEXER_BATCH_SIZE=200
   export MOISTELLO_INDEXER_POLL_INTERVAL=500ms
   export MOISTELLO_INDEXER_MAX_LEDGERS_PER_CYCLE=2000
   ```
2. Start the indexer with the high-throughput parameters:
   ```bash
   ./cmd/indexer/indexer
   ```
3. Monitor processing speed and ensure database connection pool is not exhausted.

---

## 4. Step 3: Verify Cursor Continuity & Scan for Gaps

Run the following SQL and Redis queries to confirm no ledgers were skipped during the replay.

### 4.1 SQL: Check Sequence Continuity in Processed Contributions & Payouts

```sql
-- Check for unexpected gaps in recorded circle rounds
SELECT 
    circle_id, 
    round_number, 
    COUNT(*) as total_contributions,
    MIN(created_at) as first_recorded,
    MAX(created_at) as last_recorded
FROM contributions
WHERE created_at >= NOW() - INTERVAL '24 hours'
GROUP BY circle_id, round_number
ORDER BY circle_id, round_number;

-- Verify on-chain verification status of recent payouts
SELECT 
    verification_status, 
    COUNT(*) 
FROM payouts 
WHERE created_at >= NOW() - INTERVAL '24 hours'
GROUP BY verification_status;
```

### 4.2 Deduplication Audit

Verify that the indexer deduplication cache correctly suppressed duplicate event hashes:

```bash
# Check deduplication hit metrics in Prometheus
curl -s http://localhost:1100/metrics | grep moistello_indexer
```

---

## 5. Step 4: All-Clear Criteria

Before declaring the outage resolved, verify that all of the following conditions are met:

- [ ] **Ledger Lag:** `GET /internal/indexer/lag` reports `ledgerLag < 10` ledgers (or lag_seconds < 60s).
- [ ] **Health Endpoint:** `GET /health` reports `status: "ok"` and `dependencies.postgres`, `dependencies.redis`, `dependencies.horizon` are all `healthy`.
- [ ] **Event Queue:** RabbitMQ queue depth for `moistello.notifications` and `moistello.events` has normalized to near zero.
- [ ] **WebSocket Broadcasts:** Real-time updates are propagating to active circle rooms without backlog.
- [ ] **No Unhandled Errors:** Indexer log output contains zero `level=error` logs over the last 15 minutes.

---

## 6. Emergency Rollback / Reseed Procedure

If the indexer cursor becomes corrupted or points beyond the valid chain height:

1. Stop the indexer immediately:
   ```bash
   systemctl stop moistello-indexer
   ```
2. Reset the Redis cursor to a known valid ledger sequence:
   ```bash
   # Reset to target sequence (e.g. 500 ledgers prior)
   redis-cli SET indexer:cursor <TARGET_LEDGER_SEQUENCE>
   ```
3. Restart indexer to trigger re-scan with automatic deduplication.
