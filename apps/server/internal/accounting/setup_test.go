package accounting_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

// Before Set up the books, the meta says so and every other operation
// answers 409 books_not_set_up (R12).
func TestSetup_GatesEverythingUntilTheBooksAreSetUp(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	res := c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if res.Status != http.StatusOK || meta.SetUp || meta.Settings != nil || !meta.CanClose || !meta.InvoicesEnabled {
		t.Fatalf("meta before setup = %d %+v", res.Status, meta)
	}
	for _, path := range []string{"/accounts", "/tax-codes", "/vouchers", "/lock", "/opening", "/open-items", "/grouping-codes"} {
		expectProblem(t, c.Do(http.MethodGet, base+path, nil), http.StatusConflict, "books_not_set_up")
	}
	expectProblem(t, journal(c, "2026-02-01", "x", line{"accountId": 1, "amount": "1"}), http.StatusConflict, "books_not_set_up")
	if n := h.Count(t, "SELECT count(*) FROM accounting.accounts"); n != 0 {
		t.Fatalf("%d accounts before setup", n)
	}

	settings := setUp(t, c, "nb")
	if settings.BooksStart.String() != "2026-01-01" || settings.FiscalYearStartMonth != 1 || settings.LockedThrough != nil {
		t.Fatalf("settings = %+v", settings)
	}
	expectProblem(t, c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "yearly", "language": "en"}),
		http.StatusConflict, "books_already_set_up")
	res = c.Do(http.MethodGet, base+"/meta", nil)
	res.JSON(&meta)
	if !meta.SetUp || meta.Settings == nil || meta.Settings.VatTerm != "bimonthly" {
		t.Fatalf("meta after setup = %+v", meta)
	}
}

func TestSetup_RefusesBadFields(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	res := c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "weekly", "language": "de", "fiscalYearStartMonth": 13})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	var p gen.AccountingProblem
	res.JSON(&p)
	for _, field := range []string{"vatTerm", "language", "fiscalYearStartMonth"} {
		if _, ok := (*p.Errors)[field]; !ok {
			t.Errorf("errors = %v, want %s", *p.Errors, field)
		}
	}
}

// The default chart (A3, R13): its size, every role on its account, names in
// the workspace's language from Skatteetaten's list, a grouping each, and the
// tax codes with their 2026 rates and accounts (A5).
func TestSetup_SeedsTheChartTheRolesAndTheTaxCodes(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	chart := accounts(t, c)
	if n := len(chart); n < 100 || n > 140 {
		t.Errorf("the default chart has %d accounts, want 100 to 140", n)
	}
	roles := map[string]string{
		"receivables": "1500", "payables": "2400", "bank": "1920", "vat_settlement": "2740", "rounding": "7790",
		"bad_debt": "7830", "employee_payables": "2910", "opening": "2060", "year_result": "2050",
		"clearing_vipps": "1580", "charge_income": "3900", "currency_gain": "8060", "currency_loss": "8160",
		"payment_fees": "7770",
	}
	held := map[string]string{}
	for number, a := range chart {
		if a.SystemRole != nil {
			held[string(*a.SystemRole)] = number
		}
		if len(a.Groupings) != 1 || a.Groupings[0].FromYear != 2026 {
			t.Errorf("account %s groupings = %+v, want one from 2026", number, a.Groupings)
		}
	}
	for role, number := range roles {
		if held[role] != number {
			t.Errorf("role %s on %q, want %s", role, held[role], number)
		}
	}
	if len(held) != len(roles) {
		t.Errorf("roles held = %v, want exactly %v", held, roles)
	}
	if a := chart["1500"]; a.Name != "Kundefordringer" || a.Kind != "asset" || a.Groupings[0].Category != "balanseverdiForOmloepsmiddel" {
		t.Errorf("1500 = %+v", a)
	}
	if a := chart["2710"]; a.Kind != "liability" || a.Groupings[0].Code != "2740" {
		t.Errorf("2710 = %+v, want a liability grouped as 2740", a)
	}
	if a := chart["3000"]; a.Kind != "income" || a.DefaultTaxCodeId == nil {
		t.Errorf("3000 = %+v, want income with a default tax code", a)
	}
	if a := chart["8050"]; a.Kind != "income" || a.SystemRole != nil {
		t.Errorf("8050 = %+v, want ordinary income", a)
	}
	if a := chart["2050"]; a.Kind != "equity" {
		t.Errorf("2050 = %+v", a)
	}

	codes := taxCodes(t, c)
	if len(codes) != 30 {
		t.Errorf("%d tax codes, want the 30 of the standard list", len(codes))
	}
	for code, want := range map[string]string{"1": "25.00", "11": "15.00", "12": "11.11", "13": "12.00", "3": "25.00", "31": "15.00", "32": "11.11", "33": "12.00", "5": "0.00"} {
		if got := codes[code].CurrentRate; got == nil || *got != want {
			t.Errorf("code %s rate = %v, want %s", code, got, want)
		}
	}
	for _, code := range []string{"0", "7", "20", "21", "22"} {
		if codes[code].Reported {
			t.Errorf("code %s is reported, want not", code)
		}
	}
	if c3 := codes["3"]; c3.Direction != "output" || c3.VatAccountId == nil || *c3.VatAccountId != chart["2700"].Id {
		t.Errorf("code 3 = %+v, want output on 2700", c3)
	}
	if c86 := codes["86"]; !c86.ReverseCharge || c86.CounterVatAccountId == nil || *c86.CounterVatAccountId != chart["2705"].Id {
		t.Errorf("code 86 = %+v, want reverse charge against 2705", c86)
	}
	if codes["1"].Name != "Fradragsberettiget innenlands inngående merverdiavgift, 25 %" {
		t.Errorf("code 1 name = %q", codes["1"].Name)
	}
}

