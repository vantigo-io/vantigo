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

// The whole-branch review's smaller privacy findings: what a case's and a
// milestone's revision tell, which cases the Approver reads, what a mail to
// a manager names, which events a leave-reader is listed, and where a health
// document may be filed.

// A case's and a milestone's revision go to the health group's readers alone
// — the person and staff:personal-data — since a milestone's revision moves
// with its note and a case's with its accommodation note. A reader who
// writes the case without the health group (the manager, HR) is sent an
// opaque etag instead and sends it back; the Approver, who writes neither,
// is sent neither.
func TestCases_RevisionsToTheHealthGroupAlone(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	sick := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-09-07", "2026-09-11"))
	c := caseOfRow(t, h, sick.Id)
	read := func(cl *modtest.Client) map[string]any { return raw(t, cl.Do(http.MethodGet, casePath(c), nil)) }
	milestoneRevisions := func(body map[string]any) bool {
		ms, _ := body["milestones"].([]any)
		for _, m := range ms {
			if _, ok := m.(map[string]any)["revision"]; ok {
				return true
			}
		}
		return false
	}
	for name, cl := range map[string]*modtest.Client{"personal-data": w.pd, "the person": w.kariC} {
		body := read(cl)
		if _, ok := body["revision"].(float64); !ok || !milestoneRevisions(body) {
			t.Errorf("%s does not read the revisions: %v", name, body)
		}
	}
	for name, cl := range map[string]*modtest.Client{"her manager": w.bossC, "HR without personal-data": w.hr, "the Approver": w.approver} {
		body := read(cl)
		if _, ok := body["revision"]; ok || milestoneRevisions(body) {
			t.Errorf("%s reads a revision: %v", name, body)
		}
		_, tagged := body["etag"].(string)
		if want := cl != w.approver; tagged != want {
			t.Errorf("%s: etag sent %v, want %v", name, tagged, want)
		}
	}
	tag := read(w.bossC)["etag"].(string)
	do(t, w.bossC, http.MethodPut, casePath(c), map[string]any{"accommodationNote": "Kortere dager"}, http.StatusOK, nil,
		modtest.Header("If-Match", strconv.Quote(tag)))
	refused(t, w.bossC.Do(http.MethodPut, casePath(c), map[string]any{"accommodationNote": "x"}, modtest.Header("If-Match", strconv.Quote(tag))),
		http.StatusConflict, "revision_conflict")
	refused(t, w.bossC.Do(http.MethodPut, casePath(c), map[string]any{"accommodationNote": "x"}, ifMatch(2)),
		http.StatusConflict, "revision_conflict")
}

// The Approver reads open cases only (§5): a closed case is no
// staff:approve holder's, in the list or by id; the manager reads it.
func TestCases_TheApproverReadsOpenCasesOnly(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	old := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-06-01", "2026-06-05"))
	c := caseOfRow(t, h, old.Id)
	listed := func(cl *modtest.Client) bool {
		var page struct {
			Data []map[string]any `json:"data"`
		}
		do(t, cl, http.MethodGet, casesPath+"?status=all", nil, http.StatusOK, &page)
		return slices.ContainsFunc(page.Data, func(x map[string]any) bool { return x["id"] == float64(c) })
	}
	if listed(w.approver) {
		t.Error("the Approver lists a closed case")
	}
	if r := w.approver.Do(http.MethodGet, casePath(c), nil); r.Status != http.StatusNotFound {
		t.Errorf("the Approver reads a closed case: %d, want 404", r.Status)
	}
	if !listed(w.bossC) {
		t.Error("the manager does not list the closed case")
	}
}

// A registration's mail to a manager names no type: every registered
// notice reads "an absence", so a doctor's visit is not told from a
// sickness.
func TestNotifications_ARegistrationNamesNoType(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test', preferred_language = 'en' WHERE id = $1`, w.boss.Id)
	register(t, w.hr, w.kari.Id, absence(t, h, "helse", "2026-09-15", "2026-09-15", "hours", 2))
	mail := lastMail(t, h, "bjorn@example.test")
	name := modtest.One[string](t, h.Harness, `SELECT name_en FROM staff.absence_types WHERE key = 'helse'`)
	if strings.Contains(mail, name) || !strings.Contains(mail, "an absence") {
		t.Errorf("the manager's mail names the type: %q", mail)
	}
}

