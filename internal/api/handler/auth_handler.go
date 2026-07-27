package handler

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/email"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/pkg/stellar"
)

type AuthHandler struct {
	authService      auth.Service
	userService      user.Service
	walletSvc        wallet.Service
	totpService      *totp.Service
	verificationSvc  *verification.Service
	emailSvc         *email.Service
	redisClient      *redis.Client
	userRepo         user.Repository
}

func NewAuthHandler(authSvc auth.Service, userSvc user.Service, walletSvc wallet.Service,
	totpSvc *totp.Service, verificationSvc *verification.Service, emailSvc *email.Service,
	redisClient *redis.Client, userRepo user.Repository) *AuthHandler {
	return &AuthHandler{
		authService:     authSvc,
		userService:     userSvc,
		walletSvc:       walletSvc,
		totpService:     totpSvc,
		verificationSvc: verificationSvc,
		emailSvc:        emailSvc,
		redisClient:     redisClient,
		userRepo:        userRepo,
	}
}
// @Summary Get authentication nonce
// @Description Returns a signed nonce for wallet authentication. The nonce must be signed with the wallet's private key and sent to /auth/verify.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Wallet address" { "walletAddress": "G..." }
// @Success 200 {object} response.Envelope{data=object{nonce=string}}
// @Failure 400 {object} response.Envelope
// @Router /auth/nonce [post]
func (h *AuthHandler) Nonce(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := stellar.ValidateAddress(req.WalletAddress); err != nil {
		response.BadRequest(c, "invalid wallet address: "+err.Error())
		return
	}

	nonce, err := h.authService.GenerateNonce(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}
	response.OK(c, gin.H{"nonce": nonce})
}

// @Summary Refresh JWT tokens
// @Description Exchanges a valid refresh token for a new access token and refresh token pair.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Refresh token" { "refreshToken": "string" }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refreshToken" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	tokenPair, err := h.authService.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.Unauthorized(c, "invalid refresh token")
		return
	}
	response.OK(c, gin.H{"token": tokenPair.AccessToken, "refreshToken": tokenPair.RefreshToken, "csrfToken": tokenPair.CSRFToken})
}

