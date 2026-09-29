package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/circle"
	circleMocks "github.com/moistello/backend/internal/domain/circle/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

func TestCircleHandler_GetSnapshot_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circleID := uuid.New()
	userID := uuid.New()

	expectedSnapshot := &circle.CircleSnapshot{
		CircleID:           circleID,
		Name:               "Snapshot Test Circle",
		Status:             circle.CircleStatusActive,
		ContributionAmount: 100.0,
		Currency:           circle.CurrencyUSDC,
		Frequency:          circle.FrequencyWeekly,
		CurrentRound:       1,
		TotalRounds:        10,
		MaxMembers:         5,
		MembersCount:       3,
		UserBalance: &circle.UserBalanceSnapshot{
			UserID:             userID,
			TotalContributed:   100.0,
			TotalPaidOut:       0.0,
			NetBalance:         100.0,
			HasReceivedPayout:  false,
			PendingPayoutRound: 0,
		},
		CircleBalance: circle.CircleBalanceSnapshot{
			TotalContributed: 300.0,
			TotalPaidOut:     0.0,
			VaultBalance:     300.0,
		},
		Rounds: []circle.RoundSnapshot{},
		AsOf:   time.Now().UTC(),
	}

	repo.On("GetCircleSnapshot", mock.Anything, circleID, userID).Return(expectedSnapshot, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	})
	r.GET("/circles/:id/snapshot", h.GetSnapshot)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+circleID.String()+"/snapshot", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Snapshot circle.CircleSnapshot `json:"snapshot"`
		} `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, circleID, resp.Data.Snapshot.CircleID)
	assert.Equal(t, "Snapshot Test Circle", resp.Data.Snapshot.Name)
	assert.NotNil(t, resp.Data.Snapshot.UserBalance)
	assert.Equal(t, userID, resp.Data.Snapshot.UserBalance.UserID)
	assert.Equal(t, 100.0, resp.Data.Snapshot.UserBalance.TotalContributed)

	repo.AssertExpectations(t)
}

func TestCircleHandler_GetSnapshot_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circleID := uuid.New()
	userID := uuid.New()

	repo.On("GetCircleSnapshot", mock.Anything, circleID, userID).Return(nil, apperrors.ErrNotFound)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	})
	r.GET("/circles/:id/snapshot", h.GetSnapshot)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+circleID.String()+"/snapshot", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	repo.AssertExpectations(t)
}

func TestCircleHandler_GetSnapshot_InvalidUUID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.GET("/circles/:id/snapshot", h.GetSnapshot)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/invalid-uuid/snapshot", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCircleHandler_GetBulkSnapshots_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})

	circleID1 := uuid.New()
	circleID2 := uuid.New()
	userID := uuid.New()

	expectedSnapshots := []circle.CircleSnapshot{
		{
			CircleID:           circleID1,
			Name:               "Circle 1",
			Status:             circle.CircleStatusActive,
			ContributionAmount: 50.0,
			CircleBalance:      circle.CircleBalanceSnapshot{TotalContributed: 150.0},
			AsOf:               time.Now().UTC(),
		},
		{
			CircleID:           circleID2,
			Name:               "Circle 2",
			Status:             circle.CircleStatusActive,
			ContributionAmount: 100.0,
			CircleBalance:      circle.CircleBalanceSnapshot{TotalContributed: 500.0},
			AsOf:               time.Now().UTC(),
		},
	}

	repo.On("GetBulkCircleSnapshots", mock.Anything, userID, []uuid.UUID{circleID1, circleID2}).Return(expectedSnapshots, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID.String())
		c.Next()
	})
	r.GET("/circles/snapshots", h.GetBulkSnapshots)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/snapshots?ids="+circleID1.String()+","+circleID2.String(), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Snapshots []circle.CircleSnapshot `json:"snapshots"`
		} `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp.Data.Snapshots, 2)
	assert.Equal(t, circleID1, resp.Data.Snapshots[0].CircleID)
	assert.Equal(t, circleID2, resp.Data.Snapshots[1].CircleID)

	repo.AssertExpectations(t)
}
