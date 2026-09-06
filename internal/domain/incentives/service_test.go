package incentives

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGenerateReferralCode(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New().String()

	code, err := service.GenerateReferralCode(context.Background(), userID)

	assert.NoError(t, err)
	assert.Len(t, code, referralCodeEntropyBytes*2)
	assert.Regexp(t, "^[0-9a-f]+$", code)
	assert.NotEqual(t, userID[:8], code, "code must not be a UUID prefix")
}

func TestGenerateReferralCode_ReturnsExisting(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)
	userID := uuid.New()

	first, err := service.GenerateReferralCode(context.Background(), userID.String())
	assert.NoError(t, err)

	second, err := service.GenerateReferralCode(context.Background(), userID.String())
	assert.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestGenerateReferralCode_HighEntropy(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		code, err := generateReferralCode()
		assert.NoError(t, err)
		assert.Len(t, code, referralCodeEntropyBytes*2)
		assert.Regexp(t, "^[0-9a-f]+$", code)
		_, dup := seen[code]
		assert.False(t, dup, "collision among 256 independently generated codes")
		seen[code] = struct{}{}
	}
}

func TestGenerateReferralCode_RetriesOnCollision(t *testing.T) {
	orig := newReferralCode
	t.Cleanup(func() { newReferralCode = orig })

	seq := []string{"aaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb"}
	i := 0
	newReferralCode = func() (string, error) {
		code := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return code, nil
	}

	repo := newMockRepository()
	repo.referralCodeMap["aaaaaaaaaaaaaaaaaaaa"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   uuid.New(),
		ReferralCode: "aaaaaaaaaaaaaaaaaaaa",
		Status:       "pending",
	}

	code, err := NewService(repo).GenerateReferralCode(context.Background(), uuid.New().String())
	assert.NoError(t, err)
	assert.Equal(t, "bbbbbbbbbbbbbbbbbbbb", code)
}

func TestGenerateReferralCode_CollisionRetryExhausted(t *testing.T) {
	orig := newReferralCode
	t.Cleanup(func() { newReferralCode = orig })

	newReferralCode = func() (string, error) {
		return "aaaaaaaaaaaaaaaaaaaa", nil
	}

	repo := newMockRepository()
	repo.referralCodeMap["aaaaaaaaaaaaaaaaaaaa"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   uuid.New(),
		ReferralCode: "aaaaaaaaaaaaaaaaaaaa",
		Status:       "pending",
	}

	_, err := NewService(repo).GenerateReferralCode(context.Background(), uuid.New().String())
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrReferralCodeTaken)
	assert.Contains(t, err.Error(), "after 8 attempts")
}

func TestApplyReferralCode(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	referrerID := uuid.New()
	referredID := uuid.New()

	repo.referralCodeMap["testcode"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   referrerID,
		ReferredID:   uuid.Nil,
		ReferralCode: "testcode",
		Status:       "pending",
	}

	repo.config = &IncentiveConfig{
		ReferralBonusAmount:   5.0,
		ReferralBonusCurrency: "USDC",
	}

	err := service.ApplyReferralCode(context.Background(), referredID.String(), "testcode")

	assert.NoError(t, err)
	assert.Equal(t, "completed", repo.updatedReferralStatus)
	assert.Len(t, repo.createdIncentives, 1)
	assert.Equal(t, IncentiveTypeReferral, repo.createdIncentives[0].Type)
}

