package reminderrules

import (
	"math/big"
	"slices"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func dayp(s string) *time.Time { d := day(s); return &d }

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("not a decimal: " + s)
	}
	return r
}

// eqRat fails the test when got is not the decimal want.
func eqRat(t *testing.T, what string, got *big.Rat, want string) {
	t.Helper()
	if got == nil || got.Cmp(rat(want)) != 0 {
		text := "<nil>"
		if got != nil {
			text = got.FloatString(4)
		}
		t.Errorf("%s = %s, want %s", what, text, want)
	}
}

// baseInput is 10 000 to a person, due Monday 15 Jun 2026, e-mailed on its
// issue day, under the default settings with the fee on and one reminder
// before the notice, the seeded rates, and the review well ahead.
func baseInput(l string) Input {
	return Input{
		L: day(l),
		Invoice: Invoice{
			IssueDate: day("2026-06-01"), DueDate: day("2026-06-15"), Gross: rat("10000"), BuyerType: "person",
		},
		Deliveries: []time.Time{day("2026-06-01")},
		Settings: Settings{
			Enabled: true, FirstReminderDays: 14, DeadlineDays: 14, GraceDays: 3, RemindersBeforeNotice: 1,
			StaleImportDays: 3, CollectionNotice: true, PersonCharge: ChargeFee, BusinessCharge: ChargeFee,
			RegimeReviewedThrough: day("2027-12-31"),
		},
		Mode:  ModeNormal,
		Rates: seedRates(),
	}
}

// sentLetter is a sent letter with its facts: a fee of 38 or the
// compensation of 460 when its kind says so.
func sentLetter(id int64, seq int, level Level, on string, deadlineDays int, fee FeeKind) Letter {
	sentOn := day(on)
	deadline := sentOn.AddDate(0, 0, deadlineDays)
	l := Letter{
		ID: id, Sequence: seq, Level: level, Status: StatusSent, SentOn: &sentOn, Deadline: &deadline,
		FeeKind: fee, Fee: new(big.Rat), Compensation: new(big.Rat), Interest: new(big.Rat), PrincipalOpen: rat("10000"),
	}
	switch fee {
	case FeeReminder:
		l.Fee = rat("38")
	case FeeCompensation:
		l.Compensation = rat("460")
	}
	return l
}

func withStatus(l Letter, status string) Letter { l.Status = status; return l }

func reversed(letters []Letter) []Letter { slices.Reverse(letters); return letters }

func sentDays(letters []Letter) []string {
	var days []string
	for _, l := range letters {
		if l.SentOn != nil {
			days = append(days, l.SentOn.Format(time.DateOnly))
		}
	}
	return days
}

// expectBlocked fails unless out is blocked for exactly reason.
func expectBlocked(t *testing.T, what string, out Outcome, reason string) {
	t.Helper()
	if out.Action != ActionBlocked || !slices.Equal(out.Reasons, []string{reason}) || out.Letter != nil {
		t.Errorf("%s: %s %v (letter %v), want blocked %s", what, out.Action, out.Reasons, out.Letter != nil, reason)
	}
}

