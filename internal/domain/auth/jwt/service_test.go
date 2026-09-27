package jwt_test

import (
	"context"
	"crypto/rsa"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainJWT "github.com/moistello/backend/internal/domain/auth/jwt"
)

func generateTestRSAKey(t *testing.T, kid string) *domainJWT.Key {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return &domainJWT.Key{
		ID:            kid,
		SigningKey:    privKey,
		SigningMethod: jwt.SigningMethodRS256,
		VerifyingKey:  &privKey.PublicKey,
		VerifyingAlg:  "RS256",
	}
}

func TestJWTService_KeyIDInHeader(t *testing.T) {
	key1 := generateTestRSAKey(t, "key-1")
	svc := domainJWT.NewServiceWithKeys(key1, nil)

	userID := uuid.New()
	tokenStr, err := svc.GenerateToken(context.Background(), userID, "0x123", "user", 15*time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, tokenStr)

	// Parse header unverified to assert kid
	parser := jwt.NewParser()
	parsedToken, _, err := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	require.NoError(t, err)
	assert.Equal(t, "key-1", parsedToken.Header["kid"])
}

func TestJWTService_RotateKey_BothWindowsValid(t *testing.T) {
	key1 := generateTestRSAKey(t, "key-1")
	key2 := generateTestRSAKey(t, "key-2")

	svc := domainJWT.NewServiceWithKeys(key1, nil)
	userID := uuid.New()

	// Token 1 generated under key1
	token1, err := svc.GenerateToken(context.Background(), userID, "0x111", "user", 15*time.Minute)
	require.NoError(t, err)

	claims1, err := svc.ValidateToken(context.Background(), token1)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims1.UserID)

	// Rotate key: key2 becomes current, key1 becomes previous
	svc.RotateKey(key2)

	assert.Equal(t, "key-2", svc.CurrentKey().ID)
	assert.Equal(t, "key-1", svc.PreviousKey().ID)

	// Token 2 generated under key2
	token2, err := svc.GenerateToken(context.Background(), userID, "0x222", "admin", 15*time.Minute)
	require.NoError(t, err)

	// Validate token2 (current key window)
	claims2, err := svc.ValidateToken(context.Background(), token2)
	require.NoError(t, err)
	assert.Equal(t, "admin", claims2.Role)

	// Validate token1 (previous key window)
	claims1Prev, err := svc.ValidateToken(context.Background(), token1)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims1Prev.UserID)
}

func TestJWTService_RevokedOrThirdKeyRejected(t *testing.T) {
	key1 := generateTestRSAKey(t, "key-1")
	key2 := generateTestRSAKey(t, "key-2")
	key3 := generateTestRSAKey(t, "key-3")

	svc := domainJWT.NewServiceWithKeys(key2, key1)
	userID := uuid.New()

	// Create third service to sign a token with key3
	svc3 := domainJWT.NewServiceWithKeys(key3, nil)
	token3, err := svc3.GenerateToken(context.Background(), userID, "0x333", "user", 15*time.Minute)
	require.NoError(t, err)

	// Validate token3 against svc (only knows key2 and key1)
	_, err = svc.ValidateToken(context.Background(), token3)
	assert.ErrorIs(t, err, domainJWT.ErrInvalidToken)
}

func TestJWTService_AtomicRotationConcurrent(t *testing.T) {
	key1 := generateTestRSAKey(t, "key-1")
	svc := domainJWT.NewServiceWithKeys(key1, nil)
	userID := uuid.New()

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Goroutine 1: constantly generating and validating tokens
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					tok, err := svc.GenerateToken(context.Background(), userID, "0xabc", "user", 5*time.Minute)
					if assert.NoError(t, err) {
						claims, valErr := svc.ValidateToken(context.Background(), tok)
						assert.NoError(t, valErr)
						assert.NotNil(t, claims)
					}
				}
			}
		}()
	}

	// Goroutine 2: rotating keys in background
	for i := 0; i < 10; i++ {
		newKey := generateTestRSAKey(t, uuid.New().String())
		svc.RotateKey(newKey)
		time.Sleep(1 * time.Millisecond)
	}

	close(stopCh)
	wg.Wait()
}
