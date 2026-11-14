package accounting_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// update rewrites the VAT return goldens: go test ./internal/accounting -run
// TestVatReturn_Goldens -update.
var update = flag.Bool("update", false, "rewrite the VAT return goldens")

// fakeSalesTax is Invoices' seller as far as the VAT return reads it.
type fakeSalesTax struct {
	contracts.SalesTax
	seller contracts.SellerProfile
}

func (f fakeSalesTax) Seller(context.Context) (contracts.SellerProfile, error) { return f.seller, nil }

// vatBooks is books set up bimonthly from 2026-01-01, the seller registered
// with organisation number 923 609 016; the harness's today is 2026-09-12,
// so terms 1 to 4 have ended.
func vatBooks(t *testing.T) *books {
	t.Helper()
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: contracts.SellerProfile{LegalName: "Fjord Bygg AS",
		OrganisationNumber: "923 609 016", VATRegistered: true}}))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	return &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
}

func termPath(year, number int) string { return fmt.Sprintf("%s/vat/terms/%d/%d", base, year, number) }

// term is a term's page.
func (b *books) term(t *testing.T, year, number int) gen.AccountingVatTermDetail {
	t.Helper()
	res := b.c.Do(http.MethodGet, termPath(year, number), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("term %d-%d = %d %s", year, number, res.Status, res.Body)
	}
	var d gen.AccountingVatTermDetail
	res.JSON(&d)
	return d
}

// close closes a term that must close.
func (b *books) close(t *testing.T, year, number int) gen.AccountingVatTermDetail {
	t.Helper()
	res := b.c.Do(http.MethodPost, termPath(year, number)+"/close", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("close %d-%d = %d %s", year, number, res.Status, res.Body)
	}
	var d gen.AccountingVatTermDetail
	res.JSON(&d)
	return d
}

// sale posts a manual journal of a sale (or purchase) with a tax code: the
// line on account with the code, net, against the bank.
func (b *books) taxed(t *testing.T, date, account, code, amount string) gen.AccountingVoucher {
	t.Helper()
	a := mustRat(t, amount)
	_, tax := netTax(a, b.codes[code].Rates)
	other := new(big.Rat).Neg(new(big.Rat).Add(a, tax))
	if b.codes[code].ReverseCharge {
		other = new(big.Rat).Neg(a)
	}
	return mustPost(t, b.c, date, "Code "+code+" "+amount,
		line{"accountId": b.acc(account), "amount": amount, "taxCodeId": b.code(code)},
		line{"accountId": b.acc("1920"), "amount": other.FloatString(2)})
}

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("not a number: %s", s)
	}
	return r
}

// netTax is a net amount's tax at the code's rate on 2026-01-01, rounded
// half away from zero, as the journal computes it.
func netTax(amount *big.Rat, rates []gen.AccountingTaxRate) (*big.Rat, *big.Rat) {
	rate := new(big.Rat)
	for _, r := range rates {
		if r.ValidFrom.String() <= "2026-01-01" {
			rate, _ = new(big.Rat).SetString(r.RatePercent)
		}
	}
	tax := new(big.Rat).Quo(new(big.Rat).Mul(amount, rate), big.NewRat(100, 1))
	cents := new(big.Rat).Mul(tax, big.NewRat(100, 1))
	f, _ := cents.Float64()
	n := int64(f)
	if rem := f - float64(n); rem >= 0.5 {
		n++
	} else if rem <= -0.5 {
		n--
	}
	return amount, big.NewRat(n, 100)
}

