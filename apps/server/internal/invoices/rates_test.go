package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const collectionRatesPath = "/api/v1/invoices/collection-rates"

func collectionRatePath(id int64) string { return fmt.Sprintf("%s/%d", collectionRatesPath, id) }

// collectionRateJSON is one collection rate as a client reads it.
type collectionRateJSON struct {
	ID               int64     `json:"id"`
	Kind             string    `json:"kind"`
	ValidFrom        string    `json:"validFrom"`
	Value            float64   `json:"value"`
	ReleaseValue     *float64  `json:"releaseValue"`
	ReleaseSourceRef *string   `json:"releaseSourceRef"`
	SourceRef        string    `json:"sourceRef"`
	Seeded           bool      `json:"seeded"`
	InForce          bool      `json:"inForce"`
	Usable           bool      `json:"usable"`
	CreatedBy        *string   `json:"createdBy"`
	CreatedAt        time.Time `json:"createdAt"`
}

// collectionRatesJSON is GET /collection-rates as a client reads it.
type collectionRatesJSON struct {
	Rates    []collectionRateJSON `json:"rates"`
	Warnings []string             `json:"warnings"`
}

// listRates reads the collection rates as an invoices:access holder.
func listRates(t *testing.T, h *harness) collectionRatesJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, collectionRatesPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /collection-rates = %d %s", res.Status, res.Body)
	}
	var list collectionRatesJSON
	res.JSON(&list)
	return list
}

// rateOn is the listed row of kind starting on validFrom, failing when there
// is none.
func rateOn(t *testing.T, list collectionRatesJSON, kind, validFrom string) collectionRateJSON {
	t.Helper()
	for _, r := range list.Rates {
		if r.Kind == kind && r.ValidFrom == validFrom {
			return r
		}
	}
	t.Fatalf("no %s row from %s in %+v", kind, validFrom, list.Rates)
	return collectionRateJSON{}
}

// inForceRows is the listed rows in force, as "kind validFrom".
func inForceRows(list collectionRatesJSON) []string {
	var rows []string
	for _, r := range list.Rates {
		if r.InForce {
			rows = append(rows, r.Kind+" "+r.ValidFrom)
		}
	}
	return rows
}

// rateBody is a POST /collection-rates body.
func rateBody(kind, validFrom string, value float64) map[string]any {
	return map[string]any{"kind": kind, "validFrom": validFrom, "value": value, "sourceRef": "FOR-2026-12-18-9999"}
}

// addedRate posts a rate as an invoices:manage holder and answers it,
// failing unless it was added.
func addedRate(t *testing.T, h *harness, body map[string]any) collectionRateJSON {
	t.Helper()
	res := manager(t, h).Do(http.MethodPost, collectionRatesPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /collection-rates %v = %d %s, want 201", body, res.Status, res.Body)
	}
	var r collectionRateJSON
	res.JSON(&r)
	return r
}

// releaseSeeds runs a later release's seed of one rate, the statement its
// migration runs (D6, plan reading 4).
func releaseSeeds(t *testing.T, h *harness, kind, validFrom string, value float64, sourceRef string) {
	t.Helper()
	h.Exec(t, `SELECT invoices.seed_collection_rate($1, $2::date, $3, $4)`, kind, validFrom, value, sourceRef)
}

// printedLetter plants a paper letter of invoice, printed with its facts and
// dated sentOn — a letter that relies on the rates in force on that day (plan
// reading 6) — and answers its id. The run arrives in Task 10, the batch in
// Task 13; the letter is written as they will write it.
func printedLetter(t *testing.T, h *harness, invoiceID int64, sequence int, sentOn string) int64 {
	t.Helper()
	run := plantID(t, h, `INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
		VALUES ($1::date, now(), gen_random_uuid(), false) RETURNING id`, sentOn)
	batch := plantID(t, h, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
		VALUES ($1::date, now(), gen_random_uuid()) RETURNING id`, sentOn)
	letter := plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'awaiting_print') RETURNING id`, invoiceID, run, sequence)
	h.Exec(t, `UPDATE invoices.reminders SET status = 'printed', print_batch_id = $2, sent_on = $3::date,
		deadline = $3::date + 14, regime = 'inkassolov_1988', principal_open = 1250, fee_kind = 'none',
		charges_earlier = 0, interest = 0, interest_waived = 0, interest_paid = 0, total = 1250
		WHERE id = $1`, letter, batch, sentOn)
	return letter
}

