package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A-konto and the final settlement (invoices work design D7): an a-konto is
// an ordinary invoice; a settlement adds deduction lines — quantity -1, the
// amount deducted as a positive price, no discount, the a-konto line's VAT
// code taxed at that line's snapshot — capped per (a-konto, VAT code) by what
// the a-konto has left, read under the counter and never row-locked; a credit
// note of a settlement copies its deductions as negative lines and gives the
// cap back.

func deductiblePath(id int64) string { return fmt.Sprintf("%s/%d/deductible", invoicesPath, id) }

type deductibleJSON struct {
	InvoiceID   int64   `json:"invoiceId"`
	Number      int64   `json:"number"`
	IssueDate   string  `json:"issueDate"`
	VatCodeID   int32   `json:"vatCodeId"`
	Category    string  `json:"category"`
	RatePercent float64 `json:"ratePercent"`
	Left        float64 `json:"left"`
}

// issuedFor issues a draft for customer with lines and answers it.
func issuedFor(t *testing.T, h *harness, customer int32, lines ...map[string]any) invoiceJSON {
	t.Helper()
	return issued(t, h, createDraft(t, h, draftBody(customer, lines...)).ID)
}

// deduction is a settlement's line deducting amount of a at vatCodeID, with
// the text the editor proposes.
func deduction(a invoiceJSON, amount float64, vatCodeID int32) map[string]any {
	return map[string]any{
		"description": fmt.Sprintf("Tidligere fakturert a konto, faktura %d", *a.Number), "quantity": -1, "unit": "",
		"unitPrice": amount, "vatCodeId": vatCodeID, "deductsInvoiceId": a.ID,
	}
}

// deductionOf is a deduction line naming any document id.
func deductionOf(id int64, amount float64, vatCodeID int32) map[string]any {
	return map[string]any{
		"description": "Tidligere fakturert a konto", "quantity": -1, "unit": "",
		"unitPrice": amount, "vatCodeId": vatCodeID, "deductsInvoiceId": id,
	}
}

// deductibleOf is GET /invoices/{id}/deductible for a creator.
func deductibleOf(t *testing.T, h *harness, id int64) []deductibleJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodGet, deductiblePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET deductible of %d = %d %s, want 200", id, res.Status, res.Body)
	}
	var out []deductibleJSON
	res.JSON(&out)
	return out
}

// leftOf is what invoice has left to deduct at vatCodeID, as a fresh draft of
// the customer reads it: 0 when it is not listed.
func leftOf(t *testing.T, h *harness, invoice int64, vatCodeID int32) float64 {
	t.Helper()
	d := createDraft(t, h, draftBody(customerAcme, line("Måling", 1, 1, vat25)))
	for _, r := range deductibleOf(t, h, d.ID) {
		if r.InvoiceID == invoice && r.VatCodeID == vatCodeID {
			return r.Left
		}
	}
	return 0
}

// refused400 asserts res is a 400 naming field.
func refused400(t *testing.T, what string, res *modtest.Response, field string) {
	t.Helper()
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400 on %s", what, res.Status, res.Body, field)
		return
	}
	if p := problemOf(t, res); len(p.Errors[field]) == 0 {
		t.Errorf("%s = 400 on %v, want %s", what, p.Errors, field)
	}
}

