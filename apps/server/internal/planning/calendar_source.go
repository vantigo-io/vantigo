package planning

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// calendarKindAllocation is the one kind this module projects onto the
// calendar (design S24).
const calendarKindAllocation = "planning.allocation"

// calendarSource is this module's contracts.CalendarSource (S24): the
// caller's own confirmed allocations touching the window, all day over their
// period, titled with the project's code and name and subtitled with the
// load. It is its own gatekeeper: planning:access and a person — the one
// Staff's Self answers the caller, which it does under contracts.AsUser for
// the feed as for a request — without either it answers nothing, never an
// error. No tentative row and no colleague's row is projected. It runs in
// no transaction of the module's, so the contract reads are allowed.
type calendarSource struct {
	srv *server
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource: nil — contributing nothing —
// unless the calendar module is on (S25), since Planning does not require
// it and nothing would read the source.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	if d.Config == nil || !slices.Contains(d.Config.Modules, "calendar") {
		return nil
	}
	return &calendarSource{srv: newServer(d)}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: calendarKindAllocation, Module: "planning"}}
}

// CalendarItems answers the caller's own confirmed allocations touching
// q.From..q.To, oldest first. A project the directory does not resolve is
// named by its id (rule 4); the load is in figures — "50 %", "20 h" —
// since the feed knows no language of its reader (plan reading 7).
func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermission(ctx, s.srv.deps.Access, "planning:access") {
		return nil, nil
	}
	self, err := s.srv.workforceSelf(ctx)
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, nil
	}
	rows, err := store.New(s.srv.deps.Pool).PersonConfirmedAllocationsInWindow(ctx, store.PersonConfirmedAllocationsInWindowParams{
		PersonID: self.ID, FromDate: dayParam(q.From), ToDate: dayParam(q.To),
	})
	if err != nil {
		return nil, fmt.Errorf("planning: calendar allocations of person %d: %w", self.ID, err)
	}
	projects, err := s.srv.projectsOfRows(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]contracts.CalendarItem, 0, len(rows))
	for _, a := range rows {
		var project *contracts.ProjectEntry
		if e, ok := projects[a.ProjectID]; ok {
			project = &e
		}
		items = append(items, contracts.CalendarItem{
			Kind:      calendarKindAllocation,
			ID:        strconv.FormatInt(a.ID, 10),
			Title:     noticeEnglish.projectNamed(a.ProjectID, project),
			Subtitle:  calendarLoad(a),
			AllDay:    true,
			StartDate: civil(a.FromDate),
			EndDate:   civil(a.ToDate),
			Path:      myPlanningPage,
		})
	}
	return items, nil
}

// calendarLoad is a row's load in figures: "50 %" or "20 h", the decimals
// as typed.
func calendarLoad(a store.PlanningAllocation) string {
	v := strconv.FormatFloat(loadOf(a.Load), 'f', -1, 64)
	if a.LoadUnit == loadUnitHours {
		return v + " h"
	}
	return v + " %"
}
