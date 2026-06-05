package circle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
	var startDate, endDate sql.NullTime
	err := row.Scan(
		&c.ID,
		&contractID,
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
	query := `SELECT id, contract_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles WHERE id = $1`
	return scanCircle(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByContractID(ctx context.Context, contractID string) (*Circle, error) {
	query := `SELECT id, contract_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles WHERE contract_id = $1`
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

	var conditions []string
	var args []interface{}
	argIdx := 1

	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx+1))
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern)
		argIdx += 2
	}
	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, filter.Status)
		argIdx++
	}
	if filter.Type != "" {
		conditions = append(conditions, fmt.Sprintf("circle_type = $%d", argIdx))
		args = append(args, filter.Type)
		argIdx++
	}
	if len(filter.ExcludeIDs) > 0 {
		placeholders := make([]string, len(filter.ExcludeIDs))
		for i, id := range filter.ExcludeIDs {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, id)
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("id NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`SELECT id, contract_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at FROM circles %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)
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
	var conditions []string
	var args []interface{}
	argIdx := 1

	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx+1))
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern)
		argIdx += 2
	}
	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, filter.Status)
		argIdx++
	}
	if filter.Type != "" {
		conditions = append(conditions, fmt.Sprintf("circle_type = $%d", argIdx))
		args = append(args, filter.Type)
		argIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM circles %s", whereClause)
	var count int
	err := r.db.QueryRowxContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting circles: %w", err)
	}
	return count, nil
}

func (r *pgRepo) Create(ctx context.Context, c *Circle) error {
	query := `INSERT INTO circles (id, contract_id, name, description, circle_type, payout_type,
		contribution_amount, currency, frequency, max_members, min_moi_score,
		collateral_percent, late_fee_percent, grace_period_hours, max_strikes,
		start_date, end_date, status, current_round, total_contributions,
		organizer_id, created_at, updated_at)
		VALUES (:id, :contract_id, :name, :description, :circle_type, :payout_type,
		:contribution_amount, :currency, :frequency, :max_members, :min_moi_score,
		:collateral_percent, :late_fee_percent, :grace_period_hours, :max_strikes,
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
	query := `DELETE FROM circles WHERE id = $1`
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
	query := `SELECT COUNT(*) FROM circle_members WHERE circle_id = $1 AND status = $2`
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
	query := `SELECT c.id, c.contract_id, c.name, c.description, c.circle_type, c.payout_type,
		c.contribution_amount, c.currency, c.frequency, c.max_members, c.min_moi_score,
		c.collateral_percent, c.late_fee_percent, c.grace_period_hours, c.max_strikes,
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
