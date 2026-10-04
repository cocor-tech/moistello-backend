package circle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

type dbExecutor interface {
	QueryRowxContext(ctx context.Context, query string, args ...interface{}) *sqlx.Row
	QueryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error)
	NamedExecContext(ctx context.Context, query string, arg interface{}) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

type pgRepo struct {
	db dbExecutor
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func NewRepositoryFromTx(tx *sqlx.Tx) Repository {
	return &pgRepo{db: tx}
}

func scanCircle(row interface{ Scan(...interface{}) error }) (*Circle, error) {
	var c Circle
	var contractID, description sql.NullString
	var communityID *uuid.UUID
	var startDate, endDate sql.NullTime
	err := row.Scan(
		&c.ID,
		&contractID,
		&communityID,
		&c.Name,
		&description,
		&c.CircleType,
		&c.PayoutType,
		&c.ContributionAmount,
		&c.Currency,
		&c.Frequency,
		&c.MaxMembers,
		&c.MinMoiScore,
		&c.CollateralPercent,
		&c.LateFeePercent,
		&c.GracePeriodHours,
		&c.MaxStrikes,
		&c.MemberCount,
		&c.RequiresInvite,
		&startDate,
		&endDate,
		&c.Status,
		&c.CurrentRound,
		&c.TotalContributions,
		&c.OrganizerID,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCircleNotFound
		}
		return nil, fmt.Errorf("scanning circle row: %w", err)
	}
	c.ContractID = contractID
	c.CommunityID = communityID
	c.Description = description
	c.StartDate = startDate
	c.EndDate = endDate
	return &c, nil
}

func scanCircleMember(row interface{ Scan(...interface{}) error }) (*CircleMember, error) {
	var m CircleMember
	err := row.Scan(&m.CircleID, &m.UserID, &m.Position, &m.Status, &m.JoinedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("scanning circle member row: %w", err)
	}
	return &m, nil
}

func (r *pgRepo) FindByID(ctx context.Context, id uuid.UUID) (*Circle, error) {
	query := `SELECT id, contract_id, community_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		(SELECT COUNT(*) FROM circle_members WHERE circle_id = circles.id AND status = 'active') as member_count,
		requires_invite,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles WHERE id = $1 AND deleted_at IS NULL`
	return scanCircle(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByContractID(ctx context.Context, contractID string) (*Circle, error) {
	query := `SELECT id, contract_id, community_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		(SELECT COUNT(*) FROM circle_members WHERE circle_id = circles.id AND status = 'active') as member_count,
		requires_invite,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles WHERE contract_id = $1 AND deleted_at IS NULL`
	return scanCircle(r.db.QueryRowxContext(ctx, query, contractID))
}

func (r *pgRepo) List(ctx context.Context, filter CircleFilter) ([]Circle, error) {
	page, limit := 1, 20
	if filter.Page > 0 {
		page = filter.Page
	}
	if filter.Limit > 0 && filter.Limit <= 100 {
		limit = filter.Limit
	}
	offset := (page - 1) * limit

	query := `SELECT id, contract_id, community_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		COALESCE(member_counts.member_count, 0) as member_count,
		requires_invite,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles
		LEFT JOIN (
			SELECT circle_id, COUNT(*) AS member_count
			FROM circle_members
			WHERE status = 'active'
			GROUP BY circle_id
		) member_counts ON member_counts.circle_id = circles.id
		WHERE deleted_at IS NULL`

	var args []interface{}
	var whereClauses []string

	if filter.Search != "" {
		whereClauses = append(whereClauses, "(name ILIKE $"+fmt.Sprint(len(args)+1)+" OR description ILIKE $"+fmt.Sprint(len(args)+2)+")")
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern)
	}
	if filter.Status != "" {
		whereClauses = append(whereClauses, "status = $"+fmt.Sprint(len(args)+1))
		args = append(args, filter.Status)
	}
	if filter.Type != "" {
		whereClauses = append(whereClauses, "circle_type = $"+fmt.Sprint(len(args)+1))
		args = append(args, filter.Type)
	}
	if filter.CommunityID != nil {
		whereClauses = append(whereClauses, "community_id = $"+fmt.Sprint(len(args)+1))
		args = append(args, *filter.CommunityID)
	}
	if filter.OrganizerID != nil {
		whereClauses = append(whereClauses, "organizer_id = $"+fmt.Sprint(len(args)+1))
		args = append(args, *filter.OrganizerID)
	}
	if len(filter.ExcludeIDs) > 0 {
		placeholders := make([]string, len(filter.ExcludeIDs))
		for i, id := range filter.ExcludeIDs {
			placeholders[i] = "$" + fmt.Sprint(len(args)+1+i)
			args = append(args, id)
		}
		whereClauses = append(whereClauses, "id NOT IN ("+strings.Join(placeholders, ",")+")")
	}

	if len(whereClauses) > 0 {
		query += " AND " + strings.Join(whereClauses, " AND ")
	}

	query += " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing circles: %w", err)
	}
	defer rows.Close()

	var circles []Circle
	for rows.Next() {
		c, err := scanCircle(rows)
		if err != nil {
			return nil, err
		}
		circles = append(circles, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating circles: %w", err)
	}
	return circles, nil
}

