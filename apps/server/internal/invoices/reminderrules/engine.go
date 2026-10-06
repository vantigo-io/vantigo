package reminderrules

import (
	"math/big"
	"slices"
	"time"
)

// Level is a letter's kind: a purring, or the creditor's inkassovarsel.
type Level string

const (
	LevelReminder Level = "reminder"
	LevelNotice   Level = "collection_notice"
)

// Action is what D8 says to do next with an invoice.
type Action string

const (
	ActionNone     Action = "none"
	ActionReminder Action = "reminder"
	ActionNotice   Action = "collection_notice"
	ActionHandOff  Action = "hand_off" // suggested; never automatic
	ActionBlocked  Action = "blocked"
	ActionWaiting  Action = "waiting"
)

// FeeKind is the charge a letter claims besides interest: a reminder fee or
// the § 3a compensation, never both on one invoice (R6).
type FeeKind string

const (
	FeeNone         FeeKind = "none"
	FeeReminder     FeeKind = "reminder_fee"
	FeeCompensation FeeKind = "compensation"
)

// Mode is the customer's reminder policy (D7); no row is normal.
type Mode string

const (
	ModeNormal    Mode = "normal"
	ModeNoCharges Mode = "no_charges"
	ModeNone      Mode = "none"
)

// A letter's statuses (D10), as invoices.reminders stores them.
const (
	StatusQueued        = "queued"
	StatusAwaitingPrint = "awaiting_print"
	StatusPrinted       = "printed"
	StatusSent          = "sent"
	StatusWithdrawn     = "withdrawn"
	StatusFailed        = "failed"
)

// The reminder settings' charge choices (D7) and the buyer type that may owe
// the compensation.
const (
	ChargeFee          = "fee"
	ChargeCompensation = "compensation"
	ChargeNone         = "none"
	BuyerBusiness      = "business"
)

// Why an invoice gets no letter now (Outcome.Reasons).
const (
	ReasonOnHold            = "on_hold"
	ReasonHandedOff         = "handed_off"
	ReasonPolicyNone        = "policy_none"
	ReasonRemindersDisabled = "reminders_disabled"
	ReasonLetterPending     = "letter_pending"
	ReasonWaiting           = "waiting"
	ReasonNotDelivered      = "not_delivered"
	ReasonRatesOutdated     = "collection_rates_outdated"
	ReasonRegimeUnreviewed  = "collection_regime_unreviewed"
)

// Why a letter claims less than it might (Outcome.ChargeNotes, the letter's
// charge_notes).
const (
	NoteNotDelivered    = "not_delivered"
	NoteChargesBarred   = "charges_barred"
	NoteFeeCapReached   = "fee_cap_reached"
	NoteFeeBefore14Days = "fee_before_14_days"
)

// Settings is invoices.reminder_settings as the engine needs it (D6, D7).
type Settings struct {
	Enabled                                                                            bool
	FirstReminderDays, DeadlineDays, GraceDays, RemindersBeforeNotice, StaleImportDays int
	CollectionNotice, LateInterest                                                     bool
	PersonCharge, BusinessCharge                                                       string // ChargeFee, ChargeCompensation, ChargeNone
	Inkassolov2026From                                                                 *time.Time
	RegimeReviewedThrough                                                              time.Time
}

// Invoice is the issued invoice's snapshot: its dates, gross and buyer.
type Invoice struct {
	IssueDate, DueDate                                 time.Time
	Gross                                              *big.Rat
	BuyerType, BuyerOrganisationNumber, BuyerForeignID string // "" for NULL
}

// Credit is a credit note issued against the invoice.
type Credit struct {
	IssueDate time.Time
	Gross     *big.Rat
}

// Payment is a live payment of the principal; OrderedOn is its bank line's
// order date, when the file carries one.
type Payment struct {
	PaidOn    time.Time
	OrderedOn *time.Time
	Amount    *big.Rat
}

// Reservation is a live payment reservation (4C fills it; PR 1 always empty).
type Reservation struct{ Amount *big.Rat }

// Letter is one of the invoice's reminders: in flight without facts, or sent
// with the facts written at its sending.
type Letter struct {
	ID                                         int64
	Sequence                                   int
	Level                                      Level
	AnnouncesCollection                        bool
	Status                                     string
	SentOn, Deadline                           *time.Time
	FeeKind                                    FeeKind
	Fee, Compensation, Interest, PrincipalOpen *big.Rat
}

// Waiver is a charge waiver of a letter's fee, compensation or interest.
type Waiver struct {
	ID, ReminderID int64
	Kind           string // WaiverFee, WaiverCompensation, WaiverInterest
	Amount         *big.Rat
}

// ChargePayment is a live payment of charges.
type ChargePayment struct {
	ID     int64
	PaidOn time.Time
	Amount *big.Rat
}

