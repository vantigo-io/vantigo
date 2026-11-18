package invoices_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// eFaktura and AvtaleGiro carry NOK only (phase 6 D beside C): an invoice in
// euro to a person eFaktura would reach is refused currency_not_nok, and
// nothing is shipped.
func TestCurrency_AForeignInvoiceIsNeverSentAsEfaktura(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	h.rates.SetRange("EUR", "2026-08-01", "2026-12-31", "11.5")
	settings := withCurrencies(t, h, false, "EUR")
	settings["kidLength"], settings["kidAlgorithm"] = 10, kid.Mod10
	saveSeller(t, h, settings)
	body := draftBody(customerPerson, line("Rådgivning", 1, 1000, vat25))
	body["currency"] = "EUR"
	inv := issued(t, h, createDraft(t, h, body).ID)
	if inv.Currency != "EUR" {
		t.Fatalf("the invoice is in %s, want EUR", inv.Currency)
	}
	if res := issuer(t, h).Do(http.MethodPost, sendEfakturaPath(inv.ID), nil); res.Status != http.StatusConflict ||
		!strings.Contains(string(res.Body), "currency_not_nok") {
		t.Errorf("send-efaktura of an invoice in EUR = %d %s, want 409 currency_not_nok", res.Status, res.Body)
	}
	runEfakturaCycle(t, efakturaWorker(t, h))
	if shipped := h.mps.Shipments(); len(shipped) != 0 {
		t.Errorf("the fake holds %+v, want nothing shipped", shipped)
	}
}

// euroInvoiceAt is an issued invoice of EUR 100.00 (80 + 25 % VAT) to the EUR
// customer at the agreed rate.
func euroInvoiceAt(t *testing.T, h *harness, rate float64) invoiceJSON {
	t.Helper()
	body := draftBody(customerEuro, line("Konsulenttime", 1, 80, vat25))
	body["exchangeRate"] = rate
	return issued(t, h, createDraft(t, h, body).ID)
}

// creditHalf issues a credit note of EUR 50.00 of inv's one line: its price
// lowered to 40.
func creditHalf(t *testing.T, h *harness, inv invoiceJSON) {
	t.Helper()
	c := creditDraft(t, h, inv.ID)
	l := creditLine(c.Lines[0])
	l["unitPrice"] = 40
	issued(t, h, saveCredit(t, h, c, creditBody(c, l)).ID)
}

// A credit note that closes an invoice in another currency leaves no NOK
// (review M1): EUR 100 at 11.12345 is NOK 1 112.35; EUR 50 paid clears
// 556.17 and the credit note of EUR 50 is 556.17 too, so 0.01 is left in NOK
// with nothing left in EUR — written as an exchange difference, a loss, and
// open_nok() reaches 0. With the payment last it clears the rest itself.
func TestCurrency_AClosingCreditNoteLeavesNoNOK(t *testing.T) {
	t.Parallel()
	for _, paymentLast := range []bool{false, true} {
		h := readyToIssue(t)
		euroOn(t, h, false)
		inv := euroInvoiceAt(t, h, 11.12345)
		payment := pay(50, "2026-09-12")
		payment["amountNok"] = 556.17
		register := func() {
			if res := payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), payment); res.Status != http.StatusOK {
				t.Fatalf("the payment = %d %s", res.Status, res.Body)
			}
		}
		if !paymentLast {
			register()
		}
		creditHalf(t, h, inv)
		if paymentLast {
			register()
		}
		if got := openNOK(t, h, inv.ID); got != "0.00" {
			t.Errorf("payment last %v: open in NOK = %s, want 0.00", paymentLast, got)
		}
		differences := texts(t, h, `SELECT amount_nok::text FROM invoices.exchange_differences WHERE invoice_id = $1`, inv.ID)
		switch {
		case !paymentLast && !slices.Equal(differences, []string{"0.01"}):
			t.Errorf("the credit note closing: differences %v, want the 0.01 rest", differences)
		case paymentLast && len(differences) != 0:
			t.Errorf("the payment closing: differences %v, want none — the payment clears the rest", differences)
		}
		if paymentLast {
			if got := texts(t, h, `SELECT exchange_difference_nok::text FROM invoices.payments WHERE invoice_id = $1`, inv.ID); !slices.Equal(got, []string{"-0.01"}) {
				t.Errorf("the closing payment's difference = %v, want -0.01", got)
			}
		}
	}
}

