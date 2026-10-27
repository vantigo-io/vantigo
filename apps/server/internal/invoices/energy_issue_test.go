package invoices_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A draft that bills Energy's settlements once it exists (energy consumption
// billing design D7, invoices from consumption plan readings 2, 6 and 7):
// a settlement held over several lines, one after another; judged fresh on
// GET on every line of its span; carried, shrunk and released by a save;
// rebuilt by a refresh; stamped at the issue by Energy's holder, last, with
// no project directory; refused when it is now another customer's; a
// correction's negative line issued; and released by a credit note only once
// every line of its span is returned. The drafts are made through the API
// and their rows planted by SQL, as the sources tests do, so these tests
// stand without the From consumption wizard.

const (
	settlementA = 9001
	settlementB = 9002
	correctionC = 9003
	gsrnA       = "707057500000000017"
	gsrnB       = "707057500000000024"
	// customerOther is a customer no draft here bills.
	customerOther = 2999
)

var (
	kindSettlement = contracts.WorkSourceSettlement
	refSettlementA = ref("energy.settlement", settlementA)
	refSettlementB = ref("energy.settlement", settlementB)
	refCorrectionC = ref("energy.settlement", correctionC)
)

// energySourceJSON is one of a line's sources as an energy draft is read.
type energySourceJSON struct {
	Kind      string  `json:"kind"`
	ID        int64   `json:"id"`
	ProjectID *int32  `json:"projectId"`
	Subkind   *string `json:"subkind"`
	LineCount *int32  `json:"lineCount"`
	Gsrn      *string `json:"gsrn"`
	State     string  `json:"state"`
}

// energyDocJSON is the part of a document these tests read.
type energyDocJSON struct {
	ID         int64    `json:"id"`
	Revision   int32    `json:"revision"`
	Warnings   []string `json:"warnings"`
	GrossTotal float64  `json:"grossTotal"`
	Lines      []struct {
		Position    int32              `json:"position"`
		Description string             `json:"description"`
		Quantity    float64            `json:"quantity"`
		UnitPrice   float64            `json:"unitPrice"`
		LineNet     float64            `json:"lineNet"`
		VatCodeID   int32              `json:"vatCodeId"`
		Sources     []energySourceJSON `json:"sources"`
		Warnings    []string           `json:"warnings"`
	} `json:"lines"`
	ReleasedSources []refJSON `json:"releasedSources"`
}

// energyReady is an installation billing Energy's settlements: a complete
// seller, every kind's holder, Consumption as f answers it, and opts — no
// project directory unless opts compose one.
func energyReady(t *testing.T, f *fakeConsumption, holders *fakeHolders, opts ...modtest.Option) *harness {
	t.Helper()
	all := append(holders.options(), f.options()...)
	h := newHarness(t, append(all, opts...)...)
	saveSeller(t, h, completeSeller(1))
	return h
}

// settlementOf is September 2026's settlement id of Acme on gsrn, as Energy
// holds it now.
func septemberOf(id int64, gsrn string) contracts.Settlement {
	return monthSettlement(id, customerAcme, int32(id%1000), gsrn, 2026, time.September, contracts.VATHintStandard)
}

// correctionOf is a correction of original lowering its bill by 100: 50 kWh
// less, one line.
func lowerCorrectionOf(id int64, original contracts.Settlement) contracts.Settlement {
	c := original
	c.ID, c.Kind, c.CorrectsID, c.KWh, c.Amount = id, contracts.SettlementCorrection, &original.ID, "-50.000", "-100.00"
	c.Lines = []contracts.SettlementLine{{Kind: "spot_energy", Text: "Spot NO4", Quantity: "-50.000", Unit: contracts.SettlementUnitKWh,
		UnitPrice: "2.000000", Amount: "-100.00", VATHint: contracts.VATHintStandard}}
	return c
}

// settlementLines is a request's lines billing st, one per settlement line
// at one piece of its amount, each naming refs (nil leaves sources out).
func linesBilling(st contracts.Settlement, refs []refJSON) []map[string]any {
	out := []map[string]any{}
	for _, sl := range st.Lines {
		amount := decimalFloat(sl.Amount)
		l := line(sl.Text, 1, amount, vat25)
		if amount < 0 {
			l = line(sl.Text, -1, -amount, vat25)
		}
		out = append(out, sourcedLine(l, refs))
	}
	return out
}

