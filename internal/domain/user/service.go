package user

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	GetByID(ctx context.Context, id string) (*User, error)
	GetByWallet(ctx context.Context, wallet string) (*User, error)
	Create(ctx context.Context, wallet string) (*User, error)
	UpdateProfile(ctx context.Context, id string, updates UpdateProfileInput) (*User, error)
	IsEmailTaken(ctx context.Context, email string) (bool, error)
	GetMoiScore(ctx context.Context, id string) (*MoiScoreResponse, error)
	GetCircles(ctx context.Context, id string) ([]any, error)
}

type UpdateProfileInput struct {
	DisplayName       *string `json:"displayName"`
	Email             *string `json:"email"`
	Phone             *string `json:"phone"`
	CountryCode       *string `json:"countryCode"`
	PreferredLanguage *string `json:"preferredLanguage"`
}

type MoiScoreResponse struct {
	Score     int            `json:"score"`
	Level     string         `json:"level"`
	Breakdown MoiBreakdown   `json:"breakdown"`
	History   []MonthlyScore `json:"history"`
}

type MoiBreakdown struct {
	Streaks     int `json:"streaks"`
	Completions int `json:"completions"`
	Volume      int `json:"volume"`
	Recency     int `json:"recency"`
}

type MonthlyScore struct {
	Month string `json:"month"`
	Score int    `json:"score"`
}

type userService struct {
	repo Repository
	db   interface {
		QueryRowxContext(ctx context.Context, query string, args ...interface{}) *sql.Row
		SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	}
}

func NewService(repo Repository) Service {
	return &userService{repo: repo}
}

func parseUUID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, ErrInvalidUUID
	}
	return parsed, nil
}

func (s *userService) GetByID(ctx context.Context, id string) (*User, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	u, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by id: %w", err)
	}
	return u, nil
}

func (s *userService) GetByWallet(ctx context.Context, wallet string) (*User, error) {
	u, err := s.repo.FindByWalletAddress(ctx, wallet)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("getting user by wallet: %w", err)
	}
	return u, nil
}

func (s *userService) Create(ctx context.Context, wallet string) (*User, error) {
	existing, err := s.repo.FindByWalletAddress(ctx, wallet)
	if err == nil && existing != nil {
		return existing, nil
	}
	if err != nil && err != apperrors.ErrNotFound {
		return nil, fmt.Errorf("checking existing user: %w", err)
	}

	now := time.Now().UTC()
	u := &User{
		ID:                uuid.New(),
		WalletAddress:     wallet,
		KYCStatus:         KYCUnverified,
		PreferredLanguage: "en",
		MoiScore:          0,
		Role:              RoleUser,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		if err == apperrors.ErrConflict {
			existing, findErr := s.repo.FindByWalletAddress(ctx, wallet)
			if findErr != nil {
				return nil, fmt.Errorf("conflict resolution failed: %w", findErr)
			}
			return existing, nil
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

func (s *userService) UpdateProfile(ctx context.Context, id string, updates UpdateProfileInput) (*User, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}

	u, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("finding user for update: %w", err)
	}

	if updates.DisplayName != nil {
		u.DisplayName = updates.DisplayName
	}
	if updates.Email != nil {
		hashed := hashUserEmail(*updates.Email)
		u.Email = &hashed
	}
	if updates.Phone != nil {
		u.Phone = updates.Phone
	}
	if updates.CountryCode != nil {
		u.CountryCode = updates.CountryCode
	}
	if updates.PreferredLanguage != nil {
		u.PreferredLanguage = *updates.PreferredLanguage
	}
	u.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, u); err != nil {
		return nil, fmt.Errorf("updating profile: %w", err)
	}
	return u, nil
}

func (s *userService) GetMoiScore(ctx context.Context, id string) (*MoiScoreResponse, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}

	u, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("finding user for moi score: %w", err)
	}

	level := calcLevel(u.MoiScore)

	return &MoiScoreResponse{
		Score: u.MoiScore,
		Level: level,
		Breakdown: MoiBreakdown{
			Streaks:     int(math.Min(float64(u.MoiScore)*0.35, 350)),
			Completions: int(math.Min(float64(u.MoiScore)*0.30, 300)),
			Volume:      int(math.Min(float64(u.MoiScore)*0.20, 200)),
			Recency:     int(math.Min(float64(u.MoiScore)*0.15, 150)),
		},
		History: make([]MonthlyScore, 0),
	}, nil
}

func (s *userService) GetCircles(ctx context.Context, id string) ([]any, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("finding user for circles: %w", err)
	}

	return []any{}, nil
}

func (s *userService) IsEmailTaken(ctx context.Context, email string) (bool, error) {
	// Check users table
	_, err := s.repo.FindByEmail(ctx, email)
	if err == nil {
		return true, nil
	}
	if err != nil && err != apperrors.ErrNotFound {
		return false, fmt.Errorf("checking email availability in users: %w", err)
	}
	// Also check user_emails table for verified emails from previous registrations
	exists, err := s.repo.EmailPreviouslyVerified(ctx, email)
	if err != nil {
		return false, fmt.Errorf("checking email availability in verifications: %w", err)
	}
	return exists, nil
}

func calcLevel(score int) string {
	switch {
	case score > 800:
		return "Diamond"
	case score > 600:
		return "Platinum"
	case score > 400:
		return "Gold"
	case score > 200:
		return "Silver"
	default:
		return "Bronze"
	}
}
