package staff_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The competence review (staff competence design S11; the one-to-one plan,
// Task 3b): whoever may verify records that they and the person went through
// the Competence tab together — a date and the reviewer, nothing else.

func competenceReviewsPath(personID int64) string {
	return personPath(personID) + "/competence-reviews"
}

// recordReview records a review of personID as c with body (nil: no body at
// all) and fails the test unless it answered 201.
func recordReview(t *testing.T, c *modtest.Client, personID int64, body any) gen.StaffCompetenceReview {
	t.Helper()
	var out gen.StaffCompetenceReview
	do(t, c, http.MethodPost, competenceReviewsPath(personID), body, http.StatusCreated, &out)
	return out
}

// dayBefore is the calendar day before day, both as text.
func dayBefore(t *testing.T, day string) string {
	t.Helper()
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Fatal(err)
	}
	return d.AddDate(0, 0, -1).Format(time.DateOnly)
}

// reviewWorld is the competence world with every caller named, so the
// reviewer's name can be told apart.
type reviewWorld struct {
	competenceWorldA
	approverN, hrN *modtest.Client
}

func newReviewWorld(t *testing.T) reviewWorld {
	t.Helper()
	w := reviewWorld{competenceWorldA: newCompetenceWorldA(t)}
	w.h.Exec(t, `UPDATE identity.users SET display_name = 'Bjørn Boss' WHERE id = $1`, w.bossUser)
	w.approverN, _ = signInNamed(t, w.h, "Anna Approver", "staff:approve")
	w.hrN, _ = signInNamed(t, w.h, "Hanne HR", "staff:manage")
	return w
}

// Who records a review is who verifies (§4): a manager of the person's today,
// staff:approve and staff:manage, each answered 201 with the reviewer named;
// the person on their own is own_record. A colleague and a caller who does
// not see the person are the 404, before the verifier's rule. reviewedOn
// left out is today; yesterday is accepted, tomorrow refused on the field,
// and a property the request does not take refused on its name.
func TestCompetenceReviews_WhoRecords(t *testing.T) {
	t.Parallel()
	w := newReviewWorld(t)
	h := w.h
	todayText := today(t, h)
	yesterday := dayBefore(t, todayText)

	byBoss := recordReview(t, w.bossC, w.kari.Id, nil)
	if byBoss.ReviewedOn.Format(time.DateOnly) != todayText || byBoss.Reviewer != "Bjørn Boss" || !byBoss.RecordedAt.Equal(h.Now()) {
		t.Errorf("the manager's review = %+v, want today, by Bjørn Boss, recorded now", byBoss)
	}
	byApprover := recordReview(t, w.approverN, w.kari.Id, map[string]any{"reviewedOn": yesterday})
	if byApprover.ReviewedOn.Format(time.DateOnly) != yesterday || byApprover.Reviewer != "Anna Approver" {
		t.Errorf("the Approver's review = %+v, want yesterday, by Anna Approver", byApprover)
	}
	byHR := recordReview(t, w.hrN, w.kari.Id, map[string]any{})
	if byHR.ReviewedOn.Format(time.DateOnly) != todayText || byHR.Reviewer != "Hanne HR" {
		t.Errorf("HR's review = %+v, want today, by Hanne HR", byHR)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_reviews WHERE person_id = $1 AND reviewer_person_id = $2`, w.kari.Id, w.boss.Id); n != 1 {
		t.Errorf("%d reviews name Bjørn's person as the reviewer's, want the manager's one", n)
	}

	refused(t, w.kariC.Do(http.MethodPost, competenceReviewsPath(w.kari.Id), nil), http.StatusForbidden, "own_record")
	for _, c := range []struct {
		who    string
		caller *modtest.Client
		id     int64
	}{
		{"a colleague", w.viewer, w.kari.Id},
		{"a caller who does not see her", w.nobody, w.kari.Id},
		{"an unknown person", w.hr, 9999999},
	} {
		if r := c.caller.Do(http.MethodPost, competenceReviewsPath(c.id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: %d %s, want the bare 404", c.who, r.Status, r.Body)
		}
	}
	// staff:personal-data reads the reviews but verifies nothing.
	if r := w.pd.Do(http.MethodPost, competenceReviewsPath(w.kari.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:personal-data: %d %s, want the uniform 403", r.Status, r.Body)
	}

	tomorrow, _ := time.Parse(time.DateOnly, todayText)
	p := refused(t, w.bossC.Do(http.MethodPost, competenceReviewsPath(w.kari.Id),
		map[string]any{"reviewedOn": tomorrow.AddDate(0, 0, 1).Format(time.DateOnly)}), http.StatusBadRequest, "")
	if len(p.Errors["reviewedOn"]) == 0 || len(p.Errors) != 1 {
		t.Errorf("a review dated tomorrow: errors = %v, want one on reviewedOn", p.Errors)
	}
	// The request is closed: a note is refused, not dropped.
	p = refused(t, w.bossC.Do(http.MethodPost, competenceReviewsPath(w.kari.Id),
		map[string]any{"reviewedOn": yesterday, "note": "x"}), http.StatusBadRequest, "")
	if len(p.Errors["note"]) == 0 || len(p.Errors) != 1 {
		t.Errorf("a review with a note: errors = %v, want one on note", p.Errors)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_reviews WHERE person_id = $1`, w.kari.Id); n != 3 {
		t.Errorf("%d reviews recorded, want the three accepted", n)
	}
}

