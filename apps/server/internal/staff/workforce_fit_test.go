package staff_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Workforce.PreferenceFits (planning design S17.6, reading 6): for
// staff:view, each person the Colleague rule shows over the booking's
// period, scored from 100 less weight × 10 per preference the booking
// contradicts, floored at 0, with the kinds lowered in the catalogue's order.
// The harness's today is Saturday 12 September 2026.

// preferenceRow is one stored preference: its kind, its canonical value and
// its weight.
type preferenceRow struct {
	kind   string
	value  string
	weight int
}

// setPreferences replaces personID's preferences with rows, written as the
// one-to-one replace stores them.
func setPreferences(t *testing.T, h *harness, personID int64, rows ...preferenceRow) {
	t.Helper()
	h.Exec(t, `DELETE FROM staff.person_preferences WHERE person_id = $1`, personID)
	for _, r := range rows {
		h.Exec(t, `INSERT INTO staff.person_preferences (person_id, kind, value, weight, created_at, updated_at)
			VALUES ($1, $2, $3::jsonb, $4, $5, $5)`, personID, r.kind, r.value, r.weight, h.Now())
	}
}

// booking is a booking over 14 September to 30 October 2026, inside the
// bound and Anne's employment, with the facets set.
func booking(set func(*contracts.Booking)) contracts.Booking {
	b := contracts.Booking{From: calDay(2026, 9, 14), To: calDay(2026, 10, 30)}
	if set != nil {
		set(&b)
	}
	return b
}

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }
func int64Ptr(v int64) *int64       { return &v }

func TestWorkforce_PreferenceFitsPerKind(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	h := w.h
	for _, tc := range []struct {
		kind, value string
		contradict  map[string]func(*contracts.Booking)
		fit         map[string]func(*contracts.Booking)
	}{
		{"weekdays", `{"days": [1, 2, 3, 4, 5]}`,
			map[string]func(*contracts.Booking){"a Saturday among the days": func(b *contracts.Booking) { b.Weekdays = []int{5, 6} }},
			map[string]func(*contracts.Booking){"preferred days alone": func(b *contracts.Booking) { b.Weekdays = []int{1, 2} }}},
		{"time_of_day", `{"parts": ["morning", "day"]}`,
			map[string]func(*contracts.Booking){"an evening among the parts": func(b *contracts.Booking) { b.Parts = []string{"day", "evening"} }},
			map[string]func(*contracts.Booking){"a preferred part alone": func(b *contracts.Booking) { b.Parts = []string{"day"} }}},
		{"location", `{"locationIds": [11, 12]}`,
			map[string]func(*contracts.Booking){"another location": func(b *contracts.Booking) { b.LocationID = int64Ptr(13) }},
			map[string]func(*contracts.Booking){"a preferred location": func(b *contracts.Booking) { b.LocationID = int64Ptr(12) }}},
		{"travel", `{"extent": "regional"}`,
			map[string]func(*contracts.Booking){
				"national":      func(b *contracts.Booking) { b.Travel = "national" },
				"international": func(b *contracts.Booking) { b.Travel = "international" },
			},
			map[string]func(*contracts.Booking){
				"regional": func(b *contracts.Booking) { b.Travel = "regional" },
				"none":     func(b *contracts.Booking) { b.Travel = "none" },
			}},
		// Any share other than the preferred contradicts, less remote work
		// as well as more.
		{"remote", `{"share": "some"}`,
			map[string]func(*contracts.Booking){
				"mostly": func(b *contracts.Booking) { b.Remote = "mostly" },
				"never":  func(b *contracts.Booking) { b.Remote = "never" },
			},
			map[string]func(*contracts.Booking){"some": func(b *contracts.Booking) { b.Remote = "some" }}},
		{"max_consecutive_days", `{"days": 5}`,
			map[string]func(*contracts.Booking){"six days": func(b *contracts.Booking) { b.ConsecutiveDays = intPtr(6) }},
			map[string]func(*contracts.Booking){
				"five days": func(b *contracts.Booking) { b.ConsecutiveDays = intPtr(5) },
				"two days":  func(b *contracts.Booking) { b.ConsecutiveDays = intPtr(2) },
			}},
		// Judged on the booking's hours alone: 37 hours on a 40-hour limit
		// fits, whatever else the person has and whatever their position's
		// hours (37.5 for Anne).
		{"max_hours_per_week", `{"hours": 40}`,
			map[string]func(*contracts.Booking){
				"40.5 hours": func(b *contracts.Booking) { b.HoursPerWeek = float64Ptr(40.5) },
				"60 hours":   func(b *contracts.Booking) { b.HoursPerWeek = float64Ptr(60) },
			},
			map[string]func(*contracts.Booking){
				"37 hours": func(b *contracts.Booking) { b.HoursPerWeek = float64Ptr(37) },
				"40 hours": func(b *contracts.Booking) { b.HoursPerWeek = float64Ptr(40) },
			}},
	} {
		for weight := 1; weight <= 3; weight++ {
			setPreferences(t, h, w.anne, preferenceRow{tc.kind, tc.value, weight})
			for name, set := range tc.contradict {
				fits, err := workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, booking(set))
				if err != nil {
					t.Fatalf("%s weight %d, %s: %v", tc.kind, weight, name, err)
				}
				f, ok := fits[w.anne]
				if want := 100 - weight*10; !ok || f.Score != want || !slices.Equal(f.Lowered, []string{tc.kind}) {
					t.Errorf("%s weight %d, %s = %+v (answered %v); want %d lowered by %s", tc.kind, weight, name, f, ok, want, tc.kind)
				}
			}
			for name, set := range tc.fit {
				fits, err := workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, booking(set))
				if err != nil {
					t.Fatalf("%s weight %d, %s: %v", tc.kind, weight, name, err)
				}
				if f, ok := fits[w.anne]; !ok || f.Score != 100 || f.Lowered != nil {
					t.Errorf("%s weight %d, %s = %+v (answered %v); want 100, nothing lowered", tc.kind, weight, name, f, ok)
				}
			}
		}
	}
}

