package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/stellar/go/strkey"
	"golang.org/x/crypto/argon2"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	GenerateNonce(ctx context.Context, walletAddress string) (*Nonce, error)
	VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error)
	CreateSession(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error)
	ValidateSession(ctx context.Context, refreshToken string) (*uuid.UUID, error)
	GenerateJWT(userID uuid.UUID, walletAddress, role string) (string, error)
	GenerateJWTWithTTL(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error)
	ValidateJWT(tokenString string) (*JWTCustomClaims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error)
	ListSessions(ctx context.Context, userID string, currentTokenHash string) ([]SessionInfo, error)
	RevokeSession(ctx context.Context, userID, sessionHash string) error
	RevokeAllSessions(ctx context.Context, userID, currentHash string) error
}

type authService struct {
	redis           *redis.Client
	nonceTTL        time.Duration
	accessTTL       time.Duration
	refreshTTL      time.Duration
	signingKey      any
	signingMethod   jwt.SigningMethod
	verifyingKey    any
	verifyingMethod jwt.SigningMethod
}

func NewService(redisClient *redis.Client, nonceTTL, accessTTL, refreshTTL time.Duration, jwtPrivateKeyPEM, jwtPublicKeyPEM string) (Service, error) {
	privateKeyPEM := []byte(strings.TrimSpace(jwtPrivateKeyPEM))
	publicKeyPEM := []byte(strings.TrimSpace(jwtPublicKeyPEM))
	if len(privateKeyPEM) == 0 || len(publicKeyPEM) == 0 {
		return nil, fmt.Errorf("[SECURITY CRITICAL] JWT keys must be loaded into config before auth service startup")
	}

	signingKey, signingMethod, err := ParsePrivateSigningKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing JWT private key: %w", err)
	}
	verifyingKey, verifyingMethod, err := ParsePublicVerifyingKey(publicKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing JWT public key: %w", err)
	}
	if signingMethod.Alg() != verifyingMethod.Alg() {
		return nil, fmt.Errorf("JWT key pair algorithm mismatch: private=%s public=%s", signingMethod.Alg(), verifyingMethod.Alg())
	}

	return &authService{
		redis:           redisClient,
		nonceTTL:        nonceTTL,
		accessTTL:       accessTTL,
		refreshTTL:      refreshTTL,
		signingKey:      signingKey,
		signingMethod:   signingMethod,
		verifyingKey:    verifyingKey,
		verifyingMethod: verifyingMethod,
	}, nil
}

func (s *authService) GenerateNonce(ctx context.Context, walletAddress string) (*Nonce, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating random nonce: %w", err)
	}
	nonceStr := hex.EncodeToString(b)

	// Store nonce with creation timestamp for clock skew tolerance
	now := time.Now().Unix()
	storedValue := fmt.Sprintf("%s:%d", nonceStr, now)
	key := fmt.Sprintf("nonce:%s", walletAddress)

	// Add 30s clock skew tolerance to the TTL
	ttl := s.nonceTTL + 30*time.Second
	if err := s.redis.Set(ctx, key, storedValue, ttl).Err(); err != nil {
		return nil, fmt.Errorf("storing nonce in redis: %w", err)
	}

	return &Nonce{
		WalletAddress: walletAddress,
		Nonce:         nonceStr,
		ExpiresAt:     time.Now().UTC().Add(s.nonceTTL),
	}, nil
}

func (s *authService) VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error) {
	key := fmt.Sprintf("nonce:%s", walletAddress)
	stored, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return false, apperrors.ErrNonceExpired
		}
		return false, fmt.Errorf("retrieving nonce from redis: %w", err)
	}

	// Delete nonce immediately to prevent any replay
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		log.Error().Err(err).Str("wallet", walletAddress).Msg("failed to delete nonce from redis — attempting expiry fallback")
		if expireErr := s.redis.Expire(ctx, key, 1*time.Second).Err(); expireErr != nil {
			log.Error().Err(expireErr).Str("wallet", walletAddress).Msg("nonce expiry fallback also failed")
		}
	}

	// Parse nonce value and creation timestamp
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid nonce format")
	}
	nonceStr := parts[0]
	createdAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid nonce timestamp: %w", err)
	}

	// Check expiry with 30-second clock skew tolerance
	now := time.Now().Unix()
	skewTolerance := int64(30)
	if now > createdAt+int64(s.nonceTTL.Seconds())+skewTolerance {
		return false, apperrors.ErrNonceExpired
	}
	if now < createdAt-skewTolerance {
		return false, fmt.Errorf("nonce from the future — clock skew detected")
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("decoding signature hex: %w", err)
	}

	publicKey, err := decodeStellarPublicKey(walletAddress)
	if err != nil {
		return false, fmt.Errorf("decoding public key: %w", err)
	}

	message := sha256.Sum256([]byte(nonceStr))
	valid := ed25519.Verify(publicKey, message[:], sigBytes)

	return valid, nil
}

