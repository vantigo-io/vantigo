package reminderrules

import (
	"math/big"
	"testing"
	"time"
)

// withInterest is l claiming a cumulative interest of amount.
func withInterest(l Letter, amount string) Letter { l.Interest = rat(amount); return l }

func chargePayment(id int64, on, amount string) ChargePayment {
	return ChargePayment{ID: id, PaidOn: day(on), Amount: rat(amount)}
}

// D9's formula: Σ fee + Σ compensation over the sent letters − their waivers,
// + the latest sent letter's cumulative interest − Σ interest waivers,
// − Σ the live charge payments; negative is a refund due.
func TestCharges_Formula(t *testing.T) {
	t.Parallel()
	l1 := withInterest(sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder), "20")
	l2 := withInterest(sentLetter(2, 2, LevelNotice, "2026-07-20", 14, FeeReminder), "45")
	queued := Letter{ID: 3, Sequence: 3, Level: LevelReminder, Status: StatusQueued}
	withdrawn := withInterest(withStatus(sentLetter(4, 4, LevelReminder, "2026-08-10", 14, FeeReminder), StatusWithdrawn), "70")
	letters := []Letter{l2, queued, l1, withdrawn}

	for _, c := range []struct {
		name                                                  string
		letters                                               []Letter
		waivers                                               []Waiver
		payments                                              []ChargePayment
		claimed, waived, paid, outstanding, refund, fees, int string
	}{
		{"claimed only", letters, nil, nil, "121", "0", "0", "121", "0", "76", "45"},
		{
			"a fee waived, interest waived, a payment", letters,
			[]Waiver{{ID: 1, ReminderID: 1, Kind: WaiverFee, Amount: rat("38")}, {ID: 2, ReminderID: 2, Kind: WaiverInterest, Amount: rat("5")}},
			[]ChargePayment{chargePayment(1, "2026-07-25", "50")},
			"121", "43", "50", "28", "0", "0", "28",
		},
		{
			"the compensation once", []Letter{sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeCompensation), sentLetter(2, 2, LevelReminder, "2026-07-20", 14, FeeNone)},
			nil, nil, "460", "0", "0", "460", "0", "460", "0",
		},
		{
			"the compensation waived", []Letter{sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeCompensation)},
			[]Waiver{{ID: 1, ReminderID: 1, Kind: WaiverCompensation, Amount: rat("460")}}, nil,
			"460", "460", "0", "0", "0", "0", "0",
		},
		{
			"paid, then waived: a refund due", []Letter{sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder)},
			[]Waiver{{ID: 1, ReminderID: 1, Kind: WaiverFee, Amount: rat("38")}}, []ChargePayment{chargePayment(1, "2026-07-05", "38")},
			"38", "38", "38", "0", "38", "0", "0",
		},
		{
			// A removed charge payment is not live: the loader leaves it out.
			"a removed payment absent", letters, nil, []ChargePayment{chargePayment(2, "2026-07-25", "10")},
			"121", "0", "10", "111", "0", "66", "45",
		},
	} {
		got := Charges(c.letters, c.waivers, c.payments)
		eqRat(t, c.name+": claimed", got.Claimed, c.claimed)
		eqRat(t, c.name+": waived", got.Waived, c.waived)
		eqRat(t, c.name+": paid", got.Paid, c.paid)
		eqRat(t, c.name+": outstanding", got.Outstanding, c.outstanding)
		eqRat(t, c.name+": refund due", got.RefundDue, c.refund)
		eqRat(t, c.name+": fees outstanding", got.FeesOutstanding, c.fees)
		eqRat(t, c.name+": interest outstanding", got.InterestOutstanding, c.int)
	}
}