// R5/R6: the compensation is a business's with an organisation number or a
// foreign business id, claimed once, on the first letter only, never beside a
// fee and never on a person; the NOK figure is the one in force on L, in
// either regime.
func TestReminderRules_Compensation(t *testing.T) {
	t.Parallel()
	business := func(l string) Input {
		in := baseInput(l)
		in.Invoice.BuyerType, in.Invoice.BuyerOrganisationNumber = BuyerBusiness, "923456783"
		in.Settings.BusinessCharge = ChargeCompensation
		in.Settings.RemindersBeforeNotice = 2
		return in
	}
	from2026 := day("2026-01-01")
	for _, c := range []struct {
		name   string
		in     Input
		kind   FeeKind
		amount string
	}{
		{"on 30 Jun 2026", business("2026-06-30"), FeeCompensation, "460"},
		{"on 1 Jul 2026", business("2026-07-01"), FeeCompensation, "430"},
		{"under the 2026 regime", func() Input {
			in := business("2026-07-01")
			in.Settings.Inkassolov2026From = &from2026
			return in
		}(), FeeCompensation, "430"},
		{"a foreign business id suffices", func() Input {
			in := business("2026-07-01")
			in.Invoice.BuyerOrganisationNumber, in.Invoice.BuyerForeignID = "", "DE129273398"
			return in
		}(), FeeCompensation, "430"},
		{"once: a later letter claims nothing", func() Input {
			in := business("2026-07-20")
			in.Letters = []Letter{sentLetter(1, 1, LevelReminder, "2026-06-30", 14, FeeCompensation)}
			return in
		}(), FeeNone, ""},
		{"the first letter only", func() Input {
			in := business("2026-07-20")
			in.Letters = []Letter{sentLetter(1, 1, LevelReminder, "2026-06-30", 14, FeeNone)}
			return in
		}(), FeeNone, ""},
		{"never with a fee on the invoice", func() Input {
			in := business("2026-07-20")
			in.Letters = []Letter{sentLetter(1, 1, LevelReminder, "2026-06-30", 14, FeeReminder)}
			return in
		}(), FeeNone, ""},
		{"never a fee after the compensation (R6)", func() Input {
			in := business("2026-07-20")
			in.Settings.BusinessCharge = ChargeFee
			in.Letters = []Letter{sentLetter(1, 1, LevelReminder, "2026-06-30", 14, FeeCompensation)}
			return in
		}(), FeeNone, ""},
		{"from the due date, before E + 14", func() Input {
			in := business("2026-06-16")
			in.Settings.FirstReminderDays = 1
			return in
		}(), FeeCompensation, "460"},
		{"never for a person", func() Input {
			in := business("2026-07-01")
			in.Invoice.BuyerType = "person"
			return in
		}(), FeeReminder, "38"},
		{"never for a person, whatever the setting", func() Input {
			in := business("2026-07-01")
			in.Invoice.BuyerType, in.Settings.PersonCharge = "person", ChargeCompensation
			return in
		}(), FeeNone, ""},
		{"a business without an organisation number is a person", func() Input {
			in := business("2026-07-01")
			in.Invoice.BuyerOrganisationNumber = ""
			return in
		}(), FeeReminder, "38"},
		{"a NULL buyer type is a person", func() Input {
			in := business("2026-07-01")
			in.Invoice.BuyerType = ""
			return in
		}(), FeeReminder, "38"},
	} {
		out := Next(c.in)
		if out.Letter == nil {
			t.Errorf("%s: %s %v, want a letter", c.name, out.Action, out.Reasons)
			continue
		}
		f := out.Letter
		if f.FeeKind != c.kind {
			t.Errorf("%s: fee kind %s, want %s", c.name, f.FeeKind, c.kind)
		}
		switch c.kind {
		case FeeCompensation:
			eqRat(t, c.name+": compensation", f.Compensation, c.amount)
			eqRat(t, c.name+": fee", f.Fee, "0")
		case FeeReminder:
			eqRat(t, c.name+": fee", f.Fee, c.amount)
			eqRat(t, c.name+": compensation", f.Compensation, "0")
		default:
			eqRat(t, c.name+": fee", f.Fee, "0")
			eqRat(t, c.name+": compensation", f.Compensation, "0")
		}
	}
	// The compensation's rate row is named on the letter.
	if out := Next(business("2026-07-01")); out.Letter == nil || !slices.Contains(out.Letter.RateIDs, 12) {
		t.Errorf("the letter's rate rows = %v, want the 2026-07-01 compensation row (12)", out.Letter)
	}
}

