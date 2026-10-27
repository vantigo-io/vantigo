package staff

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// People, Capacity and Self are the window reads of contracts.Workforce
// (planning design S8): the people in employment over a window, the hours a
// week and the days away each one's terms and absence answer, and the
// caller's own person. People and Capacity apply the Colleague rule widened
// to the window — staff:view, and the person in employment on today or on a
// later day of the window — and Self the Self rule. Every figure is derived:
// never a terms row, an absence type, a share of a day or a status.

// workforceWindow is the window People and Capacity are asked about, checked
// against the bound, with the business zone's today.
type workforceWindow struct {
	today, from, to time.Time
}

// window checks from..to against the bound (planning design S8): from no
// earlier than the Monday of the week thirteen weeks before today, to no
// later than three years after today, and to not before from; anything else
// is contracts.ErrWindowOutOfBounds. Both ends are read as civil dates.
func (w *workforce) window(ctx context.Context, from, to time.Time) (workforceWindow, error) {
	zone, err := w.calendar.Zone(ctx)
	if err != nil {
		return workforceWindow{}, fmt.Errorf("staff: read the business time zone: %w", err)
	}
	today := civilToday(w.clock(), zone)
	win := workforceWindow{today: today, from: civilDay(from), to: civilDay(to)}
	earliest := weekStartOf(today).AddDate(0, 0, -7*13)
	latest := addMonths(today, 36)
	if win.from.Before(earliest) || win.to.After(latest) || win.to.Before(win.from) {
		return workforceWindow{}, contracts.ErrWindowOutOfBounds
	}
	return win, nil
}

// civilDay is t's calendar date at midnight UTC, as every contract carries
// a civil date.
func civilDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// visibleIn is whether a person with employments is one the Colleague rule
// shows over win: in employment today, or on a day of the window from today
// on. A leaver whose last day was before today is not, whatever the window.
func visibleIn(employments []store.StaffEmployment, win workforceWindow) bool {
	lo := later(win.from, win.today)
	for _, e := range employments {
		if inForce(e, win.today) {
			return true
		}
		if !lo.After(win.to) && !civil(e.StartDate).After(win.to) && (!e.EndDate.Valid || !e.EndDate.Time.Before(lo)) {
			return true
		}
	}
	return false
}

// employedSpans is the days of from..to the employments are in force,
// merged into non-overlapping spans in order: two concurrent employments, or
// one that starts the day after another ends, read as one run.
func employedSpans(employments []store.StaffEmployment, from, to time.Time) []contracts.DateSpan {
	var spans []contracts.DateSpan
	for _, e := range employments {
		first, last := later(civil(e.StartDate), from), to
		if e.EndDate.Valid {
			last = earlier(e.EndDate.Time, to)
		}
		if !first.After(last) {
			spans = append(spans, contracts.DateSpan{From: first, To: last})
		}
	}
	slices.SortFunc(spans, func(a, b contracts.DateSpan) int { return a.From.Compare(b.From) })
	var out []contracts.DateSpan
	for _, s := range spans {
		if n := len(out); n > 0 && !s.From.After(out[n-1].To.AddDate(0, 0, 1)) {
			out[n-1].To = later(out[n-1].To, s.To)
			continue
		}
		out = append(out, s)
	}
	return out
}

// placeDay is the day a person's department is read on: their first day in
// employment inside the window from today on, or today when there is none —
// a window wholly past, read for someone in employment today.
func placeDay(employed []contracts.DateSpan, win workforceWindow) time.Time {
	lo := later(win.from, win.today)
	for _, s := range employed {
		if !s.To.Before(lo) {
			return later(s.From, lo)
		}
	}
	return win.today
}

// departmentOn is the department the terms in force on day of the person's
// place name, nil when no employment is in force or its terms name none.
func departmentOn(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, day time.Time) *int64 {
	if t := placeTermsOf(employments, chains, day); t != nil {
		return t.DepartmentID
	}
	return nil
}

