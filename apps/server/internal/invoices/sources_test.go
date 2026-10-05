package invoices_test

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The work a draft's lines bill (invoices work design D2): held from the
// draft, carried by identity across every save, dropped by name, answered
// from the document's own rows, and judged fresh through the source modules'
// billable reads. Until the wizard (from-work) exists, the held rows are
// planted by SQL on a draft made through the API, as the wizard will write
// them.

// sourceJSON is one of a line's sources on the wire.
type sourceJSON struct {
	Kind      string  `json:"kind"`
	ID        int64   `json:"id"`
	ProjectID int32   `json:"projectId"`
	Date      string  `json:"date"`
	Quantity  float64 `json:"quantity"`
	Amount    float64 `json:"amount"`
	State     string  `json:"state"`
}

// workDocJSON is the part of a document the sources tests read.
type workDocJSON struct {
	ID       int64    `json:"id"`
	Revision int32    `json:"revision"`
	Warnings []string `json:"warnings"`
	Lines    []struct {
		ID       int64        `json:"id"`
		Position int32        `json:"position"`
		LineNet  float64      `json:"lineNet"`
		Sources  []sourceJSON `json:"sources"`
		Warnings []string     `json:"warnings"`
	} `json:"lines"`
	Sources *struct {
		Count    int32 `json:"count"`
		Held     int32 `json:"held"`
		Invoiced int32 `json:"invoiced"`
		Released int32 `json:"released"`
	} `json:"sources"`
	ReleasedSources []refJSON `json:"releasedSources"`
}

type refJSON struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

// The four pieces of work the sourced draft holds, all on project 41:
// two hour entries on line 1 (4 h and 3.5 h at 1 200), a mileage expense of
// 90 km on line 2 and a milestone on line 3.
const (
	hourOne   = 501
	hourTwo   = 502
	mileage   = 601
	milestone = 701
	project41 = 41
)

// planted is one held row as the wizard would write it.
type planted struct {
	position         int32
	kind             string
	id               int64
	revision         int32
	subkind          *string
	quantity, amount string
	date             string
	project          int32
	currency         string
}

func plantSource(t *testing.T, h *harness, invoiceID int64, p planted) {
	t.Helper()
	currency, project := p.currency, p.project
	if currency == "" {
		currency = "NOK"
	}
	if project == 0 {
		project = project41
	}
	h.Exec(t, `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
			project_id, quantity, amount, currency, source_date)
		SELECT l.id, l.invoice_id, $3, $4, $5, $6, $7, $8::numeric, $9::numeric, $10, $11::date
		FROM invoices.lines l WHERE l.invoice_id = $1 AND l.position = $2`,
		invoiceID, p.position, p.kind, p.id, p.revision, p.subkind, project, p.quantity, p.amount, currency, p.date)
}

// theFour is the sourced draft's held work, planted on lines 1 to 3.
func theFour() []planted {
	return []planted{
		{position: 1, kind: "time.entry", id: hourOne, revision: 2, quantity: "4.00", amount: "4800", date: "2026-09-01"},
		{position: 1, kind: "time.entry", id: hourTwo, revision: 1, quantity: "3.50", amount: "4200", date: "2026-09-02"},
		{position: 2, kind: "expenses.entry", id: mileage, revision: 3, subkind: ptrTo("mileage"), quantity: "90", amount: "450", date: "2026-09-03"},
		{position: 3, kind: "projects.milestone", id: milestone, revision: 1, quantity: "1", amount: "10000", date: "2026-09-05"},
	}
}

func ptrTo[T any](v T) *T { return &v }

// The sourced draft's lines, each with the sources it names (nil: the field
// left out).
func hoursLine(quantity, price float64, sources ...refJSON) map[string]any {
	return sourcedLine(line("Konsulenttimer", quantity, price, vat25), sources)
}

func sourcedLine(l map[string]any, sources []refJSON) map[string]any {
	if sources != nil {
		l["sources"] = sources
	}
	return l
}

func ref(kind string, id int64) refJSON { return refJSON{Kind: kind, ID: id} }

var (
	refHourOne   = ref("time.entry", hourOne)
	refHourTwo   = ref("time.entry", hourTwo)
	refMileage   = ref("expenses.entry", mileage)
	refMilestone = ref("projects.milestone", milestone)
)

// sourcedDraft is an Acme draft of three lines — 7.5 h at 1 200, mileage at
// 450 and a milestone at 10 000 — holding theFour.
func sourcedDraft(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	d := createDraft(t, h, draftBody(customerAcme,
		line("Konsulenttimer", 7.5, 1200, vat25), line("Kjøregodtgjørelse", 1, 450, vat25), line("Milepæl", 1, 10000, vat25)))
	for _, p := range theFour() {
		plantSource(t, h, d.ID, p)
	}
	return d
}

