package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func creditPath(id int64) string { return fmt.Sprintf("%s/%d/credit", invoicesPath, id) }

// creditDraft makes a credit-note draft of an issued invoice.
func creditDraft(t *testing.T, h *harness, original int64) invoiceJSON {
	t.Helper()
	res := issuer(t, h).Do(http.MethodPost, creditPath(original), nil)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /credit = %d %s, want 201", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// creditBody is a replace of a credit draft keeping everything but lines.
func creditBody(c invoiceJSON, lines ...map[string]any) map[string]any {
	body := map[string]any{
		"customerId": c.CustomerID, "yourReference": c.YourReference, "ourReference": c.OurReference,
		"orderReference": c.OrderReference, "revision": c.Revision, "lines": lines,
	}
	if c.DeliveryDate != nil {
		body["deliveryDate"] = *c.DeliveryDate
	}
	if lines == nil {
		body["lines"] = []map[string]any{}
	}
	return body
}

// creditLine is a replace line crediting l.
func creditLine(l lineJSON) map[string]any {
	return map[string]any{
		"description": l.Description, "quantity": l.Quantity, "unit": l.Unit, "unitPrice": l.UnitPrice,
		"discountPercent": l.DiscountPercent, "vatCodeId": l.VatCodeID, "creditsLineId": *l.CreditsLineID,
	}
}

func saveCredit(t *testing.T, h *harness, c invoiceJSON, body map[string]any) invoiceJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT a credit draft = %d %s", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// A credit-note draft copies what the correction must name, from the
// original, with no directory read; it is issued in the same series, for a
// customer archived since and with a VAT code deactivated since, at the
// original's rates; the two documents link to each other (D8).
func TestCredit_TheDraftCopiesAndIsIssuedInTheSameSeries(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := draftBody(customerAcme, line("Konsulenttime", 10, 1000, vat25), line("Kurs", 1, 2000, vatExempt))
	body["deliveryAddress"] = map[string]any{"line1": "Byggeplassen", "city": "Bergen", "country": "NO"}
	body["ourReference"] = "Ola"
	original := issued(t, h, createDraft(t, h, body).ID)

	h.customers.setStatus(customerAcme, "archived")
	h.Exec(t, `UPDATE invoices.vat_codes SET active = false WHERE id = $1`, vat25)
	reads := 0
	h.customers.afterProfileRead(func(int32) { reads++ })

	c := creditDraft(t, h, original.ID)
	if c.Kind != "credit_note" || c.Status != "draft" || c.PaymentTermsDays != nil || c.Credits == nil || c.Credits.Number != *original.Number {
		t.Fatalf("credit draft = %+v", c)
	}
	if c.Buyer == nil || c.Buyer.Name != original.Buyer.Name || *c.DeliveryDate != *original.DeliveryDate ||
		c.OurReference != "Ola" || c.YourReference != "PO-77" || len(c.Lines) != 2 {
		t.Errorf("credit draft = %+v, want the original's buyer, delivery, references and lines", c)
	}
	if *c.Lines[0].CreditsLineID != original.Lines[0].ID || c.Lines[0].VatCodeID != vat25 {
		t.Errorf("line 1 = %+v, want it pointing at the original's line 1 on its code", c.Lines[0])
	}
	if c.GrossTotal != original.GrossTotal {
		t.Errorf("credit draft gross = %v, want the original's %v at the original's rates", c.GrossTotal, original.GrossTotal)
	}

	issuedCredit := issued(t, h, c.ID)
	if *issuedCredit.Number != 2 || issuedCredit.DueDate != nil || issuedCredit.VatTotal != 2500 || issuedCredit.GrossTotal != 14500 {
		t.Errorf("issued credit = number %d due %v vat %v gross %v, want 2, none, 2500, 14500", *issuedCredit.Number, issuedCredit.DueDate, issuedCredit.VatTotal, issuedCredit.GrossTotal)
	}
	if l := issuedCredit.Lines[0]; *l.VatRatePercent != 25 || *l.VatCategory != "S" {
		t.Errorf("a deactivated code's line = %+v, want the original's 25 %% S", l)
	}
	if reads != 0 {
		t.Errorf("the credit note read the directory %d times, want never", reads)
	}
	after := getInvoice(t, h, original.ID)
	if after.CreditedAmount == nil || *after.CreditedAmount != 14500 || *after.UncreditedAmount != 0 ||
		len(after.CreditNotes) != 1 || after.CreditNotes[0].ID != c.ID || *after.CreditNotes[0].Number != 2 {
		t.Errorf("the original = credited %v uncredited %v notes %+v", after.CreditedAmount, after.UncreditedAmount, after.CreditNotes)
	}
	if res := download(t, h, issuedCredit.ID); res.Header("Content-Disposition") != `attachment; filename="kreditnota-2.pdf"` {
		t.Errorf("a credit note's file = %q", res.Header("Content-Disposition"))
	}
	if res := issuer(t, h).Do(http.MethodPost, creditPath(original.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_fully_credited" {
		t.Errorf("crediting a fully credited invoice = %d %s, want 409 invoice_fully_credited", res.Status, res.Body)
	}
	if res := issuer(t, h).Do(http.MethodPost, creditPath(issuedCredit.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "credit_note_not_creditable" {
		t.Errorf("crediting a credit note = %d %s, want 409 credit_note_not_creditable", res.Status, res.Body)
	}
	draft := createDraft(t, h, draftBody(customerNoTerms, line("A", 1, 100, vat15)))
	if res := issuer(t, h).Do(http.MethodPost, creditPath(draft.ID), nil); res.Status != http.StatusConflict || problemOf(t, res).Code != "invoice_draft" {
		t.Errorf("crediting a draft = %d %s, want 409 invoice_draft", res.Status, res.Body)
	}
	if res := creator(t, h).Do(http.MethodPost, creditPath(original.ID), nil); res.Status != http.StatusForbidden {
		t.Errorf("POST /credit without invoices:issue = %d, want 403", res.Status)
	}
}

// What a credit draft may change, and a 400 on the field for everything else
// (D8).
func TestCredit_WhatADraftMayChange(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	disc := line("Rabattert", 4, 500, vat25)
	disc["discountPercent"] = 10
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 1000, vat25), disc)).ID)
	c := creditDraft(t, h, original.ID)

	lower := creditLine(c.Lines[0])
	lower["quantity"], lower["unitPrice"], lower["description"] = 2, 900, "Prisavslag"
	body := creditBody(c, lower)
	body["note"] = "Retting av pris"
	saved := saveCredit(t, h, c, body)
	if len(saved.Lines) != 1 || saved.Lines[0].LineNet != 1800 || saved.Lines[0].Description != "Prisavslag" || saved.Note != "Retting av pris" || saved.GrossTotal != 2250 {
		t.Errorf("a lowered credit = %+v", saved)
	}

	for _, bad := range []struct {
		name  string
		edit  func(map[string]any, []map[string]any)
		field string
	}{
		{"a raised quantity", func(_ map[string]any, l []map[string]any) { l[0]["quantity"] = 11 }, "lines[0].quantity"},
		{"a raised price", func(_ map[string]any, l []map[string]any) { l[0]["unitPrice"] = 1001 }, "lines[0].unitPrice"},
		{"a lowered discount", func(_ map[string]any, l []map[string]any) { l[1]["discountPercent"] = 5 }, "lines[1].discountPercent"},
		{"another VAT code", func(_ map[string]any, l []map[string]any) { l[0]["vatCodeId"] = vat15 }, "lines[0].vatCodeId"},
		{"a line twice", func(_ map[string]any, l []map[string]any) { l[1]["creditsLineId"] = l[0]["creditsLineId"] }, "lines[1].creditsLineId"},
		{"a new line", func(_ map[string]any, l []map[string]any) { delete(l[1], "creditsLineId") }, "lines[1].creditsLineId"},
		{"another customer", func(b map[string]any, _ []map[string]any) { b["customerId"] = customerNoTerms }, "customerId"},
		{"another currency", func(b map[string]any, _ []map[string]any) { b["currency"] = "EUR" }, "currency"},
		{"payment terms", func(b map[string]any, _ []map[string]any) { b["paymentTermsDays"] = 14 }, "paymentTermsDays"},
		{"another reference", func(b map[string]any, _ []map[string]any) { b["yourReference"] = "Ny" }, "yourReference"},
		{"another delivery", func(b map[string]any, _ []map[string]any) { b["deliveryDate"] = "2026-09-11" }, "deliveryDate"},
	} {
		lines := []map[string]any{creditLine(c.Lines[0]), creditLine(c.Lines[1])}
		body := creditBody(saved, lines...)
		bad.edit(body, lines)
		res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), body)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors[bad.field]) == 0 {
			t.Errorf("%s = %d %s, want 400 on %s", bad.name, res.Status, res.Body, bad.field)
		}
	}
}

