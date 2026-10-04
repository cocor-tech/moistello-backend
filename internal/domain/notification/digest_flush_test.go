package notification_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
	notifMocks "github.com/moistello/backend/internal/domain/notification/mocks"
)

// mapDigestPrefs resolves per-user cadences from a map.
type mapDigestPrefs struct {
	prefs map[string]notification.DigestPreferences
}

func (m *mapDigestPrefs) DigestPreferences(_ context.Context, userID string) (notification.DigestPreferences, error) {
	prefs, ok := m.prefs[userID]
	if !ok {
		return notification.DigestPreferences{}, errors.New("no preferences for user")
	}
	return prefs, nil
}

func TestService_FlushDueDigests_oneDigestPerUserPerWindow(t *testing.T) {
	// Two users on different cadences: each gets exactly one summary, sized to
	// their own events, when their own window elapses.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	buffer := notification.NewDigestBuffer()

	prefsByUser := map[string]notification.DigestPreferences{}
	svc := notification.NewService(repo, nil, bc,
		notification.WithDigestBatching(buffer, &mapDigestPrefs{prefs: prefsByUser}))

	fast := uuid.New()
	slow := uuid.New()
	prefsByUser[fast.String()] = notification.DigestPreferences{Enabled: true, Interval: 15 * time.Minute}
	prefsByUser[slow.String()] = notification.DigestPreferences{Enabled: true, Interval: 24 * time.Hour}

	base := time.Now().UTC()
	for i := 0; i < 3; i++ {
		_, err := svc.Create(ctx, digestInput(fast.String(), notification.TypeContributionReceived))
		require.NoError(t, err)
	}
	_, err := svc.Create(ctx, digestInput(slow.String(), notification.TypeMemberJoined))
	require.NoError(t, err)

	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, mock.Anything, mock.Anything).Return()

	flushed, err := svc.FlushDueDigests(ctx, base.Add(16*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, flushed, "only the fast user's cadence elapsed")
	assert.Equal(t, 1, buffer.Pending(slow))
	repo.AssertNumberOfCalls(t, "Create", 1)

	// The slow user flushes on their own schedule, later.
	flushed, err = svc.FlushDueDigests(ctx, base.Add(25*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, flushed)
	assert.Equal(t, 0, buffer.Pending(slow))
	repo.AssertNumberOfCalls(t, "Create", 2)
	repo.AssertExpectations(t)
}

func TestService_FlushDueDigests_persistFailureDoesNotBlockOtherUsers(t *testing.T) {
	// One user's digest failing to persist must not prevent the other users'
	// digests from being delivered in the same pass.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	buffer := notification.NewDigestBuffer()
	// The cadence must be a supported one: Normalize() clamps anything under
	// MinDigestInterval to DefaultDigestInterval, so a 1m cadence here would
	// silently be flushed at 24h and the assertions below would be vacuous.
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: notification.MinDigestInterval}}
	svc := notification.NewService(repo, nil, bc, notification.WithDigestBatching(buffer, prefs))

	base := time.Now().UTC()
	users := make([]uuid.UUID, 3)
	for i := range users {
		users[i] = uuid.New()
		_, err := svc.Create(ctx, digestInput(users[i].String(), notification.TypeContributionReceived))
		require.NoError(t, err)
	}

	// Fail exactly one persist; the rest succeed.
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).
		Return(errors.New("write conflict")).Once()
	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, mock.Anything, mock.Anything).Return()

	flushed, err := svc.FlushDueDigests(ctx, base.Add(notification.MinDigestInterval+time.Minute))

	assert.Error(t, err, "the failing user should be reported")
	assert.Equal(t, 2, flushed, "the other two digests must still be delivered")
}

func TestService_FlushDueDigests_isNoOpWhenNotConfigured(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	flushed, err := svc.FlushDueDigests(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, flushed)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestService_FlushDueDigests_concurrentFlushesNeverDoubleDeliver(t *testing.T) {
	// A single batch must produce exactly one summary even if several flushes
	// race — otherwise the user gets the same summary repeatedly.
	ctx := context.Background()
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	buffer := notification.NewDigestBuffer()
	// The cadence must be a supported one: Normalize() clamps anything under
	// MinDigestInterval to DefaultDigestInterval, so a 1m cadence here would
	// silently be flushed at 24h and the assertions below would be vacuous.
	prefs := &fakeDigestPrefs{prefs: notification.DigestPreferences{Enabled: true, Interval: notification.MinDigestInterval}}
	svc := notification.NewService(repo, nil, bc, notification.WithDigestBatching(buffer, prefs))

	userID := uuid.New()
	base := time.Now().UTC()
	for i := 0; i < 4; i++ {
		_, err := svc.Create(ctx, digestInput(userID.String(), notification.TypeContributionReceived))
		require.NoError(t, err)
	}

	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, mock.Anything, mock.Anything).Return()

	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := svc.FlushDueDigests(ctx, base.Add(notification.MinDigestInterval+time.Minute))
			assert.NoError(t, err)
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, total, "one window must yield exactly one digest")
	repo.AssertNumberOfCalls(t, "Create", 1)
}
