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

// ReliedOnMetDeadline is the sent letters whose reminder fee is waived
// deadline_met (D4, D8): each sent letter claiming a reminder fee not waived
// already, sent after an earlier sent letter's deadline that the payments
// ordered on or before it, as the ledger now shows, met (DeadlineMet). Any
// such fee — a second fee of its chain or a first one after a fee-free letter
// whose deadline was in fact met — was claimed as if that deadline had been
// missed. The compensation is not waived: it is due from the due date, on no
// deadline. The ids are the letters', oldest first.
func ReliedOnMetDeadline(in Input) []int64 {
	sent := sentLetters(in.Letters)
	var ids []int64
	for i, l := range sent {
		if l.FeeKind != FeeReminder || slices.ContainsFunc(in.Waivers, func(w Waiver) bool {
			return w.ReminderID == l.ID && w.Kind == WaiverFee
		}) {
			continue
		}
		if slices.ContainsFunc(sent[:i], func(p Letter) bool {
			return p.Deadline != nil && p.Deadline.Before(*l.SentOn) && DeadlineMet(in, p)
		}) {
			ids = append(ids, l.ID)
		}
	}
	return ids
}
