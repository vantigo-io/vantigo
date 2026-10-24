package staff_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The person's competences' paths.
const personCompetencesBase = "/api/v1/staff/person-competences"

func personCompetencesPath(personID int64) string { return personPath(personID) + "/competences" }
func personCompetencePath(id int64) string        { return rowPath(personCompetencesBase, id) }
func verifyPath(id int64) string                  { return personCompetencePath(id) + "/verify" }
func renewPath(id int64) string                   { return personCompetencePath(id) + "/renew" }
func evidencePath(id int64) string                { return personCompetencePath(id) + "/evidence" }

// declare posts a competence row for personID as c and fails the test unless
// it was created.
func declare(t *testing.T, c *modtest.Client, personID int64, body map[string]any) gen.StaffPersonCompetence {
	t.Helper()
	var out gen.StaffPersonCompetence
	do(t, c, http.MethodPost, personCompetencesPath(personID), body, http.StatusCreated, &out)
	return out
}

// verify verifies a row as c and fails the test unless it answered 200.
func verify(t *testing.T, c *modtest.Client, id int64, body map[string]any) gen.StaffPersonCompetence {
	t.Helper()
	var out gen.StaffPersonCompetence
	do(t, c, http.MethodPost, verifyPath(id), body, http.StatusOK, &out)
	return out
}

// renew renews a row as c and fails the test unless it was created.
func renew(t *testing.T, c *modtest.Client, id int64, body map[string]any) gen.StaffPersonCompetence {
	t.Helper()
	var out gen.StaffPersonCompetence
	do(t, c, http.MethodPost, renewPath(id), body, http.StatusCreated, &out)
	return out
}

// listPersonCompetences reads a person's rows as c — with the superseded
// ones when history — and fails the test unless it answered 200.
func listPersonCompetences(t *testing.T, c *modtest.Client, personID int64, history bool) []gen.StaffPersonCompetence {
	t.Helper()
	path := personCompetencesPath(personID)
	if history {
		path += "?history=true"
	}
	var out []gen.StaffPersonCompetence
	do(t, c, http.MethodGet, path, nil, http.StatusOK, &out)
	return out
}

// postEvidence sends one evidence upload for row id as c.
func postEvidence(t *testing.T, c *modtest.Client, id int64, u upload) *modtest.Response {
	t.Helper()
	ct, body := u.form(t)
	return c.Do(http.MethodPost, evidencePath(id), nil, modtest.RawBody(ct, body))
}

