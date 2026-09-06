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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})

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

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCircleHandler_ListCircles_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

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
	svc := circle.NewService(repo, nil, circle.Dependencies{})

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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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
	svc := circle.NewService(repo, nil, circle.Dependencies{})
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

func TestCircleHandler_ListCircles_WithSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circles := []circle.Circle{
		{ID: uuid.New(), Name: "Savings Circle", Status: circle.CircleStatusActive},
		{ID: uuid.New(), Name: "Savings Group", Status: circle.CircleStatusActive},
	}
	filter := circle.CircleFilter{Search: "savings", Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(2, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?search=savings", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "Savings Circle")
	assert.Contains(t, w.Body.String(), "Savings Group")
	assert.Contains(t, w.Body.String(), `"total":2`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_WithStatusFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circles := []circle.Circle{
		{ID: uuid.New(), Name: "Active Circle", Status: circle.CircleStatusActive},
	}
	filter := circle.CircleFilter{Status: circle.CircleStatusActive, Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(1, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?status=active", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "Active Circle")
	assert.Contains(t, w.Body.String(), `"total":1`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_WithTypeFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circles := []circle.Circle{
		{ID: uuid.New(), Name: "Public Circle", Status: circle.CircleStatusPending, CircleType: circle.CircleTypePublic},
	}
	filter := circle.CircleFilter{Type: circle.CircleTypePublic, Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(1, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?type=public", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "Public Circle")
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_WithPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circles := []circle.Circle{
		{ID: uuid.New(), Name: "Circle 3"},
		{ID: uuid.New(), Name: "Circle 4"},
	}
	filter := circle.CircleFilter{Page: 2, Limit: 2}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(10, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?page=2&page_size=2", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "Circle 3")
	assert.Contains(t, w.Body.String(), "Circle 4")
	assert.Contains(t, w.Body.String(), `"total":10`)
	assert.Contains(t, w.Body.String(), `"page":2`)
	assert.Contains(t, w.Body.String(), `"totalPages":5`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_DefaultPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

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
	assert.Contains(t, w.Body.String(), `"page":1`)
	assert.Contains(t, w.Body.String(), `"total":0`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_CombinedFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circles := []circle.Circle{
		{ID: uuid.New(), Name: "My Savings Circle", Status: circle.CircleStatusActive, CircleType: circle.CircleTypePublic},
	}
	orgID := uuid.New()
	filter := circle.CircleFilter{
		Search:      "savings",
		Status:      circle.CircleStatusActive,
		Type:        circle.CircleTypePublic,
		OrganizerID: &orgID,
		Page:        1,
		Limit:       10,
	}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(1, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?search=savings&status=active&type=public&organizerId="+orgID.String()+"&limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "My Savings Circle")
	assert.Contains(t, w.Body.String(), `"total":1`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_WithCommunityFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	communityID := uuid.New()
	circles := []circle.Circle{
		{ID: uuid.New(), Name: "Community Circle", CommunityID: &communityID},
	}
	filter := circle.CircleFilter{CommunityID: &communityID, Page: 1, Limit: 20}
	repo.On("List", mock.Anything, filter).Return(circles, nil)
	repo.On("Count", mock.Anything, filter).Return(1, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?communityId="+communityID.String(), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "Community Circle")
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_PageSizeExceedsMax(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	filter := circle.CircleFilter{Page: 1, Limit: 100}
	repo.On("List", mock.Anything, filter).Return([]circle.Circle{}, nil)
	repo.On("Count", mock.Anything, filter).Return(0, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles", h.ListCircles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles?page_size=500", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"limit":100`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_ListCircles_NilSliceReturned(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

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
	assert.Contains(t, w.Body.String(), "[]")
	repo.AssertExpectations(t)
}

func TestCircleHandler_Dispute_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	cid := uuid.New()
	uid := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Test Circle", Status: circle.CircleStatusActive, OrganizerID: uuid.New(),
	}
	member := &circle.CircleMember{CircleID: cid, UserID: uid, Status: circle.MemberStatusActive}

	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("FindMemberByCircleAndUser", mock.Anything, cid, uid).Return(member, nil)
	repo.On("CreateDispute", mock.Anything, mock.AnythingOfType("*circle.CircleDispute")).Return(nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uid.String())
		c.Next()
	})
	r.POST("/circles/:id/dispute", h.Dispute)

	body, _ := json.Marshal(map[string]interface{}{
		"reason":  "Payment issue",
		"details": "Details here",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/dispute", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 201, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), "Payment issue")
	repo.AssertExpectations(t)
}

func TestCircleHandler_Vote_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	cid := uuid.New()
	voterID := uuid.New()
	recipientID := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Vote Circle", Status: circle.CircleStatusActive, PayoutType: circle.PayoutTypeVote, CurrentRound: 1, MaxMembers: 3,
	}
	voter := &circle.CircleMember{CircleID: cid, UserID: voterID, Status: circle.MemberStatusActive}
	recipient := &circle.CircleMember{CircleID: cid, UserID: recipientID, Status: circle.MemberStatusActive}

	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("FindMemberByCircleAndUser", mock.Anything, cid, voterID).Return(voter, nil)
	repo.On("FindMemberByCircleAndUser", mock.Anything, cid, recipientID).Return(recipient, nil)
	repo.On("CreateVote", mock.Anything, mock.AnythingOfType("*circle.CircleVote")).Return(nil)
	repo.On("GetVotesByRound", mock.Anything, cid, 1).Return([]circle.CircleVote{{RecipientID: recipientID}}, nil)
	repo.On("GetMemberCount", mock.Anything, cid).Return(3, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", voterID.String())
		c.Next()
	})
	r.POST("/circles/:id/vote", h.Vote)

	body, _ := json.Marshal(map[string]interface{}{
		"recipientId": recipientID.String(),
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/vote", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"allVoted":false`)
	repo.AssertExpectations(t)
}

func TestCircleHandler_AuctionBid_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	cid := uuid.New()
	bidderID := uuid.New()

	c := &circle.Circle{
		ID: cid, Name: "Auction Circle", Status: circle.CircleStatusActive, PayoutType: circle.PayoutTypeAuction, CurrentRound: 1,
	}
	bidder := &circle.CircleMember{CircleID: cid, UserID: bidderID, Status: circle.MemberStatusActive}

	repo.On("FindByID", mock.Anything, cid).Return(c, nil)
	repo.On("FindMemberByCircleAndUser", mock.Anything, cid, bidderID).Return(bidder, nil)
	repo.On("CreateAuctionBid", mock.Anything, mock.AnythingOfType("*circle.CircleAuctionBid")).Return(nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", bidderID.String())
		c.Next()
	})
	r.POST("/circles/:id/auction-bid", h.AuctionBid)

	body, _ := json.Marshal(map[string]interface{}{
		"bidAmount": 50.0,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles/"+cid.String()+"/auction-bid", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 201, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"bidAmount":50`)
	repo.AssertExpectations(t)
}
