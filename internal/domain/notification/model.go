package notification

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type NotificationType string

const (
	TypeCircleCreated        NotificationType = "circle.created"
	TypeMemberJoined         NotificationType = "member.joined"
	TypeContributionDue      NotificationType = "contribution.due"
	TypeContributionLate     NotificationType = "contribution.late"
	TypeContributionReceived NotificationType = "contribution.received"
	TypePayoutReceived       NotificationType = "payout.received"
	TypeCircleCompleted      NotificationType = "circle.completed"
	TypeMemberExited         NotificationType = "member.exited"
	TypeDisputeRaised        NotificationType = "dispute.raised"
)

type NotificationChannel string

const (
	ChannelInApp NotificationChannel = "inapp"
	ChannelEmail NotificationChannel = "email"
	ChannelSMS   NotificationChannel = "sms"
	ChannelPush  NotificationChannel = "push"
)

type Notification struct {
	ID        uuid.UUID           `json:"id" db:"id"`
	UserID    uuid.UUID           `json:"userId" db:"user_id"`
	Type      NotificationType    `json:"type" db:"type"`
	Title     string              `json:"title" db:"title"`
	Body      string              `json:"body" db:"body"`
	Data      json.RawMessage     `json:"data,omitempty" db:"data"`
	IsRead     bool                `json:"isRead" db:"is_read"`
	IsArchived bool                `json:"isArchived" db:"is_archived"`
	Channel    NotificationChannel `json:"channel" db:"channel"`
	CreatedAt  time.Time           `json:"createdAt" db:"created_at"`
}
