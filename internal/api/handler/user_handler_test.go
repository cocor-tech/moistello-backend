package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/user"
)

func TestUserHandler_ClaimName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := handler.NewUserHandler(&fakeUserService{claimName: "@moistello_user"})

	r := gin.New()
	r.POST("/v1/users/username/claim", h.ClaimName)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/users/username/claim", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	data, ok := body["data"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, true, data["success"])
	assert.Equal(t, "username claimed successfully", data["message"])
}

func TestUserHandler_UpdateProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := handler.NewUserHandler(&fakeUserService{})

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})
	r.PATCH("/v1/users/me", h.UpdateProfile)

	w := httptest.NewRecorder()
	payload := `{"displayName":"Alice"}`
	req, _ := http.NewRequest("PATCH", "/v1/users/me", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data, ok := body["data"].(map[string]any)
	assert.True(t, ok)
	u, ok := data["user"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "Alice", u["displayName"])
}

func TestUserHandler_UpdateProfile_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := handler.NewUserHandler(&fakeUserService{})

	r := gin.New()
	r.PATCH("/v1/users/me", h.UpdateProfile)

	w := httptest.NewRecorder()
	payload := `{"displayName":"Alice"}`
	req, _ := http.NewRequest("PATCH", "/v1/users/me", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

type fakeUserService struct {
	user      *user.User
	claimName string
}

func (s *fakeUserService) GetByID(context.Context, string) (*user.User, error) {
	return s.user, nil
}

func (s *fakeUserService) Update(context.Context, *user.User) error {
	return nil
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

func (s *fakeUserService) UpdateProfile(_ context.Context, _ string, updates user.UpdateProfileInput) (*user.User, error) {
	if s.user == nil {
		s.user = &user.User{}
	}
	if updates.DisplayName != nil {
		s.user.DisplayName = updates.DisplayName
	}
	return s.user, nil
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
	return s.claimName, nil
}

func (s *fakeUserService) UpdateNotificationPreferences(_ context.Context, _ string, _ user.NotificationPrefsInput) (*user.User, error) {
	return nil, nil
}
