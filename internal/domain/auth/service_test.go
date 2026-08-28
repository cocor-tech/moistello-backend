package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/auth"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestTokenPairStructure(t *testing.T) {
	tp := auth.TokenPair{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-def",
	}
	assert.NotEmpty(t, tp.AccessToken)
	assert.NotEmpty(t, tp.RefreshToken)
}

func TestNonceStructure(t *testing.T) {
	now := time.Now().UTC()
	n := auth.Nonce{
		WalletAddress: "GABC...",
		Nonce:         "abc123",
		ExpiresAt:     now.Add(5 * time.Minute),
	}
	assert.Equal(t, "GABC...", n.WalletAddress)
	assert.Equal(t, "abc123", n.Nonce)
	assert.True(t, n.ExpiresAt.After(now))
}

func TestJWTCustomClaimsStructure(t *testing.T) {
	claims := auth.JWTCustomClaims{
		UserID: uuid.New().String(),
		Wallet: "GABC...",
		Role:   "user",
	}
	assert.NotEmpty(t, claims.UserID)
	assert.Equal(t, "user", claims.Role)
}

func TestJWTCustomClaims_AdminRole(t *testing.T) {
	claims := auth.JWTCustomClaims{
		UserID: uuid.New().String(),
		Wallet: "GADMIN...",
		Role:   "admin",
	}
	assert.NotEmpty(t, claims.UserID)
	assert.Equal(t, "admin", claims.Role)
}

func TestSessionRole_ParsesRoleFromLastField(t *testing.T) {
	// Session data format: userID|deviceInfo|timestamp|role — deviceInfo itself
	// contains '|' (userAgent|ip), so the role must come from the LAST field.
	assert.Equal(t, "admin", auth.SessionRole("3f9d...|Mozilla/5.0 (Linux)|192.168.1.1|1724687762|admin"))
	assert.Equal(t, "user", auth.SessionRole("3f9d...|Mozilla/5.0 (Linux)|192.168.1.1|1724687762|user"))
}

func TestSessionRole_LegacySessionWithoutRole_DefaultsToUser(t *testing.T) {
	// Pre-role sessions only stored userID|deviceInfo|timestamp (last field is
	// the timestamp), so they must fall back to "user".
	assert.Equal(t, "user", auth.SessionRole("3f9d...|Mozilla/5.0 (Linux)|192.168.1.1|1724687762"))
	assert.Equal(t, "user", auth.SessionRole(""))
}

func TestSessionStructure(t *testing.T) {
	s := auth.Session{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		TokenHash: sha256Hex("some-token"),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	assert.NotEqual(t, uuid.Nil, s.ID)
	assert.NotEmpty(t, s.TokenHash)
}

func generateTestRSAKeys(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privDER,
	})

	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	return string(privPEM), string(pubPEM)
}

func generateTestECKeys(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	privDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privDER,
	})

	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	return string(privPEM), string(pubPEM)
}

func jwtHeaderAlg(t *testing.T, tokenString string) string {
	t.Helper()
	parsed, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	require.NoError(t, err)
	alg, _ := parsed.Header["alg"].(string)
	return alg
}

func TestGenerateJWT_RSAHeaderAlgMatchesKey(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	privPEM, pubPEM := generateTestRSAKeys(t)
	svc, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, privPEM, pubPEM)
	require.NoError(t, err)

	userID := uuid.New()
	token, err := svc.GenerateJWT(userID, "GABC...", "user")
	require.NoError(t, err)
	assert.Equal(t, "RS256", jwtHeaderAlg(t, token))

	claims, err := svc.ValidateJWT(token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.UserID)
	assert.Equal(t, "GABC...", claims.Wallet)
	assert.Equal(t, "user", claims.Role)
}

func TestGenerateJWT_ECDSAHeaderAlgMatchesKey(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	privPEM, pubPEM := generateTestECKeys(t)
	svc, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, privPEM, pubPEM)
	require.NoError(t, err)

	userID := uuid.New()
	token, err := svc.GenerateJWT(userID, "GABC...", "admin")
	require.NoError(t, err)
	assert.Equal(t, "ES256", jwtHeaderAlg(t, token))

	ttlToken, err := svc.GenerateJWTWithTTL(userID, "GABC...", "admin", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, "ES256", jwtHeaderAlg(t, ttlToken))

	claims, err := svc.ValidateJWT(token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.UserID)
	assert.Equal(t, "admin", claims.Role)
}

func TestValidateJWT_RejectsAlgConfusion(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	privPEM, pubPEM := generateTestRSAKeys(t)
	svc, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, privPEM, pubPEM)
	require.NoError(t, err)

	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    uuid.New().String(),
		"wallet": "GABC...",
		"role":   "admin",
		"iat":    time.Now().Unix(),
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	signed, err := hmacToken.SignedString([]byte(pubPEM))
	require.NoError(t, err)

	_, err = svc.ValidateJWT(signed)
	assert.Error(t, err)
}

func TestParsePrivateSigningKey_RejectsUnsupportedType(t *testing.T) {
	_, _, err := auth.ParsePrivateSigningKey([]byte("not-a-pem"))
	assert.Error(t, err)
}

func TestNewService_RejectsKeyPairMismatch(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	rsaPriv, _ := generateTestRSAKeys(t)
	_, ecPub := generateTestECKeys(t)

	_, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, rsaPriv, ecPub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "algorithm mismatch")
}

func TestCreateSession_AtomicWrites(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	privPEM, pubPEM := generateTestRSAKeys(t)
	svc, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, privPEM, pubPEM)
	require.NoError(t, err)

	userID := uuid.New()
	ctx := context.Background()

	tp, err := svc.CreateSession(ctx, userID, "user", 15*time.Minute, "mobile-app")
	require.NoError(t, err)
	require.NotNil(t, tp)
	assert.NotEmpty(t, tp.AccessToken)
	assert.NotEmpty(t, tp.RefreshToken)
	assert.NotEmpty(t, tp.CSRFToken)

	// Verify session was written
	tokenHash := sha256Hex(tp.RefreshToken)
	sessionKey := "session:" + tokenHash
	sessionVal, err := rdb.Get(ctx, sessionKey).Result()
	require.NoError(t, err)
	assert.Contains(t, sessionVal, userID.String())
	assert.Contains(t, sessionVal, "mobile-app")

	// Verify CSRF was written
	csrfHash := sha256.Sum256([]byte(tp.AccessToken))
	csrfKey := fmt.Sprintf("csrf:%x", csrfHash)
	csrfVal, err := rdb.Get(ctx, csrfKey).Result()
	require.NoError(t, err)
	assert.Equal(t, tp.CSRFToken, csrfVal)

	// Verify user session set was indexed
	userSessionsKey := "user:sessions:" + userID.String()
	isMember, err := rdb.SIsMember(ctx, userSessionsKey, tokenHash).Result()
	require.NoError(t, err)
	assert.True(t, isMember)
}

func TestCreateSession_RollbackOnFailure(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	privPEM, pubPEM := generateTestRSAKeys(t)
	svc, err := auth.NewService(rdb, 5*time.Minute, 15*time.Minute, 7*24*time.Hour, privPEM, pubPEM)
	require.NoError(t, err)

	// Close miniredis to induce pipeline execution failure
	mr.Close()
	rdb.Close()

	userID := uuid.New()
	ctx := context.Background()

	tp, err := svc.CreateSession(ctx, userID, "user", 15*time.Minute, "mobile-app")
	assert.Error(t, err)
	assert.Nil(t, tp)
	assert.Contains(t, err.Error(), "storing session and CSRF in redis")
}
