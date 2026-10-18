package staff_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const retentionRunPath = "/api/v1/staff/retention/run"

// runCycle runs one erase cycle of the retention worker over the harness's
// installation — the worker as the runner starts it, the installation as the
// actor — and fails the test unless it ran.
func runCycle(t *testing.T, h *harness) {
	t.Helper()
	ran, err := staff.NewRetentionWorker(h.Deps()).RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
}

// runLedger makes one pass over the deletion ledger and answers what it
// attempted and what the store confirmed.
func runLedger(t *testing.T, h *harness) (int, int) {
	t.Helper()
	attempted, succeeded, err := staff.NewRetentionWorker(h.Deps()).RunLedger(context.Background())
	if err != nil {
		t.Fatalf("RunLedger: %v", err)
	}
	return attempted, succeeded
}

// erasedEvents is a person's erased events' payloads, oldest first, read
// from the table.
func erasedEvents(t *testing.T, h *harness, personID int64) []map[string]any {
	t.Helper()
	raws := modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(payload::text ORDER BY id), '{}') FROM staff.events WHERE person_id = $1 AND kind = 'erased'`, personID)
	out := make([]map[string]any, 0, len(raws))
	for _, r := range raws {
		var m map[string]any
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			t.Fatalf("decode %s: %v", r, err)
		}
		out = append(out, m)
	}
	return out
}

// firstErased is a person's first erased event's payload; the test fails
// when there is none.
func firstErased(t *testing.T, h *harness, personID int64) map[string]any {
	t.Helper()
	events := erasedEvents(t, h, personID)
	if len(events) == 0 {
		t.Fatalf("person %d has no erased event", personID)
	}
	return events[0]
}

// kindCounts is an erased payload's kinds as kind → count.
func kindCounts(payload map[string]any) map[string]float64 {
	out := map[string]float64{}
	list, _ := payload["kinds"].([]any)
	for _, item := range list {
		k, _ := item.(map[string]any)
		kind, _ := k["kind"].(string)
		count, _ := k["count"].(float64)
		out[kind] = count
	}
	return out
}

// classed is the columns of table whose class is class, sorted.
func classed(table, class string) []string {
	var out []string
	for key, c := range staff.ColumnClasses() {
		if t, column, _ := strings.Cut(key, "."); t == table && c == class {
			out = append(out, column)
		}
	}
	slices.Sort(out)
	return out
}

// endEmployment ends an employment on end.
func endEmployment(t *testing.T, hr *modtest.Client, id int64, end string) {
	t.Helper()
	do(t, hr, http.MethodPost, endPath(id), map[string]any{"endDate": end, "endReason": "employee_resigned"}, http.StatusOK, nil)
}

// pastEmployment is an employment from start to end with first as its first
// terms row.
func pastEmployment(t *testing.T, hr *modtest.Client, personID int64, start, end string, first map[string]any) gen.StaffEmployment {
	t.Helper()
	e := createEmployment(t, hr, personID, newEmployment(start, first))
	endEmployment(t, hr, e.Id, end)
	return e
}

// payTerms is a terms row with every pay field and a note set.
func payTerms(kv ...any) map[string]any {
	return terms(append([]any{
		"remunerationType", "monthly",
		"payComponents", []any{map[string]any{"name": "Base", "amount": 50000, "currency": "NOK", "period": "monthly"}},
		"payMethod", "Bank transfer", "paydayText", "The 20th", "seniorityDate", "2015-01-01", "payGrade", "L42",
		"note", "Agreed at the interview",
	}, kv...)...)
}

// fullPerson is a person with every field of class D the API writes set —
// contact and address, both sealed values, HR's notes, a next of kin — the
// care-day fields and the foreign id's country written directly (delivery B
// exposes them), a role and a link.
func fullPerson(t *testing.T, h *harness, hr *modtest.Client, given string) gen.StaffPerson {
	t.Helper()
	p := createPerson(t, hr, with(newPerson(given, "Glemt"), "birthDate", "1990-01-01", "gender", "male",
		"workEmail", strings.ToLower(given)+"@example.no", "phoneWork", "+47 22 33 44 55", "phoneMobile", "+47 900 00 001",
		"phonePrivate", "+47 900 00 002", "privateEmail", strings.ToLower(given)+"@privat.no", "street", "Storgata 1",
		"postalCode", "0150", "city", "Oslo", "country", "NO", "notes", "Asked for a parking space"))
	do(t, hr, http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": testNationalID("018190", 347)}, http.StatusOK, nil)
	do(t, hr, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "bban", "value": "12345678903"}, http.StatusOK, nil)
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola Glemt", "spouse", "phone", "+47 900 00 003", "note", "Evenings")}})
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	h.Exec(t, `UPDATE staff.people SET children_under_12 = 2, sole_carer = true, chronically_ill_children = 1,
		national_id_country = 'NO' WHERE id = $1`, p.Id)
	return p
}

// The D erase (design S15, plan Task 5): a person whose every employment
// ended more than the grace period ago loses every column of class D — the
// map is walked, so a D column added later is asserted without a test
// changing — their next of kin, their terms' notes, their documents of class
// D (each queued in the ledger), and every value of class D in their
// history's payloads, which reads "changed" for good, to HR as to anyone; the
// person is marked erased and one erased event says what went. Everything of
// class C stays: the names, the employee number, the employments and their
// other terms, the roles, the link, the history's kinds. A second cycle finds
// nothing due and writes nothing.
func TestRetention_TheDEraseNullsEveryDColumn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data", "staff:view")
	company(t, hr)
	p := fullPerson(t, h, hr, "Erik")
	_, selfID := signIn(t, h)
	link(t, h, p.Id, selfID)
	first := pastEmployment(t, hr, p.Id, "2021-01-01", "2023-06-30", payTerms())
	addTerms(t, hr, first.Id, map[string]any{"validFrom": "2022-01-01", "ftePercent": 80, "note": "Went part-time"})
	second := pastEmployment(t, hr, p.Id, "2023-08-01", "2025-12-31", payTerms())
	contract := seedDocument(t, h, p.Id, "contract")
	other := seedDocument(t, h, p.Id, "other")
	otherKey, _ := storedDocument(t, h, other)
	certificate := seedDocument(t, h, p.Id, "certificate")
	certificateKey, _ := storedDocument(t, h, certificate)

	// Every column of class D holds a value before, so "null after" means
	// the erase nulled it.
	for _, column := range classed("people", "D") {
		if n := h.Count(t, fmt.Sprintf(`SELECT count(*) FROM staff.people WHERE id = $1 AND %s IS NOT NULL`, column), p.Id); n != 1 {
			t.Fatalf("people.%s is empty before the erase; the test must fill it", column)
		}
	}
	kindsBefore := modtest.One[[]string](t, h.Harness, `SELECT array_agg(kind ORDER BY id) FROM staff.events WHERE person_id = $1`, p.Id)
	termsBefore := h.Count(t, `SELECT count(*) FROM staff.employment_terms t JOIN staff.employments e ON e.id = t.employment_id WHERE e.person_id = $1`, p.Id)

	runCycle(t, h)

	for _, column := range classed("people", "D") {
		if n := h.Count(t, fmt.Sprintf(`SELECT count(*) FROM staff.people WHERE id = $1 AND %s IS NULL`, column), p.Id); n != 1 {
			t.Errorf("people.%s is not null after the D erase", column)
		}
	}
	for _, column := range classed("employment_terms", "D") {
		if n := h.Count(t, fmt.Sprintf(`
			SELECT count(*) FROM staff.employment_terms t JOIN staff.employments e ON e.id = t.employment_id
			WHERE e.person_id = $1 AND t.%s IS NOT NULL`, column), p.Id); n != 0 {
			t.Errorf("employment_terms.%s is set on %d rows after the D erase", column, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.next_of_kin WHERE person_id = $1`, p.Id); n != 0 {
		t.Errorf("next of kin = %d after the D erase, want none", n)
	}
	for _, d := range []struct {
		id   int64
		key  string
		kept bool
	}{{other, otherKey, false}, {certificate, certificateKey, false}, {contract, "", true}} {
		n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, d.id)
		if d.kept != (n == 1) {
			t.Errorf("document %d: %d rows, want kept %v", d.id, n, d.kept)
		}
		if !d.kept && h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, d.key) != 1 {
			t.Errorf("document %d's object is not queued in the ledger", d.id)
		}
	}
	if !h.objects.has(otherKey) {
		t.Error("the erase called the object store: the ledger finishes the objects, not the erase")
	}

	// The history: every value of class D replaced by "changed", in the
	// table and as HR reads it; the payload_classes kept.
	rows := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(payload::text ORDER BY id) FROM staff.events WHERE person_id = $1 AND 'D' = ANY(payload_classes)`, p.Id)
	if len(rows) < 3 {
		t.Fatalf("events touching class D = %d, want the creation, the next of kin, the terms with notes at least", len(rows))
	}
	dFields := staff.FieldsOfClass("D")
	for _, r := range rows {
		var payload struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal([]byte(r), &payload); err != nil {
			t.Fatalf("decode %s: %v", r, err)
		}
		for name, value := range payload.Fields {
			if slices.Contains(dFields, name) && value != "changed" {
				t.Errorf("stored payload %s: field %s = %v, want \"changed\"", r, name, value)
			}
		}
		if strings.Contains(r, "Storgata") || strings.Contains(r, "parking") || strings.Contains(r, "Ola Glemt") || strings.Contains(r, "interview") {
			t.Errorf("stored payload %s still carries a value of class D", r)
		}
	}
	for _, e := range listEvents(t, hr, p.Id) {
		if e.Kind == "person_created" {
			if f := fieldsOf(e); f["workEmail"] != "changed" || f["givenName"] == "changed" {
				t.Errorf("person_created as HR reads it = %v, want workEmail changed and the names kept", f)
			}
		}
	}

	// Marked erased, with one erased event naming the kinds.
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at = $2`, p.Id, h.Now()); n != 1 {
		t.Error("erased_at is not the cycle's instant")
	}
	erased := erasedEvents(t, h, p.Id)
	if len(erased) != 1 {
		t.Fatalf("erased events = %v, want one", erased)
	}
	if counts := kindCounts(erased[0]); len(counts) != 1 || counts["D"] < 1 {
		t.Errorf("erased kinds = %v, want D alone with a count", erased[0])
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'erased' AND actor_kind = 'system' AND payload_classes = '{}'`, p.Id); n != 1 {
		t.Error("the worker's erased event is not the installation's, without classes")
	}

	// Class C stays.
	got := getPerson(t, hr, p.Id)
	if got.GivenName != "Erik" || got.FamilyName != "Glemt" || got.EmployeeNumber != p.EmployeeNumber || got.BirthDate == nil || got.Gender == nil {
		t.Errorf("person after the erase = %+v, want the names, the number, the birth date and the gender kept", got)
	}
	if got.ErasedAt == nil {
		t.Error("the person's erasedAt is absent")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.employments WHERE person_id = $1`, p.Id); n != 2 {
		t.Errorf("employments = %d, want both kept", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.employment_terms t JOIN staff.employments e ON e.id = t.employment_id WHERE e.person_id = $1`, p.Id); n != termsBefore {
		t.Errorf("terms rows = %d, want %d kept", n, termsBefore)
	}
	if chain := listTerms(t, hr, second.Id); chain[0].RemunerationType == nil || chain[0].PayGrade == nil {
		t.Errorf("the pay of an employment five years from due = %+v, want kept (class A)", chain[0])
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.person_roles WHERE person_id = $1`, p.Id); n != 1 {
		t.Errorf("roles = %d, want kept", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, p.Id); n != 1 {
		t.Errorf("links = %d, want kept", n)
	}
	kindsAfter := modtest.One[[]string](t, h.Harness, `SELECT array_agg(kind ORDER BY id) FROM staff.events WHERE person_id = $1 AND kind <> 'erased'`, p.Id)
	if !slices.Equal(kindsAfter, kindsBefore) {
		t.Errorf("event kinds %v → %v, want every kind kept", kindsBefore, kindsAfter)
	}

	// Idempotent: nothing is due a second time, and nothing is written.
	events := h.Count(t, `SELECT count(*) FROM staff.events`)
	revision := personRevision(t, h, p.Id)
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.events`); n != events {
		t.Errorf("events %d → %d after a second cycle, want none written", events, n)
	}
	if got := personRevision(t, h, p.Id); got != revision {
		t.Errorf("revision %d → %d after a second cycle, want unchanged", revision, got)
	}
}

// When the D erase is due, and when it never is: before the grace period has
// run from the last end it waits, and runs the cycle after; a person with no
// employment, or one with an employment that has not ended, is never erased;
// a legal hold stops it until it is lifted.
func TestRetention_WhenTheDEraseIsDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	company(t, hr)
	// Today is 2026-09-12; the default grace is 90 days, so the last end
	// must be before 2026-06-14.
	recent := createPerson(t, hr, with(newPerson("Nylig", "Sluttet"), "notes", "x"))
	pastEmployment(t, hr, recent.Id, "2020-01-01", "2026-06-14", terms())
	nobody := createPerson(t, hr, with(newPerson("Aldri", "Ansatt"), "notes", "x"))
	current := createPerson(t, hr, with(newPerson("Fortsatt", "Ansatt"), "notes", "x"))
	pastEmployment(t, hr, current.Id, "2018-01-01", "2019-12-31", terms())
	createEmployment(t, hr, current.Id, newEmployment("2024-01-01", terms()))
	held := createPerson(t, hr, with(newPerson("Holdt", "Tilbake"), "notes", "x"))
	pastEmployment(t, hr, held.Id, "2020-01-01", "2024-12-31", terms())
	do(t, hr, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": true, "reason": "A claim"}, http.StatusOK, nil)

	runCycle(t, h)
	for _, id := range []int64{recent.Id, nobody.Id, current.Id, held.Id} {
		if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL AND notes IS NOT NULL`, id); n != 1 {
			t.Errorf("person %d was erased, want not due", id)
		}
		if got := erasedEvents(t, h, id); len(got) != 0 {
			t.Errorf("person %d has erased events %v, want none", id, got)
		}
	}

	// A day later the grace has run for the recent leaver; the hold still
	// stands, and lifted, the held person goes in the next cycle.
	setClock(t, h, h.Now().Add(24*time.Hour))
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL AND notes IS NULL`, recent.Id); n != 1 {
		t.Error("the recent leaver is not erased once the grace has run")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL`, held.Id); n != 1 {
		t.Error("the held person was erased")
	}
	hr, _ = signIn(t, h, "staff:manage", "staff:personal-data") // a day on, the session has lapsed
	do(t, hr, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": false}, http.StatusOK, nil)
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, held.Id); n != 1 {
		t.Error("the person whose hold was lifted is not erased")
	}
	for _, id := range []int64{nobody.Id, current.Id} {
		if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL`, id); n != 1 {
			t.Errorf("person %d was erased, want never", id)
		}
	}
}

// The health erase (plan reading 3): a person whose last employment ended
// more than healthRetentionDays ago loses their documents in restricted
// categories, each queued in the ledger, and the values of their health
// documents' events; a current employee's are kept, and a held person's. It
// runs on a person already erased — after the D erase, a cycle of its own,
// once its longer period has run.
func TestRetention_TheHealthErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	company(t, hr)

	gone := createPerson(t, hr, newPerson("Lenge", "Siden"))
	pastEmployment(t, hr, gone.Id, "2015-01-01", "2025-01-31", terms())
	health := uploadDocument(t, hr, gone.Id, pdfUpload(documentCategoryID(t, h, "health")))
	healthKey, _ := storedDocument(t, h, health.Id)

	working := createPerson(t, hr, newPerson("Jobber", "Fortsatt"))
	pastEmployment(t, hr, working.Id, "2015-01-01", "2020-01-31", terms())
	createEmployment(t, hr, working.Id, newEmployment("2021-01-01", terms()))
	workingHealth := seedDocument(t, h, working.Id, "health")

	held := createPerson(t, hr, newPerson("Holdt", "Tilbake"))
	pastEmployment(t, hr, held.Id, "2015-01-01", "2025-01-31", terms())
	heldHealth := seedDocument(t, h, held.Id, "health")
	do(t, hr, http.MethodPut, legalHoldPath(held.Id), map[string]any{"held": true, "reason": "A claim"}, http.StatusOK, nil)

	// Ended 2026-01-15: the D erase is due (grace 90 days), the health
	// erase not until 365 days have run.
	later := createPerson(t, hr, newPerson("Senere", "Sluttet"))
	pastEmployment(t, hr, later.Id, "2020-01-01", "2026-01-15", terms())
	laterHealth := seedDocument(t, h, later.Id, "health")

	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, health.Id); n != 0 {
		t.Error("the leaver's health document is kept past healthRetentionDays")
	}
	if h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, healthKey) != 1 {
		t.Error("the health document's object is not queued in the ledger")
	}
	// The leaver had nothing of class D but their history's names: the D
	// erase ran — they are erased — and removed nothing, so it counts none.
	if counts := kindCounts(firstErased(t, h, gone.Id)); counts["health"] != 2 || len(counts) != 2 {
		t.Errorf("the leaver's erased kinds = %v, want D and health: 2 (the document and its event)", counts)
	} else if _, ok := counts["D"]; !ok {
		t.Errorf("the leaver's erased kinds = %v, want D among them", counts)
	}
	stored := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(payload::text ORDER BY id) FROM staff.events WHERE person_id = $1 AND 'E' = ANY(payload_classes)`, gone.Id)
	for _, r := range stored {
		if strings.Contains(r, "avtale.pdf") || !strings.Contains(r, `"changed"`) {
			t.Errorf("health event payload %s, want its values replaced by \"changed\"", r)
		}
	}
	for _, id := range []int64{workingHealth, heldHealth, laterHealth} {
		if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, id); n != 1 {
			t.Errorf("health document %d was deleted, want kept", id)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, later.Id); n != 1 {
		t.Fatal("the later leaver is not erased by the D erase")
	}

	// A year on, the later leaver's health period has run too: the health
	// erase runs on a person already erased.
	setClock(t, h, time.Date(2027, time.January, 20, 12, 0, 0, 0, time.UTC))
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, laterHealth); n != 0 {
		t.Error("the erased person's health document is kept past healthRetentionDays")
	}
	erased := erasedEvents(t, h, later.Id)
	if len(erased) != 2 {
		t.Fatalf("the later leaver's erased events = %v, want D, then health", erased)
	}
	if counts := kindCounts(erased[1]); len(counts) != 1 || counts["health"] != 1 {
		t.Errorf("the second erase's kinds = %v, want health: 1", counts)
	}
}