func TestSetup_NamesTheChartInEnglish(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	setUp(t, c, "en")
	chart := accounts(t, c)
	if chart["1920"].Name != "Bank deposits" || chart["2060"].Name != "Opening balance, contra account" {
		t.Errorf("1920 %q, 2060 %q", chart["1920"].Name, chart["2060"].Name)
	}
	if taxCodes(t, c)["3"].Name != "Output VAT, 25 %" {
		t.Errorf("code 3 = %q", taxCodes(t, c)["3"].Name)
	}
}

// Every operation is held to its permission (A1): read for the reads, post
// for journals, close for the books' settings, export for the files.
func TestPermissions_EveryEndpoint(t *testing.T) {
	h := newHarness(t)
	setUp(t, h.SignIn(t, accountant...), "en")
	none := h.SignIn(t)
	read := h.SignIn(t, reader...)
	post := h.SignIn(t, poster...)
	type call struct{ method, path string }
	reads := []call{{"GET", "/meta"}, {"GET", "/accounts"}, {"GET", "/accounts/1001"}, {"GET", "/tax-codes"},
		{"GET", "/vouchers"}, {"GET", "/vouchers/1001"}, {"GET", "/lock"}, {"GET", "/opening"}, {"GET", "/open-items"},
		{"GET", "/grouping-codes"}, {"GET", "/reports/trial_balance?from=2026-01-01&to=2026-12-31"},
		{"GET", "/vouchers/1001/attachments/1"}}
	posts := []call{{"POST", "/vouchers"}, {"POST", "/vouchers/preview"}, {"POST", "/vouchers/1001/reverse"},
		{"POST", "/vouchers/1001/attachments"}, {"POST", "/open-items/1001/settlements"}}
	closes := []call{{"POST", "/setup"}, {"PUT", "/lock"}, {"POST", "/accounts"}, {"PUT", "/accounts/1001"},
		{"DELETE", "/accounts/1001"}, {"PUT", "/accounts/1001/groupings"}, {"PUT", "/roles/bank"},
		{"PUT", "/tax-codes/1001"}, {"POST", "/tax-codes/1001/rates"}, {"POST", "/opening"}}
	exports := []call{{"GET", "/reports/journal/csv?from=2026-01-01&to=2026-12-31"}}
	check := func(cl string, calls []call, do func(call) int) {
		for _, c := range calls {
			if got := do(c); got != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403", cl, c.method, c.path, got)
			}
		}
	}
	check("no permission", reads, func(c call) int { return none.Do(c.method, base+c.path, nil).Status })
	body := func(c call) any {
		if c.method == http.MethodDelete || c.path == "/vouchers/1001/attachments" {
			return nil
		}
		return map[string]any{}
	}
	check("reader", posts, func(c call) int { return read.Do(c.method, base+c.path, body(c)).Status })
	check("poster", closes, func(c call) int { return post.Do(c.method, base+c.path, body(c)).Status })
	check("poster", exports, func(c call) int { return post.Do(c.method, base+c.path, nil).Status })
	if got := h.SignIn(t, exporter...).Do("GET", base+"/reports/journal/csv?from=2026-01-01&to=2026-12-31", nil).Status; got != http.StatusOK {
		t.Errorf("exporter's CSV = %d", got)
	}
	if got := h.SignIn(t, closer...).Do("GET", base+"/lock", nil).Status; got != http.StatusOK {
		t.Errorf("closer's lock = %d", got)
	}
}
