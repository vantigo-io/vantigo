package calendar

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// errMailboxSyncPending answers the Outlook sync's three operations until
// their handlers land (calendar Outlook sync plan, task 3c): the contract,
// the tables and the slot exist, the behaviour does not yet.
var errMailboxSyncPending = errors.New("calendar: the Outlook sync is not implemented yet")

// GetCalendarMailboxSync is a placeholder; see errMailboxSyncPending.
func (s *server) GetCalendarMailboxSync(context.Context, gen.GetCalendarMailboxSyncRequestObject) (gen.GetCalendarMailboxSyncResponseObject, error) {
	return nil, errMailboxSyncPending
}

// PutCalendarMailboxSync is a placeholder; see errMailboxSyncPending.
func (s *server) PutCalendarMailboxSync(context.Context, gen.PutCalendarMailboxSyncRequestObject) (gen.PutCalendarMailboxSyncResponseObject, error) {
	return nil, errMailboxSyncPending
}

// PostCalendarMailboxSyncRun is a placeholder; see errMailboxSyncPending.
func (s *server) PostCalendarMailboxSyncRun(context.Context, gen.PostCalendarMailboxSyncRunRequestObject) (gen.PostCalendarMailboxSyncRunResponseObject, error) {
	return nil, errMailboxSyncPending
}
