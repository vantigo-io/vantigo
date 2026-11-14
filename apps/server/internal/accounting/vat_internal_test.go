package accounting

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/mva"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

func date(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

// The terms derived from the settings (A22): two-month terms by the calendar
// whatever the fiscal year, from the term that holds books_start — a start in
// the middle of a term starts with that term — and one yearly term a
// calendar year; none without VAT.
func TestTermsThrough(t *testing.T) {
	for _, tc := range []struct {
		kind, start, through string
		want                 []string
	}{
		{"bimonthly", "2026-03-15", "2026-09-12", []string{"2026-2 2026-03-01 2026-04-30", "2026-3 2026-05-01 2026-06-30",
			"2026-4 2026-07-01 2026-08-31", "2026-5 2026-09-01 2026-10-31"}},
		{"bimonthly", "2025-12-01", "2026-01-01", []string{"2025-6 2025-11-01 2025-12-31", "2026-1 2026-01-01 2026-02-28"}},
		{"bimonthly", "2028-02-29", "2028-03-01", []string{"2028-1 2028-01-01 2028-02-29", "2028-2 2028-03-01 2028-04-30"}},
		{"yearly", "2025-08-15", "2027-01-01", []string{"2025-1 2025-01-01 2025-12-31", "2026-1 2026-01-01 2026-12-31",
			"2027-1 2027-01-01 2027-12-31"}},
		{"none", "2026-01-01", "2026-12-31", nil},
	} {
		var got []string
		for _, term := range termsThrough(tc.kind, date(tc.start), date(tc.through)) {
			got = append(got, term.label()+" "+term.Start.Format(time.DateOnly)+" "+term.End.Format(time.DateOnly))
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s from %s = %v, want %v", tc.kind, tc.start, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s from %s: term %d = %s, want %s", tc.kind, tc.start, i, got[i], tc.want[i])
			}
		}
	}
	// A term is looked up by year and number, never before the books start.
	settings := store.AccountingSetting{VatTerm: "bimonthly", BooksStart: date("2026-03-15"), FiscalYearStartMonth: 7}
	if _, ok := lookupTerm(settings, 2026, 1); ok {
		t.Error("term 2026-1 is before the books")
	}
	if term, ok := lookupTerm(settings, 2026, 2); !ok || term.Start != date("2026-03-01") || term.due() != date("2026-06-10") {
		t.Errorf("term 2026-2 = %+v %v", term, ok)
	}
	for _, n := range []int32{0, 7} {
		if _, ok := lookupTerm(settings, 2026, n); ok {
			t.Errorf("term %d exists", n)
		}
	}
	settings.VatTerm = "yearly"
	if _, ok := lookupTerm(settings, 2026, 2); ok {
		t.Error("a yearly term 2")
	}
}

// vatPart is the drill-down's CASE in Go: every combination the postings can
// carry lands where the SQL puts it.
func TestVatPart(t *testing.T) {
	for _, tc := range []struct {
		row  store.VatPeriodGroupsRow
		want string
	}{
		{store.VatPeriodGroupsRow{}, partUntaxed},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "base", TaxSpecification: "tapPåKrav"}, partLossBase},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax", TaxSpecification: "tapPåKrav"}, partLoss},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "base", TaxDirection: "output"}, partBase},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax_reverse", TaxDirection: "input", ReverseCharge: true}, partCalculated},
		// A calculated side on a code that is not reverse charge is no
		// figure: it stays unreconciled on its account (M1).
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax_reverse", TaxDirection: "input"}, partOther},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax", TaxDirection: "output"}, partOutput},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax", TaxDirection: "input", OnVatAccount: true}, partDeducted},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax", TaxDirection: "input"}, partNotDeducted},
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax", TaxDirection: "none"}, partOther},
	} {
		if got := vatPart(tc.row); got != tc.want {
			t.Errorf("vatPart(%+v) = %s, want %s", tc.row, got, tc.want)
		}
	}
}

// The close's guard against a return that does not add up to the ledger
// (M1): the VAT counted is the return's exact total turned, and the øre cut
// are less than a krone a line; and a reported code with an output side but
// no rate is named (L3).
func TestSpecificationConsistentAndRates(t *testing.T) {
	r := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	spec := &specification{counted: r("-250.25"), ret: mva.Build([]mva.Figure{{Code: "3", Direction: "output", RatePercent: "25.00",
		Base: r("1001"), Output: r("250.25")}})}
	if err := spec.consistent(); err != nil {
		t.Errorf("a consistent return: %v", err)
	}
	spec.counted = r("-250.26")
	if err := spec.consistent(); err == nil || !strings.Contains(err.Error(), "exact total") {
		t.Errorf("counted VAT off the return = %v", err)
	}
	spec = &specification{counted: r("-2.99"), ret: mva.Return{Total: big.NewInt(1), ExactTotal: r("2.99"), Considered: 1}}
	if err := spec.consistent(); err == nil || !strings.Contains(err.Error(), "rounding difference") {
		t.Errorf("a rounding difference of 1.99 on one line = %v", err)
	}
	spec = &specification{figures: []*figure{{Figure: mva.Figure{Code: "3", Direction: "output", Base: r("100"), Output: r("0"),
		Calculated: r("0")}, reported: true}}}
	if got := spec.rateMissing(); got != "3" {
		t.Errorf("rateMissing = %q, want 3", got)
	}
	spec.figures[0].RatePercent = "25.00"
	if got := spec.rateMissing(); got != "" {
		t.Errorf("rateMissing with a rate = %q", got)
	}
}
