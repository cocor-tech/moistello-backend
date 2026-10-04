ALTER TABLE governance_votes
    DROP COLUMN IF EXISTS weight;

DROP TABLE IF EXISTS governance_weight_snapshots;

ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS snapshot_total_weight;
ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS snapshot_at;
ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS against_weight;
ALTER TABLE governance_proposals
    DROP COLUMN IF EXISTS for_weight;
