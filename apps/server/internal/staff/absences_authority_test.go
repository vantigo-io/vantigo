package staff_test

import (
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// sameAnswer fails the test unless a and b are byte for byte the same
// answer: one status, one body.
func sameAnswer(t *testing.T, what string, a, b *modtest.Response) {
	t.Helper()
	if a.Status != b.Status || !bytes.Equal(scrubTrace(a.Body), scrubTrace(b.Body)) {
		t.Errorf("%s: %d %s against %d %s, want the same answer", what, a.Status, a.Body, b.Status, b.Body)
	}
}

// scrubTrace is a body without what no request chose (a trace id).
func scrubTrace(body []byte) []byte { return []byte(scrubRandom(string(body))) }

// writesOn is every write a caller might try on absence id, each answer.
func writesOn(t *testing.T, w decisionWorld, c *modtest.Client, id int64) map[string]*modtest.Response {
	t.Helper()
	h := w.h
	body := absence(t, h, "ferie", "2026-10-05", "2026-10-06")
	return map[string]*modtest.Response{
		"cancel":  c.Do(http.MethodPost, cancelAbsencePath(id), nil),
		"approve": c.Do(http.MethodPost, approvePath(id), map[string]any{}),
		"reject":  c.Do(http.MethodPost, rejectPath(id), map[string]any{"note": "Nei"}),
		"put":     c.Do(http.MethodPut, absencePath(id), body, ifMatch(1)),
		"delete":  c.Do(http.MethodDelete, absencePath(id), nil),
		"correct": c.Do(http.MethodPost, correctPath(id), body, ifMatch(1)),
	}
}

// Authority first, then the status (review C1): a caller who is neither the
// person, a decider of theirs nor HR gets one answer — the 404 of a row they
// cannot write — whatever the row's status, so a colleague cannot tell an
// approved ferie from a registered sykmelding by trying to cancel them, and
// staff:personal-data alone cannot either.
func TestAbsences_AuthorityBeforeStatus(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	approved(t, w.bossC, ferie.Id)
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-11-02", "2026-11-06"))

	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "personal-data alone": w.pd} {
		a, b := writesOn(t, w, c, ferie.Id), writesOn(t, w, c, sick.Id)
		for op := range a {
			sameAnswer(t, name+"'s "+op+" on the ferie and on the sykmelding", a[op], b[op])
			if op != "correct" && a[op].Status != http.StatusNotFound {
				t.Errorf("%s's %s: %d, want the 404 of a row they cannot write", name, op, a[op].Status)
			}
		}
	}
	if statusIn(t, h, ferie.Id) != "approved" || statusIn(t, h, sick.Id) != "registered" {
		t.Error("a refused write changed a row")
	}
}

