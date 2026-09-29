package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
)

type emailChangeUsersFake struct {
	user *user.User
}

func (f *emailChangeUsersFake) GetByID(context.Context, string) (*user.User, error) {
	return f.user, nil
}
func (f *emailChangeUsersFake) IsEmailTaken(context.Context, string) (bool, error) {
	return false, nil
}

type emailChangeRepoFake struct{ updates int }

func (f *emailChangeRepoFake) Update(context.Context, *user.User) error {
	f.updates++
	return nil
}

func TestEmailChangeRequiresBothConfirmationsAndPreservesSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	oldEmail := "old@example.com"
	users := &emailChangeUsersFake{user: &user.User{ID: uuid.New(), Email: &oldEmail, EmailVerified: true}}
	repo := &emailChangeRepoFake{}
	verificationSvc := verification.NewService(rdb)
	codes := map[string]string{}
	verificationSvc.WithEmailSender(func(address, code string) error {
		codes[address] = code
		return nil
	}, nil)
	h := NewEmailChangeHandler(users, repo, verificationSvc, rdb)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", users.user.ID.String())
		c.Next()
	})
	r.POST("/request", h.RequestEmailChange)
	r.POST("/verify-current", h.VerifyCurrentEmail)
	r.POST("/verify-new", h.VerifyNewEmail)

	// An unrelated active session represents the acceptance requirement that
	// email changes do not revoke or rotate existing sessions.
	require.NoError(t, rdb.Set(context.Background(), "session:existing", "active", 0).Err())

	w := performEmailChangeRequest(r, "/request", `{"newEmail":"new@example.com"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, codes[oldEmail])

	// The new-address step cannot bypass current-address confirmation.
	w = performEmailChangeRequest(r, "/verify-new", `{"code":"000000"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, oldEmail, *users.user.Email)
	require.Zero(t, repo.updates)

	w = performEmailChangeRequest(r, "/verify-current", `{"code":"`+codes[oldEmail]+`"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, codes["new@example.com"])
	require.Equal(t, oldEmail, *users.user.Email, "old confirmation alone must not switch the address")

	w = performEmailChangeRequest(r, "/verify-new", `{"code":"`+codes["new@example.com"]+`"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "new@example.com", *users.user.Email)
	require.True(t, users.user.EmailVerified)
	require.Equal(t, 1, repo.updates)
	require.Equal(t, "active", rdb.Get(context.Background(), "session:existing").Val())
}

func performEmailChangeRequest(r http.Handler, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