// addEvidence files evidence for row id as c and fails the test unless it
// answered 200.
func addEvidence(t *testing.T, c *modtest.Client, id int64) gen.StaffPersonCompetence {
	t.Helper()
	r := postEvidence(t, c, id, pdfUpload(0))
	if r.Status != http.StatusOK {
		t.Fatalf("evidence for row %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var out gen.StaffPersonCompetence
	r.JSON(&out)
	return out
}

// competenceEventFields is the fields of the newest event of kind about row
// id in the person's history as c reads it, nil when there is none.
func competenceEventFields(t *testing.T, c *modtest.Client, personID int64, kind string, id int64) map[string]any {
	t.Helper()
	for _, e := range listEvents(t, c, personID) {
		if e.Kind == kind && e.Payload["competenceId"] == float64(id) {
			return fieldsOf(e)
		}
	}
	return nil
}

// competenceFrom is a row's validFrom as text.
func competenceFrom(d gen.StaffPersonCompetence) string { return d.ValidFrom.Format(time.DateOnly) }

// competenceWorld is the absence world's audiences on Kari, an Approver, the
// company skill golang with a level and the seeded fse.
type competenceWorldA struct {
	absenceWorld
	approver *modtest.Client
	golang   int64
	fse      int64
}

func newCompetenceWorldA(t *testing.T) competenceWorldA {
	t.Helper()
	w := competenceWorldA{absenceWorld: newAbsenceWorld(t)}
	w.approver, _ = signIn(t, w.h, "staff:approve")
	w.golang = addCompetence(t, w.hr, skillBody(t, w.h, "golang")).Id
	w.fse = competenceID(t, w.h, "fse")
	return w
}

// The person declares (source self, declared); a manager of theirs today or
// an Approver adds (source manager), HR too (source hr), the two verified at
// once on the basis observed with the caller as the verifier. A colleague is
// the uniform 403, a caller who does not see the person the 404; each write
// is competence_declared.
func TestPersonCompetences_DeclareAndAdd(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h

	own := declare(t, w.kariC, w.kari.Id, map[string]any{
		"competenceId": w.fse, "issuedOn": "2026-03-01", "validTo": "2027-03-01", "issuer": "Kurs AS", "reference": "FSE-123",
	})
	if own.Verified || own.Source == nil || *own.Source != "self" || competenceFrom(own) != "2026-03-01" || own.VerifiedAt != nil ||
		own.Competence.Key != "fse" || own.Expiring || own.Expired || own.Superseded || own.Level != nil {
		t.Errorf("declared = %+v, want Kari's own, declared, valid from the issue date", own)
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_declared", own.Id); f == nil ||
		f["competenceKey"].(map[string]any)["to"] != "fse" || f["source"].(map[string]any)["to"] != "self" {
		t.Errorf("the declared event = %v, want the key and the source", f)
	}

	byBoss := declare(t, w.bossC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 3})
	if !byBoss.Verified || *byBoss.Source != "manager" || byBoss.VerifiedBy == nil || *byBoss.VerifiedBasis != "observed" ||
		*byBoss.Level != 3 || competenceFrom(byBoss) != today(t, h) {
		t.Errorf("the manager's = %+v, want verified at once, observed, from today", byBoss)
	}
	byApprover := declare(t, w.approver, w.kari.Id, map[string]any{"competenceId": competenceID(t, h, "truck")})
	if !byApprover.Verified || *byApprover.Source != "manager" {
		t.Errorf("the Approver's = %+v, want verified, source manager", byApprover)
	}
	byHR := declare(t, w.hr, w.kari.Id, map[string]any{"competenceId": competenceID(t, h, "kran")})
	if !byHR.Verified || *byHR.Source != "hr" {
		t.Errorf("HR's = %+v, want verified, source hr", byHR)
	}

	body := map[string]any{"competenceId": competenceID(t, h, "hms-kort")}
	if r := w.viewer.Do(http.MethodPost, personCompetencesPath(w.kari.Id), body); r.Status != http.StatusForbidden {
		t.Errorf("a colleague's add = %d, want 403", r.Status)
	}
	if r := w.nobody.Do(http.MethodPost, personCompetencesPath(w.kari.Id), body); r.Status != http.StatusNotFound {
		t.Errorf("a stranger's add = %d, want 404", r.Status)
	}
	if r := w.hr.Do(http.MethodPost, personCompetencesPath(9999999), body); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person = %d, want 404", r.Status)
	}

	refused(t, w.kariC.Do(http.MethodPost, personCompetencesPath(w.kari.Id), map[string]any{"competenceId": w.fse}),
		http.StatusConflict, "competence_held")

	rust := addCompetence(t, w.hr, skillBody(t, h, "rust")).Id
	cobol := addCompetence(t, w.hr, skillBody(t, h, "cobol")).Id
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), cobol)
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE key = 'masseforflytning'`)
	long := strings.Repeat("x", 201)
	for _, tc := range []struct {
		name, code, field string
		body              map[string]any
	}{
		{"no level on a competence with one", "level_required", "level", map[string]any{"competenceId": rust}},
		{"a level on one without", "level_not_allowed", "level", map[string]any{"competenceId": competenceID(t, h, "hms-kort"), "level": 2}},
		{"a level of 5", "", "level", map[string]any{"competenceId": rust, "level": 5}},
		{"a hidden competence", "competence_unavailable", "competenceId", map[string]any{"competenceId": competenceID(t, h, "masseforflytning")}},
		{"an archived competence", "competence_unavailable", "competenceId", map[string]any{"competenceId": cobol, "level": 1}},
		{"an unknown competence", "competence_unavailable", "competenceId", map[string]any{"competenceId": 9999999}},
		{"an end before the start", "", "validTo", map[string]any{"competenceId": rust, "level": 1, "validFrom": "2026-05-01", "validTo": "2026-04-30"}},
		{"an end before the issue date", "", "validTo", map[string]any{"competenceId": rust, "level": 1, "issuedOn": "2026-05-01", "validTo": "2026-04-30"}},
		{"an issuer of 201 characters", "", "issuer", map[string]any{"competenceId": rust, "level": 1, "issuer": long}},
		{"a reference of 101 characters", "", "reference", map[string]any{"competenceId": rust, "level": 1, "reference": long[:101]}},
	} {
		p := refused(t, w.kariC.Do(http.MethodPost, personCompetencesPath(w.kari.Id), tc.body), http.StatusBadRequest, tc.code)
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if rows := listPersonCompetences(t, w.kariC, w.kari.Id, false); len(rows) != 4 {
		t.Errorf("Kari has %d rows, want the four added", len(rows))
	}
}

// The person changes their own declared row — a field left out cleared but
// validFrom, which keeps its value — and not a verified one (verified_row);
// a verifier and HR change any current row; a superseded row is nobody's;
// a stale revision is a conflict; a colleague the uniform 403.
func TestPersonCompetences_Change(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	own := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse, "issuedOn": "2026-03-01", "issuer": "Kurs AS"})

	var changed gen.StaffPersonCompetence
	do(t, w.kariC, http.MethodPut, personCompetencePath(own.Id), map[string]any{"issuedOn": "2026-03-02", "validTo": "2027-03-02"},
		http.StatusOK, &changed, ifMatch(own.Revision))
	if competenceFrom(changed) != "2026-03-01" || changed.Issuer != nil || changed.ValidTo == nil || changed.Revision != own.Revision+1 {
		t.Errorf("changed = %+v, want validFrom kept, the issuer cleared, the end set", changed)
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_changed", own.Id); f == nil || f["issuedOn"] == nil || f["issuer"] == nil {
		t.Errorf("the changed event = %v, want the issue date and the issuer", f)
	}
	refused(t, w.kariC.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{}, ifMatch(own.Revision)), http.StatusConflict, "revision_conflict")
	if r := w.viewer.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{}, ifMatch(changed.Revision)); r.Status != http.StatusForbidden {
		t.Errorf("a colleague's change = %d, want 403", r.Status)
	}
	if r := w.nobody.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{}, ifMatch(changed.Revision)); r.Status != http.StatusNotFound {
		t.Errorf("a stranger's change = %d, want 404", r.Status)
	}

	verified := verify(t, w.bossC, own.Id, map[string]any{"basis": "evidence"})
	refused(t, w.kariC.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{}, ifMatch(verified.Revision)), http.StatusConflict, "verified_row")
	var byBoss gen.StaffPersonCompetence
	do(t, w.bossC, http.MethodPut, personCompetencePath(own.Id), map[string]any{"reference": "FSE-9"}, http.StatusOK, &byBoss, ifMatch(verified.Revision))
	if byBoss.Reference == nil || *byBoss.Reference != "FSE-9" || !byBoss.Verified {
		t.Errorf("the manager's change = %+v, want the reference, still verified", byBoss)
	}
	p := refused(t, w.hr.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{"level": 2}, ifMatch(byBoss.Revision)),
		http.StatusBadRequest, "level_not_allowed")
	if len(p.Errors["level"]) == 0 {
		t.Errorf("errors = %v, want one on level", p.Errors)
	}

	renew(t, w.kariC, own.Id, map[string]any{"issuedOn": "2026-09-01"})
	var old gen.StaffPersonCompetence
	for _, r := range listPersonCompetences(t, w.hr, w.kari.Id, true) {
		if r.Id == own.Id {
			old = r
		}
	}
	refused(t, w.hr.Do(http.MethodPut, personCompetencePath(own.Id), map[string]any{}, ifMatch(old.Revision)), http.StatusConflict, "superseded")
}

// A verifier who changes a verified row's level verifies that level: the
// stamp's instant and verifier move to them, its basis and note kept; a
// change that leaves the level alone leaves the stamp as it was.
func TestPersonCompetences_AVerifierChangingTheLevelRestamps(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 3})
	verified := verify(t, w.bossC, row.Id, map[string]any{"basis": "evidence", "level": 3, "note": "Seen in the billing project"})
	verifier := func() uuid.UUID {
		return modtest.One[uuid.UUID](t, h.Harness, `SELECT verified_by_user_id FROM staff.person_competences WHERE id = $1`, row.Id)
	}
	hr, hrUser := signIn(t, h, "staff:manage")

	h.Advance(time.Hour)
	var same gen.StaffPersonCompetence
	do(t, hr, http.MethodPut, personCompetencePath(row.Id), map[string]any{"level": 3, "reference": "GO-1"}, http.StatusOK, &same, ifMatch(verified.Revision))
	if !same.VerifiedAt.Equal(*verified.VerifiedAt) || verifier() != w.bossUser {
		t.Errorf("a change of the reference moved the stamp: %+v", same)
	}

	h.Advance(time.Hour)
	var changed gen.StaffPersonCompetence
	do(t, hr, http.MethodPut, personCompetencePath(row.Id), map[string]any{"level": 2, "reference": "GO-1"}, http.StatusOK, &changed, ifMatch(same.Revision))
	if !changed.Verified || *changed.Level != 2 || !changed.VerifiedAt.After(*verified.VerifiedAt) || verifier() != hrUser ||
		*changed.VerifiedBasis != "evidence" || changed.VerifiedNote == nil || *changed.VerifiedNote != "Seen in the billing project" {
		t.Errorf("changed = %+v, want level 2 verified by HR now, the basis and the note kept", changed)
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_changed", row.Id); f == nil ||
		f["level"].(map[string]any)["from"] != float64(3) || f["level"].(map[string]any)["to"] != float64(2) {
		t.Errorf("the changed event = %v, want the level from 3 to 2", f)
	}
}

// A manager of the person today, an Approver and HR verify — the person
// never (own_record), a colleague the uniform 403; the level the verifier
// agrees to becomes the row's, and the event names it on both sides and the
// basis, never the note; a verified row verified again moves the stamp.
func TestPersonCompetences_Verify(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 3})

	refused(t, w.kariC.Do(http.MethodPost, verifyPath(row.Id), map[string]any{"basis": "evidence", "level": 3}), http.StatusForbidden, "own_record")
	refused(t, w.viewer.Do(http.MethodPost, verifyPath(row.Id), map[string]any{"basis": "evidence", "level": 3}), http.StatusForbidden, "")
	if r := w.nobody.Do(http.MethodPost, verifyPath(row.Id), map[string]any{"basis": "evidence", "level": 3}); r.Status != http.StatusNotFound {
		t.Errorf("a stranger's verify = %d, want 404", r.Status)
	}
	for _, tc := range []struct {
		name, code, field string
		body              map[string]any
	}{
		{"an unknown basis", "", "basis", map[string]any{"basis": "hearsay", "level": 2}},
		{"a note of 201 characters", "", "note", map[string]any{"basis": "evidence", "level": 2, "note": strings.Repeat("n", 201)}},
		{"no level", "level_required", "level", map[string]any{"basis": "evidence"}},
	} {
		p := refused(t, w.bossC.Do(http.MethodPost, verifyPath(row.Id), tc.body), http.StatusBadRequest, tc.code)
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}

	verified := verify(t, w.bossC, row.Id, map[string]any{"basis": "evidence", "level": 2, "note": "Seen in the billing project"})
	if !verified.Verified || *verified.Level != 2 || *verified.VerifiedBasis != "evidence" || verified.VerifiedNote == nil || verified.VerifiedBy == nil {
		t.Errorf("verified = %+v, want level 2 on the evidence, with the note", verified)
	}
	f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_verified", row.Id)
	if level, _ := f["level"].(map[string]any); level["from"] != float64(3) || level["to"] != float64(2) ||
		f["basis"].(map[string]any)["to"] != "evidence" {
		t.Errorf("the verified event = %v, want the level from 3 to 2 and the basis", f)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND payload::text LIKE '%billing project%'`, w.kari.Id); n != 0 {
		t.Errorf("%d events carry the note, want none", n)
	}

	h.Advance(time.Hour)
	again := verify(t, w.approver, row.Id, map[string]any{"basis": "observed", "level": 2})
	if *again.VerifiedBasis != "observed" || !again.VerifiedAt.After(*verified.VerifiedAt) || again.VerifiedNote != nil {
		t.Errorf("verified again = %+v, want the stamp moved", again)
	}
	verify(t, w.hr, row.Id, map[string]any{"basis": "observed", "level": 4})

	fse := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	renew(t, w.bossC, fse.Id, map[string]any{})
	refused(t, w.bossC.Do(http.MethodPost, verifyPath(fse.Id), map[string]any{"basis": "observed"}), http.StatusConflict, "superseded")
}

