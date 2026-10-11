package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const (
	statsPath     = "/api/v1/staff/stats"
	attentionPath = "/api/v1/staff/stats/attention"
)

func thresholdPath(key string) string { return "/api/v1/staff/thresholds/" + key + "/acknowledge" }

// works numbers every employment seedWork writes, so two of one person never
// share an employment number.
var works atomic.Int64

// work is one employment seedWork writes, with its one terms row dated the
// start. A field left empty is the column's NULL — or, for the type, the form
// and the percentage, a permanent full-time position.
type work struct {
	start, end          string
	kind, ground, form  string
	expectedEnd         string
	fte                 float64
	manager, department int64
	probationEnd        string
	probationExtendedTo string
	signed              string
}

// seedWork writes an employment for personID directly, in a business unit of
// its own, and answers its id: the dashboard's subject is what the rows say,
// whatever wrote them.
func seedWork(t *testing.T, h *harness, personID int64, w work) int64 {
	t.Helper()
	if w.kind == "" {
		w.kind = "fast"
	}
	if w.form == "" {
		w.form = w.kind
	}
	if w.fte == 0 {
		w.fte = 100
	}
	text := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	id := func(v int64) *int64 {
		if v == 0 {
			return nil
		}
		return &v
	}
	now := h.Now()
	unit := modtest.One[int64](t, h.Harness, `
		WITH le AS (
			INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at)
			VALUES ('Kraft-Verket AS', false, $1, $1) RETURNING id
		)
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		SELECT id, 'Oslo', false, $1, $1 FROM le RETURNING id`, now)
	employment := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date,
			end_date, end_reason, contract_signed_at, created_at, updated_at)
		VALUES ($1, $2, 'ordinary', $3, $4::date, $5::date, CASE WHEN $5::date IS NULL THEN NULL ELSE 'contract_ended' END,
			$6::date, $7, $7) RETURNING id`,
		personID, fmt.Sprintf("W-%d", works.Add(1)), unit, w.start, text(w.end), text(w.signed), now)
	h.Exec(t, `
		INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, temporary_ground, expected_end_date,
			appointment_form, fte_percent, full_time_hours_per_week, manager_person_id, department_id, probation_end_date,
			probation_extension_agreed, probation_extended_to, created_at, updated_at)
		VALUES ($1, $2::date, $3, $4, $5::date, $6, $7, 37.5, $8, $9, $10::date, $11::date IS NOT NULL, $11::date, $12, $12)`,
		employment, w.start, w.kind, text(w.ground), text(w.expectedEnd), w.form, w.fte, id(w.manager), id(w.department),
		text(w.probationEnd), text(w.probationExtendedTo), now)
	return employment
}

// seedDepartment writes a department managed by manager (0: nobody) under
// parent (0: at the top), and answers its id.
func seedDepartment(t *testing.T, h *harness, code string, manager, parent int64) int64 {
	t.Helper()
	var m, p *int64
	if manager != 0 {
		m = &manager
	}
	if parent != 0 {
		p = &parent
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.departments (code, name, manager_person_id, parent_id, created_at, updated_at)
		VALUES ($1, $1, $2, $3, $4, $4) RETURNING id`, code, m, p, h.Now())
}

// day is the installation's today moved by offset days, as a date.
func day(t *testing.T, h *harness, offset int) string {
	t.Helper()
	d, err := time.Parse(time.DateOnly, today(t, h))
	if err != nil {
		t.Fatal(err)
	}
	return d.AddDate(0, 0, offset).Format(time.DateOnly)
}

// months is date moved by n months; the tests' days are never past the 28th.
func months(t *testing.T, date string, n int) string {
	t.Helper()
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatal(err)
	}
	return d.AddDate(0, n, 0).Format(time.DateOnly)
}

func getStats(t *testing.T, c *modtest.Client) gen.StaffStats {
	t.Helper()
	var out gen.StaffStats
	do(t, c, http.MethodGet, statsPath, nil, http.StatusOK, &out)
	return out
}

func getAttention(t *testing.T, c *modtest.Client) []gen.StaffStatsAttentionItem {
	t.Helper()
	var out []gen.StaffStatsAttentionItem
	do(t, c, http.MethodGet, attentionPath, nil, http.StatusOK, &out)
	return out
}

