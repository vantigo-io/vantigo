package staff_test

import (
	"net/http"
	"testing"
)

// A case's rows and the case itself stay consistent across the writes that
// change what a row is (whole-branch review, critical): a case any absence
// still names is never removed, nor one HR closed or the retention run
// erased, and a row that stops being sick leaves its case.

// A case the retention run erased keeps its rows — of the reserved absent
// type, still naming it — so it is no longer derived from them; a later
// sykmelding opens a case of its own and the erased one stays, with its days.
func TestCases_AnErasedCaseOutlivesANewSickRow(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	old := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-14"))
	erased := caseOfRow(t, h, old.Id)
	runCycle(t, h)

	r := w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "sykmelding", "2026-08-03", "2026-08-07"), "personId", w.kari.Id))
	if r.Status != http.StatusCreated {
		t.Fatalf("a sykmelding after an erased case: %d %s, want 201", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1 AND first_day = '2025-03-03' AND last_day = '2025-03-14'`, erased); n != 1 {
		t.Error("the erased case, or its days, did not survive the new sykmelding")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND case_id = $2`, old.Id, erased); n != 1 {
		t.Error("the erased case's row no longer names it")
	}
	// Its close is the derived one — its last day + 17, at midnight in the
	// settings' zone — not an HR-style close dated the erase (retention
	// re-check).
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases c
		WHERE c.id = $1 AND c.closed_at = ((c.last_day + 17)::timestamp AT TIME ZONE
		    coalesce((SELECT business_time_zone FROM identity.system_settings WHERE id = 1), 'Europe/Oslo'))`, erased); n != 1 {
		t.Error("the erased case's closed_at is not its derived close")
	}
}

// An event naming a case that no longer exists (rowCaseId) is an orphan,
// erased by its own instant, even when its row has since become a child-sick
// one: the child-sick schedule — the end of the row's year — owns only
// events that never named a case (retention re-check).
func TestRetention_AnEventOfAGoneCaseIsAnOrphan(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-01", "2026-09-01"))
	c := caseOfRow(t, h, sick.Id)
	do(t, w.hr, http.MethodPut, absencePath(sick.Id), absence(t, h, "sykt_barn", "2026-09-01", "2026-09-01"), http.StatusOK, nil, ifMatchAs(t, w.hr, sick.Id))
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, c); n != 0 {
		t.Fatal("the case outlived its only row")
	}
	setClock(t, h, h.Now().AddDate(0, 0, 365+17+1))
	runCycle(t, h)
	if f := eventFields(t, h, "absence_registered", "absenceId", sick.Id); f["type"] != "changed" {
		t.Errorf("the gone case's registration kept its type %v, waiting on the child-sick row's year", f["type"])
	}
}

// A sick row changed into a type that is not sick — by HR's change of a
// registration or by a correction — leaves its case and its degree, and the
// case it leaves empty goes; neither write fails on the case's key.
func TestCases_ASickRowChangedToAnotherKind(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	for _, change := range []struct {
		name  string
		path  func(int64) string
		via   string
		key   string
		extra []any
	}{
		{"changed to sykt barn", absencePath, http.MethodPut, "sykt_barn", nil},
		{"changed to helse", absencePath, http.MethodPut, "helse", []any{"hours", 2}},
		{"corrected to sykt barn", correctPath, http.MethodPost, "sykt_barn", nil},
		{"corrected to helse", correctPath, http.MethodPost, "helse", []any{"hours", 2}},
	} {
		sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-01", "2026-09-01", "degreePercent", 50))
		c := caseOfRow(t, h, sick.Id)
		r := w.hr.Do(change.via, change.path(sick.Id), absence(t, h, change.key, "2026-09-01", "2026-09-01", change.extra...), ifMatchAs(t, w.hr, sick.Id))
		if r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s, want 200", change.name, r.Status, r.Body)
		}
		if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE id = $1 AND case_id IS NULL AND degree_percent IS NULL`, sick.Id); n != 1 {
			t.Errorf("%s: the row still names a case or keeps a degree", change.name)
		}
		if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1`, c); n != 0 {
			t.Errorf("%s: the case the row left empty was kept", change.name)
		}
		h.Exec(t, `DELETE FROM staff.absences WHERE id = $1`, sick.Id)
	}
}

// A case HR closed keeps its milestones and notes when every row is gone
// from it: the employer's follow-up record is not removed with the rows.
func TestCases_AClosedCaseOutlivesItsRows(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-03", "2026-08-07"))
	c := caseOfRow(t, h, sick.Id)
	h.Exec(t, `UPDATE staff.sick_case_milestones SET note = 'Samtale holdt' WHERE case_id = $1 AND kind = 'follow_up_plan'`, c)
	do(t, w.hr, http.MethodPost, casePath(c)+"/close", nil, http.StatusOK, nil)
	do(t, w.hr, http.MethodDelete, absencePath(sick.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_cases WHERE id = $1 AND closed_at IS NOT NULL`, c); n != 1 {
		t.Fatal("HR's closed case was removed with its last row")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.sick_case_milestones WHERE case_id = $1 AND kind = 'follow_up_plan' AND note = 'Samtale holdt'`, c); n != 1 {
		t.Error("the closed case lost its milestone's note")
	}
}

// A sick row's registration event names its case (rowCaseId), so a row
// deleted from a case has its whole history erased with the case.
func TestCases_ADeletedRowsRegistrationIsErasedWithItsCase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	kept := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-07"))
	gone := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-10", "2025-03-14"))
	c := caseOfRow(t, h, kept.Id)
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'absence_registered'
		AND (payload->>'absenceId')::bigint = $1 AND (payload->>'rowCaseId')::bigint = $2`, gone.Id, c); n != 1 {
		t.Error("the registration does not name the row's case")
	}
	do(t, w.hr, http.MethodDelete, absencePath(gone.Id), nil, http.StatusNoContent, nil)
	runCycle(t, h)
	if f := eventFields(t, h, "absence_registered", "absenceId", gone.Id); f["type"] != "changed" {
		t.Errorf("the deleted row's registration: type = %v after its case's erase, want changed", f["type"])
	}
}