// Input is everything D8 judges an invoice on, read under its lock (or on
// the pool for the list), and the day L it is asked about. Every day is UTC
// midnight of an Oslo calendar day.
type Input struct {
	L              time.Time
	Invoice        Invoice
	Credits        []Credit
	Payments       []Payment
	Reservations   []Reservation
	Deliveries     []time.Time // every live delivery's day: e-mail sent, EHF delivered, manual delivered_on
	Letters        []Letter
	Waivers        []Waiver
	ChargePayments []ChargePayment
	Settings       Settings
	Mode           Mode
	OnHold         bool // a live hold
	HandedOff      bool // a live hand-off
	ChargesBarred  bool // a lifted hold barred charges (D11)
	Rates          []Rate
	Exclude        int64 // the letter being dispatched or posted, left out of every set
}

// LetterFacts is a letter as it would be sent on L: what invoices.reminders
// records at sending (D10) and the letter shows.
type LetterFacts struct {
	Level               Level
	AnnouncesCollection bool
	Regime              Regime
	FeeKind             FeeKind
	// D9's figures: Total = PrincipalOpen + ChargesEarlier + Fee + Compensation
	// + Interest − InterestWaived − InterestPaid (the interest part never below
	// zero).
	PrincipalOpen, Fee, Compensation, ChargesEarlier, Interest, InterestWaived, InterestPaid, Total *big.Rat
	Inkassosats                                                                                     *big.Rat // the rate the fee was computed from; nil without a fee
	InterestFrom                                                                                    *time.Time
	Segments                                                                                        []Segment
	Deadline                                                                                        time.Time
	RateIDs                                                                                         []int64 // the collection_rates rows used
}

// Outcome is D8's answer for an invoice on L.
type Outcome struct {
	Action      Action
	EarliestOn  time.Time // the earliest day of the action; zero when none applies
	Reasons     []string
	ChargeNotes []string
	Outdated    *OutdatedRate // with collection_rates_outdated
	Letter      *LetterFacts  // when Action is a letter and EarliestOn <= L
}

// Next is D8's next action for an invoice on L: the first that applies of
//
//  1. the principal settled → none (charges may still be outstanding);
//  2. a live hand-off → none, handed_off;
//  3. a live hold → blocked, on_hold;
//  4. the customer's mode none → blocked, policy_none; reminders off →
//     blocked, reminders_disabled;
//  5. a letter in flight (but the excluded one) → blocked, letter_pending;
//  6. no letter sent → a reminder, or the notice when no reminder comes
//     before it, from E + first_reminder_days;
//  7. the last letter's deadline + grace_days not passed → waiting, until the
//     day after;
//  8. fewer letters sent than reminders_before_notice → a reminder;
//  9. under 1988 the notice when it is offered and none was sent; under 2026
//     one reminder announcing the hand-off when none did;
//  10. otherwise the hand-off.
//
// Without a delivery on or before the due date only fee-free reminders go, at
// most max(reminders_before_notice, 1), and whatever would follow is blocked
// not_delivered. A letter due on L carries its facts; one that would carry a
// fee or be the notice past the regime review, or that needs a rate a
// half-year lacks, is blocked instead.
func Next(in Input) Outcome {
	in.Letters = slices.DeleteFunc(slices.Clone(in.Letters), func(l Letter) bool { return in.Exclude != 0 && l.ID == in.Exclude })
	if principalOpen(in).Sign() <= 0 {
		return Outcome{Action: ActionNone}
	}
	switch {
	case in.HandedOff:
		return Outcome{Action: ActionNone, Reasons: []string{ReasonHandedOff}}
	case in.OnHold:
		return blocked(ReasonOnHold)
	case in.Mode == ModeNone:
		return blocked(ReasonPolicyNone)
	case !in.Settings.Enabled:
		return blocked(ReasonRemindersDisabled)
	case slices.ContainsFunc(in.Letters, inFlight):
		return blocked(ReasonLetterPending)
	}
	s := in.Settings
	sent := sentLetters(in.Letters)
	if len(sent) == 0 {
		level := LevelReminder
		if s.RemindersBeforeNotice == 0 && (s.CollectionNotice || RegimeOn(s, in.L) == Regime2026) {
			level = LevelNotice
		}
		return letter(in, sent, level, addDays(EffectiveDue(in.Invoice.DueDate), s.FirstReminderDays))
	}
	last := sent[len(sent)-1]
	resume := addDays(deadlineOf(last), s.GraceDays+1)
	if in.L.Before(resume) {
		return Outcome{Action: ActionWaiting, EarliestOn: resume, Reasons: []string{ReasonWaiting}}
	}
	if !Delivered(in) && len(sent) >= max(s.RemindersBeforeNotice, 1) {
		return Outcome{Action: ActionBlocked, EarliestOn: resume, Reasons: []string{ReasonNotDelivered}}
	}
	if len(sent) < s.RemindersBeforeNotice {
		return letter(in, sent, LevelReminder, resume)
	}
	if RegimeOn(s, in.L) == Regime2026 {
		if !slices.ContainsFunc(sent, announces) {
			return letter(in, sent, LevelNotice, resume) // a reminder announcing the hand-off
		}
	} else if s.CollectionNotice && !slices.ContainsFunc(sent, func(l Letter) bool { return l.Level == LevelNotice }) {
		return letter(in, sent, LevelNotice, resume)
	}
	return Outcome{Action: ActionHandOff, EarliestOn: resume}
}