// The per-line cap (D8): line A at 25 % credited in full once, then again,
// is refused although the total still fits, because the 0 % line B was never
// credited; the cap only warns on the save.
func TestCredit_ThePerLineCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25), line("B", 1, 5000, vatZero))).ID)

	first := creditDraft(t, h, original.ID)
	issued(t, h, saveCredit(t, h, first, creditBody(first, creditLine(first.Lines[0]))).ID)

	second := creditDraft(t, h, original.ID)
	saved := saveCredit(t, h, second, creditBody(second, creditLine(second.Lines[0])))
	if !slices.Contains(saved.Warnings, "credit_exceeds_line") {
		t.Errorf("warnings on the save = %v, want credit_exceeds_line", saved.Warnings)
	}
	if saved.GrossTotal > *getInvoice(t, h, original.ID).UncreditedAmount {
		t.Fatal("the fixture's total does not fit, so it proves nothing about the line cap")
	}
	p := refusedWith(t, h, saved.ID, "", "credit_exceeds_line")
	if p.LinePosition == nil || *p.LinePosition != 1 {
		t.Errorf("credit_exceeds_line names line %v, want 1", p.LinePosition)
	}
	if n := counterNext(t, h); n != 3 {
		t.Errorf("counter next = %d, want 3: the refused credit note's number rolled back", n)
	}
}