// writeOff posts a konstatert loss on code 3 as the sales catch-up does
// (sales.write_off_vat): the VAT taken back on 2700 and the net on bad_debt,
// both tapPåKrav, against the customer's receivable.
func (b *books) writeOff(t *testing.T, date string, id int64, net, vat string) {
	t.Helper()
	day, _ := time.Parse(time.DateOnly, date)
	code := b.code("3")
	customer := customerAcme
	err := db.WithTx(t.Context(), b.h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := accounting.Post(t.Context(), tx, accounting.Voucher{Kind: "write_off", Date: day, Description: "Write-off",
			Source: &accounting.Source{Module: "invoices", Kind: "write_off", ID: id}, Rule: "sales.write_off_vat", RuleVersion: 1,
			Lines: []accounting.Line{
				{AccountID: b.acc("2700"), Amount: mustRat(t, vat), TaxCodeID: &code, TaxRole: "tax", TaxBase: mustRat(t, net), TaxSpecification: "tapPåKrav"},
				{AccountID: b.acc("7830"), Amount: mustRat(t, net), TaxCodeID: &code, TaxRole: "base", TaxSpecification: "tapPåKrav"},
				{Role: "receivables", Amount: new(big.Rat).Neg(new(big.Rat).Add(mustRat(t, net), mustRat(t, vat))), CustomerID: &customer},
			}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// figures is the specification's reported codes by code and specification.
func figuresOf(spec gen.AccountingVatSpecification) map[string]gen.AccountingVatCodeFigures {
	out := map[string]gen.AccountingVatCodeFigures{}
	for _, f := range append(append([]gen.AccountingVatCodeFigures{}, spec.Codes...), spec.NotReported...) {
		key := f.Code
		if f.Specification != nil {
			key += "/" + *f.Specification
		}
		out[key] = f
	}
	return out
}

// The specification against a fixed ledger, every seeded code (A21): sales at
// each rate and the zero-rate, exempt and export codes; input VAT at each
// rate and on import; reverse charge with and without deduction; the codes
// the return leaves out; a loss's tapPåKrav; and a reversal in a later term.
// Each figure's postings are the drill-down's, to the øre.
func TestVatSpecification_EveryCode(t *testing.T) {
	b := vatBooks(t)
	d := "2026-01-15"
	for _, s := range []struct{ account, code, amount string }{
		{"3000", "3", "-1000.00"}, {"3010", "31", "-200.00"}, {"3000", "32", "-90.00"}, {"3020", "33", "-100.00"},
		{"3100", "5", "-50.00"}, {"3160", "51", "-60.00"}, {"3150", "52", "-70.00"}, {"3200", "6", "-80.00"},
		{"6300", "1", "400.00"}, {"6300", "11", "100.00"}, {"4005", "12", "90.00"}, {"7000", "13", "50.00"},
		{"4005", "14", "1000.00"}, {"4005", "15", "200.00"},
		{"4005", "81", "2000.00"}, {"4005", "82", "300.00"}, {"4005", "83", "100.00"}, {"4005", "84", "100.00"}, {"4005", "85", "100.00"},
		{"6300", "86", "1000.00"}, {"6300", "87", "500.00"}, {"6300", "88", "100.00"}, {"6300", "89", "100.00"},
		{"6300", "91", "100.00"}, {"6300", "92", "100.00"},
		{"6300", "0", "40.00"}, {"3900", "7", "-30.00"}, {"4005", "20", "20.00"},
	} {
		b.taxed(t, d, s.account, s.code, s.amount)
	}
	b.writeOff(t, "2026-02-10", 9001, "800.00", "200.00")
	purchase := b.taxed(t, "2026-02-20", "6300", "1", "100.00")
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, purchase.Id), map[string]any{"date": "2026-03-05", "reason": "Wrong supplier"})
	if res.Status != http.StatusCreated {
		t.Fatalf("reverse = %d %s", res.Status, res.Body)
	}

	res = b.c.Do(http.MethodGet, base+"/vat/specification?from=2026-01-01&to=2026-02-28", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("specification = %d %s", res.Status, res.Body)
	}
	var spec gen.AccountingVatSpecification
	res.JSON(&spec)
	got := figuresOf(spec)
	type want struct{ base, output, calculated, deducted, notDeducted, loss, vat string }
	for key, w := range map[string]want{
		"3":  {"1000.00", "250.00", "0.00", "0.00", "0.00", "0.00", "250.00"},
		"31": {"200.00", "30.00", "0.00", "0.00", "0.00", "0.00", "30.00"},
		"32": {"90.00", "10.00", "0.00", "0.00", "0.00", "0.00", "10.00"},
		"33": {"100.00", "12.00", "0.00", "0.00", "0.00", "0.00", "12.00"},
		"5":  {"50.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"51": {"60.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"52": {"70.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"6":  {"80.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		// Code 1 holds the purchase of 2026-02-20 too, its reversal in March.
		"1":           {"500.00", "0.00", "0.00", "-125.00", "0.00", "0.00", "-125.00"},
		"11":          {"100.00", "0.00", "0.00", "-15.00", "0.00", "0.00", "-15.00"},
		"12":          {"90.00", "0.00", "0.00", "-10.00", "0.00", "0.00", "-10.00"},
		"13":          {"50.00", "0.00", "0.00", "-6.00", "0.00", "0.00", "-6.00"},
		"14":          {"1000.00", "0.00", "0.00", "-250.00", "0.00", "0.00", "-250.00"},
		"15":          {"200.00", "0.00", "0.00", "-30.00", "0.00", "0.00", "-30.00"},
		"81":          {"2000.00", "0.00", "500.00", "-500.00", "0.00", "0.00", "0.00"},
		"82":          {"300.00", "0.00", "75.00", "0.00", "75.00", "0.00", "75.00"},
		"83":          {"100.00", "0.00", "15.00", "-15.00", "0.00", "0.00", "0.00"},
		"84":          {"100.00", "0.00", "15.00", "0.00", "15.00", "0.00", "15.00"},
		"85":          {"100.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"86":          {"1000.00", "0.00", "250.00", "-250.00", "0.00", "0.00", "0.00"},
		"87":          {"500.00", "0.00", "125.00", "0.00", "125.00", "0.00", "125.00"},
		"88":          {"100.00", "0.00", "12.00", "-12.00", "0.00", "0.00", "0.00"},
		"89":          {"100.00", "0.00", "12.00", "0.00", "12.00", "0.00", "12.00"},
		"91":          {"100.00", "0.00", "25.00", "-25.00", "0.00", "0.00", "0.00"},
		"92":          {"100.00", "0.00", "25.00", "0.00", "25.00", "0.00", "25.00"},
		"3/tapPåKrav": {"800.00", "0.00", "0.00", "0.00", "0.00", "-200.00", "-200.00"},
		"0":           {"40.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"7":           {"30.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
		"20":          {"20.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"},
	} {
		f, ok := got[key]
		if !ok {
			t.Errorf("no figures for %s", key)
			continue
		}
		g := want{f.Base, f.OutputVat, f.CalculatedVat, f.DeductedVat, f.NotDeductedVat, f.LossVat, f.Vat}
		if g != w {
			t.Errorf("%s = %+v, want %+v", key, g, w)
		}
		// The drill-down holds the figure up, posting by posting.
		for part, figure := range map[string]string{"base": f.Base, "deducted": f.DeductedVat, "calculated": f.CalculatedVat} {
			if key == "3/tapPåKrav" || figure == "0.00" {
				continue
			}
			res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vat/postings?from=2026-01-01&to=2026-02-28&taxCodeId=%d&part=%s", base, f.TaxCodeId, part), nil)
			var list gen.AccountingVatPostingList
			res.JSON(&list)
			sum := new(big.Rat)
			for _, p := range list.Items {
				sum.Add(sum, mustRat(t, p.Amount))
			}
			// A VAT figure is owed positive; a sale's base is credited.
			if part != "base" || f.Direction == gen.AccountingTaxDirectionOutput || f.Code == "7" {
				sum.Neg(sum)
			}
			if sum.FloatString(2) != figure || list.Total != int32(len(list.Items)) {
				t.Errorf("%s %s: the postings sum to %s, the figure is %s", key, part, sum.FloatString(2), figure)
			}
		}
	}
	for _, f := range spec.NotReported {
		if f.Reported || f.Group != gen.NotReported {
			t.Errorf("a code left out of the return = %+v", f)
		}
	}
	for _, f := range spec.Codes {
		if f.Code == "0" || f.Code == "7" || f.Code == "20" {
			t.Errorf("code %s is in the return", f.Code)
		}
	}
	if !spec.Reconciliation.Reconciled || spec.Reconciliation.Difference != "0.00" {
		t.Errorf("reconciliation = %+v", spec.Reconciliation)
	}

	// The reversal shows in March, the later term, as a negative deduction.
	res = b.c.Do(http.MethodGet, base+"/vat/specification?from=2026-03-01&to=2026-04-30", nil)
	res.JSON(&spec)
	if one := figuresOf(spec)["1"]; one.DeductedVat != "25.00" || one.Base != "-100.00" {
		t.Errorf("the reversal in term 2 = %+v", one)
	}

	// The report and its CSV are the same figures.
	res = b.c.Do(http.MethodGet, base+"/reports/vat?from=2026-01-01&to=2026-02-28", nil)
	var report gen.AccountingReport
	res.JSON(&report)
	found := false
	for _, r := range report.Rows {
		if r.Cells["code"] == "87" && r.Cells["notDeducted"] == "125.00" && r.Cells["calculated"] == "125.00" && r.TaxCodeId != nil {
			found = true
		}
	}
	if res.Status != http.StatusOK || !found || report.Title != "Merverdiavgiftsspesifikasjon" {
		t.Errorf("the VAT report = %d %+v", res.Status, report)
	}
	res = b.c.Do(http.MethodGet, base+"/reports/vat/csv?from=2026-01-01&to=2026-02-28", nil)
	if res.Status != http.StatusOK || !bytes.Contains(res.Body, []byte("Merverdiavgiftsspesifikasjon")) || !bytes.Contains(res.Body, []byte("125,00")) {
		t.Errorf("the VAT report's CSV = %d %s", res.Status, res.Body)
	}
}

// A posting straight onto a VAT account without a tax code keeps the VAT
// accounts from reconciling (R10): the specification shows the difference
// with the posting, and the close refuses it naming the voucher.
func TestVatReconciliation_FindsAPostingWithoutCode(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	stray := mustPost(t, b.c, "2026-01-20", "Straight onto output VAT",
		line{"accountId": b.acc("2700"), "amount": "-40.00"}, line{"accountId": b.acc("1920"), "amount": "40.00"})
	res := b.c.Do(http.MethodGet, base+"/vat/specification?from=2026-01-01&to=2026-02-28", nil)
	var spec gen.AccountingVatSpecification
	res.JSON(&spec)
	r := spec.Reconciliation
	if r.Reconciled || r.Movement != "-290.00" || r.Counted != "-250.00" || r.Difference != "-40.00" || len(r.Postings) != 1 ||
		r.Postings[0].Voucher.Id != stray.Id || r.Postings[0].Part != gen.AccountingVatPartUntaxed {
		t.Fatalf("reconciliation = %+v", r)
	}
	res = b.c.Do(http.MethodGet, base+"/vat/postings?from=2026-01-01&to=2026-02-28&unreconciled=true", nil)
	var list gen.AccountingVatPostingList
	res.JSON(&list)
	if list.Total != 1 || list.Items[0].Amount != "-40.00" {
		t.Errorf("the unreconciled postings = %+v", list)
	}
	if d := b.term(t, 2026, 1); d.Checks.Reconciled {
		t.Errorf("the checks = %+v", d.Checks)
	}
	res = b.c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil)
	expectProblem(t, res, http.StatusConflict, "vat_term_unreconciled")
	if !strings.Contains(string(res.Body), stray.Display) {
		t.Errorf("the refusal does not name %s: %s", stray.Display, res.Body)
	}
}

// Closing a term (A22, R10): the settlement voucher on the term's last day
// moves each VAT account's movement to vat_settlement, the return's whole
// kroner there and the øre on the rounding account; the lock moves to the
// term's end with a lock event; the return is stored and the term is closed.
func TestVatClose_SettlesToTheOreAndLocks(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.99") // VAT −250.25
	b.taxed(t, "2026-02-03", "6300", "1", "301.30")   // VAT 75.33
	b.taxed(t, "2026-02-04", "6300", "86", "100.30")  // VAT ±25.08
	d := b.close(t, 2026, 1)
	if d.Term.State != gen.AccountingVatTermStateClosed || d.Term.SettlementVoucher == nil || !d.Term.LockCovers ||
		d.Return.Total != "175" || d.Return.ExactTotal != "174.92" || d.Return.RoundingDifference != "-0.08" ||
		d.Return.Direction != gen.Pay || d.Return.OrganisationNumber == nil || *d.Return.OrganisationNumber != "923609016" {
		t.Fatalf("closed term = %+v", d)
	}
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, d.Term.SettlementVoucher.Id), nil)
	var v gen.AccountingVoucher
	res.JSON(&v)
	got := map[string]string{}
	for _, p := range v.Postings {
		got[p.AccountNumber] = p.Amount
	}
	// Each line's øre are cut: 250.25 is 250, −75.33 is −75, so the return
	// says 175 and the ledger 174.92; the 0.08 goes on the rounding account.
	want := map[string]string{"2700": "250.25", "2710": "-75.33", "2715": "-25.08", "2705": "25.08", "2740": "-175.00", "7790": "0.08"}
	if v.Kind != "vat_settlement" || v.VoucherDate.String() != "2026-02-28" || len(got) != len(want) || !v.System {
		t.Fatalf("settlement = %+v", v)
	}
	for a, w := range want {
		if got[a] != w {
			t.Errorf("settlement on %s = %s, want %s", a, got[a], w)
		}
	}
	res = b.c.Do(http.MethodGet, base+"/lock", nil)
	var lock gen.AccountingLock
	res.JSON(&lock)
	if lock.LockedThrough == nil || lock.LockedThrough.String() != "2026-02-28" || lock.Events[0].Reason == nil ||
		!strings.Contains(*lock.Events[0].Reason, "2026-1") {
		t.Errorf("lock = %+v", lock)
	}
	// The settlement belongs to its term: a person cannot reverse it.
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-03-01", "reason": "No"}),
		http.StatusConflict, "voucher_owned_by_source")
	// What the close stored is what the page and the download answer.
	again := b.term(t, 2026, 1)
	if again.Return.Total != "175" || len(again.Events) != 1 || again.Events[0].Kind != gen.AccountingVatTermEventKindClosed {
		t.Errorf("the closed term's page = %+v", again)
	}
	res = b.c.Do(http.MethodGet, termPath(2026, 1)+"/xml", nil)
	if res.Status != http.StatusOK || !bytes.Contains(res.Body, []byte("<fastsattMerverdiavgift>175</fastsattMerverdiavgift>")) ||
		!strings.Contains(res.Header("Content-Disposition"), "mva-melding-2026-1.xml") {
		t.Errorf("xml = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil), http.StatusConflict, "vat_term_closed")
}

// Every refusal of a close, each in its turn.
func TestVatClose_Refusals(t *testing.T) {
	b := vatBooks(t)
	// The current term (Sep–Oct) has not ended.
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 5)+"/close", nil), http.StatusConflict, "vat_term_not_ended")
	// Terms close in order.
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 2)+"/close", nil), http.StatusConflict, "vat_term_out_of_order")
	// A fact waiting under Needs attention, dated in the term.
	b.h.Exec(t, `INSERT INTO accounting.unposted (stream, position, source_module, source_kind, source_id, fact_date, reason,
		first_seen_at, last_tried_at) VALUES ('invoices.documents', 1, 'invoices', 'document', 77, '2026-02-10', 'tax_code_unknown', now(), now())`)
	res := b.c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil)
	expectProblem(t, res, http.StatusConflict, "vat_term_attention")
	if !strings.Contains(string(res.Body), "1 facts") {
		t.Errorf("the refusal does not count: %s", res.Body)
	}
	if d := b.term(t, 2026, 1); d.Checks.Attention != 1 || d.Checks.Ended != true || d.Checks.EarlierOpen != nil {
		t.Errorf("checks = %+v", d.Checks)
	}
	b.h.Exec(t, `UPDATE accounting.unposted SET resolved_at = now()`)
	// The lock on the term's end already.
	res = b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-02-28"})
	if res.Status != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil), http.StatusConflict, "vat_term_lock_after_end")
	if d := b.term(t, 2026, 1); !d.Checks.LockAfterEnd {
		t.Errorf("checks = %+v", d.Checks)
	}
	res = b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31", "reason": "For the VAT term"})
	if res.Status != http.StatusOK {
		t.Fatalf("lock back = %d %s", res.Status, res.Body)
	}
	// An empty term closes, with nothing to settle.
	d := b.close(t, 2026, 1)
	if d.Term.SettlementVoucher != nil || d.Return.Total != "0" || len(d.Return.Lines) != 0 {
		t.Errorf("empty term = %+v", d)
	}
	// A term that does not exist, and one before the books.
	if res := b.c.Do(http.MethodGet, termPath(2026, 7), nil); res.Status != http.StatusNotFound {
		t.Errorf("term 7 = %d", res.Status)
	}
	if res := b.c.Do(http.MethodGet, termPath(2025, 6), nil); res.Status != http.StatusNotFound {
		t.Errorf("a term before the books = %d", res.Status)
	}
}

