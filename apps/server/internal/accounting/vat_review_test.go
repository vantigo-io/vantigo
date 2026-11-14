package accounting_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// A closed term's postings stay as they were closed (H1): the lock does not
// move back behind it — the term is reopened instead — so no journal can be
// dated in it; and should its figures change anyway, the next close refuses
// and names it.
func TestVatClosedTerm_LockAndStale(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	b.close(t, 2026, 1)
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31", "reason": "Fix January"}),
		http.StatusConflict, "lock_behind_closed_term")
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": nil, "reason": "Lift"}),
		http.StatusConflict, "lock_behind_closed_term")
	expectProblem(t, journal(b.c, "2026-02-10", "Late", line{"accountId": b.acc("1920"), "amount": "1"}, line{"accountId": b.acc("3000"), "amount": "-1"}),
		http.StatusConflict, "period_locked")

	// Behind the module's back: the lock lifted in the database, a sale
	// posted in the closed term, the lock put back.
	b.h.Exec(t, `UPDATE accounting.settings SET locked_through = NULL`)
	b.taxed(t, "2026-02-10", "3000", "3", "-400.00")
	b.h.Exec(t, `UPDATE accounting.settings SET locked_through = '2026-02-28'`)
	if d := b.term(t, 2026, 2); d.Checks.StaleTerm == nil || d.Checks.StaleTerm.Number != 1 {
		t.Errorf("checks = %+v", d.Checks)
	}
	res := b.c.Do(http.MethodPost, termPath(2026, 2)+"/close", nil)
	expectProblem(t, res, http.StatusConflict, "vat_term_stale")
	if !strings.Contains(string(res.Body), "2026-1") {
		t.Errorf("the refusal does not name the term: %s", res.Body)
	}
}

// Every close is kept (H2): a reopening leaves the closing with its return
// and its XML, a second close is closing 2, the filing names the closing it
// filed, and each closing's figures and XML stay downloadable.
func TestVatClosings_AreKept(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	first := b.close(t, 2026, 1)
	if first.Term.Closing == nil || *first.Term.Closing != 1 || len(first.Closings) != 1 || !first.Closings[0].Current {
		t.Fatalf("first close = %+v", first.Term)
	}
	if res := b.c.Do(http.MethodPost, termPath(2026, 1)+"/reopen", map[string]any{"reason": "A sale is missing"}); res.Status != http.StatusOK {
		t.Fatalf("reopen = %d %s", res.Status, res.Body)
	}
	b.taxed(t, "2026-02-10", "3000", "3", "-400.00")
	second := b.close(t, 2026, 1)
	if *second.Term.Closing != 2 || len(second.Closings) != 2 || second.Closings[0].Closing != 2 || !second.Closings[0].Current ||
		second.Closings[1].Current || second.Closings[1].Total != "250" || second.Return.Total != "350" {
		t.Fatalf("second close = %+v %+v", second.Term, second.Closings)
	}
	res := b.c.Do(http.MethodGet, termPath(2026, 1)+"/closings/1", nil)
	var one gen.AccountingVatReturn
	res.JSON(&one)
	if res.Status != http.StatusOK || one.Total != "250" {
		t.Errorf("closing 1 = %d %+v", res.Status, one)
	}
	res = b.c.Do(http.MethodGet, termPath(2026, 1)+"/closings/1/xml", nil)
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "<fastsattMerverdiavgift>250</fastsattMerverdiavgift>") ||
		!strings.Contains(string(res.Body), "vantigo-2026-1-1") || !strings.Contains(res.Header("Content-Disposition"), "mva-melding-2026-1-1.xml") {
		t.Errorf("closing 1's XML = %d %s", res.Status, res.Body)
	}
	if res := b.c.Do(http.MethodGet, termPath(2026, 1)+"/closings/3", nil); res.Status != http.StatusNotFound {
		t.Errorf("closing 3 = %d", res.Status)
	}
	res = b.c.Do(http.MethodPost, termPath(2026, 1)+"/filing", map[string]any{"filedAt": "2026-03-20", "reference": "AR-9"})
	var filed gen.AccountingVatTermDetail
	res.JSON(&filed)
	if filed.Term.FiledClosing == nil || *filed.Term.FiledClosing != 2 || !filed.Closings[0].Filed || filed.Events[0].Closing == nil ||
		*filed.Events[0].Closing != 2 {
		t.Errorf("filed = %+v %+v", filed.Term, filed.Closings)
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vat_term_closings"); n != 2 {
		t.Errorf("%d closings kept, want 2", n)
	}
}