func TestWorkforce_PreferenceFitsSumsAndOrders(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	h := w.h
	// Stored by kind's name, which is not the catalogue's order.
	all := []preferenceRow{
		{"weekdays", `{"days": [1, 2, 3, 4, 5]}`, 2},
		{"time_of_day", `{"parts": ["day"]}`, 2},
		{"location", `{"locationIds": [11]}`, 2},
		{"travel", `{"extent": "none"}`, 2},
		{"remote", `{"share": "never"}`, 2},
		{"max_consecutive_days", `{"days": 3}`, 2},
		{"max_hours_per_week", `{"hours": 20}`, 2},
	}
	setPreferences(t, h, w.anne, all...)
	everything := booking(func(b *contracts.Booking) {
		b.Weekdays = []int{6}
		b.Parts = []string{"night"}
		b.LocationID = int64Ptr(99)
		b.Travel = "international"
		b.Remote = "only"
		b.ConsecutiveDays = intPtr(10)
		b.HoursPerWeek = float64Ptr(37.5)
	})
	catalogue := []string{"weekdays", "time_of_day", "location", "travel", "remote", "max_consecutive_days", "max_hours_per_week"}

	// Every kind contradicted: 100 less 140, floored at 0, the seven kinds
	// in the catalogue's order.
	fits, err := workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, everything)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if f := fits[w.anne]; f.Score != 0 || !slices.Equal(f.Lowered, catalogue) {
		t.Errorf("every kind contradicted = %+v; want 0 and %v", f, catalogue)
	}

	// Two contradicted of three: their weights summed, the third not named.
	setPreferences(t, h, w.anne,
		preferenceRow{"max_hours_per_week", `{"hours": 20}`, 2},
		preferenceRow{"travel", `{"extent": "national"}`, 3},
		preferenceRow{"weekdays", `{"days": [1, 2, 3, 4, 5]}`, 1})
	fits, err = workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, everything)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if f := fits[w.anne]; f.Score != 40 || !slices.Equal(f.Lowered, []string{"weekdays", "travel", "max_hours_per_week"}) {
		t.Errorf("three contradicted = %+v; want 40 and weekdays, travel, max_hours_per_week", f)
	}
	sum := everything
	sum.Travel = "regional"
	fits, err = workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, sum)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if f := fits[w.anne]; f.Score != 70 || !slices.Equal(f.Lowered, []string{"weekdays", "max_hours_per_week"}) {
		t.Errorf("two of three contradicted = %+v; want 70 and weekdays, max_hours_per_week", f)
	}

	// A booking with every facet left nil or empty contradicts nothing.
	setPreferences(t, h, w.anne, all...)
	for name, b := range map[string]contracts.Booking{
		"nil facets":   booking(nil),
		"empty facets": booking(func(b *contracts.Booking) { b.Weekdays = []int{}; b.Parts = []string{} }),
	} {
		fits, err := workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f, ok := fits[w.anne]; !ok || f.Score != 100 || f.Lowered != nil {
			t.Errorf("%s = %+v (answered %v); want 100, nothing lowered", name, f, ok)
		}
	}

	// A stored value that does not decode contradicts nothing.
	setPreferences(t, h, w.anne,
		preferenceRow{"weekdays", `{"days": "weekdays"}`, 3},
		preferenceRow{"max_hours_per_week", `"forty"`, 3})
	fits, err = workforceOf(h).PreferenceFits(w.view, []int64{w.anne}, everything)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if f := fits[w.anne]; f.Score != 100 || f.Lowered != nil {
		t.Errorf("undecodable values = %+v; want 100, nothing lowered", f)
	}
}

