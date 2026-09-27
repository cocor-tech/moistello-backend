package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/admin"
	"github.com/moistello/backend/internal/domain/featureflag"
	ffmocks "github.com/moistello/backend/internal/domain/featureflag/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

type fakeMetricsRepo struct {
	metrics *admin.Metrics
}

func (f *fakeMetricsRepo) Metrics(_ context.Context, _ int) (*admin.Metrics, error) {
	return f.metrics, nil
}

func (f *fakeMetricsRepo) DailyVolume(_ context.Context, _ int) ([]admin.DailyVolumePoint, error) {
	return f.metrics.DailyVolume, nil
}

type failingMetricsRepo struct{}

func (failingMetricsRepo) Metrics(_ context.Context, _ int) (*admin.Metrics, error) {
	return nil, assert.AnError
}

func (failingMetricsRepo) DailyVolume(_ context.Context, _ int) ([]admin.DailyVolumePoint, error) {
	return nil, assert.AnError
}

// Regression guard (#396): GET /admin/metrics must answer with the standard
// error envelope when the repository fails, never panic the request.
func TestAdminHandler_GetMetrics_RepoErrorReturnsErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc, err := admin.NewService(failingMetricsRepo{}, 0)
	require.NoError(t, err)

	h := handler.NewAdminHandler(nil, nil, nil, nil, svc, nil, nil)
	r := gin.New()
	r.GET("/admin/metrics", h.GetMetrics)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/metrics", nil)
	require.NotPanics(t, func() { r.ServeHTTP(w, req) })

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.Success)
	assert.Equal(t, "INTERNAL_ERROR", body.Code)
}

func TestAdminHandler_GetMetrics_ReturnsRealAggregates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc, err := admin.NewService(&fakeMetricsRepo{metrics: &admin.Metrics{
		TotalUsers:         10,
		TotalCircles:       5,
		ActiveCircles:      2,
		TotalContributions: 40,
		TotalPayouts:       5,
		ActiveUsers:        8,
		NewUsers30d:        3,
		ContributionVolume: 100.5,
		PayoutVolume:       50.25,
		TotalVolumeUSD:     150.75,
		VolumeUSD30d:       20,
		DailyVolume: []admin.DailyVolumePoint{
			{ContributionVolume: 10, PayoutVolume: 5},
		},
	}}, 0)
	require.NoError(t, err)

	h := handler.NewAdminHandler(nil, nil, nil, nil, svc, nil, nil)
	r := gin.New()
	r.GET("/admin/metrics", h.GetMetrics)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/metrics?days=7", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			TotalUsers         int                      `json:"totalUsers"`
			ActiveCircles      int                      `json:"activeCircles"`
			TotalVolumeUSD     float64                  `json:"totalVolumeUSD"`
			ContributionVolume float64                  `json:"contributionVolume"`
			DailyVolume        []admin.DailyVolumePoint `json:"dailyVolume"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, 10, body.Data.TotalUsers)
	assert.Equal(t, 2, body.Data.ActiveCircles)
	assert.Equal(t, 150.75, body.Data.TotalVolumeUSD)
	assert.NotZero(t, body.Data.TotalVolumeUSD, "totalVolumeUSD must no longer be hardcoded to 0")
	assert.Equal(t, 100.5, body.Data.ContributionVolume)
	assert.Len(t, body.Data.DailyVolume, 1)
	assert.Equal(t, 10.0, body.Data.DailyVolume[0].ContributionVolume)
}

func setupFeatureFlagRouter(repo *ffmocks.Repository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := featureflag.NewService(repo)
	h := handler.NewAdminHandler(nil, nil, nil, nil, nil, svc, nil)
	r := gin.New()
	r.GET("/admin/feature-flags", h.ListFeatureFlags)
	r.GET("/admin/feature-flags/:flag", h.GetFeatureFlag)
	r.POST("/admin/feature-flags", h.UpdateFeatureFlag)
	r.DELETE("/admin/feature-flags/:flag", h.DeleteFeatureFlag)
	return r
}

func TestAdminHandler_ListFeatureFlags(t *testing.T) {
	repo := new(ffmocks.Repository)
	repo.On("List", mock.Anything).Return([]featureflag.FeatureFlag{
		{Flag: "kyc_required", Enabled: true},
	}, nil)
	r := setupFeatureFlagRouter(repo)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/feature-flags", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "kyc_required")
}

func TestAdminHandler_GetFeatureFlag_NotFound(t *testing.T) {
	repo := new(ffmocks.Repository)
	repo.On("Get", mock.Anything, "missing").Return(nil, apperrors.ErrNotFound)
	r := setupFeatureFlagRouter(repo)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/feature-flags/missing", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminHandler_UpdateFeatureFlag_PersistsAndReturnsFlag(t *testing.T) {
	repo := new(ffmocks.Repository)
	repo.On("Upsert", mock.Anything, "premium_circles", true, "Enable premium circle type").Return(nil)
	repo.On("Get", mock.Anything, "premium_circles").Return(&featureflag.FeatureFlag{
		Flag: "premium_circles", Enabled: true, Description: "Enable premium circle type",
	}, nil)
	r := setupFeatureFlagRouter(repo)

	body, _ := json.Marshal(map[string]any{
		"flag": "premium_circles", "value": true, "description": "Enable premium circle type",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/admin/feature-flags", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"enabled":true`)
	repo.AssertExpectations(t)
}

func TestAdminHandler_DeleteFeatureFlag(t *testing.T) {
	repo := new(ffmocks.Repository)
	repo.On("Delete", mock.Anything, "premium_circles").Return(nil)
	r := setupFeatureFlagRouter(repo)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/admin/feature-flags/premium_circles", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}
