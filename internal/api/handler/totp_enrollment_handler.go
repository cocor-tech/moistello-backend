package handler

import (
	"database/sql"
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

// TOTPEnrollmentHandler implements the authenticator (TOTP, RFC 6238)
// enrollment lifecycle for the signed-in user: enroll (secret + QR), enable
// (first code proves the app works and mints recovery codes), verify
// (step-up check with a code or a single-use recovery code), disable, and
// recovery-code rotation.
type TOTPEnrollmentHandler struct {
	userService user.Service
	userRepo    user.Repository
	totp        *totp.Service
}

// NewTOTPEnrollmentHandler builds the TOTP enrollment handler.
func NewTOTPEnrollmentHandler(userSvc user.Service, userRepo user.Repository, totpSvc *totp.Service) *TOTPEnrollmentHandler {
	if totpSvc == nil {
		totpSvc = totp.NewService()
	}
	return &TOTPEnrollmentHandler{userService: userSvc, userRepo: userRepo, totp: totpSvc}
}

// totpChallenge is the body accepted by every endpoint that must prove
// possession of the authenticator: either a current 6-digit code or one of
// the single-use recovery codes.
type totpChallenge struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
}

// @Summary Start TOTP enrollment
// @Description Generates a new secret and returns the otpauth:// provisioning URI plus a PNG QR code (base64). The secret stays pending until /auth/totp/enable confirms a code from the authenticator app.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{secret=string,uri=string,qrPngBase64=string}}
// @Failure 409 {object} response.Envelope "TOTP already enabled"
// @Router /auth/totp/enroll [post]
func (h *TOTPEnrollmentHandler) EnrollTOTP(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	if u.TOTPEnabled {
		response.Conflict(c, "TOTP is already enabled; disable it before enrolling again")
		return
	}

	secret, uri, err := h.totp.GenerateSecret(accountLabel(u))
	if err != nil {
		response.InternalError(c, "failed to generate TOTP secret")
		return
	}
	qr, err := h.totp.ProvisioningQR(uri, totp.DefaultQRSize)
	if err != nil {
		response.InternalError(c, "failed to render provisioning QR code")
		return
	}

	u.TOTPSecret = sql.NullString{String: secret, Valid: true}
	u.TOTPEnabled = false
	u.BackupCodes = nil
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to save TOTP enrollment")
		return
	}

	response.OK(c, gin.H{
		"secret":      secret,
		"uri":         uri,
		"qrPngBase64": base64.StdEncoding.EncodeToString(qr),
	})
}

// @Summary Enable TOTP
// @Description Confirms the pending enrollment with a code from the authenticator app, turns TOTP on and returns the recovery codes. Each recovery code works exactly once and is only shown here.
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Current code" { "code": "123456" }
// @Success 200 {object} response.Envelope{data=object{enabled=bool,recoveryCodes=array}}
// @Failure 400 {object} response.Envelope "No pending enrollment or wrong code"
// @Failure 409 {object} response.Envelope "TOTP already enabled"
// @Router /auth/totp/enable [post]
func (h *TOTPEnrollmentHandler) EnableTOTP(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "code is required")
		return
	}
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	if u.TOTPEnabled {
		response.Conflict(c, "TOTP is already enabled")
		return
	}
	if !u.TOTPSecret.Valid || u.TOTPSecret.String == "" {
		response.BadRequest(c, "no pending enrollment; call /auth/totp/enroll first")
		return
	}
	if !h.totp.ValidateCode(u.TOTPSecret.String, req.Code) {
		response.ErrorWithCode(c, http.StatusBadRequest, "invalid_totp_code", "invalid TOTP code")
		return
	}

	codes, err := h.totp.GenerateBackupCodes()
	if err != nil {
		response.InternalError(c, "failed to generate recovery codes")
		return
	}
	u.TOTPEnabled = true
	u.BackupCodes = pq.StringArray(h.totp.HashBackupCodes(codes))
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to enable TOTP")
		return
	}

	response.OK(c, gin.H{"enabled": true, "recoveryCodes": plainCodes(codes)})
}

// @Summary Verify a TOTP or recovery code
// @Description Step-up check for sensitive actions. Accepts a current authenticator code or a recovery code; a recovery code is consumed on success.
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Code or recovery code" { "code": "123456", "recoveryCode": "ABCD-EFGH-JKLM" }
// @Success 200 {object} response.Envelope{data=object{verified=bool,method=string,recoveryCodesRemaining=int}}
// @Failure 400 {object} response.Envelope "TOTP not enabled or wrong code"
// @Router /auth/totp/verify [post]
func (h *TOTPEnrollmentHandler) VerifyTOTP(c *gin.Context) {
	u, method, ok := h.challenge(c)
	if !ok {
		return
	}
	if method == "recovery" {
		if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
			response.InternalError(c, "failed to record recovery code use")
			return
		}
	}
	response.OK(c, gin.H{
		"verified":               true,
		"method":                 method,
		"recoveryCodesRemaining": len(u.BackupCodes),
	})
}