func (r *pgRepo) Count(ctx context.Context, filter CircleFilter) (int, error) {
	query := "SELECT COUNT(*) FROM circles WHERE deleted_at IS NULL"
	var args []interface{}
	var whereClauses []string

	if filter.Search != "" {
		whereClauses = append(whereClauses, "(name ILIKE $"+fmt.Sprint(len(args)+1)+" OR description ILIKE $"+fmt.Sprint(len(args)+2)+")")
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern)
	}
	if filter.Status != "" {
		whereClauses = append(whereClauses, "status = $"+fmt.Sprint(len(args)+1))
		args = append(args, filter.Status)
	}
	if filter.Type != "" {
		whereClauses = append(whereClauses, "circle_type = $"+fmt.Sprint(len(args)+1))
		args = append(args, filter.Type)
	}
	if filter.CommunityID != nil {
		whereClauses = append(whereClauses, "community_id = $"+fmt.Sprint(len(args)+1))
		args = append(args, *filter.CommunityID)
	}
	if filter.OrganizerID != nil {
		whereClauses = append(whereClauses, "organizer_id = $"+fmt.Sprint(len(args)+1))
		args = append(args, *filter.OrganizerID)
	}

	if len(whereClauses) > 0 {
		query += " AND " + strings.Join(whereClauses, " AND ")
	}

	var count int
	err := r.db.QueryRowxContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting circles: %w", err)
	}
	return count, nil
}

func (r *pgRepo) Create(ctx context.Context, c *Circle) error {
	query := `INSERT INTO circles (id, contract_id, community_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		requires_invite,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at)
		VALUES (:id, :contract_id, :community_id, :name, :description, :circle_type, :payout_type,
		:contribution_amount, :currency, :frequency, :max_members, :min_moi_score,
		:collateral_percent, :late_fee_percent, :grace_period_hours, :max_strikes,
		:requires_invite,
		:start_date, :end_date, :status, :current_round, :total_contributions,
		:organizer_id, :created_at, :updated_at)`
	_, err := r.db.NamedExecContext(ctx, query, c)
	if err != nil {
		if isUniqueViolationPg(err) {
			return apperrors.ErrConflict
		}
		return fmt.Errorf("creating circle: %w", err)
	}
	return nil
}

