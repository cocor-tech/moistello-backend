package incentives

import (
	"context"
	"database/sql"
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

	// Setup existing referral
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

func TestRecordContribution_ResetStreak(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		StreakBonusTier1: 4,
	}

	repo.streak = &SavingsStreak{
		ID:                 uuid.New(),
		UserID:             userID,
		CurrentStreak:      5,
		LongestStreak:      5,
		LastContributionAt: sql.NullTime{Time: time.Now().Add(-10 * 24 * time.Hour), Valid: true}, // 10 days ago
		BonusTier:          2,
	}

	streak, err := service.RecordContribution(context.Background(), userID.String())

	assert.NoError(t, err)
	assert.Equal(t, 1, streak.CurrentStreak)
	assert.Equal(t, 5, streak.LongestStreak) // Longest preserved
}

func TestGrantStreakBonus(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		StreakBonusTier1:       4,
		StreakBonusTier1Amount: 2.0,
		StreakBonusTier2:       8,
		StreakBonusTier2Amount: 5.0,
		StreakBonusTier3:       12,
		StreakBonusTier3Amount: 10.0,
	}

	repo.streak = &SavingsStreak{
		ID:            uuid.New(),
		UserID:        userID,
		CurrentStreak: 8,
		BonusTier:     2,
	}

	incentive, err := service.GrantStreakBonus(context.Background(), userID.String())

	assert.NoError(t, err)
	assert.NotNil(t, incentive)
	assert.Equal(t, IncentiveTypeSavingsStreak, incentive.Type)
	assert.Equal(t, 5.0, incentive.Amount)
}

func TestGrantFirstDepositBonus(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		FirstDepositBonus:     5.0,
		FirstDepositCurrency:  "USDC",
		FirstDepositMinAmount: 10.0,
	}

	incentive, err := service.GrantFirstDepositBonus(context.Background(), userID.String(), 50.0)

	assert.NoError(t, err)
	assert.NotNil(t, incentive)
	assert.Equal(t, IncentiveTypeFirstDeposit, incentive.Type)
	assert.Equal(t, 5.0, incentive.Amount)
}

func TestGrantFirstDepositBonus_BelowMinimum(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		FirstDepositBonus:     5.0,
		FirstDepositCurrency:  "USDC",
		FirstDepositMinAmount: 10.0,
	}

	_, err := service.GrantFirstDepositBonus(context.Background(), userID.String(), 5.0)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "below minimum requirement")
}

func TestGrantFirstDepositBonus_AlreadyReceived(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.config = &IncentiveConfig{
		FirstDepositBonus:     5.0,
		FirstDepositCurrency:  "USDC",
		FirstDepositMinAmount: 10.0,
	}

	repo.userIncentives = []Incentive{
		{
			ID:     uuid.New(),
			UserID: userID,
			Type:   IncentiveTypeFirstDeposit,
		},
	}

	_, err := service.GrantFirstDepositBonus(context.Background(), userID.String(), 50.0)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already received")
}

func TestClaimIncentive(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	incentiveID := uuid.New()

	repo.incentive = &Incentive{
		ID:     incentiveID,
		UserID: userID,
		Status: IncentiveStatusPending,
	}

	err := service.ClaimIncentive(context.Background(), userID.String(), incentiveID.String())

	assert.NoError(t, err)
	assert.Equal(t, IncentiveStatusClaimed, repo.updatedIncentiveStatus)
}