func TestWorkforce_PreferenceFitsWhoIsAnswered(t *testing.T) {
	t.Parallel()
	w := newWindowWorld(t)
	h := w.h
	setPreferences(t, h, w.anne, preferenceRow{"max_hours_per_week", `{"hours": 20}`, 3})
	setPreferences(t, h, w.cato, preferenceRow{"max_hours_per_week", `{"hours": 20}`, 3})
	hours := func(b *contracts.Booking) { b.HoursPerWeek = float64Ptr(30) }
	autumn := contracts.Booking{From: calDay(2026, 9, 14), To: calDay(2026, 11, 30)}
	hours(&autumn)
	ids := []int64{w.anne, w.bo, w.cato, w.dina, 999999}

	// Anne lowered; Bo, starting 15 October, and Dina with no preference at
	// 100 and nothing lowered; not Cato, who left yesterday, nor an unknown
	// id.
	fits, err := workforceOf(h).PreferenceFits(w.view, ids, autumn)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if len(fits) != 3 {
		t.Errorf("answered %v; want Anne, Bo and Dina", fits)
	}
	if f := fits[w.anne]; f.Score != 70 || !slices.Equal(f.Lowered, []string{"max_hours_per_week"}) {
		t.Errorf("Anne = %+v; want 70, max_hours_per_week", f)
	}
	for name, id := range map[string]int64{"Bo": w.bo, "Dina": w.dina} {
		if f, ok := fits[id]; !ok || f.Score != 100 || f.Lowered != nil {
			t.Errorf("%s = %+v (answered %v); want 100, Lowered nil", name, f, ok)
		}
	}

	// A booking ending before Bo starts does not answer him.
	early := autumn
	early.To = calDay(2026, 10, 14)
	fits, err = workforceOf(h).PreferenceFits(w.view, ids, early)
	if err != nil {
		t.Fatalf("PreferenceFits: %v", err)
	}
	if _, ok := fits[w.bo]; ok || len(fits) != 2 {
		t.Errorf("to 14 October answered %v; want Anne and Dina, not Bo", fits)
	}

	// No one asked about: an empty map, no error.
	if fits, err := workforceOf(h).PreferenceFits(w.view, nil, autumn); err != nil || fits == nil || len(fits) != 0 {
		t.Errorf("nobody asked = %v, %v; want an empty map", fits, err)
	}

	// The same user through AsUser, with no request.
	if fits, err := workforceOf(h).PreferenceFits(contracts.AsUser(context.Background(), w.viewUser), ids, autumn); err != nil || len(fits) != 3 {
		t.Errorf("under AsUser = %v, %v; want three", fits, err)
	}

	// The bound.
	for name, window := range map[string][2]time.Time{
		"fourteen weeks back": {calDay(2026, 6, 6), calDay(2026, 9, 30)},
		"four years ahead":    {calDay(2026, 9, 1), calDay(2030, 9, 12)},
		"to before from":      {calDay(2026, 10, 1), calDay(2026, 9, 30)},
	} {
		b := autumn
		b.From, b.To = window[0], window[1]
		if got, err := workforceOf(h).PreferenceFits(w.view, ids, b); !errors.Is(err, contracts.ErrWindowOutOfBounds) || got != nil {
			t.Errorf("%s: %v, %v; want ErrWindowOutOfBounds", name, got, err)
		}
	}

	// Without staff:view — staff:manage alone included — nothing, no error.
	noView, _ := calendarCaller(t, h.Harness, "staff:access")
	manageOnly, manageUser := calendarCaller(t, h.Harness, "staff:access", "staff:manage")
	for name, ctx := range map[string]context.Context{
		"a request without staff:view": noView,
		"a request with staff:manage":  manageOnly,
		"AsUser without staff:view":    contracts.AsUser(context.Background(), manageUser),
		"a bare context":               context.Background(),
	} {
		if got, err := workforceOf(h).PreferenceFits(ctx, ids, autumn); err != nil || got != nil {
			t.Errorf("%s: %v, %v; want nothing", name, got, err)
		}
	}
}
