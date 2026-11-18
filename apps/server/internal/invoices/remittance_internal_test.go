package invoices

import (
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file tests the token rule of the automatic split (invoices phase 6
// design I14): what a payer's text names as invoice numbers, and what it
// does not — amounts, dates, account numbers, a KID's digits, an amount
// after a currency code.

func TestWholeNumbers_TheTokenRule(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text string
		want []int64
	}{
		{"Fakt 1001/1002", []int64{1001, 1002}},
		{"Faktura #1001; 1002: takk", []int64{1001, 1002}},
		{"Faktura 1001,1002", []int64{1001, 1002}}, // a list: the run after the comma is four digits
		{"fakt nr 1001,1002,1003", []int64{1001, 1002, 1003}},
		{"1001.1002", []int64{1001, 1002}},
		{"1001, 1002", []int64{1001, 1002}},
		{"inv 1001+1002", []int64{1001, 1002}},
		{"1001&1002", []int64{1001, 1002}},
		{"1001-1002", []int64{1001, 1002}}, // both ends, never the range
		{"1001-1005", []int64{1001, 1005}},
		{"12-34", nil},
		{"(1001) (1002)", []int64{1001, 1002}},
		{"Betaling faktura 1001 og 1002.", []int64{1001, 1002}},
		{"faktura 1001.", []int64{1001}},
		{"faktura 1001-", []int64{1001}},
		{"Faktura 10023 450,00", []int64{10023}},
		{"Faktura 10023 1 450,00", []int64{10023}},
		{"3000 kr", nil},
		{"betalt 3000 NOK for 1001", []int64{1001}},
		{"kr 1 250,50 for 1001", []int64{1001}},
		{"1 250,00", nil},
		{"12.500,- for 3", []int64{3}},
		{"Betalt 1250,00", nil},
		{"1250,-", nil},
		{"1250.-", nil},
		{"1001.50", nil},
		{"1001,5", nil},
		{"17.11.2026", nil},
		{"17/11/2026", nil},
		{"17/11/26 faktura 1001", []int64{1001}},
		{"2026-11-17", nil},
		{"fra konto 1234 56 78903", nil},
		{"fra konto 1234.56.78903", nil},
		{"KID 00010017", nil},
		{"NOK 1001", nil},
		{"nok1001", nil},
		{"kr. 1001", nil},
		{"EUR 1001 og 1002", []int64{1002}},
		{"x1001 1001y 10011", []int64{10011}},
		{"", nil},
	} {
		if got := wholeNumbers(c.text); !slices.Equal(got, c.want) {
			t.Errorf("wholeNumbers(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestReferencesOf_KidsAndTheSeries(t *testing.T) {
	t.Parallel()
	k, err := kid.Compute(1003, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	// A KID-shaped token whose check character verifies under neither.
	other := ""
	for c := byte('0'); c <= '9'; c++ {
		if c != kid.CheckMod10("000100") && c != kid.CheckMod11("000100") {
			other = "000100" + string(c)
			break
		}
	}
	line := store.InvoicesBankTransaction{
		RemittanceRefs: []string{"1001", "Faktura 1002", k},
		RemittanceText: "1004 og 999 og 1 250,00 og 5000 " + other,
	}
	got := referencesOf(line, invoiceSeries{first: 1000, next: 1010})
	if !slices.Equal(got.numbers, []int64{1001, 1002, 1004}) {
		t.Errorf("the numbers = %v, want 1001, 1002 and 1004: 999 and 5000 are outside the series", got.numbers)
	}
	if !slices.Contains(got.kids, k) || slices.Contains(got.kids, other) {
		t.Errorf("the KIDs = %v, want %s and never %s", got.kids, k, other)
	}
	if got := referencesOf(line, invoiceSeries{first: 1, next: 1}); len(got.numbers) != 0 {
		t.Errorf("with nothing issued the numbers = %v, want none", got.numbers)
	}
}
