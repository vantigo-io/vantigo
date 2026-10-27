package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Consumption section, the wizard's settlements and the From consumption
// wizard (energy consumption billing design D7, D8, D11; invoices from
// consumption plan readings 4 and 5), through the API: the settlements and
// due supplies as Energy answers them (fakeConsumption), the customers as the
// directory does. The harness's clock stands on 12 September 2026, so an
// August settlement is due by default.

const (
	consumptionPath            = "/api/v1/invoices/consumption"
	consumptionSettlementsPath = "/api/v1/invoices/consumption/settlements"
	fromConsumptionPath        = "/api/v1/invoices/from-consumption"

	gsrnTromso = "707057500000000001"
	gsrnBodo   = "707057500000000002"
)

// consumptionFixture is an installation with Energy's reads faked and a
// complete, VAT-registered seller.
type consumptionFixture struct {
	h      *harness
	energy *fakeConsumption
}

func newConsumptionFixture(t *testing.T, opts ...modtest.Option) consumptionFixture {
	t.Helper()
	f := consumptionFixture{energy: newFakeConsumption()}
	f.h = newHarness(t, append(f.energy.options(), opts...)...)
	saveSeller(t, f.h, completeSeller(1))
	return f
}

// august is customer's regular August 2026 settlement id of supply.
func august(id int64, customer, supply int32, gsrn, hint string) contracts.Settlement {
	return monthSettlement(id, customer, supply, gsrn, 2026, time.August, hint)
}

// noLockedReads fails when any of Energy's reads was made under a lock.
func noLockedReads(t *testing.T, f *fakeConsumption) {
	t.Helper()
	for _, c := range f.reads() {
		if c.locked {
			t.Errorf("Consumption.%s was called under a lock", c.method)
		}
	}
}

func methodsOf(calls []consumptionCall) []string {
	out := []string{}
	for _, c := range calls {
		out = append(out, c.method)
	}
	return out
}

// dueSupplyJSON and consumptionViewJSON are GET /invoices/consumption as a
// client reads it.
type dueSupplyJSON struct {
	SupplyPeriodID  int32   `json:"supplyPeriodId"`
	MeteringPointID int32   `json:"meteringPointId"`
	Gsrn            string  `json:"gsrn"`
	Address         string  `json:"address"`
	PriceArea       string  `json:"priceArea"`
	ContractName    string  `json:"contractName"`
	Cadence         string  `json:"cadence"`
	BilledThrough   *string `json:"billedThrough"`
	DuePeriods      int32   `json:"duePeriods"`
	Blocked         *string `json:"blocked"`
}

type consumptionViewJSON struct {
	CustomerID int32           `json:"customerId"`
	Through    string          `json:"through"`
	Supplies   []dueSupplyJSON `json:"supplies"`
	Warnings   []string        `json:"warnings"`
}

// settlementJSON and settlementsViewJSON are GET
// /invoices/consumption/settlements as a client reads it.
type settlementJSON struct {
	ID            int64       `json:"id"`
	Revision      int32       `json:"revision"`
	Kind          string      `json:"kind"`
	Gsrn          string      `json:"gsrn"`
	PeriodFrom    string      `json:"periodFrom"`
	PeriodTo      string      `json:"periodTo"`
	Kwh           float64     `json:"kwh"`
	Amount        float64     `json:"amount"`
	Currency      string      `json:"currency"`
	Blocked       *string     `json:"blocked"`
	BlockedDetail *string     `json:"blockedDetail"`
	State         string      `json:"state"`
	Selectable    bool        `json:"selectable"`
	Reason        *string     `json:"reason"`
	HeldBy        *heldByJSON `json:"heldBy"`
	Lines         []struct {
		Kind      string  `json:"kind"`
		Text      string  `json:"text"`
		Band      *string `json:"band"`
		Month     *string `json:"month"`
		Quantity  float64 `json:"quantity"`
		Unit      string  `json:"unit"`
		UnitPrice float64 `json:"unitPrice"`
		Amount    float64 `json:"amount"`
		VatHint   string  `json:"vatHint"`
	} `json:"lines"`
}

type settlementsViewJSON struct {
	CustomerID  int32            `json:"customerId"`
	Through     string           `json:"through"`
	Settlements []settlementJSON `json:"settlements"`
	Warnings    []string         `json:"warnings"`
}

func getConsumption(t *testing.T, h *harness, query string) consumptionViewJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodGet, consumptionPath+"?"+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/consumption?%s = %d %s", query, res.Status, res.Body)
	}
	var v consumptionViewJSON
	res.JSON(&v)
	return v
}

func getSettlements(t *testing.T, h *harness, query string) settlementsViewJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodGet, consumptionSettlementsPath+"?"+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/consumption/settlements?%s = %d %s", query, res.Status, res.Body)
	}
	var v settlementsViewJSON
	res.JSON(&v)
	return v
}