// sourcedBody is a replace of d at its revision with lines.
func sourcedBody(d invoiceJSON, customer int32, lines ...map[string]any) map[string]any {
	body := draftBody(customer, lines...)
	body["paymentTermsDays"], body["revision"] = 30, d.Revision
	return body
}

// theSameLines is the sourced draft's three lines naming what they hold.
func theSameLines() []map[string]any {
	return []map[string]any{
		hoursLine(7.5, 1200, refHourOne, refHourTwo),
		sourcedLine(line("Kjøregodtgjørelse", 1, 450, vat25), []refJSON{refMileage}),
		sourcedLine(line("Milepæl", 1, 10000, vat25), []refJSON{refMilestone}),
	}
}

func putWork(t *testing.T, h *harness, id int64, body map[string]any) (*modtest.Response, workDocJSON) {
	t.Helper()
	res := creator(t, h).Do(http.MethodPut, invoicePath(id), body)
	var doc workDocJSON
	if res.Status == http.StatusOK {
		res.JSON(&doc)
	}
	return res, doc
}

func getWork(t *testing.T, c *modtest.Client, id int64) workDocJSON {
	t.Helper()
	res := c.Do(http.MethodGet, invoicePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var doc workDocJSON
	res.JSON(&doc)
	return doc
}

// heldRows is a document's rows as "kind:id@line-position r<rev> q<qty> a<amount> p<project> <date> <subkind> <state>",
// in the order they were inserted.
func heldRows(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(format('%s:%s@%s r%s q%s a%s p%s %s %s %s', s.source_kind, s.source_id, l.position,
			s.source_revision, s.quantity::float8, s.amount::float8, s.project_id, s.source_date, coalesce(s.source_subkind, '-'), s.state)
			ORDER BY s.id), '{}')
		FROM invoices.line_sources s JOIN invoices.lines l ON l.id = s.line_id
		WHERE s.invoice_id = $1`, id)
}

func refsOf(refs []refJSON) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.Kind+":"+strconv.FormatInt(r.ID, 10))
	}
	return out
}

// A save deletes every line and inserts new ones; the work they bill is
// carried across by identity, with the snapshot the draft took — revision,
// quantity, amount, project, date and an expense's kind — in one statement
// ordered by (kind, id), never row by row in the lines' order (D2).
func TestSources_CarriedAcrossNewLineIds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)
	before := heldRows(t, h, d.ID)

	res, saved := putWork(t, h, d.ID, sourcedBody(d, customerAcme, theSameLines()...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	for i, l := range saved.Lines {
		if l.ID == d.Lines[i].ID {
			t.Fatalf("line %d kept its id %d; a save inserts new lines", i+1, l.ID)
		}
	}
	after := heldRows(t, h, d.ID)
	slices.Sort(before)
	if sorted := slices.Sorted(slices.Values(after)); !slices.Equal(sorted, before) {
		t.Errorf("held after the save =\n%v\nwant the snapshot carried =\n%v", sorted, before)
	}
	// Inserted ordered by (kind, id): the expense, the milestone, then the
	// hours — not in the lines' order, hours first.
	want := []string{"expenses.entry:601", "projects.milestone:701", "time.entry:501", "time.entry:502"}
	var got []string
	for _, r := range after {
		got = append(got, r[:strings.Index(r, "@")])
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows inserted in the order %v, want ordered by (kind, id) %v", got, want)
	}
	if saved.Sources == nil || saved.Sources.Count != 4 || saved.Sources.Held != 4 || len(saved.ReleasedSources) != 0 ||
		slices.Contains(saved.Warnings, "sources_released") {
		t.Errorf("answer = block %+v, released %v, warnings %v; want four held and nothing released", saved.Sources, saved.ReleasedSources, saved.Warnings)
	}
	if got := saved.Lines[0].Sources; len(got) != 2 || got[0] != (sourceJSON{Kind: "time.entry", ID: hourOne, ProjectID: project41,
		Date: "2026-09-01", Quantity: 4, Amount: 4800, State: "held"}) {
		t.Errorf("line 1's sources = %+v", got)
	}
}

// A save moves work between lines and drops what no line names any more —
// with its line, or left out — and names what it dropped (D2).
func TestSources_MovedBetweenLinesAndDropped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	res, saved := putWork(t, h, d.ID, sourcedBody(d, customerAcme,
		hoursLine(4, 1200, refHourOne),
		sourcedLine(line("Kjøring og timer", 1, 4650, vat25), []refJSON{refMileage, refHourTwo})))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	rows := heldRows(t, h, d.ID)
	slices.Sort(rows)
	want := []string{
		"expenses.entry:601@2 r3 q90 a450 p41 2026-09-03 mileage held",
		"time.entry:501@1 r2 q4 a4800 p41 2026-09-01 - held",
		"time.entry:502@2 r1 q3.5 a4200 p41 2026-09-02 - held",
	}
	if !slices.Equal(rows, want) {
		t.Errorf("held = %v, want %v", rows, want)
	}
	if got := refsOf(saved.ReleasedSources); !slices.Equal(got, []string{"projects.milestone:701"}) ||
		!slices.Contains(saved.Warnings, "sources_released") {
		t.Errorf("released = %v, warnings %v; want the milestone named and sources_released", got, saved.Warnings)
	}
	if slices.Contains(saved.Warnings, "line_differs_from_sources") {
		t.Errorf("warnings = %v; every line bills its sources' sum", saved.Warnings)
	}
	// Dropped means free: another draft may hold the milestone now.
	other := createDraft(t, h, draftBody(customerAcme, line("Milepæl", 1, 10000, vat25)))
	plantSource(t, h, other.ID, theFour()[3])
}

// When a draft holds work every line names its sources: a line that leaves
// the field out is a 400 and nothing changes; [] carries none, and a save of
// [] everywhere drops every hold and names each (D2).
func TestSources_RequiredOnEveryLineOfASourcedDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	lines := theSameLines()
	delete(lines[1], "sources")
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["lines[1].sources"]) == 0 {
		t.Fatalf("a line without sources = %d %s, want 400 on lines[1].sources", res.Status, res.Body)
	}
	if n := len(heldRows(t, h, d.ID)); n != 4 {
		t.Errorf("held after the refusal = %d, want the four untouched", n)
	}
	if again := getInvoice(t, h, d.ID); again.Revision != d.Revision {
		t.Errorf("revision after the refusal = %d, want %d: the save rolled back", again.Revision, d.Revision)
	}

	none := []map[string]any{
		sourcedLine(line("Konsulenttimer", 7.5, 1200, vat25), []refJSON{}),
		sourcedLine(line("Kjøregodtgjørelse", 1, 450, vat25), []refJSON{}),
	}
	res, saved := putWork(t, h, d.ID, sourcedBody(d, customerAcme, none...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT with [] = %d %s", res.Status, res.Body)
	}
	if got := refsOf(saved.ReleasedSources); !slices.Equal(got, []string{"expenses.entry:601", "projects.milestone:701", "time.entry:501", "time.entry:502"}) {
		t.Errorf("released = %v, want all four by (kind, id)", got)
	}
	if n := len(heldRows(t, h, d.ID)); n != 0 || saved.Sources != nil {
		t.Errorf("held = %d, block %+v; want none", n, saved.Sources)
	}
}

// A save never adds work: an identity the draft does not hold, a source
// named twice, or one of a kind this module does not know is a 400 on the
// line's sources — and so is any source on a draft that holds none (D2).
func TestSources_APutNeverAddsAHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	for _, c := range []struct {
		name  string
		lines []map[string]any
		field string
	}{
		{"unheld", []map[string]any{hoursLine(7.5, 1200, refHourOne, refHourTwo, ref("time.entry", 999))}, "lines[0].sources"},
		{"twice", []map[string]any{hoursLine(7.5, 1200, refHourOne, refHourTwo), hoursLine(4, 1200, refHourOne)}, "lines[1].sources"},
		{"kind", []map[string]any{hoursLine(7.5, 1200, ref("time.entries", hourOne))}, "lines[0].sources"},
	} {
		res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, c.lines...))
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[c.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", c.name, res.Status, res.Body, c.field)
		}
	}
	if got := problemOf(t, func() *modtest.Response {
		res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, hoursLine(7.5, 1200, ref("time.entry", 999))))
		return res
	}()).Errors["lines[0].sources"]; len(got) != 1 || !strings.Contains(got[0], "uninvoiced view") {
		t.Errorf("an unheld source's message = %v, want it to point at the uninvoiced view", got)
	}

	plain := createDraft(t, h, draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25)))
	res, _ := putWork(t, h, plain.ID, sourcedBody(plain, customerAcme, hoursLine(1, 1000, refHourOne)))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["lines[0].sources"]) == 0 {
		t.Errorf("work named on a draft holding none = %d %s, want 400", res.Status, res.Body)
	}
	if n := len(heldRows(t, h, d.ID)) + len(heldRows(t, h, plain.ID)); n != 4 {
		t.Errorf("held rows = %d, want the four the sourced draft held", n)
	}
}

// A draft that holds no work saves as it always did: no field required, and
// no sources on its answer (D2).
func TestSources_ADraftWithoutSourcesNeedsNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := createDraft(t, h, draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25)))

	res := creator(t, h).Do(http.MethodPut, invoicePath(d.ID), sourcedBody(d, customerAcme, line("Rådgivning", 2, 1000, vat25)))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	if strings.Contains(string(res.Body), `"sources"`) || strings.Contains(string(res.Body), `"releasedSources"`) {
		t.Errorf("a draft without work answers %s; want no sources at all", res.Body)
	}
	if strings.Contains(string(creator(t, h).Do(http.MethodGet, invoicePath(d.ID), nil).Body), `"sources"`) {
		t.Error("GET of a draft without work answers sources")
	}
}

// A customer change drops every hold, and names each; its lines' sources are
// then not held — a client changing the customer sends [] (plan reading 33).
func TestSources_ACustomerChangeReleasesEveryHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerPerson, theSameLines()...))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["lines[0].sources"]) == 0 {
		t.Fatalf("another customer naming the held work = %d %s, want 400 (no longer held)", res.Status, res.Body)
	}
	if n := len(heldRows(t, h, d.ID)); n != 4 {
		t.Fatalf("held after the refusal = %d, want four", n)
	}

	lines := theSameLines()
	for _, l := range lines {
		l["sources"] = []refJSON{}
	}
	res, saved := putWork(t, h, d.ID, sourcedBody(d, customerPerson, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT for another customer = %d %s", res.Status, res.Body)
	}
	if got := refsOf(saved.ReleasedSources); len(got) != 4 || !slices.Contains(saved.Warnings, "sources_released") {
		t.Errorf("released = %v, warnings %v; want all four and sources_released", got, saved.Warnings)
	}
	if n := len(heldRows(t, h, d.ID)); n != 0 {
		t.Errorf("held = %d, want none", n)
	}
}

// Deleting the draft takes its holds with its lines, and the work is free
// again (D2).
func TestSources_DeletingTheDraftReleases(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	if res := creator(t, h).Do(http.MethodDelete, invoicePath(d.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1`, d.ID); n != 0 {
		t.Errorf("held after the delete = %d, want the cascade to take them", n)
	}
	again := sourcedDraft(t, h)
	if n := len(heldRows(t, h, again.ID)); n != 4 {
		t.Errorf("held by a new draft = %d, want the four free again", n)
	}
}

