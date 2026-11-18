package reminderrules

import (
	"slices"
	"testing"
)

// An invoice in another currency than NOK (invoices phase 6 design I25):
// neither the reminder fee nor the B2B compensation — NOK amounts written for
// Norwegian debtors — but the late-payment interest as on any invoice, on
// the currency principal at the Norwegian rate.
func TestReminderRules_ForeignCurrency(t *testing.T) {
	for _, buyer := range []string{"person", BuyerBusiness} {
		in := baseInput("2026-07-15")
		in.Settings.LateInterest = true
		in.Invoice.BuyerType, in.Invoice.BuyerOrganisationNumber = buyer, "923456783"
		in.Settings.BusinessCharge = ChargeCompensation
		in.Invoice.Foreign = true
		out := Next(in)
		if out.Letter == nil {
			t.Fatalf("%s: %s %v, want a letter", buyer, out.Action, out.Reasons)
		}
		if out.Letter.FeeKind != FeeNone || out.Letter.Fee.Sign() != 0 || out.Letter.Compensation.Sign() != 0 {
			t.Errorf("%s: %+v, want no fee or compensation", buyer, *out.Letter)
		}
		eqRat(t, buyer+": interest", out.Letter.Interest, "99.66")
		if !slices.Equal(out.ChargeNotes, []string{NoteForeignCurrency}) {
			t.Errorf("%s: notes %v, want foreign_currency", buyer, out.ChargeNotes)
		}
	}
}
