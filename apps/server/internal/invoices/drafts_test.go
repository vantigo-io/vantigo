package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const invoicesPath = "/api/v1/invoices"

func invoicePath(id int64) string { return fmt.Sprintf("%s/%d", invoicesPath, id) }

type lineJSON struct {
	ID              int64    `json:"id"`
	Position        int32    `json:"position"`
	Description     string   `json:"description"`
	Quantity        float64  `json:"quantity"`
	Unit            string   `json:"unit"`
	UnitPrice       float64  `json:"unitPrice"`
	DiscountPercent float64  `json:"discountPercent"`
	VatCodeID       int32    `json:"vatCodeId"`
	CreditsLineID   *int64   `json:"creditsLineId"`
	LineGross       float64  `json:"lineGross"`
	LineAllowance   float64  `json:"lineAllowance"`
	LineNet         float64  `json:"lineNet"`
	VatRatePercent  *float64 `json:"vatRatePercent"`
	VatCategory     *string  `json:"vatCategory"`
	SafTCode        *string  `json:"safTCode"`
	ExemptionReason *string  `json:"exemptionReason"`
}

type summaryJSON struct {
	VatCategory     string  `json:"vatCategory"`
	RatePercent     float64 `json:"ratePercent"`
	SafTCode        string  `json:"safTCode"`
	ExemptionReason *string `json:"exemptionReason"`
	TaxableAmount   float64 `json:"taxableAmount"`
	VatAmount       float64 `json:"vatAmount"`
	VatAmountNok    float64 `json:"vatAmountNok"`
}

type invoiceJSON struct {
	ID                int64         `json:"id"`
	Kind              string        `json:"kind"`
	Status            string        `json:"status"`
	Number            *int64        `json:"number"`
	CustomerID        int32         `json:"customerId"`
	CustomerName      *string       `json:"customerName"`
	IssueDate         *string       `json:"issueDate"`
	DeliveryDate      *string       `json:"deliveryDate"`
	DeliveryFrom      *string       `json:"deliveryFrom"`
	DeliveryTo        *string       `json:"deliveryTo"`
	PaymentTermsDays  *int32        `json:"paymentTermsDays"`
	DueDate           *string       `json:"dueDate"`
	Currency          string        `json:"currency"`
	ExchangeRate      float64       `json:"exchangeRate"`
	ExchangeRateDate  *string       `json:"exchangeRateDate"`
	YourReference     string        `json:"yourReference"`
	OurReference      string        `json:"ourReference"`
	OrderReference    string        `json:"orderReference"`
	Note              string        `json:"note"`
	InternalNote      string        `json:"internalNote"`
	NetTotal          float64       `json:"netTotal"`
	VatTotal          float64       `json:"vatTotal"`
	GrossTotal        float64       `json:"grossTotal"`
	VatTotalNok       float64       `json:"vatTotalNok"`
	Lines             []lineJSON    `json:"lines"`
	VatSummaries      []summaryJSON `json:"vatSummaries"`
	Warnings          []string      `json:"warnings"`
	AllowedIssueDates []string      `json:"allowedIssueDates"`
	PdfStored         *bool         `json:"pdfStored"`
	Kid               *string       `json:"kid"`
	KidAlgorithm      *string       `json:"kidAlgorithm"`
	Revision          int32         `json:"revision"`
	Buyer             *struct {
		CustomerNumber     int64   `json:"customerNumber"`
		Type               string  `json:"type"`
		Name               string  `json:"name"`
		OrganisationNumber *string `json:"organisationNumber"`
		ForeignID          *string `json:"foreignId"`
		AddressLine1       *string `json:"addressLine1"`
		PostalCode         *string `json:"postalCode"`
		City               *string `json:"city"`
		Country            *string `json:"country"`
		PeppolID           *string `json:"peppolId"`
		Gln                *string `json:"gln"`
		Language           string  `json:"language"`
	} `json:"buyer"`
	Seller *struct {
		LegalName            string `json:"legalName"`
		OrganisationNumber   string `json:"organisationNumber"`
		VatRegistered        bool   `json:"vatRegistered"`
		InForetaksregisteret bool   `json:"inForetaksregisteret"`
		BankAccount          string `json:"bankAccount"`
		FooterText           string `json:"footerText"`
	} `json:"seller"`
	CreditedAmount   *float64 `json:"creditedAmount"`
	UncreditedAmount *float64 `json:"uncreditedAmount"`
	CreditNotes      []struct {
		ID         int64   `json:"id"`
		Number     *int64  `json:"number"`
		IssueDate  *string `json:"issueDate"`
		GrossTotal float64 `json:"grossTotal"`
		Status     string  `json:"status"`
	} `json:"creditNotes"`
	Credits *struct {
		ID        int64  `json:"id"`
		Number    int64  `json:"number"`
		IssueDate string `json:"issueDate"`
	} `json:"credits"`
	State              string            `json:"state"`
	PaidAmount         *float64          `json:"paidAmount"`
	OpenAmount         *float64          `json:"openAmount"`
	RefundDue          *float64          `json:"refundDue"`
	Payments           []paymentJSON     `json:"payments"`
	Deliveries         []deliveryJSON    `json:"deliveries"`
	SendDefaults       *sendDefaultsJSON `json:"sendDefaults"`
	CustomerAnonymised *bool             `json:"customerAnonymised"`
}

