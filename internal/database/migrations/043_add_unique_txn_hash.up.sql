-- Idempotency: ensure a given on-chain txn_hash cannot be recorded twice
-- for contributions or payouts. NULL txn_hash rows are excluded from
-- the uniqueness constraint (partial index).

CREATE UNIQUE INDEX IF NOT EXISTS idx_contributions_txn_hash_unique
    ON contributions (txn_hash)
    WHERE txn_hash IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payouts_txn_hash_unique
    ON payouts (txn_hash)
    WHERE txn_hash IS NOT NULL;
