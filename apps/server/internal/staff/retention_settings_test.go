package staff_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The two erase settings the owner asked for (delivery C, Task 5b; B's open
// questions 11 and 12): erasePregnancyHealthTypes puts the pregnancy and
// health-appointment rows on the health schedule, eraseSickCaseDates makes a
// sick-leave case's erase remove the case itself. Both off by default, as B
// built; turned on, each applies from the next cycle to everything already
// due; turned off, each restores nothing.

// setEraseSettings turns the two erase settings to the values given, the
// rest of the settings as they are.
func setEraseSettings(t *testing.T, hr *modtest.Client, values map[string]any) {
	t.Helper()
	current := getSettings(t, hr)
	do(t, hr, http.MethodPut, settingsPath, settingsBody(values), http.StatusOK, nil, ifMatch(current.Revision))
}

// rowType is an absence's type key, its hours as text ("" when none) and
// its case ("" when none).
func rowType(t *testing.T, h *harness, id int64) (key, hours, caseID string) {
	t.Helper()
	got := modtest.One[string](t, h.Harness, `SELECT t.key || '|' || coalesce(a.hours::text, '') || '|' || coalesce(a.case_id::text, '')
		FROM staff.absences a JOIN staff.absence_types t ON t.id = a.type_id WHERE a.id = $1`, id)
	parts := strings.Split(got, "|")
	return parts[0], parts[1], parts[2]
}

// firstKind is the kind of the first event about absence id.
func firstKind(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT kind FROM staff.events WHERE (payload->>'absenceId')::bigint = $1 ORDER BY id LIMIT 1`, id)
}

// historyText is the person's history as c reads it, as text.
func historyText(t *testing.T, c *modtest.Client, personID int64) string {
	t.Helper()
	r := c.Do(http.MethodGet, pagedPath(eventsPath(personID), 1, 100), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("read person %d's history: %d %s", personID, r.Status, r.Body)
	}
	return string(r.Body)
}

// With erasePregnancyHealthTypes on, a row of svangerskapskontroll,
// svangerskapspermisjon, fødselspermisjon or helse goes to a reserved type
// healthRetentionDays after its year ended — absent_hours for helse, an hour
// row keeping its hours; the HR-only pair, absent_hr_only and
// absent_hr_only_hours, for the HR-only types — and the type values
// of its events are replaced whatever class they were written in: the type
// field changed, the typeIds the reserved type, for every reader of the
// history. A row whose year has not run, a ferie row and a held person's row
// are left; the erased event names pregnancyHealth; a second cycle erases
// nothing more. Off — the default — the cycle leaves them all; turned off
// again, nothing is restored.
func TestRetention_PregnancyAndHealthRows(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	visit := register(t, hrPD, w.kari.Id, absence(t, h, "helse", "2024-03-05", "2024-03-05", "hours", 2))
	checkup := register(t, hrPD, w.kari.Id, absence(t, h, "svangerskapskontroll", "2024-04-09", "2024-04-09", "hours", 1.5))
	leave := register(t, hrPD, w.kari.Id, absence(t, h, "svangerskapspermisjon", "2024-05-06", "2024-05-17"))
	recent := register(t, hrPD, w.kari.Id, absence(t, h, "fodselspermisjon", "2025-06-02", "2025-06-13"))
	// Recorded in 2024, ahead of its days: its events wait for the row's year, not their own.
	h.Exec(t, `UPDATE staff.events SET at = '2024-12-02T10:00:00Z' WHERE (payload->>'absenceId')::bigint = $1`, recent.Id)
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2024-07-01", "2024-07-05"))
	held, _ := employed(t, w.hr, "Holdt", "2020-01-01", terms())
	heldVisit := register(t, hrPD, held.Id, absence(t, h, "helse", "2024-03-05", "2024-03-05", "hours", 2))
	do(t, w.pd, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": true, "reason": "A claim"}, http.StatusOK, nil)

	// Off, as built: nothing of theirs is erased.
	runCycle(t, h)
	for id, want := range map[int64]string{visit.Id: "helse", checkup.Id: "svangerskapskontroll", leave.Id: "svangerskapspermisjon"} {
		if key, _, _ := rowType(t, h, id); key != want {
			t.Errorf("off: row %d's type = %s, want %s kept", id, key, want)
		}
	}
	if len(erasedEvents(t, h, w.kari.Id)) != 0 {
		t.Errorf("off: Kari's erased events = %v, want none", erasedEvents(t, h, w.kari.Id))
	}

	setEraseSettings(t, w.hr, map[string]any{"erasePregnancyHealthTypes": true})
	runCycle(t, h)
	for _, c := range []struct {
		id          int64
		key, hours  string
		description string
	}{
		{visit.Id, "absent_hours", "2.00", "the 2024 helse visit"},
		{checkup.Id, "absent_hr_only_hours", "1.50", "the 2024 svangerskapskontroll, HR-only"},
		{leave.Id, "absent_hr_only", "", "the 2024 svangerskapspermisjon, HR-only"},
		{recent.Id, "fodselspermisjon", "", "the 2025 fødselspermisjon, whose year has not run"},
		{holiday.Id, "ferie", "", "the ferie row"},
		{heldVisit.Id, "helse", "2.00", "the held person's visit"},
	} {
		if key, hours, _ := rowType(t, h, c.id); key != c.key || hours != c.hours {
			t.Errorf("%s: %s, %q hours; want %s, %q", c.description, key, hours, c.key, c.hours)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND first_day = '2024-05-06' AND last_day = '2024-05-17'`, leave.Id); n != 1 {
		t.Error("the erased leave lost its days, which stay")
	}

	hrOnlyID, hrOnlyHoursID, hoursID := typeID(t, h, "absent_hr_only"), typeID(t, h, "absent_hr_only_hours"), typeID(t, h, "absent_hours")
	for id, reserved := range map[int64]int64{visit.Id: hoursID, checkup.Id: hrOnlyHoursID, leave.Id: hrOnlyID} {
		kind := firstKind(t, h, id)
		if f := eventFields(t, h, kind, "absenceId", id); f["type"] != "changed" || f["firstDay"] == "changed" {
			t.Errorf("row %d's event fields = %v, want the type changed and the days kept", id, f)
		}
		if got := eventTypeIDs(t, h, kind, "absenceId", id); !slices.Equal(got, []int64{reserved}) {
			t.Errorf("row %d's event typeIds = %v, want [%d]", id, got, reserved)
		}
	}
	if f := eventFields(t, h, firstKind(t, h, recent.Id), "absenceId", recent.Id); f["type"] == "changed" {
		t.Error("the 2025 row's event was erased before its year had run")
	}
	if f := eventFields(t, h, firstKind(t, h, holiday.Id), "absenceId", holiday.Id); f["type"] == "changed" {
		t.Error("the ferie row's event was erased")
	}
	if f := eventFields(t, h, firstKind(t, h, heldVisit.Id), "absenceId", heldVisit.Id); f["type"] == "changed" {
		t.Error("the held person's event was erased")
	}
	// Every reader of Kari's history — HR, staff:personal-data, Kari, her
	// manager — reads none of the erased types: not as a value, not as an id.
	erasedTypes := []int64{typeID(t, h, "helse"), typeID(t, h, "svangerskapskontroll"), typeID(t, h, "svangerskapspermisjon")}
	for name, c := range map[string]*modtest.Client{"HR": w.hr, "personal-data": w.pd, "HR with personal-data": hrPD, "Kari": w.kariC, "her manager": w.bossC} {
		for _, id := range []int64{visit.Id, checkup.Id, leave.Id} {
			if f := eventOf(t, c, w.kari.Id, firstKind(t, h, id), id); f != nil && f["type"] != "changed" {
				t.Errorf("%s reads row %d's type as %v, want changed", name, id, f["type"])
			}
		}
		history := historyText(t, c, w.kari.Id)
		for _, id := range erasedTypes {
			for _, shape := range []string{`"to":` + strconv.FormatInt(id, 10), `"from":` + strconv.FormatInt(id, 10)} {
				if strings.Contains(history, shape) {
					t.Errorf("%s's history still names type %d (%s)", name, id, shape)
				}
			}
		}
	}

	events := erasedEvents(t, h, w.kari.Id)
	if len(events) != 1 {
		t.Fatalf("Kari's erased events = %v, want one", events)
	}
	// Two rows of hours and one of days, and their three events.
	if counts := kindCounts(events[0]); counts["pregnancyHealth"] != 6 || len(counts) != 1 {
		t.Errorf("the erased kinds = %v, want pregnancyHealth alone, 6", counts)
	}
	// The kind is health data: HR without staff:personal-data reads the
	// erased event without it, as it reads no other health kind.
	if text := historyText(t, w.hr, w.kari.Id); strings.Contains(text, "pregnancyHealth") {
		t.Error("HR without staff:personal-data reads the pregnancyHealth kind")
	}
	if text := historyText(t, w.pd, w.kari.Id); !strings.Contains(text, "pregnancyHealth") {
		t.Error("staff:personal-data does not read the pregnancyHealth kind")
	}

	runCycle(t, h)
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 1 {
		t.Errorf("a second cycle erased again: %v", events)
	}

	// Turned off, nothing is restored.
	setEraseSettings(t, w.hr, map[string]any{"erasePregnancyHealthTypes": false})
	runCycle(t, h)
	if key, _, _ := rowType(t, h, leave.Id); key != "absent_hr_only" {
		t.Errorf("turned off: the erased leave's type = %s, want absent_hr_only kept", key)
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", visit.Id); f["type"] != "changed" {
		t.Errorf("turned off: the erased visit's event = %v, want its type still changed", f)
	}
}

