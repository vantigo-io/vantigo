package staff_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const holidaySchemesPath = "/api/v1/staff/holiday-schemes"

func holidaySchemePath(id int64) string { return rowPath(holidaySchemesPath, id) }

// schemesAs is the list as a caller holding permissions reads it.
func schemesAs(t *testing.T, h *harness, permissions ...string) []gen.StaffHolidayScheme {
	t.Helper()
	c, _ := signIn(t, h, permissions...)
	var out []gen.StaffHolidayScheme
	do(t, c, http.MethodGet, holidaySchemesPath, nil, http.StatusOK, &out)
	return out
}

// statutoryScheme is the seeded scheme.
func statutoryScheme(t *testing.T, h *harness) gen.StaffHolidayScheme {
	t.Helper()
	for _, s := range schemesAs(t, h) {
		if s.Seeded {
			return s
		}
	}
	t.Fatal("no seeded scheme")
	return gen.StaffHolidayScheme{}
}

// addScheme adds a scheme named name with entitlement virkedager and fails
// the test unless it was created.
func addScheme(t *testing.T, hr *modtest.Client, name string, entitlement int, kv ...any) gen.StaffHolidayScheme {
	t.Helper()
	var out gen.StaffHolidayScheme
	do(t, hr, http.MethodPost, holidaySchemesPath,
		with(map[string]any{"name": name, "entitlementVirkedager": entitlement, "extraFrom60Virkedager": 6}, kv...),
		http.StatusCreated, &out)
	return out
}

// schemeChange is the change request that sends a scheme back as it was
// read, with the given fields replaced.
func schemeChange(s gen.StaffHolidayScheme, kv ...any) map[string]any {
	return with(map[string]any{
		"name": s.Name, "entitlementVirkedager": s.EntitlementVirkedager, "extraFrom60Virkedager": s.ExtraFrom60Virkedager,
		"mainPeriodVirkedager": s.MainPeriodVirkedager, "afterSeptemberVirkedager": s.AfterSeptemberVirkedager,
		"maxCarryOverVirkedager": s.MaxCarryOverVirkedager, "archived": s.ArchivedAt != nil,
	}, kv...)
}

// The statutory scheme is seeded and the default — 25 / 6 / 18 / 6 / 12
// virkedager, 21 / 5 / 15 / 5 / 10 working days — and every caller reads the
// list.
func TestHolidaySchemes_TheStatutorySeed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, perms := range [][]string{{}, {"staff:view"}, {"staff:manage"}} {
		list := schemesAs(t, h, perms...)
		if len(list) != 1 {
			t.Fatalf("as %v: %d schemes, want the one seed", perms, len(list))
		}
		s := list[0]
		if s.Name != "Statutory (ferieloven)" || !s.IsDefault || !s.Seeded || s.ArchivedAt != nil ||
			s.EntitlementVirkedager != 25 || s.ExtraFrom60Virkedager != 6 || s.MainPeriodVirkedager != 18 ||
			s.AfterSeptemberVirkedager != 6 || s.MaxCarryOverVirkedager != 12 {
			t.Errorf("as %v: the seed = %+v, want the statutory 25/6/18/6/12, default", perms, s)
		}
		if c := s.Converted; c.Entitlement != 21 || c.ExtraFrom60 != 5 || c.MainPeriod != 15 || c.AfterSeptember != 5 || c.MaxCarryOver != 10 {
			t.Errorf("as %v: converted = %+v, want 21/5/15/5/10", perms, c)
		}
	}
}