// The headline cap: a credit note's gross never passes what the invoice has
// left, counting every issued credit note.
func TestCredit_TheHeadlineCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	c := creditDraft(t, h, original.ID)
	// An issued credit note of 1000 with no lines — planted, so the line cap
	// has nothing to count and only the headline decides.
	modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		VALUES ('credit_note', 'issued', 50, $1, $2, '2026-09-12', 1000, now(), $3, now(), now()) RETURNING id`, customerAcme, original.ID, uuid.New())

	if got := getInvoice(t, h, c.ID); !slices.Contains(got.Warnings, "credit_exceeds_invoice") {
		t.Errorf("warnings = %v, want credit_exceeds_invoice", got.Warnings)
	}
	refusedWith(t, h, c.ID, "", "credit_exceeds_invoice")
}

// Two credit notes racing against one original cannot together pass a line's
// cap: the second issue waits on the original's lock and sees the first.
func TestCredit_RacingCreditNotesKeepTheCap(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	a, b := creditDraft(t, h, original.ID), creditDraft(t, h, original.ID)

	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := issueWith(t, h, id, "")
			statuses[i] = res.Status
			if res.Status == http.StatusConflict && problemOf(t, res).Code != "credit_exceeds_line" {
				t.Errorf("the losing issue = %s, want credit_exceeds_line", problemOf(t, res).Code)
			}
		}()
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Errorf("statuses = %v, want one issued and one refused", statuses)
	}
}

// A credit note corrects an invoice whatever became of its customer since —
// disabled, merged away or anonymised — and the credit note shows the buyer
// the original named (D8): the gates for new invoices never apply to it.
func TestCredit_ACustomerDisabledMergedOrAnonymisedSince(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		change func(*contracts.CustomerBillingProfile)
	}{
		{"disabled", func(p *contracts.CustomerBillingProfile) { p.Status = "disabled" }},
		{"merged away", func(p *contracts.CustomerBillingProfile) {
			into := int32(customerEuro)
			p.Status, p.Archived, p.MergedInto = "archived", true, &into
		}},
		{"anonymised", func(p *contracts.CustomerBillingProfile) {
			p.Status, p.Archived, p.Name, p.LegalName, p.InvoiceAddress = "archived", true, "Anonymisert", "", nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := readyToIssue(t)
			original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
			h.customers.edit(customerAcme, c.change)

			credit := issued(t, h, creditDraft(t, h, original.ID).ID)
			if credit.Buyer == nil || credit.Buyer.Name != original.Buyer.Name || credit.GrossTotal != original.GrossTotal {
				t.Errorf("the credit note = buyer %+v gross %v, want the original's %q and %v", credit.Buyer, credit.GrossTotal, original.Buyer.Name, original.GrossTotal)
			}
		})
	}
}

// A credit note keeps its original's rate for a code whose last period has
// ended since: it re-looks up no rate, so an expired code is no refusal (D8).
func TestCredit_ACodeExpiredSince(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	h.Exec(t, `UPDATE invoices.vat_code_rates SET valid_to = '2026-09-12' WHERE vat_code_id = $1 AND valid_to IS NULL`, vat25)
	h.Advance(24 * time.Hour)

	c := creditDraft(t, h, original.ID)
	if slices.Contains(c.Warnings, "vat_code_not_valid") {
		t.Errorf("warnings = %v: a credit note's line keeps its original's rate", c.Warnings)
	}
	credit := issued(t, h, c.ID)
	if credit.VatTotal != 250 || *credit.Lines[0].VatRatePercent != 25 {
		t.Errorf("the credit note = VAT %v at %v %%, want the original's 250 at 25 %%", credit.VatTotal, *credit.Lines[0].VatRatePercent)
	}
}

// A credit draft's preview totals its lines as its response and its issue do,
// at the original lines' snapshot rates, after the code's rate changed: never
// at today's (D8). Not parallel: the preview hook is the package's.
func TestCredit_ThePreviewUsesTheOriginalsRates(t *testing.T) {
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	res := manager(t, h).Do(http.MethodPost, fmt.Sprintf("%s/%d/rates", vatCodesPath, vat25), map[string]any{"ratePercent": 26, "validFrom": "2026-09-13"})
	if res.Status != http.StatusCreated {
		t.Fatalf("rate change = %d %s", res.Status, res.Body)
	}
	h.Advance(24 * time.Hour)
	c := creditDraft(t, h, original.ID)

	var vat string
	defer invoices.SetPreviewRendered(func(id int64, v string) {
		if id == c.ID {
			vat = v
		}
	})()
	if res := creator(t, h).Do(http.MethodGet, previewPath(c.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, res.Body)
	}
	if vat != "250.00" || c.VatTotal != 250 {
		t.Errorf("the credit draft's VAT = preview %s, response %v; want the original's 250.00 at 25 %%, not 260 at today's 26 %%", vat, c.VatTotal)
	}
}

// A credit note is issued after its original by nature and keeps its
// delivery, so issued_late would always hold and say nothing: neither its
// draft nor the issued credit note carries it (D8).
func TestCredit_NeverWarnsIssuedLate(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	body := draftBody(customerAcme, line("A", 1, 1000, vat25))
	body["deliveryDate"] = "2026-06-01"
	original := issued(t, h, createDraft(t, h, body).ID)
	if !slices.Contains(original.Warnings, "issued_late") {
		t.Fatalf("the original's warnings = %v, want issued_late, or the fixture proves nothing", original.Warnings)
	}
	c := creditDraft(t, h, original.ID)
	if slices.Contains(c.Warnings, "issued_late") {
		t.Errorf("the credit draft's warnings = %v, want no issued_late", c.Warnings)
	}
	if credit := issued(t, h, c.ID); slices.Contains(credit.Warnings, "issued_late") {
		t.Errorf("the credit note's warnings = %v, want no issued_late", credit.Warnings)
	}
}

// A credit draft over both caps warns of both; the issue refuses with the
// line's (D8).
func TestCredit_BothCapsWarn(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 1000, vat25))).ID)
	first, second := creditDraft(t, h, original.ID), creditDraft(t, h, original.ID)
	issued(t, h, first.ID)
	if got := getInvoice(t, h, second.ID); !slices.Contains(got.Warnings, "credit_exceeds_line") || !slices.Contains(got.Warnings, "credit_exceeds_invoice") {
		t.Errorf("warnings = %v, want credit_exceeds_line and credit_exceeds_invoice", got.Warnings)
	}
	refusedWith(t, h, second.ID, "", "credit_exceeds_line")
}

// creditUnits makes a credit-note draft of the original's first line at
// units of its quantity, saved.
func creditUnits(t *testing.T, h *harness, original int64, units float64) invoiceJSON {
	t.Helper()
	c := creditDraft(t, h, original)
	l := creditLine(c.Lines[0])
	l["quantity"] = units
	return saveCredit(t, h, c, creditBody(c, l))
}

// A full reversal credits exactly what was charged, øre for øre: 2 × 33.30 at
// 15 % charged 9.99 VAT, and each unit alone would round to 5.00. The credit
// note that credits the last of every line takes the VAT the original charged
// less what the credit notes before it reversed — 4.99 — so it fits what the
// invoice has left, and the invoice ends credited in full (D8, the ruling on
// the Task 8 review's I1).
func TestCredit_AFullReversalCreditsExactlyWhatWasCharged(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Matvare", 2, 33.30, vat15))).ID)
	if original.VatTotal != 9.99 || original.GrossTotal != 76.59 {
		t.Fatalf("the original = VAT %v gross %v, want 9.99 and 76.59, or the fixture proves nothing", original.VatTotal, original.GrossTotal)
	}

	first := issued(t, h, creditUnits(t, h, original.ID, 1).ID)
	if first.VatTotal != 5 || first.GrossTotal != 38.30 {
		t.Errorf("the first unit's credit = VAT %v gross %v, want 5.00 and 38.30", first.VatTotal, first.GrossTotal)
	}

	last := creditUnits(t, h, original.ID, 1)
	if last.VatTotal != 4.99 || last.GrossTotal != 38.29 || slices.Contains(last.Warnings, "credit_exceeds_invoice") {
		t.Errorf("the last unit's draft = VAT %v gross %v warnings %v, want 4.99, 38.29 and no cap", last.VatTotal, last.GrossTotal, last.Warnings)
	}
	credit := issued(t, h, last.ID)
	if credit.VatTotal != 4.99 || credit.GrossTotal != 38.29 || len(credit.VatSummaries) != 1 ||
		credit.VatSummaries[0].VatAmount != 4.99 || credit.VatSummaries[0].TaxableAmount != 33.30 {
		t.Errorf("the last unit's credit = VAT %v gross %v summaries %+v, want 4.99 on 33.30", credit.VatTotal, credit.GrossTotal, credit.VatSummaries)
	}
	after := getInvoice(t, h, original.ID)
	if *after.CreditedAmount != 76.59 || *after.UncreditedAmount != 0 {
		t.Errorf("the original = credited %v uncredited %v, want 76.59 and 0", *after.CreditedAmount, *after.UncreditedAmount)
	}
}

// A partial credit rounds as any document does, per rate on its own lines:
// of 3 × 33.30 at 15 % (VAT 14.99) and freight at 25 %, each food unit
// credited alone reverses 5.00 — none is the last, the freight is uncredited —
// so the three reverse 15.00, an øre more than was charged. The credit note
// that then credits the freight is the final full reversal: its 15 % row
// squares the øre on nothing taxable, and the credits sum to the invoice.
func TestCredit_APartialCreditRoundsAsAnyDocument(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Matvare", 3, 33.30, vat15), line("Frakt", 1, 100, vat25))).ID)
	if original.VatTotal != 39.99 || original.GrossTotal != 239.89 {
		t.Fatalf("the original = VAT %v gross %v, want 14.99 + 25.00 and 239.89", original.VatTotal, original.GrossTotal)
	}
	onlyFood := func() invoiceJSON {
		c := creditDraft(t, h, original.ID)
		l := creditLine(c.Lines[0])
		l["quantity"] = 1
		return issued(t, h, saveCredit(t, h, c, creditBody(c, l)).ID)
	}
	for i, c := range []invoiceJSON{onlyFood(), onlyFood(), onlyFood()} {
		if c.VatTotal != 5 || c.GrossTotal != 38.30 {
			t.Errorf("food credit %d = VAT %v gross %v, want 5.00 and 38.30: a partial credit rounds on its own lines", i+1, c.VatTotal, c.GrossTotal)
		}
	}
	last := creditDraft(t, h, original.ID)
	if len(last.Lines) != 2 {
		t.Fatalf("the last credit draft has %d lines, want both copied", len(last.Lines))
	}
	freight := saveCredit(t, h, last, creditBody(last, creditLine(last.Lines[1])))
	if freight.VatTotal != 24.99 || slices.Contains(freight.Warnings, "credit_exceeds_invoice") {
		t.Errorf("the freight draft = VAT %v warnings %v, want 24.99 and no cap", freight.VatTotal, freight.Warnings)
	}
	credit := issued(t, h, freight.ID)
	want := []summaryJSON{
		{VatCategory: "S", RatePercent: 25, SafTCode: "3", TaxableAmount: 100, VatAmount: 25, VatAmountNok: 25},
		{VatCategory: "S", RatePercent: 15, SafTCode: "31", TaxableAmount: 0, VatAmount: -0.01, VatAmountNok: -0.01},
	}
	if credit.VatTotal != 24.99 || credit.GrossTotal != 124.99 || !slices.Equal(credit.VatSummaries, want) {
		t.Errorf("the freight credit = VAT %v gross %v summaries %+v, want 24.99, 124.99 and %+v", credit.VatTotal, credit.GrossTotal, credit.VatSummaries, want)
	}
	after := getInvoice(t, h, original.ID)
	if *after.CreditedAmount != 239.89 || *after.UncreditedAmount != 0 {
		t.Errorf("the original = credited %v uncredited %v, want 239.89 and 0", *after.CreditedAmount, *after.UncreditedAmount)
	}
}
