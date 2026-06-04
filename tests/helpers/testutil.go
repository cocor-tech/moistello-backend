package helpers

import (
	"time"

	"github.com/google/uuid"

	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/user"
)

func NewTestUser(wallet string) *user.User {
	now := time.Now().UTC()
	return &user.User{
		ID:                uuid.New(),
		WalletAddress:     wallet,
		PreferredLanguage: "en",
		MoiScore:          500,
		Role:              user.RoleUser,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func NewTestUserWithName(wallet, name string) *user.User {
	u := NewTestUser(wallet)
	u.DisplayName = &name
	return u
}

func NewTestCircle(organizerID uuid.UUID, name string) *circle.Circle {
	now := time.Now().UTC()
	return &circle.Circle{
		ID:                 uuid.New(),
		Name:               name,
		CircleType:         circle.CircleTypePublic,
		PayoutType:         circle.PayoutTypeRandom,
		ContributionAmount: 100.0,
		Currency:           circle.CurrencyUSDC,
		Frequency:          circle.FrequencyWeekly,
		MaxMembers:         10,
		Status:             circle.CircleStatusPending,
		OrganizerID:        organizerID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func NewTestCircleMember(circleID, userID uuid.UUID, position int) *circle.CircleMember {
	return &circle.CircleMember{
		CircleID: circleID,
		UserID:   userID,
		Position: position,
		Status:   circle.MemberStatusActive,
		JoinedAt: time.Now().UTC(),
	}
}

func NewTestContribution(circleID, userID uuid.UUID, round int, amount float64) *contribution.Contribution {
	now := time.Now().UTC()
	return &contribution.Contribution{
		ID:          uuid.New(),
		CircleID:    circleID,
		UserID:      userID,
		RoundNumber: round,
		Amount:      amount,
		Status:      contribution.StatusPending,
		OnTime:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func MustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic("invalid UUID in test helper: " + s)
	}
	return id
}

func StringPtr(s string) *string   { return &s }
func FloatPtr(f float64) *float64  { return &f }
func IntPtr(i int) *int            { return &i }
func BoolPtr(b bool) *bool         { return &b }
