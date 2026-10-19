package calendar

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// This file is the person's calendar addresses (calendar feed design D1): at
// most one per variant, full or busy, each behind a token stored only as its
// hex SHA-256 and found by it, the whole address kept sealed under
// feedAddressPurpose so the owner's own screen can show it again. GET /feed
// answers them, POST /feed/{variant} creates or resets one, DELETE turns one
// off — all calendar:access's, on the caller's own rows.

// feedAddressPurpose is the secrets box purpose a calendar address is sealed
// under.
const feedAddressPurpose = "calendar/feed-address" //nolint:unused // sealed and opened by the address handlers, the plan's Task 3

// errNotImplemented answers the contract's feed and subscription operations
// until their handlers land (the plan's Tasks 3 and 4): the strict server
// must implement every operation to compile.
var errNotImplemented = errors.New("calendar: not implemented yet")

// GetCalendarFeed answers the caller's calendar addresses.
func (s *server) GetCalendarFeed(context.Context, gen.GetCalendarFeedRequestObject) (gen.GetCalendarFeedResponseObject, error) {
	return nil, errNotImplemented
}

// PostCalendarFeedByVariant creates or resets the caller's address of one
// variant.
func (s *server) PostCalendarFeedByVariant(context.Context, gen.PostCalendarFeedByVariantRequestObject) (gen.PostCalendarFeedByVariantResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteCalendarFeedByVariant turns the caller's address of one variant off.
func (s *server) DeleteCalendarFeedByVariant(context.Context, gen.DeleteCalendarFeedByVariantRequestObject) (gen.DeleteCalendarFeedByVariantResponseObject, error) {
	return nil, errNotImplemented
}
