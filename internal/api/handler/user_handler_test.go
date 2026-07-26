package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/user"
)

func TestUserHandler_GetByID_ReturnsSanitizedPublicProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	displayName := "Moistello User"
	email := "private@example.com"
	phone := "+15555550100"
	userID := uuid.New()

	h := handler.NewUserHandler(&fakeUserService{
		user: &user.User{
			ID:                userID,
			WalletAddress:     "GABC_PUBLIC_KEY",
			Email:             &email,
			Phone:             &phone,
			DisplayName:       &displayName,
			MoiScore:          700,
			Role:              user.RoleAdmin,
			SessionTTLMinutes: 1440,
			EmailVerified:     true,
		},
	})

	r := gin.New()
	r.GET("/v1/users/:id", h.GetByID)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/users/"+userID.String(), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	data := body["data"].(map[string]any)
	profile := data["user"].(map[string]any)

	assert.Equal(t, "GABC_PUBLIC_KEY", profile["publicKey"])
	assert.Equal(t, "Moistello User", profile["displayName"])
	assert.Equal(t, float64(700), profile["moiScore"])
	assert.NotContains(t, profile, "email")
	assert.NotContains(t, profile, "phone")
	assert.NotContains(t, profile, "role")
	assert.NotContains(t, profile, "sessionTtlMinutes")
	assert.NotContains(t, profile, "emailVerified")
}

type fakeUserService struct {
	user *user.User
}

func (s *fakeUserService) GetByID(context.Context, string) (*user.User, error) {
	return s.user, nil
}

func (s *fakeUserService) GetByWallet(context.Context, string) (*user.User, error) {
	return nil, nil
}

func (s *fakeUserService) GetByEmail(context.Context, string) (*user.User, error) {
	return nil, nil
}

func (s *fakeUserService) Create(context.Context, string) (*user.User, error) {
	return nil, nil
}

func (s *fakeUserService) Delete(context.Context, string) error {
	return nil
}

func (s *fakeUserService) UpdateProfile(context.Context, string, user.UpdateProfileInput) (*user.User, error) {
	return nil, nil
}

func (s *fakeUserService) IsEmailTaken(context.Context, string) (bool, error) {
	return false, nil
}

func (s *fakeUserService) GetMoiScore(context.Context, string) (*user.MoiScoreResponse, error) {
	return nil, nil
}

func (s *fakeUserService) GetCircles(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *fakeUserService) ClaimName(context.Context) (string, error) {
	return "", nil
}

func (s *fakeUserService) UpdateNotificationPreferences(_ context.Context, _ string, _ user.NotificationPrefsInput) (*user.User, error) {
	return nil, nil
}