// plantSettlement holds st on invoiceID's lines from position, count of them,
// as the wizard writes the row.
func plantSettlement(t *testing.T, h *harness, invoiceID int64, position, count int32, st contracts.Settlement) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
			project_id, quantity, amount, currency, source_date, point_gsrn, point_address, price_area, contract_name,
			period_from, estimated, line_count)
		SELECT l.id, l.invoice_id, 'energy.settlement', $3, $4, $5, NULL, $6::numeric, $7::numeric, $8, $9::date,
			$10, $11, $12, $13, $14::date, false, $15
		FROM invoices.lines l WHERE l.invoice_id = $1 AND l.position = $2`,
		invoiceID, position, st.ID, st.Revision, st.Kind, st.KWh, st.Amount, st.Currency,
		st.PeriodEnd.AddDate(0, 0, -1).Format(time.DateOnly), st.GSRN, st.Address, st.PriceArea, st.ContractName,
		st.PeriodStart.Format(time.DateOnly), count)
}

// energyDraft is an Acme draft of every settlement's lines in turn, each
// settlement held on its own; a correction's negative line is made negative
// by SQL, as the API refuses it on a draft that holds nothing yet.
func energyDraft(t *testing.T, h *harness, settlements ...contracts.Settlement) invoiceJSON {
	t.Helper()
	var lines []map[string]any
	for _, st := range settlements {
		for _, l := range linesBilling(st, nil) {
			if q := l["quantity"].(float64); q < 0 {
				l["quantity"] = -q
			}
			lines = append(lines, l)
		}
	}
	d := createDraft(t, h, draftBody(customerAcme, lines...))
	position := int32(1)
	for _, st := range settlements {
		plantSettlement(t, h, d.ID, position, int32(len(st.Lines)), st)
		if st.Kind == contracts.SettlementCorrection {
			h.Exec(t, `
				UPDATE invoices.lines SET quantity = -quantity, line_gross = -line_gross, line_net = -line_net, correction = true
				WHERE invoice_id = $1 AND position >= $2 AND position < $3`, d.ID, position, position+int32(len(st.Lines)))
		}
		position += int32(len(st.Lines))
	}
	return d
}

func decimalFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return f
}

// energyDoc reads a document as these tests do.
func energyDoc(t *testing.T, res *modtest.Response) energyDocJSON {
	t.Helper()
	var doc energyDocJSON
	res.JSON(&doc)
	return doc
}

// heldSettlementRows is a document's settlement rows as "id@position×count rN state".
func heldSettlementRows(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(format('%s@%s×%s r%s %s', s.source_id, l.position, s.line_count, s.source_revision, s.state)
			ORDER BY s.source_id), '{}')
		FROM invoices.line_sources s JOIN invoices.lines l ON l.id = s.line_id
		WHERE s.invoice_id = $1 AND s.source_kind = 'energy.settlement'`, id)
}

