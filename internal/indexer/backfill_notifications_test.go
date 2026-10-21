package indexer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type backfillEventRepoStub struct {
	batches [][]Event
	err     error
	calls   int
	lastIDs []int64
	limits  []int
}

func (r *backfillEventRepoStub) GetEventsSince(_ context.Context, lastID int64, limit int) ([]Event, error) {
	r.lastIDs = append(r.lastIDs, lastID)
	r.limits = append(r.limits, limit)
	if r.err != nil {
		return nil, r.err
	}
	if r.calls >= len(r.batches) {
		r.calls++
		return nil, nil
	}
	batch := r.batches[r.calls]
	r.calls++
	return batch, nil
}

type backfillNotificationRepoStub struct {
	notifications []*Notification
	err           error
}

func (r *backfillNotificationRepoStub) Create(_ context.Context, notif *Notification) error {
	if r.err != nil {
		return r.err
	}
	r.notifications = append(r.notifications, notif)
	return nil
}

func TestBackfillNotificationsConstructor(t *testing.T) {
	job := NewBackfillNotifications(&backfillEventRepoStub{}, &backfillNotificationRepoStub{})
	assert.Equal(t, 100, job.batchSize)
	assert.NotNil(t, job.checkpoint)
	assert.WithinDuration(t, time.Now(), job.checkpoint.StartedAt, time.Second)
}

func TestBackfillNotificationsRun(t *testing.T) {
	createdAt := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	events := []Event{
		{ID: 10, Type: "circle.joined", UserID: "user-1", Title: "Joined", Body: "Welcome", Data: []byte(`{"circle":"c1"}`), CreatedAt: createdAt},
		{ID: 11, Type: "unrecognized", UserID: "user-2", CreatedAt: createdAt},
	}
	eventRepo := &backfillEventRepoStub{batches: [][]Event{events}}
	notifRepo := &backfillNotificationRepoStub{}
	job := NewBackfillNotifications(eventRepo, notifRepo)

	require.NoError(t, job.Run(context.Background()))
	require.Len(t, notifRepo.notifications, 1)
	assert.Equal(t, "user-1", notifRepo.notifications[0].UserID)
	assert.Equal(t, "circle_joined", notifRepo.notifications[0].Type)
	assert.Equal(t, createdAt, notifRepo.notifications[0].CreatedAt)
	assert.Equal(t, int64(11), job.checkpoint.LastProcessedEventID)
	assert.Equal(t, 2, job.checkpoint.ProcessedCount)
	assert.Equal(t, []int{100, 100}, eventRepo.limits)
}

func TestBackfillNotificationsRunReturnsFetchError(t *testing.T) {
	fetchErr := errors.New("event store unavailable")
	job := NewBackfillNotifications(&backfillEventRepoStub{err: fetchErr}, &backfillNotificationRepoStub{})
	err := job.Run(context.Background())
	require.ErrorIs(t, err, fetchErr)
	assert.ErrorContains(t, err, "fetching events")
}

func TestBackfillNotificationsRunHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	job := NewBackfillNotifications(&backfillEventRepoStub{}, &backfillNotificationRepoStub{})
	assert.ErrorIs(t, job.Run(ctx), context.Canceled)
}

func TestBackfillProcessEvent(t *testing.T) {
	createdAt := time.Now().UTC()
	event := Event{ID: 42, Type: "contribution.made", UserID: "user-3", Title: "Contribution", Body: "Received", Data: []byte(`{"amount":5}`), CreatedAt: createdAt}
	notifRepo := &backfillNotificationRepoStub{}
	job := NewBackfillNotifications(&backfillEventRepoStub{}, notifRepo)

	require.NoError(t, job.processEvent(context.Background(), event))
	require.Len(t, notifRepo.notifications, 1)
	notif := notifRepo.notifications[0]
	assert.True(t, strings.HasPrefix(notif.ID, "backfill-"))
	assert.Equal(t, event.UserID, notif.UserID)
	assert.Equal(t, "contribution_received", notif.Type)
	assert.Equal(t, event.Title, notif.Title)
	assert.Equal(t, event.Body, notif.Body)
	assert.Equal(t, event.Data, notif.Data)
	assert.Equal(t, "inapp", notif.Channel)
	assert.False(t, notif.Read)
	assert.Equal(t, createdAt, notif.CreatedAt)

	unknown := event
	unknown.Type = "unknown.event"
	require.NoError(t, job.processEvent(context.Background(), unknown))
	assert.Len(t, notifRepo.notifications, 1)

	createErr := errors.New("notification write failed")
	failedJob := NewBackfillNotifications(&backfillEventRepoStub{}, &backfillNotificationRepoStub{err: createErr})
	assert.ErrorIs(t, failedJob.processEvent(context.Background(), event), createErr)
}

func TestMapEventTypeToNotification(t *testing.T) {
	cases := map[string]string{
		"circle.joined":     "circle_joined",
		"circle.payout":     "payout_executed",
		"contribution.made": "contribution_received",
		"invite.received":   "invite_received",
		"unknown":           "",
	}
	for eventType, expected := range cases {
		t.Run(eventType, func(t *testing.T) {
			assert.Equal(t, expected, mapEventTypeToNotification(eventType))
		})
	}
}

func TestGenerateNotificationID(t *testing.T) {
	assert.True(t, strings.HasPrefix(generateNotificationID(), "backfill-"))
}