// @Summary Get current user
// @Description Returns the authenticated user's profile. Requires Bearer token.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 401 {object} response.Envelope
// @Router /auth/me [post]
func (h *AuthHandler) Me(c *gin.Context) {
	userID := middleware.GetUserID(c)
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.Unauthorized(c, "user not found")
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Logout
// @Description Invalidates the current session and all refresh tokens.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Router /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		response.Unauthorized(c, "missing or invalid token")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	if h.redisClient != nil {
		// 1. Blocklist the access token
		if expiry, err := middleware.ExtractTokenExpiry(token); err == nil {
			middleware.BlocklistToken(ctx, h.redisClient, token, expiry)
		}

		// 2. Delete all user sessions from Redis
		if userID != "" {
			userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
			sessionHashes, err := h.redisClient.SMembers(ctx, userSessionsKey).Result()
			if err == nil {
				pipe := h.redisClient.Pipeline()
				for _, hash := range sessionHashes {
					pipe.Del(ctx, fmt.Sprintf("session:%s", hash))
				}
				pipe.Del(ctx, userSessionsKey)
				pipe.Exec(ctx)
			}

			// 3. Set blocklist key for any missed sessions
			middleware.BlocklistUserRefreshTokens(ctx, h.redisClient, userID)
		}

		// 4. If refresh token was provided in body, also delete that specific session
		var req struct {
			RefreshToken string `json:"refreshToken"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.RefreshToken != "" {
			tokenHash := sha256HashForLogout(req.RefreshToken)
			sessionKey := fmt.Sprintf("session:%s", tokenHash)
			h.redisClient.Del(ctx, sessionKey)
		}
	}

	response.OK(c, gin.H{"success": true})
}

// ──────────────────────────────────────────────
// Email OTP Registration & Login
// ──────────────────────────────────────────────

// Register sends an email OTP to begin registration.
// POST /auth/register { email }
// ── Registration: Email + Password ──

// Register creates a user with email+password and sends email OTP.
// POST /auth/register { email, password }
func (h *AuthHandler) Register(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "valid email and password (min 8 chars) are required")
		return
	}

	// Check if email already exists (by wallet address lookup)
	walletAddr := emailToWalletAddr(req.Email)
	existing, err := h.userService.GetByWallet(c.Request.Context(), walletAddr)
	if err == nil && existing != nil {
		response.Conflict(c, "email already registered. please log in.")
		return
	}

	// Check if there's already a pending registration for this email
	pending, err := h.verificationSvc.GetPendingRegistration(c.Request.Context(), req.Email)
	if err != nil {
		response.InternalError(c, "failed to check pending registration")
		return
	}
	if pending != nil {
		response.Conflict(c, "a verification code was already sent. check your email.")
		return
	}

	// Hash password
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "failed to process password")
		return
	}

	walletSeed := deriveWalletSeed(req.Email)

	// Store in Redis — NOT in PostgreSQL. User is only created after email verification.
	pendingData := &verification.PendingRegistration{
		PasswordHash: passwordHash,
		WalletAddr:   walletAddr,
		Email:        req.Email,
	}
	if err := h.verificationSvc.StorePendingRegistration(c.Request.Context(), req.Email, pendingData); err != nil {
		response.InternalError(c, "failed to save registration data")
		return
	}

	// Send OTP
	code, err := h.verificationSvc.SendOTP(c.Request.Context(), req.Email)
	if err != nil {
		response.InternalError(c, "failed to send verification code")
		return
	}
	if h.emailSvc != nil {
		h.emailSvc.SendOTP(req.Email, code)
	} else {
		log.Printf("[auth] verification code for %s: %s", req.Email, code)
	}

	response.Created(c, gin.H{
		"message": "verification code sent",
		"walletSeed": walletSeed,
		"expiresIn": 300,
	})
}

// RegisterVerify verifies the email OTP, creates the user, and returns a session.
// POST /auth/register/verify { email, code }
func (h *AuthHandler) RegisterVerify(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code" binding:"required,len=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and 6-digit code are required")
		return
	}

	valid, err := h.verificationSvc.VerifyOTP(c.Request.Context(), req.Email, req.Code)
	if err != nil || !valid {
		response.BadRequest(c, "invalid or expired verification code")
		return
	}

	// Read pending registration from Redis — user hasn't been created yet
	pending, err := h.verificationSvc.GetPendingRegistration(c.Request.Context(), req.Email)
	if err != nil {
		response.InternalError(c, "failed to read registration data")
		return
	}
	if pending == nil {
		response.BadRequest(c, "registration session expired. please start over.")
		return
	}

	// Double-check the user doesn't already exist (prevent race condition)
	existing, err := h.userService.GetByWallet(c.Request.Context(), pending.WalletAddr)
	if err == nil && existing != nil {
		response.Conflict(c, "account already exists.")
		return
	}

	// Create the user NOW — only after email is verified
	u := &user.User{
		ID:               uuid.New(),
		WalletAddress:    pending.WalletAddr,
		PasswordHash:     passwordHashStruct(pending.PasswordHash),
		Email:            &pending.Email,
		EmailVerified:    true,
		PreferredLanguage: "en",
		Role:             user.RoleUser,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := h.userRepo.Create(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to create account")
		return
	}

	// Wallet is NOT created here — it's triggered from the dashboard on first load.
	// This keeps registration instant (<100ms).
	// Wallet will be created deterministically from the email seed when
	// POST /auth/wallet/init is called from the frontend.

	h.verificationSvc.DeletePendingRegistration(c.Request.Context(), req.Email)

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.Created(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}

// ── Login: Email + Password ──
// POST /auth/login { email, password }
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and password are required")
		return
	}

	walletAddr := emailToWalletAddr(req.Email)
	u, err := h.userService.GetByWallet(c.Request.Context(), walletAddr)
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
		code, err := h.verificationSvc.SendOTP(c.Request.Context(), req.Email)
		if err != nil {
			response.InternalError(c, "failed to send verification code")
			return
		}
		if h.emailSvc != nil {
			h.emailSvc.SendOTP(req.Email, code)
		} else {
			log.Printf("[auth] verification code for %s: %s", req.Email, code)
		}
		response.OK(c, gin.H{"needsVerification": true, "message": "email not verified. code sent."})
		return
	}

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}

// ── Passkey Authentication ──

// PasskeyNonce generates a nonce for passkey-based wallet authentication.
// POST /auth/passkey/nonce { credentialId }
func (h *AuthHandler) PasskeyNonce(c *gin.Context) {
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId is required")
		return
	}

	// Look up user by passkey credential
	u, err := h.userRepo.FindByPasskeyCredentialID(c.Request.Context(), req.CredentialID)
	if err != nil {
		response.NotFound(c, "passkey not linked to any account")
		return
	}

	// Generate nonce for the user's wallet address
	nonce, err := h.authService.GenerateNonce(c.Request.Context(), u.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}

	response.OK(c, gin.H{"nonce": nonce, "walletAddress": u.WalletAddress})
}

// PasskeyVerify verifies a passkey-signed nonce and creates a session.
// POST /auth/passkey/verify { credentialId, signature }
func (h *AuthHandler) PasskeyVerify(c *gin.Context) {
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
		Signature    string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId and signature are required")
		return
	}

	u, err := h.userRepo.FindByPasskeyCredentialID(c.Request.Context(), req.CredentialID)
	if err != nil {
		response.NotFound(c, "passkey not linked to any account")
		return
	}

	valid, err := h.authService.VerifySignature(c.Request.Context(), u.WalletAddress, req.Signature)
	if err != nil || !valid {
		response.Unauthorized(c, "signature verification failed")
		return
	}

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}

// ── Optional TOTP 2FA (Settings only) ──

// SetupTOTP generates a new TOTP secret for an authenticated user.
// POST /auth/totp/setup [AUTH]
func (h *AuthHandler) SetupTOTP(c *gin.Context) {
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
	h.userRepo.Update(c.Request.Context(), u)

	response.OK(c, gin.H{"totpSecret": totpSecret, "totpUri": totpURI})
}

// VerifyTOTPSetup confirms TOTP setup and generates backup codes.
// POST /auth/totp/verify [AUTH] { totpCode }
func (h *AuthHandler) VerifyTOTPSetup(c *gin.Context) {
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
	h.userRepo.Update(c.Request.Context(), u)

	plainCodes := make([]string, len(backupCodes))
	for i, bc := range backupCodes {
		plainCodes[i] = bc.Plain
	}
	response.OK(c, gin.H{"backupCodes": plainCodes})
}

// Recovery uses a backup code to log in (bypasses TOTP).
// POST /auth/recovery { email, backupCode }
func (h *AuthHandler) Recovery(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		BackupCode string `json:"backupCode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and backup code are required")
		return
	}

	walletAddr := emailToWalletAddr(req.Email)
	u, err := h.userService.GetByWallet(c.Request.Context(), walletAddr)
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
	h.userRepo.Update(c.Request.Context(), u)

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}

