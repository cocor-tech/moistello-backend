package notification_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
)

// event builds a batchable (non-urgent) circle event for the buffer/assembly
// helpers.
func event(t notification.NotificationType, queuedAt time.Time) notification.DigestEntry {
	return notification.DigestEntry{
		Notification: notification.Notification{Type: t, Title: string(t), Channel: notification.ChannelInApp},
		QueuedAt:     queuedAt,
	}
}

// ---------------------------------------------------------------------------
// Urgency classification (#415)
// ---------------------------------------------------------------------------

func TestIsUrgent_AlwaysUrgentTypesBypassDigest(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// These are the events a user must never learn about late. None of them
	// carry a deadline, so only the type can make them urgent.
	for _, typ := range []notification.NotificationType{
		notification.TypePayoutReceived,
		notification.TypeCircleCompleted,
		notification.TypeDisputeRaised,
	} {
		assert.True(t, notification.IsUrgentType(typ), "%s should be an urgent class", typ)
		assert.True(t, notification.IsUrgent(typ, nil, now), "%s should bypass digest batching", typ)
	}
}

func TestIsUrgent_DeadlineWindowDecidesUrgency(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		deadline   time.Time
		wantUrgent bool
	}{
		{"six hours out", now.Add(6 * time.Hour), true},
		{"exactly at the 24h boundary", now.Add(notification.UrgentDeadlineWindow), true},
		{"just inside the boundary", now.Add(notification.UrgentDeadlineWindow - time.Minute), true},
		{"just outside the boundary", now.Add(notification.UrgentDeadlineWindow + time.Minute), false},
		{"a week out", now.Add(7 * 24 * time.Hour), false},
		// An already-lapsed deadline still demands attention right now.
		{"already past", now.Add(-time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"deadline": tt.deadline})
			require.NoError(t, err)

			got := notification.IsUrgent(notification.TypeContributionDue, data, now)
			assert.Equal(t, tt.wantUrgent, got)
		})
	}
}

func TestIsUrgent_MalformedPayloadDoesNotForceImmediateDelivery(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// A broken payload must not be able to escalate a batchable event into an
	// immediate one — only the class or a well-formed deadline can do that.
	assert.False(t, notification.IsUrgent(notification.TypeContributionReceived, json.RawMessage(`{not json`), now))
	assert.False(t, notification.IsUrgent(notification.TypeContributionReceived, json.RawMessage(`{"deadline":"soon"}`), now))
	// A payload with no deadline field is likewise not urgent on its own.
	assert.False(t, notification.IsUrgent(notification.TypeContributionReceived, json.RawMessage(`{"circleId":"abc"}`), now))
	assert.False(t, notification.IsUrgent(notification.TypeContributionReceived, nil, now))
}

// ---------------------------------------------------------------------------
// Digest assembly
// ---------------------------------------------------------------------------

func TestAssembleDigest_CollapsesManyEventsIntoOneSummary(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()

	entries := []notification.DigestEntry{
		event(notification.TypeContributionReceived, now),
		event(notification.TypeContributionReceived, now.Add(time.Minute)),
		event(notification.TypeContributionReceived, now.Add(2*time.Minute)),
		event(notification.TypeMemberJoined, now.Add(3*time.Minute)),
	}

	digest := notification.AssembleDigest(userID, entries)

	require.NotNil(t, digest)
	assert.Equal(t, userID, digest.UserID)
	assert.Equal(t, notification.TypeDigest, digest.Type)
	assert.Equal(t, notification.ChannelInApp, digest.Channel)
	assert.Contains(t, digest.Title, "4")

	// Counts are grouped by class and ordered most-frequent-first. This is the
	// core of #415: four events become one notification.
	assert.Equal(t, "3 contribution received, 1 member joined", digest.Body)

	// The summary's window starts at the oldest event, not the newest.
	var payload struct {
		Total       int            `json:"total"`
		WindowStart time.Time      `json:"windowStart"`
		ByType      map[string]int `json:"byType"`
	}
	require.NoError(t, json.Unmarshal(digest.Data, &payload))
	assert.Equal(t, 4, payload.Total)
	assert.Equal(t, now, payload.WindowStart.UTC())
	assert.Equal(t, 3, payload.ByType[string(notification.TypeContributionReceived)])
	assert.Equal(t, 1, payload.ByType[string(notification.TypeMemberJoined)])
}

func TestAssembleDigest_EmptyBatchProducesNothing(t *testing.T) {
	assert.Nil(t, notification.AssembleDigest(uuid.New(), nil))
	assert.Nil(t, notification.AssembleDigest(uuid.New(), []notification.DigestEntry{}))
}

func TestAssembleDigest_SingleEventStillSummarised(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	digest := notification.AssembleDigest(uuid.New(), []notification.DigestEntry{
		event(notification.TypeContributionReceived, now),
	})

	require.NotNil(t, digest)
	assert.Equal(t, "1 contribution received", digest.Body)
	assert.Contains(t, digest.Title, "1 circle events")
}

func TestAssembleDigest_UnknownTypeFallsBackToReadableLabel(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	digest := notification.AssembleDigest(uuid.New(), []notification.DigestEntry{
		event(notification.NotificationType("circle.round.advanced"), now),
	})

	require.NotNil(t, digest)
	// Dots become spaces so an unmapped type still reads as a phrase.
	assert.Equal(t, "1 circle round advanced", digest.Body)
}

func TestIsUrgent_BatchableTypesAloneAreNotUrgent(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	for _, typ := range []notification.NotificationType{
		notification.TypeContributionReceived,
		notification.TypeContributionDue,
		notification.TypeMemberJoined,
		notification.TypeMemberExited,
		notification.TypeCircleCreated,
	} {
		assert.False(t, notification.IsUrgentType(typ), "%s should be batchable", typ)
		assert.False(t, notification.IsUrgent(typ, nil, now), "%s should be batched", typ)
	}
}
