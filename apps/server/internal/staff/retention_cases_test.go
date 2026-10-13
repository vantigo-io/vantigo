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

// The person's health schedule leaves what a case owns to the case's: a
// leaver whose employments all ended more than healthRetentionDays ago, but
// whose last case closed since, keeps that case's rows' types, its events'
// health fields and the document filed under it until the case's own time
// has run; a health document filed under no case goes on the person's.
func TestRetention_ThePersonsHealthEraseLeavesTheCase(t *testing.T) {
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
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, loose); n != 0 {
		t.Error("the leaver's unattached health document is kept past healthRetentionDays")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, filed); n != 1 {
		t.Error("the person's health erase deleted a document the case owns, whose case closed under a year ago")
	}
	if f := eventFields(t, h, "absence_registered", "absenceId", sick.Id); f["type"] == "changed" {
		t.Error("the person's health erase blanked an event the case owns")
	}
	if got := modtest.One[int64](t, h.Harness, `SELECT type_id FROM staff.absences WHERE id = $1`, sick.Id); got != typeID(t, h, "sykmelding") {
		t.Errorf("the recent case's row's type = %d, want sykmelding", got)
	}
}