// An event of a pregnancy or health type whose row is gone — deleted, or
// corrected to another type — is erased by its own instant, healthRetentionDays
// after the year it was recorded in: no row is left to judge it by.
func TestRetention_PregnancyAndHealthEventsOfARowGone(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	visit := register(t, hrPD, w.kari.Id, absence(t, h, "helse", "2024-03-05", "2024-03-05", "hours", 2))
	do(t, hrPD, http.MethodDelete, absencePath(visit.Id), nil, http.StatusNoContent, nil)
	h.Exec(t, `UPDATE staff.events SET at = '2024-03-05T10:00:00Z' WHERE (payload->>'absenceId')::bigint = $1`, visit.Id)
	fresh := register(t, hrPD, w.kari.Id, absence(t, h, "helse", "2024-03-06", "2024-03-06", "hours", 1))
	do(t, hrPD, http.MethodDelete, absencePath(fresh.Id), nil, http.StatusNoContent, nil)

	setEraseSettings(t, w.hr, map[string]any{"erasePregnancyHealthTypes": true})
	runCycle(t, h)
	hoursID := typeID(t, h, "absent_hours")
	for _, kind := range []string{"absence_registered", "absence_deleted"} {
		if got := eventTypeIDs(t, h, kind, "absenceId", visit.Id); !slices.Equal(got, []int64{hoursID}) {
			t.Errorf("the deleted 2024 visit's %s typeIds = %v, want [absent_hours]", kind, got)
		}
		// Recorded this year, the other deleted row's events wait for their own year to run.
		if got := eventTypeIDs(t, h, kind, "absenceId", fresh.Id); !slices.Equal(got, []int64{typeID(t, h, "helse")}) {
			t.Errorf("the deleted row recorded in 2026: %s typeIds = %v, want helse kept", kind, got)
		}
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", visit.Id); f["type"] != "changed" {
		t.Errorf("the deleted 2024 visit's event fields = %v, want its type changed", f)
	}
	runCycle(t, h)
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 1 {
		t.Errorf("a second cycle erased again: %v", events)
	}
}

