package community_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/community"
	communityMocks "github.com/moistello/backend/internal/domain/community/mocks"
)

var errRepo = errors.New("repo failure")

func TestCommunityService_GetBySlug(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	c := newCommunity(uuid.New())

	repo.On("FindBySlug", mock.Anything, "test").Return(c, nil).Once()
	got, err := svc.GetBySlug(ctx(), "test")
	assert.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)

	repo.On("FindBySlug", mock.Anything, "missing").Return((*community.Community)(nil), errRepo).Once()
	got, err = svc.GetBySlug(ctx(), "missing")
	assert.ErrorIs(t, err, errRepo)
	assert.Nil(t, got)
	repo.AssertExpectations(t)
}

func TestCommunityService_List(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	filter := community.CommunityFilter{}

	repo.On("List", mock.Anything, filter).Return([]community.Community{*newCommunity(uuid.New())}, 1, nil).Once()
	list, total, err := svc.List(ctx(), filter)
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, 1, total)

	repo.On("List", mock.Anything, filter).Return([]community.Community(nil), 0, errRepo).Once()
	list, total, err = svc.List(ctx(), filter)
	assert.ErrorIs(t, err, errRepo)
	assert.ErrorContains(t, err, "listing communities")
	assert.Nil(t, list)
	assert.Equal(t, 0, total)
	repo.AssertExpectations(t)
}

func TestCommunityService_GetMembers_Errors(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	cid := uuid.New()

	_, err := svc.GetMembers(ctx(), "not-a-uuid")
	assert.Error(t, err)

	repo.On("GetMembers", mock.Anything, cid).Return([]community.CommunityMember(nil), errRepo).Once()
	_, err = svc.GetMembers(ctx(), cid.String())
	assert.ErrorIs(t, err, errRepo)
	repo.AssertExpectations(t)
}

func TestCommunityService_GetAnnouncements_Errors(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	cid := uuid.New()

	_, err := svc.GetAnnouncements(ctx(), "not-a-uuid")
	assert.Error(t, err)

	repo.On("GetAnnouncements", mock.Anything, cid, false).Return([]community.Announcement(nil), errRepo).Once()
	_, err = svc.GetAnnouncements(ctx(), cid.String())
	assert.ErrorIs(t, err, errRepo)
	repo.AssertExpectations(t)
}

func TestCommunityService_GetActivity(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	cid := uuid.New()

	_, err := svc.GetActivity(ctx(), "not-a-uuid", 10)
	assert.Error(t, err)

	repo.On("GetActivity", mock.Anything, cid, 10).Return([]community.ActivityEvent(nil), errRepo).Once()
	_, err = svc.GetActivity(ctx(), cid.String(), 10)
	assert.ErrorIs(t, err, errRepo)

	// A nil result from the repo is normalised to an empty slice.
	repo.On("GetActivity", mock.Anything, cid, 5).Return([]community.ActivityEvent(nil), nil).Once()
	events, err := svc.GetActivity(ctx(), cid.String(), 5)
	assert.NoError(t, err)
	assert.NotNil(t, events)
	assert.Empty(t, events)
	repo.AssertExpectations(t)
}

func TestCommunityService_GetMyCommunities_RepoError(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	uid := uuid.New()

	_, err := svc.GetMyCommunities(ctx(), "not-a-uuid")
	assert.Error(t, err)

	repo.On("FindByUserID", mock.Anything, uid).Return([]community.Community(nil), errRepo).Once()
	_, err = svc.GetMyCommunities(ctx(), uid.String())
	assert.ErrorIs(t, err, errRepo)
	repo.AssertExpectations(t)
}

func TestCommunityService_RecordActivity(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	cid := uuid.New()

	err := svc.RecordActivity(ctx(), "not-a-uuid", "joined", "", "", nil)
	assert.Error(t, err)

	repo.On("RecordActivity", mock.Anything, mock.AnythingOfType("*community.ActivityEvent")).
		Return(errRepo).Once()
	err = svc.RecordActivity(ctx(), cid.String(), "joined", "", "", nil)
	assert.ErrorIs(t, err, errRepo)

	repo.On("RecordActivity", mock.Anything, mock.MatchedBy(func(e *community.ActivityEvent) bool {
		return e.CommunityID == cid && e.ActorID != nil && e.TargetID == nil
	})).Return(nil).Once()
	err = svc.RecordActivity(ctx(), cid.String(), "joined", uuid.NewString(), "", nil)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCommunityService_UpdateTotalSaved(t *testing.T) {
	repo := new(communityMocks.Repository)
	svc := community.NewService(repo, nil)
	cid := uuid.New()

	err := svc.UpdateTotalSaved(ctx(), "not-a-uuid")
	assert.Error(t, err)

	repo.On("UpdateTotalSaved", mock.Anything, cid).Return(errRepo).Once()
	assert.ErrorIs(t, svc.UpdateTotalSaved(ctx(), cid.String()), errRepo)

	repo.On("UpdateTotalSaved", mock.Anything, cid).Return(nil).Once()
	assert.NoError(t, svc.UpdateTotalSaved(ctx(), cid.String()))
	repo.AssertExpectations(t)
}
