package notification

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emailSenderStub struct {
	to, subject, body string
	err               error
}

func (s *emailSenderStub) SendNotification(_ context.Context, to, subject, body string) error {
	s.to, s.subject, s.body = to, subject, body
	return s.err
}

type smsSenderStub struct {
	to, body string
	err      error
}

func (s *smsSenderStub) Send(_ context.Context, to, body string) error {
	s.to, s.body = to, body
	return s.err
}

type pushSenderStub struct {
	token, title, body string
	err                error
}

func (s *pushSenderStub) Send(_ context.Context, token, title, body string) error {
	s.token, s.title, s.body = token, title, body
	return s.err
}

func TestEmailChannel(t *testing.T) {
	sendErr := errors.New("email unavailable")
	sender := &emailSenderStub{err: sendErr}
	channel := &EmailChannel{Sender: sender}
	address := "user@example.com"
	notif := &Notification{Title: "Welcome", Body: "Your account is ready"}

	assert.Equal(t, ChannelEmail, channel.Channel())
	err := channel.Deliver(context.Background(), notif, Recipient{Email: &address})
	assert.ErrorIs(t, err, sendErr)
	assert.Equal(t, address, sender.to)
	assert.Equal(t, notif.Title, sender.subject)
	assert.Equal(t, notif.Body, sender.body)
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{}), "no email address")
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{Email: strPtrForChannel("")}), "no email address")
}

func TestSMSChannel(t *testing.T) {
	sendErr := errors.New("sms unavailable")
	sender := &smsSenderStub{err: sendErr}
	channel := &SMSChannel{Sender: sender}
	phone := "+15551234567"
	notif := &Notification{Title: "Payout", Body: "Funds sent"}

	assert.Equal(t, ChannelSMS, channel.Channel())
	err := channel.Deliver(context.Background(), notif, Recipient{Phone: &phone})
	assert.ErrorIs(t, err, sendErr)
	assert.Equal(t, phone, sender.to)
	assert.Equal(t, "Payout: Funds sent", sender.body)
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{}), "no phone number")
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{Phone: strPtrForChannel("")}), "no phone number")
}

func TestPushChannel(t *testing.T) {
	sendErr := errors.New("push unavailable")
	sender := &pushSenderStub{err: sendErr}
	channel := &PushChannel{Sender: sender}
	token := "device-token"
	notif := &Notification{Title: "Reminder", Body: "Contribution due"}

	assert.Equal(t, ChannelPush, channel.Channel())
	err := channel.Deliver(context.Background(), notif, Recipient{PushToken: &token})
	assert.ErrorIs(t, err, sendErr)
	assert.Equal(t, token, sender.token)
	assert.Equal(t, notif.Title, sender.title)
	assert.Equal(t, notif.Body, sender.body)
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{}), "no registered device token")
	assert.ErrorContains(t, channel.Deliver(context.Background(), notif, Recipient{PushToken: strPtrForChannel("")}), "no registered device token")
}

func TestChannelDeliverPropagatesSenderSuccess(t *testing.T) {
	address := "user@example.com"
	phone := "+15551234567"
	token := "device-token"
	notif := &Notification{Title: "Update", Body: "Details"}

	require.NoError(t, (&EmailChannel{Sender: &emailSenderStub{}}).Deliver(context.Background(), notif, Recipient{Email: &address}))
	require.NoError(t, (&SMSChannel{Sender: &smsSenderStub{}}).Deliver(context.Background(), notif, Recipient{Phone: &phone}))
	require.NoError(t, (&PushChannel{Sender: &pushSenderStub{}}).Deliver(context.Background(), notif, Recipient{PushToken: &token}))
}

func strPtrForChannel(value string) *string { return &value }
