package user_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/circle"
	circleMocks "github.com/moistello/backend/internal/domain/circle/mocks"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestNotificationDigestCadenceBounds(t *testing.T) {
	min, max := user.NotificationDigestCadenceBounds()
	assert.Equal(t, 15, min)
	assert.Equal(t, 7*24*60, max)
}

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

func TestUserService_Create_CheckExistingError(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	wallet := "GABC..."

	repo.On("FindByWalletAddress", ctx, wallet).Return(nil, errors.New("db down"))

	u, err := svc.Create(ctx, wallet)
	assert.Error(t, err)
	assert.Nil(t, u)
	repo.AssertExpectations(t)
}

func TestUserService_Create_ConflictResolution(t *testing.T) {
	ctx := context.Background()
	wallet := "GABC..."

	t.Run("conflict resolved", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		existingUser := &user.User{ID: uuid.New(), WalletAddress: wallet}

		repo.On("FindByWalletAddress", ctx, wallet).Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.AnythingOfType("*user.User")).Return(apperrors.ErrConflict).Once()
		repo.On("FindByWalletAddress", ctx, wallet).Return(existingUser, nil).Once()

		u, err := svc.Create(ctx, wallet)
		assert.NoError(t, err)
		assert.Equal(t, existingUser.ID, u.ID)
		repo.AssertExpectations(t)
	})

	t.Run("conflict resolution lookup error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)

		repo.On("FindByWalletAddress", ctx, wallet).Return(nil, apperrors.ErrNotFound).Once()
		repo.On("Create", ctx, mock.AnythingOfType("*user.User")).Return(apperrors.ErrConflict).Once()
		repo.On("FindByWalletAddress", ctx, wallet).Return(nil, errors.New("network dropped")).Once()

		u, err := svc.Create(ctx, wallet)
		assert.Error(t, err)
		assert.Nil(t, u)
		repo.AssertExpectations(t)
	})
}

func TestUserService_GetByWallet(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	wallet := "GABC..."

	t.Run("success", func(t *testing.T) {
		expected := &user.User{ID: uuid.New(), WalletAddress: wallet}
		repo.On("FindByWalletAddress", ctx, wallet).Return(expected, nil).Once()

		u, err := svc.GetByWallet(ctx, wallet)
		assert.NoError(t, err)
		assert.Equal(t, expected, u)
	})

	t.Run("not found", func(t *testing.T) {
		repo.On("FindByWalletAddress", ctx, wallet).Return(nil, apperrors.ErrNotFound).Once()

		u, err := svc.GetByWallet(ctx, wallet)
		assert.ErrorIs(t, err, user.ErrUserNotFound)
		assert.Nil(t, u)
	})

	t.Run("error", func(t *testing.T) {
		repo.On("FindByWalletAddress", ctx, wallet).Return(nil, errors.New("db error")).Once()

		u, err := svc.GetByWallet(ctx, wallet)
		assert.Error(t, err)
		assert.Nil(t, u)
	})
	repo.AssertExpectations(t)
}

func TestUserService_GetByEmail(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	email := "test@example.com"

	t.Run("success", func(t *testing.T) {
		expected := &user.User{ID: uuid.New(), Email: &email}
		repo.On("FindByEmail", ctx, email).Return(expected, nil).Once()

		u, err := svc.GetByEmail(ctx, email)
		assert.NoError(t, err)
		assert.Equal(t, expected, u)
	})

	t.Run("not found", func(t *testing.T) {
		repo.On("FindByEmail", ctx, email).Return(nil, apperrors.ErrNotFound).Once()

		u, err := svc.GetByEmail(ctx, email)
		assert.ErrorIs(t, err, user.ErrUserNotFound)
		assert.Nil(t, u)
	})

	t.Run("error", func(t *testing.T) {
		repo.On("FindByEmail", ctx, email).Return(nil, errors.New("db error")).Once()

		u, err := svc.GetByEmail(ctx, email)
		assert.Error(t, err)
		assert.Nil(t, u)
	})
	repo.AssertExpectations(t)
}

