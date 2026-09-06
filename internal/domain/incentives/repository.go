package incentives

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

type Repository interface {
	Transact(ctx context.Context, fn func(repo Repository) error) error

	// Referrals
	CreateReferral(ctx context.Context, ref *Referral) error
	GetReferralByCode(ctx context.Context, code string) (*Referral, error)
	GetReferrerByUserID(ctx context.Context, userID uuid.UUID) (*Referral, error)
	UpdateReferral(ctx context.Context, ref *Referral) error
	FindByReferralCode(ctx context.Context, code string) (*Referral, error)
	FindByReferrerID(ctx context.Context, userID uuid.UUID) ([]Referral, error)
	FindByReferredID(ctx context.Context, userID uuid.UUID) (*Referral, error)
	UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error

	// Incentives
	CreateIncentive(ctx context.Context, inc *Incentive) error
	GetIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error)
	FindByUserIDAndType(ctx context.Context, userID uuid.UUID, typ IncentiveType) ([]Incentive, error)
	GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error)
	GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error)
	UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error

	// Streaks
	GetStreak(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error)
	UpsertStreak(ctx context.Context, streak *SavingsStreak) (*SavingsStreak, error)
	FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error)
	CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error
	UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error

	// Config
	GetConfig(ctx context.Context) (*IncentiveConfig, error)
	UpdateConfig(ctx context.Context, config *IncentiveConfig) error
	CreateConfig(ctx context.Context, config *IncentiveConfig) error
}

type mockRepository struct {
	mu                    sync.Mutex
	referralCodeMap       map[string]*Referral
	referrerUserMap       map[uuid.UUID]*Referral
	createdIncentives     []Incentive
	userIncentives        []Incentive
	streak                *SavingsStreak
	config                *IncentiveConfig
	updatedReferralStatus string
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		referralCodeMap: make(map[string]*Referral),
		referrerUserMap: make(map[uuid.UUID]*Referral),
		config: &IncentiveConfig{
			ReferralBonusAmount:      5.0,
			ReferralBonusCurrency:    "USDC",
			CircleCompletionBonus:    10.0,
			CircleCompletionCurrency: "USDC",
			ContributionMatchPercent: 10.0,
			ContributionMatchMax:     50.0,
		},
	}
}

func (m *mockRepository) CreateReferral(ctx context.Context, ref *Referral) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.referralCodeMap[ref.ReferralCode]; exists {
		return ErrReferralCodeTaken
	}
	m.referralCodeMap[ref.ReferralCode] = ref
	m.referrerUserMap[ref.ReferrerID] = ref
	return nil
}

func (m *mockRepository) GetReferralByCode(ctx context.Context, code string) (*Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, exists := m.referralCodeMap[code]
	if !exists {
		return nil, errors.New("not found")
	}
	copy := *ref
	return &copy, nil
}

func (m *mockRepository) GetReferrerByUserID(ctx context.Context, userID uuid.UUID) (*Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, exists := m.referrerUserMap[userID]
	if !exists {
		return nil, errors.New("not found")
	}
	copy := *ref
	return &copy, nil
}

func (m *mockRepository) UpdateReferral(ctx context.Context, ref *Referral) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, exists := m.referralCodeMap[ref.ReferralCode]
	if !exists {
		return errors.New("not found")
	}
	if existing.Status == "completed" || existing.ReferredID != uuid.Nil {
		return ErrReferralCodeAlreadyUsed
	}
	existing.ReferredID = ref.ReferredID
	existing.Status = ref.Status
	m.updatedReferralStatus = ref.Status
	return nil
}

func (m *mockRepository) FindByReferralCode(ctx context.Context, code string) (*Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, exists := m.referralCodeMap[code]
	if !exists {
		return nil, errors.New("not found")
	}
	copy := *ref
	return &copy, nil
}

func (m *mockRepository) FindByReferrerID(ctx context.Context, userID uuid.UUID) ([]Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Referral
	for _, ref := range m.referrerUserMap {
		if ref.ReferrerID == userID {
			res = append(res, *ref)
		}
	}
	return res, nil
}

