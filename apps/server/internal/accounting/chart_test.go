package accounting_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

// The chart's rules (A3): a number is unique; an account with postings keeps
// its number and kind and is never deleted; a role moves, never leaves; the
// groupings are per year and from Skatteetaten's list.
func TestChart_Rules(t *testing.T) {
	b := newBooks(t)
	res := b.c.Do(http.MethodPost, base+"/accounts", map[string]any{"number": "6301", "name": "Leie lager", "kind": "expense",
		"defaultTaxCodeId": b.code("1"), "grouping": map[string]any{"fromYear": 2026, "category": "annenDriftskostnad", "code": "6300"}})
	if res.Status != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Status, res.Body)
	}
	var a gen.AccountingAccount
	res.JSON(&a)
	if a.Number != "6301" || len(a.Groupings) != 1 || a.HasPostings {
		t.Fatalf("created = %+v", a)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/accounts/%d", base, a.Id), nil)
	var read gen.AccountingAccount
	res.JSON(&read)
	if res.Status != http.StatusOK || read.Name != "Leie lager" || read.Groupings[0].Code != "6300" {
		t.Errorf("read = %d %+v", res.Status, read)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/accounts", map[string]any{"number": "6301", "name": "Again", "kind": "expense"}),
		http.StatusConflict, "account_number_taken")
	if got := b.c.Do(http.MethodPost, base+"/accounts", map[string]any{"number": "63", "name": "", "kind": "cost"}).Status; got != http.StatusBadRequest {
		t.Errorf("bad account = %d", got)
	}

	// Before its first posting, number and kind may change.
	update := func(a gen.AccountingAccount, number, kind string) *gen.AccountingAccount {
		res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d", base, a.Id), map[string]any{"number": number, "name": a.Name + " x",
			"kind": kind, "defaultTaxCodeId": a.DefaultTaxCodeId, "active": true, "revision": a.Revision})
		if res.Status != http.StatusOK {
			return nil
		}
		var out gen.AccountingAccount
		res.JSON(&out)
		return &out
	}
	changed := update(a, "6302", "expense")
	if changed == nil || changed.Number != "6302" || changed.Revision != a.Revision+1 {
		t.Fatalf("changed = %+v", changed)
	}
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d", base, a.Id), map[string]any{"number": "6302", "name": "x",
		"kind": "expense", "defaultTaxCodeId": nil, "active": true, "revision": a.Revision}), http.StatusConflict, "revision_conflict")
	mustPost(t, b.c, "2026-02-01", "Rent", line{"accountId": a.Id, "amount": "10"}, line{"accountId": b.acc("1920"), "amount": "-10"})
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d", base, a.Id), map[string]any{"number": "6303", "name": "x",
		"kind": "expense", "defaultTaxCodeId": nil, "active": true, "revision": changed.Revision}), http.StatusConflict, "account_has_postings")
	if renamed := update(*changed, "6302", "expense"); renamed == nil || !renamed.HasPostings {
		t.Errorf("a posted account's name could not change: %+v", renamed)
	}
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/accounts/%d", base, a.Id), nil), http.StatusConflict, "account_has_postings")
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/accounts/%d", base, b.acc("1920")), nil), http.StatusConflict, "account_has_role")
	if got := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/accounts/%d", base, b.acc("1150")), nil).Status; got != http.StatusNoContent {
		t.Errorf("delete an unused account = %d", got)
	}
	if got := b.c.Do(http.MethodGet, fmt.Sprintf("%s/accounts/%d", base, b.acc("1150")), nil).Status; got != http.StatusNotFound {
		t.Errorf("deleted account = %d", got)
	}
	// The database keeps the freeze too.
	if _, err := b.h.Pool().Exec(t.Context(), "UPDATE accounting.accounts SET number = '6309' WHERE id = $1", a.Id); err == nil {
		t.Error("the database let a posted account's number change")
	}

	// A role moves to another account and leaves the one that held it.
	res = b.c.Do(http.MethodPost, base+"/accounts", map[string]any{"number": "1921", "name": "Driftskonto 2", "kind": "asset"})
	var second gen.AccountingAccount
	res.JSON(&second)
	res = b.c.Do(http.MethodPut, base+"/roles/bank", map[string]any{"accountId": second.Id})
	if res.Status != http.StatusOK {
		t.Fatalf("move role = %d %s", res.Status, res.Body)
	}
	chart := accounts(t, b.c)
	if chart["1921"].SystemRole == nil || *chart["1921"].SystemRole != "bank" || chart["1920"].SystemRole != nil {
		t.Errorf("bank role: 1921 %v, 1920 %v", chart["1921"].SystemRole, chart["1920"].SystemRole)
	}
	expectProblem(t, b.c.Do(http.MethodPut, base+"/roles/bank", map[string]any{"accountId": b.acc("1500")}), 400, "account_has_role")

	// Groupings per year, from the list.
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d/groupings", base, a.Id), map[string]any{"groupings": []map[string]any{
		{"fromYear": 2026, "category": "annenDriftskostnad", "code": "6300"}, {"fromYear": 2027, "category": "annenDriftskostnad", "code": "6340"}}})
	res.JSON(&a)
	if res.Status != http.StatusOK || len(a.Groupings) != 2 || a.Groupings[1].Code != "6340" {
		t.Errorf("groupings = %d %+v", res.Status, a.Groupings)
	}
	if got := b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d/groupings", base, a.Id), map[string]any{"groupings": []map[string]any{
		{"fromYear": 2026, "category": "annenDriftskostnad", "code": "9999"}}}).Status; got != http.StatusBadRequest {
		t.Errorf("a code not in the list = %d", got)
	}
	res = b.c.Do(http.MethodGet, base+"/grouping-codes?year=2026&q=kundefordringer", nil)
	var codes gen.AccountingGroupingCodeList
	res.JSON(&codes)
	if codes.Year != 2026 || len(codes.Items) == 0 || codes.Items[0].Code != "1500" {
		t.Errorf("grouping codes = %+v", codes)
	}
	res = b.c.Do(http.MethodGet, base+"/grouping-codes", nil)
	res.JSON(&codes)
	if len(codes.Items) != 532 {
		t.Errorf("the whole list has %d codes, want 532", len(codes.Items))
	}
}

