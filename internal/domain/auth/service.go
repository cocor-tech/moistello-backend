package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	GenerateNonce(ctx context.Context, walletAddress string) (*Nonce, error)
	VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error)
	CreateSession(ctx context.Context, userID uuid.UUID, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error)
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
	redis         *redis.Client
	nonceTTL      time.Duration
	accessTTL     time.Duration
	refreshTTL    time.Duration
	jwtPrivateKey []byte
	jwtPublicKey  []byte
}

func NewService(redisClient *redis.Client, nonceTTL, accessTTL, refreshTTL time.Duration, jwtPrivateKeyPath, jwtPublicKeyPath string) (Service, error) {
	var privateKeyPEM, publicKeyPEM []byte
	var err error

	if privateKey := os.Getenv("JWT_PRIVATE_KEY"); privateKey != "" {
		privateKeyPEM = []byte(privateKey)
	} else {
		privateKeyPEM, err = os.ReadFile(jwtPrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading JWT private key: %w", err)
		}
	}

	if publicKey := os.Getenv("JWT_PUBLIC_KEY"); publicKey != "" {
		publicKeyPEM = []byte(publicKey)
	} else {
		publicKeyPEM, err = os.ReadFile(jwtPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading JWT public key: %w", err)
		}
	}

	if privateKeyPEM == nil || publicKeyPEM == nil {
		return nil, fmt.Errorf("[SECURITY CRITICAL] JWT keys must be provided via JWT_PRIVATE_KEY and JWT_PUBLIC_KEY environment variables or secure file paths")
	}

	return &authService{
		redis:         redisClient,
		nonceTTL:      nonceTTL,
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
		jwtPrivateKey: privateKeyPEM,
		jwtPublicKey:  publicKeyPEM,
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

func (s *authService) CreateSession(ctx context.Context, userID uuid.UUID, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error) {
	accessToken, err := s.GenerateJWTWithTTL(userID, "", "user", sessionTTL)
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

	sessionData := fmt.Sprintf("%s|%s|%d", userIDStr, deviceInfo, time.Now().Unix())
	sessionKey := fmt.Sprintf("session:%s", tokenHash)
	if err := s.redis.Set(ctx, sessionKey, sessionData, s.refreshTTL).Err(); err != nil {
		return nil, fmt.Errorf("storing session in redis: %w", err)
	}

	csrfKey := fmt.Sprintf("csrf:%x", sha256.Sum256([]byte(accessToken)))
	if err := s.redis.Set(ctx, csrfKey, csrfToken, sessionTTL).Err(); nil != err {
		return nil, fmt.Errorf("storing CSRF token in redis: %w", err)
	}

	// Index session by user for bulk operations (logout, force-invalidate)
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userIDStr)
	if err := s.redis.SAdd(ctx, userSessionsKey, tokenHash).Err(); nil != err {
		log.Warn().Err(err).Msg("failed to index user session — non-fatal")
	}
	s.redis.Expire(ctx, userSessionsKey, sessionTTL)

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
	block, _ := pem.Decode(s.jwtPrivateKey)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block for private key")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		privateKey2, err2 := x509.ParseECPrivateKey(block.Bytes)
		if err2 != nil {
			rsaKey, err3 := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err3 != nil {
				return "", fmt.Errorf("parsing private key: %w (also tried EC: %v, RSA: %v)", err, err2, err3)
			}
			privateKey = rsaKey
		} else {
			privateKey = privateKey2
		}
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":    userID.String(),
		"wallet": walletAddress,
		"role":   role,
		"iat":    now.Unix(),
		"exp":    now.Add(s.accessTTL).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signed, nil
}

// GenerateJWTWithTTL generates an access token with a custom TTL.
func (s *authService) GenerateJWTWithTTL(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	block, _ := pem.Decode(s.jwtPrivateKey)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block for private key")
	}

	var privateKey *rsa.PrivateKey
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		var ok bool
		privateKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("PKCS8 key is not RSA")
		}
	} else {
		_, err2 := x509.ParseECPrivateKey(block.Bytes)
		if err2 == nil {
			return "", fmt.Errorf("EC keys not supported for JWT signing")
		}
		rsaKey, err3 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err3 != nil {
			return "", fmt.Errorf("parsing private key: %w (also tried EC: %v, RSA: %v)", err, err2, err3)
		}
		privateKey = rsaKey
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":    userID.String(),
		"wallet": walletAddress,
		"role":   role,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
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
	block, _ := pem.Decode(s.jwtPublicKey)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block for public key")
	}

	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return publicKey, nil
	})
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
	uid, err := s.ValidateSession(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	// Create the NEW session first so that if this fails, the old one remains valid
	newPair, err := s.CreateSession(ctx, *uid, s.accessTTL, "")
	if err != nil {
		return nil, fmt.Errorf("creating new session: %w", err)
	}

	// Grace period: keep the old session alive for 60 seconds so that
	// in-flight requests using the old refresh token can still complete.
	oldTokenHash := sha256Hash(refreshToken)
	oldKey := fmt.Sprintf("session:%s", oldTokenHash)
	graceTTL := 60 * time.Second
	if err := s.redis.Expire(ctx, oldKey, graceTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("failed to set old session grace period — non-fatal")
	}

	return newPair, nil
}

// stellarBase32Alphabet is the RFC 4648 Base32 alphabet used by Stellar StrKey.
const stellarBase32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

var stellarBase32Decode [256]byte

func init() {
	for i := range stellarBase32Decode {
		stellarBase32Decode[i] = 0xFF
	}
	for i, c := range stellarBase32Alphabet {
		stellarBase32Decode[c] = byte(i)
	}
}

// decodeStellarPublicKey decodes a Stellar G... address to an Ed25519 public key.
// Stellar addresses use StrKey encoding: Base32 + 1 version byte + 2 CRC16 checksum.
func decodeStellarPublicKey(address string) (ed25519.PublicKey, error) {
	if len(address) != 56 {
		return nil, fmt.Errorf("invalid stellar address length: got %d, want 56", len(address))
	}
	if address[0] != 'G' {
		return nil, fmt.Errorf("invalid stellar address prefix: got %c, want G", address[0])
	}

	// Base32 decode: 56 chars → 35 bytes (1 version + 32 key + 2 checksum)
	decoded := make([]byte, 35)
	for i := 0; i < 56; i++ {
		c := address[i]
		val := stellarBase32Decode[c]
		if val == 0xFF {
			return nil, fmt.Errorf("invalid character %c at position %d", c, i)
		}
		bitPos := uint(i * 5)
		byteIdx := bitPos / 8
		bitOffset := bitPos % 8
		decoded[byteIdx] |= val << (3 - bitOffset)
		if bitOffset > 3 {
			decoded[byteIdx+1] |= val >> (bitOffset - 3)
		}
	}

	// Verify XDR CRC-16 checksum
	payload := decoded[:33]
	checksum := decoded[33:35]
	crc := xdrCRC16(payload)
	if checksum[0] != byte(crc>>8) || checksum[1] != byte(crc) {
		return nil, fmt.Errorf("stellar address checksum mismatch")
	}

	// Strip version byte (index 0), return 32-byte public key
	return ed25519.PublicKey(decoded[1:33]), nil
}

// xdrCRC16 computes the XDR CRC-16 used by Stellar for address checksums.
func xdrCRC16(data []byte) uint16 {
	const poly uint16 = 0x8005
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
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
