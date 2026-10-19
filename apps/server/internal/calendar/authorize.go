package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// This file is the whole of who the caller is (calendar design D3). The
// router has already enforced calendar:access on every operation, and
// calendar:view-colleagues beside it on a colleague's calendar, so reaching a
// handler means the caller holds what its operation needs. What is left is
// per row: the owner or an attendee reads an appointment, the owner writes
// it, an attendee answers it — anyone else gets the 404 an unknown id gets.

// caller is what one request knows about the person making it, read once.
type caller struct {
	UserID uuid.UUID
	// CanViewColleagues is calendar:view-colleagues, for GET /meta.
	CanViewColleagues bool
	// loc is the workspace's business time zone, read once per request
	// through the business calendar — never inside a transaction.
	loc *time.Location
}

// callerFor reads the request's caller: the principal, whether they may see
// colleagues' calendars, and the business zone.
func (s *server) callerFor(ctx context.Context) (*caller, error) {
	loc, err := s.calendarZone(ctx)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the business time zone: %w", err)
	}
	return &caller{
		UserID:            callerID(ctx),
		CanViewColleagues: s.has(ctx, "calendar:view-colleagues"),
		loc:               loc,
	}, nil
}

// zone is the workspace's business time zone, read when the caller was built.
func (c *caller) zone() *time.Location {
	if c.loc == nil {
		return time.UTC
	}
	return c.loc
}
