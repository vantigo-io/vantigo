package accounting

// Invoices in another currency on the sales vouchers (invoices phase 6
// delivery C, its review): a recovery scales on what it cleared of the
// write-off's NOK, a closing credit note's NOK rest posts to the currency
// roles, and every receivables line of a movement in currency carries its
// currency triple.

import (
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// euroInvoiceDoc is an invoice of EUR 1 000.00 at 11.5: NOK 9 200 at 25 %
// and its VAT of 2 300.
func euroInvoiceDoc() *contracts.JournalDocument {
	return &contracts.JournalDocument{ID: 1, Kind: contracts.JournalInvoice, VATSummaries: []contracts.JournalVATSummary{
		summary("3", "25.00", "9200.00", "2300.00"),
	}}
}

func voucherSum(t *testing.T, v *Voucher) {
	t.Helper()
	sum := new(big.Rat)
	for _, l := range v.Lines {
		sum.Add(sum, l.Amount)
	}
	if sum.Sign() != 0 {
		t.Errorf("the voucher %s does not balance: %q", v.Rule, linesWords(v.Lines))
	}
}

// Recoveries at a moved rate give back no more VAT than tap på krav took
// (review M2): EUR 400 arriving as NOK 5 000 clears 4 600 of the 11 500
// written off, a gain of 400; the last EUR 600 arriving as NOK 6 600 clears
// the 6 900 left, a loss of 300. The VAT given back is 2 300, exactly.
func TestRecovery_InCurrencyScalesOnWhatItCleared(t *testing.T) {
	b := salesBooksForTest()
	doc := euroInvoiceDoc()
	given := new(big.Rat)
	for _, r := range []struct {
		arrived, difference, before string
		want                        []string
	}{
		{"5000.00", "400.00", "0.00", []string{"bank 5000.00", "bad_debt -3680.00 3 base", "2700 -920.00 3 tax on -3680.00", "currency_gain -400.00"}},
		{"6600.00", "-300.00", "4600.00", []string{"bank 6600.00", "bad_debt -5520.00 3 base", "2700 -1380.00 3 tax on -5520.00", "currency_loss 300.00"}},
	} {
		f := contracts.JournalFact{Kind: contracts.FactRecoveredPayment, Date: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
			InvoiceID: 1, InvoiceNumber: 10001, CustomerID: 7, AmountNOK: r.arrived, Currency: "EUR", ExchangeDifferenceNOK: r.difference,
			WriteOff: &contracts.JournalWriteOff{ID: 9, Reason: contracts.WriteOffLoss, VATCorrection: true,
				AmountNOK: "11500.00", RecoveredBeforeNOK: r.before}}
		v, err := factVoucher(b, f, factInputs{invoice: doc})
		if err != nil {
			t.Fatal(err)
		}
		if got := linesWords(v.Lines); !slices.Equal(got, r.want) {
			t.Errorf("the recovery of NOK %s = %q, want %q", r.arrived, got, r.want)
		}
		voucherSum(t, v)
		for _, l := range v.Lines {
			if l.TaxRole == "tax" {
				given.Sub(given, l.Amount)
			}
		}
	}
	if given.Cmp(rat("2300.00")) != 0 {
		t.Errorf("the VAT given back = %s, want the 2300.00 the write-off took", money(given))
	}
}

// A closing credit note's NOK rest (review M1): a loss of 0.01 clears the
// invoice's item to currency loss; a gain to currency gain.
func TestExchangeDifference_PostsToTheCurrencyRoles(t *testing.T) {
	for _, tc := range []struct {
		difference string
		want       []string
	}{
		{"-0.01", []string{"receivables -0.01", "currency_loss 0.01"}},
		{"0.02", []string{"receivables 0.02", "currency_gain -0.02"}},
	} {
		f := contracts.JournalFact{Kind: contracts.FactExchangeDifference, InvoiceID: 1, InvoiceNumber: 10001, CustomerID: 7,
			AmountNOK: "0.01", Currency: "EUR", ExchangeDifferenceNOK: tc.difference}
		v, err := factVoucher(salesBooksForTest(), f, factInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if got := linesWords(v.Lines); !slices.Equal(got, tc.want) || v.Rule != salesRuleExchangeDifference {
			t.Errorf("the difference %s = %s %q, want %q", tc.difference, v.Rule, got, tc.want)
		}
	}
}

// Every receivables line of a movement in currency carries its currency
// triple (review minor 4): the write-off's (a rounding's too), the credit
// note's surplus moved and the credit applied.
func TestReceivables_InCurrencyCarryTheTriple(t *testing.T) {
	b := salesBooksForTest()
	triple := func(l Line) string {
		if l.CurrencyAmount == nil {
			return l.Role + " none"
		}
		return l.Role + " " + l.Currency + " " + money(l.CurrencyAmount) + " @ " + l.ExchangeRate.FloatString(6)
	}
	for _, tc := range []struct {
		name string
		f    contracts.JournalFact
		want string
	}{
		{"a write-off", contracts.JournalFact{Kind: contracts.FactWriteOff, InvoiceID: 1, AmountNOK: "11500.00", Currency: "EUR", Amount: "1000.00",
			WriteOff: &contracts.JournalWriteOff{ID: 9, Reason: contracts.WriteOffLoss, AmountNOK: "11500.00"}},
			"receivables EUR -1000.00 @ 11.500000"},
		{"a rounding", contracts.JournalFact{Kind: contracts.FactWriteOff, InvoiceID: 1, AmountNOK: "0.12", Currency: "EUR", Amount: "0.01",
			WriteOff: &contracts.JournalWriteOff{ID: 9, Reason: contracts.WriteOffRounding, AmountNOK: "0.12"}},
			"receivables EUR -0.01 @ 12.000000"},
		{"a credit note moved", contracts.JournalFact{Kind: contracts.FactCredit, InvoiceID: 1, AmountNOK: "115.00",
			Credit: &contracts.JournalCredit{Kind: contracts.CreditNoteMoved, Currency: "EUR", Amount: "10.00", AmountNOK: "115.00"}},
			"receivables EUR 10.00 @ 11.500000"},
		{"credit applied", contracts.JournalFact{Kind: contracts.FactCredit, InvoiceID: 1, AmountNOK: "115.00",
			Credit: &contracts.JournalCredit{Kind: contracts.CreditApplied, Currency: "EUR", Amount: "10.00", AmountNOK: "115.00"}},
			"receivables EUR -10.00 @ 11.500000"},
	} {
		v, err := factVoucher(b, tc.f, factInputs{invoice: euroInvoiceDoc()})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var got []string
		for _, l := range v.Lines {
			if l.Item != nil && !l.Item.CustomerCredit {
				got = append(got, triple(l))
			}
		}
		if !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: the invoice's receivables = %q, want %q", tc.name, got, tc.want)
		}
		voucherSum(t, v)
	}
}