func TestUserService_Delete(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	t.Run("invalid uuid", func(t *testing.T) {
		err := svc.Delete(ctx, "invalid")
		assert.ErrorIs(t, err, user.ErrInvalidUUID)
	})

	t.Run("success", func(t *testing.T) {
		repo.On("Delete", ctx, uid).Return(nil).Once()

		err := svc.Delete(ctx, uid.String())
		assert.NoError(t, err)
	})

	t.Run("repo error", func(t *testing.T) {
		repo.On("Delete", ctx, uid).Return(errors.New("db error")).Once()

		err := svc.Delete(ctx, uid.String())
		assert.Error(t, err)
	})
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
	hash := sha256.Sum256([]byte("bob@example.com"))
	assert.Equal(t, hex.EncodeToString(hash[:]), *u.Email)
	assert.Equal(t, "+1234567890", *u.Phone)
	assert.Equal(t, "US", *u.CountryCode)
	assert.Equal(t, "es", u.PreferredLanguage)
	repo.AssertExpectations(t)
}

func TestUserService_UpdateProfile_SessionTTLBounds(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	existing := &user.User{ID: uid, WalletAddress: "GABC..."}
	repo.On("FindByID", ctx, uid).Return(existing, nil)
	repo.On("Update", ctx, mock.AnythingOfType("*user.User")).Return(nil)

	lowTTL := 30
	u, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{SessionTTL: &lowTTL})
	assert.NoError(t, err)
	assert.Equal(t, 60, u.SessionTTLMinutes)

	highTTL := 2000
	u, err = svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{SessionTTL: &highTTL})
	assert.NoError(t, err)
	assert.Equal(t, 1440, u.SessionTTLMinutes)
	repo.AssertExpectations(t)
}

func TestUserService_UpdateProfile_Errors(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	t.Run("invalid uuid", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, "invalid", user.UpdateProfileInput{})
		assert.ErrorIs(t, err, user.ErrInvalidUUID)
	})

	t.Run("not found", func(t *testing.T) {
		repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound).Once()
		_, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{})
		assert.ErrorIs(t, err, user.ErrUserNotFound)
	})

	t.Run("find error", func(t *testing.T) {
		repo.On("FindByID", ctx, uid).Return(nil, errors.New("db err")).Once()
		_, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{})
		assert.Error(t, err)
	})

	t.Run("update error", func(t *testing.T) {
		repo.On("FindByID", ctx, uid).Return(&user.User{ID: uid}, nil).Once()
		repo.On("Update", ctx, mock.AnythingOfType("*user.User")).Return(errors.New("update err")).Once()
		_, err := svc.UpdateProfile(ctx, uid.String(), user.UpdateProfileInput{})
		assert.Error(t, err)
	})
	repo.AssertExpectations(t)
}

