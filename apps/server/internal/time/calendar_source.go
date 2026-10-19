package timetracking

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// calendarKindEntry is the one kind this module projects onto the calendar
// (calendar design D1).
const calendarKindEntry = "time.entry"

// calendarFallbackTitle names an entry whose project the directory cannot
// resolve and that carries neither a task's title nor a note.
const calendarFallbackTitle = "Time"

// calendarSource is this module's contracts.CalendarSource (calendar design
// D1): the caller's own entries. It is its own gatekeeper: time:access, the
// key that already reads a person's own entries; time:view-all widens
// nothing, every row being the caller's. Without the key it answers nothing,
// not an error.
type calendarSource struct {
	srv *server
	q   *store.Queries
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	return &calendarSource{srv: newServer(d), q: store.New(d.Pool)}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: calendarKindEntry, Module: "time"}}
}

// CalendarItems answers each entry as a block when it carries both a start and
// an end: a wall-clock pair the person typed, read in the workspace's business
// zone — the zone a timesheet's day is the business's day in — and so read
// once, before the rows. A pair whose end is not after its start, or an entry
// with only hours, is all-day on its date. The title is the project's name,
// which the project directory holds (the timesheet names entries the same
// way, responses.go), else the task's title, else the note's first line.
func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermission(ctx, s.srv.deps.Access, "time:access") {
		return nil, nil
	}
	zone, err := s.srv.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.CalendarEntries(ctx, store.CalendarEntriesParams{UserID: q.UserID, FromDay: pgDate(q.From), ToDay: pgDate(q.To)})
	if err != nil {
		return nil, fmt.Errorf("time: calendar entries: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	names, err := s.projectNames(ctx, rows)
	if err != nil {
		return nil, err
	}

	items := make([]contracts.CalendarItem, 0, len(rows))
	for _, r := range rows {
		hours, err := floatPtrFromNumeric(r.Hours)
		if err != nil {
			return nil, err
		}
		item := contracts.CalendarItem{
			Kind:  calendarKindEntry,
			ID:    strconv.FormatInt(r.ID, 10),
			Title: calendarEntryTitle(r, names),
			Done:  r.Status == statusApproved,
			Path:  "/time/day?date=" + r.EntryDate.Time.Format(time.DateOnly),
		}
		if hours != nil {
			item.Subtitle = strconv.FormatFloat(*hours, 'f', -1, 64) + " h"
		}
		if starts, ends, ok := calendarEntryBlock(r.EntryDate.Time, r.StartTime, r.EndTime, zone); ok {
			item.StartsAt, item.EndsAt = starts, ends
		} else {
			item.AllDay, item.StartDate, item.EndDate = true, r.EntryDate.Time, r.EntryDate.Time
		}
		items = append(items, item)
	}
	return items, nil
}

// projectNames is the name of every project the rows are on, in one directory
// call. A module installed without the directory names none, and the entries
// fall back to their own words.
func (s *calendarSource) projectNames(ctx context.Context, rows []store.CalendarEntriesRow) (map[int32]string, error) {
	names := map[int32]string{}
	if s.srv.deps.Projects == nil {
		return names, nil
	}
	seen := map[int32]bool{}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		if !seen[r.ProjectID] {
			seen[r.ProjectID] = true
			ids = append(ids, r.ProjectID)
		}
	}
	found, err := s.srv.deps.Projects.Projects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: resolve the calendar entries' projects: %w", err)
	}
	for _, p := range found {
		names[p.ID] = p.Name
	}
	return names, nil
}

// calendarEntryBlock is the instants of the wall-clock pair start..end on day
// in zone, when both are set and the end comes after the start.
func calendarEntryBlock(day time.Time, start, end pgtype.Time, zone *time.Location) (time.Time, time.Time, bool) {
	if !start.Valid || !end.Valid || end.Microseconds <= start.Microseconds {
		return time.Time{}, time.Time{}, false
	}
	at := func(t pgtype.Time) time.Time {
		clock := time.Duration(t.Microseconds) * time.Microsecond
		return time.Date(day.Year(), day.Month(), day.Day(),
			int(clock/time.Hour), int(clock%time.Hour/time.Minute), int(clock%time.Minute/time.Second), 0, zone).UTC()
	}
	return at(start), at(end), true
}

func calendarEntryTitle(r store.CalendarEntriesRow, names map[int32]string) string {
	if name, ok := names[r.ProjectID]; ok && name != "" {
		return name
	}
	if r.TaskTitle != nil && strings.TrimSpace(*r.TaskTitle) != "" {
		return *r.TaskTitle
	}
	if r.Note != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(*r.Note), "\n"); strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return calendarFallbackTitle
}