// A scheme: a name unique however it is cased, an entitlement of 25 to 60
// virkedager, 6 to 30 more from 60, the other three 0 to the entitlement with
// their defaults; converted on the way out (30 → 25, 31 → 26). staff:manage
// alone, each an installation event.
func TestHolidaySchemes_Add(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")

	five := addScheme(t, hr, "Five weeks", 30)
	if five.IsDefault || five.Seeded || five.MainPeriodVirkedager != 18 || five.AfterSeptemberVirkedager != 6 ||
		five.MaxCarryOverVirkedager != 12 || five.Converted.Entitlement != 25 || five.Revision != 1 {
		t.Errorf("five weeks = %+v, want not default, the defaults 18/6/12, 25 working days", five)
	}
	odd := addScheme(t, hr, "Five weeks and a day", 31, "mainPeriodVirkedager", 24, "afterSeptemberVirkedager", 7, "maxCarryOverVirkedager", 18)
	if odd.Converted.Entitlement != 26 || odd.Converted.MainPeriod != 20 || odd.Converted.AfterSeptember != 6 || odd.Converted.MaxCarryOver != 15 {
		t.Errorf("31 virkedager converted = %+v, want 26/20/6/15", odd.Converted)
	}
	// The agreed carry-over is at most 12 virkedager plus the days above the
	// statutory 25 (ferieloven § 7 (3)): 30 days carry up to 17.
	if s := addScheme(t, hr, "Five weeks, carried", 30, "maxCarryOverVirkedager", 17); s.MaxCarryOverVirkedager != 17 {
		t.Errorf("a carry-over of 17 at 30 = %+v", s)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NULL AND kind = 'holiday_scheme_created' AND actor_user_id = $1`, hrID); n != 3 {
		t.Errorf("holiday_scheme_created events = %d, want 3", n)
	}

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a name another scheme has, cased otherwise", map[string]any{"name": "FIVE WEEKS", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6}, "name"},
		{"a blank name", map[string]any{"name": " ", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6}, "name"},
		{"below the statutory 25", map[string]any{"name": "x", "entitlementVirkedager": 24, "extraFrom60Virkedager": 6}, "entitlementVirkedager"},
		{"above 60", map[string]any{"name": "x", "entitlementVirkedager": 61, "extraFrom60Virkedager": 6}, "entitlementVirkedager"},
		{"below the statutory 6 from 60", map[string]any{"name": "x", "entitlementVirkedager": 30, "extraFrom60Virkedager": 5}, "extraFrom60Virkedager"},
		{"above 30 from 60", map[string]any{"name": "x", "entitlementVirkedager": 30, "extraFrom60Virkedager": 31}, "extraFrom60Virkedager"},
		{"a main period above the entitlement", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "mainPeriodVirkedager": 26}, "mainPeriodVirkedager"},
		{"a main period below the statutory 18", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "mainPeriodVirkedager": 17}, "mainPeriodVirkedager"},
		{"after September below the statutory 6", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "afterSeptemberVirkedager": 5}, "afterSeptemberVirkedager"},
		{"after September above the entitlement", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "afterSeptemberVirkedager": 26}, "afterSeptemberVirkedager"},
		{"a carry-over above the statutory 12 at 25", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "maxCarryOverVirkedager": 13}, "maxCarryOverVirkedager"},
		{"a carry-over above 12 and the extra 5 at 30", map[string]any{"name": "x", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6, "maxCarryOverVirkedager": 18}, "maxCarryOverVirkedager"},
		{"a carry-over below 0", map[string]any{"name": "x", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6, "maxCarryOverVirkedager": -1}, "maxCarryOverVirkedager"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refused(t, hr.Do(http.MethodPost, holidaySchemesPath, tc.body), http.StatusBadRequest, "")
			if len(p.Errors[tc.field]) == 0 {
				t.Errorf("errors = %v, want %s named", p.Errors, tc.field)
			}
		})
	}
	for _, perms := range [][]string{{"staff:view"}, {"staff:approve"}, {"staff:personal-data"}} {
		c, _ := signIn(t, h, perms...)
		do(t, c, http.MethodPost, holidaySchemesPath, map[string]any{"name": "y", "entitlementVirkedager": 25, "extraFrom60Virkedager": 6}, http.StatusForbidden, nil)
	}
}

// The default moves — by a new scheme or a change — and there is always
// exactly one: un-flagging the default is structure_default, and an archived
// scheme never becomes it (structure_archived).
func TestHolidaySchemes_TheDefaultMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	statutory := statutoryScheme(t, h)

	five := addScheme(t, hr, "Five weeks", 30, "isDefault", true)
	defaults := func() []string {
		var out []string
		for _, s := range schemesAs(t, h) {
			if s.IsDefault {
				out = append(out, s.Name)
			}
		}
		return out
	}
	if got := defaults(); !slices.Equal(got, []string{"Five weeks"}) {
		t.Errorf("defaults = %v, want Five weeks alone", got)
	}
	statutory = statutoryScheme(t, h)
	var back gen.StaffHolidayScheme
	do(t, hr, http.MethodPut, holidaySchemePath(statutory.Id), schemeChange(statutory, "isDefault", true), http.StatusOK, &back, ifMatch(statutory.Revision))
	if !back.IsDefault {
		t.Errorf("the statutory scheme made the default = %+v", back)
	}
	if got := defaults(); !slices.Equal(got, []string{"Statutory (ferieloven)"}) {
		t.Errorf("defaults = %v, want the statutory scheme alone", got)
	}
	refused(t, hr.Do(http.MethodPut, holidaySchemePath(back.Id), schemeChange(back, "isDefault", false), ifMatch(back.Revision)), http.StatusConflict, "structure_default")

	var current gen.StaffHolidayScheme
	for _, s := range schemesAs(t, h) {
		if s.Id == five.Id {
			current = s
		}
	}
	var archived gen.StaffHolidayScheme
	do(t, hr, http.MethodPut, holidaySchemePath(five.Id), schemeChange(current, "archived", true), http.StatusOK, &archived, ifMatch(current.Revision))
	if archived.ArchivedAt == nil {
		t.Errorf("archived = %+v, want archivedAt", archived)
	}
	refused(t, hr.Do(http.MethodPut, holidaySchemePath(five.Id), schemeChange(archived, "isDefault", true), ifMatch(archived.Revision)), http.StatusConflict, "structure_archived")
	refused(t, hr.Do(http.MethodPut, holidaySchemePath(five.Id), schemeChange(archived), ifMatch(archived.Revision+1)), http.StatusConflict, "revision_conflict")
	do(t, hr, http.MethodPut, holidaySchemePath(five.Id+1000), schemeChange(archived), http.StatusNotFound, nil, ifMatch(1))
}

// A scheme in use is never archived — the default, or one an employment
// names (holiday_scheme_in_use) — and is restored as freely as it was
// archived; a scheme nobody names is archived and leaves the picker, staying
// in the list.
func TestHolidaySchemes_ArchiveInUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	statutory := statutoryScheme(t, h)
	refused(t, hr.Do(http.MethodPut, holidaySchemePath(statutory.Id), schemeChange(statutory, "archived", true), ifMatch(statutory.Revision)),
		http.StatusConflict, "holiday_scheme_in_use")

	five := addScheme(t, hr, "Five weeks", 30)
	person := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	e := createEmployment(t, hr, person.Id, newEmployment("2026-01-01", terms(), "holidaySchemeId", five.Id))
	if e.HolidaySchemeId == nil || *e.HolidaySchemeId != five.Id {
		t.Fatalf("employment = %+v, want it to follow Five weeks", e)
	}
	refused(t, hr.Do(http.MethodPut, holidaySchemePath(five.Id), schemeChange(five, "archived", true), ifMatch(five.Revision)),
		http.StatusConflict, "holiday_scheme_in_use")

	var moved gen.StaffEmployment
	do(t, hr, http.MethodPut, employmentPath(e.Id), map[string]any{
		"startDate": "2026-01-01", "relationshipType": "ordinary", "businessUnitId": *e.BusinessUnitId,
	}, http.StatusOK, &moved, ifMatch(e.Revision))
	if moved.HolidaySchemeId != nil {
		t.Errorf("an employment changed with no scheme = %+v, want it to follow the default", moved)
	}
	var archived gen.StaffHolidayScheme
	do(t, hr, http.MethodPut, holidaySchemePath(five.Id), schemeChange(five, "archived", true), http.StatusOK, &archived, ifMatch(five.Revision))
	var restored gen.StaffHolidayScheme
	do(t, hr, http.MethodPut, holidaySchemePath(five.Id), schemeChange(archived, "archived", false), http.StatusOK, &restored, ifMatch(archived.Revision))
	if restored.ArchivedAt != nil {
		t.Errorf("restored = %+v, want no archivedAt", restored)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NULL AND kind = 'holiday_scheme_updated'`); n != 2 {
		t.Errorf("holiday_scheme_updated events = %d, want 2 (the archive and the restore)", n)
	}
}

