package community

import (
	"time"

	"github.com/google/uuid"
)

type Community struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Slug        string    `json:"slug" db:"slug"`
	Description string    `json:"description" db:"description"`
	Category    string    `json:"category" db:"category"`
	Tags        []string  `json:"tags" db:"tags"`
	AvatarURL   *string   `json:"avatarUrl,omitempty" db:"avatar_url"`
	BannerURL   *string   `json:"bannerUrl,omitempty" db:"banner_url"`
	OwnerID     uuid.UUID `json:"ownerId" db:"owner_id"`
	MemberCount int       `json:"memberCount" db:"member_count"`
	TotalSaved  float64   `json:"totalSaved" db:"total_saved"`
	IsFeatured  bool      `json:"isFeatured" db:"is_featured"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type CommunityMember struct {
	CommunityID uuid.UUID `json:"communityId" db:"community_id"`
	UserID      uuid.UUID `json:"userId" db:"user_id"`
	Role        string    `json:"role" db:"role"`
	JoinedAt    time.Time `json:"joinedAt" db:"joined_at"`
}

type Announcement struct {
	ID          uuid.UUID `json:"id" db:"id"`
	CommunityID uuid.UUID `json:"communityId" db:"community_id"`
	AuthorID    uuid.UUID `json:"authorId" db:"author_id"`
	Content     string    `json:"content" db:"content"`
	IsPinned    bool      `json:"isPinned" db:"is_pinned"`
	LikeCount   int       `json:"likeCount" db:"like_count"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type ActivityEvent struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	CommunityID uuid.UUID              `json:"communityId" db:"community_id"`
	EventType   string                 `json:"eventType" db:"event_type"`
	ActorID     *uuid.UUID             `json:"actorId,omitempty" db:"actor_id"`
	TargetID    *uuid.UUID             `json:"targetId,omitempty" db:"target_id"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
	CreatedAt   time.Time              `json:"createdAt" db:"created_at"`
}

type CreateCommunityInput struct {
	Name        string   `json:"name" validate:"required,min=2,max=100"`
	Slug        string   `json:"slug" validate:"required,min=2,max=120,slug"`
	Description string   `json:"description" validate:"max=2000"`
	Category    string   `json:"category" validate:"oneof=finance tech community social_impact education health entertainment other"`
	Tags        []string `json:"tags" validate:"max=10,dive,max=30"`
}

type UpdateCommunityInput struct {
	Name        *string   `json:"name" validate:"omitempty,min=2,max=100"`
	Description *string   `json:"description" validate:"omitempty,max=2000"`
	Category    *string   `json:"category" validate:"omitempty,oneof=finance tech community social_impact education health entertainment other"`
	Tags        *[]string `json:"tags" validate:"omitempty,max=10,dive,max=30"`
	AvatarURL   *string   `json:"avatarUrl"`
	BannerURL   *string   `json:"bannerUrl"`
}