func TestClaimIncentive_NotOwner(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	incentiveID := uuid.New()

	repo.incentive = &Incentive{
		ID:     incentiveID,
		UserID: uuid.New(), // Different user
		Status: IncentiveStatusPending,
	}

	err := service.ClaimIncentive(context.Background(), userID.String(), incentiveID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to user")
}

func TestClaimIncentive_AlreadyClaimed(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	incentiveID := uuid.New()

	repo.incentive = &Incentive{
		ID:     incentiveID,
		UserID: userID,
		Status: IncentiveStatusClaimed,
	}

	err := service.ClaimIncentive(context.Background(), userID.String(), incentiveID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already claimed")
}

func TestClaimIncentive_Expired(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()
	incentiveID := uuid.New()

	repo.incentive = &Incentive{
		ID:        incentiveID,
		UserID:    userID,
		Status:    IncentiveStatusPending,
		ExpiresAt: sql.NullTime{Time: time.Now().Add(-24 * time.Hour), Valid: true},
	}

	err := service.ClaimIncentive(context.Background(), userID.String(), incentiveID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestEnsureNoIncentive_NoExisting(t *testing.T) {
	repo := newMockRepository()
	userID := uuid.New()

	err := ensureNoIncentive(context.Background(), repo, userID, IncentiveTypeFirstDeposit, nil, "already received")

	assert.NoError(t, err)
}

func TestEnsureNoIncentive_AnyExistingConflicts(t *testing.T) {
	repo := newMockRepository()
	userID := uuid.New()

	repo.userIncentives = []Incentive{
		{ID: uuid.New(), UserID: userID, Type: IncentiveTypeFirstDeposit},
	}

	err := ensureNoIncentive(context.Background(), repo, userID, IncentiveTypeFirstDeposit, nil, "already received first deposit bonus")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already received")
}

func TestEnsureNoIncentive_PredicateMatch(t *testing.T) {
	repo := newMockRepository()
	userID := uuid.New()
	circleID := uuid.New().String()

	repo.userIncentives = []Incentive{
		{ID: uuid.New(), UserID: userID, Type: IncentiveTypeCircleCompletion, ReferenceID: sql.NullString{String: circleID, Valid: true}},
	}

	err := ensureNoIncentive(context.Background(), repo, userID, IncentiveTypeCircleCompletion, func(inc Incentive) bool {
		return inc.ReferenceID.Valid && inc.ReferenceID.String == circleID
	}, "already received completion reward for this circle")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already received")
}

func TestEnsureNoIncentive_PredicateNoMatch(t *testing.T) {
	repo := newMockRepository()
	userID := uuid.New()

	// Same type, but a different reference ID than the one being granted.
	repo.userIncentives = []Incentive{
		{ID: uuid.New(), UserID: userID, Type: IncentiveTypeCircleCompletion, ReferenceID: sql.NullString{String: uuid.New().String(), Valid: true}},
	}

	err := ensureNoIncentive(context.Background(), repo, userID, IncentiveTypeCircleCompletion, func(inc Incentive) bool {
		return inc.ReferenceID.Valid && inc.ReferenceID.String == uuid.New().String()
	}, "already received completion reward for this circle")

	assert.NoError(t, err)
}

func TestGetUserSummary(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	userID := uuid.New()

	repo.summary = &UserIncentiveSummary{
		TotalEarned:   100.0,
		TotalClaimed:  50.0,
		PendingAmount: 50.0,
		ReferralCount: 5,
		CurrentStreak: 8,
		LongestStreak: 12,
		BonusTier:     2,
	}

	summary, err := service.GetUserSummary(context.Background(), userID.String())

	assert.NoError(t, err)
	assert.NotNil(t, summary)
	assert.Equal(t, 100.0, summary.TotalEarned)
	assert.Equal(t, 5, summary.ReferralCount)
}

func TestUpdateConfig(t *testing.T) {
	repo := newMockRepository()
	service := NewService(repo)

	config := &IncentiveConfig{
		ID:                       uuid.New(),
		ReferralBonusAmount:      10.0,
		ReferralBonusCurrency:    "USDC",
		CircleCompletionBonus:    20.0,
		CircleCompletionCurrency: "USDC",
		ContributionMatchPercent: 15.0,
		ContributionMatchMax:     100.0,
		IsActive:                 true,
	}

	err := service.UpdateConfig(context.Background(), config)

	assert.NoError(t, err)
	assert.Equal(t, config, repo.updatedConfig)
}

// Mock repository for testing

type mockRepository struct {
	referralCodeMap        map[string]*Referral
	referrals              []Referral
	config                 *IncentiveConfig
	streak                 *SavingsStreak
	incentive              *Incentive
	userIncentives         []Incentive
	createdIncentives      []Incentive
	updatedReferralStatus  string
	updatedIncentiveStatus IncentiveStatus
	updatedConfig          *IncentiveConfig
	summary                *UserIncentiveSummary
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		referralCodeMap: map[string]*Referral{},
	}
}

func (m *mockRepository) CreateIncentive(ctx context.Context, incentive *Incentive) error {
	m.createdIncentives = append(m.createdIncentives, *incentive)
	return nil
}

func (m *mockRepository) FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error) {
	return m.incentive, nil
}

func (m *mockRepository) FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	return m.userIncentives, nil
}

func (m *mockRepository) FindByUserIDAndType(ctx context.Context, userID uuid.UUID, incentiveType IncentiveType) ([]Incentive, error) {
	var result []Incentive
	for _, inc := range m.userIncentives {
		if inc.Type == incentiveType {
			result = append(result, inc)
		}
	}
	return result, nil
}

func (m *mockRepository) UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error {
	m.updatedIncentiveStatus = status
	return nil
}

func (m *mockRepository) GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	return m.userIncentives, nil
}

func (m *mockRepository) CreateReferral(ctx context.Context, referral *Referral) error {
	if _, exists := m.referralCodeMap[referral.ReferralCode]; exists {
		return ErrReferralCodeTaken
	}
	copied := *referral
	m.referralCodeMap[referral.ReferralCode] = &copied
	m.referrals = append(m.referrals, copied)
	return nil
}

func (m *mockRepository) FindByReferralCode(ctx context.Context, code string) (*Referral, error) {
	if ref, ok := m.referralCodeMap[code]; ok {
		return ref, nil
	}
	return nil, ErrReferralNotFound
}

func (m *mockRepository) FindByReferrerID(ctx context.Context, referrerID uuid.UUID) ([]Referral, error) {
	var out []Referral
	for _, ref := range m.referrals {
		if ref.ReferrerID == referrerID {
			out = append(out, ref)
		}
	}
	return out, nil
}

func (m *mockRepository) FindByReferredID(ctx context.Context, referredID uuid.UUID) (*Referral, error) {
	return nil, ErrReferralNotFound
}

func (m *mockRepository) UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error {
	m.updatedReferralStatus = status
	return nil
}

func (m *mockRepository) GetReferralCount(ctx context.Context, referrerID uuid.UUID) (int, error) {
	return 0, nil
}

func (m *mockRepository) CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	m.streak = streak
	return nil
}

func (m *mockRepository) FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	if m.streak != nil {
		return m.streak, nil
	}
	return nil, ErrIncentiveNotFound
}

func (m *mockRepository) UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	m.streak = streak
	return nil
}

func (m *mockRepository) GetConfig(ctx context.Context) (*IncentiveConfig, error) {
	if m.config != nil {
		return m.config, nil
	}
	return nil, ErrIncentiveNotFound
}

func (m *mockRepository) UpdateConfig(ctx context.Context, config *IncentiveConfig) error {
	m.updatedConfig = config
	return nil
}

func (m *mockRepository) CreateConfig(ctx context.Context, config *IncentiveConfig) error {
	m.updatedConfig = config
	return nil
}

func (m *mockRepository) GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error) {
	return m.summary, nil
}