// ofType is the items of one type.
func ofType(items []gen.StaffStatsAttentionItem, kind gen.StaffStatsAttentionItemType) []gen.StaffStatsAttentionItem {
	var out []gen.StaffStatsAttentionItem
	for _, i := range items {
		if i.Type == kind {
			out = append(out, i)
		}
	}
	return out
}

// entities is the items' entity ids, each with the day it occurred on.
func entities(items []gen.StaffStatsAttentionItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.EntityId+"@"+i.OccurredAt.UTC().Format(time.DateOnly))
	}
	slices.Sort(out)
	return out
}

func idString(id int64) string { return fmt.Sprint(id) }

// at is entity@date, as entities writes it.
func at(id any, date string) string { return fmt.Sprint(id) + "@" + date }

// The figures (design S20): who is employed today, the full-time equivalents
// in force today summed over every employment, who starts and who ends within
// thirty days, and the records that never got an employment — today in the
// settings' zone, each window's two ends counted as written.
func TestStats_TheFigures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	seedWork(t, h, seedPerson(t, h, "Active", "Full"), work{start: day(t, h, -100)})
	two := seedPerson(t, h, "Two", "Jobs")
	seedWork(t, h, two, work{start: day(t, h, -100), fte: 60})
	seedWork(t, h, two, work{start: day(t, h, -50), fte: 40})
	seedWork(t, h, seedPerson(t, h, "Ended", "Yesterday"), work{start: day(t, h, -300), end: day(t, h, -1)})
	seedWork(t, h, seedPerson(t, h, "Starts", "In30"), work{start: day(t, h, 30)})
	seedWork(t, h, seedPerson(t, h, "Starts", "In31"), work{start: day(t, h, 31)})
	seedWork(t, h, seedPerson(t, h, "Starts", "Today"), work{start: day(t, h, 0)})
	seedWork(t, h, seedPerson(t, h, "Ends", "In30"), work{start: day(t, h, -10), end: day(t, h, 30), fte: 50})
	seedWork(t, h, seedPerson(t, h, "Ends", "Today"), work{start: day(t, h, -10), end: day(t, h, 0), fte: 50})
	seedWork(t, h, seedPerson(t, h, "Ends", "In31"), work{start: day(t, h, -10), end: day(t, h, 31), fte: 25})
	seedPerson(t, h, "Never", "Employed")
	erased := seedPerson(t, h, "Erased", "Long-ago")
	seedWork(t, h, erased, work{start: "2015-01-01", end: "2016-01-01"})
	erasedBare := seedPerson(t, h, "Erased", "Bare")
	h.Exec(t, `UPDATE staff.people SET erased_at = $1 WHERE id = ANY($2)`, h.Now(), []int64{erased, erasedBare})

	got := getStats(t, hr)
	want := gen.StaffStats{Headcount: 6, FteSum: 4.25, StartingSoon: 1, EndingSoon: 2, PeopleWithoutEmployment: 1}
	if got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

// The figures are the directory's and HR's (the pre-flight's answer 6):
// staff:view or staff:manage, and the uniform 403 for anyone else.
func TestStats_ViewOrManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, perms := range [][]string{{}, {"staff:approve"}, {"staff:personal-data"}} {
		c, _ := signIn(t, h, perms...)
		if r := c.Do(http.MethodGet, statsPath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d body %s, want 403", perms, r.Status, r.Body)
		}
	}
	for _, perm := range []string{"staff:view", "staff:manage"} {
		c, _ := signIn(t, h, perm)
		getStats(t, c)
	}
}

