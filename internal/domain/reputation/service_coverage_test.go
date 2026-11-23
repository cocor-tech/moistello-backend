package reputation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/reputation"
	repMocks "github.com/moistello/backend/internal/domain/reputation/mocks"
)

func TestReputationService_UpdateScore_InvalidUUIDNeverTouchesRepo(t *testing.T) {
	repo := new(repMocks.Repository)
	svc := reputation.NewService(repo)

	snapshot, err := svc.UpdateScore(context.Background(), "not-a-uuid", 1, 1, 1, 1)
	assert.Error(t, err)
	assert.Nil(t, snapshot)
	repo.AssertNotCalled(t, "SaveSnapshot", mock.Anything, mock.Anything)
}

func TestReputationService_CalculateScore_ZeroActivityIsBronzeWithRecencyOnly(t *testing.T) {
	userID := uuid.New()
	svc := reputation.NewService(new(repMocks.Repository))

	snapshot, err := svc.CalculateScore(context.Background(), userID.String(), 0, 0, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, userID, snapshot.UserID)
	assert.Equal(t, 150, snapshot.Score) // only recency contributes when daysSinceLast=0
	assert.Equal(t, "Bronze", snapshot.Level)

	var breakdown reputation.ScoreBreakdown
	require.NoError(t, json.Unmarshal(snapshot.Breakdown, &breakdown))
	assert.Equal(t, reputation.ScoreBreakdown{Recency: 150}, breakdown)
}

func TestReputationService_CalculateScore_RecencyFloorsAtZero(t *testing.T) {
	svc := reputation.NewService(new(repMocks.Repository))

	snapshot, err := svc.CalculateScore(context.Background(), uuid.New().String(), 0, 0, 0, 1000)
	require.NoError(t, err)

	var breakdown reputation.ScoreBreakdown
	require.NoError(t, json.Unmarshal(snapshot.Breakdown, &breakdown))
	assert.Equal(t, 0, breakdown.Recency)
}