// settlementRows is every settlement of the view as "id selectable|reason".
func settlementRows(v settlementsViewJSON) []string {
	var out []string
	for _, s := range v.Settlements {
		state := "selectable"
		if !s.Selectable {
			state = "-"
			if s.Reason != nil {
				state = *s.Reason
			}
		}
		out = append(out, fmt.Sprintf("%d %s", s.ID, state))
	}
	return out
}

func fromConsumptionBody(customer int32, ids ...int64) map[string]any {
	body := map[string]any{"customerId": customer}
	if ids != nil {
		body["settlementIds"] = ids
	}
	return body
}

func postFromConsumption(t *testing.T, c *modtest.Client, body map[string]any) (*modtest.Response, wizardDocJSON) {
	t.Helper()
	res := c.Do(http.MethodPost, fromConsumptionPath, body)
	var doc wizardDocJSON
	if res.Status == http.StatusCreated || res.Status == http.StatusOK {
		res.JSON(&doc)
	}
	return res, doc
}

// fromConsumption posts body and answers the new draft, failing unless it
// was made.
func fromConsumption(t *testing.T, h *harness, body map[string]any) wizardDocJSON {
	t.Helper()
	res, doc := postFromConsumption(t, creator(t, h), body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/from-consumption = %d %s, want 201", res.Status, res.Body)
	}
	return doc
}

// refusedFromConsumption asserts the wizard answers 409 code and answers the
// problem.
func refusedFromConsumption(t *testing.T, h *harness, body map[string]any, code string) problemJSON {
	t.Helper()
	res, _ := postFromConsumption(t, creator(t, h), body)
	if res.Status != http.StatusConflict {
		t.Fatalf("POST /invoices/from-consumption = %d %s, want 409 %s", res.Status, res.Body, code)
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Fatalf("POST /invoices/from-consumption = %q (%s), want %s", p.Code, p.Detail, code)
	}
	return p
}

// invalidFromConsumption asserts the wizard answers 400 on field.
func invalidFromConsumption(t *testing.T, h *harness, body map[string]any, field string) []string {
	t.Helper()
	res, _ := postFromConsumption(t, creator(t, h), body)
	errs := problemOf(t, res).Errors
	if res.Status != http.StatusBadRequest || len(errs[field]) == 0 {
		t.Fatalf("POST /invoices/from-consumption = %d %s, want 400 on %s", res.Status, res.Body, field)
	}
	return errs[field]
}

func codesOfLines(doc wizardDocJSON) []int32 {
	var out []int32
	for _, l := range doc.Lines {
		out = append(out, l.VatCodeID)
	}
	return out
}

// The panel's section: Energy's due supplies for the customer through the
// date — the start of the next day in Oslo as the provider's instant —
// sorted by GSRN, billedThrough as a civil date, blocked only when set;
// today when no date is given; consumption_truncated when Energy had more.
func TestConsumption_TheDueSupplies(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	billed := marketDay(2026, time.August, 1)
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerAcme, SupplyPeriodID: 12, MeteringPointID: 112, GSRN: gsrnBodo,
		Address: "Sjøgata 3, 8006 Bodø", PriceArea: "NO4", ContractName: "Spot Nord", Cadence: contracts.CadenceQuarterly,
		DuePeriods: 1, Blocked: contracts.SettlementPricePreliminary})
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerAcme, SupplyPeriodID: 11, MeteringPointID: 111, GSRN: gsrnTromso,
		Address: "Storgata 1, 9008 Tromsø", PriceArea: "NO4", ContractName: "Spot Nord", Cadence: contracts.CadenceMonthly,
		BilledThrough: &billed, DuePeriods: 2})
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 13, GSRN: "707057500000000003", Cadence: contracts.CadenceMonthly})

	v := getConsumption(t, f.h, fmt.Sprintf("customerId=%d&through=2026-08-31", customerAcme))
	if v.CustomerID != customerAcme || v.Through != "2026-08-31" || len(v.Warnings) != 0 || len(v.Supplies) != 2 {
		t.Fatalf("view = %+v", v)
	}
	first, second := v.Supplies[0], v.Supplies[1]
	if first.Gsrn != gsrnTromso || first.SupplyPeriodID != 11 || first.MeteringPointID != 111 || first.Cadence != "monthly" ||
		first.BilledThrough == nil || *first.BilledThrough != "2026-08-01" || first.DuePeriods != 2 || first.Blocked != nil ||
		first.Address != "Storgata 1, 9008 Tromsø" || first.PriceArea != "NO4" || first.ContractName != "Spot Nord" {
		t.Errorf("Tromsø = %+v", first)
	}
	if second.Gsrn != gsrnBodo || second.BilledThrough != nil || second.Blocked == nil || *second.Blocked != "price_preliminary" {
		t.Errorf("Bodø = %+v", second)
	}
	reads := f.energy.reads()
	if len(reads) != 1 || reads[0].method != "DueSupplies" || !slices.Equal(reads[0].customers, []int32{customerAcme}) ||
		!reads[0].through.Equal(marketDay(2026, time.September, 1)) {
		t.Errorf("reads = %+v, want one DueSupplies for Acme through 1 September 00:00 in Oslo", reads)
	}
	// No date: today in the business zone, 12 September.
	if v := getConsumption(t, f.h, fmt.Sprintf("customerId=%d", customerAcme)); v.Through != "2026-09-12" {
		t.Errorf("through without a date = %s, want 2026-09-12", v.Through)
	}
	f.energy.answerMore(true)
	if v := getConsumption(t, f.h, fmt.Sprintf("customerId=%d", customerAcme)); !slices.Equal(v.Warnings, []string{"consumption_truncated"}) {
		t.Errorf("warnings = %v, want consumption_truncated", v.Warnings)
	}
	for _, path := range []string{consumptionPath, consumptionSettlementsPath} {
		res := creator(t, f.h).Do(http.MethodGet, path+"?customerId=0", nil)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["customerId"]) == 0 {
			t.Errorf("GET %s?customerId=0 = %d %s, want 400 on customerId", path, res.Status, res.Body)
		}
	}
	noLockedReads(t, f.energy)
}

