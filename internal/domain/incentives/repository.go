package incentives

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

var (
	ErrIncentiveNotFound = fmt.Errorf("incentive not found")
	ErrReferralNotFound  = fmt.Errorf("referral not found")
	ErrInvalidReferral   = fmt.Errorf("invalid referral code")
	ErrReferralCodeTaken = fmt.Errorf("referral code already exists")
)

type Repository interface {
	// Incentives
	CreateIncentive(ctx context.Context, incentive *Incentive) error
	FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error)
	FindByUserIDAndType(ctx context.Context, userID uuid.UUID, incentiveType IncentiveType) ([]Incentive, error)
	UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error
	GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error)

	// Referrals
	CreateReferral(ctx context.Context, referral *Referral) error
	FindByReferralCode(ctx context.Context, code string) (*Referral, error)
	FindByReferrerID(ctx context.Context, referrerID uuid.UUID) ([]Referral, error)
	FindByReferredID(ctx context.Context, referredID uuid.UUID) (*Referral, error)
	UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error
	GetReferralCount(ctx context.Context, referrerID uuid.UUID) (int, error)

	// Savings Streak
	CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error
	FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error)
	UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error

	// Config
	GetConfig(ctx context.Context) (*IncentiveConfig, error)
	UpdateConfig(ctx context.Context, config *IncentiveConfig) error
	CreateConfig(ctx context.Context, config *IncentiveConfig) error

	// Summary
	GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error)
}

type repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &repository{db: db}
}

// Incentives

func (r *repository) CreateIncentive(ctx context.Context, incentive *Incentive) error {
	query := `
		INSERT INTO incentives (id, user_id, type, status, amount, currency, metadata, reference_id, expires_at, claimed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err := r.db.ExecContext(ctx, query,
		incentive.ID, incentive.UserID, incentive.Type, incentive.Status,
		incentive.Amount, incentive.Currency, incentive.Metadata,
		incentive.ReferenceID, incentive.ExpiresAt, incentive.ClaimedAt,
		incentive.CreatedAt, incentive.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating incentive: %w", err)
	}
	return nil
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error) {
	var incentive Incentive
	query := `SELECT * FROM incentives WHERE id = $1`
	err := r.db.GetContext(ctx, &incentive, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrIncentiveNotFound
		}
		return nil, fmt.Errorf("finding incentive by id: %w", err)
	}
	return &incentive, nil
}

func (r *repository) FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	var incentives []Incentive
	query := `SELECT * FROM incentives WHERE user_id = $1 ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &incentives, query, userID)
	if err != nil {
		return nil, fmt.Errorf("finding incentives by user id: %w", err)
	}
	return incentives, nil
}

func (r *repository) FindByUserIDAndType(ctx context.Context, userID uuid.UUID, incentiveType IncentiveType) ([]Incentive, error) {
	var incentives []Incentive
	query := `SELECT * FROM incentives WHERE user_id = $1 AND type = $2 ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &incentives, query, userID, incentiveType)
	if err != nil {
		return nil, fmt.Errorf("finding incentives by user id and type: %w", err)
	}
	return incentives, nil
}

func (r *repository) UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error {
	query := `UPDATE incentives SET status = $1, claimed_at = CASE WHEN $1 = 'claimed' THEN NOW() ELSE claimed_at END, updated_at = NOW() WHERE id = $2`
	result, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating incentive status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncentiveNotFound
	}
	return nil
}

func (r *repository) GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	var incentives []Incentive
	query := `
		SELECT * FROM incentives 
		WHERE user_id = $1 AND status = 'pending' 
		AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
	`
	err := r.db.SelectContext(ctx, &incentives, query, userID)
	if err != nil {
		return nil, fmt.Errorf("getting pending incentives: %w", err)
	}
	return incentives, nil
}

// Referrals

func (r *repository) CreateReferral(ctx context.Context, referral *Referral) error {
	query := `
		INSERT INTO referrals (id, referrer_id, referred_id, referral_code, status, completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.ExecContext(ctx, query,
		referral.ID, referral.ReferrerID, referral.ReferredID,
		referral.ReferralCode, referral.Status, referral.CompletedAt,
		referral.CreatedAt, referral.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrReferralCodeTaken
		}
		return fmt.Errorf("creating referral: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func (r *repository) FindByReferralCode(ctx context.Context, code string) (*Referral, error) {
	var referral Referral
	query := `SELECT * FROM referrals WHERE referral_code = $1 ORDER BY created_at DESC LIMIT 1`
	err := r.db.GetContext(ctx, &referral, query, code)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrReferralNotFound
		}
		return nil, fmt.Errorf("finding referral by code: %w", err)
	}
	return &referral, nil
}

