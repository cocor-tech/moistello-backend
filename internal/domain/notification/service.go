package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// EventsExchange is the topic exchange notification events are published to.
const EventsExchange = "moistello.events"

// Publisher abstracts the RabbitMQ producer so the service can be tested
// with a mock. *rabbitmq.Client satisfies it.
type Publisher interface {
	Publish(exchange, routingKey string, body []byte) error
}

type Service interface {
	Create(ctx context.Context, input CreateInput) (*Notification, error)
	List(ctx context.Context, userID string, page, limit int, unreadOnly bool) ([]Notification, int, error)
	Search(ctx context.Context, userID string, filter SearchFilter, page, limit int) ([]Notification, int, error)
	MarkRead(ctx context.Context, id, userID string) error
	MarkAllRead(ctx context.Context, userID string) error
	BulkArchive(ctx context.Context, userID string, ids []string, archived bool) ([]string, error)
	// FlushDueDigests delivers one summary per user whose digest cadence has
	// elapsed (#415). It returns the number of digests delivered. Safe to call
	// on a ticker; a no-op when digest batching is not configured.
	FlushDueDigests(ctx context.Context, now time.Time) (int, error)
}

type CreateInput struct {
	UserID  string              `json:"userId" validate:"required"`
	Type    NotificationType    `json:"type" validate:"required"`
	Title   string              `json:"title" validate:"required"`
	Body    string              `json:"body" validate:"required"`
	Data    json.RawMessage     `json:"data"`
	Channel NotificationChannel `json:"channel" validate:"required,oneof=inapp email sms push"`
}

// Broadcaster defines the interface for real-time notification delivery.
type Broadcaster interface {
	NotificationCreated(ctx context.Context, userID, notificationID string)
}

type notificationService struct {
	repo         Repository
	rabbitClient Publisher
	broadcaster  Broadcaster

	// Delivery channels (#191). userLookup and deliveryAudit are both
	// @Optional in spirit: NewService still works with them nil (matching
	// how this service already ran with rabbitClient/broadcaster nil), it
	// just means no email/SMS/push is ever attempted — the same
	// "in-app-only" behaviour this service already had before #191.
	userLookup    UserLookup
	deliveryAudit DeliveryAuditRepository
	channels      map[NotificationChannel]DeliveryChannel

	// Digest batching (#415). digestBuffer and digestPrefs are both optional:
	// with them nil Create behaves exactly as it did before #415 — every
	// notification is delivered immediately.
	digestBuffer *DigestBuffer
	digestPrefs  DigestPreferenceLookup
}

type ServiceOption func(*notificationService)

// WithDeliveryChannels enables email/SMS/push delivery (#191). userLookup
// resolves a recipient's contact details and channel preferences;
// deliveryAudit records the outcome of every attempt; chans are keyed by
// the channel they each handle (see EmailChannel/SMSChannel/PushChannel).
// Without this option the service behaves exactly as it did before #191:
// in-app only.
func WithDeliveryChannels(userLookup UserLookup, deliveryAudit DeliveryAuditRepository, chans ...DeliveryChannel) ServiceOption {
	return func(s *notificationService) {
		s.userLookup = userLookup
		s.deliveryAudit = deliveryAudit
		s.channels = make(map[NotificationChannel]DeliveryChannel, len(chans))
		for _, ch := range chans {
			s.channels[ch.Channel()] = ch
		}
	}
}

// WithDigestBatching enables digest batching for high-frequency circle events
// (#415). Non-urgent events are held in buffer and collapsed into a periodic
// per-user summary; urgent classes (payout, dispute, completed circle, or any
// deadline under UrgentDeadlineWindow) still go out immediately.
//
// prefs resolves each user's cadence. When it is nil, or when it reports a
// user as having digesting disabled, that user's notifications are delivered
// immediately exactly as they were before #415 — so opting out is always safe
// regardless of how the rest of the system is wired.
func WithDigestBatching(buffer *DigestBuffer, prefs DigestPreferenceLookup) ServiceOption {
	return func(s *notificationService) {
		s.digestBuffer = buffer
		s.digestPrefs = prefs
	}
}

func NewService(repo Repository, rabbitClient Publisher, broadcaster Broadcaster, opts ...ServiceOption) Service {
	s := &notificationService{repo: repo, rabbitClient: rabbitClient, broadcaster: broadcaster}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}