func blocked(reason string) Outcome {
	return Outcome{Action: ActionBlocked, Reasons: []string{reason}}
}

// letter is the outcome for a letter of level due from earliest: its facts
// when it is due on L, or the reasons it is blocked.
func letter(in Input, sent []Letter, level Level, earliest time.Time) Outcome {
	regime := RegimeOn(in.Settings, in.L)
	delivered := Delivered(in)
	announcesCollection := false
	switch {
	case !delivered:
		level = LevelReminder // a fee-free plain reminder: nothing is due yet (NI3)
	case level == LevelNotice && regime == Regime2026:
		level, announcesCollection = LevelReminder, true // no creditor's inkassovarsel under 2026 (D6)
	}
	out := Outcome{Action: ActionReminder, EarliestOn: earliest}
	if level == LevelNotice {
		out.Action = ActionNotice
	}
	if in.L.Before(earliest) {
		return out
	}

	f := &LetterFacts{
		Level: level, AnnouncesCollection: announcesCollection, Regime: regime, FeeKind: FeeNone,
		PrincipalOpen: principalOpen(in), Fee: new(big.Rat), Compensation: new(big.Rat),
		Deadline: addDays(in.L, in.Settings.DeadlineDays),
	}
	var notes []string
	var outdated *OutdatedRate
	charge := judgeCharge(in, sent, regime, delivered)
	notes = charge.notes
	switch {
	case charge.compensation:
		outdated = Outdated(in.Rates, KindCompensation, in.L, in.L)
		if rate, ok := RateOn(in.Rates, KindCompensation, in.L); ok && outdated == nil {
			f.FeeKind, f.Compensation = FeeCompensation, rate.Value
			f.RateIDs = append(f.RateIDs, rate.ID)
		}
	case charge.fee:
		outdated = Outdated(in.Rates, KindInkassosats, in.L, in.L)
		if rate, ok := RateOn(in.Rates, KindInkassosats, in.L); ok && outdated == nil {
			f.FeeKind, f.Fee, f.Inkassosats = FeeReminder, ReminderFee(rate.Value), rate.Value
			f.RateIDs = append(f.RateIDs, rate.ID)
		}
	}
	ir := interest(in)
	if ir.outdated != nil {
		outdated = ir.outdated
	}
	f.Interest, f.InterestFrom, f.Segments = ir.total, ir.from, ir.segs
	f.RateIDs = append(ir.rateIDs, f.RateIDs...)

	unreviewed := regime == Regime1988 && in.L.After(in.Settings.RegimeReviewedThrough) &&
		(charge.fee || level == LevelNotice)
	if outdated != nil || unreviewed {
		out.Action = ActionBlocked
		if outdated != nil {
			out.Reasons, out.Outdated = append(out.Reasons, ReasonRatesOutdated), outdated
		}
		if unreviewed {
			out.Reasons = append(out.Reasons, ReasonRegimeUnreviewed)
		}
		return out
	}

	// D9's figures: the earlier letters' fees and compensation less their
	// waivers and what was paid of them, never interest; the cumulative
	// interest less its waivers and what was paid of it, the allocation
	// capped at this letter's interest.
	withThis := append(slices.Clone(sent), Letter{
		ID: -1, Sequence: highestSequence(sent) + 1, Status: StatusSent, SentOn: &in.L, Interest: f.Interest,
	})
	perLetter, interestPaid, _ := Allocate(withThis, in.Waivers, in.ChargePayments)
	f.ChargesEarlier = new(big.Rat)
	for _, l := range sent {
		f.ChargesEarlier.Add(f.ChargesEarlier, chargeNet(l, in.Waivers))
		f.ChargesEarlier.Sub(f.ChargesEarlier, perLetter[l.ID])
	}
	f.InterestWaived, f.InterestPaid = interestWaived(in.Waivers), interestPaid
	f.Total = new(big.Rat).Add(f.PrincipalOpen, f.ChargesEarlier)
	f.Total.Add(f.Total, f.Fee).Add(f.Total, f.Compensation)
	// interest − interest_waived, never below zero: a letter without interest
	// after one with it does not take the waivers off the principal.
	f.Total.Add(f.Total, interestCap(f.Interest, in.Waivers)).Sub(f.Total, f.InterestPaid)
	out.Letter, out.ChargeNotes = f, notes
	return out
}