func (s *authService) CreateSession(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error) {
	if role == "" {
		role = "user"
	}
	accessToken, err := s.GenerateJWTWithTTL(userID, "", role, sessionTTL)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	refreshBytes := make([]byte, 64)
	if _, err := rand.Read(refreshBytes); err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}
	refreshToken := hex.EncodeToString(refreshBytes)
	tokenHash := sha256Hash(refreshToken)

	csrfBytes := make([]byte, 32)
	if _, err := rand.Read(csrfBytes); err != nil {
		return nil, fmt.Errorf("generating CSRF token: %w", err)
	}
	csrfToken := hex.EncodeToString(csrfBytes)

	userIDStr := userID.String()

	sessionData := fmt.Sprintf("%s|%s|%d|%s", userIDStr, deviceInfo, time.Now().Unix(), role)
	sessionKey := fmt.Sprintf("session:%s", tokenHash)
	csrfKey := fmt.Sprintf("csrf:%x", sha256.Sum256([]byte(accessToken)))
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userIDStr)

	pipe := s.redis.TxPipeline()
	pipe.Set(ctx, sessionKey, sessionData, s.refreshTTL)
	pipe.Set(ctx, csrfKey, csrfToken, sessionTTL)
	pipe.SAdd(ctx, userSessionsKey, tokenHash)
	pipe.Expire(ctx, userSessionsKey, sessionTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		// Rollback partial writes on failure to avoid leaving orphan state
		_ = s.redis.Del(ctx, sessionKey, csrfKey).Err()
		_ = s.redis.SRem(ctx, userSessionsKey, tokenHash).Err()
		return nil, fmt.Errorf("storing session and CSRF in redis: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		CSRFToken:    csrfToken,
	}, nil
}

func (s *authService) ValidateSession(ctx context.Context, refreshToken string) (*uuid.UUID, error) {
	tokenHash := sha256Hash(refreshToken)
	key := fmt.Sprintf("session:%s", tokenHash)

	userIDStr, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, apperrors.ErrTokenExpired
		}
		return nil, fmt.Errorf("retrieving session from redis: %w", err)
	}

	// Check if the user's refresh tokens have been blocklisted
	blocklistKey := fmt.Sprintf("refresh:blocklist:%s", userIDStr)
	blocklisted, err := s.redis.Exists(ctx, blocklistKey).Result()
	if err != nil {
		log.Warn().Err(err).Str("userID", userIDStr).Msg("failed to check refresh blocklist")
		return nil, fmt.Errorf("session validation error")
	}
	if blocklisted > 0 {
		// Session revoked — delete it immediately
		s.redis.Del(ctx, key)
		return nil, fmt.Errorf("session revoked")
	}

	uid, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("parsing session user ID: %w", err)
	}

	return &uid, nil
}

func (s *authService) GenerateJWT(userID uuid.UUID, walletAddress, role string) (string, error) {
	return s.signJWT(userID, walletAddress, role, s.accessTTL)
}

// GenerateJWTWithTTL generates an access token with a custom TTL.
func (s *authService) GenerateJWTWithTTL(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	return s.signJWT(userID, walletAddress, role, ttl)
}

func (s *authService) signJWT(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":    userID.String(),
		"wallet": walletAddress,
		"role":   role,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	}

	token := jwt.NewWithClaims(s.signingMethod, claims)
	signed, err := token.SignedString(s.signingKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signed, nil
}

// SessionInfo holds metadata about an active session.
type SessionInfo struct {
	ID         string `json:"id"`
	DeviceInfo string `json:"deviceInfo"`
	LastActive string `json:"lastActive"`
	IsCurrent  bool   `json:"isCurrent"`
}

// ListSessions returns all active sessions for a user.
func (s *authService) ListSessions(ctx context.Context, userID string, currentTokenHash string) ([]SessionInfo, error) {
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
	hashes, err := s.redis.SMembers(ctx, userSessionsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	var sessions []SessionInfo
	for _, hash := range hashes {
		sessionKey := fmt.Sprintf("session:%s", hash)
		data, err := s.redis.Get(ctx, sessionKey).Result()
		if err != nil {
			continue
		}
		parts := strings.SplitN(data, "|", 3)
		deviceInfo := ""
		lastActive := ""
		if len(parts) >= 2 {
			deviceInfo = parts[1]
		}
		if len(parts) >= 3 {
			ts, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				lastActive = time.Unix(ts, 0).Format(time.RFC3339)
			}
		}
		sessions = append(sessions, SessionInfo{
			ID:         hash,
			DeviceInfo: deviceInfo,
			LastActive: lastActive,
			IsCurrent:  hash == currentTokenHash,
		})
	}
	return sessions, nil
}

// RevokeSession deletes a specific session by its hash.
func (s *authService) RevokeSession(ctx context.Context, userID, sessionHash string) error {
	sessionKey := fmt.Sprintf("session:%s", sessionHash)
	s.redis.Del(ctx, sessionKey)
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
	s.redis.SRem(ctx, userSessionsKey, sessionHash)
	return nil
}