// @Summary Disable TOTP
// @Description Turns TOTP off after a current code or a recovery code proves possession. The secret and all recovery codes are discarded.
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Code or recovery code" { "code": "123456", "recoveryCode": "ABCD-EFGH-JKLM" }
// @Success 200 {object} response.Envelope{data=object{enabled=bool}}
// @Failure 400 {object} response.Envelope "TOTP not enabled or wrong code"
// @Router /auth/totp/disable [post]
func (h *TOTPEnrollmentHandler) DisableTOTP(c *gin.Context) {
	u, _, ok := h.challenge(c)
	if !ok {
		return
	}
	u.TOTPEnabled = false
	u.TOTPSecret = sql.NullString{}
	u.BackupCodes = nil
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to disable TOTP")
		return
	}
	response.OK(c, gin.H{"enabled": false})
}

// @Summary Rotate recovery codes
// @Description Replaces every recovery code after a current authenticator code proves possession. Old codes stop working immediately.
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Current code" { "code": "123456" }
// @Success 200 {object} response.Envelope{data=object{recoveryCodes=array}}
// @Failure 400 {object} response.Envelope "TOTP not enabled or wrong code"
// @Router /auth/totp/recovery-codes [post]
func (h *TOTPEnrollmentHandler) RegenerateTOTPRecoveryCodes(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "code is required")
		return
	}
	u, ok := h.currentUser(c)
	if !ok {
		return
	}
	if !u.TOTPEnabled || !u.TOTPSecret.Valid {
		response.BadRequest(c, "TOTP is not enabled")
		return
	}
	if !h.totp.ValidateCode(u.TOTPSecret.String, req.Code) {
		response.ErrorWithCode(c, http.StatusBadRequest, "invalid_totp_code", "invalid TOTP code")
		return
	}
	codes, err := h.totp.GenerateBackupCodes()
	if err != nil {
		response.InternalError(c, "failed to generate recovery codes")
		return
	}
	u.BackupCodes = pq.StringArray(h.totp.HashBackupCodes(codes))
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to save recovery codes")
		return
	}
	response.OK(c, gin.H{"recoveryCodes": plainCodes(codes)})
}

// challenge loads the user and checks the submitted code or recovery code.
// On a recovery-code match the code is removed from u.BackupCodes; callers
// persist u. It writes the error response itself when ok is false.
func (h *TOTPEnrollmentHandler) challenge(c *gin.Context) (u *user.User, method string, ok bool) {
	var req totpChallenge
	if err := c.ShouldBindJSON(&req); err != nil || (req.Code == "" && req.RecoveryCode == "") {
		response.BadRequest(c, "code or recoveryCode is required")
		return nil, "", false
	}
	u, ok = h.currentUser(c)
	if !ok {
		return nil, "", false
	}
	if !u.TOTPEnabled || !u.TOTPSecret.Valid {
		response.BadRequest(c, "TOTP is not enabled")
		return nil, "", false
	}
	if req.Code != "" {
		if h.totp.ValidateCode(u.TOTPSecret.String, req.Code) {
			return u, "totp", true
		}
		response.ErrorWithCode(c, http.StatusBadRequest, "invalid_totp_code", "invalid TOTP code")
		return nil, "", false
	}
	remaining, valid := h.totp.ValidateBackupCode(req.RecoveryCode, []string(u.BackupCodes))
	if !valid {
		response.ErrorWithCode(c, http.StatusBadRequest, "invalid_recovery_code", "invalid or already used recovery code")
		return nil, "", false
	}
	u.BackupCodes = pq.StringArray(remaining)
	return u, "recovery", true
}

func (h *TOTPEnrollmentHandler) currentUser(c *gin.Context) (*user.User, bool) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "authentication required")
		return nil, false
	}
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil || u == nil {
		response.Unauthorized(c, "user not found")
		return nil, false
	}
	return u, true
}

// accountLabel picks the identifier the authenticator app displays.
func accountLabel(u *user.User) string {
	if u.Email != nil && *u.Email != "" {
		return *u.Email
	}
	if u.WalletAddress != "" {
		return u.WalletAddress
	}
	return u.ID.String()
}

func plainCodes(codes []totp.BackupCode) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = c.Plain
	}
	return out
}
