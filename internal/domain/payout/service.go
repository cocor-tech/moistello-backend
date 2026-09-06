package payout

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/metrics"
	"github.com/rs/zerolog/log"
)

// RecordInput carries all fields needed to record a payout.
type RecordInput struct {
	CircleID    string
	RecipientID string
	RoundNumber int
	Amount      float64
	FeeAmount   float64
	TxnHash     string
	PayoutType  PayoutType
	// Optional overrides — used by the indexer / tests to set verification
	// state directly without going through the Horizon check.
	VerifiedOnchain    *bool
	VerificationStatus *VerificationStatus
}

// HorizonVerifier is satisfied by *stellar.Client.
type HorizonVerifier interface {
	// VerifyPayment checks that txnHash exists, was successful, and has a
	// payment TO expectedTo for expectedAmount.
	VerifyPayment(ctx context.Context, txnHash, expectedTo, expectedAmount string) (bool, error)
}

// WalletLookup resolves a user's on-chain Stellar wallet address by their UUID.
// Implemented in main.go by a thin shim over user.Repository to avoid a
// direct dependency on the user domain.
type WalletLookup interface {
	WalletAddressForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service is the public API for the payout domain.
type Service interface {
	// Record persists a new payout, verifying the txnHash on-chain before
	// saving. Idempotent: the same txnHash submitted twice returns the
	// already-recorded payout.
	Record(ctx context.Context, input RecordInput) (*Payout, error)
	GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Payout, int, error)
	GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Payout, int, error)
	UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error
}

type service struct {
	repo         Repository
	horizon      HorizonVerifier
	walletLookup WalletLookup
}

// NewService constructs the payout service.
//
//	horizon      – may be nil (on-chain verification skipped; useful in tests)
//	walletLookup – may be nil (recipient wallet check skipped)
//
// The third parameter is typed as interface{} so that callers can pass a
// concrete *user.pgRepo (which satisfies WalletLookup when wrapped) or nil.
// If it already satisfies WalletLookup it is used directly; otherwise it is
// ignored.
func NewService(repo Repository, horizon HorizonVerifier, walletLookup interface{}) Service {
	var wl WalletLookup
	if walletLookup != nil {
		if v, ok := walletLookup.(WalletLookup); ok {
			wl = v
		}
		// If it doesn't satisfy WalletLookup (e.g. bare *user.pgRepo),
		// main.go must wrap it with a NewWalletLookupAdapter first.
	}
	return &service{
		repo:         repo,
		horizon:      horizon,
		walletLookup: wl,
	}
}

// Record verifies the on-chain transaction and persists the payout.
// The call is idempotent: submitting the same txnHash twice returns the
// already-recorded payout rather than an error.
func (s *service) Record(ctx context.Context, input RecordInput) (*Payout, error) {
	circleUID, err := uuid.Parse(input.CircleID)
	if err != nil {
		return nil, fmt.Errorf("invalid circleID: %w", err)
	}
	recipientUID, err := uuid.Parse(input.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("invalid recipientID: %w", err)
	}

	// Idempotency: return existing record if this txnHash was already processed.
	if input.TxnHash != "" {
		existing, err := s.repo.FindByTxnHash(ctx, input.TxnHash)
		if err == nil && existing != nil {
			log.Info().Str("txn_hash", input.TxnHash).Msg("payout already recorded (idempotent replay)")
			return existing, nil
		}
	}

	// Determine verification state — caller may override (e.g. indexer).
	verifiedOnchain := false
	verificationStatus := VerificationStatusUnverified

	if input.VerifiedOnchain != nil {
		verifiedOnchain = *input.VerifiedOnchain
	}
	if input.VerificationStatus != nil {
		verificationStatus = *input.VerificationStatus
	}

	// On-chain verification: only run when horizon is available and the caller
	// has not already provided an explicit verification state.
	if s.horizon != nil && input.TxnHash != "" &&
		input.VerifiedOnchain == nil && input.VerificationStatus == nil {

		recipientWallet, walletErr := s.resolveRecipientWallet(ctx, recipientUID)
		if walletErr != nil {
			// Cannot resolve wallet — record as pending for async retry.
			log.Warn().Err(walletErr).Str("recipient_id", input.RecipientID).
				Msg("could not resolve recipient wallet; recording payout as pending")
			verificationStatus = VerificationStatusPending
		} else {
			amountStr := fmt.Sprintf("%.7f", input.Amount)
			ok, verErr := s.horizon.VerifyPayment(ctx, input.TxnHash, recipientWallet, amountStr)
			if verErr != nil {
				log.Warn().Err(verErr).Str("txn_hash", input.TxnHash).
					Msg("horizon verification failed; recording payout as pending")
				verificationStatus = VerificationStatusPending
			} else if !ok {
				metrics.PayoutsTotal.WithLabelValues("rejected", "", "").Inc()
				return nil, fmt.Errorf("on-chain verification failed: txnHash %s does not match recipient/amount", input.TxnHash)
			} else {
				verifiedOnchain = true
				verificationStatus = VerificationStatusVerified
			}
		}
	}

	// Guard against duplicate payouts in the same circle/round/recipient
	// (belt-and-suspenders alongside the DB UNIQUE index on txn_hash).
	existingPayouts, _, _ := s.repo.ListByCircle(ctx, circleUID, 1, 100)
	for _, p := range existingPayouts {
		if p.RecipientID == recipientUID && p.RoundNumber == input.RoundNumber {
			log.Warn().Str("circle_id", input.CircleID).
				Int("round", input.RoundNumber).
				Msg("payout already exists for this circle/round/recipient")
			return &p, nil
		}
	}

	p := &Payout{
		ID:                 uuid.New(),
		CircleID:           circleUID,
		RecipientID:        recipientUID,
		RoundNumber:        input.RoundNumber,
		Amount:             input.Amount,
		FeeAmount:          input.FeeAmount,
		TxnHash:            sql.NullString{String: input.TxnHash, Valid: input.TxnHash != ""},
		PayoutType:         input.PayoutType,
		VerifiedOnchain:    verifiedOnchain,
		VerificationStatus: verificationStatus,
		CreatedAt:          time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, p); err != nil {
		if err == apperrors.ErrConflict {
			// Race: another request created the same row — return existing.
			if input.TxnHash != "" {
				if got, fErr := s.repo.FindByTxnHash(ctx, input.TxnHash); fErr == nil && got != nil {
					return got, nil
				}
			}
		}
		metrics.PayoutsTotal.WithLabelValues("failure", "", "").Inc()
		return nil, fmt.Errorf("saving payout: %w", err)
	}

	metrics.PayoutsTotal.WithLabelValues("success", "", "").Inc()
	metrics.PayoutVolumeTotal.WithLabelValues("").Add(input.Amount)
	return p, nil
}

func (s *service) resolveRecipientWallet(ctx context.Context, recipientUID uuid.UUID) (string, error) {
	if s.walletLookup == nil {
		return "", fmt.Errorf("no wallet lookup available")
	}
	return s.walletLookup.WalletAddressForUser(ctx, recipientUID)
}

func (s *service) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Payout, int, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid userID: %w", err)
	}
	return s.repo.ListByUser(ctx, uid, page, limit)
}

func (s *service) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Payout, int, error) {
	cid, err := uuid.Parse(circleID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid circleID: %w", err)
	}
	return s.repo.ListByCircle(ctx, cid, page, limit)
}

func (s *service) UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid payout ID: %w", err)
	}
	return s.repo.UpdateVerificationStatus(ctx, uid, verifiedOnchain, status)
}