func (s *notificationService) Create(ctx context.Context, input CreateInput) (*Notification, error) {
	userID, err := parseUUID(input.UserID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	n := &Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      input.Type,
		Title:     input.Title,
		Body:      input.Body,
		Data:      input.Data,
		IsRead:    false,
		Channel:   input.Channel,
		CreatedAt: now,
	}

	// Digest batching (#415). This runs before persistence on purpose: a
	// batched event must not be written to notifications, because that table
	// is what the in-app list reads — writing it and then also summarising it
	// would show the user every event *and* the digest, defeating the batching.
	//
	// shouldDigest is deliberately fail-open: any error resolving the user's
	// cadence, or a full buffer, falls through to immediate delivery. A
	// preference lookup outage must never silently swallow a notification.
	if s.shouldDigest(ctx, n, now) {
		return n, nil
	}

	if err := s.repo.Create(ctx, n); err != nil {
		return nil, fmt.Errorf("creating notification: %w", err)
	}

	// Deliver over email/SMS/push if configured (#191). This is separate
	// from — and does not affect — the in-app broadcast below: in-app
	// always fires; the additional channel here is gated by the
	// notification's own Channel plus the recipient's preferences (see
	// dispatchDelivery). Backgrounded so a slow/unreachable provider can
	// never make notification creation itself slow or fail; a persisted
	// notification always succeeds independent of delivery.
	s.dispatchDelivery(context.WithoutCancel(ctx), n)

	// When a publisher is wired, the durable notification queue drives
	// real-time delivery (the consumer fans out over WebSockets) — the direct
	// broadcast is skipped to avoid duplicate deliveries. Without a publisher
	// the broadcaster is used in-process as a graceful fallback.
	if s.rabbitClient != nil {
		payload, err := json.Marshal(n)
		if err == nil {
			routingKey := fmt.Sprintf("notification.%s", input.Channel)
			if pubErr := s.rabbitClient.Publish(EventsExchange, routingKey, payload); pubErr != nil {
				// Persistence already succeeded; fall back to an immediate
				// broadcast so the user does not miss the event.
				log.Warn().Err(pubErr).Msg("publishing notification event")
				if s.broadcaster != nil {
					s.broadcaster.NotificationCreated(ctx, input.UserID, n.ID.String())
				}
				return n, nil
			}
			return n, nil
		}
	}

	if s.broadcaster != nil {
		s.broadcaster.NotificationCreated(ctx, input.UserID, n.ID.String())
	}

	return n, nil
}

// dispatchDelivery attempts delivery over n.Channel when it names an
// external channel (email/SMS/push) and delivery has been configured via
// WithDeliveryChannels. It never returns an error to the caller — Create()
// must succeed regardless of delivery outcome — and always records a
// DeliveryRecord (sent, failed, or skipped) so the outcome is auditable
// (#191). In-app notifications need no entry here: they have no separate
// delivery step to audit, and are excluded so the audit trail only reflects
// channels that actually attempt external delivery.
func (s *notificationService) dispatchDelivery(ctx context.Context, n *Notification) {
	if n.Channel == ChannelInApp || n.Channel == "" {
		return
	}
	if s.userLookup == nil || s.channels == nil {
		return
	}
	channel, ok := s.channels[n.Channel]
	if !ok {
		log.Warn().Str("channel", string(n.Channel)).Msg("notification delivery: no channel implementation configured")
		return
	}

	go func() {
		const maxAttempts = 2 // one retry beyond the initial attempt
		rec := &DeliveryRecord{
			NotificationID: n.ID,
			Channel:        n.Channel,
		}

		recipient, err := s.userLookup.FindRecipient(ctx, n.UserID.String())
		if err != nil {
			log.Error().Err(err).Str("userID", n.UserID.String()).Msg("notification delivery: resolving recipient failed")
			rec.Status, rec.Attempts = DeliveryFailed, 1
			errStr := err.Error()
			rec.Error = &errStr
			s.recordDelivery(ctx, rec)
			return
		}

		if !recipient.allows(n.Channel) {
			rec.Status, rec.Attempts = DeliverySkipped, 0
			s.recordDelivery(ctx, rec)
			return
		}

		var lastErr error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = channel.Deliver(ctx, n, recipient)
			if lastErr == nil {
				rec.Status, rec.Attempts = DeliverySent, attempt
				s.recordDelivery(ctx, rec)
				return
			}
			log.Warn().Err(lastErr).Str("channel", string(n.Channel)).Int("attempt", attempt).
				Str("notificationID", n.ID.String()).Msg("notification delivery attempt failed")
		}

		rec.Status, rec.Attempts = DeliveryFailed, maxAttempts
		errStr := lastErr.Error()
		rec.Error = &errStr
		s.recordDelivery(ctx, rec)
	}()
}

func (s *notificationService) recordDelivery(ctx context.Context, rec *DeliveryRecord) {
	if s.deliveryAudit == nil {
		return
	}
	if err := s.deliveryAudit.Record(ctx, rec); err != nil {
		log.Error().Err(err).Str("notificationID", rec.NotificationID.String()).Msg("notification delivery: recording audit row failed")
	}
}

