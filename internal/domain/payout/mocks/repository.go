package mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/payout"
)

type Repository struct {
	mock.Mock
}

func (m *Repository) FindByID(ctx context.Context, id uuid.UUID) (*payout.Payout, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*payout.Payout), args.Error(1)
}

func (m *Repository) FindByTxnHash(ctx context.Context, txnHash string) (*payout.Payout, error) {
	args := m.Called(ctx, txnHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*payout.Payout), args.Error(1)
}

func (m *Repository) Create(ctx context.Context, p *payout.Payout) error {
	return m.Called(ctx, p).Error(0)
}

func (m *Repository) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status payout.VerificationStatus) error {
	return m.Called(ctx, id, verifiedOnchain, status).Error(0)
}

func (m *Repository) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, userID, page, limit)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]payout.Payout), args.Int(1), args.Error(2)
}

func (m *Repository) ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, circleID, page, limit)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]payout.Payout), args.Int(1), args.Error(2)
}