// A save names at most 5 000 sources — the billable reads' page — refused on
// lines before any read (D2).
func TestSources_TheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := createDraft(t, h, draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25)))

	many := make([]refJSON, 0, contracts.MaxBillableRows+1)
	for i := range contracts.MaxBillableRows + 1 {
		many = append(many, ref("time.entry", int64(i+1)))
	}
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, hoursLine(1, 1000, many...)))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["lines"]) == 0 {
		t.Errorf("5 001 sources = %d, want 400 on lines", res.Status)
	}
	res, _ = putWork(t, h, d.ID, sourcedBody(d, customerAcme, hoursLine(1, 1000, many[:contracts.MaxBillableRows]...)))
	if p := problemOf(t, res); res.Status != http.StatusBadRequest || len(p.Errors["lines"]) != 0 || len(p.Errors["lines[0].sources"]) == 0 {
		t.Errorf("5 000 unheld sources = %d %s, want 400 on lines[0].sources only", res.Status, res.Body)
	}
}

// A line whose net is not its sources' sum rounded to øre warns on the line
// and once on the document — a write-down is allowed (D2).
func TestSources_LineDiffersFromSourcesWarns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := sourcedDraft(t, h)

	lines := theSameLines()
	lines[0]["unitPrice"] = 1100
	lines[1]["unitPrice"] = 400
	res, saved := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("a write-down = %d %s, want 200: a difference never refuses", res.Status, res.Body)
	}
	for i, want := range [][]string{{"line_differs_from_sources"}, {"line_differs_from_sources"}, {}} {
		if got := saved.Lines[i].Warnings; !slices.Equal(got, want) {
			t.Errorf("line %d warnings = %v, want %v", i+1, got, want)
		}
	}
	if n := countOf(saved.Warnings, "line_differs_from_sources"); n != 1 {
		t.Errorf("document warnings = %v, want line_differs_from_sources once", saved.Warnings)
	}
	if read := getWork(t, h.SignIn(t, "invoices:access"), d.ID); countOf(read.Warnings, "line_differs_from_sources") != 1 ||
		!slices.Equal(read.Lines[0].Warnings, []string{"line_differs_from_sources"}) {
		t.Errorf("GET = %v, line 1 %v; want the warning read back", read.Warnings, read.Lines[0].Warnings)
	}
}