// conflictAt asserts res is a 409 with code and, when position is not 0, the
// line position.
func conflictAt(t *testing.T, what string, res *modtest.Response, code string, position int32) problemJSON {
	t.Helper()
	if res.Status != http.StatusConflict {
		t.Fatalf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Fatalf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	if position != 0 && (p.LinePosition == nil || *p.LinePosition != position) {
		t.Errorf("%s: linePosition = %v, want %d", what, p.LinePosition, position)
	}
	return p
}

// settledAcme is an Acme settlement draft's replace body at d's revision.
func settledAcme(d invoiceJSON, lines ...map[string]any) map[string]any {
	return sourcedBody(d, customerAcme, lines...)
}

// The save's rules for a deduction line: a negative quantity only with
// deductsInvoiceId, and with it exactly -1, a positive price and no
// discount; the deducted document an issued invoice of the same customer —
// never a draft, a credit note, another customer's or the draft itself; and
// never named on a credit note's own request.
func TestDeduction_TheSaveRules(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	c := creator(t, h)
	post := func(lines ...map[string]any) *modtest.Response {
		return c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, lines...))
	}
	ordinary := func() map[string]any { return line("Sluttoppgjør", 1, 150000, vat25) }

	refused400(t, "a negative line deducting nothing", post(ordinary(), line("Rabatt", -1, 1000, vat25)), "lines[1].quantity")
	for _, q := range []float64{-2, -0.5, 1} {
		d := deduction(a, 100000, vat25)
		d["quantity"] = q
		refused400(t, fmt.Sprintf("a deduction of quantity %v", q), post(ordinary(), d), "lines[1].quantity")
	}
	refused400(t, "a deduction at price 0", post(ordinary(), deduction(a, 0, vat25)), "lines[1].unitPrice")
	discounted := deduction(a, 100000, vat25)
	discounted["discountPercent"] = 10
	refused400(t, "a discounted deduction", post(ordinary(), discounted), "lines[1].discountPercent")

	draft := createDraft(t, h, draftBody(customerAcme, line("Utkast", 1, 1000, vat25)))
	b := issuedFor(t, h, customerAcme, line("Kreditert", 1, 1000, vat25))
	note := issued(t, h, creditDraft(t, h, b.ID).ID)
	other := issuedFor(t, h, customerPerson, line("Annen kunde", 1, 1000, vat25))
	for _, d := range []struct {
		what string
		id   int64
	}{{"a draft", draft.ID}, {"a credit note", note.ID}, {"another customer's invoice", other.ID}, {"no document", 999999}} {
		refused400(t, d.what+" as the deducted", post(ordinary(), deductionOf(d.id, 1000, vat25)), "lines[1].deductsInvoiceId")
	}
	refused400(t, "itself as the deducted",
		c.Do(http.MethodPut, invoicePath(draft.ID), settledAcme(draft, ordinary(), deductionOf(draft.ID, 1000, vat25))),
		"lines[1].deductsInvoiceId")

	cd := creditDraft(t, h, other.ID)
	named := creditLine(cd.Lines[0])
	named["deductsInvoiceId"] = a.ID
	refused400(t, "deductsInvoiceId on a credit note's own request",
		c.Do(http.MethodPut, invoicePath(cd.ID), creditBody(cd, named)), "lines[0].deductsInvoiceId")

	s := createDraft(t, h, draftBody(customerAcme, ordinary(), deduction(a, 100000, vat25)))
	got := s.Lines[1]
	if got.DeductsInvoiceID == nil || *got.DeductsInvoiceID != a.ID || got.Quantity != -1 || got.UnitPrice != 100000 ||
		got.LineGross != -100000 || got.LineNet != -100000 {
		t.Errorf("the deduction line = %+v, want -1 × 100 000 deducting %d", got, a.ID)
	}
	if s.NetTotal != 50000 || s.VatTotal != 12500 || s.GrossTotal != 62500 {
		t.Errorf("the settlement = %v + %v = %v, want 50 000 + 12 500 = 62 500", s.NetTotal, s.VatTotal, s.GrossTotal)
	}
}

// A deduction is taxed at its a-konto line's snapshot, so a code withdrawn
// since — no longer offered, and with no rate on the issue date — neither
// refuses its save nor its issue; an ordinary line on that code still does.
func TestDeduction_ExemptFromTheInactiveAndNoRateChecks(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat25)
	h.Exec(t, `UPDATE invoices.vat_code_rates SET valid_to = '2026-09-12' WHERE vat_code_id = $1 AND valid_to IS NULL`, vat25)
	h.Advance(24 * time.Hour)

	refused400(t, "an ordinary line on the withdrawn code",
		creator(t, h).Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Vanlig", 1, 1000, vat25))), "lines[0].vatCodeId")

	s := createDraft(t, h, draftBody(customerAcme, line("Sluttoppgjør", 1, 150000, vat15), deduction(a, 100000, vat25)))
	if slices.Contains(s.Warnings, "vat_code_not_valid") {
		t.Errorf("warnings = %v: a deduction is taxed at its a-konto's snapshot", s.Warnings)
	}
	// 22 500 at 15 % and -25 000 at the a-konto's 25 %.
	if s.VatTotal != -2500 || s.GrossTotal != 47500 {
		t.Errorf("the draft = VAT %v gross %v, want -2 500 and 47 500", s.VatTotal, s.GrossTotal)
	}
	inv := issued(t, h, s.ID)
	if l := inv.Lines[1]; l.VatCategory == nil || *l.VatCategory != "S" || l.VatRatePercent == nil || *l.VatRatePercent != 25 {
		t.Errorf("the issued deduction = %+v, want S at 25 %%", l)
	}
}

