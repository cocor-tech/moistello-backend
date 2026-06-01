package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	GenerateNonce(ctx context.Context, walletAddress string) (*Nonce, error)
	VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error)
	CreateSession(ctx context.Context, userID uuid.UUID) (*TokenPair, error)
	ValidateSession(ctx context.Context, refreshToken string) (*uuid.UUID, error)
	GenerateJWT(userID uuid.UUID, walletAddress, role string) (string, error)
	ValidateJWT(tokenString string) (*JWTCustomClaims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*TokenPair, error)
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
	privateKeyPEM, err := os.ReadFile(jwtPrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading JWT private key: %w", err)
	}
	publicKeyPEM, err := os.ReadFile(jwtPublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading JWT public key: %w", err)
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

	key := fmt.Sprintf("nonce:%s", walletAddress)
	if err := s.redis.Set(ctx, key, nonceStr, s.nonceTTL).Err(); err != nil {
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
	storedNonce, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return false, apperrors.ErrNonceExpired
		}
		return false, fmt.Errorf("retrieving nonce from redis: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("decoding signature hex: %w", err)
	}

	publicKey, err := decodeStellarPublicKey(walletAddress)
	if err != nil {
		return false, fmt.Errorf("decoding public key: %w", err)
	}

	message := sha256.Sum256([]byte(storedNonce))
	valid := ed25519.Verify(publicKey, message[:], sigBytes)

	if valid {
		s.redis.Del(ctx, key)
	}

	return valid, nil
}

func (s *authService) CreateSession(ctx context.Context, userID uuid.UUID) (*TokenPair, error) {
	accessToken, err := s.GenerateJWT(userID, "", "user")
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	refreshBytes := make([]byte, 64)
	if _, err := rand.Read(refreshBytes); err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}
	refreshToken := hex.EncodeToString(refreshBytes)
	tokenHash := sha256Hash(refreshToken)

	key := fmt.Sprintf("session:%s", tokenHash)
	sessionData := userID.String()
	if err := s.redis.Set(ctx, key, sessionData, s.refreshTTL).Err(); err != nil {
		return nil, fmt.Errorf("storing session in redis: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
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

	tokenHash := sha256Hash(refreshToken)
	key := fmt.Sprintf("session:%s", tokenHash)
	s.redis.Del(ctx, key)

	return s.CreateSession(ctx, *uid)
}

func decodeStellarPublicKey(address string) (ed25519.PublicKey, error) {
	pubKeyBytes, err := hex.DecodeString(address)
	if err != nil {
		return ed25519.PublicKey(hexDecodeStellar(address)), nil
	}
	return ed25519.PublicKey(pubKeyBytes), nil
}

func hexDecodeStellar(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		key := make([]byte, 32)
		if len(s) == 56 {
			copy(key, []byte(s)[:32])
		}
		return key
	}
	return b
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