// The A erase (plan reading 4), per employment, five years after the year it
// ended: the employment's pay columns nulled on every terms row, its
// documents of class A deleted and queued — filed against it, or against no
// employment while it is the person's last — and the pay in its events
// replaced by "changed". The boundary is the year: ended in 2021, it is due in
// 2027, not in 2026. A rehired person's old employment loses its pay while the
// current one keeps its own, and an employment of a person already erased is
// erased the same.
func TestRetention_TheAErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	company(t, hr)
	aColumns := classed("employment_terms", "A")
	payLive := func(employmentID int64) int {
		return h.Count(t, fmt.Sprintf(`SELECT coalesce(sum(num_nonnulls(%s)), 0) FROM staff.employment_terms WHERE employment_id = $1`,
			strings.Join(aColumns, ", ")), employmentID)
	}

	// Rehired: the old employment ended in 2019, the new one is current.
	back := createPerson(t, hr, newPerson("Kom", "Tilbake"))
	old := pastEmployment(t, hr, back.Id, "2015-01-01", "2019-12-31", payTerms())
	addTerms(t, hr, old.Id, map[string]any{"validFrom": "2017-01-01", "ftePercent": 100, "payGrade": "L44", "remunerationType": "hourly"})
	now := createEmployment(t, hr, back.Id, newEmployment("2024-01-01", payTerms()))
	oldContract := seedDocument(t, h, back.Id, "contract")
	h.Exec(t, `UPDATE staff.documents SET employment_id = $2 WHERE id = $1`, oldContract, old.Id)
	oldContractKey, _ := storedDocument(t, h, oldContract)
	looseContract := seedDocument(t, h, back.Id, "attest") // no employment: the last one's, which is current

	// Ended in 2020 and in 2021, one employment each.
	due := createPerson(t, hr, newPerson("Lenge", "Siden"))
	dueJob := pastEmployment(t, hr, due.Id, "2016-01-01", "2020-06-30", payTerms())
	dueLoose := seedDocument(t, h, due.Id, "contract") // no employment: the last one's, which is due
	boundary := createPerson(t, hr, newPerson("Akkurat", "Ikke"))
	boundaryJob := pastEmployment(t, hr, boundary.Id, "2016-01-01", "2021-03-31", payTerms())

	runCycle(t, h)

	if n := payLive(old.Id); n != 0 {
		t.Errorf("the old employment keeps %d pay values, want none", n)
	}
	if n := payLive(now.Id); n != len(aColumns) {
		t.Errorf("the current employment keeps %d pay values, want its %d", n, len(aColumns))
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, oldContract); n != 0 {
		t.Error("the old employment's contract is kept")
	}
	if h.Count(t, `SELECT count(*) FROM staff.object_deletions WHERE object_key = $1`, oldContractKey) != 1 {
		t.Error("the old contract's object is not queued in the ledger")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, looseContract); n != 1 {
		t.Error("an unattached attest of a person whose last employment is current was deleted")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL`, back.Id); n != 1 {
		t.Error("the rehired person was marked erased")
	}
	for _, r := range modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(payload::text ORDER BY id) FROM staff.events
		WHERE person_id = $1 AND kind LIKE 'terms_%' AND (payload ->> 'employmentId')::bigint = $2`, back.Id, old.Id) {
		if strings.Contains(r, "L42") || strings.Contains(r, "L44") || strings.Contains(r, "Bank transfer") {
			t.Errorf("the old employment's terms event %s keeps its pay", r)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind LIKE 'terms_%' AND (payload ->> 'employmentId')::bigint = $2 AND payload::text LIKE '%L42%'`, back.Id, now.Id); n != 1 {
		t.Error("the current employment's terms event lost its pay")
	}
	if counts := kindCounts(firstErased(t, h, back.Id)); len(counts) != 1 || counts["A"] < 1 {
		t.Errorf("the rehired person's erased kinds = %v, want A alone", counts)
	}

	if n := payLive(dueJob.Id); n != 0 {
		t.Errorf("the 2020 employment keeps %d pay values, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.documents WHERE id = $1`, dueLoose); n != 0 {
		t.Error("the unattached contract of a due last employment is kept")
	}
	if n := payLive(boundaryJob.Id); n != len(aColumns) {
		t.Errorf("the 2021 employment keeps %d pay values in 2026, want its %d", n, len(aColumns))
	}
	// Both leavers' D erase ran too; the boundary's employment waits for its
	// year on a person already erased.
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, boundary.Id); n != 1 {
		t.Fatal("the 2021 leaver is not erased by the D erase")
	}

	setClock(t, h, time.Date(2027, time.January, 2, 12, 0, 0, 0, time.UTC))
	runCycle(t, h)
	if n := payLive(boundaryJob.Id); n != 0 {
		t.Errorf("the 2021 employment keeps %d pay values in 2027, want none", n)
	}
	erased := erasedEvents(t, h, boundary.Id)
	if len(erased) != 2 {
		t.Fatalf("the 2021 leaver's erased events = %v, want D, then A", erased)
	}
	if counts := kindCounts(erased[1]); len(counts) != 1 || counts["A"] < 1 {
		t.Errorf("the second erase's kinds = %v, want A alone", counts)
	}
	if n := payLive(now.Id); n != len(aColumns) {
		t.Errorf("the current employment keeps %d pay values in 2027, want its %d", n, len(aColumns))
	}
}

