package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

type pgRepository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepository{db: db}
}

// proposalColumns is the canonical projection for reading a proposal. Every
// read path shares it so a column added here is picked up everywhere at once.
const proposalColumns = `id, title, description, proposal_type, creator_id, status,
	for_votes, against_votes, for_weight, against_weight,
	snapshot_at, snapshot_total_weight, executed_at, created_at, updated_at`

func (r *pgRepository) CreateProposal(ctx context.Context, p *Proposal) error {
	query := `
		INSERT INTO governance_proposals (id, title, description, proposal_type, creator_id, status, for_votes, against_votes, for_weight, against_weight, snapshot_at, snapshot_total_weight, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	_, err := r.db.ExecContext(ctx, query,
		p.ID, p.Title, p.Description, p.ProposalType, p.CreatorID,
		string(p.Status), p.ForVotes, p.AgainstVotes, p.ForWeight, p.AgainstWeight,
		p.SnapshotAt, p.SnapshotTotalWeight, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting governance proposal: %w", err)
	}

	// The per-voter snapshot is written to its own table rather than as a JSON
	// column on the proposal: it is queried by (proposal, voter) on every vote
	// and grows with the electorate, so a child table keeps the proposal row
	// small and the lookup indexed.
	for voterID, weight := range p.WeightSnapshot {
		if err := r.insertWeight(ctx, p.ID, voterID, weight); err != nil {
			return err
		}
	}
	return nil
}

func (r *pgRepository) insertWeight(ctx context.Context, proposalID, voterID uuid.UUID, weight int64) error {
	query := `
		INSERT INTO governance_weight_snapshots (proposal_id, voter_id, weight)
		VALUES ($1, $2, $3)
		ON CONFLICT (proposal_id, voter_id) DO UPDATE SET weight = EXCLUDED.weight
	`
	if _, err := r.db.ExecContext(ctx, query, proposalID, voterID, weight); err != nil {
		return fmt.Errorf("inserting governance weight snapshot: %w", err)
	}
	return nil
}

// loadWeightSnapshot populates p.WeightSnapshot from the snapshot table. A
// proposal with no rows is not an error: it simply means weighting was not
// active when it was created, and the service falls back to head counts.
func (r *pgRepository) loadWeightSnapshot(ctx context.Context, p *Proposal) error {
	rows, err := r.db.QueryxContext(ctx,
		`SELECT voter_id, weight FROM governance_weight_snapshots WHERE proposal_id = $1`, p.ID)
	if err != nil {
		return fmt.Errorf("loading governance weight snapshot: %w", err)
	}
	defer rows.Close()

	snapshot := make(map[uuid.UUID]int64)
	for rows.Next() {
		var voterID uuid.UUID
		var weight int64
		if err := rows.Scan(&voterID, &weight); err != nil {
			return fmt.Errorf("scanning governance weight snapshot: %w", err)
		}
		snapshot[voterID] = weight
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating governance weight snapshot: %w", err)
	}
	if len(snapshot) > 0 {
		p.WeightSnapshot = snapshot
	}
	return nil
}

func (r *pgRepository) GetProposal(ctx context.Context, id uuid.UUID) (*Proposal, error) {
	query := `SELECT ` + proposalColumns + `
		FROM governance_proposals
		WHERE id = $1
	`
	var p Proposal
	err := r.db.GetContext(ctx, &p, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProposalNotFound
		}
		return nil, fmt.Errorf("getting governance proposal: %w", err)
	}
	if err := r.loadWeightSnapshot(ctx, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pgRepository) ListProposals(ctx context.Context, page, limit int) ([]Proposal, int, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	var total int
	err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM governance_proposals`)
	if err != nil {
		return nil, 0, fmt.Errorf("counting governance proposals: %w", err)
	}

	query := `SELECT ` + proposalColumns + `
		FROM governance_proposals
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	var proposals []Proposal
	err = r.db.SelectContext(ctx, &proposals, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing governance proposals: %w", err)
	}
	if proposals == nil {
		proposals = []Proposal{}
	}
	// Load each proposal's snapshot so readers can see the weight a vote was
	// cast against. A failure here is non-fatal: the list is still useful
	// without the per-voter breakdown, and the proposal's own snapshot columns
	// (snapshot_at, snapshot_total_weight) are already populated.
	for i := range proposals {
		if err := r.loadWeightSnapshot(ctx, &proposals[i]); err != nil {
			log.Warn().Err(err).Str("proposalID", proposals[i].ID.String()).
				Msg("listing governance proposals: loading weight snapshot failed")
		}
	}
	return proposals, total, nil
}

func (r *pgRepository) HasVoted(ctx context.Context, proposalID, voterID uuid.UUID) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM governance_votes WHERE proposal_id = $1 AND voter_id = $2)`
	err := r.db.GetContext(ctx, &exists, query, proposalID, voterID)
	if err != nil {
		return false, fmt.Errorf("checking governance vote existence: %w", err)
	}
	return exists, nil
}

// RecordVoteWeighted records a vote and accumulates both the head count and
// the snapshotted weight (#418).
//
// The weight is supplied by the service from the proposal's creation-time
// snapshot — it is never recomputed here, so a balance change after creation
// cannot retroactively alter a vote already cast. It is also persisted on the
// vote row itself, so the audit trail shows the power actually used even if the
// snapshot table is later altered.
func (r *pgRepository) RecordVoteWeighted(ctx context.Context, proposalID, voterID uuid.UUID, vote bool, weight int64) error {
	voted, err := r.HasVoted(ctx, proposalID, voterID)
	if err != nil {
		return err
	}
	if voted {
		return ErrAlreadyVoted
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning vote transaction: %w", err)
	}
	defer tx.Rollback()

	insertQuery := `
		INSERT INTO governance_votes (proposal_id, voter_id, vote, weight, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err = tx.ExecContext(ctx, insertQuery, proposalID, voterID, vote, weight, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("recording governance vote: %w", err)
	}

	// Both tallies move together in the same statement: the head count for
	// display, the weight for the actual decision.
	now := time.Now().UTC()
	if vote {
		_, err = tx.ExecContext(ctx, `
			UPDATE governance_proposals
			SET for_votes = for_votes + 1, for_weight = for_weight + $1, updated_at = $2
			WHERE id = $3`, weight, now, proposalID)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE governance_proposals
			SET against_votes = against_votes + 1, against_weight = against_weight + $1, updated_at = $2
			WHERE id = $3`, weight, now, proposalID)
	}
	if err != nil {
		return fmt.Errorf("updating proposal vote count: %w", err)
	}

	return tx.Commit()
}

func (r *pgRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status ProposalStatus, executedAt *time.Time) error {
	now := time.Now().UTC()
	if executedAt != nil {
		_, err := r.db.ExecContext(ctx, `UPDATE governance_proposals SET status = $1, executed_at = $2, updated_at = $3 WHERE id = $4`,
			string(status), *executedAt, now, id)
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE governance_proposals SET status = $1, updated_at = $2 WHERE id = $3`,
		string(status), now, id)
	return err
}
