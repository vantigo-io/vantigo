package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// accountOf reads a person's holiday account for year as c.
func accountOf(t *testing.T, c *modtest.Client, personID int64, year int) gen.StaffHolidayAccount {
	t.Helper()
	var out gen.StaffHolidayAccount
	do(t, c, http.MethodGet, fmt.Sprintf("%s?year=%d", holidayAccountPath(personID), year), nil, http.StatusOK, &out)
	return out
}

// The holiday account is read by the person, their manager, HR and
// staff:personal-data, and is the 404 of an unknown person to anyone else;
// it counts approved days on working days and shows the requested ones
// apart, the default year being this year in the business time zone.
func TestHolidayAccount_WhoReadsIt(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	approved := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-12", "2026-10-13"))
	h.Exec(t, `UPDATE staff.absences SET status = 'approved' WHERE id = $1`, approved.Id)

	for name, c := range map[string]*modtest.Client{"the person": w.kariC, "her manager": w.bossC, "HR": w.hr, "personal-data": w.pd} {
		a := accountOf(t, c, w.kari.Id, 2026)
		if a.PersonId != w.kari.Id || a.Year != 2026 || a.Entitlement != 21 || a.Taken != 2 || a.Pending != 5 || a.Remaining != 19 || a.Overdrawn {
			t.Errorf("%s reads %+v, want 21, 2 taken, 5 pending, 19 remaining", name, a)
		}
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "a stranger": w.nobody} {
		if r := c.Do(http.MethodGet, holidayAccountPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, r.Status)
		}
	}
	var mine gen.StaffHolidayAccount
	do(t, w.kariC, http.MethodGet, meHolidayAccountPath, nil, http.StatusOK, &mine)
	if mine.Year != 2026 || mine.Taken != 2 {
		t.Errorf("my account = %+v, want this year's", mine)
	}
	refused(t, w.kariC.Do(http.MethodGet, meHolidayAccountPath+"?year=1999", nil), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodGet, holidayAccountPath(w.kari.Id)+"?year=2101", nil), http.StatusBadRequest, "")
	if r := w.hr.Do(http.MethodGet, holidayAccountPath(w.kari.Id+1000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person: %d, want 404", r.Status)
	}
	// The account turns overdrawn when the requests would take more than
	// remains: a warning on the write, never a refusal.
	big := requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-11-02", "2026-11-27"))
	if big.Overdrawn == nil || !*big.Overdrawn {
		t.Errorf("a request beyond what remains: overdrawn %v, want true", big.Overdrawn)
	}
}