// A leave-reader is listed none of a row's events once the row is of an
// HR-only type, even those recorded while it was a plain one. No write
// moves a row across that line any more — a change and a correction are
// both absence_visibility_change (the privacy review of delivery C) — so
// the row is moved by hand here: the listing rule still holds should a row
// ever be.
func TestEvents_ARowNowHROnlyHidesItsHistory(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	hrPD, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-06"))
	approved(t, w.bossC, ferie.Id)
	refused(t, hrPD.Do(http.MethodPost, correctPath(ferie.Id), absence(t, h, "svangerskapspermisjon", "2026-11-02", "2026-11-06"),
		ifMatchAs(t, hrPD, ferie.Id)), http.StatusConflict, "absence_visibility_change")
	do(t, hrPD, http.MethodPost, correctPath(ferie.Id), absence(t, h, "ferie", "2026-11-02", "2026-11-05"),
		http.StatusOK, nil, ifMatchAs(t, hrPD, ferie.Id))
	h.Exec(t, `UPDATE staff.absences SET type_id = $2 WHERE id = $1`, ferie.Id, typeID(t, h, "svangerskapspermisjon"))
	for name, cl := range map[string]*modtest.Client{"her manager": w.bossC, "HR without personal-data": w.hr} {
		if e := eventsAbout(t, cl, w.kari.Id, ferie.Id); len(e) != 0 {
			t.Errorf("%s is listed the now HR-only row's events: %v", name, e)
		}
	}
	if e := eventsAbout(t, w.pd, w.kari.Id, ferie.Id); len(e) < 3 {
		t.Errorf("personal-data reads %d of the row's events, want its whole history", len(e))
	}
}

// A health document is never filed under a case the retention run has
// erased, or whose health content is due for it.
func TestDocuments_NotFiledUnderAnErasedCase(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	old := register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2025-03-03", "2025-03-07"))
	c := caseOfRow(t, h, old.Id)
	runCycle(t, h)
	doc := seedDocument(t, h, w.kari.Id, "health")
	r := w.pd.Do(http.MethodPut, documentPath(doc), map[string]any{"categoryId": documentCategoryID(t, h, "health"), "caseId": c}, ifMatch(1))
	refused(t, r, http.StatusBadRequest, "")
	if !strings.Contains(string(r.Body), fmt.Sprint(c)) {
		t.Errorf("the refusal does not name the case: %s", r.Body)
	}
}

// A colleague's spans are coalesced over the person's whole absence and
// clipped to the window asked for (privacy re-check): sliding from or to
// across where one row ends and another begins shows only the clipped
// union, never a row's own first or last day.
func TestAbsences_ColleagueSpansAreClippedToTheWindow(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-01", "2026-10-10"))
	approveRow(t, h, ferie.Id)
	register(t, w.hr, w.kari.Id, absence(t, h, "sykmelding", "2026-10-08", "2026-10-15"))
	spans := func(query string) [][2]any {
		var out [][2]any
		for _, r := range rowsAs(t, w.viewer, fmt.Sprintf("?personId=%d%s", w.kari.Id, query)) {
			out = append(out, [2]any{r["firstDay"], r["lastDay"]})
		}
		return out
	}
	for query, want := range map[string][2]any{
		"":                 {"2026-10-01", "2026-10-15"},
		"&from=2026-10-09": {"2026-10-09", "2026-10-15"},
		"&from=2026-10-11": {"2026-10-11", "2026-10-15"},
		"&to=2026-10-09":   {"2026-10-01", "2026-10-09"},
		"&to=2026-10-12":   {"2026-10-01", "2026-10-12"},
	} {
		if got := spans(query); len(got) != 1 || got[0] != want {
			t.Errorf("a colleague's spans%s = %v, want the one clipped union %v", query, got, want)
		}
	}
	cal, _ := calendarAs(t, w.viewer, "?from=2026-10-11&to=2026-10-31")
	if bars := cal[w.kari.Id]; len(bars) != 1 || bars[0]["firstDay"] != "2026-10-11" || bars[0]["lastDay"] != "2026-10-15" {
		t.Errorf("a colleague's calendar from 11 October = %v, want 11–15 October", bars)
	}
}

// An HR-only hour row never blocks a day row in the real rule, so a writer
// who reads HR-only types as leave judges none at all (privacy re-check):
// a manager approves ferie over a day with a check-up, and HR without
// staff:personal-data registers a sykmelding over it, as they would over a
// day without one.
func TestAbsences_AnHROnlyHourRowBlocksNoDayRow(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-10-07", "2026-10-07", "hours", 1))
	ferie := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	if r := decide(w.bossC, approvePath(ferie.Id), ""); r.Status != http.StatusOK {
		t.Errorf("the manager approving ferie over a check-up day: %d %s, want 200", r.Status, r.Body)
	}
	requestOwn(t, w.kariC, absence(t, h, "svangerskapskontroll", "2026-11-04", "2026-11-04", "hours", 1))
	if r := w.hr.Do(http.MethodPost, absencesPath, with(absence(t, h, "sykmelding", "2026-11-02", "2026-11-06"), "personId", w.kari.Id)); r.Status != http.StatusCreated {
		t.Errorf("HR registering a sykmelding over a check-up day: %d %s, want 201", r.Status, r.Body)
	}
}
