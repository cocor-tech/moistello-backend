package notification

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// DeliveryStatus is the outcome of one channel delivery attempt, persisted
// to notification_deliveries (issue #191's audit requirement).
type DeliveryStatus string

const (
	DeliverySent    DeliveryStatus = "sent"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliverySkipped DeliveryStatus = "skipped"
)

// DeliveryRecord is one row of the delivery audit trail: what channel was
// attempted for a notification, how many attempts it took, and — if it
// didn't succeed — why.
type DeliveryRecord struct {
	ID             uuid.UUID           `json:"id" db:"id"`
	NotificationID uuid.UUID           `json:"notificationId" db:"notification_id"`
	Channel        NotificationChannel `json:"channel" db:"channel"`
	Status         DeliveryStatus      `json:"status" db:"status"`
	Attempts       int                 `json:"attempts" db:"attempts"`
	Error          *string             `json:"error,omitempty" db:"error"`
	CreatedAt      time.Time           `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time           `json:"updatedAt" db:"updated_at"`
}

// DeliveryAuditRepository records channel delivery attempts.
type DeliveryAuditRepository interface {
	Record(ctx context.Context, rec *DeliveryRecord) error
}

type pgDeliveryAuditRepo struct {
	db *sqlx.DB
}

func NewDeliveryAuditRepository(db *sqlx.DB) DeliveryAuditRepository {
	return &pgDeliveryAuditRepo{db: db}
}

func (r *pgDeliveryAuditRepo) Record(ctx context.Context, rec *DeliveryRecord) error {
	if rec.ID == uuid.Nil {
		rec.ID = uuid.New()
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	query := `INSERT INTO notification_deliveries (id, notification_id, channel, status, attempts, error, created_at, updated_at)
		VALUES (:id, :notification_id, :channel, :status, :attempts, :error, :created_at, :updated_at)`
	_, err := r.db.NamedExecContext(ctx, query, rec)
	return err
}
