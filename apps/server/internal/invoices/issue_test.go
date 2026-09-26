package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func issuePath(id int64) string { return fmt.Sprintf("%s/%d/issue", invoicesPath, id) }

func issuer(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:issue")
}

// issueWith issues id with date ("" for today) and answers the response.
func issueWith(t *testing.T, h *harness, id int64, date string) *modtest.Response {
	t.Helper()
	body := map[string]any{}
	if date != "" {
		body["issueDate"] = date
	}
	return issuer(t, h).Do(http.MethodPost, issuePath(id), body)
}

// issued issues id today and answers the document, failing unless it was.
func issued(t *testing.T, h *harness, id int64) invoiceJSON {
	t.Helper()
	res := issueWith(t, h, id, "")
	if res.Status != http.StatusOK {
		t.Fatalf("issue %d = %d %s, want 200", id, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	addsUp(t, inv)
	return inv
}

// readyToIssue is an installation with a complete, VAT-registered seller.
func readyToIssue(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	return h
}

// refusedWith asserts the issue of id is a 409 with code and answers it.
func refusedWith(t *testing.T, h *harness, id int64, date, code string) problemJSON {
	t.Helper()
	res := issueWith(t, h, id, date)
	if res.Status != http.StatusConflict {
		t.Fatalf("issue %d = %d %s, want 409 %s", id, res.Status, res.Body, code)
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Fatalf("issue %d = %s (%s), want %s", id, p.Code, p.Detail, code)
	}
	return p
}

// counterNext is the counter's next_value, 0 without a row.
func counterNext(t *testing.T, h *harness) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT coalesce((SELECT next_value FROM invoices.counters WHERE counter_name = 'documents'), 0)`)
}

// An invoice issued today: the next number, today's date, the due date from
// its terms, both snapshots, the rates as they stand today, and NOK at 1.
func TestIssue_AnInvoice(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1200, vat25), line("Kurs", 1, 5000, vatExempt)))

	inv := issued(t, h, draft.ID)
	if inv.Status != "issued" || inv.Number == nil || *inv.Number != 1 || *inv.IssueDate != "2026-09-12" {
		t.Fatalf("issued = %+v, want number 1 on 2026-09-12", inv)
	}
	if *inv.DueDate != "2026-10-12" || *inv.ExchangeRateDate != "2026-09-12" || inv.ExchangeRate != 1 {
		t.Errorf("due %v, rate date %v, rate %v; want 2026-10-12 (30 days), 2026-09-12, 1", *inv.DueDate, *inv.ExchangeRateDate, inv.ExchangeRate)
	}
	if inv.NetTotal != 17000 || inv.VatTotal != 3000 || inv.GrossTotal != 20000 || inv.VatTotalNok != 3000 {
		t.Errorf("totals = %v %v %v %v, want 17000, 3000, 20000, 3000", inv.NetTotal, inv.VatTotal, inv.GrossTotal, inv.VatTotalNok)
	}
	if l := inv.Lines[0]; l.VatRatePercent == nil || *l.VatRatePercent != 25 || *l.VatCategory != "S" || *l.SafTCode != "3" || l.ExemptionReason != nil {
		t.Errorf("line 1 snapshot = %+v, want 25 %% S 3", l)
	}
	if l := inv.Lines[1]; *l.VatCategory != "E" || l.ExemptionReason == nil || *l.ExemptionReason != "Unntatt fra merverdiavgift (mval. kap. 3)" {
		t.Errorf("line 2 snapshot = %+v, want E with its reason", l)
	}
	if len(inv.VatSummaries) != 2 || inv.VatSummaries[1].VatCategory != "E" || inv.VatSummaries[1].TaxableAmount != 5000 {
		t.Errorf("summaries = %+v, want the 25 %% row and the E row", inv.VatSummaries)
	}
	if inv.AllowedIssueDates != nil || inv.PdfStored == nil || len(inv.Warnings) != 0 {
		t.Errorf("issued = allowed %v, pdfStored %v, warnings %v", inv.AllowedIssueDates, inv.PdfStored, inv.Warnings)
	}
	if next := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Mer", 1, 100, vat25))).ID); *next.Number != 2 {
		t.Errorf("the second issue = %d, want 2", *next.Number)
	}
	if res := creator(t, h).Do(http.MethodPost, issuePath(draft.ID), map[string]any{}); res.Status != http.StatusForbidden {
		t.Errorf("issue without invoices:issue = %d, want 403", res.Status)
	}
	if res := issueWith(t, h, draft.ID, ""); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_issued" {
		t.Errorf("issuing again = %d %s, want 409 invoice_issued", res.Status, res.Body)
	}
	if res := issueWith(t, h, 424242, ""); res.Status != http.StatusNotFound {
		t.Errorf("issuing an unknown id = %d, want 404", res.Status)
	}
}

// Every buyer and seller snapshot column (D4); a later change to the customer
// or to the settings changes nothing on an issued document.
func TestIssue_TheSnapshots(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)

	acme := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	b := acme.Buyer
	if b == nil || b.CustomerNumber != 10001 || b.Type != "business" || b.Name != "Acme AS Norge" ||
		b.OrganisationNumber == nil || *b.OrganisationNumber != "923609016" || b.ForeignID != nil ||
		*b.AddressLine1 != "Kundeveien 2" || *b.PostalCode != "0150" || *b.City != "Oslo" || *b.Country != "NO" ||
		*b.PeppolID != "0192:923609016" || *b.Gln != "7080000000001" || b.Language != "nb" {
		t.Errorf("Acme's snapshot = %+v", b)
	}
	s := acme.Seller
	if s == nil || s.LegalName != "Kraft-Verket AS" || s.OrganisationNumber != "974760673" || !s.VatRegistered ||
		!s.InForetaksregisteret || s.BankAccount != "86011117947" || s.FooterText != "Takk for handelen." {
		t.Errorf("the seller snapshot = %+v", s)
	}
	if acme.CustomerName == nil || *acme.CustomerName != "Acme AS Norge" {
		t.Errorf("an issued document's name = %v, want its snapshot's", acme.CustomerName)
	}

	// The legal name when there is one, else the name; a foreign business's id
	// with its country; English where the profile says so.
	if b := issued(t, h, createDraft(t, h, draftBody(customerNoTerms, line("A", 1, 100, vat25))).ID).Buyer; b.Name != "Uten Vilkår AS" {
		t.Errorf("no legal name: %q, want the name", b.Name)
	}
	foreign := issued(t, h, createDraft(t, h, draftBody(customerForeign, line("A", 1, 100, vatExport))).ID).Buyer
	if foreign.OrganisationNumber != nil || foreign.ForeignID == nil || *foreign.ForeignID != "SE556677889901" || foreign.Language != "en" {
		t.Errorf("a Swedish business = %+v, want no organisation number, SE556677889901, en", foreign)
	}
	if person := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID).Buyer; person.Type != "person" || person.OrganisationNumber != nil || person.ForeignID != nil {
		t.Errorf("a person = %+v, want no identifier", person)
	}

	// Later changes change nothing issued.
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.LegalName, p.InvoiceAddress = "Nytt Navn AS", nil })
	body := completeSeller(2)
	body["legalName"] = "Kraft-Verket Holding AS"
	saveSeller(t, h, body)
	if again := getInvoice(t, h, acme.ID); again.Buyer.Name != "Acme AS Norge" || again.Seller.LegalName != "Kraft-Verket AS" {
		t.Errorf("after the changes = buyer %q seller %q, want both as issued", again.Buyer.Name, again.Seller.LegalName)
	}
}

// The issue-date rule (D6): today, or the last day of the previous month
// while today's calendar day is at most 15 and the delivery ended by then —
// and never before the latest issue date.
func TestIssue_TheIssueDateRule(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t) // 2026-09-12
	august := func() int64 {
		body := draftBody(customerAcme, line("A", 1, 100, vat25))
		body["deliveryDate"] = "2026-08-20"
		return createDraft(t, h, body).ID
	}

	for _, day := range []string{"2026-09-13", "2026-09-05", "2026-09-01"} {
		p := refusedWith(t, h, august(), day, "issue_date_not_allowed")
		if !slices.Equal(p.AllowedIssueDates, []string{"2026-08-31", "2026-09-12"}) {
			t.Errorf("%s: allowed = %v, want 2026-08-31 and 2026-09-12", day, p.AllowedIssueDates)
		}
	}
	h.Advance(3 * 24 * time.Hour) // the 15th
	if inv := getInvoice(t, h, august()); !slices.Equal(inv.AllowedIssueDates, []string{"2026-08-31", "2026-09-15"}) {
		t.Errorf("a draft's allowedIssueDates on the 15th = %v", inv.AllowedIssueDates)
	}
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	body["deliveryDate"] = "2026-09-01"
	p := refusedWith(t, h, createDraft(t, h, body).ID, "2026-08-31", "issue_date_not_allowed")
	if !slices.Equal(p.AllowedIssueDates, []string{"2026-09-15"}) {
		t.Errorf("delivered in September: allowed = %v, want only today", p.AllowedIssueDates)
	}
	previousMonth := august()
	res := issueWith(t, h, previousMonth, "2026-08-31")
	if res.Status != http.StatusOK {
		t.Fatalf("the last day of August on the 15th = %d %s, want 200", res.Status, res.Body)
	}
	// Once a document is dated in the current month, the previous month's day
	// is refused: dates follow numbers.
	issued(t, h, august())
	refusedWith(t, h, august(), "2026-08-31", "issue_date_not_allowed")
	h.Advance(24 * time.Hour) // the 16th
	p = refusedWith(t, h, august(), "2026-08-31", "issue_date_not_allowed")
	if !slices.Equal(p.AllowedIssueDates, []string{"2026-09-16"}) {
		t.Errorf("on the 16th: allowed = %v, want only today", p.AllowedIssueDates)
	}
}

// issued_late is judged on the day a document was actually issued, not the
// date it carries (§ 5-2-2 is about issuing): delivered 1 August, issued on 14
// September dated 31 August (§ 5-1-3's backdate) is late, though 31 August
// alone is within the month.
func TestIssue_IssuedLateIsJudgedOnTheDayItWasIssued(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.Advance(2 * 24 * time.Hour) // the 14th
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	body["deliveryDate"] = "2026-08-01"
	res := issueWith(t, h, createDraft(t, h, body).ID, "2026-08-31")
	if res.Status != http.StatusOK {
		t.Fatalf("issue dated 31 August = %d %s", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	if *inv.IssueDate != "2026-08-31" || !slices.Equal(inv.Warnings, []string{"issued_late"}) {
		t.Errorf("issued = dated %s warnings %v, want dated 2026-08-31 and issued_late", *inv.IssueDate, inv.Warnings)
	}
	if got := getInvoice(t, h, inv.ID); !slices.Equal(got.Warnings, []string{"issued_late"}) {
		t.Errorf("read back = warnings %v, want issued_late", got.Warnings)
	}
}

// issued_late warns when the delivery ended more than a month before the
// issue date; the issue still succeeds (§ 5-2-2).
func TestIssue_IssuedLateWarnsAndIssues(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	delete(body, "deliveryDate")
	body["deliveryFrom"], body["deliveryTo"] = "2026-07-01", "2026-07-31"

	inv := issued(t, h, createDraft(t, h, body).ID)
	if !slices.Equal(inv.Warnings, []string{"issued_late"}) {
		t.Errorf("warnings = %v, want issued_late", inv.Warnings)
	}
}

// Every refusal of D6 step 5, each rolling the number back: the issue that
// finally succeeds gets the series' first number.
func TestIssue_TheRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	draftFor := func(customer int32, lines ...map[string]any) int64 {
		return createDraft(t, h, draftBody(customer, lines...)).ID
	}
	standard := line("A", 1, 100, vat25)

	// The seller: each missing field.
	for _, field := range []string{"legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"} {
		body := completeSeller(0)
		body[field] = ""
		var current settingsJSON
		h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
		body["revision"] = current.Revision
		saveSeller(t, h, body)
		if p := refusedWith(t, h, draftFor(customerAcme, standard), "", "seller_incomplete"); !strings.Contains(p.Detail, field) {
			t.Errorf("seller_incomplete without %s: %q, want it named", field, p.Detail)
		}
	}
	var current settingsJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, settingsPath, nil).JSON(&current)
	saveSeller(t, h, completeSeller(current.Revision))

	refusedWith(t, h, draftFor(customerAcme), "", "no_lines")
	noDelivery := draftBody(customerAcme, standard)
	delete(noDelivery, "deliveryDate")
	refusedWith(t, h, createDraft(t, h, noDelivery).ID, "", "delivery_date_missing")

	// The buyer: a person with no address is refused; a business with an
	// organisation number and no address passes.
	refusedWith(t, h, draftFor(customerNoAddress, standard), "", "buyer_incomplete")
	h.customers.edit(customerNoTerms, func(p *contracts.CustomerBillingProfile) { p.InvoiceAddress = nil })

	// Reverse charge needs the buyer's organisation number.
	reverse := line("Byggetjeneste", 1, 1000, vatReverse)
	refusedWith(t, h, draftFor(customerForeign, reverse), "", "reverse_charge_needs_org_number")

	// A registered seller issues no O line.
	refusedWith(t, h, draftFor(customerAcme, line("Utenfor", 1, 100, vatOutside)), "", "category_o_not_allowed")

	// A code deactivated after the save; a code with no period on the day.
	inactive := draftFor(customerAcme, standard, line("Mat", 1, 100, vat15))
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat15)
	if p := refusedWith(t, h, inactive, "", "vat_code_inactive"); p.LinePosition == nil || *p.LinePosition != 2 {
		t.Errorf("vat_code_inactive line = %v, want 2", p.LinePosition)
	}
	h.Exec(t, `UPDATE invoices.vat_codes SET active = true WHERE id = $1`, vat15)
	var future vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "25N", "name": "Ny sats", "safTCode": "3", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-10-01",
	}).JSON(&future)
	if p := refusedWith(t, h, draftFor(customerAcme, line("Fremtid", 1, 100, future.ID)), "", "vat_code_not_valid"); p.LinePosition == nil || *p.LinePosition != 1 {
		t.Errorf("vat_code_not_valid line = %v, want 1", p.LinePosition)
	}

	// Two codes at one (category, rate) with different SAF-T codes.
	var twin vatCodeJSON
	manager(t, h).Do(http.MethodPost, vatCodesPath, map[string]any{
		"code": "3B", "name": "Tvilling", "safTCode": "3B", "ehfCategory": "S", "ratePercent": 25, "validFrom": "2026-01-01",
	}).JSON(&twin)
	refusedWith(t, h, draftFor(customerAcme, standard, line("B", 1, 100, twin.ID)), "", "vat_codes_ambiguous")

	// The customer gates, again at issue.
	blocked := draftFor(customerAcme, standard)
	h.customers.setStatus(customerAcme, "disabled")
	refusedWith(t, h, blocked, "", "customer_blocked")
	h.customers.setStatus(customerAcme, "active")

	if n := counterNext(t, h); n != 0 {
		t.Errorf("after only refusals the counter = %d, want no row: every refusal rolled its number back", n)
	}
	if inv := issued(t, h, draftFor(customerNoTerms, standard)); *inv.Number != 1 {
		t.Errorf("the first successful issue = %d, want 1", *inv.Number)
	}
	if inv := issued(t, h, draftFor(customerAcme, reverse)); *inv.Number != 2 {
		t.Errorf("reverse charge to a buyer with an organisation number = %d, want issued as 2", *inv.Number)
	}
}

// § 5-1-2's buyer address, the postal-code clause only buyerComplete holds: a
// Norwegian address without a postal code is incomplete, a foreign one
// without one is complete — many countries have none.
func TestIssue_ThePostalCodeIsNorwaysRule(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) {
		p.InvoiceAddress = &contracts.CustomerAddressEntry{Line1: "Hjemveien 5", City: "Bergen", Country: "NO"}
	})
	refusedWith(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID, "", "buyer_incomplete")

	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) {
		p.InvoiceAddress = &contracts.CustomerAddressEntry{Line1: "Main Street 1", City: "Dublin", Country: "IE"}
	})
	if inv := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID); inv.Buyer == nil || *inv.Buyer.Country != "IE" {
		t.Errorf("a foreign address without a postal code = %+v, want issued to it", inv.Buyer)
	}
}

// A body that does not decode — none at all, or an issueDate that is no
// calendar day — is the strict handler's 400, declared in the contract, and
// takes no number.
func TestIssue_ABodyThatDoesNotDecodeIs400(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	for name, body := range map[string]any{"no body": nil, "February 30": map[string]any{"issueDate": "2026-02-30"}} {
		if res := issuer(t, h).Do(http.MethodPost, issuePath(draft.ID), body); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Status, res.Body)
		}
	}
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no number taken", n)
	}
}

// A seller outside the VAT register issues only O lines (research §2.1).
func TestIssue_ANonRegisteredSeller(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["vatRegistered"] = false
	saveSeller(t, h, body)

	refusedWith(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID, "", "vat_not_registered")
	refusedWith(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vatZero))).ID, "", "vat_not_registered")
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vatOutside))).ID)
	if inv.VatTotal != 0 || inv.Seller.VatRegistered {
		t.Errorf("an O invoice = vat %v, registered %v", inv.VatTotal, inv.Seller.VatRegistered)
	}
}

// A merge that re-points the draft between the directory read and the
// transaction is caught under the lock (invoice_changed).
func TestIssue_AMergeInBetweenIsInvoiceChanged(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.customers.afterProfileRead(func(id int32) {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if id != customerAcme {
			return
		}
		if _, err := h.Pool().Exec(context.Background(), `UPDATE invoices.invoices SET customer_id = $1 WHERE id = $2`, customerNoTerms, draft.ID); err != nil {
			t.Errorf("re-point the draft: %v", err)
		}
	})

	refusedWith(t, h, draft.ID, "", "invoice_changed")
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no number taken", n)
	}
}

// A draft deleted between the directory read and the transaction is the
// issue's 404 under the lock, never a 500, and takes no number.
func TestIssue_ADeleteInBetweenIsNotFound(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.customers.afterProfileRead(func(int32) {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if _, err := h.Pool().Exec(context.Background(), `DELETE FROM invoices.invoices WHERE id = $1`, draft.ID); err != nil {
			t.Errorf("delete the draft: %v", err)
		}
	})

	if res := issueWith(t, h, draft.ID, ""); res.Status != http.StatusNotFound {
		t.Errorf("issue of a draft deleted under it = %d %s, want 404", res.Status, res.Body)
	}
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no number taken", n)
	}
}

// No object store: refused before any number is allocated (D6 step 2).
func TestIssue_WithoutAStoreNothingIsAllocated(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutStore(t)
	saveSeller(t, h, completeSeller(1))
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	res := issueWith(t, h, draft.ID, "")
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "storage_unavailable" {
		t.Fatalf("issue without a store = %d %s, want 503 storage_unavailable", res.Status, res.Body)
	}
	if n := counterNext(t, h); n != 0 {
		t.Errorf("counter = %d, want no row", n)
	}
}

// The series start: the first issue allocates exactly it, the second one
// more; a rate changed from tomorrow applies to tomorrow's issue and not
// today's.
func TestIssue_TheSeriesStartAndARateChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := completeSeller(1)
	body["seriesStart"] = 1000
	saveSeller(t, h, body)
	res := manager(t, h).Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25), map[string]any{"ratePercent": 26, "validFrom": "2026-09-13"})
	if res.Status != http.StatusCreated {
		t.Fatalf("rate change = %d %s", res.Status, res.Body)
	}
	first := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	second := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	today := issued(t, h, first.ID)
	h.Advance(24 * time.Hour)
	tomorrow := issued(t, h, second.ID)
	if *today.Number != 1000 || *tomorrow.Number != 1001 {
		t.Errorf("numbers = %d, %d, want 1000, 1001", *today.Number, *tomorrow.Number)
	}
	if today.VatTotal != 25 || tomorrow.VatTotal != 26 {
		t.Errorf("VAT = %v, %v, want the old 25 today and the new 26 tomorrow", today.VatTotal, tomorrow.VatTotal)
	}
}

// Two issues racing get consecutive numbers, and none is lost.
func TestIssue_RacingIssuesGetConsecutiveNumbers(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	var drafts []int64
	for range 6 {
		drafts = append(drafts, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	}
	var wg sync.WaitGroup
	numbers := make([]int64, len(drafts))
	for i, id := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inv invoiceJSON
			res := issueWith(t, h, id, "")
			if res.Status != http.StatusOK {
				t.Errorf("racing issue %d = %d %s", id, res.Status, res.Body)
				return
			}
			res.JSON(&inv)
			numbers[i] = *inv.Number
		}()
	}
	wg.Wait()
	slices.Sort(numbers)
	if !slices.Equal(numbers, []int64{1, 2, 3, 4, 5, 6}) {
		t.Errorf("numbers = %v, want 1..6", numbers)
	}
}

// Two racing issues with different dates cannot give a later number an
// earlier date (D6): the one that allocates second reads the latest issue
// date only after the counter's lock, so it sees the first's. Deterministic:
// A holds the counter (the hook runs right after its allocation) until B —
// asking for the last day of August — waits on it; B's only possible wait is
// the counter row, since its document differs and FOR SHARE on the settings
// is compatible with A's. Read before the lock, B would see August 31 and
// issue number 3 dated before number 2. Not parallel: the hook is the
// package's.
func TestIssue_RacingDatesStayMonotone(t *testing.T) {
	h := readyToIssue(t)
	h.Advance(3 * 24 * time.Hour) // the 15th: the last day of August is allowed
	august := func() int64 {
		body := draftBody(customerAcme, line("A", 1, 100, vat25))
		body["deliveryDate"] = "2026-08-20"
		return createDraft(t, h, body).ID
	}
	// A first issue makes the counter row, so the wait below is the ordinary
	// one, on that row's lock.
	if res := issueWith(t, h, august(), "2026-08-31"); res.Status != http.StatusOK {
		t.Fatalf("the first issue = %d %s", res.Status, res.Body)
	}
	a := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID
	b := august()

	var bRes *modtest.Response
	bDone := make(chan struct{})
	bIssuer := issuer(t, h) // signed in here, on the test's goroutine
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if id != a {
			return nil
		}
		go func() {
			defer close(bDone)
			bRes = bIssuer.Do(http.MethodPost, issuePath(b), map[string]any{"issueDate": "2026-08-31"})
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("B: %v", err)
			return err
		}
		return nil
	})
	defer restore()

	aDoc := issued(t, h, a)
	<-bDone
	if *aDoc.Number != 2 || *aDoc.IssueDate != "2026-09-15" {
		t.Errorf("A = number %d dated %s, want 2 dated 2026-09-15", *aDoc.Number, *aDoc.IssueDate)
	}
	if bRes.Status != http.StatusConflict {
		t.Fatalf("B = %d %s, want 409 issue_date_not_allowed", bRes.Status, bRes.Body)
	}
	if p := problemOf(t, bRes); p.Code != "issue_date_not_allowed" || !slices.Equal(p.AllowedIssueDates, []string{"2026-09-15"}) {
		t.Errorf("B = %s allowing %v, want issue_date_not_allowed allowing only 2026-09-15", p.Code, p.AllowedIssueDates)
	}
	if n := counterNext(t, h); n != 3 {
		t.Errorf("counter next = %d, want 3: B's number rolled back", n)
	}
}

// "Today" is read after the counter: an issue that waits there across Oslo
// midnight is dated the day it is issued. The hook moves the clock a day on
// right after the allocation. Not parallel: the hook is the package's.
func TestIssue_TodayIsReadAfterTheCounter(t *testing.T) {
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		if id == draft {
			h.Advance(24 * time.Hour)
		}
		return nil
	})
	defer restore()

	if inv := issued(t, h, draft); *inv.IssueDate != "2026-09-13" {
		t.Errorf("issue date = %s, want 2026-09-13, the day after the wait", *inv.IssueDate)
	}
}

// Two issues of one draft at once: both pass the pre-read, the second waits
// on the document's lock and finds it issued — exactly one 200 and one 409
// invoice_issued, and one number taken. Deterministic as above: the first
// holds its transaction until the second waits. Not parallel: the hook is
// the package's.
func TestIssue_TwoIssuesOfOneDraft(t *testing.T) {
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID
	var second *modtest.Response
	done := make(chan struct{})
	secondIssuer := issuer(t, h) // signed in here, on the test's goroutine
	var fired atomic.Bool
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		// Only the first issue should reach its allocation — the second
		// stops at the document's lock and finds it issued — but should it
		// not, it gets no second waiter.
		if id != draft || fired.Swap(true) {
			return nil
		}
		go func() {
			defer close(done)
			second = secondIssuer.Do(http.MethodPost, issuePath(draft), map[string]any{})
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the second issue: %v", err)
			return err
		}
		return nil
	})
	defer restore()

	first := issued(t, h, draft)
	<-done
	if *first.Number != 1 {
		t.Errorf("the first issue = number %d, want 1", *first.Number)
	}
	if second.Status != http.StatusConflict || problemOf(t, second).Code != "invoice_issued" {
		t.Errorf("the second issue = %d %s, want 409 invoice_issued", second.Status, second.Body)
	}
	if n := counterNext(t, h); n != 2 {
		t.Errorf("counter next = %d, want 2: one number taken", n)
	}
}

// A failure after the number is allocated rolls it back: the next issue gets
// it, and the series has no gap.
func TestIssue_AFailureAfterAllocationLeavesNoGap(t *testing.T) {
	h := readyToIssue(t)
	doomed := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		if id == doomed.ID {
			return errors.New("injected")
		}
		return nil
	})
	defer restore()

	res := issuer(t, h).Do(http.MethodPost, issuePath(doomed.ID), map[string]any{}, modtest.SkipContract("an injected failure answers the undeclared 500"))
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("the injected failure = %d, want 500", res.Status)
	}
	if inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25))).ID); *inv.Number != 1 {
		t.Errorf("the next issue = %d, want 1", *inv.Number)
	}
	if inv := getInvoice(t, h, doomed.ID); inv.Status != "draft" {
		t.Errorf("the failed draft = %s, want still a draft", inv.Status)
	}
}

// waitForALockWaiter polls until a session of this installation's database
// waits on a lock. It fails the test, so it runs on the test's goroutine only;
// a hook on a handler's goroutine calls awaitLockWaiter.
func waitForALockWaiter(t *testing.T, h *harness) {
	t.Helper()
	if err := awaitLockWaiter(h); err != nil {
		t.Fatal(err)
	}
}

// awaitLockWaiter is waitForALockWaiter answering an error instead of failing
// the test: what a hook running on a handler's goroutine may call.
func awaitLockWaiter(h *harness) error {
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := h.Pool().QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			return fmt.Errorf("read the lock waiters: %w", err)
		}
		if n > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("no session ever waited on a lock")
}

// A settings replace racing the first issue waits behind it and is refused:
// the settings never show a start that was not used (D2).
func TestIssue_ASettingsReplaceRacingTheFirstIssueWaitsAndIsRefused(t *testing.T) {
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	var put *modtest.Response
	done := make(chan struct{})
	settingsManager := h.SignIn(t, "invoices:access", "invoices:manage") // on the test's goroutine
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if id != draft.ID {
			return nil
		}
		go func() {
			defer close(done)
			body := completeSeller(2)
			body["seriesStart"] = 5000
			put = settingsManager.Do(http.MethodPut, settingsPath, body)
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the settings replace: %v", err)
			return err
		}
		return nil
	})
	defer restore()

	inv := issued(t, h, draft.ID)
	<-done
	if *inv.Number != 1 {
		t.Errorf("number = %d, want the start that was in force, 1", *inv.Number)
	}
	if put.Status != http.StatusConflict || problemOf(t, put).Code != "series_locked" {
		t.Errorf("the waiting replace = %d %s, want 409 series_locked", put.Status, put.Body)
	}
}

// Invoice issues racing a settings write and a bulk update of the documents'
// customer_id all finish without a deadlock: an invoice issue takes the
// document, then the settings row, then the counter, and the other two take
// one of those each. The merge holder against a credit-note issue — the one
// pair that locks two documents — is raced in customer_slots_test.go.
func TestIssue_NoDeadlockBesideASettingsWriteAndACustomerUpdate(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	var drafts []int64
	for range 4 {
		drafts = append(drafts, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	}
	var wg sync.WaitGroup
	for _, id := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := issueWith(t, h, id, "")
			if res.Status != http.StatusOK && (res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_changed") {
				t.Errorf("issue %d = %d %s", id, res.Status, res.Body)
			}
		}()
	}
	// Off the test's goroutine: t.Errorf, never a t.Fatal — so no h.Exec,
	// and the manager signs in here first.
	settingsManager := h.SignIn(t, "invoices:access", "invoices:manage")
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := h.Pool().Exec(context.Background(), `UPDATE invoices.invoices SET customer_id = $1 WHERE customer_id = $2`, customerAcme, customerAcme); err != nil {
			t.Errorf("the customer update: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		body := completeSeller(2)
		if res := settingsManager.Do(http.MethodPut, settingsPath, body); res.Status != http.StatusOK {
			t.Errorf("the settings write = %d %s", res.Status, res.Body)
		}
	}()
	wg.Wait()
}

// Immutability in SQL too (D9): bypassing the handlers, an issued document's
// columns cannot change but for customer_id and the PDF set once; it cannot be
// deleted; its lines and summaries cannot be written. A draft's delete still
// cascades.
func TestIssue_AnIssuedDocumentIsImmutableInSQL(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	ctx := context.Background()
	immutable := func(sql string, args ...any) {
		t.Helper()
		_, err := h.Pool().Exec(ctx, sql, args...)
		if err == nil || !strings.Contains(err.Error(), "invoices: issued document is immutable") || !strings.Contains(err.Error(), "P0001") {
			t.Errorf("%s: %v, want the immutability refusal", sql, err)
		}
	}

	immutable(`UPDATE invoices.invoices SET note = 'endret' WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET revision = revision + 1 WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET number = 99 WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET internal_note = 'x' WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET status = 'draft', number = NULL, issue_date = NULL WHERE id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.invoices WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.lines SET description = 'x' WHERE invoice_id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.lines WHERE invoice_id = $1`, inv.ID)
	immutable(`INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, line_gross, line_allowance, line_net)
		VALUES ($1, 9, 'x', 1, 1, 1, 1, 0, 1)`, inv.ID)
	immutable(`UPDATE invoices.vat_summaries SET vat_amount = 0 WHERE invoice_id = $1`, inv.ID)
	immutable(`DELETE FROM invoices.vat_summaries WHERE invoice_id = $1`, inv.ID)
	immutable(`INSERT INTO invoices.vat_summaries (invoice_id, vat_category, rate_percent, saf_t_code, taxable_amount, vat_amount, vat_amount_nok)
		VALUES ($1, 'Z', 0, '5', 0, 0, 0)`, inv.ID)

	// What may move: the customer (the merge holder) and the PDF, once.
	h.Exec(t, `UPDATE invoices.invoices SET customer_id = $1 WHERE id = $2`, customerNoTerms, inv.ID)
	h.Exec(t, `UPDATE invoices.invoices SET pdf_object_key = 'documents/x.pdf', pdf_sha256 = repeat('a', 64) WHERE id = $1 AND pdf_sha256 IS NULL`, inv.ID)
	immutable(`UPDATE invoices.invoices SET pdf_sha256 = repeat('b', 64) WHERE id = $1`, inv.ID)
	immutable(`UPDATE invoices.invoices SET pdf_object_key = NULL WHERE id = $1`, inv.ID)
	if n := h.Count(t, `SELECT revision FROM invoices.invoices WHERE id = $1`, inv.ID); n != int(inv.Revision) {
		t.Errorf("revision = %d after the allowed updates, want %d unmoved", n, inv.Revision)
	}

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, draft.ID)
	if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, draft.ID); n != 0 {
		t.Errorf("a deleted draft's lines = %d, want the cascade to take them", n)
	}
}

// The child-row trigger cannot be raced (D9): a line inserted beside an
// issue that has not committed yet waits for it — the trigger takes the
// document FOR SHARE — and is refused once the issue commits. Without the
// lock the insert would see the draft, succeed, and give the issued document
// a line its totals and its PDF never saw. The issue is made by hand on one
// connection, the insert on another.
func TestIssue_ALineRacingAnUncommittedIssueWaitsAndIsRefused(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE invoices.invoices SET status = 'issued', number = 1, issue_date = '2026-09-12', due_date = '2026-10-12',
			exchange_rate_date = '2026-09-12', issued_at = now(), seller_legal_name = 'Selger AS', buyer_name = 'Acme AS'
		WHERE id = $1`, draft.ID); err != nil {
		t.Fatalf("issue by hand: %v", err)
	}

	inserted := make(chan error, 1)
	go func() {
		_, err := h.Pool().Exec(ctx, `
			INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, line_gross, line_allowance, line_net)
			VALUES ($1, 9, 'Sniket inn', 1, 1, 1, 1, 0, 1)`, draft.ID)
		inserted <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := false; !waiting; {
		select {
		case err := <-inserted:
			t.Fatalf("the insert did not wait for the uncommitted issue: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the insert neither finished nor waited")
		}
		waiting = h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the issue: %v", err)
	}
	if err := <-inserted; err == nil || !strings.Contains(err.Error(), "invoices: issued document is immutable") {
		t.Errorf("the waiting insert = %v, want the immutability refusal", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.lines WHERE invoice_id = $1`, draft.ID); n != 1 {
		t.Errorf("lines = %d, want the one the draft had", n)
	}
}

// The other half of the race (D2): a settings replace that holds the row when
// the first issue arrives commits first, and the issue waits and then uses
// the new start. The replace is held open by hand — the lock PUT /settings
// takes, the update it makes — so the order is not left to the scheduler.
func TestIssue_ASettingsReplaceThatCommitsFirstSetsTheStartTheIssueUses(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices.settings WHERE id = 1 FOR UPDATE`); err != nil {
		t.Fatalf("lock the settings: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE invoices.settings SET series_start = 5000, revision = revision + 1 WHERE id = 1`); err != nil {
		t.Fatalf("change the start: %v", err)
	}

	// The issue runs off the test's goroutine, so it only answers; the test
	// asserts on it here.
	c := issuer(t, h)
	done := make(chan *modtest.Response)
	go func() { done <- c.Do(http.MethodPost, issuePath(draft.ID), map[string]any{}) }()
	waitForALockWaiter(t, h)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	res := <-done
	if res.Status != http.StatusOK {
		t.Fatalf("the waiting issue = %d %s, want 200", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	if *inv.Number != 5000 {
		t.Errorf("number = %d, want the start the replace committed, 5000", *inv.Number)
	}
}
