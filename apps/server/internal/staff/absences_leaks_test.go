package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// ifMatchAs is the If-Match header c sends back for absence id, as c reads
// it: the revision to the person and to HR with staff:personal-data, the
// opaque etag to HR without it (review of Task 3's fix, leak 1).
func ifMatchAs(t *testing.T, c *modtest.Client, id int64) modtest.RequestOption {
	t.Helper()
	r := raw(t, c.Do(http.MethodGet, absencePath(id), nil))
	if tag, ok := r["etag"].(string); ok {
		return modtest.Header("If-Match", strconv.Quote(tag))
	}
	if rev, ok := r["revision"].(float64); ok {
		return modtest.Header("If-Match", strconv.Itoa(int(rev)))
	}
	t.Fatalf("absence %d carries neither a revision nor an etag: %v", id, r)
	return nil
}

// An absence's revision is sent only to whoever writes the row (leak 1): a
// registration stays at revision 1 and a decided request moves past it, so
// the number would tell which rows are notifications. The person and HR
// with staff:personal-data read the revision; HR without it — who reads an
// HR-only row as leave — an opaque etag on every row, accepted in If-Match,
// a bare revision refused without naming one; a manager, staff:personal-data
// alone and a colleague neither.
func TestAbsences_RevisionOnlyToItsWriters(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-02", "2026-11-06"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved', revision = revision + 1 WHERE id = $1`, ferie.Id)

	for _, id := range []int64{ferie.Id, sick.Id} {
		for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "personal-data alone": w.pd, "a colleague": w.viewer} {
			body := raw(t, c.Do(http.MethodGet, absencePath(id), nil))
			if _, ok := body["revision"]; ok {
				t.Errorf("%s reads row %d's revision: %v", name, id, body)
			}
			if _, ok := body["etag"]; ok {
				t.Errorf("%s reads row %d's etag: %v", name, id, body)
			}
		}
		for name, c := range map[string]*modtest.Client{"the person": w.kariC, "HR with personal-data": hrPD} {
			body := raw(t, c.Do(http.MethodGet, absencePath(id), nil))
			if _, ok := body["revision"].(float64); !ok {
				t.Errorf("%s does not read row %d's revision: %v", name, id, body)
			}
			if _, ok := body["etag"]; ok {
				t.Errorf("%s reads row %d's etag: %v", name, id, body)
			}
		}
		body := raw(t, w.hr.Do(http.MethodGet, absencePath(id), nil))
		tag, ok := body["etag"].(string)
		if _, has := body["revision"]; has || !ok || tag == "" {
			t.Errorf("HR without personal-data reads row %d as %v, want an etag and no revision", id, body)
		}
		if _, err := strconv.Atoi(tag); err == nil {
			t.Errorf("HR's etag %q is a number", tag)
		}
	}
	for _, r := range rowsAs(t, w.bossC, fmt.Sprintf("?personId=%d", w.kari.Id)) {
		if _, ok := r["revision"]; ok {
			t.Errorf("her manager lists a revision: %v", r)
		}
	}
	for _, r := range rowsAs(t, w.hr, fmt.Sprintf("?personId=%d", w.kari.Id)) {
		if _, ok := r["revision"]; ok || r["etag"] == nil {
			t.Errorf("HR lists %v, want an etag and no revision", r)
		}
	}

	// HR without personal-data writes with the etag; a bare revision is a
	// conflict that names no revision.
	p := refused(t, w.hr.Do(http.MethodPut, absencePath(sick.Id), absence(t, h, "sykmelding", "2026-11-02", "2026-11-05"), ifMatch(1)),
		http.StatusConflict, "revision_conflict")
	if strings.Contains(p.Detail, "revision 1") {
		t.Errorf("the conflict names the revision: %q", p.Detail)
	}
	before := raw(t, w.hr.Do(http.MethodGet, absencePath(sick.Id), nil))["etag"]
	r := w.hr.Do(http.MethodPut, absencePath(sick.Id), absence(t, h, "sykmelding", "2026-11-02", "2026-11-05"), ifMatchAs(t, w.hr, sick.Id))
	if r.Status != http.StatusOK {
		t.Fatalf("HR's change with its etag: %d %s", r.Status, r.Body)
	}
	if after := raw(t, r)["etag"]; after == nil || after == before {
		t.Errorf("the etag after the change = %v, was %v: want a new one", after, before)
	}
	stale, _ := before.(string)
	refused(t, w.hr.Do(http.MethodPut, absencePath(sick.Id), absence(t, h, "sykmelding", "2026-11-02", "2026-11-04"),
		modtest.Header("If-Match", strconv.Quote(stale))), http.StatusConflict, "revision_conflict")
}

// A leave-reader cannot tell the HR-only notification from an HR-only
// request (leak 2): to her manager and to HR without staff:personal-data, a
// registered svangerskapskontroll and a requested-then-approved
// svangerskapspermisjon carry the same keys, the same status and the same
// revision shape; neither row's events are listed to them at all (they read
// the rows' dates already), and a status filter treats the registration as
// the approval it is shown as. The person and staff:personal-data read both
// as they are.
func TestAbsences_LeaveReadersCannotTellTheHROnlyNotification(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	check := requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-10-07", "2026-10-07", "hours", 2))
	perm := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-10-12", "2026-10-16"))
	if r := decide(w.pd, approvePath(perm.Id), "Gratulerer"); r.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}

	for name, c := range map[string]*modtest.Client{"her manager": w.bossC, "HR without personal-data": w.hr} {
		a := raw(t, c.Do(http.MethodGet, absencePath(check.Id), nil))
		b := raw(t, c.Do(http.MethodGet, absencePath(perm.Id), nil))
		if !slices.Equal(keysOf(a), keysOf(b)) {
			t.Errorf("%s: the check's keys %v, the leave's %v, want the same", name, keysOf(a), keysOf(b))
		}
		for _, k := range []string{"shownAs", "status", "percent", "decisionNote"} {
			if a[k] != b[k] {
				t.Errorf("%s: %s = %v for the check, %v for the leave, want the same", name, k, a[k], b[k])
			}
		}
		if e := eventsAbout(t, c, w.kari.Id, check.Id); len(e) != 0 {
			t.Errorf("%s is listed the check's events: %v", name, e)
		}
		if e := eventsAbout(t, c, w.kari.Id, perm.Id); len(e) != 0 {
			t.Errorf("%s is listed the leave's events: %v", name, e)
		}
		ids := func(status string) []int64 {
			var out []int64
			for _, r := range rowsAs(t, c, fmt.Sprintf("?personId=%d&status=%s", w.kari.Id, status)) {
				out = append(out, int64(r["id"].(float64)))
			}
			return out
		}
		if got := ids("registered"); slices.Contains(got, check.Id) || slices.Contains(got, perm.Id) {
			t.Errorf("%s filtering on registered lists %v", name, got)
		}
		if got := ids("approved"); !slices.Contains(got, check.Id) || !slices.Contains(got, perm.Id) {
			t.Errorf("%s filtering on approved lists %v, want both", name, got)
		}
	}
	for name, c := range map[string]*modtest.Client{"personal-data": w.pd, "the person": w.kariC} {
		if e := eventsAbout(t, c, w.kari.Id, check.Id); len(e) != 1 || e[0].Kind != "absence_registered" {
			t.Errorf("%s reads the check's events as %v, want absence_registered", name, e)
		}
		if a := raw(t, c.Do(http.MethodGet, absencePath(check.Id), nil)); a["hours"] != float64(2) || a["status"] != "registered" {
			t.Errorf("%s reads the check as %v, want its hours and registered", name, a)
		}
	}
}
