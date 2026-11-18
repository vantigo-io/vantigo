package invoices

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// A partial clear never takes more than is open in NOK (review minor 1):
// JPY's rows may round the NOK receivable below what an amount is worth.
func TestClearedNOK_IsClampedToTheNOKOpen(t *testing.T) {
	got := clearedNOK(big.NewRat(60414, 1000000), big.NewRat(99990, 1), big.NewRat(10, 1), big.NewRat(604070, 100))
	if got.FloatString(2) != "6040.70" {
		t.Errorf("cleared = %s, want the 6040.70 open in NOK", got.FloatString(2))
	}
	if got := clearedNOK(big.NewRat(115, 10), big.NewRat(100, 1), big.NewRat(900, 1), big.NewRat(11500, 1)); got.FloatString(2) != "1150.00" {
		t.Errorf("an ordinary partial clear = %s, want 1150.00", got.FloatString(2))
	}
}

// Partial reversals of a movement in currency round cumulatively (review
// minor 3): three thirds of EUR 300.00 worth NOK 3 451.00 reach 0 in both.
func TestNOKOfCredit_ReachesZeroInBoth(t *testing.T) {
	amount, _ := numericFromRat(big.NewRat(300, 1), 2)
	nok, _ := numericFromRat(big.NewRat(3451, 1), 2)
	c := store.InvoicesCustomerCredit{Currency: "EUR", Amount: amount, AmountNok: nok}
	done, total := new(big.Rat), new(big.Rat)
	for range 3 {
		back := big.NewRat(-100, 1)
		n := nokOfCredit(c, back, done)
		r, _ := ratFromNumeric(n)
		total.Add(total, r)
		done.Add(done, back)
	}
	if total.FloatString(2) != "-3451.00" {
		t.Errorf("the reversals' NOK = %s, want -3451.00", total.FloatString(2))
	}
}

// A dismissed line queued currency_differs is the customer's money for an
// invoice it named (review minor 5).
func TestOwedBack_CurrencyDiffers(t *testing.T) {
	reason := reasonCurrencyDiffers
	if !owedBack(&reason) {
		t.Error("a dismissed currency_differs line is not owed back")
	}
}