func countOf(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// The fake's rows as theFour stands: everything fresh.
func freshBillable(f *fakeBillable) {
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	f.putHour(contracts.BillableHour{ID: hourOne, Revision: 2, ProjectID: project41, Date: day("2026-09-01"), HoursHundredths: 400,
		BillRate: "1200.00", Currency: "NOK", Amount: "4800.00000000"})
	f.putHour(contracts.BillableHour{ID: hourTwo, Revision: 1, ProjectID: project41, Date: day("2026-09-02"), HoursHundredths: 350,
		BillRate: "1200.00", Currency: "NOK", Amount: "4200"})
	f.putExpense(contracts.BillableExpense{ID: mileage, Revision: 3, ProjectID: project41, Kind: "mileage", Date: day("2026-09-03"),
		DistanceKm: ptrTo("90.0"), BillRatePerKm: ptrTo("5.00"), BillAmount: "450.00", Currency: "NOK"})
	f.putMilestone(contracts.BillableMilestone{ID: milestone, Revision: 1, ProjectID: project41, Name: "Fase 1",
		ReadyAt: time.Date(2026, 9, 4, 22, 30, 0, 0, time.UTC), Amount: "10000.00", Currency: "NOK"})
}

// flipIssued makes draft id an issued invoice by SQL, its held rows invoiced
// first, as an issue would leave it — without the issue path, whose
// write-back is another task's.
func flipIssued(t *testing.T, h *harness, id, number int64) {
	t.Helper()
	h.Exec(t, `UPDATE invoices.lines SET vat_rate_percent = 25, vat_category = 'S', saf_t_code = '3' WHERE invoice_id = $1`, id)
	h.Exec(t, `UPDATE invoices.line_sources SET state = 'invoiced' WHERE invoice_id = $1`, id)
	h.Exec(t, `
		UPDATE invoices.invoices SET status = 'issued', number = $2, issue_date = '2026-09-12', due_date = '2026-10-12',
			exchange_rate_date = '2026-09-12', seller_legal_name = 'Selger AS', buyer_name = 'Acme AS', issued_at = now()
		WHERE id = $1`, id, number)
}

// GET of an invoice draft judges its held work through the billable reads,
// by id, on the pool, for a caller holding invoices:create: a source whose
// revision moved is source_changed, one no longer answered is
// source_not_invoiceable — on its line and once on the document. No read for
// a reader, in the list, on an issued document or a credit-note draft; a kind
// whose module is off is not judged; a read that fails leaves the warnings
// out (D2, plan reading 11).
func TestSources_FreshnessOnGet(t *testing.T) {
	t.Parallel()
	f := newFakeBillable()
	h := newHarness(t, f.options()...)
	saveSeller(t, h, completeSeller(1))
	d := sourcedDraft(t, h)
	freshBillable(f)

	c, userID := h.SignInUser(t, "invoices:access", "invoices:create")
	if doc := getWork(t, c, d.ID); len(doc.Warnings) != 0 {
		t.Errorf("fresh work warns %v", doc.Warnings)
	}
	if got := len(f.reads()); got != 3 {
		t.Errorf("reads = %d, want one per kind", got)
	}

	f.putHour(contracts.BillableHour{ID: hourTwo, Revision: 2, ProjectID: project41, HoursHundredths: 350, Currency: "NOK", Amount: "4200"})
	f.mu.Lock()
	delete(f.milestones, milestone)
	f.mu.Unlock()
	doc := getWork(t, c, d.ID)
	if !slices.Equal(doc.Lines[0].Warnings, []string{"source_changed"}) || len(doc.Lines[1].Warnings) != 0 ||
		!slices.Equal(doc.Lines[2].Warnings, []string{"source_not_invoiceable"}) {
		t.Errorf("line warnings = %v / %v / %v", doc.Lines[0].Warnings, doc.Lines[1].Warnings, doc.Lines[2].Warnings)
	}
	if countOf(doc.Warnings, "source_changed") != 1 || countOf(doc.Warnings, "source_not_invoiceable") != 1 {
		t.Errorf("document warnings = %v, want each once", doc.Warnings)
	}
	for _, r := range f.reads() {
		if r.locked {
			t.Errorf("%s was read under a lock", r.method)
		}
		if !r.until.IsZero() {
			t.Errorf("%s was read until %v; a held source is read by id with no bound", r.method, r.until)
		}
	}
	var calls []string
	for _, call := range contractCalls.by(userID) {
		calls = append(calls, call.method)
	}
	if !slices.Equal(calls, []string{
		"Directory.BillingProfile", "BillableMilestones.BillableMilestones", "BillableExpenses.BillableExpenses", "BillableHours.BillableHours",
		"Directory.BillingProfile", "BillableMilestones.BillableMilestones", "BillableExpenses.BillableExpenses", "BillableHours.BillableHours",
	}) {
		t.Errorf("contract calls = %v", calls)
	}

	// A milestone's amount moved at the same revision — a fixed price edited
	// under a percent milestone — is a change too.
	f.putMilestone(contracts.BillableMilestone{ID: milestone, Revision: 1, ProjectID: project41, Name: "Fase 1",
		ReadyAt: time.Date(2026, 9, 4, 22, 30, 0, 0, time.UTC), Amount: "10500.00", Currency: "NOK"})
	if doc := getWork(t, c, d.ID); !slices.Equal(doc.Lines[2].Warnings, []string{"source_changed"}) {
		t.Errorf("a milestone at 10 500 = %v, want source_changed", doc.Lines[2].Warnings)
	}

	// A reader without invoices:create, and the list, read nothing.
	n := len(f.reads())
	if doc := getWork(t, h.SignIn(t, "invoices:access"), d.ID); len(doc.Warnings) != 0 {
		t.Errorf("a reader is told %v", doc.Warnings)
	}
	if res := c.Do(http.MethodGet, invoicesPath, nil); res.Status != http.StatusOK {
		t.Fatalf("list = %d", res.Status)
	}
	if got := len(f.reads()); got != n {
		t.Errorf("reads after a reader's GET and the list = %d, want %d", got, n)
	}

	// An issued document and a credit-note draft read nothing.
	other := createDraft(t, h, draftBody(customerAcme, line("Timer", 1, 1000, vat25)))
	plantSource(t, h, other.ID, planted{position: 1, kind: "time.entry", id: 503, revision: 1, quantity: "1", amount: "1000", date: "2026-09-04"})
	flipIssued(t, h, other.ID, 1)
	credit := creditDraft(t, h, other.ID)
	for _, id := range []int64{other.ID, credit.ID} {
		if doc := getWork(t, c, id); slices.Contains(doc.Warnings, "source_not_invoiceable") {
			t.Errorf("document %d judged fresh: %v", id, doc.Warnings)
		}
	}
	if got := len(f.reads()); got != n {
		t.Errorf("reads after an issued document and a credit draft = %d, want %d", got, n)
	}

	// A failing read is logged; the draft still reads, unjudged.
	f.fail(errors.New("time is down"))
	if doc := getWork(t, c, d.ID); slices.Contains(doc.Warnings, "source_changed") {
		t.Errorf("a failed read still judged: %v", doc.Warnings)
	}
	if !strings.Contains(h.Logs(), "could not be judged fresh") {
		t.Error("a failed freshness read is not logged")
	}

	// Only Time composed: the expense and the milestone are not judged.
	only := newFakeBillable()
	h2 := newHarness(t, modtest.WithBillableHours(only))
	d2 := sourcedDraft(t, h2)
	freshBillable(only)
	if doc := getWork(t, creator(t, h2), d2.ID); len(doc.Warnings) != 0 {
		t.Errorf("with only Time composed = %v, want nothing judged but the hours", doc.Warnings)
	}
}

// An expense's revision is display-only: a reimbursement moves it without
// touching what is billed. Its bill amount, project, currency and kind are
// what freshness judges (D1, D2).
func TestSources_AReimbursementIsNoChangeOnGet(t *testing.T) {
	t.Parallel()
	f := newFakeBillable()
	h := newHarness(t, f.options()...)
	d := sourcedDraft(t, h)
	freshBillable(f)
	c := creator(t, h)

	e := f.expenses[mileage]
	e.Revision = 9
	f.putExpense(e)
	if doc := getWork(t, c, d.ID); len(doc.Warnings) != 0 {
		t.Errorf("a reimbursed expense = %v, want no warning", doc.Warnings)
	}
	for _, change := range []func(*contracts.BillableExpense){
		func(e *contracts.BillableExpense) { e.BillAmount = "450.01" },
		func(e *contracts.BillableExpense) { e.Kind = "outlay" },
		func(e *contracts.BillableExpense) { e.ProjectID = 42 },
		func(e *contracts.BillableExpense) { e.Currency = "SEK" },
	} {
		changed := e
		change(&changed)
		f.putExpense(changed)
		if doc := getWork(t, c, d.ID); !slices.Equal(doc.Lines[1].Warnings, []string{"source_changed"}) {
			t.Errorf("expense %+v = %v, want source_changed", changed, doc.Lines[1].Warnings)
		}
	}
	// Amounts by value: 450 and "450.00000" are one amount.
	e.BillAmount = "450.00000"
	f.putExpense(e)
	if doc := getWork(t, c, d.ID); len(doc.Warnings) != 0 {
		t.Errorf("the same amount as other text = %v", doc.Warnings)
	}
}

// A create holds no work: a line naming sources, or refreshSources, is a
// 400; [] is none (D2).
func TestSources_APostWithSourcesIsA400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, hoursLine(1, 1000, refHourOne)))
	if p := problemOf(t, res); res.Status != http.StatusBadRequest || len(p.Errors["lines[0].sources"]) == 0 ||
		!strings.Contains(p.Errors["lines[0].sources"][0], "uninvoiced view") {
		t.Errorf("POST with sources = %d %s, want 400 on lines[0].sources", res.Status, res.Body)
	}
	body := draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25))
	body["refreshSources"] = true
	if res := c.Do(http.MethodPost, invoicesPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["refreshSources"]) == 0 {
		t.Errorf("POST with refreshSources = %d %s, want 400", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, sourcedLine(line("Rådgivning", 1, 1000, vat25), []refJSON{}))); res.Status != http.StatusCreated {
		t.Errorf("POST with no sources named = %d %s, want 201", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.line_sources`); n != 0 {
		t.Errorf("line sources after the creates = %d, want none", n)
	}
}

// refreshSources reads the held work before the save's transaction and takes
// each source's current facts; what is no longer invoiceable is dropped and
// named (D2).
func TestSources_RefreshSourcesTakesNewRevisionsAndDrops(t *testing.T) {
	t.Parallel()
	f := newFakeBillable()
	h := newHarness(t, f.options()...)
	d := sourcedDraft(t, h)
	freshBillable(f)

	hour := f.hours[hourOne]
	hour.Revision, hour.HoursHundredths, hour.Amount = 5, 425, "5100.00"
	f.putHour(hour)
	e := f.expenses[mileage]
	e.Revision, e.DistanceKm, e.BillAmount = 9, ptrTo("92"), "460.00"
	f.putExpense(e)
	f.mu.Lock()
	delete(f.milestones, milestone)
	f.mu.Unlock()

	body := sourcedBody(d, customerAcme, theSameLines()...)
	body["refreshSources"] = true
	res, saved := putWork(t, h, d.ID, body)
	if res.Status != http.StatusOK {
		t.Fatalf("refresh = %d %s", res.Status, res.Body)
	}
	rows := heldRows(t, h, d.ID)
	slices.Sort(rows)
	want := []string{
		"expenses.entry:601@2 r9 q92 a460 p41 2026-09-03 mileage held",
		"time.entry:501@1 r5 q4.25 a5100 p41 2026-09-01 - held",
		"time.entry:502@1 r1 q3.5 a4200 p41 2026-09-02 - held",
	}
	if !slices.Equal(rows, want) {
		t.Errorf("held after the refresh = %v, want %v", rows, want)
	}
	if got := refsOf(saved.ReleasedSources); !slices.Equal(got, []string{"projects.milestone:701"}) || !slices.Contains(saved.Warnings, "sources_released") {
		t.Errorf("released = %v, warnings %v", got, saved.Warnings)
	}
	if !slices.Equal(saved.Lines[0].Warnings, []string{"line_differs_from_sources"}) {
		t.Errorf("line 1 = %v; its sources now sum to 9 300", saved.Lines[0].Warnings)
	}
	for _, r := range f.reads() {
		if r.locked {
			t.Errorf("%s read under the save's lock", r.method)
		}
	}
	if doc := getWork(t, creator(t, h), d.ID); slices.Contains(doc.Warnings, "source_changed") {
		t.Errorf("after the refresh GET warns %v", doc.Warnings)
	}

	// An expense now billed in SEK is not taken into a NOK draft: dropped and
	// named, never refreshed into "fresh".
	e.Currency = "SEK"
	f.putExpense(e)
	body = draftBody(customerAcme, theSameLines()[:2]...)
	body["paymentTermsDays"], body["revision"], body["refreshSources"] = 30, saved.Revision, true
	res, saved = putWork(t, h, d.ID, body)
	if res.Status != http.StatusOK {
		t.Fatalf("refresh with an expense in SEK = %d %s", res.Status, res.Body)
	}
	if got := refsOf(saved.ReleasedSources); !slices.Equal(got, []string{"expenses.entry:601"}) {
		t.Errorf("released = %v, want the expense now in SEK", got)
	}
	if rows := heldRows(t, h, d.ID); len(rows) != 2 || strings.Contains(strings.Join(rows, " "), "expenses.entry") {
		t.Errorf("held = %v, want only the two hours", rows)
	}

	plain := createDraft(t, h, draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25)))
	body = sourcedBody(plain, customerAcme, line("Rådgivning", 1, 1000, vat25))
	body["refreshSources"] = true
	if res, _ := putWork(t, h, plain.ID, body); res.Status != http.StatusOK {
		t.Errorf("a refresh of a draft holding nothing = %d %s, want 200", res.Status, res.Body)
	}
}

// A save that changes the held work between a refresh's read and its lock
// makes the refresh's judgment moot: 409 invoice_changed, nothing written
// (plan reading 32).
func TestSources_ASaveBetweenRefreshReadAndLockIsInvoiceChanged(t *testing.T) {
	t.Parallel()
	f := newFakeBillable()
	h := newHarness(t, f.options()...)
	d := sourcedDraft(t, h)
	freshBillable(f)

	other := creator(t, h)
	f.afterNextRead(func() {
		if res := other.Do(http.MethodPut, invoicePath(d.ID), sourcedBody(d, customerAcme, theSameLines()[:2]...)); res.Status != http.StatusOK {
			t.Errorf("the slipped save = %d %s", res.Status, res.Body)
		}
	})
	body := sourcedBody(d, customerAcme, theSameLines()...)
	body["refreshSources"] = true
	res, _ := putWork(t, h, d.ID, body)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_changed" {
		t.Fatalf("refresh around a slipped save = %d %s, want 409 invoice_changed", res.Status, res.Body)
	}
	if n := len(heldRows(t, h, d.ID)); n != 3 {
		t.Errorf("held = %d, want the slipped save's three", n)
	}
}

// The sources block and each line's sources are the document's own rows —
// its count by state — on a draft, on an issued invoice and after a release,
// with no read of any source module (D2, D17).
func TestSources_TheBlockIsAnsweredFromOwnRows(t *testing.T) {
	t.Parallel()
	f := newFakeBillable()
	f.fail(errors.New("no module answers"))
	h := newHarness(t, f.options()...)
	d := sourcedDraft(t, h)
	reader := h.SignIn(t, "invoices:access")

	doc := getWork(t, reader, d.ID)
	if doc.Sources == nil || *doc.Sources != (struct {
		Count    int32 `json:"count"`
		Held     int32 `json:"held"`
		Invoiced int32 `json:"invoiced"`
		Released int32 `json:"released"`
	}{4, 4, 0, 0}) {
		t.Errorf("draft block = %+v, want four held", doc.Sources)
	}
	if got := doc.Lines[1].Sources; len(got) != 1 || got[0] != (sourceJSON{Kind: "expenses.entry", ID: mileage, ProjectID: project41,
		Date: "2026-09-03", Quantity: 90, Amount: 450, State: "held"}) {
		t.Errorf("line 2's sources = %+v", got)
	}
	if got := doc.Lines[2].Sources; len(got) != 1 || got[0].Kind != "projects.milestone" || got[0].Amount != 10000 {
		t.Errorf("line 3's sources = %+v", got)
	}

	flipIssued(t, h, d.ID, 1)
	h.Exec(t, `UPDATE invoices.line_sources SET state = 'released' WHERE invoice_id = $1 AND source_kind = 'projects.milestone'`, d.ID)
	doc = getWork(t, creator(t, h), d.ID)
	if doc.Sources == nil || doc.Sources.Count != 4 || doc.Sources.Invoiced != 3 || doc.Sources.Released != 1 || doc.Sources.Held != 0 {
		t.Errorf("issued block = %+v, want three invoiced and one released", doc.Sources)
	}
	if got := doc.Lines[2].Sources; len(got) != 1 || got[0].State != "released" {
		t.Errorf("the released line's sources = %+v", got)
	}
	if got := doc.Lines[0].Sources; len(got) != 2 || got[0].State != "invoiced" || got[1].ID != hourTwo {
		t.Errorf("line 1's sources = %+v", got)
	}
	if len(f.reads()) != 0 {
		t.Errorf("reads = %v, want none: the block is the document's own rows", f.reads())
	}
}

// A credit note credits what was billed and adds no work: a line naming a
// source, or refreshSources, is a 400 on the field; [] is none (D2, D16).
func TestSources_ACreditNoteDraftRefusesSourcesAndRefresh(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	original := createDraft(t, h, draftBody(customerAcme, line("Timer", 1, 1000, vat25)))
	plantSource(t, h, original.ID, planted{position: 1, kind: "time.entry", id: 503, revision: 1, quantity: "1", amount: "1000", date: "2026-09-04"})
	flipIssued(t, h, original.ID, 1)
	c := creditDraft(t, h, original.ID)

	named := creditLine(c.Lines[0])
	named["sources"] = []refJSON{ref("time.entry", 503)}
	res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), creditBody(c, named))
	if p := problemOf(t, res); res.Status != http.StatusBadRequest || !slices.Equal(p.Errors["lines[0].sources"], []string{"A credit note adds no work"}) {
		t.Errorf("a credit line naming a source = %d %s, want 400 on lines[0].sources", res.Status, res.Body)
	}
	refresh := creditBody(c, creditLine(c.Lines[0]))
	refresh["refreshSources"] = true
	res = creator(t, h).Do(http.MethodPut, invoicePath(c.ID), refresh)
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["refreshSources"]) == 0 {
		t.Errorf("a credit draft with refreshSources = %d %s, want 400 on refreshSources", res.Status, res.Body)
	}
	none := creditLine(c.Lines[0])
	none["sources"] = []refJSON{}
	saveCredit(t, h, c, creditBody(c, none))
	if n := h.Count(t, `SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1`, c.ID); n != 0 {
		t.Errorf("the credit draft holds %d sources, want none", n)
	}
}
