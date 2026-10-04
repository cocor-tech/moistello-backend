-- Issue #414: execution timelock with cancellation window.
--
-- Proposals used to execute the moment a caller POSTed /execute after the vote
-- passed, so a malicious-but-valid proposal could drain before anyone had a
-- chance to read it or exit. This adds a delay between vote-pass and
-- executability, during which the proposal can be cancelled by threshold vote.

-- When a passed proposal becomes executable. Set when the proposal passes, not
-- at creation, so the timelock measures from the decision rather than from when
-- voting opened.
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS executable_at TIMESTAMPTZ;

-- Set when the proposal was cancelled inside the timelock window. NULL for
-- proposals that were never cancellable.
ALTER TABLE governance_proposals
    ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ;

-- Cancellation votes are kept separate from the original for/against votes:
-- they answer a different question ("should this still happen?") and are only
-- meaningful inside the timelock window. Reusing governance_votes would either
-- overwrite the original tally or force a vote column to mean two things.
CREATE TABLE IF NOT EXISTS governance_cancellation_votes (
    proposal_id UUID NOT NULL REFERENCES governance_proposals(id) ON DELETE CASCADE,
    voter_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (proposal_id, voter_id)
);

-- Serves the "how many cancellation votes does this proposal have" count that
-- the threshold check runs on.
CREATE INDEX IF NOT EXISTS idx_governance_cancellation_votes_proposal
    ON governance_cancellation_votes(proposal_id);

-- status is an unconstrained VARCHAR(50) (migration 035), so the new
-- 'cancelled' value needs no enum or CHECK change.
--
-- Note: proposals in the timelock window carry status 'passed', which was
-- already declared in the domain but never assigned before this change.
--
-- executable_at is queried directly by the sweeper that finds proposals whose
-- timelock has elapsed.
CREATE INDEX IF NOT EXISTS idx_governance_proposals_executable_at
    ON governance_proposals(executable_at)
    WHERE executable_at IS NOT NULL;