// A recovery of a written-off invoice in currency clears its share of the
// write-off's NOK, cumulatively, the last taking the rest (review M2): EUR
// 1000 at 11.5 written off is NOK 11 500; EUR 400 recovered at a moved rate
// (NOK 5 000) clears 4 600 with a gain of 400; the last EUR 600 (NOK 6 600)
// clears the 6 900 left with a loss of 300 — never more than was written off.
func TestCurrency_ARecoveryClearsItsShareOfTheWriteOff(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	inv := euroInvoice(t, h)
	c := payer(t, h)
	if res := c.Do(http.MethodPost, writeOffPath(inv.ID), map[string]any{"writtenOffOn": "2026-09-12", "reason": "loss", "vatCorrection": true}); res.Status != http.StatusOK {
		t.Fatalf("the write-off = %d %s", res.Status, res.Body)
	}
	for _, r := range []struct{ amount, nok float64 }{{400, 5000}, {600, 6600}} {
		body := map[string]any{"amount": r.amount, "amountNok": r.nok, "paidOn": "2026-09-12", "kind": "recovered"}
		if res := c.Do(http.MethodPost, paymentsPath(inv.ID), body); res.Status != http.StatusOK {
			t.Fatalf("a recovery = %d %s", res.Status, res.Body)
		}
	}
	got := texts(t, h, `SELECT (amount_nok - exchange_difference_nok)::text || ' ' || exchange_difference_nok::text
		FROM invoices.payments WHERE invoice_id = $1 AND kind = 'recovered' ORDER BY id`, inv.ID)
	if !slices.Equal(got, []string{"4600.00 400.00", "6900.00 -300.00"}) {
		t.Errorf("the recoveries = %v, want 4600 cleared with a gain of 400, then the 6900 left with a loss of 300", got)
	}
}

// A day's rate a manager entered issues as manual (review M3), and a draft
// keeps its agreed rate over a PUT that leaves it out — only
// useNorgesBankRate drops it (review minor 6).
func TestCurrency_TheSourceAndTheAgreedRateKept(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	m := h.SignIn(t, "invoices:access", "invoices:manage")
	if res := m.Do(http.MethodPut, ratesPath+"/EUR/2026-09-11", map[string]any{"rate": 11.7}); res.Status != http.StatusOK {
		t.Fatal(res.Status)
	}
	inv := issued(t, h, createDraft(t, h, draftBody(customerEuro, line("Time", 1, 100, vat25))).ID)
	if got := modtest1(t, h, `SELECT exchange_rate_source FROM invoices.invoices WHERE id = $1`, inv.ID); got != "manual" {
		t.Errorf("issued at a day's manual rate with source %s, want manual", got)
	}

	body := draftBody(customerEuro, line("Time", 1, 100, vat25))
	body["exchangeRate"] = 11.1
	d := createDraft(t, h, body)
	put := draftBody(customerEuro, line("Time", 2, 100, vat25))
	put["revision"], put["paymentTermsDays"] = d.Revision, 14
	kept := putDoc(t, creator(t, h), d.ID, put)
	if kept.ExchangeRate != 11.1 {
		t.Errorf("a PUT without exchangeRate = %v, want the agreed 11.1 kept", kept.ExchangeRate)
	}
	put["revision"], put["useNorgesBankRate"] = kept.Revision, true
	if dropped := putDoc(t, creator(t, h), d.ID, put); dropped.ExchangeRate == 11.1 {
		t.Errorf("useNorgesBankRate kept the agreed rate")
	}
}

// Removing a manual rate fetches Norges Bank's of the day again (review
// minor 7), and a rate of VND keeps its ten decimals (minor 2).
func TestCurrency_RemovingAManualRateRefetchesTheDay(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	m := h.SignIn(t, "invoices:access", "invoices:manage")
	if res := m.Do(http.MethodPut, ratesPath+"/EUR/2026-09-10", map[string]any{"rate": 12}); res.Status != http.StatusOK {
		t.Fatal(res.Status)
	}
	if res := m.Do(http.MethodDelete, ratesPath+"/EUR/2026-09-10", nil); res.Status != http.StatusNoContent {
		t.Fatalf("the delete = %d", res.Status)
	}
	if got := modtest1(t, h, `SELECT rate::text || ' ' || source FROM invoices.exchange_rates WHERE currency = 'EUR' AND rate_date = '2026-09-10'`); got != "11.5000000000 norges_bank" {
		t.Errorf("the day after the delete = %s, want Norges Bank's again", got)
	}
	if res := m.Do(http.MethodPut, ratesPath+"/VND/2026-09-10", map[string]any{"rate": 0.0004031234}); res.Status != http.StatusOK {
		t.Fatalf("a VND rate = %d %s", res.Status, res.Body)
	}
	if got := modtest1(t, h, `SELECT rate::text FROM invoices.exchange_rates WHERE currency = 'VND'`); got != "0.0004031234" {
		t.Errorf("the VND rate = %s, want its ten decimals", got)
	}
}
