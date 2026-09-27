package notification_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/notification"
	notifMocks "github.com/moistello/backend/internal/domain/notification/mocks"
)

// mockBroadcaster implements notification.Broadcaster for testing.
type mockBroadcaster struct {
	mock.Mock
}

func (m *mockBroadcaster) NotificationCreated(ctx context.Context, userID, notificationID string) {
	m.Called(ctx, userID, notificationID)
}

func TestService_Create_Success(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New().String()
	input := notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypeCircleCreated,
		Title:   "Circle Created",
		Body:    "Your circle was created successfully",
		Data:    json.RawMessage(`{"circleId":"abc"}`),
		Channel: notification.ChannelInApp,
	}

	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)

	result, err := svc.Create(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, notification.TypeCircleCreated, result.Type)
	assert.Equal(t, "Circle Created", result.Title)
	assert.Equal(t, "Your circle was created successfully", result.Body)
	assert.Equal(t, notification.ChannelInApp, result.Channel)
	assert.False(t, result.IsRead)
	assert.NotEqual(t, uuid.Nil, result.ID)
	repo.AssertExpectations(t)
}

func TestService_Create_InvalidUUID(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	input := notification.CreateInput{
		UserID:  "not-a-uuid",
		Type:    notification.TypeCircleCreated,
		Title:   "Test",
		Body:    "Test body",
		Channel: notification.ChannelInApp,
	}

	result, err := svc.Create(context.Background(), input)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestService_Create_WithBroadcaster(t *testing.T) {
	repo := new(notifMocks.Repository)
	bc := new(mockBroadcaster)
	svc := notification.NewService(repo, nil, bc)

	userID := uuid.New().String()
	input := notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypePayoutReceived,
		Title:   "Payout Received",
		Body:    "You received a payout",
		Channel: notification.ChannelInApp,
	}

	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).Return(nil)
	bc.On("NotificationCreated", mock.Anything, userID, mock.AnythingOfType("string")).Return()

	result, err := svc.Create(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	bc.AssertCalled(t, "NotificationCreated", mock.Anything, userID, result.ID.String())
	repo.AssertExpectations(t)
	bc.AssertExpectations(t)
}

func TestService_Create_RepoError(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New().String()
	input := notification.CreateInput{
		UserID:  userID,
		Type:    notification.TypeCircleCreated,
		Title:   "Test",
		Body:    "Test body",
		Channel: notification.ChannelInApp,
	}

	repo.On("Create", mock.Anything, mock.AnythingOfType("*notification.Notification")).
		Return(assert.AnError)

	result, err := svc.Create(context.Background(), input)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "creating notification")
	repo.AssertExpectations(t)
}

func TestService_List_Success(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	expected := []notification.Notification{
		{ID: uuid.New(), UserID: userID, Title: "Notif 1"},
		{ID: uuid.New(), UserID: userID, Title: "Notif 2"},
	}

	repo.On("List", mock.Anything, userID, 1, 20, false).Return(expected, 2, nil)

	results, total, err := svc.List(context.Background(), userID.String(), 1, 20, false)

	assert.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, results, 2)
	assert.Equal(t, "Notif 1", results[0].Title)
	repo.AssertExpectations(t)
}

func TestService_List_UnreadOnly(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	expected := []notification.Notification{
		{ID: uuid.New(), UserID: userID, Title: "Unread", IsRead: false},
	}

	repo.On("List", mock.Anything, userID, 1, 10, true).Return(expected, 1, nil)

	results, total, err := svc.List(context.Background(), userID.String(), 1, 10, true)

	assert.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, results, 1)
	repo.AssertExpectations(t)
}

func TestService_List_InvalidUUID(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	results, total, err := svc.List(context.Background(), "bad-uuid", 1, 20, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
	assert.Nil(t, results)
	assert.Equal(t, 0, total)
}

func TestService_MarkRead_Success(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	notifID := uuid.New()
	userID := uuid.New()

	repo.On("MarkRead", mock.Anything, notifID, userID).Return(nil)

	err := svc.MarkRead(context.Background(), notifID.String(), userID.String())

	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestService_MarkRead_InvalidNotificationID(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	err := svc.MarkRead(context.Background(), "bad-id", uuid.New().String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestService_MarkRead_InvalidUserID(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	err := svc.MarkRead(context.Background(), uuid.New().String(), "bad-user-id")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestService_MarkRead_RepoError(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	notifID := uuid.New()
	userID := uuid.New()

	repo.On("MarkRead", mock.Anything, notifID, userID).Return(assert.AnError)

	err := svc.MarkRead(context.Background(), notifID.String(), userID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marking notification read")
	repo.AssertExpectations(t)
}

func TestService_MarkAllRead_Success(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	repo.On("MarkAllRead", mock.Anything, userID).Return(nil)

	err := svc.MarkAllRead(context.Background(), userID.String())

	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestService_MarkAllRead_InvalidUUID(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	err := svc.MarkAllRead(context.Background(), "bad-uuid")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestService_MarkAllRead_RepoError(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	repo.On("MarkAllRead", mock.Anything, userID).Return(assert.AnError)

	err := svc.MarkAllRead(context.Background(), userID.String())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marking all notifications read")
	repo.AssertExpectations(t)
}

func TestService_BulkArchive_Success(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()
	id1 := uuid.New()
	id2 := uuid.New()

	repo.On("BulkArchive", mock.Anything, userID, []uuid.UUID{id1, id2}, true).Return([]uuid.UUID{id1, id2}, nil)

	updated, err := svc.BulkArchive(context.Background(), userID.String(), []string{id1.String(), id2.String()}, true)
	assert.NoError(t, err)
	assert.Equal(t, []string{id1.String(), id2.String()}, updated)
	repo.AssertExpectations(t)
}

func TestService_BulkArchive_Validation(t *testing.T) {
	repo := new(notifMocks.Repository)
	svc := notification.NewService(repo, nil, nil)

	userID := uuid.New()

	// Empty IDs
	_, err := svc.BulkArchive(context.Background(), userID.String(), []string{}, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one notification ID is required")

	// Invalid ID
	_, err = svc.BulkArchive(context.Background(), userID.String(), []string{"not-a-uuid"}, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid notification id")
}