// A contract or an attest deleted by hand before its employment's A erase
// leaves its events behind, of class A, and no document to say which
// employment it was filed against: the erase of the person's last employment
// replaces their values, and the A due query finds that employment due on
// them alone — here its terms carry no pay. A second cycle writes nothing.
func TestRetention_TheAEraseReachesAHandDeletedDocumentsEvents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	company(t, hr)
	p := createPerson(t, hr, newPerson("Lenge", "Siden"))
	pastEmployment(t, hr, p.Id, "2016-01-01", "2020-06-30", terms())
	contract := uploadDocument(t, hr, p.Id, pdfUpload(documentCategoryID(t, h, "contract")))
	do(t, hr, http.MethodDelete, documentPath(contract.Id), nil, http.StatusNoContent, nil)
	stored := func() []string {
		return modtest.One[[]string](t, h.Harness, `
			SELECT coalesce(array_agg(payload::text ORDER BY id), '{}') FROM staff.events
			WHERE person_id = $1 AND kind IN ('document_added', 'document_removed')`, p.Id)
	}
	if before := stored(); len(before) != 2 || !strings.Contains(before[0], "avtale.pdf") {
		t.Fatalf("the hand-deleted contract's events = %v, want its added and removed, naming the file", before)
	}

	runCycle(t, h)
	after := stored()
	for _, r := range after {
		if strings.Contains(r, "avtale.pdf") || !strings.Contains(r, `"changed"`) {
			t.Errorf("the hand-deleted contract's event %s, want its values replaced by \"changed\"", r)
		}
	}
	if counts := kindCounts(firstErased(t, h, p.Id)); counts["A"] != 2 {
		t.Errorf("erased kinds = %v, want A: 2 (the two payloads)", counts)
	}

	events := h.Count(t, `SELECT count(*) FROM staff.events`)
	runCycle(t, h)
	if n := h.Count(t, `SELECT count(*) FROM staff.events`); n != events || !slices.Equal(stored(), after) {
		t.Errorf("a second cycle wrote events (%d → %d) or payloads, want nothing", events, n)
	}
}

