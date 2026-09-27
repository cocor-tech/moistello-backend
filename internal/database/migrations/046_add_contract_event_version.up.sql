-- Record the deployed contract version (the executable WASM hash) on every
-- indexed contract event, so an event stays attributable to the exact code that
-- emitted it. Contract IDs are stable across upgrades, so without this column
-- it is impossible to tell which implementation produced a historical event.
--
-- The column is NOT NULL with a default of 'unknown' rather than nullable:
--
--   * 'unknown' is a real, queryable state. Legacy rows genuinely cannot be
--     attributed (the code that produced them is not recoverable), and marking
--     them explicitly keeps that visible instead of silently NULL.
--   * A non-null column means callers filter with a plain equality comparison
--     and never have to remember an IS NULL branch that returns different rows.
--
-- Adding a column with a non-volatile default is a metadata-only operation in
-- PostgreSQL 11+, so existing rows are backfilled to 'unknown' without a table
-- rewrite.
ALTER TABLE contract_events
    ADD COLUMN IF NOT EXISTS contract_version TEXT NOT NULL DEFAULT 'unknown';

-- Fast lookup by contract version: "which events came from the v2 contract?" and
-- per-version aggregation over the audit log.
CREATE INDEX IF NOT EXISTS idx_contract_events_contract_version
    ON contract_events (contract_version);
