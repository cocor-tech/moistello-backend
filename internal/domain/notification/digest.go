package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Digest batching (#415).
//
// Every circle event used to fan out to every member the moment it happened.
// In a large circle that is dozens of near-identical pushes per round, so users
// mute everything — and then miss the one alert that actually mattered (their
// payout landed). Digest batching fixes that by holding non-urgent circle
// events in a per-user buffer and collapsing them into a single periodic
// summary, while urgent classes always go out immediately.
//
// Urgent classes are:
//   - payout.received, circle.completed, dispute.raised — events a user must
//     never learn about late;
//   - any event carrying a deadline less than UrgentDeadlineWindow away.
//
// Everything else (contribution received/ due/ late, round advanced via
// member joined/exited, circle created) is batchable.

// UrgentDeadlineWindow is how close a deadline must be for its notification to
// be treated as urgent rather than batched.
const UrgentDeadlineWindow = 24 * time.Hour

// MaxDigestBatchSize bounds a single user's pending buffer. A user who
// generates more events than this within one cadence window still gets the
// overflow delivered immediately rather than silently dropped — batching is a
// spam reduction, never a delivery guarantee we are willing to break.
const MaxDigestBatchSize = 100

// DefaultDigestInterval is the cadence used when a user enables digesting
// without naming an interval, and when a stored interval is unusable.
const DefaultDigestInterval = 24 * time.Hour

// MinDigestInterval / MaxDigestInterval bound what a user may configure, so a
// typo cannot produce a cadence that effectively never fires (or one that
// flushes on every event and defeats the purpose entirely).
const (
	MinDigestInterval = 15 * time.Minute
	MaxDigestInterval = 7 * 24 * time.Hour
)

// TypeDigest is the synthetic notification type of an assembled summary. It is
// never produced by domain code directly — only by AssembleDigest.
const TypeDigest NotificationType = "digest.summary"

// urgentTypes are the notification classes that always bypass batching,
// regardless of how quiet the user's cadence is.
var urgentTypes = map[NotificationType]struct{}{
	TypePayoutReceived: {},
	TypeCircleCompleted: {},
	TypeDisputeRaised:  {},
}

// digestTypeLabels give each batchable event class a human-readable phrase for
// the assembled summary body. Types absent from this map fall back to a label
// derived from the type string itself.
var digestTypeLabels = map[NotificationType]string{
	TypeContributionReceived: "contribution received",
	TypeContributionDue:      "contribution due",
	TypeContributionLate:     "contribution late",
	TypeCircleCreated:        "circle created",
	TypeMemberJoined:         "member joined",
	TypeMemberExited:         "member exited",
}

// deadlinePayload is the shape CreateInput.Data is expected to have when the
// event carries a deadline. Only deadline is read; any other fields (circleId,
// amount, ...) are carried by the event and irrelevant to urgency.
type deadlinePayload struct {
	Deadline *time.Time `json:"deadline"`
}

// IsUrgentType reports whether t is an always-immediate notification class.
func IsUrgentType(t NotificationType) bool {
	_, ok := urgentTypes[t]
	return ok
}

// IsUrgent reports whether a notification of type t carrying data must bypass
// digest batching. It is true for the always-urgent classes and for any event
// whose deadline falls within UrgentDeadlineWindow of now.
//
// A missing, unparseable or absent deadline is not urgent on its own merits:
// the notification's class decides. This keeps a malformed payload from being
// able to force immediate delivery of an otherwise batchable event.
func IsUrgent(t NotificationType, data json.RawMessage, now time.Time) bool {
	if IsUrgentType(t) {
		return true
	}
	deadline, ok := deadlineFrom(data)
	if !ok {
		return false
	}
	// A deadline already in the past still counts: the user has something to
	// act on right now (a late contribution, a lapsed window).
	return deadline.Sub(now) <= UrgentDeadlineWindow
}

// deadlineFrom extracts a deadline from an event payload, reporting false when
// there is none we can act on.
func deadlineFrom(data json.RawMessage) (time.Time, bool) {
	if len(data) == 0 {
		return time.Time{}, false
	}
	var payload deadlinePayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.Deadline == nil {
		return time.Time{}, false
	}
	return *payload.Deadline, true
}

// DigestPreferences is a user's digest cadence (#415).
type DigestPreferences struct {
	// Enabled turns batching on for this user. When false every notification
	// is delivered immediately, exactly as before #415.
	Enabled bool
	// Interval is the cadence at which buffered events are summarised.
	Interval time.Duration
}

// Normalize clamps a stored/configured cadence into the supported range,
// substituting DefaultDigestInterval for a nonsensical value.
func (p DigestPreferences) Normalize() DigestPreferences {
	if p.Interval < MinDigestInterval || p.Interval > MaxDigestInterval {
		p.Interval = DefaultDigestInterval
	}
	return p
}

// DigestPreferenceLookup resolves a user's digest settings. Implemented by an
// adapter over user.Repository in cmd/api-server/main.go, following the same
// adapter pattern as notification's UserLookup — this package does not import
// the user domain.
type DigestPreferenceLookup interface {
	DigestPreferences(ctx context.Context, userID string) (DigestPreferences, error)
}

// DigestEntry is one batched event awaiting summarisation.
type DigestEntry struct {
	Notification Notification
	QueuedAt     time.Time
}

// digestBucket is one user's pending events, the time their window opened
// (what the cadence is measured from), and that user's cadence.
type digestBucket struct {
	entries     []DigestEntry
	windowStart time.Time
	interval    time.Duration
}

// DigestBuffer holds per-user pending events until their cadence elapses.
// It is safe for concurrent use: circle events arrive from many request
// goroutines while the flusher drains on its own tick.
type DigestBuffer struct {
	mu      sync.Mutex
	buckets map[uuid.UUID]*digestBucket
	now     func() time.Time
}

// NewDigestBuffer returns an empty buffer.
func NewDigestBuffer() *DigestBuffer {
	return &DigestBuffer{
		buckets: make(map[uuid.UUID]*digestBucket),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Enqueue buffers e for userID under the given cadence, reporting whether it
// was accepted. It returns false when the user's buffer is already at
// MaxDigestBatchSize, in which case the caller must deliver the event
// immediately instead of dropping it.
func (b *DigestBuffer) Enqueue(userID uuid.UUID, e DigestEntry, interval time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	bucket, ok := b.buckets[userID]
	if !ok {
		bucket = &digestBucket{windowStart: now, interval: interval}
		b.buckets[userID] = bucket
	}
	if len(bucket.entries) >= MaxDigestBatchSize {
		return false
	}
	bucket.entries = append(bucket.entries, e)
	return true
}

// TakeDue atomically removes and returns every user's bucket whose own cadence
// has elapsed relative to now. Draining under the same lock that Enqueue takes
// is what guarantees an event is either summarised in exactly one digest or
// never enqueued at all — never both, never lost between the two.
func (b *DigestBuffer) TakeDue(now time.Time) map[uuid.UUID][]DigestEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	due := make(map[uuid.UUID][]DigestEntry)
	for userID, bucket := range b.buckets {
		if now.Sub(bucket.windowStart) < bucket.interval {
			continue
		}
		due[userID] = bucket.entries
		delete(b.buckets, userID)
	}
	return due
}

// Pending reports how many events are currently buffered for userID.
func (b *DigestBuffer) Pending(userID uuid.UUID) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket, ok := b.buckets[userID]
	if !ok {
		return 0
	}
	return len(bucket.entries)
}

// digestSummaryData is the Data payload of an assembled digest.
type digestSummaryData struct {
	Total       int                      `json:"total"`
	WindowStart time.Time                `json:"windowStart"`
	ByType      map[NotificationType]int `json:"byType"`
}

// AssembleDigest collapses a user's batched events into a single summary
// notification, or returns nil when there is nothing to summarise.
//
// The body groups events by class and counts them ("3 contribution received,
// 1 member joined") rather than replaying each one, which is the whole point:
// N events become 1 notification. Classes are ordered by descending count and
// then by label so the output is stable and easy to scan.
func AssembleDigest(userID uuid.UUID, entries []DigestEntry) *Notification {
	if len(entries) == 0 {
		return nil
	}

	counts := make(map[NotificationType]int, len(entries))
	oldest := entries[0].QueuedAt
	for _, e := range entries {
		counts[e.Notification.Type]++
		if e.QueuedAt.Before(oldest) {
			oldest = e.QueuedAt
		}
	}

	types := make([]NotificationType, 0, len(counts))
	for t := range counts {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool {
		if counts[types[i]] != counts[types[j]] {
			return counts[types[i]] > counts[types[j]]
		}
		return digestLabel(types[i]) < digestLabel(types[j])
	})

	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, fmt.Sprintf("%d %s", counts[t], digestLabel(t)))
	}

	// The payload is a map of string-typed keys and ints; marshalling it cannot
	// fail in practice, and a degraded payload is better than dropping a digest.
	payload, err := json.Marshal(digestSummaryData{
		Total:       len(entries),
		WindowStart: oldest.UTC(),
		ByType:      counts,
	})
	if err != nil {
		payload = json.RawMessage(`{}`)
	}

	return &Notification{
		ID:      uuid.New(),
		UserID:  userID,
		Type:    TypeDigest,
		Title:   fmt.Sprintf("Your update: %d circle events", len(entries)),
		Body:    strings.Join(parts, ", "),
		Data:    payload,
		IsRead:  false,
		Channel: ChannelInApp,
	}
}

