package payout_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/payout"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestPayoutService_Record_Success(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   5.0,
		TxnHash:     "txn-payout-123",
		PayoutType:  payout.PayoutTypeFixed,
	}

	repo.On("FindByTxnHash", ctx, "txn-payout-123").Return(nil, apperrors.ErrNotFound)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)

	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.Amount == 500.0 &&
			p.PayoutType == payout.PayoutTypeFixed &&
			p.VerifiedOnchain == false &&
			p.VerificationStatus == payout.VerificationStatusUnverified
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.False(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusUnverified, p.VerificationStatus)
	assert.Equal(t, 500.0, p.Amount)
	repo.AssertExpectations(t)
}

func TestPayoutService_Record_VerifiedOnchain(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	verifiedTrue := true
	customStatus := payout.VerificationStatusVerified

	input := payout.RecordInput{
		CircleID:           uuid.New().String(),
		RecipientID:        uuid.New().String(),
		RoundNumber:        1,
		Amount:             500.0,
		FeeAmount:          5.0,
		TxnHash:            "txn-payout-456",
		PayoutType:         payout.PayoutTypeAuction,
		VerifiedOnchain:    &verifiedTrue,
		VerificationStatus: &customStatus,
	}

	repo.On("FindByTxnHash", ctx, "txn-payout-456").Return(nil, apperrors.ErrNotFound)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)

	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerifiedOnchain == true && p.VerificationStatus == payout.VerificationStatusVerified
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.True(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusVerified, p.VerificationStatus)
	repo.AssertExpectations(t)
}

func TestPayoutService_UpdateVerification(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	payoutID := uuid.New()

	repo.On("UpdateVerificationStatus", ctx, payoutID, true, payout.VerificationStatusVerified).Return(nil)

	err := svc.UpdateVerification(ctx, payoutID.String(), true, payout.VerificationStatusVerified)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestPayoutService_GetUserHistory(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	userID := uuid.New()

	payouts := []payout.Payout{
		{ID: uuid.New(), Amount: 500.0, VerifiedOnchain: true, VerificationStatus: payout.VerificationStatusVerified},
	}
	repo.On("ListByUser", ctx, userID, 1, 10).Return(payouts, 1, nil)

	res, total, err := svc.GetUserHistory(ctx, userID.String(), 1, 10)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, 1, total)
	repo.AssertExpectations(t)
}

func TestPayoutService_GetCircleHistory(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	circleID := uuid.New()

	payouts := []payout.Payout{
		{ID: uuid.New(), Amount: 1000.0, VerifiedOnchain: false, VerificationStatus: payout.VerificationStatusUnverified},
	}
	repo.On("ListByCircle", ctx, circleID, 1, 10).Return(payouts, 1, nil)

	res, total, err := svc.GetCircleHistory(ctx, circleID.String(), 1, 10)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, 1, total)
	repo.AssertExpectations(t)
}
