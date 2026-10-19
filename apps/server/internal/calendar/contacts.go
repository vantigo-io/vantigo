package calendar

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// GetCalendarContacts A customer's contacts
// (GET /api/v1/calendar/contacts)
//
// The contacts of one customer that have an address, for the attendee picker
// (calendar invitations design D2), ordered as the customer directory orders
// them, by name. Besides calendar:access, which the router checks, the caller
// must hold the pair Customers' own contact list requires —
// customers:contacts-view, the sensitive key that guards a contact's address,
// and customers:associations-view — judged here and answered 403 otherwise:
// the address is on this answer because its caller holds that key. With
// Customers off the list is empty; a customer the directory does not know is
// the bare 404.
func (s *server) GetCalendarContacts(ctx context.Context, req gen.GetCalendarContactsRequestObject) (gen.GetCalendarContactsResponseObject, error) {
	if !contracts.HasPermissions(ctx, s.deps.Access, permContactsView, permAssociationsView) {
		return gen.GetCalendarContacts403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	out := gen.GetCalendarContacts200JSONResponse{}
	if s.deps.Directory == nil {
		return out, nil
	}
	id := req.Params.CustomerId
	c, err := s.directoryCustomer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read customer %d: %w", id, err)
	}
	if c == nil {
		return gen.GetCalendarContacts404Response{}, nil
	}
	contacts, err := s.directoryContactsFor(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read customer %d's contacts: %w", id, err)
	}
	for _, ct := range contacts {
		if ct.Email == nil || *ct.Email == "" {
			continue
		}
		name := ""
		if n := contactName(&ct); n != nil {
			name = *n
		}
		out = append(out, gen.CalendarContact{Id: ct.ID, Name: name, Email: *ct.Email})
	}
	return out, nil
}
