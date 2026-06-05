package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestUserService_Create_NewUser(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	wallet := "GABC1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890"

	repo.On("FindByWalletAddress", ctx, wallet).Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.AnythingOfType("*user.User")).Return(nil)

	u, err := svc.Create(ctx, wallet)

	assert.NoError(t, err)
	assert.NotNil(t, u)
	assert.Equal(t, wallet, u.WalletAddress)
	assert.Equal(t, user.RoleUser, u.Role)
	assert.Equal(t, 0, u.MoiScore)
	repo.AssertExpectations(t)
}

func TestUserService_Create_ExistingUser(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	wallet := "GABC1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890"

	existingUser := &user.User{
		ID: uuid.New(), WalletAddress: wallet,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.On("FindByWalletAddress", ctx, wallet).Return(existingUser, nil)

	u, err := svc.Create(ctx, wallet)

	assert.NoError(t, err)
	assert.Equal(t, existingUser.ID, u.ID)
	repo.AssertExpectations(t)
}

func TestUserService_Create_DBError(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	wallet := "GABC1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890"

	repo.On("FindByWalletAddress", ctx, wallet).Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.AnythingOfType("*user.User")).Return(apperrors.ErrInternal)

	u, err := svc.Create(ctx, wallet)

	assert.Error(t, err)
	assert.Nil(t, u)
	repo.AssertExpectations(t)
}

func TestUserService_UpdateProfile(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	existing := &user.User{
		ID: uid, WalletAddress: "GABC...",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.On("FindByID", ctx, uid).Return(existing, nil)
	repo.On("Update", ctx, mock.AnythingOfType("*user.User")).Return(nil)

	displayName := "Alice"
	u, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{DisplayName: &displayName})

	assert.NoError(t, err)
	assert.Equal(t, "Alice", *u.DisplayName)
	repo.AssertExpectations(t)
}

func TestUserService_UpdateProfile_AllFields(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	existing := &user.User{
		ID: uid, WalletAddress: "GABC...",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.On("FindByID", ctx, uid).Return(existing, nil)
	repo.On("Update", ctx, mock.AnythingOfType("*user.User")).Return(nil)

	displayName := "Bob"
	email := "bob@example.com"
	phone := "+1234567890"
	countryCode := "US"
	lang := "es"

	u, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{
		DisplayName:       &displayName,
		Email:             &email,
		Phone:             &phone,
		CountryCode:       &countryCode,
		PreferredLanguage: &lang,
	})

	assert.NoError(t, err)
	assert.Equal(t, "Bob", *u.DisplayName)
	assert.Equal(t, "bob@example.com", *u.Email)
	assert.Equal(t, "+1234567890", *u.Phone)
	assert.Equal(t, "US", *u.CountryCode)
	assert.Equal(t, "es", u.PreferredLanguage)
	repo.AssertExpectations(t)
}

func TestUserService_UpdateProfile_NotFound(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound)

	displayName := "Alice"
	u, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{DisplayName: &displayName})

	assert.Error(t, err)
	assert.Nil(t, u)
	repo.AssertExpectations(t)
}

func TestUserService_GetByID_NotFound(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound)

	u, err := svc.GetByID(ctx, uid.String())

	assert.Error(t, err)
	assert.Nil(t, u)
	repo.AssertExpectations(t)
}

func TestUserService_GetByID_InvalidUUID(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()

	u, err := svc.GetByID(ctx, "not-a-uuid")

	assert.Error(t, err)
	assert.Nil(t, u)
}

func TestUserService_GetByID_Success(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	expected := &user.User{
		ID: uid, WalletAddress: "GABC...",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.On("FindByID", ctx, uid).Return(expected, nil)

	u, err := svc.GetByID(ctx, uid.String())

	assert.NoError(t, err)
	assert.Equal(t, expected.ID, u.ID)
	repo.AssertExpectations(t)
}

func TestUserService_GetMoiScore(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	expected := &user.User{
		ID: uid, WalletAddress: "GABC...", MoiScore: 450,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	repo.On("FindByID", ctx, uid).Return(expected, nil)

	resp, err := svc.GetMoiScore(ctx, uid.String())

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 450, resp.Score)
	assert.Equal(t, "Gold", resp.Level)
	repo.AssertExpectations(t)
}
