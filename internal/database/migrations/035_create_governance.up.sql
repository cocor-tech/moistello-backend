CREATE TABLE IF NOT EXISTS governance_proposals (
    id UUID PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    proposal_type VARCHAR(100) NOT NULL,
    creator_id UUID NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    for_votes INT NOT NULL DEFAULT 0,
    against_votes INT NOT NULL DEFAULT 0,
    executed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_governance_proposals_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS governance_votes (
    proposal_id UUID NOT NULL REFERENCES governance_proposals(id) ON DELETE CASCADE,
    voter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vote BOOLEAN NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (proposal_id, voter_id)
);

CREATE INDEX idx_governance_proposals_creator ON governance_proposals(creator_id);
CREATE INDEX idx_governance_proposals_status ON governance_proposals(status);
CREATE INDEX idx_governance_votes_proposal ON governance_votes(proposal_id);