// After a rate change a deduction keeps the a-konto line's rate on the draft,
// in its preview and at the issue, beside an ordinary line at today's.
// Not parallel: the preview hook is the package's.
func TestDeduction_TaxedAtTheSnapshotAcrossARateChange(t *testing.T) {
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	res := manager(t, h).Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25), map[string]any{"ratePercent": 26, "validFrom": "2026-09-13"})
	if res.Status != http.StatusCreated {
		t.Fatalf("rate change = %d %s", res.Status, res.Body)
	}
	h.Advance(24 * time.Hour)

	s := createDraft(t, h, draftBody(customerAcme, line("Sluttoppgjør", 1, 200000, vat25), deduction(a, 100000, vat25)))
	want := []summaryJSON{
		{VatCategory: "S", RatePercent: 26, SafTCode: "3", TaxableAmount: 200000, VatAmount: 52000, VatAmountNok: 52000},
		{VatCategory: "S", RatePercent: 25, SafTCode: "3", TaxableAmount: -100000, VatAmount: -25000, VatAmountNok: -25000},
	}
	if !slices.Equal(s.VatSummaries, want) || s.VatTotal != 27000 || s.GrossTotal != 127000 {
		t.Errorf("the draft = %+v VAT %v gross %v, want %+v, 27 000 and 127 000", s.VatSummaries, s.VatTotal, s.GrossTotal, want)
	}

	var mu sync.Mutex
	var previewVAT string
	restore := invoices.SetPreviewRendered(func(id int64, vat string) {
		if id == s.ID {
			mu.Lock()
			previewVAT = vat
			mu.Unlock()
		}
	})
	defer restore()
	if res := creator(t, h).Do(http.MethodGet, previewPath(s.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, res.Body)
	}
	mu.Lock()
	if previewVAT != "27000.00" {
		t.Errorf("the preview's VAT = %s, want 27000.00", previewVAT)
	}
	mu.Unlock()

	inv := issued(t, h, s.ID)
	if inv.VatTotal != 27000 || *inv.Lines[0].VatRatePercent != 26 || *inv.Lines[1].VatRatePercent != 25 {
		t.Errorf("the issued settlement = VAT %v at %v and %v %%, want 27 000 at 26 and 25", inv.VatTotal,
			*inv.Lines[0].VatRatePercent, *inv.Lines[1].VatRatePercent)
	}
}