// A renewal is a new current row and the old row superseded, kept as it
// was: validTo proposed from the issue date and the validity, the person's
// renewal declared, a verifier's verified; ?history adds the chain, newest
// first.
func TestPersonCompetences_Renew(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	old := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse, "issuedOn": "2025-09-01", "validTo": "2026-09-01"})
	if !old.Expired || old.Expiring {
		t.Errorf("old = %+v, want expired on 12 September 2026", old)
	}
	if r := w.viewer.Do(http.MethodPost, renewPath(old.Id), map[string]any{}); r.Status != http.StatusForbidden {
		t.Errorf("a colleague's renewal = %d, want 403", r.Status)
	}

	renewed := renew(t, w.kariC, old.Id, map[string]any{"issuedOn": "2026-09-01", "issuer": "Kurs AS"})
	if renewed.Verified || *renewed.Source != "self" || competenceFrom(renewed) != "2026-09-01" || renewed.ValidTo == nil ||
		renewed.ValidTo.Format(time.DateOnly) != "2027-09-01" || renewed.Expired || renewed.Expiring {
		t.Errorf("renewed = %+v, want declared, from 2026-09-01 to 2027-09-01", renewed)
	}
	current := listPersonCompetences(t, w.kariC, w.kari.Id, false)
	if len(current) != 1 || current[0].Id != renewed.Id {
		t.Errorf("current rows = %+v, want the renewal alone", current)
	}
	chain := listPersonCompetences(t, w.kariC, w.kari.Id, true)
	if len(chain) != 2 || chain[0].Id != renewed.Id || chain[1].Id != old.Id || !chain[1].Superseded || chain[1].Expired ||
		chain[1].ValidTo.Format(time.DateOnly) != "2026-09-01" {
		t.Errorf("history = %+v, want the renewal then the old row, superseded, its dates kept", chain)
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_renewed", renewed.Id); f == nil ||
		f["validTo"].(map[string]any)["to"] != "2027-09-01" {
		t.Errorf("the renewed event = %v, want the new end", f)
	}
	refused(t, w.kariC.Do(http.MethodPost, renewPath(old.Id), map[string]any{}), http.StatusConflict, "superseded")

	byBoss := renew(t, w.bossC, renewed.Id, map[string]any{"issuedOn": "2026-09-10", "validTo": "2027-06-01"})
	if !byBoss.Verified || *byBoss.Source != "manager" || byBoss.ValidTo.Format(time.DateOnly) != "2027-06-01" {
		t.Errorf("the manager's renewal = %+v, want verified, the end as given", byBoss)
	}
	p := refused(t, w.bossC.Do(http.MethodPost, renewPath(byBoss.Id), map[string]any{"validFrom": "2026-09-10", "validTo": "2026-09-09"}),
		http.StatusBadRequest, "")
	if len(p.Errors["validTo"]) == 0 {
		t.Errorf("errors = %v, want one on validTo", p.Errors)
	}

	// A certificate with a level keeps it, and the event names it.
	welding := addCompetence(t, w.hr, with(skillBody(t, w.h, "sveisesertifikat"), "kind", "certificate", "validityMonths", 24)).Id
	graded := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": welding, "level": 3, "issuedOn": "2025-01-10"})
	regraded := renew(t, w.kariC, graded.Id, map[string]any{"issuedOn": "2026-09-01"})
	if regraded.Level == nil || *regraded.Level != 3 {
		t.Errorf("the renewal's level = %v, want the old row's 3", regraded.Level)
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_renewed", regraded.Id); f == nil ||
		f["level"].(map[string]any)["to"] != float64(3) || f["validTo"].(map[string]any)["to"] != "2028-09-01" {
		t.Errorf("the renewed event = %v, want the level and the new end", f)
	}
}

// A skill is a proficiency, never valid until a day (skill_no_validity): an
// end on a skill's row is a field error on validTo when it is declared and
// when it is changed, and a skill's row is changed, never renewed.
func TestPersonCompetences_ASkillHasNoValidity(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	p := refused(t, w.kariC.Do(http.MethodPost, personCompetencesPath(w.kari.Id),
		map[string]any{"competenceId": w.golang, "level": 2, "validTo": "2027-01-01"}), http.StatusBadRequest, "skill_no_validity")
	if len(p.Errors["validTo"]) == 0 {
		t.Errorf("a declaration: errors = %v, want one on validTo", p.Errors)
	}

	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 2})
	p = refused(t, w.bossC.Do(http.MethodPut, personCompetencePath(row.Id), map[string]any{"level": 2, "validTo": "2027-01-01"}, ifMatch(row.Revision)),
		http.StatusBadRequest, "skill_no_validity")
	if len(p.Errors["validTo"]) == 0 {
		t.Errorf("a change: errors = %v, want one on validTo", p.Errors)
	}
	for _, c := range []*modtest.Client{w.kariC, w.bossC, w.hr} {
		refused(t, c.Do(http.MethodPost, renewPath(row.Id), map[string]any{}), http.StatusConflict, "skill_no_validity")
	}
	if rows := listPersonCompetences(t, w.hr, w.kari.Id, true); len(rows) != 1 || rows[0].ValidTo != nil || rows[0].Superseded {
		t.Errorf("Kari's rows = %+v, want the one skill row, without end, current", rows)
	}
}