// shouldDigest reports whether n should be held for a digest rather than
// delivered immediately (#415). It returns true only when batching is
// configured, the user has it enabled, the event is not urgent, and the buffer
// accepted it.
//
// Every failure mode returns false — the event is delivered immediately:
//   - batching not configured, or preference lookup missing/unavailable
//   - the user's cadence is disabled
//   - the event is an urgent class or is inside UrgentDeadlineWindow
//   - the buffer is full, so the event would otherwise be dropped
func (s *notificationService) shouldDigest(ctx context.Context, n *Notification, now time.Time) bool {
	if s.digestBuffer == nil || s.digestPrefs == nil {
		return false
	}
	if IsUrgent(n.Type, n.Data, now) {
		return false
	}

	prefs, err := s.digestPrefs.DigestPreferences(ctx, n.UserID.String())
	if err != nil {
		log.Warn().Err(err).Str("userID", n.UserID.String()).
			Msg("notification digest: resolving preferences failed, delivering immediately")
		return false
	}
	if !prefs.Enabled {
		return false
	}

	return s.digestBuffer.Enqueue(n.UserID, DigestEntry{Notification: *n, QueuedAt: now}, prefs.Normalize().Interval)
}

// FlushDueDigests drains every user whose cadence has elapsed, assembles one
// summary notification per user, and persists + broadcasts each. It is
// intended to be called on a ticker by the caller.
//
// Digests are persisted and broadcast inline rather than routed back through
// Create, because a digest is synthetic: re-entering Create would run the
// digest decision over our own output. AssembleDigest output is never urgent,
// so that would be self-consistent, but keeping the path explicit avoids the
// ambiguity.
func (s *notificationService) FlushDueDigests(ctx context.Context, now time.Time) (int, error) {
	if s.digestBuffer == nil {
		return 0, nil
	}

	due := s.digestBuffer.TakeDue(now)
	delivered := 0
	var firstErr error

	for userID, entries := range due {
		digest := AssembleDigest(userID, entries)
		if digest == nil {
			continue
		}
		if err := s.repo.Create(ctx, digest); err != nil {
			log.Error().Err(err).Str("userID", userID.String()).
				Int("batchedEvents", len(entries)).
				Msg("notification digest: persisting summary failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.dispatchDelivery(context.WithoutCancel(ctx), digest)
		s.broadcast(ctx, digest)
		delivered++
	}

	return delivered, firstErr
}

// broadcast fans a notification out to the user's connected clients, preferring
// the durable queue when a publisher is wired and falling back to the
// in-process broadcaster otherwise.
func (s *notificationService) broadcast(ctx context.Context, n *Notification) {
	if s.rabbitClient != nil {
		payload, err := json.Marshal(n)
		if err == nil {
			routingKey := fmt.Sprintf("notification.%s", n.Channel)
			if pubErr := s.rabbitClient.Publish(EventsExchange, routingKey, payload); pubErr == nil {
				return
			}
			log.Warn().Err(pubErr).Msg("publishing notification event")
		}
	}
	if s.broadcaster != nil {
		s.broadcaster.NotificationCreated(ctx, n.UserID.String(), n.ID.String())
	}
}

func (s *notificationService) List(ctx context.Context, userID string, page, limit int, unreadOnly bool) ([]Notification, int, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, 0, err
	}
	notifications, total, err := s.repo.List(ctx, uid, page, limit, unreadOnly)
	if err != nil {
		return nil, 0, fmt.Errorf("listing notifications: %w", err)
	}
	return notifications, total, nil
}

func (s *notificationService) MarkRead(ctx context.Context, id, userID string) error {
	nid, err := parseUUID(id)
	if err != nil {
		return err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}
	if err := s.repo.MarkRead(ctx, nid, uid); err != nil {
		return fmt.Errorf("marking notification read: %w", err)
	}
	return nil
}

func (s *notificationService) MarkAllRead(ctx context.Context, userID string) error {
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}
	if err := s.repo.MarkAllRead(ctx, uid); err != nil {
		return fmt.Errorf("marking all notifications read: %w", err)
	}
	return nil
}

func (s *notificationService) BulkArchive(ctx context.Context, userID string, ids []string, archived bool) ([]string, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("at least one notification ID is required")
	}
	if len(ids) > 100 {
		return nil, errors.New("cannot process more than 100 notifications at once")
	}
	parsedIDs := make([]uuid.UUID, 0, len(ids))
	for _, idStr := range ids {
		id, err := parseUUID(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid notification id %s: %w", idStr, err)
		}
		parsedIDs = append(parsedIDs, id)
	}
	updated, err := s.repo.BulkArchive(ctx, uid, parsedIDs, archived)
	if err != nil {
		return nil, fmt.Errorf("bulk archiving notifications: %w", err)
	}
	res := make([]string, 0, len(updated))
	for _, u := range updated {
		res = append(res, u.String())
	}
	return res, nil
}