func (r *pgRepo) Update(ctx context.Context, c *Circle) error {
	query := `UPDATE circles SET name = :name, description = :description,
		circle_type = :circle_type, payout_type = :payout_type,
		contribution_amount = :contribution_amount, currency = :currency,
		frequency = :frequency, max_members = :max_members, min_moi_score = :min_moi_score,
		collateral_percent = :collateral_percent, late_fee_percent = :late_fee_percent,
		grace_period_hours = :grace_period_hours, max_strikes = :max_strikes,
		requires_invite = :requires_invite,
		start_date = :start_date, end_date = :end_date, status = :status,
		current_round = :current_round, total_contributions = :total_contributions,
		updated_at = :updated_at WHERE id = :id`
	result, err := r.db.NamedExecContext(ctx, query, c)
	if err != nil {
		return fmt.Errorf("updating circle: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrCircleNotFound
	}
	return nil
}

func (r *pgRepo) Delete(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE circles SET deleted_at = NOW() WHERE id = $1`
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting circle: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrCircleNotFound
	}
	return nil
}

func (r *pgRepo) CreateMember(ctx context.Context, m *CircleMember) error {
	query := `INSERT INTO circle_members (circle_id, user_id, position, status, joined_at)
		VALUES (:circle_id, :user_id, :position, :status, :joined_at)`
	_, err := r.db.NamedExecContext(ctx, query, m)
	if err != nil {
		if isUniqueViolationPg(err) {
			return ErrAlreadyMember
		}
		return fmt.Errorf("creating circle member: %w", err)
	}
	return nil
}

func (r *pgRepo) GetMembers(ctx context.Context, circleID uuid.UUID) ([]CircleMember, error) {
	query := `SELECT circle_id, user_id, position, status, joined_at
		FROM circle_members WHERE circle_id = $1 ORDER BY position ASC`
	rows, err := r.db.QueryxContext(ctx, query, circleID)
	if err != nil {
		return nil, fmt.Errorf("getting circle members: %w", err)
	}
	defer rows.Close()

	var members []CircleMember
	for rows.Next() {
		m, err := scanCircleMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating circle members: %w", err)
	}
	return members, nil
}

func (r *pgRepo) GetMemberCount(ctx context.Context, circleID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM circle_members WHERE circle_id = $1 AND status = $2 FOR UPDATE`
	var count int
	err := r.db.QueryRowxContext(ctx, query, circleID, MemberStatusActive).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting member count: %w", err)
	}
	return count, nil
}

func (r *pgRepo) UpdateMemberStatus(ctx context.Context, circleID, userID uuid.UUID, status MemberStatus) error {
	query := `UPDATE circle_members SET status = $1 WHERE circle_id = $2 AND user_id = $3`
	result, err := r.db.ExecContext(ctx, query, status, circleID, userID)
	if err != nil {
		return fmt.Errorf("updating member status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotMember
	}
	return nil
}

func (r *pgRepo) FindMemberByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*CircleMember, error) {
	query := `SELECT circle_id, user_id, position, status, joined_at
		FROM circle_members WHERE circle_id = $1 AND user_id = $2`
	return scanCircleMember(r.db.QueryRowxContext(ctx, query, circleID, userID))
}

func (r *pgRepo) FindCirclesByUserID(ctx context.Context, userID uuid.UUID) ([]Circle, error) {
	query := `SELECT c.id, c.contract_id, c.community_id, c.name, c.description, c.circle_type, c.payout_type,
		c.contribution_amount, c.currency, c.frequency, c.max_members, c.min_moi_score,
		c.collateral_percent, c.late_fee_percent, c.grace_period_hours, c.max_strikes,
		(SELECT COUNT(*) FROM circle_members WHERE circle_id = c.id AND status = 'active') as member_count,
		c.requires_invite,
		c.start_date, c.end_date, c.status, c.current_round, c.total_contributions,
		c.organizer_id, c.created_at, c.updated_at
		FROM circles c
		INNER JOIN circle_members cm ON cm.circle_id = c.id
		WHERE cm.user_id = $1 AND cm.status = 'active'
		ORDER BY c.created_at DESC`
	rows, err := r.db.QueryxContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("finding circles by user ID: %w", err)
	}
	defer rows.Close()

	var circles []Circle
	for rows.Next() {
		c, err := scanCircle(rows)
		if err != nil {
			return nil, err
		}
		circles = append(circles, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating circles: %w", err)
	}
	return circles, nil
}