// A held settlement is judged on GET through Consumption, and each verdict is
// on every line of its span: fresh says nothing; another revision or amount
// is source_changed; invoiced, blocked or gone is source_not_invoiceable. A
// settlement listed on each of its lines, its lines' nets summing to its
// amount, does not differ.
func TestEnergy_FreshnessOnEveryLineOfASpan(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, st)
	c := creator(t, h)

	read := func() energyDocJSON {
		t.Helper()
		res := c.Do(http.MethodGet, invoicePath(d.ID), nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET = %d %s", res.Status, res.Body)
		}
		return energyDoc(t, res)
	}
	fresh := read()
	if len(fresh.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(fresh.Lines))
	}
	for _, l := range fresh.Lines {
		if len(l.Sources) != 1 || l.Sources[0].ID != settlementA || l.Sources[0].LineCount == nil || *l.Sources[0].LineCount != 3 ||
			l.Sources[0].ProjectID != nil || l.Sources[0].Gsrn == nil || *l.Sources[0].Gsrn != gsrnA {
			t.Errorf("line %d's sources = %+v, want settlement A over 3 lines, its point and no project", l.Position, l.Sources)
		}
		if len(l.Warnings) != 0 {
			t.Errorf("fresh: line %d warns %v, want nothing", l.Position, l.Warnings)
		}
	}

	for _, c := range []struct {
		name   string
		change func(*contracts.Settlement)
		want   string
	}{
		{"another revision", func(s *contracts.Settlement) { s.Revision = 2 }, "source_changed"},
		{"another amount", func(s *contracts.Settlement) { s.Amount = "1200.00" }, "source_changed"},
		{"invoiced", func(s *contracts.Settlement) { s.State = contracts.SettlementInvoiced }, "source_not_invoiceable"},
		{"blocked", func(s *contracts.Settlement) { s.Blocked = contracts.SettlementPricePreliminary }, "source_not_invoiceable"},
	} {
		now := st
		c.change(&now)
		f.put(now)
		doc := read()
		for _, l := range doc.Lines {
			if !slices.Contains(l.Warnings, c.want) {
				t.Errorf("%s: line %d warns %v, want %s", c.name, l.Position, l.Warnings, c.want)
			}
		}
		if !slices.Contains(doc.Warnings, c.want) {
			t.Errorf("%s: the document warns %v, want %s", c.name, doc.Warnings, c.want)
		}
	}
	f.drop(settlementA)
	for _, l := range read().Lines {
		if !slices.Contains(l.Warnings, "source_not_invoiceable") {
			t.Errorf("gone: line %d warns %v, want source_not_invoiceable", l.Position, l.Warnings)
		}
	}
}

// A line's net that, with its span's, misses the settlement's amount warns
// line_differs_from_sources on every line of the span.
func TestEnergy_ASpanThatDiffersWarnsOnEachOfItsLines(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, st)
	lines := linesBilling(st, []refJSON{refSettlementA})
	lines[2]["unitPrice"] = 80.0 // 85.04 billed as 80
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	doc := energyDoc(t, res)
	for _, l := range doc.Lines {
		if !slices.Contains(l.Warnings, "line_differs_from_sources") {
			t.Errorf("line %d warns %v, want line_differs_from_sources", l.Position, l.Warnings)
		}
	}
}

// A save carries a settlement named on each of its lines, one after another,
// as one row under the first with their number; the unit price of a line
// naming a settlement may have eight decimals, any other line's four. A
// settlement whose lines are split by another line is a 400; a save naming
// it on fewer lines keeps it with a smaller count, one naming it on none
// releases it.
func TestEnergy_ASaveCarriesShrinksAndReleasesASpan(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, st)

	lines := linesBilling(st, []refJSON{refSettlementA})
	lines[0]["quantity"], lines[0]["unitPrice"] = 1500.25, 0.51234512
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT the span = %d %s, want 200", res.Status, res.Body)
	}
	saved := energyDoc(t, res)
	if got := heldSettlementRows(t, h, d.ID); !slices.Equal(got, []string{"9001@1×3 r1 held"}) {
		t.Errorf("rows = %v, want 9001@1×3 r1 held", got)
	}
	if saved.Lines[0].UnitPrice != 0.51234512 {
		t.Errorf("line 1's price = %v, want 0.51234512", saved.Lines[0].UnitPrice)
	}

	// Eight decimals on a line naming no settlement are refused.
	plain := line("Gebyr", 1, 0.12345, vat25)
	plain["sources"] = []refJSON{}
	res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, append(linesBilling(st, []refJSON{refSettlementA}), plain)...))
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["lines[3].unitPrice"]) == 0 {
		t.Errorf("five decimals on a plain line = %d %s, want 400 on lines[3].unitPrice", res.Status, res.Body)
	}

	// Split by another line.
	split := linesBilling(st, []refJSON{refSettlementA})
	split = []map[string]any{split[0], plain, split[1], split[2]}
	plain["unitPrice"] = 10.0
	res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, split...))
	if res.Status != http.StatusBadRequest ||
		!slices.Contains(problemOf(t, res).Errors["lines[2].sources"], "A settlement's lines stay together, one after another") {
		t.Errorf("a split span = %d %s, want 400 on lines[2].sources", res.Status, res.Body)
	}
	// Named twice on one line.
	twice := linesBilling(st, []refJSON{refSettlementA, refSettlementA})
	if res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, twice...)); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["lines[0].sources"]) == 0 {
		t.Errorf("a settlement twice on a line = %d %s, want 400 on lines[0].sources", res.Status, res.Body)
	}

	// Shrunk: two of its lines, the third billing nothing.
	shrunk := linesBilling(st, []refJSON{refSettlementA})
	shrunk[2]["sources"] = []refJSON{}
	res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, shrunk...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT a shrunk span = %d %s, want 200", res.Status, res.Body)
	}
	saved = energyDoc(t, res)
	if got := heldSettlementRows(t, h, d.ID); !slices.Equal(got, []string{"9001@1×2 r1 held"}) {
		t.Errorf("rows = %v, want 9001@1×2 r1 held", got)
	}
	if len(saved.ReleasedSources) != 0 {
		t.Errorf("a shrunk span released %v, want nothing", saved.ReleasedSources)
	}

	// Released: no line names it.
	none := linesBilling(st, []refJSON{})
	res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, none...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT without the settlement = %d %s, want 200", res.Status, res.Body)
	}
	saved = energyDoc(t, res)
	if got := refsOf(saved.ReleasedSources); !slices.Equal(got, []string{"energy.settlement:9001"}) || !slices.Contains(saved.Warnings, "sources_released") {
		t.Errorf("released = %v, warnings %v, want energy.settlement:9001 and sources_released", got, saved.Warnings)
	}
	if got := heldSettlementRows(t, h, d.ID); len(got) != 0 {
		t.Errorf("rows = %v, want none", got)
	}
}

