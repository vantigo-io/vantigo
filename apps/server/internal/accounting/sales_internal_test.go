package accounting

// The write-off's arithmetic (R4 as amended by R15), to the øre, on the pure
// builders: the VAT correction is shared over what the invoice's credit notes
// leave of its VAT summaries, and recoveries are scaled cumulatively, the
// rounding on bad debt and never on VAT.

import (
	"fmt"
	"math/big"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// salesBooksForTest are books with codes 3 (25 %, VAT account 2700), 31
// (15 %, 2701) and 6 (outside the law, no VAT account).
func salesBooksForTest() salesBooks {
	vat := func(id int32) *int32 { return &id }
	return salesBooks{language: "en", taxCodes: map[string]store.TaxCodesByCodeRow{
		"3":  {ID: 3, Code: "3", Active: true, VatAccountID: vat(2700)},
		"31": {ID: 31, Code: "31", Active: true, VatAccountID: vat(2701)},
		"6":  {ID: 6, Code: "6", Active: true},
	}}
}

// lineWords is a line as "role-or-account amount[ code role base]".
func lineWords(l Line) string {
	where := l.Role
	if where == "" {
		where = fmt.Sprint(l.AccountID)
	}
	out := where + " " + money(l.Amount)
	if l.TaxCodeID != nil {
		role := l.TaxRole
		if role == "" {
			role = "base"
		}
		out += fmt.Sprintf(" %d %s", *l.TaxCodeID, role)
	}
	if l.TaxBase != nil {
		out += " on " + money(l.TaxBase)
	}
	return out
}

func linesWords(lines []Line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, lineWords(l))
	}
	return out
}

func summary(saft, rate, base, vat string) contracts.JournalVATSummary {
	return contracts.JournalVATSummary{SAFT: saft, RatePercent: rate, BaseNOK: base, VATNOK: vat}
}

// The VAT correction follows what is left of the invoice (R15): its VAT
// summaries less its credit notes', per code and rate; payments stay in
// proportion.
func TestWriteOffLines_TheVATCorrectionFollowsWhatTheCreditNotesLeave(t *testing.T) {
	invoice := &contracts.JournalDocument{ID: 1, Kind: contracts.JournalInvoice, VATSummaries: []contracts.JournalVATSummary{
		summary("3", "25.00", "1000.00", "250.00"), summary("6", "0.00", "500.00", "0.00"),
	}}
	creditFull25 := contracts.JournalDocument{ID: 2, Kind: contracts.JournalCreditNote, CreditsID: 1,
		VATSummaries: []contracts.JournalVATSummary{summary("3", "25.00", "1000.00", "250.00")}}
	creditHalf25 := contracts.JournalDocument{ID: 3, Kind: contracts.JournalCreditNote, CreditsID: 1,
		VATSummaries: []contracts.JournalVATSummary{summary("3", "25.00", "500.00", "125.00")}}
	twoRates := &contracts.JournalDocument{ID: 4, Kind: contracts.JournalInvoice, VATSummaries: []contracts.JournalVATSummary{
		summary("3", "25.00", "800.00", "200.00"), summary("31", "15.00", "200.00", "30.00"),
	}}
	creditOf31 := contracts.JournalDocument{ID: 5, Kind: contracts.JournalCreditNote, CreditsID: 4,
		VATSummaries: []contracts.JournalVATSummary{summary("31", "15.00", "100.00", "15.00")}}
	loss := &contracts.JournalWriteOff{ID: 9, Reason: contracts.WriteOffLoss, VATCorrection: true}
	for _, tc := range []struct {
		name    string
		doc     *contracts.JournalDocument
		credits []contracts.JournalDocument
		amount  string
		want    []string
	}{
		{
			// The 25 % line credited in full; the exempt rest, written off,
			// takes no VAT back.
			name: "the taxed line credited in full", doc: invoice, credits: []contracts.JournalDocument{creditFull25},
			amount: "500.00", want: []string{"bad_debt 500.00"},
		},
		{
			// Without the credit note the same 500 would take 250 × 500/1750
			// back: what the old rule did.
			name: "no credit note", doc: invoice, amount: "500.00",
			want: []string{"bad_debt 285.71 3 base", "2700 71.43 3 tax on 285.71", "bad_debt 142.86"},
		},
		{
			// Half the 25 % line credited: 625 + 500 left, 1125 open.
			name: "half the taxed line credited", doc: invoice, credits: []contracts.JournalDocument{creditHalf25},
			amount: "1125.00", want: []string{"bad_debt 500.00 3 base", "2700 125.00 3 tax on 500.00", "bad_debt 500.00"},
		},
		{
			// A payment of 300 leaves 815 open of 1115; shares in proportion
			// of 1000 and 115 gross: 730.94 and the rest, 84.06.
			name: "two rates, one credited in part, a payment in proportion", doc: twoRates,
			credits: []contracts.JournalDocument{creditOf31}, amount: "815.00",
			want: []string{"bad_debt 584.75 3 base", "2700 146.19 3 tax on 584.75", "bad_debt 73.10 31 base", "2701 10.96 31 tax on 73.10"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines, rule, err := writeOffLines(salesBooksForTest(), loss, rat(tc.amount), tc.doc, tc.credits)
			if err != nil {
				t.Fatal(err)
			}
			if rule != salesRuleWriteOffVAT {
				t.Errorf("rule = %s", rule)
			}
			if got := linesWords(lines); !slices.Equal(got, tc.want) {
				t.Errorf("lines = %q, want %q", got, tc.want)
			}
			sum := new(big.Rat)
			for _, l := range lines {
				sum.Add(sum, l.Amount)
			}
			if sum.Cmp(rat(tc.amount)) != 0 {
				t.Errorf("the lines sum to %s, want %s", money(sum), tc.amount)
			}
		})
	}
}

