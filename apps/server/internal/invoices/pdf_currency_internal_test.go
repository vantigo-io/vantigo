package invoices

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
	"time"
)

// aEuroInvoice is anInvoice in EUR at 11.7234 with two VAT rates (invoices
// phase 6 design I26): each VAT row's base and VAT in NOK as the issue
// snapshots them.
func aEuroInvoice() pdfDocument {
	d := anInvoice()
	d.currency, d.exchangeRate, d.rateDate, d.rateSource = "EUR", rat("11.7234"), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), rateSourceNorgesBank
	d.lines = []pdfLine{
		{description: "Konsulenttime", unit: "timer", quantity: rat("10"), unitPrice: rat("120"), discount: rat("0"), rate: rat("25"), net: rat("1200")},
		{description: "Servering", unit: "stk", quantity: rat("4"), unitPrice: rat("25"), discount: rat("0"), rate: rat("15"), net: rat("100")},
	}
	d.summaries = []vatSummary{
		{Category: "S", Rate: rat("25"), SAFT: "3", Taxable: rat("1200"), VAT: rat("300"), BaseNOK: rat("14068.08"), VATNOK: rat("3517.02")},
		{Category: "S", Rate: rat("15"), SAFT: "31", Taxable: rat("100"), VAT: rat("15"), BaseNOK: rat("1172.34"), VATNOK: rat("175.85")},
	}
	d.totals = documentTotals{Net: rat("1300"), VAT: rat("315"), Gross: rat("1615")}
	return d
}

// Off NOK the page prints the rate and the VAT in NOK per rate and in total
// (bokføringsforskriften § 5-1-1 første ledd nr. 6), leads the payment block
// with the IBAN and the BIC, and keeps the currency on every amount; a NOK
// document prints none of it (its pinned hash is unchanged).
func TestPDFModel_InCurrency(t *testing.T) {
	t.Parallel()
	m := buildPDFModel(aEuroInvoice())
	if m.rateLine != "Kurs EUR/NOK 11,7234, Norges Bank 11.09.2026" {
		t.Errorf("rate line = %q", m.rateLine)
	}
	want := [][]string{{"S 25 %", "14\u00a0068,08", "3\u00a0517,02"}, {"S 15 %", "1\u00a0172,34", "175,85"}, {"Sum", "15\u00a0240,42", "3\u00a0692,87"}}
	if len(m.nokRows) != len(want) {
		t.Fatalf("NOK rows = %q, want %q", m.nokRows, want)
	}
	for i := range want {
		if !slices.Equal(m.nokRows[i], want[i]) {
			t.Errorf("NOK row %d = %q, want %q", i, m.nokRows[i], want[i])
		}
	}
	if m.payment[0][0] != "IBAN" || m.payment[1][0] != "BIC" || m.payment[2][0] != "Kontonummer" {
		t.Errorf("payment block = %q, want the IBAN and the BIC first", m.payment)
	}
	if m.totals[2][1] != "EUR 1\u00a0615,00" || m.vatHeader[2] != "MVA (EUR)" {
		t.Errorf("totals %q, VAT header %q; want the currency on them", m.totals, m.vatHeader)
	}
	agreed := aEuroInvoice()
	agreed.rateSource = rateSourceManual
	if got := buildPDFModel(agreed).rateLine; got != "Kurs EUR/NOK 11,7234, avtalt kurs" {
		t.Errorf("an agreed rate's line = %q", got)
	}
	if nok := buildPDFModel(anInvoice()); nok.rateLine != "" || len(nok.nokRows) != 0 {
		t.Errorf("a NOK document prints a rate block: %q %q", nok.rateLine, nok.nokRows)
	}
}

// The EUR fixture's render, pinned as the NOK one is.
func TestRenderPDF_InCurrencyIsPinned(t *testing.T) {
	t.Parallel()
	body, err := renderPDF(buildPDFModel(aEuroInvoice()))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != pinnedRenderSHA256EUR {
		t.Errorf("the EUR render's SHA-256 = %s, want %s", got, pinnedRenderSHA256EUR)
	}
}

const pinnedRenderSHA256EUR = "b0dc03fb97a2c978646f752fbd8434e4596d9b364ba0497b997e95d45a1070f1"
