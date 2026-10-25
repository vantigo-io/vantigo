package staff

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the glance's hours (person-glance design S2 and §4's hours
// row): the week the card shows, the hours the contract expects in it, and
// the one read from time through contracts.Timesheets, made with the
// request's context before the glance's snapshot opens. The card is the
// Hours group's; time disabled is the section {timeTracking: false}, never a
// zero.

// weekStartOf is the Monday on or before today — this business week's first
// day, since today is already the business zone's calendar day at midnight
// UTC — as the date contracts.Timesheets is asked about.
func weekStartOf(today time.Time) time.Time {
	back := (int(today.Weekday()) + 6) % 7
	return today.AddDate(0, 0, -back)
}

// quarterHours is hours rounded to the nearest quarter of an hour.
func quarterHours(hours float64) float64 { return math.Round(hours*4) / 4 }

// expectedHours is the week the terms rows in force agree, summed over them
// — each the full-time hours per week times the percentage — and rounded to
// a quarter once summed (design reading 5: the contract's week, not reduced
// by absence); nil without terms in force.
func expectedHours(terms []*store.StaffEmploymentTerm) *float64 {
	if len(terms) == 0 {
		return nil
	}
	var sum float64
	for _, t := range terms {
		sum += numberOf(t.FullTimeHoursPerWeek) * numberOf(t.FtePercent) / 100
	}
	v := quarterHours(sum)
	return &v
}

// glanceWeek is the linked user's hours in the week from weekStart, read
// from time outside any transaction — nil when the audience does not read
// the hours, time is disabled, or the person is linked to no user. A user
// with no entry in the week logged nothing: zero, not absent.
func (s *server) glanceWeek(ctx context.Context, a audience, weekStart time.Time, link *string) (*contracts.WeekHours, error) {
	if !a.reads(glanceGroupOf("hours")) || s.deps.Timesheets == nil || link == nil {
		return nil, nil
	}
	user, err := uuid.Parse(*link)
	if err != nil {
		return nil, fmt.Errorf("staff: a self-service link names %q, which is no user id: %w", *link, err)
	}
	hours, err := s.timesheetsWeek(ctx, weekStart, []uuid.UUID{user})
	if err != nil {
		return nil, fmt.Errorf("staff: read the week's hours: %w", err)
	}
	week := hours[user]
	return &week, nil
}

// hoursSection is the hours card: off when time is disabled; else the week,
// the expected hours (expectedWeekOf, nil when no terms are in force), and —
// when the person has a linked user — what they logged and what was
// approved.
func hoursSection(timeTracking bool, weekStart time.Time, expected *float64, week *contracts.WeekHours) *gen.StaffGlanceHours {
	if !timeTracking {
		return &gen.StaffGlanceHours{TimeTracking: false}
	}
	out := &gen.StaffGlanceHours{
		TimeTracking: true,
		WeekStart:    &openapi_types.Date{Time: weekStart},
		Expected:     expected,
	}
	if week != nil {
		logged, approved := week.Logged, week.Approved
		out.Logged, out.Approved = &logged, &approved
	}
	return out
}
