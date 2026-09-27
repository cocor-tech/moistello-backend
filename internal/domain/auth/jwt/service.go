package jwt

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Key struct {
	ID            string
	SigningKey    any
	SigningMethod jwt.SigningMethod
	VerifyingKey  any
	VerifyingAlg  string
}

type Service interface {
	GenerateToken(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error)
	ValidateToken(ctx context.Context, tokenString string) (*Claims, error)
	RotateKey(newKey *Key)
	SetPreviousKey(prevKey *Key)
	CurrentKey() *Key
	PreviousKey() *Key
}

type Claims struct {
	UserID    string `json:"sub"`
	Wallet    string `json:"wallet"`
	Role      string `json:"role"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type service struct {
	mu          sync.RWMutex
	currentKey  *Key
	previousKey *Key
}

func NewService(signingKey any, signingMethod jwt.SigningMethod, verifyingKey any, verifyingAlg string) Service {
	curKey := &Key{
		ID:            "v1",
		SigningKey:    signingKey,
		SigningMethod: signingMethod,
		VerifyingKey:  verifyingKey,
		VerifyingAlg:  verifyingAlg,
	}
	return &service{
		currentKey: curKey,
	}
}

func NewServiceWithKeys(currentKey *Key, previousKey *Key) Service {
	return &service{
		currentKey:  currentKey,
		previousKey: previousKey,
	}
}

func (s *service) RotateKey(newKey *Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previousKey = s.currentKey
	s.currentKey = newKey
}

func (s *service) SetPreviousKey(prevKey *Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previousKey = prevKey
}

func (s *service) CurrentKey() *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentKey
}

func (s *service) PreviousKey() *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.previousKey
}

func (s *service) GenerateToken(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	s.mu.RLock()
	curKey := s.currentKey
	s.mu.RUnlock()

	if curKey == nil || curKey.SigningKey == nil {
		return "", fmt.Errorf("no private signing key configured")
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":    userID.String(),
		"wallet": walletAddress,
		"role":   role,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	}

	token := jwt.NewWithClaims(curKey.SigningMethod, claims)
	if curKey.ID != "" {
		token.Header["kid"] = curKey.ID
	}
	signed, err := token.SignedString(curKey.SigningKey)
	if err != nil {
		return "", err
	}
	return signed, nil
}

func (s *service) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	s.mu.RLock()
	curKey := s.currentKey
	prevKey := s.previousKey
	s.mu.RUnlock()

	if curKey == nil {
		return nil, ErrInvalidToken
	}

	tokenHeaderKID := extractKID(tokenString)

	var keysToTry []*Key
	if tokenHeaderKID != "" && prevKey != nil && tokenHeaderKID == prevKey.ID {
		keysToTry = append(keysToTry, prevKey)
		if curKey != nil {
			keysToTry = append(keysToTry, curKey)
		}
	} else {
		if curKey != nil {
			keysToTry = append(keysToTry, curKey)
		}
		if prevKey != nil {
			keysToTry = append(keysToTry, prevKey)
		}
	}

	for _, k := range keysToTry {
		claims, err := parseAndValidateWithKey(tokenString, k)
		if err == nil && claims != nil {
			return claims, nil
		}
	}

	return nil, ErrInvalidToken
}

func extractKID(tokenString string) string {
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err == nil && token != nil {
		if kid, ok := token.Header["kid"].(string); ok {
			return kid
		}
	}
	return ""
}

func parseAndValidateWithKey(tokenString string, k *Key) (*Claims, error) {
	if k == nil || k.VerifyingKey == nil {
		return nil, ErrInvalidToken
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != k.VerifyingAlg {
			return nil, ErrInvalidSigningMethod
		}
		return k.VerifyingKey, nil
	}, jwt.WithValidMethods([]string{k.VerifyingAlg}))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
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