// The erased event per audience: it is one of the hold's kinds for the list
// (holdEventKinds) — left out of a manager's history and its count, shown to
// the person as its kind alone; its kinds are HR's, the health entry
// staff:personal-data's alone, so HR without it does not learn that health
// content existed.
func TestRetention_TheErasedEventPerAudience(t *testing.T) {
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

	var before gen.PaginatedResponseOfStaffEvent
	do(t, boss, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &before)
	h.Exec(t, `INSERT INTO staff.events (person_id, kind, actor_kind, at, payload, payload_classes) VALUES
		($1, 'erased', 'system', $2, '{"kinds":[{"kind":"D","count":3},{"kind":"health","count":2}]}', '{}')`, p.Id, h.Now())

	kindsOf := func(c *modtest.Client) []string {
		t.Helper()
		e := eventOfKind(t, listEvents(t, c, p.Id), "erased")
		var out []string
		list, _ := e.Payload["kinds"].([]any)
		for _, item := range list {
			k, _ := item.(map[string]any)
			out = append(out, fmt.Sprint(k["kind"]))
		}
		return out
	}
	if got := kindsOf(hr); !slices.Equal(got, []string{"D"}) {
		t.Errorf("staff:manage reads the erased kinds %v, want D alone", got)
	}
	if got := kindsOf(pd); !slices.Equal(got, []string{"D", "health"}) {
		t.Errorf("staff:personal-data reads the erased kinds %v, want D and health", got)
	}

	var managed gen.PaginatedResponseOfStaffEvent
	do(t, boss, http.MethodGet, eventsPath(p.Id), nil, http.StatusOK, &managed)
	if slices.Contains(kinds(managed.Data), "erased") {
		t.Error("the manager reads an erased event")
	}
	if managed.Pagination.TotalCount != before.Pagination.TotalCount || len(managed.Data) != len(before.Data) {
		t.Errorf("the manager's history counts %d (lists %d), want %d as before", managed.Pagination.TotalCount, len(managed.Data), before.Pagination.TotalCount)
	}

	var mine gen.PaginatedResponseOfStaffEvent
	do(t, self, http.MethodGet, meEventsPath, nil, http.StatusOK, &mine)
	for _, list := range [][]gen.StaffEvent{mine.Data, listEvents(t, self, p.Id)} {
		if e := eventOfKind(t, list, "erased"); len(e.Payload) != 0 {
			t.Errorf("the person reads the erased event as %v, want its kind alone", e.Payload)
		}
	}
}

