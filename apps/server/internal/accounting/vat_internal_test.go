package accounting

import (
	"testing"
	"time"

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
		{store.VatPeriodGroupsRow{TaxCodeID: 1, TaxRole: "tax_reverse", TaxDirection: "input"}, partCalculated},
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