// queuedLetter plants an e-mail letter still queued — no facts, so no rate
// relied on yet.
func queuedLetter(t *testing.T, h *harness, invoiceID int64, sequence int) int64 {
	t.Helper()
	run := plantID(t, h, `INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
		VALUES (DATE '2026-09-12', now(), gen_random_uuid(), false) RETURNING id`)
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'email', 'faktura@acme.example', 'nb', now(), gen_random_uuid(), 'queued') RETURNING id`, invoiceID, run, sequence)
}

// advanceTo moves the harness clock forward to at.
func advanceTo(t *testing.T, h *harness, at time.Time) {
	t.Helper()
	if !at.After(h.Now()) {
		t.Fatalf("the clock is at %s, cannot move back to %s", h.Now(), at)
	}
	h.Advance(at.Sub(h.Now()))
}

// osloAt is a wall-clock time in Oslo.
func osloAt(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatalf("load Europe/Oslo: %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

// The list (D6): every seeded row by kind and date, seeded and without a
// user; the one row of each kind in force today; a row a printed letter
// relied on no longer usable — a queued letter, without facts, relies on
// nothing; the release's value over a user's row, with the warning only
// while it differs.
func TestCollectionRates_List(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)

	list := listRates(t, h)
	if len(list.Rates) != 14 || len(list.Warnings) != 0 {
		t.Fatalf("the list = %d rows, warnings %v; want the 14 seeds and none", len(list.Rates), list.Warnings)
	}
	var order []string
	for _, r := range list.Rates {
		order = append(order, r.Kind+" "+r.ValidFrom)
		if !r.Seeded || r.CreatedBy != nil || r.ReleaseValue != nil || !r.Usable {
			t.Errorf("seed %s %s = seeded %v createdBy %v releaseValue %v usable %v; want seeded, no user, no release value, usable",
				r.Kind, r.ValidFrom, r.Seeded, r.CreatedBy, r.ReleaseValue, r.Usable)
		}
	}
	if !slices.IsSortedFunc(order, strings.Compare) {
		t.Errorf("the list's order = %v, want by kind and date", order)
	}
	if got, want := inForceRows(list), []string{
		"b2b_compensation_nok 2026-07-01", "inkassosats 2026-01-01", "late_interest_percent 2026-07-01",
	}; !slices.Equal(got, want) {
		t.Errorf("in force on 2026-09-12 = %v, want %v", got, want)
	}
	if r := rateOn(t, list, "late_interest_percent", "2026-07-01"); r.Value != 12.25 || r.SourceRef != "FOR-2026-06-25-1372" {
		t.Errorf("the 2026-H2 interest row = %v %s, want 12.25 FOR-2026-06-25-1372", r.Value, r.SourceRef)
	}

	// A printed letter dated the last day of 2026-H1 relies on that
	// half-year's rows and the inkassosats from 2026-01-01; a queued one
	// relies on nothing.
	invoice := issuedFor(t, h, customerAcme, line("Konsulenttimer", 1, 1000, vat25))
	printedLetter(t, h, invoice.ID, 1, "2026-06-30")
	queuedLetter(t, h, invoice.ID, 2)
	list = listRates(t, h)
	var unusable []string
	for _, r := range list.Rates {
		if !r.Usable {
			unusable = append(unusable, r.Kind+" "+r.ValidFrom)
		}
	}
	if want := []string{
		"b2b_compensation_nok 2026-01-01", "inkassosats 2026-01-01", "late_interest_percent 2026-01-01",
	}; !slices.Equal(unusable, want) {
		t.Errorf("the rows a letter of 2026-06-30 used = %v, want %v", unusable, want)
	}

	// A release's seed over a user's row records the release's value beside
	// the user's: equal, no warning; different, the warning.
	user, who := h.SignInUser(t, "invoices:access", "invoices:manage")
	res := user.Do(http.MethodPost, collectionRatesPath, rateBody("b2b_compensation_nok", "2027-01-01", 440))
	if res.Status != http.StatusCreated {
		t.Fatalf("POST a compensation row = %d %s", res.Status, res.Body)
	}
	addedRate(t, h, rateBody("late_interest_percent", "2027-01-01", 12.5))
	releaseSeeds(t, h, "b2b_compensation_nok", "2027-01-01", 440, "FOR-2026-12-18-1111")
	list = listRates(t, h)
	added := rateOn(t, list, "b2b_compensation_nok", "2027-01-01")
	if added.Seeded || added.CreatedBy == nil || *added.CreatedBy != who.String() || added.InForce || !added.Usable {
		t.Errorf("the user's row = seeded %v by %v inForce %v usable %v; want the user's, by %s, not in force, usable",
			added.Seeded, added.CreatedBy, added.InForce, added.Usable, who)
	}
	if added.ReleaseValue == nil || *added.ReleaseValue != 440 || added.ReleaseSourceRef == nil || *added.ReleaseSourceRef != "FOR-2026-12-18-1111" ||
		added.Value != 440 || len(list.Warnings) != 0 {
		t.Errorf("an equal release seed = value %v release %v %v, warnings %v; want the release recorded beside it and no warning",
			added.Value, added.ReleaseValue, added.ReleaseSourceRef, list.Warnings)
	}
	releaseSeeds(t, h, "late_interest_percent", "2027-01-01", 12.75, "FOR-2026-12-18-2222")
	list = listRates(t, h)
	if r := rateOn(t, list, "late_interest_percent", "2027-01-01"); r.Value != 12.5 || r.ReleaseValue == nil || *r.ReleaseValue != 12.75 {
		t.Errorf("a differing release seed = value %v release %v; want the user's 12.5 kept beside the release's 12.75", r.Value, r.ReleaseValue)
	}
	if want := []string{"collection_rate_differs_from_release"}; !slices.Equal(list.Warnings, want) {
		t.Errorf("the warnings = %v, want %v", list.Warnings, want)
	}
}

// inForce moves at Oslo midnight (D6, the clock rule): the 2026-H2 rows on
// 31 December at 23:30 Oslo, the 2027-H1 rows half an hour later — 23:00 UTC
// — and the same across 30 June and 1 July, an inkassosats row from
// 1 July changing with them.
func TestCollectionRates_InForceOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, body := range []map[string]any{
		rateBody("late_interest_percent", "2027-01-01", 12.5), rateBody("b2b_compensation_nok", "2027-01-01", 440),
		rateBody("late_interest_percent", "2027-07-01", 12.75), rateBody("b2b_compensation_nok", "2027-07-01", 450),
		rateBody("inkassosats", "2027-07-01", 780),
	} {
		addedRate(t, h, body)
	}
	for _, c := range []struct {
		at   time.Time
		want []string
	}{
		{osloAt(t, 2026, time.December, 31, 23, 30), []string{
			"b2b_compensation_nok 2026-07-01", "inkassosats 2026-01-01", "late_interest_percent 2026-07-01"}},
		{osloAt(t, 2027, time.January, 1, 0, 30), []string{
			"b2b_compensation_nok 2027-01-01", "inkassosats 2026-01-01", "late_interest_percent 2027-01-01"}},
		{osloAt(t, 2027, time.June, 30, 23, 30), []string{
			"b2b_compensation_nok 2027-01-01", "inkassosats 2026-01-01", "late_interest_percent 2027-01-01"}},
		{osloAt(t, 2027, time.July, 1, 0, 30), []string{
			"b2b_compensation_nok 2027-07-01", "inkassosats 2027-07-01", "late_interest_percent 2027-07-01"}},
	} {
		advanceTo(t, h, c.at)
		if got := inForceRows(listRates(t, h)); !slices.Equal(got, c.want) {
			t.Errorf("in force at %s = %v, want %v", c.at, got, c.want)
		}
	}
}

// The add's refusals (D6), each by its guard: the 400s on the field —
// validFrom not after today, a half-yearly kind off 1 January and 1 July,
// each kind's bounds with their edges allowed, more than two decimals, an
// unknown kind, a blank or long sourceRef — come before the duplicate's 409,
// so a past day a seed already holds is a 400; and only invoices:manage adds.
func TestCollectionRates_PostRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	refused := func(body map[string]any, field string) {
		t.Helper()
		res := manager(t, h).Do(http.MethodPost, collectionRatesPath, body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("POST %v = %d %s, want 400 on %s", body, res.Status, res.Body, field)
			return
		}
		if p := problemOf(t, res); len(p.Errors[field]) == 0 {
			t.Errorf("POST %v = 400 %v, want it on %s", body, p.Errors, field)
		}
	}
	long := rateBody("inkassosats", "2027-02-02", 800)
	long["sourceRef"] = strings.Repeat("F", 101)
	blank := rateBody("inkassosats", "2027-02-02", 800)
	blank["sourceRef"] = "  "
	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{rateBody("inkassosats", "2026-09-12", 800), "validFrom"},
		{rateBody("inkassosats", "2026-09-11", 800), "validFrom"},
		{rateBody("late_interest_percent", "2026-07-01", 12.25), "validFrom"},
		{rateBody("late_interest_percent", "2027-01-02", 12.5), "validFrom"},
		{rateBody("b2b_compensation_nok", "2027-06-01", 440), "validFrom"},
		{rateBody("late_interest_percent", "2027-01-01", 0), "value"},
		{rateBody("late_interest_percent", "2027-01-01", 30.01), "value"},
		{rateBody("late_interest_percent", "2027-01-01", 12.345), "value"},
		{rateBody("b2b_compensation_nok", "2027-01-01", 99.99), "value"},
		{rateBody("b2b_compensation_nok", "2027-01-01", 2000.01), "value"},
		{rateBody("inkassosats", "2027-01-01", 99), "value"},
		{rateBody("inkassosats", "2027-01-01", 5001), "value"},
		{rateBody("purregebyr", "2027-01-01", 70), "kind"},
		{long, "sourceRef"},
		{blank, "sourceRef"},
	} {
		refused(c.body, c.field)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE created_by_user_id IS NOT NULL`); n != 0 {
		t.Fatalf("%d rows written by refused adds", n)
	}

	for _, body := range []map[string]any{
		rateBody("late_interest_percent", "2027-01-01", 0.01), rateBody("late_interest_percent", "2027-07-01", 30),
		rateBody("b2b_compensation_nok", "2027-01-01", 100), rateBody("b2b_compensation_nok", "2027-07-01", 2000),
		rateBody("inkassosats", "2026-09-13", 100), rateBody("inkassosats", "2027-01-02", 5000),
	} {
		addedRate(t, h, body)
	}

	res := manager(t, h).Do(http.MethodPost, collectionRatesPath, rateBody("late_interest_percent", "2027-01-01", 12.5))
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "collection_rate_exists" {
		t.Errorf("a second 2027-01-01 interest row = %d %s, want 409 collection_rate_exists", res.Status, res.Body)
	}
	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:payments"}} {
		if res := h.SignIn(t, perms...).Do(http.MethodPost, collectionRatesPath, rateBody("inkassosats", "2027-03-03", 800)); res.Status != http.StatusForbidden {
			t.Errorf("POST as %v = %d, want 403", perms, res.Status)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE created_by_user_id IS NOT NULL`); n != 6 {
		t.Errorf("%d user rows, want the 6 added", n)
	}
}

// The delete's refusals (D6, plan reading 6): a row a printed letter dated
// on or after its start relied on, though not yet in force; a seeded row,
// though future and unused; a row in force — each 409
// collection_rate_in_force — then 404 and 204; only invoices:manage deletes.
func TestCollectionRates_DeleteRefusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	invoice := issuedFor(t, h, customerAcme, line("Konsulenttimer", 1, 1000, vat25))
	inUse := func(id int64, why string) {
		t.Helper()
		res := manager(t, h).Do(http.MethodDelete, collectionRatePath(id), nil)
		if res.Status != http.StatusConflict || problemOf(t, res).Code != "collection_rate_in_force" {
			t.Errorf("DELETE %s = %d %s, want 409 collection_rate_in_force", why, res.Status, res.Body)
		}
	}

	used := addedRate(t, h, rateBody("inkassosats", "2026-09-15", 800))
	later := addedRate(t, h, rateBody("inkassosats", "2026-09-20", 810))
	tomorrow := addedRate(t, h, rateBody("inkassosats", "2026-09-13", 790))
	printedLetter(t, h, invoice.ID, 1, "2026-09-15")
	inUse(used.ID, "a row a printed letter relied on")
	if r := rateOn(t, listRates(t, h), "inkassosats", "2026-09-20"); !r.Usable {
		t.Error("the next row = not usable, want usable: the letter is dated before it")
	}

	addedRate(t, h, rateBody("late_interest_percent", "2027-01-01", 12.5))
	releaseSeeds(t, h, "late_interest_percent", "2027-01-01", 12.75, "FOR-2026-12-18-2222")
	user := rateOn(t, listRates(t, h), "late_interest_percent", "2027-01-01")
	if res := manager(t, h).Do(http.MethodDelete, collectionRatePath(user.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE the user's row a release seeded over = %d %s, want 204", res.Status, res.Body)
	}
	inUse(rateOn(t, listRates(t, h), "late_interest_percent", "2027-01-01").ID, "a seeded row")
	inUse(rateOn(t, listRates(t, h), "inkassosats", "2026-01-01").ID, "a seeded row in force")

	h.Advance(24 * time.Hour)
	inUse(tomorrow.ID, "a user's row in force")

	if res := manager(t, h).Do(http.MethodDelete, collectionRatePath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE an unknown rate = %d, want 404", res.Status)
	}
	for _, perms := range [][]string{{"invoices:access"}, {"invoices:access", "invoices:payments"}} {
		if res := h.SignIn(t, perms...).Do(http.MethodDelete, collectionRatePath(later.ID), nil); res.Status != http.StatusForbidden {
			t.Errorf("DELETE as %v = %d, want 403", perms, res.Status)
		}
	}
	if res := manager(t, h).Do(http.MethodDelete, collectionRatePath(later.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE a future unused row = %d %s, want 204", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE id = $1`, later.ID); n != 0 {
		t.Error("the deleted row is still there")
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE kind = 'inkassosats' AND valid_from = DATE '2026-09-20'`); n != 0 {
		t.Error("a row without a release value was replaced on delete")
	}
}

// m6: a user's row a release seeded over is deleted and replaced, in the
// same transaction, by a seeded row of the release's value and regulation,
// so the half-year never goes empty.
func TestCollectionRates_DeleteReseedsTheRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	user := addedRate(t, h, rateBody("b2b_compensation_nok", "2027-01-01", 440))
	releaseSeeds(t, h, "b2b_compensation_nok", "2027-01-01", 450, "FOR-2026-12-18-3333")
	h.Advance(time.Hour)
	if res := manager(t, h).Do(http.MethodDelete, collectionRatePath(user.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE kind = 'b2b_compensation_nok' AND valid_from = DATE '2027-01-01'`); n != 1 {
		t.Fatalf("%d rows of 2027-01-01, want the release's one", n)
	}
	r := rateOn(t, listRates(t, h), "b2b_compensation_nok", "2027-01-01")
	if r.ID == user.ID || !r.Seeded || r.CreatedBy != nil || r.Value != 450 || r.SourceRef != "FOR-2026-12-18-3333" ||
		r.ReleaseValue != nil || !r.CreatedAt.Equal(h.Now()) {
		t.Errorf("the row after the delete = %+v; want a new seeded row of 450 under FOR-2026-12-18-3333, created now", r)
	}
	created := modtest.One[time.Time](t, h.Harness, `SELECT created_at FROM invoices.collection_rates WHERE id = $1`, r.ID)
	if !created.Equal(h.Now()) {
		t.Errorf("created_at = %s, want the request's clock %s", created, h.Now())
	}
}