// asInvoice is doc as sourcedBody takes it: its id and revision.
func (doc energyDocJSON) asInvoice() invoiceJSON {
	return invoiceJSON{ID: doc.ID, Revision: doc.Revision}
}

// refreshSources builds a held settlement's lines anew from what Energy
// answers now — their number may change — in the buyer's language at the
// settings' energy code, and takes its revision; a settlement no longer
// invoiceable is dropped as work is, its lines kept. Every read is made
// before the save's lock.
func TestEnergy_RefreshRebuildsTheLines(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a, b := septemberOf(settlementA, gsrnA), septemberOf(settlementB, gsrnB)
	f.put(a)
	f.put(b)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, a, b)

	now := a
	now.Revision, now.Amount = 2, "853.69"
	now.Lines = slices.Clone(a.Lines[:2])
	now.Lines[1].Amount = "85.04"
	now.Lines[1] = contracts.SettlementLine{Kind: "monthly_fee", Text: "Fastledd", Quantity: "1.000", Unit: contracts.SettlementUnitMonth,
		UnitPrice: "85.040000", Amount: "85.04", VATHint: contracts.VATHintStandard}
	f.put(now)
	gone := b
	gone.State = contracts.SettlementInvoiced
	f.put(gone)

	lines := append(linesBilling(a, []refJSON{refSettlementA}), linesBilling(b, []refJSON{refSettlementB})...)
	body := sourcedBody(d, customerAcme, lines...)
	body["refreshSources"] = true
	res, _ := putWork(t, h, d.ID, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT refreshSources = %d %s, want 200", res.Status, res.Body)
	}
	doc := energyDoc(t, res)
	if len(doc.Lines) != 5 {
		t.Fatalf("lines = %d, want 2 rebuilt and B's 3 kept", len(doc.Lines))
	}
	for _, l := range doc.Lines[:2] {
		if !strings.Contains(l.Description, gsrnA) || !strings.Contains(l.Description, "september 2026") || l.VatCodeID != vat25 {
			t.Errorf("rebuilt line %d = %q at code %d, want the text built for %s in Norwegian at the energy code", l.Position, l.Description, l.VatCodeID, gsrnA)
		}
	}
	if doc.Lines[0].Quantity != 1500.25 || doc.Lines[1].LineNet != 85.04 {
		t.Errorf("rebuilt lines = %+v, want Energy's quantities and amounts", doc.Lines[:2])
	}
	if got := heldSettlementRows(t, h, d.ID); !slices.Equal(got, []string{"9001@1×2 r2 held"}) {
		t.Errorf("rows = %v, want 9001@1×2 r2 held", got)
	}
	if got := refsOf(doc.ReleasedSources); !slices.Equal(got, []string{"energy.settlement:9002"}) {
		t.Errorf("released = %v, want energy.settlement:9002", got)
	}
	for _, c := range f.reads() {
		if c.locked {
			t.Errorf("%s was called under the save's lock", c.method)
		}
	}
}