// A cycle needs exactly one pooled connection, the one its lease holds (the
// customers worker's PR #124): every due query and every person's
// transaction runs on it. On a pool of one, a cycle that asked the pool for a
// second connection would wait for itself; the deadline turns that hang into
// this test's failure.
func TestRetention_TheCycleRunsOnTheLeasesConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	p := createPerson(t, hr, with(newPerson("Erik", "Glemt"), "notes", "x"))
	pastEmployment(t, hr, p.Id, "2015-01-01", "2020-12-31", payTerms())
	seedDocument(t, h, p.Id, "other")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	deps := h.Deps()
	deps.Pool = one
	if ran, err := staff.NewRetentionWorker(deps).RunCycle(ctx); err != nil || !ran {
		t.Fatalf("RunCycle on a pool of one = %v, %v; want true, nil", ran, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, p.Id); n != 1 {
		t.Error("the cycle on a pool of one erased nobody")
	}
	if counts := kindCounts(firstErased(t, h, p.Id)); counts["D"] < 1 || counts["A"] < 1 {
		t.Errorf("erased kinds = %v, want D and A", counts)
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held = %d, want the lease released", n)
	}
}

// POST /retention/run (staff:manage) runs the worker's two steps now: an
// erase cycle with the caller as the erased events' actor, and a pass over
// the ledger. It answers what was erased per kind, summed, and what the
// ledger attempted. The count is every item removed: each column value
// nulled, each row deleted, each payload rewritten. While another holder has
// the lease it is 409 retention_busy and runs nothing; HR without
// staff:manage is the uniform 403.
func TestRetention_RunByHand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signInNamed(t, h, "Hanne HR", "staff:manage")
	company(t, hr)
	// Two values of class D on the person (the work email and HR's notes), a
	// next of kin, a terms row's note, a document of class D, and three
	// events whose payloads hold class D values: the creation, the next of
	// kin and the first terms row.
	p := createPerson(t, hr, with(newPerson("Erik", "Glemt"), "workEmail", "erik@example.no", "notes", "x"))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{kin("Ola Glemt", "spouse")}})
	pastEmployment(t, hr, p.Id, "2022-01-01", "2024-12-31", terms("note", "y"))
	doc := seedDocument(t, h, p.Id, "other")
	key, _ := storedDocument(t, h, doc)

	view, _ := signIn(t, h, "staff:view", "staff:personal-data")
	if r := view.Do(http.MethodPost, retentionRunPath, nil); r.Status != http.StatusForbidden {
		t.Fatalf("run without staff:manage = %d, want 403", r.Status)
	}

	// Another holder of the lease: busy, and nothing runs.
	func() {
		conn, err := h.Pool().Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, staff.RetentionLeaseKey); err != nil {
			t.Fatalf("take the lease: %v", err)
		}
		defer func() {
			_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, staff.RetentionLeaseKey)
		}()
		r := hr.Do(http.MethodPost, retentionRunPath, nil)
		if r.Status != http.StatusConflict {
			t.Errorf("run while the lease is held = %d %s, want 409 retention_busy", r.Status, r.Body)
			return
		}
		refused(t, r, http.StatusConflict, "retention_busy")
		if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NULL`, p.Id); n != 1 {
			t.Error("a busy run erased the person")
		}
	}()

	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if len(run.Erased.Kinds) != 1 || run.Erased.Kinds[0].Kind != "D" || run.Erased.Kinds[0].Count != 8 {
		t.Errorf("erased = %+v, want D: 8 (2 values, a next of kin, a note, a document, 3 payloads)", run.Erased)
	}
	if run.Deletions.Attempted != 1 || run.Deletions.Succeeded != 1 {
		t.Errorf("deletions = %+v, want the document's object attempted and gone", run.Deletions)
	}
	if h.objects.has(key) || h.Count(t, `SELECT count(*) FROM staff.object_deletions`) != 0 {
		t.Error("the ledger pass left the object or its row")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'erased'
		AND actor_kind = 'user' AND actor_user_id = $2 AND actor_display = 'Hanne HR'`, p.Id, hrID); n != 1 {
		t.Error("the erased event of a run by hand does not name the caller")
	}
	// The erased event as HR reads it: the kinds, nothing else.
	for _, e := range listEvents(t, hr, p.Id) {
		if e.Kind == "erased" {
			if kinds, _ := e.Payload["kinds"].([]any); len(kinds) != 1 || len(e.Payload) != 1 {
				t.Errorf("erased event payload = %v, want the kinds alone", e.Payload)
			}
		}
	}

	// Nothing due: no kinds, and nothing in the ledger.
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if len(run.Erased.Kinds) != 0 || run.Deletions.Attempted != 0 {
		t.Errorf("a second run = %+v, want nothing", run)
	}
}

