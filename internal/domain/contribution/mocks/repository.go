package mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/contribution"
)

type Repository struct {
	mock.Mock
}

func (m *Repository) FindByID(ctx context.Context, id uuid.UUID) (*contribution.Contribution, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*contribution.Contribution), args.Error(1)
}

func (m *Repository) FindByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*contribution.Contribution, error) {
	args := m.Called(ctx, circleID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*contribution.Contribution), args.Error(1)
}

func (m *Repository) FindByTxnHash(ctx context.Context, txnHash string) (*contribution.Contribution, error) {
	args := m.Called(ctx, txnHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*contribution.Contribution), args.Error(1)
}

func (m *Repository) Create(ctx context.Context, c *contribution.Contribution) error {
	return m.Called(ctx, c).Error(0)
}

func (m *Repository) UpdateStatus(ctx context.Context, id uuid.UUID, status contribution.ContributionStatus, txnHash string) error {
	return m.Called(ctx, id, status, txnHash).Error(0)
}

func (m *Repository) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status contribution.VerificationStatus) error {
	return m.Called(ctx, id, verifiedOnchain, status).Error(0)
}

func (m *Repository) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]contribution.Contribution, int, error) {
	args := m.Called(ctx, userID, page, limit)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]contribution.Contribution), args.Int(1), args.Error(2)
}

func (m *Repository) ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]contribution.Contribution, int, error) {
	args := m.Called(ctx, circleID, page, limit)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]contribution.Contribution), args.Int(1), args.Error(2)
}
