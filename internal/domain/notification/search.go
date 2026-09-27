package notification

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// MaxSearchQueryLength caps the free-text search term.
const MaxSearchQueryLength = 100

// SearchFilter narrows a notification listing. Zero values mean "no filter".
type SearchFilter struct {
	Query           string           // case-insensitive match on title or body
	Type            NotificationType // exact type match
	UnreadOnly      bool
	IncludeArchived bool
}

// escapeLike escapes LIKE wildcards so user input is matched literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// buildSearchConditions returns a WHERE clause and its args ($1 is userID).
func buildSearchConditions(userID uuid.UUID, f SearchFilter) (string, []any) {
	conds := []string{"user_id = $1"}
	args := []any{userID}
	if !f.IncludeArchived {
		conds = append(conds, "is_archived = false")
	}
	if f.UnreadOnly {
		conds = append(conds, "is_read = false")
	}
	if f.Type != "" {
		args = append(args, string(f.Type))
		conds = append(conds, fmt.Sprintf("type = $%d", len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		conds = append(conds, fmt.Sprintf("(title ILIKE $%d OR body ILIKE $%d)", len(args), len(args)))
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// Search lists notifications matching the filter, newest first.
func (r *pgRepo) Search(ctx context.Context, userID uuid.UUID, f SearchFilter, page, limit int) ([]Notification, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	where, args := buildSearchConditions(userID, f)

	var total int
	if err := r.db.QueryRowxContext(ctx, "SELECT COUNT(*) FROM notifications "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting notifications: %w", err)
	}

	query := fmt.Sprintf(`SELECT id, user_id, type, title, body, data, is_read, is_archived, channel, created_at
		FROM notifications %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	rows, err := r.db.QueryxContext(ctx, query, append(args, limit, (page-1)*limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("searching notifications: %w", err)
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

func (s *notificationService) Search(ctx context.Context, userID string, f SearchFilter, page, limit int) ([]Notification, int, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, 0, err
	}
	if len(f.Query) > MaxSearchQueryLength {
		return nil, 0, fmt.Errorf("search query must be at most %d characters", MaxSearchQueryLength)
	}
	notifications, total, err := s.repo.Search(ctx, uid, f, page, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("searching notifications: %w", err)
	}
	return notifications, total, nil
}