// A contract is overdue (design S20, R §4.1) seven days after the start —
// from the start itself for an employment under a month or a hired-out one —
// while neither a typed signing day nor a signed contract document filed for
// the employment says it was signed; an ended employment never is. HR's item,
// one per employment, the employment number in the title when the person has
// more than one.
func TestAttention_ContractOverdue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view")

	seven := seedPerson(t, h, "Seven", "Days")
	seedWork(t, h, seven, work{start: day(t, h, -7)})
	seedWork(t, h, seedPerson(t, h, "Six", "Days"), work{start: day(t, h, -6)})
	short := seedPerson(t, h, "Under", "Amonth")
	seedWork(t, h, short, work{start: day(t, h, -2), kind: "midlertidig", ground: "a", expectedEnd: day(t, h, 18)})
	long := seedPerson(t, h, "Over", "Amonth")
	seedWork(t, h, long, work{start: day(t, h, -2), kind: "midlertidig", ground: "a", expectedEnd: day(t, h, 200)})
	hired := seedPerson(t, h, "Hired", "Out")
	seedWork(t, h, hired, work{start: day(t, h, -1), form: "fast_utleid"})
	document := seedPerson(t, h, "Signed", "Document")
	documented := seedWork(t, h, document, work{start: day(t, h, -30)})
	doc := seedDocument(t, h, document, "contract")
	h.Exec(t, `UPDATE staff.documents SET employment_id = $1, signed_at = $2::date WHERE id = $3`, documented, day(t, h, -29), doc)
	unattached := seedPerson(t, h, "Unsigned", "Document")
	seedWork(t, h, unattached, work{start: day(t, h, -30)})
	unsigned := seedDocument(t, h, unattached, "contract")
	h.Exec(t, `UPDATE staff.documents SET employment_id = (SELECT id FROM staff.employments WHERE person_id = $1) WHERE id = $2`, unattached, unsigned)
	seedWork(t, h, seedPerson(t, h, "Typed", "Date"), work{start: day(t, h, -30), signed: day(t, h, -30)})
	seedWork(t, h, seedPerson(t, h, "Ended", "Unsigned"), work{start: day(t, h, -60), end: day(t, h, -1)})
	future := seedPerson(t, h, "Future", "Start")
	seedWork(t, h, future, work{start: day(t, h, 3), form: "fast_utleid"})
	twice := seedPerson(t, h, "Two", "Contracts")
	first := seedWork(t, h, twice, work{start: day(t, h, -20), fte: 50})
	seedWork(t, h, twice, work{start: day(t, h, -20), fte: 50, signed: day(t, h, -20)})

	items := ofType(getAttention(t, hr), gen.ContractOverdue)
	// "Over a month" falls overdue five days from now: not yet.
	want := sorted(
		at(seven, day(t, h, 0)),
		at(short, day(t, h, -2)),
		at(hired, day(t, h, -1)),
		at(unattached, day(t, h, -23)),
		at(twice, day(t, h, -13)),
	)
	if got := entities(items); !slices.Equal(got, want) {
		t.Fatalf("contractOverdue = %v, want %v", got, want)
	}
	for _, i := range items {
		number := modtest.One[string](t, h.Harness, `SELECT employment_number FROM staff.employments WHERE id = $1`, first)
		switch i.EntityId {
		case idString(twice):
			if i.Title != "Two Contracts ("+number+")" || i.Id != "contractOverdue/"+idString(first) {
				t.Errorf("two employments' item = %+v, want the number in the title", i)
			}
		case idString(seven):
			if i.Title != "Seven Days" {
				t.Errorf("title = %q, want the person's name", i.Title)
			}
		}
	}
	if got := ofType(getAttention(t, viewer), gen.ContractOverdue); len(got) != 0 {
		t.Errorf("staff:view sees %v, want nothing", got)
	}
}

