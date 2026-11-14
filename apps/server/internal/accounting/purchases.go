package accounting

// What a supplier invoice posts as (A18 as amended by R8): the pure builder
// the posting, the preview of each line on the form and the tests share.
// Every amount is exact; a line's VAT is computed per line with docmath's
// rounding, as a manual journal's is (A6), and converted to NOK at the
// invoice's rate posting by posting.
//
// The tax code decides the VAT, by its direction and flags as the books
// seeded them (A5):
//
//   - an input code with a VAT account (1, 11, 12, 13; the import VAT paid,
//     14 and 15): the invoice's VAT is debited to the code's VAT account;
//   - an input code without one: the VAT is not deductible and is debited to
//     the line's own account, on the cost;
//   - a reverse-charge code (81–89, 91, 92): the invoice carries no VAT; the
//     buyer calculates it on the NOK base at the code's rate and posts both
//     sides — the deductible side on the code's VAT account, or, for the
//     codes without deduction (82, 84, 87, 89, 92), on the line's own account,
//     and the calculated side on its counter account — so the payable is the
//     net. 85 has a rate of nothing and posts its base alone;
//   - a code of no VAT treatment (0, 20, 21, 22): the base alone, with the
//     code, no VAT;
//   - an output code is a sale's: refused (tax_code_not_purchase).
//
// Import of goods proper is not a supplier invoice's: its VAT base is the
// customs declaration's, so it is a manual journal with the import codes
// (R8), and the user guide shows the entry.