// Reading 8: the charge payments pay the fees and the compensation net of
// waivers, oldest letter first, then interest capped at the latest letter's
// interest less the interest waivers; the excess is the refund.
func TestCharges_AllocationOrder(t *testing.T) {
	t.Parallel()
	first := sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder)
	first.Fee = rat("35")
	second := withInterest(sentLetter(2, 2, LevelNotice, "2026-07-20", 14, FeeReminder), "50")
	letters := []Letter{second, first} // out of order on purpose
	for _, c := range []struct {
		name                            string
		waivers                         []Waiver
		payments                        []ChargePayment
		first, second, interest, refund string
	}{
		{"short of the fees", nil, []ChargePayment{chargePayment(1, "2026-07-25", "20"), chargePayment(2, "2026-07-26", "40")}, "35", "25", "0", "0"},
		{"into interest", nil, []ChargePayment{chargePayment(1, "2026-07-25", "100")}, "35", "38", "27", "0"},
		{
			"capped at interest less its waivers", []Waiver{{ID: 1, ReminderID: 2, Kind: WaiverInterest, Amount: rat("10")}},
			[]ChargePayment{chargePayment(1, "2026-07-25", "200")}, "35", "38", "40", "87",
		},
		{
			"a waived fee takes nothing", []Waiver{{ID: 1, ReminderID: 1, Kind: WaiverFee, Amount: rat("35")}},
			[]ChargePayment{chargePayment(1, "2026-07-25", "60")}, "0", "38", "22", "0",
		},
	} {
		per, interestPaid, refund := Allocate(letters, c.waivers, c.payments)
		eqRat(t, c.name+": first letter", per[1], c.first)
		eqRat(t, c.name+": second letter", per[2], c.second)
		eqRat(t, c.name+": interest", interestPaid, c.interest)
		eqRat(t, c.name+": refund", refund, c.refund)
	}
}

// halfKroneADay is 1 000 at a rate of 18.25 %: 0.50 a day, so the interest
// figures of D9's examples fall on whole days.
func halfKroneADay(l string) Input {
	in := baseInput(l)
	in.Invoice.IssueDate, in.Invoice.DueDate, in.Invoice.Gross = day("2026-02-20"), day("2026-03-02"), rat("1000")
	in.Deliveries = []time.Time{day("2026-02-20")}
	in.Settings.LateInterest, in.Settings.RemindersBeforeNotice = true, 2
	in.Rates = []Rate{
		{ID: 1, Kind: KindLateInterest, ValidFrom: day("2026-01-01"), Value: rat("18.25")},
		{ID: 2, Kind: KindInkassosats, ValidFrom: day("2026-01-01"), Value: rat("750")},
	}
	return in
}

// NI1: letter 1 claims interest 20, 12 of it is paid, 8 waived; letter 2's
// cumulative interest is 33, so it states 33 − 8 − 12 = 13, and the charges
// outstanding once it is sent say the same 13. (The server's
// TestCharges_InterestWaiverThenLaterLetter, Task 8, adds the auto-match.)
func TestLetterFigures_InterestWaiverThenLaterLetter(t *testing.T) {
	t.Parallel()
	in := halfKroneADay("2026-04-11") // day 40 from 3 Mar
	in.Settings.PersonCharge = ChargeNone
	if total, _, _, _ := Interest(in); total.Cmp(rat("20")) != 0 {
		t.Fatalf("letter 1's interest = %s, want 20", total.FloatString(2))
	}
	first := withInterest(sentLetter(1, 1, LevelReminder, "2026-04-11", 14, FeeNone), "20")
	first.PrincipalOpen = rat("1000")
	in.L = day("2026-05-07") // day 66
	in.Letters = []Letter{first}
	in.ChargePayments = []ChargePayment{chargePayment(1, "2026-04-20", "12")}
	in.Waivers = []Waiver{{ID: 1, ReminderID: 1, Kind: WaiverInterest, Amount: rat("8")}}

	out := Next(in)
	if out.Action != ActionReminder || out.Letter == nil {
		t.Fatalf("letter 2: %s %v, want a reminder", out.Action, out.Reasons)
	}
	f := out.Letter
	eqRat(t, "interest", f.Interest, "33")
	eqRat(t, "interest waived", f.InterestWaived, "8")
	eqRat(t, "interest paid", f.InterestPaid, "12")
	eqRat(t, "charges earlier", f.ChargesEarlier, "0")
	eqRat(t, "total", f.Total, "1013")

	second := withInterest(sentLetter(2, 2, LevelReminder, "2026-05-07", 14, FeeNone), "33")
	got := Charges([]Letter{first, second}, in.Waivers, in.ChargePayments)
	eqRat(t, "interest outstanding", got.InterestOutstanding, "13")
	eqRat(t, "outstanding", got.Outstanding, "13")

	// Interest turned off before letter 2: its interest part is nothing, and
	// the waivers are never taken off the principal.
	in.Settings.LateInterest = false
	if out := Next(in); out.Letter == nil {
		t.Errorf("letter 2 without interest: %s %v", out.Action, out.Reasons)
	} else {
		eqRat(t, "without interest: total", out.Letter.Total, "1000")
	}
}

