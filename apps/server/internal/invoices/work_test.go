package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The uninvoiced view and the wizard (invoices work design D3, D4, D6,
// D10-D12, D14, D15), through the API: the work as the billable reads answer
// it (fakeBillable), the projects as the directory does (fakeProjects), the
// people as identity names them.

const (
	workPath     = "/api/v1/invoices/work"
	fromWorkPath = "/api/v1/invoices/from-work"
)

// The September work on Acme's projects: on project 41 Kari's 4 h and Ola's
// 3.5 h at 1 200, an outlay, a mileage and a milestone; on project 42 Kari's
// 2 h at 1 000.
const (
	workHourKari = 801
	workHourOla  = 802
	workHour42   = 803
	workOutlay   = 901
	workMileage  = 902
	workMilestne = 951
)

// workFixture is an installation with the work above composed as all three
// billable reads, the two projects and a complete, VAT-registered seller.
type workFixture struct {
	h        *harness
	billable *fakeBillable
	projects *fakeProjects
	kari     uuid.UUID
	ola      uuid.UUID
}

func wDay(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

// namedUser is a user identity names name.
func namedUser(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	_, id := h.SignInUser(t)
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, id, name)
	return id
}

func newWorkFixture(t *testing.T, opts ...modtest.Option) workFixture {
	t.Helper()
	f := workFixture{billable: newFakeBillable(), projects: newFakeProjects()}
	f.h = newHarness(t, append(append(f.billable.options(), modtest.WithProjects(f.projects)), opts...)...)
	saveSeller(t, f.h, completeSeller(1))
	f.kari, f.ola = namedUser(t, f.h, "Kari Nordmann"), namedUser(t, f.h, "Ola Hansen")
	b := f.billable
	b.putHour(contracts.BillableHour{ID: workHourKari, Revision: 2, ProjectID: project41, UserID: f.kari, Date: wDay("2026-09-01"),
		HoursHundredths: 400, BillRate: "1200.00", Currency: "NOK", Amount: "4800.00000000"})
	b.putHour(contracts.BillableHour{ID: workHourOla, Revision: 1, ProjectID: project41, UserID: f.ola, Date: wDay("2026-09-02"),
		HoursHundredths: 350, BillRate: "1200.00", Currency: "NOK", Amount: "4200.00000000"})
	b.putHour(contracts.BillableHour{ID: workHour42, Revision: 1, ProjectID: project42, UserID: f.kari, Date: wDay("2026-09-03"),
		HoursHundredths: 200, BillRate: "1000.00", Currency: "NOK", Amount: "2000.00000000"})
	b.putExpense(contracts.BillableExpense{ID: workOutlay, Revision: 4, ProjectID: project41, Kind: "outlay", Date: wDay("2026-09-03"),
		Description: "Hotell Bergen", NetAmount: "1500.00", BillAmount: "1500.00", Currency: "NOK"})
	b.putExpense(contracts.BillableExpense{ID: workMileage, Revision: 1, ProjectID: project41, Kind: "mileage", Date: wDay("2026-09-04"),
		NetAmount: "405.00", DistanceKm: ptrTo("90.0"), BillRatePerKm: ptrTo("4.50"), BillAmount: "405.00", Currency: "NOK"})
	b.putMilestone(contracts.BillableMilestone{ID: workMilestne, Revision: 1, ProjectID: project41, Name: "Fase 1",
		ReadyAt: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), Amount: "10000.00", Currency: "NOK"})
	return f
}

