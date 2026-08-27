package notification

import "context"

// Recipient is the subset of user data a DeliveryChannel needs to actually
// deliver a notification, decoupled from the `user` package's full model so
// this package doesn't import it directly (see UserLookup below, and
// cmd/api-server/main.go's userLookupAdapter, which follows the same
// adapter pattern already used for moiAdapter/communityAdapter).
type Recipient struct {
	Email     *string
	Phone     *string
	PushToken *string
	// PreferredChannels mirrors users.notification_channels — the channels
	// the user has opted into beyond the always-on in-app channel.
	PreferredChannels []string
	// Muted mirrors users.notifications_muted — when true, no channel beyond
	// in-app is attempted regardless of PreferredChannels.
	Muted bool
}

// allows reports whether ch is enabled for this recipient: not muted, and
// present in their preferred channels. In-app is always allowed — muting
// controls the *additional* channels, not the base in-app record.
func (r Recipient) allows(ch NotificationChannel) bool {
	if ch == ChannelInApp {
		return true
	}
	if r.Muted {
		return false
	}
	for _, c := range r.PreferredChannels {
		if NotificationChannel(c) == ch {
			return true
		}
	}
	return false
}

// UserLookup resolves delivery details for a notification's recipient.
// Implemented by an adapter over user.Repository in cmd/api-server/main.go.
type UserLookup interface {
	FindRecipient(ctx context.Context, userID string) (Recipient, error)
}

// DeliveryChannel sends a notification over one channel (email, SMS, push).
// In-app delivery is handled separately via Broadcaster — it doesn't need
// retry/audit the way an external provider call does.
type DeliveryChannel interface {
	Channel() NotificationChannel
	Deliver(ctx context.Context, n *Notification, recipient Recipient) error
}
