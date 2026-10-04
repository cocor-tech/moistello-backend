DROP INDEX IF EXISTS idx_governance_proposals_executable_at;
DROP TABLE IF EXISTS governance_cancellation_votes;

ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS cancelled_at;
ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS executable_at;