// chargeJudgement is whether a letter on L claims the fee or the
// compensation, and the notes on what it may not claim.
type chargeJudgement struct {
	fee, compensation bool
	notes             []string
}

// judgeCharge is D8's fee and compensation rules for a letter on L — all but
// the rates and the regime review, which the caller applies.
func judgeCharge(in Input, sent []Letter, regime Regime, delivered bool) chargeJudgement {
	var j chargeJudgement
	if !delivered {
		j.notes = []string{NoteNotDelivered}
		return j
	}
	if in.Mode != ModeNormal {
		return j
	}
	inv := in.Invoice
	business := inv.BuyerType == BuyerBusiness && (inv.BuyerOrganisationNumber != "" || inv.BuyerForeignID != "")
	claimed := func(kind FeeKind) bool {
		return slices.ContainsFunc(sent, func(l Letter) bool { return l.FeeKind == kind })
	}
	switch {
	case business && in.Settings.BusinessCharge == ChargeCompensation:
		// R5/R6: once per invoice, on its first letter, never beside a fee.
		if len(sent) > 0 || claimed(FeeReminder) {
			return j
		}
		if in.ChargesBarred {
			j.notes = []string{NoteChargesBarred}
			return j
		}
		j.compensation = true
	case business && in.Settings.BusinessCharge == ChargeFee, !business && in.Settings.PersonCharge == ChargeFee:
		if regime != Regime1988 || claimed(FeeCompensation) {
			return j // R20; R6
		}
		E := EffectiveDue(inv.DueDate)
		switch count := feeChainCount(sent, in.L); {
		case in.ChargesBarred:
			j.notes = []string{NoteChargesBarred}
		case in.L.Before(addDays(E, 14)):
			j.notes = []string{NoteFeeBefore14Days} // R7: the letter goes, without a fee
		case count >= 2:
			j.notes = []string{NoteFeeCapReached} // R9, R11
		case count == 1 && !missedDeadline(in, sent):
			// R10: no second fee without a missed deadline of at least 14 days.
		default:
			j.fee = true
		}
	}
	return j
}

// missedDeadline is R10's condition on the latest fee letter: a deadline at
// least 14 days after its sending, passed by L, and not met.
func missedDeadline(in Input, sent []Letter) bool {
	var prev *Letter
	for i := range sent {
		if sent[i].FeeKind == FeeReminder {
			prev = &sent[i]
		}
	}
	if prev == nil || prev.Deadline == nil {
		return false
	}
	return !prev.Deadline.Before(addDays(*prev.SentOn, 14)) && in.L.After(*prev.Deadline) && !DeadlineMet(in, *prev)
}

// principalOpen is gross less the credit notes, the live payments and the
// live reservations.
func principalOpen(in Input) *big.Rat {
	open := new(big.Rat).Set(in.Invoice.Gross)
	for _, c := range in.Credits {
		open.Sub(open, c.Gross)
	}
	for _, p := range in.Payments {
		open.Sub(open, p.Amount)
	}
	for _, r := range in.Reservations {
		open.Sub(open, r.Amount)
	}
	return open
}

// sentLetters is the sent letters, oldest first.
func sentLetters(letters []Letter) []Letter {
	var sent []Letter
	for _, l := range letters {
		if l.Status == StatusSent && l.SentOn != nil {
			sent = append(sent, l)
		}
	}
	slices.SortStableFunc(sent, func(a, b Letter) int {
		if c := a.SentOn.Compare(*b.SentOn); c != 0 {
			return c
		}
		return a.Sequence - b.Sequence
	})
	return sent
}

// inFlight is a letter on its way: queued, awaiting print, printed or failed.
func inFlight(l Letter) bool {
	switch l.Status {
	case StatusQueued, StatusAwaitingPrint, StatusPrinted, StatusFailed:
		return true
	}
	return false
}

// announces is a letter that told the debtor collection follows.
func announces(l Letter) bool { return l.AnnouncesCollection || l.Level == LevelNotice }

// highestSequence is the highest sequence among the sent letters.
func highestSequence(sent []Letter) int {
	highest := 0
	for _, l := range sent {
		highest = max(highest, l.Sequence)
	}
	return highest
}

// deadlineOf is a sent letter's deadline; a sent letter always has one, its
// sending day stands in for a row that lacks it.
func deadlineOf(l Letter) time.Time {
	if l.Deadline != nil {
		return *l.Deadline
	}
	return *l.SentOn
}
