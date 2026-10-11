package staff_test

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func legalHoldPath(id int64) string  { return personPath(id) + "/legal-hold" }
func reassessedPath(id int64) string { return personPath(id) + "/reassessed" }

// The legal hold (design S15, class F) is set with a reason and lifted by
// staff:personal-data: setting it records the reason, who and when, as a
// legal_hold_set event naming legalHold and reason before and after; lifting
// it clears all three, as legal_hold_lifted. Writing what is there already
// records nothing.
func TestLegalHold_SetAndLifted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signIn(t, h, "staff:personal-data")
	p := createPerson(t, hr, newPerson("Tove", "Tvist"))

	var held gen.StaffPerson
	do(t, pd, http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": true, "reason": "Tvist om oppsigelsen"}, http.StatusOK, &held)
	if l := held.LegalHold; l == nil || !l.Held || l.Reason == nil || *l.Reason != "Tvist om oppsigelsen" || l.At == nil || !l.At.Equal(h.Now()) {
		t.Fatalf("legalHold = %+v, want held with the reason and now", held.LegalHold)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND legal_hold AND legal_hold_by_user_id = $2`, p.Id, pdID); n != 1 {
		t.Error("the hold does not name who set it")
	}
	set := listEvents(t, hr, p.Id)[0]
	if set.Kind != "legal_hold_set" {
		t.Fatalf("latest event = %s, want legal_hold_set", set.Kind)
	}
	if f := fieldsOf(set); f["legalHold"].(map[string]any)["from"] != false || f["legalHold"].(map[string]any)["to"] != true ||
		f["reason"].(map[string]any)["to"] != "Tvist om oppsigelsen" {
		t.Errorf("legal_hold_set fields = %v", f)
	}

	// The same again: nothing written.
	events := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.Id)
	do(t, pd, http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": true, "reason": "Tvist om oppsigelsen"}, http.StatusOK, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1`, p.Id); n != events {
		t.Errorf("an unchanged hold recorded %d events", n-events)
	}

	h.Advance(time.Hour)
	var lifted gen.StaffPerson
	do(t, pd, http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": false}, http.StatusOK, &lifted)
	if l := lifted.LegalHold; l == nil || l.Held || l.Reason != nil || l.At != nil {
		t.Errorf("legalHold = %+v, want lifted, no reason, no time", lifted.LegalHold)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND NOT legal_hold AND legal_hold_reason IS NULL
		AND legal_hold_by_user_id IS NULL AND legal_hold_at IS NULL`, p.Id); n != 1 {
		t.Error("the lift left a hold column set")
	}
	out := listEvents(t, hr, p.Id)[0]
	if f := fieldsOf(out); out.Kind != "legal_hold_lifted" || f["legalHold"].(map[string]any)["to"] != false ||
		f["reason"].(map[string]any)["from"] != "Tvist om oppsigelsen" {
		t.Errorf("latest event = %s %v, want legal_hold_lifted from the reason", out.Kind, f)
	}

	for name, body := range map[string]map[string]any{
		"a hold without a reason": {"held": true},
		"a blank reason":          {"held": true, "reason": "  "},
		"a reason too long":       {"held": true, "reason": strings.Repeat("x", 501)},
		"a lift with a reason":    {"held": false, "reason": "Done"},
	} {
		if pr := refused(t, pd.Do(http.MethodPut, legalHoldPath(p.Id), body), http.StatusBadRequest, ""); len(pr.Errors["reason"]) == 0 {
			t.Errorf("%s: errors %v, want reason", name, pr.Errors)
		}
	}
	if r := pd.Do(http.MethodPut, legalHoldPath(p.Id+1000), map[string]any{"held": false}); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want the bare 404", r.Status, r.Body)
	}
	if r := hr.Do(http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": false}); r.Status != http.StatusForbidden {
		t.Errorf("staff:manage alone: %d, want 403", r.Status)
	}
}

// A held person is never deleted (design S15): DELETE answers legal_hold,
// judged before whatever else names them; lifted, a person created in error
// is deleted as before.
func TestLegalHold_RefusesTheDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	alone := createPerson(t, hr, newPerson("Created", "InError"))
	employed := createPerson(t, hr, newPerson("Has", "Employment"))
	seedEmployment(t, h, employed.Id, 0)
	for _, p := range []gen.StaffPerson{alone, employed} {
		do(t, pd, http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": true, "reason": "Varslingssak"}, http.StatusOK, nil)
		refused(t, hr.Do(http.MethodDelete, personPath(p.Id), nil), http.StatusConflict, "legal_hold")
	}
	do(t, pd, http.MethodPut, legalHoldPath(alone.Id), map[string]any{"held": false}, http.StatusOK, nil)
	do(t, hr, http.MethodDelete, personPath(alone.Id), nil, http.StatusNoContent, nil)
}

// The hold group (plan, event shaping): erasedAt, legalHold and reassessedAt
// are staff:manage's and staff:personal-data's, absent for the person, their
// manager and a colleague; canHold is staff:personal-data's. A hold event's
// fields are "changed" to an audience without the group.
func TestLegalHold_TheHoldGroupPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:view")
	pd, _ := signIn(t, h, "staff:personal-data")
	manager := createPerson(t, hr, newPerson("Mona", "Leder"))
	boss, bossID := signIn(t, h)
	link(t, h, manager.Id, bossID)
	seedEmployment(t, h, manager.Id, 0)
	p := createPerson(t, hr, newPerson("Tove", "Tvist"))
	seedEmployment(t, h, p.Id, manager.Id)
	self, selfID := signIn(t, h)
	link(t, h, p.Id, selfID)
	colleague, _ := signIn(t, h, "staff:view")
	h.Exec(t, `UPDATE staff.people SET erased_at = $2, reassessed_at = $2 WHERE id = $1`, p.Id, h.Now())
	do(t, pd, http.MethodPut, legalHoldPath(p.Id), map[string]any{"held": true, "reason": "Tvist"}, http.StatusOK, nil)

	hold := []string{"erasedAt", "legalHold", "reassessedAt"}
	for _, c := range []struct {
		name    string
		client  *modtest.Client
		reads   bool
		canHold bool
	}{
		{"self", self, false, false},
		{"manager", boss, false, false},
		{"colleague", colleague, false, false},
		{"staff:manage", hr, true, false},
		{"staff:personal-data", pd, true, true},
	} {
		body := raw(t, c.client.Do(http.MethodGet, personPath(p.Id), nil))
		keys := slices.Collect(maps.Keys(body))
		for _, field := range hold {
			if slices.Contains(keys, field) != c.reads {
				t.Errorf("%s: %s present = %v, want %v", c.name, field, !c.reads, c.reads)
			}
		}
		capabilities, _ := body["capabilities"].(map[string]any)
		if capabilities["canHold"] != c.canHold {
			t.Errorf("%s: canHold = %v, want %v", c.name, capabilities["canHold"], c.canHold)
		}
	}

	var mine gen.PaginatedResponseOfStaffEvent
	do(t, self, http.MethodGet, meEventsPath, nil, http.StatusOK, &mine)
	if f := fieldsOf(eventOfKind(t, mine.Data, "legal_hold_set")); f["legalHold"] != "changed" || f["reason"] != "changed" {
		t.Errorf("the person reads legal_hold_set as %v, want each field changed", f)
	}
	if f := fieldsOf(eventOfKind(t, listEvents(t, hr, p.Id), "legal_hold_set")); f["reason"].(map[string]any)["to"] != "Tvist" {
		t.Errorf("staff:manage reads legal_hold_set as %v, want the values", f)
	}
}

// The reassessment (design S15, plan reading 16) is due for an erased person
// not yet reassessed from the day twelve months after their last employment
// ended: the day before it is reassessment_not_due, on the day it is
// recorded — when and by whom, a reassessed event naming reassessedAt — and
// once recorded it is not due again. A person who is not erased, or who has
// an employment that has not ended, is never due.
func TestReassessed_DueTwelveMonthsAfterTheLastEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	// The harness clock is 2026-09-12 12:00 UTC: the last end on 2025-09-13
	// makes 2026-09-13 the first day the reassessment is due.
	p := createPerson(t, hr, newPerson("Erik", "Glemt"))
	seedEmploymentDated(t, h, p.Id, 0, "2020-01-01", ptr("2023-06-30"))
	seedEmploymentDated(t, h, p.Id, 0, "2024-01-01", ptr("2025-09-13"))
	refused(t, pd.Do(http.MethodPost, reassessedPath(p.Id), nil), http.StatusConflict, "reassessment_not_due")
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, p.Id, h.Now())
	refused(t, pd.Do(http.MethodPost, reassessedPath(p.Id), nil), http.StatusConflict, "reassessment_not_due")

	setClock(t, h, time.Date(2026, time.September, 12, 22, 30, 0, 0, time.UTC)) // 00:30 on the 13th in Oslo
	// A session of the day before has lapsed by now; the reassessment is
	// recorded by a fresh one.
	hr, _ = signIn(t, h, "staff:manage")
	pd, pdID := signIn(t, h, "staff:personal-data")
	var out gen.StaffPerson
	do(t, pd, http.MethodPost, reassessedPath(p.Id), nil, http.StatusOK, &out)
	if out.ReassessedAt == nil || !out.ReassessedAt.Equal(h.Now()) {
		t.Errorf("reassessedAt = %v, want %s", out.ReassessedAt, h.Now())
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND reassessed_by_user_id = $2`, p.Id, pdID); n != 1 {
		t.Error("the reassessment does not name who recorded it")
	}
	e := listEvents(t, hr, p.Id)[0]
	if f := fieldsOf(e); e.Kind != "reassessed" || f["reassessedAt"] == nil || f["reassessedAt"].(map[string]any)["to"] == nil {
		t.Errorf("latest event = %s %v, want reassessed naming reassessedAt", e.Kind, f)
	}
	refused(t, pd.Do(http.MethodPost, reassessedPath(p.Id), nil), http.StatusConflict, "reassessment_not_due")

	open := createPerson(t, hr, newPerson("Åpen", "Ansatt"))
	seedEmploymentDated(t, h, open.Id, 0, "2020-01-01", ptr("2024-01-31"))
	seedEmploymentDated(t, h, open.Id, 0, "2024-02-01", nil)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, open.Id, h.Now())
	refused(t, pd.Do(http.MethodPost, reassessedPath(open.Id), nil), http.StatusConflict, "reassessment_not_due")

	if r := pd.Do(http.MethodPost, reassessedPath(p.Id+1000), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want the bare 404", r.Status, r.Body)
	}
	if r := hr.Do(http.MethodPost, reassessedPath(p.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:manage alone: %d, want 403", r.Status)
	}
}