// line is one request line on code vatCodeID.
func line(description string, quantity, unitPrice float64, vatCodeID int32) map[string]any {
	return map[string]any{"description": description, "quantity": quantity, "unit": "timer", "unitPrice": unitPrice, "vatCodeId": vatCodeID}
}

// draftBody is a draft for customer with lines, delivered on 2026-09-10.
func draftBody(customer int32, lines ...map[string]any) map[string]any {
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"customerId": customer, "deliveryDate": "2026-09-10", "lines": lines}
}

func creator(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:create")
}

// createDraft posts body and answers the draft, failing unless it was created.
func createDraft(t *testing.T, h *harness, body map[string]any) invoiceJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodPost, invoicesPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices = %d %s, want 201", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// getInvoice reads one document as an invoices:access holder.
func getInvoice(t *testing.T, h *harness, id int64) invoiceJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// A create fills what it was not told from the billing profile — the buyer
// reference and the terms — and falls back to the settings' terms; what the
// request says wins (D4).
func TestDrafts_CreateFillsWhatItWasNotTold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	acme := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 2, 1000, vat25)))
	if acme.Kind != "invoice" || acme.Status != "draft" || acme.Number != nil || acme.IssueDate != nil {
		t.Errorf("draft = %+v, want an unnumbered invoice draft", acme)
	}
	if acme.YourReference != "PO-77" || acme.PaymentTermsDays == nil || *acme.PaymentTermsDays != 30 {
		t.Errorf("prefills = %q, %v; want the profile's PO-77 and 30 days", acme.YourReference, acme.PaymentTermsDays)
	}
	if acme.CustomerName == nil || *acme.CustomerName != "Acme AS" || acme.Currency != "NOK" || acme.ExchangeRate != 1 {
		t.Errorf("draft = name %v, %s at %v; want Acme AS in NOK at 1", acme.CustomerName, acme.Currency, acme.ExchangeRate)
	}
	if acme.Buyer != nil || acme.Seller != nil {
		t.Error("an invoice draft carries a snapshot; the issue writes them")
	}
	if read := getInvoice(t, h, acme.ID); read.YourReference != "PO-77" || len(read.Lines) != 1 || read.GrossTotal != 2500 ||
		!slices.Equal(read.AllowedIssueDates, []string{"2026-09-12"}) {
		t.Errorf("GET = %+v, want the draft as created, 2 × 1000 + 25 %%, issuable today", read)
	}

	none := createDraft(t, h, draftBody(customerNoTerms))
	if none.YourReference != "" || none.PaymentTermsDays == nil || *none.PaymentTermsDays != 14 {
		t.Errorf("prefills without a profile = %q, %v; want empty and the settings' 14", none.YourReference, none.PaymentTermsDays)
	}

	body := draftBody(customerAcme)
	body["yourReference"], body["paymentTermsDays"], body["ourReference"] = "", 10, "Ola Nordmann"
	told := createDraft(t, h, body)
	if told.YourReference != "" || *told.PaymentTermsDays != 10 || told.OurReference != "Ola Nordmann" {
		t.Errorf("told = %q, %v, %q; want what the request said", told.YourReference, *told.PaymentTermsDays, told.OurReference)
	}
}

