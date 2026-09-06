package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type pgRepository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepository{db: db}
}

func (r *pgRepository) CreateProposal(ctx context.Context, p *Proposal) error {
	query := `
		INSERT INTO governance_proposals (id, title, description, proposal_type, creator_id, status, for_votes, against_votes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		p.ID, p.Title, p.Description, p.ProposalType, p.CreatorID,
		string(p.Status), p.ForVotes, p.AgainstVotes, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting governance proposal: %w", err)
	}
	return nil
}

func (r *pgRepository) GetProposal(ctx context.Context, id uuid.UUID) (*Proposal, error) {
	query := `
		SELECT id, title, description, proposal_type, creator_id, status, for_votes, against_votes, executed_at, created_at, updated_at
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

	query := `
		SELECT id, title, description, proposal_type, creator_id, status, for_votes, against_votes, executed_at, created_at, updated_at
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

func (r *pgRepository) RecordVote(ctx context.Context, proposalID, voterID uuid.UUID, vote bool) error {
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
		INSERT INTO governance_votes (proposal_id, voter_id, vote, created_at)
		VALUES ($1, $2, $3, $4)
	`
	_, err = tx.ExecContext(ctx, insertQuery, proposalID, voterID, vote, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("recording governance vote: %w", err)
	}

	now := time.Now().UTC()
	if vote {
		_, err = tx.ExecContext(ctx, `UPDATE governance_proposals SET for_votes = for_votes + 1, updated_at = $1 WHERE id = $2`, now, proposalID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE governance_proposals SET against_votes = against_votes + 1, updated_at = $1 WHERE id = $2`, now, proposalID)
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
