package notification

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	Create(ctx context.Context, n *Notification) error
	List(ctx context.Context, userID uuid.UUID, page, limit int, unreadOnly bool) ([]Notification, int, error)
	MarkRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	MarkAllRead(ctx context.Context, userID uuid.UUID) error
	BulkArchive(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, archived bool) ([]uuid.UUID, error)
}