// capacitySegments is the hours a week in force over from..to: on each day
// in employment the full-time hours times the percentage of every
// employment's terms in force, summed (expectedWeekOf), runs of one figure
// merged. A day in no employment has no segment. The figure can change only
// where an employment starts or ends or a terms row takes effect, so the
// window is cut there rather than walked day by day.
func capacitySegments(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, from, to time.Time) []contracts.CapacitySegment {
	points := []time.Time{from}
	cut := func(d time.Time) {
		if d.After(from) && !d.After(to) {
			points = append(points, d)
		}
	}
	for _, e := range employments {
		cut(civil(e.StartDate))
		if e.EndDate.Valid {
			cut(e.EndDate.Time.AddDate(0, 0, 1))
		}
		for _, t := range chains[e.ID] {
			cut(civil(t.ValidFrom))
		}
	}
	slices.SortFunc(points, time.Time.Compare)
	points = slices.CompactFunc(points, time.Time.Equal)
	var out []contracts.CapacitySegment
	for i, p := range points {
		last := to
		if i+1 < len(points) {
			last = points[i+1].AddDate(0, 0, -1)
		}
		if !slices.ContainsFunc(employments, func(e store.StaffEmployment) bool { return inForce(e, p) }) {
			continue
		}
		hours := 0.0
		if h := expectedWeekOf(employments, chains, p); h != nil {
			hours = *h
		}
		if n := len(out); n > 0 && out[n-1].HoursPerWeek == hours && out[n-1].To.AddDate(0, 0, 1).Equal(p) {
			out[n-1].To = last
			continue
		}
		out = append(out, contracts.CapacitySegment{From: p, To: last, HoursPerWeek: hours})
	}
	return out
}

// windowPeople is the people the Colleague rule shows over win, with their
// employments — of personIDs' alone when it is not nil — by id.
func (w *workforce) windowPeople(ctx context.Context, win workforceWindow, personIDs []int64) (map[int64][]store.StaffEmployment, []int64, error) {
	rows, err := w.q.WorkforceEmployments(ctx, store.WorkforceEmploymentsParams{
		FromDay: dayParam(win.from), ToDay: dayParam(win.to), PersonIds: personIDs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("staff: read the employments of the window: %w", err)
	}
	byPerson := map[int64][]store.StaffEmployment{}
	for _, e := range rows {
		byPerson[e.PersonID] = append(byPerson[e.PersonID], e)
	}
	ids := make([]int64, 0, len(byPerson))
	for id, employments := range byPerson {
		if visibleIn(employments, win) {
			ids = append(ids, id)
		} else {
			delete(byPerson, id)
		}
	}
	slices.Sort(ids)
	return byPerson, ids, nil
}

// termsChains is the terms chains of employments, by employment, each oldest
// first.
func (w *workforce) termsChains(ctx context.Context, byPerson map[int64][]store.StaffEmployment) (map[int64][]store.StaffEmploymentTerm, error) {
	var ids []int64
	for _, employments := range byPerson {
		for _, e := range employments {
			ids = append(ids, e.ID)
		}
	}
	chains := map[int64][]store.StaffEmploymentTerm{}
	if len(ids) == 0 {
		return chains, nil
	}
	slices.Sort(ids)
	rows, err := w.q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the terms of the window: %w", err)
	}
	for _, r := range rows {
		chains[r.EmploymentID] = append(chains[r.EmploymentID], r)
	}
	return chains, nil
}