// The person removes their own declared row and the evidence they uploaded
// for it — its object queued and removed — but not HR's upload, nor a
// verified row (verified_row); a verifier removes any current row; HR alone
// removes a chain; a superseded row goes only with its chain.
func TestPersonCompetences_Remove(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h

	own := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	withEvidence := addEvidence(t, w.kariC, own.Id)
	key := modtest.One[string](t, h.Harness, `SELECT object_key FROM staff.documents WHERE id = $1`, withEvidence.Evidence.DocumentId)
	if r := w.viewer.Do(http.MethodDelete, personCompetencePath(own.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("a colleague's removal = %d, want 403", r.Status)
	}
	do(t, w.kariC, http.MethodDelete, personCompetencePath(own.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d documents left, want Kari's evidence removed with her row", n)
	}
	if h.objects.has(key) || h.Count(t, `SELECT count(*) FROM staff.object_deletions`) != 0 {
		t.Error("the evidence's object or its ledger row is left")
	}
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_removed", own.Id); f == nil || f["chain"].(map[string]any)["to"] != false {
		t.Errorf("the removed event = %v, want chain false", f)
	}

	again := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	addEvidence(t, w.hr, again.Id)
	do(t, w.kariC, http.MethodDelete, personCompetencePath(again.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, w.kari.Id); n != 1 {
		t.Errorf("%d documents, want HR's upload kept", n)
	}

	skill := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 2})
	verify(t, w.bossC, skill.Id, map[string]any{"basis": "observed", "level": 2})
	refused(t, w.kariC.Do(http.MethodDelete, personCompetencePath(skill.Id), nil), http.StatusConflict, "verified_row")
	do(t, w.bossC, http.MethodDelete, personCompetencePath(skill.Id), nil, http.StatusNoContent, nil)

	hms := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": competenceID(t, h, "hms-kort")})
	next := renew(t, w.kariC, hms.Id, map[string]any{})
	refused(t, w.hr.Do(http.MethodDelete, personCompetencePath(hms.Id)+"?chain=true", nil), http.StatusConflict, "superseded")
	if r := w.bossC.Do(http.MethodDelete, personCompetencePath(next.Id)+"?chain=true", nil); r.Status != http.StatusForbidden {
		t.Errorf("a manager's chain removal = %d, want 403", r.Status)
	}
	do(t, w.hr, http.MethodDelete, personCompetencePath(next.Id)+"?chain=true", nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.person_competences WHERE person_id = $1 AND competence_id = $2`, w.kari.Id, competenceID(t, h, "hms-kort")); n != 0 {
		t.Errorf("%d rows of the chain left, want none", n)
	}
	if r := w.hr.Do(http.MethodDelete, personCompetencePath(next.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a removed row = %d, want 404", r.Status)
	}
}

// A renewal removed alone makes the row it renewed current again, in the
// default list, and competence_removed names it as restored: the person's
// own declared renewal, and a verifier's of any current row. The chain goes
// whole, as before.
func TestPersonCompetences_RemovingARenewalRestoresTheRowItRenewed(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h

	old := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse, "issuedOn": "2025-09-01", "validTo": "2026-09-01"})
	verify(t, w.bossC, old.Id, map[string]any{"basis": "evidence"})
	renewed := renew(t, w.kariC, old.Id, map[string]any{"issuedOn": "2026-09-01"})
	do(t, w.kariC, http.MethodDelete, personCompetencePath(renewed.Id), nil, http.StatusNoContent, nil)
	current := listPersonCompetences(t, w.kariC, w.kari.Id, false)
	if len(current) != 1 || current[0].Id != old.Id || current[0].Superseded || !current[0].Verified ||
		current[0].ValidTo.Format(time.DateOnly) != "2026-09-01" || current[0].Revision <= old.Revision {
		t.Errorf("Kari's current rows = %+v, want the verified row she renewed, current again", current)
	}
	f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_removed", renewed.Id)
	if f == nil || f["chain"].(map[string]any)["to"] != false || f["restored"].(map[string]any)["to"] != float64(old.Id) {
		t.Errorf("the removed event = %v, want the restored row named", f)
	}

	hms := declare(t, w.hr, w.kari.Id, map[string]any{"competenceId": competenceID(t, h, "hms-kort")})
	next := renew(t, w.bossC, hms.Id, map[string]any{"issuedOn": "2026-09-01"})
	do(t, w.bossC, http.MethodDelete, personCompetencePath(next.Id), nil, http.StatusNoContent, nil)
	if rows := listPersonCompetences(t, w.hr, w.kari.Id, true); len(rows) != 2 {
		t.Errorf("Kari's rows = %+v, want FSE's and HMS-kort's, one each", rows)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.person_competences WHERE id = $1 AND superseded_by_id IS NULL`, hms.Id); n != 1 {
		t.Error("the manager's removal of the renewal did not restore the row it renewed")
	}
	f = competenceEventFields(t, w.bossC, w.kari.Id, "competence_removed", next.Id)
	if f == nil || f["restored"].(map[string]any)["to"] != float64(hms.Id) {
		t.Errorf("the removed event = %v, want the restored row named", f)
	}

	// A row that renewed nothing names no row restored.
	golang := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 1})
	do(t, w.kariC, http.MethodDelete, personCompetencePath(golang.Id), nil, http.StatusNoContent, nil)
	if f := competenceEventFields(t, w.kariC, w.kari.Id, "competence_removed", golang.Id); f == nil || f["restored"] != nil {
		t.Errorf("the removed event = %v, want no restored row", f)
	}

	again := renew(t, w.kariC, old.Id, map[string]any{"issuedOn": "2026-09-02"})
	do(t, w.hr, http.MethodDelete, personCompetencePath(again.Id)+"?chain=true", nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.person_competences WHERE person_id = $1 AND competence_id = $2`, w.kari.Id, w.fse); n != 0 {
		t.Errorf("%d rows of the chain left, want none", n)
	}
}

// The evidence is downloaded by everyone who reads the row's detail — the
// person, their manager, the Approver, HR and staff:personal-data — as a
// document is, with its headers; a colleague and a stranger get the 404, as
// does a row with no evidence; the store failing is 503.
func TestPersonCompetences_EvidenceDownload(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	filed := addEvidence(t, w.kariC, row.Id)
	want := pdfUpload(0).data

	for name, c := range map[string]*modtest.Client{
		"Kari": w.kariC, "her manager": w.bossC, "an Approver": w.approver, "HR": w.hr, "staff:personal-data": w.pd,
	} {
		r := c.Do(http.MethodGet, evidencePath(row.Id), nil)
		if r.Status != http.StatusOK || !bytes.Equal(r.Body, want) {
			t.Errorf("%s: status %d, %d bytes; want 200 and the file", name, r.Status, len(r.Body))
			continue
		}
		for header, value := range map[string]string{
			"Content-Type":            "application/pdf",
			"Cache-Control":           "private, no-store",
			"X-Content-Type-Options":  "nosniff",
			"Content-Security-Policy": "default-src 'none'; sandbox",
			"Content-Disposition":     "inline; filename*=UTF-8''avtale.pdf",
		} {
			if got := r.Header(header); got != value {
				t.Errorf("%s: %s = %q, want %q", name, header, got, value)
			}
		}
	}
	// The manager reads the evidence, not the person's documents.
	if r := w.bossC.Do(http.MethodGet, documentPath(filed.Evidence.DocumentId), nil); r.Status != http.StatusNotFound {
		t.Errorf("the manager's download of the document = %d, want 404", r.Status)
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "a stranger": w.nobody} {
		if r := c.Do(http.MethodGet, evidencePath(row.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want the bare 404", name, r.Status, r.Body)
		}
	}
	bare := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": competenceID(t, h, "hms-kort")})
	if r := w.hr.Do(http.MethodGet, evidencePath(bare.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a row with no evidence = %d, want 404", r.Status)
	}
	if r := w.hr.Do(http.MethodGet, evidencePath(9999999), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown row = %d, want 404", r.Status)
	}

	h.objects.failGet(errors.New("disk gone"))
	refused(t, w.bossC.Do(http.MethodGet, evidencePath(row.Id), nil), http.StatusServiceUnavailable, "storage_unavailable")
	h.objects.failGet(nil)
}

// The evidence is a document in the person's archive under the seeded
// certificate category, with no employment, uploaded by the person on their
// own row or by HR; a second upload replaces the reference and keeps the
// earlier document; the store failing is 503 with nothing recorded; the
// documents' cap and file rules hold.
func TestPersonCompetences_Evidence(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})

	first := addEvidence(t, w.kariC, row.Id)
	if first.Evidence == nil || first.Evidence.ContentType != "application/pdf" || first.Evidence.FileName != "avtale.pdf" {
		t.Fatalf("the row = %+v, want its evidence", first)
	}
	var category string
	var employment *int64
	var uploader uuid.UUID
	if err := h.Pool().QueryRow(t.Context(), `
		SELECT c.key, d.employment_id, d.uploaded_by_user_id FROM staff.documents d
		JOIN staff.document_categories c ON c.id = d.category_id WHERE d.id = $1`, first.Evidence.DocumentId).Scan(&category, &employment, &uploader); err != nil {
		t.Fatal(err)
	}
	if category != "certificate" || employment != nil || uploader != w.kariUser {
		t.Errorf("the document = %s, %v, %v; want certificate, no employment, uploaded by Kari", category, employment, uploader)
	}
	if len(h.objects.keys()) != 1 {
		t.Errorf("the store holds %v, want the one object", h.objects.keys())
	}
	if competenceEventFields(t, w.kariC, w.kari.Id, "competence_evidence_added", row.Id) == nil {
		t.Error("no competence_evidence_added event")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'document_added'`, w.kari.Id); n != 1 {
		t.Errorf("%d document_added events, want one", n)
	}

	second := addEvidence(t, w.hr, row.Id)
	if second.Evidence.DocumentId == first.Evidence.DocumentId {
		t.Error("a second upload kept the first reference")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, w.kari.Id); n != 2 {
		t.Errorf("%d documents, want the earlier one kept", n)
	}

	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "a colleague": w.viewer} {
		if r := postEvidence(t, c, row.Id, pdfUpload(0)); r.Status != http.StatusForbidden {
			t.Errorf("%s's upload = %d, want 403", name, r.Status)
		}
	}
	if r := postEvidence(t, w.nobody, row.Id, pdfUpload(0)); r.Status != http.StatusNotFound {
		t.Errorf("a stranger's upload = %d, want 404", r.Status)
	}
	refused(t, postEvidence(t, w.kariC, row.Id, upload{fileName: "a.txt", contentType: "text/plain", data: []byte("hello")}),
		http.StatusBadRequest, "document_type_unsupported")

	h.objects.failPut(errors.New("disk full"))
	refused(t, postEvidence(t, w.kariC, row.Id, pdfUpload(0)), http.StatusServiceUnavailable, "storage_unavailable")
	h.objects.failPut(nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, w.kari.Id); n != 2 {
		t.Errorf("%d documents after the store failed, want 2", n)
	}

	// The upload's body cap is the documents' (bodyLimits), not the
	// platform's 1 MiB: a file of 9 MiB is filed, one over 10 MiB refused.
	big := upload{fileName: "stor.pdf", contentType: "application/pdf", data: testPDF(9 * 1024 * 1024)}
	if r := postEvidence(t, w.kariC, row.Id, big); r.Status != http.StatusOK {
		t.Errorf("a 9 MiB file = %d %s, want 200", r.Status, r.Body)
	}
	big.data = testPDF(10*1024*1024 + 1)
	refused(t, postEvidence(t, w.kariC, row.Id, big), http.StatusBadRequest, "document_too_large")

	h.Exec(t, `
		INSERT INTO staff.documents (person_id, category_id, object_key, file_name, content_type, size_bytes,
			uploaded_by_user_id, created_at, updated_at)
		SELECT $1::bigint, c.id, 'people/' || $1::bigint || '/seed-' || n, 'x.pdf', 'application/pdf', 10, $2, $3, $3
		FROM generate_series(1, 197) AS n, staff.document_categories c WHERE c.key = 'other'`, w.kari.Id, uuid.New(), h.Now())
	before := len(h.objects.keys())
	refused(t, postEvidence(t, w.kariC, row.Id, pdfUpload(0)), http.StatusConflict, "document_count_exceeded")
	if len(h.objects.keys()) != before {
		t.Error("the object written before the refusal is left in the store")
	}

	renew(t, w.kariC, row.Id, map[string]any{})
	refused(t, postEvidence(t, w.hr, row.Id, pdfUpload(0)), http.StatusConflict, "superseded")
}