// Two closes at once: the settings row serialises them, one wins and the
// other is told the term is closed; one settlement voucher.
func TestVatClose_Concurrent(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = b.c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil).Status
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			refused++
		}
	}
	if ok != 1 || refused != 1 {
		t.Errorf("statuses = %v, want one 200 and one 409", statuses)
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE kind = 'vat_settlement'"); n != 1 {
		t.Errorf("%d settlement vouchers", n)
	}
}

// balances is every account's balance through a day, by number.
func (b *books) balances(t *testing.T, to string) map[string]string {
	t.Helper()
	res := b.c.Do(http.MethodGet, base+"/reports/trial_balance?from=2026-01-01&to="+to, nil)
	var report gen.AccountingReport
	res.JSON(&report)
	out := map[string]string{}
	for _, r := range report.Rows {
		if r.AccountId != nil {
			out[r.Cells["account"]] = r.Cells["closing"]
		}
	}
	return out
}

// Reopening (A22, R10) restores exactly what the close changed: the lock
// back to the day before the term's start, the settlement turned back on the
// term's last day, every balance as it was. Only the latest closed term
// reopens, and never a filed one; a filed term bounds the lock.
func TestVatReopen_RestoresAndFilingBounds(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.99")
	b.taxed(t, "2026-03-10", "6300", "1", "200.00")
	before := b.balances(t, "2026-12-31")
	b.close(t, 2026, 1)
	b.close(t, 2026, 2)
	// Not the earlier one while the later stands.
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/reopen", map[string]any{"reason": "Fix"}), http.StatusConflict, "vat_term_not_latest")
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 2)+"/reopen", map[string]any{"reason": " "}), http.StatusBadRequest, "lock_reason_required")
	res := b.c.Do(http.MethodPost, termPath(2026, 2)+"/reopen", map[string]any{"reason": "A purchase is missing"})
	if res.Status != http.StatusOK {
		t.Fatalf("reopen = %d %s", res.Status, res.Body)
	}
	var d gen.AccountingVatTermDetail
	res.JSON(&d)
	if d.Term.State != gen.AccountingVatTermStateOpen || d.Term.SettlementVoucher != nil || d.Events[0].Kind != gen.AccountingVatTermEventKindReopened ||
		d.Events[0].Voucher == nil || *d.Events[0].Reason != "A purchase is missing" {
		t.Fatalf("reopened = %+v", d)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, d.Events[0].Voucher.Id), nil)
	var rev gen.AccountingVoucher
	res.JSON(&rev)
	if rev.VoucherDate.String() != "2026-04-30" || rev.Reverses == nil {
		t.Errorf("the reversal = %+v", rev)
	}
	res = b.c.Do(http.MethodGet, base+"/lock", nil)
	var lock gen.AccountingLock
	res.JSON(&lock)
	if lock.LockedThrough.String() != "2026-02-28" {
		t.Errorf("lock after reopening = %v", lock.LockedThrough)
	}
	// Term 1 is still settled; term 2 is as before its close.
	after := b.balances(t, "2026-12-31")
	if after["2710"] != before["2710"] || after["2700"] != "0.00" || after["2740"] != "-250.00" || after["7790"] != "-0.25" {
		t.Errorf("after reopening term 2 = %v (before closing: %v)", after, before)
	}
	// Reopen term 1 too: everything as it was.
	res = b.c.Do(http.MethodPost, termPath(2026, 1)+"/reopen", map[string]any{"reason": "Back to the start"})
	if res.Status != http.StatusOK {
		t.Fatalf("reopen term 1 = %d %s", res.Status, res.Body)
	}
	after = b.balances(t, "2026-12-31")
	for _, a := range []string{"2700", "2710", "2740", "7790", "1920"} {
		if after[a] != before[a] && (before[a] != "" || after[a] != "0.00") {
			t.Errorf("%s = %s after reopening both, %s before closing", a, after[a], before[a])
		}
	}
	res = b.c.Do(http.MethodGet, base+"/lock", nil)
	res.JSON(&lock)
	if lock.LockedThrough != nil {
		t.Errorf("the lock after reopening the first term = %v, want lifted", lock.LockedThrough)
	}

	// Close and file term 1: it does not reopen, and the lock stays on it.
	d = b.close(t, 2026, 1)
	if d.Return.Total != "250" {
		t.Errorf("closed again = %+v", d.Return)
	}
	filed := map[string]any{"filedAt": "2026-03-20", "reference": "AR-123456"}
	poster := b.h.SignIn(t, poster...)
	res = poster.Do(http.MethodPost, termPath(2026, 1)+"/filing", filed)
	if res.Status != http.StatusOK {
		t.Fatalf("file = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/filing", filed), http.StatusConflict, "vat_term_filed")
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/reopen", map[string]any{"reason": "Fix"}), http.StatusConflict, "vat_term_filed")
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31", "reason": "Back"}),
		http.StatusConflict, "lock_behind_filed_term")
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": nil, "reason": "Lift"}),
		http.StatusConflict, "lock_behind_filed_term")
	// A filing's day is after the term and not in the future.
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 2)+"/filing", map[string]any{"filedAt": "2026-02-01", "reference": "x"}),
		http.StatusBadRequest, "")
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 2)+"/filing", map[string]any{"filedAt": "2026-05-20", "reference": "x"}),
		http.StatusConflict, "vat_term_not_closed")
	// Unfiling is accounting:close's, with a reason, and opens the way back.
	if res := poster.Do(http.MethodPost, termPath(2026, 1)+"/unfile", map[string]any{"reason": "Typed the wrong reference"}); res.Status != http.StatusForbidden {
		t.Errorf("a poster unfiles = %d", res.Status)
	}
	res = b.c.Do(http.MethodPost, termPath(2026, 1)+"/unfile", map[string]any{"reason": "Typed the wrong reference"})
	if res.Status != http.StatusOK {
		t.Fatalf("unfile = %d %s", res.Status, res.Body)
	}
	res.JSON(&d)
	if d.Term.FiledAt != nil || d.Events[0].Kind != gen.AccountingVatTermEventKindUnfiled || d.Events[1].Kind != gen.AccountingVatTermEventKindFiled ||
		*d.Events[1].FiledReference != "AR-123456" {
		t.Errorf("unfiled = %+v", d)
	}
	expectProblem(t, b.c.Do(http.MethodPost, termPath(2026, 1)+"/unfile", map[string]any{"reason": "Again"}), http.StatusConflict, "vat_term_not_filed")
	if !strings.Contains(b.h.Logs(), "filing mark was removed") {
		t.Error("the unfiling is not logged")
	}
}