// The customer gates, in their order, on create and on save (D4).
func TestDrafts_TheCustomerGates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	for _, g := range []struct {
		customer int32
		code     string
	}{
		{customerMerged, "customer_merged"},
		{customerArchived, "customer_archived"},
		{customerDisabled, "customer_blocked"},
		{customerUnknown, "customer_missing"},
	} {
		res := c.Do(http.MethodPost, invoicesPath, draftBody(g.customer))
		if res.Status != http.StatusConflict {
			t.Errorf("customer %d = %d %s, want 409 %s", g.customer, res.Status, res.Body, g.code)
			continue
		}
		p := problemOf(t, res)
		if p.Code != g.code {
			t.Errorf("customer %d = %s, want %s", g.customer, p.Code, g.code)
		}
		if g.code == "customer_merged" && (p.MergedInto == nil || *p.MergedInto != customerAcme) {
			t.Errorf("customer_merged mergedInto = %v, want %d", p.MergedInto, customerAcme)
		}
	}

	draft := createDraft(t, h, draftBody(customerAcme))
	h.customers.setStatus(customerAcme, "disabled")
	body := draftBody(customerAcme)
	body["paymentTermsDays"], body["revision"] = 30, draft.Revision
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "customer_blocked" {
		t.Errorf("saving for a customer disabled since = %d %s, want 409 customer_blocked", res.Status, res.Body)
	}
}

// A replace is whole and carries its revision; a delete takes the lines with
// it; an issued document answers invoice_issued to both (D4).
func TestDrafts_ReplaceAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 1000, vat25), line("Reise", 1, 500, vat25)))

	body := draftBody(customerAcme, line("Rådgivning", 3, 1200, vat25))
	body["paymentTermsDays"], body["revision"], body["note"] = 20, draft.Revision, "Takk"
	res := c.Do(http.MethodPut, invoicePath(draft.ID), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	var replaced invoiceJSON
	res.JSON(&replaced)
	if len(replaced.Lines) != 1 || replaced.Lines[0].Description != "Rådgivning" || replaced.Note != "Takk" ||
		replaced.YourReference != "" || *replaced.PaymentTermsDays != 20 || replaced.Revision != draft.Revision+1 {
		t.Errorf("replaced = %+v, want the one new line, the note, the reference cleared, 20 days, the revision on", replaced)
	}

	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusConflict || problemOf(t, res).Code != "" {
		t.Errorf("a stale revision = %d %s, want 409 without a code", res.Status, res.Body)
	}
	delete(body, "revision")
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusBadRequest {
		t.Errorf("a replace without a revision = %d, want 400", res.Status)
	}
	body["revision"] = replaced.Revision
	delete(body, "paymentTermsDays")
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["paymentTermsDays"]) == 0 {
		t.Errorf("a replace without terms = %d %s, want 400 on paymentTermsDays", res.Status, res.Body)
	}

	if res := c.Do(http.MethodDelete, invoicePath(draft.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, draft.ID); n != 0 {
		t.Errorf("lines after the delete = %d, want the cascade to take them", n)
	}
	if res := c.Do(http.MethodDelete, invoicePath(draft.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE again = %d, want 404", res.Status)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(draft.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("GET a deleted draft = %d, want 404", res.Status)
	}

	issued := plantIssuedDocument(t, h, 1, "2026-09-01")
	body["revision"] = 1
	body["paymentTermsDays"] = 14
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var send any
		if method == http.MethodPut {
			send = body
		}
		if res := c.Do(method, invoicePath(issued), send); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
			t.Errorf("%s an issued document = %d %s, want 409 invoice_issued", method, res.Status, res.Body)
		}
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, invoicesPath, draftBody(customerAcme)); res.Status != http.StatusForbidden {
		t.Errorf("POST without invoices:create = %d, want 403", res.Status)
	}
}

