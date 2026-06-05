package wallet

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type Repository interface {
	Create(ctx context.Context, w *Wallet) error
	FindByID(ctx context.Context, id string) (*Wallet, error)
	FindByUserID(ctx context.Context, userID string) ([]Wallet, error)
	FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error)
	Delete(ctx context.Context, id string) error
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Create(ctx context.Context, w *Wallet) error {
	query := `
		INSERT INTO wallets (user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, query,
		w.UserID, w.PublicKey, w.EncryptedSecretKey, w.EncryptionNonce,
		w.WalletType, w.IsPrimary,
	).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
}

func (r *pgRepo) FindByID(ctx context.Context, id string) (*Wallet, error) {
	var w Wallet
	err := r.db.GetContext(ctx, &w,
		`SELECT id, user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("finding wallet by id: %w", err)
	}
	return &w, nil
}

func (r *pgRepo) FindByUserID(ctx context.Context, userID string) ([]Wallet, error) {
	var wallets []Wallet
	err := r.db.SelectContext(ctx, &wallets,
		`SELECT id, user_id, public_key, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE user_id = $1 ORDER BY is_primary DESC, created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("finding wallets by user: %w", err)
	}
	return wallets, nil
}

func (r *pgRepo) FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error) {
	var w Wallet
	err := r.db.GetContext(ctx, &w,
		`SELECT id, user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE public_key = $1`, publicKey)
	if err != nil {
		return nil, fmt.Errorf("finding wallet by public key: %w", err)
	}
	return &w, nil
}

func (r *pgRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM wallets WHERE id = $1`, id)
	return err
}
