package notification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
	notifMocks "github.com/moistello/backend/internal/domain/notification/mocks"
)

// ---------------------------------------------------------------------------
// Fail-open guarantees
//
// Every path below must deliver immediately. Digesting is a spam reduction
// and must never become a way to silently lose a notification.
// ---------------------------------------------------------------------------

func TestService_Create_disabledDigestDeliversImmediately(t *testing.T) {
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	buffer := notification.NewDigestBuffer()
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: false, Interval: time.Hour}}
	svc := notification.NewService(repo, nil, nil, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
	require.NoError(t, err)

	assert.Equal(t, 0, buffer.Pending(userID))
	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
}

func TestService_Create_preferenceLookupFailureDeliversImmediately(t *testing.T) {
	// If we cannot tell whether the user opted in, we must assume they did not
	// — a preferences outage must not swallow circle events.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	buffer := notification.NewDigestBuffer()
	prefs := &fakeDigestPrefs{err: errors.New("user store unavailable")}
	svc := notification.NewService(repo, nil, nil, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
	require.NoError(t, err)

	assert.Equal(t, 0, buffer.Pending(userID))
	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
}

func TestService_Create_deliversImmediatelyWhenDigestingNotConfigured(t *testing.T) {
	// With no WithDigestBatching option the service must behave exactly as it
	// did before #415: everything is persisted immediately.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
	require.NoError(t, err)

	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
}

func TestService_Create_bufferOverflowFallsBackToImmediateDelivery(t *testing.T) {
	// Past the batch cap the event is delivered rather than discarded, so a
	// pathological event rate degrades to spam instead of to silence.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	buffer := notification.NewDigestBuffer()
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: 24 * time.Hour}}
	svc := notification.NewService(repo, nil, nil, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	for i := 0; i < notification.MaxDigestBatchSize; i++ {
		_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
		require.NoError(t, err)
	}
	assert.Empty(t, repo.Calls, "events within the cap are held, not persisted")

	// The next one overflows the buffer and must go out immediately.
	_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
	require.NoError(t, err)

	assert.Equal(t, notification.MaxDigestBatchSize, buffer.Pending(userID))
	repo.AssertNumberOfCalls(t, "Create", 1)
	repo.AssertExpectations(t)
}
