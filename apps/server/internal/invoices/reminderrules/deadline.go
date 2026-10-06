package reminderrules

import (
	"math/big"
	"slices"
	"time"
)

// Delivered is D8's delivery fact (I4): a live delivery — an e-mail sent, an
// EHF transmission delivered, a manual delivery — on or before the due date
// itself (not E). A delivery after the due date is none: the due date the
// customer had was not a fair one (the spec's reading 30). Every charge needs
// it.
func Delivered(in Input) bool {
	for _, d := range in.Deliveries {
		if !d.After(in.Invoice.DueDate) {
			return true
		}
	}
	return false
}

// DeadlineMet is whether letter's deadline was met (I5): the live payments
// ordered on or before it — ordered_on where the bank line has one, else
// paid_on, which is never earlier — cover the principal open on the letter's
// sending. Computed from the ledger, so a payment made before the letter
// counts on both sides; a credit note issued by the deadline lowers what had
// to be paid. A letter without a deadline has none to meet.
func DeadlineMet(in Input, letter Letter) bool {
	if letter.SentOn == nil || letter.Deadline == nil {
		return false
	}
	deadline := *letter.Deadline
	owed := new(big.Rat).Set(in.Invoice.Gross)
	for _, c := range in.Credits {
		if !c.IssueDate.After(deadline) {
			owed.Sub(owed, c.Gross)
		}
	}
	for _, p := range in.Payments {
		if !orderedOn(p).After(deadline) {
			owed.Sub(owed, p.Amount)
		}
	}
	return owed.Sign() <= 0
}

// orderedOn is the day a payment was ordered, as far as Vantigo knows it.
func orderedOn(p Payment) time.Time {
	if p.OrderedOn != nil {
		return *p.OrderedOn
	}
	return p.PaidOn
}

// ReliedOnMetDeadline is the sent letters whose reminder fee relied on a
// missed deadline that, as the ledger now shows, was met (D8's "a fee claimed
// afterwards in reliance on the missed deadline is waived when the proof
// arrives"; D4's deadline_met waiver): each second fee of its chain — R10's,
// claimed because the previous fee letter's deadline passed unpaid — whose
// previous fee letter's deadline DeadlineMet now finds met, and whose fee is
// not waived already. A first fee, or one after the six-month reset, relied
// on no deadline. The ids are the letters', oldest first.
func ReliedOnMetDeadline(in Input) []int64 {
	var fees []Letter
	for _, l := range sentLetters(in.Letters) {
		if l.FeeKind == FeeReminder {
			fees = append(fees, l)
		}
	}
	var ids []int64
	for i := 1; i < len(fees); i++ {
		letter, prev := fees[i], fees[i-1]
		if feeChainCount(fees[:i], *letter.SentOn) != 1 || !DeadlineMet(in, prev) {
			continue
		}
		waived := slices.ContainsFunc(in.Waivers, func(w Waiver) bool {
			return w.ReminderID == letter.ID && w.Kind == WaiverFee
		})
		if !waived {
			ids = append(ids, letter.ID)
		}
	}
	return ids
}