func isUniqueViolationPg(err error) bool {
	if err == nil {
		return false
	}
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == pq.ErrorCode("23505")
	}
	return false
}

func (r *pgRepo) CreatePenalty(ctx context.Context, p *Penalty) error {
	query := `
		INSERT INTO penalties (id, circle_id, user_id, round_number, penalty_type, amount, strikes_applied, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.ExecContext(ctx, query, p.ID, p.CircleID, p.UserID, p.RoundNumber, p.PenaltyType, p.Amount, p.StrikesApplied, p.Reason, p.CreatedAt)
	return err
}

func (r *pgRepo) GetPenaltiesByCircle(ctx context.Context, circleID uuid.UUID) ([]Penalty, error) {
	query := `SELECT id, circle_id, user_id, round_number, penalty_type, amount, strikes_applied, reason, created_at FROM penalties WHERE circle_id = $1 ORDER BY created_at DESC`
	var penalties []Penalty
	err := r.db.SelectContext(ctx, &penalties, query, circleID)
	return penalties, err
}

func (r *pgRepo) GetPenaltiesByUser(ctx context.Context, userID uuid.UUID) ([]Penalty, error) {
	query := `SELECT id, circle_id, user_id, round_number, penalty_type, amount, strikes_applied, reason, created_at FROM penalties WHERE user_id = $1 ORDER BY created_at DESC`
	var penalties []Penalty
	err := r.db.SelectContext(ctx, &penalties, query, userID)
	return penalties, err
}

func (r *pgRepo) GetContributionsByCircleAndRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]uuid.UUID, error) {
	query := `SELECT user_id FROM contributions WHERE circle_id = $1 AND round_number = $2`
	var userIDs []uuid.UUID
	err := r.db.SelectContext(ctx, &userIDs, query, circleID, roundNumber)
	return userIDs, err
}

func (r *pgRepo) CreateDispute(ctx context.Context, dispute *CircleDispute) error {
	query := `
		INSERT INTO circle_disputes (id, circle_id, raiser_id, reason, details, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.ExecContext(ctx, query,
		dispute.ID,
		dispute.CircleID,
		dispute.RaiserID,
		dispute.Reason,
		dispute.Details,
		dispute.Status,
		dispute.CreatedAt,
		dispute.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating dispute: %w", err)
	}
	return nil
}

func (r *pgRepo) CreateVote(ctx context.Context, vote *CircleVote) error {
	query := `
		INSERT INTO circle_votes (id, circle_id, voter_id, recipient_id, round_number, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.ExecContext(ctx, query,
		vote.ID,
		vote.CircleID,
		vote.VoterID,
		vote.RecipientID,
		vote.RoundNumber,
		vote.CreatedAt,
	)
	if err != nil {
		if isUniqueViolationPg(err) {
			return apperrors.ErrConflict
		}
		return fmt.Errorf("creating vote: %w", err)
	}
	return nil
}

func (r *pgRepo) GetVotesByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]CircleVote, error) {
	query := `
		SELECT id, circle_id, voter_id, recipient_id, round_number, created_at
		FROM circle_votes
		WHERE circle_id = $1 AND round_number = $2
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryxContext(ctx, query, circleID, roundNumber)
	if err != nil {
		return nil, fmt.Errorf("getting votes by round: %w", err)
	}
	defer rows.Close()

	var votes []CircleVote
	for rows.Next() {
		var v CircleVote
		if err := rows.StructScan(&v); err != nil {
			return nil, fmt.Errorf("scanning vote: %w", err)
		}
		votes = append(votes, v)
	}
	if votes == nil {
		votes = []CircleVote{}
	}
	return votes, nil
}

func (r *pgRepo) CreateAuctionBid(ctx context.Context, bid *CircleAuctionBid) error {
	query := `
		INSERT INTO circle_auction_bids (id, circle_id, bidder_id, round_number, bid_amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (circle_id, bidder_id, round_number)
		DO UPDATE SET bid_amount = EXCLUDED.bid_amount, created_at = EXCLUDED.created_at
	`
	_, err := r.db.ExecContext(ctx, query,
		bid.ID,
		bid.CircleID,
		bid.BidderID,
		bid.RoundNumber,
		bid.BidAmount,
		bid.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating auction bid: %w", err)
	}
	return nil
}

func (r *pgRepo) GetAuctionBidsByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]CircleAuctionBid, error) {
	query := `
		SELECT id, circle_id, bidder_id, round_number, bid_amount, created_at
		FROM circle_auction_bids
		WHERE circle_id = $1 AND round_number = $2
		ORDER BY bid_amount DESC, created_at ASC
	`
	rows, err := r.db.QueryxContext(ctx, query, circleID, roundNumber)
	if err != nil {
		return nil, fmt.Errorf("getting auction bids by round: %w", err)
	}
	defer rows.Close()

	var bids []CircleAuctionBid
	for rows.Next() {
		var b CircleAuctionBid
		if err := rows.StructScan(&b); err != nil {
			return nil, fmt.Errorf("scanning auction bid: %w", err)
		}
		bids = append(bids, b)
	}
	if bids == nil {
		bids = []CircleAuctionBid{}
	}
	return bids, nil
}