// An employment names its scheme — on its creation and on its change, HR's
// write — in the employment group, which a colleague does not read; an
// archived scheme is refused structure_archived, unless the employment
// already follows it; an unknown one is a field error.
func TestEmployments_HolidayScheme(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	five := addScheme(t, hr, "Five weeks", 30)
	six := addScheme(t, hr, "Six weeks", 36)
	person := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	e := createEmployment(t, hr, person.Id, newEmployment("2026-01-01", terms()))
	if e.HolidaySchemeId != nil {
		t.Errorf("an employment naming no scheme = %+v, want none (the default)", e)
	}
	change := func(scheme any) map[string]any {
		return map[string]any{"startDate": "2026-01-01", "relationshipType": "ordinary", "businessUnitId": *e.BusinessUnitId, "holidaySchemeId": scheme}
	}
	var named gen.StaffEmployment
	do(t, hr, http.MethodPut, employmentPath(e.Id), change(five.Id), http.StatusOK, &named, ifMatch(e.Revision))
	if named.HolidaySchemeId == nil || *named.HolidaySchemeId != five.Id {
		t.Errorf("named = %+v, want Five weeks", named)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'employment_updated'
		AND payload->'fields' ? 'holidaySchemeId' AND 'C' = ANY(payload_classes)`, person.Id); n != 1 {
		t.Errorf("employment_updated events naming holidaySchemeId (class C) = %d, want 1", n)
	}

	var archivedSix gen.StaffHolidayScheme
	do(t, hr, http.MethodPut, holidaySchemePath(six.Id), schemeChange(six, "archived", true), http.StatusOK, &archivedSix, ifMatch(six.Revision))
	refused(t, hr.Do(http.MethodPut, employmentPath(e.Id), change(six.Id), ifMatch(named.Revision)), http.StatusConflict, "structure_archived")
	refused(t, hr.Do(http.MethodPost, employmentsPath(person.Id), newEmployment("2027-01-01", terms(), "holidaySchemeId", six.Id)),
		http.StatusConflict, "structure_archived")
	p := refused(t, hr.Do(http.MethodPut, employmentPath(e.Id), change(six.Id+1000), ifMatch(named.Revision)), http.StatusBadRequest, "")
	if len(p.Errors["holidaySchemeId"]) == 0 {
		t.Errorf("errors = %v, want holidaySchemeId named", p.Errors)
	}

	// Archived under it, the employment keeps its scheme through a change.
	var five2 gen.StaffHolidayScheme
	for _, s := range schemesAs(t, h) {
		if s.Id == five.Id {
			five2 = s
		}
	}
	h.Exec(t, `UPDATE staff.holiday_schemes SET archived_at = now() WHERE id = $1`, five.Id)
	var kept gen.StaffEmployment
	do(t, hr, http.MethodPut, employmentPath(e.Id), change(five2.Id), http.StatusOK, &kept, ifMatch(named.Revision))
	if kept.HolidaySchemeId == nil || *kept.HolidaySchemeId != five.Id {
		t.Errorf("kept = %+v, want the archived scheme it already follows", kept)
	}

	viewer, _ := signIn(t, h, "staff:view")
	var seen map[string]any
	do(t, viewer, http.MethodGet, employmentPath(e.Id), nil, http.StatusOK, &seen)
	if _, ok := seen["holidaySchemeId"]; ok {
		t.Errorf("a colleague reads holidaySchemeId: %v", seen)
	}
}

// ---- The archive racing an employment write (plan pre-flight I9) ----------

// ownConn is a connection of its own on the installation's database, outside
// its pool: a raw lock-holder's or a pg_blocking_pids reader's (the race kit,
// internal/invoices/races_helpers_test.go).
func ownConn(t *testing.T, h *harness) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.Deps().Config.DatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// waiterOn waits until a backend other than known waits on a lock, and
// answers its pid and whom it waits on (pg_blocking_pids).
func waiterOn(t *testing.T, probe *pgx.Conn, known ...uint32) (uint32, []uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := probe.Query(context.Background(), `
			SELECT pid, pg_blocking_pids(pid) FROM pg_stat_activity
			WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0 ORDER BY pid`)
		if err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		type waiting struct {
			pid      uint32
			blockers []uint32
		}
		found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (waiting, error) {
			var w waiting
			return w, r.Scan(&w.pid, &w.blockers)
		})
		if err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		for _, w := range found {
			if !slices.Contains(known, w.pid) {
				return w.pid, w.blockers
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend besides %v came to wait on a lock within 10 s", known)
	return 0, nil
}

// deadlocksIn is how many deadlocks Postgres has detected in the database,
// read once the statistics have settled.
func deadlocksIn(t *testing.T, probe *pgx.Conn) int64 {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	ctx := context.Background()
	if _, err := probe.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
		t.Fatalf("clear the statistics snapshot: %v", err)
	}
	var n int64
	if err := probe.QueryRow(ctx, `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatalf("read the deadlocks: %v", err)
	}
	return n
}

