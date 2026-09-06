package nonce

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stellar/go/strkey"

	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	Generate(ctx context.Context, walletAddress string) (*Nonce, error)
	Verify(ctx context.Context, walletAddress, signature string) (bool, error)
}

type Nonce struct {
	WalletAddress string
	Nonce         string
	ExpiresAt     time.Time
}

type service struct {
	redis    *redis.Client
	nonceTTL time.Duration
}

func NewService(redisClient *redis.Client, nonceTTL time.Duration) Service {
	return &service{
		redis:    redisClient,
		nonceTTL: nonceTTL,
	}
}

func (s *service) Generate(ctx context.Context, walletAddress string) (*Nonce, error) {
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

func (s *service) Verify(ctx context.Context, walletAddress, signature string) (bool, error) {
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
		// Log but don't fail - expiry fallback will handle cleanup
		if expireErr := s.redis.Expire(ctx, key, 1*time.Second).Err(); expireErr != nil {
			// Nonce will expire naturally
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

func decodeStellarPublicKey(address string) (ed25519.PublicKey, error) {
	raw, err := strkey.Decode(strkey.VersionByteAccountID, address)
	if err != nil {
		return nil, fmt.Errorf("decoding stellar address: %w", err)
	}
	return ed25519.PublicKey(raw), nil
}
