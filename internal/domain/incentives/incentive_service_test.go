package incentives

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

// stubRepo wraps the in-memory mock and lets a test inject an error into a
// single repository call. Unset fields delegate to the mock.
type stubRepo struct {
	*mockRepository
	findByIDErr     error
	updateStatusErr error
	findByUserErr   error
	pendingErr      error
	summaryErr      error
	findByRefErr    error
	getConfigErr    error
	updateConfigErr error
	createConfigErr error
}

func (s *stubRepo) FindByID(ctx context.Context, id uuid.UUID) (*Incentive, error) {
	if s.findByIDErr != nil {
		return nil, s.findByIDErr
	}
	return s.mockRepository.FindByID(ctx, id)
}

func (s *stubRepo) UpdateIncentiveStatus(ctx context.Context, id uuid.UUID, status IncentiveStatus) error {
	if s.updateStatusErr != nil {
		return s.updateStatusErr
	}
	return s.mockRepository.UpdateIncentiveStatus(ctx, id, status)
}

func (s *stubRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	if s.findByUserErr != nil {
		return nil, s.findByUserErr
	}
	return s.mockRepository.FindByUserID(ctx, userID)
}

func (s *stubRepo) GetPendingIncentives(ctx context.Context, userID uuid.UUID) ([]Incentive, error) {
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	return s.mockRepository.GetPendingIncentives(ctx, userID)
}

func (s *stubRepo) GetUserIncentiveSummary(ctx context.Context, userID uuid.UUID) (*UserIncentiveSummary, error) {
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	return s.mockRepository.GetUserIncentiveSummary(ctx, userID)
}

func (s *stubRepo) FindByReferrerID(ctx context.Context, userID uuid.UUID) ([]Referral, error) {
	if s.findByRefErr != nil {
		return nil, s.findByRefErr
	}
	return s.mockRepository.FindByReferrerID(ctx, userID)
}

func (s *stubRepo) GetConfig(ctx context.Context) (*IncentiveConfig, error) {
	if s.getConfigErr != nil {
		return nil, s.getConfigErr
	}
	return s.mockRepository.GetConfig(ctx)
}

func (s *stubRepo) UpdateConfig(ctx context.Context, config *IncentiveConfig) error {
	if s.updateConfigErr != nil {
		return s.updateConfigErr
	}
	return s.mockRepository.UpdateConfig(ctx, config)
}

func (s *stubRepo) CreateConfig(ctx context.Context, config *IncentiveConfig) error {
	if s.createConfigErr != nil {
		return s.createConfigErr
	}
	return s.mockRepository.CreateConfig(ctx, config)
}

func pendingIncentive(userID uuid.UUID) Incentive {
	return Incentive{
		ID:     uuid.New(),
		UserID: userID,
		Status: IncentiveStatusPending,
	}
}

func TestClaimIncentive_HappyPath(t *testing.T) {
	userID := uuid.New()
	inc := pendingIncentive(userID)
	repo := &stubRepo{mockRepository: newMockRepository()}
	repo.userIncentives = []Incentive{inc}

	err := NewService(repo).ClaimIncentive(context.Background(), userID.String(), inc.ID.String())

	assert.NoError(t, err)
}

func TestClaimIncentive_InvalidIDs(t *testing.T) {
	svc := NewService(&stubRepo{mockRepository: newMockRepository()})
	validID := uuid.NewString()

	assert.Error(t, svc.ClaimIncentive(context.Background(), "not-a-uuid", validID))
	assert.Error(t, svc.ClaimIncentive(context.Background(), validID, "not-a-uuid"))
}

func TestClaimIncentive_FindError(t *testing.T) {
	repo := &stubRepo{mockRepository: newMockRepository(), findByIDErr: errors.New("db down")}

	err := NewService(repo).ClaimIncentive(context.Background(), uuid.NewString(), uuid.NewString())

	require.Error(t, err)
	assert.ErrorContains(t, err, "finding incentive")
	assert.ErrorContains(t, err, "db down")
}

func TestClaimIncentive_NotOwner(t *testing.T) {
	owner := uuid.New()
	inc := pendingIncentive(owner)
	repo := &stubRepo{mockRepository: newMockRepository()}
	repo.userIncentives = []Incentive{inc}

	err := NewService(repo).ClaimIncentive(context.Background(), uuid.NewString(), inc.ID.String())

	assert.ErrorContains(t, err, "does not belong to user")
}