func TestUserService_UpdateNotificationPreferences(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()

	t.Run("invalid uuid", func(t *testing.T) {
		svc := user.NewService(nil, nil)
		_, err := svc.UpdateNotificationPreferences(ctx, "invalid", user.NotificationPrefsInput{Channels: []string{"email"}})
		assert.ErrorIs(t, err, user.ErrInvalidUUID)
	})

	t.Run("invalid channel", func(t *testing.T) {
		svc := user.NewService(nil, nil)
		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{Channels: []string{"carrier_pigeon"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid notification channel")
	})

	t.Run("user not found", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound).Once()
		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{Channels: []string{"email"}})
		assert.ErrorIs(t, err, user.ErrUserNotFound)
		repo.AssertExpectations(t)
	})

	t.Run("find error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, errors.New("db error")).Once()
		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{Channels: []string{"email"}})
		assert.Error(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("invalid digest interval too low", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(&user.User{ID: uid}, nil).Once()
		low := 5
		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{
			Channels:              []string{"email"},
			DigestIntervalMinutes: &low,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid digest interval")
		repo.AssertExpectations(t)
	})

	t.Run("invalid digest interval too high", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(&user.User{ID: uid}, nil).Once()
		high := 200000
		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{
			Channels:              []string{"email"},
			DigestIntervalMinutes: &high,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid digest interval")
		repo.AssertExpectations(t)
	})

	t.Run("success with default interval when digest enabled", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		existing := &user.User{ID: uid}
		enabled := true
		repo.On("FindByID", ctx, uid).Return(existing, nil).Once()
		repo.On("Update", ctx, mock.MatchedBy(func(u *user.User) bool {
			return u.DigestEnabled && u.DigestIntervalMinutes == user.DefaultDigestIntervalMinutes && u.NotificationsMuted
		})).Return(nil).Once()

		res, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{
			Channels:      []string{"email", "inapp"},
			Muted:         true,
			DigestEnabled: &enabled,
		})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		repo.AssertExpectations(t)
	})

	t.Run("success with custom interval and repo error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		existing := &user.User{ID: uid}
		customInterval := 120
		repo.On("FindByID", ctx, uid).Return(existing, nil).Once()
		repo.On("Update", ctx, mock.AnythingOfType("*user.User")).Return(errors.New("save err")).Once()

		_, err := svc.UpdateNotificationPreferences(ctx, uid.String(), user.NotificationPrefsInput{
			Channels:              []string{"sms", "push"},
			DigestIntervalMinutes: &customInterval,
		})
		assert.Error(t, err)
		repo.AssertExpectations(t)
	})
}

func TestUserService_GetByID(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	t.Run("not found", func(t *testing.T) {
		repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound).Once()
		u, err := svc.GetByID(ctx, uid.String())
		assert.Error(t, err)
		assert.Nil(t, u)
	})

	t.Run("invalid uuid", func(t *testing.T) {
		u, err := svc.GetByID(ctx, "not-a-uuid")
		assert.Error(t, err)
		assert.Nil(t, u)
	})

	t.Run("db error", func(t *testing.T) {
		repo.On("FindByID", ctx, uid).Return(nil, errors.New("db error")).Once()
		u, err := svc.GetByID(ctx, uid.String())
		assert.Error(t, err)
		assert.Nil(t, u)
	})

	t.Run("success", func(t *testing.T) {
		expected := &user.User{
			ID: uid, WalletAddress: "GABC...",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		repo.On("FindByID", ctx, uid).Return(expected, nil).Once()
		u, err := svc.GetByID(ctx, uid.String())
		assert.NoError(t, err)
		assert.Equal(t, expected.ID, u.ID)
	})
	repo.AssertExpectations(t)
}

func TestUserService_GetMoiScore(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()

	cases := []struct {
		name          string
		score         int
		expectedLevel string
	}{
		{"Diamond", 850, "Diamond"},
		{"Platinum", 650, "Platinum"},
		{"Gold", 450, "Gold"},
		{"Silver", 250, "Silver"},
		{"Bronze", 50, "Bronze"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(userMocks.Repository)
			svc := user.NewService(repo, nil)
			expected := &user.User{ID: uid, MoiScore: tc.score}
			repo.On("FindByID", ctx, uid).Return(expected, nil)

			resp, err := svc.GetMoiScore(ctx, uid.String())
			assert.NoError(t, err)
			assert.Equal(t, tc.score, resp.Score)
			assert.Equal(t, tc.expectedLevel, resp.Level)
			repo.AssertExpectations(t)
		})
	}

	t.Run("invalid uuid", func(t *testing.T) {
		svc := user.NewService(nil, nil)
		_, err := svc.GetMoiScore(ctx, "invalid")
		assert.ErrorIs(t, err, user.ErrInvalidUUID)
	})

	t.Run("not found", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound)
		_, err := svc.GetMoiScore(ctx, uid.String())
		assert.ErrorIs(t, err, user.ErrUserNotFound)
		repo.AssertExpectations(t)
	})

	t.Run("repo error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, errors.New("db error"))
		_, err := svc.GetMoiScore(ctx, uid.String())
		assert.Error(t, err)
		repo.AssertExpectations(t)
	})
}

