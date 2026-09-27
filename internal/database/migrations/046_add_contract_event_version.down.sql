DROP INDEX IF EXISTS idx_contract_events_contract_version;

ALTER TABLE contract_events
    DROP COLUMN IF EXISTS contract_version;