// An energy-only draft is issued with no project directory: Energy's holder
// is handed each settlement with its revision, currency and amount, no
// project and no expense kind, and its rows move to invoiced.
func TestEnergy_TheIssueNeedsNoProjects(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	holders := newFakeHolders()
	h := energyReady(t, f, holders)
	d := energyDraft(t, h, st)

	inv := issued(t, h, d.ID)
	calls := holders.callsOf(kindSettlement)
	if len(calls) != 1 || calls[0].op != "mark" || calls[0].ref.Number != *inv.Number {
		t.Fatalf("energy's holder calls = %+v, want one mark with number %d", calls, *inv.Number)
	}
	want := contracts.WorkSource{Kind: kindSettlement, ID: settlementA, Revision: 1, ProjectID: 0, Currency: "NOK", Amount: "1187.07000000"}
	if got := calls[0].sources; len(got) != 1 || got[0] != want {
		t.Errorf("handed %+v, want %+v", got, want)
	}
	if got := heldSettlementRows(t, h, d.ID); !slices.Equal(got, []string{"9001@1×3 r1 invoiced"}) {
		t.Errorf("rows = %v, want 9001@1×3 r1 invoiced", got)
	}
	if got := holders.calledOrder(); !slices.Equal(got, []contracts.WorkSourceKind{kindSettlement}) {
		t.Errorf("holders called = %v, want only energy's", got)
	}
}

// A draft billing work and a settlement calls Energy's holder last, after
// Projects', Expenses' and Time's — contracts.InvoicedWorkOrder.
func TestEnergy_TheSettlementsHolderIsCalledLast(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	holders := newFakeHolders()
	h := energyReady(t, f, holders, modtest.WithProjects(newFakeProjects()))
	lines := []map[string]any{line("Konsulenttimer", 7.5, 1200, vat25), line("Kjøregodtgjørelse", 1, 450, vat25), line("Milepæl", 1, 10000, vat25)}
	d := createDraft(t, h, draftBody(customerAcme, append(lines, linesBilling(st, nil)...)...))
	for _, p := range theFour() {
		plantSource(t, h, d.ID, p)
	}
	plantSettlement(t, h, d.ID, 4, 3, st)

	issued(t, h, d.ID)
	if got, want := holders.calledOrder(), append(slices.Clone(workKinds), kindSettlement); !slices.Equal(got, want) {
		t.Errorf("holders called = %v, want %v", got, want)
	}
	if got, want := contracts.InvoicedWorkOrder[len(contracts.InvoicedWorkOrder)-1], kindSettlement; got != want {
		t.Errorf("the lock order ends with %s, want %s", got, want)
	}
}

// A held settlement Energy now answers for another customer refuses the
// issue with source_customer_changed on its first line, one it no longer has
// with source_not_invoiceable — read before the transaction, so no number is
// taken and no holder is called.
func TestEnergy_SourceCustomerChangedBeforeANumber(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a, b := septemberOf(settlementA, gsrnA), septemberOf(settlementB, gsrnB)
	f.put(a)
	f.put(b)
	holders := newFakeHolders()
	h := energyReady(t, f, holders)
	d := energyDraft(t, h, a, b)

	moved := b
	moved.CustomerID = customerOther
	f.put(moved)
	res := issueWith(t, h, d.ID, "")
	p := problemOf(t, res)
	if res.Status != http.StatusConflict || p.Code != "source_customer_changed" || p.LinePosition == nil || *p.LinePosition != 4 ||
		p.SourceKind == nil || *p.SourceKind != "energy.settlement" || p.SourceID == nil || *p.SourceID != settlementB {
		t.Errorf("issue = %d %s, want 409 source_customer_changed on line 4 for settlement %d", res.Status, res.Body, settlementB)
	}
	f.drop(settlementB)
	res = issueWith(t, h, d.ID, "")
	if p := problemOf(t, res); res.Status != http.StatusConflict || p.Code != "source_not_invoiceable" {
		t.Errorf("issue = %d %s, want 409 source_not_invoiceable", res.Status, res.Body)
	}
	noHolderCalled(t, holders)
	if n := modtest.One[int64](t, h.Harness, `SELECT count(*) FROM invoices.invoices WHERE number IS NOT NULL`); n != 0 {
		t.Errorf("%d documents numbered, want none", n)
	}
}