// Adjustments and carry-over (design §4, plan pre-flight I8): HR records a
// correction (with its note) and a carry-over — agreed, the year's remaining
// by default, capped by the scheme; forced, with its note — at most one per
// person and year it comes from (carry_over_recorded), whichever way it is
// recorded; deleting it lets it be recorded again. Each write is an event.
func TestHolidayAdjustments(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	h := w.h
	// 2025: four days taken, so seventeen remain.
	h.Exec(t, `INSERT INTO staff.absences (person_id, type_id, first_day, last_day, status, requested_by_user_id, created_at, updated_at)
		VALUES ($1, $2, '2025-06-02', '2025-06-05', 'approved', $3, now(), now())`, w.kari.Id, typeID(t, h, "ferie"), w.kariUser)

	refused(t, w.hr.Do(http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 2, "reason": "correction"}), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 1.25, "reason": "correction", "note": "x"}), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 2, "reason": "agreed_carry_over"}), http.StatusBadRequest, "")
	var correction gen.StaffHolidayAdjustment
	do(t, w.hr, http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": -1.5, "reason": "correction", "note": "Feilregistrert"},
		http.StatusCreated, &correction)
	if correction.Days != -1.5 || correction.Reason != gen.Correction {
		t.Errorf("correction = %+v", correction)
	}

	refused(t, w.hr.Do(http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "forced"}), http.StatusBadRequest, "")
	refused(t, w.hr.Do(http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "agreed", "days": 11}), http.StatusBadRequest, "")
	var carried gen.StaffHolidayAdjustment
	do(t, w.hr, http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "agreed"}, http.StatusCreated, &carried)
	if carried.Year != 2026 || carried.FromYear == nil || *carried.FromYear != 2025 || carried.Days != 10 || carried.Reason != gen.AgreedCarryOver {
		t.Errorf("the agreed carry-over = %+v, want 10 days (the cap) from 2025 into 2026", carried)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.CarriedIn != 8.5 || a.Remaining != 29.5 {
		t.Errorf("2026 = %+v, want 10 carried in less the 1.5 corrected", a)
	}
	refused(t, w.hr.Do(http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "forced", "note": "x"}),
		http.StatusConflict, "carry_over_recorded")
	refused(t, w.hr.Do(http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 1, "reason": "forced_carry_over", "fromYear": 2025, "note": "x"}),
		http.StatusConflict, "carry_over_recorded")
	// A carry-over comes from the year just before the one it changes.
	p := refused(t, w.hr.Do(http.MethodPost, adjustmentsPath(w.kari.Id), map[string]any{"year": 2026, "days": 1, "reason": "forced_carry_over", "fromYear": 2024, "note": "x"}),
		http.StatusBadRequest, "")
	if len(p.Errors["fromYear"]) == 0 {
		t.Errorf("a carry-over from two years back: errors %v, want fromYear named", p.Errors)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'carry_over_recorded'
		AND (payload->>'adjustmentId')::bigint = $2`, w.kari.Id, carried.Id); n != 1 {
		t.Errorf("carry_over_recorded events = %d, want 1", n)
	}

	var list []gen.StaffHolidayAdjustment
	do(t, w.hr, http.MethodGet, adjustmentsPath(w.kari.Id), nil, http.StatusOK, &list)
	if len(list) != 2 {
		t.Errorf("adjustments = %+v, want two", list)
	}
	for name, c := range map[string]*modtest.Client{"the person": w.kariC, "her manager": w.bossC} {
		if r := c.Do(http.MethodGet, adjustmentsPath(w.kari.Id), nil); r.Status != http.StatusForbidden {
			t.Errorf("%s lists the adjustments: %d, want 403", name, r.Status)
		}
		if r := c.Do(http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2024, "kind": "agreed"}); r.Status != http.StatusForbidden {
			t.Errorf("%s carries over: %d, want 403", name, r.Status)
		}
	}
	do(t, w.hr, http.MethodDelete, adjustmentPath(carried.Id), nil, http.StatusNoContent, nil)
	if r := w.hr.Do(http.MethodDelete, adjustmentPath(carried.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a deleted adjustment deleted again: %d, want 404", r.Status)
	}
	var forced gen.StaffHolidayAdjustment
	do(t, w.hr, http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "forced", "days": 12.5, "note": "Syk i hovedferien"},
		http.StatusCreated, &forced)
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.CarriedIn != 11 {
		t.Errorf("2026 carried in %v, want the forced 12.5 uncapped less the 1.5 corrected", a.CarriedIn)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'holiday_adjustment_deleted'`, w.kari.Id); n != 1 {
		t.Errorf("holiday_adjustment_deleted events = %d, want 1", n)
	}
	// A year with nothing left has nothing to carry over.
	refused(t, w.hr.Do(http.MethodPost, carryOverPath(w.boss.Id), map[string]any{"fromYear": 2018, "kind": "agreed"}), http.StatusBadRequest, "")
}

// Automatic carry-over (plan pre-flight M6): turned on, the account derives
// the year before's remaining as a forced carry-in into each year whose
// 1 January is on or after the day it was turned on — turned on 12 September
// 2026, first into 2027 — and a carry-over recorded by hand takes its place.
func TestHolidayAccount_AutomaticCarryOver(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.CarriedInDerived != 0 {
		t.Errorf("off: derived %v, want 0", a.CarriedInDerived)
	}
	w.h.Exec(t, `UPDATE staff.settings SET auto_carry_over = true, auto_carry_over_since = '2026-09-12'`)
	if a := accountOf(t, w.hr, w.kari.Id, 2026); a.CarriedInDerived != 0 {
		t.Errorf("2026, already under way when it was turned on: derived %v, want 0", a.CarriedInDerived)
	}
	if a := accountOf(t, w.hr, w.kari.Id, 2027); a.CarriedInDerived != 21 || a.Remaining != 42 {
		t.Errorf("2027: %+v, want 2026's 21 carried in", a)
	}
	do(t, w.hr, http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2026, "kind": "agreed", "days": 3}, http.StatusCreated, nil)
	if a := accountOf(t, w.hr, w.kari.Id, 2027); a.CarriedInDerived != 0 || a.CarriedIn != 3 {
		t.Errorf("with a carry-over by hand: %+v, want 3 carried in, nothing derived", a)
	}
}