// A probation ending within thirty days (design S20) is HR's item and the
// person's manager's today, the extension's end when there is one; a past one
// or one further off is no item, and a colleague sees nothing.
func TestAttention_ProbationEnding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	both, bothUser := signIn(t, h, "staff:manage")
	boss, bossUser := signIn(t, h)
	colleague, colleagueUser := signIn(t, h, "staff:view")

	manager := seedPerson(t, h, "Mona", "Manager")
	seedWork(t, h, manager, work{start: "2020-01-01"})
	link(t, h, manager, bossUser)
	other := seedPerson(t, h, "Olav", "Other")
	seedWork(t, h, other, work{start: "2020-01-01"})
	link(t, h, other, bothUser)
	seedWork(t, h, seedPerson(t, h, "Colle", "Ague"), work{start: "2020-01-01"})
	link(t, h, modtest.One[int64](t, h.Harness, `SELECT id FROM staff.people WHERE given_name = 'Colle'`), colleagueUser)

	in30 := seedPerson(t, h, "Ends", "In30")
	seedWork(t, h, in30, work{start: day(t, h, -150), manager: manager, probationEnd: day(t, h, 30)})
	in31 := seedPerson(t, h, "Ends", "In31")
	seedWork(t, h, in31, work{start: day(t, h, -149), manager: manager, probationEnd: day(t, h, 31)})
	notMine := seedPerson(t, h, "Not", "Mine")
	seedWork(t, h, notMine, work{start: day(t, h, -100), manager: other, probationEnd: day(t, h, 5)})
	past := seedPerson(t, h, "Ended", "Yesterday")
	seedWork(t, h, past, work{start: day(t, h, -100), manager: manager, probationEnd: day(t, h, -1)})
	extended := seedPerson(t, h, "Extended", "Probation")
	seedWork(t, h, extended, work{start: day(t, h, -170), manager: manager, probationEnd: day(t, h, -10), probationExtendedTo: day(t, h, 10)})
	endsToday := seedPerson(t, h, "Ends", "Today")
	seedWork(t, h, endsToday, work{start: day(t, h, -100), manager: other, probationEnd: day(t, h, 0)})

	if got, want := entities(ofType(getAttention(t, hr), gen.ProbationEnding)), sorted(
		at(in30, day(t, h, 30)), at(notMine, day(t, h, 5)), at(extended, day(t, h, 10)), at(endsToday, day(t, h, 0)),
	); !slices.Equal(got, want) {
		t.Errorf("staff:manage = %v, want %v", got, want)
	}
	if got, want := entities(ofType(getAttention(t, boss), gen.ProbationEnding)), sorted(
		at(in30, day(t, h, 30)), at(extended, day(t, h, 10)),
	); !slices.Equal(got, want) {
		t.Errorf("the manager = %v, want only their reports' %v", got, want)
	}
	// HR who also manages two of the people sees each once.
	if got, want := entities(ofType(getAttention(t, both), gen.ProbationEnding)), sorted(
		at(in30, day(t, h, 30)), at(notMine, day(t, h, 5)), at(extended, day(t, h, 10)), at(endsToday, day(t, h, 0)),
	); !slices.Equal(got, want) {
		t.Errorf("staff:manage and a manager = %v, want %v", got, want)
	}
	if got := getAttention(t, colleague); len(got) != 0 {
		t.Errorf("a colleague sees %v, want nothing", got)
	}
}

func sorted(s ...string) []string {
	slices.Sort(s)
	return s
}

// A temporary employment that has lasted twelve months is owed a month's
// notice of its end (design S20, R §4.2): from the day the twelve months
// pass, while it lasts and is temporary.
func TestAttention_TemporaryNoticeDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	twelve := seedPerson(t, h, "Twelve", "Months")
	seedWork(t, h, twelve, work{start: months(t, day(t, h, 0), -12), kind: "midlertidig", ground: "c", signed: "2025-09-12"})
	seedWork(t, h, seedPerson(t, h, "Not", "Quite"), work{start: months(t, day(t, h, 1), -12), kind: "midlertidig", ground: "c", signed: "2025-09-13"})
	seedWork(t, h, seedPerson(t, h, "Permanent", "Two-years"), work{start: months(t, day(t, h, 0), -24), signed: "2024-09-12"})
	seedWork(t, h, seedPerson(t, h, "Ended", "Temporary"), work{start: months(t, day(t, h, 0), -24), end: day(t, h, -1), kind: "midlertidig", ground: "c", signed: "2024-09-12"})

	if got, want := entities(ofType(getAttention(t, hr), gen.TemporaryNoticeDue)), []string{at(twelve, day(t, h, 0))}; !slices.Equal(got, want) {
		t.Errorf("temporaryNoticeDue = %v, want %v", got, want)
	}
}