// A system voucher dated in a closed term is booked on the first open day
// (A8) and shows in the next term; a person's is refused.
func TestVatClose_LateSystemVoucherShowsInTheNextTerm(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-400.00")
	b.close(t, 2026, 1)
	code := b.code("3")
	day := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)
	customer := customerAcme
	var posted accounting.Posted
	err := db.WithTx(t.Context(), b.h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		posted, err = accounting.Post(t.Context(), tx, accounting.Voucher{Kind: "sales_invoice", Date: day, Description: "Late invoice",
			Source: &accounting.Source{Module: "invoices", Kind: "document", ID: 4242}, Rule: "sales.invoice", RuleVersion: 1,
			Lines: []accounting.Line{
				{Role: "receivables", Amount: big.NewRat(125, 1), CustomerID: &customer},
				{AccountID: b.acc("3000"), Amount: big.NewRat(-100, 1), TaxCodeID: &code},
				{AccountID: b.acc("2700"), Amount: big.NewRat(-25, 1), TaxCodeID: &code, TaxRole: "tax", TaxBase: big.NewRat(-100, 1)},
			}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !posted.Moved || posted.VoucherDate.Format(time.DateOnly) != "2026-03-01" {
		t.Fatalf("posted = %+v", posted)
	}
	if d := b.term(t, 2026, 2); d.Return.Total != "25" {
		t.Errorf("term 2's return = %+v", d.Return)
	}
	expectProblem(t, journal(b.c, "2026-02-14", "Late", line{"accountId": b.acc("1920"), "amount": "1"}, line{"accountId": b.acc("3000"), "amount": "-1"}),
		http.StatusConflict, "period_locked")
}

// The terms derived from the settings (A22): bimonthly from the term that
// holds books_start, through today's, each with its due date; the list and
// the meta say what waits.
func TestVatTerms_ListAndMeta(t *testing.T) {
	b := vatBooks(t)
	res := b.c.Do(http.MethodGet, base+"/vat/terms", nil)
	var list gen.AccountingVatTermList
	res.JSON(&list)
	if list.VatTerm != gen.AccountingVatTermBimonthly || len(list.Terms) != 5 {
		t.Fatalf("terms = %+v", list)
	}
	want := []struct{ start, end, due, name string }{
		{"2026-01-01", "2026-02-28", "2026-04-10", "januar-februar"},
		{"2026-03-01", "2026-04-30", "2026-06-10", "mars-april"},
		{"2026-05-01", "2026-06-30", "2026-08-31", "mai-juni"},
		{"2026-07-01", "2026-08-31", "2026-10-10", "juli-august"},
		{"2026-09-01", "2026-10-31", "2026-12-10", "september-oktober"},
	}
	for i, w := range want {
		got := list.Terms[i]
		if got.PeriodStart.String() != w.start || got.PeriodEnd.String() != w.end || got.DueDate.String() != w.due || got.PeriodName != w.name ||
			got.Number != int32(i+1) {
			t.Errorf("term %d = %+v, want %+v", i+1, got, w)
		}
	}
	if !list.Terms[0].NeedsClosing || !list.Terms[0].NeedsFiling || !list.Terms[3].NeedsClosing || list.Terms[3].NeedsFiling || list.Terms[4].NeedsClosing {
		t.Errorf("the attention flags = %+v", list.Terms)
	}
	res = b.c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if meta.VatTermsToClose != 4 || meta.VatTermsToFile != 3 || meta.VatNextTerm == nil || meta.VatNextTerm.Number != 1 || meta.VatRegistrationMismatch {
		t.Errorf("meta = %+v", meta)
	}
	// A reader sees the terms, and closes nothing.
	reader := b.h.SignIn(t, reader...)
	if res := reader.Do(http.MethodGet, termPath(2026, 1), nil); res.Status != http.StatusOK {
		t.Errorf("a reader's term = %d", res.Status)
	}
	for path, body := range map[string]any{"/close": nil, "/reopen": map[string]any{"reason": "x"}, "/unfile": map[string]any{"reason": "x"}} {
		if res := reader.Do(http.MethodPost, termPath(2026, 1)+path, body); res.Status != http.StatusForbidden {
			t.Errorf("a reader's %s = %d", path, res.Status)
		}
	}
	poster := b.h.SignIn(t, poster...)
	if res := poster.Do(http.MethodPost, termPath(2026, 1)+"/close", nil); res.Status != http.StatusForbidden {
		t.Errorf("a poster's close = %d", res.Status)
	}
	expectProblem(t, b.c.Do(http.MethodGet, termPath(2026, 1)+"/xml", nil), http.StatusConflict, "vat_term_not_closed")
}

// Yearly terms with a fiscal year from July and books from the middle of a
// year: one term a calendar year, due 10 March; and books that are not
// VAT-registered have none, and the meta says so.
func TestVatTerms_YearlyAndNone(t *testing.T) {
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: contracts.SellerProfile{VATRegistered: true}}))
	c := h.SignIn(t, accountant...)
	res := c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2025-08-15", "fiscalYearStartMonth": 7, "vatTerm": "yearly", "language": "en"})
	if res.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	res = c.Do(http.MethodGet, base+"/vat/terms", nil)
	var list gen.AccountingVatTermList
	res.JSON(&list)
	if len(list.Terms) != 2 || list.Terms[0].PeriodStart.String() != "2025-01-01" || list.Terms[0].PeriodEnd.String() != "2025-12-31" ||
		list.Terms[0].DueDate.String() != "2026-03-10" || list.Terms[0].PeriodName != "aarlig" || list.Terms[1].Year != 2026 {
		t.Fatalf("yearly terms = %+v", list)
	}
	d := func() gen.AccountingVatTermDetail {
		// The term starts before the books: its return covers 15 August on
		// only, and the close asks for that to be confirmed (M2).
		expectProblem(t, c.Do(http.MethodPost, termPath(2025, 1)+"/close", nil), http.StatusConflict, "vat_term_partial")
		res := c.Do(http.MethodPost, termPath(2025, 1)+"/close", map[string]any{"confirmPartial": true})
		if res.Status != http.StatusOK {
			t.Fatalf("close 2025 = %d %s", res.Status, res.Body)
		}
		var d gen.AccountingVatTermDetail
		res.JSON(&d)
		return d
	}()
	res = c.Do(http.MethodGet, termPath(2025, 1)+"/xml", nil)
	if !bytes.Contains(res.Body, []byte("<skattleggingsperiodeAar>aarlig</skattleggingsperiodeAar>")) ||
		!strings.Contains(strings.Join(d.Return.Warnings, " "), "covers 2025-08-15–2025-12-31 only") || !d.Term.Partial ||
		d.Term.CoversFrom.String() != "2025-08-15" || !d.Checks.Partial {
		t.Errorf("the yearly return = %s, warnings %v", res.Body, d.Return.Warnings)
	}

	h = newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: contracts.SellerProfile{VATRegistered: true}}))
	c = h.SignIn(t, accountant...)
	res = c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "none", "language": "en"})
	if res.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	res = c.Do(http.MethodGet, base+"/vat/terms", nil)
	res.JSON(&list)
	if list.VatTerm != gen.AccountingVatTermNone || len(list.Terms) != 0 {
		t.Errorf("terms without VAT = %+v", list)
	}
	expectProblem(t, c.Do(http.MethodGet, termPath(2026, 1), nil), http.StatusConflict, "vat_not_registered")
	expectProblem(t, c.Do(http.MethodPost, termPath(2026, 1)+"/close", nil), http.StatusConflict, "vat_not_registered")
	res = c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if meta.VatNextTerm != nil || !meta.VatRegistrationMismatch {
		t.Errorf("meta without VAT = %+v", meta)
	}
}

