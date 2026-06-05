package circle

import (
	"context"

	"github.com/google/uuid"
)

type CircleFilter struct {
	Search     string
	Status     CircleStatus
	Type       CircleType
	Page       int
	Limit      int
	ExcludeIDs []uuid.UUID
}

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*Circle, error)
	FindByContractID(ctx context.Context, contractID string) (*Circle, error)
	List(ctx context.Context, filter CircleFilter) ([]Circle, error)
	Count(ctx context.Context, filter CircleFilter) (int, error)
	Create(ctx context.Context, c *Circle) error
	Update(ctx context.Context, c *Circle) error
	Delete(ctx context.Context, id uuid.UUID) error
	CreateMember(ctx context.Context, m *CircleMember) error
	GetMembers(ctx context.Context, circleID uuid.UUID) ([]CircleMember, error)
	GetMemberCount(ctx context.Context, circleID uuid.UUID) (int, error)
	UpdateMemberStatus(ctx context.Context, circleID, userID uuid.UUID, status MemberStatus) error
	FindMemberByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*CircleMember, error)
	FindCirclesByUserID(ctx context.Context, userID uuid.UUID) ([]Circle, error)
}