func (r *pgRepo) SaveRoundConfigSnapshot(ctx context.Context, snapshot *RoundConfigSnapshot) error {
	query := `
		INSERT INTO round_config_snapshots (id, circle_id, round_number, config_hash, config_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (circle_id, round_number) DO NOTHING
	`
	_, err := r.db.ExecContext(ctx, query, snapshot.ID, snapshot.CircleID, snapshot.RoundNumber, snapshot.ConfigHash, snapshot.ConfigJSON, snapshot.CreatedAt)
	if err != nil {
		return fmt.Errorf("saving round config snapshot: %w", err)
	}
	return nil
}

func (r *pgRepo) GetRoundConfigSnapshot(ctx context.Context, circleID uuid.UUID, roundNumber int) (*RoundConfigSnapshot, error) {
	query := `SELECT id, circle_id, round_number, config_hash, config_json, created_at
		FROM round_config_snapshots WHERE circle_id = $1 AND round_number = $2`
	var snapshot RoundConfigSnapshot
	err := r.db.GetContext(ctx, &snapshot, query, circleID, roundNumber)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("getting round config snapshot: %w", err)
	}
	return &snapshot, nil
}

type rawContrib struct {
	ID                 uuid.UUID      `db:"id"`
	CircleID           uuid.UUID      `db:"circle_id"`
	UserID             uuid.UUID      `db:"user_id"`
	RoundNumber        int            `db:"round_number"`
	Amount             float64        `db:"amount"`
	TxnHash            sql.NullString `db:"txn_hash"`
	Status             string         `db:"status"`
	OnTime             bool           `db:"on_time"`
	VerifiedOnchain    bool           `db:"verified_onchain"`
	VerificationStatus string         `db:"verification_status"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

type rawPayout struct {
	ID                 uuid.UUID      `db:"id"`
	CircleID           uuid.UUID      `db:"circle_id"`
	RecipientID        uuid.UUID      `db:"recipient_id"`
	RoundNumber        int            `db:"round_number"`
	Amount             float64        `db:"amount"`
	FeeAmount          float64        `db:"fee_amount"`
	TxnHash            sql.NullString `db:"txn_hash"`
	PayoutType         string         `db:"payout_type"`
	VerifiedOnchain    bool           `db:"verified_onchain"`
	VerificationStatus string         `db:"verification_status"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

func (r *pgRepo) GetCircleSnapshot(ctx context.Context, circleID, userID uuid.UUID) (*CircleSnapshot, error) {
	var tx *sqlx.Tx
	var exec dbExecutor = r.db

	if db, ok := r.db.(*sqlx.DB); ok {
		var err error
		tx, err = db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			tx, err = db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				return nil, fmt.Errorf("beginning snapshot transaction: %w", err)
			}
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
		exec = tx
	}

	repo := &pgRepo{db: exec}
	cir, err := repo.FindByID(ctx, circleID)
	if err != nil {
		return nil, err
	}

	members, err := repo.GetMembers(ctx, circleID)
	if err != nil {
		return nil, fmt.Errorf("getting circle members: %w", err)
	}

	var rawContribs []rawContrib
	contribQuery := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE circle_id = $1 ORDER BY round_number ASC, created_at ASC`
	if err := exec.SelectContext(ctx, &rawContribs, contribQuery, circleID); err != nil {
		rawContribs = []rawContrib{}
	}

	var rawPayouts []rawPayout
	payoutQuery := `SELECT id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at, updated_at
		FROM payouts WHERE circle_id = $1 ORDER BY round_number ASC`
	if err := exec.SelectContext(ctx, &rawPayouts, payoutQuery, circleID); err != nil {
		rawPayouts = []rawPayout{}
	}

	var configs []RoundConfigSnapshot
	configQuery := `SELECT id, circle_id, round_number, config_hash, config_json, created_at
		FROM round_config_snapshots WHERE circle_id = $1 ORDER BY round_number ASC`
	if err := exec.SelectContext(ctx, &configs, configQuery, circleID); err != nil {
		configs = []RoundConfigSnapshot{}
	}

	configMap := make(map[int]RoundConfigSnapshot)
	for _, cfg := range configs {
		configMap[cfg.RoundNumber] = cfg
	}

	userRole := "none"
	if cir.OrganizerID == userID {
		userRole = "organizer"
	} else {
		for _, m := range members {
			if m.UserID == userID && m.Status == MemberStatusActive {
				userRole = "member"
				break
			}
		}
	}

	// A snapshot carries the member list and every contribution and payout of
	// the circle, so it is circle-private data: only the organizer and active
	// members may read one. Without this gate any authenticated caller could
	// read any circle's roster and financial history by id (#440).
	if userRole == "none" {
		return nil, apperrors.ErrForbidden
	}

	var userContributed, userPaidOut float64
	var circleContributed, circlePaidOut float64
	isCurrentRoundPaid := false

	contribsByRound := make(map[int][]any)
	for _, c := range rawContribs {
		if c.Status != "failed" {
			circleContributed += c.Amount
			if c.UserID == userID {
				userContributed += c.Amount
				if c.RoundNumber == cir.CurrentRound && (c.Status == "confirmed" || c.Status == "pending" || c.Status == "late") {
					isCurrentRoundPaid = true
				}
			}
		}
		contribMap := map[string]any{
			"id":                 c.ID,
			"circleId":           c.CircleID,
			"userId":             c.UserID,
			"roundNumber":        c.RoundNumber,
			"amount":             c.Amount,
			"txnHash":            c.TxnHash.String,
			"status":             c.Status,
			"onTime":             c.OnTime,
			"verifiedOnchain":    c.VerifiedOnchain,
			"verificationStatus": c.VerificationStatus,
			"createdAt":          c.CreatedAt,
		}
		contribsByRound[c.RoundNumber] = append(contribsByRound[c.RoundNumber], contribMap)
	}

	payoutByRound := make(map[int]any)
	for _, p := range rawPayouts {
		circlePaidOut += p.Amount
		if p.RecipientID == userID {
			userPaidOut += p.Amount
		}
		payoutMap := map[string]any{
			"id":                 p.ID,
			"circleId":           p.CircleID,
			"recipientId":        p.RecipientID,
			"roundNumber":        p.RoundNumber,
			"amount":             p.Amount,
			"feeAmount":          p.FeeAmount,
			"txnHash":            p.TxnHash.String,
			"payoutType":         p.PayoutType,
			"verifiedOnchain":    p.VerifiedOnchain,
			"verificationStatus": p.VerificationStatus,
			"createdAt":          p.CreatedAt,
		}
		payoutByRound[p.RoundNumber] = payoutMap
	}

	maxRound := cir.CurrentRound
	for r := range contribsByRound {
		if r > maxRound {
			maxRound = r
		}
	}
	for r := range payoutByRound {
		if r > maxRound {
			maxRound = r
		}
	}

	rounds := make([]RoundSnapshot, 0, maxRound)
	for r := 1; r <= maxRound; r++ {
		roundStatus := "pending"
		if r < cir.CurrentRound || cir.Status == CircleStatusCompleted {
			roundStatus = "completed"
		} else if r == cir.CurrentRound && cir.Status == CircleStatusActive {
			roundStatus = "active"
		}

		cList := contribsByRound[r]
		if cList == nil {
			cList = []any{}
		}

		var cfgPtr *RoundConfigSnapshot
		if cfg, ok := configMap[r]; ok {
			cfgCopy := cfg
			cfgPtr = &cfgCopy
		}

		rounds = append(rounds, RoundSnapshot{
			RoundNumber:   r,
			Status:        roundStatus,
			Contributions: cList,
			Payout:        payoutByRound[r],
			Config:        cfgPtr,
		})
	}

	pendingContrib := 0.0
	if cir.Status == CircleStatusActive && !isCurrentRoundPaid && (userRole == "member" || userRole == "organizer") {
		pendingContrib = cir.ContributionAmount
	}

	userBalance := &UserBalanceSnapshot{
		UserID:              userID,
		TotalContributed:    userContributed,
		TotalPaidOut:        userPaidOut,
		NetBalance:          userPaidOut - userContributed,
		PendingContribution: pendingContrib,
		IsCurrentRoundPaid:  isCurrentRoundPaid,
	}

	circleBalance := &CircleBalanceSnapshot{
		CircleID:         circleID,
		TotalContributed: circleContributed,
		TotalPaidOut:     circlePaidOut,
		VaultBalance:     circleContributed - circlePaidOut,
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("committing snapshot transaction: %w", err)
		}
		tx = nil
	}

	return &CircleSnapshot{
		Circle:        cir,
		Members:       members,
		Rounds:        rounds,
		UserBalance:   userBalance,
		CircleBalance: circleBalance,
		UserRole:      userRole,
		SnapshotAt:    time.Now().UTC(),
	}, nil
}

func (r *pgRepo) GetBulkCircleSnapshots(ctx context.Context, userID uuid.UUID, circleIDs []uuid.UUID) ([]CircleSnapshot, error) {
	if len(circleIDs) == 0 {
		query := `SELECT DISTINCT circle_id FROM circle_members WHERE user_id = $1
			UNION
			SELECT id AS circle_id FROM circles WHERE organizer_id = $1 AND deleted_at IS NULL`
		var ids []uuid.UUID
		if err := r.db.SelectContext(ctx, &ids, query, userID); err != nil {
			return nil, fmt.Errorf("fetching user circle IDs: %w", err)
		}
		circleIDs = ids
	}

	snapshots := make([]CircleSnapshot, 0, len(circleIDs))
	for _, cid := range circleIDs {
		snap, err := r.GetCircleSnapshot(ctx, cid, userID)
		if err != nil {
			if errors.Is(err, apperrors.ErrNotFound) {
				continue
			}
			return nil, err
		}
		snapshots = append(snapshots, *snap)
	}
	return snapshots, nil
}