// ── Wallet Initialization ──

// InitWallet creates the user's Stellar wallet from their email-derived seed.
// POST /auth/wallet/init [AUTH]
func (h *AuthHandler) InitWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "not authenticated")
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}

	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	if email == "" {
		response.BadRequest(c, "no email on account")
		return
	}

	walletSeed := deriveWalletSeed(email)
	w, err := h.walletSvc.CreateWallet(c.Request.Context(), userID, []byte(walletSeed))
	if err != nil {
		response.InternalError(c, "wallet creation failed: "+err.Error())
		return
	}

	response.Created(c, gin.H{"wallet": w})
}

// PasskeyLink links a passkey credential to the authenticated user's account.
// POST /auth/passkey/link [AUTH] { credentialId }
func (h *AuthHandler) PasskeyLink(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId is required")
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}

	u.PasskeyCredentialID = &req.CredentialID
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to link passkey")
		return
	}

	response.OK(c, gin.H{"success": true})
}

// ── Session Management ──

// ListSessions returns all active sessions for the authenticated user.
// GET /sessions [AUTH]
func (h *AuthHandler) ListSessions(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "not authenticated")
		return
	}

	authHeader := c.GetHeader("Authorization")
	currentHash := ""
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 {
			sha := sha256.Sum256([]byte(parts[1]))
			currentHash = fmt.Sprintf("%x", sha)
		}
	}

	sessions, err := h.authService.ListSessions(c.Request.Context(), userID, currentHash)
	if err != nil {
		response.InternalError(c, "failed to list sessions")
		return
	}

	response.OK(c, gin.H{"sessions": sessions})
}