// eventPayload is the newest stored payload of kind naming key = id.
func eventPayload(t *testing.T, h *harness, kind, key string, id int64) map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h.Harness, `SELECT payload::text FROM staff.events
		WHERE kind = $1 AND (payload->>$2)::bigint = $3 ORDER BY id DESC LIMIT 1`, kind, key, id)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s's payload: %v", kind, err)
	}
	return out
}

// With eraseSickCaseDates on, a closed case's erase removes the case itself
// (B's open question 12): its milestones and the case row are deleted, its
// rows unlinked keeping their days as the reserved absent, its documents
// deleted and queued as B's erase does, and its events blanked to their kind
// — no case, no milestone, no field — while its rows' events keep their days
// and lose the case they named. A ferie row, a recent case and a held
// person's case are left; the erased event names cases; a second cycle
// erases nothing more.
func TestRetention_TheCaseEraseRemovesTheCase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	setEraseSettings(t, w.hr, map[string]any{"eraseSickCaseDates": true})

	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14", "degreePercent", 50))
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2025-06-02", "2025-06-06"))
	old := caseOfRow(t, h, sick.Id)
	h.Exec(t, `UPDATE staff.sick_case_milestones SET note = 'Snakket med lege', done_at = '2025-03-10', done_by_user_id = $2
		WHERE case_id = $1 AND kind = 'follow_up_plan'`, old, w.kariUser)
	filed := seedDocument(t, h, w.kari.Id, "health")
	h.Exec(t, `UPDATE staff.documents SET case_id = $1 WHERE id = $2`, old, filed)
	filedKey := modtest.One[string](t, h.Harness, `SELECT object_key FROM staff.documents WHERE id = $1`, filed)

	recent := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-08-07"))
	recentCase := caseOfRow(t, h, recent.Id)
	held, _ := employed(t, w.hr, "Holdt", "2020-01-01", terms())
	heldSick := register(t, w.hr, held.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14"))
	heldCase := caseOfRow(t, h, heldSick.Id)
	do(t, w.pd, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": true, "reason": "A claim"}, http.StatusOK, nil)

	runCycle(t, h)

	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, old); n != 0 {
		t.Error("the erased case's row is kept")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1`, old); n != 0 {
		t.Errorf("%d milestones of the erased case are kept", n)
	}
	if key, _, caseID := rowType(t, h, sick.Id); key != "absent" || caseID != "" {
		t.Errorf("the erased case's row: %s in case %q, want absent and in no case", key, caseID)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND first_day = '2025-03-03' AND last_day = '2025-03-14' AND degree_percent IS NULL`, sick.Id); n != 1 {
		t.Error("the erased case's row lost its days, or kept its degree")
	}
	if key, _, _ := rowType(t, h, holiday.Id); key != "ferie" {
		t.Errorf("the ferie row's type = %s, want ferie", key)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, filed); n != 0 {
		t.Error("the document filed under the case is kept")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, filedKey); n != 1 {
		t.Error("the case document's object is not queued")
	}
	for name, id := range map[string]int64{"the recent case": recentCase, "the held person's case": heldCase} {
		if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, id); n != 1 {
			t.Errorf("%s was removed", name)
		}
		if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1`, id); n == 0 {
			t.Errorf("%s lost its milestones", name)
		}
	}

	n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'case_%' AND payload ? 'caseId'`, w.kari.Id)
	keep := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'case_%' AND (payload->>'caseId')::bigint = $2`, w.kari.Id, recentCase)
	if n != keep {
		t.Errorf("%d case events still name a case, want only the recent case's %d", n, keep)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'case_opened'
		AND NOT payload ? 'caseId' AND payload->'fields' = '{}'::jsonb`, w.kari.Id); n != 1 {
		t.Errorf("blanked case_opened events = %d, want the erased case's one, its kind alone", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND payload ? 'milestone'
		AND NOT (payload ? 'caseId')`, w.kari.Id); n != 0 {
		t.Errorf("%d blanked events still name a milestone", n)
	}
	row := eventPayload(t, h, "absence_registered", "absenceId", sick.Id)
	if _, named := row["rowCaseId"]; named {
		t.Errorf("the erased case's row's event still names the case: %v", row)
	}
	if f, _ := row["fields"].(map[string]any); f["firstDay"] == "changed" || f["type"] != "changed" {
		t.Errorf("the erased case's row's event fields = %v, want the days kept and the type changed", f)
	}
	// Her manager reads the blanked case events as their kind alone.
	if text := historyText(t, w.bossC, w.kari.Id); strings.Contains(text, `"caseId":`+strconv.FormatInt(old, 10)) {
		t.Error("her manager's history still names the erased case")
	}

	if counts := kindCounts(firstErased(t, h, w.kari.Id)); counts["cases"] == 0 {
		t.Errorf("the erased kinds = %v, want cases", counts)
	}
	runCycle(t, h)
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 1 {
		t.Errorf("a second cycle erased again: %v", events)
	}
}

// Turned on after B's erase had run, eraseSickCaseDates reaches the cases
// already erased (their dates and milestones' kinds kept until now): the
// next cycle removes them. Off — the default — the case erase is B's: the
// case keeps its dates and its milestones' kinds and dates. Turned off
// again, nothing is restored.
func TestRetention_TheCaseEraseSettingReachesCasesErasedBefore(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14"))
	old := caseOfRow(t, h, sick.Id)

	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1 AND first_day = '2025-03-03' AND last_day = '2025-03-14'`, old); n != 1 {
		t.Fatal("off: B's case erase removed the case or its dates")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1`, old); n == 0 {
		t.Fatal("off: B's case erase removed the milestones")
	}
	if _, _, caseID := rowType(t, h, sick.Id); caseID == "" {
		t.Fatal("off: B's case erase unlinked the row")
	}

	setEraseSettings(t, w.hr, map[string]any{"eraseSickCaseDates": true})
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, old); n != 0 {
		t.Error("on: the case erased before is kept")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1`, old); n != 0 {
		t.Error("on: the milestones of the case erased before are kept")
	}
	if key, _, caseID := rowType(t, h, sick.Id); key != "absent" || caseID != "" {
		t.Errorf("on: the row is %s in case %q, want absent in none", key, caseID)
	}
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 2 {
		t.Errorf("Kari's erased events = %v, want two: B's and the case's removal", events)
	}

	setEraseSettings(t, w.hr, map[string]any{"eraseSickCaseDates": false})
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Error("turned off: a case came back")
	}
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 2 {
		t.Errorf("turned off, a cycle erased again: %v", events)
	}
}

