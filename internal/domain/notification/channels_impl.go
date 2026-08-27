package notification

import (
	"context"
	"fmt"
)

// EmailSender is the subset of email.Service this package depends on —
// defined locally (rather than importing internal/domain/email directly for
// the type) so tests can supply a fake without pulling in the Brevo client.
type EmailSender interface {
	SendNotification(ctx context.Context, to, subject, body string) error
}

type EmailChannel struct{ Sender EmailSender }

func (c *EmailChannel) Channel() NotificationChannel { return ChannelEmail }

func (c *EmailChannel) Deliver(ctx context.Context, n *Notification, recipient Recipient) error {
	if recipient.Email == nil || *recipient.Email == "" {
		return fmt.Errorf("email channel: recipient has no email address on file")
	}
	return c.Sender.SendNotification(ctx, *recipient.Email, n.Title, n.Body)
}

// SMSSender is the subset of sms.Service this package depends on.
type SMSSender interface {
	Send(ctx context.Context, to, body string) error
}

type SMSChannel struct{ Sender SMSSender }

func (c *SMSChannel) Channel() NotificationChannel { return ChannelSMS }

func (c *SMSChannel) Deliver(ctx context.Context, n *Notification, recipient Recipient) error {
	if recipient.Phone == nil || *recipient.Phone == "" {
		return fmt.Errorf("sms channel: recipient has no phone number on file")
	}
	return c.Sender.Send(ctx, *recipient.Phone, fmt.Sprintf("%s: %s", n.Title, n.Body))
}

// PushSender is the subset of push.Service this package depends on.
type PushSender interface {
	Send(ctx context.Context, token, title, body string) error
}

type PushChannel struct{ Sender PushSender }

func (c *PushChannel) Channel() NotificationChannel { return ChannelPush }

func (c *PushChannel) Deliver(ctx context.Context, n *Notification, recipient Recipient) error {
	if recipient.PushToken == nil || *recipient.PushToken == "" {
		return fmt.Errorf("push channel: recipient has no registered device token")
	}
	return c.Sender.Send(ctx, *recipient.PushToken, n.Title, n.Body)
}