// ---- Races (plan Global Constraints "Races") ------------------------------

// Two carry-overs for one person and year at once (plan pre-flight I8): the
// first holds the person's lock and has written its row; the second waits on
// the lock — pg_blocking_pids names the first — and, once it commits, finds
// the carry-over recorded: carry_over_recorded, one row, never 40P01.
func TestHolidayAdjustments_TwoCarryOversRace(t *testing.T) {
	w := newAbsenceWorld(t, modtest.WithPoolMaxConns(2))
	h := w.h
	probe := ownConn(t, h)
	before := deadlocksIn(t, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	firstConn := ownConn(t, h)
	first, err := firstConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = first.Rollback(context.Background()) }()
	if _, err := first.Exec(ctx, `SELECT id FROM staff.people WHERE id = $1 FOR NO KEY UPDATE`, w.kari.Id); err != nil {
		t.Fatalf("the first's lock: %v", err)
	}
	if _, err := first.Exec(ctx, `INSERT INTO staff.holiday_adjustments (person_id, year, days, reason, from_year, by_user_id, created_at, updated_at)
		VALUES ($1, 2026, 5, 'agreed_carry_over', 2025, $2, now(), now())`, w.kari.Id, w.kariUser); err != nil {
		t.Fatalf("the first's carry-over: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() {
		answered <- w.hr.Do(http.MethodPost, carryOverPath(w.kari.Id), map[string]any{"fromYear": 2025, "kind": "agreed", "days": 2}, modtest.Context(ctx))
	}()
	_, blockers := waiterOn(t, probe)
	if want := []uint32{firstConn.PgConn().PID()}; !slices.Equal(blockers, want) {
		t.Fatalf("the second waits on %v, want the first %v", blockers, want)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// The refusal is the lock-time check's, which reads the committed row
	// once the lock is free — not the unique index's backstop, which would
	// answer only after a write that failed.
	if p := refused(t, <-answered, http.StatusConflict, "carry_over_recorded"); !strings.Contains(p.Detail, "already recorded") {
		t.Errorf("detail %q: want the lock-time check's (already recorded), not the index's", p.Detail)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.holiday_adjustments WHERE person_id = $1 AND from_year = 2025`, w.kari.Id); n != 1 {
		t.Errorf("carry-overs from 2025 = %d, want 1", n)
	}
	if after := deadlocksIn(t, probe); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
}

// Two requests for overlapping days by one person at once (plan Task 3): the
// first holds the person's lock and has written its row; the second waits on
// the lock and, once it commits, is judged against that row:
// absence_overlap, one row, never 40P01.
func TestAbsences_TwoOverlappingRequestsRace(t *testing.T) {
	w := newAbsenceWorld(t, modtest.WithPoolMaxConns(2))
	h := w.h
	probe := ownConn(t, h)
	before := deadlocksIn(t, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	firstConn := ownConn(t, h)
	first, err := firstConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = first.Rollback(context.Background()) }()
	if _, err := first.Exec(ctx, `SELECT id FROM staff.people WHERE id = $1 FOR NO KEY UPDATE`, w.kari.Id); err != nil {
		t.Fatalf("the first's lock: %v", err)
	}
	if _, err := first.Exec(ctx, `INSERT INTO staff.absences (person_id, type_id, first_day, last_day, status, requested_by_user_id, created_at, updated_at)
		VALUES ($1, $2, '2026-10-05', '2026-10-09', 'requested', $3, now(), now())`, w.kari.Id, typeID(t, h, "ferie"), w.kariUser); err != nil {
		t.Fatalf("the first's request: %v", err)
	}
	answered := make(chan *modtest.Response, 1)
	go func() {
		answered <- w.kariC.Do(http.MethodPost, meAbsencesPath, absence(t, h, "ferie", "2026-10-08", "2026-10-12"), modtest.Context(ctx))
	}()
	_, blockers := waiterOn(t, probe)
	if want := []uint32{firstConn.PgConn().PID()}; !slices.Equal(blockers, want) {
		t.Fatalf("the second waits on %v, want the first %v", blockers, want)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	refused(t, <-answered, http.StatusConflict, "absence_overlap")
	if n := h.Count(t, `SELECT count(*) FROM staff.absences WHERE person_id = $1`, w.kari.Id); n != 1 {
		t.Errorf("absences = %d, want 1", n)
	}
	if after := deadlocksIn(t, probe); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
}