// RevokeAllSessions deletes all sessions for a user except the current one.
func (s *authService) RevokeAllSessions(ctx context.Context, userID, currentHash string) error {
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
	hashes, err := s.redis.SMembers(ctx, userSessionsKey).Result()
	if err != nil {
		return fmt.Errorf("listing sessions for revoke: %w", err)
	}

	for _, hash := range hashes {
		if hash == currentHash {
			continue
		}
		sessionKey := fmt.Sprintf("session:%s", hash)
		s.redis.Del(ctx, sessionKey)
	}
	s.redis.Del(ctx, userSessionsKey)
	// Re-add current session to the set
	if currentHash != "" {
		s.redis.SAdd(ctx, userSessionsKey, currentHash)
		s.redis.Expire(ctx, userSessionsKey, s.refreshTTL)
	}
	return nil
}

func (s *authService) ValidateJWT(tokenString string) (*JWTCustomClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != s.verifyingMethod.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.verifyingKey, nil
	}, jwt.WithValidMethods([]string{s.verifyingMethod.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("parsing JWT: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		userID, _ := claims["sub"].(string)
		wallet, _ := claims["wallet"].(string)
		role, _ := claims["role"].(string)
		return &JWTCustomClaims{
			UserID: userID,
			Wallet: wallet,
			Role:   role,
		}, nil
	}

	return nil, apperrors.ErrUnauthorized
}

func (s *authService) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	tokenHash := sha256Hash(refreshToken)
	key := fmt.Sprintf("session:%s", tokenHash)

	data, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, apperrors.ErrTokenExpired
		}
		return nil, fmt.Errorf("retrieving session from redis: %w", err)
	}

	parts := strings.Split(data, "|")
	userIDStr := parts[0]
	role := SessionRole(data)

	// Check if the user's refresh tokens have been blocklisted
	blocklistKey := fmt.Sprintf("refresh:blocklist:%s", userIDStr)
	blocklisted, err := s.redis.Exists(ctx, blocklistKey).Result()
	if err != nil {
		log.Warn().Err(err).Str("userID", userIDStr).Msg("failed to check refresh blocklist")
		return nil, fmt.Errorf("session validation error")
	}
	if blocklisted > 0 {
		s.redis.Del(ctx, key)
		return nil, fmt.Errorf("session revoked")
	}

	uid, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("parsing session user ID: %w", err)
	}

	// Create the NEW session first so that if this fails, the old one remains valid
	newPair, err := s.CreateSession(ctx, uid, role, s.accessTTL, "")
	if err != nil {
		return nil, fmt.Errorf("creating new session: %w", err)
	}

	// Grace period: keep the old session alive for 60 seconds so that
	// in-flight requests using the old refresh token can still complete.
	oldKey := fmt.Sprintf("session:%s", tokenHash)
	graceTTL := 60 * time.Second
	if err := s.redis.Expire(ctx, oldKey, graceTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("failed to set old session grace period — non-fatal")
	}

	return newPair, nil
}

// decodeStellarPublicKey decodes a Stellar G... address to an Ed25519 public
// key using the canonical StrKey implementation from the Stellar SDK (Base32 +
// version byte + CRC-16 checksum, all validated by strkey.Decode). This
// replaces the hand-rolled Base32/CRC16 code that risked diverging from the
// SDK (#167).
func decodeStellarPublicKey(address string) (ed25519.PublicKey, error) {
	raw, err := strkey.Decode(strkey.VersionByteAccountID, address)
	if err != nil {
		return nil, fmt.Errorf("decoding stellar address: %w", err)
	}
	return ed25519.PublicKey(raw), nil
}

// SessionRole extracts the role claim from a stored session data string.
// Session data is formatted as "userID|deviceInfo|timestamp|role", but
// deviceInfo itself contains '|' (userAgent|ip), so the role is always the
// LAST pipe-separated field. Values that are not a known role (e.g. the
// timestamp of a legacy session without a role field) fall back to "user".
func SessionRole(data string) string {
	parts := strings.Split(data, "|")
	if len(parts) == 0 {
		return "user"
	}
	last := parts[len(parts)-1]
	if last == "user" || last == "admin" {
		return last
	}
	return "user"
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ── Password Hashing (Argon2id) ──

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
)

// HashPassword hashes a plaintext password using Argon2id with a random salt.
// Returns the encoded hash in the format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	buf := &strings.Builder{}
	buf.WriteString("$argon2id$v=19$m=65536,t=3,p=4$")
	buf.WriteString(base64Encode(salt))
	buf.WriteByte('$')
	buf.WriteString(base64Encode(hash))
	return buf.String(), nil
}

// VerifyPassword checks a plaintext password against an Argon2id encoded hash.
func VerifyPassword(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	salt, err := base64Decode(parts[4])
	if err != nil {
		return false
	}

	expected, err := base64Decode(parts[5])
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return subtle.ConstantTimeCompare(computed, expected) == 1
}

// base64Encode encodes bytes to base64 without padding (matching argon2 standard format).
func base64Encode(data []byte) string {
	return strings.TrimRight(base64.StdEncoding.EncodeToString(data), "=")
}

// base64Decode decodes base64 without padding.
func base64Decode(s string) ([]byte, error) {
	// Add padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}