func (r *repository) FindByReferrerID(ctx context.Context, referrerID uuid.UUID) ([]Referral, error) {
	var referrals []Referral
	query := `SELECT * FROM referrals WHERE referrer_id = $1 ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &referrals, query, referrerID)
	if err != nil {
		return nil, fmt.Errorf("finding referrals by referrer id: %w", err)
	}
	return referrals, nil
}

func (r *repository) FindByReferredID(ctx context.Context, referredID uuid.UUID) (*Referral, error) {
	var referral Referral
	query := `SELECT * FROM referrals WHERE referred_id = $1`
	err := r.db.GetContext(ctx, &referral, query, referredID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrReferralNotFound
		}
		return nil, fmt.Errorf("finding referral by referred id: %w", err)
	}
	return &referral, nil
}

func (r *repository) UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error {
	query := `UPDATE referrals SET status = $1, completed_at = CASE WHEN $1 = 'completed' THEN NOW() ELSE completed_at END, updated_at = NOW() WHERE id = $2`
	result, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating referral status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrReferralNotFound
	}
	return nil
}

func (r *repository) GetReferralCount(ctx context.Context, referrerID uuid.UUID) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM referrals WHERE referrer_id = $1 AND status = 'completed'`
	err := r.db.GetContext(ctx, &count, query, referrerID)
	if err != nil {
		return 0, fmt.Errorf("getting referral count: %w", err)
	}
	return count, nil
}

// Savings Streak

