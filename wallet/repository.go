package wallet

import (
	"context"
	"database/sql"
	"errors"
)

type SQLRepository struct {
	db *sql.DB
}

func NewSQLRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{db: db}
}

func (r *SQLRepository) GetByID(ctx context.Context, walletID string) (*Wallet, error) {
	query := `SELECT id, user_id, address, created_at FROM wallets WHERE id = $1`
	row := r.db.QueryRowContext(ctx, query, walletID)

	var w Wallet
	if err := row.Scan(&w.ID, &w.UserID, &w.Address, &w.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

// DeleteByIDAndUserID enforces ownership at the database query level.
func (r *SQLRepository) DeleteByIDAndUserID(ctx context.Context, walletID, userID string) error {
	query := `DELETE FROM wallets WHERE id = $1 AND user_id = $2`
	result, err := r.db.ExecContext(ctx, query, walletID, userID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrWalletNotFound
	}

	return nil
}