// D7's modes: no_charges sends letters without fee, compensation or
// interest; none sends nothing.
func TestReminderRules_NoCharges(t *testing.T) {
	t.Parallel()
	in := baseInput("2026-07-15")
	in.Settings.LateInterest = true
	in.Mode = ModeNoCharges
	out := Next(in)
	if out.Action != ActionReminder || out.Letter == nil {
		t.Fatalf("no_charges: %s %v, want a reminder", out.Action, out.Reasons)
	}
	if out.Letter.FeeKind != FeeNone {
		t.Errorf("no_charges: fee kind %s, want none", out.Letter.FeeKind)
	}
	eqRat(t, "no_charges: fee", out.Letter.Fee, "0")
	eqRat(t, "no_charges: compensation", out.Letter.Compensation, "0")
	eqRat(t, "no_charges: interest", out.Letter.Interest, "0")
	eqRat(t, "no_charges: total", out.Letter.Total, "10000")

	in.Mode = ModeNone
	expectBlocked(t, "none", Next(in), ReasonPolicyNone)

	in.Mode, in.Settings.Enabled = ModeNormal, false
	expectBlocked(t, "reminders disabled", Next(in), ReasonRemindersDisabled)
}

// The delivery fact (I4, NI3): without a delivery on or before the due date
// only fee-free reminders go, at most max(reminders_before_notice, 1), and
// the notice and the hand-off are blocked not_delivered.
func TestReminderRules_DeliveryFact(t *testing.T) {
	t.Parallel()
	undelivered := func(l string, n int) Input {
		in := baseInput(l)
		in.Deliveries = nil
		in.Settings.LateInterest = true
		in.Settings.RemindersBeforeNotice = n
		return in
	}
	// The first letter: fee-free, interest-free, noted.
	for _, n := range []int{0, 1, 2} {
		out := Next(undelivered("2026-07-15", n))
		if out.Action != ActionReminder || out.Letter == nil {
			t.Fatalf("n=%d: %s %v, want a reminder", n, out.Action, out.Reasons)
		}
		f := out.Letter
		if f.Level != LevelReminder || f.AnnouncesCollection || f.FeeKind != FeeNone || f.Interest.Sign() != 0 || f.Fee.Sign() != 0 {
			t.Errorf("n=%d: letter %+v, want a plain fee-free reminder", n, *f)
		}
		if !slices.Equal(out.ChargeNotes, []string{NoteNotDelivered}) {
			t.Errorf("n=%d: notes %v, want not_delivered", n, out.ChargeNotes)
		}
	}
	one := []Letter{sentLetter(1, 1, LevelReminder, "2026-07-15", 14, FeeNone)}
	two := append(slices.Clone(one), sentLetter(2, 2, LevelReminder, "2026-08-02", 14, FeeNone))
	// n = 2: a second fee-free reminder, then blocked.
	in := undelivered("2026-08-02", 2)
	in.Letters = one
	if out := Next(in); out.Action != ActionReminder || out.Letter == nil || out.Letter.FeeKind != FeeNone {
		t.Errorf("n=2, one sent: %s %v, want a fee-free reminder", out.Action, out.Reasons)
	}
	in = undelivered("2026-08-20", 2)
	in.Letters = two
	expectBlocked(t, "n=2, two sent", Next(in), ReasonNotDelivered)
	// n = 0 and n = 1: one, then the notice blocked; the hand-off too.
	for _, n := range []int{0, 1} {
		in := undelivered("2026-08-02", n)
		in.Letters = one
		expectBlocked(t, "the notice", Next(in), ReasonNotDelivered)
		in.Settings.CollectionNotice = false
		expectBlocked(t, "the hand-off", Next(in), ReasonNotDelivered)
	}
	// What counts as a delivery.
	for _, c := range []struct {
		name       string
		due        string
		deliveries []time.Time
		delivered  bool
	}{
		{"none", "2026-06-15", nil, false},
		{"after the due date", "2026-06-15", []time.Time{day("2026-06-16")}, false},
		{"a manual delivery on the due date", "2026-06-15", []time.Time{day("2026-06-15")}, true},
		{"an EHF transmission delivered", "2026-06-15", []time.Time{day("2026-06-02")}, true},
		{"an e-mail, and a later one", "2026-06-15", []time.Time{day("2026-06-20"), day("2026-06-01")}, true},
		{"after a Saturday due date, before its Monday", "2026-06-13", []time.Time{day("2026-06-14")}, false},
	} {
		in := baseInput("2026-07-15")
		in.Invoice.DueDate, in.Deliveries = day(c.due), c.deliveries
		if got := Delivered(in); got != c.delivered {
			t.Errorf("%s: Delivered = %v, want %v", c.name, got, c.delivered)
		}
		out := Next(in)
		if out.Letter == nil || (out.Letter.FeeKind == FeeReminder) != c.delivered {
			t.Errorf("%s: letter %+v, want a fee=%v", c.name, out.Letter, c.delivered)
		}
	}
}

