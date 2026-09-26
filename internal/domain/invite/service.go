package invite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/logger"
)

type Service interface {
	Generate(ctx context.Context, input GenerateInput) (*Invite, error)
	Validate(ctx context.Context, code string) (*Invite, error)
	List(ctx context.Context, circleID string) ([]Invite, error)
	Revoke(ctx context.Context, id, userID string) error
}

type GenerateInput struct {
	CircleID string `json:"circleId"`
	UserID   string `json:"userId"`
	MaxUses  int    `json:"maxUses" validate:"gte=1,lte=100"`
	TTLHours int    `json:"ttlHours" validate:"omitempty,gte=1,lte=720"`
}

type inviteService struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &inviteService{repo: repo}
}

func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}

func generateCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating invite code: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (s *inviteService) Generate(ctx context.Context, input GenerateInput) (*Invite, error) {
	circleID, err := parseUUID(input.CircleID)
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(input.UserID)
	if err != nil {
		return nil, err
	}

	code, err := generateCode()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	inv := &Invite{
		ID:        uuid.New(),
		CircleID:  circleID,
		Code:      code,
		CreatedBy: userID,
		MaxUses:   input.MaxUses,
		UseCount:  0,
		CreatedAt: now,
	}

	if input.TTLHours > 0 {
		inv.ExpiresAt = sql.NullTime{Time: now.Add(time.Duration(input.TTLHours) * time.Hour), Valid: true}
	}

	if err := s.repo.Create(ctx, inv); err != nil {
		logger.Ctx(ctx).Error().Err(err).Str("circleID", input.CircleID).Msg("failed to create invite")
		return nil, fmt.Errorf("generating invite: %w", err)
	}
	logger.Ctx(ctx).Info().Str("inviteID", inv.ID.String()).Str("circleID", input.CircleID).Msg("invite created")
	return inv, nil
}

func (s *inviteService) Validate(ctx context.Context, code string) (*Invite, error) {
	inv, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, apperrors.ErrInvalidInvite
		}
		return nil, fmt.Errorf("validating invite: %w", err)
	}

	if inv.MaxUses > 0 && inv.UseCount >= inv.MaxUses {
		return nil, apperrors.ErrInvalidInvite
	}
	if inv.ExpiresAt.Valid && time.Now().UTC().After(inv.ExpiresAt.Time) {
		return nil, apperrors.ErrInvalidInvite
	}

	return inv, nil
}

func (s *inviteService) List(ctx context.Context, circleID string) ([]Invite, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return nil, err
	}
	invites, err := s.repo.FindByCircle(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("listing invites: %w", err)
	}
	return invites, nil
}

func (s *inviteService) Revoke(ctx context.Context, id, userID string) error {
	iid, err := parseUUID(id)
	if err != nil {
		return err
	}
	_ = userID

	if err := s.repo.Delete(ctx, iid); err != nil {
		if err == apperrors.ErrNotFound {
			return apperrors.ErrInvalidInvite
		}
		return fmt.Errorf("revoking invite: %w", err)
	}
	return nil
}
