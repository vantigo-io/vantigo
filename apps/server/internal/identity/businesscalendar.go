package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// defaultBusinessTimeZone is the zone an installation runs on until an Owner
// sets another: the column's default (00052), and what a missing singleton
// row means.
const defaultBusinessTimeZone = "Europe/Oslo"

// businessCalendar is this module's contracts.BusinessCalendar (time zones
// design D2): the workspace's business time zone, read from the system
// settings singleton on every call, and the two derivations of a day from
// an instant every module made for itself until now. Compose builds it
// once, before any module mounts, and hands it to every module including
// this one; Workers does the same in worker mode.
type businessCalendar struct {
	q *store.Queries
}

var _ contracts.BusinessCalendar = (*businessCalendar)(nil)

// newBusinessCalendar is Module's Calendar: the constructor Compose and
// Workers call with the dependencies they were given.
func newBusinessCalendar(d module.Deps) contracts.BusinessCalendar {
	return &businessCalendar{q: store.New(d.Pool)}
}

// zoneName is the stored name, the default when no row holds one yet.
func (c *businessCalendar) zoneName(ctx context.Context) (string, error) {
	name, err := c.q.GetBusinessTimeZone(ctx)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && name == nil {
		return defaultBusinessTimeZone, nil
	}
	if err != nil {
		return "", fmt.Errorf("identity: read the business time zone: %w", err)
	}
	return *name, nil
}

// Zone is the business time zone. A stored name Go cannot load answers UTC
// rather than failing the request: PUT /owner/workspace refuses a name Go
// cannot load and one the database reads differently, so a row holding one
// was written past this module — by hand, or by a Go release that dropped a
// zone — and a day in UTC is better than no answer. Setting the zone again
// is the way back.
func (c *businessCalendar) Zone(ctx context.Context) (*time.Location, error) {
	name, err := c.zoneName(ctx)
	if err != nil {
		return nil, err
	}
	return locationOrUTC(name), nil
}

// locationOrUTC is name as a location, UTC when Go cannot load it.
func locationOrUTC(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Today is the business calendar day of now, at midnight UTC.
func (c *businessCalendar) Today(ctx context.Context, now time.Time) (time.Time, error) {
	return c.Day(ctx, now)
}

// Day is the business calendar day t falls on, at midnight UTC.
func (c *businessCalendar) Day(ctx context.Context, t time.Time) (time.Time, error) {
	loc, err := c.Zone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return holidays.DayIn(t, loc), nil
}