// R16 and D11: a live hold blocks; a barring lift bars fees and the
// compensation and keeps interest; a hand-off ends the reminding.
func TestReminderRules_HoldsAndBarring(t *testing.T) {
	t.Parallel()
	in := baseInput("2026-07-15")
	in.OnHold = true
	expectBlocked(t, "a live hold", Next(in), ReasonOnHold)

	in = baseInput("2026-07-15")
	in.HandedOff, in.OnHold = true, true
	if out := Next(in); out.Action != ActionNone || !slices.Equal(out.Reasons, []string{ReasonHandedOff}) {
		t.Errorf("handed off: %s %v, want none handed_off", out.Action, out.Reasons)
	}

	in = baseInput("2026-07-15")
	in.OnHold, in.Payments = true, []Payment{{PaidOn: day("2026-07-01"), Amount: rat("10000")}}
	if out := Next(in); out.Action != ActionNone || len(out.Reasons) != 0 {
		t.Errorf("settled and held: %s %v, want none", out.Action, out.Reasons)
	}

	for _, buyer := range []string{"person", BuyerBusiness} {
		in := baseInput("2026-07-15")
		in.ChargesBarred, in.Settings.LateInterest = true, true
		in.Invoice.BuyerType, in.Invoice.BuyerOrganisationNumber = buyer, "923456783"
		in.Settings.BusinessCharge = ChargeCompensation
		out := Next(in)
		if out.Letter == nil {
			t.Fatalf("barred %s: %s %v, want a letter", buyer, out.Action, out.Reasons)
		}
		if out.Letter.FeeKind != FeeNone || out.Letter.Fee.Sign() != 0 || out.Letter.Compensation.Sign() != 0 {
			t.Errorf("barred %s: %+v, want no fee or compensation", buyer, *out.Letter)
		}
		eqRat(t, "barred "+buyer+": interest", out.Letter.Interest, "99.66")
		if !slices.Equal(out.ChargeNotes, []string{NoteChargesBarred}) {
			t.Errorf("barred %s: notes %v, want charges_barred", buyer, out.ChargeNotes)
		}
	}
}

// I10: a letter in flight blocks the next one, except the letter being
// dispatched or posted, which the caller excludes.
func TestReminderRules_LetterPending(t *testing.T) {
	t.Parallel()
	pending := Letter{ID: 7, Sequence: 1, Level: LevelReminder}
	for _, status := range []string{StatusQueued, StatusAwaitingPrint, StatusPrinted, StatusFailed} {
		in := baseInput("2026-07-15")
		in.Letters = []Letter{withStatus(pending, status)}
		expectBlocked(t, status, Next(in), ReasonLetterPending)

		in.Exclude = 7
		if out := Next(in); out.Action != ActionReminder || out.Letter == nil {
			t.Errorf("%s excluded: %s %v, want the reminder", status, out.Action, out.Reasons)
		}
	}
	in := baseInput("2026-07-15")
	in.Letters = []Letter{withStatus(pending, StatusWithdrawn)}
	if out := Next(in); out.Action != ActionReminder || out.Letter == nil {
		t.Errorf("withdrawn: %s %v, want the reminder", out.Action, out.Reasons)
	}
}