// A line naming a held correction settlement may carry a negative quantity —
// derived from the work it names, never the request — and no discount; any
// other line may not be negative. The correction's negative net is issued,
// the document still above zero; a document its corrections bring to zero or
// below is warned and refused as invoice_total_not_positive.
func TestEnergy_ACorrectionLineIsIssuedNegative(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a := septemberOf(settlementA, gsrnA)
	c := lowerCorrectionOf(correctionC, a)
	c.GSRN = gsrnB // another point's, so both stay draft beside each other
	f.put(a)
	f.put(c)
	holders := newFakeHolders()
	h := energyReady(t, f, holders)

	// The correction's line is planted positive and made negative by a save.
	d := createDraft(t, h, draftBody(customerAcme, append(linesBilling(a, nil), line("Korreksjon", 1, 100, vat25))...))
	plantSettlement(t, h, d.ID, 1, 3, a)
	plantSettlement(t, h, d.ID, 4, 1, c)
	lines := append(linesBilling(a, []refJSON{refSettlementA}), linesBilling(c, []refJSON{refCorrectionC})...)

	negativeA := slices.Clone(lines)
	negativeA[0] = sourcedLine(line("Spot NO4", -1, 768.65, vat25), []refJSON{refSettlementA})
	if res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, negativeA...)); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["lines[0].quantity"]) == 0 {
		t.Errorf("a negative regular line = %d %s, want 400 on lines[0].quantity", res.Status, res.Body)
	}
	discounted := slices.Clone(lines)
	discounted[3] = sourcedLine(line("Korreksjon", -1, 100, vat25), []refJSON{refCorrectionC})
	discounted[3]["discountPercent"] = 10.0
	if res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, discounted...)); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["lines[3].discountPercent"]) == 0 {
		t.Errorf("a discounted correction = %d %s, want 400 on lines[3].discountPercent", res.Status, res.Body)
	}
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT a negative correction = %d %s, want 200", res.Status, res.Body)
	}
	if got := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(format('%s:%s:%s', position, quantity::float8, correction) ORDER BY position) FROM invoices.lines WHERE invoice_id = $1`,
		d.ID); !slices.Equal(got, []string{"1:1:f", "2:1:f", "3:1:f", "4:-1:t"}) {
		t.Errorf("lines = %v, want the fourth -1 and a correction", got)
	}

	inv := issued(t, h, d.ID)
	if inv.Lines[3].LineNet != -100 || inv.NetTotal != 1087.07 {
		t.Errorf("issued line 4 net %v, total %v; want -100 and 1087.07", inv.Lines[3].LineNet, inv.NetTotal)
	}
	if got := idsOf(holders.callsOf(kindSettlement)[0].sources); !slices.Equal(got, []int64{settlementA, correctionC}) {
		t.Errorf("energy's holder was handed %v, want both by id", got)
	}

	// A correction alone takes the document below zero.
	alone := lowerCorrectionOf(correctionC+1, a)
	f.put(alone)
	d2 := createDraft(t, h, draftBody(customerAcme, line("Korreksjon", 1, 100, vat25)))
	plantSettlement(t, h, d2.ID, 1, 1, alone)
	res, _ = putWork(t, h, d2.ID, sourcedBody(d2, customerAcme, linesBilling(alone, []refJSON{ref("energy.settlement", alone.ID)})...))
	if res.Status != http.StatusOK || !slices.Contains(energyDoc(t, res).Warnings, "invoice_total_not_positive") {
		t.Errorf("PUT a correction alone = %d %s, want 200 warning invoice_total_not_positive", res.Status, res.Body)
	}
	res = issueWith(t, h, d2.ID, "")
	if p := problemOf(t, res); res.Status != http.StatusConflict || p.Code != "invoice_total_not_positive" {
		t.Errorf("issue a correction alone = %d %s, want 409 invoice_total_not_positive", res.Status, res.Body)
	}
}

// A credit note releases a settlement billed over three lines only once all
// three are returned in full: one leaving out a line releases nothing; the
// next returning that line releases it, its line_releases row naming that
// note's credit line. A credit of a correction line is negative and carries
// the flag, as a deduction's credit does.
func TestEnergy_ACreditNoteReleasesOnlyWhenEveryLineIsReturned(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a := septemberOf(settlementA, gsrnA)
	f.put(a)
	holders := newFakeHolders()
	h := energyReady(t, f, holders)
	inv := issued(t, h, energyDraft(t, h, a).ID)

	first := creditOf(t, h, inv.ID, map[int32]float64{2: 0})
	issued(t, h, first.ID)
	if got := stateBySource(t, h, inv.ID); got[settlementA] != "invoiced" {
		t.Errorf("after two of three lines: %v, want settlement A invoiced", got)
	}
	if calls := holders.callsOf(kindSettlement); len(calls) != 1 {
		t.Errorf("energy's holder had %d calls, want only the mark", len(calls))
	}

	second := creditDraft(t, h, inv.ID)
	if len(second.Lines) != 3 {
		t.Fatalf("second credit draft lines = %d", len(second.Lines))
	}
	second = saveCredit(t, h, second, creditBody(second, creditLine(second.Lines[1])))
	if got := getRelease(t, h, second.ID); got.Sources == nil || !slices.Equal(refsOf(got.Sources.WouldRelease), []string{"energy.settlement:9001"}) {
		t.Errorf("the second draft would release %+v, want settlement A", got.Sources)
	}
	issued(t, h, second.ID)
	if got := stateBySource(t, h, inv.ID); got[settlementA] != "released" {
		t.Errorf("after the last line: %v, want settlement A released", got)
	}
	if got := releaseRows(t, h, second.ID); !slices.Equal(got, []string{"1:energy.settlement:9001"}) {
		t.Errorf("line_releases = %v, want the second note's line 1", got)
	}
	if got := releasedIDs(holders); len(got) != 0 {
		// releaseCalls reads the work kinds only; energy's is read here.
		t.Errorf("work released = %v, want none", got)
	}
	if calls := holders.callsOf(kindSettlement); len(calls) != 2 || calls[1].op != "release" {
		t.Errorf("energy's holder calls = %+v, want a mark and a release", calls)
	}
}

// A credit of a correction line is negative and is a correction itself, so
// the credit note's issue gives the correction back with the consumption.
func TestEnergy_ACreditOfACorrectionLineIsNegative(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a := septemberOf(settlementA, gsrnA)
	c := lowerCorrectionOf(correctionC, a)
	c.GSRN = gsrnB
	f.put(a)
	f.put(c)
	h := energyReady(t, f, newFakeHolders())
	inv := issued(t, h, energyDraft(t, h, a, c).ID)

	credit := creditDraft(t, h, inv.ID)
	if got := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(format('%s:%s', quantity::float8, correction) ORDER BY position) FROM invoices.lines WHERE invoice_id = $1`,
		credit.ID); !slices.Equal(got, []string{"1:f", "1:f", "1:f", "-1:t"}) {
		t.Errorf("the credit draft's lines = %v, want the fourth -1 and a correction", got)
	}
	lines := []map[string]any{}
	for _, l := range credit.Lines {
		lines = append(lines, creditLine(l))
	}
	positive := slices.Clone(lines)
	positive[3] = creditLine(credit.Lines[3])
	positive[3]["quantity"] = 1.0
	if res := creator(t, h).Do(http.MethodPut, invoicePath(credit.ID), creditBody(credit, positive...)); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["lines[3].quantity"]) == 0 {
		t.Errorf("a positive credit of a negative correction = %d %s, want 400 on lines[3].quantity", res.Status, res.Body)
	}
	saved := saveCredit(t, h, credit, creditBody(credit, lines...))
	done := issued(t, h, saved.ID)
	if done.Lines[3].LineNet != -100 || done.NetTotal != 1087.07 {
		t.Errorf("credit line 4 net %v, total %v; want -100 and 1087.07", done.Lines[3].LineNet, done.NetTotal)
	}
	if got := stateBySource(t, h, inv.ID); got[settlementA] != "released" || got[correctionC] != "released" {
		t.Errorf("after the full credit: %v, want both released", got)
	}
}