// sentLetter plants an e-mail letter sent on sentOn with its facts.
func sentLetter(t *testing.T, h *harness, invoiceID int64, sequence int, sentOn string) int64 {
	t.Helper()
	letter := queuedLetter(t, h, invoiceID, sequence)
	h.Exec(t, `UPDATE invoices.reminders SET status = 'sent', sent_on = $2::date, sent_at = now(),
		deadline = $2::date + 14, regime = 'inkassolov_1988', principal_open = 1250, fee_kind = 'none',
		charges_earlier = 0, interest = 0, interest_waived = 0, interest_paid = 0, total = 1250
		WHERE id = $1`, letter, sentOn)
	return letter
}

// A new rate never contradicts a letter already printed or posted (D6, the
// Task 6 review's M2): its validFrom must be after the sent_on of the latest
// printed or sent letter — a paper letter is printed for a posting date
// ahead of today — else a 400 on validFrom. A letter without facts, queued
// or withdrawn, relies on nothing.
func TestCollectionRates_PostAfterTheLatestLetter(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	invoice := issuedFor(t, h, customerAcme, line("Konsulenttimer", 1, 1000, vat25))
	sentLetter(t, h, invoice.ID, 1, "2026-09-12")
	printedLetter(t, h, invoice.ID, 2, "2026-09-18")
	queuedLetter(t, h, invoice.ID, 3)
	for _, day := range []string{"2026-09-13", "2026-09-18"} {
		res := manager(t, h).Do(http.MethodPost, collectionRatesPath, rateBody("inkassosats", day, 800))
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["validFrom"]) == 0 {
			t.Errorf("a rate from %s, a letter printed for 2026-09-18 = %d %s, want 400 on validFrom", day, res.Status, res.Body)
		}
	}
	if r := addedRate(t, h, rateBody("inkassosats", "2026-09-19", 800)); r.ValidFrom != "2026-09-19" {
		t.Errorf("the rate the day after the latest letter = %+v", r)
	}
}