// A person's unbroken run of temporary employments on ground a or b converts
// after thirty-six months (design S20, R §4.2): each employment starting no
// later than the day after the one before ended, the latest in force today.
// Ground c is no part of a run, and a gap breaks one.
func TestAttention_TemporaryConverts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	temp := func(person int64, ground, start, end string) {
		seedWork(t, h, person, work{start: start, end: end, kind: "midlertidig", ground: ground, signed: start})
	}

	chain := seedPerson(t, h, "Chain", "AB")
	temp(chain, "a", "2023-06-01", "2024-12-31")
	temp(chain, "b", "2025-01-01", "")
	single := seedPerson(t, h, "Single", "A")
	temp(single, "a", "2023-01-01", "")
	groundC := seedPerson(t, h, "Ground", "C")
	temp(groundC, "a", "2023-01-01", "2024-12-31")
	temp(groundC, "c", "2025-01-01", "")
	startsC := seedPerson(t, h, "Starts", "C")
	temp(startsC, "c", "2023-01-01", "2024-12-31")
	temp(startsC, "a", "2025-01-01", "")
	gap := seedPerson(t, h, "With", "Gap")
	temp(gap, "a", "2023-01-01", "2024-11-30")
	temp(gap, "a", "2024-12-15", "")
	ended := seedPerson(t, h, "Run", "Ended")
	temp(ended, "a", "2022-01-01", "2026-01-01")
	short := seedPerson(t, h, "Under", "Three")
	temp(short, "b", "2024-01-01", "")

	if got, want := entities(ofType(getAttention(t, hr), gen.TemporaryConverts)), sorted(
		at(chain, "2026-06-01"), at(single, "2026-01-01"),
	); !slices.Equal(got, want) {
		t.Errorf("temporaryConverts = %v, want %v", got, want)
	}
}

// A person in employment today with no manager in employment today — none
// named, the one named gone, and none in the department or above — is
// listed (design §9); the department's manager covers its people, and the
// head of a top-level department, whom nobody manages, is not listed.
func TestAttention_ReportsWithoutManager(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	head := seedPerson(t, h, "Head", "Ofcompany")
	top := seedDepartment(t, h, "TOP", head, 0)
	seedWork(t, h, head, work{start: "2020-01-01", department: top, signed: "2020-01-01"})
	sub := seedDepartment(t, h, "SUB", 0, top)
	covered := seedPerson(t, h, "Covered", "Bydepartment")
	seedWork(t, h, covered, work{start: "2021-01-01", department: sub, signed: "2021-01-01"})
	named := seedPerson(t, h, "Named", "Manager")
	seedWork(t, h, named, work{start: "2021-01-01", manager: head, signed: "2021-01-01"})
	alone := seedPerson(t, h, "Nobody", "Above")
	seedWork(t, h, alone, work{start: "2022-03-01", signed: "2022-03-01"})
	gone := seedPerson(t, h, "Gone", "Boss")
	seedWork(t, h, gone, work{start: "2019-01-01", end: "2026-06-30", signed: "2019-01-01"})
	orphan := seedPerson(t, h, "Boss", "Left")
	seedWork(t, h, orphan, work{start: "2023-02-01", manager: gone, signed: "2023-02-01"})
	leftDept := seedDepartment(t, h, "LEFT", gone, 0)
	inLeft := seedPerson(t, h, "Department", "Headless")
	seedWork(t, h, inLeft, work{start: "2024-05-01", department: leftDept, signed: "2024-05-01"})

	if got, want := entities(ofType(getAttention(t, hr), gen.ReportsWithoutManager)), sorted(
		at(alone, "2022-03-01"), at(orphan, "2023-02-01"), at(inLeft, "2024-05-01"),
	); !slices.Equal(got, want) {
		t.Errorf("reportsWithoutManager = %v, want %v", got, want)
	}
}

// The reassessment of an erased person falls due twelve months after their
// last employment ended (design S15) and is staff:personal-data's item until
// it is recorded.
func TestAttention_ReassessmentDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	pd, _ := signIn(t, h, "staff:personal-data")
	hr, _ := signIn(t, h, "staff:manage")

	due := seedPerson(t, h, "Due", "Now")
	seedWork(t, h, due, work{start: "2015-01-01", end: months(t, day(t, h, -1), -12)})
	early := seedPerson(t, h, "Not", "Yet")
	seedWork(t, h, early, work{start: "2015-01-01", end: months(t, day(t, h, 0), -11)})
	done := seedPerson(t, h, "Already", "Reassessed")
	seedWork(t, h, done, work{start: "2015-01-01", end: "2020-01-01"})
	live := seedPerson(t, h, "Not", "Erased")
	seedWork(t, h, live, work{start: "2015-01-01", end: "2020-01-01"})
	h.Exec(t, `UPDATE staff.people SET erased_at = $1 WHERE id = ANY($2)`, h.Now(), []int64{due, early, done})
	h.Exec(t, `UPDATE staff.people SET reassessed_at = $1 WHERE id = $2`, h.Now(), done)

	items := ofType(getAttention(t, pd), gen.ReassessmentDue)
	if got, want := entities(items), []string{at(due, day(t, h, -1))}; !slices.Equal(got, want) {
		t.Fatalf("reassessmentDue = %v, want %v", got, want)
	}
	if items[0].Title != "Due Now" {
		t.Errorf("title = %q, want the name, which survives the erase", items[0].Title)
	}
	if got := ofType(getAttention(t, hr), gen.ReassessmentDue); len(got) != 0 {
		t.Errorf("staff:manage sees %v, want nothing", got)
	}
}