// A refresh that drops a correction settlement — invoiced elsewhere since —
// drops the correction's own lines with it: a -1 line billing nothing must
// not stay on the draft as a correction and issue (plan reading 7). The
// settlement it corrects stays, its lines rebuilt, and the issue bills it
// alone.
func TestEnergy_ARefreshDropsADroppedCorrectionsLines(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	a := septemberOf(settlementA, gsrnA)
	c := lowerCorrectionOf(correctionC, a)
	c.GSRN = gsrnB
	f.put(a)
	f.put(c)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, a, c)

	gone := c
	gone.State = contracts.SettlementInvoiced
	f.put(gone)

	lines := append(linesBilling(a, []refJSON{refSettlementA}), linesBilling(c, []refJSON{refCorrectionC})...)
	body := sourcedBody(d, customerAcme, lines...)
	body["refreshSources"] = true
	res, _ := putWork(t, h, d.ID, body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT refreshSources = %d %s, want 200", res.Status, res.Body)
	}
	if got := refsOf(energyDoc(t, res).ReleasedSources); !slices.Equal(got, []string{"energy.settlement:9003"}) {
		t.Errorf("released = %v, want energy.settlement:9003", got)
	}
	if got := modtest.One[[]string](t, h.Harness, `
		SELECT array_agg(format('%s:%s', position, correction) ORDER BY position)
		FROM invoices.lines WHERE invoice_id = $1 AND quantity > 0`, d.ID); !slices.Equal(got, []string{"1:f", "2:f", "3:f"}) ||
		modtest.One[int64](t, h.Harness, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, d.ID) != 3 {
		t.Errorf("lines = %v, want A's three and no correction line", got)
	}
	inv := issued(t, h, d.ID)
	if len(inv.Lines) != 3 || inv.NetTotal != 1187.07 {
		t.Errorf("issued %d lines, net %v; want A's 3 and 1187.07", len(inv.Lines), inv.NetTotal)
	}
}

