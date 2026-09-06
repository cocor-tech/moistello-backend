package payout_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/payout"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

// mockPayoutHorizon is a test double for payout.HorizonVerifier.
type mockPayoutHorizon struct{ mock.Mock }

func (m *mockPayoutHorizon) VerifyPayment(ctx context.Context, txnHash, to, amount string) (bool, error) {
	args := m.Called(ctx, txnHash, to, amount)
	return args.Bool(0), args.Error(1)
}

// mockWalletLookup is a test double for payout.WalletLookup.
type mockWalletLookup struct{ mock.Mock }

func (m *mockWalletLookup) WalletAddressForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

// --------------------------------------------------------------------------
// Happy-path: horizon confirms payout to the correct recipient wallet
// --------------------------------------------------------------------------

func TestPayoutService_Record_VerifiesOnChain_Success(t *testing.T) {
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	wallets := new(mockWalletLookup)
	svc := payout.NewService(repo, horizon, wallets)
	ctx := context.Background()

	recipientID := uuid.New()
	recipientWallet := "GCEZWKCA5VLDNRLN3RPRJMRZOX3Z6G5CHCGVEF1MSIFWIO6CUDR5"

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: recipientID.String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   5.0,
		TxnHash:     "valid-payout-txn",
		PayoutType:  payout.PayoutTypeFixed,
	}

	// No prior record.
	repo.On("FindByTxnHash", ctx, "valid-payout-txn").Return(nil, apperrors.ErrNotFound)
	// No duplicate in same circle/round.
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
	// Wallet lookup succeeds.
	wallets.On("WalletAddressForUser", ctx, recipientID).Return(recipientWallet, nil)
	// Horizon confirms payment to recipient for the amount.
	horizon.On("VerifyPayment", ctx, "valid-payout-txn", recipientWallet, "500.0000000").Return(true, nil)
	// Record is created as verified.
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerifiedOnchain == true &&
			p.VerificationStatus == payout.VerificationStatusVerified &&
			p.TxnHash.String == "valid-payout-txn"
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.True(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusVerified, p.VerificationStatus)

	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
	wallets.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Security: organizer cannot record a payout to an arbitrary address
// --------------------------------------------------------------------------

func TestPayoutService_Record_RejectsWrongRecipient(t *testing.T) {
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	wallets := new(mockWalletLookup)
	svc := payout.NewService(repo, horizon, wallets)
	ctx := context.Background()

	recipientID := uuid.New()
	legitWallet := "GCEZWKCA5VLDNRLN3RPRJMRZOX3Z6G5CHCGVEF1MSIFWIO6CUDR5"

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: recipientID.String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   0,
		TxnHash:     "wrong-recipient-txn",
		PayoutType:  payout.PayoutTypeFixed,
	}

	repo.On("FindByTxnHash", ctx, "wrong-recipient-txn").Return(nil, apperrors.ErrNotFound)
	// ListByCircle is called for duplicate-round check after verification passes,
	// but here verification rejects first — so we don't expect it.
	wallets.On("WalletAddressForUser", ctx, recipientID).Return(legitWallet, nil)
	// Horizon says the payment went to a DIFFERENT address — mismatch.
	horizon.On("VerifyPayment", ctx, "wrong-recipient-txn", legitWallet, "500.0000000").Return(false, nil)

	p, err := svc.Record(ctx, input)

	assert.Error(t, err)
	assert.Nil(t, p)
	assert.Contains(t, err.Error(), "on-chain verification failed")

	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
	wallets.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Security: replay protection — same payout txnHash submitted twice
// --------------------------------------------------------------------------

func TestPayoutService_Record_IdempotentOnReplay(t *testing.T) {
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	svc := payout.NewService(repo, horizon, nil)
	ctx := context.Background()

	existingPayout := &payout.Payout{
		ID:                 uuid.New(),
		Amount:             500.0,
		VerifiedOnchain:    true,
		VerificationStatus: payout.VerificationStatusVerified,
	}

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		TxnHash:     "replayed-payout-txn",
		PayoutType:  payout.PayoutTypeFixed,
	}

	// FindByTxnHash returns the existing payout — replay detected.
	repo.On("FindByTxnHash", ctx, "replayed-payout-txn").Return(existingPayout, nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err, "replay must succeed (idempotent)")
	require.NotNil(t, p)
	assert.Equal(t, existingPayout.ID, p.ID, "must return the original payout record")

	// Horizon must NOT be consulted and no new DB row must be created.
	horizon.AssertNotCalled(t, "VerifyPayment", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Horizon unavailable: record as pending (soft failure)
// --------------------------------------------------------------------------

func TestPayoutService_Record_PendingWhenHorizonUnavailable(t *testing.T) {
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	wallets := new(mockWalletLookup)
	svc := payout.NewService(repo, horizon, wallets)
	ctx := context.Background()

	recipientID := uuid.New()

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: recipientID.String(),
		RoundNumber: 1,
		Amount:      300.0,
		TxnHash:     "horizon-down-txn",
		PayoutType:  payout.PayoutTypeRandom,
	}

	repo.On("FindByTxnHash", ctx, "horizon-down-txn").Return(nil, apperrors.ErrNotFound)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
	wallets.On("WalletAddressForUser", ctx, recipientID).Return("GWALLET...", nil)
	// Horizon is down.
	horizon.On("VerifyPayment", ctx, "horizon-down-txn", "GWALLET...", "300.0000000").
		Return(false, errors.New("connection refused"))
	// Record is persisted but marked pending.
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerificationStatus == payout.VerificationStatusPending &&
			p.VerifiedOnchain == false
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.False(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusPending, p.VerificationStatus)

	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Wallet lookup fails: record as pending
// --------------------------------------------------------------------------

func TestPayoutService_Record_PendingWhenWalletLookupFails(t *testing.T) {
	repo := new(payoutMocks.Repository)
	horizon := new(mockPayoutHorizon)
	wallets := new(mockWalletLookup)
	svc := payout.NewService(repo, horizon, wallets)
	ctx := context.Background()

	recipientID := uuid.New()

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: recipientID.String(),
		RoundNumber: 1,
		Amount:      250.0,
		TxnHash:     "wallet-lookup-fail-txn",
		PayoutType:  payout.PayoutTypeVote,
	}

	repo.On("FindByTxnHash", ctx, "wallet-lookup-fail-txn").Return(nil, apperrors.ErrNotFound)
	repo.On("ListByCircle", ctx, mock.AnythingOfType("uuid.UUID"), 1, 100).Return([]payout.Payout{}, 0, nil)
	// Wallet lookup fails (e.g. user was deleted).
	wallets.On("WalletAddressForUser", ctx, recipientID).Return("", errors.New("user not found"))
	// Still persisted as pending — horizon is not consulted.
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerificationStatus == payout.VerificationStatusPending &&
			p.VerifiedOnchain == false
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, payout.VerificationStatusPending, p.VerificationStatus)

	horizon.AssertNotCalled(t, "VerifyPayment", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	wallets.AssertExpectations(t)
}