// D8's list in order: reminders, waiting until deadline + grace_days, the
// notice, the hand-off; the notice first when reminders_before_notice is 0
// under 1988; R20 under 2026: no fee, no notice, one reminder announcing the
// hand-off, then the hand-off.
func TestReminderRules_TheSequence(t *testing.T) {
	t.Parallel()
	// Before its day the reminder is named with its earliest date, no letter.
	out := Next(baseInput("2026-06-20"))
	if out.Action != ActionReminder || !out.EarliestOn.Equal(day("2026-06-29")) || out.Letter != nil {
		t.Errorf("before E + 14: %s %s %v, want reminder on 2026-06-29 without a letter", out.Action, out.EarliestOn, out.Letter)
	}
	// The first reminder, its facts.
	in := baseInput("2026-06-29")
	in.Payments = []Payment{{PaidOn: day("2026-06-20"), Amount: rat("2500")}}
	out = Next(in)
	if out.Action != ActionReminder || out.Letter == nil {
		t.Fatalf("on E + 14: %s %v, want a reminder", out.Action, out.Reasons)
	}
	f := out.Letter
	if f.Level != LevelReminder || f.Regime != Regime1988 || !f.Deadline.Equal(day("2026-07-13")) || f.FeeKind != FeeReminder {
		t.Errorf("the first reminder: %+v", *f)
	}
	eqRat(t, "principal open", f.PrincipalOpen, "7500")
	eqRat(t, "inkassosats", f.Inkassosats, "750")
	eqRat(t, "total", f.Total, "7538")

	one := []Letter{sentLetter(1, 1, LevelReminder, "2026-06-29", 14, FeeReminder)} // deadline 13 Jul
	in = baseInput("2026-07-16")
	in.Letters = one
	out = Next(in)
	if out.Action != ActionWaiting || !out.EarliestOn.Equal(day("2026-07-17")) || !slices.Equal(out.Reasons, []string{ReasonWaiting}) {
		t.Errorf("deadline + grace: %s %s %v, want waiting until 2026-07-17", out.Action, out.EarliestOn, out.Reasons)
	}
	in.L = day("2026-07-17")
	if out = Next(in); out.Action != ActionNotice || out.Letter == nil || out.Letter.Level != LevelNotice {
		t.Errorf("after the reminder: %s %v, want the notice", out.Action, out.Reasons)
	}

	two := append(slices.Clone(one), sentLetter(2, 2, LevelNotice, "2026-07-17", 14, FeeReminder)) // deadline 31 Jul
	in = baseInput("2026-08-03")
	in.Letters = two
	if out = Next(in); out.Action != ActionWaiting || !out.EarliestOn.Equal(day("2026-08-04")) {
		t.Errorf("after the notice, inside the grace: %s %s", out.Action, out.EarliestOn)
	}
	in.L = day("2026-08-04")
	if out = Next(in); out.Action != ActionHandOff || out.Letter != nil || !out.EarliestOn.Equal(day("2026-08-04")) {
		t.Errorf("after the notice: %s %s, want hand_off", out.Action, out.EarliestOn)
	}

	// reminders_before_notice = 0 under 1988: the notice first, with its fee.
	in = baseInput("2026-06-29")
	in.Settings.RemindersBeforeNotice = 0
	if out = Next(in); out.Action != ActionNotice || out.Letter == nil || out.Letter.FeeKind != FeeReminder {
		t.Errorf("n=0: %s %v, want the notice with a fee", out.Action, out.Reasons)
	}
	// … and with the notice off, a reminder, then the hand-off.
	in.Settings.CollectionNotice = false
	if out = Next(in); out.Action != ActionReminder {
		t.Errorf("n=0, notice off: %s, want a reminder", out.Action)
	}
	in.L, in.Letters = day("2026-07-17"), one
	if out = Next(in); out.Action != ActionHandOff {
		t.Errorf("n=0, notice off, one sent: %s, want hand_off", out.Action)
	}

	// R20: the 2026 regime from 1 Jan 2026.
	from := day("2026-01-01")
	r20 := func(l string, n int, letters ...Letter) Outcome {
		in := baseInput(l)
		in.Settings.Inkassolov2026From, in.Settings.RemindersBeforeNotice, in.Letters = &from, n, letters
		return Next(in)
	}
	out = r20("2026-06-29", 1)
	if out.Letter == nil || out.Letter.Regime != Regime2026 || out.Letter.FeeKind != FeeNone || out.Letter.AnnouncesCollection {
		t.Errorf("2026, first: %+v, want a plain fee-free reminder", out.Letter)
	}
	plain := sentLetter(1, 1, LevelReminder, "2026-06-29", 14, FeeNone)
	out = r20("2026-07-17", 1, plain)
	if out.Action != ActionReminder || out.Letter == nil || out.Letter.Level != LevelReminder || !out.Letter.AnnouncesCollection || out.Letter.FeeKind != FeeNone {
		t.Errorf("2026, second: %s %+v, want a fee-free reminder announcing the hand-off", out.Action, out.Letter)
	}
	announcing := sentLetter(2, 2, LevelReminder, "2026-07-17", 14, FeeNone)
	announcing.AnnouncesCollection = true
	if out = r20("2026-08-04", 1, plain, announcing); out.Action != ActionHandOff {
		t.Errorf("2026, after the announcement: %s, want hand_off", out.Action)
	}
	out = r20("2026-06-29", 0)
	if out.Action != ActionReminder || out.Letter == nil || !out.Letter.AnnouncesCollection {
		t.Errorf("2026, n=0: %s %+v, want a reminder announcing the hand-off", out.Action, out.Letter)
	}
	if out = r20("2026-08-04", 0, announcing); out.Action != ActionHandOff {
		t.Errorf("2026, n=0, announced: %s, want hand_off", out.Action)
	}

	// 2026, n = 0 with the notice off: the creditor's notice is no setting of
	// the 2026 regime, and its first letter announces the hand-off.
	in = baseInput("2026-06-29")
	in.Settings.Inkassolov2026From, in.Settings.RemindersBeforeNotice, in.Settings.CollectionNotice = &from, 0, false
	if out = Next(in); out.Action != ActionReminder || out.Letter == nil || !out.Letter.AnnouncesCollection {
		t.Errorf("2026, n=0, notice off: %s %+v, want a reminder announcing the hand-off", out.Action, out.Letter)
	}

	// The regime switch, judged on the letter's own day.
	for _, c := range []struct {
		from  string
		level Level
		ann   bool
	}{{"2026-07-17", LevelReminder, true}, {"2026-07-18", LevelNotice, false}} {
		in := baseInput("2026-07-17")
		in.Settings.Inkassolov2026From, in.Letters = dayp(c.from), one
		out := Next(in)
		if out.Letter == nil || out.Letter.Level != c.level || out.Letter.AnnouncesCollection != c.ann {
			t.Errorf("2026 from %s: %+v, want %s announces=%v", c.from, out.Letter, c.level, c.ann)
		}
	}

	// Settled: nothing.
	in = baseInput("2026-07-17")
	in.Letters = one
	in.Credits = []Credit{{IssueDate: day("2026-07-01"), Gross: rat("10000")}}
	if out = Next(in); out.Action != ActionNone {
		t.Errorf("credited in full: %s, want none", out.Action)
	}
}

