package staff_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The health erase on the case's schedule (design S15, §4 Erase; plan
// Task 6): a closed sick-leave case's health content healthRetentionDays
// after it closed, whatever the person's employment; a year's child-sick
// rows healthRetentionDays after the year; a withdrawal of egenmelding
// healthRetentionDays after it lapsed.

// eventFields is the fields of the newest event of kind naming key = id.
func eventFields(t *testing.T, h *harness, kind, key string, id int64) map[string]any {
	t.Helper()
	return modtest.One[map[string]any](t, h.Harness, `SELECT payload->'fields' FROM staff.events
		WHERE kind = $1 AND (payload->>$2)::bigint = $3 ORDER BY id DESC LIMIT 1`, kind, key, id)
}

// eventTypeIDs is the typeIds of the newest event of kind naming key = id.
func eventTypeIDs(t *testing.T, h *harness, kind, key string, id int64) []int64 {
	t.Helper()
	return modtest.One[[]int64](t, h.Harness, `SELECT ARRAY(SELECT jsonb_array_elements_text(payload->'typeIds')::bigint)
		FROM staff.events WHERE kind = $1 AND (payload->>$2)::bigint = $3 ORDER BY id DESC LIMIT 1`, kind, key, id)
}

// caseOfRow is the case an absence belongs to.
func caseOfRow(t *testing.T, h *harness, absenceID int64) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT case_id FROM staff.absences WHERE id = $1`, absenceID)
}

// A case closed more than healthRetentionDays ago — on the derived close,
// sixteen days after its last day — is erased while its person still works:
// its sick rows' type becomes the reserved absent and their degree goes,
// the case's accommodation note and estimate and its milestones' facts and
// notes go (their dates stay), the documents filed under it are deleted and
// queued, and the health fields of its events and its rows' events are
// replaced — the case events' types among them. A ferie row beside it, an
// unattached health document, a recent case and a held person's case are
// left; a second cycle erases nothing more; the erased event names cases.
func TestRetention_TheCaseErase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	absent, sykmelding, ferie := typeID(t, h, "absent"), typeID(t, h, "sykmelding"), typeID(t, h, "ferie")

	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14", "degreePercent", 50))
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2025-06-02", "2025-06-06"))
	old := caseOfRow(t, h, sick.Id)
	h.Exec(t, `UPDATE staff.sick_cases SET accommodation_note = 'Lettere oppgaver', max_benefit_estimate = '2026-03-01' WHERE id = $1`, old)
	h.Exec(t, `UPDATE staff.sick_case_milestones SET note = 'Snakket med lege', done_at = '2025-03-10', done_by_user_id = $2
		WHERE case_id = $1 AND kind = 'follow_up_plan'`, old, w.kariUser)
	h.Exec(t, `UPDATE staff.sick_case_milestones SET waived = true, waived_reason = 'Friskmeldt', waived_by_user_id = $2
		WHERE case_id = $1 AND kind = 'dialogue_1'`, old, w.kariUser)
	filed := seedDocument(t, h, w.kari.Id, "health")
	h.Exec(t, `UPDATE staff.documents SET case_id = $1 WHERE id = $2`, old, filed)
	filedKey := modtest.One[string](t, h.Harness, `SELECT object_key FROM staff.documents WHERE id = $1`, filed)
	loose := seedDocument(t, h, w.kari.Id, "health")

	recent := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-08-07"))

	held, _ := employed(t, w.hr, "Holdt", "2020-01-01", terms())
	heldSick := register(t, w.hr, held.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14"))
	do(t, w.pd, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": true, "reason": "A claim"}, http.StatusOK, nil)

	runCycle(t, h)

	row := func(id int64) (int64, bool) {
		var degree *float64
		typ := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, id)
		degree = modtest.One[*float64](t, h.Harness, `SELECT degree_percent::float8 FROM staff.absences WHERE id = $1`, id)
		return typ, degree != nil
	}
	if typ, hasDegree := row(sick.Id); typ != absent || hasDegree {
		t.Errorf("the erased case's row: type %d, degree kept %v; want absent (%d) and no degree", typ, hasDegree, absent)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND first_day = '2025-03-03' AND last_day = '2025-03-14'`, sick.Id); n != 1 {
		t.Error("the erased case's row lost its days, which are class C")
	}
	if typ, _ := row(holiday.Id); typ != ferie {
		t.Errorf("the ferie row's type = %d, want ferie kept", typ)
	}
	for _, id := range []int64{recent.Id, heldSick.Id} {
		if typ, _ := row(id); typ != sykmelding {
			t.Errorf("row %d's type = %d, want sykmelding kept", id, typ)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1 AND accommodation_note IS NULL
		AND max_benefit_estimate IS NULL AND first_day = '2025-03-03' AND last_day = '2025-03-14'`, old); n != 1 {
		t.Error("the case keeps its notes, or lost its dates")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1 AND (note IS NOT NULL OR done_at IS NOT NULL
		OR done_by_user_id IS NOT NULL OR waived OR waived_reason IS NOT NULL OR waived_by_user_id IS NOT NULL)`, old); n != 0 {
		t.Errorf("%d milestones keep their facts", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1 AND kind = 'follow_up_plan' AND due_date = '2025-03-31'`, old); n != 1 {
		t.Error("the follow-up plan's date was lost")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, filed); n != 0 {
		t.Error("the document filed under the case is kept")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, filedKey); n != 1 {
		t.Error("the case document's object is not queued")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, loose); n != 1 {
		t.Error("an unattached health document of a current employee was deleted; it is on A2's schedule")
	}

	f := eventFields(t, h, "absence_registered", "absenceId", sick.Id)
	for _, k := range []string{"type", "degree"} {
		if f[k] != "changed" {
			t.Errorf("the sick row's event: %s = %v, want changed", k, f[k])
		}
	}
	if f["firstDay"] == "changed" {
		t.Error("the sick row's event lost its first day, class C")
	}
	if got := eventTypeIDs(t, h, "absence_registered", "absenceId", sick.Id); !slices.Equal(got, []int64{absent}) {
		t.Errorf("the sick row's event typeIds = %v, want [absent]", got)
	}
	for k, v := range eventFields(t, h, "case_opened", "caseId", old) {
		if v != "changed" {
			t.Errorf("case_opened: %s = %v, want changed", k, v)
		}
	}
	if got := eventTypeIDs(t, h, "case_opened", "caseId", old); !slices.Equal(got, []int64{absent}) {
		t.Errorf("case_opened's typeIds = %v, want [absent]", got)
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", recent.Id); f["type"] == "changed" {
		t.Error("a recent case's row's event was erased")
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", heldSick.Id); f["type"] == "changed" {
		t.Error("a held person's case was erased")
	}

	events := erasedEvents(t, h, w.kari.Id)
	if len(events) != 1 {
		t.Fatalf("Kari's erased events = %v, want one", events)
	}
	if counts := kindCounts(events[0]); counts["cases"] == 0 || len(counts) != 1 {
		t.Errorf("the erased kinds = %v, want cases alone", counts)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL`, w.kari.Id); n != 1 {
		t.Error("the case erase marked a current employee erased")
	}
	runCycle(t, h)
	if events := erasedEvents(t, h, w.kari.Id); len(events) != 1 {
		t.Errorf("a second cycle erased again: %v", events)
	}
}

// A child-sick row's type becomes the reserved absent healthRetentionDays
// after its year ended (plan reading 21), and its events' type is replaced;
// this year's is kept. The erased event names childSick.
func TestRetention_ChildSickRows(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	old := register(t, w.hr, w.kari.Id, absence(t, h, "sykt_barn", "2024-11-04", "2024-11-05"))
	lastYear := register(t, w.hr, w.kari.Id, absence(t, h, "sykt_barn", "2025-11-03", "2025-11-04"))
	runCycle(t, h)
	if got := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, old.Id); got != typeID(t, h, "absent") {
		t.Errorf("the 2024 child-sick row's type = %d, want absent", got)
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", old.Id); f["type"] != "changed" || f["firstDay"] == "changed" {
		t.Errorf("the 2024 row's event fields = %v, want the type changed and the days kept", f)
	}
	if got := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, lastYear.Id); got != typeID(t, h, "sykt_barn") {
		t.Errorf("the 2025 child-sick row's type = %d, want kept until 2026 is over and a year has run", got)
	}
	if counts := kindCounts(firstErased(t, h, w.kari.Id)); counts["childSick"] == 0 {
		t.Errorf("the erased kinds = %v, want childSick", counts)
	}
}