// The deletion ledger failing (design S20, the A2 pre-flight) is one item for
// HR while rows the store has refused have waited more than a day: a fresh
// failure, or a row never yet tried, does not count.
func TestAttention_RetentionFailing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	now := h.Now()
	row := func(key string, attempts int, age time.Duration) {
		h.Exec(t, `INSERT INTO staff.object_deletions (object_key, attempts, next_attempt_at, created_at) VALUES ($1, $2, $3, $4)`,
			key, attempts, now.Add(time.Hour), now.Add(-age))
	}
	row("people/1/fresh", 3, time.Hour)
	row("people/1/untried", 0, 48*time.Hour)
	if got := ofType(getAttention(t, hr), gen.RetentionFailing); len(got) != 0 {
		t.Fatalf("retentionFailing = %v, want nothing yet", got)
	}
	row("people/1/old", 1, 25*time.Hour)
	row("people/1/older", 7, 30*time.Hour)

	items := ofType(getAttention(t, hr), gen.RetentionFailing)
	if len(items) != 1 {
		t.Fatalf("retentionFailing = %v, want one item", items)
	}
	i := items[0]
	if i.EntityId != "retention" || i.Count == nil || *i.Count != 2 || !i.OccurredAt.Equal(now.Add(-30*time.Hour)) {
		t.Errorf("item = %+v (count %v), want entityId retention, count 2 and the oldest failing row's queueing", i, i.Count)
	}
	if got := ofType(getAttention(t, pd), gen.RetentionFailing); len(got) != 0 {
		t.Errorf("staff:personal-data alone sees %v, want nothing", got)
	}
}

// The OTP test without an owner flag (plan reading 17, R §4.8): over the
// percentages in force today summed per person, someone at 75 % or more, or
// the people at 20 % or more adding up to two full-time equivalents; the item
// names the owner exception, and an acknowledged otp threshold is not shown.
func TestAttention_OTPThreshold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		ftes  [][]float64 // per person, per employment
		shown bool
	}{
		{"one person at 75 %", [][]float64{{75}}, true},
		{"one person at 70 %", [][]float64{{70}}, false},
		{"one person at 40 % twice", [][]float64{{40, 40}}, true},
		{"two people at 80 %", [][]float64{{80}, {80}}, true},
		{"four people at 50 %", [][]float64{{50}, {50}, {50}, {50}}, true},
		{"three at 60 % and one at 19 %", [][]float64{{60}, {60}, {60}, {19}}, false},
		{"nobody", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			hr, _ := signIn(t, h, "staff:manage")
			for i, person := range tc.ftes {
				p := seedPerson(t, h, "Person", fmt.Sprint(i))
				for _, fte := range person {
					seedWork(t, h, p, work{start: "2025-01-01", fte: fte, signed: "2025-01-01"})
				}
			}
			items := ofType(getAttention(t, hr), gen.OtpThreshold)
			if !tc.shown {
				if len(items) != 0 {
					t.Errorf("otpThreshold = %v, want none", items)
				}
				return
			}
			if len(items) != 1 {
				t.Fatalf("otpThreshold = %v, want one", items)
			}
			i := items[0]
			if i.EntityId != "thresholds" || i.Id != "otpThreshold" || i.OccurredAt.UTC().Format(time.DateOnly) != today(t, h) {
				t.Errorf("item = %+v, want entityId thresholds and today", i)
			}
			if !strings.Contains(i.Title, "unless the only such person is the owner") {
				t.Errorf("title = %q, want the owner exception named", i.Title)
			}
			do(t, hr, http.MethodPost, thresholdPath("otp"), nil, http.StatusOK, nil)
			if got := ofType(getAttention(t, hr), gen.OtpThreshold); len(got) != 0 {
				t.Errorf("after the acknowledgement: %v, want none", got)
			}
		})
	}
}

