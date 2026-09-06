package incentives

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrReferralCodeNotFound    = errors.New("referral code not found")
	ErrReferralCodeAlreadyUsed = errors.New("referral code already used")
	ErrSelfReferral            = errors.New("cannot refer yourself")
	ErrReferralCodeTaken       = errors.New("referral code is already taken")
	ErrIncentiveNotFound       = errors.New("incentive not found")
)

type Service interface {
	GenerateReferralCode(ctx context.Context, userID string) (string, error)
	ApplyReferralCode(ctx context.Context, referredUserID string, referralCode string) error
	GetReferrals(ctx context.Context, userID string) ([]Referral, error)
	GrantCircleCompletionReward(ctx context.Context, userID string, circleID string) (*Incentive, error)
	CalculateContributionMatch(ctx context.Context, userID string, amount float64) (float64, error)
	GrantContributionMatch(ctx context.Context, userID string, circleID string, amount float64) (*Incentive, error)
	GrantFirstDepositBonus(ctx context.Context, userID string, depositAmount float64) (*Incentive, error)
	RecordContribution(ctx context.Context, userID string) (*SavingsStreak, error)
	GrantStreakBonus(ctx context.Context, userID string) (*Incentive, error)
	ClaimIncentive(ctx context.Context, userID string, incentiveID string) error
	GetUserIncentives(ctx context.Context, userID string) ([]Incentive, error)
	GetPendingIncentives(ctx context.Context, userID string) ([]Incentive, error)
	GetUserSummary(ctx context.Context, userID string) (*UserIncentiveSummary, error)
	GetIncentives(ctx context.Context, userID string) ([]Incentive, error)
	GetStreak(ctx context.Context, userID string) (*SavingsStreak, error)
	GetConfig(ctx context.Context) (*IncentiveConfig, error)
	UpdateConfig(ctx context.Context, config *IncentiveConfig) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}

func (s *service) GetIncentives(ctx context.Context, userIDStr string) ([]Incentive, error) {
	userID, err := parseUUID(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}
	return s.repo.GetIncentives(ctx, userID)
}

func (s *service) GetStreak(ctx context.Context, userIDStr string) (*SavingsStreak, error) {
	userID, err := parseUUID(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}
	return s.repo.GetStreak(ctx, userID)
}
