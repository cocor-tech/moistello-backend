package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/incentives"
)

type stubIncentivesService struct {
	updateConfigErr error
	updatedConfig   *incentives.IncentiveConfig
	matchResult     float64
	matchErr        error
	lastMatchUser   string
	lastMatchAmount float64
}

func (s *stubIncentivesService) GenerateReferralCode(context.Context, string) (string, error) {
	return "", nil
}
func (s *stubIncentivesService) ApplyReferralCode(context.Context, string, string) error {
	return nil
}
func (s *stubIncentivesService) GetReferrals(context.Context, string) ([]incentives.Referral, error) {
	return nil, nil
}
func (s *stubIncentivesService) GrantCircleCompletionReward(context.Context, string, string) (*incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) CalculateContributionMatch(_ context.Context, userID string, amount float64) (float64, error) {
	s.lastMatchUser = userID
	s.lastMatchAmount = amount
	return s.matchResult, s.matchErr
}
func (s *stubIncentivesService) GrantContributionMatch(context.Context, string, string, float64) (*incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) GrantFirstDepositBonus(context.Context, string, float64) (*incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) RecordContribution(context.Context, string) (*incentives.SavingsStreak, error) {
	return nil, nil
}
func (s *stubIncentivesService) GrantStreakBonus(context.Context, string) (*incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) ClaimIncentive(context.Context, string, string) error {
	return nil
}
func (s *stubIncentivesService) GetUserIncentives(context.Context, string) ([]incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) GetPendingIncentives(context.Context, string) ([]incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) GetUserSummary(context.Context, string) (*incentives.UserIncentiveSummary, error) {
	return nil, nil
}
func (s *stubIncentivesService) GetIncentives(context.Context, string) ([]incentives.Incentive, error) {
	return nil, nil
}
func (s *stubIncentivesService) GetStreak(context.Context, string) (*incentives.SavingsStreak, error) {
	return nil, nil
}
func (s *stubIncentivesService) GetConfig(context.Context) (*incentives.IncentiveConfig, error) {
	return nil, nil
}
func (s *stubIncentivesService) UpdateConfig(_ context.Context, cfg *incentives.IncentiveConfig) error {
	s.updatedConfig = cfg
	return s.updateConfigErr
}

func TestIncentivesHandler_UpdateConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "happy path updates config",
			body:       `{}`,
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "invalid JSON returns 400",
			body:       `{bad-json`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
		},
		{
			name:       "service error returns 500",
			body:       `{}`,
			serviceErr: errors.New("db unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{updateConfigErr: tc.serviceErr}
			h := handler.NewIncentivesHandler(svc)

			r := gin.New()
			r.PUT("/admin/incentives/config", h.UpdateConfig)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/admin/incentives/config", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCalled {
				assert.NotNil(t, svc.updatedConfig)
			} else {
				assert.Nil(t, svc.updatedConfig)
			}
		})
	}
}

func TestIncentivesHandler_CalculateContributionMatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       string
		matchVal   float64
		serviceErr error
		wantStatus int
		wantMatch  float64
		wantCalled bool
	}{
		{
			name:       "happy path returns match amount",
			body:       `{"userId":"user-123","amount":250.5}`,
			matchVal:   25.05,
			wantStatus: http.StatusOK,
			wantMatch:  25.05,
			wantCalled: true,
		},
		{
			name:       "missing or non-positive amount returns 400",
			body:       `{"userId":"user-123","amount":0}`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
		},
		{
			name:       "service error returns 500",
			body:       `{"userId":"user-123","amount":100}`,
			serviceErr: errors.New("calc failed"),
			wantStatus: http.StatusInternalServerError,
			wantCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{
				matchResult: tc.matchVal,
				matchErr:    tc.serviceErr,
			}
			h := handler.NewIncentivesHandler(svc)

			r := gin.New()
			r.POST("/incentives/calculate-match", h.CalculateContributionMatch)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/incentives/calculate-match", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCalled {
				assert.Equal(t, "user-123", svc.lastMatchUser)
			}
			if tc.wantStatus == http.StatusOK {
				var resp struct {
					Data struct {
						MatchAmount float64 `json:"matchAmount"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.InDelta(t, tc.wantMatch, resp.Data.MatchAmount, 1e-9)
			}
		})
	}
}
