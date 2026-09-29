package handler

import (
	"context"
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/pkg/response"
)

const emailChangeTTL = 15 * time.Minute

type emailChangeState struct {
	CurrentEmail string `json:"currentEmail"`
	NewEmail     string `json:"newEmail"`
	OldVerified  bool   `json:"oldVerified"`
}

type emailChangeUsers interface {
	GetByID(context.Context, string) (*user.User, error)
	IsEmailTaken(context.Context, string) (bool, error)
}

type emailChangeRepository interface {
	Update(context.Context, *user.User) error
}

type EmailChangeHandler struct {
	users        emailChangeUsers
	userRepo     emailChangeRepository
	verification *verification.Service
	rdb          *redis.Client
}

func NewEmailChangeHandler(users emailChangeUsers, repo emailChangeRepository, verificationSvc *verification.Service, rdb *redis.Client) *EmailChangeHandler {
	return &EmailChangeHandler{users: users, userRepo: repo, verification: verificationSvc, rdb: rdb}
}

func emailChangeKey(userID string) string { return "email-change:" + userID }

func (h *EmailChangeHandler) RequestEmailChange(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		NewEmail string `json:"newEmail" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "newEmail is required")
		return
	}
	req.NewEmail = strings.ToLower(strings.TrimSpace(req.NewEmail))
	address, err := mail.ParseAddress(req.NewEmail)
	if err != nil || address.Address != req.NewEmail {
		response.BadRequest(c, "invalid email address")
		return
	}
	u, err := h.users.GetByID(c.Request.Context(), userID)
	if err != nil || u.Email == nil || strings.TrimSpace(*u.Email) == "" {
		response.BadRequest(c, "an existing verified email is required")
		return
	}
	current := strings.ToLower(strings.TrimSpace(*u.Email))
	if current == req.NewEmail {
		response.BadRequest(c, "new email must differ from current email")
		return
	}
	taken, err := h.users.IsEmailTaken(c.Request.Context(), req.NewEmail)
	if err != nil {
		response.InternalError(c, "failed to validate email")
		return
	}
	if taken {
		response.Conflict(c, "email is already in use")
		return
	}
	state := emailChangeState{CurrentEmail: current, NewEmail: req.NewEmail}
	encoded, _ := json.Marshal(state)
	if err := h.rdb.Set(c.Request.Context(), emailChangeKey(userID), encoded, emailChangeTTL).Err(); err != nil {
		response.InternalError(c, "failed to start email change")
		return
	}
	if err := h.verification.SendOTP(c.Request.Context(), current); err != nil {
		h.rdb.Del(c.Request.Context(), emailChangeKey(userID))
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"message": "verification code sent to current email"})
}

func (h *EmailChangeHandler) VerifyCurrentEmail(c *gin.Context) {
	userID := middleware.GetUserID(c)
	state, ok := h.loadState(c, userID)
	if !ok {
		return
	}
	code, ok := bindEmailCode(c)
	if !ok {
		return
	}
	valid, err := h.verification.VerifyOTP(c.Request.Context(), state.CurrentEmail, code)
	if err != nil || !valid {
		response.BadRequest(c, "invalid or expired current-email code")
		return
	}
	state.OldVerified = true
	encoded, _ := json.Marshal(state)
	if err := h.rdb.Set(c.Request.Context(), emailChangeKey(userID), encoded, emailChangeTTL).Err(); err != nil {
		response.InternalError(c, "failed to continue email change")
		return
	}
	if err := h.verification.SendOTP(c.Request.Context(), state.NewEmail); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"message": "verification code sent to new email"})
}

func (h *EmailChangeHandler) VerifyNewEmail(c *gin.Context) {
	userID := middleware.GetUserID(c)
	state, ok := h.loadState(c, userID)
	if !ok {
		return
	}
	if !state.OldVerified {
		response.BadRequest(c, "current email must be verified first")
		return
	}
	code, ok := bindEmailCode(c)
	if !ok {
		return
	}
	valid, err := h.verification.VerifyOTP(c.Request.Context(), state.NewEmail, code)
	if err != nil || !valid {
		response.BadRequest(c, "invalid or expired new-email code")
		return
	}
	u, err := h.users.GetByID(c.Request.Context(), userID)
	if err != nil || u.Email == nil || !strings.EqualFold(strings.TrimSpace(*u.Email), state.CurrentEmail) {
		response.Conflict(c, "current email changed; restart the flow")
		return
	}
	u.Email = &state.NewEmail
	u.EmailVerified = true
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to update email")
		return
	}
	h.rdb.Del(c.Request.Context(), emailChangeKey(userID))
	log.Info().Str("security_event", "email.changed").Str("userID", userID).
		Str("oldEmailHash", user.HashEmail(state.CurrentEmail)).Str("newEmailHash", user.HashEmail(state.NewEmail)).
		Msg("account email changed after dual verification")
	response.OK(c, gin.H{"message": "email changed", "email": state.NewEmail})
}

func (h *EmailChangeHandler) loadState(c *gin.Context, userID string) (emailChangeState, bool) {
	data, err := h.rdb.Get(c.Request.Context(), emailChangeKey(userID)).Bytes()
	if err != nil {
		response.BadRequest(c, "email change request expired or not found")
		return emailChangeState{}, false
	}
	var state emailChangeState
	if err := json.Unmarshal(data, &state); err != nil {
		response.InternalError(c, "invalid email change state")
		return emailChangeState{}, false
	}
	return state, true
}

func bindEmailCode(c *gin.Context) (string, bool) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		response.BadRequest(c, "code is required")
		return "", false
	}
	return strings.TrimSpace(req.Code), true
}
