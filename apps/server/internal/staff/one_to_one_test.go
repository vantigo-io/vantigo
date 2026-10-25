package staff_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The one-to-one's paths (staff competence design §6, delivery B).
func interestsPath(personID int64) string    { return personPath(personID) + "/interests" }
func preferencesPath(personID int64) string  { return personPath(personID) + "/preferences" }
func availabilityPath(personID int64) string { return personPath(personID) + "/availability" }
func wishesSeenPath(personID int64) string   { return personPath(personID) + "/wishes/seen" }

// interestsOf is an interests body naming ids.
func interestsOf(ids ...int64) map[string]any {
	rows := make([]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"competenceId": id})
	}
	return map[string]any{"interests": rows}
}

// putInterests replaces personID's interests as c and fails the test unless
// it answered 200.
func putInterests(t *testing.T, c *modtest.Client, personID int64, body map[string]any) []gen.StaffPersonInterest {
	t.Helper()
	var out gen.StaffPersonInterestList
	do(t, c, http.MethodPut, interestsPath(personID), body, http.StatusOK, &out)
	return out.Interests
}

// putPreferences replaces personID's preferences as c and fails the test
// unless it answered 200.
func putPreferences(t *testing.T, c *modtest.Client, personID int64, rows ...any) []gen.StaffPersonPreference {
	t.Helper()
	var out gen.StaffPersonPreferenceList
	do(t, c, http.MethodPut, preferencesPath(personID), map[string]any{"preferences": rows}, http.StatusOK, &out)
	return out.Preferences
}

// putAvailability replaces personID's declaration as c and fails the test
// unless it answered 200.
func putAvailability(t *testing.T, c *modtest.Client, personID int64, rows ...any) []gen.StaffAvailabilityDeclaration {
	t.Helper()
	var out gen.StaffAvailabilityList
	do(t, c, http.MethodPut, availabilityPath(personID), map[string]any{"declarations": rows}, http.StatusOK, &out)
	return out.Declarations
}

// pref is one preference row.
func pref(kind string, value map[string]any, weight int) map[string]any {
	return map[string]any{"kind": kind, "value": value, "weight": weight}
}

// markSeen stamps personID's wishes Seen as c and fails the test unless it
// answered 200.
func markSeen(t *testing.T, c *modtest.Client, personID int64) gen.StaffWishesSeen {
	t.Helper()
	var out gen.StaffWishesSeen
	do(t, c, http.MethodPost, wishesSeenPath(personID), nil, http.StatusOK, &out)
	return out
}