// B2: letter 1 with a fee and interest, a charge payment between, letter 2
// whose charges_earlier, interest, interest_paid and total are pinned, and
// whose total is the principal plus the charges outstanding once it is sent.
func TestLetterFigures_TwoLettersWithInterestAndAChargePayment(t *testing.T) {
	t.Parallel()
	first := withInterest(sentLetter(1, 1, LevelReminder, "2026-04-11", 14, FeeReminder), "20") // fee 38
	first.PrincipalOpen = rat("1000")
	for _, c := range []struct {
		name, paid                           string
		earlier, interestPaid, total, charge string
	}{
		{"the fee and some interest paid", "50", "0", "12", "1059", "59"}, // 38 to the fee, 12 to interest
		{"part of the fee paid", "30", "8", "0", "1079", "79"},            // 30 to the fee
	} {
		in := halfKroneADay("2026-05-07")
		in.Letters = []Letter{first}
		in.ChargePayments = []ChargePayment{chargePayment(1, "2026-04-20", c.paid)}
		out := Next(in)
		if out.Letter == nil {
			t.Fatalf("%s: %s %v, want letter 2", c.name, out.Action, out.Reasons)
		}
		f := out.Letter
		eqRat(t, c.name+": fee", f.Fee, "38")
		eqRat(t, c.name+": charges earlier", f.ChargesEarlier, c.earlier)
		eqRat(t, c.name+": interest", f.Interest, "33")
		eqRat(t, c.name+": interest paid", f.InterestPaid, c.interestPaid)
		eqRat(t, c.name+": total", f.Total, c.total)

		second := withInterest(sentLetter(2, 2, LevelReminder, "2026-05-07", 14, FeeReminder), "33")
		got := Charges([]Letter{first, second}, nil, in.ChargePayments)
		eqRat(t, c.name+": outstanding after letter 2", got.Outstanding, c.charge)
		if sum := new(big.Rat).Add(f.PrincipalOpen, got.Outstanding); sum.Cmp(f.Total) != 0 {
			t.Errorf("%s: principal + outstanding = %s, letter 2's total %s", c.name, sum.FloatString(2), f.Total.FloatString(2))
		}
	}
}

// Task 4 review, B1: charge payments beyond the earlier charges — a fee paid
// then waived, or paid over — pay the new letter's own fee, so
// charges_earlier goes below zero (a credit) and the letter's total is the
// principal plus what Charges says once it is sent.
func TestLetterFigures_ChargeCreditPaysThisLettersFee(t *testing.T) {
	t.Parallel()
	first := sentLetter(1, 1, LevelReminder, "2026-07-01", 14, FeeReminder) // fee 38, deadline 15 Jul
	waived := []Waiver{{ID: 1, ReminderID: 1, Kind: WaiverFee, Amount: rat("38")}}
	for _, c := range []struct {
		name           string
		paid           string
		waivers        []Waiver
		refundBefore   string
		earlier, total string
		outstanding    string
		refundAfter    string
	}{
		{"the fee paid, then waived", "38", waived, "38", "-38", "10000", "0", "0"},
		{"50 paid against the fee of 38", "50", nil, "12", "-12", "10026", "26", "0"},
		{"100 paid against the fee of 38", "100", nil, "62", "-38", "10000", "0", "24"},
	} {
		payments := []ChargePayment{chargePayment(1, "2026-07-05", c.paid)}
		eqRat(t, c.name+": refund due before letter 2", Charges([]Letter{first}, c.waivers, payments).RefundDue, c.refundBefore)

		in := baseInput("2026-07-20")
		in.Settings.RemindersBeforeNotice = 2
		in.Letters, in.Waivers, in.ChargePayments = []Letter{first}, c.waivers, payments
		out := Next(in)
		if out.Letter == nil || out.Letter.FeeKind != FeeReminder {
			t.Fatalf("%s: %s %v, want a fee letter", c.name, out.Action, out.Reasons)
		}
		eqRat(t, c.name+": charges earlier", out.Letter.ChargesEarlier, c.earlier)
		eqRat(t, c.name+": total", out.Letter.Total, c.total)

		second := sentLetter(2, 2, LevelReminder, "2026-07-20", 14, FeeReminder)
		after := Charges([]Letter{first, second}, c.waivers, payments)
		eqRat(t, c.name+": outstanding after letter 2", after.Outstanding, c.outstanding)
		eqRat(t, c.name+": refund due after letter 2", after.RefundDue, c.refundAfter)
		if sum := new(big.Rat).Add(out.Letter.PrincipalOpen, after.Outstanding); sum.Cmp(out.Letter.Total) != 0 {
			t.Errorf("%s: principal + outstanding = %s, the letter's total %s", c.name, sum.FloatString(2), out.Letter.Total.FloatString(2))
		}
	}
}
