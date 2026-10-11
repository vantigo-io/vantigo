package staff

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is who the caller is to this module (design §5). Handlers build
// one callerContext per request and ask it; none of them consults a
// permission on its own, so there is one place where "what may this caller
// see and do" starts.

// The permission keys this module decides on beyond staff:access, which the
// router enforces on every operation.
const (
	permView         = "staff:view"
	permApprove      = "staff:approve"
	permManage       = "staff:manage"
	permPersonalData = "staff:personal-data"
)

// callerContext is what one request knows about the person making it, read
// once: their user id, the permissions this module decides on, the
// installation's settings, and today's calendar day in the business time
// zone. staff:access is not here: the router enforced it on every operation,
// so reaching a handler means the caller holds it.
type callerContext struct {
	UserID uuid.UUID

	View         bool // staff:view — the directory and the structure
	Approve      bool // staff:approve — decides absence (delivery B); no A1 audience (plan reading 18)
	Manage       bool // staff:manage — HR
	PersonalData bool // staff:personal-data — the sealed fields, the erase

	// Person is the person the caller is, through their self-service link —
	// the Self audience on that person — and nil when they are linked to
	// nobody.
	Person *int64

	Settings store.StaffSetting

	// Zone is Settings.TimeZone parsed once, and Today the calendar day of the
	// request's clock reading in it, as a date at midnight UTC — the value a
	// date parameter carries to SQL. Every rule of one request judges "today"
	// by this one reading, never by SQL's current_date, which is the
	// database session's day in the database's own zone.
	Zone  *time.Location
	Today time.Time
}

// callerFor reads the request's caller: the permissions, the person they are
// linked to, the settings and today. It reads on q, which is the pool for
// every handler today; it is never called inside a transaction, since the
// permission checks read identity's tables through the access layer's own
// connection.
func (s *server) callerFor(ctx context.Context, q *store.Queries) (*callerContext, error) {
	row, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	userID := callerID(ctx)
	person, err := personForUser(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	zone := zoneOf(row)
	return &callerContext{
		UserID:       userID,
		View:         s.has(ctx, permView),
		Approve:      s.has(ctx, permApprove),
		Manage:       s.has(ctx, permManage),
		PersonalData: s.has(ctx, permPersonalData),
		Person:       person,
		Settings:     row,
		Zone:         zone,
		Today:        civilToday(s.deps.Clock(), zone),
	}, nil
}

// audience is who the caller is to one person (design §5): any of the
// columns of the field group × audience matrix at once — HR reading their own
// record is both Self and staff:manage, and reads what either may. What each
// may read is shaping.go's table; this is only who they are. staff:approve
// is no audience in delivery A1 (plan reading 18).
type audience struct {
	Self         bool // the caller's self-service link names the person
	Manager      bool // a manager of the person today, by the terms in force
	Colleague    bool // staff:view
	Manage       bool // staff:manage
	PersonalData bool // staff:personal-data
}

// audienceOn is the caller's audience on personID. manager is whether the
// caller manages the person today, which managesPerson answers; active is
// whether the person is active today, without which staff:view is no
// audience on them (plan reading 15: an unannounced hire or a leaver stays
// out of the colleague directory, and ids are sequential).
func (c *callerContext) audienceOn(personID int64, manager, active bool) audience {
	return audience{
		Self:         c.Person != nil && *c.Person == personID,
		Manager:      manager,
		Colleague:    c.View && active,
		Manage:       c.Manage,
		PersonalData: c.PersonalData,
	}
}

// personAudience resolves the caller's audience on personID on q: whether
// they manage the person today, and — for staff:view — whether the person is
// active today in the business zone.
func personAudience(ctx context.Context, q *store.Queries, c *callerContext, personID int64) (audience, error) {
	manager, err := managesPerson(ctx, q, c, personID)
	if err != nil {
		return audience{}, err
	}
	active := false
	if c.View {
		if active, err = q.PersonActiveOn(ctx, store.PersonActiveOnParams{
			PersonID: personID, Today: pgtype.Date{Time: c.Today, Valid: true},
		}); err != nil {
			return audience{}, fmt.Errorf("staff: is person %d active today: %w", personID, err)
		}
	}
	return c.audienceOn(personID, manager, active), nil
}

// managesPerson reports whether the caller is a manager of personID today
// (design §5, plan reading 6): the person the caller's self-service link
// names is a manager of personID by the terms in force today — the manager
// they name, or the department's — on any of personID's employments, since
// concurrent employments may give two. A caller linked to nobody manages
// nobody.
func managesPerson(ctx context.Context, q *store.Queries, c *callerContext, personID int64) (bool, error) {
	if c.Person == nil {
		return false, nil
	}
	g, err := loadManagerGraph(ctx, q, []int64{personID})
	if err != nil {
		return false, err
	}
	return slices.Contains(g.managerOf(personID, c.Today), *c.Person), nil
}

// civilToday is the calendar day of now in zone, as a date at midnight UTC.
// It is Expenses' derivation (internal/expenses/authorize.go, caller.zone and
// businessDay): Go answers the zone's day, and SQL is handed the day itself.
func civilToday(now time.Time, zone *time.Location) time.Time {
	y, m, d := now.In(zone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// zoneOf parses a settings row's business time zone. A name Go cannot load
// falls back to UTC rather than failing the request: PUT /settings refuses a
// name Go cannot load and one the database reads differently, so a row that
// holds one was written past this module — by hand, or by a Go release that
// dropped a zone — and answering a day in UTC is better than answering
// nothing. Setting the zone again through PUT /settings is the way back.
func zoneOf(row store.StaffSetting) *time.Location {
	loc, err := time.LoadLocation(row.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// personForUser is the audience resolver's link lookup (design S24, plan
// reading 1): the person whose self-service link names userID under the
// identity provider, whatever state the link is in — delivery C's states
// (pending, disabled, failed …) never take the Self audience away from the
// person — and nil when nobody is linked.
func personForUser(ctx context.Context, q *store.Queries, userID uuid.UUID) (*int64, error) {
	id, err := q.PersonForUser(ctx, userID.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read the person linked to user %s: %w", userID, err)
	}
	return &id, nil
}
