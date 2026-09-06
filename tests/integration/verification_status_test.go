package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/contribution"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	"github.com/moistello/backend/internal/domain/payout"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestIntegration_Contribution_VerificationStatusFlow(t *testing.T) {
	ctx := context.Background()
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil, "")

	circleID := uuid.New().String()
	userID := uuid.New().String()

	t.Run("client-supplied contribution defaults to unverified", func(t *testing.T) {
		input := contribution.RecordInput{
			CircleID:    circleID,
			UserID:      userID,
			RoundNumber: 1,
			Amount:      100.0,
			TxnHash:     "tx_client_unverified_1",
		}

		repo.On("FindByTxnHash", ctx, "tx_client_unverified_1").Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
			return c.VerifiedOnchain == false &&
				c.VerificationStatus == contribution.VerificationStatusUnverified &&
				c.Amount == 100.0 &&
				c.TxnHash.String == "tx_client_unverified_1"
		})).Return(nil).Once()

		record, err := svc.Record(ctx, input)
		require.NoError(t, err)
		assert.False(t, record.VerifiedOnchain)
		assert.Equal(t, contribution.VerificationStatusUnverified, record.VerificationStatus)
	})

	t.Run("indexer or verified flow creates verified record", func(t *testing.T) {
		verified := true
		status := contribution.VerificationStatusVerified

		input := contribution.RecordInput{
			CircleID:           circleID,
			UserID:             userID,
			RoundNumber:        2,
			Amount:             100.0,
			TxnHash:            "tx_verified_onchain_2",
			VerifiedOnchain:    &verified,
			VerificationStatus: &status,
		}

		repo.On("FindByTxnHash", ctx, "tx_verified_onchain_2").Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
			return c.VerifiedOnchain == true &&
				c.VerificationStatus == contribution.VerificationStatusVerified &&
				c.Amount == 100.0
		})).Return(nil).Once()

		record, err := svc.Record(ctx, input)
		require.NoError(t, err)
		assert.True(t, record.VerifiedOnchain)
		assert.Equal(t, contribution.VerificationStatusVerified, record.VerificationStatus)
	})

	t.Run("update verification status on existing contribution", func(t *testing.T) {
		contribID := uuid.New()
		repo.On("UpdateVerificationStatus", ctx, contribID, true, contribution.VerificationStatusVerified).Return(nil).Once()

		err := svc.UpdateVerification(ctx, contribID.String(), true, contribution.VerificationStatusVerified)
		require.NoError(t, err)
	})

	repo.AssertExpectations(t)
}

func TestIntegration_Payout_VerificationStatusFlow(t *testing.T) {
	ctx := context.Background()
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)

	circleUUID := uuid.New()
	circleID := circleUUID.String()
	recipientID := uuid.New().String()

	repo.On("ListByCircle", ctx, circleUUID, 1, 100).Return([]payout.Payout{}, 0, nil)

	t.Run("client-supplied payout defaults to unverified", func(t *testing.T) {
		input := payout.RecordInput{
			CircleID:    circleID,
			RecipientID: recipientID,
			RoundNumber: 1,
			Amount:      500.0,
			FeeAmount:   5.0,
			TxnHash:     "tx_payout_unverified_1",
			PayoutType:  payout.PayoutTypeFixed,
		}

		repo.On("FindByTxnHash", ctx, "tx_payout_unverified_1").Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
			return p.VerifiedOnchain == false &&
				p.VerificationStatus == payout.VerificationStatusUnverified &&
				p.Amount == 500.0
		})).Return(nil).Once()

		record, err := svc.Record(ctx, input)
		require.NoError(t, err)
		assert.False(t, record.VerifiedOnchain)
		assert.Equal(t, payout.VerificationStatusUnverified, record.VerificationStatus)
	})

	t.Run("verified onchain payout execution", func(t *testing.T) {
		verified := true
		status := payout.VerificationStatusVerified

		input := payout.RecordInput{
			CircleID:           circleID,
			RecipientID:        recipientID,
			RoundNumber:        2,
			Amount:             500.0,
			FeeAmount:          5.0,
			TxnHash:            "tx_payout_verified_2",
			PayoutType:         payout.PayoutTypeAuction,
			VerifiedOnchain:    &verified,
			VerificationStatus: &status,
		}

		repo.On("FindByTxnHash", ctx, "tx_payout_verified_2").Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
			return p.VerifiedOnchain == true &&
				p.VerificationStatus == payout.VerificationStatusVerified
		})).Return(nil).Once()

		record, err := svc.Record(ctx, input)
		require.NoError(t, err)
		assert.True(t, record.VerifiedOnchain)
		assert.Equal(t, payout.VerificationStatusVerified, record.VerificationStatus)
	})

	t.Run("update verification status on existing payout", func(t *testing.T) {
		payoutID := uuid.New()
		repo.On("UpdateVerificationStatus", ctx, payoutID, true, payout.VerificationStatusVerified).Return(nil).Once()

		err := svc.UpdateVerification(ctx, payoutID.String(), true, payout.VerificationStatusVerified)
		require.NoError(t, err)
	})

	repo.AssertExpectations(t)
}
