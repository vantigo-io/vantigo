package planning

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is who the caller is to this module (design §5). Handlers build
// one callerContext per request and ask it; none of them consults a
// permission on its own, so there is one place where "what may this caller
// see and do" starts.

// The permission keys this module decides on beyond planning:access, which
// the router enforces on every operation.
const (
	permView   = "planning:view"
	permManage = "planning:manage"
)

// callerContext is what one request knows about the person making it, read
// once, before any transaction: their user id, the permissions this module
// decides on, and today's calendar day in the business time zone.
// planning:access is not here: the router enforced it on every operation, so
// reaching a handler means the caller holds it.
type callerContext struct {
	UserID uuid.UUID

	View   bool // planning:view — the board, the plans, the timelines, the stats
	Manage bool // planning:manage — the writes, the notes, the settings

	// Zone is the workspace's business time zone (contracts.BusinessCalendar,
	// read once per request), and Today the calendar day of the request's
	// clock reading in it, as a date at midnight UTC (rule 1). Every rule of
	// one request judges "today" by this one reading.
	Zone  *time.Location
	Today time.Time

	// self is the caller's own person as Staff answers it (Workforce.Self),
	// read the first time a handler asks (callerSelf) and kept for the rest
	// of the request; selfRead says it has been read, since nil is an answer.
	self     *contracts.PersonEntry
	selfRead bool
}

// callerFor reads the request's caller: the permissions and today. It is
// never called inside a transaction, since the permission checks read
// identity's tables through the access layer's own connection and the zone
// is a directory read.
func (s *server) callerFor(ctx context.Context) (*callerContext, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	return &callerContext{
		UserID: callerID(ctx),
		View:   s.has(ctx, permView),
		Manage: s.has(ctx, permManage),
		Zone:   zone,
		Today:  civilToday(s.deps.Clock(), zone),
	}, nil
}

// callerSelf is the caller's own person — the one Staff's Self rule gives
// them (S8, S19) — or nil when Staff answers none: a user linked to nobody, or
// through a link Staff blocks. The module never restates the rule. It is read
// once per request, before any transaction.
func (s *server) callerSelf(ctx context.Context, c *callerContext) (*contracts.PersonEntry, error) {
	if c.selfRead {
		return c.self, nil
	}
	self, err := s.workforceSelf(ctx)
	if err != nil {
		return nil, err
	}
	c.self, c.selfRead = self, true
	return self, nil
}

// businessZone is the workspace's business time zone, read through the
// business calendar (time zones design D2) — never inside one of this
// module's transactions, since the read takes a pool connection of its own.
// A stored name Go cannot load is answered as UTC by the calendar itself.
func (s *server) businessZone(ctx context.Context) (*time.Location, error) {
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	zone, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning: read the business time zone: %w", err)
	}
	return zone, nil
}
