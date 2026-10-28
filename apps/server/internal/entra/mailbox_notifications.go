package entra

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the module's change notifications for the mailbox calendar
// (calendar Outlook sync design O11.2–O11.4). Until the subscriptions are
// made, the provider offers none: Watch and Unwatch answer nil, as the
// contract allows a provider without notifications, and Notified answers
// none, so the calendar's poll alone runs.

// Watch answers nil: the provider offers no notifications yet.
func (m *mailboxCalendar) Watch(context.Context, string) error { return nil }

// Unwatch answers nil: no subscription is ever made, so none is owed.
func (m *mailboxCalendar) Unwatch(context.Context, string) error { return nil }

// Notified answers no notice.
func (m *mailboxCalendar) Notified(context.Context, time.Time) ([]contracts.MailboxNotice, error) {
	return []contracts.MailboxNotice{}, nil
}
