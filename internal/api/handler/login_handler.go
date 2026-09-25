package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/pkg/response"
)

// LoginHandler handles email + password authentication (Login), the
// counterpart to the RegistrationHandler flows.
type LoginHandler struct {
	authService     auth.Service
	userService     user.Service
	verificationSvc *verification.Service
}

// NewLoginHandler builds the email/password login handler.
func NewLoginHandler(authSvc auth.Service, userSvc user.Service, verificationSvc *verification.Service) *LoginHandler {
	return &LoginHandler{authService: authSvc, userService: userSvc, verificationSvc: verificationSvc}
}

// Login authenticates an existing email + password account and issues a
// session. If the account's email is not yet verified it re-sends the OTP
// instead of logging the user in.
//
// @Summary Login with email and password
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Login payload" {"email":"string","password":"string"}
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /auth/login [post]
func (h *LoginHandler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and password are required")
		return
	}

	u, err := h.userService.GetByWallet(c.Request.Context(), emailWalletAddress(req.Email))
	if err != nil {
		response.NotFound(c, "account not found")
		return
	}

	if !u.PasswordHash.Valid {
		response.BadRequest(c, "account has no password set. use passkey.")
		return
	}

	if !auth.VerifyPassword(req.Password, u.PasswordHash.String) {
		response.Unauthorized(c, "incorrect password")
		return
	}

	if !u.EmailVerified {
		if h.verificationSvc == nil {
			response.InternalError(c, "email verification service is unavailable")
			return
		}
		if err := h.verificationSvc.SendOTP(c.Request.Context(), req.Email); err != nil {
			response.InternalError(c, "failed to send verification code")
			return
		}
		response.OK(c, gin.H{"needsVerification": true, "message": "email not verified. code sent."})
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