func TestApplyReferralCode_RaceCondition(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	referrerID := uuid.New()
	repo.referralCodeMap["racecode"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   referrerID,
		ReferredID:   uuid.Nil,
		ReferralCode: "racecode",
		Status:       "pending",
	}

	repo.config = &IncentiveConfig{
		ReferralBonusAmount:   5.0,
		ReferralBonusCurrency: "USDC",
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			userId := uuid.New().String()
			err := service.ApplyReferralCode(context.Background(), userId, "racecode")
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	otherErrors := 0
	for err := range errs {
		if err.Error() == "referral code already used" || err.Error() == "not found" {
			otherErrors++
		} else {
			otherErrors++
		}
	}

	// Exactly one should succeed, others fail with already used / conflict
	assert.Equal(t, 1, len(repo.createdIncentives), "exactly one incentive should be created across concurrent claims")
}

func TestApplyReferralCode_SelfReferral(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.referralCodeMap["testcode"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   userID,
		ReferredID:   uuid.Nil,
		ReferralCode: "testcode",
		Status:       "pending",
	}

	err := service.ApplyReferralCode(context.Background(), userID.String(), "testcode")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot refer yourself")
}

func TestApplyReferralCode_AlreadyUsed(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	referrerID := uuid.New()
	referredID := uuid.New()

	repo.referralCodeMap["testcode"] = &Referral{
		ID:           uuid.New(),
		ReferrerID:   referrerID,
		ReferredID:   uuid.New(),
		ReferralCode: "testcode",
		Status:       "completed",
	}

	err := service.ApplyReferralCode(context.Background(), referredID.String(), "testcode")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already used")
}

func TestGrantCircleCompletionReward(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	circleID := uuid.New()

	repo.config = &IncentiveConfig{
		CircleCompletionBonus:    10.0,
		CircleCompletionCurrency: "USDC",
	}

	incentive, err := service.GrantCircleCompletionReward(context.Background(), userID.String(), circleID.String())

	assert.NoError(t, err)
	assert.NotNil(t, incentive)
	assert.Equal(t, IncentiveTypeCircleCompletion, incentive.Type)
	assert.Equal(t, 10.0, incentive.Amount)
}

func TestGrantCircleCompletionReward_AlreadyReceived(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	circleID := uuid.New()

	repo.config = &IncentiveConfig{
		CircleCompletionBonus:    10.0,
		CircleCompletionCurrency: "USDC",
	}

	repo.userIncentives = []Incentive{
		{
			ID:          uuid.New(),
			UserID:      userID,
			Type:        IncentiveTypeCircleCompletion,
			ReferenceID: sql.NullString{String: circleID.String(), Valid: true},
		},
	}

	_, err := service.GrantCircleCompletionReward(context.Background(), userID.String(), circleID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already received")
}

func TestCalculateContributionMatch(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	repo.config = &IncentiveConfig{
		ContributionMatchPercent: 10.0,
		ContributionMatchMax:     50.0,
	}

	tests := []struct {
		amount   float64
		expected float64
	}{
		{100.0, 10.0},
		{500.0, 50.0}, // Capped at max
		{25.0, 2.5},
	}

	for _, tt := range tests {
		match, err := service.CalculateContributionMatch(context.Background(), uuid.New().String(), tt.amount)
		assert.NoError(t, err)
		assert.Equal(t, tt.expected, match)
	}
}

func TestRecordContribution_NewStreak(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		StreakBonusTier1: 4,
	}

	streak, err := service.RecordContribution(context.Background(), userID.String())

	assert.NoError(t, err)
	assert.NotNil(t, streak)
	assert.Equal(t, 1, streak.CurrentStreak)
	assert.Equal(t, 1, streak.LongestStreak)
	assert.Equal(t, 1, streak.BonusTier)
}

func TestRecordContribution_ContinueStreak(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		StreakBonusTier1: 4,
		StreakBonusTier2: 8,
	}

	repo.streak = &SavingsStreak{
		ID:                 uuid.New(),
		UserID:             userID,
		CurrentStreak:      3,
		LongestStreak:      3,
		LastContributionAt: sql.NullTime{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		BonusTier:          1,
	}

	streak, err := service.RecordContribution(context.Background(), userID.String())

	assert.NoError(t, err)
	assert.Equal(t, 4, streak.CurrentStreak)
	assert.Equal(t, 4, streak.LongestStreak)
}
