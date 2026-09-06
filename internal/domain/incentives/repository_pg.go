package incentives

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type postgresRepository struct {
	db *sqlx.DB
}

func NewPostgresRepository(db *sqlx.DB) Repository {
	return &postgresRepository{db: db}
}

func NewRepository(db *sqlx.DB) Repository {
	return NewPostgresRepository(db)
}

func (r *postgresRepository) Transact(ctx context.Context, fn func(repo Repository) error) error {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()

	txRepo := &postgresRepository{db: r.db}
	_ = txRepo // We can bind tx if needed, or implement tx wrapper. For completeness:
	err = fn(txRepo)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (r *postgresRepository) CreateReferral(ctx context.Context, ref *Referral) error {
	query := `INSERT INTO referrals (id, referrer_id, referral_code, status, created_at) VALUES ($1, $2, $3, $4, NOW())`
	_, err := r.db.ExecContext(ctx, query, ref.ID, ref.ReferrerID, ref.ReferralCode, ref.Status)
	return err
}

func (r *postgresRepository) GetReferralByCode(ctx context.Context, code string) (*Referral, error) {
	query := `SELECT id, referrer_id, COALESCE(referred_id, '00000000-0000-0000-0000-000000000000'), referral_code, status, created_at FROM referrals WHERE referral_code = $1 FOR UPDATE`
	var ref Referral
	err := r.db.GetContext(ctx, &ref, query, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrReferralCodeNotFound
		}
		return nil, err
	}
	return &ref, nil
}

func (r *postgresRepository) GetReferrerByUserID(ctx context.Context, userID uuid.UUID) (*Referral, error) {
	query := `SELECT id, referrer_id, COALESCE(referred_id, '00000000-0000-0000-0000-000000000000'), referral_code, status, created_at FROM referrals WHERE referrer_id = $1`
	var ref Referral
	err := r.db.GetContext(ctx, &ref, query, userID)
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

func (r *postgresRepository) UpdateReferral(ctx context.Context, ref *Referral) error {
	query := `UPDATE referrals SET referred_id = $1, status = $2 WHERE referral_code = $3 AND status = 'pending'`
	res, err := r.db.ExecContext(ctx, query, ref.ReferredID, ref.Status, ref.ReferralCode)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrReferralCodeAlreadyUsed
	}
	return nil
}

func (r *postgresRepository) CreateIncentive(ctx context.Context, inc *Incentive) error {
	query := `INSERT INTO incentives (id, user_id, type, status, amount, currency, reference_id, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`
	_, err := r.db.ExecContext(ctx, query, inc.ID, inc.UserID, inc.Type, inc.Status, inc.Amount, inc.Currency, inc.ReferenceID)
	return err
}

func (r *postgresRepository) GetIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	var incentives []Incentive
	query := `SELECT id, user_id, type, amount, currency, reference_id, created_at FROM incentives WHERE user_id = $1`
	err := r.db.SelectContext(ctx, &incentives, query, userID)
	return incentives, err
}

func (r *postgresRepository) GetStreak(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	var streak SavingsStreak
	query := `SELECT id, user_id, current_streak, longest_streak, last_contribution_at, bonus_tier FROM savings_streaks WHERE user_id = $1`
	err := r.db.GetContext(ctx, &streak, query, userID)
	if err != nil {
		return nil, err
	}
	return &streak, nil
}

func (r *postgresRepository) UpsertStreak(ctx context.Context, streak *SavingsStreak) (*SavingsStreak, error) {
	query := `INSERT INTO savings_streaks (id, user_id, current_streak, longest_streak, last_contribution_at, bonus_tier)
	          VALUES ($1, $2, $3, $4, $5, $6)
	          ON CONFLICT (user_id) DO UPDATE SET current_streak = EXCLUDED.current_streak, longest_streak = EXCLUDED.longest_streak, last_contribution_at = EXCLUDED.last_contribution_at, bonus_tier = EXCLUDED.bonus_tier`
	_, err := r.db.ExecContext(ctx, query, streak.ID, streak.UserID, streak.CurrentStreak, streak.LongestStreak, streak.LastContributionAt, streak.BonusTier)
	if err != nil {
		return nil, err
	}
	return streak, nil
}