// entries is the work group of people — the name, the employee number and
// the department each one's place names on the day placeOn answers — and
// the user each one's self-service link names under PersonForUser's rule
// (PeopleLinks), by display name then id.
func (w *workforce) entries(ctx context.Context, ids []int64, placeOn func(personID int64) *int64) ([]contracts.PersonEntry, error) {
	summaries, err := w.q.PeopleSummaries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the people: %w", err)
	}
	links, err := w.q.PeopleLinks(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the people's links: %w", err)
	}
	users := make(map[int64]uuid.UUID, len(links))
	for _, l := range links {
		if l.ExternalID == nil {
			continue
		}
		if u, err := uuid.Parse(*l.ExternalID); err == nil {
			users[l.PersonID] = u
		}
	}
	out := make([]contracts.PersonEntry, 0, len(summaries))
	departments := map[int64]bool{}
	for _, s := range summaries {
		entry := contracts.PersonEntry{ID: s.ID, DisplayName: s.DisplayName, EmployeeNumber: s.EmployeeNumber, DepartmentID: placeOn(s.ID)}
		if u, ok := users[s.ID]; ok {
			entry.UserID = &u
		}
		if entry.DepartmentID != nil {
			departments[*entry.DepartmentID] = true
		}
		out = append(out, entry)
	}
	if len(departments) > 0 {
		rows, err := w.q.DepartmentsByID(ctx, slices.Sorted(maps.Keys(departments)))
		if err != nil {
			return nil, fmt.Errorf("staff: read the people's departments: %w", err)
		}
		names := make(map[int64]string, len(rows))
		for _, d := range rows {
			names[d.ID] = d.Name
		}
		for i := range out {
			if out[i].DepartmentID != nil {
				out[i].DepartmentName = names[*out[i].DepartmentID]
			}
		}
	}
	slices.SortFunc(out, func(a, b contracts.PersonEntry) int {
		return cmp.Or(cmp.Compare(a.DisplayName, b.DisplayName), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// People is, for staff:view, everyone the Colleague rule shows over
// from..to — in employment on today or on a later day of the window — and
// in employment on some day of it: the work group, the place on today or
// the first day in employment inside the window when that is later, the
// user a link giving Self names, and the days of the window in employment.
// Nothing otherwise; a window outside the bound is ErrWindowOutOfBounds.
func (w *workforce) People(ctx context.Context, from, to time.Time) ([]contracts.PersonEntry, error) {
	if !contracts.HasPermission(ctx, w.access, permView) {
		return nil, nil
	}
	win, err := w.window(ctx, from, to)
	if err != nil {
		return nil, err
	}
	byPerson, ids, err := w.windowPeople(ctx, win, nil)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []contracts.PersonEntry{}, nil
	}
	chains, err := w.termsChains(ctx, byPerson)
	if err != nil {
		return nil, err
	}
	employed := make(map[int64][]contracts.DateSpan, len(ids))
	for _, id := range ids {
		employed[id] = employedSpans(byPerson[id], win.from, win.to)
	}
	out, err := w.entries(ctx, ids, func(id int64) *int64 {
		return departmentOn(byPerson[id], chains, placeDay(employed[id], win))
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Employed = employed[out[i].ID]
	}
	return out, nil
}

// Capacity is, for staff:view, each of personIDs the Colleague rule shows
// over from..to with the hours a week in force over the window and the
// days away: the approved and registered day rows of any type, coalesced
// over the person's whole absence and then clipped (colleagueSpans) — no
// share, no hours, no type, no status, and no hour row. Nothing otherwise;
// a window outside the bound is ErrWindowOutOfBounds.
func (w *workforce) Capacity(ctx context.Context, from, to time.Time, personIDs []int64) (map[int64]contracts.PersonCapacity, error) {
	if !contracts.HasPermission(ctx, w.access, permView) {
		return nil, nil
	}
	win, err := w.window(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := map[int64]contracts.PersonCapacity{}
	if len(personIDs) == 0 {
		return out, nil
	}
	byPerson, ids, err := w.windowPeople(ctx, win, personIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	chains, err := w.termsChains(ctx, byPerson)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = contracts.PersonCapacity{Segments: capacitySegments(byPerson[id], chains, win.from, win.to)}
	}
	spans, err := colleagueSpans(ctx, w.q, ids, dayParam(win.from), dayParam(win.to))
	if err != nil {
		return nil, err
	}
	for _, s := range spans {
		c := out[s.PersonId]
		c.Away = append(c.Away, contracts.DateSpan{From: s.FirstDay.Time, To: s.LastDay.Time})
		out[s.PersonId] = c
	}
	return out, nil
}

// Self is, for staff:access, the caller's own person under the Self rule —
// the one whose self-service link names the caller (personForUser: a worker
// link blocked mismatch or account_not_managed gives nothing) — with the
// work group and the place today, Employed empty; nil for anyone else.
func (w *workforce) Self(ctx context.Context) (*contracts.PersonEntry, error) {
	if !contracts.HasPermission(ctx, w.access, "staff:access") {
		return nil, nil
	}
	user := callerID(ctx)
	if user == uuid.Nil {
		return nil, nil
	}
	person, err := personForUser(ctx, w.q, user)
	if err != nil || person == nil {
		return nil, err
	}
	zone, err := w.calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the business time zone: %w", err)
	}
	today := civilToday(w.clock(), zone)
	employments, err := w.q.ListPersonEmployments(ctx, *person)
	if err != nil {
		return nil, fmt.Errorf("staff: read the person's employments: %w", err)
	}
	chains, err := w.termsChains(ctx, map[int64][]store.StaffEmployment{*person: employments})
	if err != nil {
		return nil, err
	}
	out, err := w.entries(ctx, []int64{*person}, func(int64) *int64 { return departmentOn(employments, chains, today) })
	if err != nil || len(out) == 0 {
		return nil, err
	}
	self := out[0]
	self.UserID = &user
	return &self, nil
}