// Delivery is a day, a period with from on or before to, or — on a draft —
// nothing; the place of delivery is whole or absent (D4).
func TestDrafts_Delivery(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	period := draftBody(customerAcme)
	delete(period, "deliveryDate")
	period["deliveryFrom"], period["deliveryTo"] = "2026-08-01", "2026-08-31"
	if inv := createDraft(t, h, period); inv.DeliveryFrom == nil || *inv.DeliveryTo != "2026-08-31" || inv.DeliveryDate != nil {
		t.Errorf("a period = %+v", inv)
	}
	none := draftBody(customerAcme)
	delete(none, "deliveryDate")
	if inv := createDraft(t, h, none); inv.DeliveryDate != nil || inv.DeliveryFrom != nil {
		t.Errorf("no delivery = %+v, want none on a draft", inv)
	}
	for _, bad := range []struct {
		name  string
		edit  func(map[string]any)
		field string
	}{
		{"a day and a period", func(b map[string]any) { b["deliveryFrom"], b["deliveryTo"] = "2026-08-01", "2026-08-31" }, "deliveryDate"},
		{"a period without its end", func(b map[string]any) { delete(b, "deliveryDate"); b["deliveryFrom"] = "2026-08-01" }, "deliveryTo"},
		{"a period that ends before it starts", func(b map[string]any) {
			delete(b, "deliveryDate")
			b["deliveryFrom"], b["deliveryTo"] = "2026-08-31", "2026-08-01"
		}, "deliveryTo"},
		{"a place without a city", func(b map[string]any) {
			b["deliveryAddress"] = map[string]any{"line1": "Byggeplassen", "city": "", "country": "NO"}
		}, "deliveryAddress.city"},
		{"a country ISO never assigned", func(b map[string]any) {
			b["deliveryAddress"] = map[string]any{"line1": "Byggeplassen", "city": "Oslo", "country": "ZZ"}
		}, "deliveryAddress.country"},
	} {
		body := draftBody(customerAcme)
		bad.edit(body)
		if res := c.Do(http.MethodPost, invoicesPath, body); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
}

// A draft warns — never refuses — when the customer invoices in another
// currency, and when its delivery is more than a month before today (D4).
func TestDrafts_Warnings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if inv := createDraft(t, h, draftBody(customerEuro)); !slices.Equal(inv.Warnings, []string{"customer_currency_differs"}) {
		t.Errorf("warnings for an EUR customer = %v, want customer_currency_differs", inv.Warnings)
	}
	late := draftBody(customerAcme)
	late["deliveryDate"] = "2026-08-11"
	if inv := createDraft(t, h, late); !slices.Equal(inv.Warnings, []string{"issued_late"}) {
		t.Errorf("warnings for a delivery on 2026-08-11 on 2026-09-12 = %v, want issued_late", inv.Warnings)
	}
	late["deliveryDate"] = "2026-08-12"
	if inv := createDraft(t, h, late); len(inv.Warnings) != 0 {
		t.Errorf("warnings for a delivery exactly a month back = %v, want none", inv.Warnings)
	}
}