// canVerifyCompetence is the verifier's rule for the caller on the person
// (design §4): a manager of theirs today, staff:approve or staff:manage —
// never the person, never a colleague or staff:personal-data alone.
func TestPersonCompetences_TheVerifyCapability(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	for _, tc := range []struct {
		name string
		c    *modtest.Client
		id   int64
		want bool
	}{
		{"Kari on herself", w.kariC, w.kari.Id, false},
		{"Bjørn on Kari, his report", w.bossC, w.kari.Id, true},
		{"Bjørn on himself", w.bossC, w.boss.Id, false},
		{"HR on Kari", w.hr, w.kari.Id, true},
		{"an Approver on Kari", w.approver, w.kari.Id, true},
		{"a colleague on Kari", w.viewer, w.kari.Id, false},
		{"staff:personal-data on Kari", w.pd, w.kari.Id, false},
	} {
		if got := getPerson(t, tc.c, tc.id).Capabilities.CanVerifyCompetence; got != tc.want {
			t.Errorf("%s: canVerifyCompetence = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Both personal-data files carry the person's competences, the chain newest
// first with every field the file's reader reads — the verifier's note
// among them, both readers reading the detail.
func TestPersonCompetences_InTheExports(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	old := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse, "issuedOn": "2025-09-01", "issuer": "Kurs AS"})
	verify(t, w.bossC, old.Id, map[string]any{"basis": "evidence", "note": "Card seen"})
	renewed := renew(t, w.kariC, old.Id, map[string]any{"issuedOn": "2026-09-01"})

	for name, file := range map[string]gen.StaffPersonalData{
		"the complete file": exportOf(t, w.pd.Do(http.MethodGet, personalDataPath(w.kari.Id), nil)),
		"the person's copy": exportOf(t, w.kariC.Do(http.MethodGet, meExportPath, nil)),
	} {
		if len(file.Competences) != 2 || file.Competences[0].Id != renewed.Id || file.Competences[1].Id != old.Id {
			t.Fatalf("%s: competences = %+v, want the chain newest first", name, file.Competences)
		}
		o := file.Competences[1]
		if !o.Superseded || o.VerifiedNote == nil || *o.VerifiedNote != "Card seen" || o.Issuer == nil || o.VerifiedBy == nil {
			t.Errorf("%s: the old row = %+v, want superseded with its detail", name, o)
		}
	}
}

// The D erase deletes the person's rows — a renewal's chain with them — and
// their evidence as a certificate document, and blanks the competence
// events' values.
func TestPersonCompetences_TheEraseRemovesThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p := createPerson(t, hr, newPerson("Erik", "Glemt"))
	pastEmployment(t, hr, p.Id, "2022-01-01", "2024-12-31", terms())
	row := declare(t, hr, p.Id, map[string]any{"competenceId": competenceID(t, h, "fse"), "issuer": "Kurs AS"})
	addEvidence(t, hr, row.Id)
	renew(t, hr, row.Id, map[string]any{"issuedOn": "2024-06-01"})

	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if n := h.Count(t, `SELECT count(*) FROM staff.person_competences WHERE person_id = $1`, p.Id); n != 0 {
		t.Errorf("%d competence rows after the erase, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE person_id = $1`, p.Id); n != 0 {
		t.Errorf("%d documents after the erase, want the evidence gone", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'competence_%'
		AND payload->'fields'->>'competenceKey' <> '"changed"' AND payload->'fields'->>'competenceKey' <> 'changed'`, p.Id); n != 0 {
		t.Errorf("%d competence events still name the competence, want every value blanked", n)
	}
}