// The tax codes (A5): name, accounts and activity may change; a new rate
// period closes the open one the day before.
func TestTaxCodes(t *testing.T) {
	b := newBooks(t)
	c := b.codes["3"]
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, c.Id), map[string]any{"name": "Salg 25", "vatAccountId": b.acc("2700"),
		"counterVatAccountId": nil, "active": true, "revision": c.Revision})
	var got gen.AccountingTaxCode
	res.JSON(&got)
	if res.Status != http.StatusOK || got.Name != "Salg 25" {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/tax-codes/%d", base, c.Id), map[string]any{"name": "x", "vatAccountId": nil,
		"counterVatAccountId": nil, "active": true, "revision": c.Revision}), http.StatusConflict, "revision_conflict")
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/tax-codes/%d/rates", base, c.Id), map[string]any{"ratePercent": "26", "validFrom": "2027-01-01"})
	res.JSON(&got)
	if res.Status != http.StatusCreated || len(got.Rates) != 2 || got.Rates[0].ValidTo == nil || got.Rates[0].ValidTo.String() != "2026-12-31" ||
		got.Rates[1].RatePercent != "26.00" || *got.CurrentRate != "25.00" {
		t.Fatalf("rate = %d %+v", res.Status, got)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/tax-codes/%d/rates", base, c.Id), map[string]any{"ratePercent": "27", "validFrom": "2026-06-01"}),
		400, "rate_period_invalid")
	// A journal dated in 2027 takes the new rate.
	res = b.c.Do(http.MethodPost, base+"/vouchers/preview", map[string]any{"voucherDate": "2027-02-01", "description": "x",
		"lines": []line{{"accountId": b.acc("3000"), "amount": "-100", "taxCodeId": c.Id}}})
	var p gen.AccountingJournalPreview
	res.JSON(&p)
	if len(p.Lines) != 2 || p.Lines[1].Amount != "-26.00" {
		t.Errorf("2027 preview = %+v", p.Lines)
	}
}
