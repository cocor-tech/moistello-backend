-- Issue #418: snapshot voting weight at proposal creation.
--
-- Votes previously counted weight implicitly (one vote per user) and the tally
-- was a head count. Weight now means something — governance-token balance
-- scaled by reputation tier — so it must be frozen when the proposal is
-- created. Otherwise a holder can borrow tokens, vote, return them, and the
-- outcome shifts (flash-vote gaming).
--
-- The snapshot is the source of truth for every vote on a proposal. Weight
-- moved after creation does not change the power of any in-flight vote.

-- Weighted tallies on the proposal. These are the authoritative decision
-- inputs; for_votes/against_votes remain as head counts for display.
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS for_weight BIGINT NOT NULL DEFAULT 0;
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS against_weight BIGINT NOT NULL DEFAULT 0;

-- When the snapshot was taken, and the total weight it captured. snapshot_at
-- is what makes "which moment governs this proposal" answerable by a reader.
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS snapshot_at TIMESTAMPTZ;
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS snapshot_total_weight BIGINT NOT NULL DEFAULT 0;

-- Per-voter snapshot, in its own table rather than a JSONB column: it is
-- looked up by (proposal_id, voter_id) on every vote and grows with the
-- electorate, so a child table keeps the proposal row small and the lookup
-- indexed.
CREATE TABLE IF NOT EXISTS governance_weight_snapshots (
    proposal_id UUID NOT NULL REFERENCES governance_proposals(id) ON DELETE CASCADE,
    voter_id     UUID NOT NULL,
    weight       BIGINT NOT NULL CHECK (weight >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (proposal_id, voter_id)
);

CREATE INDEX IF NOT EXISTS idx_governance_weight_snapshots_voter
    ON governance_weight_snapshots(voter_id);

-- The weight a vote was actually cast at, kept on the vote row so the audit
-- trail preserves the power used even if a snapshot row is later corrected.
ALTER TABLE governance_votes
    ADD COLUMN IF NOT EXISTS weight BIGINT NOT NULL DEFAULT 1;
