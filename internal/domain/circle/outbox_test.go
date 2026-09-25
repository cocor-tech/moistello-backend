package circle_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/circle"
)

type mockOutboxRepo struct {
	mock.Mock
}

func (m *mockOutboxRepo) Save(ctx context.Context, tx *sqlx.Tx, event *circle.OutboxEvent) error {
	return m.Called(ctx, tx, event).Error(0)
}

func (m *mockOutboxRepo) FetchPending(ctx context.Context, limit int) ([]circle.OutboxEvent, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.OutboxEvent), args.Error(1)
}

func (m *mockOutboxRepo) MarkPublished(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockOutboxRepo) MarkFailed(ctx context.Context, id uuid.UUID, retryCount int) error {
	return m.Called(ctx, id, retryCount).Error(0)
}

func (m *mockOutboxRepo) DeletePublished(ctx context.Context, before time.Time) (int64, error) {
	args := m.Called(ctx, before)
	return args.Get(0).(int64), args.Error(1)
}

type mockBroadcaster struct {
	mock.Mock
}

func (b *mockBroadcaster) CircleCreated(ctx context.Context, circleID, organizerID string) {
	b.Called(ctx, circleID, organizerID)
}

func (b *mockBroadcaster) CircleStatusChanged(ctx context.Context, circleID, status string) {
	b.Called(ctx, circleID, status)
}

func (b *mockBroadcaster) MemberJoined(ctx context.Context, circleID, userID string) {
	b.Called(ctx, circleID, userID)
}

func (b *mockBroadcaster) MemberLeft(ctx context.Context, circleID, userID string) {
	b.Called(ctx, circleID, userID)
}

func (b *mockBroadcaster) ContributionRecorded(ctx context.Context, circleID, userID string, roundNumber int, amount float64) {
	b.Called(ctx, circleID, userID, roundNumber, amount)
}

func (b *mockBroadcaster) MemberPenalized(ctx context.Context, circleID, userID string, roundNumber int, penaltyAmount float64) {
	b.Called(ctx, circleID, userID, roundNumber, penaltyAmount)
}

func TestOutboxRelay_ProcessBatch_NoEventLossOnCrash(t *testing.T) {
	repo := new(mockOutboxRepo)
	broadcaster := new(mockBroadcaster)
	relay := circle.NewOutboxRelay(repo, broadcaster)

	circleID := uuid.New().String()
	payload, _ := json.Marshal(map[string]string{
		"circleId": circleID,
		"status":   "active",
	})

	eventID := uuid.New()
	events := []circle.OutboxEvent{
		{
			ID:            eventID,
			AggregateType: "circle",
			AggregateID:   uuid.MustParse(circleID),
			EventType:     "CircleStatusChanged",
			Payload:       payload,
			Status:        circle.OutboxStatusPending,
			CreatedAt:     time.Now().UTC(),
		},
	}

	ctx := context.Background()
	repo.On("FetchPending", ctx, 10).Return(events, nil)
	broadcaster.On("CircleStatusChanged", ctx, circleID, "active").Return()
	repo.On("MarkPublished", ctx, eventID).Return(nil)

	count, err := relay.ProcessBatch(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	repo.AssertExpectations(t)
	broadcaster.AssertExpectations(t)
}