// With the energy module off, both views and the wizard are 409
// consumption_unavailable — the wizard after its body and its target, before
// the customer's gates.
func TestConsumption_Unavailable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := h.SignInUser(t, "invoices:access", "invoices:create")
	for _, path := range []string{consumptionPath, consumptionSettlementsPath} {
		res := c.Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", path, customerAcme), nil)
		if res.Status != http.StatusConflict || problemOf(t, res).Code != "consumption_unavailable" {
			t.Errorf("GET %s = %d %s, want 409 consumption_unavailable", path, res.Status, res.Body)
		}
	}
	refusedFromConsumption(t, h, fromConsumptionBody(customerDisabled), "consumption_unavailable")
	body := fromConsumptionBody(customerAcme)
	body["invoiceId"], body["revision"] = 987654, 1
	if res, _ := postFromConsumption(t, c, body); res.Status != http.StatusNotFound {
		t.Errorf("an unknown target = %d %s, want 404 before consumption_unavailable", res.Status, res.Body)
	}
	if res, _ := postFromConsumption(t, c, fromConsumptionBody(0)); res.Status != http.StatusBadRequest {
		t.Errorf("no customer = %d %s, want 400 before consumption_unavailable", res.Status, res.Body)
	}
	if calls := contractCalls.by(user); len(calls) != 0 {
		t.Errorf("calls = %+v, want none", calls)
	}
}

// The views and the wizard are invoices:create's, as the work wizard's are.
func TestConsumption_NeedsCreate(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	reader := f.h.SignIn(t, "invoices:access")
	for _, path := range []string{consumptionPath, consumptionSettlementsPath} {
		if res := reader.Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", path, customerAcme), nil); res.Status != http.StatusForbidden {
			t.Errorf("GET %s without invoices:create = %d, want 403", path, res.Status)
		}
	}
	if res := reader.Do(http.MethodPost, fromConsumptionPath, fromConsumptionBody(customerAcme)); res.Status != http.StatusForbidden {
		t.Errorf("POST /from-consumption without invoices:create = %d, want 403", res.Status)
	}
	if got := f.energy.reads(); len(got) != 0 {
		t.Errorf("reads = %v, want none", got)
	}
}