// HR-only types are decided by staff:personal-data alone (review H4, interim
// option b): an HR-only request never reaches a manager's, an Approver's or
// HR-without-personal-data's inbox, list or calendar, and never their mail;
// they neither decide nor change such a row. To them every HR-only row —
// requested, then approved by a staff:personal-data holder, or registered —
// is one approved "leave" they never saw asked for: a svangerskapspermisjon
// and a svangerskapskontroll read alike, with no note, no events, and the
// same refusal to every write.
func TestAbsences_HROnlyRowsArePersonalDatasToDecide(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test' WHERE id = $1`, w.boss.Id)
	pd2, _ := signIn(t, h, "staff:personal-data")

	perm := requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-10-12", "2026-10-16", "note", "Termin i oktober"))
	readers := map[string]*modtest.Client{"her manager": w.bossC, "HR without personal-data": w.hr, "the Approver": w.approver}
	inboxIDs := func(c *modtest.Client) []float64 {
		var page struct {
			Data []struct {
				Absence map[string]any `json:"absence"`
			} `json:"data"`
		}
		do(t, c, http.MethodGet, approvalsPath, nil, http.StatusOK, &page)
		var out []float64
		for _, it := range page.Data {
			out = append(out, it.Absence["id"].(float64))
		}
		return out
	}
	for name, c := range readers {
		if slices.Contains(inboxIDs(c), float64(perm.Id)) {
			t.Errorf("%s's inbox holds the HR-only request", name)
		}
		if r := c.Do(http.MethodGet, absencePath(perm.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s reads the HR-only request: %d, want 404", name, r.Status)
		}
		for _, r := range rowsAs(t, c, fmt.Sprintf("?personId=%d", w.kari.Id)) {
			if r["id"] == float64(perm.Id) {
				t.Errorf("%s lists the HR-only request", name)
			}
		}
		cal, _ := calendarAs(t, c, "?from=2026-10-01&to=2026-10-31")
		for _, b := range cal[w.kari.Id] {
			if b["id"] == float64(perm.Id) {
				t.Errorf("%s's calendar shows the HR-only request", name)
			}
		}
		if r := decide(c, approvePath(perm.Id), ""); r.Status != http.StatusNotFound {
			t.Errorf("%s approving the HR-only request: %d, want 404", name, r.Status)
		}
	}
	if !slices.Contains(inboxIDs(w.pd), float64(perm.Id)) {
		t.Error("staff:personal-data's inbox lacks the HR-only request")
	}
	if len(mailsTo(h, "bjorn@example.test")) != 0 {
		t.Error("the manager was mailed about an HR-only request")
	}
	refused(t, decide(w.kariC, approvePath(perm.Id), ""), http.StatusForbidden, "own_request")
	approved(t, pd2, perm.Id)

	check := requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-10-07", "2026-10-07", "hours", 2))
	if len(mailsTo(h, "bjorn@example.test")) != 0 {
		t.Error("the manager was mailed about an HR-only registration or decision")
	}
	for name, c := range readers {
		// The HR-only hour row is not read by a leave-reader at all
		// (whole-branch review); the leave is, without its note.
		if r := c.Do(http.MethodGet, absencePath(check.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s reads the check: %d, want 404", name, r.Status)
		}
		b := raw(t, c.Do(http.MethodGet, absencePath(perm.Id), nil))
		if _, ok := b["note"]; ok {
			t.Errorf("%s reads the leave's note", name)
		}
		if b["status"] != "approved" || b["shownAs"] != "leave" {
			t.Errorf("%s reads the leave as %v, want approved leave", name, b)
		}
		if c != w.approver {
			if e := eventsAbout(t, c, w.kari.Id, perm.Id); len(e) != 0 {
				t.Errorf("%s is listed the leave's events: %v", name, e)
			}
			if e := eventsAbout(t, c, w.kari.Id, check.Id); len(e) != 0 {
				t.Errorf("%s is listed the check's events: %v", name, e)
			}
		}
		x, y := writesOn(t, w, c, check.Id), writesOn(t, w, c, perm.Id)
		for op := range x {
			sameAnswer(t, name+"'s "+op+" on the check and on the leave", x[op], y[op])
		}
	}
	if statusIn(t, h, perm.Id) != "approved" || statusIn(t, h, check.Id) != "registered" {
		t.Error("a refused write changed an HR-only row")
	}
	// HR with staff:personal-data writes HR-only rows as any other.
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	do(t, hrPD, http.MethodPost, cancelAbsencePath(perm.Id), nil, http.StatusOK, nil)
}

// An HR-only type is a target only for the person and staff:personal-data
// (whole-branch review): HR without staff:personal-data, who reads such a
// type as leave, cannot register, change or correct a row into one — the
// answer is the one an unknown type gets, so the attempt tells nothing.
func TestAbsences_HROnlyTargetTypesAreThePersonsAndPersonalDatas(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	unknown := func(what string, r *modtest.Response, key string) {
		t.Helper()
		p := refused(t, r, http.StatusBadRequest, "")
		if want := fmt.Sprintf("No absence type has id %d", typeID(t, h, key)); !bytes.Contains(r.Body, []byte(want)) {
			t.Errorf("%s: %+v, want the unknown type's answer %q", what, p, want)
		}
	}
	unknown("HR registering svangerskapspermisjon", w.hr.Do(http.MethodPost, absencesPath,
		with(absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"), "personId", w.kari.Id)), "svangerskapspermisjon")
	visit := register(t, w.hr, w.kari.Id, absence(t, h, "helse", "2026-09-01", "2026-09-01", "hours", 2))
	unknown("HR changing helse into svangerskapskontroll", w.hr.Do(http.MethodPut, absencePath(visit.Id),
		absence(t, h, "svangerskapskontroll", "2026-09-01", "2026-09-01", "hours", 2), ifMatchAs(t, w.hr, visit.Id)), "svangerskapskontroll")
	unknown("HR correcting helse into svangerskapskontroll", w.hr.Do(http.MethodPost, correctPath(visit.Id),
		absence(t, h, "svangerskapskontroll", "2026-09-01", "2026-09-01", "hours", 2), ifMatchAs(t, w.hr, visit.Id)), "svangerskapskontroll")

	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	register(t, hrPD, w.kari.Id, absence(t, h, "religios_hoytid", "2026-12-01", "2026-12-01"))
	// HR with staff:personal-data corrects it into one; a change never
	// crosses that line (privacy review MEDIUM-1).
	refused(t, hrPD.Do(http.MethodPut, absencePath(visit.Id), absence(t, h, "svangerskapskontroll", "2026-09-01", "2026-09-01", "hours", 2),
		ifMatchAs(t, hrPD, visit.Id)), http.StatusConflict, "absence_visibility_change")
	do(t, hrPD, http.MethodPost, correctPath(visit.Id), absence(t, h, "svangerskapskontroll", "2026-09-01", "2026-09-01", "hours", 2),
		http.StatusOK, nil, ifMatchAs(t, hrPD, visit.Id))
}

// Overlap tells a writer who reads HR-only types as leave nothing they may
// not read (whole-branch review): to them an approved or registered HR-only
// row is one whole day of leave — its hours and share unread — and an
// HR-only request is not there at all, so neither a 24-hour probe beside a
// svangerskapskontroll nor a day row over a hidden request is refused.
func TestAbsences_OverlapProbesNoHROnlyRow(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-09-01", "2026-09-01", "hours", 2))
	if r := w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "helse", "2026-09-01", "2026-09-01", "hours", 24), "personId", w.kari.Id)); r.Status != http.StatusCreated {
		t.Errorf("HR's 24 hours beside a svangerskapskontroll: %d %s, want 201 — the refusal would tell its hours", r.Status, r.Body)
	}
	requestOwn(t, w.kariC, absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"))
	if r := w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "velferdspermisjon", "2026-11-03", "2026-11-03"), "personId", w.kari.Id)); r.Status != http.StatusCreated {
		t.Errorf("HR's day row over a hidden HR-only request: %d %s, want 201", r.Status, r.Body)
	}
	if r := w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "velferdspermisjon", "2026-11-03", "2026-11-03"), "personId", w.kari.Id)); r.Status != http.StatusConflict {
		t.Errorf("a second day row over HR's own: %d, want 409 — rows the writer reads still overlap", r.Status)
	}
}
