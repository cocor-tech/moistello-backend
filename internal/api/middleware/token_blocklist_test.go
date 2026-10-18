package middleware_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/redis/go-redis/v9"
)

func TestTokenBlocklistHelpersAndMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	ctx := context.Background()

	token := "refresh-token-for-blocklist"
	middleware.BlocklistToken(ctx, redisClient, token, time.Now().Add(time.Hour))
	tokenKey := fmt.Sprintf("token:blocklist:%x", sha256.Sum256([]byte(token)))
	if !redisServer.Exists(tokenKey) {
		t.Fatal("token was not added to the blocklist")
	}

	accessRouter := gin.New()
	accessRouter.Use(middleware.TokenBlocklistMiddleware(redisClient))
	accessRouter.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	accessRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	accessRequest.Header.Set("Authorization", "Bearer "+token)
	accessResponse := httptest.NewRecorder()
	accessRouter.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusUnauthorized {
		t.Fatalf("token middleware returned %d, want %d", accessResponse.Code, http.StatusUnauthorized)
	}

	userID := "user-123"
	middleware.BlocklistUserRefreshTokens(ctx, redisClient, userID)
	if !redisServer.Exists("refresh:blocklist:" + userID) {
		t.Fatal("user refresh tokens were not added to the blocklist")
	}

	refreshRouter := gin.New()
	refreshRouter.Use(middleware.RefreshTokenBlocklistMiddleware(redisClient))
	refreshRouter.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	refreshRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"refreshToken":"`+token+`"}`))
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshResponse := httptest.NewRecorder()
	refreshRouter.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusUnauthorized {
		t.Fatalf("refresh middleware returned %d, want %d", refreshResponse.Code, http.StatusUnauthorized)
	}

	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	encodedToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": expiry.Unix()}).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	gotExpiry, err := middleware.ExtractTokenExpiry(encodedToken)
	if err != nil {
		t.Fatalf("extract token expiry: %v", err)
	}
	if !gotExpiry.Equal(expiry) {
		t.Fatalf("got expiry %v, want %v", gotExpiry, expiry)
	}
}
