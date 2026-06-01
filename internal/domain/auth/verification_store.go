package auth

import (
	"context"
)

type VerificationStore interface {
	Save(ctx context.Context, v *VerificationCode) error
	FindByID(ctx context.Context, id string) (*VerificationCode, error)
	Delete(ctx context.Context, id string) error
	MarkEmailVerified(ctx context.Context, email string) error
	IsEmailVerified(ctx context.Context, email string) (bool, error)
}