// The money rules (D5): a float is read as the decimal it was written as;
// too many decimals, the line and total bounds and the 500-line cap are 400s,
// never a 500; a line is gross less allowance; VAT is per rate on the sum of
// the nets; only NOK.
func TestDrafts_TheMoney(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := creator(t, h)

	// 0.1 × 3 is exactly 0.30.
	if inv := createDraft(t, h, draftBody(customerAcme, line("Småting", 3, 0.1, vat25))); inv.Lines[0].LineGross != 0.3 || inv.NetTotal != 0.3 {
		t.Errorf("3 × 0.1 = %v, want 0.30", inv.Lines[0].LineGross)
	}

	// Three lines of 33.33 at 25 %: per line the VAT would be 3 × 8.33 =
	// 24.99; per rate it is round(99.99 × 25 %) = 25.00, and so is the total.
	third := line("Tredjedel", 1, 33.33, vat25)
	perRate := createDraft(t, h, draftBody(customerAcme, third, third, third))
	if perRate.NetTotal != 99.99 || perRate.VatTotal != 25 || perRate.GrossTotal != 124.99 || perRate.VatTotalNok != 25 {
		t.Errorf("per rate = net %v vat %v gross %v, want 99.99, 25.00, 124.99", perRate.NetTotal, perRate.VatTotal, perRate.GrossTotal)
	}

	// A discount is an allowance of the rounded gross, itself rounded, and the
	// net is the difference (D5): 1 × 33.35 at 10 % is an allowance of
	// round(3.335) = 3.34 and a net of 30.01. One rounding of the discounted
	// amount, round(33.35 × 0.9 = 30.015), would give 30.02.
	discounted := line("Rabattert", 1, 33.35, vat25)
	discounted["discountPercent"] = 10
	if inv := createDraft(t, h, draftBody(customerAcme, discounted)); inv.Lines[0].LineGross != 33.35 || inv.Lines[0].LineAllowance != 3.34 || inv.Lines[0].LineNet != 30.01 {
		t.Errorf("discounted line = %+v, want 33.35 − 3.34 = 30.01", inv.Lines[0])
	}

	// Half away from zero, not half to even: 1 × 10.02 at 25 % is VAT
	// round(2.505) = 2.51, where banker's rounding would give 2.50.
	if inv := createDraft(t, h, draftBody(customerAcme, line("Halv", 1, 10.02, vat25))); inv.VatTotal != 2.51 || inv.GrossTotal != 12.53 {
		t.Errorf("1 × 10.02 at 25 %% = VAT %v gross %v, want 2.51 and 12.53", inv.VatTotal, inv.GrossTotal)
	}

	// Three rates, a 0 % category with its own row, no øre rounding.
	mixed := createDraft(t, h, draftBody(customerAcme,
		line("Tjeneste", 1, 100.01, vat25), line("Mat", 2, 100, vat15), line("Fritatt", 1, 50.5, vatZero)))
	type row struct {
		category           string
		rate, taxable, vat float64
	}
	var rows []row
	for _, s := range mixed.VatSummaries {
		rows = append(rows, row{s.VatCategory, s.RatePercent, s.TaxableAmount, s.VatAmount})
	}
	if want := []row{{"S", 25, 100.01, 25}, {"S", 15, 200, 30}, {"Z", 0, 50.5, 0}}; !slices.Equal(rows, want) {
		t.Errorf("summaries = %+v, want %+v", rows, want)
	}
	if mixed.NetTotal != 350.51 || mixed.VatTotal != 55 || mixed.GrossTotal != 405.51 {
		t.Errorf("totals = %v, %v, %v, want 350.51, 55.00, 405.51", mixed.NetTotal, mixed.VatTotal, mixed.GrossTotal)
	}

	for _, bad := range []struct {
		name  string
		line  map[string]any
		field string
	}{
		{"four quantity decimals", map[string]any{"quantity": 1.2345}, "lines[0].quantity"},
		{"five unit price decimals", map[string]any{"unitPrice": 1.23456}, "lines[0].unitPrice"},
		{"three discount decimals", map[string]any{"discountPercent": 1.234}, "lines[0].discountPercent"},
		{"a zero quantity", map[string]any{"quantity": 0}, "lines[0].quantity"},
		{"a negative price", map[string]any{"unitPrice": -1}, "lines[0].unitPrice"},
		{"a discount over 100", map[string]any{"discountPercent": 101}, "lines[0].discountPercent"},
		{"a line amount over the bound", map[string]any{"quantity": 1000, "unitPrice": 1000000}, "lines[0].unitPrice"},
		{"no description", map[string]any{"description": "  "}, "lines[0].description"},
		{"an unknown VAT code", map[string]any{"vatCodeId": vatUnknownID}, "lines[0].vatCodeId"},
	} {
		l := line("Linje", 1, 100, vat25)
		for k, v := range bad.line {
			l[k] = v
		}
		res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, l))
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Linje", 1, 100, vat25), line("Rabatt", 1, 100, vat25))); res.Status != http.StatusCreated {
		t.Fatalf("two valid lines = %d %s, want 201", res.Status, res.Body)
	}

	// The document bound: 101 lines at the line bound, at 25 %.
	var big []map[string]any
	for range 101 {
		big = append(big, line("Stor", 1, 999999999.99, vat25))
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, big...)); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["lines"], []string{"The document total is too large"}) {
		t.Errorf("a total over the bound = %d %s, want 400 on lines", res.Status, res.Body)
	}
	var many []map[string]any
	for range 501 {
		many = append(many, line("Mange", 1, 1, vat25))
	}
	if res := c.Do(http.MethodPost, invoicesPath, draftBody(customerAcme, many...)); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["lines"], []string{"At most 500 lines"}) {
		t.Errorf("501 lines = %d %s, want 400 \"At most 500 lines\"", res.Status, res.Body)
	}

	// NOK only, on create and on save.
	eur := draftBody(customerAcme)
	eur["currency"] = "EUR"
	if res := c.Do(http.MethodPost, invoicesPath, eur); res.Status != http.StatusBadRequest ||
		!slices.Equal(problemOf(t, res).Errors["currency"], []string{"Only NOK in this phase"}) {
		t.Errorf("EUR on create = %d %s, want 400 on currency", res.Status, res.Body)
	}
	draft := createDraft(t, h, draftBody(customerAcme))
	eur["revision"], eur["paymentTermsDays"] = draft.Revision, 14
	if res := c.Do(http.MethodPut, invoicePath(draft.ID), eur); res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["currency"]) == 0 {
		t.Errorf("EUR on save = %d %s, want 400 on currency", res.Status, res.Body)
	}
}