func TestUserService_ClaimName(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo.On("ClaimNextName", ctx).Return(int64(10), nil).Once()

		name, err := svc.ClaimName(ctx)
		assert.NoError(t, err)
		assert.NotEmpty(t, name)
	})

	t.Run("error", func(t *testing.T) {
		repo.On("ClaimNextName", ctx).Return(int64(0), errors.New("db lock")).Once()

		name, err := svc.ClaimName(ctx)
		assert.Error(t, err)
		assert.Empty(t, name)
	})
	repo.AssertExpectations(t)
}

func TestUserService_GetCircles(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()

	t.Run("invalid uuid", func(t *testing.T) {
		svc := user.NewService(nil, nil)
		_, err := svc.GetCircles(ctx, "invalid")
		assert.ErrorIs(t, err, user.ErrInvalidUUID)
	})

	t.Run("user not found", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, apperrors.ErrNotFound).Once()

		_, err := svc.GetCircles(ctx, uid.String())
		assert.ErrorIs(t, err, user.ErrUserNotFound)
		repo.AssertExpectations(t)
	})

	t.Run("find user db error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		svc := user.NewService(repo, nil)
		repo.On("FindByID", ctx, uid).Return(nil, errors.New("db error")).Once()

		_, err := svc.GetCircles(ctx, uid.String())
		assert.Error(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("circle repo error", func(t *testing.T) {
		repo := new(userMocks.Repository)
		circleRepo := new(circleMocks.Repository)
		svc := user.NewService(repo, circleRepo)
		repo.On("FindByID", ctx, uid).Return(&user.User{ID: uid}, nil).Once()
		circleRepo.On("FindCirclesByUserID", ctx, uid).Return(nil, errors.New("circle db error")).Once()

		_, err := svc.GetCircles(ctx, uid.String())
		assert.Error(t, err)
		repo.AssertExpectations(t)
		circleRepo.AssertExpectations(t)
	})

	t.Run("success", func(t *testing.T) {
		repo := new(userMocks.Repository)
		circleRepo := new(circleMocks.Repository)
		svc := user.NewService(repo, circleRepo)
		repo.On("FindByID", ctx, uid).Return(&user.User{ID: uid}, nil).Once()
		circleRepo.On("FindCirclesByUserID", ctx, uid).Return([]circle.Circle{{ID: uuid.New(), Name: "Tech"}}, nil).Once()

		res, err := svc.GetCircles(ctx, uid.String())
		assert.NoError(t, err)
		assert.Len(t, res, 1)
		repo.AssertExpectations(t)
		circleRepo.AssertExpectations(t)
	})
}

func TestUserService_IsEmailTaken(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()

	t.Run("taken", func(t *testing.T) {
		repo.On("FindByEmail", ctx, "taken@example.com").Return(&user.User{ID: uuid.New()}, nil).Once()
		taken, err := svc.IsEmailTaken(ctx, "taken@example.com")
		assert.NoError(t, err)
		assert.True(t, taken)
	})

	t.Run("not taken", func(t *testing.T) {
		repo.On("FindByEmail", ctx, "free@example.com").Return(nil, apperrors.ErrNotFound).Once()
		taken, err := svc.IsEmailTaken(ctx, "free@example.com")
		assert.NoError(t, err)
		assert.False(t, taken)
	})

	t.Run("error", func(t *testing.T) {
		repo.On("FindByEmail", ctx, "err@example.com").Return(nil, errors.New("db error")).Once()
		taken, err := svc.IsEmailTaken(ctx, "err@example.com")
		assert.Error(t, err)
		assert.False(t, taken)
	})
	repo.AssertExpectations(t)
}

func TestUserService_Update(t *testing.T) {
	repo := new(userMocks.Repository)
	svc := user.NewService(repo, nil)
	ctx := context.Background()
	u := &user.User{ID: uuid.New()}

	repo.On("Update", ctx, u).Return(nil).Once()
	err := svc.Update(ctx, u)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}
