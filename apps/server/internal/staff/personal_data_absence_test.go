package staff_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// What absence adds to the person's data in one file (design S16; plan
// Task 6): every absence, the holiday adjustments, every sick-leave case with
// its milestones, this year's care days, the standing of egenmelding and the
// care-day fields — the complete file whole, the person's own copy as they
// read it.
func TestPersonalData_AbsenceInBothFiles(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET children_under_12 = 2, sole_carer = true, chronically_ill_children = 1 WHERE id = $1`, w.kari.Id)
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-08-10", "2026-08-21", "degreePercent", 50))
	register(t, w.hr, w.kari.Id, absence(t, h, "sykt_barn", "2026-09-01", "2026-09-02"))
	do(t, w.hr, http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 2, "reason": "correction", "note": "Avtalt"},
		http.StatusCreated, nil)
	h.Exec(t, `UPDATE staff.sick_case_milestones SET note = 'Samtale' WHERE kind = 'follow_up_plan'
		AND case_id = (SELECT case_id FROM staff.absences WHERE id = $1)`, sick.Id)

	check := func(name string, f gen.StaffPersonalData) {
		t.Helper()
		if len(f.Absences) != 3 {
			t.Errorf("%s: absences = %d, want 3", name, len(f.Absences))
		}
		if len(f.HolidayAdjustments) != 1 {
			t.Errorf("%s: holidayAdjustments = %d, want 1", name, len(f.HolidayAdjustments))
		}
		if len(f.Cases) != 1 || len(f.Cases[0].Milestones) == 0 {
			t.Fatalf("%s: cases = %+v, want one with its milestones", name, f.Cases)
		}
		var note *string
		for _, m := range f.Cases[0].Milestones {
			if m.Kind == "follow_up_plan" {
				note = m.Note
			}
		}
		if note == nil || *note != "Samtale" {
			t.Errorf("%s: the follow-up plan's note = %v, want it — the person and staff:personal-data read it", name, note)
		}
		if f.CareDays.Year != 2026 || f.CareDays.Entitlement != 50 || f.CareDays.Used != 2 {
			t.Errorf("%s: careDays = %+v, want 2026's 50 (a sole carer of three, one chronically ill: 30 + 20), 2 used", name, f.CareDays)
		}
		if f.SelfCertification.PersonId != w.kari.Id {
			t.Errorf("%s: selfCertification = %+v, want Kari's", name, f.SelfCertification)
		}
		p := f.Person
		if p.ChildrenUnder12 == nil || *p.ChildrenUnder12 != 2 || p.SoleCarer == nil || !*p.SoleCarer ||
			p.ChronicallyIllChildren == nil || *p.ChronicallyIllChildren != 1 {
			t.Errorf("%s: the care-day fields = %v %v %v, want 2, true, 1", name, p.ChildrenUnder12, p.SoleCarer, p.ChronicallyIllChildren)
		}
	}
	check("the complete file", exportOf(t, w.pd.Do(http.MethodGet, personalDataPath(w.kari.Id), nil)))
	check("my own copy", exportOf(t, w.kariC.Do(http.MethodGet, meExportPath, nil)))
}

// A person with an absence or a holiday adjustment is in use: absence is
// the history of their employment, and the delete of one created in error
// would take it with them.
func TestDeletePerson_RefusedWithAbsenceOrAdjustments(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	absent := createPerson(t, w.hr, newPerson("Har", "Fravær"))
	adjusted := createPerson(t, w.hr, newPerson("Har", "Justering"))
	h.Exec(t, `INSERT INTO staff.absences (person_id, type_id, first_day, last_day, status, requested_by_user_id, created_at, updated_at)
		VALUES ($1, $2, '2026-09-01', '2026-09-01', 'registered', $3, $4, $4)`, absent.Id, typeID(t, h, "helse"), w.kariUser, h.Now())
	h.Exec(t, `INSERT INTO staff.holiday_adjustments (person_id, year, days, reason, by_user_id, created_at, updated_at)
		VALUES ($1, 2026, 1, 'correction', $2, $3, $3)`, adjusted.Id, w.kariUser, h.Now())
	for name, c := range map[int64]string{absent.Id: "has absences", adjusted.Id: "has holiday adjustments"} {
		p := refused(t, w.hr.Do(http.MethodDelete, personPath(name), nil), http.StatusConflict, "person_in_use")
		if !strings.Contains(p.Detail, c) {
			t.Errorf("person %d: detail %q, want it to say %q", name, p.Detail, c)
		}
		if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1`, name); n != 1 {
			t.Errorf("person %d is gone after a refused delete", name)
		}
	}
}