// The wizard's list: Energy's Settle for the customer, in a document's order
// (by GSRN, then period, a regular before its correction), each with its
// numbers and lines, what holds it, and why the wizard would not take it
// without ids — held, then blocked, then a yearly supply of a person.
func TestConsumption_TheSettlementsView(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	held := august(301, customerAcme, 11, gsrnTromso, contracts.VATHintStandard)
	f.energy.put(held)
	doc := fromConsumption(t, f.h, fromConsumptionBody(customerAcme, held.ID))
	blocked := august(302, customerAcme, 12, gsrnBodo, contracts.VATHintStandard)
	blocked.Lines, blocked.Amount, blocked.Blocked, blocked.BlockedDetail = nil, "0.00", contracts.SettlementPricePreliminary, "prices for 2026-08-14 are preliminary"
	f.energy.put(blocked)
	july := monthSettlement(303, customerAcme, 12, gsrnBodo, 2026, time.July, contracts.VATHintStandard)
	f.energy.put(july)
	september := monthSettlement(304, customerAcme, 11, gsrnTromso, 2026, time.September, contracts.VATHintStandard)
	f.energy.put(september) // not closed through today

	v := getSettlements(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))
	if got, want := settlementRows(v), []string{"301 held", "303 selectable", "302 blocked"}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if v.Through != "2026-09-12" || len(v.Warnings) != 0 {
		t.Errorf("through %s, warnings %v", v.Through, v.Warnings)
	}
	if by := v.Settlements[0].HeldBy; by == nil || by.InvoiceID != doc.ID || by.Status != "draft" {
		t.Errorf("heldBy = %+v, want draft %d", by, doc.ID)
	}
	s := v.Settlements[1]
	if s.Kind != "regular" || s.PeriodFrom != "2026-07-01" || s.PeriodTo != "2026-07-31" || s.Kwh != 1500.25 || s.Amount != 1187.07 ||
		s.Currency != "NOK" || s.State != "draft" || s.Blocked != nil || s.HeldBy != nil || s.Reason != nil || len(s.Lines) != 3 {
		t.Errorf("July = %+v", s)
	}
	if l := s.Lines[1]; l.Text != "Energiledd" || l.Band == nil || *l.Band != "day" || l.Quantity != 1000.125 || l.UnitPrice != 0.333333 ||
		l.Amount != 333.38 || l.Unit != "kWh" || l.VatHint != "standard" || l.Month != nil {
		t.Errorf("July's grid line = %+v", l)
	}
	if b := v.Settlements[2]; b.Blocked == nil || *b.Blocked != "price_preliminary" || b.BlockedDetail == nil || len(b.Lines) != 0 {
		t.Errorf("the blocked one = %+v", b)
	}

	// A person: a yearly supply's settlement, and one of a supply DueSupplies
	// does not list whose period is longer than three months, are cadence.
	yearly := august(311, customerPerson, 21, gsrnTromso, contracts.VATHintExemptHouseholdNorth)
	f.energy.put(yearly)
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 21, GSRN: gsrnTromso, Cadence: contracts.CadenceYearly})
	long := august(312, customerPerson, 22, gsrnBodo, contracts.VATHintStandard)
	long.PeriodStart = marketDay(2026, time.January, 1)
	f.energy.put(long)
	quarter := august(313, customerPerson, 23, "707057500000000003", contracts.VATHintStandard)
	quarter.PeriodStart = marketDay(2026, time.June, 1)
	f.energy.put(quarter)
	p := getSettlements(t, f.h, fmt.Sprintf("customerId=%d&through=2026-08-31", customerPerson))
	if got, want := settlementRows(p), []string{"311 cadence", "312 cadence", "313 selectable"}; !slices.Equal(got, want) {
		t.Errorf("a person's rows = %v, want %v", got, want)
	}
	// A business is never refused for its cadence.
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerAcme, SupplyPeriodID: 12, GSRN: gsrnBodo, Cadence: contracts.CadenceYearly})
	if got := settlementRows(getSettlements(t, f.h, fmt.Sprintf("customerId=%d", customerAcme))); got[1] != "303 selectable" {
		t.Errorf("a business's yearly supply = %v, want selectable", got)
	}
	f.energy.answerMore(true)
	if v := getSettlements(t, f.h, fmt.Sprintf("customerId=%d", customerAcme)); !slices.Equal(v.Warnings, []string{"consumption_truncated"}) {
		t.Errorf("warnings = %v, want consumption_truncated", v.Warnings)
	}
	noLockedReads(t, f.energy)
}