// A save that detaches a settlement sends its lines with no sources, and may
// give back the eight-decimal prices the draft stores; a price it does not
// store is still held to four.
func TestEnergy_ADetachingSaveKeepsTheStoredPrices(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	st := septemberOf(settlementA, gsrnA)
	f.put(st)
	h := energyReady(t, f, newFakeHolders())
	d := energyDraft(t, h, st)

	lines := linesBilling(st, []refJSON{refSettlementA})
	lines[0]["quantity"], lines[0]["unitPrice"] = 1500.25, 0.51234512
	res, _ := putWork(t, h, d.ID, sourcedBody(d, customerAcme, lines...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT the span = %d %s, want 200", res.Status, res.Body)
	}
	saved := energyDoc(t, res)

	other := linesBilling(st, []refJSON{})
	other[0]["quantity"], other[0]["unitPrice"] = 1500.25, 0.51234513
	if res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, other...)); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["lines[0].unitPrice"]) == 0 {
		t.Errorf("a new eight-decimal price on a detached line = %d %s, want 400 on lines[0].unitPrice", res.Status, res.Body)
	}

	detached := linesBilling(st, []refJSON{})
	detached[0]["quantity"], detached[0]["unitPrice"] = 1500.25, 0.51234512
	res, _ = putWork(t, h, d.ID, sourcedBody(saved.asInvoice(), customerAcme, detached...))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT detaching the settlement at its stored price = %d %s, want 200", res.Status, res.Body)
	}
	if doc := energyDoc(t, res); doc.Lines[0].UnitPrice != 0.51234512 ||
		!slices.Equal(refsOf(doc.ReleasedSources), []string{"energy.settlement:9001"}) {
		t.Errorf("detached line 1 = %v, released %v; want 0.51234512 and energy.settlement:9001", doc.Lines[0].UnitPrice, doc.ReleasedSources)
	}
}