// viewJSON is GET /invoices/work as a client reads it.
type viewJSON struct {
	CustomerID *int32 `json:"customerId"`
	Projects   []struct {
		ID           int32   `json:"id"`
		Code         string  `json:"code"`
		Name         string  `json:"name"`
		BillingType  string  `json:"billingType"`
		Currency     *string `json:"currency"`
		Hours        []viewRowJSON
		Expenses     []viewRowJSON
		Milestones   []viewRowJSON
		HeldOnDrafts []struct {
			InvoiceID int64  `json:"invoiceId"`
			Kind      string `json:"kind"`
			Count     int32  `json:"count"`
		} `json:"heldOnDrafts"`
		Warnings []string `json:"warnings"`
	} `json:"projects"`
	Totals []struct {
		Currency string  `json:"currency"`
		Amount   float64 `json:"amount"`
	} `json:"totals"`
	Users []struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"displayName"`
	} `json:"users"`
	Warnings []string `json:"warnings"`
}

// viewRowJSON is the part of a row of any kind the tests read.
type viewRowJSON struct {
	ID         int64       `json:"id"`
	Revision   int32       `json:"revision"`
	Date       string      `json:"date"`
	Hours      float64     `json:"hours"`
	Rate       float64     `json:"rate"`
	Amount     float64     `json:"amount"`
	BillAmount float64     `json:"billAmount"`
	Currency   string      `json:"currency"`
	Selectable bool        `json:"selectable"`
	Reason     *string     `json:"reason"`
	HeldBy     *heldByJSON `json:"heldBy"`
	Warnings   []string    `json:"warnings"`
}

func getView(t *testing.T, h *harness, query string) viewJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodGet, workPath+"?"+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/work?%s = %d %s", query, res.Status, res.Body)
	}
	var v viewJSON
	res.JSON(&v)
	return v
}

// rowsOf is every row of the view as "kind:id selectable|reason", in order.
func rowsOf(v viewJSON) []string {
	var out []string
	add := func(p int32, kind string, rows []viewRowJSON) {
		for _, r := range rows {
			state := "selectable"
			if !r.Selectable {
				state = "-"
				if r.Reason != nil {
					state = *r.Reason
				}
			}
			out = append(out, fmt.Sprintf("%d/%s:%d %s", p, kind, r.ID, state))
		}
	}
	for _, p := range v.Projects {
		add(p.ID, "h", p.Hours)
		add(p.ID, "e", p.Expenses)
		add(p.ID, "m", p.Milestones)
	}
	return out
}

func totalOf(v viewJSON, currency string) float64 {
	for _, t := range v.Totals {
		if t.Currency == currency {
			return t.Amount
		}
	}
	return 0
}

// workSrc is one source of a wizard's body.
func workSrc(kind string, id int64, revision int32) map[string]any {
	return map[string]any{"kind": kind, "id": id, "revision": revision}
}

// allSeptember is every piece of project 41's September work, at the
// revisions the view shows.
func allSeptember() []map[string]any {
	return []map[string]any{
		workSrc("time.entry", workHourKari, 2), workSrc("time.entry", workHourOla, 1),
		workSrc("expenses.entry", workOutlay, 4), workSrc("expenses.entry", workMileage, 1),
		workSrc("projects.milestone", workMilestne, 1),
	}
}

func fromWorkBody(customer int32, sources ...map[string]any) map[string]any {
	return map[string]any{"customerId": customer, "sources": sources}
}

// wizardDocJSON is the wizard's answer as the tests read it.
type wizardDocJSON struct {
	ID               int64    `json:"id"`
	Kind             string   `json:"kind"`
	Status           string   `json:"status"`
	CustomerID       int32    `json:"customerId"`
	DeliveryDate     *string  `json:"deliveryDate"`
	DeliveryFrom     *string  `json:"deliveryFrom"`
	DeliveryTo       *string  `json:"deliveryTo"`
	PaymentTermsDays *int32   `json:"paymentTermsDays"`
	YourReference    string   `json:"yourReference"`
	Revision         int32    `json:"revision"`
	NetTotal         float64  `json:"netTotal"`
	Warnings         []string `json:"warnings"`
	Lines            []struct {
		Position    int32        `json:"position"`
		Description string       `json:"description"`
		Unit        string       `json:"unit"`
		Quantity    float64      `json:"quantity"`
		UnitPrice   float64      `json:"unitPrice"`
		VatCodeID   int32        `json:"vatCodeId"`
		LineNet     float64      `json:"lineNet"`
		Sources     []sourceJSON `json:"sources"`
		Warnings    []string     `json:"warnings"`
	} `json:"lines"`
	Sources *struct {
		Count int32 `json:"count"`
		Held  int32 `json:"held"`
	} `json:"sources"`
}

func postFromWork(t *testing.T, c *modtest.Client, body map[string]any) (*modtest.Response, wizardDocJSON) {
	t.Helper()
	res := c.Do(http.MethodPost, fromWorkPath, body)
	var doc wizardDocJSON
	if res.Status == http.StatusCreated || res.Status == http.StatusOK {
		res.JSON(&doc)
	}
	return res, doc
}

// fromWork posts body and answers the new draft, failing unless it was made.
func fromWork(t *testing.T, h *harness, body map[string]any) wizardDocJSON {
	t.Helper()
	res, doc := postFromWork(t, creator(t, h), body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/from-work = %d %s, want 201", res.Status, res.Body)
	}
	return doc
}

// refusedFromWork asserts the wizard answers 409 code and answers the problem.
func refusedFromWork(t *testing.T, h *harness, body map[string]any, code string) problemJSON {
	t.Helper()
	res, _ := postFromWork(t, creator(t, h), body)
	if res.Status != http.StatusConflict {
		t.Fatalf("POST /invoices/from-work = %d %s, want 409 %s", res.Status, res.Body, code)
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Fatalf("POST /invoices/from-work = %s (%s), want %s", p.Code, p.Detail, code)
	}
	return p
}

func linesOf(doc wizardDocJSON) []string {
	var out []string
	for _, l := range doc.Lines {
		var refs []string
		for _, s := range l.Sources {
			refs = append(refs, fmt.Sprintf("%s:%d", s.Kind, s.ID))
		}
		slices.Sort(refs)
		out = append(out, fmt.Sprintf("%s | %s | %g × %g | %d | %s", l.Description, l.Unit, l.Quantity, l.UnitPrice, l.VatCodeID,
			strings.Join(refs, " ")))
	}
	return out
}

// The view per customer — every project the customer is billed for, by code —
// and per project, each kind's rows as the billable reads answer them, every
// one selectable here; the totals over them; the people the hours name; an
// until bound passed to the reads.
func TestWork_TheViewPerCustomerAndPerProject(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	v := getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	if v.CustomerID == nil || *v.CustomerID != customerAcme {
		t.Errorf("customerId = %v, want %d", v.CustomerID, customerAcme)
	}
	want := []string{
		"41/h:801 selectable", "41/h:802 selectable", "41/e:901 selectable", "41/e:902 selectable", "41/m:951 selectable",
		"42/h:803 selectable",
	}
	if got := rowsOf(v); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if p := v.Projects[0]; p.Code != "P-41" || p.Name != "Project 41" || p.BillingType != "time-and-materials" || p.Currency == nil || *p.Currency != "NOK" {
		t.Errorf("project = %+v", p)
	}
	if got := totalOf(v, "NOK"); got != 4800+4200+1500+405+10000+2000 {
		t.Errorf("NOK total = %v, want 22905", got)
	}
	if h := v.Projects[0].Hours[0]; h.Hours != 4 || h.Rate != 1200 || h.Amount != 4800 || h.Revision != 2 || h.Date != "2026-09-01" {
		t.Errorf("Kari's hour = %+v", h)
	}
	if m := v.Projects[0].Milestones[0]; m.Date != "2026-09-05" || m.Amount != 10000 {
		t.Errorf("the milestone = %+v", m)
	}
	var names []string
	for _, u := range v.Users {
		names = append(names, u.DisplayName)
	}
	if !slices.Equal(names, []string{"Kari Nordmann", "Ola Hansen"}) {
		t.Errorf("users = %v", names)
	}

	one := getView(t, f.h, fmt.Sprintf("projectId=%d&until=2026-09-02", project41))
	if got := rowsOf(one); !slices.Equal(got, []string{"41/h:801 selectable", "41/h:802 selectable"}) {
		t.Errorf("project 41 until the 2nd = %v", got)
	}
	if one.CustomerID == nil || *one.CustomerID != customerAcme {
		t.Errorf("a project's view names its customer: %v", one.CustomerID)
	}
	if res := creator(t, f.h).Do(http.MethodGet, workPath+"?projectId=4040", nil); res.Status != http.StatusNotFound {
		t.Errorf("an unknown project = %d %s, want 404", res.Status, res.Body)
	}
	// Every read was made on the pool, by project and with its bound.
	for _, r := range f.billable.reads() {
		if r.locked {
			t.Errorf("%s was read under a lock", r.method)
		}
	}
}

// Work a live draft holds is listed, not selectable: it names the draft
// (heldBy), counts in no total, and the project says how much each draft
// holds of it (heldOnDrafts).
func TestWork_HeldWorkIsListedNotSelectable(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	d := fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2), workSrc("expenses.entry", workOutlay, 4)))
	v := getView(t, f.h, fmt.Sprintf("projectId=%d", project41))
	want := []string{"41/h:801 held", "41/h:802 selectable", "41/e:901 held", "41/e:902 selectable", "41/m:951 selectable"}
	if got := rowsOf(v); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if by := v.Projects[0].Hours[0].HeldBy; by == nil || by.InvoiceID != d.ID || by.Status != "draft" || by.Number != nil {
		t.Errorf("heldBy = %+v, want draft %d", by, d.ID)
	}
	if got := totalOf(v, "NOK"); got != 4200+405+10000 {
		t.Errorf("NOK total = %v, want 14605 — held work counts in no total", got)
	}
	held := v.Projects[0].HeldOnDrafts
	if len(held) != 2 || held[0].InvoiceID != d.ID || held[0].Count != 1 || held[1].Count != 1 ||
		held[0].Kind != "expenses.entry" || held[1].Kind != "time.entry" {
		t.Errorf("heldOnDrafts = %+v, want one expense and one hour on draft %d", held, d.ID)
	}
}

// Exactly one of customerId and projectId.
func TestWork_BothOrNeitherIdIsA400(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	for _, q := range []string{"", fmt.Sprintf("customerId=%d&projectId=%d", customerAcme, project41)} {
		res := creator(t, f.h).Do(http.MethodGet, workPath+"?"+q, nil)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["customerId"]) == 0 {
			t.Errorf("?%s = %d %s, want 400 on customerId", q, res.Status, res.Body)
		}
	}
	if got := f.billable.reads(); len(got) != 0 {
		t.Errorf("reads = %v, want none before the body passed", got)
	}
}

// With no billable read composed the view is 409 work_unavailable, before any
// directory read; with the projects module off, projects_unavailable.
func TestWork_WorkUnavailable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithProjects(newFakeProjects()))
	c, user := h.SignInUser(t, "invoices:access", "invoices:create")
	res := c.Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", workPath, customerAcme), nil)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "work_unavailable" {
		t.Errorf("no billable read = %d %s, want 409 work_unavailable", res.Status, res.Body)
	}
	if calls := contractCalls.by(user); len(calls) != 0 {
		t.Errorf("calls = %+v, want none", calls)
	}
	noProjects := newHarness(t, newFakeBillable().options()...)
	res = creator(t, noProjects).Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", workPath, customerAcme), nil)
	if res.Status != http.StatusConflict || problemOf(t, res).Code != "projects_unavailable" {
		t.Errorf("no projects module = %d %s, want 409 projects_unavailable", res.Status, res.Body)
	}
}

// A fixed-price project's hours are listed as information, not selectable,
// and its milestones are what it invoices (D14); a non-billable project's
// work, of any kind, is listed the same way. Neither counts in a total.
func TestWork_FixedPriceAndNonBillableAreListedNotSelectable(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	f.projects.edit(project41, func(p *contracts.ProjectEntry) { p.BillingType = "fixed-price" })
	f.projects.edit(project42, func(p *contracts.ProjectEntry) { p.BillingType = "non-billable" })
	v := getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	want := []string{
		"41/h:801 fixed_price", "41/h:802 fixed_price", "41/e:901 selectable", "41/e:902 selectable", "41/m:951 selectable",
		"42/h:803 non_billable",
	}
	if got := rowsOf(v); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if got := totalOf(v, "NOK"); got != 1500+405+10000 {
		t.Errorf("NOK total = %v, want 11905", got)
	}
}

// D12: a project warns work_overdue_to_invoice when its oldest selectable
// work is dated more than a calendar month before today (2026-09-12 in
// Oslo): the 12th of August is not, the 11th is. Work that is not selectable
// does not count.
func TestWork_TheOverdueWarningAtItsBoundary(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	f.billable.putHour(contracts.BillableHour{ID: 811, Revision: 1, ProjectID: project42, UserID: f.kari, Date: wDay("2026-08-12"),
		HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"})
	v := getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	for _, p := range v.Projects {
		if len(p.Warnings) != 0 {
			t.Errorf("project %d warns %v with its oldest work on 2026-08-12, a month and no more", p.ID, p.Warnings)
		}
	}
	f.billable.putHour(contracts.BillableHour{ID: 812, Revision: 1, ProjectID: project42, UserID: f.kari, Date: wDay("2026-08-11"),
		HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"})
	v = getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	if w := v.Projects[1].Warnings; !slices.Equal(w, []string{"work_overdue_to_invoice"}) {
		t.Errorf("project 42 with work of 2026-08-11 warns %v, want work_overdue_to_invoice", w)
	}
	if w := v.Projects[0].Warnings; len(w) != 0 {
		t.Errorf("project 41 warns %v", w)
	}
	// Held, the old work no longer counts.
	fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", 812, 1)))
	v = getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	if w := v.Projects[1].Warnings; len(w) != 0 {
		t.Errorf("with the old work held, project 42 warns %v", w)
	}
}

// D11: work in another currency than NOK is listed with currency_not_nok and
// is not selectable; the totals are per currency, over the selectable work.
func TestWork_CurrencyNotNok(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	f.billable.putExpense(contracts.BillableExpense{ID: 903, Revision: 1, ProjectID: project42, Kind: "outlay", Date: wDay("2026-09-06"),
		Description: "Hotel Stockholm", NetAmount: "300.00", BillAmount: "300.00", Currency: "EUR"})
	v := getView(t, f.h, fmt.Sprintf("projectId=%d", project42))
	e := v.Projects[0].Expenses[0]
	if e.Selectable || e.Reason == nil || *e.Reason != "currency" || !slices.Equal(e.Warnings, []string{"currency_not_nok"}) {
		t.Errorf("the EUR outlay = %+v, want not selectable for its currency, warning currency_not_nok", e)
	}
	if len(v.Totals) != 1 || v.Totals[0].Currency != "NOK" || v.Totals[0].Amount != 2000 {
		t.Errorf("totals = %+v, want NOK 2000 only", v.Totals)
	}
}

// D15: a supplier invoice Expenses says is already invoiced elsewhere warns
// supplier_invoice_rebilled, and so do both rows of a pair this answer
// repeats — the supplier compared trimmed and case-folded.
func TestWork_SupplierInvoiceRebilledOnBothRowsOfAPair(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	supplier := func(id int64, name, number string, rebilled bool) {
		f.billable.putExpense(contracts.BillableExpense{ID: id, Revision: 1, ProjectID: project42, Kind: "supplier_invoice",
			Date: wDay("2026-09-07"), Supplier: name, SupplierInvoiceNumber: number, NetAmount: "100", BillAmount: "100",
			Currency: "NOK", SupplierInvoiceRebilled: rebilled})
	}
	supplier(904, "Byggmakker AS", "F-1", false)
	supplier(905, " byggmakker as ", "F-1", false)
	supplier(906, "Byggmakker AS", "F-2", false)
	supplier(907, "Rør AS", "9", true)
	v := getView(t, f.h, fmt.Sprintf("projectId=%d", project42))
	got := map[int64][]string{}
	for _, e := range v.Projects[0].Expenses {
		got[e.ID] = e.Warnings
	}
	for id, want := range map[int64][]string{
		904: {"supplier_invoice_rebilled"}, 905: {"supplier_invoice_rebilled"}, 906: nil, 907: {"supplier_invoice_rebilled"},
	} {
		if !slices.Equal(got[id], want) {
			t.Errorf("expense %d warns %v, want %v", id, got[id], want)
		}
	}
	for _, e := range v.Projects[0].Expenses {
		if !e.Selectable {
			t.Errorf("expense %d is not selectable; a warning never refuses", e.ID)
		}
	}
}

// A billable read with more than a page says so: work_truncated; so does a
// customer with as many projects as the directory answers.
func TestWork_Truncated(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	if v := getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme)); len(v.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", v.Warnings)
	}
	f.billable.answerMore(true)
	if v := getView(t, f.h, fmt.Sprintf("customerId=%d", customerAcme)); !slices.Equal(v.Warnings, []string{"work_truncated"}) {
		t.Errorf("warnings = %v, want work_truncated", v.Warnings)
	}
	// The directory answers at most MaxActualsRequests projects: a customer
	// with exactly that many may have more, whose work is not listed.
	g := newWorkFixture(t)
	for id := int32(1000); g.projects.count() < contracts.MaxActualsRequests; id++ {
		g.projects.put(contracts.ProjectEntry{ID: id, Code: fmt.Sprintf("X-%d", id), Name: "More", CustomerID: ptrTo(int32(customerAcme)),
			Status: "active", BillingType: "time-and-materials", Currency: ptrTo("NOK")})
	}
	if v := getView(t, g.h, fmt.Sprintf("customerId=%d", customerAcme)); !slices.Equal(v.Warnings, []string{"work_truncated"}) {
		t.Errorf("with %d projects, warnings = %v, want work_truncated", contracts.MaxActualsRequests, v.Warnings)
	}
}

// The view and the wizard are invoices:create's (D10): whoever builds the
// invoice sees the hours, the people and the rates it will state.
func TestWork_NeedsCreate(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	reader := f.h.SignIn(t, "invoices:access")
	if res := reader.Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", workPath, customerAcme), nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /work without invoices:create = %d, want 403", res.Status)
	}
	if res := reader.Do(http.MethodPost, fromWorkPath, fromWorkBody(customerAcme, allSeptember()...)); res.Status != http.StatusForbidden {
		t.Errorf("POST /from-work without invoices:create = %d, want 403", res.Status)
	}
	if got := f.billable.reads(); len(got) != 0 {
		t.Errorf("reads = %v, want none", got)
	}
}

// The wizard's refusals come in D3's order: the body (400), then the
// customer's gates, then — the sources read — a project billing another
// customer, fixed-price hours or non-billable work, work no longer billable,
// a revision moved, and last the currencies: mixed_currency before
// currency_not_nok. Each is shown by removing the cause of the one before.
func TestFromWork_RefusalsInOrder(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	other, nonBillable := int32(43), int32(44)
	f.projects.put(contracts.ProjectEntry{ID: other, Code: "P-43", Name: "Project 43", CustomerID: ptrTo(int32(customerPerson)),
		Status: "active", BillingType: "time-and-materials", Currency: ptrTo("NOK")})
	f.projects.put(contracts.ProjectEntry{ID: nonBillable, Code: "P-44", Name: "Project 44", CustomerID: ptrTo(int32(customerAcme)),
		Status: "active", BillingType: "non-billable", Currency: ptrTo("NOK")})
	f.billable.putHour(contracts.BillableHour{ID: 1301, Revision: 1, ProjectID: other, UserID: f.kari, Date: wDay("2026-09-08"),
		HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"})
	f.billable.putExpense(contracts.BillableExpense{ID: 1302, Revision: 1, ProjectID: nonBillable, Kind: "outlay", Date: wDay("2026-09-08"),
		NetAmount: "10", BillAmount: "10", Currency: "NOK"})
	f.billable.putExpense(contracts.BillableExpense{ID: 1303, Revision: 1, ProjectID: project41, Kind: "outlay", Date: wDay("2026-09-08"),
		NetAmount: "10", BillAmount: "10", Currency: "EUR"})
	sources := []map[string]any{
		workSrc("time.entry", 1301, 1), workSrc("expenses.entry", 1302, 1), workSrc("time.entry", 1399, 1),
		workSrc("projects.milestone", workMilestne, 7), workSrc("expenses.entry", 1303, 1),
	}

	// The body first: a source named twice is a 400, read before anything.
	twice := fromWorkBody(customerAcme, append(slices.Clone(sources), workSrc("time.entry", 1301, 1))...)
	if res, _ := postFromWork(t, creator(t, f.h), twice); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["sources[5]"]) == 0 {
		t.Fatalf("a source named twice = %d %s, want 400 on sources[5]", res.Status, res.Body)
	}
	// Then the customer's gates, before any billable read.
	refusedFromWork(t, f.h, fromWorkBody(customerDisabled, sources...), "customer_blocked")
	if got := f.billable.reads(); len(got) != 0 {
		t.Fatalf("reads = %v before the gates passed", got)
	}

	steps := []struct {
		code string
		kind string
		id   int64
		fix  func()
	}{
		{"source_not_for_customer", "time.entry", 1301, func() {
			f.projects.edit(other, func(p *contracts.ProjectEntry) { p.CustomerID = ptrTo(int32(customerAcme)) })
		}},
		{"source_not_selectable", "expenses.entry", 1302, func() {
			f.projects.edit(nonBillable, func(p *contracts.ProjectEntry) { p.BillingType = "time-and-materials" })
		}},
		{"source_not_invoiceable", "time.entry", 1399, func() {
			f.billable.putHour(contracts.BillableHour{ID: 1399, Revision: 1, ProjectID: project41, UserID: f.ola, Date: wDay("2026-09-09"),
				HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"})
		}},
		{"source_changed", "projects.milestone", workMilestne, func() { sources[3] = workSrc("projects.milestone", workMilestne, 1) }},
	}
	for _, s := range steps {
		p := refusedFromWork(t, f.h, fromWorkBody(customerAcme, sources...), s.code)
		if p.SourceKind == nil || *p.SourceKind != s.kind || p.SourceID == nil || *p.SourceID != s.id {
			t.Errorf("%s names %v %v, want %s %d", s.code, p.SourceKind, p.SourceID, s.kind, s.id)
		}
		s.fix()
	}
	// NOK and EUR: mixed_currency, judged before currency_not_nok.
	refusedFromWork(t, f.h, fromWorkBody(customerAcme, sources...), "mixed_currency")
	refusedFromWork(t, f.h, fromWorkBody(customerAcme, workSrc("expenses.entry", 1303, 1)), "currency_not_nok")
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 0 {
		t.Fatalf("%d documents after the refusals, want none", n)
	}
	f.billable.putExpense(contracts.BillableExpense{ID: 1303, Revision: 1, ProjectID: project41, Kind: "outlay", Date: wDay("2026-09-08"),
		NetAmount: "10", BillAmount: "10", Currency: "NOK"})
	fromWork(t, f.h, fromWorkBody(customerAcme, sources...))
}

// An expense's revision is display-only (D1): a reimbursement moves it
// without touching what is billed, so a body naming an older one is taken at
// the facts read now; an hour's or a milestone's revision is judged.
func TestFromWork_AnExpensesRevisionIsDisplayOnly(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	doc := fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("expenses.entry", workOutlay, 1)))
	if got := heldRows(t, f.h, doc.ID); len(got) != 1 || !strings.Contains(got[0], "expenses.entry:901@1 r4 ") {
		t.Errorf("held = %v, want the outlay at the revision read now, 4", got)
	}
	refusedFromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 1)), "source_changed")
}

// manyHours puts n hours of Kari's on project 41, one per day from
// 2026-01-01 when daily, else all on three days.
func manyHours(f workFixture, n int, daily bool) []map[string]any {
	var sources []map[string]any
	for i := range n {
		date := wDay("2026-09-01").AddDate(0, 0, i%3)
		if daily {
			date = wDay("2025-01-01").AddDate(0, 0, i)
		}
		id := int64(20000 + i)
		f.billable.putHour(contracts.BillableHour{ID: id, Revision: 1, ProjectID: project41, UserID: f.kari, Date: date,
			HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"})
		sources = append(sources, workSrc("time.entry", id, 1))
	}
	return sources
}

// More than 500 lines is 409 too_many_lines naming the next coarser grouping
// whose lines fit: date for 501 entries on three days, and past date — which
// would still make 501 — person for 501 entries on 501 days.
func TestFromWork_TooManyLinesSuggestsACoarserGrouping(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	body := fromWorkBody(customerAcme, manyHours(f, 501, false)...)
	body["grouping"] = "itemised"
	if p := refusedFromWork(t, f.h, body, "too_many_lines"); p.SuggestedGrouping == nil || *p.SuggestedGrouping != "date" {
		t.Errorf("suggestedGrouping = %v, want date", p.SuggestedGrouping)
	}
	g := newWorkFixture(t)
	daily := fromWorkBody(customerAcme, manyHours(g, 501, true)...)
	daily["grouping"] = "itemised"
	if p := refusedFromWork(t, g.h, daily, "too_many_lines"); p.SuggestedGrouping == nil || *p.SuggestedGrouping != "person" {
		t.Errorf("suggestedGrouping = %v, want person", p.SuggestedGrouping)
	}
	daily["grouping"] = "person"
	if doc := fromWork(t, g.h, daily); len(doc.Lines) != 1 || doc.Sources == nil || doc.Sources.Count != 501 {
		t.Errorf("grouped by person: %d lines, block %+v, want one line holding 501", len(doc.Lines), doc.Sources)
	}
}

// More than 5 000 sources is 409 too_many_sources, before any read.
func TestFromWork_TooManySources(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	var sources []map[string]any
	for i := range contracts.MaxBillableRows + 1 {
		sources = append(sources, workSrc("time.entry", int64(100000+i), 1))
	}
	refusedFromWork(t, f.h, fromWorkBody(customerAcme, sources...), "too_many_sources")
	if got := f.billable.reads(); len(got) != 0 {
		t.Errorf("reads = %d, want none", len(got))
	}
}

// An append counts the target's held work with the new: 4 999 held and two
// more is past 5 000; one more is not.
func TestFromWork_TooManySourcesCountsTheTargetsHolds(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	d := createDraft(t, f.h, draftBody(customerAcme, line("Konsulenttimer", 1, 1000, vat25)))
	f.h.Exec(t, `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, project_id,
			quantity, amount, currency, source_date)
		SELECT l.id, l.invoice_id, 'time.entry', 500000 + g, 1, $2, 1, 1, 'NOK', '2026-09-01'
		FROM invoices.lines l, generate_series(1, $3::int) AS g WHERE l.invoice_id = $1`, d.ID, project41, contracts.MaxBillableRows-1)
	body := fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2), workSrc("time.entry", workHourOla, 1))
	body["invoiceId"], body["revision"] = d.ID, d.Revision
	refusedFromWork(t, f.h, body, "too_many_sources")
	if got := f.billable.reads(); len(got) != 0 {
		t.Errorf("reads = %d, want none", len(got))
	}
	body["sources"] = []map[string]any{workSrc("time.entry", workHourKari, 2)}
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status == http.StatusConflict && problemOf(t, res).Code == "too_many_sources" {
		t.Errorf("4 999 held and one more = too_many_sources, want it under the bound")
	}
}

// Work another live draft holds is 409 source_held_elsewhere, judged under
// the new draft's lock before anything is held: heldBy names the first draft
// holding any of it, and the source. Nothing is left behind.
func TestFromWork_HeldElsewhereNamesTheFirstDraft(t *testing.T) {
	f := newWorkFixture(t)
	first := fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", workHourOla, 1)))
	fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("expenses.entry", workOutlay, 4)))
	reached := 0
	defer invoices.SetFromWorkBeforeInsert(func(context.Context, int64) { reached++ })()
	p := refusedFromWork(t, f.h, fromWorkBody(customerAcme,
		workSrc("expenses.entry", workOutlay, 4), workSrc("time.entry", workHourOla, 1), workSrc("time.entry", workHourKari, 2)),
		"source_held_elsewhere")
	if p.HeldBy == nil || p.HeldBy.InvoiceID != first.ID || p.HeldBy.Status != "draft" ||
		p.SourceKind == nil || *p.SourceKind != "time.entry" || p.SourceID == nil || *p.SourceID != workHourOla {
		t.Errorf("refusal = heldBy %+v, %v %v; want draft %d's time.entry %d", p.HeldBy, p.SourceKind, p.SourceID, first.ID, workHourOla)
	}
	if reached != 0 {
		t.Errorf("the wizard reached its insert %d times; the hold elsewhere is judged under the lock before it", reached)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 2 {
		t.Errorf("%d documents, want the two drafts and no third", n)
	}
}

// Two wizards racing for overlapping work end in one hold: A parked after
// finding the work free, B — the same work in the opposite order — runs to
// 201, and A, released, fails on ux_line_sources_live, answered as 409
// source_held_elsewhere naming B's draft — never a 500, never a deadlock —
// and leaves nothing behind.
func TestFromWork_TwoRacingHoldsEndInOne(t *testing.T) {
	f := newWorkFixture(t)
	parked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer invoices.SetFromWorkBeforeInsert(func(context.Context, int64) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(parked)
			<-release
		}
	})()
	type result struct {
		res *modtest.Response
	}
	done := make(chan result, 1)
	a := creator(t, f.h)
	go func() {
		res, _ := postFromWork(t, a, fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2), workSrc("time.entry", workHourOla, 1)))
		done <- result{res}
	}()
	select {
	case <-parked:
	case <-time.After(15 * time.Second):
		t.Fatal("wizard A never reached its insert")
	}
	b := fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", workHourOla, 1), workSrc("time.entry", workHourKari, 2)))
	close(release)
	var got result
	select {
	case got = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("wizard A never finished")
	}
	if got.res.Status != http.StatusConflict {
		t.Fatalf("A = %d %s, want 409 source_held_elsewhere", got.res.Status, got.res.Body)
	}
	if p := problemOf(t, got.res); p.Code != "source_held_elsewhere" || p.HeldBy == nil || p.HeldBy.InvoiceID != b.ID {
		t.Errorf("A = %s heldBy %+v, want source_held_elsewhere naming B's draft %d", p.Code, p.HeldBy, b.ID)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 1 {
		t.Errorf("%d documents, want B's alone", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.line_sources`); n != 2 {
		t.Errorf("%d holds, want B's two", n)
	}
}

