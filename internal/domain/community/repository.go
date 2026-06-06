package community

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	Create(ctx context.Context, c *Community) error
	FindByID(ctx context.Context, id uuid.UUID) (*Community, error)
	FindBySlug(ctx context.Context, slug string) (*Community, error)
	List(ctx context.Context, filter CommunityFilter) ([]Community, int, error)
	Update(ctx context.Context, c *Community) error
	Delete(ctx context.Context, id uuid.UUID) error

	AddMember(ctx context.Context, m *CommunityMember) error
	RemoveMember(ctx context.Context, communityID, userID uuid.UUID) error
	UpdateMemberRole(ctx context.Context, communityID, userID uuid.UUID, role string) error
	GetMembers(ctx context.Context, communityID uuid.UUID) ([]CommunityMember, error)
	IsMember(ctx context.Context, communityID, userID uuid.UUID) (bool, error)
	GetMemberCount(ctx context.Context, communityID uuid.UUID) (int, error)

	CreateAnnouncement(ctx context.Context, a *Announcement) error
	GetAnnouncements(ctx context.Context, communityID uuid.UUID, pinned bool) ([]Announcement, error)
	DeleteAnnouncement(ctx context.Context, id uuid.UUID) error
	LikeAnnouncement(ctx context.Context, id uuid.UUID) error
	SetAnnouncementPin(ctx context.Context, id uuid.UUID, pinned bool) error

	RecordActivity(ctx context.Context, e *ActivityEvent) error
	GetActivity(ctx context.Context, communityID uuid.UUID, limit int) ([]ActivityEvent, error)

	UpdateTotalSaved(ctx context.Context, communityID uuid.UUID) error
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]Community, error)
	UpdateOwner(ctx context.Context, communityID, newOwnerID uuid.UUID) error
}

type CommunityFilter struct {
	Search   string
	Category string
	Page     int
	Limit    int
}
