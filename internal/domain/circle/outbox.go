package circle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

type OutboxStatus string

const (
	OutboxStatusPending   OutboxStatus = "pending"
	OutboxStatusPublished OutboxStatus = "published"
	OutboxStatusFailed    OutboxStatus = "failed"
)

// OutboxEvent represents an event persisted transactionally alongside entity state changes.
type OutboxEvent struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	AggregateType string          `json:"aggregateType" db:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregateId" db:"aggregate_id"`
	EventType     string          `json:"eventType" db:"event_type"`
	Payload       json.RawMessage `json:"payload" db:"payload"`
	Status        OutboxStatus    `json:"status" db:"status"`
	RetryCount    int             `json:"retryCount" db:"retry_count"`
	CreatedAt     time.Time       `json:"createdAt" db:"created_at"`
	ProcessedAt   *time.Time      `json:"processedAt,omitempty" db:"processed_at"`
}

// OutboxRepository defines methods for managing outbox events.
type OutboxRepository interface {
	Save(ctx context.Context, tx *sqlx.Tx, event *OutboxEvent) error
	FetchPending(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, retryCount int) error
	DeletePublished(ctx context.Context, before time.Time) (int64, error)
}

type pgOutboxRepo struct {
	db *sqlx.DB
}

func NewOutboxRepository(db *sqlx.DB) OutboxRepository {
	return &pgOutboxRepo{db: db}
}

func (r *pgOutboxRepo) Save(ctx context.Context, tx *sqlx.Tx, event *OutboxEvent) error {
	query := `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status, retry_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, event.ID, event.AggregateType, event.AggregateID, event.EventType, event.Payload, event.Status, event.RetryCount, event.CreatedAt)
	} else if r.db != nil {
		_, err = r.db.ExecContext(ctx, query, event.ID, event.AggregateType, event.AggregateID, event.EventType, event.Payload, event.Status, event.RetryCount, event.CreatedAt)
	}
	if err != nil {
		return fmt.Errorf("saving outbox event: %w", err)
	}
	return nil
}

func (r *pgOutboxRepo) FetchPending(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if r.db == nil {
		return []OutboxEvent{}, nil
	}
	query := `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, status, retry_count, created_at, processed_at
		FROM outbox_events
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1
	`
	var events []OutboxEvent
	err := r.db.SelectContext(ctx, &events, query, limit)
	if err != nil {
		if err == sql.ErrNoRows {
			return []OutboxEvent{}, nil
		}
		return nil, fmt.Errorf("fetching pending outbox events: %w", err)
	}
	return events, nil
}

func (r *pgOutboxRepo) MarkPublished(ctx context.Context, id uuid.UUID) error {
	if r.db == nil {
		return nil
	}
	query := `UPDATE outbox_events SET status = 'published', processed_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

func (r *pgOutboxRepo) MarkFailed(ctx context.Context, id uuid.UUID, retryCount int) error {
	if r.db == nil {
		return nil
	}
	query := `UPDATE outbox_events SET status = 'failed', retry_count = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, retryCount)
	return err
}

func (r *pgOutboxRepo) DeletePublished(ctx context.Context, before time.Time) (int64, error) {
	if r.db == nil {
		return 0, nil
	}
	query := `DELETE FROM outbox_events WHERE status = 'published' AND processed_at < $1`
	res, err := r.db.ExecContext(ctx, query, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// OutboxRelay polls pending outbox events and publishes them asynchronously.
type OutboxRelay struct {
	repo        OutboxRepository
	broadcaster Broadcaster
}

func NewOutboxRelay(repo OutboxRepository, broadcaster Broadcaster) *OutboxRelay {
	return &OutboxRelay{repo: repo, broadcaster: broadcaster}
}

func (relay *OutboxRelay) ProcessBatch(ctx context.Context, limit int) (int, error) {
	if relay.repo == nil {
		return 0, nil
	}
	events, err := relay.repo.FetchPending(ctx, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, event := range events {
		if relay.broadcaster != nil {
			switch event.EventType {
			case "CircleStatusChanged":
				var p struct {
					CircleID string `json:"circleId"`
					Status   string `json:"status"`
				}
				if err := json.Unmarshal(event.Payload, &p); err == nil {
					relay.broadcaster.CircleStatusChanged(ctx, p.CircleID, p.Status)
				}
			case "MemberJoined":
				var p struct {
					CircleID string `json:"circleId"`
					UserID   string `json:"userId"`
				}
				if err := json.Unmarshal(event.Payload, &p); err == nil {
					relay.broadcaster.MemberJoined(ctx, p.CircleID, p.UserID)
				}
			}
		}

		if err := relay.repo.MarkPublished(ctx, event.ID); err != nil {
			log.Warn().Err(err).Str("eventID", event.ID.String()).Msg("failed to mark outbox event published")
		} else {
			processed++
		}
	}

	return processed, nil
}