// Each kind's line_sources row takes D3's quantity and amount: an hour its
// hours and Time's exact amount, to the eighth decimal; a mileage its
// kilometres and bill amount; an outlay and a milestone 1 and their amount;
// an expense its kind.
func TestFromWork_LineSourcesQuantityAndAmountPerKind(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	multiplier := "150.25"
	f.billable.putHour(contracts.BillableHour{ID: 821, Revision: 3, ProjectID: project41, UserID: f.kari, Date: wDay("2026-09-08"),
		HoursHundredths: 125, BillRate: "100.33", BillMultiplierPercent: &multiplier, Currency: "NOK", Amount: "188.43228125"})
	f.billable.putExpense(contracts.BillableExpense{ID: 921, Revision: 1, ProjectID: project41, Kind: "supplier_invoice",
		Date: wDay("2026-09-08"), Supplier: "Byggmakker AS", SupplierInvoiceNumber: "F-77", NetAmount: "2000", BillAmount: "2200.00", Currency: "NOK"})
	doc := fromWork(t, f.h, fromWorkBody(customerAcme, append(allSeptember(),
		workSrc("time.entry", 821, 3), workSrc("expenses.entry", 921, 1))...))
	got := heldRows(t, f.h, doc.ID)
	slices.Sort(got)
	// Hours first, by rate — the 150.7458 line before the 1 200 one — then
	// the expenses by kind, then the milestone.
	want := []string{
		"expenses.entry:901@4 r4 q1 a1500 p41 2026-09-03 outlay held",
		"expenses.entry:902@3 r1 q90 a405 p41 2026-09-04 mileage held",
		"expenses.entry:921@5 r1 q1 a2200 p41 2026-09-08 supplier_invoice held",
		"projects.milestone:951@6 r1 q1 a10000 p41 2026-09-05 - held",
		"time.entry:801@2 r2 q4 a4800 p41 2026-09-01 - held",
		"time.entry:802@2 r1 q3.5 a4200 p41 2026-09-02 - held",
		"time.entry:821@1 r3 q1.25 a188.43228125 p41 2026-09-08 - held",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("held =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if q := modtest.One[string](t, f.h.Harness, `SELECT amount::text FROM invoices.line_sources WHERE source_id = 821`); q != "188.43228125" {
		t.Errorf("the exact amount stored = %s", q)
	}
}

// The lines are written in the buyer's language: a profile in English gets
// English lines, every other one Norwegian.
func TestFromWork_TheLineLanguageIsTheBuyers(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	f.projects.put(contracts.ProjectEntry{ID: 45, Code: "P-45", Name: "Hytta", CustomerID: ptrTo(int32(customerPerson)),
		Status: "active", BillingType: "time-and-materials", Currency: ptrTo("NOK")})
	f.billable.putHour(contracts.BillableHour{ID: 845, Revision: 1, ProjectID: 45, UserID: f.ola, Date: wDay("2026-09-07"),
		HoursHundredths: 300, BillRate: "900", Currency: "NOK", Amount: "2700"})
	en := fromWork(t, f.h, fromWorkBody(customerPerson, workSrc("time.entry", 845, 1)))
	if got := linesOf(en); !slices.Equal(got, []string{"Consulting hours, Hytta, 7 Sep 2026 | hours | 3 × 900 | 1 | time.entry:845"}) {
		t.Errorf("an English buyer's lines = %v", got)
	}
	nb := fromWork(t, f.h, fromWorkBody(customerAcme, allSeptember()...))
	want := []string{
		"Konsulenttimer, Project 41, 1.–2. sep. 2026 | timer | 7.5 × 1200 | 1 | time.entry:801 time.entry:802",
		"Kjøregodtgjørelse, Project 41, 4. sep. 2026 |  | 1 × 405 | 1 | expenses.entry:902",
		"Viderefakturerte kostnader, Project 41, 3. sep. 2026 |  | 1 × 1500 | 1 | expenses.entry:901",
		"Fase 1 |  | 1 × 10000 | 1 | projects.milestone:951",
	}
	if got := linesOf(nb); !slices.Equal(got, want) {
		t.Errorf("a Norwegian buyer's lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A default code that has since become inactive, with no code given, is a 400
// on vatCodes.<kind> naming it; a code given for that kind passes, and a
// kind the selection does not have is not judged.
func TestFromWork_AnInactiveDefaultVatCode(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	f.h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = 1`)
	res, _ := postFromWork(t, creator(t, f.h), fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2)))
	errs := problemOf(t, res).Errors
	if res.Status != http.StatusBadRequest || len(errs["vatCodes.hours"]) == 0 || !strings.Contains(errs["vatCodes.hours"][0], "code 3") ||
		len(errs["vatCodes.expenses"]) != 0 {
		t.Fatalf("an inactive default = %d %s, want 400 on vatCodes.hours naming code 3", res.Status, res.Body)
	}
	body := fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2))
	body["vatCodes"] = map[string]any{"hours": 1}
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusBadRequest ||
		!slices.Contains(problemOf(t, res).Errors["vatCodes.hours"], "This VAT code is no longer offered for new lines") {
		t.Errorf("the inactive code given = %d %s, want 400 on vatCodes.hours", res.Status, res.Body)
	}
	body["vatCodes"] = map[string]any{"hours": 2}
	if doc := fromWork(t, f.h, body); doc.Lines[0].VatCodeID != 2 {
		t.Errorf("the line's code = %d, want the 2 given", doc.Lines[0].VatCodeID)
	}
}

// What the wizard fills in: the delivery period from the first to the last
// day of the work, the buyer reference and the customer's terms; a period
// the request gives wins.
func TestFromWork_Prefills(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	doc := fromWork(t, f.h, fromWorkBody(customerAcme, allSeptember()...))
	if doc.DeliveryFrom == nil || *doc.DeliveryFrom != "2026-09-01" || doc.DeliveryTo == nil || *doc.DeliveryTo != "2026-09-05" ||
		doc.DeliveryDate != nil {
		t.Errorf("delivery = %v %v–%v, want 2026-09-01 to 2026-09-05", doc.DeliveryDate, doc.DeliveryFrom, doc.DeliveryTo)
	}
	if doc.YourReference != "PO-77" || doc.PaymentTermsDays == nil || *doc.PaymentTermsDays != 30 || doc.Kind != "invoice" ||
		doc.Status != "draft" || doc.CustomerID != customerAcme {
		t.Errorf("draft = %+v, want Acme's reference and terms", doc)
	}
	if doc.Sources == nil || doc.Sources.Count != 5 || doc.Sources.Held != 5 || doc.NetTotal != 4800+4200+1500+405+10000 {
		t.Errorf("block %+v, net %v", doc.Sources, doc.NetTotal)
	}
	body := fromWorkBody(customerNoTerms, workSrc("time.entry", workHour42, 1))
	f.projects.edit(project42, func(p *contracts.ProjectEntry) { p.CustomerID = ptrTo(int32(customerNoTerms)) })
	body["deliveryFrom"], body["deliveryTo"] = "2026-09-01", "2026-09-30"
	given := fromWork(t, f.h, body)
	if *given.DeliveryFrom != "2026-09-01" || *given.DeliveryTo != "2026-09-30" || *given.PaymentTermsDays != 14 {
		t.Errorf("given period %v–%v, terms %v; want the request's and the settings' 14 days", *given.DeliveryFrom, *given.DeliveryTo, *given.PaymentTermsDays)
	}
	if got := linesOf(given); len(got) != 1 || !strings.HasPrefix(got[0], "Konsulenttimer, Project 42, 3. sep. 2026 |") {
		t.Errorf("lines = %v", got)
	}
}

// Work is added to an existing invoice draft of the customer at the revision
// it was read at: its own lines first, their work carried, the new lines
// after; 200 with the draft, its delivery widened to cover the added work.
// Work it holds already is held by it; a stale
// revision is the revision 409; a credit note, another customer's draft and
// an issued invoice are refused.
func TestFromWork_AppendsAtARevision(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	first := fromWork(t, f.h, fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2)))
	body := fromWorkBody(customerAcme, workSrc("projects.milestone", workMilestne, 1), workSrc("time.entry", workHourOla, 1))
	body["invoiceId"], body["revision"] = first.ID, first.Revision
	res, doc := postFromWork(t, creator(t, f.h), body)
	if res.Status != http.StatusOK {
		t.Fatalf("append = %d %s, want 200", res.Status, res.Body)
	}
	want := []string{
		"Konsulenttimer, Project 41, 1. sep. 2026 | timer | 4 × 1200 | 1 | time.entry:801",
		"Konsulenttimer, Project 41, 2. sep. 2026 | timer | 3.5 × 1200 | 1 | time.entry:802",
		"Fase 1 |  | 1 × 10000 | 1 | projects.milestone:951",
	}
	if got := linesOf(doc); doc.ID != first.ID || doc.Revision != first.Revision+1 || !slices.Equal(got, want) {
		t.Errorf("appended draft %d r%d =\n%s\nwant draft %d r%d\n%s", doc.ID, doc.Revision, strings.Join(got, "\n"),
			first.ID, first.Revision+1, strings.Join(want, "\n"))
	}
	// The target's period, 1 September, widened to the added work's last
	// day, the milestone's 5 September: every line's period sits inside it.
	if doc.DeliveryDate != nil || *doc.DeliveryFrom != "2026-09-01" || *doc.DeliveryTo != "2026-09-05" || doc.NetTotal != 4800+4200+10000 {
		t.Errorf("an append's delivery = %v %v–%v, net %v; want 2026-09-01 to 2026-09-05", doc.DeliveryDate, *doc.DeliveryFrom,
			*doc.DeliveryTo, doc.NetTotal)
	}

	// Work the draft holds already is held by the draft itself.
	again := fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2))
	again["invoiceId"], again["revision"] = doc.ID, doc.Revision
	if p := refusedFromWork(t, f.h, again, "source_held_elsewhere"); p.HeldBy == nil || p.HeldBy.InvoiceID != first.ID {
		t.Errorf("work the target holds = heldBy %+v, want the target %d", p.HeldBy, first.ID)
	}

	// The revision just used is stale now.
	body["sources"] = []map[string]any{workSrc("expenses.entry", workOutlay, 4)}
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "" {
		t.Errorf("a stale revision = %d %s, want the revision 409", res.Status, res.Body)
	}
	// Another customer's draft, a credit note, an issued invoice.
	person := createDraft(t, f.h, draftBody(customerPerson, line("Arbeid", 1, 100, vat25)))
	body["invoiceId"], body["revision"] = person.ID, person.Revision
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["invoiceId"]) == 0 {
		t.Errorf("another customer's draft = %d %s, want 400 on invoiceId", res.Status, res.Body)
	}
	plain := createDraft(t, f.h, draftBody(customerAcme, line("Arbeid", 1, 100, vat25)))
	original := issued(t, f.h, plain.ID)
	body["invoiceId"], body["revision"] = original.ID, original.Revision
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
		t.Errorf("an issued invoice = %d %s, want 409 invoice_issued", res.Status, res.Body)
	}
	credit := creditDraft(t, f.h, original.ID)
	body["invoiceId"], body["revision"] = credit.ID, credit.Revision
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["invoiceId"]) == 0 {
		t.Errorf("a credit note = %d %s, want 400 on invoiceId", res.Status, res.Body)
	}
	body["invoiceId"], body["revision"] = int64(987654), 1
	if res, _ := postFromWork(t, creator(t, f.h), body); res.Status != http.StatusNotFound {
		t.Errorf("an unknown target = %d %s, want 404", res.Status, res.Body)
	}
}

// D6: each kind's lines take the settings' code for it, unless the request
// gives one; while the seller is not VAT-registered every kind takes id 9,
// the category-O code.
func TestFromWork_VatCodeDefaultsOverridesAndNotRegistered(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	seller := completeSeller(2)
	seller["workVatCodes"] = map[string]any{"hours": 2, "expenses": 3, "milestones": 4}
	saveSeller(t, f.h, seller)
	codesOf := func(doc wizardDocJSON) []int32 {
		var out []int32
		for _, l := range doc.Lines {
			out = append(out, l.VatCodeID)
		}
		return out
	}
	sources := []map[string]any{
		workSrc("time.entry", workHourKari, 2), workSrc("expenses.entry", workOutlay, 4), workSrc("projects.milestone", workMilestne, 1),
	}
	if got := codesOf(fromWork(t, f.h, fromWorkBody(customerAcme, sources...))); !slices.Equal(got, []int32{2, 3, 4}) {
		t.Errorf("the settings' codes = %v, want 2, 3, 4", got)
	}
	g := newWorkFixture(t)
	body := fromWorkBody(customerAcme, sources...)
	body["vatCodes"] = map[string]any{"expenses": 5}
	if got := codesOf(fromWork(t, g.h, body)); !slices.Equal(got, []int32{1, 5, 1}) {
		t.Errorf("an expenses code given = %v, want 1, 5, 1", got)
	}
	unregistered := newWorkFixture(t)
	seller = completeSeller(2)
	seller["vatRegistered"] = false
	saveSeller(t, unregistered.h, seller)
	if got := codesOf(fromWork(t, unregistered.h, fromWorkBody(customerAcme, sources...))); !slices.Equal(got, []int32{9, 9, 9}) {
		t.Errorf("a seller outside the VAT register = %v, want 9 for every kind", got)
	}
}

// The settings carry the code each kind of work is invoiced at: 1 for all
// three until changed, each a known code — active when it changes — on the
// settings' revision and under invoices:manage.
func TestSettings_WorkVatCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var read settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if c := read.WorkVatCodes; c.Hours != 1 || c.Expenses != 1 || c.Milestones != 1 {
		t.Errorf("defaults = %+v, want 1, 1, 1", c)
	}
	body := completeSeller(read.Revision)
	body["workVatCodes"] = map[string]any{"hours": 2, "expenses": 3, "milestones": 9}
	saved := saveSeller(t, h, body)
	if c := saved.WorkVatCodes; c.Hours != 2 || c.Expenses != 3 || c.Milestones != 9 || saved.Revision != read.Revision+1 {
		t.Errorf("saved = %+v r%d", c, saved.Revision)
	}
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = 4`)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	for field, codes := range map[string]map[string]any{
		"workVatCodes.hours":      {"hours": 4, "expenses": 1, "milestones": 1},
		"workVatCodes.expenses":   {"hours": 1, "expenses": 999, "milestones": 1},
		"workVatCodes.milestones": {"hours": 1, "expenses": 1, "milestones": 0},
	} {
		body := completeSeller(saved.Revision)
		body["workVatCodes"] = codes
		if res := manager.Do(http.MethodPut, settingsPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[field]) == 0 {
			t.Errorf("%v = %d %s, want 400 on %s", codes, res.Status, res.Body, field)
		}
	}
	// A code kept as stored passes though it has since been deactivated, so
	// the seller record can still be saved; a changed one is judged.
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = 3`)
	kept := completeSeller(saved.Revision)
	kept["workVatCodes"] = map[string]any{"hours": 2, "expenses": 3, "milestones": 9}
	if res := manager.Do(http.MethodPut, settingsPath, kept); res.Status != http.StatusOK {
		t.Errorf("an inactive code kept as stored = %d %s, want 200", res.Status, res.Body)
	}
	// The same inactive code moved to hours is a change there, refused on
	// hours alone; kept for expenses it still passes.
	moved := completeSeller(saved.Revision + 1)
	moved["workVatCodes"] = map[string]any{"hours": 3, "expenses": 3, "milestones": 9}
	res := manager.Do(http.MethodPut, settingsPath, moved)
	if errs := problemOf(t, res).Errors; res.Status != http.StatusBadRequest || len(errs["workVatCodes.hours"]) == 0 ||
		len(errs["workVatCodes.expenses"]) != 0 || len(errs["workVatCodes.milestones"]) != 0 {
		t.Errorf("code 3 moved to hours = %d %s, want 400 on workVatCodes.hours only", res.Status, res.Body)
	}
	stale := completeSeller(read.Revision)
	if res := manager.Do(http.MethodPut, settingsPath, stale); res.Status != http.StatusConflict {
		t.Errorf("a stale revision = %d, want 409", res.Status)
	}
	if res := creator(t, h).Do(http.MethodPut, settingsPath, completeSeller(saved.Revision)); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:manage = %d, want 403", res.Status)
	}
}

// Meta says whether work can be invoiced here, and which kinds.
func TestMeta_WorkAvailable(t *testing.T) {
	t.Parallel()
	type workMeta struct {
		WorkAvailable bool `json:"workAvailable"`
		Work          struct {
			Hours      bool `json:"hours"`
			Expenses   bool `json:"expenses"`
			Milestones bool `json:"milestones"`
		} `json:"work"`
	}
	read := func(h *harness) workMeta {
		var m workMeta
		h.SignIn(t, "invoices:access").Do(http.MethodGet, metaPath, nil).JSON(&m)
		return m
	}
	if m := read(newHarness(t)); m.WorkAvailable || m.Work.Hours || m.Work.Expenses || m.Work.Milestones {
		t.Errorf("no billable read = %+v", m)
	}
	f := newFakeBillable()
	if m := read(newHarness(t, modtest.WithBillableHours(f))); !m.WorkAvailable || !m.Work.Hours || m.Work.Expenses || m.Work.Milestones {
		t.Errorf("hours alone = %+v", m)
	}
	if m := read(newHarness(t, f.options()...)); !m.WorkAvailable || !m.Work.Hours || !m.Work.Expenses || !m.Work.Milestones {
		t.Errorf("all three = %+v", m)
	}
}

// With the projects module off the wizard is 409 projects_unavailable, after
// the sources were read and before any judgment needing their projects.
func TestFromWork_ProjectsUnavailable(t *testing.T) {
	t.Parallel()
	billable := newFakeBillable()
	h := newHarness(t, billable.options()...)
	saveSeller(t, h, completeSeller(1))
	billable.putHour(contracts.BillableHour{ID: workHourKari, Revision: 2, ProjectID: project41, Date: wDay("2026-09-01"),
		HoursHundredths: 400, BillRate: "1200.00", Currency: "NOK", Amount: "4800"})
	refusedFromWork(t, h, fromWorkBody(customerAcme, workSrc("time.entry", workHourKari, 2)), "projects_unavailable")
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 0 {
		t.Errorf("%d documents, want none", n)
	}
}

// appendBody is an append of sources to d at its revision.
func appendBody(d invoiceJSON, sources ...map[string]any) map[string]any {
	body := fromWorkBody(d.CustomerID, sources...)
	body["invoiceId"], body["revision"] = d.ID, d.Revision
	return body
}

func appended(t *testing.T, h *harness, body map[string]any) invoiceJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodPost, fromWorkPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("append = %d %s, want 200", res.Status, res.Body)
	}
	var doc invoiceJSON
	res.JSON(&doc)
	return doc
}

func deliveryOf(d invoiceJSON) string {
	deref := func(s *string) string {
		if s == nil {
			return "-"
		}
		return *s
	}
	return fmt.Sprintf("day %s, period %s–%s", deref(d.DeliveryDate), deref(d.DeliveryFrom), deref(d.DeliveryTo))
}

// An append's delivery (D3): a target delivered on one day, widened by work
// on either side of it, becomes the period that covers both; one whose day
// the work does not leave keeps its day; one with no delivery takes the span
// of all the work it holds, its own included; and a period the request gives
// is the draft's, whatever the work.
func TestFromWork_AppendWidensTheDelivery(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t)
	onDay := func(day string) invoiceJSON {
		body := draftBody(customerAcme, line("Arbeid", 1, 100, vat25))
		body["deliveryDate"] = day
		return createDraft(t, f.h, body)
	}

	widened := appended(t, f.h, appendBody(onDay("2026-09-03"),
		workSrc("time.entry", workHourKari, 2), workSrc("projects.milestone", workMilestne, 1)))
	if got := deliveryOf(widened); got != "day -, period 2026-09-01–2026-09-05" {
		t.Errorf("a day widened by work on the 1st and the 5th = %s", got)
	}
	kept := appended(t, f.h, appendBody(onDay("2026-09-02"), workSrc("time.entry", workHourOla, 1)))
	if got := deliveryOf(kept); got != "day 2026-09-02, period -–-" {
		t.Errorf("a day the work does not leave = %s, want the day kept", got)
	}

	body := draftBody(customerAcme, line("Arbeid", 1, 100, vat25))
	delete(body, "deliveryDate")
	none := createDraft(t, f.h, body)
	plantSource(t, f.h, none.ID, planted{position: 1, kind: "time.entry", id: 7001, revision: 1, quantity: "1", amount: "100", date: "2026-08-20"})
	if got := deliveryOf(appended(t, f.h, appendBody(none, workSrc("expenses.entry", workOutlay, 4)))); got != "day -, period 2026-08-20–2026-09-03" {
		t.Errorf("no delivery = %s, want the span of the held and the added work", got)
	}

	given := appendBody(onDay("2026-09-10"), workSrc("expenses.entry", workMileage, 1))
	given["deliveryFrom"], given["deliveryTo"] = "2026-08-01", "2026-08-31"
	if got := deliveryOf(appended(t, f.h, given)); got != "day -, period 2026-08-01–2026-08-31" {
		t.Errorf("a period given = %s, want exactly the request's", got)
	}
}