// RevokeSession revokes a specific session by its hash.
// DELETE /sessions/:id [AUTH]
func (h *AuthHandler) RevokeSession(c *gin.Context) {
	userID := middleware.GetUserID(c)
	sessionHash := c.Param("id")
	if sessionHash == "" {
		response.BadRequest(c, "session ID is required")
		return
	}

	if err := h.authService.RevokeSession(c.Request.Context(), userID, sessionHash); err != nil {
		response.InternalError(c, "failed to revoke session")
		return
	}

	response.OK(c, gin.H{"success": true})
}

// RevokeAllSessions revokes all sessions except the current one.
// DELETE /sessions [AUTH]
func (h *AuthHandler) RevokeAllSessions(c *gin.Context) {
	userID := middleware.GetUserID(c)

	authHeader := c.GetHeader("Authorization")
	currentHash := ""
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 {
			sha := sha256.Sum256([]byte(parts[1]))
			currentHash = fmt.Sprintf("%x", sha)
		}
	}

	if err := h.authService.RevokeAllSessions(c.Request.Context(), userID, currentHash); err != nil {
		response.InternalError(c, "failed to revoke sessions")
		return
	}

	response.OK(c, gin.H{"success": true})
}

// totpSecretString wraps a TOTP secret as sql.NullString.
func totpSecretString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// emailToWalletAddr converts an email to the wallet address format used in the users table.
// Must match the format in the Register handler.
func emailToWalletAddr(email string) string {
	emailHash := sha256.Sum256([]byte(email))
	return fmt.Sprintf("EMAIL:%x", emailHash[:16])
}

// deriveWalletSeed derives a deterministic Stellar wallet seed from an email.
// Uses SHA-256(email + ":" + pepper) for deterministic, recoverable wallets.
func deriveWalletSeed(email string) string {
	pepper := os.Getenv("MOISTELLO_WALLET_PEPPER")
	if pepper == "" {
		log.Fatal("MOISTELLO_WALLET_PEPPER environment variable is not set")
	}
	seed := sha256.Sum256([]byte(email + ":" + pepper))
	return hex.EncodeToString(seed[:])
}

// getPasskeyPepper returns the passkey pepper for wallet seed derivation.
func getPasskeyPepper() string {
	p := os.Getenv("MOISTELLO_PASSKEY_PEPPER")
	if p == "" {
		log.Fatal("MOISTELLO_PASSKEY_PEPPER environment variable is not set")
	}
	return p
}

// sha256HashForLogout computes SHA-256 for refresh token session lookup.
func sessionTTLFromUser(u *user.User) time.Duration {
	ttl := u.SessionTTLMinutes
	if ttl < 60 {
		ttl = 240
	}
	return time.Duration(ttl) * time.Minute
}

func deviceInfoFromContext(c *gin.Context) string {
	ua := c.GetHeader("User-Agent")
	if ua == "" {
		ua = "unknown"
	}
	ip := c.ClientIP()
	if ip == "" {
		ip = "unknown"
	}
	return fmt.Sprintf("%s|%s", ua, ip)
}

func passwordHashStruct(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func sha256HashForLogout(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash)
}
