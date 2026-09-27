package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/moistello/backend/pkg/apperrors"
)

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func scanNotification(row interface{ Scan(...interface{}) error }) (*Notification, error) {
	var n Notification
	var data json.RawMessage
	err := row.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &data, &n.IsRead, &n.IsArchived, &n.Channel, &n.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("scanning notification row: %w", err)
	}
	n.Data = data
	return &n, nil
}

func (r *pgRepo) Create(ctx context.Context, n *Notification) error {
	query := `INSERT INTO notifications (id, user_id, type, title, body, data, is_read, is_archived, channel, created_at)
		VALUES (:id, :user_id, :type, :title, :body, :data, :is_read, :is_archived, :channel, :created_at)`
	_, err := r.db.NamedExecContext(ctx, query, n)
	if err != nil {
		return fmt.Errorf("creating notification: %w", err)
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, userID uuid.UUID, page, limit int, unreadOnly bool) ([]Notification, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var conditions string
	if unreadOnly {
		conditions = "WHERE user_id = $1 AND is_read = false"
	} else {
		conditions = "WHERE user_id = $1"
	}

	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM notifications %s", conditions)
	if err := r.db.QueryRowxContext(ctx, countQuery, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting notifications: %w", err)
	}

	query := fmt.Sprintf(`SELECT id, user_id, type, title, body, data, is_read, is_archived, channel, created_at
		FROM notifications %s ORDER BY created_at DESC LIMIT $2 OFFSET $3`, conditions)
	rows, err := r.db.QueryxContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing notifications: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, 0, err
		}
		notifications = append(notifications, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating notifications: %w", err)
	}
	return notifications, total, nil
}

func (r *pgRepo) MarkRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	query := `UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2`
	result, err := r.db.ExecContext(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("marking notification read: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *pgRepo) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE notifications SET is_read = true WHERE user_id = $1 AND is_read = false`
	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("marking all notifications read: %w", err)
	}
	return nil
}

func (r *pgRepo) BulkArchive(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, archived bool) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := sqlx.In(`
		UPDATE notifications
		SET is_archived = ?
		WHERE user_id = ? AND id IN (?)
		RETURNING id`, archived, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("building bulk archive query: %w", err)
	}
	query = r.db.Rebind(query)
	var updatedIDs []uuid.UUID
	if err := r.db.SelectContext(ctx, &updatedIDs, query, args...); err != nil {
		return nil, fmt.Errorf("bulk updating notifications archive status: %w", err)
	}
	return updatedIDs, nil
}
