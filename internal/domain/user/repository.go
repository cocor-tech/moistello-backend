package user

import (
	"context"

	"github.com/google/uuid"
)

type UserFilter struct {
	Search string
	Page   int
	Limit  int
}

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByWalletAddress(ctx context.Context, walletAddress string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	EmailPreviouslyVerified(ctx context.Context, email string) (bool, error)
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, u *User) error
	UpdateKYCStatus(ctx context.Context, id uuid.UUID, status KYCStatus, providerRef string) error
	UpdateMoiScore(ctx context.Context, id uuid.UUID, score int) error
	List(ctx context.Context, filter UserFilter) ([]User, error)
	Count(ctx context.Context, filter UserFilter) (int, error)
}
