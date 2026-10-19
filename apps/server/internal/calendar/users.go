package calendar

import (
	"context"
	"fmt"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// The pickers' page size: the default, and the most one request may ask for.
const (
	usersDefaultLimit = 20
	usersMaxLimit     = 50
)

// GetCalendarUsers Search colleagues
// (GET /api/v1/calendar/users)
//
// The attendee picker and the colleague picker (calendar design D6, D8).
// identity's own user listing needs the authorization-management policy, so
// without this a person who may book a colleague could not find one — the
// reason contracts.UserDirectory exists, and Customers' assignable-users
// handler's shape. What it answers, an active user's id and display name, is
// what every appointment already shows of its attendees. SearchUsers answers
// active users only, ordered by display name; a blank query is its first page.
func (s *server) GetCalendarUsers(ctx context.Context, req gen.GetCalendarUsersRequestObject) (gen.GetCalendarUsersResponseObject, error) {
	limit := int32(usersDefaultLimit)
	if req.Params.Limit != nil {
		if *req.Params.Limit < 1 || *req.Params.Limit > usersMaxLimit {
			var f fieldErrors
			f.add("limit", fmt.Sprintf("limit is 1 to %d", usersMaxLimit))
			return gen.GetCalendarUsers400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
		}
		limit = *req.Params.Limit
	}
	query := ""
	if req.Params.Query != nil {
		query = strings.TrimSpace(*req.Params.Query)
	}
	found, err := s.usersSearch(ctx, query, int(limit))
	if err != nil {
		return nil, fmt.Errorf("calendar: search users: %w", err)
	}
	out := make(gen.GetCalendarUsers200JSONResponse, 0, len(found))
	for _, u := range found {
		if !u.Active {
			continue
		}
		out = append(out, gen.CalendarUserRef{Id: u.ID, DisplayName: u.DisplayName})
		if len(out) == int(limit) {
			// The directory clamps its own limit; this one is the module's
			// promise, kept here rather than assumed of a collaborator.
			break
		}
	}
	return out, nil
}
