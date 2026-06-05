package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/circle"
	circleMocks "github.com/moistello/backend/internal/domain/circle/mocks"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/validator"
)

func init() {
	validator.Init()
}

func TestCircleHandler_CreateCircle_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	orgID := uuid.New()

	repo.On("Create", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil)
	repo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", orgID.String())
		c.Next()
	})
	r.POST("/circles", h.CreateCircle)

	body, _ := json.Marshal(map[string]interface{}{
		"name":               "Test Circle",
		"circleType":         "public",
		"payoutType":         "random",
		"contributionAmount": 100,
		"currency":           "USDC",
		"frequency":          "weekly",
		"maxMembers":         10,
		"maxStrikes":         3,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 201, w.Code)
	assert.Contains(t, w.Body.String(), "Test Circle")
	repo.AssertExpectations(t)
}

func TestCircleHandler_CreateCircle_InvalidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uuid.New().String())
		c.Next()
	})
	r.POST("/circles", h.CreateCircle)

	body, _ := json.Marshal(map[string]interface{}{
		"name":               "",
		"circleType":         "public",
		"payoutType":         "random",
		"contributionAmount": 100,
		"currency":           "USDC",
		"frequency":          "weekly",
		"maxMembers":         10,
		"maxStrikes":         3,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestCircleHandler_ListCircles_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)

	filter := circle.CircleFilter{Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return([]circle.Circle{}, nil)
	repo.On("Count", mock.Anything, filter).Return(0, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"total":0`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_ServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)

	filter := circle.CircleFilter{Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return(nil, apperrors.ErrInternal)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 500, w.Code)
	repo.AssertExpectations(t)
}

func TestCircleHandler_GetCircle_Exists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()

	expected := &circle.Circle{
		ID: cid, Name: "My Circle", Status: circle.CircleStatusActive,
	}
	repo.On("FindByID", mock.Anything, cid).Return(expected, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles/:id", h.GetCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+cid.String(), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "My Circle")
	repo.AssertExpectations(t)
}

func TestCircleHandler_GetCircle_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()

	repo.On("FindByID", mock.Anything, cid).Return(nil, circle.ErrCircleNotFound)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles/:id", h.GetCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+cid.String(), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code)
	repo.AssertExpectations(t)
}

func TestCircleHandler_JoinCircle_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()
	uid := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Test", Status: circle.CircleStatusActive,
		MaxMembers: 10, CircleType: circle.CircleTypePublic,
	}
	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("GetMemberCount", mock.Anything, cid).Return(3, nil)
	repo.On("FindMemberByCircleAndUser", mock.Anything, cid, uid).Return(nil, apperrors.ErrNotFound)
	repo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uid.String())
		c.Next()
	})
	r.POST("/circles/:id/join", h.JoinCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/join", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_JoinCircle_CircleFull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()
	uid := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Test", Status: circle.CircleStatusActive,
		MaxMembers: 5, CircleType: circle.CircleTypePublic,
	}
	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("GetMemberCount", mock.Anything, cid).Return(5, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uid.String())
		c.Next()
	})
	r.POST("/circles/:id/join", h.JoinCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/join", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	repo.AssertExpectations(t)
}

func TestCircleHandler_GetMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()

	c := &circle.Circle{ID: cid, Name: "Test"}
	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("GetMembers", mock.Anything, cid).Return([]circle.CircleMember{}, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles/:id/members", h.GetMembers)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+cid.String()+"/members", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "members")
	repo.AssertExpectations(t)
}

func TestCircleHandler_CancelCircle_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()
	orgID := cid

	c := &circle.Circle{
		ID: cid, Name: "Test", Status: circle.CircleStatusPending,
		OrganizerID: orgID,
	}
	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("Update", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", orgID.String())
		c.Next()
	})
	r.POST("/circles/:id/cancel", h.CancelCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/cancel", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_CancelCircle_NotOrganizer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil)
	cid := uuid.New()
	notOrg := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Test", Status: circle.CircleStatusPending,
		OrganizerID: uuid.New(),
	}
	repo.On("FindByID", mock.Anything, cid).Return(c, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", notOrg.String())
		c.Next()
	})
	r.POST("/circles/:id/cancel", h.CancelCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/cancel", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	repo.AssertExpectations(t)
}
