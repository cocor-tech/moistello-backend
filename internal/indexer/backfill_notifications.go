package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// BackfillNotifications is a one-shot job that rebuilds notification projections
// from historical events. This is useful after schema migrations or when
// notification data needs to be reconstructed.
//
// The job processes events in batches to avoid memory exhaustion and supports
// resume via checkpoint tracking.
type BackfillNotifications struct {
	eventRepo   EventRepository
	notifRepo   NotificationRepository
	batchSize   int
	checkpoint  *BackfillCheckpoint
}

type BackfillCheckpoint struct {
	LastProcessedEventID int64     `json:"lastProcessedEventID"`
	ProcessedCount       int       `json:"processedCount"`
	StartedAt            time.Time `json:"startedAt"`
	LastUpdated          time.Time `json:"lastUpdated"`
}

func NewBackfillNotifications(eventRepo EventRepository, notifRepo NotificationRepository) *BackfillNotifications {
	return &BackfillNotifications{
		eventRepo:  eventRepo,
		notifRepo:  notifRepo,
		batchSize:  100,
		checkpoint: &BackfillCheckpoint{StartedAt: time.Now()},
	}
}

// Run executes the backfill job. It processes events in batches and emits
// progress logs. The context can be cancelled to stop the job gracefully.
func (b *BackfillNotifications) Run(ctx context.Context) error {
	log := log.Ctx(ctx)
	log.Info().Time("startedAt", b.checkpoint.StartedAt).Msg("starting notification backfill")

	for {
		select {
		case <-ctx.Done():
			log.Warn().
				Int("processed", b.checkpoint.ProcessedCount).
				Msg("backfill cancelled")
			return ctx.Err()
		default:
		}

		events, err := b.eventRepo.GetEventsSince(ctx, b.checkpoint.LastProcessedEventID, b.batchSize)
		if err != nil {
			return fmt.Errorf("fetching events: %w", err)
		}

		if len(events) == 0 {
			log.Info().
				Int("totalProcessed", b.checkpoint.ProcessedCount).
				Msg("backfill completed")
			return nil
		}

		for _, event := range events {
			if err := b.processEvent(ctx, event); err != nil {
				log.Error().
					Int64("eventID", event.ID).
					Err(err).
					Msg("failed to process event")
				continue // Skip failed events, log and continue
			}
			b.checkpoint.LastProcessedEventID = event.ID
			b.checkpoint.ProcessedCount++
		}

		b.checkpoint.LastUpdated = time.Now()
		log.Info().
			Int("processed", b.checkpoint.ProcessedCount).
			Int("batchSize", len(events)).
			Msg("backfill batch completed")
	}
}

func (b *BackfillNotifications) processEvent(ctx context.Context, event Event) error {
	// Map event types to notification types
	notifType := mapEventTypeToNotification(event.Type)
	if notifType == "" {
		return nil // Skip unknown event types
	}

	notif := &Notification{
		ID:        generateNotificationID(),
		UserID:    event.UserID,
		Type:      notifType,
		Title:     event.Title,
		Body:      event.Body,
		Data:      event.Data,
		Channel:   "inapp",
		Read:      false,
		CreatedAt: event.CreatedAt,
	}

	return b.notifRepo.Create(ctx, notif)
}

func mapEventTypeToNotification(eventType string) string {
	switch eventType {
	case "circle.joined":
		return "circle_joined"
	case "circle.payout":
		return "payout_executed"
	case "contribution.made":
		return "contribution_received"
	case "invite.received":
		return "invite_received"
	default:
		return ""
	}
}

func generateNotificationID() string {
	return fmt.Sprintf("backfill-%d", time.Now().UnixNano())
}

// EventRepository provides access to historical events for backfill.
type EventRepository interface {
	GetEventsSince(ctx context.Context, lastID int64, limit int) ([]Event, error)
}

type Event struct {
	ID        int64
	Type      string
	UserID    string
	Title     string
	Body      string
	Data      []byte
	CreatedAt time.Time
}

// NotificationRepository provides write access for backfill.
type NotificationRepository interface {
	Create(ctx context.Context, notif *Notification) error
}