func (r *postgresRepository) GetConfig(ctx context.Context) (*IncentiveConfig, error) {
	var cfg IncentiveConfig
	query := `SELECT referral_bonus_amount, referral_bonus_currency, circle_completion_bonus, circle_completion_currency, contribution_match_percent, contribution_match_max, streak_bonus_tier1, streak_bonus_tier2, streak_bonus_tier3 FROM incentive_configs LIMIT 1`
	err := r.db.GetContext(ctx, &cfg, query)
	if err != nil {
		// fallback default config
		return &IncentiveConfig{
			ReferralBonusAmount:      5.0,
			ReferralBonusCurrency:    "USDC",
			CircleCompletionBonus:    10.0,
			CircleCompletionCurrency: "USDC",
			ContributionMatchPercent: 10.0,
			ContributionMatchMax:     50.0,
			StreakBonusTier1:         4,
			StreakBonusTier2:         8,
			StreakBonusTier3:         12,
		}, nil
	}
	return &cfg, nil
}

func (r *postgresRepository) UpdateConfig(ctx context.Context, config *IncentiveConfig) error {
	query := `UPDATE incentive_configs SET referral_bonus_amount = $1, referral_bonus_currency = $2, circle_completion_bonus = $3, circle_completion_currency = $4, contribution_match_percent = $5, contribution_match_max = $6`
	_, err := r.db.ExecContext(ctx, query, config.ReferralBonusAmount, config.ReferralBonusCurrency, config.CircleCompletionBonus, config.CircleCompletionCurrency, config.ContributionMatchPercent, config.ContributionMatchMax)
	return err
}

func (r *postgresRepository) FindByReferralCode(ctx context.Context, code string) (*Referral, error) {
	query := `SELECT id, referrer_id, COALESCE(referred_id, '00000000-0000-0000-0000-000000000000'), referral_code, status, created_at FROM referrals WHERE referral_code = $1 FOR UPDATE`
	var ref Referral
	err := r.db.GetContext(ctx, &ref, query, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrReferralCodeNotFound
		}
		return nil, err
	}
	return &ref, nil
}

func (r *postgresRepository) FindByReferrerID(ctx context.Context, userID uuid.UUID) ([]Referral, error) {
	query := `SELECT id, referrer_id, COALESCE(referred_id, '00000000-0000-0000-0000-000000000000'), referral_code, status, created_at FROM referrals WHERE referrer_id = $1`
	var refs []Referral
	err := r.db.SelectContext(ctx, &refs, query, userID)
	return refs, err
}

func (r *postgresRepository) FindByReferredID(ctx context.Context, userID uuid.UUID) (*Referral, error) {
	query := `SELECT id, referrer_id, COALESCE(referred_id, '00000000-0000-0000-0000-000000000000'), referral_code, status, created_at FROM referrals WHERE referred_id = $1`
	var ref Referral
	err := r.db.GetContext(ctx, &ref, query, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &ref, nil
}

func (r *postgresRepository) UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error {
	query := `UPDATE referrals SET status = $2, completed_at = NOW(), updated_at = NOW() WHERE id = $1 AND status = 'pending'`
	res, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrReferralCodeAlreadyUsed
	}
	return nil
}

func (r *postgresRepository) FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error) {
	query := `SELECT id, user_id, type, status, amount, currency, metadata, reference_id, expires_at, claimed_at, created_at, updated_at FROM incentives WHERE id = $1`
	var inc Incentive
	err := r.db.GetContext(ctx, &inc, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrIncentiveNotFound
		}
		return nil, err
	}
	return &inc, nil
}

func (r *postgresRepository) FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	query := `SELECT id, user_id, type, status, amount, currency, metadata, reference_id, expires_at, claimed_at, created_at, updated_at FROM incentives WHERE user_id = $1`
	var incentives []Incentive
	err := r.db.SelectContext(ctx, &incentives, query, userID)
	return incentives, err
}