// A deactivated code is refused on a new line; a draft may have no lines
// (the issue refuses that).
func TestDrafts_AnInactiveCodeAndNoLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat15)

	res := creator(t, h).Do(http.MethodPost, invoicesPath, draftBody(customerAcme, line("Mat", 1, 100, vat15)))
	if res.Status != http.StatusBadRequest || !strings.Contains(strings.Join(problemOf(t, res).Errors["lines[0].vatCodeId"], " "), "no longer offered") {
		t.Errorf("an inactive code = %d %s, want 400 on lines[0].vatCodeId", res.Status, res.Body)
	}
	if inv := createDraft(t, h, draftBody(customerAcme)); len(inv.Lines) != 0 || inv.GrossTotal != 0 {
		t.Errorf("an empty draft = %+v", inv)
	}
}

// A line whose code has no rate period covering today is a warning on the
// draft, never a refusal: the draft totals it at 0 %, and the issue would
// refuse it (vat_code_not_valid).
func TestDrafts_ACodeWithNoRateTodayWarns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var future vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "25N", "name": "Ny sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-10-01",
	}).JSON(&future)

	inv := createDraft(t, h, draftBody(customerAcme, line("Fremtid", 1, 100, future.ID), line("Nå", 1, 100, vat25)))
	if !slices.Equal(inv.Warnings, []string{"vat_code_not_valid"}) || inv.VatTotal != 25 {
		t.Errorf("warnings %v, VAT %v; want vat_code_not_valid and only the 25 %% line's VAT", inv.Warnings, inv.VatTotal)
	}
}