// eventsOfKind is personID's events of kind as c reads them, newest first.
func eventsOfKind(t *testing.T, c *modtest.Client, personID int64, kind string) []gen.StaffEvent {
	t.Helper()
	var out []gen.StaffEvent
	for _, e := range listEvents(t, c, personID) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// change is one field's change in an event's fields.
func change(t *testing.T, e gen.StaffEvent, field string) map[string]any {
	t.Helper()
	c, _ := fieldsOf(e)[field].(map[string]any)
	if c == nil {
		t.Fatalf("the %s event has no %s: %v", e.Kind, field, e.Payload)
	}
	return c
}

// The person writes their interests alone, each read with its competence
// and whether they hold it; their manager, HR and staff:personal-data read
// them; a colleague, an Approver and a caller who does not see the person
// get the 404; anyone but the person who sees them gets the uniform 403 on
// a write. Each change is interests_changed with the counts before and
// after; a replace that changes nothing records nothing; an erased person's
// list is person_erased.
func TestInterests_ThePersonWritesTheirOwn(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})

	rows := putInterests(t, w.kariC, w.kari.Id, interestsOf(w.golang, w.fse))
	if len(rows) != 2 {
		t.Fatalf("interests = %+v, want two", rows)
	}
	for _, r := range rows {
		wantHeld := r.CompetenceId == w.fse
		if r.Held != wantHeld || r.SeenAt != nil || r.Competence.Id != r.CompetenceId || r.Competence.Key == "" {
			t.Errorf("interest = %+v, want held %v, unstamped, with its competence", r, wantHeld)
		}
	}

	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "her manager": w.bossC, "HR": w.hr, "staff:personal-data": w.pd} {
		var got gen.StaffPersonInterestList
		do(t, c, http.MethodGet, interestsPath(w.kari.Id), nil, http.StatusOK, &got)
		if len(got.Interests) != 2 {
			t.Errorf("%s reads %+v, want Kari's two", name, got.Interests)
		}
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "an Approver": w.approver, "a stranger": w.nobody} {
		if r := c.Do(http.MethodGet, interestsPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s reads Kari's interests: %d, want 404", name, r.Status)
		}
	}
	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "HR": w.hr, "an Approver": w.approver, "a colleague": w.viewer, "staff:personal-data": w.pd} {
		if r := c.Do(http.MethodPut, interestsPath(w.kari.Id), interestsOf(w.golang)); r.Status != http.StatusForbidden {
			t.Errorf("%s writes Kari's interests: %d, want 403", name, r.Status)
		}
	}
	if r := w.nobody.Do(http.MethodPut, interestsPath(w.kari.Id), interestsOf(w.golang)); r.Status != http.StatusNotFound {
		t.Errorf("a stranger writes Kari's interests: %d, want 404", r.Status)
	}
	if r := w.kariC.Do(http.MethodPut, interestsPath(9999999), interestsOf(w.golang)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person = %d, want 404", r.Status)
	}

	events := eventsOfKind(t, w.bossC, w.kari.Id, "interests_changed")
	if len(events) != 1 {
		t.Fatalf("%d interests_changed events, want one", len(events))
	}
	if c := change(t, events[0], "interests"); c["from"] != float64(0) || c["to"] != float64(2) {
		t.Errorf("interests change = %v, want the counts 0 and 2", c)
	}
	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.fse, w.golang))
	if n := len(eventsOfKind(t, w.kariC, w.kari.Id, "interests_changed")); n != 1 {
		t.Errorf("%d interests_changed events after the same list again, want still one", n)
	}
	if rows := putInterests(t, w.kariC, w.kari.Id, interestsOf()); len(rows) != 0 {
		t.Errorf("an empty list leaves %+v, want none", rows)
	}
	if c := change(t, eventsOfKind(t, w.kariC, w.kari.Id, "interests_changed")[0], "interests"); c["from"] != float64(2) || c["to"] != float64(0) {
		t.Errorf("the emptying = %v, want the counts 2 and 0", c)
	}

	h.Exec(t, `UPDATE staff.people SET erased_at = now() WHERE id = $1`, w.kari.Id)
	refused(t, w.kariC.Do(http.MethodPut, interestsPath(w.kari.Id), interestsOf(w.golang)), http.StatusConflict, "person_erased")
	refused(t, w.kariC.Do(http.MethodPut, preferencesPath(w.kari.Id), map[string]any{"preferences": []any{}}), http.StatusConflict, "person_erased")
	refused(t, w.kariC.Do(http.MethodPut, availabilityPath(w.kari.Id), map[string]any{"declarations": []any{}}), http.StatusConflict, "person_erased")
	refused(t, w.bossC.Do(http.MethodPost, wishesSeenPath(w.kari.Id), nil), http.StatusConflict, "person_erased")
}

