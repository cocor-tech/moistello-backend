package nonce

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stellar/go/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

func TestNonceReplayAttackMatrix(t *testing.T) {
	redisClient := setupRedis()
	defer redisClient.Close()

	svc := NewService(redisClient, 5*time.Minute)
	ctx := context.Background()

	// Generate a test Stellar keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Encode to Stellar address format
	walletAddress, err := strkey.Encode(strkey.VersionByteAccountID, publicKey[:])
	require.NoError(t, err)

	t.Run("single-use enforcement: nonce deleted after successful verification", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// First verification should succeed
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)

		// Second verification with same nonce should fail (replay)
		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("replay attack after successful use rejected", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Verify successfully (deletes nonce)
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		require.NoError(t, err)
		require.True(t, valid)

		// Wait a moment then attempt replay
		time.Sleep(100 * time.Millisecond)
		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("concurrent double-submit of same signature", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Simulate concurrent double-submit (both calls in quick succession)
		results := make(chan struct {
			valid bool
			err   error
		}, 2)

		go func() {
			valid, err := svc.Verify(ctx, walletAddress, sigHex)
			results <- struct {
				valid bool
				err   error
			}{valid, err}
		}()

		go func() {
			valid, err := svc.Verify(ctx, walletAddress, sigHex)
			results <- struct {
				valid bool
				err   error
			}{valid, err}
		}()

		// Collect results
		r1 := <-results
		r2 := <-results

		// One should succeed, one should fail
		successCount := 0
		if r1.err == nil && r1.valid {
			successCount++
		}
		if r2.err == nil && r2.valid {
			successCount++
		}

		// Either exactly one or zero succeed (depending on Redis ordering)
		// Both failing is also acceptable (if deletion happens before both reads)
		assert.True(t, successCount <= 1, "At most one concurrent request should succeed")
	})

	t.Run("post-restart reuse of nonce fails", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Verify successfully
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		require.NoError(t, err)
		require.True(t, valid)

		// Simulate service restart by creating new service instance
		// (in practice, nonce is deleted from Redis so this is just validation)
		svc2 := NewService(redisClient, 5*time.Minute)

		// Attempt to use same signature after "restart"
		valid, err = svc2.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("invalid signature is rejected without deleting nonce", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Create invalid signature
		invalidSig := hex.EncodeToString(make([]byte, 64))

		// Verify with invalid signature should fail
		valid, err := svc.Verify(ctx, walletAddress, invalidSig)
		assert.NoError(t, err)
		assert.False(t, valid)

		// Nonce should still exist - try with valid signature
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("nonce expiry prevents reuse", func(t *testing.T) {
		// Create service with very short TTL
		shortTTLSvc := NewService(redisClient, 100*time.Millisecond)

		// Generate nonce
		nonce, err := shortTTLSvc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Wait for nonce to expire
		time.Sleep(150 * time.Millisecond)

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Verification should fail due to expiry
		valid, err := shortTTLSvc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})
}

func TestNonceBasicFlow(t *testing.T) {
	redisClient := setupRedis()
	defer redisClient.Close()

	svc := NewService(redisClient, 5*time.Minute)
	ctx := context.Background()

	// Generate a test Stellar keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	walletAddress, err := strkey.Encode(strkey.VersionByteAccountID, publicKey[:])
	require.NoError(t, err)

	t.Run("generate and verify valid nonce", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)
		assert.NotEmpty(t, nonce.Nonce)
		assert.Equal(t, walletAddress, nonce.WalletAddress)
		assert.False(t, nonce.ExpiresAt.IsZero())

		// Sign the nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Verify signature
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("reject invalid signature", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Create wrong signature (sign wrong data)
		wrongMessage := sha256.Sum256([]byte("wrong"))
		wrongSig := ed25519.Sign(privateKey, wrongMessage[:])
		wrongSigHex := hex.EncodeToString(wrongSig)

		// Verify wrong signature
		valid, err := svc.Verify(ctx, walletAddress, wrongSigHex)
		assert.NoError(t, err)
		assert.False(t, valid)
	})

	t.Run("reject expired nonce", func(t *testing.T) {
		// Create service with very short TTL
		shortTTLSvc := NewService(redisClient, 50*time.Millisecond)

		// Generate nonce
		nonce, err := shortTTLSvc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Wait for expiry
		time.Sleep(100 * time.Millisecond)

		// Sign the expired nonce
		message := sha256.Sum256([]byte(nonce.Nonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		// Verify expired nonce
		valid, err := shortTTLSvc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("reject nonexistent nonce", func(t *testing.T) {
		// Try to verify a nonce that was never generated
		fakeNonce := "0000000000000000000000000000000000000000000000000000000000000000"
		message := sha256.Sum256([]byte(fakeNonce))
		signature := ed25519.Sign(privateKey, message[:])
		sigHex := hex.EncodeToString(signature)

		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})
}

func setupRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
}