// The cap per (a-konto, VAT code) counts what a credit note gave back and
// what an earlier settlement took: warned on the draft, refused at the issue
// with the line, each code on its own.
func TestDeduction_TheCapPerCodeAfterACreditAndAnEarlierSettlement(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := creator(t, h)
	a := issuedFor(t, h, customerAcme, line("A konto høy sats", 1, 100000, vat25), line("A konto lav sats", 1, 40000, vat15))
	credit := creditDraft(t, h, a.ID)
	lowered := creditLine(credit.Lines[0])
	lowered["unitPrice"] = 20000
	issued(t, h, saveCredit(t, h, credit, creditBody(credit, lowered)).ID)
	issuedFor(t, h, customerAcme, line("Delleveranse", 1, 100000, vat25), deduction(a, 30000, vat25))

	// Left: 50 000 at 25 %, 40 000 at 15 %.
	s := createDraft(t, h, draftBody(customerAcme, line("Sluttoppgjør", 1, 300000, vat25),
		deduction(a, 50000.01, vat25), deduction(a, 40000, vat15)))
	if !slices.Contains(s.Warnings, "deduction_exceeds_invoice") {
		t.Errorf("warnings = %v, want deduction_exceeds_invoice", s.Warnings)
	}
	before := counterNext(t, h)
	conflictAt(t, "the issue past the 25 % cap", issueWith(t, h, s.ID, ""), "deduction_exceeds_invoice", 2)

	s = putDoc(t, c, s.ID, settledAcme(s, line("Sluttoppgjør", 1, 300000, vat25),
		deduction(a, 50000, vat25), deduction(a, 40000.01, vat15)))
	if !slices.Contains(s.Warnings, "deduction_exceeds_invoice") {
		t.Errorf("warnings = %v, want deduction_exceeds_invoice at 15 %%", s.Warnings)
	}
	conflictAt(t, "the issue past the 15 % cap", issueWith(t, h, s.ID, ""), "deduction_exceeds_invoice", 3)
	if got := counterNext(t, h); got != before {
		t.Errorf("the counter moved from %d to %d across refused issues", before, got)
	}

	s = putDoc(t, c, s.ID, settledAcme(s, line("Sluttoppgjør", 1, 300000, vat25),
		deduction(a, 50000, vat25), deduction(a, 40000, vat15)))
	if slices.Contains(s.Warnings, "deduction_exceeds_invoice") {
		t.Errorf("warnings = %v, want none: both deductions are within their caps", s.Warnings)
	}
	issued(t, h, s.ID)
}

// A settlement whose gross is zero or less is warned on the draft and
// refused at the issue, the number rolled back: neither could ever be
// credited, nor the a-konto it deducted.
func TestDeduction_AZeroAndANegativeSettlementAreRefused(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	for _, c := range []struct {
		name  string
		price float64
	}{{"zero", 100000}, {"negative", 50000}} {
		s := createDraft(t, h, draftBody(customerAcme, line("Sluttoppgjør", 1, c.price, vat25), deduction(a, 100000, vat25)))
		if !slices.Contains(s.Warnings, "invoice_total_not_positive") {
			t.Errorf("%s: warnings = %v, want invoice_total_not_positive", c.name, s.Warnings)
		}
		before := counterNext(t, h)
		refusedWith(t, h, s.ID, "", "invoice_total_not_positive")
		if got := counterNext(t, h); got != before {
			t.Errorf("%s: the counter moved from %d to %d", c.name, before, got)
		}
	}
}

