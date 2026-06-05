package integration_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/circle"
	circleMocks "github.com/moistello/backend/internal/domain/circle/mocks"
	"github.com/moistello/backend/internal/domain/contribution"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	"github.com/moistello/backend/internal/domain/payout"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/tests/helpers"
)

func TestCircleLifecycle(t *testing.T) {
	userRepo := new(userMocks.Repository)
	circleRepo := new(circleMocks.Repository)
	contribRepo := new(contribMocks.Repository)
	payoutRepo := new(payoutMocks.Repository)

	userSvc := user.NewService(userRepo, nil)
	circleSvc := circle.NewService(circleRepo, nil)
	contribSvc := contribution.NewService(contribRepo, nil)
	payoutSvc := payout.NewService(payoutRepo)

	_ = contribSvc
	_ = payoutSvc

	organizer := helpers.NewTestUser("GORGANIZER1234567890ABCDEF1234567890ABCDEF")
	member1 := helpers.NewTestUser("GMEMBER11234567890ABCDEF1234567890ABCDEF")
	member2 := helpers.NewTestUser("GMEMBER21234567890ABCDEF1234567890ABCDEF")

	assert.NotEqual(t, uuid.Nil, organizer.ID)
	assert.NotEqual(t, uuid.Nil, member1.ID)
	assert.NotEqual(t, uuid.Nil, member2.ID)
	assert.NotEqual(t, organizer.ID, member1.ID)

	t.Run("Step1_CreateUser", func(t *testing.T) {
		userRepo.On("FindByWalletAddress", mock.Anything, organizer.WalletAddress).Return(nil, nil).Once()
		userRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil).Once()

		u, err := userSvc.Create(nil, organizer.WalletAddress)
		assert.NoError(t, err)
		assert.Equal(t, organizer.WalletAddress, u.WalletAddress)
		userRepo.AssertExpectations(t)
	})

	t.Run("Step2_CreateCircle", func(t *testing.T) {
		circleRepo.On("Create", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil).Once()
		circleRepo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil).Once()

		c, err := circleSvc.Create(nil, organizer.ID.String(), circle.CreateCircleInput{
			Name:               "Lifecycle Test Circle",
			CircleType:         circle.CircleTypePublic,
			PayoutType:         circle.PayoutTypeRandom,
			ContributionAmount: 100.0,
			Currency:           circle.CurrencyUSDC,
			Frequency:          circle.FrequencyWeekly,
			MaxMembers:         3,
			MaxStrikes:         3,
		})
		assert.NoError(t, err)
		assert.Equal(t, "Lifecycle Test Circle", c.Name)
		assert.Equal(t, circle.CircleStatusPending, c.Status)
		circleRepo.AssertExpectations(t)
	})

	t.Run("Step3_MembersJoin", func(t *testing.T) {
		circleID := uuid.New()
		c := &circle.Circle{
			ID: circleID, Name: "Test", Status: circle.CircleStatusActive,
			MaxMembers: 3, CircleType: circle.CircleTypePublic,
		}

		circleRepo.On("FindByID", mock.Anything, circleID).Return(c, nil).Once()
		circleRepo.On("GetMemberCount", mock.Anything, circleID).Return(1, nil).Once()
		circleRepo.On("FindMemberByCircleAndUser", mock.Anything, circleID, member1.ID).Return(nil, nil).Once()
		circleRepo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil).Once()

		err := circleSvc.Join(nil, circleID.String(), member1.ID.String(), "")
		assert.NoError(t, err)
		circleRepo.AssertExpectations(t)
	})

	t.Run("Step4_RecordContribution", func(t *testing.T) {
		contribRepo.On("Create", mock.Anything, mock.AnythingOfType("*contribution.Contribution")).Return(nil).Once()

		c, err := contribSvc.Record(nil, contribution.RecordInput{
			CircleID:    uuid.New().String(),
			UserID:      organizer.ID.String(),
			RoundNumber: 1,
			Amount:      100.0,
			TxnHash:     "txn-lc-001",
		})
		assert.NoError(t, err)
		assert.NotNil(t, c)
		assert.Equal(t, contribution.StatusPending, c.Status)
		assert.Equal(t, 1, c.RoundNumber)
		contribRepo.AssertExpectations(t)
	})

	t.Run("Step5_RecordPayout", func(t *testing.T) {
		payoutRepo.On("Create", mock.Anything, mock.AnythingOfType("*payout.Payout")).Return(nil).Once()

		p, err := payoutSvc.Record(nil, payout.RecordInput{
			CircleID:    uuid.New().String(),
			RecipientID: member1.ID.String(),
			RoundNumber: 1,
			Amount:      200.0,
			FeeAmount:   5.0,
			TxnHash:     "txn-payout-001",
			PayoutType:  payout.PayoutTypeRandom,
		})
		assert.NoError(t, err)
		assert.NotNil(t, p)
		assert.Equal(t, 200.0, p.Amount)
		assert.Equal(t, payout.PayoutTypeRandom, p.PayoutType)
		payoutRepo.AssertExpectations(t)
	})

	t.Run("Step6_VerifyUserMoiScore", func(t *testing.T) {
		userRepo.On("FindByID", mock.Anything, organizer.ID).Return(organizer, nil).Once()

		resp, err := userSvc.GetMoiScore(nil, organizer.ID.String())
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, organizer.MoiScore, resp.Score)
		assert.NotEmpty(t, resp.Level)
		userRepo.AssertExpectations(t)
	})
}