import (
	"fmt"
	"math/big"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// The system rules a purchase posts by, with their version (A14).
const (
	purchaseRuleInvoice              = "purchase.supplier_invoice"
	purchaseRuleCreditNote           = "purchase.supplier_credit_note"
	purchaseRuleReversal             = "purchase.supplier_invoice_reversed"
	purchaseRulePayment              = "purchase.payment"
	purchaseRulePaymentRemoved       = "purchase.payment_removed"
	purchaseRuleVersion        int32 = 1

	// sourceAccounting is the source module of a voucher this module's own
	// documents post as: a supplier invoice, its reversal, a payment.
	sourceAccounting = "accounting"
)

// purchaseLineIn is one line of a supplier invoice as entered.
type purchaseLineIn struct {
	AccountID int32
	TaxCodeID *int32
	// Amount is in the invoice's currency; Gross says whether it holds the
	// invoice's VAT.
	Amount    *big.Rat
	Gross     bool
	ProjectID *int32
	Text      string
}

// purchaseLine is a line computed: its net and the invoice's VAT on it in
// the invoice's currency (VAT is zero for a reverse-charge code, whose VAT
// the buyer calculates), and the code and rate that made them.
type purchaseLine struct {
	purchaseLineIn
	code       *store.TaxCodesForPostingRow
	rate       *big.Rat
	net, vat   *big.Rat
	gross      *big.Rat
	hasVAT     bool
	reverse    bool
	deductible bool
}

// computePurchaseLine is one line's net, VAT and gross in the invoice's
// currency, or the field error that refuses it: an unknown or inactive code,
// an output code, a code with no rate on the day, a reverse-charge code
// entered gross.
func computePurchaseLine(in purchaseLineIn, code *store.TaxCodesForPostingRow) (purchaseLine, string, string) {
	out := purchaseLine{purchaseLineIn: in, code: code, net: new(big.Rat).Set(in.Amount), vat: new(big.Rat)}
	if code != nil {
		switch {
		case !code.Active:
			return out, codeTaxCodeUnknown, "No such active tax code."
		case code.Direction == "output":
			return out, codeTaxCodeNotPurchase, "An output code is a sale's, not a purchase's."
		case code.Direction == "input":
			rate := numText(code.RatePercent)
			if rate == nil {
				return out, codeTaxCodeNoRate, "No rate of the code covers the invoice's date."
			}
			out.rate = ratPercent(*rate)
			out.reverse = code.ReverseCharge
			out.deductible = code.VatAccountID != nil
			out.hasVAT = out.rate.Sign() > 0
			if out.reverse {
				if in.Gross {
					return out, codeReverseChargeGross, "A reverse-charge code is entered net: the invoice carries no VAT."
				}
				break
			}
			out.net, out.vat = taxOf(in.Amount, out.rate, in.Gross)
		}
	}
	out.gross = add(out.net, out.vat)
	return out, "", ""
}

// purchaseDoc is a supplier invoice as its voucher is built from it.
type purchaseDoc struct {
	ID         int64
	CreditNote bool
	Number     string
	Supplier   int64
	Currency   string
	// Rate is NOK per unit; TotalNOK the invoice's total in NOK, the payable.
	Rate              *big.Rat
	Total, TotalNOK   *big.Rat
	DocumentDate      time.Time
	DueDate           *time.Time
	CreditsOpenItemID *int64
	Description       string
	Lines             []purchaseLine
}

// purchaseVoucherLines is a supplier invoice's postings (A18, R8): each
// line's net on its account with its code as the base, the VAT as the code
// says (above), and the payable — the total in NOK, credited on the
// supplier's item. On an invoice in another currency each posting is its
// currency amount at the rate, rounded to øre, and the difference between
// their sum and the NOK total rounding leaves goes on the largest base line,
// so the voucher follows the invoice's own total. A reverse-charge VAT is
// computed on the NOK base. A credit note turns every sign.
func purchaseVoucherLines(d purchaseDoc) []Line {
	sign := big.NewRat(1, 1)
	if d.CreditNote {
		sign = big.NewRat(-1, 1)
	}
	signed := func(r *big.Rat) *big.Rat { return new(big.Rat).Mul(r, sign) }
	foreign := d.Currency != currency
	toNOK := func(r *big.Rat) *big.Rat {
		if !foreign {
			return new(big.Rat).Set(r)
		}
		return docmath.Round2(new(big.Rat).Mul(r, d.Rate))
	}
	inCurrency := func(l *Line, amount *big.Rat) {
		if foreign {
			l.Currency, l.CurrencyAmount, l.ExchangeRate = d.Currency, signed(amount), new(big.Rat).Set(d.Rate)
		}
	}
	var lines []Line
	nokSum := new(big.Rat)
	largest := -1
	var largestNet *big.Rat
	for i, l := range d.Lines {
		text := l.Text
		if text == "" {
			text = fmt.Sprintf("Line %d", i+1)
		}
		baseNOK := toNOK(l.net)
		base := Line{AccountID: l.AccountID, Amount: signed(baseNOK), ProjectID: l.ProjectID, Text: text}
		if l.code != nil {
			base.TaxCodeID, base.TaxRole = &l.code.ID, "base"
		}
		inCurrency(&base, l.net)
		lines = append(lines, base)
		nokSum.Add(nokSum, baseNOK)
		if largest < 0 || abs(l.net).Cmp(largestNet) > 0 {
			largest, largestNet = len(lines)-1, abs(l.net)
		}
		if !l.hasVAT {
			continue
		}
		tax := func(account int32, amount *big.Rat) Line {
			return Line{AccountID: account, Amount: amount, TaxCodeID: &l.code.ID, TaxRole: "tax", TaxBase: signed(baseNOK),
				ProjectID: l.ProjectID, Text: text}
		}
		if l.reverse {
			vat := docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(baseNOK, l.rate), hundred))
			deductible := l.AccountID
			if l.code.VatAccountID != nil {
				deductible = *l.code.VatAccountID
			}
			counter := l.AccountID
			if l.code.CounterVatAccountID != nil {
				counter = *l.code.CounterVatAccountID
			}
			lines = append(lines, tax(deductible, signed(vat)), tax(counter, neg(signed(vat))))
			continue
		}
		vatNOK := toNOK(l.vat)
		account := l.AccountID
		if l.code.VatAccountID != nil {
			account = *l.code.VatAccountID
		}
		t := tax(account, signed(vatNOK))
		inCurrency(&t, l.vat)
		lines = append(lines, t)
		nokSum.Add(nokSum, vatNOK)
	}
	if diff := sub(d.TotalNOK, nokSum); diff.Sign() != 0 && largest >= 0 {
		lines[largest].Amount = add(lines[largest].Amount, signed(diff))
	}
	payable := Line{Role: rolePayables, Amount: neg(signed(d.TotalNOK)), SupplierID: &d.Supplier,
		Text: d.Description}
	inCurrency(&payable, neg(d.Total))
	if d.CreditsOpenItemID != nil {
		payable.OpenItemID = d.CreditsOpenItemID
	} else {
		payable.Item = &OpenItemSpec{DocumentNumber: d.Number, DocumentDate: &d.DocumentDate, DueDate: d.DueDate}
	}
	return append([]Line{payable}, lines...)
}
