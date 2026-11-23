package payout_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/payout"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

var errDB = errors.New("db down")

// fakeCircle implements only what payout.Record uses; the embedded nil
// interface panics if any other method is ever called, which is the point.
type fakeCircle struct {
	circle.Service
	isMember    bool
	isMemberErr error
	cir         *circle.Circle
	getErr      error
}

func (f *fakeCircle) IsMember(context.Context, string, string) (bool, error) {
	return f.isMember, f.isMemberErr
}

func (f *fakeCircle) Get(context.Context, string) (*circle.Circle, error) {
	return f.cir, f.getErr
}

func validRecordInput() payout.RecordInput {
	return payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      10,
		PayoutType:  payout.PayoutTypeFixed,
	}
}

func TestPayoutService_Record_InvalidIDs(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil, nil)

	badCircle := validRecordInput()
	badCircle.CircleID = "nope"
	_, err := svc.Record(ctx, badCircle)
	assert.ErrorContains(t, err, "invalid circleID")

	badRecipient := validRecordInput()
	badRecipient.RecipientID = "nope"
	_, err = svc.Record(ctx, badRecipient)
	assert.ErrorContains(t, err, "invalid recipientID")

	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestPayoutService_Record_MembershipCheckErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("membership lookup fails", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil, &fakeCircle{isMemberErr: errDB})
		_, err := svc.Record(ctx, validRecordInput())
		assert.ErrorIs(t, err, errDB)
		repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("recipient not a member", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil, &fakeCircle{isMember: false})
		_, err := svc.Record(ctx, validRecordInput())
		assert.ErrorContains(t, err, "not a member")
		repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("circle lookup fails", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil, &fakeCircle{isMember: true, getErr: errDB})
		_, err := svc.Record(ctx, validRecordInput())
		assert.ErrorIs(t, err, errDB)
	})
}

func TestPayoutService_Record_RoundAfterCurrentIsNotOnTime(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
	repo.On("Create", ctx, mock.Anything).Return(nil)
	svc := payout.NewService(repo, nil, nil, &fakeCircle{isMember: true, cir: &circle.Circle{CurrentRound: 2}})

	input := validRecordInput()
	input.RoundNumber = 5
	p, err := svc.Record(ctx, input)
	require.NoError(t, err)
	assert.False(t, p.OnTime)
}

func TestPayoutService_Record_SetCircleServiceEnablesMembershipCheck(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil, nil)

	withCircle, ok := svc.(payout.ServiceWithCircle)
	require.True(t, ok)
	withCircle.SetCircleService(&fakeCircle{isMember: false})

	_, err := svc.Record(ctx, validRecordInput())
	assert.ErrorContains(t, err, "not a member")
}

func TestPayoutService_Record_CreateErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("conflict with txn hash returns the racing row", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		existing := &payout.Payout{ID: uuid.New(), Amount: 7}
		repo.On("FindByTxnHash", ctx, "txn-1").Return(nil, apperrors.ErrNotFound).Once()
		repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
		repo.On("Create", ctx, mock.Anything).Return(apperrors.ErrConflict)
		repo.On("FindByTxnHash", ctx, "txn-1").Return(existing, nil).Once()
		svc := payout.NewService(repo, nil, nil, nil)

		input := validRecordInput()
		input.TxnHash = "txn-1"
		got, err := svc.Record(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, existing, got)
	})

	t.Run("conflict without txn hash is an error", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
		repo.On("Create", ctx, mock.Anything).Return(apperrors.ErrConflict)
		svc := payout.NewService(repo, nil, nil, nil)

		_, err := svc.Record(ctx, validRecordInput())
		assert.ErrorIs(t, err, apperrors.ErrConflict)
	})

	t.Run("generic create failure is wrapped", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
		repo.On("Create", ctx, mock.Anything).Return(errDB)
		svc := payout.NewService(repo, nil, nil, nil)

		_, err := svc.Record(ctx, validRecordInput())
		assert.ErrorIs(t, err, errDB)
	})
}

func TestPayoutService_Record_RejectsNonFiniteAmountWhenVerifying(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	wallets := new(mockWalletLookup)
	wallets.On("WalletAddressForUser", ctx, mock.AnythingOfType("uuid.UUID")).Return("GWALLET", nil)
	repo.On("FindByTxnHash", ctx, "txn-nan").Return(nil, apperrors.ErrNotFound)
	svc := payout.NewService(repo, horizon, wallets, nil)

	input := validRecordInput()
	input.TxnHash = "txn-nan"
	input.Amount = math.NaN()
	_, err := svc.Record(ctx, input)
	assert.ErrorContains(t, err, "invalid payout amount")
	horizon.AssertNotCalled(t, "VerifyPayment", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPayoutService_Record_PendingWhenNoWalletLookupConfigured(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	repo.On("FindByTxnHash", ctx, "txn-2").Return(nil, apperrors.ErrNotFound)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerificationStatus == payout.VerificationStatusPending && !p.VerifiedOnchain
	})).Return(nil)
	svc := payout.NewService(repo, new(mockPayoutHorizon), nil, nil)

	input := validRecordInput()
	input.TxnHash = "txn-2"
	p, err := svc.Record(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, payout.VerificationStatusPending, p.VerificationStatus)
}

func TestPayoutService_HistoryAndVerificationRejectInvalidIDs(t *testing.T) {
	ctx := context.Background()
	svc := payout.NewService(new(payoutMocks.Repository), nil, nil, nil)

	_, _, err := svc.GetUserHistory(ctx, "bad", 1, 10)
	assert.ErrorContains(t, err, "invalid userID")

	_, _, err = svc.GetCircleHistory(ctx, "bad", 1, 10)
	assert.ErrorContains(t, err, "invalid circleID")

	err = svc.UpdateVerification(ctx, "bad", true, payout.VerificationStatusVerified)
	assert.ErrorContains(t, err, "invalid payout ID")
}

func TestPayoutService_UpdateVerification_PropagatesRepoErrors(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	repo := new(payoutMocks.Repository)
	repo.On("UpdateVerificationStatus", ctx, id, true, payout.VerificationStatusVerified).Return(apperrors.ErrNotFound)
	svc := payout.NewService(repo, nil, nil, nil)

	err := svc.UpdateVerification(ctx, id.String(), true, payout.VerificationStatusVerified)
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}
