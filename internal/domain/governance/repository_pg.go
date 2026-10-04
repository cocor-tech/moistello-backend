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

	"github.com/moistello/backend/pkg/apperrors"
)

type pgRepository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepository{db: db}
}

// proposalColumns is the canonical projection for reading a proposal. Every
// read path shares it so a column added here is picked up everywhere at once,
// and the scan order matches scanProposal below: the weight snapshot columns
// (#418) and the execution timelock columns (#414) are both projected.
const proposalColumns = `id, title, description, proposal_type, creator_id, status,
	for_votes, against_votes, for_weight, against_weight,
	snapshot_at, snapshot_total_weight, executed_at, executable_at, cancelled_at,
	created_at, updated_at`

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

// loadCancellationVotes populates p.CancellationVotes. A failure is
// non-fatal: the proposal is still readable without the count, and the
// timelock fields that gate execution are on the proposal row itself.
func (r *pgRepository) loadCancellationVotes(ctx context.Context, p *Proposal) {
	count, err := r.CountCancellationVotes(ctx, p.ID)
	if err != nil {
		log.Warn().Err(err).Str("proposalID", p.ID.String()).
			Msg("governance: counting cancellation votes failed")
		return
	}
	p.CancellationVotes = count
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
	r.loadCancellationVotes(ctx, &p)
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
	//
	// The cancellation counts (#414) are populated in the same pass so a client
	// can show how close a proposal in its timelock window is to being cancelled.
	for i := range proposals {
		if err := r.loadWeightSnapshot(ctx, &proposals[i]); err != nil {
			log.Warn().Err(err).Str("proposalID", proposals[i].ID.String()).
				Msg("listing governance proposals: loading weight snapshot failed")
		}
		r.loadCancellationVotes(ctx, &proposals[i])
	}
	return proposals, total, nil
}

// MarkPassed moves a proposal from pending to passed and stamps the moment it
// becomes executable (#414). Conditional on still being pending so two
// concurrent callers cannot both open a timelock.
func (r *pgRepository) MarkPassed(ctx context.Context, id uuid.UUID, executableAt time.Time) error {
	query := `
		UPDATE governance_proposals
		SET status = $1, executable_at = $2, updated_at = NOW()
		WHERE id = $3 AND status = $4
	`
	res, err := r.db.ExecContext(ctx, query, ProposalStatusPassed, executableAt, id, ProposalStatusPending)
	if err != nil {
		return fmt.Errorf("marking governance proposal passed: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Someone else already moved it out of pending.
		return apperrors.ErrConflict
	}
	return nil
}

// MarkCancelled moves a proposal from passed to cancelled (#414). Conditional
// on still being passed, so a proposal that has already been executed — or
// already cancelled — cannot be cancelled after the fact.
func (r *pgRepository) MarkCancelled(ctx context.Context, id uuid.UUID, cancelledAt time.Time) error {
	query := `
		UPDATE governance_proposals
		SET status = $1, cancelled_at = $2, updated_at = NOW()
		WHERE id = $3 AND status = $4
	`
	res, err := r.db.ExecContext(ctx, query, ProposalStatusCancelled, cancelledAt, id, ProposalStatusPassed)
	if err != nil {
		return fmt.Errorf("cancelling governance proposal: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return apperrors.ErrConflict
	}
	return nil
}

// RecordCancellationVote records a member's vote to cancel, reporting false if
// they had already voted to cancel (#414).
func (r *pgRepository) RecordCancellationVote(ctx context.Context, proposalID, voterID uuid.UUID) (bool, error) {
	query := `
		INSERT INTO governance_cancellation_votes (proposal_id, voter_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (proposal_id, voter_id) DO NOTHING
	`
	res, err := r.db.ExecContext(ctx, query, proposalID, voterID)
	if err != nil {
		return false, fmt.Errorf("recording governance cancellation vote: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	// DO NOTHING affects 0 rows when the vote already existed, which is how a
	// repeat vote is detected without a separate existence check.
	return rows > 0, nil
}

// CountCancellationVotes returns how many members have voted to cancel.
func (r *pgRepository) CountCancellationVotes(ctx context.Context, proposalID uuid.UUID) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM governance_cancellation_votes WHERE proposal_id = $1`
	if err := r.db.GetContext(ctx, &count, query, proposalID); err != nil {
		return 0, fmt.Errorf("counting governance cancellation votes: %w", err)
	}
	return count, nil
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