// An archive racing an employment write that names the scheme (plan
// pre-flight I9), both ways, on a pool of two with the other side a raw
// transaction of its own doing what that side's write does.
//
// The employment write first: it holds the scheme FOR SHARE and has named it;
// the archive waits on the scheme row — pg_blocking_pids names the write —
// and, once it commits, finds an employment naming the scheme:
// holiday_scheme_in_use.
//
// The archive first: it holds the scheme FOR UPDATE and has archived it; the
// employment write waits on the scheme row and, once the archive commits,
// finds it archived: structure_archived — not an employment following an
// archived scheme, which a write reading the row without the lock would
// leave.
//
// Never 40P01, and pg_stat_database.deadlocks unchanged. Not parallel: a
// race test never is.
func TestHolidaySchemes_ArchiveRacesAnEmploymentWrite(t *testing.T) {
	h := newHarness(t, modtest.WithPoolMaxConns(2))
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	person := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	e := createEmployment(t, hr, person.Id, newEmployment("2026-01-01", terms()))
	probe := ownConn(t, h)
	before := deadlocksIn(t, probe)

	t.Run("the employment write first", func(t *testing.T) {
		five := addScheme(t, hr, "Five weeks", 30)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		writerConn := ownConn(t, h)
		writer, err := writerConn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the write: %v", err)
		}
		defer func() { _ = writer.Rollback(context.Background()) }()
		if _, err := writer.Exec(ctx, `SELECT id FROM staff.holiday_schemes WHERE id = $1 FOR SHARE`, five.Id); err != nil {
			t.Fatalf("the write's share lock: %v", err)
		}
		if _, err := writer.Exec(ctx, `UPDATE staff.employments SET holiday_scheme_id = $1 WHERE id = $2`, five.Id, e.Id); err != nil {
			t.Fatalf("the write: %v", err)
		}

		answered := make(chan *modtest.Response, 1)
		go func() {
			answered <- hr.Do(http.MethodPut, holidaySchemePath(five.Id), schemeChange(five, "archived", true), ifMatch(five.Revision), modtest.Context(ctx))
		}()
		_, blockers := waiterOn(t, probe)
		if want := []uint32{writerConn.PgConn().PID()}; !slices.Equal(blockers, want) {
			t.Fatalf("the archive waits on %v, want the write %v", blockers, want)
		}
		if err := writer.Commit(ctx); err != nil {
			t.Fatalf("commit the write: %v", err)
		}
		refused(t, <-answered, http.StatusConflict, "holiday_scheme_in_use")
	})

	t.Run("the archive first", func(t *testing.T) {
		six := addScheme(t, hr, "Six weeks", 36)
		current := modtest.One[int32](t, h.Harness, `SELECT revision FROM staff.employments WHERE id = $1`, e.Id)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		archiverConn := ownConn(t, h)
		archiver, err := archiverConn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the archive: %v", err)
		}
		defer func() { _ = archiver.Rollback(context.Background()) }()
		if _, err := archiver.Exec(ctx, `SELECT id FROM staff.holiday_schemes WHERE id = $1 FOR UPDATE`, six.Id); err != nil {
			t.Fatalf("the archive's lock: %v", err)
		}
		if _, err := archiver.Exec(ctx, `UPDATE staff.holiday_schemes SET archived_at = now() WHERE id = $1`, six.Id); err != nil {
			t.Fatalf("the archive: %v", err)
		}

		answered := make(chan *modtest.Response, 1)
		go func() {
			answered <- hr.Do(http.MethodPut, employmentPath(e.Id), map[string]any{
				"startDate": "2026-01-01", "relationshipType": "ordinary", "businessUnitId": *e.BusinessUnitId, "holidaySchemeId": six.Id,
			}, ifMatch(current), modtest.Context(ctx))
		}()
		_, blockers := waiterOn(t, probe)
		if want := []uint32{archiverConn.PgConn().PID()}; !slices.Equal(blockers, want) {
			t.Fatalf("the employment write waits on %v, want the archive %v", blockers, want)
		}
		if err := archiver.Commit(ctx); err != nil {
			t.Fatalf("commit the archive: %v", err)
		}
		refused(t, <-answered, http.StatusConflict, "structure_archived")
		if n := h.Count(t, `SELECT count(*) FROM staff.employments WHERE holiday_scheme_id = $1`, six.Id); n != 0 {
			t.Errorf("%d employments follow the archived scheme, want none", n)
		}
	})

	if after := deadlocksIn(t, probe); after != before {
		t.Errorf("deadlocks = %d, was %d: Postgres broke a deadlock", after, before)
	}
}
