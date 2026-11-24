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

	recordContributionStreak   *incentives.SavingsStreak
	recordContributionErr      error
	lastRecordContributionUser string

	grantStreakBonusIncentive *incentives.Incentive
	grantStreakBonusErr       error
	lastGrantStreakUser       string

	grantFirstDepositIncentive  *incentives.Incentive
	grantFirstDepositErr        error
	lastGrantFirstDepositUser   string
	lastGrantFirstDepositAmount float64
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
func (s *stubIncentivesService) GrantFirstDepositBonus(_ context.Context, userID string, depositAmount float64) (*incentives.Incentive, error) {
	s.lastGrantFirstDepositUser = userID
	s.lastGrantFirstDepositAmount = depositAmount
	return s.grantFirstDepositIncentive, s.grantFirstDepositErr
}
func (s *stubIncentivesService) RecordContribution(_ context.Context, userID string) (*incentives.SavingsStreak, error) {
	s.lastRecordContributionUser = userID
	return s.recordContributionStreak, s.recordContributionErr
}
func (s *stubIncentivesService) GrantStreakBonus(_ context.Context, userID string) (*incentives.Incentive, error) {
	s.lastGrantStreakUser = userID
	return s.grantStreakBonusIncentive, s.grantStreakBonusErr
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

func TestIncentivesHandler_RecordContribution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	streakID := uuid.New()
	userUUID := uuid.New()
	expectedStreak := &incentives.SavingsStreak{
		ID:            streakID,
		UserID:        userUUID,
		CurrentStreak: 5,
		LongestStreak: 10,
		BonusTier:     2,
	}

	tests := []struct {
		name         string
		authUserID   string
		streakResult *incentives.SavingsStreak
		serviceErr   error
		wantStatus   int
		wantCalled   bool
		wantErrMsg   string
	}{
		{
			name:         "happy path records contribution and returns streak",
			authUserID:   userUUID.String(),
			streakResult: expectedStreak,
			wantStatus:   http.StatusOK,
			wantCalled:   true,
		},
		{
			name:       "service error returns 500",
			authUserID: userUUID.String(),
			serviceErr: errors.New("database connection failed"),
			wantStatus: http.StatusInternalServerError,
			wantCalled: true,
			wantErrMsg: "Failed to record contribution",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{
				recordContributionStreak: tc.streakResult,
				recordContributionErr:    tc.serviceErr,
			}
			h := handler.NewIncentivesHandler(svc)

			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.authUserID != "" {
					c.Set("user_id", tc.authUserID)
				}
				c.Next()
			})
			r.POST("/incentives/contribution", h.RecordContribution)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/incentives/contribution", nil)
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCalled {
				assert.Equal(t, tc.authUserID, svc.lastRecordContributionUser)
			}
			if tc.wantStatus == http.StatusOK {
				var resp struct {
					Data incentives.SavingsStreak `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, streakID, resp.Data.ID)
				assert.Equal(t, userUUID, resp.Data.UserID)
				assert.Equal(t, 5, resp.Data.CurrentStreak)
				assert.Equal(t, 10, resp.Data.LongestStreak)
				assert.Equal(t, 2, resp.Data.BonusTier)
			} else {
				var errResp struct {
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Contains(t, errResp.Message, tc.wantErrMsg)
			}
		})
	}
}

func TestIncentivesHandler_GrantStreakBonus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	incentiveID := uuid.New()
	userUUID := uuid.New()
	expectedIncentive := &incentives.Incentive{
		ID:       incentiveID,
		UserID:   userUUID,
		Type:     incentives.IncentiveTypeSavingsStreak,
		Status:   incentives.IncentiveStatusPending,
		Amount:   25.0,
		Currency: "USDC",
	}

	tests := []struct {
		name            string
		authUserID      string
		incentiveResult *incentives.Incentive
		serviceErr      error
		wantStatus      int
		wantCalled      bool
		wantErrMsg      string
	}{
		{
			name:            "happy path grants streak bonus and returns incentive",
			authUserID:      userUUID.String(),
			incentiveResult: expectedIncentive,
			wantStatus:      http.StatusOK,
			wantCalled:      true,
		},
		{
			name:       "service error returns 400",
			authUserID: userUUID.String(),
			serviceErr: errors.New("streak bonus criteria not met"),
			wantStatus: http.StatusBadRequest,
			wantCalled: true,
			wantErrMsg: "Failed to grant streak bonus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{
				grantStreakBonusIncentive: tc.incentiveResult,
				grantStreakBonusErr:       tc.serviceErr,
			}
			h := handler.NewIncentivesHandler(svc)

			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.authUserID != "" {
					c.Set("user_id", tc.authUserID)
				}
				c.Next()
			})
			r.POST("/incentives/streak-bonus", h.GrantStreakBonus)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/incentives/streak-bonus", nil)
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCalled {
				assert.Equal(t, tc.authUserID, svc.lastGrantStreakUser)
			}
			if tc.wantStatus == http.StatusOK {
				var resp struct {
					Data incentives.Incentive `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, incentiveID, resp.Data.ID)
				assert.Equal(t, userUUID, resp.Data.UserID)
				assert.Equal(t, incentives.IncentiveTypeSavingsStreak, resp.Data.Type)
				assert.Equal(t, 25.0, resp.Data.Amount)
				assert.Equal(t, "USDC", resp.Data.Currency)
			} else {
				var errResp struct {
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Contains(t, errResp.Message, tc.wantErrMsg)
			}
		})
	}
}