// A withdrawal of egenmelding is erased healthRetentionDays after it
// lapsed: its columns nulled and its events' values replaced. The erased
// event names withdrawal.
func TestRetention_TheWithdrawal(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	do(t, w.hr, http.MethodPut, personPath(w.kari.Id)+"/self-certification/withdrawal",
		map[string]any{"until": "2026-12-31", "reason": "Repeated short absences"}, http.StatusOK, nil)
	runCycle(t, h)
	if len(erasedEvents(t, h, w.kari.Id)) != 0 {
		t.Fatal("a withdrawal in force was erased")
	}
	setClock(t, h, time.Date(2027, time.December, 30, 12, 0, 0, 0, time.UTC))
	runCycle(t, h)
	if len(erasedEvents(t, h, w.kari.Id)) != 0 {
		t.Fatal("a withdrawal was erased before healthRetentionDays had run since it lapsed")
	}
	setClock(t, h, time.Date(2028, time.January, 2, 12, 0, 0, 0, time.UTC))
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND self_cert_withdrawn_until IS NULL
		AND self_cert_withdrawn_reason IS NULL AND self_cert_withdrawn_from IS NULL`, w.kari.Id); n != 1 {
		t.Error("the lapsed withdrawal's columns are kept")
	}
	raw := modtest.One[string](t, h.Harness, `SELECT payload::text FROM staff.events WHERE kind = 'self_cert_withdrawn' AND person_id = $1`, w.kari.Id)
	if strings.Contains(raw, "Repeated") || strings.Contains(raw, "2026-12-31") {
		t.Errorf("the withdrawal's event keeps its values: %s", raw)
	}
	if counts := kindCounts(firstErased(t, h, w.kari.Id)); counts["withdrawal"] == 0 {
		t.Errorf("the erased kinds = %v, want withdrawal", counts)
	}
}

// What a case owns goes on whichever schedule comes first (whole-branch
// review): a leaver whose employments all ended more than healthRetentionDays
// ago, but whose last case closed since, loses that case's rows' types, its
// events' health fields and the document filed under it with the rest of
// their health data — the person's own cutoff caps the case's.
func TestRetention_ThePersonsHealthCutoffCapsTheCase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	p, e := employed(t, w.hr, "Sluttet", "2020-01-01", terms())
	sick := register(t, w.hr, p.Id, absence(t, h, "sykmelding", "2025-08-25", "2025-08-29"))
	endEmployment(t, w.hr, e.Id, "2025-09-01")
	filed := seedDocument(t, h, p.Id, "health")
	h.Exec(t, `UPDATE staff.documents SET case_id = $1 WHERE id = $2`, caseOfRow(t, h, sick.Id), filed)
	loose := seedDocument(t, h, p.Id, "health")

	runCycle(t, h)
	for name, id := range map[string]int64{"unattached": loose, "filed under the case": filed} {
		if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, id); n != 0 {
			t.Errorf("the leaver's %s health document is kept past their health cutoff", name)
		}
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", sick.Id); f["type"] != "changed" {
		t.Error("the case's event kept its health fields past the person's health cutoff")
	}
	if got := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, sick.Id); got != typeID(t, h, "absent") {
		t.Errorf("the case's row's type = %d, want absent", got)
	}
}

// What a case owns reaches past its rows of today: the events of a case it
// took in by a merge, a ferie split for one of its rows (sickAbsenceId), and
// a row deleted from it — whose events name the case (rowCaseId) — are
// erased with it.
func TestRetention_TheCaseEraseReachesWhatItOwns(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	first := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-07"))
	firstCase := caseOfRow(t, h, first.Id)
	later := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-04-07", "2025-04-11"))
	laterCase := caseOfRow(t, h, later.Id)
	if laterCase == firstCase {
		t.Fatal("the two rows share a case before the bridge")
	}
	// A row between them joins the two cases: the later is merged into the
	// first.
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-10", "2025-03-28"))
	if caseOfRow(t, h, later.Id) != firstCase {
		t.Fatal("the bridge did not merge the cases")
	}
	// A row added to the case and deleted again: its events name the case.
	extra := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-04-14", "2025-04-15"))
	do(t, w.hr, http.MethodDelete, absencePath(extra.Id), nil, http.StatusNoContent, nil)
	// Ferie over a sykmelding of the case, postponed on demand.
	holiday := register(t, w.hr, w.kari.Id, absence(t, h, "ferie", "2025-04-16", "2025-04-25"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, holiday.Id)
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-04-21", "2025-04-25"))
	do(t, w.hr, http.MethodPost, postponePath(sick.Id), map[string]any{"demandedOn": "2025-04-22"}, http.StatusOK, nil)

	runCycle(t, h)
	if f := eventFields(t, h, "case_opened", "caseId", laterCase); f["firstDay"] != "changed" {
		t.Errorf("the merged-in case's case_opened = %v, want its fields changed", f)
	}
	if f := eventFields(t, h, "absence_deleted", "absenceId", extra.Id); f["type"] != "changed" {
		t.Errorf("the deleted row's absence_deleted = %v, want its type changed", f)
	}
	if f := eventFields(t, h, "absence_split", "absenceId", holiday.Id); f["demandedOn"] != "changed" || f["postponedUnder"] != "changed" {
		t.Errorf("the ferie's absence_split = %v, want the demand changed", f)
	}
	if f := eventFields(t, h, "absence_split", "absenceId", holiday.Id); f["firstDay"] == "changed" {
		t.Error("the ferie's absence_split lost its days")
	}
}

// A sick row's events that no case owns any more — the case removed with
// its only row — are erased by their own instant: sixteen days and
// healthRetentionDays after they were recorded, whatever the employment.
func TestRetention_OrphanedSickEventsGoByTheirInstant(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-07"))
	gone := caseOfRow(t, h, sick.Id)
	do(t, w.hr, http.MethodDelete, absencePath(sick.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, gone); n != 0 {
		t.Fatal("the case outlived its only row")
	}
	runCycle(t, h)
	if f := eventFields(t, h, "absence_registered", "absenceId", sick.Id); f["type"] == "changed" {
		t.Fatal("an orphaned event recorded today was erased")
	}
	// The bound is the case's own (whole-branch review): the latest a case
	// can close is its last day + 17 (caseClosesAfter), so an orphan waits
	// healthRetentionDays and seventeen days, not sixteen.
	start := h.Now()
	setClock(t, h, start.AddDate(0, 0, 365+17))
	runCycle(t, h)
	if f := eventFields(t, h, "absence_registered", "absenceId", sick.Id); f["type"] == "changed" {
		t.Fatal("an orphaned event was erased on the seventeenth day, before the case's own bound")
	}
	setClock(t, h, start.AddDate(0, 0, 365+17+1))
	runCycle(t, h)
	for _, c := range []struct {
		kind, key string
		id        int64
	}{
		{"absence_registered", "absenceId", sick.Id}, {"absence_deleted", "absenceId", sick.Id}, {"case_opened", "caseId", gone},
	} {
		if f := eventFields(t, h, c.kind, c.key, c.id); f["type"] != "changed" && f["firstDay"] != "changed" {
			t.Errorf("%s = %v, want its health fields changed", c.kind, f)
		}
	}
	if counts := kindCounts(firstErased(t, h, w.kari.Id)); counts["cases"] == 0 {
		t.Errorf("the erased kinds = %v, want cases", counts)
	}
}

// The withdrawal's schedule judges its columns and its events apart: a
// withdrawal that lapsed long ago is erased with its old events, while an
// event recorded since keeps its values until healthRetentionDays have run
// from it.
func TestRetention_TheWithdrawalsEventsByTheirInstant(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET self_cert_withdrawn_until = '2025-01-31', self_cert_withdrawn_from = '2024-11-01',
		self_cert_withdrawn_reason = 'Old' WHERE id = $1`, w.kari.Id)
	insert := func(at time.Time, reason string) int64 {
		return modtest.One[int64](t, h.Harness, `INSERT INTO staff.events (person_id, kind, actor_kind, at, payload, payload_classes)
			VALUES ($1, 'self_cert_withdrawn', 'system', $2,
				jsonb_build_object('selfCertWithdrawal', true, 'fields', jsonb_build_object('reason', jsonb_build_object('to', $3::text)),
					'classes', jsonb_build_object('reason', 'E')), '{E}') RETURNING id`, w.kari.Id, at, reason)
	}
	old := insert(time.Date(2024, time.November, 1, 9, 0, 0, 0, time.UTC), "Old")
	recent := insert(h.Now().Add(-time.Hour), "Recent")
	runCycle(t, h)
	reason := func(id int64) string {
		return modtest.One[string](t, h.Harness, `SELECT (payload->'fields'->'reason')::text FROM staff.events WHERE id = $1`, id)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND self_cert_withdrawn_until IS NULL`, w.kari.Id); n != 1 {
		t.Error("the long-lapsed withdrawal's columns are kept")
	}
	if got := reason(old); got != `"changed"` {
		t.Errorf("the old event's reason = %s, want changed", got)
	}
	if got := reason(recent); got == `"changed"` {
		t.Error("an event recorded an hour ago was erased")
	}
}

// A leaver's owned health content goes on their own health schedule when it
// comes first (whole-branch review): healthRetentionDays after their last
// employment ended, a child-sick row of a year not yet past the cutoff and a
// withdrawal of egenmelding still in force are erased with the rest of
// their health data.
func TestRetention_ALeaversOwnedHealthGoesWithTheirs(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	leaver, job := employed(t, w.hr, "Leaver", "2020-01-01", terms())
	child := register(t, w.hr, leaver.Id, absence(t, h, "sykt_barn", "2025-05-05", "2025-05-05"))
	do(t, w.hr, http.MethodPost, endPath(job.Id), map[string]any{"endDate": "2025-06-30", "endReason": "employee_resigned"}, http.StatusOK, nil)
	h.Exec(t, `UPDATE staff.people SET self_cert_withdrawn_until = '2027-01-31', self_cert_withdrawn_from = '2025-05-01',
		self_cert_withdrawn_reason = 'Gjentatt fravær' WHERE id = $1`, leaver.Id)

	runCycle(t, h)

	if typ := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, child.Id); typ != typeID(t, h, "absent") {
		t.Errorf("the leaver's child-sick row kept its type %d past their health cutoff", typ)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND self_cert_withdrawn_until IS NULL AND self_cert_withdrawn_reason IS NULL`, leaver.Id); n != 1 {
		t.Error("the leaver's withdrawal outlived their health cutoff")
	}
}