// An erased HR-only row reads to every leave-reader exactly as it did (the
// coordinator's decision on Task 5b): svangerskapskontroll,
// svangerskapspermisjon and fødselspermisjon take the reserved HR-only pair
// — absent_hr_only for a day row, absent_hr_only_hours for an hour row — so
// for her manager, an Approver and HR without staff:personal-data every read
// of the rows and of Kari's history — the absence list, each row by id, the
// calendar, the inbox, the events page and its count, /stats and the
// attention items — is byte-equal before and after the erase: still
// "leave", the events still unlisted, no erased entry. Kari and
// staff:personal-data read the reserved type.
func TestRetention_AnErasedHROnlyRowReadsAsBefore(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	check := register(t, hrPD, w.kari.Id, absence(t, h, "svangerskapskontroll", "2024-04-09", "2024-04-09", "hours", 1.5))
	leave := register(t, hrPD, w.kari.Id, absence(t, h, "svangerskapspermisjon", "2024-05-06", "2024-05-17"))
	birth := register(t, hrPD, w.kari.Id, absence(t, h, "fodselspermisjon", "2024-06-03", "2024-06-14"))
	rows := []int64{check.Id, leave.Id, birth.Id}
	setEraseSettings(t, w.hr, map[string]any{"erasePregnancyHealthTypes": true})

	paths := []string{
		fmt.Sprintf("%s?personId=%d", absencesPath, w.kari.Id),
		calendarPath + "?from=2024-01-01&to=2024-12-31",
		approvalsPath,
		pagedPath(eventsPath(w.kari.Id), 1, 100),
		statsPath,
		attentionPath,
	}
	for _, id := range rows {
		paths = append(paths, absencePath(id))
	}
	reads := func() map[string]string {
		out := map[string]string{}
		for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "an Approver": w.approver, "HR without personal-data": w.hr} {
			for _, path := range paths {
				r := c.Do(http.MethodGet, path, nil)
				out[name+" GET "+path] = fmt.Sprintf("%d %s", r.Status, scrubRandom(string(r.Body)))
			}
		}
		return out
	}
	before := reads()
	runCycle(t, h)
	hrOnly, hrOnlyHours := typeID(t, h, "absent_hr_only"), typeID(t, h, "absent_hr_only_hours")
	for id, want := range map[int64]string{check.Id: "absent_hr_only_hours", leave.Id: "absent_hr_only", birth.Id: "absent_hr_only"} {
		if key, _, _ := rowType(t, h, id); key != want {
			t.Errorf("row %d's type = %s, want %s", id, key, want)
		}
	}
	after := reads()
	for key, got := range after {
		if want := before[key]; got != want {
			t.Errorf("%s differs after the erase:\n got %s\nwant %s", key, got, want)
		}
	}
	if len(after) != 3*len(paths) {
		t.Errorf("%d reads compared, want %d", len(after), 3*len(paths))
	}
	// Kari and staff:personal-data read the reserved type.
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "personal-data": w.pd} {
		for id, want := range map[int64]int64{check.Id: hrOnlyHours, leave.Id: hrOnly} {
			var got struct {
				TypeID int64 `json:"typeId"`
			}
			do(t, c, http.MethodGet, absencePath(id), nil, http.StatusOK, &got)
			if got.TypeID != want {
				t.Errorf("%s reads row %d's type as %d, want %d", name, id, got.TypeID, want)
			}
		}
	}
	if counts := kindCounts(firstErased(t, h, w.kari.Id)); counts["pregnancyHealth"] == 0 {
		t.Errorf("the erased kinds = %v, want pregnancyHealth", counts)
	}
}