// rawConn is a connection of its own, outside the harness's pool: a NOWAIT
// probe.
func rawConn(t *testing.T, h *harness) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.Deps().Config.DatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// rowFree reports, through conn, whether the document's row is free to lock
// at once: nil when it is, the 55P03 error when another transaction holds it.
func rowFree(conn *pgx.Conn, id int64) error {
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT id FROM invoices.invoices WHERE id = $1 FOR UPDATE NOWAIT`, id)
	return err
}

// The issue reads the cap after the counter and locks no a-konto (reading
// 4): parked inside its own transaction at the holder's call — after every
// check, the cap's read among them — the settlement's row is held, and a
// FOR UPDATE NOWAIT of the a-konto from a connection of its own succeeds.
func TestDeduction_TheCapIsReadUnderTheCounterNotLocked(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	s := createDraft(t, h, draftBody(customerAcme, line("Milepæl", 1, 150000, vat25), deduction(a, 100000, vat25)))
	plantSource(t, h, s.ID, planted{position: 1, kind: "projects.milestone", id: milestone, revision: 1, quantity: "1", amount: "150000", date: "2026-09-05"})

	probe := rawConn(t, h)
	var mu sync.Mutex
	var probed bool
	var aKonto, settlement error
	holders.duringCall(func() {
		free, held := rowFree(probe, a.ID), rowFree(probe, s.ID)
		mu.Lock()
		probed, aKonto, settlement = true, free, held
		mu.Unlock()
	})
	issued(t, h, s.ID)

	mu.Lock()
	defer mu.Unlock()
	if !probed {
		t.Fatal("the holder was never called: the settlement holds a milestone")
	}
	if aKonto != nil {
		t.Errorf("FOR UPDATE NOWAIT of the a-konto under the issue = %v, want it free: the cap is read, never locked", aKonto)
	}
	var pgErr *pgconn.PgError
	if !errors.As(settlement, &pgErr) || pgErr.Code != "55P03" {
		t.Errorf("FOR UPDATE NOWAIT of the settlement under its issue = %v, want 55P03: the probe sees a held row", settlement)
	}
}

// A deduction line bills no work: naming a source on one is a 400; with none
// it saves on a draft that holds work.
func TestDeduction_NoSourcesOnADeductionLine(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 1000, vat25))
	d := sourcedDraft(t, h)

	lines := theSameLines()
	lines[2] = sourcedLine(line("Milepæl", 1, 10000, vat25), []refJSON{})
	sourced := sourcedLine(deduction(a, 1000, vat25), []refJSON{refMilestone})
	refused400(t, "a deduction naming a source",
		creator(t, h).Do(http.MethodPut, invoicePath(d.ID), sourcedBody(d, customerAcme, append(lines, sourced)...)), "lines[3].sources")

	saved := putDoc(t, creator(t, h), d.ID, sourcedBody(d, customerAcme,
		append(theSameLines(), sourcedLine(deduction(a, 1000, vat25), []refJSON{}))...))
	if l := saved.Lines[3]; l.DeductsInvoiceID == nil || *l.DeductsInvoiceID != a.ID {
		t.Errorf("the saved deduction = %+v, want it deducting %d", l, a.ID)
	}
}

// A deduction's VAT code is one its a-konto has a line at.
func TestDeduction_TheCodeMustBeOneTheAKontoHas(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	refused400(t, "a deduction at a code the a-konto has no line at",
		creator(t, h).Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 1000, vat15))),
		"lines[1].vatCodeId")
}

// One deduction line per (a-konto, VAT code) per draft: a second is a 409
// naming its position, on a create and on a save; two codes of one a-konto
// are two lines.
func TestDeduction_OneLinePerAKontoAndCode(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := creator(t, h)
	a := issuedFor(t, h, customerAcme, line("Høy", 1, 100000, vat25), line("Lav", 1, 40000, vat15))
	ordinary := func() map[string]any { return line("Sluttoppgjør", 1, 300000, vat25) }

	conflictAt(t, "a create with two deductions of one code",
		c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, ordinary(), deduction(a, 1000, vat25), deduction(a, 2000, vat25))),
		"deduction_duplicated", 3)
	s := createDraft(t, h, draftBody(customerAcme, ordinary(), deduction(a, 1000, vat25), deduction(a, 2000, vat15)))
	conflictAt(t, "a save with two deductions of one code",
		c.Do(http.MethodPut, invoicePath(s.ID), settledAcme(s, ordinary(), deduction(a, 1000, vat15), deduction(a, 2000, vat15))),
		"deduction_duplicated", 3)
}

// A credit note deducts nothing: its draft's deductible read is a 409; an
// issued document's is invoice_issued; an unknown id a 404.
func TestDeductible_ACreditNoteDraftIsA409(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	cd := creditDraft(t, h, a.ID)
	c := creator(t, h)
	conflictAt(t, "a credit-note draft's deductible", c.Do(http.MethodGet, deductiblePath(cd.ID), nil), "credit_note_deducts_nothing", 0)
	conflictAt(t, "an issued invoice's deductible", c.Do(http.MethodGet, deductiblePath(a.ID), nil), "invoice_issued", 0)
	if res := c.Do(http.MethodGet, deductiblePath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown document's deductible = %d %s, want 404", res.Status, res.Body)
	}
}

// A credit note of a settlement copies its deduction as a negative line —
// the same deducted invoice, -1, its net negative — totals as the settlement
// did, and once issued gives the a-konto's cap back.
func TestCredit_ASettlementsCreditCopiesTheDeductionNegativeAndRestoresTheCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	s := issuedFor(t, h, customerAcme, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 100000, vat25))
	if left := leftOf(t, h, a.ID, vat25); left != 0 {
		t.Errorf("the a-konto after the settlement has %v left, want nothing", left)
	}

	c := creditDraft(t, h, s.ID)
	if l := c.Lines[1]; l.Quantity != -1 || l.DeductsInvoiceID == nil || *l.DeductsInvoiceID != a.ID || l.LineNet != -100000 ||
		l.CreditsLineID == nil || *l.CreditsLineID != s.Lines[1].ID {
		t.Errorf("the credit's deduction line = %+v, want -1 × 100 000 deducting %d, crediting %d", l, a.ID, s.Lines[1].ID)
	}
	if c.GrossTotal != s.GrossTotal {
		t.Errorf("the credit draft = %v, want the settlement's %v", c.GrossTotal, s.GrossTotal)
	}
	credit := issued(t, h, c.ID)
	if l := credit.Lines[1]; l.DeductsInvoiceID == nil || *l.DeductsInvoiceID != a.ID || l.VatRatePercent == nil || *l.VatRatePercent != 25 {
		t.Errorf("the issued credit's deduction line = %+v", l)
	}
	if left := leftOf(t, h, a.ID, vat25); left != 100000 {
		t.Errorf("the a-konto after the settlement's credit has %v left, want 100 000 back", left)
	}
}

// A credit line is negative exactly when the line it credits is a deduction:
// against an ordinary line a negative quantity is a 400; against a deduction
// a smaller negative one is the partial credit.
func TestCredit_ANegativeLineAgainstAnOrdinaryLineIsRefused(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ordinary := issuedFor(t, h, customerAcme, line("Konsulent", 2, 1000, vat25))
	c := creditDraft(t, h, ordinary.ID)
	negative := creditLine(c.Lines[0])
	negative["quantity"] = -1
	refused400(t, "a negative credit of an ordinary line",
		creator(t, h).Do(http.MethodPut, invoicePath(c.ID), creditBody(c, negative)), "lines[0].quantity")

	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	s := issuedFor(t, h, customerAcme, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 100000, vat25))
	cs := creditDraft(t, h, s.ID)
	work, half := creditLine(cs.Lines[0]), creditLine(cs.Lines[1])
	work["quantity"], half["quantity"] = 0.5, -0.5
	saved := saveCredit(t, h, cs, creditBody(cs, work, half))
	if l := saved.Lines[1]; l.Quantity != -0.5 || l.LineNet != -50000 || l.DeductsInvoiceID == nil || *l.DeductsInvoiceID != a.ID {
		t.Errorf("the half credit of the deduction = %+v, want -0.5, -50 000, deducting %d", l, a.ID)
	}
}

// Every comparison of a credit line with the deduction it credits is by
// magnitude and of the same sign: the never-raise rule (-1.5 and a sign
// change refused), the line cap across credit notes (-0.5 then -0.75 is past
// -1) and lastReturn (the second -0.5 squares the line to what it has left).
func TestCredit_NeverRaiseTheLineCapAndLastReturnByMagnitude(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := creator(t, h)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	s := issuedFor(t, h, customerAcme, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 99999.99, vat25))
	credit := func(d invoiceJSON, work, deducted float64) map[string]any {
		w, x := creditLine(d.Lines[0]), creditLine(d.Lines[1])
		w["quantity"], x["quantity"] = work, deducted
		return creditBody(d, w, x)
	}

	c1 := creditDraft(t, h, s.ID)
	for _, q := range []float64{-1.5, 1, 0.5} {
		refused400(t, fmt.Sprintf("a credit of the deduction at %v", q), c.Do(http.MethodPut, invoicePath(c1.ID), credit(c1, 0.5, q)), "lines[1].quantity")
	}
	c1 = saveCredit(t, h, c1, credit(c1, 0.5, -0.5))
	if c1.Lines[1].LineNet != -50000 {
		t.Errorf("half of -99 999.99 = %v, want -50 000.00 (half away from zero)", c1.Lines[1].LineNet)
	}
	issued(t, h, c1.ID)

	c2 := creditDraft(t, h, s.ID)
	c2 = saveCredit(t, h, c2, credit(c2, 0.5, -0.75))
	if !slices.Contains(c2.Warnings, "credit_exceeds_line") {
		t.Errorf("warnings = %v, want credit_exceeds_line: -0.5 and -0.75 pass the deduction's -1", c2.Warnings)
	}
	conflictAt(t, "the issue past the deduction's -1", issueWith(t, h, c2.ID, ""), "credit_exceeds_line", 2)
	if res := c.Do(http.MethodDelete, invoicePath(c2.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete c2 = %d %s", res.Status, res.Body)
	}

	c3 := creditDraft(t, h, s.ID)
	c3 = saveCredit(t, h, c3, credit(c3, 0.5, -0.5))
	if c3.Lines[1].LineNet != -49999.99 {
		t.Errorf("the deduction's last return = %v, want what it has left, -49 999.99", c3.Lines[1].LineNet)
	}
	if done := issued(t, h, c3.ID); done.GrossTotal != 31250.01 {
		t.Errorf("the final credit note = %v, want what the settlement had left, 31 250.01", done.GrossTotal)
	}
}

// A credit note whose gross is below zero — a settlement's deduction
// credited without its work — is refused at the issue.
func TestCredit_CreditTotalNegative(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("A konto", 1, 100000, vat25))
	s := issuedFor(t, h, customerAcme, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 100000, vat25))
	c := creditDraft(t, h, s.ID)
	c = saveCredit(t, h, c, creditBody(c, creditLine(c.Lines[1])))
	if c.GrossTotal != -125000 {
		t.Errorf("the deduction's credit alone = %v, want -125 000", c.GrossTotal)
	}
	before := counterNext(t, h)
	refusedWith(t, h, c.ID, "", "credit_total_negative")
	if got := counterNext(t, h); got != before {
		t.Errorf("the counter moved from %d to %d", before, got)
	}
}

// A credit note of nothing but a line with no money in it is zero, and a
// zero credit note still issues, as before deductions existed.
func TestCredit_AZeroCreditNoteStillIssues(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issuedFor(t, h, customerAcme, line("Konsulent", 1, 1000, vat25), line("Frakt", 1, 0, vat25))
	c := creditDraft(t, h, inv.ID)
	c = saveCredit(t, h, c, creditBody(c, creditLine(c.Lines[1])))
	if c.GrossTotal != 0 {
		t.Errorf("the freight's credit = %v, want 0", c.GrossTotal)
	}
	issued(t, h, c.ID)
}

// Crediting a deducted a-konto is judged per VAT code: at a code a settlement
// deducted, a credit taking more than is left there is refused, naming the
// settlement; at a code no settlement deducted, the line caps alone decide.
func TestCredit_InvoiceDeductedPerVatCode(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a := issuedFor(t, h, customerAcme, line("Høy", 1, 100000, vat25), line("Lav", 1, 40000, vat15))
	s := issuedFor(t, h, customerAcme, line("Sluttoppgjør", 1, 200000, vat25), deduction(a, 60000, vat25))

	c1 := creditDraft(t, h, a.ID)
	high := creditLine(c1.Lines[0])
	high["unitPrice"] = 40000.01
	c1 = saveCredit(t, h, c1, creditBody(c1, high))
	p := refusedWith(t, h, c1.ID, "", "invoice_deducted")
	if !strings.Contains(p.Detail, fmt.Sprint(*s.Number)) {
		t.Errorf("detail = %q, want it naming settlement %d", p.Detail, *s.Number)
	}
	high["unitPrice"], high["quantity"] = 40000, 1
	c1 = saveCredit(t, h, c1, creditBody(c1, high))
	issued(t, h, c1.ID)

	c2 := creditDraft(t, h, a.ID)
	issued(t, h, saveCredit(t, h, c2, creditBody(c2, creditLine(c2.Lines[1]))).ID)
}

// pdfText is one laid-out PDF's meta rows and line rows.
type pdfText struct {
	meta  [][2]string
	lines [][]string
}

// A deduction prints with a leading minus on its quantity and its amount, its
// unit price positive, and the deducted invoices are listed under the
// references, in Norwegian and in English. Not parallel: the PDF hook is the
// package's.
func TestPDF_TheDeductionsMinusSigns(t *testing.T) {
	h := readyToIssue(t)
	var mu sync.Mutex
	models := map[int64]pdfText{}
	restore := invoices.SetPDFModelText(func(id int64, meta [][2]string, lines [][]string) {
		mu.Lock()
		defer mu.Unlock()
		models[id] = pdfText{meta: meta, lines: lines}
	})
	defer restore()

	for _, c := range []struct {
		language                string
		customer                int32
		label, value            string
		quantity, price, amount string
	}{
		// Norwegian groups thousands with a no-break space.
		{"nb", customerAcme, "Fratrukket a konto", "Faktura %d av 12.09.2026", "-1", "100\u00a0000,00", "-100\u00a0000,00"},
		{"en", customerPerson, "Deducted on account", "Invoice %d of 2026-09-12", "-1", "100,000.00", "-100,000.00"},
	} {
		a := issuedFor(t, h, c.customer, line("A konto", 1, 100000, vat25))
		s := issuedFor(t, h, c.customer, line("Sluttoppgjør", 1, 150000, vat25), deduction(a, 100000, vat25))
		mu.Lock()
		m, ok := models[s.ID]
		mu.Unlock()
		if !ok || len(m.lines) != 2 {
			t.Fatalf("%s: the settlement's PDF = %+v", c.language, m)
		}
		if l := m.lines[1]; l[1] != c.quantity || l[3] != c.price || l[6] != c.amount {
			t.Errorf("%s: the deduction prints %v, want quantity %s, unit price %s, amount %s", c.language, l, c.quantity, c.price, c.amount)
		}
		want := [2]string{c.label, fmt.Sprintf(c.value, *a.Number)}
		if !slices.Contains(m.meta, want) {
			t.Errorf("%s: meta = %v, want %v", c.language, m.meta, want)
		}
	}
}

// GET /invoices/{id}/deductible: per (issued invoice of the draft's customer,
// VAT code) with something left — net of a credit note and of a settlement
// that took it all — by number and code, with the a-konto line's snapshot;
// never another customer's, a credit note or the draft's own deductions.
func TestDeductible_TheRead(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	a1 := issuedFor(t, h, customerAcme, line("Høy", 1, 100000, vat25), line("Lav", 1, 40000, vat15))
	a2 := issuedFor(t, h, customerAcme, line("A konto 2", 1, 50000, vat25))
	credit := creditDraft(t, h, a1.ID)
	lowered := creditLine(credit.Lines[0])
	lowered["unitPrice"] = 20000
	issued(t, h, saveCredit(t, h, credit, creditBody(credit, lowered)).ID)
	s1 := issuedFor(t, h, customerAcme, line("Delleveranse", 1, 100000, vat25), deduction(a2, 50000, vat25))
	issuedFor(t, h, customerPerson, line("Annen kunde", 1, 1000, vat25))

	d := createDraft(t, h, draftBody(customerAcme, line("Sluttoppgjør", 1, 300000, vat25), deduction(a1, 1000, vat25)))
	want := []deductibleJSON{
		{InvoiceID: a1.ID, Number: *a1.Number, IssueDate: "2026-09-12", VatCodeID: vat25, Category: "S", RatePercent: 25, Left: 80000},
		{InvoiceID: a1.ID, Number: *a1.Number, IssueDate: "2026-09-12", VatCodeID: vat15, Category: "S", RatePercent: 15, Left: 40000},
		{InvoiceID: s1.ID, Number: *s1.Number, IssueDate: "2026-09-12", VatCodeID: vat25, Category: "S", RatePercent: 25, Left: 50000},
	}
	if got := deductibleOf(t, h, d.ID); !slices.Equal(got, want) {
		t.Errorf("deductible = %+v\nwant %+v", got, want)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, deductiblePath(d.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("deductible without invoices:create = %d, want 403", res.Status)
	}
}
