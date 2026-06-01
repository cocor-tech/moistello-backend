package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type EmailSender struct {
	mock.Mock
}

func (m *EmailSender) Send(ctx context.Context, to, subject, body string) error {
	args := m.Called(ctx, to, subject, body)
	return args.Error(0)
}
