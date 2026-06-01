package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
)

func TestAuthHandler_Logout_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil)

	r := gin.New()
	r.POST("/v1/auth/logout", h.Logout)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.New().String(),
		"exp": float64(time.Now().Add(15 * time.Minute).Unix()),
	})
	tokenStr, _ := token.SignedString([]byte("test-secret"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestAuthHandler_Logout_NoAuthHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil)

	r := gin.New()
	r.POST("/v1/auth/logout", h.Logout)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthHandler_Logout_InvalidAuthFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil)

	r := gin.New()
	r.POST("/v1/auth/logout", h.Logout)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "InvalidFormat")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAuthHandler_Logout_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil)

	r := gin.New()
	r.POST("/v1/auth/logout", h.Logout)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestAuthHandler_Logout_WithRedisBlocklist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 0})
	defer rdb.Close()

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, rdb, nil)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uuid.New().String())
		c.Next()
	})
	r.POST("/v1/auth/logout", h.Logout)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.New().String(),
		"exp": float64(time.Now().Add(15 * time.Minute).Unix()),
	})
	tokenStr, _ := token.SignedString([]byte("test-secret"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	r.ServeHTTP(w, req)

	// Handler gracefully degrades when Redis is unreachable
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestAuthHandler_Logout_ExpiredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo)
	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil)

	r := gin.New()
	r.POST("/v1/auth/logout", h.Logout)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.New().String(),
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()),
	})
	tokenStr, _ := token.SignedString([]byte("test-secret"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	r.ServeHTTP(w, req)

	// Expired tokens are still valid for logout — ExtractTokenExpiry can parse them
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}
