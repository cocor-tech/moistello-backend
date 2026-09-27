package contribution_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

// statusCircleService returns a circle with a fixed status; other methods are unused.
type statusCircleService struct {
	circle.Service
	status circle.CircleStatus
}

func (s statusCircleService) Get(_ context.Context, _ string) (*circle.Circle, error) {
	return &circle.Circle{Status: s.status}, nil
}

func TestContributionService_Record_RejectsEndedCircle(t *testing.T) {
	for _, status := range []circle.CircleStatus{circle.CircleStatusCompleted, circle.CircleStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			repo := new(contribMocks.Repository)
			svc := contribution.NewService(repo, nil, nil, nil, "", statusCircleService{status: status})

			c, err := svc.Record(context.Background(), contribution.RecordInput{
				CircleID:    uuid.New().String(),
				UserID:      uuid.New().String(),
				RoundNumber: 1,
				Amount:      100,
				TxnHash:     "txn-ended",
			})

			assert.ErrorIs(t, err, apperrors.ErrCircleEnded)
			assert.Nil(t, c)
			// Rejected before any repository access.
			repo.AssertNotCalled(t, "FindByTxnHash")
			repo.AssertNotCalled(t, "Create")
		})
	}
}
