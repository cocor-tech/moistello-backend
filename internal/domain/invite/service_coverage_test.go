package invite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/invite"
	inviteMocks "github.com/moistello/backend/internal/domain/invite/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

var errInviteDB = errors.New("db down")

func openInvite(code string) *invite.Invite {
	return &invite.Invite{ID: uuid.New(), CircleID: uuid.New(), Code: code, MaxUses: 0}
}

func TestService_Accept_Success_IncrementsUseCount(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	inv := openInvite("abc")
	repo.On("FindByCode", ctx, "abc").Return(inv, nil)
	repo.On("IncrementUse", ctx, "abc").Return(nil)
	svc := invite.NewService(repo)

	got, err := svc.Accept(ctx, "abc", uuid.New().String())
	require.NoError(t, err)
	assert.Equal(t, 1, got.UseCount)
	repo.AssertExpectations(t)
}

func TestService_Accept_UnknownCodeIsInvalidInvite(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCode", ctx, "nope").Return(nil, apperrors.ErrNotFound)
	svc := invite.NewService(repo)

	_, err := svc.Accept(ctx, "nope", uuid.New().String())
	assert.ErrorIs(t, err, apperrors.ErrInvalidInvite)
	repo.AssertNotCalled(t, "IncrementUse", mock.Anything, mock.Anything)
}

func TestService_Accept_ValidationErrorIsPropagated(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCode", ctx, "abc").Return(nil, errInviteDB)
	svc := invite.NewService(repo)

	_, err := svc.Accept(ctx, "abc", uuid.New().String())
	assert.ErrorIs(t, err, errInviteDB)
	repo.AssertNotCalled(t, "IncrementUse", mock.Anything, mock.Anything)
}

func TestService_Accept_InvalidUserIDRejectedBeforeIncrement(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCode", ctx, "abc").Return(openInvite("abc"), nil)
	svc := invite.NewService(repo)

	_, err := svc.Accept(ctx, "abc", "not-a-uuid")
	assert.ErrorContains(t, err, "invalid UUID")
	repo.AssertNotCalled(t, "IncrementUse", mock.Anything, mock.Anything)
}

func TestService_Accept_IncrementFailureIsWrapped(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCode", ctx, "abc").Return(openInvite("abc"), nil)
	repo.On("IncrementUse", ctx, "abc").Return(errInviteDB)
	svc := invite.NewService(repo)

	_, err := svc.Accept(ctx, "abc", uuid.New().String())
	assert.ErrorIs(t, err, errInviteDB)
	assert.ErrorContains(t, err, "accepting invite")
}

func TestService_Validate_RepoErrorIsWrapped(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCode", ctx, "abc").Return(nil, errInviteDB)
	svc := invite.NewService(repo)

	_, err := svc.Validate(ctx, "abc")
	assert.ErrorIs(t, err, errInviteDB)
	assert.NotErrorIs(t, err, apperrors.ErrInvalidInvite)
}

func TestService_List_RepoErrorIsWrapped(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("FindByCircle", ctx, mock.AnythingOfType("uuid.UUID")).Return(nil, errInviteDB)
	svc := invite.NewService(repo)

	_, err := svc.List(ctx, uuid.New().String())
	assert.ErrorIs(t, err, errInviteDB)
	assert.ErrorContains(t, err, "listing invites")
}

func TestService_Revoke_RepoErrorIsWrapped(t *testing.T) {
	ctx := context.Background()
	repo := new(inviteMocks.Repository)
	repo.On("Delete", ctx, mock.AnythingOfType("uuid.UUID")).Return(errInviteDB)
	svc := invite.NewService(repo)

	err := svc.Revoke(ctx, uuid.New().String(), uuid.New().String())
	assert.ErrorIs(t, err, errInviteDB)
	assert.NotErrorIs(t, err, apperrors.ErrInvalidInvite)
}

func TestService_Revoke_InvalidIDRejected(t *testing.T) {
	svc := invite.NewService(new(inviteMocks.Repository))
	assert.ErrorContains(t, svc.Revoke(context.Background(), "bad", uuid.New().String()), "invalid UUID")
}
