package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// UserResponse represents the public/admin-safe projection of a User record,
// intentionally omitting password_hash, totp_secret, and backup_codes.
type UserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ListUsersForAdmin selects user attributes explicitly excluding credentials/secrets.
func (r *PostgresRepository) ListUsersForAdmin(ctx context.Context) ([]UserResponse, error) {
	query := `
		SELECT id, email, role, created_at, updated_at 
		FROM users 
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []UserResponse
	for rows.Next() {
		var u UserResponse
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user row: %w", err)
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return users, nil
}