func (r *postgresRepository) FindByUserIDAndType(ctx context.Context, userID uuid.UUID, typ IncentiveType) ([]Incentive, error) {
	query := `SELECT id, user_id, type, status, amount, currency, metadata, reference_id, expires_at, claimed_at, created_at, updated_at FROM incentives WHERE user_id = $1 AND type = $2`
	var incentives []Incentive
	err := r.db.SelectContext(ctx, &incentives, query, userID, typ)
	return incentives, err
}

func (r *postgresRepository) GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	query := `SELECT id, user_id, type, status, amount, currency, metadata, reference_id, expires_at, claimed_at, created_at, updated_at FROM incentives WHERE user_id = $1 AND status = 'pending'`
	var incentives []Incentive
	err := r.db.SelectContext(ctx, &incentives, query, userID)
	return incentives, err
}

func (r *postgresRepository) GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error) {
	query := `SELECT
		COALESCE(SUM(CASE WHEN status != 'cancelled' THEN amount ELSE 0 END), 0) AS total_earned,
		COALESCE(SUM(CASE WHEN status = 'claimed' THEN amount ELSE 0 END), 0) AS total_claimed,
		COALESCE(SUM(CASE WHEN status = 'pending' THEN amount ELSE 0 END), 0) AS pending_amount,
		COALESCE(COUNT(CASE WHEN type = 'referral' THEN 1 END), 0) AS referral_count
		FROM incentives WHERE user_id = $1`
	summary := &UserIncentiveSummary{BonusTier: 1}
	err := r.db.GetContext(ctx, summary, query, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	streakQuery := `SELECT COALESCE(current_streak, 0), COALESCE(longest_streak, 0), COALESCE(bonus_tier, 1) FROM savings_streaks WHERE user_id = $1`
	var current, longest, tier int
	if err := r.db.QueryRowContext(ctx, streakQuery, userID).Scan(&current, &longest, &tier); err == nil {
		summary.CurrentStreak = current
		summary.LongestStreak = longest
		summary.BonusTier = tier
	}
	return summary, nil
}

func (r *postgresRepository) UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error {
	query := `UPDATE incentives SET status = $2, claimed_at = CASE WHEN $2 = 'claimed' THEN NOW() ELSE claimed_at END, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, status)
	return err
}

func (r *postgresRepository) FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	var streak SavingsStreak
	query := `SELECT id, user_id, current_streak, longest_streak, last_contribution_at, bonus_tier FROM savings_streaks WHERE user_id = $1`
	err := r.db.GetContext(ctx, &streak, query, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrIncentiveNotFound
		}
		return nil, err
	}
	return &streak, nil
}

func (r *postgresRepository) CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	query := `INSERT INTO savings_streaks (id, user_id, current_streak, longest_streak, last_contribution_at, bonus_tier) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.db.ExecContext(ctx, query, streak.ID, streak.UserID, streak.CurrentStreak, streak.LongestStreak, streak.LastContributionAt, streak.BonusTier)
	return err
}

func (r *postgresRepository) UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	query := `UPDATE savings_streaks SET current_streak = $1, longest_streak = $2, last_contribution_at = $3, bonus_tier = $4, updated_at = NOW() WHERE user_id = $5`
	_, err := r.db.ExecContext(ctx, query, streak.CurrentStreak, streak.LongestStreak, streak.LastContributionAt, streak.BonusTier, streak.UserID)
	return err
}

func (r *postgresRepository) CreateConfig(ctx context.Context, config *IncentiveConfig) error {
	query := `INSERT INTO incentive_configs (id, referral_bonus_amount, referral_bonus_currency, circle_completion_bonus, circle_completion_currency, contribution_match_percent, contribution_match_max, is_active) VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)`
	_, err := r.db.ExecContext(ctx, query, config.ID, config.ReferralBonusAmount, config.ReferralBonusCurrency, config.CircleCompletionBonus, config.CircleCompletionCurrency, config.ContributionMatchPercent, config.ContributionMatchMax)
	return err
}
