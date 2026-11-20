package handler_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func postJSON(r *gin.Engine, path string, body any, headers map[string]string) (int, map[string]any, string) {
	payload, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(w, req)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	return w.Code, envelope.Data, w.Body.String()
}

// #459: the session that made the request must not survive a password change;
// the caller gets a brand-new token pair instead.
func TestChangePassword_ReplacesEverySessionIncludingTheCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.New()
	oldHash, err := auth.HashPassword("old-password-123")
	require.NoError(t, err)

	repo := new(userMocks.Repository)
	repo.On("FindByID", mock.Anything, uid).Return(&user.User{
		ID: uid, Role: user.RoleAdmin, PasswordHash: sql.NullString{String: oldHash, Valid: true},
	}, nil)
	repo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	authSvc := new(mockAuthService)
	authSvc.On("RevokeAllSessions", mock.Anything, uid.String(), "").Return(nil).Once()
	authSvc.On("CreateSession", mock.Anything, uid, "admin", mock.AnythingOfType("time.Duration"), mock.AnythingOfType("string")).
		Return(&auth.TokenPair{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", CSRFToken: "fresh-csrf"}, nil).Once()

	h := handler.NewAuthHandler(authSvc, user.NewService(repo, nil), nil, nil, nil, nil, nil, repo)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uid.String()); c.Next() })
	r.POST("/auth/password/change", h.ChangePassword)

	code, data, _ := postJSON(r, "/auth/password/change",
		gin.H{"oldPassword": "old-password-123", "newPassword": "new-password-456"},
		map[string]string{"Authorization": "Bearer pre-change-access"})

	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "fresh-access", data["token"])
	assert.Equal(t, "fresh-refresh", data["refreshToken"])
	assert.Equal(t, "fresh-csrf", data["csrfToken"])
	authSvc.AssertExpectations(t)
}

func TestChangePassword_SignalsReauthWhenNoReplacementSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.New()

	repo := new(userMocks.Repository)
	repo.On("FindByID", mock.Anything, uid).Return(&user.User{ID: uid}, nil)
	repo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	authSvc := new(mockAuthService)
	authSvc.On("RevokeAllSessions", mock.Anything, uid.String(), "").Return(nil).Once()
	authSvc.On("CreateSession", mock.Anything, uid, "user", mock.Anything, mock.Anything).
		Return(nil, assert.AnError).Once()

	h := handler.NewAuthHandler(authSvc, user.NewService(repo, nil), nil, nil, nil, nil, nil, repo)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uid.String()); c.Next() })
	r.POST("/auth/password/change", h.ChangePassword)

	code, data, _ := postJSON(r, "/auth/password/change",
		gin.H{"oldPassword": "unchecked-no-hash-set", "newPassword": "new-password-456"}, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, data["reauthRequired"])
	assert.NotContains(t, data, "token")
	authSvc.AssertExpectations(t)
}

// #459: a session minted while the user was an admin must not keep refreshing
// into admin tokens once the user has been demoted.
func TestRefresh_RejectsSessionThatPredatesARoleChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.New()

	repo := new(userMocks.Repository)
	repo.On("FindByID", mock.Anything, uid).Return(&user.User{ID: uid, Role: user.RoleUser}, nil)

	authSvc := new(mockAuthService)
	authSvc.On("RefreshToken", mock.Anything, "admin-era-refresh").
		Return(&auth.TokenPair{AccessToken: "minted-as-admin", RefreshToken: "next-refresh"}, nil)
	authSvc.On("ValidateJWT", "minted-as-admin").Return(&auth.JWTCustomClaims{UserID: uid.String(), Role: "admin"}, nil)
	authSvc.On("RevokeAllSessions", mock.Anything, uid.String(), "").Return(nil).Once()

	h := handler.NewAuthHandler(authSvc, user.NewService(repo, nil), nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/refresh", h.Refresh)

	code, _, body := postJSON(r, "/auth/refresh", gin.H{"refreshToken": "admin-era-refresh"}, nil)

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.NotContains(t, body, "minted-as-admin", "the stale-role token must not be handed out")
	assert.NotContains(t, body, "next-refresh")
	authSvc.AssertExpectations(t)
}

func TestRefresh_RejectsSessionOfDeletedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.New()

	repo := new(userMocks.Repository)
	repo.On("FindByID", mock.Anything, uid).Return(nil, apperrors.ErrNotFound)

	authSvc := new(mockAuthService)
	authSvc.On("RefreshToken", mock.Anything, "orphan-refresh").
		Return(&auth.TokenPair{AccessToken: "orphan-access", RefreshToken: "orphan-next"}, nil)
	authSvc.On("ValidateJWT", "orphan-access").Return(&auth.JWTCustomClaims{UserID: uid.String(), Role: "user"}, nil)

	h := handler.NewAuthHandler(authSvc, user.NewService(repo, nil), nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/refresh", h.Refresh)

	code, _, body := postJSON(r, "/auth/refresh", gin.H{"refreshToken": "orphan-refresh"}, nil)

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.NotContains(t, body, "orphan-access")
}