// Recoveries are scaled cumulatively (R15): each is what the write-off's
// lines come to with it, less what they came to before it, so several part
// payments add up to exactly what was written off; what rounding leaves lands
// on bad debt, never on VAT.
func TestScaleRecovery_PartPaymentsAddUpToTheWriteOff(t *testing.T) {
	vat3 := int32(3)
	vatOnly := []Line{
		{Role: roleBadDebt, Amount: rat("80.00"), TaxCodeID: &vat3, TaxRole: "base"},
		{AccountID: 2700, Amount: rat("20.00"), TaxCodeID: &vat3, TaxRole: "tax", TaxBase: rat("80.00")},
	}
	mixed := []Line{
		{Role: roleBadDebt, Amount: rat("40.00"), TaxCodeID: &vat3, TaxRole: "base"},
		{AccountID: 2700, Amount: rat("10.00"), TaxCodeID: &vat3, TaxRole: "tax", TaxBase: rat("40.00")},
		{Role: roleBadDebt, Amount: rat("50.00")},
	}
	for _, tc := range []struct {
		name     string
		lines    []Line
		payments []string
		want     [][]string
	}{
		{
			name: "33.33 three times and 0.01, all on taxed lines", lines: vatOnly,
			payments: []string{"33.33", "33.33", "33.33", "0.01"},
			want: [][]string{
				{"bad_debt -26.66 3 base", "2700 -6.67 3 tax on -26.66"},
				{"bad_debt -26.67 3 base", "2700 -6.66 3 tax on -26.67"},
				{"bad_debt -26.66 3 base", "2700 -6.67 3 tax on -26.66"},
				{"bad_debt -0.01 3 base", "2700 0.00 3 tax on -0.01"},
			},
		},
		{
			// At 99.99 of 100 the shares come to 40.00, 10.00 and 50.00
			// (49.995 rounds up): 33.34 for a recovery of 33.33, and the
			// øre comes off the bad debt line with no code, never the VAT;
			// the last 0.01 is that line's alone.
			name: "33.33 three times and 0.01, a plain bad debt line", lines: mixed,
			payments: []string{"33.33", "33.33", "33.33", "0.01"},
			want: [][]string{
				{"bad_debt -13.33 3 base", "2700 -3.33 3 tax on -13.33", "bad_debt -16.67"},
				{"bad_debt -13.33 3 base", "2700 -3.34 3 tax on -13.33", "bad_debt -16.66"},
				{"bad_debt -13.34 3 base", "2700 -3.33 3 tax on -13.34", "bad_debt -16.66"},
				{"bad_debt 0.00 3 base", "2700 0.00 3 tax on 0.00", "bad_debt -0.01"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			written := new(big.Rat)
			for _, l := range tc.lines {
				written.Add(written, l.Amount)
			}
			before := new(big.Rat)
			totals := make([]*big.Rat, len(tc.lines)+1)
			for i := range totals {
				totals[i] = new(big.Rat)
			}
			for step, p := range tc.payments {
				got := scaleRecovery(tc.lines, before, rat(p), written)
				if words := linesWords(got); !slices.Equal(words, tc.want[step]) {
					t.Errorf("recovery %d of %s = %q, want %q", step+1, p, words, tc.want[step])
				}
				sum := new(big.Rat)
				for i, l := range got {
					sum.Add(sum, l.Amount)
					if i < len(totals) {
						totals[i].Add(totals[i], l.Amount)
					}
				}
				if sum.Cmp(new(big.Rat).Neg(rat(p))) != 0 {
					t.Errorf("recovery %d's lines sum to %s, want -%s", step+1, money(sum), p)
				}
				before.Add(before, rat(p))
			}
			for i, l := range tc.lines {
				if totals[i].Cmp(new(big.Rat).Neg(l.Amount)) != 0 {
					t.Errorf("line %d (%s) recovered %s in all, want all of it", i+1, lineWords(l), money(totals[i]))
				}
			}
		})
	}
}