func TestIncentivesHandler_GrantFirstDepositBonus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	incentiveID := uuid.New()
	userUUID := uuid.New()
	expectedIncentive := &incentives.Incentive{
		ID:       incentiveID,
		UserID:   userUUID,
		Type:     incentives.IncentiveTypeFirstDeposit,
		Status:   incentives.IncentiveStatusPending,
		Amount:   10.0,
		Currency: "USDC",
	}

	tests := []struct {
		name            string
		body            string
		incentiveResult *incentives.Incentive
		serviceErr      error
		wantStatus      int
		wantCalled      bool
		wantUserID      string
		wantDepositAmt  float64
		wantErrMsg      string
	}{
		{
			name:            "happy path grants first deposit bonus",
			body:            `{"userId":"` + userUUID.String() + `","depositAmount":150.0}`,
			incentiveResult: expectedIncentive,
			wantStatus:      http.StatusOK,
			wantCalled:      true,
			wantUserID:      userUUID.String(),
			wantDepositAmt:  150.0,
		},
		{
			name:       "invalid JSON body returns 400",
			body:       `{malformed-json`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
			wantErrMsg: "Invalid request body",
		},
		{
			name:       "missing userId returns 400",
			body:       `{"depositAmount":150.0}`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
			wantErrMsg: "Invalid request body",
		},
		{
			name:       "zero depositAmount returns 400",
			body:       `{"userId":"` + userUUID.String() + `","depositAmount":0}`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
			wantErrMsg: "Invalid request body",
		},
		{
			name:       "negative depositAmount returns 400",
			body:       `{"userId":"` + userUUID.String() + `","depositAmount":-50.0}`,
			wantStatus: http.StatusBadRequest,
			wantCalled: false,
			wantErrMsg: "Invalid request body",
		},
		{
			name:           "service error returns 400",
			body:           `{"userId":"` + userUUID.String() + `","depositAmount":100.0}`,
			serviceErr:     errors.New("user has already received first deposit bonus"),
			wantStatus:     http.StatusBadRequest,
			wantCalled:     true,
			wantUserID:     userUUID.String(),
			wantDepositAmt: 100.0,
			wantErrMsg:     "Failed to grant first deposit bonus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubIncentivesService{
				grantFirstDepositIncentive: tc.incentiveResult,
				grantFirstDepositErr:       tc.serviceErr,
			}
			h := handler.NewIncentivesHandler(svc)

			r := gin.New()
			r.POST("/incentives/first-deposit", h.GrantFirstDepositBonus)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/incentives/first-deposit", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantCalled {
				assert.Equal(t, tc.wantUserID, svc.lastGrantFirstDepositUser)
				assert.Equal(t, tc.wantDepositAmt, svc.lastGrantFirstDepositAmount)
			}
			if tc.wantStatus == http.StatusOK {
				var resp struct {
					Data incentives.Incentive `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, incentiveID, resp.Data.ID)
				assert.Equal(t, userUUID, resp.Data.UserID)
				assert.Equal(t, incentives.IncentiveTypeFirstDeposit, resp.Data.Type)
				assert.Equal(t, 10.0, resp.Data.Amount)
				assert.Equal(t, "USDC", resp.Data.Currency)
			} else {
				var errResp struct {
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Contains(t, errResp.Message, tc.wantErrMsg)
			}
		})
	}
}
