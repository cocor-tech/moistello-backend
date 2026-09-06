package jwt

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Service interface {
	GenerateToken(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error)
	ValidateToken(ctx context.Context, tokenString string) (*Claims, error)
}

type Claims struct {
	UserID    string `json:"sub"`
	Wallet    string `json:"wallet"`
	Role      string `json:"role"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type service struct {
	signingKey    any
	signingMethod jwt.SigningMethod
	verifyingKey  any
	verifyingAlg  string
}

func NewService(signingKey any, signingMethod jwt.SigningMethod, verifyingKey any, verifyingAlg string) Service {
	return &service{
		signingKey:    signingKey,
		signingMethod: signingMethod,
		verifyingKey:  verifyingKey,
		verifyingAlg:  verifyingAlg,
	}
}

func (s *service) GenerateToken(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
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
		return "", err
	}
	return signed, nil
}

func (s *service) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != s.verifyingAlg {
			return nil, ErrInvalidSigningMethod
		}
		return s.verifyingKey, nil
	}, jwt.WithValidMethods([]string{s.verifyingAlg}))
	if err != nil {
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		userID, _ := claims["sub"].(string)
		wallet, _ := claims["wallet"].(string)
		role, _ := claims["role"].(string)
		iat, _ := claims["iat"].(float64)
		exp, _ := claims["exp"].(float64)
		return &Claims{
			UserID:    userID,
			Wallet:    wallet,
			Role:      role,
			IssuedAt:  int64(iat),
			ExpiresAt: int64(exp),
		}, nil
	}

	return nil, ErrInvalidToken
}

var (
	ErrInvalidSigningMethod = &jwtError{"invalid signing method"}
	ErrInvalidToken         = &jwtError{"invalid token"}
)

type jwtError struct {
	msg string
}

func (e *jwtError) Error() string {
	return e.msg
}