func (r *repository) CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	query := `
		INSERT INTO savings_streaks (id, user_id, current_streak, longest_streak, last_contribution_at, bonus_tier, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.ExecContext(ctx, query,
		streak.ID, streak.UserID, streak.CurrentStreak, streak.LongestStreak,
		streak.LastContributionAt, streak.BonusTier, streak.CreatedAt, streak.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating savings streak: %w", err)
	}
	return nil
}

func (r *repository) FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	var streak SavingsStreak
	query := `SELECT * FROM savings_streaks WHERE user_id = $1`
	err := r.db.GetContext(ctx, &streak, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrIncentiveNotFound
		}
		return nil, fmt.Errorf("finding savings streak by user id: %w", err)
	}
	return &streak, nil
}

func (r *repository) UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	query := `
		UPDATE savings_streaks 
		SET current_streak = $1, longest_streak = $2, last_contribution_at = $3, bonus_tier = $4, updated_at = $5
		WHERE id = $6
	`
	result, err := r.db.ExecContext(ctx, query,
		streak.CurrentStreak, streak.LongestStreak, streak.LastContributionAt,
		streak.BonusTier, streak.UpdatedAt, streak.ID,
	)
	if err != nil {
		return fmt.Errorf("updating savings streak: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIncentiveNotFound
	}
	return nil
}

// Config

func (r *repository) GetConfig(ctx context.Context) (*IncentiveConfig, error) {
	var config IncentiveConfig
	query := `SELECT * FROM incentive_configs WHERE is_active = true ORDER BY created_at DESC LIMIT 1`
	err := r.db.GetContext(ctx, &config, query)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("getting incentive config: %w", err)
	}
	return &config, nil
}

func (r *repository) UpdateConfig(ctx context.Context, config *IncentiveConfig) error {
	query := `
		UPDATE incentive_configs 
		SET referral_bonus_amount = $1, referral_bonus_currency = $2,
		    circle_completion_bonus = $3, circle_completion_currency = $4,
		    contribution_match_percent = $5, contribution_match_max = $6,
		    streak_bonus_tier1 = $7, streak_bonus_tier1_amount = $8,
		    streak_bonus_tier2 = $9, streak_bonus_tier2_amount = $10,
		    streak_bonus_tier3 = $11, streak_bonus_tier3_amount = $12,
		    first_deposit_bonus = $13, first_deposit_currency = $14,
		    first_deposit_min_amount = $15, is_active = $16, updated_at = $17
		WHERE id = $18
	`
	result, err := r.db.ExecContext(ctx, query,
		config.ReferralBonusAmount, config.ReferralBonusCurrency,
		config.CircleCompletionBonus, config.CircleCompletionCurrency,
		config.ContributionMatchPercent, config.ContributionMatchMax,
		config.StreakBonusTier1, config.StreakBonusTier1Amount,
		config.StreakBonusTier2, config.StreakBonusTier2Amount,
		config.StreakBonusTier3, config.StreakBonusTier3Amount,
		config.FirstDepositBonus, config.FirstDepositCurrency,
		config.FirstDepositMinAmount, config.IsActive, config.UpdatedAt, config.ID,
	)
	if err != nil {
		return fmt.Errorf("updating incentive config: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *repository) CreateConfig(ctx context.Context, config *IncentiveConfig) error {
	query := `
		INSERT INTO incentive_configs (id, referral_bonus_amount, referral_bonus_currency, circle_completion_bonus, circle_completion_currency,
		    contribution_match_percent, contribution_match_max, streak_bonus_tier1, streak_bonus_tier1_amount,
		    streak_bonus_tier2, streak_bonus_tier2_amount, streak_bonus_tier3, streak_bonus_tier3_amount,
		    first_deposit_bonus, first_deposit_currency, first_deposit_min_amount, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`
	_, err := r.db.ExecContext(ctx, query,
		config.ID, config.ReferralBonusAmount, config.ReferralBonusCurrency,
		config.CircleCompletionBonus, config.CircleCompletionCurrency,
		config.ContributionMatchPercent, config.ContributionMatchMax,
		config.StreakBonusTier1, config.StreakBonusTier1Amount,
		config.StreakBonusTier2, config.StreakBonusTier2Amount,
		config.StreakBonusTier3, config.StreakBonusTier3Amount,
		config.FirstDepositBonus, config.FirstDepositCurrency,
		config.FirstDepositMinAmount, config.IsActive, config.CreatedAt, config.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating incentive config: %w", err)
	}
	return nil
}

// Summary

func (r *repository) GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error) {
	var summary UserIncentiveSummary

	// Total earned and claimed
	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN status = 'claimed' THEN amount ELSE 0 END), 0) as total_claimed,
			COALESCE(SUM(amount), 0) as total_earned,
			COALESCE(SUM(CASE WHEN status = 'pending' AND (expires_at IS NULL OR expires_at > NOW()) THEN amount ELSE 0 END), 0) as pending_amount
		FROM incentives WHERE user_id = $1
	`
	err := r.db.GetContext(ctx, &summary, query, userID)
	if err != nil {
		return nil, fmt.Errorf("getting incentive summary: %w", err)
	}

	// Referral count
	var referralCount int
	query = `SELECT COUNT(*) FROM referrals WHERE referrer_id = $1 AND status = 'completed'`
	err = r.db.GetContext(ctx, &referralCount, query, userID)
	if err != nil {
		return nil, fmt.Errorf("getting referral count: %w", err)
	}
	summary.ReferralCount = referralCount

	// Savings streak info
	var streak SavingsStreak
	query = `SELECT * FROM savings_streaks WHERE user_id = $1`
	err = r.db.GetContext(ctx, &streak, query, userID)
	if err == nil {
		summary.CurrentStreak = streak.CurrentStreak
		summary.LongestStreak = streak.LongestStreak
		summary.BonusTier = streak.BonusTier
	}

	return &summary, nil
}