func TestCircleLifecycle_FullCircle(t *testing.T) {
	circleRepo := new(circleMocks.Repository)
	circleSvc := circle.NewService(circleRepo, nil)

	org := helpers.NewTestUser("GORGFULL1234567890ABCDEF1234567890ABCDEF")
	m1 := helpers.NewTestUser("GM1FULL1234567890ABCDEF1234567890ABCDEF")
	m2 := helpers.NewTestUser("GM2FULL1234567890ABCDEF1234567890ABCDEF")

	circleID := uuid.New()

	circleRepo.On("Create", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil).Once()
	circleRepo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil).Once()
	c, err := circleSvc.Create(nil, org.ID.String(), circle.CreateCircleInput{
		Name:               "Full Circle", CircleType: circle.CircleTypePublic,
		PayoutType: circle.PayoutTypeRandom, ContributionAmount: 50,
		Currency: circle.CurrencyXLM, Frequency: circle.FrequencyDaily,
		MaxMembers: 3, MaxStrikes: 3,
	})
	assert.NoError(t, err)
	assert.NotNil(t, c)
	circleRepo.AssertExpectations(t)

	circleRepo.On("FindByID", mock.Anything, circleID).Return(
		&circle.Circle{ID: circleID, Name: "Full Circle", Status: circle.CircleStatusActive, MaxMembers: 3, CircleType: circle.CircleTypePublic},
		nil,
	).Once()
	circleRepo.On("GetMemberCount", mock.Anything, circleID).Return(2, nil).Once()
	circleRepo.On("FindMemberByCircleAndUser", mock.Anything, circleID, m2.ID).Return(nil, nil).Once()
	circleRepo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(nil).Once()
	err = circleSvc.Join(nil, circleID.String(), m2.ID.String(), "")
	assert.NoError(t, err)

	circleRepo.On("FindByID", mock.Anything, circleID).Return(
		&circle.Circle{ID: circleID, Name: "Full Circle", Status: circle.CircleStatusActive, MaxMembers: 3, CircleType: circle.CircleTypePublic},
		nil,
	).Once()
	circleRepo.On("GetMemberCount", mock.Anything, circleID).Return(3, nil).Once()
	m3 := helpers.NewTestUser("GM3FULL1234567890ABCDEF1234567890ABCDEF")
	err = circleSvc.Join(nil, circleID.String(), m3.ID.String(), "")
	assert.Error(t, err)
	assert.Equal(t, circle.ErrCircleFull, err)
	circleRepo.AssertExpectations(t)

	_ = m1
}
