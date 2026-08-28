package incentives

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	// 10 random bytes → 20 hex chars, filling referrals.referral_code VARCHAR(20)
	// with 80 bits of entropy (vs the previous 32-bit UUID prefix).
	referralCodeEntropyBytes = 10
	maxReferralCodeAttempts  = 8
)

// Referral System

func (s *service) GenerateReferralCode(ctx context.Context, userID string) (string, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return "", err
	}

	// Check if referral already exists for this user
	existing, err := s.repo.FindByReferrerID(ctx, uid)
	if err == nil && len(existing) > 0 {
		return existing[0].ReferralCode, nil
	}

	var lastErr error
	for attempt := 0; attempt < maxReferralCodeAttempts; attempt++ {
		code, err := newReferralCode()
		if err != nil {
			return "", err
		}

		referral := &Referral{
			ID:           uuid.New(),
			ReferrerID:   uid,
			ReferredID:   uuid.Nil, // Will be set when someone uses the code
			ReferralCode: code,
			Status:       "pending",
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}

		if err := s.repo.CreateReferral(ctx, referral); err != nil {
			if errors.Is(err, ErrReferralCodeTaken) {
				lastErr = err
				continue
			}
			return "", err
		}

		return code, nil
	}

	return "", fmt.Errorf("generating unique referral code after %d attempts: %w", maxReferralCodeAttempts, lastErr)
}

func (s *service) ApplyReferralCode(ctx context.Context, userID string, code string) error {
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}

	// Check if user already used a referral
	existingReferral, err := s.repo.FindByReferredID(ctx, uid)
	if err == nil && existingReferral != nil {
		return fmt.Errorf("user already used a referral code")
	}

	// Find the referral by code
	referral, err := s.repo.FindByReferralCode(ctx, code)
	if err != nil {
		return fmt.Errorf("invalid referral code: %w", err)
	}

	// Cannot refer yourself
	if referral.ReferrerID == uid {
		return fmt.Errorf("cannot refer yourself")
	}

	// Check if referral is still available
	if referral.Status != "pending" || referral.ReferredID != uuid.Nil {
		return fmt.Errorf("referral code already used")
	}

	// Get config for bonus amount
	config, err := s.repo.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("getting incentive config: %w", err)
	}

	// Update referral with referred user
	referral.ReferredID = uid
	referral.Status = "completed"
	now := time.Now().UTC()
	referral.CompletedAt = sql.NullTime{Time: now, Valid: true}
	referral.UpdatedAt = now

	if err := s.repo.UpdateReferralStatus(ctx, referral.ID, "completed"); err != nil {
		return fmt.Errorf("updating referral status: %w", err)
	}

	// Grant bonus to referrer
	incentive := &Incentive{
		ID:          uuid.New(),
		UserID:      referral.ReferrerID,
		Type:        IncentiveTypeReferral,
		Status:      IncentiveStatusPending,
		Amount:      config.ReferralBonusAmount,
		Currency:    config.ReferralBonusCurrency,
		ReferenceID: sql.NullString{String: code, Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateIncentive(ctx, incentive); err != nil {
		return fmt.Errorf("creating referral incentive: %w", err)
	}

	return nil
}

func (s *service) GetReferrals(ctx context.Context, userID string) ([]Referral, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	return s.repo.FindByReferrerID(ctx, uid)
}

// generateReferralCode returns a cryptographically random hex code.
// 10 bytes → 20 hex characters (80 bits), collision-safe for VARCHAR(20).
func generateReferralCode() (string, error) {
	b := make([]byte, referralCodeEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating referral code: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// newReferralCode is the code generator used by GenerateReferralCode.
// Tests replace it to exercise collision retry without depending on rand.
var newReferralCode = generateReferralCode