// Changing or deleting a draft needs invoices:create besides invoices:access.
func TestDrafts_ReplaceAndDeleteNeedCreate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	reader := h.SignIn(t, "invoices:access", "invoices:issue", "invoices:manage")
	body := draftBody(customerAcme)
	body["paymentTermsDays"], body["revision"] = 14, draft.Revision
	if res := reader.Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusForbidden {
		t.Errorf("PUT without invoices:create = %d, want 403", res.Status)
	}
	if res := reader.Do(http.MethodDelete, invoicePath(draft.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("DELETE without invoices:create = %d, want 403", res.Status)
	}
	if got := getInvoice(t, h, draft.ID); got.Revision != draft.Revision || len(got.Lines) != 1 {
		t.Errorf("the draft after the refusals = %+v, want it untouched", got)
	}
}

// A draft deleted between the PUT's first read and its lock is the PUT's 404,
// never a 500: two users, one deleting and one saving, is a real race. The
// delete lands after the billing-profile read, which runs between the two.
func TestDrafts_AReplaceRacingADeleteIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.customers.afterProfileRead(func(int32) {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if _, err := h.Pool().Exec(context.Background(), `DELETE FROM invoices.invoices WHERE id = $1`, draft.ID); err != nil {
			t.Errorf("delete the draft: %v", err)
		}
	})

	body := draftBody(customerAcme, line("B", 1, 200, vat25))
	body["paymentTermsDays"], body["revision"] = 14, draft.Revision
	if res := creator(t, h).Do(http.MethodPut, invoicePath(draft.ID), body); res.Status != http.StatusNotFound {
		t.Errorf("a replace of a draft deleted under it = %d %s, want 404", res.Status, res.Body)
	}
}

// A draft in another currency shows its VAT in NOK at its own exchange rate,
// as its issue will write it (D5) — never at 1. Only NOK is accepted in this
// phase, so the currency and the rate are planted.
func TestDrafts_VatInNOKIsAtTheDraftsRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.Exec(t, `UPDATE invoices.invoices SET currency = 'EUR', exchange_rate = 11.5 WHERE id = $1`, draft.ID)

	inv := getInvoice(t, h, draft.ID)
	if inv.VatTotal != 25 || inv.VatTotalNok != 287.5 {
		t.Errorf("a EUR draft at 11.5 = VAT %v, NOK %v, want 25.00 and 287.50", inv.VatTotal, inv.VatTotalNok)
	}
	if len(inv.VatSummaries) != 1 || inv.VatSummaries[0].VatAmountNok != 287.5 {
		t.Errorf("summaries = %+v, want one row with 287.50 NOK", inv.VatSummaries)
	}
}

// A document carries its KID and the algorithm it was computed with once it
// is issued under the agreement (EHF and KID design D3, D14), and a draft
// under the same agreement carries neither: the KID comes from the number,
// which only the issue allocates.
func TestDocument_CarriesItsKID(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	withKidAgreement(t, h, 6, "mod11")
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 1, 1000, vat25)))
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(draft.ID), nil)
	if strings.Contains(string(res.Body), `"kid`) {
		t.Errorf("a draft = %s, want no kid and no kidAlgorithm", res.Body)
	}
	issued(t, h, draft.ID)
	// 1 under 6/mod11: 00001, 1 × 2 = 2, 11 − 2 = 9.
	if got := getInvoice(t, h, draft.ID); got.Kid == nil || *got.Kid != "000019" || got.KidAlgorithm == nil || *got.KidAlgorithm != "mod11" {
		t.Errorf("issued = kid %v algorithm %v, want 000019 and mod11", got.Kid, got.KidAlgorithm)
	}
}