// The wizard's refusals come in plan reading 5's order: the body (400) and
// the customer's gates before any read of Energy; then, the settlements read,
// one of another customer, one missing, one blocked (its blocker in the
// detail), one of a person's yearly supply, and last the currency. Each is
// shown by removing the cause of the one before.
func TestFromConsumption_RefusalsInOrder(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 21, GSRN: gsrnTromso, Cadence: contracts.CadenceYearly})
	f.energy.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 22, GSRN: gsrnBodo, Cadence: contracts.CadenceMonthly})
	f.energy.put(august(401, customerAcme, 22, gsrnBodo, contracts.VATHintStandard))
	blocked := august(403, customerPerson, 22, gsrnBodo, contracts.VATHintStandard)
	blocked.Blocked, blocked.BlockedDetail = contracts.SettlementPriceGap, "no price for 2026-08-14"
	f.energy.put(blocked)
	f.energy.put(august(404, customerPerson, 21, gsrnTromso, contracts.VATHintStandard))
	euro := august(405, customerPerson, 22, gsrnBodo, contracts.VATHintStandard)
	euro.PeriodStart, euro.PeriodEnd, euro.Currency = marketDay(2026, time.July, 1), marketDay(2026, time.August, 1), "EUR"
	f.energy.put(euro)
	ids := []int64{401, 499, 403, 404, 405}

	// The body: an id twice, an id that is none, an empty list, a note too
	// long, an append without its revision.
	invalidFromConsumption(t, f.h, fromConsumptionBody(customerPerson, 401, 401), "settlementIds[1]")
	invalidFromConsumption(t, f.h, fromConsumptionBody(customerPerson, 0), "settlementIds[0]")
	invalidFromConsumption(t, f.h, map[string]any{"customerId": customerPerson, "settlementIds": []int64{}}, "settlementIds")
	long := fromConsumptionBody(customerPerson, ids...)
	long["note"] = strings.Repeat("ø", 1001)
	invalidFromConsumption(t, f.h, long, "note")
	half := fromConsumptionBody(customerPerson, ids...)
	half["invoiceId"] = 1
	invalidFromConsumption(t, f.h, half, "revision")
	invalidFromConsumption(t, f.h, fromConsumptionBody(0, ids...), "customerId")
	// Then the customer's gates.
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerDisabled, ids...), "customer_blocked")
	if got := f.energy.reads(); len(got) != 0 {
		t.Fatalf("reads = %v before the gates passed", methodsOf(got))
	}

	steps := []struct {
		code string
		id   int64
		fix  func()
	}{
		{"source_not_for_customer", 401, func() {
			s, _ := f.energy.get(401)
			s.CustomerID = customerPerson
			f.energy.put(s)
		}},
		{"source_not_invoiceable", 499, func() {
			f.energy.put(august(499, customerPerson, 22, gsrnBodo, contracts.VATHintStandard))
		}},
		{"settlement_blocked", 403, func() {
			s, _ := f.energy.get(403)
			s.Blocked, s.BlockedDetail = "", ""
			f.energy.put(s)
		}},
		{"cadence_not_allowed", 404, func() {
			s, _ := f.energy.get(404)
			s.SupplyPeriodID = 22
			f.energy.put(s)
		}},
	}
	for _, s := range steps {
		p := refusedFromConsumption(t, f.h, fromConsumptionBody(customerPerson, ids...), s.code)
		if p.SourceKind == nil || *p.SourceKind != "energy.settlement" || p.SourceID == nil || *p.SourceID != s.id {
			t.Errorf("%s names %v %v, want energy.settlement %d", s.code, p.SourceKind, p.SourceID, s.id)
		}
		if s.code == "settlement_blocked" && (!strings.Contains(p.Detail, "price_gap") || !strings.Contains(p.Detail, "no price for 2026-08-14")) {
			t.Errorf("settlement_blocked's detail = %q, want the blocker and its sentence", p.Detail)
		}
		s.fix()
	}
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerPerson, ids...), "currency_not_nok")
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 0 {
		t.Fatalf("%d documents after the refusals, want none", n)
	}
	// An invoiced settlement is not invoiceable either.
	s, _ := f.energy.get(405)
	s.Currency, s.State = "NOK", contracts.SettlementInvoiced
	f.energy.put(s)
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerPerson, ids...), "source_not_invoiceable")
	s.State = contracts.SettlementDraft
	f.energy.put(s)
	doc := fromConsumption(t, f.h, fromConsumptionBody(customerPerson, ids...))
	if doc.Sources == nil || doc.Sources.Count != 5 {
		t.Errorf("the draft holds %+v, want the five settlements", doc.Sources)
	}
	// Every read was DueSupplies then Settlements by id, on the pool.
	for i, c := range f.energy.reads() {
		if want := []string{"DueSupplies", "Settlements"}[i%2]; c.method != want {
			t.Errorf("read %d = %s, want %s", i, c.method, want)
		}
	}
	noLockedReads(t, f.energy)
}

