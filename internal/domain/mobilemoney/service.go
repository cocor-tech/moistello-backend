package mobilemoney

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service orchestrates the mobile-money bridge: picking the right provider
// per currency, enforcing idempotency on initiation, and reconciling
// pending transactions whose provider callback never arrived (or hasn't
// arrived yet).
type Service interface {
	InitiateOnramp(ctx context.Context, userID string, req OnrampRequest, idempotencyKey string) (*Transaction, error)
	InitiateOfframp(ctx context.Context, userID string, req OfframpRequest, idempotencyKey string) (*Transaction, error)
	GetTransaction(ctx context.Context, id string) (*Transaction, error)
	// Reconcile polls GetStatus for every pending transaction older than
	// reconcileMinAge and updates its stored status accordingly. It
	// returns how many transactions were updated. Meant to be called on a
	// schedule (see cmd/api-server main.go) as a safety net for missed or
	// delayed provider callbacks/webhooks.
	Reconcile(ctx context.Context) (int, error)
	ListProviders(ctx context.Context) []ProviderInfo
	GetSupportedCurrencies(ctx context.Context) []string
}

// reconcileMinAge is how long a pending transaction must have existed
// before the reconciler polls it, so a transaction isn't queried before
// the provider has had any realistic chance to settle it.
const reconcileMinAge = 30 * time.Second

type service struct {
	repo     Repository
	registry *Registry
}

func NewService(repo Repository, registry *Registry) Service {
	return &service{repo: repo, registry: registry}
}

func (s *service) ListProviders(ctx context.Context) []ProviderInfo {
	if s.registry == nil {
		return []ProviderInfo{}
	}
	return s.registry.Providers()
}

func (s *service) GetSupportedCurrencies(ctx context.Context) []string {
	if s.registry == nil {
		return []string{}
	}
	return s.registry.SupportedCurrencies()
}

func (s *service) InitiateOnramp(ctx context.Context, userID string, req OnrampRequest, idempotencyKey string) (*Transaction, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency key is required")
	}

	existing, err := s.repo.GetByIdempotencyKey(ctx, userID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	provider, err := s.registry.For(req.Currency)
	if err != nil {
		return nil, err
	}

	result, err := provider.InitiateOnramp(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("initiating on-ramp with %s: %w", provider.Name(), err)
	}

	txn := &Transaction{
		ID:                 uuid.New().String(),
		UserID:             userID,
		Provider:           provider.Name(),
		Direction:          DirectionOnramp,
		Currency:           req.Currency,
		Amount:             req.Amount,
		PhoneNumber:        req.PhoneNumber,
		DestinationAddress: req.DestinationAddress,
		Status:             result.Status,
		ProviderRef:        result.ProviderRef,
		IdempotencyKey:     idempotencyKey,
		CreatedAt:          time.Now().UTC(),
	}
	// The provider has already accepted the request at this point (e.g. an
	// STK push is on its way to the customer's phone), so a persistence
	// failure here must surface loudly for manual reconciliation rather
	// than being silently dropped — the transaction still exists on the
	// provider's side even if we failed to record it.
	if err := s.repo.Create(ctx, txn); err != nil {
		return nil, fmt.Errorf("persisting mobile money on-ramp after %s accepted it (providerRef=%s): %w", provider.Name(), result.ProviderRef, err)
	}
	return txn, nil
}

func (s *service) InitiateOfframp(ctx context.Context, userID string, req OfframpRequest, idempotencyKey string) (*Transaction, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency key is required")
	}

	existing, err := s.repo.GetByIdempotencyKey(ctx, userID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	provider, err := s.registry.For(req.Currency)
	if err != nil {
		return nil, err
	}

	result, err := provider.InitiateOfframp(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("initiating off-ramp with %s: %w", provider.Name(), err)
	}

	txn := &Transaction{
		ID:             uuid.New().String(),
		UserID:         userID,
		Provider:       provider.Name(),
		Direction:      DirectionOfframp,
		Currency:       req.Currency,
		Amount:         req.Amount,
		PhoneNumber:    req.PhoneNumber,
		Status:         result.Status,
		ProviderRef:    result.ProviderRef,
		IdempotencyKey: idempotencyKey,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, txn); err != nil {
		return nil, fmt.Errorf("persisting mobile money off-ramp after %s accepted it (providerRef=%s): %w", provider.Name(), result.ProviderRef, err)
	}
	return txn, nil
}

func (s *service) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) Reconcile(ctx context.Context) (int, error) {
	pending, err := s.repo.ListPending(ctx, int(reconcileMinAge.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("listing pending mobile money transactions: %w", err)
	}

	reconciled := 0
	for _, txn := range pending {
		provider, err := s.registry.For(txn.Currency)
		if err != nil {
			// No provider registered for this currency (e.g. config
			// changed) — nothing to poll against; leave it pending for a
			// human to investigate rather than guessing at a status.
			continue
		}

		result, err := provider.GetStatus(ctx, txn.ProviderRef)
		if err != nil || result.Status == StatusPending {
			// Transient provider error or genuinely still pending — retry
			// on the next reconciliation pass.
			continue
		}

		var reason *string
		if result.FailureReason != "" {
			reason = &result.FailureReason
		}
		if err := s.repo.UpdateStatus(ctx, txn.ID, result.Status, reason); err != nil {
			continue
		}
		reconciled++
	}
	return reconciled, nil
}