// The reviews are the Reviews group's (§5): the person, their manager, an
// Approver, HR and staff:personal-data read them, the newest first, each
// reviewer named; a colleague and a stranger get the 404.
func TestCompetenceReviews_List(t *testing.T) {
	t.Parallel()
	w := newReviewWorld(t)
	todayText := today(t, w.h)
	yesterday := dayBefore(t, todayText)
	older := recordReview(t, w.approverN, w.kari.Id, map[string]any{"reviewedOn": dayBefore(t, yesterday)})
	newest := recordReview(t, w.bossC, w.kari.Id, nil)
	middle := recordReview(t, w.hrN, w.kari.Id, map[string]any{"reviewedOn": yesterday})

	for _, c := range []struct {
		who    string
		caller *modtest.Client
	}{
		{"the person", w.kariC}, {"the manager", w.bossC}, {"an Approver", w.approver}, {"HR", w.hr}, {"staff:personal-data", w.pd},
	} {
		var got []gen.StaffCompetenceReview
		do(t, c.caller, http.MethodGet, competenceReviewsPath(w.kari.Id), nil, http.StatusOK, &got)
		if len(got) != 3 || got[0].Id != newest.Id || got[1].Id != middle.Id || got[2].Id != older.Id {
			t.Errorf("%s reads %+v, want the three, the newest first", c.who, got)
			continue
		}
		if got[0].Reviewer != "Bjørn Boss" || got[1].Reviewer != "Hanne HR" || got[2].Reviewer != "Anna Approver" {
			t.Errorf("%s reads the reviewers %q, %q, %q", c.who, got[0].Reviewer, got[1].Reviewer, got[2].Reviewer)
		}
	}
	for _, c := range []*modtest.Client{w.viewer, w.nobody} {
		if r := c.Do(http.MethodGet, competenceReviewsPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("GET: %d %s, want the bare 404", r.Status, r.Body)
		}
	}
}