// The lines are the buyer's language's (plan reading 3): one per settlement
// line, "<component><qualifier>, <GSRN> <address>, <period>", the units in
// words, the amount kept; the settlement's one row on its first line with
// its kWh, amount and period's last day; the delivery the period.
func TestFromConsumption_TheLinesInTheBuyersLanguage(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	f.energy.put(august(501, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	f.energy.put(august(502, customerPerson, 21, gsrnBodo, contracts.VATHintStandard))

	nb := fromConsumption(t, f.h, fromConsumptionBody(customerAcme))
	wantNB := []string{
		"Spot NO4, 707057500000000001 Storgata 1, 9008 Tromsø, august 2026 | kWh | 1500.25 × 0.512345 | 1 | energy.settlement:501",
		"Energiledd (dag), 707057500000000001 Storgata 1, 9008 Tromsø, august 2026 | kWh | 1000.125 × 0.33333833 | 1 | energy.settlement:501",
		"Fastledd, 707057500000000001 Storgata 1, 9008 Tromsø, august 2026 | mnd | 1 × 85.04 | 1 | energy.settlement:501",
	}
	if got := linesOf(nb); !slices.Equal(got, wantNB) {
		t.Errorf("a Norwegian buyer's lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantNB, "\n"))
	}
	if nb.NetTotal != 1187.07 || nb.DeliveryFrom == nil || *nb.DeliveryFrom != "2026-08-01" || nb.DeliveryTo == nil ||
		*nb.DeliveryTo != "2026-08-31" || nb.DeliveryDate != nil || nb.YourReference != "PO-77" {
		t.Errorf("draft = net %v, delivery %v–%v, reference %q", nb.NetTotal, nb.DeliveryFrom, nb.DeliveryTo, nb.YourReference)
	}
	if got := heldRows(t, f.h, nb.ID); !slices.Equal(got, []string{"energy.settlement:501@1 r1 q1500.25 a1187.07 p 2026-08-31 regular held"}) {
		t.Errorf("held = %v", got)
	}
	if got := modtest.One[string](t, f.h.Harness, `
		SELECT format('%s %s %s %s %s %s', line_count, point_gsrn, price_area, contract_name, period_from, estimated)
		FROM invoices.line_sources WHERE invoice_id = $1`, nb.ID); got != "3 707057500000000001 NO4 Spot Nord 2026-08-01 f" {
		t.Errorf("the snapshot = %s", got)
	}

	en := fromConsumption(t, f.h, fromConsumptionBody(customerPerson))
	wantEN := []string{
		"Spot NO4, 707057500000000002 Storgata 1, 9008 Tromsø, August 2026 | kWh | 1500.25 × 0.512345 | 1 | energy.settlement:502",
		"Energiledd (day), 707057500000000002 Storgata 1, 9008 Tromsø, August 2026 | kWh | 1000.125 × 0.33333833 | 1 | energy.settlement:502",
		"Fastledd, 707057500000000002 Storgata 1, 9008 Tromsø, August 2026 | month | 1 × 85.04 | 1 | energy.settlement:502",
	}
	if got := linesOf(en); !slices.Equal(got, wantEN) {
		t.Errorf("an English buyer's lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantEN, "\n"))
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE project_id IS NOT NULL`); n != 0 {
		t.Errorf("%d drafts name a project; a settlement makes none", n)
	}
	noLockedReads(t, f.energy)
}

// D8: a standard line takes the settings' energy code, a household in the
// north's the exempt one, unless the request gives codes; a code given must
// exist and be active, a default too, each judged only when a line carries
// its hint; while the seller is not VAT registered both are 9.
func TestFromConsumption_VatCodes(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	exempt := modtest.One[int32](t, f.h.Harness, `SELECT energy_vat_code_exempt FROM invoices.settings`)
	f.energy.put(august(601, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	f.energy.put(august(602, customerAcme, 12, gsrnBodo, contracts.VATHintExemptHouseholdNorth))
	if got := codesOfLines(fromConsumption(t, f.h, fromConsumptionBody(customerAcme, 601, 602))); !slices.Equal(got, []int32{1, 1, 1, exempt, exempt, exempt}) {
		t.Errorf("the defaults = %v, want 1 ×3 and %d ×3", got, exempt)
	}

	g := newConsumptionFixture(t)
	g.energy.put(august(601, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	g.energy.put(august(602, customerAcme, 12, gsrnBodo, contracts.VATHintExemptHouseholdNorth))
	body := fromConsumptionBody(customerAcme, 601, 602)
	body["vatCodes"] = map[string]any{"energy": 9999}
	invalidFromConsumption(t, g.h, body, "vatCodes.energy")
	body["vatCodes"] = map[string]any{"energyExempt": 9999}
	invalidFromConsumption(t, g.h, body, "vatCodes.energyExempt")
	// An inactive default with no code given names the code; the exempt
	// field is not judged for standard lines alone.
	g.h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = 1`)
	msgs := invalidFromConsumption(t, g.h, fromConsumptionBody(customerAcme, 601), "vatCodes.energy")
	if !strings.Contains(msgs[0], "code 3") {
		t.Errorf("an inactive default = %v, want it named", msgs)
	}
	given := fromConsumptionBody(customerAcme, 601)
	given["vatCodes"] = map[string]any{"energy": 1}
	if got := invalidFromConsumption(t, g.h, given, "vatCodes.energy"); !slices.Contains(got, "This VAT code is no longer offered for new lines") {
		t.Errorf("the inactive code given = %v", got)
	}
	g.h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, exempt)
	given = fromConsumptionBody(customerAcme, 601)
	given["vatCodes"] = map[string]any{"energy": 2}
	if got := codesOfLines(fromConsumption(t, g.h, given)); !slices.Equal(got, []int32{2, 2, 2}) {
		t.Errorf("a code given = %v, want 2 on every line, the inactive exempt default unjudged", got)
	}
	both := fromConsumptionBody(customerAcme, 602)
	both["vatCodes"] = map[string]any{"energy": 2, "energyExempt": 5}
	if got := codesOfLines(fromConsumption(t, g.h, both)); !slices.Equal(got, []int32{5, 5, 5}) {
		t.Errorf("an exempt code given = %v, want 5", got)
	}

	unregistered := newConsumptionFixture(t)
	seller := completeSeller(2)
	seller["vatRegistered"] = false
	saveSeller(t, unregistered.h, seller)
	unregistered.energy.put(august(601, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	unregistered.energy.put(august(602, customerAcme, 12, gsrnBodo, contracts.VATHintExemptHouseholdNorth))
	if got := codesOfLines(fromConsumption(t, unregistered.h, fromConsumptionBody(customerAcme))); !slices.Equal(got, []int32{9, 9, 9, 9, 9, 9}) {
		t.Errorf("a seller outside the VAT register = %v, want 9 on every line", got)
	}
}

// Without settlementIds the wizard takes what Energy settles through the
// date and leaves out the blocked ones, the ones a live document holds and a
// person's yearly supply's; nothing left is 409 no_consumption_due. More
// than Energy can answer is too_many_sources.
func TestFromConsumption_WithoutIdsLeavesOut(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	f.energy.put(august(701, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	f.energy.put(monthSettlement(702, customerAcme, 11, gsrnTromso, 2026, time.July, contracts.VATHintStandard))
	blocked := august(703, customerAcme, 12, gsrnBodo, contracts.VATHintStandard)
	blocked.Blocked = contracts.SettlementIncomplete
	f.energy.put(blocked)
	f.energy.put(monthSettlement(704, customerAcme, 12, gsrnBodo, 2026, time.September, contracts.VATHintStandard))
	first := fromConsumption(t, f.h, fromConsumptionBody(customerAcme, 702))
	doc := fromConsumption(t, f.h, fromConsumptionBody(customerAcme))
	if got := heldRows(t, f.h, doc.ID); len(got) != 1 || !strings.HasPrefix(got[0], "energy.settlement:701@1 ") {
		t.Errorf("held = %v, want 701 alone: 702 is held by draft %d, 703 blocked, 704 not due", got, first.ID)
	}
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerAcme), "no_consumption_due")
	// Through the end of September, 704 is due.
	body := fromConsumptionBody(customerAcme)
	body["through"] = "2026-09-30"
	if got := heldRows(t, f.h, fromConsumption(t, f.h, body).ID); len(got) != 1 || !strings.HasPrefix(got[0], "energy.settlement:704@1 ") {
		t.Errorf("through September = %v, want 704", got)
	}

	f.energy.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 21, GSRN: gsrnTromso, Cadence: contracts.CadenceYearly})
	f.energy.put(august(711, customerPerson, 21, gsrnTromso, contracts.VATHintStandard))
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerPerson), "no_consumption_due")

	f.energy.answerMore(true)
	refusedFromConsumption(t, f.h, fromConsumptionBody(customerAcme), "too_many_sources")
	noLockedReads(t, f.energy)
}

// Settlements are added to an invoice draft of the customer at its revision:
// its own lines first, the settlements' after; 200 with the draft, its
// delivery day widened to the settlements' period. A settlement it holds is
// held by it; a stale revision, another customer's draft and an unknown one
// are refused.
func TestFromConsumption_AppendsToADraft(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	f.energy.put(august(801, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	d := createDraft(t, f.h, draftBody(customerAcme, line("Montering", 2, 500, vat25)))
	body := fromConsumptionBody(customerAcme, 801)
	body["invoiceId"], body["revision"] = d.ID, d.Revision
	res, doc := postFromConsumption(t, creator(t, f.h), body)
	if res.Status != http.StatusOK {
		t.Fatalf("append = %d %s, want 200", res.Status, res.Body)
	}
	got := linesOf(doc)
	if doc.ID != d.ID || doc.Revision != d.Revision+1 || len(got) != 4 || !strings.HasPrefix(got[0], "Montering | timer | 2 × 500 | 1 |") ||
		!strings.HasPrefix(got[1], "Spot NO4, ") || doc.NetTotal != 1000+1187.07 {
		t.Errorf("appended draft %d r%d, net %v =\n%s", doc.ID, doc.Revision, doc.NetTotal, strings.Join(got, "\n"))
	}
	// The draft's day, 10 September, widened to cover August.
	if doc.DeliveryDate != nil || doc.DeliveryFrom == nil || *doc.DeliveryFrom != "2026-08-01" || *doc.DeliveryTo != "2026-09-10" {
		t.Errorf("an append's delivery = %v %v–%v, want 2026-08-01 to 2026-09-10", doc.DeliveryDate, doc.DeliveryFrom, doc.DeliveryTo)
	}
	if rows := heldRows(t, f.h, d.ID); len(rows) != 1 || !strings.HasPrefix(rows[0], "energy.settlement:801@2 ") {
		t.Errorf("held = %v, want 801 from the second line", rows)
	}

	again := fromConsumptionBody(customerAcme, 801)
	again["invoiceId"], again["revision"] = doc.ID, doc.Revision
	if p := refusedFromConsumption(t, f.h, again, "source_held_elsewhere"); p.HeldBy == nil || p.HeldBy.InvoiceID != d.ID {
		t.Errorf("a settlement the target holds = heldBy %+v, want the target %d", p.HeldBy, d.ID)
	}
	// Without ids the target's own settlement is left out: nothing is due.
	none := fromConsumptionBody(customerAcme)
	none["invoiceId"], none["revision"] = doc.ID, doc.Revision
	refusedFromConsumption(t, f.h, none, "no_consumption_due")

	f.energy.put(monthSettlement(802, customerAcme, 11, gsrnTromso, 2026, time.July, contracts.VATHintStandard))
	body["settlementIds"] = []int64{802}
	if res, _ := postFromConsumption(t, creator(t, f.h), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "" {
		t.Errorf("a stale revision = %d %s, want the revision 409", res.Status, res.Body)
	}
	person := createDraft(t, f.h, draftBody(customerPerson, line("Arbeid", 1, 100, vat25)))
	body["invoiceId"], body["revision"] = person.ID, person.Revision
	if res, _ := postFromConsumption(t, creator(t, f.h), body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["invoiceId"]) == 0 {
		t.Errorf("another customer's draft = %d %s, want 400 on invoiceId", res.Status, res.Body)
	}
	body["invoiceId"], body["revision"] = int64(987654), 1
	if res, _ := postFromConsumption(t, creator(t, f.h), body); res.Status != http.StatusNotFound {
		t.Errorf("an unknown target = %d %s, want 404", res.Status, res.Body)
	}
	noLockedReads(t, f.energy)
}

// More than 500 lines, the target's counted, is 409 too_many_lines.
func TestFromConsumption_TooManyLines(t *testing.T) {
	t.Parallel()
	f := newConsumptionFixture(t)
	big := august(901, customerAcme, 11, gsrnTromso, contracts.VATHintStandard)
	big.Lines, big.Amount = nil, "499.00"
	for range 499 {
		big.Lines = append(big.Lines, contracts.SettlementLine{Kind: "markup", Text: "Påslag", Quantity: "1.000",
			Unit: contracts.SettlementUnitKWh, UnitPrice: "1.000000", Amount: "1.00", VATHint: contracts.VATHintStandard})
	}
	f.energy.put(big)
	d := createDraft(t, f.h, draftBody(customerAcme, line("Montering", 1, 500, vat25), line("Kabel", 1, 100, vat25)))
	body := fromConsumptionBody(customerAcme, 901)
	body["invoiceId"], body["revision"] = d.ID, d.Revision
	refusedFromConsumption(t, f.h, body, "too_many_lines")
	if doc := fromConsumption(t, f.h, fromConsumptionBody(customerAcme, 901)); len(doc.Lines) != 499 {
		t.Errorf("%d lines on a new draft, want 499", len(doc.Lines))
	}
}

// A settlement another live document holds is 409 source_held_elsewhere,
// judged under the new draft's lock: heldBy names the first draft, and
// nothing is left behind.
func TestFromConsumption_HeldElsewhere(t *testing.T) {
	f := newConsumptionFixture(t)
	f.energy.put(august(951, customerAcme, 11, gsrnTromso, contracts.VATHintStandard))
	f.energy.put(monthSettlement(952, customerAcme, 11, gsrnTromso, 2026, time.July, contracts.VATHintStandard))
	first := fromConsumption(t, f.h, fromConsumptionBody(customerAcme, 951))
	p := refusedFromConsumption(t, f.h, fromConsumptionBody(customerAcme, 952, 951), "source_held_elsewhere")
	if p.HeldBy == nil || p.HeldBy.InvoiceID != first.ID || p.SourceKind == nil || *p.SourceKind != "energy.settlement" ||
		p.SourceID == nil || *p.SourceID != 951 {
		t.Errorf("refusal = heldBy %+v, %v %v; want draft %d's energy.settlement 951", p.HeldBy, p.SourceKind, p.SourceID, first.ID)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices`); n != 1 {
		t.Errorf("%d documents, want the first alone", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.line_sources`); n != 1 {
		t.Errorf("%d holds, want the first's one", n)
	}
	noLockedReads(t, f.energy)
}