// An erase of health content alone — a closed case, a year's child-sick
// rows, a lapsed withdrawal — lists and counts nothing new to a reader
// without staff:personal-data (the coordinator's follow-up to Task 5b): its
// erased event is of class E, the health group's, so HR without
// staff:personal-data and her manager are listed the same events, by id and
// kind, and the same count before and after it — the erase blanks values in
// events they already read, which is the erase. staff:personal-data reads
// the erased entry with its kind.
func TestRetention_AHealthEraseListsNothingNewToOthers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, kind string
		setup      func(w decisionWorld)
	}{
		{"a closed case", "cases", func(w decisionWorld) {
			register(t, w.hr, w.kari.Id, absence(t, w.h, "sykmelding", "2025-03-03", "2025-03-14"))
		}},
		{"a child-sick year", "childSick", func(w decisionWorld) {
			register(t, w.hr, w.kari.Id, absence(t, w.h, "sykt_barn", "2024-11-04", "2024-11-05"))
		}},
		{"a lapsed withdrawal", "withdrawal", func(w decisionWorld) {
			do(t, w.hr, http.MethodPut, personPath(w.kari.Id)+"/self-certification/withdrawal",
				map[string]any{"until": "2026-12-31", "reason": "Repeated short absences"}, http.StatusOK, nil)
			// Lapsed in 2024 and recorded then: due now, with no clock to move
			// past the sessions.
			w.h.Exec(t, `UPDATE staff.people SET self_cert_withdrawn_from = '2024-01-01', self_cert_withdrawn_until = '2024-06-30' WHERE id = $1`, w.kari.Id)
			w.h.Exec(t, `UPDATE staff.events SET at = '2024-01-01T10:00:00Z' WHERE person_id = $1 AND payload ? 'selfCertWithdrawal'`, w.kari.Id)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newDecisionWorld(t)
			c.setup(w)
			listed := func() map[string]string {
				out := map[string]string{}
				for name, cl := range map[string]*modtest.Client{"HR without personal-data": w.hr, "her manager": w.bossC, "an Approver": w.approver} {
					r := cl.Do(http.MethodGet, pagedPath(eventsPath(w.kari.Id), 1, 100), nil)
					var page struct {
						Data []struct {
							ID   int64  `json:"id"`
							Kind string `json:"kind"`
						} `json:"data"`
						Pagination struct {
							TotalCount int64 `json:"totalCount"`
						} `json:"pagination"`
					}
					_ = json.Unmarshal(r.Body, &page)
					out[name] = fmt.Sprintf("%d %d %v", r.Status, page.Pagination.TotalCount, page.Data)
				}
				return out
			}
			before := listed()
			runCycle(t, w.h)
			if counts := kindCounts(firstErased(t, w.h, w.kari.Id)); counts[c.kind] == 0 {
				t.Fatalf("the erased kinds = %v, want %s", counts, c.kind)
			}
			for name, got := range listed() {
				if want := before[name]; got != want {
					t.Errorf("%s is listed something new after the erase:\n got %s\nwant %s", name, got, want)
				}
			}
			if !strings.Contains(historyText(t, w.pd, w.kari.Id), `"kind":"`+c.kind+`"`) {
				t.Errorf("staff:personal-data does not read the erased entry's %s", c.kind)
			}
		})
	}
}