// GET /settings carries the two retention periods, editable within their
// bounds — a period left out of a replace keeps its value — and the
// retention block derived from the ledger and the history: the objects
// waiting, those the store has refused, the oldest queued and the last
// erase.
func TestRetention_TheSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	s := getSettings(t, hr)
	if s.RetentionGraceDays != 90 || s.HealthRetentionDays != 365 {
		t.Errorf("periods = %d/%d, want 90/365", s.RetentionGraceDays, s.HealthRetentionDays)
	}
	if s.Retention.PendingDeletions != 0 || s.Retention.FailingDeletions != 0 || s.Retention.OldestPendingSince != nil || s.Retention.LastErasedAt != nil {
		t.Errorf("retention = %+v, want an empty ledger and no erase", s.Retention)
	}

	for _, bad := range []map[string]any{
		{"retentionGraceDays": -1}, {"retentionGraceDays": 3651},
		{"healthRetentionDays": 29}, {"healthRetentionDays": 3651},
	} {
		p := refused(t, hr.Do(http.MethodPut, settingsPath, settingsBody(bad), ifMatch(s.Revision)), http.StatusBadRequest, "")
		for field := range bad {
			if len(p.Errors[field]) == 0 {
				t.Errorf("%v: errors = %v, want one on %s", bad, p.Errors, field)
			}
		}
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"retentionGraceDays": 0, "healthRetentionDays": 30}),
		http.StatusOK, &s, ifMatch(s.Revision))
	if s.RetentionGraceDays != 0 || s.HealthRetentionDays != 30 {
		t.Errorf("periods = %d/%d, want 0/30", s.RetentionGraceDays, s.HealthRetentionDays)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"retentionGraceDays": 3650, "healthRetentionDays": 3650}),
		http.StatusOK, &s, ifMatch(s.Revision))
	do(t, hr, http.MethodPut, settingsPath, settingsBody(nil), http.StatusOK, &s, ifMatch(s.Revision))
	if s.RetentionGraceDays != 3650 || s.HealthRetentionDays != 3650 {
		t.Errorf("periods left out = %d/%d, want 3650/3650 kept", s.RetentionGraceDays, s.HealthRetentionDays)
	}
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"retentionGraceDays": 90}), http.StatusOK, &s, ifMatch(s.Revision))

	// A refused object and an erase.
	company(t, hr)
	p := createPerson(t, hr, newPerson("Erik", "Glemt"))
	pastEmployment(t, hr, p.Id, "2020-01-01", "2024-12-31", terms())
	seedDocument(t, h, p.Id, "other")
	h.objects.failDelete(fmt.Errorf("store down"))
	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	s = getSettings(t, hr)
	if s.Retention.PendingDeletions != 1 || s.Retention.FailingDeletions != 1 {
		t.Errorf("retention = %+v, want one pending and failing", s.Retention)
	}
	if s.Retention.OldestPendingSince == nil || !s.Retention.OldestPendingSince.Equal(h.Now()) {
		t.Errorf("oldestPendingSince = %v, want %s", s.Retention.OldestPendingSince, h.Now())
	}
	if s.Retention.LastErasedAt == nil || !s.Retention.LastErasedAt.Equal(h.Now()) {
		t.Errorf("lastErasedAt = %v, want %s", s.Retention.LastErasedAt, h.Now())
	}
}

