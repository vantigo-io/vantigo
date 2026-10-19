package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// GetCalendarContacts A customer's contacts
// (GET /api/v1/calendar/contacts)
//
// TODO(calendar invitations, Task 3): the contacts of one customer with an
// address, through the customer directory, under customers:contacts-view
// and customers:associations-view (calendar invitations design D2).
func (s *server) GetCalendarContacts(context.Context, gen.GetCalendarContactsRequestObject) (gen.GetCalendarContactsResponseObject, error) {
	return nil, module.ErrNotImplemented
}