// Balances on VAT accounts at the books' start (H3): the opening refuses a
// line on one — unpaid VAT belongs on the VAT settlement account — and an
// opening voucher, however it was made, is no part of a term's VAT.
func TestVatOpening_VatAccounts(t *testing.T) {
	b := vatBooks(t)
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "5000"},
		{"accountId": b.acc("2700"), "amount": "-5000"},
	}})
	expectProblem(t, res, http.StatusBadRequest, "opening_vat_account")
	if !strings.Contains(string(res.Body), "lines[1].accountId") {
		t.Errorf("the refusal names no line: %s", res.Body)
	}
	if res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "5000"},
		{"accountId": b.acc("2740"), "amount": "-5000"},
	}}); res.Status != http.StatusCreated {
		t.Fatalf("an opening on 2740 = %d %s", res.Status, res.Body)
	}
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	// An opening voucher posted on a VAT account by code is left out too.
	err := db.WithTx(t.Context(), b.h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := accounting.Post(t.Context(), tx, accounting.Voucher{Kind: "opening", Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Description: "Opening on a VAT account", Lines: []accounting.Line{
				{AccountID: b.acc("2710"), Amount: big.NewRat(300, 1)}, {Role: "opening", Amount: big.NewRat(-300, 1)}}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	d := b.term(t, 2026, 1)
	if !d.Checks.Reconciled || d.Specification.Reconciliation.Movement != "-250.00" {
		t.Fatalf("with an opening on 2710 = %+v", d.Specification.Reconciliation)
	}
	if d := b.close(t, 2026, 1); d.Return.Total != "250" {
		t.Errorf("closed = %+v", d.Return)
	}
}

// A tax code's VAT and counter accounts are fixed once a posting carries it
// (H4); its name and activity still change, and the specification with them
// stays as it was.
func TestVatTaxCodeAccountsFreeze(t *testing.T) {
	b := vatBooks(t)
	code := b.codes["1"]
	body := func(vat int32, name string) map[string]any {
		return map[string]any{"name": name, "vatAccountId": vat, "counterVatAccountId": nil, "active": true, "revision": code.Revision}
	}
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, code.Id), body(b.acc("2711"), code.Name))
	if res.Status != http.StatusOK {
		t.Fatalf("an unused code's account = %d %s", res.Status, res.Body)
	}
	res.JSON(&code)
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, code.Id), body(b.acc("2710"), code.Name))
	res.JSON(&code)
	b.taxed(t, "2026-01-10", "6300", "1", "400.00")
	spec := func() string {
		res := b.c.Do(http.MethodGet, base+"/vat/specification?from=2026-01-01&to=2026-02-28", nil)
		var s gen.AccountingVatSpecification
		res.JSON(&s)
		raw, _ := json.Marshal([]any{s.Codes, s.Reconciliation.Accounts})
		return string(raw)
	}
	before := spec()
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, code.Id), body(b.acc("6300"), code.Name)),
		http.StatusConflict, "tax_code_in_use")
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, code.Id), body(b.acc("2710"), code.Name))
	if res.Status != http.StatusOK {
		t.Fatalf("the same account = %d %s", res.Status, res.Body)
	}
	if after := spec(); after != before {
		t.Errorf("the specification changed with an allowed edit:\n%s\n%s", before, after)
	}
}

// One key per kind of term (M3), and the books' VAT term fixed once a term
// has been closed.
func TestVatTerms_KindIsTheKey(t *testing.T) {
	b := vatBooks(t)
	b.close(t, 2026, 1)
	_, err := b.h.Pool().Exec(t.Context(), `UPDATE accounting.settings SET vat_term = 'yearly'`)
	if err == nil || !strings.Contains(err.Error(), "VAT term is fixed") {
		t.Errorf("changing vat_term after a close = %v", err)
	}
	b.h.Exec(t, `INSERT INTO accounting.vat_terms (year, term, term_kind, period_start, period_end, state, updated_at)
		VALUES (2026, 1, 'yearly', '2026-01-01', '2026-12-31', 'open', now())`)
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vat_terms WHERE year = 2026 AND term = 1"); n != 2 {
		t.Errorf("%d rows for 2026 term 1, want a two-month one and a yearly one", n)
	}
}

// The drill-down's sum is the whole selection's, beyond the page (L4); the
// meta computes no figures for an open term (L6).
func TestVatPostingsSumAndMeta(t *testing.T) {
	b := vatBooks(t)
	b.taxed(t, "2026-01-10", "3000", "3", "-1000.00")
	b.taxed(t, "2026-01-11", "3000", "3", "-200.00")
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vat/postings?from=2026-01-01&to=2026-02-28&taxCodeId=%d&part=base&limit=1", base, b.code("3")), nil)
	var list gen.AccountingVatPostingList
	res.JSON(&list)
	if len(list.Items) != 1 || list.Total != 2 || list.Sum != "-1200.00" {
		t.Errorf("a page of one = %+v", list)
	}
	res = b.c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if meta.VatNextTerm == nil || meta.VatNextTerm.Total != nil || meta.VatTermsToClose != 4 {
		t.Errorf("meta = %+v", meta.VatNextTerm)
	}
	b.close(t, 2026, 1)
	res = b.c.Do(http.MethodGet, base+"/meta", nil)
	res.JSON(&meta)
	if meta.VatNextTerm.Total == nil || *meta.VatNextTerm.Total != "300" || meta.VatNextTerm.Closing == nil {
		t.Errorf("meta after the close = %+v", meta.VatNextTerm)
	}
}
