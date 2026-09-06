package handler

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/response"
)

// RegistrationHandler handles the email + password registration flow: starting
// registration (sends an email OTP) and completing it by confirming the OTP.
type RegistrationHandler struct {
	authService     auth.Service
	userRepo        user.Repository
	verificationSvc *verification.Service
	walletSvc       wallet.Service
}

// NewRegistrationHandler builds the email/password registration handler.
func NewRegistrationHandler(authSvc auth.Service, userRepo user.Repository,
	verificationSvc *verification.Service, walletSvc wallet.Service) *RegistrationHandler {
	return &RegistrationHandler{
		authService:     authSvc,
		userRepo:        userRepo,
		verificationSvc: verificationSvc,
		walletSvc:       walletSvc,
	}
}

// emailWalletAddress returns the deterministic pseudo-wallet address used for
// email-based accounts. It is a stable, non-reversible identifier derived from
// the first 16 bytes of the email's SHA-256 digest.
func emailWalletAddress(email string) string {
	h := sha256.Sum256([]byte(email))
	return "EMAIL:" + hex.EncodeToString(h[:16])
}

// Register starts the email-based registration flow. It accepts an email +
// password, checks for existing accounts, and dispatches a 6-digit OTP to the
// provided email address. The pending registration is stored in Redis until
// RegisterVerify confirms the OTP.
//
// Email is stored using user.HashEmail (full SHA-256 hex) — the single
// canonical representation used by FindByEmail and UpdateProfile.
//
// @Summary Start email registration
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Registration payload" {"email":"string","password":"string"}
// @Success 201 {object} response.Envelope
// @Failure 400 {object} response.Envelope
// @Failure 409 {object} response.Envelope
// @Router /auth/register [post]
func (h *RegistrationHandler) Register(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Derive a deterministic wallet address for email-based accounts.
	walletAddr := emailWalletAddress(req.Email)

	// Check for an existing account using the wallet address key.
	existing, err := h.userRepo.FindByWalletAddress(c.Request.Context(), walletAddr)
	if err != nil && err != apperrors.ErrNotFound {
		response.InternalError(c, "failed to check existing account")
		return
	}
	if existing != nil {
		response.Conflict(c, "account already exists")
		return
	}

	// Hash the password before storing in pending registration.
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "failed to process password")
		return
	}

	// Store the pending registration and send the OTP.
	pending := verification.PendingRegistration{
		Email:        req.Email,
		PasswordHash: passwordHash,
		WalletAddr:   walletAddr,
	}
	if err := h.verificationSvc.StorePendingRegistration(c.Request.Context(), req.Email, &pending); err != nil {
		response.InternalError(c, "failed to store pending registration")
		return
	}

	if err := h.verificationSvc.SendOTP(c.Request.Context(), req.Email); err != nil {
		response.InternalError(c, "failed to send verification code")
		return
	}

	response.Created(c, gin.H{"message": "verification code sent to your email"})
}

// RegisterVerify completes the email registration flow by confirming the OTP.
// On success it creates the user record (with hashed email), creates the
// on-chain wallet, and issues a session.
//
// @Summary Complete email registration
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Verification payload" {"email":"string","code":"string"}
// @Success 201 {object} response.Envelope
// @Failure 400 {object} response.Envelope
// @Router /auth/register/verify [post]
func (h *RegistrationHandler) RegisterVerify(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Verify the OTP.
	valid, err := h.verificationSvc.VerifyOTP(c.Request.Context(), req.Email, req.Code)
	if err != nil || !valid {
		response.BadRequest(c, "invalid or expired verification code")
		return
	}

	// Retrieve the pending registration payload.
	pending, err := h.verificationSvc.GetPendingRegistration(c.Request.Context(), req.Email)
	if err != nil || pending == nil {
		response.BadRequest(c, "registration session expired; please register again")
		return
	}

	ctx := c.Request.Context()

	// Hash the email using the single canonical transform: full SHA-256 hex.
	// This matches FindByEmail and UpdateProfile so all paths are consistent.
	hashedEmail := user.HashEmail(req.Email)

	now := time.Now().UTC()
	u := &user.User{
		ID:                uuid.New(),
		WalletAddress:     pending.WalletAddr,
		PreferredLanguage: strings.TrimSpace(pending.Language),
		Role:              user.RoleUser,
		Email:             &hashedEmail,
		EmailVerified:     true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if pending.DisplayName != "" {
		u.DisplayName = &pending.DisplayName
	}
	// Store the password hash if provided.
	if pending.PasswordHash != "" {
		u.PasswordHash = sql.NullString{String: pending.PasswordHash, Valid: true}
	}

	if err := h.userRepo.Create(ctx, u); err != nil {
		if err == apperrors.ErrConflict {
			response.Conflict(c, "account already exists")
			return
		}
		response.InternalError(c, "failed to create account")
		return
	}

	// Derive wallet seed and create the on-chain wallet.
	if h.walletSvc != nil {
		if seed, seedErr := h.walletSvc.DeriveWalletSeed(ctx, req.Email); seedErr == nil {
			// Ignore wallet creation errors — user is already persisted.
			_, _ = h.walletSvc.CreateWallet(ctx, u.ID.String(), []byte(seed))
		}
	}

	// Clean up the pending registration.
	_ = h.verificationSvc.DeletePendingRegistration(ctx, req.Email)

	// Issue a session.
	pair, err := h.authService.CreateSession(ctx, u.ID, string(u.Role), sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.Created(c, gin.H{
		"token":        pair.AccessToken,
		"refreshToken": pair.RefreshToken,
		"csrfToken":    pair.CSRFToken,
	})
}
