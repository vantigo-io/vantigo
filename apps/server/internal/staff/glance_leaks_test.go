package staff_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// leafPaths is every leaf of a JSON object as its dotted path, sorted: what
// a response tells its reader, key by key, a null included.
func leafPaths(m map[string]any) []string {
	var out []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		obj, ok := v.(map[string]any)
		if !ok {
			out = append(out, prefix)
			return
		}
		for k, child := range obj {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			walk(p, child)
		}
	}
	walk("", m)
	slices.Sort(out)
	return out
}

// glanceKeys is the paths of the glance's cards: every audience that reads
// the absence detail gets base, and each extra list on top of it.
var (
	glanceSpan = []string{"firstDay", "lastDay", "shownAs", "typeId"}
	glanceBase = []string{
		"sickDays.selfCertified", "sickDays.certified",
		"selfCertification.occurrencesUsed", "selfCertification.occurrencesLimit", "selfCertification.daysUsed",
		"holiday.takenToDate", "holiday.planned", "holiday.remaining", "holiday.overdrawn",
		"childSick.days",
		"employment.startDate", "employment.tenureMonths", "employment.ftePercent", "employment.title", "employment.probationEndsOn",
		"absence.openCase.firstDay", "absence.openCase.day",
	}
	glanceCare     = []string{"childSick.entitlement", "childSick.remaining"}
	glanceHours    = []string{"hours.timeTracking", "hours.weekStart", "hours.logged", "hours.approved", "hours.expected"}
	glanceContract = []string{"contract.signed"}
)

// glancePaths is base, both spans with fields, and the extras, sorted.
func glancePaths(span []string, extras ...[]string) []string {
	out := slices.Clone(glanceBase)
	for _, which := range []string{"now", "next"} {
		for _, f := range span {
			out = append(out, "absence."+which+"."+f)
		}
	}
	for _, e := range extras {
		out = append(out, e...)
	}
	slices.Sort(out)
	return out
}

// Every audience's glance carries exactly the keys its groups read, nothing
// more: the care-day entitlement to the person and HR, the hours to the
// person, the manager and HR, the contract to staff:manage alone, the
// probation and the case to no colleague, who reads the employment's dates
// and the absence as spans without a type. No note — the ferie's, the
// case's accommodation note — reaches anyone through it.
func TestGlance_Leaks(t *testing.T) {
	t.Parallel()
	w := newGlanceWorld(t)
	colleague := []string{
		"employment.startDate", "employment.tenureMonths", "employment.ftePercent", "employment.title",
		"absence.now.firstDay", "absence.now.lastDay", "absence.now.shownAs",
		"absence.next.firstDay", "absence.next.lastDay", "absence.next.shownAs",
	}
	slices.Sort(colleague)
	for _, c := range []struct {
		name string
		c    *modtest.Client
		want []string
	}{
		{"the person", w.kariC, glancePaths(glanceSpan, glanceCare, glanceHours)},
		{"her manager", w.bossC, glancePaths(glanceSpan, glanceHours)},
		{"the Approver", w.approver, glancePaths(glanceSpan)},
		{"HR", w.hr, glancePaths(glanceSpan, glanceCare, glanceHours, glanceContract)},
		{"personal-data", w.pd, glancePaths(glanceSpan, glanceCare, glanceHours)},
		{"a colleague", w.viewer, colleague},
	} {
		r := c.c.Do(http.MethodGet, glancePath(w.kari.Id), nil)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: status %d body %s", c.name, r.Status, r.Body)
		}
		if got := leafPaths(raw(t, r)); !slices.Equal(got, c.want) {
			t.Errorf("%s: keys\n got %v\nwant %v", c.name, got, c.want)
		}
		for _, secret := range []string{"Høstferie", "Tilrettelegging", "kortere dager", "note", "Note"} {
			if strings.Contains(string(r.Body), secret) {
				t.Errorf("%s: the glance carries %q: %s", c.name, secret, r.Body)
			}
		}
	}
}

// An HR-only absence is shown through the glance as GET /absences shows the
// row: as itself to the person and staff:personal-data, as leave — no type —
// to the manager and to HR without personal data, as absent to a colleague;
// and an HR-only request is no one's next but theirs who read it as itself,
// since to a reader of it as leave every HR-only row is an approved one.
func TestGlance_AnHROnlyAbsence(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	hrOnly := typeID(t, h, "svangerskapspermisjon")
	now := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-09-07", "2026-09-18"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, now.Id)
	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-10-05", "2026-10-09"))

	for _, c := range []struct {
		name    string
		c       *modtest.Client
		shownAs gen.StaffAbsenceShownAs
		next    bool
	}{
		{"the person", w.kariC, gen.StaffAbsenceShownAsType, true},
		{"personal-data", w.pd, gen.StaffAbsenceShownAsType, true},
		{"her manager", w.bossC, gen.StaffAbsenceShownAsLeave, false},
		{"HR", w.hr, gen.StaffAbsenceShownAsLeave, false},
		{"a colleague", w.viewer, gen.StaffAbsenceShownAsAbsent, false},
	} {
		g, _ := glanceOf(t, c.c, w.kari.Id)
		a := g.Absence
		if a == nil || a.Now == nil || a.Now.ShownAs != c.shownAs || (a.Now.TypeId != nil) != (c.shownAs == gen.StaffAbsenceShownAsType) ||
			(a.Now.TypeId != nil && *a.Now.TypeId != hrOnly) {
			t.Errorf("%s: now = %+v, want shown as %s", c.name, a, c.shownAs)
			continue
		}
		if (a.Next != nil) != c.next {
			t.Errorf("%s: next = %+v, want present %v", c.name, a.Next, c.next)
		}
	}
}
