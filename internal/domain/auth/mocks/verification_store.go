package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/auth"
)

type VerificationStore struct {
	mock.Mock
}

func (m *VerificationStore) Save(ctx context.Context, v *auth.VerificationCode) error {
	args := m.Called(ctx, v)
	return args.Error(0)
}

func (m *VerificationStore) FindByID(ctx context.Context, id string) (*auth.VerificationCode, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.VerificationCode), args.Error(1)
}

func (m *VerificationStore) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *VerificationStore) MarkEmailVerified(ctx context.Context, email string) error {
	args := m.Called(ctx, email)
	return args.Error(0)
}

func (m *VerificationStore) IsEmailVerified(ctx context.Context, email string) (bool, error) {
	args := m.Called(ctx, email)
	return args.Bool(0), args.Error(1)
}
