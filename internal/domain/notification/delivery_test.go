package notification_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
	notifMocks "github.com/moistello/backend/internal/domain/notification/mocks"
)

// fakeUserLookup returns a fixed Recipient (or error) for every user ID.
type fakeUserLookup struct {
	recipient notification.Recipient
	err       error
}

func (f *fakeUserLookup) FindRecipient(ctx context.Context, userID string) (notification.Recipient, error) {
	return f.recipient, f.err
}

// fakeDeliveryChannel is a DeliveryChannel whose Deliver result is scripted
// per-call, and which signals a channel after each Deliver call so tests
// can synchronize with dispatchDelivery's background goroutine without
// sleeping.
type fakeDeliveryChannel struct {
	channel NotificationChannelAlias
	// results is consumed in order across calls; the last entry repeats.
	results []error
	calls   int
	mu      sync.Mutex
	done    chan struct{}
}

// NotificationChannelAlias avoids importing notification twice under two
// names in this file; it's just notification.NotificationChannel.
type NotificationChannelAlias = notification.NotificationChannel

func (f *fakeDeliveryChannel) Channel() NotificationChannelAlias { return f.channel }

func (f *fakeDeliveryChannel) Deliver(ctx context.Context, n *notification.Notification, recipient notification.Recipient) error {
	f.mu.Lock()
	idx := f.calls
	if idx >= len(f.results) {
		idx = len(f.results) - 1
	}
	result := f.results[idx]
	f.calls++
	f.mu.Unlock()
	if f.done != nil {
		f.done <- struct{}{}
	}
	return result
}

// fakeDeliveryAudit records DeliveryRecords and signals a channel per
// record so tests can wait for dispatchDelivery's async write.
type fakeDeliveryAudit struct {
	mu      sync.Mutex
	records []*notification.DeliveryRecord
	done    chan struct{}
}

func (f *fakeDeliveryAudit) Record(ctx context.Context, rec *notification.DeliveryRecord) error {
	f.mu.Lock()
	f.records = append(f.records, rec)
	f.mu.Unlock()
	if f.done != nil {
		f.done <- struct{}{}
	}
	return nil
}

func (f *fakeDeliveryAudit) last() *notification.DeliveryRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return nil
	}
	return f.records[len(f.records)-1]
}

func waitOn(t *testing.T, ch chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async delivery dispatch")
	}
}

func strPtr(s string) *string { return &s }

func TestDispatchDelivery_SkipsInAppChannel(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 4)}
	lookup := &fakeUserLookup{recipient: notification.Recipient{PreferredChannels: []string{"email"}}}
	emailCh := &fakeDeliveryChannel{channel: notification.ChannelEmail, results: []error{nil}}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelInApp,
	})
	require.NoError(t, err)

	// In-app never dispatches to an external channel or writes an audit
	// row — give any (incorrect) async work a moment to happen, then assert
	// nothing did.
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, audit.last())
	assert.Equal(t, 0, emailCh.calls)
}

func TestDispatchDelivery_SkippedWhenRecipientDoesNotAllowChannel(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	// Recipient has not opted into email.
	lookup := &fakeUserLookup{recipient: notification.Recipient{
		Email:             strPtr("user@example.com"),
		PreferredChannels: []string{},
	}}
	emailCh := &fakeDeliveryChannel{channel: notification.ChannelEmail, results: []error{nil}}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, audit.done)
	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliverySkipped, rec.Status)
	assert.Equal(t, 0, emailCh.calls)
}

func TestDispatchDelivery_SkippedWhenRecipientMuted(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	lookup := &fakeUserLookup{recipient: notification.Recipient{
		Email:             strPtr("user@example.com"),
		PreferredChannels: []string{"email"},
		Muted:             true,
	}}
	emailCh := &fakeDeliveryChannel{channel: notification.ChannelEmail, results: []error{nil}}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, audit.done)
	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliverySkipped, rec.Status)
}

func TestDispatchDelivery_RecordsSentOnSuccess(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	lookup := &fakeUserLookup{recipient: notification.Recipient{
		Email:             strPtr("user@example.com"),
		PreferredChannels: []string{"email"},
	}}
	emailCh := &fakeDeliveryChannel{channel: notification.ChannelEmail, results: []error{nil}, done: make(chan struct{}, 1)}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, emailCh.done)
	waitOn(t, audit.done)
	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliverySent, rec.Status)
	assert.Equal(t, 1, rec.Attempts)
	assert.Equal(t, notification.ChannelEmail, rec.Channel)
	assert.Nil(t, rec.Error)
}

func TestDispatchDelivery_RetriesOnceThenRecordsFailed(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	lookup := &fakeUserLookup{recipient: notification.Recipient{
		Email:             strPtr("user@example.com"),
		PreferredChannels: []string{"email"},
	}}
	deliverErr := errors.New("smtp: connection refused")
	emailCh := &fakeDeliveryChannel{
		channel: notification.ChannelEmail,
		results: []error{deliverErr, deliverErr},
		done:    make(chan struct{}, 2),
	}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, emailCh.done)
	waitOn(t, emailCh.done)
	waitOn(t, audit.done)

	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliveryFailed, rec.Status)
	assert.Equal(t, 2, rec.Attempts, "should retry exactly once beyond the initial attempt")
	require.NotNil(t, rec.Error)
	assert.Contains(t, *rec.Error, "connection refused")
}

func TestDispatchDelivery_RecoversOnRetry(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	lookup := &fakeUserLookup{recipient: notification.Recipient{
		Email:             strPtr("user@example.com"),
		PreferredChannels: []string{"email"},
	}}
	emailCh := &fakeDeliveryChannel{
		channel: notification.ChannelEmail,
		results: []error{errors.New("transient"), nil},
		done:    make(chan struct{}, 2),
	}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, emailCh.done)
	waitOn(t, emailCh.done)
	waitOn(t, audit.done)

	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliverySent, rec.Status)
	assert.Equal(t, 2, rec.Attempts)
}

func TestDispatchDelivery_FailedRecipientLookupIsAudited(t *testing.T) {
	repo := new(notifMocks.Repository)
	audit := &fakeDeliveryAudit{done: make(chan struct{}, 1)}
	lookup := &fakeUserLookup{err: errors.New("db unreachable")}
	emailCh := &fakeDeliveryChannel{channel: notification.ChannelEmail, results: []error{nil}}

	svc := notification.NewService(repo, nil, nil,
		notification.WithDeliveryChannels(lookup, audit, emailCh))

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	_, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)

	waitOn(t, audit.done)
	rec := audit.last()
	require.NotNil(t, rec)
	assert.Equal(t, notification.DeliveryFailed, rec.Status)
	assert.Equal(t, 0, emailCh.calls)
}

func TestDispatchDelivery_NoOpWhenNotConfigured(t *testing.T) {
	repo := new(notifMocks.Repository)
	// notification.NewService with no WithDeliveryChannels option — the
	// same construction every other Create test in this package uses,
	// confirming #191's channels are opt-in and never break existing
	// in-app-only behaviour.
	svc := notification.NewService(repo, nil, nil)

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	result, err := svc.Create(context.Background(), notification.CreateInput{
		UserID:  uuid.New().String(),
		Type:    notification.TypeCircleCreated,
		Title:   "t", Body: "b",
		Channel: notification.ChannelEmail,
	})
	require.NoError(t, err)
	assert.NotNil(t, result)
}