// D6: past regime_reviewed_through under 1988 a fee or a notice is blocked
// collection_regime_unreviewed, a fee-free reminder still goes; a half-year a
// letter needs without its row is blocked collection_rates_outdated.
func TestReminderRules_ReviewAndRates(t *testing.T) {
	t.Parallel()
	lapsed := func(l string) Input {
		in := baseInput(l)
		in.Invoice.DueDate = day("2026-12-01")
		in.Settings.RegimeReviewedThrough = day("2026-12-31")
		return in
	}
	expectBlocked(t, "a fee past the review", Next(lapsed("2027-01-05")), ReasonRegimeUnreviewed)

	in := lapsed("2027-01-05")
	in.Settings.PersonCharge = ChargeNone
	if out := Next(in); out.Action != ActionReminder || out.Letter == nil || out.Letter.FeeKind != FeeNone {
		t.Errorf("a fee-free reminder past the review: %s %v, want it offered", out.Action, out.Reasons)
	}
	in.Letters = []Letter{sentLetter(1, 1, LevelReminder, "2027-01-05", 14, FeeNone)}
	in.L = day("2027-01-25")
	expectBlocked(t, "a notice past the review", Next(in), ReasonRegimeUnreviewed)

	in = lapsed("2026-12-31")
	if out := Next(in); out.Letter == nil || out.Letter.FeeKind != FeeReminder {
		t.Errorf("on the review's last day: %s %v, want the fee", out.Action, out.Reasons)
	}
	in = lapsed("2027-01-05")
	in.Settings.RegimeReviewedThrough = day("2027-06-30")
	if out := Next(in); out.Letter == nil || out.Letter.FeeKind != FeeReminder {
		t.Errorf("the review moved: %s %v, want the fee", out.Action, out.Reasons)
	}
	in = lapsed("2027-01-05")
	in.Settings.Inkassolov2026From = dayp("2027-01-01")
	if out := Next(in); out.Letter == nil || out.Letter.FeeKind != FeeNone {
		t.Errorf("the 2026 regime set: %s %v, want a fee-free reminder", out.Action, out.Reasons)
	}

	// Outdated: interest over 2027-H1, the compensation on a 2027 day.
	in = baseInput("2027-01-05")
	in.Invoice.DueDate, in.Settings.LateInterest = day("2026-12-01"), true
	out := Next(in)
	expectBlocked(t, "interest into 2027-H1", out, ReasonRatesOutdated)
	if out.Outdated == nil || *out.Outdated != (OutdatedRate{KindLateInterest, "2027-H1"}) {
		t.Errorf("outdated = %+v, want late_interest_percent 2027-H1", out.Outdated)
	}
	in.Rates = append(seedRates(), Rate{ID: 99, Kind: KindLateInterest, ValidFrom: day("2027-01-01"), Value: rat("12.00")})
	if out := Next(in); out.Letter == nil || out.Letter.Interest.Sign() <= 0 || !slices.Contains(out.Letter.RateIDs, 99) {
		t.Errorf("with the 2027-H1 row: %s %v, want a letter with interest", out.Action, out.Reasons)
	}

	in = baseInput("2027-01-05")
	in.Invoice.DueDate = day("2026-12-01")
	in.Invoice.BuyerType, in.Invoice.BuyerOrganisationNumber = BuyerBusiness, "923456783"
	in.Settings.BusinessCharge = ChargeCompensation
	out = Next(in)
	expectBlocked(t, "the compensation in 2027-H1", out, ReasonRatesOutdated)
	if out.Outdated == nil || *out.Outdated != (OutdatedRate{KindCompensation, "2027-H1"}) {
		t.Errorf("outdated = %+v, want b2b_compensation_nok 2027-H1", out.Outdated)
	}
	// Nothing needing a rate is never outdated.
	in.Settings.BusinessCharge = ChargeNone
	if out := Next(in); out.Letter == nil {
		t.Errorf("a fee-free letter in 2027: %s %v, want it", out.Action, out.Reasons)
	}
}