// validateXML holds an XML file to Skatteetaten's XSD with xmllint when it is
// on the PATH; CI runs tools/mva/validate.sh over every golden whatever this
// finds.
func validateXML(t *testing.T, file string) {
	t.Helper()
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Logf("xmllint is not on the PATH: %s is validated by tools/mva/validate.sh", file)
		return
	}
	xsd := filepath.Join("mva", "testdata", "xsd", "no.skatteetaten.fastsetting.avgift.mva.skattemeldingformerverdiavgift.v1.0.xsd")
	if out, err := exec.Command("xmllint", "--noout", "--schema", xsd, file).CombinedOutput(); err != nil {
		t.Errorf("%s does not validate: %v\n%s", file, err, out)
	}
}

// golden compares a term's XML with its golden, and validates it.
func (b *books) golden(t *testing.T, year, number int, name string) {
	t.Helper()
	res := b.c.Do(http.MethodGet, termPath(year, number)+"/xml", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("xml %d-%d = %d %s", year, number, res.Status, res.Body)
	}
	file := filepath.Join("mva", "testdata", "golden", name)
	if *update {
		if err := os.WriteFile(file, res.Body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if !bytes.Equal(res.Body, want) {
		t.Errorf("%s differs from its golden:\n%s", name, res.Body)
	}
	validateXML(t, file)
}

// The returns' XML against golden files, each valid against Skatteetaten's
// XSD (A23): a term of sales and purchases at mixed rates; one with reverse
// charge, VAT not deductible, a loss on a receivable and a reversal of an
// earlier term's purchase; and an empty one.
func TestVatReturn_Goldens(t *testing.T) {
	b := vatBooks(t)
	for _, s := range []struct{ date, account, code, amount string }{
		{"2026-01-05", "3000", "3", "-10000.40"}, {"2026-01-06", "3010", "31", "-2000.00"}, {"2026-01-07", "3020", "33", "-1500.55"},
		{"2026-01-08", "3100", "5", "-800.00"}, {"2026-01-09", "3150", "52", "-1200.00"}, {"2026-01-10", "3200", "6", "-300.00"},
		{"2026-01-11", "3000", "32", "-1000.00"}, {"2026-01-12", "4005", "12", "500.00"},
		{"2026-02-01", "6300", "1", "4000.80"}, {"2026-02-02", "6300", "11", "600.00"}, {"2026-02-03", "7000", "13", "250.50"},
		{"2026-02-04", "3000", "3", "1000.00"},
	} {
		b.taxed(t, s.date, s.account, s.code, s.amount)
	}
	late := b.taxed(t, "2026-02-25", "7000", "13", "100.00")
	for _, s := range []struct{ date, account, code, amount string }{
		{"2026-03-02", "3000", "3", "-4000.00"},
		{"2026-03-03", "6300", "86", "2000.30"}, {"2026-03-04", "6300", "87", "1000.00"},
		{"2026-03-05", "4005", "81", "5000.00"}, {"2026-03-06", "4005", "82", "400.00"}, {"2026-03-07", "4005", "85", "300.00"},
		{"2026-03-08", "6300", "88", "150.00"}, {"2026-03-09", "3900", "7", "-75.00"},
	} {
		b.taxed(t, s.date, s.account, s.code, s.amount)
	}
	b.writeOff(t, "2026-04-02", 9002, "800.00", "200.00")
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, late.Id), map[string]any{"date": "2026-04-03", "reason": "Wrong term"}); res.Status != http.StatusCreated {
		t.Fatalf("reverse = %d %s", res.Status, res.Body)
	}
	one := b.close(t, 2026, 1)
	two := b.close(t, 2026, 2)
	b.close(t, 2026, 3)
	b.golden(t, 2026, 1, "2026-1-mixed-rates.xml")
	b.golden(t, 2026, 2, "2026-2-reverse-charge-and-loss.xml")
	b.golden(t, 2026, 3, "2026-3-empty.xml")

	// The figures beside the XML: the loss on the input code, reverse charge
	// as its two lines, the reversal with the remark, the total the lines'.
	lines := map[string][]gen.AccountingVatReturnLine{}
	for _, l := range two.Return.Lines {
		lines[l.Code] = append(lines[l.Code], l)
	}
	if l := lines["1"]; len(l) != 1 || l[0].Specification == nil || *l[0].Specification != "tapPåKrav" || l[0].Vat != "-200" || l[0].Base != nil {
		t.Errorf("the loss = %+v", l)
	}
	if l := lines["86"]; len(l) != 2 || *l[0].Base != "2000" || *l[0].Rate != "25" || l[0].Vat != "500" || l[1].Vat != "-500" || l[1].Base != nil {
		t.Errorf("86 = %+v", l)
	}
	if l := lines["87"]; len(l) != 1 || l[0].Vat != "250" {
		t.Errorf("87, not deductible = %+v", l)
	}
	if l := lines["13"]; len(l) != 1 || l[0].Vat != "12" || l[0].Remark == nil || *l[0].Remark != "kreditnota" {
		t.Errorf("the reversed purchase = %+v", l)
	}
	if _, ok := lines["7"]; ok {
		t.Error("code 7 is in the return")
	}
	if one.Return.Total == "" || one.Return.Lines[0].Code != "3" || *one.Return.Lines[0].Base != "9000" {
		t.Errorf("term 1 = %+v", one.Return)
	}
}
