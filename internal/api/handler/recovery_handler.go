package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

// RecoveryHandler handles backup-code login recovery for accounts with TOTP
// enabled: a valid backup code authenticates the user and consumes the code.
type RecoveryHandler struct {
	authService auth.Service
	userService user.Service
	userRepo    user.Repository
	totpService *totp.Service
}

// NewRecoveryHandler builds the TOTP backup-code recovery handler.
func NewRecoveryHandler(authSvc auth.Service, userSvc user.Service, userRepo user.Repository, totpSvc *totp.Service) *RecoveryHandler {
	if totpSvc == nil {
		totpSvc = totp.NewService()
	}
	return &RecoveryHandler{authService: authSvc, userService: userSvc, userRepo: userRepo, totpService: totpSvc}
}

// Recovery authenticates a user with an email + backup code when their TOTP
// authenticator is unavailable. The used backup code is consumed (removed from
// the stored hashed list).
//
// @Summary Recover account with backup code
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Recovery payload" {"email":"string","backupCode":"string"}
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /auth/recovery [post]
func (h *RecoveryHandler) Recovery(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		BackupCode string `json:"backupCode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and backup code are required")
		return
	}

	u, err := h.userService.GetByWallet(c.Request.Context(), emailWalletAddress(req.Email))
	if err != nil {
		response.NotFound(c, "account not found")
		return
	}
	if len(u.BackupCodes) == 0 {
		response.BadRequest(c, "no backup codes remaining")
		return
	}

	remaining, valid := h.totpService.ValidateBackupCode(req.BackupCode, u.BackupCodes)
	if !valid {
		response.BadRequest(c, "invalid backup code")
		return
	}

	u.BackupCodes = remaining
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to consume backup code")
		return
	}

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, string(u.Role), sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}
