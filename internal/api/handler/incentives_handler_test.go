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
	"github.com/google/uuid"
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

	lastUserID        string
	lastClaimID       string
	claimErr          error
	userIncentives    []incentives.Incentive
	userIncentivesErr error
	pendingIncentives []incentives.Incentive
	pendingErr        error
	summary           *incentives.UserIncentiveSummary
	summaryErr        error
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
func (s *stubIncentivesService) ClaimIncentive(_ context.Context, userID, incentiveID string) error {
	s.lastUserID, s.lastClaimID = userID, incentiveID
	return s.claimErr
}
func (s *stubIncentivesService) GetUserIncentives(_ context.Context, userID string) ([]incentives.Incentive, error) {
	s.lastUserID = userID
	return s.userIncentives, s.userIncentivesErr
}
func (s *stubIncentivesService) GetPendingIncentives(_ context.Context, userID string) ([]incentives.Incentive, error) {
	s.lastUserID = userID
	return s.pendingIncentives, s.pendingErr
}
func (s *stubIncentivesService) GetUserSummary(_ context.Context, userID string) (*incentives.UserIncentiveSummary, error) {
	s.lastUserID = userID
	return s.summary, s.summaryErr
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

const incentivesTestUserID = "user-123"

// incentivesEnvelope is the part of response.Envelope these tests assert on.
type incentivesEnvelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// serveIncentivesAsUser runs one request through h with user_id set, the context key IncentivesHandler reads.
func serveIncentivesAsUser(h gin.HandlerFunc, method, route, target string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Handle(method, route, func(c *gin.Context) { c.Set("user_id", incentivesTestUserID) }, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func decodeIncentivesEnvelope[T any](t *testing.T, w *httptest.ResponseRecorder) incentivesEnvelope[T] {
	t.Helper()
	var resp incentivesEnvelope[T]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body: %s", w.Body.String())
	return resp
}

func TestIncentivesHandler_ClaimIncentive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	validID := uuid.NewString()

	tests := []struct {
		name        string
		incentiveID string
		serviceErr  error
		wantStatus  int
		wantMessage string
		wantCalled  bool
	}{
		{
			name:        "happy path claims incentive for caller",
			incentiveID: validID,
			wantStatus:  http.StatusOK,
			wantMessage: "Incentive claimed successfully",
			wantCalled:  true,
		},
		{
			name:        "non-UUID incentive ID returns 400 without calling service",
			incentiveID: "not-a-uuid",
			wantStatus:  http.StatusBadRequest,
			wantMessage: "Invalid incentive ID",
			wantCalled:  false,
		},
		{
			name:        "service error returns 400",
			incentiveID: validID,
			serviceErr:  errors.New("incentive already claimed"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "Failed to claim incentive",
			wantCalled:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{claimErr: tc.serviceErr}
			h := handler.NewIncentivesHandler(svc)

			w := serveIncentivesAsUser(h.ClaimIncentive, http.MethodPost, "/incentives/:id/claim", "/incentives/"+tc.incentiveID+"/claim")

			require.Equal(t, tc.wantStatus, w.Code)
			resp := decodeIncentivesEnvelope[map[string]string](t, w)
			if tc.wantStatus == http.StatusOK {
				assert.True(t, resp.Success)
				assert.Equal(t, tc.wantMessage, resp.Data["message"])
			} else {
				assert.False(t, resp.Success)
				assert.Equal(t, tc.wantMessage, resp.Message)
			}
			if tc.wantCalled {
				assert.Equal(t, incentivesTestUserID, svc.lastUserID)
				assert.Equal(t, tc.incentiveID, svc.lastClaimID)
			} else {
				assert.Empty(t, svc.lastClaimID)
			}
		})
	}
}

func TestIncentivesHandler_GetUserIncentives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pending := incentives.Incentive{ID: uuid.New(), Status: incentives.IncentiveStatusPending, Amount: 5}
	claimed := incentives.Incentive{ID: uuid.New(), Status: incentives.IncentiveStatusClaimed, Amount: 10}

	tests := []struct {
		name        string
		query       string
		serviceErr  error
		wantStatus  int
		wantMessage string
		want        []incentives.Incentive
	}{
		{
			name:       "happy path returns all incentives",
			wantStatus: http.StatusOK,
			want:       []incentives.Incentive{pending, claimed},
		},
		{
			name:       "status filter keeps only matching incentives",
			query:      "?status=claimed",
			wantStatus: http.StatusOK,
			want:       []incentives.Incentive{claimed},
		},
		{
			name:        "service error returns 500",
			serviceErr:  errors.New("db unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Failed to get incentives",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{
				userIncentives:    []incentives.Incentive{pending, claimed},
				userIncentivesErr: tc.serviceErr,
			}
			h := handler.NewIncentivesHandler(svc)

			w := serveIncentivesAsUser(h.GetUserIncentives, http.MethodGet, "/incentives", "/incentives"+tc.query)

			require.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, incentivesTestUserID, svc.lastUserID)
			resp := decodeIncentivesEnvelope[[]incentives.Incentive](t, w)
			assert.Equal(t, tc.wantStatus == http.StatusOK, resp.Success)
			assert.Equal(t, tc.want, resp.Data)
			assert.Equal(t, tc.wantMessage, resp.Message)
		})
	}
}

func TestIncentivesHandler_GetPendingIncentives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pending := []incentives.Incentive{{ID: uuid.New(), Status: incentives.IncentiveStatusPending, Amount: 5}}

	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantMessage string
		want        []incentives.Incentive
	}{
		{
			name:       "happy path returns pending incentives",
			wantStatus: http.StatusOK,
			want:       pending,
		},
		{
			name:        "service error returns 500",
			serviceErr:  errors.New("db unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Failed to get pending incentives",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{pendingIncentives: pending, pendingErr: tc.serviceErr}
			h := handler.NewIncentivesHandler(svc)

			w := serveIncentivesAsUser(h.GetPendingIncentives, http.MethodGet, "/incentives/pending", "/incentives/pending")

			require.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, incentivesTestUserID, svc.lastUserID)
			resp := decodeIncentivesEnvelope[[]incentives.Incentive](t, w)
			assert.Equal(t, tc.wantStatus == http.StatusOK, resp.Success)
			assert.Equal(t, tc.want, resp.Data)
			assert.Equal(t, tc.wantMessage, resp.Message)
		})
	}
}

func TestIncentivesHandler_GetUserSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	summary := &incentives.UserIncentiveSummary{
		TotalEarned:   120,
		TotalClaimed:  100,
		PendingAmount: 20,
		ReferralCount: 3,
		CurrentStreak: 4,
		LongestStreak: 6,
		BonusTier:     2,
	}

	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantMessage string
		want        *incentives.UserIncentiveSummary
	}{
		{
			name:       "happy path returns summary",
			wantStatus: http.StatusOK,
			want:       summary,
		},
		{
			name:        "service error returns 500",
			serviceErr:  errors.New("db unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Failed to get user summary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{summary: summary, summaryErr: tc.serviceErr}
			h := handler.NewIncentivesHandler(svc)

			w := serveIncentivesAsUser(h.GetUserSummary, http.MethodGet, "/incentives/summary", "/incentives/summary")

			require.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, incentivesTestUserID, svc.lastUserID)
			resp := decodeIncentivesEnvelope[*incentives.UserIncentiveSummary](t, w)
			assert.Equal(t, tc.wantStatus == http.StatusOK, resp.Success)
			assert.Equal(t, tc.want, resp.Data)
			assert.Equal(t, tc.wantMessage, resp.Message)
		})
	}
}