// seedHeadcount writes n people, each employed full time from 2025.
func seedHeadcount(t *testing.T, h *harness, n int) {
	t.Helper()
	for i := range n {
		seedWork(t, h, seedPerson(t, h, "Headcount", fmt.Sprint(i)), work{start: "2025-01-01", signed: "2025-01-01"})
	}
}

// A headcount threshold (design S20, R §4.10) is one item per threshold the
// headcount has reached and not acknowledged, its count the headcount; an
// acknowledged one is never shown again.
func TestAttention_HeadcountThreshold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view")
	thresholds := func() []string {
		var out []string
		for _, i := range ofType(getAttention(t, hr), gen.HeadcountThreshold) {
			if i.Count == nil || i.Id != "headcountThreshold/"+i.EntityId || i.OccurredAt.UTC().Format(time.DateOnly) != today(t, h) {
				t.Errorf("item = %+v, want the headcount counted, today", i)
				continue
			}
			out = append(out, fmt.Sprintf("%s:%d", i.EntityId, *i.Count))
		}
		slices.Sort(out)
		return out
	}

	seedHeadcount(t, h, 4)
	if got := thresholds(); len(got) != 0 {
		t.Fatalf("four people: %v, want none", got)
	}
	seedHeadcount(t, h, 1)
	if got, want := thresholds(), []string{"5:5"}; !slices.Equal(got, want) {
		t.Fatalf("five people: %v, want %v", got, want)
	}
	for _, i := range ofType(getAttention(t, hr), gen.HeadcountThreshold) {
		if !strings.Contains(i.Title, "verneombud") || !strings.Contains(i.Title, "varslingsrutine") {
			t.Errorf("title = %q, want the duties named", i.Title)
		}
	}
	do(t, hr, http.MethodPost, thresholdPath("5"), nil, http.StatusOK, nil)
	if got := thresholds(); len(got) != 0 {
		t.Fatalf("five people, acknowledged: %v, want none", got)
	}
	seedHeadcount(t, h, 45)
	if got, want := thresholds(), []string{"30:50", "50:50"}; !slices.Equal(got, want) {
		t.Fatalf("fifty people: %v, want %v", got, want)
	}
	do(t, hr, http.MethodPost, thresholdPath("30"), nil, http.StatusOK, nil)
	if got, want := thresholds(), []string{"50:50"}; !slices.Equal(got, want) {
		t.Errorf("fifty people, 30 acknowledged: %v, want %v", got, want)
	}
	if got := getAttention(t, viewer); len(got) != 0 {
		t.Errorf("staff:view sees %v, want nothing", got)
	}
}

// Attention is shaped per caller and never refused: a caller with none of the
// roles gets an empty list — a JSON array, not null — and every list is
// oldest first, then by type and id.
func TestAttention_EmptyForNoRoleAndSorted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	everyone, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	nobody, _ := signIn(t, h)
	approver, _ := signIn(t, h, "staff:approve", "staff:view")

	seedHeadcount(t, h, 5)
	seedWork(t, h, seedPerson(t, h, "Late", "Contract"), work{start: day(t, h, -40)})
	erased := seedPerson(t, h, "Erased", "Due")
	seedWork(t, h, erased, work{start: "2010-01-01", end: "2015-01-01"})
	h.Exec(t, `UPDATE staff.people SET erased_at = $1 WHERE id = $2`, h.Now(), erased)

	for _, c := range []*modtest.Client{nobody, approver} {
		r := c.Do(http.MethodGet, attentionPath, nil)
		if r.Status != http.StatusOK || strings.TrimSpace(string(r.Body)) != "[]" {
			t.Errorf("status %d body %s, want 200 []", r.Status, r.Body)
		}
	}
	items := getAttention(t, everyone)
	if len(items) < 4 {
		t.Fatalf("items = %v, want the contract, the reassessment, the threshold and the managers", items)
	}
	ids := map[string]bool{}
	for n, i := range items {
		if ids[i.Id] {
			t.Errorf("item %s twice", i.Id)
		}
		ids[i.Id] = true
		if n == 0 {
			continue
		}
		prev := items[n-1]
		if c := prev.OccurredAt.Compare(i.OccurredAt); c > 0 ||
			c == 0 && (prev.Type > i.Type || prev.Type == i.Type && prev.Id > i.Id) {
			t.Errorf("%s before %s: not oldest first, then by type and id", prev.Id, i.Id)
		}
	}
}

