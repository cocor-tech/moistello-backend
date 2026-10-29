package circle_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/circle"
)

func TestOutboxRepository_Methods(t *testing.T) {
	mockDB, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	repo := circle.NewOutboxRepository(sqlx.NewDb(mockDB, "sqlmock"))
	ctx := context.Background()
	event := &circle.OutboxEvent{
		ID:            uuid.New(),
		AggregateType: "circle",
		AggregateID:   uuid.New(),
		EventType:     "CircleStatusChanged",
		Payload:       json.RawMessage(`{"status":"active"}`),
		Status:        circle.OutboxStatusPending,
		CreatedAt:     time.Now().UTC(),
	}

	sqlMock.ExpectExec("INSERT INTO outbox_events").
		WithArgs(event.ID, event.AggregateType, event.AggregateID, event.EventType, event.Payload, event.Status, event.RetryCount, event.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.Save(ctx, nil, event))

	sqlMock.ExpectQuery("SELECT id, aggregate_type, aggregate_id, event_type, payload, status, retry_count, created_at, processed_at").
		WithArgs(10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "aggregate_type", "aggregate_id", "event_type", "payload", "status", "retry_count", "created_at", "processed_at"}).
			AddRow(event.ID, event.AggregateType, event.AggregateID, event.EventType, []byte(event.Payload), event.Status, event.RetryCount, event.CreatedAt, nil))
	events, err := repo.FetchPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, event.ID, events[0].ID)

	sqlMock.ExpectExec("UPDATE outbox_events SET status = 'published'").
		WithArgs(event.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkPublished(ctx, event.ID))

	sqlMock.ExpectExec("UPDATE outbox_events SET status = 'failed'").
		WithArgs(event.ID, 2).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkFailed(ctx, event.ID, 2))

	cutoff := time.Now().UTC()
	sqlMock.ExpectExec("DELETE FROM outbox_events").
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 1))
	deleted, err := repo.DeletePublished(ctx, cutoff)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	assert.NoError(t, sqlMock.ExpectationsWereMet())
}

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
