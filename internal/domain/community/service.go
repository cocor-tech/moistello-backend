package community

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	Create(ctx context.Context, userID string, input CreateCommunityInput) (*Community, error)
	Get(ctx context.Context, id string) (*Community, error)
	GetBySlug(ctx context.Context, slug string) (*Community, error)
	List(ctx context.Context, filter CommunityFilter) ([]Community, int, error)
	Update(ctx context.Context, id, userID string, input UpdateCommunityInput) (*Community, error)
	Delete(ctx context.Context, id, userID string) error

	Join(ctx context.Context, communityID, userID string) error
	Leave(ctx context.Context, communityID, userID string) error
	GetMembers(ctx context.Context, communityID string) ([]CommunityMember, error)
	IsMember(ctx context.Context, communityID, userID string) (bool, error)

	CreateAnnouncement(ctx context.Context, communityID, userID, content string) (*Announcement, error)
	GetAnnouncements(ctx context.Context, communityID string) ([]Announcement, error)
	DeleteAnnouncement(ctx context.Context, id, userID string) error
	LikeAnnouncement(ctx context.Context, id string) error

	GetActivity(ctx context.Context, communityID string, limit int) ([]ActivityEvent, error)
	GetMyCommunities(ctx context.Context, userID string) ([]Community, error)
	RecordActivity(ctx context.Context, communityID, eventType string, actorID string, targetID string, metadata map[string]interface{}) error
	UpdateTotalSaved(ctx context.Context, communityID string) error
}

type communityService struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &communityService{repo: repo}
}

func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}

func (s *communityService) Create(ctx context.Context, userID string, input CreateCommunityInput) (*Community, error) {
	ownerID, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	c := &Community{
		ID:          uuid.New(),
		Name:        input.Name,
		Slug:        input.Slug,
		Description: input.Description,
		Category:    input.Category,
		Tags:        input.Tags,
		OwnerID:     ownerID,
		MemberCount: 1,
		IsFeatured:  false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if c.Category == "" {
		c.Category = "community"
	}

	if err := s.repo.Create(ctx, c); err != nil {
		if err == apperrors.ErrConflict {
			return nil, fmt.Errorf("a community with this slug already exists")
		}
		return nil, fmt.Errorf("creating community: %w", err)
	}

	member := &CommunityMember{
		CommunityID: c.ID,
		UserID:      ownerID,
		Role:        "admin",
		JoinedAt:    now,
	}
	if err := s.repo.AddMember(ctx, member); err != nil {
		_ = s.repo.Delete(ctx, c.ID)
		return nil, fmt.Errorf("adding owner as member: %w", err)
	}

	return c, nil
}

func (s *communityService) Get(ctx context.Context, id string) (*Community, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	c, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("getting community: %w", err)
	}
	return c, nil
}

func (s *communityService) GetBySlug(ctx context.Context, slug string) (*Community, error) {
	return s.repo.FindBySlug(ctx, slug)
}

func (s *communityService) List(ctx context.Context, filter CommunityFilter) ([]Community, int, error) {
	communities, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("listing communities: %w", err)
	}
	if communities == nil {
		communities = []Community{}
	}
	return communities, total, nil
}

func (s *communityService) Update(ctx context.Context, id, userID string, input UpdateCommunityInput) (*Community, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}

	c, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("finding community: %w", err)
	}

	ownerID, _ := parseUUID(userID)
	if c.OwnerID != ownerID {
		return nil, apperrors.ErrForbidden
	}

	if input.Name != nil {
		c.Name = *input.Name
	}
	if input.Description != nil {
		c.Description = *input.Description
	}
	if input.Category != nil {
		c.Category = *input.Category
	}
	if input.Tags != nil {
		c.Tags = *input.Tags
	}
	c.AvatarURL = input.AvatarURL
	c.BannerURL = input.BannerURL

	if err := s.repo.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("updating community: %w", err)
	}
	return c, nil
}

