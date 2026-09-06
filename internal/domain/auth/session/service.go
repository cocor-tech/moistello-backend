package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type Service interface {
	Create(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error)
	Validate(ctx context.Context, refreshToken string) (*uuid.UUID, error)
	Refresh(ctx context.Context, refreshToken string, accessTTL time.Duration) (*TokenPair, error)
	List(ctx context.Context, userID string, currentTokenHash string) ([]SessionInfo, error)
	Revoke(ctx context.Context, userID, sessionHash string) error
	RevokeAll(ctx context.Context, userID, currentHash string) error
	GenerateJWT(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error)
}

type TokenPair struct {
	AccessToken  string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	CSRFToken    string `json:"csrfToken,omitempty"`
}

type SessionInfo struct {
	ID         string `json:"id"`
	DeviceInfo string `json:"deviceInfo"`
	LastActive string `json:"lastActive"`
	IsCurrent  bool   `json:"isCurrent"`
}

type service struct {
	redis      *redis.Client
	refreshTTL time.Duration
	jwtSvc     JWTService
}

type JWTService interface {
	GenerateToken(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error)
}

func NewService(redisClient *redis.Client, refreshTTL time.Duration, jwtSvc JWTService) Service {
	return &service{
		redis:      redisClient,
		refreshTTL: refreshTTL,
		jwtSvc:     jwtSvc,
	}
}

func (s *service) Create(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*TokenPair, error) {
	if role == "" {
		role = "user"
	}
	accessToken, err := s.jwtSvc.GenerateToken(ctx, userID, "", role, sessionTTL)
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

func (s *service) Validate(ctx context.Context, refreshToken string) (*uuid.UUID, error) {
	tokenHash := sha256Hash(refreshToken)
	key := fmt.Sprintf("session:%s", tokenHash)

	userIDStr, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrTokenExpired
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

func (s *service) Refresh(ctx context.Context, refreshToken string, accessTTL time.Duration) (*TokenPair, error) {
	tokenHash := sha256Hash(refreshToken)
	key := fmt.Sprintf("session:%s", tokenHash)

	data, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrTokenExpired
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
	newPair, err := s.Create(ctx, uid, role, accessTTL, "")
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

func (s *service) List(ctx context.Context, userID string, currentTokenHash string) ([]SessionInfo, error) {
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

func (s *service) Revoke(ctx context.Context, userID, sessionHash string) error {
	sessionKey := fmt.Sprintf("session:%s", sessionHash)
	s.redis.Del(ctx, sessionKey)
	userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
	s.redis.SRem(ctx, userSessionsKey, sessionHash)
	return nil
}

func (s *service) RevokeAll(ctx context.Context, userID, currentHash string) error {
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

func (s *service) GenerateJWT(ctx context.Context, userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	return s.jwtSvc.GenerateToken(ctx, userID, walletAddress, role, ttl)
}

// SessionRole extracts the role claim from a stored session data string.
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

var ErrTokenExpired = &sessionError{"token expired"}

type sessionError struct {
	msg string
}

func (e *sessionError) Error() string {
	return e.msg
}
