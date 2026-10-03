package notification_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
	notifMocks "github.com/moistello/backend/internal/domain/notification/mocks"
)

// fakeDigestPrefs is a DigestPreferenceLookup returning fixed preferences (or
// an error) for every user ID.
type fakeDigestPrefs struct {
	prefs notification.DigestPreferences
	err   error
}

func (f *fakeDigestPrefs) DigestPreferences(_ context.Context, _ string) (notification.DigestPreferences, error) {
	return f.prefs, f.err
}

// digestInput builds a CreateInput for a batchable circle event.
func digestInput(userID string, typ notification.NotificationType) notification.CreateInput {
	return notification.CreateInput{
		UserID:  userID,
		Type:    typ,
		Title:   string(typ),
		Body:    "something happened in your circle",
		Channel: notification.ChannelInApp,
	}
}

// ---------------------------------------------------------------------------
// Urgent bypass (#415)
// ---------------------------------------------------------------------------

func TestService_Create_urgentClassesBypassDigestEntirely(t *testing.T) {
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	buffer := notification.NewDigestBuffer()
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: time.Hour}}

	svc := notification.NewService(repo, nil, bc,
		notification.WithDigestBatching(buffer, prefs),
	)

	userID := uuid.New()
	// repo.Create and the broadcast are both expected: an urgent event must be
	// delivered now, not held for the next digest.
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, mock.Anything, mock.Anything).Return()

	urgent := []notification.NotificationType{
		notification.TypePayoutReceived,
		notification.TypeCircleCompleted,
		notification.TypeDisputeRaised,
	}
	for _, typ := range urgent {
		result, err := svc.Create(ctx, digestInput(userID.String(), typ))
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, typ, result.Type)
	}

	// Nothing was held back: every urgent event escaped the buffer.
	assert.Equal(t, 0, buffer.Pending(userID))
	repo.AssertNumberOfCalls(t, "Create", len(urgent))
	repo.AssertExpectations(t)
	bc.AssertExpectations(t)
}

func TestService_Create_deadlineInsideWindowBypassesDigest(t *testing.T) {
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	buffer := notification.NewDigestBuffer()
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: 24 * time.Hour}}
	svc := notification.NewService(repo, nil, nil, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	// A contribution due in 6h is urgent even though the class is batchable.
	data, err := json.Marshal(map[string]any{"deadline": time.Now().UTC().Add(6 * time.Hour)})
	require.NoError(t, err)

	input := digestInput(userID.String(), notification.TypeContributionDue)
	input.Data = data

	_, err = svc.Create(ctx, input)
	require.NoError(t, err)

	assert.Equal(t, 0, buffer.Pending(userID), "a deadline inside 24h must not be batched")
	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
}

func TestService_Create_nonUrgentEventsAreHeldAndSummarisedOnFlush(t *testing.T) {
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	buffer := notification.NewDigestBuffer()
	interval := 15 * time.Minute
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: interval}}
	svc := notification.NewService(repo, nil, bc, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	base := time.Now().UTC()

	// Five routine events. None should be persisted individually.
	for i := 0; i < 5; i++ {
		_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
		require.NoError(t, err)
	}
	assert.Equal(t, 5, buffer.Pending(userID))
	repo.AssertNumberOfCalls(t, "Create", 0)

	// Not due yet: flushing delivers nothing.
	flushed, err := svc.FlushDueDigests(ctx, base.Add(interval-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 0, flushed)
	assert.Equal(t, 5, buffer.Pending(userID))

	// Due: exactly one summary is persisted and broadcast.
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, mock.Anything, mock.Anything).Return()

	flushed, err = svc.FlushDueDigests(ctx, base.Add(interval+time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1, flushed)
	assert.Equal(t, 0, buffer.Pending(userID))

	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
	bc.AssertNumberOfCalls(t, "NotificationCreated", 1)
}