func (s *communityService) Delete(ctx context.Context, id, userID string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		return fmt.Errorf("finding community: %w", err)
	}

	ownerID, _ := parseUUID(userID)
	if c.OwnerID != ownerID {
		return apperrors.ErrForbidden
	}

	return s.repo.Delete(ctx, uid)
}

func (s *communityService) Join(ctx context.Context, communityID, userID string) error {
	cid, err := parseUUID(communityID)
	if err != nil {
		return err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}

	member := &CommunityMember{
		CommunityID: cid,
		UserID:      uid,
		Role:        "member",
		JoinedAt:    time.Now().UTC(),
	}
	if err := s.repo.AddMember(ctx, member); err != nil {
		return err
	}
	return nil
}

func (s *communityService) Leave(ctx context.Context, communityID, userID string) error {
	cid, err := parseUUID(communityID)
	if err != nil {
		return err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return err
	}

	ownerUUID, _ := parseUUID(userID)
	if c.OwnerID == ownerUUID {
		return fmt.Errorf("owner cannot leave; transfer ownership or delete the community")
	}

	return s.repo.RemoveMember(ctx, cid, uid)
}

func (s *communityService) GetMembers(ctx context.Context, communityID string) ([]CommunityMember, error) {
	cid, err := parseUUID(communityID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetMembers(ctx, cid)
}

func (s *communityService) IsMember(ctx context.Context, communityID, userID string) (bool, error) {
	cid, err := parseUUID(communityID)
	if err != nil {
		return false, err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return false, err
	}
	return s.repo.IsMember(ctx, cid, uid)
}

func (s *communityService) CreateAnnouncement(ctx context.Context, communityID, userID, content string) (*Announcement, error) {
	cid, err := parseUUID(communityID)
	if err != nil {
		return nil, err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	a := &Announcement{
		ID:          uuid.New(),
		CommunityID: cid,
		AuthorID:    uid,
		Content:     content,
		IsPinned:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateAnnouncement(ctx, a); err != nil {
		return nil, fmt.Errorf("creating announcement: %w", err)
	}
	return a, nil
}

func (s *communityService) GetAnnouncements(ctx context.Context, communityID string) ([]Announcement, error) {
	cid, err := parseUUID(communityID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetAnnouncements(ctx, cid, false)
}

func (s *communityService) DeleteAnnouncement(ctx context.Context, id, userID string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return s.repo.DeleteAnnouncement(ctx, uid)
}

func (s *communityService) LikeAnnouncement(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return s.repo.LikeAnnouncement(ctx, uid)
}

func (s *communityService) GetActivity(ctx context.Context, communityID string, limit int) ([]ActivityEvent, error) {
	cid, err := parseUUID(communityID)
	if err != nil {
		return nil, err
	}
	events, err := s.repo.GetActivity(ctx, cid, limit)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []ActivityEvent{}
	}
	return events, nil
}

func (s *communityService) GetMyCommunities(ctx context.Context, userID string) ([]Community, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	communities, err := s.repo.FindByUserID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if communities == nil {
		communities = []Community{}
	}
	return communities, nil
}

func (s *communityService) RecordActivity(ctx context.Context, communityID, eventType string, actorID string, targetID string, metadata map[string]interface{}) error {
	cid, err := parseUUID(communityID)
	if err != nil {
		return err
	}
	var actorUUID, targetUUID *uuid.UUID
	if actorID != "" {
		id, _ := parseUUID(actorID)
		actorUUID = &id
	}
	if targetID != "" {
		id, _ := parseUUID(targetID)
		targetUUID = &id
	}
	e := &ActivityEvent{
		ID:          uuid.New(),
		CommunityID: cid,
		EventType:   eventType,
		ActorID:     actorUUID,
		TargetID:    targetUUID,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
	}
	return s.repo.RecordActivity(ctx, e)
}

func (s *communityService) UpdateTotalSaved(ctx context.Context, communityID string) error {
	cid, err := parseUUID(communityID)
	if err != nil {
		return err
	}
	return s.repo.UpdateTotalSaved(ctx, cid)
}