// digestLabel returns the human-readable phrase for a notification class.
func digestLabel(t NotificationType) string {
	if label, ok := digestTypeLabels[t]; ok {
		return label
	}
	// "contribution.received" -> "contribution received"
	return strings.ReplaceAll(string(t), ".", " ")
}

// DigestFlusher periodically drains due digests (#415).
//
// The tick is deliberately much shorter than any supported user cadence
// (MinDigestInterval is 15m, the default flush tick is 1m): each user's own
// cadence decides when their digest is actually due, and this just gives the
// system a heartbeat to notice. A short tick with per-user gating costs one
// cheap map walk per minute and means a cadence change takes effect promptly
// instead of waiting out a long previous interval.
type DigestFlusher struct {
	svc      Service
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// DefaultDigestFlushInterval is how often the flusher checks for due digests.
const DefaultDigestFlushInterval = time.Minute

// NewDigestFlusher constructs a flusher over svc. A non-positive interval
// falls back to DefaultDigestFlushInterval.
func NewDigestFlusher(svc Service, interval time.Duration) *DigestFlusher {
	if interval <= 0 {
		interval = DefaultDigestFlushInterval
	}
	return &DigestFlusher{
		svc:      svc,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the flush loop in the background. It returns immediately.
func (f *DigestFlusher) Start(ctx context.Context) {
	go f.run(ctx)
}

// Stop ends the flush loop and waits for the in-flight flush to finish. It is
// safe to call more than once.
func (f *DigestFlusher) Stop() {
	f.stopOnce.Do(func() { close(f.stopCh) })
	select {
	case <-f.doneCh:
	case <-time.After(f.interval):
		log.Warn().Msg("notification digest flusher did not stop within one interval")
	}
}

func (f *DigestFlusher) run(ctx context.Context) {
	defer close(f.doneCh)
	log.Info().Dur("interval", f.interval).Msg("notification digest flusher started")

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Use a detached context: a flush that is mid-persist when the
			// server starts draining should still complete, and must not be
			// cancelled into a half-written digest.
			flushed, err := f.svc.FlushDueDigests(context.WithoutCancel(ctx), time.Now().UTC())
			if err != nil {
				log.Error().Err(err).Msg("notification digest flush failed")
				continue
			}
			if flushed > 0 {
				log.Info().Int("digests", flushed).Msg("notification digests delivered")
			}
		case <-ctx.Done():
			return
		case <-f.stopCh:
			return
		}
	}
}
