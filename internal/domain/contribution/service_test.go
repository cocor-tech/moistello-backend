package contribution_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/contribution"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestContributionService_Record_Success(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "txn-abc123",
	}

	repo.On("FindByTxnHash", ctx, "txn-abc123").Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.AnythingOfType("*contribution.Contribution")).Return(nil)

	c, err := svc.Record(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, c)
	assert.Equal(t, contribution.StatusPending, c.Status)
	assert.Equal(t, 100.0, c.Amount)
	assert.True(t, c.OnTime)
	repo.AssertExpectations(t)
}

func TestContributionService_Record_Conflict(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "txn-abc123",
	}

	repo.On("FindByTxnHash", ctx, "txn-abc123").Return(nil, apperrors.ErrNotFound).Once()
	repo.On("Create", ctx, mock.AnythingOfType("*contribution.Contribution")).Return(apperrors.ErrConflict)
	// After conflict, second FindByTxnHash also returns not found (no recovery)
	repo.On("FindByTxnHash", ctx, "txn-abc123").Return(nil, apperrors.ErrNotFound).Once()

	c, err := svc.Record(ctx, input)

	assert.Error(t, err)
	assert.Nil(t, c)
	repo.AssertExpectations(t)
}

func TestContributionService_Record_InvalidUUID(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    "not-a-uuid",
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "txn-abc123",
	}

	// FindByTxnHash is called before UUID parse for idempotency check.
	repo.On("FindByTxnHash", ctx, "txn-abc123").Return(nil, apperrors.ErrNotFound)

	c, err := svc.Record(ctx, input)

	assert.Error(t, err)
	assert.Nil(t, c)
}

func TestContributionService_GetUserHistory_Success(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()
	userID := uuid.New().String()

	contribs := []contribution.Contribution{
		{ID: uuid.New(), Amount: 100, Status: contribution.StatusConfirmed},
		{ID: uuid.New(), Amount: 200, Status: contribution.StatusPending},
	}
	repo.On("ListByUser", ctx, mock.AnythingOfType("uuid.UUID"), 1, 10).Return(contribs, 2, nil)

	result, total, err := svc.GetUserHistory(ctx, userID, 1, 10)

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, 2, total)
	repo.AssertExpectations(t)
}

func TestContributionService_GetCircleHistory_Success(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()
	circleID := uuid.New().String()

	contribs := []contribution.Contribution{
		{ID: uuid.New(), Amount: 100, Status: contribution.StatusConfirmed},
	}
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 10).Return(contribs, 1, nil)

	result, total, err := svc.GetCircleHistory(ctx, circleID, 1, 10)

	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 1, total)
	repo.AssertExpectations(t)
}

func TestContributionService_GetUserHistory_InvalidUUID(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	_, _, err := svc.GetUserHistory(ctx, "not-a-uuid", 1, 10)

	assert.Error(t, err)
}

func TestContributionService_GetUserHistory_Empty(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()
	userID := uuid.New().String()

	repo.On("ListByUser", ctx, mock.AnythingOfType("uuid.UUID"), 1, 10).Return([]contribution.Contribution{}, 0, nil)

	result, total, err := svc.GetUserHistory(ctx, userID, 1, 10)

	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, 0, total)
	repo.AssertExpectations(t)
}

func TestContributionService_Record_VerificationStatus(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	verifiedTrue := true
	customStatus := contribution.VerificationStatusVerified

	input := contribution.RecordInput{
		CircleID:           uuid.New().String(),
		UserID:             uuid.New().String(),
		RoundNumber:        1,
		Amount:             100.0,
		TxnHash:            "txn-verified-123",
		VerifiedOnchain:    &verifiedTrue,
		VerificationStatus: &customStatus,
	}

	repo.On("FindByTxnHash", ctx, "txn-verified-123").Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerifiedOnchain == true && c.VerificationStatus == contribution.VerificationStatusVerified
	})).Return(nil)

	c, err := svc.Record(ctx, input)
	assert.NoError(t, err)
	assert.NotNil(t, c)
	assert.True(t, c.VerifiedOnchain)
	assert.Equal(t, contribution.VerificationStatusVerified, c.VerificationStatus)
	repo.AssertExpectations(t)
}

func TestContributionService_UpdateVerification(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()
	contribID := uuid.New()

	repo.On("UpdateVerificationStatus", ctx, contribID, true, contribution.VerificationStatusVerified).Return(nil)

	err := svc.UpdateVerification(ctx, contribID.String(), true, contribution.VerificationStatusVerified)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}
