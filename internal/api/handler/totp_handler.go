package handler

import (
	"database/sql"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

// TOTPHandler handles optional TOTP 2FA setup for authenticated users:
// generating a new secret (SetupTOTP) and confirming setup, which activates
// TOTP and issues backup codes (VerifyTOTPSetup).
type TOTPHandler struct {
	userService user.Service
	userRepo    user.Repository
	totpService *totp.Service
}

// NewTOTPHandler builds the TOTP 2FA handler.
func NewTOTPHandler(userSvc user.Service, userRepo user.Repository, totpSvc *totp.Service) *TOTPHandler {
	if totpSvc == nil {
		totpSvc = totp.NewService()
	}
	return &TOTPHandler{userService: userSvc, userRepo: userRepo, totpService: totpSvc}
}

// SetupTOTP generates a new TOTP secret and provisioning URI for the
// authenticated user. The secret is staged on the account but not activated
// until VerifyTOTPSetup confirms a valid code.
//
// @Summary Set up TOTP 2FA
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{totpSecret=string,totpUri=string}}
// @Failure 404 {object} response.Envelope
// @Router /auth/totp/setup [post]
func (h *TOTPHandler) SetupTOTP(c *gin.Context) {
	userID := middleware.GetUserID(c)
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}

	email := ""
	if u.Email != nil {
		email = *u.Email
	}

	totpSecret, totpURI, err := h.totpService.GenerateSecret(email)
	if err != nil {
		response.InternalError(c, "failed to generate TOTP secret")
		return
	}

	u.TOTPSecret = totpSecretString(totpSecret)
	u.TOTPEnabled = false
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to save TOTP secret")
		return
	}

	response.OK(c, gin.H{"totpSecret": totpSecret, "totpUri": totpURI})
}

// VerifyTOTPSetup confirms that the user's authenticator app is producing
// valid codes, then activates TOTP and returns the one-time backup codes.
//
// @Summary Confirm TOTP setup
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "TOTP code" { "totpCode": "string" }
// @Success 200 {object} response.Envelope{data=object{backupCodes=array}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /auth/totp/verify [post]
func (h *TOTPHandler) VerifyTOTPSetup(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		TOTPCode string `json:"totpCode" binding:"required,len=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "6-digit TOTP code is required")
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}
	if !u.TOTPSecret.Valid {
		response.BadRequest(c, "TOTP not set up. use /auth/totp/setup first.")
		return
	}
	if !h.totpService.ValidateCode(u.TOTPSecret.String, req.TOTPCode) {
		response.BadRequest(c, "invalid TOTP code")
		return
	}

	backupCodes, err := h.totpService.GenerateBackupCodes()
	if err != nil {
		response.InternalError(c, "failed to generate backup codes")
		return
	}

	u.TOTPEnabled = true
	u.BackupCodes = h.totpService.HashBackupCodes(backupCodes)
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to save TOTP setup")
		return
	}

	plainCodes := make([]string, len(backupCodes))
	for i, bc := range backupCodes {
		plainCodes[i] = bc.Plain
	}
	response.OK(c, gin.H{"backupCodes": plainCodes})
}

// totpSecretString wraps a TOTP secret for storage as sql.NullString.
func totpSecretString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}