// The worker as production starts it: through Module().Workers, collected by
// module.Workers — in worker mode, from Deps that carry no object store
// (cmd/vantigo's worker Deps) — on by default, off with
// STAFF_RETENTION_ENABLED=0, polling STAFF_WORKERS_POLL. Built that way it
// starts without storage, and its ledger pass keeps every row, each failed
// as storage_unavailable, until storage is configured.
func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want []string
		poll time.Duration
	}{
		{name: "the default installation runs both workers", want: []string{"staff-retention", "staff-accounts"}, poll: time.Hour},
		{name: "the poll is the operator's", env: map[string]string{"STAFF_WORKERS_POLL": "15m"}, want: []string{"staff-retention", "staff-accounts"}, poll: 15 * time.Minute},
		{name: "retention turned off is never started", env: map[string]string{"STAFF_RETENTION_ENABLED": "0"}, want: []string{"staff-accounts"}, poll: time.Hour},
		{name: "accounts turned off is never started", env: map[string]string{"STAFF_ACCOUNTS_ENABLED": "0"}, want: []string{"staff-retention"}, poll: time.Hour},
		{name: "both turned off", env: map[string]string{"STAFF_RETENTION_ENABLED": "0", "STAFF_ACCOUNTS_ENABLED": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []modtest.Option
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)
			var names []string
			for _, w := range module.Workers(h.Deps(), staff.Module()) {
				names = append(names, w.Name())
				if w.Interval() != tc.poll {
					t.Errorf("worker %s polls every %v, want %v", w.Name(), w.Interval(), tc.poll)
				}
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("workers = %v, want exactly %v", names, tc.want)
			}
		})
	}

	h := newHarness(t)
	h.Exec(t, `INSERT INTO staff.object_deletions (object_key, next_attempt_at, created_at) VALUES ('people/1/a', $1, $1)`, h.Now())
	full := h.Deps()
	workerDeps := module.Deps{Config: full.Config, Pool: full.Pool, Logger: full.Logger, Clock: full.Clock}
	workers := module.Workers(workerDeps, staff.Module())
	if len(workers) != 2 {
		t.Fatalf("worker-mode workers = %d, want staff-retention and staff-accounts", len(workers))
	}
	retention, ok := workers[0].(*staff.RetentionWorker)
	if !ok {
		t.Fatalf("worker %T is not the retention worker", workers[0])
	}
	attempted, succeeded, err := retention.RunLedger(context.Background())
	if err != nil || attempted != 1 || succeeded != 0 {
		t.Fatalf("ledger without storage = %d, %d, %v; want 1 attempted, none confirmed", attempted, succeeded, err)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT coalesce(last_error, '') FROM staff.object_deletions WHERE object_key = 'people/1/a'`); !strings.HasPrefix(got, "storage_unavailable") {
		t.Errorf("last_error = %q, want storage_unavailable's", got)
	}
}