func (m *mockRepository) FindByReferredID(ctx context.Context, userID uuid.UUID) (*Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ref := range m.referralCodeMap {
		if ref.ReferredID == userID && ref.ReferredID != uuid.Nil {
			copy := *ref
			return &copy, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockRepository) UpdateReferralStatus(ctx context.Context, id uuid.UUID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ref := range m.referralCodeMap {
		if ref.ID == id {
			if ref.Status == "completed" || ref.ReferredID != uuid.Nil {
				return ErrReferralCodeAlreadyUsed
			}
			ref.Status = status
			m.updatedReferralStatus = status
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockRepository) CreateIncentive(ctx context.Context, inc *Incentive) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdIncentives = append(m.createdIncentives, *inc)
	m.userIncentives = append(m.userIncentives, *inc)
	return nil
}

func (m *mockRepository) GetIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Incentive
	for _, inc := range m.userIncentives {
		if inc.UserID == userID {
			res = append(res, inc)
		}
	}
	return res, nil
}

func (m *mockRepository) FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inc := range m.userIncentives {
		if inc.ID == id {
			copy := inc
			return &copy, nil
		}
	}
	return nil, ErrIncentiveNotFound
}

func (m *mockRepository) FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Incentive
	for _, inc := range m.userIncentives {
		if inc.UserID == userID {
			res = append(res, inc)
		}
	}
	return res, nil
}

func (m *mockRepository) FindByUserIDAndType(ctx context.Context, userID uuid.UUID, typ IncentiveType) ([]Incentive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Incentive
	for _, inc := range m.userIncentives {
		if inc.UserID == userID && inc.Type == typ {
			res = append(res, inc)
		}
	}
	return res, nil
}

func (m *mockRepository) GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []Incentive
	for _, inc := range m.userIncentives {
		if inc.UserID == userID && inc.Status == IncentiveStatusPending {
			res = append(res, inc)
		}
	}
	return res, nil
}

func (m *mockRepository) GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	summary := &UserIncentiveSummary{}
	for _, inc := range m.userIncentives {
		if inc.UserID != userID {
			continue
		}
		summary.TotalEarned += inc.Amount
		if inc.Status == IncentiveStatusClaimed {
			summary.TotalClaimed += inc.Amount
		}
		if inc.Status == IncentiveStatusPending {
			summary.PendingAmount += inc.Amount
		}
		if inc.Type == IncentiveTypeReferral {
			summary.ReferralCount++
		}
	}
	if m.streak != nil && m.streak.UserID == userID {
		summary.CurrentStreak = m.streak.CurrentStreak
		summary.LongestStreak = m.streak.LongestStreak
		summary.BonusTier = m.streak.BonusTier
	} else {
		summary.BonusTier = 1
	}
	return summary, nil
}

func (m *mockRepository) UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.userIncentives {
		if m.userIncentives[i].ID == id {
			m.userIncentives[i].Status = status
			return nil
		}
	}
	return ErrIncentiveNotFound
}

func (m *mockRepository) GetStreak(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streak != nil && m.streak.UserID == userID {
		copy := *m.streak
		return &copy, nil
	}
	return nil, errors.New("not found")
}

func (m *mockRepository) UpsertStreak(ctx context.Context, streak *SavingsStreak) (*SavingsStreak, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streak = streak
	copy := *streak
	return &copy, nil
}

func (m *mockRepository) FindStreakByUserID(ctx context.Context, userID uuid.UUID) (*SavingsStreak, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streak != nil && m.streak.UserID == userID {
		copy := *m.streak
		return &copy, nil
	}
	return nil, ErrIncentiveNotFound
}

func (m *mockRepository) CreateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streak = streak
	return nil
}

func (m *mockRepository) UpdateSavingsStreak(ctx context.Context, streak *SavingsStreak) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streak = streak
	return nil
}

func (m *mockRepository) GetConfig(ctx context.Context) (*IncentiveConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config, nil
}

func (m *mockRepository) UpdateConfig(ctx context.Context, config *IncentiveConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
	return nil
}

func (m *mockRepository) CreateConfig(ctx context.Context, config *IncentiveConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
	return nil
}

func (m *mockRepository) Transact(ctx context.Context, fn func(repo Repository) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(m)
}
