package contribution_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/contribution"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

// mockHorizon is a test double for contribution.HorizonVerifier.
type mockHorizon struct{ mock.Mock }

func (m *mockHorizon) VerifyTransaction(ctx context.Context, txnHash, from, amount string) (bool, error) {
	args := m.Called(ctx, txnHash, from, amount)
	return args.Bool(0), args.Error(1)
}

// --------------------------------------------------------------------------
// Happy-path: horizon confirms the transaction
// --------------------------------------------------------------------------

func TestContributionService_Record_VerifiesOnChain_Success(t *testing.T) {
	repo := new(contribMocks.Repository)
	horizon := new(mockHorizon)
	svc := contribution.NewService(repo, nil, nil, horizon, "MASTER_PUBLIC_KEY")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "valid-txn-hash-001",
	}

	// No prior record for this hash.
	repo.On("FindByTxnHash", ctx, "valid-txn-hash-001").Return(nil, apperrors.ErrNotFound)
	// Horizon confirms the transaction.
	horizon.On("VerifyTransaction", ctx, "valid-txn-hash-001", "MASTER_PUBLIC_KEY", "100.0000000").Return(true, nil)
	// Record is persisted as verified.
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerifiedOnchain == true &&
			c.VerificationStatus == contribution.VerificationStatusVerified &&
			c.Status == contribution.StatusConfirmed &&
			c.TxnHash.String == "valid-txn-hash-001"
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, c)
	assert.True(t, c.VerifiedOnchain, "contribution must be marked verified_onchain=true")
	assert.Equal(t, contribution.VerificationStatusVerified, c.VerificationStatus)
	assert.Equal(t, contribution.StatusConfirmed, c.Status)

	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Security: mismatched txnHash must be rejected
// --------------------------------------------------------------------------

func TestContributionService_Record_RejectsOnChainMismatch(t *testing.T) {
	repo := new(contribMocks.Repository)
	horizon := new(mockHorizon)
	svc := contribution.NewService(repo, nil, nil, horizon, "MASTER_PUBLIC_KEY")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		TxnHash:     "forged-txn-hash",
	}

	repo.On("FindByTxnHash", ctx, "forged-txn-hash").Return(nil, apperrors.ErrNotFound)
	// Horizon says the transaction does NOT match sender/amount.
	horizon.On("VerifyTransaction", ctx, "forged-txn-hash", "MASTER_PUBLIC_KEY", "500.0000000").Return(false, nil)

	c, err := svc.Record(ctx, input)

	assert.Error(t, err, "mismatched txnHash must return an error")
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "on-chain verification failed")

	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Security: replay protection — same txnHash submitted twice
// --------------------------------------------------------------------------

func TestContributionService_Record_IdempotentOnReplay(t *testing.T) {
	repo := new(contribMocks.Repository)
	horizon := new(mockHorizon)
	svc := contribution.NewService(repo, nil, nil, horizon, "MASTER_PUBLIC_KEY")
	ctx := context.Background()

	existing := &contribution.Contribution{
		ID:                 uuid.New(),
		Amount:             100.0,
		TxnHash:            sql.NullString{String: "replayed-txn", Valid: true},
		VerifiedOnchain:    true,
		VerificationStatus: contribution.VerificationStatusVerified,
		Status:             contribution.StatusConfirmed,
	}

	input := contribution.RecordInput{
		CircleID:    existing.CircleID.String(),
		UserID:      existing.UserID.String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "replayed-txn",
	}

	// FindByTxnHash returns the existing record — replay detected.
	repo.On("FindByTxnHash", ctx, "replayed-txn").Return(existing, nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err, "replay must succeed (idempotent)")
	require.NotNil(t, c)
	assert.Equal(t, existing.ID, c.ID, "must return the existing record")

	// Horizon must NOT be called and no new DB row must be created.
	horizon.AssertNotCalled(t, "VerifyTransaction", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Horizon unavailable: record as pending (soft failure)
// --------------------------------------------------------------------------

func TestContributionService_Record_PendingWhenHorizonUnavailable(t *testing.T) {
	repo := new(contribMocks.Repository)
	horizon := new(mockHorizon)
	svc := contribution.NewService(repo, nil, nil, horizon, "MASTER_PUBLIC_KEY")
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      75.0,
		TxnHash:     "offline-txn-hash",
	}

	repo.On("FindByTxnHash", ctx, "offline-txn-hash").Return(nil, apperrors.ErrNotFound)
	// Horizon returns a transient network error.
	horizon.On("VerifyTransaction", ctx, "offline-txn-hash", "MASTER_PUBLIC_KEY", "75.0000000").
		Return(false, errors.New("connection refused"))
	// Record is still persisted but marked as pending verification.
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerificationStatus == contribution.VerificationStatusPending &&
			c.VerifiedOnchain == false
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, c)
	assert.False(t, c.VerifiedOnchain)
	assert.Equal(t, contribution.VerificationStatusPending, c.VerificationStatus)

	repo.AssertExpectations(t)
	horizon.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// No horizon client — caller-supplied verification state is used (indexer path)
// --------------------------------------------------------------------------

func TestContributionService_Record_UsesCallerVerificationState(t *testing.T) {
	repo := new(contribMocks.Repository)
	// No horizon client — verification state comes from caller.
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	verified := true
	status := contribution.VerificationStatusVerified

	input := contribution.RecordInput{
		CircleID:           uuid.New().String(),
		UserID:             uuid.New().String(),
		RoundNumber:        1,
		Amount:             200.0,
		TxnHash:            "indexer-confirmed-txn",
		VerifiedOnchain:    &verified,
		VerificationStatus: &status,
	}

	repo.On("FindByTxnHash", ctx, "indexer-confirmed-txn").Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerifiedOnchain == true &&
			c.VerificationStatus == contribution.VerificationStatusVerified
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	assert.True(t, c.VerifiedOnchain)
	assert.Equal(t, contribution.VerificationStatusVerified, c.VerificationStatus)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Conflict error on Create (race) is handled gracefully
// --------------------------------------------------------------------------

func TestContributionService_Record_HandlesCreateConflict(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")
	ctx := context.Background()

	existing := &contribution.Contribution{
		ID:     uuid.New(),
		Amount: 100.0,
	}

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "race-condition-txn",
	}

	repo.On("FindByTxnHash", ctx, "race-condition-txn").Return(nil, apperrors.ErrNotFound).Once()
	repo.On("Create", ctx, mock.AnythingOfType("*contribution.Contribution")).Return(apperrors.ErrConflict)
	// After the conflict, FindByTxnHash returns the row created by the other request.
	repo.On("FindByTxnHash", ctx, "race-condition-txn").Return(existing, nil).Once()

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	assert.Equal(t, existing.ID, c.ID)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// Compile-time import check to keep sql in scope.
var _ sql.NullString