// A threshold is acknowledged once and permanently (plan reading 18): the
// settings record when, a threshold_acknowledged event of the installation
// names the caller and the key, and a second acknowledgement changes nothing
// and records nothing. Any key but the four is threshold_unknown, and the
// acknowledgement is staff:manage's.
func TestThresholds_AcknowledgedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrUser := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")

	var before gen.StaffSettings
	do(t, hr, http.MethodGet, settingsPath, nil, http.StatusOK, &before)
	if got := raw(t, hr.Do(http.MethodGet, settingsPath, nil))["thresholdsAcknowledged"]; fmt.Sprint(got) != "map[]" {
		t.Errorf("a fresh installation's thresholdsAcknowledged = %v, want {}", got)
	}

	var acked gen.StaffThresholdsAcknowledged
	do(t, hr, http.MethodPost, thresholdPath("5"), nil, http.StatusOK, &acked)
	first := h.Now()
	if acked.N5 == nil || !acked.N5.Equal(first) || acked.N30 != nil || acked.N50 != nil || acked.Otp != nil {
		t.Fatalf("acknowledged = %+v, want 5 alone, now", acked)
	}
	events := func() int {
		return h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'threshold_acknowledged'`)
	}
	if events() != 1 {
		t.Fatalf("%d events, want one", events())
	}
	var personID *int64
	var actorKind, actorUser, payload string
	if err := h.Pool().QueryRow(t.Context(), `
		SELECT person_id, actor_kind, actor_user_id::text, payload::text FROM staff.events WHERE kind = 'threshold_acknowledged'`).
		Scan(&personID, &actorKind, &actorUser, &payload); err != nil {
		t.Fatal(err)
	}
	if personID != nil || actorKind != "user" || actorUser != hrUser.String() || payload != `{"key": "5"}` {
		t.Errorf("event = person %v, %s %s, %s; want the installation's, the caller, {key: 5}", personID, actorKind, actorUser, payload)
	}

	h.Advance(time.Hour)
	do(t, hr, http.MethodPost, thresholdPath("5"), nil, http.StatusOK, &acked)
	if acked.N5 == nil || !acked.N5.Equal(first) {
		t.Errorf("acknowledged again = %+v, want the first acknowledgement kept", acked)
	}
	if events() != 1 {
		t.Errorf("%d events after acknowledging again, want still one", events())
	}
	for _, key := range []string{"30", "50", "otp"} {
		do(t, hr, http.MethodPost, thresholdPath(key), nil, http.StatusOK, &acked)
	}
	if events() != 4 {
		t.Errorf("%d events, want four", events())
	}
	for _, key := range []string{"7", "OTP", "05", "unknown"} {
		refused(t, hr.Do(http.MethodPost, thresholdPath(key), nil), http.StatusNotFound, "threshold_unknown")
	}
	if r := viewer.Do(http.MethodPost, thresholdPath("50"), nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:view: status %d, want 403", r.Status)
	}

	var after gen.StaffSettings
	do(t, hr, http.MethodGet, settingsPath, nil, http.StatusOK, &after)
	got := after.ThresholdsAcknowledged
	second := first.Add(time.Hour)
	if got.N5 == nil || !got.N5.Equal(first) || got.N30 == nil || !got.N30.Equal(second) ||
		got.N50 == nil || !got.N50.Equal(second) || got.Otp == nil || !got.Otp.Equal(second) {
		t.Errorf("settings' thresholdsAcknowledged = %+v, want the four days", got)
	}
	if after.Revision != before.Revision {
		t.Errorf("revision %d → %d, want the settings' revision left alone", before.Revision, after.Revision)
	}
}