func TestClaimIncentive_AlreadyClaimed(t *testing.T) {
	userID := uuid.New()
	inc := pendingIncentive(userID)
	inc.Status = IncentiveStatusClaimed
	repo := &stubRepo{mockRepository: newMockRepository()}
	repo.userIncentives = []Incentive{inc}

	err := NewService(repo).ClaimIncentive(context.Background(), userID.String(), inc.ID.String())

	assert.ErrorContains(t, err, "already claimed")
}

func TestClaimIncentive_Expired(t *testing.T) {
	userID := uuid.New()
	inc := pendingIncentive(userID)
	inc.ExpiresAt = sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
	repo := &stubRepo{mockRepository: newMockRepository()}
	repo.userIncentives = []Incentive{inc}

	err := NewService(repo).ClaimIncentive(context.Background(), userID.String(), inc.ID.String())

	assert.ErrorContains(t, err, "expired")
}

func TestClaimIncentive_UpdateError(t *testing.T) {
	userID := uuid.New()
	inc := pendingIncentive(userID)
	repo := &stubRepo{mockRepository: newMockRepository(), updateStatusErr: errors.New("write failed")}
	repo.userIncentives = []Incentive{inc}

	err := NewService(repo).ClaimIncentive(context.Background(), userID.String(), inc.ID.String())

	assert.ErrorContains(t, err, "claiming incentive")
	assert.ErrorContains(t, err, "write failed")
}

func TestIncentiveQueries_InvalidUserID(t *testing.T) {
	svc := NewService(&stubRepo{mockRepository: newMockRepository()})
	ctx := context.Background()

	_, err := svc.GetUserIncentives(ctx, "bad")
	assert.Error(t, err)
	_, err = svc.GetPendingIncentives(ctx, "bad")
	assert.Error(t, err)
	_, err = svc.GetUserSummary(ctx, "bad")
	assert.Error(t, err)
	_, err = svc.GetReferrals(ctx, "bad")
	assert.Error(t, err)
}

func TestIncentiveQueries_RepoError(t *testing.T) {
	boom := errors.New("repo failure")
	repo := &stubRepo{
		mockRepository: newMockRepository(),
		findByUserErr:  boom,
		pendingErr:     boom,
		summaryErr:     boom,
		findByRefErr:   boom,
	}
	svc := NewService(repo)
	id := uuid.NewString()
	ctx := context.Background()

	_, err := svc.GetUserIncentives(ctx, id)
	assert.ErrorIs(t, err, boom)
	_, err = svc.GetPendingIncentives(ctx, id)
	assert.ErrorIs(t, err, boom)
	_, err = svc.GetUserSummary(ctx, id)
	assert.ErrorIs(t, err, boom)
	_, err = svc.GetReferrals(ctx, id)
	assert.ErrorIs(t, err, boom)
}

func TestGetConfig_RepoError(t *testing.T) {
	boom := errors.New("config unavailable")
	repo := &stubRepo{mockRepository: newMockRepository(), getConfigErr: boom}

	cfg, err := NewService(repo).GetConfig(context.Background())

	assert.ErrorIs(t, err, boom)
	assert.Nil(t, cfg)
}

func TestUpdateConfig_UpdateError(t *testing.T) {
	boom := errors.New("write failed")
	repo := &stubRepo{mockRepository: newMockRepository(), updateConfigErr: boom}

	err := NewService(repo).UpdateConfig(context.Background(), &IncentiveConfig{})

	assert.ErrorIs(t, err, boom)
}

func TestUpdateConfig_NotFoundCreatesConfig(t *testing.T) {
	repo := &stubRepo{mockRepository: newMockRepository(), updateConfigErr: apperrors.ErrNotFound}
	cfg := &IncentiveConfig{}

	err := NewService(repo).UpdateConfig(context.Background(), cfg)

	assert.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, cfg.ID, "a new config must receive an ID")
	assert.False(t, cfg.CreatedAt.IsZero())
}

func TestUpdateConfig_CreateError(t *testing.T) {
	boom := errors.New("insert failed")
	repo := &stubRepo{
		mockRepository:  newMockRepository(),
		updateConfigErr: apperrors.ErrNotFound,
		createConfigErr: boom,
	}

	err := NewService(repo).UpdateConfig(context.Background(), &IncentiveConfig{})

	assert.ErrorIs(t, err, boom)
}