// The person's response carries lastCompetenceReview under the Reviews
// group: absent before any review, the latest one's date and reviewer after
// — on GET /people/{id} to the manager, an Approver, HR and
// staff:personal-data, on GET /me to the person — and never to a colleague.
func TestCompetenceReviews_LastOnThePerson(t *testing.T) {
	t.Parallel()
	w := newReviewWorld(t)
	readers := []struct {
		who    string
		caller *modtest.Client
		path   string
	}{
		{"the person", w.kariC, mePath}, {"the manager", w.bossC, personPath(w.kari.Id)}, {"an Approver", w.approver, personPath(w.kari.Id)},
		{"HR", w.hr, personPath(w.kari.Id)}, {"staff:personal-data", w.pd, personPath(w.kari.Id)},
	}
	for _, c := range readers {
		if last, ok := raw(t, c.caller.Do(http.MethodGet, c.path, nil))["lastCompetenceReview"]; ok {
			t.Errorf("%s reads lastCompetenceReview %v before any review, want it absent", c.who, last)
		}
	}
	yesterday := dayBefore(t, today(t, w.h))
	recordReview(t, w.hrN, w.kari.Id, map[string]any{"reviewedOn": dayBefore(t, yesterday)})
	recordReview(t, w.bossC, w.kari.Id, map[string]any{"reviewedOn": yesterday})
	for _, c := range readers {
		last, _ := raw(t, c.caller.Do(http.MethodGet, c.path, nil))["lastCompetenceReview"].(map[string]any)
		if len(last) != 2 || last["reviewedOn"] != yesterday || last["reviewer"] != "Bjørn Boss" {
			t.Errorf("%s reads lastCompetenceReview %v, want yesterday's by Bjørn Boss", c.who, last)
		}
	}
	if last, ok := raw(t, w.viewer.Do(http.MethodGet, personPath(w.kari.Id), nil))["lastCompetenceReview"]; ok {
		t.Errorf("a colleague reads lastCompetenceReview %v, want it absent", last)
	}
}

// Each review is a competence_reviewed event on the person's history
// carrying the review's date as reviewedOn's after (plan reading 21), read
// by the person and their manager as every event is.
func TestCompetenceReviews_TheEvent(t *testing.T) {
	t.Parallel()
	w := newReviewWorld(t)
	yesterday := dayBefore(t, today(t, w.h))
	recordReview(t, w.bossC, w.kari.Id, map[string]any{"reviewedOn": yesterday})
	for _, c := range []struct {
		who    string
		caller *modtest.Client
	}{{"the manager", w.bossC}, {"the person", w.kariC}, {"HR", w.hr}} {
		var found *gen.StaffEvent
		for _, e := range listEvents(t, c.caller, w.kari.Id) {
			if e.Kind == "competence_reviewed" {
				found = &e
				break
			}
		}
		if found == nil {
			t.Errorf("%s reads no competence_reviewed event", c.who)
			continue
		}
		f := fieldsOf(*found)
		reviewed, _ := f["reviewedOn"].(map[string]any)
		if len(f) != 1 || len(reviewed) != 1 || reviewed["to"] != yesterday || found.Actor.DisplayName != "Bjørn Boss" {
			t.Errorf("%s reads the event %+v (fields %v), want reviewedOn to %s by Bjørn Boss", c.who, found, f, yesterday)
		}
	}
}

// An erased person is read-only (design S15 as amended): a review of theirs
// is 409 person_erased and records nothing. The D erase deletes a person's
// reviews whole, every column of competence_reviews being class D.
func TestCompetenceReviews_TheErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data", "staff:view")
	r := seedErased(t, h, hr)
	refused(t, hr.Do(http.MethodPost, competenceReviewsPath(r.person.Id), nil), http.StatusConflict, "person_erased")
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_reviews WHERE person_id = $1`, r.person.Id); n != 0 {
		t.Errorf("%d reviews of the erased person, want none recorded", n)
	}

	// Today is 2026-09-12 and the grace 90 days: an employment ended at
	// the end of 2024 is due at the next cycle.
	leaver := createPerson(t, hr, newPerson("Lise", "Sluttet"))
	pastEmployment(t, hr, leaver.Id, "2020-01-01", "2024-12-31", terms())
	recordReview(t, hr, leaver.Id, map[string]any{"reviewedOn": "2024-06-01"})
	recordReview(t, hr, leaver.Id, nil)
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, leaver.Id); n != 1 {
		t.Fatal("the leaver was not erased")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.competence_reviews WHERE person_id = $1`, leaver.Id); n != 0 {
		t.Errorf("%d reviews survive the erase, want none", n)
	}
}
