package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/moistello/backend/internal/domain/auth/jwt"
	"github.com/moistello/backend/internal/domain/auth/nonce"
	"github.com/moistello/backend/internal/domain/auth/password"
	"github.com/moistello/backend/internal/domain/auth/session"
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
	nonceService   nonce.Service
	sessionService session.Service
	jwtService     jwt.Service
	accessTTL      time.Duration
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

	nonceSvc := nonce.NewService(redisClient, nonceTTL)
	jwtSvc := jwt.NewService(signingKey, signingMethod, verifyingKey, verifyingMethod.Alg())
	sessionSvc := session.NewService(redisClient, refreshTTL, jwtSvc)

	return &authService{
		nonceService:   nonceSvc,
		sessionService: sessionSvc,
		jwtService:     jwtSvc,
		accessTTL:      accessTTL,
	}, nil
}

func (s *authService) GenerateNonce(ctx context.Context, walletAddress string) (*Nonce, error) {
	n, err := s.nonceService.Generate(ctx, walletAddress)
	if err != nil {
		return nil, err
	}
	return &Nonce{
		WalletAddress: n.WalletAddress,
		Nonce:         n.Nonce,
		ExpiresAt:     n.ExpiresAt,
	}, nil
}

func (s *authService) VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error) {
	return s.nonceService.Verify(ctx, walletAddress, signature)
}

func (s *authService) CreateSession(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error) {
	pair, err := s.sessionService.Create(ctx, userID, role, sessionTTL, deviceInfo)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		CSRFToken:    pair.CSRFToken,
	}, nil
}

func (s *authService) ValidateSession(ctx context.Context, refreshToken string) (*uuid.UUID, error) {
	return s.sessionService.Validate(ctx, refreshToken)
}

func (s *authService) GenerateJWT(userID uuid.UUID, walletAddress, role string) (string, error) {
	return s.sessionService.GenerateJWT(context.Background(), userID, walletAddress, role, s.accessTTL)
}

func (s *authService) GenerateJWTWithTTL(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	return s.sessionService.GenerateJWT(context.Background(), userID, walletAddress, role, ttl)
}

func (s *authService) ValidateJWT(tokenString string) (*JWTCustomClaims, error) {
	claims, err := s.jwtService.ValidateToken(context.Background(), tokenString)
	if err != nil {
		return nil, err
	}
	return &JWTCustomClaims{
		UserID: claims.UserID,
		Wallet: claims.Wallet,
		Role:   claims.Role,
	}, nil
}

func (s *authService) RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error) {
	pair, err := s.sessionService.Refresh(ctx, refreshToken, s.accessTTL)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		CSRFToken:    pair.CSRFToken,
	}, nil
}

func (s *authService) ListSessions(ctx context.Context, userID string, currentTokenHash string) ([]SessionInfo, error) {
	sessions, err := s.sessionService.List(ctx, userID, currentTokenHash)
	if err != nil {
		return nil, err
	}
	var result []SessionInfo
	for _, sess := range sessions {
		result = append(result, SessionInfo{
			ID:         sess.ID,
			DeviceInfo: sess.DeviceInfo,
			LastActive: sess.LastActive,
			IsCurrent:  sess.IsCurrent,
		})
	}
	return result, nil
}

func (s *authService) RevokeSession(ctx context.Context, userID, sessionHash string) error {
	return s.sessionService.Revoke(ctx, userID, sessionHash)
}

func (s *authService) RevokeAllSessions(ctx context.Context, userID, currentHash string) error {
	return s.sessionService.RevokeAll(ctx, userID, currentHash)
}

// HashPassword hashes a plaintext password using Argon2id with a random salt.
func HashPassword(plaintext string) (string, error) {
	return password.HashPassword(plaintext)
}

// VerifyPassword checks a plaintext password against an Argon2id encoded hash.
func VerifyPassword(plaintext, encodedHash string) bool {
	return password.VerifyPassword(plaintext, encodedHash)
}

// SessionRole extracts the role claim from a stored session data string.
func SessionRole(data string) string {
	return session.SessionRole(data)
}