// The DELETE locks the rate row FOR UPDATE before it judges whether a letter
// used it (D6, the Task 6 review's M1), so a print batch holding the row FOR
// KEY SHARE while it prints a letter that relies on it (Task 13) is waited
// for and then seen: the DELETE waits on the batch — pg_blocking_pids names
// it — and, the letter committed, answers 409 collection_rate_in_force. A
// DELETE that judged first would have found no letter and deleted the row the
// letter printed. The seam sees the lock; deadlocks unchanged. Not parallel:
// the seam is the package's.
func TestCollectionRates_DeleteLocksTheRowFirst(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	invoice := issuedFor(t, h, customerAcme, line("Konsulenttimer", 1, 1000, vat25))
	relied := addedRate(t, h, rateBody("inkassosats", "2026-09-20", 800))
	spare := addedRate(t, h, rateBody("inkassosats", "2026-09-25", 810))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	if res := manager(t, h).Do(http.MethodDelete, collectionRatePath(spare.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE an unused rate = %d %s", res.Status, res.Body)
	}
	restore()
	if got, want := seen.take(), []string{"collection_rate " + idKey(spare.ID)}; !slices.Equal(got, want) {
		t.Errorf("the DELETE locked %v, want %v", got, want)
	}

	// The print batch's position: the rate row FOR KEY SHARE, and a letter
	// dated on its first day printed in the same transaction.
	batch := holdRow(t, h, `SELECT 1 FROM invoices.collection_rates WHERE id = $1 FOR KEY SHARE`, relied.ID)
	ctx := context.Background()
	var run, printBatch, letter int64
	for _, step := range []struct {
		dst  *int64
		sql  string
		args []any
	}{
		{&run, `INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
			VALUES (DATE '2026-09-12', now(), gen_random_uuid(), false) RETURNING id`, nil},
		{&printBatch, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
			VALUES (DATE '2026-09-20', now(), gen_random_uuid()) RETURNING id`, nil},
	} {
		if err := batch.tx.QueryRow(ctx, step.sql, step.args...).Scan(step.dst); err != nil {
			t.Fatalf("plant in the batch's transaction: %v", err)
		}
	}
	if err := batch.tx.QueryRow(ctx, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, 1, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'awaiting_print') RETURNING id`,
		invoice.ID, run).Scan(&letter); err != nil {
		t.Fatalf("plant the letter: %v", err)
	}
	if _, err := batch.tx.Exec(ctx, `UPDATE invoices.reminders SET status = 'printed', print_batch_id = $2, sent_on = DATE '2026-09-20',
		deadline = DATE '2026-10-05', regime = 'inkassolov_1988', principal_open = 1250, fee_kind = 'none',
		charges_earlier = 0, interest = 0, interest_waived = 0, interest_paid = 0, total = 1250 WHERE id = $1`, letter, printBatch); err != nil {
		t.Fatalf("print the letter: %v", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	answered := make(chan *modtest.Response, 1)
	client := manager(t, h)
	go func() {
		answered <- client.Do(http.MethodDelete, collectionRatePath(relied.ID), nil, modtest.Context(deadline))
	}()
	del := newWaiter(t, probeConn)
	if got, want := blockersOf(t, probeConn, del), []uint32{batch.pid}; !slices.Equal(got, want) {
		t.Fatalf("pg_blocking_pids(the DELETE) = %v, want the batch %v", got, want)
	}
	batch.release(t)
	res := <-answered
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "collection_rate_in_force" {
		t.Errorf("the DELETE after the letter was printed = %d %s, want 409 collection_rate_in_force", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.collection_rates WHERE id = $1`, relied.ID); n != 1 {
		t.Error("the rate the printed letter relied on was deleted")
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
}