// An interests replace is refused whole, each failure on the row's index:
// more than twenty, a competence named twice, a new interest in a hidden,
// archived or unknown competence (competence_unavailable), a property a row
// does not name. One already on file is kept though its competence is
// archived since.
func TestInterests_Refusals(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	cobol := addCompetence(t, w.hr, skillBody(t, h, "cobol")).Id
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), cobol)
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE key = 'masseforflytning'`)
	hidden := competenceID(t, h, "masseforflytning")
	many := make([]int64, 21)
	for i := range many {
		many[i] = int64(i + 1)
	}
	for _, tc := range []struct {
		name, code, field string
		body              map[string]any
	}{
		{"twenty-one", "", "interests", interestsOf(many...)},
		{"a competence twice", "", "interests[1].competenceId", interestsOf(w.golang, w.golang)},
		{"a hidden competence", "competence_unavailable", "interests[1].competenceId", interestsOf(w.golang, hidden)},
		{"an archived competence", "competence_unavailable", "interests[0].competenceId", interestsOf(cobol)},
		{"an unknown competence", "competence_unavailable", "interests[0].competenceId", interestsOf(9999999)},
		{"a property a row does not name", "", "interests[0].level", map[string]any{"interests": []any{map[string]any{"competenceId": w.golang, "level": 2}}}},
		{"a property the list does not name", "", "note", with(interestsOf(w.golang), "note", "x")},
	} {
		p := refused(t, w.kariC.Do(http.MethodPut, interestsPath(w.kari.Id), tc.body), http.StatusBadRequest, tc.code)
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.person_interests WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d interests written, want none", n)
	}

	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.golang))
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), w.golang)
	if rows := putInterests(t, w.kariC, w.kari.Id, interestsOf(w.golang, w.fse)); len(rows) != 2 || !rows[0].Competence.Archived && !rows[1].Competence.Archived {
		t.Errorf("rows = %+v, want the archived interest kept beside the new one", rows)
	}
}

// Preferences, one per kind, each value judged by its kind and answered in
// its canonical form; preferences_changed names the kinds added, changed or
// removed and never a value.
func TestPreferences_ThePersonWritesTheirOwn(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	var loc gen.StaffLocation
	do(t, w.hr, http.MethodPost, locationsPath, map[string]any{"name": "Bergen"}, http.StatusCreated, &loc)

	rows := putPreferences(t, w.kariC, w.kari.Id,
		pref("weekdays", map[string]any{"days": []int{5, 1, 3}}, 2),
		pref("time_of_day", map[string]any{"parts": []string{"night", "morning"}}, 3),
		pref("location", map[string]any{"locationIds": []int64{loc.Id}}, 1),
		pref("travel", map[string]any{"extent": "regional"}, 1),
		pref("remote", map[string]any{"share": "some"}, 2),
		pref("max_consecutive_days", map[string]any{"days": 5}, 3),
		pref("max_hours_per_week", map[string]any{"hours": 37}, 1),
	)
	if len(rows) != 7 {
		t.Fatalf("preferences = %+v, want seven", rows)
	}
	byKind := map[string]gen.StaffPersonPreference{}
	for _, r := range rows {
		byKind[string(r.Kind)] = r
	}
	if days, _ := json.Marshal(byKind["weekdays"].Value); string(days) != `{"days":[1,3,5]}` || byKind["weekdays"].Weight != 2 {
		t.Errorf("weekdays = %s weight %d, want the days sorted, weight 2", days, byKind["weekdays"].Weight)
	}
	if parts, _ := json.Marshal(byKind["time_of_day"].Value); string(parts) != `{"parts":["morning","night"]}` {
		t.Errorf("time_of_day = %s, want the parts in the day's order", parts)
	}
	var got gen.StaffPersonPreferenceList
	do(t, w.bossC, http.MethodGet, preferencesPath(w.kari.Id), nil, http.StatusOK, &got)
	if len(got.Preferences) != 7 {
		t.Errorf("the manager reads %d preferences, want seven", len(got.Preferences))
	}
	if r := w.approver.Do(http.MethodGet, preferencesPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("an Approver reads Kari's preferences: %d, want 404", r.Status)
	}
	if r := w.bossC.Do(http.MethodPut, preferencesPath(w.kari.Id), map[string]any{"preferences": []any{}}); r.Status != http.StatusForbidden {
		t.Errorf("the manager writes Kari's preferences: %d, want 403", r.Status)
	}

	added := eventsOfKind(t, w.kariC, w.kari.Id, "preferences_changed")
	if len(added) != 1 {
		t.Fatalf("%d preferences_changed events, want one", len(added))
	}
	kinds := change(t, added[0], "preferences")["to"]
	if b, _ := json.Marshal(kinds); string(b) != `["location","max_consecutive_days","max_hours_per_week","remote","time_of_day","travel","weekdays"]` {
		t.Errorf("the kinds = %s, want the seven sorted", b)
	}
	if b, _ := json.Marshal(added[0].Payload); strings.Contains(string(b), "regional") || strings.Contains(string(b), "Bergen") || strings.Contains(string(b), "days\":[") {
		t.Errorf("the payload %s carries a value", b)
	}

	// The same seven, one weight changed and one dropped: the kinds changed
	// and removed, sorted.
	putPreferences(t, w.kariC, w.kari.Id,
		pref("weekdays", map[string]any{"days": []int{3, 5, 1}}, 2),
		pref("time_of_day", map[string]any{"parts": []string{"morning", "night"}}, 3),
		pref("location", map[string]any{"locationIds": []int64{loc.Id}}, 1),
		pref("travel", map[string]any{"extent": "regional"}, 2),
		pref("remote", map[string]any{"share": "some"}, 2),
		pref("max_consecutive_days", map[string]any{"days": 5}, 3),
	)
	changed := eventsOfKind(t, w.kariC, w.kari.Id, "preferences_changed")
	if len(changed) != 2 {
		t.Fatalf("%d preferences_changed events, want two", len(changed))
	}
	if b, _ := json.Marshal(change(t, changed[0], "preferences")["to"]); string(b) != `["max_hours_per_week","travel"]` {
		t.Errorf("the kinds = %s, want the changed travel and the removed hours", b)
	}

	// An archived location is kept on a preference that does not change, and
	// refused on one that does.
	w.h.Exec(t, `UPDATE staff.locations SET archived_at = now() WHERE id = $1`, loc.Id)
	putPreferences(t, w.kariC, w.kari.Id, pref("location", map[string]any{"locationIds": []int64{loc.Id}}, 1))
	p := refused(t, w.kariC.Do(http.MethodPut, preferencesPath(w.kari.Id),
		map[string]any{"preferences": []any{pref("location", map[string]any{"locationIds": []int64{loc.Id}}, 2)}}), http.StatusBadRequest, "")
	if len(p.Errors["preferences[0].value"]) == 0 {
		t.Errorf("a changed preference naming an archived location: errors = %v, want preferences[0].value", p.Errors)
	}
}

// A preferences replace is refused whole, each failure on the row's index.
func TestPreferences_Refusals(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	var loc gen.StaffLocation
	do(t, w.hr, http.MethodPost, locationsPath, map[string]any{"name": "Bergen"}, http.StatusCreated, &loc)
	w.h.Exec(t, `UPDATE staff.locations SET archived_at = now() WHERE id = $1`, loc.Id)
	travel := pref("travel", map[string]any{"extent": "none"}, 1)
	eight := []any{travel, travel, travel, travel, travel, travel, travel, travel}
	for _, tc := range []struct {
		name, field string
		rows        []any
	}{
		{"eight rows", "preferences", eight},
		{"a day of 8", "preferences[0].value", []any{pref("weekdays", map[string]any{"days": []int{1, 8}}, 1)}},
		{"a day twice", "preferences[0].value", []any{pref("weekdays", map[string]any{"days": []int{1, 1}}, 1)}},
		{"no days", "preferences[0].value", []any{pref("weekdays", map[string]any{"days": []int{}}, 1)}},
		{"an unknown part", "preferences[0].value", []any{pref("time_of_day", map[string]any{"parts": []string{"noon"}}, 1)}},
		{"a kind twice", "preferences[1].kind", []any{travel, pref("travel", map[string]any{"extent": "national"}, 2)}},
		{"an unknown kind", "preferences[0].kind", []any{pref("customer", map[string]any{"id": 1}, 1)}},
		{"a weight of 4", "preferences[0].weight", []any{pref("travel", map[string]any{"extent": "none"}, 4)}},
		{"a weight of 0", "preferences[0].weight", []any{pref("travel", map[string]any{"extent": "none"}, 0)}},
		{"a key the kind does not take", "preferences[0].value", []any{pref("travel", map[string]any{"extent": "none", "reason": "knee"}, 1)}},
		{"an unknown extent", "preferences[0].value", []any{pref("travel", map[string]any{"extent": "far"}, 1)}},
		{"an unknown share", "preferences[0].value", []any{pref("remote", map[string]any{"share": "always"}, 1)}},
		{"15 consecutive days", "preferences[0].value", []any{pref("max_consecutive_days", map[string]any{"days": 15}, 1)}},
		{"61 hours", "preferences[0].value", []any{pref("max_hours_per_week", map[string]any{"hours": 61}, 1)}},
		{"half an hour", "preferences[0].value", []any{pref("max_hours_per_week", map[string]any{"hours": 37.5}, 1)}},
		{"an archived location", "preferences[0].value", []any{pref("location", map[string]any{"locationIds": []int64{loc.Id}}, 1)}},
		{"an unknown location", "preferences[0].value", []any{pref("location", map[string]any{"locationIds": []int64{9999999}}, 1)}},
		{"eleven locations", "preferences[0].value", []any{pref("location", map[string]any{"locationIds": []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}}, 1)}},
		{"a property a row does not name", "preferences[0].note", []any{with(travel, "note", "knee")}},
	} {
		p := refused(t, w.kariC.Do(http.MethodPut, preferencesPath(w.kari.Id), map[string]any{"preferences": tc.rows}), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.person_preferences WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d preferences written, want none", n)
	}
}

// The availability declaration: recurring rows by weekday first, then the
// dated ones; availability_changed counts the rows before and after.
func TestAvailability_ThePersonWritesTheirOwn(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	rows := putAvailability(t, w.kariC, w.kari.Id,
		map[string]any{"kind": "unavailable", "fromDate": "2026-11-03", "toDate": "2026-11-07"},
		map[string]any{"kind": "available", "weekday": 1, "startMinute": 480, "endMinute": 960},
	)
	if len(rows) != 2 || rows[0].Weekday == nil || *rows[0].Weekday != 1 || *rows[0].StartMinute != 480 ||
		rows[1].FromDate == nil || rows[1].FromDate.String() != "2026-11-03" || rows[1].Weekday != nil || rows[1].StartMinute != nil {
		t.Fatalf("declarations = %+v, want Monday first, then the dated row", rows)
	}
	var got gen.StaffAvailabilityList
	do(t, w.hr, http.MethodGet, availabilityPath(w.kari.Id), nil, http.StatusOK, &got)
	if len(got.Declarations) != 2 {
		t.Errorf("HR reads %d rows, want two", len(got.Declarations))
	}
	if r := w.viewer.Do(http.MethodGet, availabilityPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a colleague reads Kari's declaration: %d, want 404", r.Status)
	}
	if r := w.hr.Do(http.MethodPut, availabilityPath(w.kari.Id), map[string]any{"declarations": []any{}}); r.Status != http.StatusForbidden {
		t.Errorf("HR writes Kari's declaration: %d, want 403", r.Status)
	}
	events := eventsOfKind(t, w.bossC, w.kari.Id, "availability_changed")
	if len(events) != 1 {
		t.Fatalf("%d availability_changed events, want one", len(events))
	}
	if c := change(t, events[0], "declarations"); c["from"] != float64(0) || c["to"] != float64(2) {
		t.Errorf("declarations change = %v, want the counts 0 and 2", c)
	}
}

// An availability replace is refused whole, each failure on the row's
// index.
func TestAvailability_Refusals(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	monday := map[string]any{"kind": "available", "weekday": 1}
	fiftyOne := make([]any, 51)
	for i := range fiftyOne {
		fiftyOne[i] = map[string]any{"kind": "available", "fromDate": "2026-11-03", "startMinute": i, "endMinute": i + 60}
	}
	for _, tc := range []struct {
		name, field string
		rows        []any
	}{
		{"fifty-one rows", "declarations", fiftyOne},
		{"a dated row without its day", "declarations[0].fromDate", []any{map[string]any{"kind": "available"}}},
		{"a weekday of 8", "declarations[0].weekday", []any{map[string]any{"kind": "available", "weekday": 8}}},
		{"an unknown kind", "declarations[0].kind", []any{map[string]any{"kind": "maybe", "weekday": 1}}},
		{"the last day before the first", "declarations[0].toDate", []any{map[string]any{"kind": "available", "fromDate": "2026-11-07", "toDate": "2026-11-03"}}},
		{"a start without an end", "declarations[0].endMinute", []any{with(monday, "startMinute", 480)}},
		{"an end without a start", "declarations[0].startMinute", []any{with(monday, "endMinute", 480)}},
		{"the end before the start", "declarations[0].endMinute", []any{with(monday, "startMinute", 600, "endMinute", 480)}},
		{"the end at the start", "declarations[0].endMinute", []any{with(monday, "startMinute", 600, "endMinute", 600)}},
		{"a minute past the day", "declarations[0].endMinute", []any{with(monday, "startMinute", 600, "endMinute", 1441)}},
		{"a row twice", "declarations[1]", []any{monday, monday}},
		{"a property a row does not name", "declarations[0].note", []any{with(monday, "note", "school run")}},
	} {
		p := refused(t, w.kariC.Do(http.MethodPut, availabilityPath(w.kari.Id), map[string]any{"declarations": tc.rows}), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.availability_declarations WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d rows written, want none", n)
	}
}

// The manager and HR stamp everything unstamped Seen — the three lists and
// the current competence rows — and the answer counts it; an Approver, a
// colleague and a stranger get the 404 (they read no wishes), the person and
// staff:personal-data the uniform 403. A Seen with nothing new answers zeros
// and records nothing. A replace keeps an unchanged row's stamp: the new or
// changed row reads as new.
func TestWishesSeen_StampsWhatIsNew(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.fse))
	travel := pref("travel", map[string]any{"extent": "regional"}, 1)
	putPreferences(t, w.kariC, w.kari.Id, travel)
	monday := map[string]any{"kind": "available", "weekday": 1}
	putAvailability(t, w.kariC, w.kari.Id, monday)

	for name, c := range map[string]*modtest.Client{"an Approver": w.approver, "a colleague": w.viewer, "a stranger": w.nobody} {
		if r := c.Do(http.MethodPost, wishesSeenPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s marks Kari's wishes seen: %d, want 404", name, r.Status)
		}
	}
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "staff:personal-data": w.pd} {
		if r := c.Do(http.MethodPost, wishesSeenPath(w.kari.Id), nil); r.Status != http.StatusForbidden {
			t.Errorf("%s marks Kari's wishes seen: %d, want 403", name, r.Status)
		}
	}
	if r := w.hr.Do(http.MethodPost, wishesSeenPath(9999999), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person = %d, want 404", r.Status)
	}

	seen := markSeen(t, w.bossC, w.kari.Id)
	if seen.Interests != 1 || seen.Preferences != 1 || seen.Declarations != 1 || seen.Competences != 1 || !seen.SeenAt.Equal(w.h.Now()) {
		t.Errorf("seen = %+v, want one of each, now", seen)
	}
	events := eventsOfKind(t, w.kariC, w.kari.Id, "wishes_seen")
	if len(events) != 1 {
		t.Fatalf("%d wishes_seen events, want one", len(events))
	}
	if c := change(t, events[0], "seen"); c["to"] != float64(4) {
		t.Errorf("seen change = %v, want the four rows", c)
	}
	if again := markSeen(t, w.hr, w.kari.Id); again.Interests+again.Preferences+again.Declarations+again.Competences != 0 {
		t.Errorf("a second Seen = %+v, want zeros", again)
	}
	if n := len(eventsOfKind(t, w.kariC, w.kari.Id, "wishes_seen")); n != 1 {
		t.Errorf("%d wishes_seen events after a Seen of nothing, want still one", n)
	}
	for _, r := range listPersonCompetences(t, w.bossC, w.kari.Id, false) {
		if r.Id == row.Id && r.SeenAt == nil {
			t.Errorf("the manager reads the competence row %+v, want its Seen stamp", r)
		}
	}

	// The same list plus one: the old row keeps its stamp, the new has none.
	for _, r := range putInterests(t, w.kariC, w.kari.Id, interestsOf(w.fse, w.golang)) {
		if (r.CompetenceId == w.fse) != (r.SeenAt != nil) {
			t.Errorf("interest %d seenAt %v, want the kept one stamped and the new one not", r.CompetenceId, r.SeenAt)
		}
	}
	for _, r := range putPreferences(t, w.kariC, w.kari.Id, travel, pref("remote", map[string]any{"share": "some"}, 2)) {
		if (r.Kind == "travel") != (r.SeenAt != nil) {
			t.Errorf("preference %s seenAt %v, want the kept travel stamped and the new remote not", r.Kind, r.SeenAt)
		}
	}
	for _, r := range putPreferences(t, w.kariC, w.kari.Id, pref("travel", map[string]any{"extent": "regional"}, 3)) {
		if r.SeenAt != nil {
			t.Errorf("a changed preference %+v, want its stamp cleared", r)
		}
	}
	for _, r := range putAvailability(t, w.kariC, w.kari.Id, monday, map[string]any{"kind": "preferred", "weekday": 2}) {
		if (*r.Weekday == 1) != (r.SeenAt != nil) {
			t.Errorf("declaration %+v, want Monday stamped and Tuesday not", r)
		}
	}
	if next := markSeen(t, w.hr, w.kari.Id); next.Interests != 1 || next.Preferences != 1 || next.Declarations != 1 || next.Competences != 0 {
		t.Errorf("the next Seen = %+v, want the one new interest, preference and declaration", next)
	}
}

// A replace that does not say its list — a body without it, or with it null
// — is refused on the list's name and leaves every row on file: the decoder
// reads either as an empty list, and only [] removes them all.
func TestWishes_ABodyWithoutTheListRemovesNothing(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.golang))
	putPreferences(t, w.kariC, w.kari.Id, pref("travel", map[string]any{"extent": "regional"}, 1))
	putAvailability(t, w.kariC, w.kari.Id, map[string]any{"kind": "available", "weekday": 1})
	for _, list := range []struct {
		name, path, table string
	}{
		{"interests", interestsPath(w.kari.Id), "staff.person_interests"},
		{"preferences", preferencesPath(w.kari.Id), "staff.person_preferences"},
		{"declarations", availabilityPath(w.kari.Id), "staff.availability_declarations"},
	} {
		for what, body := range map[string]map[string]any{"no list": {}, "a null list": {list.name: nil}} {
			p := refused(t, w.kariC.Do(http.MethodPut, list.path, body), http.StatusBadRequest, "")
			if len(p.Errors[list.name]) == 0 || len(p.Errors) != 1 {
				t.Errorf("%s with %s: errors = %v, want one on %s", list.name, what, p.Errors, list.name)
			}
		}
		if n := h.Count(t, `SELECT count(*) FROM `+list.table+` WHERE person_id = $1`, w.kari.Id); n != 1 {
			t.Errorf("%d %s on file, want the one kept", n, list.name)
		}
	}
}

// A competence row the person changes after a Seen reads as new again
// (design S8, amendment 17): their own change of their declared row, or
// their evidence upload, clears its stamp; a manager's or HR's edit, HR's
// upload and the person's change that changes nothing keep it.
func TestWishesSeen_ThePersonsChangeOfACompetenceRowReadsAsNew(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	stamped := func() bool {
		t.Helper()
		for _, r := range listPersonCompetences(t, w.bossC, w.kari.Id, false) {
			if r.Id == row.Id {
				return r.SeenAt != nil
			}
		}
		t.Fatalf("row %d is gone", row.Id)
		return false
	}
	edit := func(c *modtest.Client, body map[string]any) {
		t.Helper()
		var out gen.StaffPersonCompetence
		do(t, c, http.MethodPut, personCompetencePath(row.Id), body, http.StatusOK, &out, ifMatch(row.Revision))
		row = out
	}
	if seen := markSeen(t, w.bossC, w.kari.Id); seen.Competences != 1 {
		t.Fatalf("the first Seen = %+v, want the one competence", seen)
	}

	edit(w.kariC, map[string]any{})
	if !stamped() {
		t.Error("Kari's change that changed nothing cleared the stamp, want it kept")
	}
	edit(w.bossC, map[string]any{"reference": "FSE-1"})
	edit(w.hr, map[string]any{"reference": "FSE-2"})
	if !stamped() {
		t.Error("the manager's and HR's edits cleared the stamp, want it kept")
	}
	edit(w.kariC, map[string]any{"reference": "FSE-3"})
	if stamped() {
		t.Error("Kari's change of her declared row kept the stamp, want it cleared")
	}
	if seen := markSeen(t, w.hr, w.kari.Id); seen.Competences != 1 {
		t.Errorf("the Seen after her change = %+v, want the one competence", seen)
	}

	row = addEvidence(t, w.hr, row.Id)
	if !stamped() {
		t.Error("HR's upload cleared the stamp, want it kept")
	}
	row = addEvidence(t, w.kariC, row.Id)
	if stamped() {
		t.Error("Kari's upload kept the stamp, want it cleared")
	}
	if seen := markSeen(t, w.bossC, w.kari.Id); seen.Competences != 1 {
		t.Errorf("the Seen after her upload = %+v, want the one competence", seen)
	}
}

// Both personal-data files carry the person's interests, preferences and
// availability declaration (design S16): the complete file and the person's
// own copy.
func TestWishes_InBothExports(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.fse))
	putPreferences(t, w.kariC, w.kari.Id, pref("travel", map[string]any{"extent": "regional"}, 1))
	putAvailability(t, w.kariC, w.kari.Id, map[string]any{"kind": "available", "weekday": 1})
	for name, r := range map[string]*modtest.Response{
		"the complete file": w.pd.Do(http.MethodGet, personalDataPath(w.kari.Id), nil),
		"the person's copy": w.kariC.Do(http.MethodGet, meExportPath, nil),
	} {
		file := exportOf(t, r)
		if len(file.Interests) != 1 || file.Interests[0].CompetenceId != w.fse || len(file.Preferences) != 1 ||
			file.Preferences[0].Kind != "travel" || len(file.Availability) != 1 || file.CompetenceReviews == nil {
			t.Errorf("%s: interests %+v, preferences %+v, availability %+v, reviews %v; want one of each wish and the reviews' list",
				name, file.Interests, file.Preferences, file.Availability, file.CompetenceReviews)
		}
	}
}

// The D erase deletes the person's wishes and reviews with the rest of
// class D, and the delete of a person created in error takes them too.
func TestWishes_TheEraseAndTheDeleteRemoveThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrUser := signIn(t, h, "staff:manage")
	company(t, hr)
	fse := competenceID(t, h, "fse")
	seed := func(personID int64) {
		h.Exec(t, `INSERT INTO staff.person_interests (person_id, competence_id, created_at) VALUES ($1, $2, $3)`, personID, fse, h.Now())
		h.Exec(t, `INSERT INTO staff.person_preferences (person_id, kind, value, weight, created_at, updated_at)
			VALUES ($1, 'travel', '{"extent":"none"}', 1, $2, $2)`, personID, h.Now())
		h.Exec(t, `INSERT INTO staff.availability_declarations (person_id, kind, weekday, created_at) VALUES ($1, 'available', 1, $2)`, personID, h.Now())
		h.Exec(t, `INSERT INTO staff.competence_reviews (person_id, reviewed_on, reviewer_user_id, created_at) VALUES ($1, '2024-06-01', $2, $3)`,
			personID, hrUser, h.Now())
	}
	left := func(personID int64) []int {
		var out []int
		for _, table := range []string{"person_interests", "person_preferences", "availability_declarations", "competence_reviews"} {
			out = append(out, h.Count(t, `SELECT count(*) FROM staff.`+table+` WHERE person_id = $1`, personID))
		}
		return out
	}

	gone := createPerson(t, hr, newPerson("Erik", "Glemt"))
	pastEmployment(t, hr, gone.Id, "2022-01-01", "2024-12-31", terms())
	seed(gone.Id)
	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if rows := left(gone.Id); !slices.Equal(rows, []int{0, 0, 0, 0}) {
		t.Errorf("rows after the erase = %v, want none in any of the four tables", rows)
	}

	mistake := createPerson(t, hr, newPerson("Feil", "Registrert"))
	seed(mistake.Id)
	do(t, hr, http.MethodDelete, personPath(mistake.Id), nil, http.StatusNoContent, nil)
	if rows := left(mistake.Id); !slices.Equal(rows, []int{0, 0, 0, 0}) {
		t.Errorf("rows after the delete = %v, want none in any of the four tables", rows)
	}
}