// The Input contract: every fixture keeps it, and Validate names a nil
// amount and a day that is not a UTC midnight. A Letter's nil amounts read
// as zero.
func TestReminderRules_InputContract(t *testing.T) {
	t.Parallel()
	full := halfKroneADay("2026-05-07")
	full.Letters = []Letter{withInterest(sentLetter(1, 1, LevelReminder, "2026-04-11", 14, FeeReminder), "20")}
	full.Credits = []Credit{{IssueDate: day("2026-04-01"), Gross: rat("100")}}
	full.Payments = []Payment{{PaidOn: day("2026-04-02"), OrderedOn: dayp("2026-04-01"), Amount: rat("100")}}
	full.Waivers = []Waiver{{ID: 1, ReminderID: 1, Kind: WaiverInterest, Amount: rat("8")}}
	full.ChargePayments = []ChargePayment{chargePayment(1, "2026-04-20", "12")}
	full.Settings.Inkassolov2026From = dayp("2027-01-01")
	for name, in := range map[string]Input{"baseInput": baseInput("2026-07-15"), "halfKroneADay": halfKroneADay("2026-05-07"), "every list": full} {
		if err := Validate(in); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	for name, breakIt := range map[string]func(*Input){
		"a nil gross":                 func(in *Input) { in.Invoice.Gross = nil },
		"L with a time of day":        func(in *Input) { in.L = in.L.Add(2 * time.Hour) },
		"L in Oslo":                   func(in *Input) { in.L = time.Date(2026, 5, 7, 0, 0, 0, 0, oslo) },
		"a nil payment amount":        func(in *Input) { in.Payments[0].Amount = nil },
		"a nil rate":                  func(in *Input) { in.Rates[0].Value = nil },
		"a nil charge payment amount": func(in *Input) { in.ChargePayments[0].Amount = nil },
		"a nil waiver amount":         func(in *Input) { in.Waivers[0].Amount = nil },
		"a delivery with a time":      func(in *Input) { in.Deliveries[0] = in.Deliveries[0].Add(time.Minute) },
	} {
		in := full
		in.Payments, in.Rates = slices.Clone(full.Payments), slices.Clone(full.Rates)
		in.ChargePayments, in.Waivers = slices.Clone(full.ChargePayments), slices.Clone(full.Waivers)
		in.Deliveries = slices.Clone(full.Deliveries)
		breakIt(&in)
		if Validate(in) == nil {
			t.Errorf("%s: Validate accepts it", name)
		}
	}
	// A letter without facts: its nil amounts are zero, nothing panics.
	bare := Letter{ID: 1, Sequence: 1, Level: LevelReminder, Status: StatusSent, SentOn: dayp("2026-04-11"), Deadline: dayp("2026-04-25")}
	in := halfKroneADay("2026-05-07")
	in.Letters = []Letter{bare}
	if out := Next(in); out.Letter == nil {
		t.Errorf("after a letter without facts: %s %v", out.Action, out.Reasons)
	}
	eqRat(t, "charges of a letter without facts", Charges([]Letter{bare}, nil, nil).Outstanding, "0")
}

// The facts are the caller's to keep and change: a letter's rate figures are
// copies, never the rate rows' own values.
func TestReminderRules_FactsDoNotAliasTheRates(t *testing.T) {
	t.Parallel()
	rates := seedRates()
	in := baseInput("2026-07-15")
	in.Settings.LateInterest, in.Rates = true, rates
	out := Next(in)
	if out.Letter == nil || out.Letter.Inkassosats == nil || len(out.Letter.Segments) == 0 {
		t.Fatalf("a fee letter with interest: %s %v", out.Action, out.Reasons)
	}
	out.Letter.Inkassosats.SetInt64(1)
	out.Letter.Segments[0].Rate.SetInt64(1)

	in = baseInput("2026-07-01")
	in.Invoice.BuyerType, in.Invoice.BuyerOrganisationNumber = BuyerBusiness, "923456783"
	in.Settings.BusinessCharge, in.Rates = ChargeCompensation, rates
	if out := Next(in); out.Letter == nil || out.Letter.FeeKind != FeeCompensation {
		t.Fatalf("a compensation letter: %s %v", out.Action, out.Reasons)
	} else {
		out.Letter.Compensation.SetInt64(1)
	}
	for _, c := range []struct {
		kind, on, want string
	}{{KindInkassosats, "2026-07-15", "750"}, {KindLateInterest, "2026-06-16", "12.00"}, {KindCompensation, "2026-07-01", "430"}} {
		if r, _ := RateOn(rates, c.kind, day(c.on)); r.Value.Cmp(rat(c.want)) != 0 {
			t.Errorf("the %s row in force on %s became %s", c.kind, c.on, r.Value.FloatString(2))
		}
	}
}
