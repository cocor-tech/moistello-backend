package middleware_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/middleware"
)

// rsaTestKeys holds a generated RSA key pair for use across tests in this package.
type rsaTestKeys struct {
	privateKey   *rsa.PrivateKey
	publicKeyPEM []byte
}

// newRSATestKeys generates a 2048-bit RSA key pair and returns PEM-encoded public key.
func newRSATestKeys(t *testing.T) rsaTestKeys {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generating RSA test key pair")

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	require.NoError(t, err, "marshalling RSA public key")

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	return rsaTestKeys{
		privateKey:   priv,
		publicKeyPEM: pubPEM,
	}
}

// signRS256 creates a signed RS256 JWT string from the given claims.
func signRS256(t *testing.T, priv *rsa.PrivateKey, claims *middleware.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(priv)
	require.NoError(t, err, "signing RS256 token")
	return tokenString
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "user-123",
		Wallet: "GABC...",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tokenString := signRS256(t, keys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"userID": middleware.GetUserID(c)})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "user-123")
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tokenString := signRS256(t, keys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthMiddleware_InvalidFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "InvalidFormat")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthMiddleware_InvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Sign with one RSA key, verify with a different one — must be rejected.
	signingKeys := newRSATestKeys(t)
	verifyKeys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tokenString := signRS256(t, signingKeys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(verifyKeys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

// TestAuthMiddleware_WrongAlgorithm verifies that a token signed with HMAC (HS256)
// is rejected even when the token appears well-formed. This guards against algorithm
// confusion attacks where an attacker substitutes a symmetric secret for the public key.
func TestAuthMiddleware_WrongAlgorithm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	// Sign with HS256 — the middleware must reject this regardless of validity.
	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := hmacToken.SignedString([]byte("some-secret"))
	require.NoError(t, err)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthMiddleware_ECDSATokenUsesES256(t *testing.T) {
	gin.SetMode(gin.TestMode)

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	claims := &middleware.Claims{
		UserID: "user-ec",
		Wallet: "GABC...",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tokenString, err := token.SignedString(priv)
	require.NoError(t, err)
	assert.Equal(t, "ES256", token.Header["alg"])

	r := gin.New()
	r.Use(middleware.AuthMiddleware(pubPEM))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"userID": middleware.GetUserID(c)})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "user-ec")
}

func TestAdminMiddleware_AdminUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", "admin")
		c.Next()
	})
	r.Use(middleware.AdminMiddleware())
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
}

func TestAdminMiddleware_RegularUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", "user")
		c.Next()
	})
	r.Use(middleware.AdminMiddleware())
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 403, w.Code)
}

func TestAdminMiddleware_FullPipeline_AdminJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "admin-user-123",
		Wallet: "GADMIN...",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tokenString := signRS256(t, keys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.Use(middleware.AdminMiddleware())
	r.GET("/admin/protected", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"userID": middleware.GetUserID(c),
			"role":   middleware.GetRole(c),
		})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "admin-user-123")
	assert.Contains(t, w.Body.String(), "admin")
}

func TestAdminMiddleware_FullPipeline_UserJWT_Forbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "regular-user-456",
		Wallet: "GUSER...",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tokenString := signRS256(t, keys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.AuthMiddleware(keys.publicKeyPEM))
	r.Use(middleware.AdminMiddleware())
	r.GET("/admin/protected", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "admin access required")
}

func TestOptionalAuthMiddleware_WithValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	claims := &middleware.Claims{
		UserID: "user-123",
		Wallet: "GABC...",
		Role:   "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tokenString := signRS256(t, keys.privateKey, claims)

	r := gin.New()
	r.Use(middleware.OptionalAuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"userID": middleware.GetUserID(c)})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "user-123")
}

func TestOptionalAuthMiddleware_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	r := gin.New()
	r.Use(middleware.OptionalAuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
}

// TestOptionalAuthMiddleware_InvalidToken verifies that OptionalAuth continues (200)
// rather than aborting when the token is present but invalid.
func TestOptionalAuthMiddleware_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := newRSATestKeys(t)

	r := gin.New()
	r.Use(middleware.OptionalAuthMiddleware(keys.publicKeyPEM))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.token")
	r.ServeHTTP(w, req)

	// OptionalAuth should not abort on bad tokens — unauthenticated pass-through.
	assert.Equal(t, 200, w.Code)
}

func TestGetUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "test-id-123")
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) {
		id := middleware.GetUserID(c)
		c.JSON(200, gin.H{"id": id})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "test-id-123")
}

func TestGetWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("wallet", "GABC...")
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) {
		wallet := middleware.GetWallet(c)
		c.JSON(200, gin.H{"wallet": wallet})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "GABC...")
}

func TestGetRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", "admin")
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) {
		role := middleware.GetRole(c)
		c.JSON(200, gin.H{"role": role})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "admin")
}

func TestAdminAPIKeyMiddleware_ValidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware("test-admin-key-123"))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Admin-API-Key", "test-admin-key-123")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}

func TestAdminAPIKeyMiddleware_InvalidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware("test-admin-key-123"))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Admin-API-Key", "wrong-key")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "invalid admin API key")
}

func TestAdminAPIKeyMiddleware_MissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware("test-admin-key-123"))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "invalid admin API key")
}

func TestAdminAPIKeyMiddleware_KeyNotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.AdminAPIKeyMiddleware(""))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Admin-API-Key", "any-key")
	r.ServeHTTP(w, req)

	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "admin API key not configured")
}
