package mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/notification"
)

type Repository struct {
	mock.Mock
}

func (m *Repository) Create(ctx context.Context, n *notification.Notification) error {
	return m.Called(ctx, n).Error(0)
}

func (m *Repository) List(ctx context.Context, userID uuid.UUID, page, limit int, unreadOnly bool) ([]notification.Notification, int, error) {
	args := m.Called(ctx, userID, page, limit, unreadOnly)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]notification.Notification), args.Int(1), args.Error(2)
}

func (m *Repository) MarkRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	return m.Called(ctx, id, userID).Error(0)
}

func (m *Repository) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	return m.Called(ctx, userID).Error(0)
}

func (m *Repository) BulkArchive(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, archived bool) ([]uuid.UUID, error) {
	args := m.Called(ctx, userID, ids, archived)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]uuid.UUID), args.Error(1)
}
