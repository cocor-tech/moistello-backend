package contribution

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/metrics"
	"github.com/rs/zerolog/log"
)

// RecordInput carries all fields needed to record a contribution.
type RecordInput struct {
	CircleID    string
	UserID      string
	RoundNumber int
	Amount      float64
	TxnHash     string
	// Optional overrides — used by the indexer / tests to set verification
	// state directly without going through the Horizon check.
	VerifiedOnchain    *bool
	VerificationStatus *VerificationStatus
}

// HorizonVerifier is satisfied by *stellar.Client.
type HorizonVerifier interface {
	// VerifyTransaction checks that txnHash exists, was successful, and has a
	// payment operation sent FROM expectedFrom for expectedAmount.
	VerifyTransaction(ctx context.Context, txnHash, expectedFrom, expectedAmount string) (bool, error)
}

// Broadcaster notifies WebSocket clients when a contribution is confirmed.
type Broadcaster interface {
	ContributionRecorded(ctx context.Context, circleID, userID string, roundNumber int, amount float64)
}

// Transactor wraps a database transaction for atomic contribution writes.
type Transactor interface {
	WithTransaction(ctx context.Context, fn func(repo Repository) error) error
}

// Service is the public API for the contribution domain.
type Service interface {
	// Record persists a new contribution, verifying the txnHash on-chain
	// before saving. Returns the existing record (idempotent) when the same
	// txnHash is submitted twice.
	Record(ctx context.Context, input RecordInput) (*Contribution, error)
	GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Contribution, int, error)
	GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Contribution, int, error)
	UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error
}

type service struct {
	repo            Repository
	broadcaster     Broadcaster
	tx              Transactor
	horizon         HorizonVerifier
	masterPublicKey string
}

// NewService constructs the contribution service.
//
//	broadcaster  – may be nil (no WS events)
//	tx           – may be nil (no DB transaction wrapping)
//	horizon      – may be nil (on-chain verification skipped; useful in tests)
//	masterPK     – Stellar master public key; if empty the sender check is skipped
func NewService(repo Repository, broadcaster Broadcaster, tx Transactor, horizon HorizonVerifier, masterPublicKey string) Service {
	return &service{
		repo:            repo,
		broadcaster:     broadcaster,
		tx:              tx,
		horizon:         horizon,
		masterPublicKey: masterPublicKey,
	}
}

// NewTransactor creates a DB-backed Transactor for the contribution domain.
func NewTransactor(db *sqlx.DB) Transactor {
	return &pgTransactor{db: db}
}

type pgTransactor struct{ db *sqlx.DB }

func (t *pgTransactor) WithTransaction(ctx context.Context, fn func(repo Repository) error) error {
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(NewRepositoryFromTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Record verifies the on-chain transaction and persists the contribution.
// The call is idempotent: submitting the same txnHash twice returns the
// already-recorded contribution rather than an error.
func (s *service) Record(ctx context.Context, input RecordInput) (*Contribution, error) {
	circleUID, err := uuid.Parse(input.CircleID)
	if err != nil {
		return nil, fmt.Errorf("invalid circleID: %w", err)
	}
	userUID, err := uuid.Parse(input.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid userID: %w", err)
	}

	// Idempotency: if this txnHash was already recorded, return existing row.
	if input.TxnHash != "" {
		existing, err := s.repo.FindByTxnHash(ctx, input.TxnHash)
		if err == nil && existing != nil {
			log.Info().Str("txn_hash", input.TxnHash).Msg("contribution already recorded (idempotent replay)")
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

	// On-chain verification: only run when horizon client is available and
	// the caller has NOT already supplied an explicit verification state.
	if s.horizon != nil && input.TxnHash != "" &&
		input.VerifiedOnchain == nil && input.VerificationStatus == nil {

		amountStr := fmt.Sprintf("%.7f", input.Amount)
		ok, verErr := s.horizon.VerifyTransaction(ctx, input.TxnHash, s.masterPublicKey, amountStr)
		if verErr != nil {
			// Horizon is unavailable — record as pending for async retry.
			log.Warn().Err(verErr).Str("txn_hash", input.TxnHash).
				Msg("horizon verification failed; recording contribution as pending")
			verificationStatus = VerificationStatusPending
		} else if !ok {
			// Transaction does not match — reject immediately.
			metrics.ContributionsTotal.WithLabelValues("rejected", "", "").Inc()
			return nil, fmt.Errorf("on-chain verification failed: txnHash %s does not match expected sender/amount", input.TxnHash)
		} else {
			verifiedOnchain = true
			verificationStatus = VerificationStatusVerified
		}
	}

	c := &Contribution{
		ID:                 uuid.New(),
		CircleID:           circleUID,
		UserID:             userUID,
		RoundNumber:        input.RoundNumber,
		Amount:             input.Amount,
		TxnHash:            sql.NullString{String: input.TxnHash, Valid: input.TxnHash != ""},
		Status:             StatusPending,
		OnTime:             true,
		VerifiedOnchain:    verifiedOnchain,
		VerificationStatus: verificationStatus,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}

	if verifiedOnchain {
		c.Status = StatusConfirmed
	}

	if err := s.repo.Create(ctx, c); err != nil {
		if err == apperrors.ErrConflict {
			// Race: another request created the same row first — return existing.
			existing, fErr := s.repo.FindByTxnHash(ctx, input.TxnHash)
			if fErr == nil && existing != nil {
				return existing, nil
			}
		}
		metrics.ContributionsTotal.WithLabelValues("failure", "", "").Inc()
		return nil, fmt.Errorf("saving contribution: %w", err)
	}

	metrics.ContributionsTotal.WithLabelValues("success", "", "").Inc()
	metrics.ContributionVolumeTotal.WithLabelValues("").Add(input.Amount)

	if s.broadcaster != nil {
		s.broadcaster.ContributionRecorded(ctx, input.CircleID, input.UserID, input.RoundNumber, input.Amount)
	}

	return c, nil
}

func (s *service) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]Contribution, int, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid userID: %w", err)
	}
	return s.repo.ListByUser(ctx, uid, page, limit)
}

func (s *service) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]Contribution, int, error) {
	cid, err := uuid.Parse(circleID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid circleID: %w", err)
	}
	return s.repo.ListByCircle(ctx, cid, page, limit)
}

func (s *service) UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status VerificationStatus) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid contribution ID: %w", err)
	}
	return s.repo.UpdateVerificationStatus(ctx, uid, verifiedOnchain, status)
}
