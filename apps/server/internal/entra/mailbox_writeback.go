package entra

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Versions answers ErrMailboxUnavailable: the write-back's reads of the
// copies' versions (calendar Outlook sync design O10.1, O10.2) are not yet
// made against Graph, so the calendar's write-back skips them.
func (m *mailboxCalendar) Versions(context.Context, string, string, time.Time, time.Time) (contracts.MailboxVersions, error) {
	return contracts.MailboxVersions{}, contracts.ErrMailboxUnavailable
}

// Events answers ErrMailboxUnavailable: the write-back's whole reads of the
// copies (design O10.2) are not yet made against Graph.
func (m *mailboxCalendar) Events(context.Context, string, string, []string) ([]contracts.MailboxEventState, error) {
	return nil, contracts.ErrMailboxUnavailable
}
